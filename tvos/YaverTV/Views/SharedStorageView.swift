import SwiftUI

/// Lean-back browser/search for the selected agent's shared-storage profiles.
/// All credentials and object operations stay on the agent. This surface uses
/// the same bearer + relay-password request ladder as every other TV feature.
struct SharedStorageView: View {
    @EnvironmentObject private var store: YaverStore
    @State private var profiles: [SharedStorageProfileSummary] = []
    @State private var selectedProfileId = ""
    @State private var path = ""
    @State private var query = ""
    @State private var entries: [SharedStorageEntrySummary] = []
    @State private var hits: [SharedStorageSearchHitSummary] = []
    @State private var loading = false
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 22) {
            HStack {
                VStack(alignment: .leading, spacing: 5) {
                    Text("Shared storage").font(.system(size: 38, weight: .bold))
                    Text("Browse and search NAS, SMB, WebDAV, Storage Box, and S3-compatible objects.")
                        .font(.system(size: 18)).foregroundStyle(.secondary)
                }
                Spacer()
                Button { Task { await loadProfiles() } } label: { Label("Refresh", systemImage: "arrow.clockwise") }
            }

            if let error {
                Text(error).foregroundStyle(.red).font(.system(size: 17, design: .monospaced))
            }

            if profiles.isEmpty && !loading {
                Text("No shared-storage profiles are configured on this machine.")
                    .font(.system(size: 22)).foregroundStyle(.secondary)
            } else {
                ScrollView(.horizontal) {
                    HStack(spacing: 14) {
                        ForEach(profiles) { profile in
                            Button {
                                selectedProfileId = profile.id
                                path = ""
                                query = ""
                                hits = []
                                Task { await browse() }
                            } label: {
                                VStack(alignment: .leading, spacing: 3) {
                                    Text(profile.name).font(.headline)
                                    Text(profile.type.uppercased()).font(.caption).foregroundStyle(.secondary)
                                }.frame(minWidth: 190, alignment: .leading)
                            }
                            .buttonStyle(.bordered)
                            .tint(profile.id == selectedProfileId ? .accentColor : .secondary)
                            .disabled(!profile.available || !profile.supportsBrowse)
                        }
                    }
                }

                HStack(spacing: 14) {
                    TextField("Search object names or text", text: $query)
                        .textFieldStyle(.plain)
                        .padding(14)
                        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 12))
                        .onSubmit { Task { await search() } }
                    Button("Search") { Task { await search() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || selectedProfileId.isEmpty)
                    if !path.isEmpty {
                        Button("Up") {
                            path = path.split(separator: "/").dropLast().joined(separator: "/")
                            hits = []
                            Task { await browse() }
                        }
                    }
                }

                Text(path.isEmpty ? "/" : "/\(path)")
                    .font(.system(size: 16, design: .monospaced)).foregroundStyle(.secondary)

                if loading {
                    ProgressView("Loading…")
                } else {
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 10) {
                            if !hits.isEmpty {
                                ForEach(hits) { hit in
                                    row(icon: "magnifyingglass", title: hit.path,
                                        detail: [hit.matchType, hit.snippet].compactMap { $0 }.joined(separator: " · "))
                                }
                            } else {
                                ForEach(entries) { entry in
                                    Button {
                                        guard entry.isDir else { return }
                                        path = entry.path
                                        Task { await browse() }
                                    } label: {
                                        row(icon: entry.isDir ? "folder.fill" : "doc.fill", title: entry.name,
                                            detail: entry.isDir ? entry.path : byteLabel(entry.size))
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                        }
                    }
                }
            }
        }
        .padding(54)
        .task { await loadProfiles() }
    }

    private func row(icon: String, title: String, detail: String) -> some View {
        HStack(spacing: 18) {
            Image(systemName: icon).font(.system(size: 24)).frame(width: 34)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.system(size: 21, weight: .semibold)).lineLimit(1)
                if !detail.isEmpty { Text(detail).font(.system(size: 15)).foregroundStyle(.secondary).lineLimit(2) }
            }
            Spacer()
        }
        .padding(14)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 12))
    }

    @MainActor private func loadProfiles() async {
        guard let client = store.client() else { error = "Choose a reachable machine first."; return }
        loading = true
        defer { loading = false }
        do {
            profiles = try await client.sharedStorageProfiles()
            if selectedProfileId.isEmpty { selectedProfileId = profiles.first(where: { $0.available && $0.supportsBrowse })?.id ?? "" }
            error = nil
            if !selectedProfileId.isEmpty { await browse(using: client) }
        } catch { self.error = error.localizedDescription }
    }

    @MainActor private func browse(using supplied: AgentClient? = nil) async {
        guard let client = supplied ?? store.client(), !selectedProfileId.isEmpty else { return }
        loading = true
        defer { loading = false }
        do {
            entries = try await client.sharedStorageEntries(profileId: selectedProfileId, path: path)
            hits = []
            error = nil
        } catch { self.error = error.localizedDescription }
    }

    @MainActor private func search() async {
        let term = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let client = store.client(), !selectedProfileId.isEmpty, !term.isEmpty else { return }
        loading = true
        defer { loading = false }
        do {
            hits = try await client.searchSharedStorage(profileId: selectedProfileId, query: term, path: path)
            error = nil
        } catch { self.error = error.localizedDescription }
    }

    private func byteLabel(_ bytes: Int64) -> String { ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file) }
}
