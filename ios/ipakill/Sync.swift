import Foundation

/// An app the PC signed, as reported by `ipakill serve`.
struct SignedApp: Codable, Identifiable, Equatable {
    let name: String
    let bundle: String
    let signed: Date
    let expires: Date
    let version: String?

    var id: String { bundle.isEmpty ? name : bundle }
    var daysLeft: Double { expires.timeIntervalSinceNow / 86_400 }
}

private struct StatusResponse: Decodable {
    let ok: Bool
    let pc: String?
    let phone: String?
    let apps: [SignedApp]?
    let error: String?
}

private struct InstallResponse: Decodable {
    let ok: Bool
    let error: String?
    let log: String?
}

private struct ProgressResponse: Decodable {
    let running: Bool
    let name: String?
    let stage: String?
    let done: Int64?
    let total: Int64?
    let percent: Int?
    let started: Int64?
    let lines: [String]?
    let next: Int?
}

/// What the PC is doing for the current install, for the progress bar.
struct InstallProgress: Equatable {
    var name = ""
    var stage = ""          // download, upload, sign, install, done, failed
    var done: Int64 = 0
    var total: Int64 = 0
    var percent = 0

    /// 0...1 across the whole install: getting the file is the first 40%,
    /// signing 40-60%, installing on the phone the rest.
    var fraction: Double {
        switch stage {
        case "download", "upload": return total > 0 ? 0.4 * Double(done) / Double(total) : 0.05
        case "sign": return 0.45
        case "install": return 0.6 + 0.4 * Double(percent) / 100
        case "done": return 1
        default: return 0
        }
    }

    var label: String {
        let f = ByteCountFormatter()
        switch stage {
        case "download", "upload":
            let verb = stage == "download" ? "Downloading" : "Uploading"
            return total > 0 ? "\(verb) \(f.string(fromByteCount: done)) of \(f.string(fromByteCount: total))"
                             : "\(verb) \(f.string(fromByteCount: done))"
        case "sign": return "Signing with your Apple ID…"
        case "install": return "Installing on iPhone \(percent)%"
        case "done": return "Done"
        case "failed": return "Failed"
        default: return "Starting…"
        }
    }
}

struct AppIDInfo: Decodable, Identifiable {
    let name: String
    let identifier: String
    var id: String { identifier }
}

private struct AppIDsResponse: Decodable {
    let ok: Bool
    let error: String?
    let ids: [AppIDInfo]?
    let limit: Int?
}

private struct ExecResponse: Decodable {
    let ok: Bool
    let error: String?
    let output: String?
    let code: Int?
    let cwd: String?
}

private struct CertResponse: Decodable {
    let ok: Bool
    let error: String?
    let p12: Data?          // base64 in the JSON
    let password: String?
}

/// Talks to `ipakill serve` on the PC over Wi-Fi.
@MainActor
final class Sync: ObservableObject {
    @Published var online = false
    @Published var pcName = ""
    @Published var phoneLink = ""          // "wifi", "usb" or "" as seen by the PC
    @Published var apps: [SignedApp] = []
    @Published var log: [String] = ["ipakill ready."]
    @Published var busy = false
    @Published var progress: InstallProgress?
    private var progressLines = 0

    @Published var appIDs: [AppIDInfo]?
    @Published var appIDLimit = 10
    @Published var appIDsError: String?
    @Published var loadingAppIDs = false

    // Terminal: commands run on the PC (needs 'ipakill-core serve --shell').
    @Published var term: [String] = ["type 'help' - commands run on the PC"]
    @Published var termCwd = ""
    @Published var termBusy = false

    @Published var sources: [String] { didSet { defaults.set(sources, forKey: "sources") } }
    @Published var store: [StoreApp] = [] {
        didSet {
            storeSorted = store.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
            storeBySource = Dictionary(grouping: store, by: \.source)
        }
    }
    /// Kept ready so views don't sort or filter thousands of apps on every redraw.
    private(set) var storeSorted: [StoreApp] = []
    private var storeBySource: [String: [StoreApp]] = [:]
    @Published var storeErrors: [String: String] = [:]   // source -> error
    @Published var sourceInfo: [String: SourceInfo] = [:]
    @Published var loadingStore = false

