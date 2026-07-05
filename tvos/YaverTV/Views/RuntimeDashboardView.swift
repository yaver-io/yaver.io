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
    @State private var platformMatrix: PlatformMatrixReport?
    @State private var authSession: RunnerAuthSession?
    @State private var authPollingTask: Task<Void, Never>?
    @State private var authStartingRunner: String?
    @State private var notice: String?
    @State private var refreshTask: Task<Void, Never>?
    @State private var reloading = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 34) {
                header

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
                    RuntimeCard(icon: "sparkles", title: "Claude / Codex") {
                        RuntimeRow("Sessions", "\(runners?.count ?? runners?.sessions?.count ?? 0)")
                        if let sessions = runners?.sessions, !sessions.isEmpty {
                            ForEach(sessions.prefix(3)) { session in
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(session.title?.isEmpty == false ? session.title! : session.agent ?? "Agent session")
                                        .font(.system(size: 22, weight: .semibold))
                                    Text([session.agent, session.status, session.workDir].compactMap { value in
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
                            Text("Start Claude Code or Codex on your MacBook; active sessions appear here.")
                                .font(.system(size: 18))
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                        }
                    }

                    RuntimeCard(icon: "arrow.triangle.2.circlepath", title: "Reload") {
                        Text("Refresh the connected phone, simulator, or emulator after changes from terminal, Claude Code, Codex, or mobile.")
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

                RuntimeCard(icon: "qrcode", title: "OAuth QR", wide: true) {
                    HStack(alignment: .center, spacing: 28) {
                        VStack(alignment: .leading, spacing: 14) {
                            Text("Start remote-runtime auth on the selected machine, then scan the QR with your phone camera. Claude Code, Codex, and Yaver stay on their normal browser/device-code paths.")
                                .font(.system(size: 18))
                                .foregroundStyle(.secondary)
                                .lineLimit(3)
                            HStack(spacing: 16) {
                                Button {
                                    Task { await startRunnerAuth("claude") }
                                } label: {
                                    Label("Claude Code", systemImage: "sparkles")
                                        .frame(minWidth: 210)
                                }
                                .disabled(authStartingRunner != nil)

                                Button {
                                    Task { await startRunnerAuth("codex") }
                                } label: {
                                    Label("Codex", systemImage: "terminal")
                                        .frame(minWidth: 160)
                                }
                                .disabled(authStartingRunner != nil)
                            }

                            if let authSession {
                                RuntimeRow("Runner", runnerLabel(authSession.runner))
                                RuntimeRow("Status", authSession.status ?? "pending")
                                if let code = authSession.code, !code.isEmpty {
                                    RuntimeRow("Code", code)
                                }
                                if let detail = authSession.detail, !detail.isEmpty {
                                    Text(detail)
                                        .font(.system(size: 16))
                                        .foregroundStyle(.secondary)
                                        .lineLimit(2)
                                }
                            }
                        }
                        .frame(width: 880, alignment: .leading)

                        ZStack {
                            RoundedRectangle(cornerRadius: 18).fill(.white)
                            if let url = authSession?.openURL, let img = qrImage(url) {
                                Image(uiImage: img)
                                    .interpolation(.none)
                                    .resizable()
                                    .frame(width: 220, height: 220)
                            } else {
                                VStack(spacing: 10) {
                                    Image(systemName: "qrcode.viewfinder")
                                        .font(.system(size: 54))
                                        .foregroundStyle(.black.opacity(0.75))
                                    Text(authStartingRunner == nil ? "Choose a runner" : "Starting...")
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
        guard let client = store.client() else {
            notice = "No runtime machine selected"
            return
        }
        do {
            async let nextInfo = client.info()
            async let nextStatus = client.status()
            async let nextVoice = client.voiceStatus()
            async let nextRunners = client.runnerSessions()
            async let nextPlatformMatrix = client.platformMatrix()
            info = try await nextInfo
            status = try await nextStatus
            voice = try await nextVoice
            runners = try await nextRunners
            platformMatrix = try await nextPlatformMatrix.matrix
            notice = nil
        } catch {
            notice = error.localizedDescription
        }
    }

    private func startRunnerAuth(_ runner: String) async {
        guard let client = store.client() else {
            notice = "No runtime machine selected"
            return
        }
        authStartingRunner = runner
        authPollingTask?.cancel()
        defer { authStartingRunner = nil }
        do {
            let result = try await client.startRunnerAuth(runner)
            guard let session = result.session else {
                notice = "Runner auth did not return a session"
                return
            }
            authSession = session
            notice = session.openURL == nil ? "Waiting for \(runnerLabel(session.runner)) OAuth URL..." : "Scan the QR to authorize \(runnerLabel(session.runner))."
            pollRunnerAuth(session.id)
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
                guard let client = store.client() else { return }
                do {
                    let result = try await client.runnerAuthStatus(sessionId: sessionId)
                    if let session = result.session {
                        authSession = session
                        let status = (session.status ?? "").lowercased()
                        if status == "completed" || status == "success" {
                            notice = "\(runnerLabel(session.runner)) is authorized on the remote runtime."
                            return
                        }
                        if status == "cancelled" || status == "canceled" || status == "error" || status == "failed" {
                            notice = session.error ?? "\(runnerLabel(session.runner)) auth ended with \(status)."
                            return
                        }
                    }
                } catch {
                    notice = error.localizedDescription
                }
            }
        }
    }

    private func triggerReload(mode: String) async {
        guard let client = store.client() else { return }
        reloading = true
        defer { reloading = false }
        do {
            let result = try await client.reload(mode: mode)
            let target = result.deliveredTo.map { "\($0) device(s)" } ?? result.framework ?? "runtime"
            notice = mode == "bundle" ? "Hermes push requested for \(target)." : "Hot reload requested for \(target)."
            await refresh()
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
