// RuntimeDashboardView.swift — Apple TV control room for the Yaver remote
// runtime. It shows the same machine/runner/voice/dev-server surfaces used by
// CLI, MCP, phone, and web without trying to turn tvOS into a code editor.

import SwiftUI
import UIKit
import CoreImage.CIFilterBuiltins

struct RuntimeDashboardView: View {
    @EnvironmentObject var store: YaverStore

    @State private var info: AgentInfo?
    @State private var status: AgentStatus?
    @State private var voice: VoiceRuntimeStatus?
    @State private var runners: RunnerSessions?
    @State private var runtimeTurns: [RuntimeTurnRow] = []
    @State private var platformMatrix: PlatformMatrixReport?
    @State private var authSession: RunnerAuthSession?
    @State private var gitAuthSession: GitAuthSession?
    @State private var authPollingTask: Task<Void, Never>?
    @State private var authStartingRunner: String?
    /// Set when the agent refused to start a sign-in because the runner is
    /// already signed in AND the user may override by confirming (switching
    /// accounts). Drives the "Sign in anyway" affordance — a refusal with no
    /// way forward is its own dead end.
    @State private var authReauthableRunner: String?
    @State private var gitAuthStartingProvider: String?
    @State private var notice: String?
    @State private var refreshTask: Task<Void, Never>?
    @State private var reloading = false
    /// Drives the runner-auth liveness line so "Started 2m 14s ago" keeps
    /// counting between the 2s status polls instead of sitting still.
    @State private var authTicker = Date()

