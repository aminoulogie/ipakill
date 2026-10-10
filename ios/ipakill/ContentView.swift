import SwiftUI

// Native iOS look in the style of ESign: tab bar at the bottom, grouped lists,
// rounded app icons and capsule GET buttons.

private enum Tab: Hashable {
    case sources, library, activity, terminal, settings
}

struct ContentView: View {
    @EnvironmentObject var sync: Sync
    @Environment(\.scenePhase) private var phase
    @Environment(\.openURL) private var openURL
    @State private var tab: Tab = .sources
    @State private var pendingInstall: StoreApp?
    @State private var showingInstallMethod = false

    var body: some View {
        TabView(selection: $tab) {
            SourcesView(onInstall: install)
                .tabItem { Label("Sources", systemImage: "square.stack.3d.up.fill") }
                .tag(Tab.sources)
            LibraryView(onInstall: install, onImport: importFile)
                .tabItem { Label("Library", systemImage: "square.grid.2x2.fill") }
                .badge(sync.updateCount)
                .tag(Tab.library)
            ActivityView()
                .tabItem { Label("Activity", systemImage: "arrow.down.circle.fill") }
                .tag(Tab.activity)
            TerminalView()
                .tabItem { Label("Terminal", systemImage: "terminal.fill") }
                .tag(Tab.terminal)
            SettingsView()
                .tabItem { Label("Settings", systemImage: "gearshape.fill") }
                .tag(Tab.settings)
        }
        .onAppear {
            if !sync.configured { tab = .settings }
            Task { await sync.refreshStore() }
        }
        .onChange(of: phase) { p in
            p == .active ? sync.start() : sync.stop()
        }
        .confirmationDialog(
            "Choose Installation Method",
            isPresented: $showingInstallMethod,
            titleVisibility: .visible
        ) {
            Button("LiveContainer") {
                guard let app = pendingInstall else { return }
                installWithLiveContainer(app)
            }
            Button("Full Sideload to iPhone") {
                guard let app = pendingInstall else { return }
                installPhysical(app)
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("LiveContainer runs the app inside LiveContainer. Full Sideload signs it on the PC and installs it as a normal app on this iPhone.")
        }
    }

    private func install(_ s: StoreApp) {
        pendingInstall = s
        showingInstallMethod = true
    }

    private func installPhysical(_ s: StoreApp) {
        pendingInstall = nil
        tab = .activity
        Task { await sync.install(s) }
    }

    private func installWithLiveContainer(_ s: StoreApp) {
        pendingInstall = nil
        var components = URLComponents()
        components.scheme = "livecontainer"
        components.host = "install"
        components.queryItems = [URLQueryItem(name: "url", value: s.url)]

        guard let url = components.url else {
            sync.say("! could not build LiveContainer install link")
            tab = .activity
            return
        }
        openURL(url)
    }

    private func importFile(_ url: URL) {
        tab = .activity
        Task { await sync.install(url) }
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

/// App Store style capsule button: GET / UPDATE / OPEN.
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

// MARK: - Sources

struct SourcesView: View {
    @EnvironmentObject var sync: Sync
    let onInstall: (StoreApp) -> Void
    @State private var adding = false
    @State private var newSource = ""

    var body: some View {
        NavigationStack {
            List {
                Section {
                    NavigationLink {
                        StoreAppsView(title: "All Apps", apps: allApps, onInstall: onInstall)
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
                            StoreAppsView(title: sync.sourceName(src), apps: sync.apps(in: src), onInstall: onInstall)
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
            .alert("Add Source", isPresented: $adding) {
                TextField("https://…", text: $newSource)
                    .keyboardType(.URL)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                Button("Cancel", role: .cancel) { newSource = "" }
                Button("Add") {
                    sync.addSource(newSource)
                    newSource = ""
                }
            } message: {
                Text("Paste a GitHub repo or a source URL.")
            }
        }
    }

    private var allApps: [StoreApp] {
        sync.store.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
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
struct StoreAppsView: View {
    @EnvironmentObject var sync: Sync
    let title: String
    let apps: [StoreApp]
    let onInstall: (StoreApp) -> Void
    @State private var query = ""

    var body: some View {
        List {
            if shown.isEmpty {
                Text(sync.loadingStore ? "Loading…" : "No apps")
                    .foregroundColor(.secondary)
            }
            ForEach(shown) { s in
                StoreAppRow(app: s, onInstall: onInstall)
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
    let onInstall: (StoreApp) -> Void

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
            button.disabled(sync.busy || !sync.online)
        }
        .padding(.vertical, 4)
        .contextMenu {
            Button { onInstall(app) } label: { Label("Sign & Install", systemImage: "arrow.down.app") }
            Button { UIPasteboard.general.string = app.url } label: {
                Label("Copy IPA Link", systemImage: "link")
            }
        }
    }

    @ViewBuilder private var button: some View {
        if let have = sync.installed(app) {
            if let v = have.version, !v.isEmpty, Version.isNewer(app.version, than: v) {
                CapsuleButton(title: "UPDATE", filled: true) { onInstall(app) }
            } else {
                CapsuleButton(title: "SIGN") { onInstall(app) }
            }
        } else {
            CapsuleButton(title: "GET") { onInstall(app) }
        }
    }
}

// MARK: - Library

struct LibraryView: View {
    @EnvironmentObject var sync: Sync
    let onInstall: (StoreApp) -> Void
    let onImport: (URL) -> Void
    @State private var picking = false

    var body: some View {
        NavigationStack {
            List {
                Section { StatusCard() }

                Section {
                    NavigationLink {
                        IPALabView()
                    } label: {
                        Label {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("IPA Lab").font(.body.weight(.semibold))
                                Text("Inspect, extract, inject and modify Mach-O libraries")
                                    .font(.footnote).foregroundColor(.secondary)
                            }
                        } icon: {
                            Image(systemName: "shippingbox.and.arrow.backward")
                                .foregroundColor(.accentColor)
                        }
                    }
                } header: {
                    Text("Tools")
                }

                Section {
                    if sync.apps.isEmpty {
                        Text("No signed apps yet. Get one from Sources or import an .ipa with +.")
                            .font(.footnote).foregroundColor(.secondary)
                    }
                    ForEach(sync.apps) { app in
                        LibraryRow(app: app, onInstall: onInstall)
                    }
                } header: {
                    Text("Signed Apps")
                }
            }
            .listStyle(.insetGrouped)
            .navigationTitle("Library")
            .refreshable {
                await sync.poll()
                await sync.refreshStore()
            }
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button { picking = true } label: { Image(systemName: "plus") }
                        .disabled(sync.busy)
                }
            }
            .fileImporter(isPresented: $picking, allowedContentTypes: [.data]) { result in
                if case .success(let url) = result { onImport(url) }
            }
        }
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
        }
        .padding(.vertical, 4)
    }

    private var linkText: String {
        switch sync.phoneLink {
        case "wifi": return "iPhone linked over Wi-Fi"
        case "usb": return "iPhone linked over USB"
        default: return "iPhone not seen by the PC"
        }
    }
}

private struct LibraryRow: View {
    @EnvironmentObject var sync: Sync
    let app: SignedApp
    let onInstall: (StoreApp) -> Void

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
                    CapsuleButton(title: "UPDATE", filled: true) { onInstall(up) }
                } else if app.daysLeft < 7, let entry = sync.storeEntry(for: app) {
                    CapsuleButton(title: "RENEW") { onInstall(entry) }
                }
            }
            .disabled(sync.busy || !sync.online)
        }
        .padding(.vertical, 4)
    }
}

// MARK: - Activity

struct ActivityView: View {
    @EnvironmentObject var sync: Sync

