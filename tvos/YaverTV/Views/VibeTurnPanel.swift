// VibeTurnPanel.swift — the vibe loop ON the preview screen.
//
// This is what makes the TV a vibing surface instead of a monitor: the same
// screen that streams the app also takes the next prompt. Web gets this with
// a chat pane beside an iframe (RuntimeLabView); the TV gets a focusable
// overlay — press Vibe, dictate or type into the tvOS keyboard (Siri Remote
// dictation types into a TextField for free), and the turn goes to the
// RUNNER box while the preview keeps polling underneath. HMR lands in the
// frame stream on its own; nothing re-mounts, nothing blanks.
//
// Turn plumbing is the existing SessionClient (/runner/session/turn) — the
// endpoint that drives the session the user already has running, with the
// same pane + options + awaitingChoice contract SessionView renders. Options
// come back as focusable buttons, so "runner asks, user picks" works from
// the couch without ever leaving the preview.
//
// Role rule: the turn client is built from store.runnerBox() — NEVER the
// selected box. In a runner/render split the selected box may be the render
// machine, and a prompt sent there lands on a box with no runner sessions.
//
// Project + MCP picker (2026-08-10): the TV remembers the last project and
// MCP selection the SAME way mobile and the web chat do — Convex
// defaultRuntimeProjectByDevice / mcpServersByDevice, synced here via
// YaverStore.rememberProject / rememberMCPServers. Picking a project on the
// phone then vibing on the TV keeps the same workDir; the picker shows the
// runner box's /projects and highlights the remembered one.

import SwiftUI

struct VibeTurnPanel: View {
    @EnvironmentObject var store: YaverStore

    /// The project being previewed (the vibe turn runs in this repo's workDir).
    let project: ProjectSummary?
    /// External one-shot prompt seed (the DOM-mode "Deep audit this element"
    /// button). When the binding becomes non-empty the panel fills the prompt,
    /// expands, and SENDS — one tap from selection to runner turn. The value is
    /// cleared so a repeated tap re-fires. Defaults to a constant so existing
    /// call sites (visionOS, ProjectsView previews) compile unchanged.
    @Binding var prefill: String

    init(project: ProjectSummary?, prefill: Binding<String> = .constant("")) {
        self.project = project
        self._prefill = prefill
    }

    @State private var expanded = false
    @State private var prompt = ""
    @State private var sending = false
    @State private var turn: SessionTurnResult?
    @State private var turnError: String?
    // Project/MCP picker state — loaded from the runner box on first open.
    @State private var showProjectPicker = false
    @State private var availableProjects: [ProjectSummary] = []
    @State private var pickedProjectPath: String?
    @State private var availableMCPServers: [String] = []
    @State private var pickedMCPServers: Set<String> = []
    @State private var yaverMcpOn = true

    /// Focus the prompt the moment the panel expands so the Siri Remote's mic
    /// button dictates straight into it — the only speech input a TV has.
    @FocusState private var promptFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let turnError {
                Text(turnError)
                    .font(.system(size: 15))
                    .foregroundStyle(.orange)
                    .lineLimit(2)
            }

            if let turn {
                turnStatus(turn)
            }

