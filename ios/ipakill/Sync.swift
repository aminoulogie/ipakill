import Foundation

/// An app the PC signed, as reported by `ipakill serve`.
struct SignedApp: Codable, Identifiable {
    let name: String
    let bundle: String
    let signed: Date
    let expires: Date

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

/// Talks to `ipakill serve` on the PC over Wi-Fi.
@MainActor
final class Sync: ObservableObject {
    @Published var online = false
    @Published var pcName = ""
    @Published var phoneLink = ""          // "wifi", "usb" or "" as seen by the PC
    @Published var apps: [SignedApp] = []
    @Published var log: [String] = ["ipakill ready."]
    @Published var busy = false

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

        busy = true
        defer { busy = false }
        say("$ ipakill \(name)")
        say("uploading to \(pcName.isEmpty ? "PC" : pcName)... signing takes ~30s")
        do {
            let (data, _) = try await URLSession.shared.upload(for: req, fromFile: tmp)
            let r = try Sync.decoder.decode(InstallResponse.self, from: data)
            for line in (r.log ?? "").split(separator: "\n") {
                say(line.trimmingCharacters(in: .whitespaces))
            }
            if !r.ok { say("! \(r.error ?? "install failed")") }
        } catch {
            say("! \(error.localizedDescription)")
        }
        await poll()
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
