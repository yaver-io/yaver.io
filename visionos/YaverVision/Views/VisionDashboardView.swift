// VisionDashboardView.swift — spatial runtime control room.
//
// This is not a code editor. It is the headset control surface for a Yaver
// machine: machine health, active project, connected preview devices, tasks,
// and deliberate reload controls with honest delivery feedback.

import SwiftUI

struct VisionDashboardView: View {
    @EnvironmentObject var store: YaverStore

    @State private var info: AgentInfo?
    @State private var status: AgentStatus?
    @State private var tasks: [TaskSummary] = []
    @State private var platformMatrix: PlatformMatrixReport?
    @State private var notice: VisionNotice?
    @State private var loading = false
    @State private var reloadingMode: String?
    @State private var showAddBox = false
    @State private var showCodingPreferences = false
    @State private var confirmRemoval = false
    @State private var removing = false

    /// Open the headset directly on a screen instead of the dashboard.
    ///
    /// Same key and same values as tvOS (`yaver.tv.startAt`) ON PURPOSE: both
    /// surfaces read it from UserDefaults, both are driven by the same closed
    /// loop, and one vocabulary is one thing to learn. Values: "" (normal),
    /// "projects", "preview:<projectName>".
    ///
    /// The tvOS half documents why this exists rather than walking the UI —
    /// short version, its tile grid is width-adaptive so no press count is
    /// stable. A headset has the same problem for a stronger reason: there is
    /// no remote at all, and driving gaze-and-pinch from a test is not a thing
    /// XCUITest can do reliably. Routing by name is the honest mechanism, and
    /// it is also what a user wants — "put my project in front of me".
    @AppStorage("yaver.tv.startAt") private var startAt: String = ""
    @State private var routedFromStartAt = false

