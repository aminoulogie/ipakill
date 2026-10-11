import Foundation

/// An app the PC signed, as reported by `ipakill serve`.
struct SignedApp: Codable, Identifiable {
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
    let started: Bool?
}

private struct InstallProgressResponse: Decodable {
    let ok: Bool
    let running: Bool
    let name: String
    let stage: String
    let done: Int64
    let total: Int64
    let percent: Double
    let started: Int64
    let bytesPerSecond: Double?
    let lines: [String]
    let next: Int
}

private struct TerminalRequest: Encodable {
    let command: String
    let cwd: String
}

private struct TerminalResponse: Decodable {
    let ok: Bool
    let output: String
    let cwd: String
    let exitCode: Int
    let error: String?
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
    @Published var installPhase = "Idle"
    @Published var installDetail = "Waiting"
    @Published var installPercent: Double = 0
    @Published var installConsoleLines: [String] = []
    @Published var installBytesDone: Int64 = 0
    @Published var installBytesTotal: Int64 = 0
    @Published var installBytesPerSecond: Double = 0
    @Published var terminalLines: [String] = ["Remote PowerShell ready."]
    @Published var terminalCWD = ""
    @Published var terminalBusy = false

    @Published var sources: [String] { didSet { defaults.set(sources, forKey: "sources") } }
    @Published var store: [StoreApp] = []
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
    private var storeRefreshTask: Task<Void, Never>?
    private var lastStoreRefresh: Date?
    private var installProgressCursor = 0
    private var installProgressStarted: Int64 = 0
    private var installRequestStarted: Int64 = 0
    private var installProgressFinished = false
    private var installDisplayTarget: Double = 0

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
                online = false
                return
            }
            if !online { say("synced with \(s.pc ?? "PC").") }
            online = true
            pcName = s.pc ?? ""
            phoneLink = s.phone ?? ""
            apps = s.apps ?? []
            if let data = try? JSONEncoder.iso.encode(apps) { defaults.set(data, forKey: "apps") }
        } catch {
            if online { say("lost connection to PC.") }
            online = false
        }
    }

    // MARK: store

    func refreshStore(force: Bool = false) async {
        if loadingStore { return }
        if !force, let lastStoreRefresh, Date().timeIntervalSince(lastStoreRefresh) < 60 { return }
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
        lastStoreRefresh = Date()
    }

    /// The apps one source offers, in the order the source lists them.
    func apps(in source: String) -> [StoreApp] {
        store.filter { $0.source == source }
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

    // MARK: remote terminal

    func runTerminal(_ command: String) async {
        let line = command.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !line.isEmpty else { return }
        guard online else {
            terminalLines.append("! PC is offline")
            return
        }
        guard var req = request("/terminal", timeout: 65) else { return }

        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        do {
            req.httpBody = try JSONEncoder().encode(TerminalRequest(command: line, cwd: terminalCWD))
        } catch {
            terminalLines.append("! \(error.localizedDescription)")
            return
        }

        let prompt = terminalCWD.isEmpty ? "PS>" : "PS \(terminalCWD)>"
        terminalLines.append("\(prompt) \(line)")
        terminalBusy = true
        defer {
            terminalBusy = false
            if terminalLines.count > 500 {
                terminalLines.removeFirst(terminalLines.count - 500)
            }
        }

        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            if (resp as? HTTPURLResponse)?.statusCode == 404 {
                terminalLines.append("! update/restart the PC companion to enable Terminal")
                return
            }
            let result = try Sync.decoder.decode(TerminalResponse.self, from: data)
            terminalCWD = result.cwd

            let normalized = result.output.replacingOccurrences(of: "\r\n", with: "\n")
            if !normalized.isEmpty {
                let lines = normalized.split(separator: "\n", omittingEmptySubsequences: false)
                terminalLines.append(contentsOf: lines.map(String.init))
                if terminalLines.last == "" { terminalLines.removeLast() }
            }
            if !result.ok {
                terminalLines.append("! \(result.error ?? "command exited with code \(result.exitCode)")")
            }
        } catch {
            terminalLines.append("! \(error.localizedDescription)")
        }
    }

    func clearTerminal() {
        terminalLines = ["Remote PowerShell ready."]
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
            URLQueryItem(name: "async", value: "1"),
        ]
        guard var req = request("/install-url?" + (q.percentEncodedQuery ?? ""), timeout: 900) else { return }
        req.httpMethod = "POST"
        let command = "$ ipakill get \(app.name) \(app.version)"
        say(command)
        await send(req, upload: nil, seed: command)
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
        guard var req = request("/install?name=\(encoded)&async=1", timeout: 600) else { return }
        req.httpMethod = "POST"

        let command = "$ ipakill \(name)"
        say(command)
        await send(req, upload: tmp, seed: command)
    }

    func clearInstallConsole() {
        installConsoleLines = []
    }

    private func beginInstallProgress(seed: String) {
        busy = true
        installPhase = "Preparing"
        installDetail = "Waiting for PC…"
        installPercent = 1
        installDisplayTarget = 1
        installBytesDone = 0
        installBytesTotal = 0
        installBytesPerSecond = 0
        installConsoleLines = [seed]
        installProgressCursor = 0
        installProgressStarted = 0
        installProgressFinished = false
        installRequestStarted = Int64(Date().timeIntervalSince1970 * 1000) - 2_000
    }

    private func phaseTitle(_ stage: String) -> String {
        switch stage {
        case "download": return "Downloading"
        case "upload": return "Uploading"
        case "sign": return "Signing"
        case "install": return "Installing"
        case "done": return "Complete"
        case "failed": return "Failed"
        default: return stage.isEmpty ? "Preparing" : stage.capitalized
        }
    }

    private func formatBytes(_ bytes: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file)
    }

    private func updateInstallDetail(stage: String) {
        if stage == "download" || stage == "upload" {
            var parts: [String] = []
            if installBytesTotal > 0 {
                parts.append("\(formatBytes(installBytesDone)) / \(formatBytes(installBytesTotal))")
            } else if installBytesDone > 0 {
                parts.append(formatBytes(installBytesDone))
            }
            if installBytesPerSecond > 0 {
                parts.append("\(formatBytes(Int64(installBytesPerSecond)))/s")
            }
            installDetail = parts.isEmpty ? phaseTitle(stage) : parts.joined(separator: " · ")
        } else {
            installDetail = phaseTitle(stage)
        }
    }

    private func animateInstallPercent() async {
        while busy && !Task.isCancelled {
            let delta = installDisplayTarget - installPercent
            if abs(delta) < 0.005 {
                installPercent = installDisplayTarget
            } else {
                installPercent += delta * 0.35
            }
            if installPercent < 1 && busy { installPercent = 1 }
            if installPercent > 100 { installPercent = 100 }
            try? await Task.sleep(nanoseconds: 16_000_000)
        }
    }

    private func send(_ req: URLRequest, upload file: URL?, seed: String) async {
        beginInstallProgress(seed: seed)

        let progressTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.pollInstallProgress()
                try? await Task.sleep(nanoseconds: 50_000_000)
            }
        }
        let animationTask = Task { [weak self] in
            await self?.animateInstallPercent()
        }

        defer {
            progressTask.cancel()
            animationTask.cancel()
            busy = false
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
                installConsoleLines.append("! the PC is running an older ipakill")
                installPhase = "Failed"
                installDetail = "Update/restart the PC companion"
                installProgressFinished = true
                return
            }

            let r = try Sync.decoder.decode(InstallResponse.self, from: data)
            if !r.ok {
                installConsoleLines.append("! \(r.error ?? "install failed")")
                installPhase = "Failed"
                installDetail = r.error ?? "Install failed"
                installProgressFinished = true
                return
            }

            // New PC versions return immediately and continue signing in the
            // background. Stay on the live /progress stream until it finishes.
            if r.started == true {
                let deadline = Date().addingTimeInterval(15 * 60)
                while !installProgressFinished && Date() < deadline {
                    try? await Task.sleep(nanoseconds: 50_000_000)
                }
                if !installProgressFinished {
                    installConsoleLines.append("! timed out waiting for install completion")
                    installPhase = "Failed"
                    installDetail = "Timed out"
                }
            } else {
                // Compatibility with an older synchronous PC response.
                for line in (r.log ?? "").split(separator: "\n") {
                    installConsoleLines.append(String(line))
                }
                installPhase = "Complete"
                installDetail = "Finished"
                installDisplayTarget = 100
                installPercent = 100
            }
        } catch {
            installConsoleLines.append("! \(error.localizedDescription)")
            installPhase = "Failed"
            installDetail = error.localizedDescription
            installProgressFinished = true
        }

        await pollInstallProgress()
        await poll()
    }

    private func pollInstallProgress() async {
        guard busy else { return }
        var path = "/progress?from=\(installProgressCursor)"
        guard let req = request(path, timeout: 3) else { return }

        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            guard (resp as? HTTPURLResponse)?.statusCode == 200 else { return }
            let p = try Sync.decoder.decode(InstallProgressResponse.self, from: data)
            guard p.ok else { return }

            // Older servers used seconds; normalize to milliseconds.
            let normalizedStarted = p.started > 0 && p.started < 100_000_000_000 ? p.started * 1000 : p.started
            if normalizedStarted > 0 && normalizedStarted < installRequestStarted && !p.running {
                return
            }

            if normalizedStarted != 0 && installProgressStarted != normalizedStarted {
                installProgressStarted = normalizedStarted
                installProgressCursor = 0
                path = "/progress?from=0"
            }

            installProgressCursor = p.next
            if !p.lines.isEmpty {
                installConsoleLines.append(contentsOf: p.lines)
                if installConsoleLines.count > 5000 {
                    installConsoleLines.removeFirst(installConsoleLines.count - 5000)
                }
            }

            let newPhase = phaseTitle(p.stage)
            if installPhase != newPhase {
                installPhase = newPhase
                installPercent = max(1, min(100, p.percent))
            }

            installDisplayTarget = max(1, min(100, p.percent))
            installBytesDone = p.done
            installBytesTotal = p.total
            installBytesPerSecond = p.bytesPerSecond ?? 0
            updateInstallDetail(stage: p.stage)

            if p.stage == "done" {
                installDisplayTarget = 100
                installPercent = 100
                installPhase = "Complete"
                installDetail = "Finished"
                installProgressFinished = true
            } else if p.stage == "failed" {
                installPhase = "Failed"
                installProgressFinished = true
            } else if !p.running && normalizedStarted >= installRequestStarted {
                installProgressFinished = true
            }
        } catch {
            // Best-effort. A transient missed poll must not kill the install.
        }
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