    var body: some View {
        NavigationStack {
            ScrollViewReader { proxy in
                List {
                    if sync.busy {
                        VStack(alignment: .leading, spacing: 10) {
                            HStack {
                                Text(sync.installPhase)
                                    .font(.headline)
                                Spacer()
                                Text("\(sync.installPercent)%")
                                    .font(.subheadline.monospacedDigit())
                                    .foregroundColor(.secondary)
                            }

                            ProgressView(value: Double(sync.installPercent), total: 100)
                                .progressViewStyle(.linear)

                            Text(sync.installDetail)
                                .font(.footnote)
                                .foregroundColor(.secondary)
                        }
                        .padding(.vertical, 6)
                    }
                    Section {
                        ForEach(Array(sync.log.enumerated()), id: \.offset) { i, line in
                            Text(line)
                                .font(.system(.footnote, design: .monospaced))
                                .foregroundColor(line.hasPrefix("!") ? .red : .primary)
                                .id(i)
                        }
                    } header: {
                        Text(sync.busy ? "Technical details" : "Log")
                    } footer: {
                        if sync.busy {
                            Text("Detailed signer output is kept here for troubleshooting; the progress card above shows the useful signing status.")
                        }
                    }
                }
                .listStyle(.insetGrouped)
                .onChange(of: sync.log.count) { n in
                    withAnimation { proxy.scrollTo(n - 1, anchor: .bottom) }
                }
            }
            .navigationTitle("Activity")
        }
    }
}

// MARK: - Terminal

struct TerminalView: View {
    @EnvironmentObject var sync: Sync
    @State private var command = ""
    @FocusState private var commandFocused: Bool

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                HStack(spacing: 8) {
                    Circle()
                        .fill(sync.online ? Color.green : Color.red)
                        .frame(width: 8, height: 8)
                    Text(sync.online ? (sync.pcName.isEmpty ? "PC connected over LAN" : sync.pcName) : "PC offline")
                        .font(.caption)
                        .foregroundColor(.secondary)
                    Spacer()
                    if sync.terminalBusy { ProgressView().controlSize(.small) }
                }
                .padding(.horizontal)
                .padding(.vertical, 8)

