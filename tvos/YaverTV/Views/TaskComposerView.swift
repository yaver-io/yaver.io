// TaskComposerView.swift — START a vibe from the couch (POST /tasks).
//
// This closes the gap that made the TV a monitor instead of a surface: the
// client had createTask (AgentClient.swift) since 2026-08-03 and no view ever
// called it, so the TV could watch work happen and never start any. The
// composer mirrors the web dispatch funnel's body — empty runner/model lets
// the agent apply the account's per-device primary, exactly like the phone —
// plus the opencode mode/goal/askMode fields so a TV-started task is
// indistinguishable from one started in the dashboard.
//
// Project + MCP pickers reuse VibeTurnPanel's Convex-remembered pattern
// (defaultRuntimeProjectByDevice / mcpServersByDevice via YaverStore), so a
// project picked on the phone shows up pre-selected on the TV.

import SwiftUI

struct TaskComposerView: View {
    @EnvironmentObject var store: YaverStore
    @Environment(\.dismiss) private var dismiss

    /// Optional project to preselect (e.g. "Start a vibe" from a project's
    /// dead-end preview screen). Matched by name against /projects.
    private let initialProjectName: String?

    init(initialProjectName: String? = nil) {
        self.initialProjectName = initialProjectName
    }

    @State private var prompt = ""
    @State private var creating = false
    @State private var createdTask: TaskSummary?
    @State private var error: String?
    // Project/MCP picker state (runner box /projects, same as VibeTurnPanel).
    @State private var availableProjects: [ProjectSummary] = []
    @State private var pickedProjectPath: String?
    @State private var availableMCPServers: [String] = []
    @State private var pickedMCPServers: Set<String> = []
    @State private var yaverMcpOn = true
    // opencode mode (build/plan/custom) and the explain-first askMode frame.
    @State private var mode = ""
    @State private var askMode = false

    /// The composer opens focused on the prompt so the Siri Remote's mic
    /// button dictates into it immediately — the only speech input a TV has.
    @FocusState private var promptFocused: Bool

    private var runnerBoxId: String? { store.runnerBox()?.id }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            header

            TextField("What should I build or change?", text: $prompt, axis: .vertical)
                .textFieldStyle(.plain)
                .font(.system(size: 22))
                .lineLimit(3, reservesSpace: true)
                .focused($promptFocused)
                .onSubmit { create() }

