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

/// Downloaded images, shrunk to the size they're shown at and kept in
/// memory. AsyncImage re-downloads every time a list row scrolls back in.
@MainActor final class ImageCache {
    static let shared = ImageCache()
    private let cache = NSCache<NSString, UIImage>()
    private var loading: [NSString: Task<UIImage?, Never>] = [:]

    private func key(_ url: URL, _ maxPixel: CGFloat) -> NSString {
        "\(Int(maxPixel))|\(url.absoluteString)" as NSString
    }

    func cached(_ url: URL, maxPixel: CGFloat) -> UIImage? {
        cache.object(forKey: key(url, maxPixel))
    }

    func load(_ url: URL, maxPixel: CGFloat) async -> UIImage? {
        let k = key(url, maxPixel)
        if let img = cache.object(forKey: k) { return img }
        if let running = loading[k] { return await running.value }
        let task = Task<UIImage?, Never> {
            guard let (data, _) = try? await URLSession.shared.data(from: url),
                  let img = UIImage(data: data) else { return nil }
            let scale = min(1, maxPixel / max(img.size.width * img.scale, img.size.height * img.scale))
            let target = CGSize(width: img.size.width * img.scale * scale, height: img.size.height * img.scale * scale)
            return await img.byPreparingThumbnail(ofSize: target) ?? img
        }
        loading[k] = task
        let img = await task.value
        loading[k] = nil
        if let img { cache.setObject(img, forKey: k) }
        return img
    }
}

struct CachedImage<Placeholder: View>: View {
    let url: URL?
    var maxPixel: CGFloat = 200
    var mode: ContentMode = .fill
    @ViewBuilder var placeholder: () -> Placeholder
    @State private var loaded: UIImage?

    var body: some View {
        Group {
            if let img = loaded ?? url.flatMap({ ImageCache.shared.cached($0, maxPixel: maxPixel) }) {
                Image(uiImage: img).resizable().aspectRatio(contentMode: mode)
            } else {
                placeholder()
            }
        }
        .task(id: url) {
            guard let url else { return }
            loaded = await ImageCache.shared.load(url, maxPixel: maxPixel)
        }
    }
}

/// An app's real icon; until it loads (or if there is none) a tile with its first letter.
struct AppIcon: View {
    let url: URL?
    let name: String
    var size: CGFloat = 52

    var body: some View {
        CachedImage(url: url, maxPixel: size * 3) {
            ZStack {
                LinearGradient(colors: [Color(.systemGray4), Color(.systemGray6)],
                               startPoint: .top, endPoint: .bottom)
                Text(String(name.prefix(1)).uppercased())
                    .font(.system(size: size * 0.42, weight: .semibold, design: .rounded))
                    .foregroundColor(.secondary)
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
                        StoreAppsView(title: "All Apps", apps: sync.storeSorted, picked: $picked)
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
    @State private var results: [StoreApp]?   // nil: not searching
    @State private var keys: [String] = []

    var body: some View {
        let shown = results ?? apps
        List {
            if shown.isEmpty {
                Text(sync.loadingStore ? "Loading…" : results == nil ? "No apps" : "No results")
                    .foregroundColor(.secondary)
            }
            ForEach(shown) { s in
                NavigationLink {
                    AppDetailView(app: s, picked: $picked)
                } label: {
                    StoreAppRow(app: s, picked: $picked)
                }
            }
        }
        .listStyle(.plain)
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.large)
        .searchable(text: $query, prompt: "Search apps")
        .disableAutocorrection(true)
        .refreshable { await sync.refreshStore() }
        // Search after typing pauses, over text lowercased once per list.
        .task(id: query) {
            let q = query.trimmingCharacters(in: .whitespaces).lowercased()
            guard !q.isEmpty else {
                results = nil
                return
            }
            try? await Task.sleep(nanoseconds: 180_000_000)
            guard !Task.isCancelled else { return }
            if keys.count != apps.count { keys = apps.map(\.searchKey) }
            results = zip(apps, keys).filter { $0.1.contains(q) }.map(\.0)
        }
        .onChange(of: apps.count) { _ in keys = [] }
    }
}

/// An app's page: screenshots, description, what's new, details.
private struct AppDetailView: View {
    @EnvironmentObject var sync: Sync
    let app: StoreApp
    @Binding var picked: StoreApp?
    @State private var fullDescription = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                HStack(alignment: .top, spacing: 16) {
                    AppIcon(url: sync.icon(for: app), name: app.name, size: 112)
                    VStack(alignment: .leading, spacing: 4) {
                        Text(app.name).font(.title2.weight(.bold)).lineLimit(3)
                        if let d = app.developer {
                            Text(d).font(.subheadline).foregroundColor(.secondary).lineLimit(1)
                        }
                        Spacer(minLength: 10)
                        CapsuleButton(title: buttonTitle, filled: true) { picked = app }
                            .disabled(sync.busy)
                    }
                }

                HStack(spacing: 0) {
                    fact("VERSION", app.version)
                    Divider()
                    fact("SIZE", sizeText ?? "–")
                    Divider()
                    fact("UPDATED", app.date ?? "–")
                }
                .frame(height: 46)

                if !app.screenshots.isEmpty {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 12) {
                            ForEach(app.screenshots, id: \.self) { link in
                                CachedImage(url: URL(string: link), maxPixel: 1000, mode: .fit) {
                                    RoundedRectangle(cornerRadius: 22, style: .continuous)
                                        .fill(Color(.secondarySystemBackground))
                                        .frame(width: 214)
                                        .overlay(ProgressView())
                                }
                                .frame(height: 460)
                                .clipShape(RoundedRectangle(cornerRadius: 22, style: .continuous))
                                .overlay(RoundedRectangle(cornerRadius: 22, style: .continuous)
                                    .stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
                            }
                        }
                        .padding(.horizontal, 20)
                    }
                    .padding(.horizontal, -20)
                }

                if let notes = app.notes?.trimmingCharacters(in: .whitespacesAndNewlines), !notes.isEmpty {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("What's New").font(.title3.weight(.bold))
                        Text("Version \(app.version)").font(.footnote).foregroundColor(.secondary)
                        Text(notes).font(.callout)
                    }
                }

