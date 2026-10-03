import SwiftUI

// ipakill's own tabs. They are compiled into LiveContainer's UI framework and
// shown by its tab bar (see ios/patch_lc.py), next to LiveContainer's "Apps"
// tab where apps run inside ipakill. iOS 15 APIs only: that's the framework's target.
//
// Look: ESign style - grouped lists, rounded app icons, capsule GET buttons.

/// Starts and stops polling the PC with the app's lifecycle.
struct IpakillLifecycle: ViewModifier {
    @EnvironmentObject var sync: Sync
    @Environment(\.scenePhase) private var phase

    func body(content: Content) -> some View {
        content
            .onAppear {
                sync.start()
                Task { await sync.refreshStore() }
            }
            .onChange(of: phase) { p in
                p == .active ? sync.start() : sync.stop()
            }
    }
}

// MARK: - shared pieces

/// An app's real icon; until it loads (or if there is none) a tile with its first letter.
struct AppIcon: View {
    let url: URL?
    let name: String
    var size: CGFloat = 52

    var body: some View {
        AsyncImage(url: url) { phase in
            if case .success(let img) = phase {
                img.resizable().scaledToFill()
            } else {
                ZStack {
                    LinearGradient(colors: [Color(.systemGray4), Color(.systemGray6)],
                                   startPoint: .top, endPoint: .bottom)
                    Text(String(name.prefix(1)).uppercased())
                        .font(.system(size: size * 0.42, weight: .semibold, design: .rounded))
                        .foregroundColor(.secondary)
                }
            }
        }
        .frame(width: size, height: size)
        .clipShape(RoundedRectangle(cornerRadius: size * 0.225, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: size * 0.225, style: .continuous)
            .stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
    }
}

/// App Store style capsule button: GET / UPDATE / RENEW.
struct CapsuleButton: View {
    let title: String
    var filled = false
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(title)
                .font(.subheadline.weight(.bold))
                .foregroundColor(filled ? .white : .accentColor)
                .padding(.horizontal, 16)
                .padding(.vertical, 6)
                .frame(minWidth: 74)
                .background(Capsule().fill(filled ? Color.accentColor : Color(.tertiarySystemFill)))
        }
        .buttonStyle(.borderless)
    }
}

private func daysText(_ days: Double) -> String {
    if days <= 0 { return "Expired" }
    if days < 1 { return "\(Int(days * 24))h left" }
    let d = Int(days), h = Int((days - Double(d)) * 24)
    return h > 0 ? "\(d)d \(h)h left" : "\(d)d left"
}

private func daysColor(_ days: Double) -> Color {
    days > 3 ? .green : days > 1 ? .orange : .red
}

/// "Fri 9 Oct, 21:04"
private func expiryText(_ date: Date) -> String {
    let f = DateFormatter()
    f.dateFormat = "EEE d MMM, HH:mm"
    return f.string(from: date)
}

/// The two ways to get an app: run it inside ipakill (no install slot, no
/// app ID) or have the PC sign and install it as a normal app.
struct GetActions: ViewModifier {
    @EnvironmentObject var sync: Sync
    @EnvironmentObject var sharedModel: SharedModel
    @Binding var app: StoreApp?
    let onPCInstall: (StoreApp) -> Void

    func body(content: Content) -> some View {
        content.confirmationDialog(app?.name ?? "", isPresented: Binding(
            get: { app != nil }, set: { if !$0 { app = nil } }
        ), titleVisibility: .visible, presenting: app) { a in
            Button("Run inside ipakill") { runInside(a) }
            Button("Install with PC signing") {
                sharedModel.selectedTab = .ipakillActivity
                onPCInstall(a)
            }
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("Inside ipakill needs no install slot and no re-signing of its own.")
        }
    }

    private func runInside(_ a: StoreApp) {
        Task {
            // Apps inside ipakill are signed on the phone with ipakill's certificate.
            if !sync.hasCertificate {
                guard await sync.importCertificate() else { return }
            }
            var c = URLComponents(string: "livecontainer://install")!
            c.queryItems = [URLQueryItem(name: "url", value: a.url)]
            sharedModel.selectedTab = .apps
            sharedModel.deepLink = c.url
        }
    }
}

// MARK: - Sources

struct IpakillSourcesView: View {
    @EnvironmentObject var sync: Sync
    @State private var adding = false
    @State private var picked: StoreApp?

