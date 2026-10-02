import SwiftUI

// Everything on screen is monospaced text: no icons, buttons look like [this].
private let green = Color(red: 0.2, green: 1.0, blue: 0.4)
private let dim = Color(white: 0.55)
private let amber = Color(red: 1.0, green: 0.8, blue: 0.2)
private let mono = Font.system(.body, design: .monospaced)
private let small = Font.system(.caption, design: .monospaced)

private enum Tab: String, CaseIterable {
    case apps, store, log
}

/// A text button: `[ label ]`.
private struct Cmd: View {
    let label: String
    var color: Color = green
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            Text("[\(label)]").foregroundColor(color)
        }
        .buttonStyle(.plain)
    }
}

/// An app's real icon; until it loads (or if there is none) a code-style box
/// with the app's first letter.
private struct AppIcon: View {
    let url: URL?
    let name: String
    var size: CGFloat = 44

    var body: some View {
        AsyncImage(url: url) { phase in
            if case .success(let img) = phase {
                img.resizable().scaledToFill()
            } else {
                Text(String(name.prefix(1)).uppercased())
                    .font(.system(size: size * 0.45, weight: .bold, design: .monospaced))
                    .foregroundColor(green)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .overlay(RoundedRectangle(cornerRadius: size * 0.22).stroke(green.opacity(0.5)))
            }
        }
        .frame(width: size, height: size)
        .clipShape(RoundedRectangle(cornerRadius: size * 0.22))
    }
}