                if let about = app.about?.trimmingCharacters(in: .whitespacesAndNewlines), !about.isEmpty {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Description").font(.title3.weight(.bold))
                        Text(about).font(.callout).lineLimit(fullDescription ? nil : 6)
                        if !fullDescription && about.count > 280 {
                            Button("more") { withAnimation { fullDescription = true } }.font(.callout)
                        }
                    }
                }

                VStack(alignment: .leading, spacing: 0) {
                    Text("Information").font(.title3.weight(.bold)).padding(.bottom, 6)
                    info("Source", sync.sourceName(app.source))
                    if let d = app.developer { info("Developer", d) }
                    if let b = app.bundleID { info("Bundle ID", b) }
                    info("Version", app.version)
                    if let s = sizeText { info("Size", s) }
                    Button { UIPasteboard.general.string = app.url } label: {
                        Label("Copy IPA Link", systemImage: "link").font(.callout)
                    }
                    .padding(.top, 10)
                }
            }
            .padding(20)
        }
        .navigationTitle("")
        .navigationBarTitleDisplayMode(.inline)
    }

    private var buttonTitle: String {
        guard let have = sync.installed(app) else { return "GET" }
        if let v = have.version, !v.isEmpty, Version.isNewer(app.version, than: v) { return "UPDATE" }
        return "SIGN"
    }

    private var sizeText: String? {
        app.size.map { ByteCountFormatter.string(fromByteCount: $0, countStyle: .file) }
    }

    private func fact(_ title: String, _ value: String) -> some View {
        VStack(spacing: 4) {
            Text(title).font(.caption2.weight(.semibold)).foregroundColor(.secondary)
            Text(value).font(.subheadline.weight(.semibold)).lineLimit(1).minimumScaleFactor(0.7)
        }
        .frame(maxWidth: .infinity)
    }

    private func info(_ title: String, _ value: String) -> some View {
        VStack(spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                Text(title).foregroundColor(.secondary)
                Spacer(minLength: 16)
                Text(value).multilineTextAlignment(.trailing).lineLimit(2)
            }
            .font(.callout)
            .padding(.vertical, 9)
            Divider()
        }
    }
}

/// The install the PC is working on: stage, bytes, and a bar.
struct InstallProgressCard: View {
    @EnvironmentObject var sync: Sync

    var body: some View {
        if let p = sync.progress {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text(p.name.isEmpty ? "Installing" : p.name).font(.body.weight(.semibold)).lineLimit(1)
                    Spacer()
                    Text("\(Int(p.fraction * 100))%").font(.footnote.monospacedDigit()).foregroundColor(.secondary)
                }
                ProgressView(value: p.fraction)
                    .tint(p.stage == "failed" ? .red : p.stage == "done" ? .green : .accentColor)
                Text(p.label).font(.footnote.monospacedDigit()).foregroundColor(.secondary)
            }
            .padding(.vertical, 4)
            .animation(.easeInOut(duration: 0.3), value: p)
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
                if sync.progress != nil {
                    Section { InstallProgressCard() }
                }