    var body: some View {
        NavigationView {
            List {
                Section {
                    NavigationLink {
                        StoreAppsView(title: "All Apps", apps: allApps, picked: $picked)
                    } label: {
                        HStack(spacing: 14) {
                            Image(systemName: "square.grid.3x3.fill")
                                .font(.title3)
                                .foregroundColor(.white)
                                .frame(width: 40, height: 40)
                                .background(RoundedRectangle(cornerRadius: 9, style: .continuous).fill(Color.accentColor))
                            VStack(alignment: .leading, spacing: 2) {
                                Text("All Apps").font(.body.weight(.semibold))
                                Text("\(sync.store.count) apps from \(sync.sources.count) sources")
                                    .font(.footnote).foregroundColor(.secondary)
                            }
                        }
                        .padding(.vertical, 2)
                    }
                }

                Section {
                    ForEach(sync.sources, id: \.self) { src in
                        NavigationLink {
                            StoreAppsView(title: sync.sourceName(src), apps: sync.apps(in: src), picked: $picked)
                        } label: {
                            SourceRow(source: src)
                        }
                        .swipeActions {
                            Button(role: .destructive) { sync.removeSource(src) } label: {
                                Label("Remove", systemImage: "trash")
                            }
                        }
                        .contextMenu {
                            Button { UIPasteboard.general.string = src } label: {
                                Label("Copy Source URL", systemImage: "doc.on.doc")
                            }
                            Button(role: .destructive) { sync.removeSource(src) } label: {
                                Label("Remove Source", systemImage: "trash")
                            }
                        }
                    }
                } header: {
                    Text("Sources")
                } footer: {
                    Text("GitHub repos (newest release .ipa) and AltStore, SideStore or ESign source links.")
                }
            }
            .listStyle(.insetGrouped)
            .navigationTitle("Sources")
            .refreshable { await sync.refreshStore() }
            .toolbar {
                ToolbarItem(placement: .navigationBarLeading) {
                    if sync.loadingStore { ProgressView() }
                }
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button { adding = true } label: { Image(systemName: "plus") }
                }
            }
            .sheet(isPresented: $adding) {
                AddSourceView().environmentObject(sync)
            }
        }
        .navigationViewStyle(.stack)
        .modifier(GetActions(app: $picked) { a in Task { await sync.install(a) } })
    }

    private var allApps: [StoreApp] {
        sync.store.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
    }
}

private struct AddSourceView: View {
    @EnvironmentObject var sync: Sync
    @Environment(\.dismiss) private var dismiss
    @State private var link = ""

    var body: some View {
        NavigationView {
            Form {
                Section {
                    TextField("https://…", text: $link)
                        .keyboardType(.URL)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                        .onSubmit(add)
                } footer: {
                    Text("Paste a GitHub repo or an AltStore, SideStore or ESign source link.")
                }
            }
            .navigationTitle("Add Source")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Add", action: add).disabled(link.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        }
    }

    private func add() {
        sync.addSource(link)
        dismiss()
    }
}

private struct SourceRow: View {
    @EnvironmentObject var sync: Sync
    let source: String

    var body: some View {
        HStack(spacing: 14) {
            AppIcon(url: sync.sourceInfo[source]?.icon.flatMap { URL(string: $0) },
                    name: sync.sourceName(source), size: 40)
            VStack(alignment: .leading, spacing: 2) {
                Text(sync.sourceName(source)).font(.body.weight(.semibold)).lineLimit(1)
                if let err = sync.storeErrors[source] {
                    Label(err, systemImage: "exclamationmark.triangle.fill")
                        .font(.footnote).foregroundColor(.red).lineLimit(1)
                } else {
                    Text(SourceLoader.label(source)).font(.footnote).foregroundColor(.secondary).lineLimit(1)
                }
            }
            Spacer()
            let n = sync.apps(in: source).count
            if n > 0 {
                Text("\(n)")
                    .font(.footnote.weight(.semibold))
                    .foregroundColor(.secondary)
                    .padding(.horizontal, 8).padding(.vertical, 2)
                    .background(Capsule().fill(Color(.tertiarySystemFill)))
            }
        }
        .padding(.vertical, 2)
    }
}

/// The apps of one source (or of all sources), searchable.
private struct StoreAppsView: View {
    @EnvironmentObject var sync: Sync
    let title: String
    let apps: [StoreApp]
    @Binding var picked: StoreApp?
    @State private var query = ""

