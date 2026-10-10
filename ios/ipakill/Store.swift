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
    var id: String { source + "|" + name }
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

    private struct GHRelease: Codable {
        let tag_name: String
        let draft: Bool
        let assets: [GHAsset]
    }
    private struct GHAsset: Codable {
        let name: String
        let browser_download_url: String
    }

    private static func loadGitHub(_ repo: String, source: String) async throws -> [StoreApp] {
        let safeKey = repo.replacingOccurrences(of: "/", with: "_")
        let cacheKey = "github.release.cache." + safeKey
        let etagKey = "github.release.etag." + safeKey
        let defaults = UserDefaults.standard
        let decoder = JSONDecoder()

        func apps(from release: GHRelease) -> [StoreApp] {
            guard !release.draft,
                  let ipa = release.assets.first(where: { $0.name.lowercased().hasSuffix(".ipa") }) else { return [] }
            let file = String(ipa.name.dropLast(4))
            let version = Version.find(in: file) ?? Version.find(in: release.tag_name) ?? release.tag_name
            return [StoreApp(name: Version.stripped(file), version: version,
                             url: ipa.browser_download_url, source: source)]
        }

        let cachedRelease: GHRelease? = defaults.data(forKey: cacheKey).flatMap { try? decoder.decode(GHRelease.self, from: $0) }
        var req = URLRequest(url: URL(string: "https://api.github.com/repos/\(repo)/releases/latest")!,
                             cachePolicy: .reloadRevalidatingCacheData, timeoutInterval: 15)
        req.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        req.setValue("ipakill-ios", forHTTPHeaderField: "User-Agent")
        if let etag = defaults.string(forKey: etagKey) { req.setValue(etag, forHTTPHeaderField: "If-None-Match") }

        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw SourceError.badLink }

        if http.statusCode == 304, let cachedRelease { return apps(from: cachedRelease) }

        if http.statusCode == 403 || http.statusCode == 429 {
            if let cachedRelease { return apps(from: cachedRelease) }
            let reset = http.value(forHTTPHeaderField: "X-RateLimit-Reset").flatMap(TimeInterval.init)
            throw SourceError.rateLimited(reset)
        }

        guard http.statusCode == 200 else { throw SourceError.http(http.statusCode) }
        let release = try decoder.decode(GHRelease.self, from: data)
        defaults.set(data, forKey: cacheKey)
        if let etag = http.value(forHTTPHeaderField: "ETag") { defaults.set(etag, forKey: etagKey) }
        return apps(from: release)
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
            return StoreApp(name: name, version: version, url: link, source: source,
                            icon: str(a, "iconURL", "iconUrl", "icon"),
                            subtitle: str(a, "subtitle", "localizedDescription", "versionDescription"),
                            developer: str(a, "developerName", "developer"))
        }
        return (info, list)
    }
}

enum SourceError: LocalizedError {
    case http(Int), rateLimited(TimeInterval?), badLink, notASource
    var errorDescription: String? {
        switch self {
        case .rateLimited(let reset):
            if let reset { return "GitHub limit reached · retry after \(Date(timeIntervalSince1970: reset).formatted(date: .omitted, time: .shortened))" }
            return "GitHub limit reached · cached data unavailable"
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