                Section {
                    Button { pairing = true } label: { StatusCard() }
                        .buttonStyle(.plain)
                    CertificateRow()
                    NavigationLink { AppIDsView() } label: { AppIDsRow() }
                    NavigationLink { HomeScreenView() } label: {
                        HStack(spacing: 14) {
                            Image(systemName: "apps.iphone")
                                .font(.title2)
                                .foregroundColor(.accentColor)
                                .frame(width: 44, height: 44)
                            VStack(alignment: .leading, spacing: 2) {
                                Text("Home Screen Icons").font(.body.weight(.semibold))
                                Text("Open an app inside ipakill in one tap").font(.footnote).foregroundColor(.secondary)
                            }
                        }
                        .padding(.vertical, 4)
                    }
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

private struct AppIDsRow: View {
    @EnvironmentObject var sync: Sync

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: "person.badge.key.fill")
                .font(.title2)
                .foregroundColor(.accentColor)
                .frame(width: 44, height: 44)
            VStack(alignment: .leading, spacing: 2) {
                Text("App IDs").font(.body.weight(.semibold))
                Group {
                    if let ids = sync.appIDs {
                        Text("\(ids.count) of \(sync.appIDLimit) in use · \(max(0, sync.appIDLimit - ids.count)) left")
                            .foregroundColor(ids.count >= sync.appIDLimit ? .red : .secondary)
                    } else if sync.loadingAppIDs {
                        Text("Asking Apple…").foregroundColor(.secondary)
                    } else {
                        Text(sync.appIDsError ?? "Tap to check").foregroundColor(.secondary)
                    }
                }
                .font(.footnote)
                .lineLimit(2)
            }
        }
        .padding(.vertical, 4)
        .task { if sync.appIDs == nil && sync.online { await sync.loadAppIDs() } }
    }
}

private struct AppIDsView: View {
    @EnvironmentObject var sync: Sync

    var body: some View {
        List {
            Section {
                if let ids = sync.appIDs {
                    if ids.isEmpty { Text("None in use").foregroundColor(.secondary) }
                    ForEach(ids) { a in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(a.name.isEmpty ? a.identifier : a.name).font(.body.weight(.semibold))
                            Text(a.identifier).font(.footnote.monospaced()).foregroundColor(.secondary)
                        }
                        .padding(.vertical, 2)
                    }
                } else if sync.loadingAppIDs {
                    HStack(spacing: 12) { ProgressView(); Text("Asking Apple…").foregroundColor(.secondary) }
                } else if let err = sync.appIDsError {
                    Text(err).foregroundColor(.red)
                }
            } header: {
                if let ids = sync.appIDs { Text("\(ids.count) of \(sync.appIDLimit) in use") }
            } footer: {
                Text("A free Apple ID can create \(sync.appIDLimit) app IDs per 7 days; each frees up 7 days after it was made. Re-signing the same app reuses its ID. Apps you run inside ipakill use none.")
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("App IDs")
        .refreshable { await sync.loadAppIDs(fresh: true) }
        .task { await sync.loadAppIDs() }
    }
}

/// An app installed inside ipakill (LiveContainer's Documents/Applications).
private struct ContainerApp: Identifiable {
    let folder: String
    let name: String
    var id: String { folder }
    var launchLink: String {
        var c = URLComponents(string: "livecontainer://livecontainer-launch")!
        c.queryItems = [URLQueryItem(name: "bundle-name", value: folder)]
        return c.url!.absoluteString
    }

    static func all() -> [ContainerApp] {
        let dir = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Applications")
        let folders = (try? FileManager.default.contentsOfDirectory(atPath: dir.path)) ?? []
        return folders.filter { $0.hasSuffix(".app") }.map { folder in
            let info = NSDictionary(contentsOf: dir.appendingPathComponent(folder).appendingPathComponent("Info.plist"))
            let name = info?["CFBundleDisplayName"] as? String ?? info?["CFBundleName"] as? String
                ?? (folder as NSString).deletingPathExtension
            return ContainerApp(folder: folder, name: name)
        }
        .sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
    }
}

/// How to make a one-tap Home Screen icon for an app inside ipakill: an
/// Apple Shortcut saves the app's name to ipakill-launch.txt, then opens the
/// launch link. ipakill reads the note before it starts (see patch_lc.py),
/// so it opens straight into the app - no extension, no app ID.
private struct HomeScreenView: View {
    @State private var apps: [ContainerApp] = []
    @State private var copied: String?

