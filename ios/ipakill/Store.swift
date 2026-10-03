import Foundation

/// An app offered by a source.
struct StoreApp: Identifiable, Hashable {
    let name: String
    let version: String
    let url: String       // direct https link to the .ipa
    let source: String
    var icon: String? = nil
    var subtitle: String? = nil
    var developer: String? = nil
    var about: String? = nil          // the long description
    var notes: String? = nil          // what's new in this version
    var screenshots: [String] = []
    var size: Int64? = nil            // bytes
    var date: String? = nil
    var bundleID: String? = nil
    var category: String? = nil       // AltStore categories: games, utilities, ...
    var downloads: Int? = nil         // only GitHub releases report this
    var id: String { source + "|" + name }

    /// Lowercased text the search matches against.
    var searchKey: String { (name + " " + (developer ?? "") + " " + (subtitle ?? "")).lowercased() }
}

/// A source's own name and icon, shown on the Sources tab.
struct SourceInfo {
    var name: String
    var icon: String? = nil
}

/// Loads a source. Two kinds are understood:
///  - a GitHub repo link (github.com/owner/repo): newest release with an .ipa asset
///  - an AltStore / SideStore / ESign source JSON (https://.../apps.json)
enum SourceLoader {
    static func load(_ source: String) async throws -> (SourceInfo, [StoreApp]) {
        if let repo = githubRepo(source) {
            let owner = repo.split(separator: "/").first.map(String.init) ?? repo
            let info = SourceInfo(name: repo, icon: "https://github.com/\(owner).png")
            return (info, try await loadGitHub(repo, source: source))
        }
        return try await loadAltStore(source)
    }

    /// "https://github.com/a/b", "github.com/a/b.git" -> "a/b"
    static func githubRepo(_ s: String) -> String? {
        var t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        for p in ["https://", "http://", "www."] where t.hasPrefix(p) { t.removeFirst(p.count) }
        guard t.lowercased().hasPrefix("github.com/") else { return nil }
        // A source file hosted on GitHub (e.g. a release asset apps.json) is a
        // source, not a repo to read releases from.
        if t.lowercased().hasSuffix(".json") || t.contains("/releases/download/") { return nil }
        let parts = t.dropFirst("github.com/".count).split(separator: "/")
        guard parts.count >= 2 else { return nil }
        var repo = String(parts[1])
        if repo.hasSuffix(".git") { repo.removeLast(4) }
        return "\(parts[0])/\(repo)"
    }

    /// Short label for a source, used in the UI.
    static func label(_ s: String) -> String {
        if let repo = githubRepo(s) { return "gh:" + repo }
        return s.replacingOccurrences(of: "https://", with: "")
    }

    private struct GHRelease: Decodable {
        let tag_name: String
        let draft: Bool
        let body: String?
        let published_at: String?
        let assets: [GHAsset]
    }
    private struct GHAsset: Decodable {
        let name: String
        let size: Int64?
        let download_count: Int?
        let browser_download_url: String
    }

    private static func loadGitHub(_ repo: String, source: String) async throws -> [StoreApp] {
        var req = URLRequest(url: URL(string: "https://api.github.com/repos/\(repo)/releases?per_page=10")!)
        req.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        let (data, resp) = try await URLSession.shared.data(for: req)
        if let h = resp as? HTTPURLResponse, h.statusCode != 200 {
            throw SourceError.http(h.statusCode)
        }
        let releases = try JSONDecoder().decode([GHRelease].self, from: data)
        for r in releases where !r.draft {
            guard let ipa = r.assets.first(where: { $0.name.lowercased().hasSuffix(".ipa") }) else { continue }
            let file = String(ipa.name.dropLast(4))
            let version = Version.find(in: file) ?? Version.find(in: r.tag_name) ?? r.tag_name
            return [StoreApp(name: Version.stripped(file), version: version,
                             url: ipa.browser_download_url, source: source,
                             developer: repo.split(separator: "/").first.map(String.init),
                             notes: r.body, size: ipa.size,
                             date: r.published_at.map { String($0.prefix(10)) },
                             downloads: ipa.download_count)]
        }
        return []
    }

