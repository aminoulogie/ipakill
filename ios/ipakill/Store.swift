import Foundation

/// An app offered by a source.
struct StoreApp: Identifiable, Hashable {
    let name: String
    let version: String
    let url: String       // direct https link to the .ipa
    let source: String
    var id: String { source + "|" + name }
}

/// Loads a source. Two kinds are understood:
///  - a GitHub repo link (github.com/owner/repo): newest release with an .ipa asset
///  - an AltStore-style source JSON (https://.../apps.json)
enum SourceLoader {
    static func load(_ source: String) async throws -> [StoreApp] {
        if let repo = githubRepo(source) {
            return try await loadGitHub(repo, source: source)
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

    private struct GHRelease: Decodable {
        let tag_name: String
        let draft: Bool
        let assets: [GHAsset]
    }
    private struct GHAsset: Decodable {
        let name: String
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
                             url: ipa.browser_download_url, source: source)]
        }
        return []
    }

    private struct ASSource: Decodable {
        let apps: [ASApp]
    }
    private struct ASApp: Decodable {
        let name: String
        let version: String?
        let downloadURL: String?
        let versions: [ASVersion]?
    }
    private struct ASVersion: Decodable {
        let version: String
        let downloadURL: String
    }

    private static func loadAltStore(_ source: String) async throws -> [StoreApp] {
        guard let url = URL(string: source.hasPrefix("http") ? source : "https://" + source) else {
            throw SourceError.badLink
        }
        let (data, _) = try await URLSession.shared.data(from: url)
        let src = try JSONDecoder().decode(ASSource.self, from: data)
        return src.apps.compactMap { a in
            if let v = a.versions?.first {
                return StoreApp(name: a.name, version: v.version, url: v.downloadURL, source: source)
            }
            guard let v = a.version, let u = a.downloadURL else { return nil }
            return StoreApp(name: a.name, version: v, url: u, source: source)
        }
    }
}

enum SourceError: LocalizedError {
    case http(Int), badLink
    var errorDescription: String? {
        switch self {
        case .http(403): return "GitHub rate limit hit, try again later"
        case .http(404): return "repo not found"
        case .http(let c): return "server said \(c)"
        case .badLink: return "not a valid link"
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