    var body: some View {
        List {
            Section {
                step(1, "Open the **Shortcuts** app, tap **+**.")
                step(2, "Add **Text** and paste the app's **name** (copy it below).")
                step(3, "Add **Save File**. Tap the folder, pick **On My iPhone › ipakill**. Turn **Ask Where to Save** off, set **Subpath** to `ipakill-launch.txt`, turn **Overwrite If File Exists** on.")
                step(4, "Add **Open URLs** and paste the app's **link** (copy it below).")
                step(5, "Tap the shortcut's name at the top › **Add to Home Screen**, choose its icon and name.")
            } header: {
                Text("Make an icon (once per app)")
            } footer: {
                Text("Tapping the icon opens the app directly. No extension and no app ID needed. The first time, Shortcuts asks to allow access to ipakill's folder: allow it.")
            }

            Section {
                if apps.isEmpty {
                    Text("No apps inside ipakill yet. Use Get › Run inside ipakill.").foregroundColor(.secondary)
                }
                ForEach(apps) { app in
                    VStack(alignment: .leading, spacing: 8) {
                        Text(app.name).font(.body.weight(.semibold))
                        HStack(spacing: 10) {
                            copyButton("Copy name", app.name, id: app.folder + "name")
                            copyButton("Copy link", app.launchLink, id: app.folder + "link")
                        }
                    }
                    .padding(.vertical, 4)
                }
            } header: {
                Text("Apps inside ipakill")
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Home Screen Icons")
        .onAppear { apps = ContainerApp.all() }
    }

    private func step(_ n: Int, _ text: LocalizedStringKey) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text("\(n)").font(.footnote.weight(.bold)).foregroundColor(.white)
                .frame(width: 22, height: 22)
                .background(Circle().fill(Color.accentColor))
            Text(text).font(.callout)
        }
        .padding(.vertical, 2)
    }

    private func copyButton(_ title: String, _ value: String, id: String) -> some View {
        Button {
            UIPasteboard.general.string = value
            copied = id
        } label: {
            Label(copied == id ? "Copied" : title, systemImage: copied == id ? "checkmark" : "doc.on.doc")
                .font(.footnote.weight(.semibold))
                .padding(.horizontal, 12).padding(.vertical, 6)
                .background(Capsule().fill(Color(.tertiarySystemFill)))
        }
        .buttonStyle(.borderless)
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

// MARK: - Activity (log + terminal)

struct IpakillActivityView: View {
    @EnvironmentObject var sync: Sync
    @State private var mode = 0

    var body: some View {
        NavigationView {
            VStack(spacing: 0) {
                Picker("", selection: $mode) {
                    Text("Log").tag(0)
                    Text("Terminal").tag(1)
                }
                .pickerStyle(.segmented)
                .padding(.horizontal)
                .padding(.vertical, 8)
                if mode == 0 { LogView() } else { TerminalView() }
            }
            .navigationTitle(mode == 0 ? "Activity" : "Terminal")
            .navigationBarTitleDisplayMode(.inline)
        }
        .navigationViewStyle(.stack)
    }
}

private struct LogView: View {
    @EnvironmentObject var sync: Sync

    var body: some View {
        ScrollViewReader { proxy in
            List {
                if sync.progress != nil {
                    Section { InstallProgressCard() }
                } else if sync.busy {
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
    }
}

/// Commands typed here run on the PC through 'ipakill serve --shell'.
private struct TerminalView: View {
    @EnvironmentObject var sync: Sync
    @State private var input = ""
    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 4) {
                        ForEach(Array(sync.term.enumerated()), id: \.offset) { i, line in
                            Text(line)
                                .font(.system(.footnote, design: .monospaced))
                                .foregroundColor(color(line))
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .textSelection(.enabled)
                                .id(i)
                        }
                        if sync.termBusy {
                            ProgressView().padding(.top, 4).id(-1)
                        }
                    }
                    .padding(12)
                }
                .background(Color.black)
                .onChange(of: sync.term.count) { n in
                    withAnimation { proxy.scrollTo(n - 1, anchor: .bottom) }
                }
                .onTapGesture { focused = true }
            }
            HStack(spacing: 8) {
                Text(">").font(.system(.body, design: .monospaced)).foregroundColor(.green)
                TextField("command", text: $input)
                    .font(.system(.body, design: .monospaced))
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .submitLabel(.send)
                    .focused($focused)
                    .onSubmit(send)
                Button(action: send) { Image(systemName: "arrow.up.circle.fill").font(.title2) }
                    .disabled(input.trimmingCharacters(in: .whitespaces).isEmpty || sync.termBusy)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(Color(.secondarySystemBackground))
        }
    }

    private func color(_ line: String) -> Color {
        if line.hasPrefix("!") { return .red }
        if line.contains("> ") && !line.contains("\n") { return .green }
        return Color(white: 0.85)
    }

    private func send() {
        let cmd = input
        guard !cmd.trimmingCharacters(in: .whitespaces).isEmpty, !sync.termBusy else { return }
        input = ""
        Task { await sync.run(cmd) }
        focused = true
    }
}