    var body: some View {
        List {
            if shown.isEmpty {
                Text(sync.loadingStore ? "Loading…" : "No apps")
                    .foregroundColor(.secondary)
            }
            ForEach(shown) { s in
                StoreAppRow(app: s, picked: $picked)
            }
        }
        .listStyle(.plain)
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.large)
        .searchable(text: $query, prompt: "Search apps")
        .refreshable { await sync.refreshStore() }
    }

    private var shown: [StoreApp] {
        guard !query.isEmpty else { return apps }
        return apps.filter {
            $0.name.localizedCaseInsensitiveContains(query)
                || ($0.developer ?? "").localizedCaseInsensitiveContains(query)
        }
    }
}

private struct StoreAppRow: View {
    @EnvironmentObject var sync: Sync
    let app: StoreApp
    @Binding var picked: StoreApp?

    var body: some View {
        HStack(spacing: 14) {
            AppIcon(url: sync.icon(for: app), name: app.name)
            VStack(alignment: .leading, spacing: 2) {
                Text(app.name).font(.body.weight(.semibold)).lineLimit(1)
                Text([app.version, app.developer].compactMap { $0 }.joined(separator: " · "))
                    .font(.footnote).foregroundColor(.secondary).lineLimit(1)
                if let sub = app.subtitle {
                    Text(sub).font(.footnote).foregroundColor(.secondary).lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            button.disabled(sync.busy)
        }
        .padding(.vertical, 4)
        .contextMenu {
            Button { picked = app } label: { Label("Get…", systemImage: "arrow.down.app") }
            Button { UIPasteboard.general.string = app.url } label: {
                Label("Copy IPA Link", systemImage: "link")
            }
        }
    }

    @ViewBuilder private var button: some View {
        if let have = sync.installed(app) {
            if let v = have.version, !v.isEmpty, Version.isNewer(app.version, than: v) {
                CapsuleButton(title: "UPDATE", filled: true) { picked = app }
            } else {
                CapsuleButton(title: "SIGN") { picked = app }
            }
        } else {
            CapsuleButton(title: "GET") { picked = app }
        }
    }
}

// MARK: - Library (apps the PC signed and installed as normal apps)

struct IpakillLibraryView: View {
    @EnvironmentObject var sync: Sync
    @State private var picking = false
    @State private var pairing = false

    var body: some View {
        NavigationView {
            List {
                Section {
                    Button { pairing = true } label: { StatusCard() }
                        .buttonStyle(.plain)
                    CertificateRow()
                } footer: {
                    Text("Apps you run inside ipakill are in the Apps tab. They need ipakill's certificate, which comes from the PC.")
                }

                Section {
                    if sync.apps.isEmpty {
                        Text("No PC-signed apps yet. Get one from Sources or import an .ipa with +.")
                            .font(.footnote).foregroundColor(.secondary)
                    }
                    ForEach(sync.apps) { app in
                        LibraryRow(app: app)
                    }
                } header: {
                    Text("Installed by the PC")
                }
            }
            .listStyle(.insetGrouped)
            .navigationTitle("Library")
            .refreshable {
                await sync.poll()
                await sync.refreshStore()
            }
            .toolbar {
                ToolbarItem(placement: .navigationBarLeading) {
                    Button { pairing = true } label: { Image(systemName: "desktopcomputer") }
                }
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button { picking = true } label: { Image(systemName: "plus") }
                        .disabled(sync.busy)
                }
            }
            .fileImporter(isPresented: $picking, allowedContentTypes: [.data]) { result in
                if case .success(let url) = result { Task { await sync.install(url) } }
            }
            .sheet(isPresented: $pairing) {
                IpakillPairView().environmentObject(sync)
            }
            .onAppear { if !sync.configured { pairing = true } }
        }
        .navigationViewStyle(.stack)
    }
}

private struct StatusCard: View {
    @EnvironmentObject var sync: Sync

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: sync.online ? "desktopcomputer" : "wifi.slash")
                .font(.title2)
                .foregroundColor(.white)
                .frame(width: 44, height: 44)
                .background(RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .fill(sync.online ? Color.green : Color.gray))
            VStack(alignment: .leading, spacing: 2) {
                Text(sync.online ? (sync.pcName.isEmpty ? "PC connected" : sync.pcName) : "PC offline")
                    .font(.body.weight(.semibold))
                Text(sync.online ? linkText : "Run 'ipakill serve' on the PC")
                    .font(.footnote).foregroundColor(.secondary)
                if let exp = sync.selfExpires {
                    let d = exp.timeIntervalSinceNow / 86_400
                    Text("ipakill: \(daysText(d))").font(.footnote).foregroundColor(daysColor(d))
                }
            }
            Spacer()
            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundColor(.secondary)
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
    }

    private var linkText: String {
        switch sync.phoneLink {
        case "wifi": return "iPhone linked over Wi-Fi"
        case "usb": return "iPhone linked over USB"
        default: return "iPhone not seen by the PC"
        }
    }
}