            if promptFocused && !creating {
                // YouTube-style listening cue: the prompt is focused, so one
                // press of the Siri Remote mic dictates immediately.
                HStack(spacing: 14) {
                    MicListeningIndicator(color: .blue)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Mic ready").font(.system(size: 16, weight: .semibold))
                        Text("Press the mic button on the Siri Remote to dictate — one press starts dictation.")
                            .font(.system(size: 13)).foregroundStyle(.secondary)
                    }
                    Spacer()
                    EqualizerBars(barCount: 5, color: .blue, active: true)
                }
                .padding(.horizontal, 16).padding(.vertical, 12)
                .background(Color.blue.opacity(0.12), in: RoundedRectangle(cornerRadius: 14))
                .transition(.opacity)
            } else {
                Text("Press the mic button on the Siri Remote to dictate — or type with the remote's keyboard.")
                    .font(.system(size: 14))
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 10) {
                projectChip
                mcpChip
                modeChip
                askChip
            }

            HStack(spacing: 14) {
                if creating {
                    EqualizerBars(barCount: 4, color: .blue, active: true)
                }
                Button(creating ? "Starting…" : "Start vibe") {
                    create()
                }
                .disabled(creating || prompt.trimmingCharacters(in: .whitespaces).isEmpty)
                .buttonStyle(.borderedProminent)
                .controlSize(.large)

                Button("Cancel") { dismiss() }
                    .buttonStyle(.bordered)
                    .controlSize(.large)
            }

            if let createdTask {
                HStack(spacing: 10) {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                    Text("Started — \(createdTask.safeTitle)")
                        .font(.system(size: 16))
                }
                .padding(.top, 4)
            }
            if let error {
                Text(error)
                    .font(.system(size: 15))
                    .foregroundStyle(.orange)
                    .lineLimit(3)
            }
            Spacer(minLength: 0)
        }
        .padding(40)
        .frame(maxWidth: 900)
        .task { await loadPickerState() }
        .onAppear { promptFocused = true }
        // tvOS 17 default-focus: the prompt is the first thing focus lands on,
        // so the Siri Remote mic button dictates immediately (the only STT a
        // TV has) — YouTube-style, no navigation first (2026-08-13).
        .defaultFocus($promptFocused, true)
    }

    private var header: some View {
        HStack {
            Image(systemName: "wand.and.stars").font(.system(size: 26)).foregroundStyle(.blue)
            Text("New vibe").font(.system(size: 30, weight: .bold))
            Spacer()
        }
    }

    // ── Project / MCP / mode / ask chips ───────────────────────────────────

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
        return "Project ▾"
    }

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

    /// opencode agent mode: build/plan/custom. Empty = the account's default
    /// agent. Only shown when the user wants it — most TV vibes want the
    /// default.
    private var modeChip: some View {
        Menu {
            Button { mode = "" } label: {
                if mode.isEmpty { Label("Default agent", systemImage: "checkmark") } else { Text("Default agent") }
            }
            ForEach(["build", "plan"], id: \.self) { m in
                Button { mode = m } label: {
                    if mode == m { Label(m, systemImage: "checkmark") } else { Text(m) }
                }
            }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "gearshape.2")
                Text(mode.isEmpty ? "Mode ▾" : mode)
            }
            .font(.system(size: 15, weight: .semibold))
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(.ultraThinMaterial, in: Capsule())
        }
    }

    /// Explain-first frame (askMode): grounded file:line answer, no changes
    /// without confirmation. The couch's "deep audit this repo" toggle.
    private var askChip: some View {
        Button {
            askMode.toggle()
        } label: {
            HStack(spacing: 6) {
                Image(systemName: askMode ? "checkmark.seal.fill" : "checkmark.seal")
                Text(askMode ? "Deep audit (ask)" : "Deep audit")
            }
            .font(.system(size: 15, weight: .semibold))
            .padding(.horizontal, 12).padding(.vertical, 8)
            .background(askMode ? Color.blue.opacity(0.25) : Color.clear, in: Capsule())
            .overlay(Capsule().stroke(askMode ? Color.blue : Color.secondary.opacity(0.4), lineWidth: 1.5))
        }
    }

    private func persistMCP() {
        guard let boxId = runnerBoxId else { return }
        store.rememberMCPServers(Array(pickedMCPServers), includeYaverMcp: yaverMcpOn, for: boxId)
    }

    private func loadPickerState() async {
        guard let boxId = runnerBoxId else { return }
        if let prefs = store.lastProjectByDevice[boxId], let name = prefs.projectName {
            pickedProjectPath = availableProjects.first(where: { $0.name == name })?.path
        }
        if let mcpPref = store.lastMCPServersByDevice[boxId] {
            yaverMcpOn = mcpPref.includeYaverMcp ?? true
            pickedMCPServers = Set(mcpPref.mcpServers ?? [])
        }
        guard let client = store.runnerClient() else { return }
        if let list = try? await client.listProjects() {
            availableProjects = list
            // Remembered project may name a path the /projects list matches.
            if pickedProjectPath == nil, let prefs = store.lastProjectByDevice[boxId] {
                pickedProjectPath = availableProjects.first(where: { $0.name == prefs.projectName })?.path
            }
            // Explicit "start a vibe in THIS project" wins over the remembered
            // one (2026-08-13) — comes from a project's preview dead-end.
            if pickedProjectPath == nil, let name = initialProjectName {
                pickedProjectPath = availableProjects.first(where: { $0.name == name })?.path
                if let path = pickedProjectPath, let proj = availableProjects.first(where: { $0.path == path }) {
                    store.rememberProject(proj, for: boxId)
                }
            }
        }
        if let servers = try? await client.listMCPServers() {
            availableMCPServers = servers.map(\.name)
        }
    }

    private func create() {
        let text = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !creating else { return }
        creating = true
        error = nil
        Task {
            do {
                guard let client = store.runnerClient() else {
                    throw AgentError(message: store.machineSplitActive
                        ? "Your AI runner machine needs the relay to be reachable from this TV."
                        : "No machine selected")
                }
                let task = try await client.createTask(
                    title: text,
                    description: text,
                    workDir: pickedProjectPath ?? "",
                    projectName: pickedProjectPath.flatMap { path in
                        availableProjects.first(where: { $0.path == path })?.name
                    } ?? "",
                    mode: mode,
                    askMode: askMode,
                    mcpServers: Array(pickedMCPServers),
                    includeYaverMcp: yaverMcpOn
                )
                createdTask = task
                prompt = ""
                // A moment of confirmation, then the TV drops back to the list
                // where the new task is visible at the top of Active.
                try? await Task.sleep(nanoseconds: 1_200_000_000)
                dismiss()
            } catch {
                self.error = error.localizedDescription
            }
            creating = false
        }
    }
}
