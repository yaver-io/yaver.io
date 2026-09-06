// VibeTurnPanel.swift — the vibe loop ON the preview screen.
//
// This is what makes the TV a vibing surface instead of a monitor: the same
// screen that streams the app also takes the next prompt. Web gets this with
// a chat pane beside an iframe (RuntimeLabView); interactive TV previews now
// use the same lean split layout. Dictation types into a TextField for free,
// and the turn goes to the
// RUNNER box while the preview keeps polling underneath. HMR lands in the
// frame stream on its own; nothing re-mounts, nothing blanks.
//
// A preview turn starts a normal task. It must work from a cold runner box;
// requiring an already-live terminal session made Send a guaranteed no-op on
// healthy machines that simply had no pane open.
//
// Role rule: the turn client is built from store.runnerBox() — NEVER the
// selected box. In a runner/render split the selected box may be the render
// machine, and a prompt sent there lands on a box with no runner sessions.
//
// Project + MCP picker: task authority starts at No project / No MCP. The
// cross-surface Convex memories remain available only through explicit
// "Use latest" actions; opening a composer never silently grants a previous
// task's workDir or tools.

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
    @Binding var showConsolePopup: Bool
    @Binding var modelLabel: String
    let modelFocusRequest: Int
    let focusRequest: Int

    init(
        project: ProjectSummary?,
        prefill: Binding<String> = .constant(""),
        startsExpanded: Bool = false,
        focusRequest: Int = 0,
        showConsolePopup: Binding<Bool> = .constant(false),
        modelLabel: Binding<String> = .constant("DeepSeek V4 Flash"),
        modelFocusRequest: Int = 0
    ) {
        self.project = project
        self._prefill = prefill
        self._showConsolePopup = showConsolePopup
        self._modelLabel = modelLabel
        self.modelFocusRequest = modelFocusRequest
        self.focusRequest = focusRequest
        self._expanded = State(initialValue: startsExpanded)
    }

    @State private var expanded: Bool
    @State private var prompt = ""
    @State private var sending = false
    @State private var activeTask: TaskSummary?
    @State private var taskLog = ""
    @State private var liveAssistantText = ""
    @State private var optimisticTurns: [TaskConversationTurn] = []
    @State private var appConsole = ""
    @State private var showFullAppConsole = false
    @State private var showFullTaskLog = false
    @State private var taskStream: Task<Void, Never>?
    @State private var taskStreamRetry: Task<Void, Never>?
    @State private var taskStreamNotice: String?
    @State private var rawCursor = 0
    @State private var transcriptCursor = 0
    @State private var detailRefreshTask: Task<Void, Never>?
    @State private var appConsoleTask: Task<Void, Never>?
    @State private var appConsoleRetryTask: Task<Void, Never>?
    @State private var turnError: String?
    // Project/MCP picker state — loaded from the runner box on first open.
    @State private var showProjectPicker = false
    @State private var availableMCPServers: [String] = []
    @State private var pickedMCPServers: Set<String> = []
    @State private var yaverMcpOn = false
    @State private var availableRunners: [AgentRunnerSummary] = []
    @State private var pickedRunner = ""
    @State private var pickedModel = ""
    @State private var pickedReasoningEffort = ""
    @State private var pickedMode = "build"
    @State private var showModelPicker = false
    @State private var showRunnerPicker = false
    @State private var conversationSettingsChanged = false
    @State private var spokenTaskID: String?
    @State private var taskControlCatalog: TaskRunnerControlCatalog?
    @State private var taskControlModel = ""
    @State private var showTaskEffortPicker = false
    @State private var showExitConfirmation = false

    private enum PanelFocus: Hashable {
        case prompt, context, conversation, appConsole, appConsoleLog, taskLog, runner, model, project, mcp
    }

    /// An explicit focus chain is required on tvOS. A focused TextField keeps
    /// directional input for editing, so relying on geometric focus made the
    /// runner/model menus visible but unreachable from the Siri Remote.
    @FocusState private var panelFocus: PanelFocus?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let turnError {
                Text(turnError)
                    .font(.system(size: 15))
                    .foregroundStyle(.orange)
                    .lineLimit(2)
            }

            if expanded {
                VStack(alignment: .leading, spacing: 12) {
                    if activeTask != nil || !optimisticTurns.isEmpty {
                        conversation
                    }
                    // Keep Vibing on SwiftUI's native tvOS text field. The
                    // Siri Remote dictation session is attached by tvOS to the
                    // native TextField; the UIKit first-responder bridge can
                    // show a keyboard and accept typed text but does not make
                    // the remote microphone route into this prompt.
                    TextField("What should change?", text: $prompt)
                        .textFieldStyle(.plain)
                        .font(.system(size: 20, weight: .medium))
                        .foregroundStyle(.black)
                        .tint(.black)
                        .lineLimit(1)
                        .focused($panelFocus, equals: .prompt)
                        .padding(.horizontal, 16)
                        .frame(height: 56)
                        .focusEffectDisabled()
                        .accessibilityIdentifier("vibing.prompt")
                        .onSubmit { send() }
                        #if os(tvOS)
                        .onMoveCommand { direction in
                            if direction == .down { panelFocus = .runner }
                        }
                        #endif
                }
                if panelFocus == .prompt {
                    HStack(spacing: 10) {
                        MicListeningIndicator(color: .blue)
                        Text("Mic ready — one press of the Siri Remote mic dictates.")
                            .font(.system(size: 13))
                            .foregroundStyle(.secondary)
                    }
                    .transition(.opacity)
                }
                // One context control, four selectors inside. Four adjacent
                // native Menu capsules looked like a second toolbar and their
                // intrinsic widths could not share a baseline cleanly. The
                // active authority remains explicit in this one-line summary.
                contextChip
                    .padding(.top, 2)
                if let activeTask {
                    taskStatus(activeTask)
                }
            } else {
                Button {
                    expanded = true
                    // Focus follows the expansion so a second mic press dictates.
                    panelFocus = .prompt
                } label: {
                    Label(activeTask == nil ? "Vibe — ask for a change" : "Ask for another change",
                          systemImage: "wand.and.stars")
                        .font(.system(size: 17, weight: .semibold))
                }
            }
        }
        .padding(.vertical, 4)
        .task(id: project?.id ?? "no-project") {
            await loadPickerState()
            startAppConsole()
        }
        .onDisappear {
            taskStream?.cancel()
            taskStreamRetry?.cancel()
            detailRefreshTask?.cancel()
            appConsoleTask?.cancel()
            appConsoleRetryTask?.cancel()
        }
        .onChange(of: panelFocus) { oldFocus, newFocus in
            // The Apple TV Remote blue tick can dismiss the native keyboard
            // without delivering SwiftUI's onSubmit. If the prompt field was
            // the active chat control, ending that edit is the send action.
            guard oldFocus == .prompt,
                  newFocus == nil,
                  !sending,
                  !prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
            send()
        }
        .fullScreenCover(isPresented: $showConsolePopup) {
            VStack(alignment: .leading, spacing: 12) {
                HStack {
                    Label("Console logs", systemImage: "terminal")
                        .font(.title3.bold())
                    Spacer()
                    Button { showConsolePopup = false } label: {
                        Label("Done", systemImage: "checkmark")
                    }
                    .buttonStyle(.borderedProminent)
                }
                ScrollView(.vertical) {
                    Text(appConsole.isEmpty
                         ? "Waiting for app, npm, Metro, and dev-server output…"
                         : redactHomePaths(appConsole))
                        .font(.system(size: 14, design: .monospaced))
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .focusable()
                .focused($panelFocus, equals: .appConsoleLog)
                .background(Color.white.opacity(0.05), in: RoundedRectangle(cornerRadius: 12))
            }
            .padding(24)
            .frame(minWidth: 680, idealWidth: 860, minHeight: 360, idealHeight: 520)
            .background(Color.black.opacity(0.96))
            .onAppear {
                showFullAppConsole = true
                startAppConsole()
            }
        }
        .onChange(of: showConsolePopup) { _, visible in
            if visible {
                showFullAppConsole = true
                startAppConsole()
            }
        }
        .onChange(of: pickedModel) { _, _ in
            modelLabel = selectedModelLabel
        }
        .onChange(of: modelFocusRequest) { _, _ in
            expanded = true
            panelFocus = .model
        }
        .sheet(isPresented: $showModelPicker) {
            VStack(alignment: .leading, spacing: 18) {
                Text("Select model")
                    .font(.title2.bold())
                Text(taskControlCatalog?.runnerId ?? selectedRunner?.displayName ?? "Runner")
                    .foregroundStyle(.secondary)
                ForEach(taskControlCatalog?.models ?? []) { model in
                    Button {
                        taskControlModel = model.id
                        if taskControlCatalog?.runnerId == "codex", model.supportedReasoningEfforts?.isEmpty == false {
                            showModelPicker = false
                            showTaskEffortPicker = true
                        } else {
                            Task { await applyTaskModel(model.id, effort: nil) }
                        }
                    } label: {
                        HStack {
                            Image(systemName: model.id == taskControlCatalog?.model ? "checkmark.circle.fill" : "circle")
                            Text(model.name ?? model.id)
                            Spacer()
                        }
                    }
                    .buttonStyle(.bordered)
                    .disabled(taskControlCatalog?.isAdopted == true)
                }
                if taskControlCatalog == nil {
                    ForEach(selectedRunner?.models ?? []) { model in
                        Button {
                            pickedModel = model.id
                            reconcilePickedReasoning(for: model)
                            modelLabel = model.name
                            showModelPicker = false
                            if activeTask != nil { conversationSettingsChanged = true }
                        } label: {
                            HStack {
                                Image(systemName: model.id == pickedModel ? "checkmark.circle.fill" : "circle")
                                Text(model.name)
                                Spacer()
                            }
                        }
                        .buttonStyle(.bordered)
                    }
                }
                if taskControlCatalog?.isAdopted == true {
                    Text("This terminal was adopted. Change its model in Runner details so Yaver never guesses at menu positions.")
                        .foregroundStyle(.orange)
                }
            }
            .padding(42)
            .frame(minWidth: 520, minHeight: 360)
        }
        .sheet(isPresented: $showTaskEffortPicker) {
            VStack(alignment: .leading, spacing: 18) {
                Text("Choose reasoning level").font(.title2.bold())
                if let model = taskControlCatalog?.models.first(where: { $0.id == taskControlModel }) {
                    ForEach(model.supportedReasoningEfforts ?? []) { effort in
                        Button(effort.reasoningEffort) {
                            Task { await applyTaskModel(model.id, effort: effort.reasoningEffort) }
                        }
                        .buttonStyle(.bordered)
                    }
                }
            }
            .padding(42)
            .frame(minWidth: 520, minHeight: 300)
        }
        .alert("Exit runner session?", isPresented: $showExitConfirmation) {
            Button("Cancel", role: .cancel) {}
            Button("Exit and verify", role: .destructive) { Task { await exitActiveTask() } }
        } message: {
            Text("Stops this task's real runner seat. Yaver verifies it is gone before reporting success.")
        }
        .sheet(isPresented: $showRunnerPicker) {
            VStack(alignment: .leading, spacing: 18) {
                Text("Select runner")
                    .font(.title2.bold())
                ForEach(supportedRunnerOptions) { runner in
                    Button {
                        pickedRunner = runner.canonicalId
                        let model = runner.models.first(where: { $0.isDefault == true }) ?? runner.models.first
                        pickedModel = model?.id ?? ""
                        reconcilePickedReasoning(for: model)
                        modelLabel = model?.name ?? "Default"
                        showRunnerPicker = false
                        if activeTask != nil { conversationSettingsChanged = true }
                    } label: {
                        HStack {
                            Image(systemName: runner.canonicalId == RegisteredRunner.canonical(pickedRunner) ? "checkmark.circle.fill" : "circle")
                            Text(runner.displayName)
                            Spacer()
                        }
                    }
                    .buttonStyle(.bordered)
                }
            }
            .padding(42)
            .frame(minWidth: 520, minHeight: 360)
        }
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
            DispatchQueue.main.async { panelFocus = .prompt }
            send()
        }
        .onChange(of: focusRequest) { _, _ in
            expanded = true
            DispatchQueue.main.async {
                panelFocus = .prompt
            }
        }
        .onAppear {
            InputStateReporter.shared.route = "vibe-turn"
            if expanded {
                DispatchQueue.main.async {
                    panelFocus = .prompt
                }
            }
        }
        .onChange(of: expanded) { _, isExpanded in
            if isExpanded {
                DispatchQueue.main.async {
                    panelFocus = .prompt
                }
            }
        }
    }

    // ── Project / MCP picker ──────────────────────────────────────────────

    private var runnerBoxId: String? { store.runnerBox()?.id }

    /// Load selectable inventory without applying remembered task authority.
    private func loadPickerState() async {
        guard runnerBoxId != nil else { return }
        yaverMcpOn = false
        pickedMCPServers.removeAll()
        guard let client = store.runnerClient() else { return }
        if let servers = try? await client.listMCPServers() {
            availableMCPServers = servers.map(\.name)
            pickedMCPServers = pickedMCPServers.intersection(Set(availableMCPServers))
        }
        if let list = try? await client.listRunners() {
            // Keep unavailable-but-known runners visible in the … menu (Aider
            // is commonly installed on the box after first launch). The agent
            // supplies the measured installed/ready flags and models; hiding
            // the row made the TV catalogue disagree with web.
            availableRunners = list.runners
            let preferred = runnerBoxId.flatMap { store.primaryRunnerByDevice[$0] }
                ?? list.default
                ?? availableRunners.first(where: \.isDefault)?.id
                ?? availableRunners.first?.id
                ?? ""
            pickedRunner = RegisteredRunner.canonical(preferred)
            let savedModel = runnerBoxId.flatMap { store.primaryModelByDevice[$0] }
            let model = selectedRunner?.models.first(where: { $0.id == savedModel })
                ?? selectedRunner?.models.first(where: { $0.isDefault == true })
                ?? selectedRunner?.models.first
            pickedModel = model?.id ?? ""
            if pickedRunner == "codex" {
                let choices = reasoningEfforts(for: model)
                let savedEffort = runnerBoxId.flatMap { store.primaryReasoningEffortByDevice[$0] }
                pickedReasoningEffort = choices.isEmpty
                    ? (savedEffort ?? model?.defaultReasoningEffort ?? "medium")
                    : choices.contains(savedEffort ?? "")
                        ? (savedEffort ?? "medium")
                        : (model?.defaultReasoningEffort ?? "medium")
            } else {
                pickedReasoningEffort = ""
            }
        }
        // tvOS Vibing deliberately has no project picker. The TV is a lean
        // conversation surface; project authority must be chosen in Tasks or
        // on the web/mobile surface, never from a crowded Siri Remote menu.
    }

    /// "yaver · 2 MCP ▾" — toggles the yaver doorway (default OFF) and the
    /// box's external MCP servers; selection syncs to Convex on change.
    private var mcpChip: some View {
        Menu {
            if let boxId = runnerBoxId, store.lastMCPServersByDevice[boxId] != nil {
                Button("Use latest") { useLatestMCP(for: boxId) }
                Divider()
            }
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
                Text(yaverMcpOn ? "Yaver" : (pickedMCPServers.isEmpty ? "No MCP" : "MCP"))
                if yaverMcpOn && !pickedMCPServers.isEmpty {
                    Text("· \(pickedMCPServers.count) MCP")
                } else if !yaverMcpOn && !pickedMCPServers.isEmpty {
                    Text("· \(pickedMCPServers.count)")
                }
            }
            .lineLimit(1)
            .minimumScaleFactor(0.62)
            .font(.system(size: 15, weight: .semibold))
            .padding(.horizontal, 8).padding(.vertical, 7)
        }
        .focused($panelFocus, equals: .mcp)
        #if os(tvOS)
        .onMoveCommand { direction in
            if direction == .left { panelFocus = .project }
            if direction == .up { panelFocus = .prompt }
        }
        #endif
    }

    private func persistMCP() {
        guard let boxId = runnerBoxId else { return }
        if activeTask != nil { conversationSettingsChanged = true }
        store.rememberMCPServers(Array(pickedMCPServers), includeYaverMcp: yaverMcpOn, for: boxId)
    }

    private func useLatestMCP(for boxId: String) {
        guard let pref = store.lastMCPServersByDevice[boxId] else { return }
        yaverMcpOn = pref.includeYaverMcp ?? false
        pickedMCPServers = Set(pref.mcpServers ?? []).intersection(Set(availableMCPServers))
        if activeTask != nil { conversationSettingsChanged = true }
    }

    private var selectedRunner: AgentRunnerSummary? {
        availableRunners.first(where: { $0.canonicalId == RegisteredRunner.canonical(pickedRunner) })
    }

    private func reasoningEfforts(for model: AgentRunnerModel?) -> [String] {
        model?.supportedReasoningEfforts?.map(\.reasoningEffort) ?? []
    }

    private func reconcilePickedReasoning(for model: AgentRunnerModel?) {
        guard RegisteredRunner.canonical(pickedRunner) == "codex" else {
            pickedReasoningEffort = ""
            return
        }
        let choices = reasoningEfforts(for: model)
        if !choices.isEmpty && !choices.contains(pickedReasoningEffort) {
            pickedReasoningEffort = model?.defaultReasoningEffort ?? "medium"
        }
    }

    private var supportedRunnerOptions: [AgentRunnerSummary] {
        availableRunners.filter {
            ["opencode", "codex", "claude"].contains(RegisteredRunner.canonical($0.canonicalId))
        }
    }

    /// tvOS keeps the authority controls visible and inline. Menus/popovers are
    /// awkward with a Siri Remote and made it look as if settings had vanished;
    /// these small widgets expose the current choice and let the user change it
    /// without leaving the Vibing surface.
    private var contextChip: some View {
        HStack(spacing: 8) {
                inlineModelWidget
                if RegisteredRunner.canonical(pickedRunner) == "opencode" {
                    inlineOpenCodeModeWidget
                }
                inlineMCPWidget
                Button { showRunnerPicker = true } label: {
                    HStack(spacing: 5) {
                        Image(systemName: "cpu")
                        Text(selectedRunner?.displayName ?? "Runner")
                    }
                    .lineLimit(1)
                    .minimumScaleFactor(0.62)
                }
                .buttonStyle(.bordered)
                .accessibilityLabel("Select runner")
        }
        .padding(.vertical, 2)
        .controlSize(.small)
        .font(.system(size: 12, weight: .semibold))
        .lineLimit(1)
        .minimumScaleFactor(0.62)
        .clipped()
        .contentShape(Rectangle())
        .allowsHitTesting(true)
        .zIndex(20)
        .accessibilityIdentifier("vibe.context.widgets")
    }

    private var inlineRunnerWidget: some View {
        HStack(spacing: 4) {
            Image(systemName: "cpu")
            Text(selectedRunner?.displayName ?? "Runner")
                .lineLimit(1).truncationMode(.tail).minimumScaleFactor(0.62)
                .fixedSize(horizontal: true, vertical: false)
        }
    }

    private var inlineModelWidget: some View {
        Button {
            showModelPicker = true
        } label: {
                HStack(spacing: 5) {
                    Image(systemName: "sparkles")
                    Text(selectedModelLabel)
                }
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .minimumScaleFactor(0.62)
                    .fixedSize(horizontal: true, vertical: false)
            }
        .buttonStyle(.bordered)
        .lineLimit(1)
        .accessibilityIdentifier("vibe.model-chip")
        .accessibilityLabel("Select model, current \(selectedModelLabel)")
        }

    @ViewBuilder
    private var inlineMCPWidget: some View {
        if yaverMcpOn || !pickedMCPServers.isEmpty {
            HStack(spacing: 4) {
                Image(systemName: "platter.2.filled.ipad")
                Button(currentMCPLabel) {
                    yaverMcpOn.toggle()
                    persistMCP()
                }
                .buttonStyle(.bordered)
                .lineLimit(1)
            }
        }
    }

    private var inlineOpenCodeModeWidget: some View {
        Button {
            pickedMode = pickedMode == "build" ? "plan" : "build"
            if activeTask != nil { conversationSettingsChanged = true }
        } label: {
            HStack(spacing: 5) {
                Image(systemName: pickedMode == "plan" ? "list.bullet.clipboard" : "hammer")
                Text(pickedMode.capitalized)
            }
            .lineLimit(1)
        }
        .buttonStyle(.bordered)
        .accessibilityLabel("OpenCode mode \(pickedMode)")
    }

    private func cycleModel() {
        let models = selectedRunner?.models ?? []
        guard !models.isEmpty else { return }
        let current = models.firstIndex(where: { $0.id == pickedModel }) ?? -1
        let model = models[(current + 1) % models.count]
        pickedModel = model.id
        reconcilePickedReasoning(for: model)
        if activeTask != nil { conversationSettingsChanged = true }
    }

    private var selectedModelLabel: String {
        selectedRunner?.models.first(where: { $0.id == pickedModel })?.name ?? "Default"
    }

    private var currentMCPLabel: String {
        if yaverMcpOn && !pickedMCPServers.isEmpty { return "Yaver + \(pickedMCPServers.count)" }
        if yaverMcpOn { return "Yaver" }
        if !pickedMCPServers.isEmpty { return "\(pickedMCPServers.count) selected" }
        return "None"
    }

    private var runnerChip: some View {
        Menu {
            ForEach(supportedRunnerOptions) { runner in
                Button {
                    pickedRunner = runner.canonicalId
                    let model = runner.models.first(where: { $0.isDefault == true }) ?? runner.models.first
                    pickedModel = model?.id ?? ""
                    reconcilePickedReasoning(for: model)
                    if activeTask != nil { conversationSettingsChanged = true }
                } label: {
                    if runner.canonicalId == RegisteredRunner.canonical(pickedRunner) {
                        Label(runner.displayName, systemImage: "checkmark")
                    } else { Text(runner.displayName) }
                }
            }
        } label: {
            Label(selectedRunner?.displayName ?? "Choose runner", systemImage: "cpu")
                .font(.system(size: 15, weight: .semibold))
                .lineLimit(1)
                .minimumScaleFactor(0.62)
                .padding(.horizontal, 8).padding(.vertical, 7)
        }
        .disabled(availableRunners.isEmpty)
        .focused($panelFocus, equals: .runner)
        .accessibilityIdentifier("vibe.runner")
        #if os(tvOS)
        .onMoveCommand { direction in
            if direction == .right { panelFocus = .model }
            if direction == .up { panelFocus = .prompt }
        }
        #endif
    }

    private var modelChip: some View {
        Menu {
            Button("Runner default") {
                pickedModel = ""
                reconcilePickedReasoning(for: selectedRunner?.models.first(where: { $0.isDefault == true }))
                if activeTask != nil { conversationSettingsChanged = true }
            }
            ForEach(selectedRunner?.models ?? []) { model in
                Button {
                    pickedModel = model.id
                    reconcilePickedReasoning(for: model)
                    if activeTask != nil { conversationSettingsChanged = true }
                } label: {
                    if model.id == pickedModel { Label(model.name, systemImage: "checkmark") }
                    else { Text(model.name) }
                }
            }
        } label: {
            Label(selectedRunner?.models.first(where: { $0.id == pickedModel })?.name ?? "Runner default", systemImage: "sparkles")
                .font(.system(size: 15, weight: .semibold))
                .lineLimit(1)
                .minimumScaleFactor(0.62)
                .padding(.horizontal, 8).padding(.vertical, 7)
        }
        .disabled(selectedRunner == nil)
        .focused($panelFocus, equals: .model)
        .accessibilityIdentifier("vibe.model")
        #if os(tvOS)
        .onMoveCommand { direction in
            if direction == .left { panelFocus = .runner }
            if direction == .right { panelFocus = .project }
            if direction == .up { panelFocus = .prompt }
        }
        #endif
    }

    @ViewBuilder
    private func taskStatus(_ task: TaskSummary) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("\(taskConversationLabel(task)) · \(task.status ?? "queued")")
                .font(.system(size: 14, weight: .semibold))
                .foregroundStyle(.secondary)
        }
    }

    private func taskConversationLabel(_ task: TaskSummary) -> String {
        if let model = task.model?.trimmingCharacters(in: .whitespacesAndNewlines), !model.isEmpty {
            let effort = task.reasoningEffort?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            return [model, effort].filter { !$0.isEmpty }.joined(separator: " · ")
        }
        return task.runner ?? pickedRunner
    }

    private var appConsolePanel: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button { showFullAppConsole.toggle() } label: {
                HStack {
                    Text("App console")
                    Spacer(minLength: 8)
                    Text(showFullAppConsole ? "Show latest" : "Show full")
                        .foregroundStyle(.secondary)
                    Image(systemName: showFullAppConsole ? "chevron.up" : "chevron.down")
                }
                .font(.system(size: 13, weight: .bold))
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .focusable()
            .focusEffectDisabled()
            .focused($panelFocus, equals: .appConsole)
            .accessibilityIdentifier("vibe.app-console")
            #if os(tvOS)
            .onMoveCommand { direction in
                if direction == .up { panelFocus = .context }
                if direction == .down {
                    if !showFullAppConsole { showFullAppConsole = true }
                    DispatchQueue.main.async { panelFocus = .appConsoleLog }
                }
            }
            #endif

            if showFullAppConsole {
                ScrollView(.vertical) {
                    VStack(alignment: .leading, spacing: 10) {
                        Text(appConsole.isEmpty
                             ? "No Node/Metro output captured yet. Start or reload the app to populate this console."
                             : redactHomePaths(appConsole))
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundStyle(appConsole.isEmpty ? .secondary : .primary)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        if appConsole.isEmpty {
                            Button("Refresh logs") { startAppConsole() }
                                .buttonStyle(.bordered)
                                .focusEffectDisabled()
                        }
                    }
                }
                .focusable()
                .focusEffectDisabled()
                .focused($panelFocus, equals: .appConsoleLog)
                #if os(tvOS)
                .onMoveCommand { direction in
                    if direction == .up { panelFocus = .appConsole }
                }
                #endif
                // Keep a one-line console from becoming a blank diagnostics
                // wall. It grows as output arrives, but yields space back to
                // the conversation when the dev server is quiet.
                .frame(minHeight: 64, maxHeight: 180, alignment: .top)
            } else {
                Text(appConsole.isEmpty ? "Waiting for app and dev-server output…" : redactHomePaths(paneTail(appConsole, lines: 3)))
                    .font(.system(size: 12, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
                    .truncationMode(.head)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(10)
        .background(Color.white.opacity(0.05), in: RoundedRectangle(cornerRadius: 10))
    }

    private var displayTurns: [TaskConversationTurn] {
        var rows = activeTask?.turns ?? []
        if rows.isEmpty, let title = activeTask?.title, !title.isEmpty {
            rows.append(TaskConversationTurn(role: "user", content: title, timestamp: nil))
        }
        for turn in optimisticTurns where !rows.contains(where: { $0.role == turn.role && $0.content == turn.content }) {
            rows.append(turn)
        }
        if !liveAssistantText.isEmpty {
            // Keep the in-flight assistant lane visible even when the task
            // already contains a persisted/placeholder assistant turn. The
            // web Vibing surface appends tokens to this bubble; suppressing it
            // whenever any assistant row exists made tvOS appear to answer in
            // one bulk update after completion.
            let lastAssistant = rows.last(where: { $0.role == "assistant" })?.content ?? ""
            if lastAssistant != liveAssistantText {
                rows.append(TaskConversationTurn(role: "assistant", content: liveAssistantText, timestamp: nil))
            }
        } else if !rows.contains(where: { $0.role == "assistant" }) {
            let answer = (activeTask?.resultText?.isEmpty == false ? activeTask?.resultText : activeTask?.output) ?? ""
            if liveAssistantText.isEmpty, !answer.isEmpty {
                rows.append(TaskConversationTurn(role: "assistant", content: answer, timestamp: nil))
            }
        }
        return rows
    }

    private var conversation: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 10) {
                    ForEach(displayTurns) { turn in
                        conversationBubble(turn)
                    }
                    if activeTask != nil {
                        liveRunnerTurn
                    }
                    Color.clear.frame(height: 1).id("vibe-chat-bottom")
                }
            }
            // tvOS does not enter a plain ScrollView in the focus system by
            // default. Make the conversation itself a Siri Remote target so
            // the user's prompts and Yaver replies can be scrolled, not only
            // the technical log panes.
            .focusable()
            .focusEffectDisabled()
            .focused($panelFocus, equals: .conversation)
            .frame(maxHeight: 400)
            .onChange(of: displayTurns.count) { _, _ in
                withAnimation(.none) { proxy.scrollTo("vibe-chat-bottom", anchor: .bottom) }
            }
            .onChange(of: liveAssistantText) { _, _ in
                withAnimation(.none) { proxy.scrollTo("vibe-chat-bottom", anchor: .bottom) }
            }
        }
    }

    /// Runner stdout is part of the active assistant turn. Rendering it here
    /// avoids both failure modes the TV shipped with: a silent spinner while
    /// the agent works, and a second standalone "Agent logs" card that repeats
    /// the conversation and steals vertical space from the prompt.
    private var liveRunnerTurn: some View {
        let coding = tvTaskIsRunnerCoding(activeTask?.status)
        let terminalStatus = activeTask?.status?.lowercased() ?? "unknown"
        let emptyLogMessage = coding
            ? "Waiting for the runner's first output…"
            : "Task \(terminalStatus); no runner output was captured."
        return HStack {
            if showFullTaskLog {
                VStack(alignment: .leading, spacing: 6) {
                    Button { showFullTaskLog = false } label: {
                        HStack {
                            Text(activeTask.map { tvTaskIsRunnerCoding($0.status) ? "Yaver · working" : "Yaver · logs" } ?? "Yaver · logs")
                            Spacer()
                            Text("Show latest")
                                .foregroundStyle(.secondary)
                            Image(systemName: "chevron.up")
                        }
                        .font(.system(size: 12, weight: .bold))
                    }
                    .buttonStyle(.plain)
                    .focusEffectDisabled()
                    #if os(tvOS)
                    .onMoveCommand { direction in
                        if direction == .down { panelFocus = .taskLog }
                    }
                    #endif
                    ScrollView(.vertical) {
                        if let taskStreamNotice {
                            Text(taskStreamNotice)
                                .font(.system(size: 11))
                                .foregroundStyle(.orange)
                        }
                        Text(taskLog.isEmpty ? emptyLogMessage : redactHomePaths(taskLog))
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundStyle(taskLog.isEmpty ? .secondary : .primary)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .focusable()
                    .focusEffectDisabled()
                    .focused($panelFocus, equals: .taskLog)
                    #if os(tvOS)
                    .onMoveCommand { direction in
                        if direction == .up { panelFocus = .conversation }
                    }
                    #endif
                    .frame(minHeight: 120, maxHeight: 240, alignment: .top)
                }
                .padding(12)
                .background(Color.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
            } else {
                Button { showFullTaskLog = true } label: {
                    VStack(alignment: .leading, spacing: 6) {
                        HStack(spacing: 7) {
                            if coding { ProgressView() }
                                Text(activeTask.map { tvTaskIsRunnerCoding($0.status) ? "Yaver · working" : "Yaver · logs" } ?? "Yaver · logs")
                                .font(.system(size: 12, weight: .bold))
                                .foregroundStyle(.secondary)
                            Spacer()
                            Text("Show logs")
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundStyle(.secondary)
                        }
                        if let taskStreamNotice {
                            Text(taskStreamNotice)
                                .font(.system(size: 11))
                                .foregroundStyle(.orange)
                        }
                        Text(taskLog.isEmpty ? emptyLogMessage : redactHomePaths(paneTail(taskLog, lines: 5)))
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundStyle(taskLog.isEmpty ? .secondary : .primary)
                            .lineLimit(5)
                            .truncationMode(.head)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .padding(12)
                    .contentShape(Rectangle())
                }
                    .buttonStyle(.plain)
                    .focusEffectDisabled()
                    #if os(tvOS)
                    .onMoveCommand { direction in
                        if direction == .down {
                            showFullTaskLog = true
                            DispatchQueue.main.async { panelFocus = .taskLog }
                        }
                    }
                    #endif
                    .background(Color.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
            }
            Spacer(minLength: 70)
        }
    }

    private func conversationBubble(_ turn: TaskConversationTurn) -> some View {
        let user = turn.role == "user"
        return HStack {
            if user { Spacer(minLength: 70) }
            VStack(alignment: .leading, spacing: 4) {
                Text(user ? "You" : "Yaver")
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(user ? .blue : .secondary)
                Text(redactHomePaths(turn.content))
                    .font(.system(size: 15))
                    .frame(maxWidth: 820, alignment: .leading)
            }
            .padding(12)
            .background(user ? Color.blue.opacity(0.22) : Color.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
            if !user { Spacer(minLength: 70) }
        }
    }

    private func paneTail(_ pane: String, lines: Int = 3) -> String {
        pane.split(separator: "\n", omittingEmptySubsequences: true)
            .suffix(lines)
            .joined(separator: "\n")
    }

    @MainActor
    private func startAppConsole() {
        appConsoleTask?.cancel()
        appConsole = "[console] connecting to app/dev-server logs…"
        guard let preferredClient = store.renderClient() ?? store.runnerClient() else {
            appConsole = "App console unavailable: no render machine is reachable."
            return
        }
        let relayFallback = store.runnerClient()
        appConsoleTask = Task {
            var client = preferredClient
            // Seed late subscribers from the agent's bounded Node/Metro
            // stdout tail before waiting for new SSE events. Otherwise a
            // render that already failed can leave the console looking empty
            // even though /dev/status has the useful npm/node lines.
            var status = try? await client.devServerStatus()
            if status == nil, let fallback = relayFallback {
                // A split render box can be stale/offline while the runner is
                // healthy through Yaver relay. Do not leave Vibing's console
                // blank just because the preferred render leg failed.
                client = fallback
                status = try? await client.devServerStatus()
                await MainActor.run {
                    appConsole = "[console] render leg unavailable; using runner relay…"
                }
            }
            if let status {
                await MainActor.run {
                    if let recent = status.recentLogs, !recent.isEmpty {
                        appConsole = recent.joined(separator: "\n")
                    } else if let error = status.error, !error.isEmpty {
                        appConsole = "[dev-server] \(error)"
                    } else if let label = status.servingLabel, !label.isEmpty {
                        appConsole = "[dev-server] \(label)"
                    } else {
                        appConsole = "[dev-server] connected; no recent output"
                    }
                }
            }
            // SSE is the live path, but a restarted dev server can have a
            // useful bounded tail without emitting a new event. Poll that
            // authoritative tail while subscribed so App console never
            // depends on one missed event to become permanently empty.
            appConsoleRetryTask?.cancel()
            appConsoleRetryTask = Task { @MainActor in
                while !Task.isCancelled {
                    try? await Task.sleep(nanoseconds: 2_000_000_000)
                    guard !Task.isCancelled, let latest = try? await client.devServerStatus() else { continue }
                    if let recent = latest.recentLogs, !recent.isEmpty {
                        appConsole = recent.joined(separator: "\n")
                    }
                }
            }
            let stream = await client.subscribeDevEvents { event in
                let line = event.logLine ?? event.message
                guard let line, !line.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
                Task { @MainActor in
                    appConsole = String((appConsole + (appConsole.isEmpty ? "" : "\n") + line).suffix(512 * 1024))
                }
            } onGap: { gap in
                Task { @MainActor in
                    appConsole = String((appConsole + (appConsole.isEmpty ? "" : "\n") + gap.summary).suffix(512 * 1024))
                }
            } onEnd: { kind, reason in
                guard kind != .cancelled else { return }
                Task { @MainActor in
                    let line = "[console stream interrupted] \(reason ?? "connection closed")"
                    appConsole = String((appConsole + (appConsole.isEmpty ? "" : "\n") + line).suffix(512 * 1024))
                    appConsoleRetryTask?.cancel()
                    appConsoleRetryTask = Task { @MainActor in
                        try? await Task.sleep(nanoseconds: 1_000_000_000)
                        guard !Task.isCancelled else { return }
                        startAppConsole()
                    }
                }
            } onError: { message in
                Task { @MainActor in
                    appConsole = String((appConsole + (appConsole.isEmpty ? "" : "\n") + "[console] " + message).suffix(512 * 1024))
                }
            }
            await stream.value
        }
    }

    private func send() {
        let text = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        guard let client = store.runnerClient() else {
            turnError = store.machineSplitActive
                ? "Your AI machine needs the relay to be reachable from this TV."
                : "No machine selected"
            return
        }
        if text.lowercased() == "/model", let current = activeTask {
            prompt = ""
            Task { await openTaskModelControl(current, client: client) }
            return
        }
        if text.lowercased() == "/exit", activeTask != nil {
            prompt = ""
            showExitConfirmation = true
            return
        }
        sending = true
        turnError = nil
        prompt = ""
        let optimistic = TaskConversationTurn(
            role: "user",
            content: text,
            timestamp: ISO8601DateFormatter().string(from: Date())
        )
        optimisticTurns.append(optimistic)
        Task {
            do {
                if let current = activeTask {
                    switch tvChatFollowUpAction(
                        status: current.status,
                        runner: current.runner,
                        selectedRunner: pickedRunner,
                        settingsChanged: conversationSettingsChanged
                    ) {
                    case .continueCurrent:
                        try await client.continueTask(current.id, input: text)
                        await MainActor.run {
                            activeTask = taskWithStatus(current, "running")
                            liveAssistantText = ""
                            sending = false
                            // A previous turn may have ended its SSE stream
                            // after reaching a terminal state. Reattach on a
                            // follow-up so the new runner output cannot be
                            // silently lost behind the old connection.
                            attach(to: current.id, client: client)
                        }
                    case .settingsChangeBlocked(let message):
                        await MainActor.run {
                            optimisticTurns.removeAll { $0.id == optimistic.id }
                            if prompt.isEmpty { prompt = text }
                            turnError = message
                            sending = false
                        }
                        return
                    }
                } else {
                    let created = try await client.createTask(
                        title: text,
                        description: text,
                        workDir: "",
                        projectName: "",
                        runner: pickedRunner,
                        model: pickedModel,
                        reasoningEffort: RegisteredRunner.canonical(pickedRunner) == "codex" ? pickedReasoningEffort : "",
                        mode: RegisteredRunner.canonical(pickedRunner) == "opencode" ? pickedMode : "",
                        mcpServers: Array(pickedMCPServers),
                        includeYaverMcp: yaverMcpOn,
                        sessionStartedFrom: "vibing"
                    )
                    await MainActor.run {
                        sending = false
                        activeTask = created
                        taskLog = ""
                        liveAssistantText = ""
                        rawCursor = 0
                        transcriptCursor = 0
                        conversationSettingsChanged = false
                        attach(to: created.id, client: client)
                    }
                }
            } catch {
                await MainActor.run {
                    optimisticTurns.removeAll { $0.id == optimistic.id }
                    if prompt.isEmpty { prompt = text }
                    sending = false
                    turnError = error.localizedDescription
                }
            }
        }
    }

    private func openTaskModelControl(_ task: TaskSummary, client: AgentClient) async {
        turnError = nil
        do {
            let catalog = try await client.taskRunnerControls(task.id)
            await MainActor.run {
                taskControlCatalog = catalog
                taskControlModel = catalog.model
                    ?? catalog.models.first(where: { $0.isDefault == true })?.id
                    ?? catalog.models.first?.id
                    ?? ""
                showModelPicker = true
            }
        } catch {
            await MainActor.run { turnError = error.localizedDescription }
        }
    }

    private func applyTaskModel(_ model: String, effort: String?) async {
        guard let current = activeTask, let client = store.runnerClient() else { return }
        do {
            let result = try await client.applyTaskRunnerControl(
                current.id, control: "model", model: model, reasoningEffort: effort)
            guard result.ok else { throw AgentError(message: result.error ?? "The model could not be changed.") }
            let refreshed = try await client.task(current.id)
            await MainActor.run {
                activeTask = refreshed
                pickedModel = result.model ?? model
                modelLabel = result.display ?? model
                conversationSettingsChanged = false
                showModelPicker = false
                showTaskEffortPicker = false
                turnError = "Model set to \(result.display ?? model) for the next turn."
            }
        } catch {
            await MainActor.run { turnError = error.localizedDescription }
        }
    }

    private func exitActiveTask() async {
        guard let current = activeTask, let client = store.runnerClient() else { return }
        do {
            let result = try await client.applyTaskRunnerControl(current.id, control: "exit", confirmed: true)
            guard result.ok, result.verified == true else {
                throw AgentError(message: result.error ?? "The runner did not verify that it exited.")
            }
            let refreshed = try? await client.task(current.id)
            await MainActor.run {
                activeTask = refreshed ?? taskWithStatus(current, result.status ?? "stopped")
                turnError = result.alreadyExited == true
                    ? "Runner session was already exited; the agent verified no seat remains."
                    : "Runner session exited and verified."
            }
        } catch {
            await MainActor.run { turnError = error.localizedDescription }
        }
    }

    @MainActor
    private func clearChat() {
        taskStream?.cancel()
        taskStreamRetry?.cancel()
        detailRefreshTask?.cancel()
        activeTask = nil
        optimisticTurns.removeAll()
        taskLog = ""
        liveAssistantText = ""
        rawCursor = 0
        transcriptCursor = 0
        taskStreamNotice = nil
        turnError = nil
        prompt = ""
        expanded = true
        panelFocus = .prompt
    }

    @MainActor
    private func attach(to taskId: String, client: AgentClient) {
        taskStream?.cancel()
        taskStreamRetry?.cancel()
        Task {
            taskStream = await client.subscribeTaskOutput(
                taskId: taskId,
                since: transcriptCursor > 0 ? transcriptCursor : nil,
                rawSince: rawCursor,
                onRaw: { text, offset, full in Task { @MainActor in
                    taskStreamNotice = nil
                    taskLog = full ? text : String((taskLog + text).suffix(128 * 1024))
                    rawCursor = offset
                } },
                onData: { text, offset, full in Task { @MainActor in
                    taskStreamNotice = nil
                    // Groomed runner text is the conversational assistant
                    // lane. Render it incrementally beside the WebRTC app,
                    // rather than waiting for the terminal task snapshot.
                    liveAssistantText = full
                        ? String(text.suffix(64 * 1024))
                        : String((liveAssistantText + text).suffix(64 * 1024))
                    if let offset { transcriptCursor = offset }
                    // Older/stale agents can emit groomed `output` while their
                    // raw stdout lane is empty. Do not render a false-empty
                    // Agent logs panel after successful work: show the measured
                    // compatibility lane until a raw replay replaces it.
                    if taskLog.isEmpty, !text.isEmpty {
                        taskLog = String(("[task output]\n" + text).suffix(128 * 1024))
                    }
                    scheduleDetailRefresh(taskId: taskId, client: client)
                } },
                onDone: { status in Task { @MainActor in
                    if var row = activeTask, row.id == taskId {
                        row = taskWithStatus(row, status)
                        activeTask = row
                    }
                    await refreshTask(taskId: taskId, client: client)
                } },
                onPresentation: { event in Task { @MainActor in
                    guard let row = activeTask, row.id == taskId else { return }
                    var messages = event.type == "presentation_snapshot"
                        ? (event.messages ?? [])
                        : (row.presentation ?? [])
                    if event.type != "presentation_snapshot", let message = event.message {
                        if let index = messages.firstIndex(where: { $0.id == message.id }) {
                            if event.op == "append" {
                                let previous = messages[index]
                                messages[index] = TaskPresentationMessage(
                                    id: previous.id, kind: message.kind, role: message.role ?? previous.role,
                                    text: previous.text + message.text, phase: message.phase, state: message.state,
                                    runner: message.runner, project: message.project, machine: message.machine,
                                    platform: message.platform, surface: message.surface,
                                    createdAt: previous.createdAt, updatedAt: message.updatedAt
                                )
                            } else { messages[index] = message }
                        } else { messages.append(message) }
                    }
                    activeTask = taskWithPresentation(row, messages)
                    if let answer = messages.last(where: { $0.kind == "message" && $0.role == "assistant" }) {
                        liveAssistantText = answer.text
                    } else if let state = messages.last(where: { $0.kind != "message" }) {
                        liveAssistantText = state.text
                    }
                } },
                onEnd: { kind, reason in Task { @MainActor in
                    if kind == .interrupted {
                        taskStreamNotice = reason ?? "Agent log stream interrupted; reconnecting…"
                    }
                    guard kind == .interrupted,
                          activeTask?.id == taskId else { return }
                    // The agent retains raw stdout and exposes a byte cursor,
                    // so a relay/SSE drop is recoverable without duplicate
                    // logs. Re-probe task state, then resume while it codes.
                    await refreshTask(taskId: taskId, client: client)
                    guard let task = activeTask,
                          task.id == taskId,
                          tvTaskIsRunnerCoding(task.status) else { return }
                    taskStreamRetry?.cancel()
                    taskStreamRetry = Task { @MainActor in
                        try? await Task.sleep(nanoseconds: 1_000_000_000)
                        guard !Task.isCancelled, activeTask?.id == taskId else { return }
                        attach(to: taskId, client: client)
                    }
                } }
            )
        }
    }

    @MainActor
    private func scheduleDetailRefresh(taskId: String, client: AgentClient) {
        detailRefreshTask?.cancel()
        detailRefreshTask = Task {
            try? await Task.sleep(nanoseconds: 350_000_000)
            guard !Task.isCancelled else { return }
            await refreshTask(taskId: taskId, client: client)
        }
    }

    @MainActor
    private func refreshTask(taskId: String, client: AgentClient) async {
        guard let detail = try? await client.task(taskId), activeTask?.id == taskId else { return }
        activeTask = detail
        if liveAssistantText.isEmpty, let output = detail.output, !output.isEmpty {
            liveAssistantText = output
        }
        optimisticTurns.removeAll { optimistic in
            (detail.turns ?? []).contains { $0.role == optimistic.role && $0.content == optimistic.content }
                || (detail.pendingFollowUps ?? []).contains { $0.input == optimistic.content }
        }
        speakCompletedTaskIfNeeded(detail)
    }

    /// Keep the rendered-preview vibe loop lean-back: once a task reaches a
    /// terminal state, speak one redacted summary through tvOS TTS. The task
    /// ID guard prevents every SSE refresh from repeating the answer.
    @MainActor
    private func speakCompletedTaskIfNeeded(_ task: TaskSummary) {
        let terminal = Set(["completed", "review", "failed", "stopped"])
        guard terminal.contains((task.status ?? "").lowercased()), spokenTaskID != task.id else { return }
        let semantic = task.presentation?.last(where: { $0.kind == "message" && $0.role == "assistant" })?.text
        let text = [semantic, task.resultText, task.output, liveAssistantText]
            .compactMap { value -> String? in
                guard let value, !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
                return value
            }
            .first
        guard let text else { return }
        spokenTaskID = task.id
        Speech.speakSummary(of: text)
    }

    private func taskWithStatus(_ task: TaskSummary, _ status: String) -> TaskSummary {
        TaskSummary(
            id: task.id,
            title: task.title,
            status: status,
            runner: task.runner,
            model: task.model,
            reasoningEffort: task.reasoningEffort,
            workDir: task.workDir,
            projectName: task.projectName,
            sessionId: task.sessionId,
            output: task.output,
            resultText: task.resultText,
            presentation: task.presentation,
            turns: task.turns,
            pendingFollowUps: task.pendingFollowUps,
            tmuxSession: task.tmuxSession,
            executionSession: task.executionSession
        )
    }

    private func taskWithPresentation(_ task: TaskSummary, _ presentation: [TaskPresentationMessage]) -> TaskSummary {
        TaskSummary(
            id: task.id, title: task.title, status: task.status, runner: task.runner,
            model: task.model, reasoningEffort: task.reasoningEffort, workDir: task.workDir, projectName: task.projectName,
            sessionId: task.sessionId, output: task.output, resultText: task.resultText,
            presentation: presentation, turns: task.turns,
            pendingFollowUps: task.pendingFollowUps, tmuxSession: task.tmuxSession,
            executionSession: task.executionSession
        )
    }
}