private struct CertificateRow: View {
    @EnvironmentObject var sync: Sync

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: sync.hasCertificate ? "checkmark.seal.fill" : "seal")
                .font(.title2)
                .foregroundColor(sync.hasCertificate ? .green : .secondary)
                .frame(width: 44, height: 44)
            VStack(alignment: .leading, spacing: 2) {
                Text("Certificate").font(.body.weight(.semibold))
                Text(sync.hasCertificate ? "Imported - apps can run inside ipakill" : "Not imported yet")
                    .font(.footnote).foregroundColor(.secondary)
            }
            Spacer(minLength: 8)
            CapsuleButton(title: sync.hasCertificate ? "RENEW" : "IMPORT") {
                Task { await sync.importCertificate() }
            }
            .disabled(sync.busy || !sync.online)
        }
        .padding(.vertical, 4)
    }
}

private struct LibraryRow: View {
    @EnvironmentObject var sync: Sync
    let app: SignedApp

    var body: some View {
        HStack(spacing: 14) {
            AppIcon(url: sync.icon(for: app), name: app.name)
            VStack(alignment: .leading, spacing: 2) {
                Text(app.name).font(.body.weight(.semibold)).lineLimit(1)
                Text(app.version ?? app.bundle).font(.footnote).foregroundColor(.secondary).lineLimit(1)
                HStack(spacing: 4) {
                    Circle().fill(daysColor(app.daysLeft)).frame(width: 7, height: 7)
                    Text(daysText(app.daysLeft)).foregroundColor(daysColor(app.daysLeft))
                    Text("· \(expiryText(app.expires))").foregroundColor(.secondary)
                }
                .font(.caption)
            }
            Spacer(minLength: 8)
            Group {
                if let up = sync.update(for: app) {
                    CapsuleButton(title: "UPDATE", filled: true) { Task { await sync.install(up) } }
                } else if app.daysLeft < 7, let entry = sync.storeEntry(for: app) {
                    CapsuleButton(title: "RENEW") { Task { await sync.install(entry) } }
                }
            }
            .disabled(sync.busy || !sync.online)
        }
        .padding(.vertical, 4)
    }
}

struct IpakillPairView: View {
    @EnvironmentObject var sync: Sync
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationView {
            Form {
                Section {
                    HStack {
                        Text("Status")
                        Spacer()
                        Text(sync.online ? "Connected" : "Offline")
                            .foregroundColor(sync.online ? .green : .red)
                    }
                    TextField("PC address (192.168.1.20)", text: $sync.host)
                        .keyboardType(.decimalPad)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                    TextField("Pairing code", text: $sync.code)
                        .keyboardType(.numberPad)
                } footer: {
                    Text("On the PC run 'ipakill serve' and type the address and code it prints.")
                }
            }
            .navigationTitle("PC")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        sync.start()
                        dismiss()
                    }
                }
            }
        }
    }
}

// MARK: - Activity

struct IpakillActivityView: View {
    @EnvironmentObject var sync: Sync

    var body: some View {
        NavigationView {
            ScrollViewReader { proxy in
                List {
                    if sync.busy {
                        HStack(spacing: 12) {
                            ProgressView()
                            Text("Working on the PC…").foregroundColor(.secondary)
                        }
                    }
                    Section {
                        ForEach(Array(sync.log.enumerated()), id: \.offset) { i, line in
                            Text(line)
                                .font(.system(.footnote, design: .monospaced))
                                .foregroundColor(line.hasPrefix("!") ? .red : .primary)
                                .id(i)
                        }
                    } header: {
                        Text("Log")
                    }
                }
                .listStyle(.insetGrouped)
                .onChange(of: sync.log.count) { n in
                    withAnimation { proxy.scrollTo(n - 1, anchor: .bottom) }
                }
            }
            .navigationTitle("Activity")
        }
        .navigationViewStyle(.stack)
    }
}