    /// Both "projects" and "preview:<name>" enter through Projects. Matching by
    /// EQUALITY here was a real regression on tvOS: adding the deeper value
    /// silently stopped the first hop and the app just sat on its dashboard.
    private var routesToProjects: Bool {
        startAt == "projects" || startAt.hasPrefix("preview:")
    }
    @State private var showSession = false
    @State private var logTask: Task<Void, Never>?
    @State private var devLog: [String] = []

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
            // Programmatic route from `yaver.tv.startAt` — see the property.
            // Guarded so it fires once and only with a box selected: routing
            // into Projects with no machine renders an empty screen, which
            // reads as a broken deep link rather than "pick a box first".
            .navigationDestination(isPresented: $routedFromStartAt) { ProjectsView() }
            .onChange(of: store.selectedBox?.id) { _, id in
                guard id != nil, routesToProjects, !routedFromStartAt else { return }
                routedFromStartAt = true
            }
            .onAppear {
                guard store.selectedBox != nil, routesToProjects, !routedFromStartAt else { return }
                routedFromStartAt = true
            }
            .sheet(isPresented: $showAddBox) { AddBoxView() }
            .sheet(isPresented: $showCodingPreferences) { VisionCodingPreferencesView() }
            .sheet(isPresented: $showSession) { VisionSessionView() }
            .confirmationDialog("Remove this machine from Yaver?", isPresented: $confirmRemoval) {
                Button("Remove", role: .destructive) { Task { await removeSelectedMachine() } }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("BYO and local machines disappear from every surface immediately. Yaver-hosted boxes are fully decommissioned with no snapshot.")
            }
        }
        .task(id: store.selectedBox?.id) { await refresh() }
        .task(id: store.selectedBox?.id) { await startDevEventStream() }
        .onDisappear { logTask?.cancel() }
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
                    logsPanel
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
            Button(role: .destructive) {
                confirmRemoval = true
            } label: {
                Label("Remove from Yaver", systemImage: "trash")
            }
            .disabled(removing || info?.deviceId == nil)
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
        panel("Tasks", systemImage: "bubble.left.and.bubble.right") {
            if tasks.isEmpty {
                Text("No active tasks")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(Array(tasks.prefix(4))) { task in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(task.safeTitle)
                            .font(.headline)
                            .lineLimit(1)
                        Text((task.status ?? "task").capitalized)
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
                Label("Open Tasks", systemImage: "paperplane.fill")
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

    private var logsPanel: some View {
        panel("Render Logs", systemImage: "text.alignleft") {
            if devLog.isEmpty {
                Text("Metro, Expo, Flutter, and web-preview logs appear here while the selected machine starts or reloads a project.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                ForEach(Array(devLog.suffix(8).enumerated()), id: \.offset) { _, line in
                    Text(line)
                        .font(.system(size: 13, design: .monospaced))
                        .foregroundStyle(logColor(for: line))
                        .lineLimit(1)
                        .truncationMode(.middle)
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
            NavigationLink {
                BoxlessCodeView()
            } label: {
                Label("Use boxless Yaver Code", systemImage: "sparkles")
            }
            .buttonStyle(.bordered)
            Text("Chat and deep audit work here with your DeepSeek key. Git edits, builds, simulators, and rendering require a connected remote runner.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 560)
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

                    Button {
                        showCodingPreferences = true
                    } label: {
                        Label("Coding", systemImage: "sparkles")
                    }
                }

                Button {
                    Task { await toggleAppearance() }
                } label: {
                    Label(
                        store.appearanceTheme == "light" ? "Dark" : "Light",
                        systemImage: store.appearanceTheme == "light" ? "moon.fill" : "sun.max.fill"
                    )
                }
                .accessibilityLabel(
                    store.appearanceTheme == "light"
                        ? "Switch visionOS to dark appearance"
                        : "Switch visionOS to light appearance"
                )

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

    private func toggleAppearance() async {
        let next = store.appearanceTheme == "light" ? "dark" : "light"
        do {
            try await store.setAppearanceTheme(next)
            notice = .success("Appearance saved for this Vision Pro.")
        } catch {
            notice = .error("Couldn't save appearance: \(error.localizedDescription)")
        }
    }

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
            async let nextTasks = client.listTasks()
            async let nextMatrix = client.platformMatrix()
            info = try await nextInfo
            status = try await nextStatus
            tasks = (try? await nextTasks) ?? []
            platformMatrix = try? await nextMatrix.matrix
            if clearNotice {
                notice = nil
            }
        } catch {
            if store.handleAuthenticationFailure(error) { return }
            notice = .error("Couldn't reach \(store.selectedBox?.name ?? "the machine"): \(error.localizedDescription)")
        }
    }

    private func removeSelectedMachine() async {
        guard let deviceId = info?.deviceId,
              let selected = store.selectedBox else { return }
        removing = true
        defer { removing = false }
        do {
            let devices = try await MachineRegistry.fetch(token: store.token)
            guard let device = devices.first(where: { $0.deviceId == deviceId }) else {
                throw AgentError(message: "This machine is no longer in your Yaver account.")
            }
            if device.hosting == "yaver-hosted" {
                guard let machineId = device.machineId, !machineId.isEmpty else {
                    throw AgentError(message: "This cloud box is missing its provider identity. Open Cloud Workspace to decommission it.")
                }
                try await MachineRegistry.decommissionCloudMachine(machineId: machineId, token: store.token)
            } else {
                // A vision-scoped token may remove the account row, but not
                // destroy the local agent through the companion API.
                try await MachineRegistry.removeDevice(deviceId: device.deviceId, token: store.token)
            }
            store.removeBox(selected)
        } catch {
            if store.handleAuthenticationFailure(error) { return }
            notice = .error(error.localizedDescription)
        }
    }

    private func startDevEventStream() async {
        logTask?.cancel()
        devLog = []
        guard let client = store.client() else { return }
        let stream = await client.subscribeDevEvents { ev in
            let line = ev.logLine ?? ev.message
            guard let line, !line.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
            Task { @MainActor in appendLog(line) }
        } onError: { message in
            Task { @MainActor in appendLog("[stream] \(message)") }
        }
        logTask = stream
        await stream.value
    }

    @MainActor
    private func appendLog(_ line: String) {
        devLog.append(line)
        if devLog.count > 60 {
            devLog.removeFirst(devLog.count - 60)
        }
    }

    private func logColor(for line: String) -> Color {
        let lower = line.lowercased()
        if lower.contains("error") || lower.contains("failed") || lower.contains("exception") || lower.contains("cannot ") {
            return .red
        }
        if lower.contains("warning") || lower.contains("warn") || lower.contains("deprecated") || lower.contains("expected version") {
            return .orange
        }
        if lower.contains("ready") || lower.contains("listening") || lower.contains("bundled") || lower.contains("waiting on") {
            return .blue
        }
        return .secondary
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
