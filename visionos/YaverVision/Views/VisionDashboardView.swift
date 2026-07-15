// VisionDashboardView.swift — spatial runtime control room.
//
// This is not a code editor. It is the headset control surface for a Yaver
// machine: machine health, active project, connected preview devices, runner
// sessions, and deliberate reload controls with honest delivery feedback.

import SwiftUI

struct VisionDashboardView: View {
    @EnvironmentObject var store: YaverStore

    @State private var info: AgentInfo?
    @State private var status: AgentStatus?
    @State private var runners: RunnerSessions?
    @State private var platformMatrix: PlatformMatrixReport?
    @State private var notice: VisionNotice?
    @State private var loading = false
    @State private var reloadingMode: String?
    @State private var showAddBox = false
    @State private var showSession = false

    private let columns = [
        GridItem(.adaptive(minimum: 330, maximum: 520), spacing: 20, alignment: .top)
    ]

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
            .toolbar { toolbar }
            .sheet(isPresented: $showAddBox) { AddBoxView() }
            .sheet(isPresented: $showSession) { VisionSessionView() }
        }
        .task(id: store.selectedBox?.id) { await refresh() }
    }

    // MARK: - Main

    private var dashboard: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                hero

                if let notice {
                    NoticeView(notice: notice)
                }

                LazyVGrid(columns: columns, alignment: .leading, spacing: 20) {
                    machinePanel
                    runtimePanel
                    projectPanel
                    reloadPanel
                    runnersPanel
                    surfacesPanel
                }
            }
            .padding(32)
        }
        .refreshable { await refresh() }
    }

    private var hero: some View {
        HStack(alignment: .center, spacing: 18) {
            ZStack {
                Circle().fill(.blue.opacity(0.18))
                Image(systemName: "visionpro")
                    .font(.system(size: 34, weight: .semibold))
                    .foregroundStyle(.blue)
            }
            .frame(width: 70, height: 70)

            VStack(alignment: .leading, spacing: 4) {
                Text(store.selectedBox?.name ?? "Yaver")
                    .font(.extraLargeTitle2)
                    .lineLimit(1)
                Text(store.selectedBox.map { "\($0.host):\($0.port)" } ?? "No machine selected")
                    .font(.title3)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            if loading {
                ProgressView()
                    .controlSize(.large)
            }
        }
        .padding(24)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 28))
    }

    private var machinePanel: some View {
        panel("Machine", systemImage: "desktopcomputer") {
            row("Host", store.selectedBox.map { "\($0.host):\($0.port)" } ?? "-")
            row("Platform", joined([info?.platform, info?.arch]))
            row("Agent", info?.agentVersion ?? status?.agentVersion ?? "-")
            row("Device", info?.deviceId ?? "-")
            if let cpu = info?.cpuPercent {
                row("CPU", String(format: "%.0f%%", cpu))
            }
        }
    }

    private var runtimePanel: some View {
        panel("Runtime", systemImage: "bolt.horizontal.circle") {
            if status?.authExpired == true {
                Label("Auth expired", systemImage: "xmark.seal.fill")
                    .foregroundStyle(.orange)
            } else {
                Label("Signed in", systemImage: "checkmark.seal.fill")
                    .foregroundStyle(.green)
            }
            row("Tasks", taskLine)
            row("Dev server", devServerLine)
            row("Framework", status?.devServer?.framework ?? "-")
        }
    }

    private var projectPanel: some View {
        panel("Preview Target", systemImage: "iphone.gen3.radiowaves.left.and.right") {
            row("Project", status?.devServer?.project ?? "-")
            row("Work dir", status?.devServer?.workDir ?? "-")
            Text("Hermes Push uses this work dir. If it is empty, start or select a mobile project on the machine before pushing.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var reloadPanel: some View {
        panel("Reload", systemImage: "arrow.triangle.2.circlepath") {
            Text("Hot Reload sends a live reload command. Hermes Push rebuilds bytecode and swaps the guest bundle.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            HStack(spacing: 12) {
                Button {
                    Task { await reload(mode: "dev") }
                } label: {
                    Label("Hot Reload", systemImage: "bolt.fill")
                }
                .disabled(reloadingMode != nil || !hasDevServer)

                Button {
                    Task { await reload(mode: "bundle") }
                } label: {
                    Label("Hermes Push", systemImage: "shippingbox.fill")
                }
                .disabled(reloadingMode != nil || !hasWorkDir)
            }
            .buttonStyle(.borderedProminent)

            if let reloadingMode {
                Label(reloadingMode == "bundle" ? "Building Hermes bundle..." : "Sending reload...", systemImage: "clock")
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var runnersPanel: some View {
        panel("Coding Agents", systemImage: "terminal") {
            let sessions = runners?.sessions ?? []
            if sessions.isEmpty {
                Text("No active runner sessions")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(Array(sessions.prefix(4))) { session in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(session.label)
                            .font(.headline)
                            .lineLimit(1)
                        Text(session.attached == true ? "attached" : "detached")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    .padding(.vertical, 4)
                }
            }

            Button {
                showSession = true
            } label: {
                Label("Open Session", systemImage: "paperplane.fill")
            }
            .padding(.top, 6)
        }
    }

    private var surfacesPanel: some View {
        panel("Apple Surfaces", systemImage: "square.grid.2x2") {
            let surfaces = platformMatrix?.surfaces?.filter { $0.family == "apple" } ?? []
            if surfaces.isEmpty {
                Text("Surface readiness appears after the machine reports its platform matrix.")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(surfaces.prefix(6)) { surface in
                    HStack {
                        Text(surface.label ?? surface.id)
                            .lineLimit(1)
                        Spacer()
                        Text(surface.status ?? "unknown")
                            .font(.caption.bold())
                            .foregroundStyle(surface.status == "ready" ? .green : .secondary)
                    }
                }
            }
        }
    }

    private var noBoxView: some View {
        VStack(spacing: 18) {
            Image(systemName: "visionpro")
                .font(.system(size: 72))
                .foregroundStyle(.secondary)
            Text("Add Your Machine")
                .font(.extraLargeTitle2)
            Text("Enter the LAN address of a machine running `yaver serve`. The headset must be on the same network for this native app.")
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 560)
            Button {
                showAddBox = true
            } label: {
                Label("Add Machine", systemImage: "plus")
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
        }
        .padding(48)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .glassBackgroundEffect()
    }

    // MARK: - Toolbar

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .bottomOrnament) {
            HStack(spacing: 14) {
                if store.selectedBox != nil {
                    Button {
                        Task { await refresh() }
                    } label: {
                        Label("Refresh", systemImage: "arrow.clockwise")
                    }
                    .disabled(loading)

                    Button {
                        showAddBox = true
                    } label: {
                        Label("Machine", systemImage: "server.rack")
                    }
                }

                Button(role: .destructive) {
                    store.signOut()
                } label: {
                    Label("Sign out", systemImage: "rectangle.portrait.and.arrow.right")
                }
            }
        }
    }

    // MARK: - Building Blocks

    private func panel<C: View>(
        _ title: String,
        systemImage: String,
        @ViewBuilder content: () -> C
    ) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Label(title, systemImage: systemImage)
                .font(.title2.bold())
            content()
        }
        .frame(maxWidth: .infinity, minHeight: 210, alignment: .topLeading)
        .padding(22)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 24))
    }

    private func row(_ key: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text(key)
                .foregroundStyle(.secondary)
            Spacer(minLength: 18)
            Text(value.isEmpty ? "-" : value)
                .monospaced()
                .lineLimit(1)
                .truncationMode(.middle)
        }
    }

    private func joined(_ values: [String?]) -> String {
        let parts = values.compactMap { value -> String? in
            guard let value, !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
            return value
        }
        return parts.isEmpty ? "-" : parts.joined(separator: " / ")
    }

    // MARK: - Derived State

    private var hasDevServer: Bool {
        status?.devServer?.running == true
    }

    private var hasWorkDir: Bool {
        !(status?.devServer?.workDir ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private var taskLine: String {
        let running = status?.tasks?.running ?? 0
        let total = status?.tasks?.total ?? 0
        return "\(running) running / \(total) total"
    }

    private var devServerLine: String {
        guard let dev = status?.devServer else { return "unknown" }
        return dev.running == true ? "running" : "stopped"
    }

    // MARK: - Actions

    /// Re-read the machine's state.
    ///
    /// `clearNotice` exists because a reload refreshes immediately afterwards to
    /// pick up the new dev-server state, and a refresh that always cleared the
    /// notice would wipe the very thing the reload just said. That is not
    /// hypothetical: it swallowed every success AND the "nobody received this"
    /// warning within milliseconds of them being set, so the only outcome a human
    /// could ever actually read was a failure (which throws, and so never reached
    /// the refresh). The button looked dead on the happy path for the opposite
    /// reason it looked dead on the sad one.
    ///
    /// A user-initiated refresh (pull-to-refresh, the toolbar button, switching
    /// machine) still clears — there the notice IS stale.
    private func refresh(clearNotice: Bool = true) async {
        guard let client = store.client() else { return }
        loading = true
        defer { loading = false }
        do {
            async let nextInfo = client.info()
            async let nextStatus = client.status()
            async let nextRunners = client.runnerSessions()
            async let nextMatrix = client.platformMatrix()
            info = try await nextInfo
            status = try await nextStatus
            runners = try? await nextRunners
            platformMatrix = try? await nextMatrix.matrix
            if clearNotice {
                notice = nil
            }
        } catch {
            notice = .error("Couldn't reach \(store.selectedBox?.name ?? "the machine"): \(error.localizedDescription)")
        }
    }

    private func reload(mode: String) async {
        guard let client = store.client() else { return }
        reloadingMode = mode
        defer { reloadingMode = nil }
        do {
            let workDir = status?.devServer?.workDir
            let result = try await client.reload(mode: mode, workDir: mode == "bundle" ? workDir : nil)
            if let delivered = result.deliveredTo, delivered == 0 {
                notice = .warning("Reload accepted, but no connected phone, simulator, or preview worker received it. Open Yaver on the target device and select this machine.")
            } else if mode == "bundle" {
                notice = .success("Hermes bundle built and push command sent.")
            } else {
                notice = .success("Hot reload command sent.")
            }
            // Keep what we just told the user; only re-read the machine state.
            await refresh(clearNotice: false)
        } catch {
            notice = .error(error.localizedDescription)
        }
    }
}

private enum VisionNotice {
    case success(String)
    case warning(String)
    case error(String)

    var text: String {
        switch self {
        case .success(let text), .warning(let text), .error(let text): return text
        }
    }

    var icon: String {
        switch self {
        case .success: return "checkmark.circle.fill"
        case .warning: return "exclamationmark.triangle.fill"
        case .error: return "xmark.octagon.fill"
        }
    }

    var color: Color {
        switch self {
        case .success: return .green
        case .warning: return .orange
        case .error: return .red
        }
    }
}

private struct NoticeView: View {
    let notice: VisionNotice

    var body: some View {
        Label(notice.text, systemImage: notice.icon)
            .font(.headline)
            .foregroundStyle(notice.color)
            .padding(18)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 20))
    }
}