    /// Reads AltStore-style JSON loosely: sources disagree on field names and
    /// types (ESign uses "down"/"icon", sizes are numbers or strings...).
    private static func loadAltStore(_ source: String) async throws -> (SourceInfo, [StoreApp]) {
        guard let url = URL(string: source.hasPrefix("http") ? source : "https://" + source) else {
            throw SourceError.badLink
        }
        let (data, resp) = try await URLSession.shared.data(from: url)
        if let h = resp as? HTTPURLResponse, h.statusCode != 200 {
            throw SourceError.http(h.statusCode)
        }
        guard let root = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let apps = root["apps"] as? [[String: Any]]
        else { throw SourceError.notASource }

        func str(_ d: [String: Any], _ keys: String...) -> String? {
            for k in keys {
                if let v = d[k] as? String, !v.isEmpty { return v }
                if let v = d[k] as? NSNumber { return v.stringValue }
            }
            return nil
        }
        let info = SourceInfo(name: str(root, "name") ?? label(source),
                              icon: str(root, "iconURL", "iconUrl", "icon"))
        let list: [StoreApp] = apps.compactMap { a in
            guard let name = str(a, "name") else { return nil }
            let latest = (a["versions"] as? [[String: Any]])?.first ?? [:]
            guard let link = str(latest, "downloadURL", "down") ?? str(a, "downloadURL", "downloadUrl", "down")
            else { return nil }
            let version = str(latest, "version") ?? str(a, "version") ?? "?"
            let size = str(latest, "size") ?? str(a, "size")
            return StoreApp(name: name, version: version, url: link, source: source,
                            icon: str(a, "iconURL", "iconUrl", "icon"),
                            subtitle: str(a, "subtitle", "localizedDescription", "versionDescription"),
                            developer: str(a, "developerName", "developer"),
                            about: str(a, "localizedDescription", "description"),
                            notes: str(latest, "localizedDescription", "versionDescription") ?? str(a, "versionDescription"),
                            screenshots: screenshots(a),
                            size: size.flatMap { Int64($0) ?? Double($0).map { Int64($0) } },
                            date: (str(latest, "date") ?? str(a, "versionDate")).map { String($0.prefix(10)) },
                            bundleID: str(a, "bundleIdentifier", "bundleID"),
                            category: str(a, "category")?.lowercased(),
                            downloads: str(a, "downloads", "downloadCount", "download_count").flatMap { Int($0) })
        }
        return (info, list)
    }
}

/// Screenshot links, in whichever shape the source uses: "screenshotURLs",
/// "screenshots" as a list of links or of {imageURL}, or split per device.
private func screenshots(_ a: [String: Any]) -> [String] {
    func links(_ v: Any?) -> [String] {
        if let list = v as? [String] { return list }
        if let list = v as? [[String: Any]] {
            return list.compactMap { ($0["imageURL"] ?? $0["url"]) as? String }
        }
        return []
    }
    if let byDevice = a["screenshots"] as? [String: Any] {
        let phone = links(byDevice["iphone"])
        return phone.isEmpty ? links(byDevice["ipad"]) : phone
    }
    let list = links(a["screenshots"])
    return list.isEmpty ? links(a["screenshotURLs"]) : list
}

enum SourceError: LocalizedError {
    case http(Int), badLink, notASource
    var errorDescription: String? {
        switch self {
        case .http(403): return "GitHub rate limit hit, try again later"
        case .http(404): return "repo not found"
        case .http(let c): return "server said \(c)"
        case .badLink: return "not a valid link"
        case .notASource: return "not an app source (no \"apps\" list)"
        }
    }
}

enum Version {
    private static let re = try! NSRegularExpression(pattern: #"\d+(\.\d+)+(-[0-9A-Za-z.]+)?"#)
    private static let tail = try! NSRegularExpression(pattern: #"(?i)[-_ ]v?\d[\w.\-() ]*$"#)

    static func find(in s: String) -> String? {
        let range = NSRange(s.startIndex..., in: s)
        guard let m = re.matches(in: s, range: range).last, let r = Range(m.range, in: s) else { return nil }
        return String(s[r])
    }

    /// "SOMA-0.0.167" -> "SOMA"
    static func stripped(_ s: String) -> String {
        let out = tail.stringByReplacingMatches(in: s, range: NSRange(s.startIndex..., in: s), withTemplate: "")
        return out.isEmpty ? s : out
    }

    static func isNewer(_ a: String, than b: String) -> Bool {
        a.compare(b, options: .numeric) == .orderedDescending
    }
}