    private let authClock = Timer.publish(every: 1, on: .main, in: .common).autoconnect()

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 34) {
                header

                RuntimeCard(icon: "rectangle.connected.to.line.below", title: "Live Room", wide: true) {
                    HStack(alignment: .top, spacing: 24) {
                        VStack(alignment: .leading, spacing: 10) {
                            Text("Phone, watch, car, Android remote, and TV all point at the same runtime.")
                                .font(.system(size: 19))
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                            RuntimeRow("Machine", store.selectedBox?.name ?? "Not selected")
                            if let badge = store.machineRolesBadge {
                                RuntimeRow("Roles", badge)
                            }
                            RuntimeRow("Preview", status?.devServer?.running == true ? "Running" : "Waiting")
                            RuntimeRow("Sessions", "\(runners?.count ?? runners?.sessions?.count ?? 0)")
                        }
                        .frame(width: 500, alignment: .leading)

                        VStack(alignment: .leading, spacing: 10) {
                            Text("Latest commands")
                                .font(.system(size: 20, weight: .bold))
                            if runtimeTurns.isEmpty {
                                Text("Start a voice turn from your phone, watch, car, or remote. The TV follows it here.")
                                    .font(.system(size: 18))
                                    .foregroundStyle(.secondary)
                                    .lineLimit(2)
                            } else {
                                ForEach(runtimeTurns.prefix(4)) { row in
                                    RuntimeTurnTile(row: row)
                                }
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }

                HStack(alignment: .top, spacing: 24) {
                    RuntimeCard(icon: "desktopcomputer", title: "Machine") {
                        RuntimeRow("Host", info?.hostname ?? store.selectedBox?.name ?? "Unknown")
                        RuntimeRow("Agent", info?.agentVersion ?? status?.agentVersion ?? "Unknown")
                        RuntimeRow("Platform", [info?.platform, info?.arch].compactMap { $0 }.joined(separator: " / "))
                        RuntimeRow("CPU", info?.cpuPercent.map { "\(Int($0.rounded()))%" } ?? "Unknown")
                    }

                    RuntimeCard(icon: "iphone.gen3.radiowaves.left.and.right", title: "Mobile Runtime") {
                        RuntimeRow("Dev server", status?.devServer?.running == true ? "Running" : "Not running")
                        RuntimeRow("Framework", status?.devServer?.framework ?? "Unknown")
                        RuntimeRow("Port", status?.devServer?.port.map(String.init) ?? "Unknown")
                        RuntimeRow("Tasks", "\(status?.tasks?.running ?? 0) running / \(status?.tasks?.total ?? 0) total")
                    }

                    RuntimeCard(icon: "waveform", title: "Voice") {
                        RuntimeRow("STT", providerLine(voice?.sttProvider, ready: voice?.sttReady))
                        RuntimeRow("TTS", providerLine(voice?.ttsProvider, ready: voice?.ttsReady))
                        RuntimeRow("Enabled", voice?.enabled == true ? "Yes" : "No")
                        RuntimeRow("Project", voice?.defaultProject?.isEmpty == false ? voice!.defaultProject! : "Default")
                    }
                }

                HStack(alignment: .top, spacing: 24) {
                    RuntimeCard(icon: "sparkles", title: "OpenCode") {
                        RuntimeRow("Sessions", "\(runners?.count ?? runners?.sessions?.count ?? 0)")
                        if let sessions = runners?.sessions, !sessions.isEmpty {
                            ForEach(sessions.prefix(3)) { session in
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(session.name)
                                        .font(.system(size: 22, weight: .semibold))
                                    // Runner + attachment only. The old row also
                                    // printed `workDir` — an absolute path, i.e.
                                    // the user's home directory and username, on
                                    // a screen that gets filmed and screen-shared.
                                    Text([session.runner, session.attached == true ? "attached" : nil]
                                        .compactMap { value in
                                            guard let value, !value.isEmpty else { return nil }
                                            return value
                                        }.joined(separator: " · "))
                                    .font(.system(size: 16))
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                                }
                                .padding(.top, 8)
                            }
                        } else {
                            Text("Start OpenCode in tmux on your machine; active sessions appear here.")
                                .font(.system(size: 18))
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                        }
                    }

                    RuntimeCard(icon: "arrow.triangle.2.circlepath", title: "Reload") {
                        Text("Refresh the connected phone, simulator, or emulator after changes from terminal, OpenCode, or mobile.")
                            .font(.system(size: 18))
                            .foregroundStyle(.secondary)
                            .lineLimit(3)
                        HStack(spacing: 16) {
                            Button {
                                Task { await triggerReload(mode: "dev") }
                            } label: {
                                Label("Hot Reload", systemImage: "bolt.fill")
                                    .frame(minWidth: 190)
                            }
                            .disabled(reloading)

                            Button {
                                Task { await triggerReload(mode: "bundle") }
                            } label: {
                                Label("Hermes Push", systemImage: "shippingbox.fill")
                                    .frame(minWidth: 190)
                            }
                            .disabled(reloading)
                        }
                        .padding(.top, 10)
                    }
                }

                RuntimeCard(icon: "qrcode", title: "Git sign-in", wide: true) {
                    HStack(alignment: .center, spacing: 28) {
                        VStack(alignment: .leading, spacing: 14) {
                            Text("Authorize GitHub or GitLab for repository operations. Runner credentials are configured locally on the machine that executes the runner.")
                                .font(.system(size: 18))
                                .foregroundStyle(.secondary)
                                .lineLimit(3)
                            HStack(spacing: 16) {
                                Button {
                                    Task { await startGitAuth("github") }
                                } label: {
                                    Label("GitHub", systemImage: "chevron.left.forwardslash.chevron.right")
                                        .frame(minWidth: 160)
                                }
                                .disabled(authStartingRunner != nil || gitAuthStartingProvider != nil)

                                Button {
                                    Task { await startGitAuth("gitlab") }
                                } label: {
                                    Label("GitLab", systemImage: "arrow.triangle.branch")
                                        .frame(minWidth: 160)
                                }
                                .disabled(authStartingRunner != nil || gitAuthStartingProvider != nil)
                            }

                            if let gitAuthSession {
                                RuntimeRow("Git", gitProviderLabel(gitAuthSession.provider))
                                RuntimeRow("Status", gitAuthSession.state ?? "pending")
                                if let code = gitAuthSession.userCode, !code.isEmpty {
                                    RuntimeRow("Code", code)
                                }
                                if let username = gitAuthSession.username, !username.isEmpty {
                                    RuntimeRow("User", username)
                                }
                            }
                        }
                        .frame(width: 880, alignment: .leading)

                        ZStack {
                            RoundedRectangle(cornerRadius: 18).fill(.white)
                            if let url = gitAuthSession?.verificationURI, let img = qrImage(url) {
                                Image(uiImage: img)
                                    .interpolation(.none)
                                    .resizable()
                                    .frame(width: 220, height: 220)
                            } else {
                                VStack(spacing: 10) {
                                    Image(systemName: "qrcode.viewfinder")
                                        .font(.system(size: 54))
                                        .foregroundStyle(.black.opacity(0.75))
                                    Text(gitAuthStartingProvider == nil ? "Choose Git provider" : "Starting...")
                                        .font(.system(size: 18, weight: .semibold))
                                        .foregroundStyle(.black.opacity(0.75))
                                }
                            }
                        }
                        .frame(width: 270, height: 270)
                    }
                }

                RuntimeCard(icon: "square.grid.2x2", title: "Apple Surfaces", wide: true) {
                    let appleSurfaces = platformMatrix?.surfaces?.filter { $0.family == "apple" } ?? []
                    if appleSurfaces.isEmpty {
                        Text("iPhone, Apple Watch, CarPlay, and Apple TV readiness appears here when the runtime reports its platform matrix.")
                            .font(.system(size: 18))
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                    } else {
                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 300), spacing: 18)], spacing: 18) {
                            ForEach(appleSurfaces) { surface in
                                SurfaceStatusTile(surface: surface)
                            }
                        }
                    }
                }

                if let notice {
                    Text(notice)
                        .font(.system(size: 18, weight: .medium))
                        .foregroundStyle(.orange)
                }
            }
            .padding(56)
        }
        .task { await startLoop() }
        .onReceive(authClock) { now in
            if authSession != nil, !FailureSignals.isRunnerAuthTerminal(authSession?.status) {
                authTicker = now
            }
        }
        .onDisappear {
            refreshTask?.cancel()
            authPollingTask?.cancel()
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Runtime Control")
                .font(.system(size: 52, weight: .heavy))
            Text("Develop mobile, watch, car, and TV from a remote Yaver machine while Apple TV stays on the wall.")
                .font(.system(size: 22))
                .foregroundStyle(.secondary)
        }
    }

    private func providerLine(_ provider: String?, ready: Bool?) -> String {
        let name = provider?.isEmpty == false ? provider! : "Unknown"
        guard let ready else { return name }
        return ready ? "\(name) ready" : "\(name) not ready"
    }

    private func startLoop() async {
        await refresh()
        refreshTask?.cancel()
        refreshTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 4_000_000_000)
                if Task.isCancelled { return }
                await refresh()
            }
        }
    }

    private func refresh() async {
        // Runner/render split: runners, runner auth and git auth live on the
        // RUNNER box. runnerClient() falls back to the selected box when no
        // split is configured, so single-box behavior is unchanged.
        guard let box = store.selectedBox, let client = store.runnerClient() else {
            notice = store.machineSplitActive
                ? "Your AI machine needs the relay to be reachable from this TV."
                : "No runtime machine selected"
            return
        }
        // Each verb catches on its own. A single do/catch around five chained
        // `try await`s meant one failure (e.g. a runner-list decode error) blanked
        // every card after it in the chain — the platform matrix and voice cards
        // would go empty because the sessions fetch threw, not because they
        // failed. Fetch concurrently, then apply each result independently; the
        // core info/status verbs decide the one shared notice.
        async let nextInfo = client.info()
        async let nextStatus = client.status()
        async let nextVoice = client.voiceStatus()
        async let nextRunners = client.runnerSessions()
        async let nextPlatformMatrix = client.platformMatrix()
        // Runner turns live on the machine-roles RUNNER box when a split is
        // active (falls back to the selected box otherwise).
        async let nextRuntimeTurns = SessionClient(token: store.token, box: store.runnerBox() ?? box).runtimeTurns(limit: 8)

        var coreError: String?
        do {
            info = try await nextInfo
            status = try await nextStatus
            // A rotated/expired token reads as 401/403 → sign out, don't sit on a
            // stale dashboard that can never refresh.
            if status?.authExpired == true { store.signOut(); return }
        } catch {
            coreError = error.localizedDescription
            if isAuthFailure(error) { store.signOut(); return }
        }
        voice = (try? await nextVoice) ?? voice
        runners = (try? await nextRunners) ?? runners
        platformMatrix = (try? await nextPlatformMatrix)?.matrix ?? platformMatrix
        runtimeTurns = await nextRuntimeTurns
        notice = coreError
    }

    /// A 401/403 from the agent means the stored token no longer authorizes this
    /// box — the only recovery is to sign in again.
    private func isAuthFailure(_ error: Error) -> Bool {
        let m = error.localizedDescription.lowercased()
        return m.contains("invalid token") || m.contains("missing or invalid authorization")
    }

    private func startRunnerAuth(_ runner: String, confirm: Bool = false) async {
        guard let client = store.runnerClient() else {
            notice = "No runtime machine selected"
            return
        }
        authStartingRunner = runner
        authPollingTask?.cancel()
        defer { authStartingRunner = nil }
        do {
            let result = try await client.startRunnerAuth(runner, confirm: confirm)
            // The agent declined on purpose: this runner is already signed in,
            // and starting a flow would reap a working credential. Say so —
            // "did not return a session" reads as a bug and invites a retry
            // that would do the damage the refusal just prevented.
            if result.action == "noop" {
                notice = result.reason ?? "\(runnerLabel(runner)) is already signed in on this machine."
                authReauthableRunner = (result.reauthable ?? true) ? runner : nil
                return
            }
            authReauthableRunner = nil
            guard let session = result.session else {
                notice = result.reason ?? "Runner auth did not return a session"
                return
            }
            authSession = session
            gitAuthSession = nil
            notice = session.openURL == nil ? "Waiting for \(runnerLabel(session.runner)) OAuth URL..." : "Scan the QR to authorize \(runnerLabel(session.runner))."
            pollRunnerAuth(session.id)
        } catch {
            notice = error.localizedDescription
        }
    }

    private func startGitAuth(_ provider: String) async {
        guard let client = store.runnerClient() else {
            notice = "No runtime machine selected"
            return
        }
        gitAuthStartingProvider = provider
        authPollingTask?.cancel()
        defer { gitAuthStartingProvider = nil }
        do {
            let session = try await client.startGitAuth(provider)
            guard !session.sessionId.isEmpty else {
                notice = "Git auth did not return a session"
                return
            }
            gitAuthSession = session
            authSession = nil
            notice = session.verificationURI == nil ? "Waiting for \(gitProviderLabel(session.provider)) OAuth URL..." : "Scan the QR to authorize \(gitProviderLabel(session.provider))."
            pollGitAuth(session.sessionId)
        } catch {
            notice = error.localizedDescription
        }
    }

    private func pollRunnerAuth(_ sessionId: String) {
        authPollingTask?.cancel()
        authPollingTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                if Task.isCancelled { return }
                guard let client = store.runnerClient() else { return }
                do {
                    let result = try await client.runnerAuthStatus(sessionId: sessionId)
                    if let session = result.session {
                        authSession = session
                        let status = (session.status ?? "").lowercased()
                        if status == "completed" || status == "success" {
                            notice = "\(runnerLabel(session.runner)) is authorized on the remote runtime."
                            return
                        }
                        // account_not_eligible was MISSING from this set, so an
                        // ineligible account polled every 2s forever behind a
                        // stale "pending" row — a spinner over a decided
                        // verdict. FailureSignals owns the terminal set now, so
                        // it cannot drift from the agent's again.
                        if FailureSignals.isRunnerAuthTerminal(status) {
                            notice = FailureSignals.explainRunnerAuthOutcome(
                                status: status,
                                runnerLabel: runnerLabel(session.runner),
                                error: session.error,
                                detail: session.detail
                            ) ?? "\(runnerLabel(session.runner)) auth ended with \(status)."
                            return
                        }
                        // Still running: say how long, and when the CLI last
                        // said anything.
                        if let line = FailureSignals.runnerAuthLivenessLine(
                            now: Date().timeIntervalSince1970 * 1000,
                            startedAt: session.startedAt,
                            lastOutputAt: session.lastOutputAt
                        ) {
                            notice = line
                        }
                    }
                } catch {
                    // A status call that keeps failing used to overwrite the
                    // notice every 2 seconds forever. Report it once and stop —
                    // the session is unobservable from here.
                    notice = "Lost contact with the auth session: \(error.localizedDescription)"
                    return
                }
            }
        }
    }

    private func pollGitAuth(_ sessionId: String) {
        authPollingTask?.cancel()
        authPollingTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                if Task.isCancelled { return }
                guard let client = store.runnerClient() else { return }
                do {
                    let session = try await client.gitAuthStatus(sessionId: sessionId)
                    gitAuthSession = session
                    let state = (session.state ?? "").lowercased()
                    if state == "done" || state == "completed" || state == "success" {
                        notice = "\(gitProviderLabel(session.provider)) is authorized on the remote runtime."
                        return
                    }
                    if state == "error" || state == "expired" || state == "failed" {
                        notice = session.error ?? "\(gitProviderLabel(session.provider)) auth ended with \(state)."
                        return
                    }
                } catch {
                    notice = error.localizedDescription
                }
            }
        }
    }

    private func triggerReload(mode: String) async {
        // Runner/render split: the reload lands on the RENDER box — it holds
        // and serves the app; the runner's push reaches it via pre-build-pull.
        guard let client = store.renderClient() ?? store.client() else { return }
        reloading = true
        defer { reloading = false }
        do {
            let result = try await client.reload(mode: mode)
            // Refresh FIRST — it sets notice=nil on success and would otherwise
            // erase the confirmation the instant it lands. Then say what happened.
            await refresh()
            let target = result.deliveredTo.map { "\($0) device(s)" } ?? result.framework ?? "the runtime"
            notice = mode == "bundle" ? "Hermes bundle pushed to \(target)." : "Hot reload sent to \(target)."
        } catch {
            notice = error.localizedDescription
        }
    }

    private func runnerLabel(_ runner: String?) -> String {
        switch runner?.lowercased() {
        case "claude", "claude-code":
            return "Claude Code"
        case "codex":
            return "Codex"
        case .some(let value) where !value.isEmpty:
            return value
        default:
            return "Runner"
        }
    }

    private func gitProviderLabel(_ provider: String?) -> String {
        switch provider?.lowercased() {
        case "gitlab":
            return "GitLab"
        default:
            return "GitHub"
        }
    }

    private func qrImage(_ string: String) -> UIImage? {
        let context = CIContext()
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 10, y: 10)),
              let cg = context.createCGImage(output, from: output.extent) else { return nil }
        return UIImage(cgImage: cg)
    }
}