            if expanded {
                HStack(spacing: 12) {
                    TextField("What should change?", text: $prompt)
                        .textFieldStyle(.plain)
                        .font(.system(size: 20))
                        .frame(maxWidth: 700)
                        .focused($promptFocused)
                        .onSubmit { send() }
                    Button(sending ? "Sending…" : "Send") { send() }
                        .disabled(sending || prompt.trimmingCharacters(in: .whitespaces).isEmpty)
                    Button("Close") { expanded = false }
                }
                if promptFocused {
                    // YouTube-style listening cue while the prompt holds focus —
                    // replaces the static hint so the cue is the only voice
                    // guidance on screen.
                    HStack(spacing: 10) {
                        MicListeningIndicator(color: .blue)
                        Text("Mic ready — one press of the Siri Remote mic dictates.")
                            .font(.system(size: 13)).foregroundStyle(.secondary)
                    }
                    .transition(.opacity)
                } else {
                    Text("Press the mic button on the Siri Remote to dictate — the prompt is focused.")
                        .font(.system(size: 13))
                        .foregroundStyle(.secondary)
                }
                HStack(spacing: 10) {
                    projectChip
                    mcpChip
                }
                .padding(.top, 4)
            } else {
                Button {
                    expanded = true
                    // Focus follows the expansion so a second mic press dictates.
                    promptFocused = true
                } label: {
                    Label(turn == nil ? "Vibe — ask for a change" : "Ask for another change",
                          systemImage: "wand.and.stars")
                        .font(.system(size: 17, weight: .semibold))
                }
            }
        }
        .padding(16)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 18))
        .task { await loadPickerState() }
        // One-tap deep-audit from DOM mode: seed the prompt, expand, and send
        // immediately (the agent's per-turn hook prepends the selected
        // element's block to the turn — the runner gets the element, not a
        // grep request). Clearing the binding lets the same button re-fire.
        .onChange(of: prefill) { _, value in
            let text = value.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else { return }
            prefill = ""
            prompt = text
            expanded = true
            send()
        }
    }

    // ── Project / MCP picker ──────────────────────────────────────────────

    private var runnerBoxId: String? { store.runnerBox()?.id }

    /// Seed the picker from the runner box's /projects + the Convex-remembered
    /// selection (same rows the phone/web write). Runs once per panel mount.
    private func loadPickerState() async {
        guard let boxId = runnerBoxId else { return }
        let prefs = store.lastProjectByDevice[boxId]
        // Start from the remembered Convex choice so a phone-picked project
        // shows up selected on the TV without any TV-side tap.
        if let prefName = prefs?.projectName, let proj = project, proj.name == prefName {
            pickedProjectPath = proj.path
        } else if let proj = project {
            pickedProjectPath = proj.path
        }
        if let mcpPref = store.lastMCPServersByDevice[boxId] {
            yaverMcpOn = mcpPref.includeYaverMcp ?? true
            pickedMCPServers = Set(mcpPref.mcpServers ?? [])
        }
        guard let client = store.runnerClient() else { return }
        // Projects + MCP server names from the RUNNER box — the machine whose
        // repo the AI edits (same list ProjectsView shows).
        if let list = try? await client.listProjects() {
            availableProjects = list
        }
        if let servers = try? await client.listMCPServers() {
            availableMCPServers = servers.map(\.name)
        }
    }

    /// "Project: <name> ▾" — opens a dpad-friendly list of the runner box's
    /// repos. Picking one remembers it to Convex (store.rememberProject).
    private var projectChip: some View {
        Menu {
            ForEach(availableProjects) { p in
                Button {
                    pickedProjectPath = p.path
                    if let boxId = runnerBoxId {
                        store.rememberProject(p, for: boxId)
                    }
                } label: {
                    if p.path == pickedProjectPath {
                        Label(p.name, systemImage: "checkmark")
                    } else {
                        Text(p.name)
                    }
                }
            }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "folder")
                Text(currentProjectLabel)
            }
            .font(.system(size: 15, weight: .semibold))
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(.ultraThinMaterial, in: Capsule())
        }
        .disabled(availableProjects.isEmpty)
    }

    private var currentProjectLabel: String {
        if let path = pickedProjectPath {
            return availableProjects.first(where: { $0.path == path })?.name
                ?? path.split(separator: "/").last.map(String.init)
                ?? path
        }
        return project?.name ?? "Project ▾"
    }

    /// "yaver · 2 MCP ▾" — toggles the yaver doorway (default ON) and the
    /// box's external MCP servers; selection syncs to Convex on change.
    private var mcpChip: some View {
        Menu {
            Button {
                yaverMcpOn.toggle()
                persistMCP()
            } label: {
                if yaverMcpOn {
                    Label("yaver (on)", systemImage: "checkmark")
                } else {
                    Text("yaver (off)")
                }
            }
            if !availableMCPServers.isEmpty {
                Divider()
                ForEach(availableMCPServers, id: \.self) { name in
                    Button {
                        if pickedMCPServers.contains(name) {
                            pickedMCPServers.remove(name)
                        } else {
                            pickedMCPServers.insert(name)
                        }
                        persistMCP()
                    } label: {
                        if pickedMCPServers.contains(name) {
                            Label(name, systemImage: "checkmark")
                        } else {
                            Text(name)
                        }
                    }
                }
            }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "platter.2.filled.ipad")
                Text(yaverMcpOn ? "yaver" : "yaver (off)")
                if !pickedMCPServers.isEmpty {
                    Text("· \(pickedMCPServers.count) MCP")
                }
            }
            .font(.system(size: 15, weight: .semibold))
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(.ultraThinMaterial, in: Capsule())
        }
    }

    private func persistMCP() {
        guard let boxId = runnerBoxId else { return }
        store.rememberMCPServers(Array(pickedMCPServers), includeYaverMcp: yaverMcpOn, for: boxId)
    }

    /// The turn's life, narrated in place: what was sent, what the runner is
    /// showing, and — when it asks — the choices as focusable buttons.
    @ViewBuilder
    private func turnStatus(_ turn: SessionTurnResult) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            if sending {
                HStack(spacing: 10) {
                    EqualizerBars(barCount: 5, color: .blue, active: true)
                    Text("Working on \(currentProjectLabel)…").foregroundStyle(.secondary)
                }
                .font(.system(size: 16))
            }
            if let pane = turn.pane, !pane.isEmpty {
                // The last few pane lines, home-paths redacted — a TV is a
                // shared-room surface; never print a username on it.
                Text(redactHomePaths(paneTail(pane)))
                    .font(.system(size: 13, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
                    .frame(maxWidth: 900, alignment: .leading)
            }
            if turn.awaitingChoice == true, let options = turn.options, !options.isEmpty {
                Text("The runner is asking:")
                    .font(.system(size: 15, weight: .semibold))
                HStack(spacing: 10) {
                    ForEach(options.prefix(4), id: \.self) { option in
                        Button(option) { choose(option) }
                            .font(.system(size: 15))
                            .disabled(sending)
                    }
                }
            }
        }
    }

    private func paneTail(_ pane: String, lines: Int = 3) -> String {
        pane.split(separator: "\n", omittingEmptySubsequences: true)
            .suffix(lines)
            .joined(separator: "\n")
    }

    private func client() -> SessionClient? {
        guard store.isAuthenticated, let box = store.runnerBox() else { return nil }
        return SessionClient(token: store.token, box: box)
    }

    private func send() {
        let text = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        guard let client = client() else {
            turnError = store.machineSplitActive
                ? "Your AI machine needs the relay to be reachable from this TV."
                : "No machine selected"
            return
        }
        sending = true
        turnError = nil
        prompt = ""
        Task {
            do {
                // The turn runs in the picked repo (workDir) with the chosen
                // MCP set — the same selection a phone/web task carries.
                let result = try await client.sendText(
                    text,
                    session: nil,
                    workDir: pickedProjectPath,
                    mcpServers: Array(pickedMCPServers),
                    includeYaverMcp: yaverMcpOn
                )
                await MainActor.run {
                    sending = false
                    turn = result
                    if result.ok == false, let err = result.error, !err.isEmpty {
                        turnError = err
                    }
                }
            } catch {
                await MainActor.run {
                    sending = false
                    turnError = error.localizedDescription
                }
            }
        }
    }

    private func choose(_ option: String) {
        guard let client = client() else { return }
        sending = true
        turnError = nil
        Task {
            do {
                let result = try await client.sendChoice(option, session: turn?.session)
                await MainActor.run {
                    sending = false
                    turn = result
                }
            } catch {
                await MainActor.run {
                    sending = false
                    turnError = error.localizedDescription
                }
            }
        }
    }
}