struct ContentView: View {
    @EnvironmentObject var sync: Sync
    @Environment(\.scenePhase) private var phase
    @State private var tab: Tab = .apps
    @State private var picking = false
    @State private var settings = false
    @State private var newSource = ""

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            VStack(alignment: .leading, spacing: 10) {
                header
                tabs
                rule
                ScrollView {
                    VStack(alignment: .leading, spacing: 8) {
                        switch tab {
                        case .apps: appsTab
                        case .store: storeTab
                        case .log: logTab
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .refreshable {
                    await sync.poll()
                    await sync.refreshStore()
                }
                rule
                bottomBar
            }
            .font(mono)
            .foregroundColor(green)
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
        }
        .fileImporter(isPresented: $picking, allowedContentTypes: [.data]) { result in
            if case .success(let url) = result {
                tab = .log
                Task { await sync.install(url) }
            }
        }
        .sheet(isPresented: $settings) { SettingsView().environmentObject(sync) }
        .onAppear {
            if !sync.configured { settings = true }
            Task { await sync.refreshStore() }
        }
        .onChange(of: phase) { p in
            p == .active ? sync.start() : sync.stop()
        }
        .onChange(of: settings) { open in
            if !open { sync.start() }
        }
    }

    private var rule: some View {
        Text(String(repeating: "-", count: 60)).lineLimit(1).foregroundColor(green.opacity(0.35))
    }

    // MARK: header

    private var header: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                Text("ipakill").font(.system(.title2, design: .monospaced).bold())
                Text("v\(appVersion)").font(small).foregroundColor(dim)
                Spacer()
                Text(sync.online ? "● SYNCED" : "● OFFLINE")
                    .font(small.bold())
                    .foregroundColor(sync.online ? green : .red)
                    .shadow(color: sync.online ? green : .red, radius: 4)
                Cmd(label: "cfg", color: dim) { settings = true }.font(small)
            }
            Text("> pc     \(sync.online ? sync.pcName : "-")  \(sync.host)").font(small).foregroundColor(dim)
            Text("> link   \(linkText)").font(small).foregroundColor(dim)
            if let exp = sync.selfExpires {
                let d = exp.timeIntervalSinceNow / 86_400
                Text("> self   \(daysText(d))").font(small).foregroundColor(color(d))
            }
        }
    }

    private var appVersion: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "?"
    }

    private var linkText: String {
        guard sync.online else { return "-" }
        switch sync.phoneLink {
        case "wifi": return "wi-fi"
        case "usb": return "usb"
        default: return "iphone not seen by pc"
        }
    }

    private var tabs: some View {
        HStack(spacing: 14) {
            ForEach(Tab.allCases, id: \.self) { t in
                Button { tab = t } label: {
                    Text(tab == t ? "[\(t.rawValue)]" : " \(t.rawValue) ")
                        .foregroundColor(tab == t ? .black : green)
                        .padding(.horizontal, 2)
                        .background(tab == t ? green : Color.clear)
                }
                .buttonStyle(.plain)
            }
            if updateCount > 0 {
                Text("\(updateCount) update\(updateCount == 1 ? "" : "s")").font(small).foregroundColor(amber)
            }
        }
    }

    private var updateCount: Int { sync.apps.filter { sync.update(for: $0) != nil }.count }

    // MARK: apps

    @ViewBuilder private var appsTab: some View {
        Text("$ ipakill list").foregroundColor(dim)
        if sync.apps.isEmpty {
            Text("  no apps yet. use [+ ipa] or the store.").foregroundColor(dim)
        }
        ForEach(sync.apps) { app in
            HStack(alignment: .top, spacing: 12) {
                AppIcon(url: sync.icon(for: app), name: app.name)
                VStack(alignment: .leading, spacing: 2) {
                    HStack {
                        Text(app.name).bold().lineLimit(1)
                        Text(app.version ?? "").font(small).foregroundColor(dim)
                        Spacer()
                        Text(daysText(app.daysLeft)).foregroundColor(color(app.daysLeft))
                    }
                    Text("expires \(expiry(app.expires))").font(small).foregroundColor(dim)
                    HStack(spacing: 12) {
                        if let up = sync.update(for: app) {
                            Cmd(label: "update -> \(up.version)", color: amber) { get(up) }
                        }
                        if app.daysLeft < 7, let entry = sync.storeEntry(for: app), sync.update(for: app) == nil {
                            Cmd(label: "re-sign", color: dim) { get(entry) }
                        }
                    }
                    .font(small)
                    .disabled(sync.busy || !sync.online)
                }
            }
            .padding(.vertical, 4)
        }
    }

    // MARK: store

    @ViewBuilder private var storeTab: some View {
        HStack {
            Text("$ ipakill store").foregroundColor(dim)
            Spacer()
            Cmd(label: sync.loadingStore ? "loading.." : "refresh", color: dim) {
                Task { await sync.refreshStore() }
            }
            .font(small)
        }
        if sync.store.isEmpty && !sync.loadingStore {
            Text("  nothing here. add a source below.").foregroundColor(dim)
        }
        ForEach(sync.store) { s in
            HStack(spacing: 12) {
                AppIcon(url: sync.icon(for: s), name: s.name)
                VStack(alignment: .leading, spacing: 1) {
                    Text(s.name).bold().lineLimit(1)
                    Text("\(s.version)  \(s.developer ?? SourceLoader.label(s.source))")
                        .font(small).foregroundColor(dim).lineLimit(1)
                    if let sub = s.subtitle {
                        Text(sub).font(small).foregroundColor(dim.opacity(0.8)).lineLimit(1)
                    }
                }
                Spacer()
                storeButton(s).font(small).disabled(sync.busy || !sync.online)
            }
            .padding(.vertical, 3)
        }

        Text(" ").font(small)
        Text("$ ipakill sources").foregroundColor(dim)
        ForEach(sync.sources, id: \.self) { src in
            VStack(alignment: .leading, spacing: 1) {
                HStack {
                    Text("> " + SourceLoader.label(src)).font(small).lineLimit(1)
                    Spacer()
                    Cmd(label: "rm", color: .red) { sync.removeSource(src) }.font(small)
                }
                if let err = sync.storeErrors[src] {
                    Text("  ! \(err)").font(small).foregroundColor(.red)
                }
            }
        }
        HStack {
            Text(">").foregroundColor(dim)
            TextField("github repo, sidestore/altstore/esign source url", text: $newSource)
                .font(small)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .keyboardType(.URL)
                .onSubmit(addSource)
            Cmd(label: "add") { addSource() }.font(small)
        }
    }

    @ViewBuilder private func storeButton(_ s: StoreApp) -> some View {
        if let have = sync.installed(s) {
            if let v = have.version, !v.isEmpty, Version.isNewer(s.version, than: v) {
                Cmd(label: "update", color: amber) { get(s) }
            } else {
                Cmd(label: "installed", color: dim) { get(s) }
            }
        } else {
            Cmd(label: "get") { get(s) }
        }
    }

    private func addSource() {
        sync.addSource(newSource)
        newSource = ""
    }

    private func get(_ s: StoreApp) {
        tab = .log
        Task { await sync.install(s) }
    }

    // MARK: log

    @ViewBuilder private var logTab: some View {
        ForEach(Array(sync.log.enumerated()), id: \.offset) { _, line in
            Text(line)
                .font(small)
                .foregroundColor(line.hasPrefix("!") ? .red : green.opacity(0.85))
        }
        if sync.busy {
            Text("working...").font(small).foregroundColor(dim)
        }
    }

    // MARK: bottom bar

    private var bottomBar: some View {
        HStack {
            Text(sync.busy ? "$ _ working" : "$ _").foregroundColor(dim).font(small)
            Spacer()
            Button { picking = true } label: {
                Text(sync.busy ? "[ ... ]" : "[ + ipa ]")
                    .bold()
                    .foregroundColor(.black)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 6)
                    .background(sync.online && !sync.busy ? green : dim)
            }
            .buttonStyle(.plain)
            .disabled(sync.busy)
        }
    }

    // MARK: helpers

    private func daysText(_ days: Double) -> String {
        if days <= 0 { return "EXPIRED" }
        if days < 1 { return "\(Int(days * 24))h left" }
        return String(format: "%.1fd left", days)
    }

    /// "fri 9 oct 21:04"
    private func expiry(_ date: Date) -> String {
        let f = DateFormatter()
        f.dateFormat = "EEE d MMM HH:mm"
        return f.string(from: date).lowercased()
    }

    private func color(_ days: Double) -> Color {
        days > 3 ? green : days > 1 ? amber : .red
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
                Text("on the pc run:  ipakill serve\nthen type what it prints.")
                    .foregroundColor(dim)
                field("pc address", "192.168.1.20", $sync.host, .decimalPad)
                field("pairing code", "123456", $sync.code, .numberPad)
                Button { dismiss() } label: {
                    Text("[ save ]").bold().frame(maxWidth: .infinity).padding(12)
                        .overlay(Rectangle().stroke(green))
                }
                .buttonStyle(.plain)
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
                .overlay(Rectangle().stroke(green.opacity(0.5)))
        }
    }
}