private struct RuntimeCard<Content: View>: View {
    let icon: String
    let title: String
    var wide = false
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Image(systemName: icon)
                .font(.system(size: 34, weight: .semibold))
            Text(title)
                .font(.system(size: 28, weight: .bold))
            content
        }
        .frame(width: wide ? 1550 : 500, alignment: .topLeading)
        .frame(minHeight: 230, alignment: .topLeading)
        .padding(24)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 20))
    }
}

private struct SurfaceStatusTile: View {
    let surface: PlatformSurface

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Text(surface.label ?? surface.id)
                    .font(.system(size: 20, weight: .bold))
                    .lineLimit(1)
                Spacer()
                Text(surface.status ?? "unknown")
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(statusColor)
            }
            Text(statusLine)
                .font(.system(size: 16))
                .foregroundStyle(.secondary)
                .lineLimit(2)
        }
        .frame(minHeight: 96, alignment: .topLeading)
        .padding(16)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14))
    }

    private var statusLine: String {
        var parts: [String] = []
        if surface.buildSupported == true {
            parts.append("build")
        }
        if surface.submitSupported == true {
            parts.append("submit")
        }
        if surface.scriptPresent == true, let target = surface.deployTarget, !target.isEmpty {
            parts.append(target)
        }
        if parts.isEmpty {
            return surface.limitations?.first ?? surface.notes?.first ?? "Needs setup"
        }
        return parts.joined(separator: " · ")
    }

    private var statusColor: Color {
        switch surface.status {
        case "ready":
            return .green
        case "build-only", "bundled":
            return .yellow
        case "blocked":
            return .orange
        default:
            return .secondary
        }
    }
}

private struct RuntimeTurnTile: View {
    let row: RuntimeTurnRow

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Circle()
                .fill(color)
                .frame(width: 12, height: 12)
                .padding(.top, 8)
            VStack(alignment: .leading, spacing: 4) {
                Text(row.safeUtterance)
                    .font(.system(size: 18, weight: .semibold))
                    .lineLimit(1)
                Text([row.state, row.testSummary].compactMap { $0 }.joined(separator: " · "))
                    .font(.system(size: 15))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14))
    }

    private var color: Color {
        switch row.state {
        case "ready_to_test", "ready_to_deploy", "done":
            return .green
        case "needs_input":
            return .orange
        case "failed":
            return .red
        case "captured":
            return .blue
        default:
            return .yellow
        }
    }
}

private struct RuntimeRow: View {
    let label: String
    let value: String

    init(_ label: String, _ value: String) {
        self.label = label
        self.value = value.isEmpty ? "Unknown" : value
    }

    var body: some View {
        HStack {
            Text(label)
                .font(.system(size: 18, weight: .medium))
                .foregroundStyle(.secondary)
            Spacer(minLength: 20)
            Text(value)
                .font(.system(size: 18, weight: .semibold))
                .lineLimit(1)
        }
    }
}
