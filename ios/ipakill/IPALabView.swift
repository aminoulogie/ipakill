import SwiftUI
import UniformTypeIdentifiers

struct IPALabItem: Codable, Identifiable, Hashable {
    let path: String
    let kind: String
    let size: UInt64
    let architecture: String?
    let loadCommands: [String]?
    let rpaths: [String]?

    var id: String { path }
    var displayName: String { URL(fileURLWithPath: path).lastPathComponent }
}

struct IPALabReport: Codable {
    let ok: Bool
    let session: String?
    let appRoot: String?
    let mainBinary: String?
    let items: [IPALabItem]?
    let warnings: [String]?
    let error: String?
    let modified: Bool
}

@MainActor
private final class IPALabClient {
    let host: String
    let code: String

    init(sync: Sync) {
        host = sync.host
        code = sync.code
    }

    private func request(_ path: String, method: String = "GET") throws -> URLRequest {
        var h = host.trimmingCharacters(in: .whitespacesAndNewlines)
        if h.hasPrefix("http://") { h.removeFirst(7) }
        if !h.contains(":") { h += ":7777" }
        guard let url = URL(string: "http://\(h)\(path)") else { throw LabError.message("Bad PC address") }
        var req = URLRequest(url: url, timeoutInterval: 900)
        req.httpMethod = method
        req.setValue(code, forHTTPHeaderField: "X-Ipakill-Code")
        return req
    }

    private func decode(_ data: Data) throws -> IPALabReport {
        let report = try JSONDecoder().decode(IPALabReport.self, from: data)
        if !report.ok { throw LabError.message(report.error ?? "IPA Lab operation failed") }
        return report
    }

    private func localCopy(_ source: URL) throws -> URL {
        let scoped = source.startAccessingSecurityScopedResource()
        defer { if scoped { source.stopAccessingSecurityScopedResource() } }
        let dst = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + "-" + source.lastPathComponent)
        try? FileManager.default.removeItem(at: dst)
        try FileManager.default.copyItem(at: source, to: dst)
        return dst
    }

    func open(_ source: URL) async throws -> IPALabReport {
        let tmp = try localCopy(source)
        defer { try? FileManager.default.removeItem(at: tmp) }
        var req = try request("/lab/open", method: "POST")
        req.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        let (data, _) = try await URLSession.shared.upload(for: req, fromFile: tmp)
        return try decode(data)
    }

    func inject(session: String, dylib source: URL) async throws -> IPALabReport {
        let tmp = try localCopy(source)
        defer { try? FileManager.default.removeItem(at: tmp) }
        let name = source.lastPathComponent.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? source.lastPathComponent
        var req = try request("/lab/inject?session=\(session)&name=\(name)", method: "POST")
        req.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        let (data, _) = try await URLSession.shared.upload(for: req, fromFile: tmp)
        return try decode(data)
    }

    func replace(session: String, path: String, with source: URL) async throws -> IPALabReport {
        let tmp = try localCopy(source)
        defer { try? FileManager.default.removeItem(at: tmp) }
        let p = path.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? path
        var req = try request("/lab/replace?session=\(session)&path=\(p)", method: "POST")
        req.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        let (data, _) = try await URLSession.shared.upload(for: req, fromFile: tmp)
        return try decode(data)
    }

    func remove(session: String, path: String) async throws -> IPALabReport {
        let p = path.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? path
        let req = try request("/lab/remove?session=\(session)&path=\(p)", method: "POST")
        let (data, _) = try await URLSession.shared.data(for: req)
        return try decode(data)
    }

    func extract(session: String, item: IPALabItem) async throws -> URL {
        let p = item.path.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? item.path
        let req = try request("/lab/extract?session=\(session)&path=\(p)")
        let (data, response) = try await URLSession.shared.data(for: req)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else {
            throw LabError.message(String(data: data, encoding: .utf8) ?? "Extract failed")
        }
        let dst = FileManager.default.temporaryDirectory.appendingPathComponent(item.displayName)
        try? FileManager.default.removeItem(at: dst)
        try data.write(to: dst, options: .atomic)
        return dst
    }

    func exportIPA(session: String) async throws -> URL {
        let req = try request("/lab/export?session=\(session)")
        let (data, response) = try await URLSession.shared.data(for: req)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else {
            throw LabError.message(String(data: data, encoding: .utf8) ?? "Export failed")
        }
        let dst = FileManager.default.temporaryDirectory.appendingPathComponent("ipakill-modified.ipa")
        try? FileManager.default.removeItem(at: dst)
        try data.write(to: dst, options: .atomic)
        return dst
    }
}

