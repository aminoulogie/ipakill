import SwiftUI
import UniformTypeIdentifiers

private let green = Color(red: 0.2, green: 1.0, blue: 0.4)
private let dim = Color(white: 0.55)
private let mono = Font.system(.body, design: .monospaced)

struct ContentView: View {
    @EnvironmentObject var sync: Sync
    @Environment(\.scenePhase) private var phase
    @State private var picking = false
    @State private var settings = false

    var body: some View {
        ZStack(alignment: .bottomTrailing) {
            Color.black.ignoresSafeArea()

            VStack(alignment: .leading, spacing: 10) {
                header
                rule
                appList
                rule
                logView
            }
            .font(mono)
            .foregroundColor(green)
            .padding(.horizontal, 16)
            .padding(.top, 8)

            plusButton
        }
        .fileImporter(isPresented: $picking, allowedContentTypes: [.data]) { result in
            if case .success(let url) = result {
                Task { await sync.install(url) }
            }
        }
        .sheet(isPresented: $settings) { SettingsView().environmentObject(sync) }
        .onAppear {
            if !sync.configured { settings = true }
        }
        .onChange(of: phase) { p in
            p == .active ? sync.start() : sync.stop()
        }
        .onChange(of: settings) { open in
            if !open { sync.start() }
        }
    }

    private var rule: some View {
        Rectangle().fill(green.opacity(0.3)).frame(height: 1)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("ipakill").font(.system(.title2, design: .monospaced).bold())
                Spacer()
                Circle()
                    .fill(sync.online ? green : Color.red)
                    .frame(width: 12, height: 12)
                    .shadow(color: sync.online ? green : .red, radius: 6)
                Text(sync.online ? "SYNCED" : "OFFLINE")
                    .font(.system(.caption, design: .monospaced).bold())
                    .foregroundColor(sync.online ? green : .red)
                Button { settings = true } label: {
                    Image(systemName: "gearshape").foregroundColor(dim)
                }
                .padding(.leading, 6)
            }
            Text("> pc: \(sync.online ? sync.pcName : "-") \(sync.host.isEmpty ? "" : "[\(sync.host)]")")
                .font(.system(.caption, design: .monospaced)).foregroundColor(dim)
            Text("> iphone link: \(linkText)")
                .font(.system(.caption, design: .monospaced)).foregroundColor(dim)
            if let exp = sync.selfExpires {
                Text("> ipakill itself: \(daysText(exp.timeIntervalSinceNow / 86_400))")
                    .font(.system(.caption, design: .monospaced))
                    .foregroundColor(color(exp.timeIntervalSinceNow / 86_400))
            }
        }
    }

    private var linkText: String {
        guard sync.online else { return "-" }
        switch sync.phoneLink {
        case "wifi": return "wi-fi (installs without usb)"
        case "usb": return "usb"
        default: return "not seen - turn on Wi-Fi sync in iTunes"
        }
    }

    private var appList: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("$ ipakill list").foregroundColor(dim)
            if sync.apps.isEmpty {
                Text("  no apps yet - tap + to install").foregroundColor(dim)
            }
            ForEach(sync.apps) { app in
                HStack {
                    Text(app.name).lineLimit(1)
                    Spacer()
                    Text(daysText(app.daysLeft)).foregroundColor(color(app.daysLeft))
                }
                ProgressView(value: max(0, min(7, app.daysLeft)), total: 7)
                    .tint(color(app.daysLeft))
            }
        }
    }

    private var logView: some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(Array(sync.log.enumerated()), id: \.offset) { i, line in
                        Text(line)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundColor(line.hasPrefix("!") ? .red : green.opacity(0.85))
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .id(i)
                    }
                    if sync.busy {
                        Text("working…").font(.system(.caption, design: .monospaced)).foregroundColor(dim)
                    }
                    Color.clear.frame(height: 80) // room under the + button
                }
            }
            .onChange(of: sync.log.count) { n in
                withAnimation { proxy.scrollTo(n - 1, anchor: .bottom) }
            }
        }
    }

    private var plusButton: some View {
        Button { picking = true } label: {
            Image(systemName: sync.busy ? "hourglass" : "plus")
                .font(.system(size: 30, weight: .bold))
                .foregroundColor(.black)
                .frame(width: 66, height: 66)
                .background(Circle().fill(sync.online && !sync.busy ? green : dim))
                .shadow(color: green.opacity(sync.online ? 0.6 : 0), radius: 10)
        }
        .disabled(sync.busy)
        .padding(24)
    }

    private func daysText(_ days: Double) -> String {
        if days <= 0 { return "EXPIRED" }
        if days < 1 { return "\(Int(days * 24))h left" }
        return String(format: "%.1fd left", days)
    }

    private func color(_ days: Double) -> Color {
        days > 3 ? green : days > 1 ? .yellow : .red
    }
}

struct SettingsView: View {
    @EnvironmentObject var sync: Sync
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            VStack(alignment: .leading, spacing: 16) {
                Text("$ ipakill pair").font(.system(.title3, design: .monospaced).bold())
                Text("On the PC run:  ipakill serve\nthen type what it prints.")
                    .foregroundColor(dim)
                field("PC address", "192.168.1.20", $sync.host, .decimalPad)
                field("pairing code", "123456", $sync.code, .numberPad)
                Button {
                    dismiss()
                } label: {
                    Text("[ save ]").bold().frame(maxWidth: .infinity).padding(12)
                        .background(RoundedRectangle(cornerRadius: 6).stroke(green))
                }
                Spacer()
            }
            .font(mono)
            .foregroundColor(green)
            .padding(20)
        }
        .preferredColorScheme(.dark)
    }

    private func field(_ label: String, _ hint: String, _ value: Binding<String>, _ kb: UIKeyboardType) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("> \(label)").foregroundColor(dim)
            TextField(hint, text: value)
                .keyboardType(kb)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .padding(10)
                .background(RoundedRectangle(cornerRadius: 6).stroke(green.opacity(0.5)))
        }
    }
}