    static let defaultSources = [
        "https://github.com/aminoulogie/kite-bay-otter-topaz",   // SOMA
        "https://github.com/aminoulogie/ipakill",
    ]

    @Published var host: String { didSet { defaults.set(host, forKey: "host") } }
    @Published var code: String { didSet { defaults.set(code, forKey: "code") } }

    /// When this copy of ipakill itself stops working, read from its own provisioning profile.
    let selfExpires: Date? = Sync.readOwnExpiry()

    private let defaults = UserDefaults.standard
    private var timer: Timer?

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()

    init() {
        host = defaults.string(forKey: "host") ?? ""
        code = defaults.string(forKey: "code") ?? ""
        sources = defaults.stringArray(forKey: "sources") ?? Sync.defaultSources
        // Show the last known list even before the PC answers.
        if let data = defaults.data(forKey: "apps"),
           let cached = try? Sync.decoder.decode([SignedApp].self, from: data) {
            apps = cached
        }
    }

    var configured: Bool { !host.isEmpty && !code.isEmpty }

    func start() {
        timer?.invalidate()
        timer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            Task { await self?.poll() }
        }
        Task { await poll() }
    }

    func stop() {
        timer?.invalidate()
        timer = nil
    }

    func say(_ line: String) {
        log.append(line)
        if log.count > 200 { log.removeFirst(log.count - 200) }
    }

    private func url(_ path: String) -> URL? {
        var h = host.trimmingCharacters(in: .whitespacesAndNewlines)
        if h.hasPrefix("http://") { h.removeFirst(7) }
        if !h.contains(":") { h += ":7777" }
        return URL(string: "http://\(h)\(path)")
    }

    private func request(_ path: String, timeout: TimeInterval) -> URLRequest? {
        guard let u = url(path) else { return nil }
        var r = URLRequest(url: u, timeoutInterval: timeout)
        r.setValue(code, forHTTPHeaderField: "X-Ipakill-Code")
        return r
    }

    func poll() async {
        guard configured, let req = request("/status", timeout: 4) else {
            online = false
            return
        }
        do {
            let (data, _) = try await URLSession.shared.data(for: req)
            let s = try Sync.decoder.decode(StatusResponse.self, from: data)
            guard s.ok else {
                if online || log.last != "! \(s.error ?? "rejected")" { say("! \(s.error ?? "rejected")") }
                if online { online = false }
                return
            }
            if !online { say("synced with \(s.pc ?? "PC").") }
            // Only assign what changed: every assignment redraws every screen.
            if !online { online = true }
            if pcName != (s.pc ?? "") { pcName = s.pc ?? "" }
            if phoneLink != (s.phone ?? "") { phoneLink = s.phone ?? "" }
            let newApps = s.apps ?? []
            if apps != newApps {
                apps = newApps
                if let data = try? JSONEncoder.iso.encode(apps) { defaults.set(data, forKey: "apps") }
            }
        } catch {
            if online {
                say("lost connection to PC.")
                online = false
            }
        }
    }

    // MARK: store

    func refreshStore() async {
        loadingStore = true
        defer { loadingStore = false }
        var all: [StoreApp] = []
        var errors: [String: String] = [:]
        var infos: [String: SourceInfo] = [:]
        await withTaskGroup(of: (String, Result<(SourceInfo, [StoreApp]), Error>).self) { group in
            for src in sources {
                group.addTask {
                    do { return (src, .success(try await SourceLoader.load(src))) }
                    catch { return (src, .failure(error)) }
                }
            }
            for await (src, result) in group {
                switch result {
                case .success(let (info, apps)):
                    infos[src] = info
                    all += apps
                case .failure(let e): errors[src] = e.localizedDescription
                }
            }
        }
        store = all
        storeErrors = errors
        sourceInfo = infos
    }

    /// The apps one source offers, in the order the source lists them.
    func apps(in source: String) -> [StoreApp] {
        storeBySource[source] ?? []
    }

    func sourceName(_ source: String) -> String {
        sourceInfo[source]?.name ?? SourceLoader.label(source)
    }

    func addSource(_ s: String) {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty, !sources.contains(t) else { return }
        sources.append(t)
        say("source added: \(SourceLoader.label(t))")
        Task { await refreshStore() }
    }

    func removeSource(_ s: String) {
        sources.removeAll { $0 == s }
        store.removeAll { $0.source == s }
        storeErrors[s] = nil
        sourceInfo[s] = nil
    }

    /// The store entry for an installed app (matched by name).
    func storeEntry(for app: SignedApp) -> StoreApp? {
        store.first { $0.name.lowercased() == app.name.lowercased() }
    }

    /// The installed app for a store entry, if any.
    func installed(_ s: StoreApp) -> SignedApp? {
        apps.first { $0.name.lowercased() == s.name.lowercased() }
    }

    /// Icon for an installed app: the source's icon, else the one the PC pulls out of the .ipa.
    func icon(for app: SignedApp) -> URL? {
        if let s = storeEntry(for: app)?.icon, let u = URL(string: s) { return u }
        var q = URLComponents()
        q.queryItems = [URLQueryItem(name: "app", value: app.id), URLQueryItem(name: "code", value: code)]
        return configured ? url("/icon?" + (q.percentEncodedQuery ?? "")) : nil
    }

    func icon(for s: StoreApp) -> URL? {
        if let i = s.icon, let u = URL(string: i) { return u }
        return installed(s).flatMap { icon(for: $0) }
    }

    var updateCount: Int { apps.filter { update(for: $0) != nil }.count }

    func update(for app: SignedApp) -> StoreApp? {
        guard let s = storeEntry(for: app), let v = app.version, !v.isEmpty else { return nil }
        return Version.isNewer(s.version, than: v) ? s : nil
    }

    /// Asks the PC to download the .ipa from the source, sign it and install it.
    func install(_ app: StoreApp) async {
        guard online else {
            say("! not synced - start 'ipakill serve' on the PC")
            return
        }
        var q = URLComponents()
        q.queryItems = [
            URLQueryItem(name: "url", value: app.url),
            URLQueryItem(name: "name", value: app.name),
            URLQueryItem(name: "version", value: app.version),
        ]
        guard var req = request("/install-url?" + (q.percentEncodedQuery ?? ""), timeout: 900) else { return }
        req.httpMethod = "POST"
        say("$ ipakill get \(app.name) \(app.version)")
        say("pc is downloading + signing... ~1 min")
        await send(req, upload: nil)
    }

    // MARK: install from Files

    /// Uploads an .ipa to the PC, which signs it and installs it back onto this iPhone.
    func install(_ file: URL) async {
        guard file.pathExtension.lowercased() == "ipa" else {
            say("! \(file.lastPathComponent) is not an .ipa")
            return
        }
        guard online else {
            say("! not synced - start 'ipakill serve' on the PC")
            return
        }
        let name = file.lastPathComponent
        let scoped = file.startAccessingSecurityScopedResource()
        defer { if scoped { file.stopAccessingSecurityScopedResource() } }

        // Copy first: the picked file may live in iCloud and vanish after access ends.
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent(name)
        try? FileManager.default.removeItem(at: tmp)
        do {
            try FileManager.default.copyItem(at: file, to: tmp)
        } catch {
            say("! cannot read \(name): \(error.localizedDescription)")
            return
        }
        defer { try? FileManager.default.removeItem(at: tmp) }

        let encoded = name.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? name
        guard var req = request("/install?name=\(encoded)", timeout: 600) else { return }
        req.httpMethod = "POST"

        say("$ ipakill \(name)")
        say("uploading to \(pcName.isEmpty ? "PC" : pcName)... signing takes ~30s")
        await send(req, upload: tmp)
    }

    private func send(_ req: URLRequest, upload file: URL?) async {
        busy = true
        progress = InstallProgress()
        progressLines = 0
        // Poll the PC for the progress bar and the full log while it works.
        progressFrom = 0
        progressStarted = nil
        let poller = Task { @MainActor in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 500_000_000)
                if Task.isCancelled { break }
                await pollProgress()
            }
        }
        defer {
            poller.cancel()
            busy = false
            Task { @MainActor in
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                if !self.busy { self.progress = nil }
            }
        }
        do {
            let data: Data
            let resp: URLResponse
            if let file {
                (data, resp) = try await URLSession.shared.upload(for: req, fromFile: file)
            } else {
                (data, resp) = try await URLSession.shared.data(for: req)
            }
            if (resp as? HTTPURLResponse)?.statusCode == 404 {
                say("! the pc is running an older ipakill - restart 'ipakill serve' on the pc")
                return
            }
            let r = try Sync.decoder.decode(InstallResponse.self, from: data)
            poller.cancel()
            await drainProgress()
            if progressLines == 0 {  // an older PC without /progress: show its summary
                for line in (r.log ?? "").split(separator: "\n") {
                    say(line.trimmingCharacters(in: .whitespaces))
                }
            }
            progress?.stage = r.ok ? "done" : "failed"
            if !r.ok { say("! \(r.error ?? "install failed")") }
        } catch {
            say("! \(error.localizedDescription)")
        }
        await poll()
    }

    private var progressFrom = 0
    private var progressStarted: Int64?

    /// Takes the PC's new log lines and progress for the current install.
    private func pollProgress() async {
        guard let p = await fetchProgress(from: progressFrom) else { return }
        if progressStarted == nil { progressStarted = p.started }
        guard p.started == progressStarted else { return }  // an older install
        for line in p.lines ?? [] {
            say("  " + line)
            progressLines += 1
        }
        progressFrom = p.next ?? progressFrom
        progress = InstallProgress(name: p.name ?? "", stage: p.stage ?? "", done: p.done ?? 0,
                                   total: p.total ?? 0, percent: p.percent ?? 0)
    }

    /// After the install answered: the last lines, which hold the error if any.
    private func drainProgress() async {
        guard progressStarted != nil else { return }
        await pollProgress()
    }

    private func fetchProgress(from: Int) async -> ProgressResponse? {
        guard let req = request("/progress?from=\(from)", timeout: 4),
              let (data, resp) = try? await URLSession.shared.data(for: req),
              (resp as? HTTPURLResponse)?.statusCode == 200
        else { return nil }
        return try? JSONDecoder().decode(ProgressResponse.self, from: data)
    }

    // MARK: app IDs

    /// The app IDs the Apple ID holds right now (a free one gets 10 per 7 days).
    func loadAppIDs(fresh: Bool = false) async {
        guard online, let req = request("/appids" + (fresh ? "?fresh=1" : ""), timeout: 60) else { return }
        loadingAppIDs = true
        defer { loadingAppIDs = false }
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            if (resp as? HTTPURLResponse)?.statusCode == 404 {
                appIDsError = "Update the PC part to see this (Terminal: update)"
                return
            }
            let r = try JSONDecoder().decode(AppIDsResponse.self, from: data)
            if r.ok {
                appIDs = r.ids ?? []
                appIDLimit = r.limit ?? 10
                appIDsError = nil
            } else {
                appIDsError = r.error ?? "could not list app IDs"
            }
        } catch {
            appIDsError = error.localizedDescription
        }
    }

    // MARK: terminal

    func run(_ command: String) async {
        let line = command.trimmingCharacters(in: .whitespacesAndNewlines)
        if line == "clear" {
            term = []
            return
        }
        term.append("\(termCwd.isEmpty ? "" : termCwd)> \(line)")
        guard online else {
            term.append("! not connected to the PC")
            return
        }
        guard var req = request("/exec", timeout: 900),
              let body = try? JSONEncoder().encode(["cmd": line]) else { return }
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        termBusy = true
        defer { termBusy = false }
        do {
            let (data, resp) = try await URLSession.shared.upload(for: req, from: body)
            if (resp as? HTTPURLResponse)?.statusCode == 404 {
                term.append("! the pc is running an older ipakill without the terminal - update it once from the PC")
                return
            }
            let r = try JSONDecoder().decode(ExecResponse.self, from: data)
            guard r.ok else {
                term.append("! \(r.error ?? "failed")")
                return
            }
            let out = (r.output ?? "").trimmingCharacters(in: .newlines)
            if !out.isEmpty { term.append(out) }
            if let c = r.code, c != 0 { term.append("! exit code \(c)") }
            if let cwd = r.cwd { termCwd = cwd }
        } catch {
            term.append("! \(error.localizedDescription)")
        }
        if term.count > 500 { term.removeFirst(term.count - 500) }
    }

    // MARK: certificate for apps run inside ipakill

    /// Whether LiveContainer's signer has a certificate (it signs guest apps on the phone).
    var hasCertificate: Bool { LCSharedUtils.certificatePassword() != nil }

    /// Asks the PC for the certificate this copy of ipakill is signed with and
    /// stores it where LiveContainer's signer reads it. The PC only keeps the
    /// key; the certificate itself comes from our own provisioning profile.
    @discardableResult
    func importCertificate() async -> Bool {
        guard online else {
            say("! connect to the PC first - it holds the signing key")
            return false
        }
        guard let path = Bundle.main.path(forResource: "embedded", ofType: "mobileprovision"),
              let profile = FileManager.default.contents(atPath: path)
        else {
            say("! this copy of ipakill has no provisioning profile")
            return false
        }
        guard var req = request("/cert", timeout: 30) else { return false }
        req.httpMethod = "POST"
        say("$ ipakill cert")
        busy = true
        defer { busy = false }
        do {
            let (data, resp) = try await URLSession.shared.upload(for: req, from: profile)
            if (resp as? HTTPURLResponse)?.statusCode == 404 {
                say("! the pc is running an older ipakill - rebuild ipakill-core and restart 'ipakill serve'")
                return false
            }
            let r = try JSONDecoder().decode(CertResponse.self, from: data)
            guard r.ok, let p12 = r.p12, let pass = r.password else {
                say("! \(r.error ?? "the PC sent no certificate")")
                return false
            }
            guard let team = LCUtils.getCertTeamId(withKeyData: p12, password: pass) else {
                say("! the certificate from the PC could not be opened")
                return false
            }
            // Same keys LiveContainer's own "Import Certificate" writes.
            LCUtils.appGroupUserDefault.set(p12, forKey: "LCCertificateData")
            LCUtils.appGroupUserDefault.set(pass, forKey: "LCCertificatePassword")
            LCUtils.appGroupUserDefault.set(NSDate.now, forKey: "LCCertificateUpdateDate")
            UserDefaults.standard.set(LCSharedUtils.appGroupID(), forKey: "LCAppGroupID")
            objectWillChange.send()
            say("certificate imported (team \(team)). apps can now run inside ipakill.")
            return true
        } catch {
            say("! \(error.localizedDescription)")
            return false
        }
    }

    private static func readOwnExpiry() -> Date? {
        guard let path = Bundle.main.path(forResource: "embedded", ofType: "mobileprovision"),
              let raw = try? Data(contentsOf: URL(fileURLWithPath: path)),
              let text = String(data: raw, encoding: .isoLatin1),
              let key = text.range(of: "<key>ExpirationDate</key>"),
              let open = text.range(of: "<date>", range: key.upperBound..<text.endIndex),
              let close = text.range(of: "</date>", range: open.upperBound..<text.endIndex)
        else { return nil }
        return ISO8601DateFormatter().date(from: String(text[open.upperBound..<close.lowerBound]))
    }
}

private extension JSONEncoder {
    static let iso: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }()
}