private enum LabError: LocalizedError {
    case message(String)
    var errorDescription: String? {
        switch self { case .message(let text): return text }
    }
}

private enum LabPickerMode: Equatable {
    case ipa
    case inject
    case replace(String)
}

struct IPALabView: View {
    @EnvironmentObject private var sync: Sync
    @State private var report: IPALabReport?
    @State private var status = "Choose an IPA to inspect."
    @State private var picker: LabPickerMode?
    @State private var busy = false
    @State private var sharedFile: URL?
    @State private var errorText: String?

    private var session: String? { report?.session }

    var body: some View {
        List {
            Section {
                Button {
                    picker = .ipa
                } label: {
                    Label(report == nil ? "Open IPA" : "Open Another IPA", systemImage: "shippingbox")
                }
                .disabled(busy || !sync.online)

                if let report, let session = report.session {
                    Button {
                        picker = .inject
                    } label: {
                        Label("Inject Dylib", systemImage: "plus.circle")
                    }
                    .disabled(busy)

                    Button {
                        Task { await export(session) }
                    } label: {
                        Label("Export Modified IPA", systemImage: "square.and.arrow.up")
                    }
                    .disabled(busy)
                }
            } footer: {
                Text(sync.online ? status : "Connect the PC companion first. IPA Lab processing runs on the PC.")
            }

            if let warnings = report?.warnings, !warnings.isEmpty {
                Section("Compatibility") {
                    ForEach(warnings, id: \.self) { warning in
                        Label(warning, systemImage: "exclamationmark.triangle")
                            .font(.footnote)
                            .foregroundStyle(.orange)
                    }
                }
            }

            if let report {
                Section("App") {
                    LabeledContent("Root", value: report.appRoot ?? "Unknown")
                    LabeledContent("Main Binary", value: report.mainBinary.map { URL(fileURLWithPath: $0).lastPathComponent } ?? "Unknown")
                    LabeledContent("State", value: report.modified ? "Modified" : "Original")
                }

                Section("Mach-O / Dylibs") {
                    ForEach(report.items ?? []) { item in
                        NavigationLink {
                            IPALabItemView(
                                item: item,
                                onExtract: { Task { await extract(item) } },
                                onReplace: {
                                    picker = .replace(item.path)
                                },
                                onRemove: { Task { await remove(item) } }
                            )
                        } label: {
                            VStack(alignment: .leading, spacing: 3) {
                                HStack {
                                    Text(item.displayName)
                                        .font(.body.weight(.semibold))
                                        .lineLimit(1)
                                    Spacer()
                                    Text(item.kind.uppercased())
                                        .font(.caption2.weight(.bold))
                                        .foregroundStyle(.secondary)
                                }
                                Text([item.architecture, ByteCountFormatter.string(fromByteCount: Int64(item.size), countStyle: .file)]
                                    .compactMap { $0 }.joined(separator: " · "))
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                            if item.kind != "executable" {
                                Button(role: .destructive) { Task { await remove(item) } } label: {
                                    Label("Remove", systemImage: "trash")
                                }
                            }
                            Button { picker = .replace(item.path) } label: {
                                Label("Replace", systemImage: "arrow.triangle.2.circlepath")
                            }
                            .tint(.blue)
                        }
                        .swipeActions(edge: .leading, allowsFullSwipe: true) {
                            Button { Task { await extract(item) } } label: {
                                Label("Extract", systemImage: "square.and.arrow.down")
                            }
                            .tint(.green)
                        }
                    }
                }
            }
        }
        .navigationTitle("IPA Lab")
        .navigationBarTitleDisplayMode(.inline)
        .overlay {
            if busy {
                ZStack {
                    Color.black.opacity(0.18).ignoresSafeArea()
                    ProgressView().controlSize(.large)
                }
            }
        }
        .fileImporter(
            isPresented: Binding(
                get: { picker != nil },
                set: { if !$0 { picker = nil } }
            ),
            allowedContentTypes: [.data]
        ) { result in
            guard case .success(let url) = result, let mode = picker else {
                picker = nil
                return
            }
            picker = nil
            Task { await handlePick(url, mode: mode) }
        }
        .sheet(isPresented: Binding(
            get: { sharedFile != nil },
            set: { if !$0 { sharedFile = nil } }
        )) {
            if let sharedFile {
                NavigationStack {
                    VStack(spacing: 20) {
                        Image(systemName: "doc.zipper").font(.system(size: 48))
                        Text(sharedFile.lastPathComponent).font(.headline)
                        ShareLink(item: sharedFile) {
                            Label("Share / Save File", systemImage: "square.and.arrow.up")
                                .font(.headline)
                        }
                    }
                    .padding()
                    .navigationTitle("Ready")
                    .toolbar {
                        ToolbarItem(placement: .topBarTrailing) {
                            Button("Done") { self.sharedFile = nil }
                        }
                    }
                }
                .presentationDetents([.medium])
            }
        }
        .alert("IPA Lab", isPresented: Binding(
            get: { errorText != nil },
            set: { if !$0 { errorText = nil } }
        )) {
            Button("OK", role: .cancel) { errorText = nil }
        } message: {
            Text(errorText ?? "")
        }
    }

    private func handlePick(_ url: URL, mode: LabPickerMode) async {
        busy = true
        defer { busy = false }
        do {
            let client = IPALabClient(sync: sync)
            switch mode {
            case .ipa:
                status = "Uploading and analyzing \(url.lastPathComponent)…"
                report = try await client.open(url)
                status = "Analysis complete."
            case .inject:
                guard let session else { return }
                status = "Injecting \(url.lastPathComponent)…"
                report = try await client.inject(session: session, dylib: url)
                status = "Dylib injected. Re-sign before installing."
            case .replace(let path):
                guard let session else { return }
                status = "Replacing \(URL(fileURLWithPath: path).lastPathComponent)…"
                report = try await client.replace(session: session, path: path, with: url)
                status = "File replaced. Re-sign before installing."
            }
        } catch {
            errorText = error.localizedDescription
            status = "Operation failed."
        }
    }

    private func extract(_ item: IPALabItem) async {
        guard let session else { return }
        busy = true
        defer { busy = false }
        do {
            sharedFile = try await IPALabClient(sync: sync).extract(session: session, item: item)
            status = "Extracted \(item.displayName)."
        } catch {
            errorText = error.localizedDescription
        }
    }

    private func remove(_ item: IPALabItem) async {
        guard let session else { return }
        busy = true
        defer { busy = false }
        do {
            report = try await IPALabClient(sync: sync).remove(session: session, path: item.path)
            status = "Removed \(item.displayName)."
        } catch {
            errorText = error.localizedDescription
        }
    }

    private func export(_ session: String) async {
        busy = true
        defer { busy = false }
        do {
            sharedFile = try await IPALabClient(sync: sync).exportIPA(session: session)
            status = "Modified IPA is ready."
        } catch {
            errorText = error.localizedDescription
        }
    }
}

private struct IPALabItemView: View {
    let item: IPALabItem
    let onExtract: () -> Void
    let onReplace: () -> Void
    let onRemove: () -> Void

    var body: some View {
        List {
            Section("Binary") {
                LabeledContent("Type", value: item.kind)
                LabeledContent("Architecture", value: item.architecture ?? "Unknown")
                LabeledContent("Size", value: ByteCountFormatter.string(fromByteCount: Int64(item.size), countStyle: .file))
                LabeledContent("Path", value: item.path)
            }

            if let rpaths = item.rpaths, !rpaths.isEmpty {
                Section("RPaths") {
                    ForEach(rpaths, id: \.self) { Text($0).font(.system(.footnote, design: .monospaced)) }
                }
            }

            if let loads = item.loadCommands, !loads.isEmpty {
                Section("Loaded Libraries") {
                    ForEach(loads, id: \.self) { Text($0).font(.system(.footnote, design: .monospaced)) }
                }
            }

            Section {
                Button(action: onExtract) { Label("Extract", systemImage: "square.and.arrow.down") }
                Button(action: onReplace) { Label("Replace", systemImage: "arrow.triangle.2.circlepath") }
                if item.kind != "executable" {
                    Button(role: .destructive, action: onRemove) { Label("Remove", systemImage: "trash") }
                }
            }
        }
        .navigationTitle(item.displayName)
        .navigationBarTitleDisplayMode(.inline)
    }
}