                Divider()

                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 3) {
                            ForEach(Array(sync.terminalLines.enumerated()), id: \.offset) { i, line in
                                Text(line.isEmpty ? " " : line)
                                    .font(.system(.footnote, design: .monospaced))
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .textSelection(.enabled)
                                    .id(i)
                            }
                        }
                        .padding(12)
                    }
                    .background(Color(.systemBackground))
                    .onChange(of: sync.terminalLines.count) { count in
                        guard count > 0 else { return }
                        withAnimation(.easeOut(duration: 0.15)) {
                            proxy.scrollTo(count - 1, anchor: .bottom)
                        }
                    }
                }

                Divider()

                if !sync.terminalCWD.isEmpty {
                    Text(sync.terminalCWD)
                        .font(.caption2.monospaced())
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 12)
                        .padding(.top, 7)
                }

                HStack(spacing: 8) {
                    TextField("PowerShell command", text: $command)
                        .font(.system(.body, design: .monospaced))
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.send)
                        .focused($commandFocused)
                        .onSubmit(sendCommand)

                    Button(action: sendCommand) {
                        Image(systemName: "arrow.up.circle.fill")
                            .font(.title2)
                    }
                    .disabled(command.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                              || sync.terminalBusy || !sync.online)
                }
                .padding(12)
            }
            .navigationTitle("Terminal")
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("Clear") { sync.clearTerminal() }
                }
            }
            .onAppear { commandFocused = true }
        }
    }

    private func sendCommand() {
        let line = command.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !line.isEmpty, !sync.terminalBusy, sync.online else { return }
        command = ""
        Task { await sync.runTerminal(line) }
    }
}

// MARK: - Settings

struct SettingsView: View {
    @EnvironmentObject var sync: Sync

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    LabeledContent("Status") {
                        Text(sync.online ? "Connected" : "Offline")
                            .foregroundColor(sync.online ? .green : .red)
                    }
                    TextField("PC address (192.168.1.20)", text: $sync.host)
                        .keyboardType(.decimalPad)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                    TextField("Pairing code", text: $sync.code)
                        .keyboardType(.numberPad)
                    Button("Connect") { sync.start() }
                } header: {
                    Text("PC")
                } footer: {
                    Text("On the PC run 'ipakill serve' and type the address and code it prints.")
                }

                Section("About") {
                    LabeledContent("Version", value: appVersion)
                    if let exp = sync.selfExpires {
                        LabeledContent("Certificate expires", value: expiryText(exp))
                    }
                }
            }
            .navigationTitle("Settings")
        }
    }

    private var appVersion: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "?"
    }
}
