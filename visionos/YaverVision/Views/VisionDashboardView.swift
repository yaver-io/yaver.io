// VisionDashboardView.swift — the spatial runtime control room.
//
// Scope mirrors tvOS deliberately (see tvos/README.md): this is a lean-back
// control surface, NOT an editor. Machine status, dev-server state, runner
// sessions, and a reload trigger. Dense code authoring and raw logs stay on the
// machines where they belong — a headset is a terrible place to read a stack
// trace, and pretending otherwise is how you ship a surface nobody opens twice.
//
// Every call goes through the SAME AgentClient/ops verbs the TV and phone use,
// so there is no visionOS-specific backend to keep in sync.

import SwiftUI

struct VisionDashboardView: View {
    @EnvironmentObject var store: YaverStore

    @State private var info: AgentInfo?
    @State private var status: AgentStatus?
    @State private var runners: RunnerSessions?
    @State private var error: String?
    @State private var loading = false

    var body: some View {
        NavigationStack {
            Group {
                if store.selectedBox == nil {
                    noBoxView
                } else {
                    dashboard
                }
            }
            .navigationTitle("Yaver")
            .toolbar {
                ToolbarItem(placement: .bottomOrnament) {
                    HStack(spacing: 16) {
                        Button {
                            Task { await refresh() }
                        } label: {
                            Label("Refresh", systemImage: "arrow.clockwise")
                        }
                        .disabled(loading || store.selectedBox == nil)

                        Button {
                            Task { await reload() }
                        } label: {
                            Label("Hot reload", systemImage: "bolt.fill")
                        }
                        .disabled(store.selectedBox == nil)

                        Button(role: .destructive) {
                            store.signOut()
                        } label: {
                            Label("Sign out", systemImage: "rectangle.portrait.and.arrow.right")
                        }
                    }
                }
            }
        }
        .task { await refresh() }
    }

    // MARK: - Panels

    private var dashboard: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                if let error {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .padding()
                        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
                }

                machinePanel
                runtimePanel
                runnersPanel
            }
            .padding(32)
        }
    }

    private var machinePanel: some View {
        panel("Machine", systemImage: "desktopcomputer") {
            row("Name", store.selectedBox?.name ?? "—")
            row("Host", "\(store.selectedBox?.host ?? "—"):\(store.selectedBox?.port ?? 0)")
            row("Platform", [info?.platform, info?.arch].compactMap { $0 }.joined(separator: " · "))
            row("Agent", info?.agentVersion ?? status?.agentVersion ?? "—")
            if let cpu = info?.cpuPercent {
                row("CPU", String(format: "%.0f%%", cpu))
            }
        }
    }

    private var runtimePanel: some View {
        panel("Runtime", systemImage: "bolt.horizontal.circle") {
            // authExpired is the one that actually strands you: the box answers,
            // but every verb 401s. Surface it as a failure, not a footnote.
            if status?.authExpired == true {
                Label("Auth expired — re-run `yaver auth` on the box", systemImage: "xmark.seal.fill")
                    .foregroundStyle(.orange)
            } else {
                Label("Signed in", systemImage: "checkmark.seal.fill")
                    .foregroundStyle(.green)
            }
            if let t = status?.tasks?.total {
                row("Tasks", "\(t)")
            }
            if let dev = status?.devServer {
                row("Dev server", dev.running == true ? "running" : "stopped")
            }
        }
    }

    private var runnersPanel: some View {
        panel("Coding agents", systemImage: "cpu") {
            let sessions = runners?.sessions ?? []
            if sessions.isEmpty {
                Text("No active runner sessions")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(sessions) { s in
                    row(s.id, "active")
                }
            }
        }
    }

    private var noBoxView: some View {
        VStack(spacing: 16) {
            Image(systemName: "desktopcomputer.trianglebadge.exclamationmark")
                .font(.system(size: 64))
                .foregroundStyle(.secondary)
            Text("No machine selected")
                .font(.title)
            Text("Pick a box in the Yaver phone app — it syncs here.")
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Building blocks

    private func panel<C: View>(
        _ title: String,
        systemImage: String,
        @ViewBuilder content: () -> C
    ) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Label(title, systemImage: systemImage)
                .font(.title2).bold()
            content()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(24)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 24))
    }

    private func row(_ k: String, _ v: String) -> some View {
        HStack {
            Text(k).foregroundStyle(.secondary)
            Spacer()
            Text(v.isEmpty ? "—" : v).monospaced()
        }
    }

    // MARK: - Actions

    private func refresh() async {
        guard let client = store.client() else { return }
        loading = true
        defer { loading = false }
        error = nil
        do {
            info = try await client.info()
            status = try await client.status()
            runners = try? await client.runnerSessions()
        } catch {
            // A headset that silently shows stale numbers is worse than one that
            // admits it lost the box.
            self.error = "Couldn't reach \(store.selectedBox?.name ?? "the machine"): \(error.localizedDescription)"
        }
    }

    private func reload() async {
        guard let client = store.client() else { return }
        do {
            _ = try await client.call("reload")
        } catch {
            self.error = error.localizedDescription
        }
    }
}
