// TaskDetailView.swift — one continuous mobile-style conversation on tvOS.
//
// The old screen called a raw stdout terminal "Chat" and offered no reply
// field. A user had to back out, create another task, then find the new console.
// Mobile's mechanic is the contract: render user/assistant turns, show a sent
// message immediately and continue finished or live tasks in their exact
// runner conversation and task-owned tmux seat.
// The raw console remains available as progressive disclosure, not the primary
// interaction model.

import SwiftUI

struct TaskDetailView: View {
    @EnvironmentObject var store: YaverStore

    @State private var task: TaskSummary
    @State private var status: String?
    @State private var console = ""
    // The chat already renders groomed task output. Console is progressive
    // disclosure for genuine raw stdout only, otherwise it duplicates the
    // assistant answer and spends most of the television on the same text.
    @State private var showConsole = false
    @State private var streamMessage: String?
    @State private var streamRetrying = false
    @State private var reattachNonce = 0
    @State private var stream: Task<Void, Never>?
    @State private var reattachTask: Task<Void, Never>?
    @State private var reattachAttempt = 0
    @State private var rawCursor = 0
    @State private var transcriptCursor = 0
    @State private var liveAssistantText = ""
    @State private var presentation: [TaskPresentationMessage]

    @State private var reply = ""
    @State private var sending = false
    @State private var sendError: String?
    @State private var optimisticTurns: [TaskConversationTurn] = []
    @State private var pendingQuestion: TaskAgentQuestion?
    @State private var questionReply = ""
    @State private var questionSelections: Set<String> = []
    @State private var answeringQuestion = false
    @State private var questionError: String?
    @State private var parkedRunner: String?
    @State private var taskScopeDenied = false

    // Optional project + MCP context. Inventory is shared with mobile/web,
    // while authority starts empty until the user chooses or taps Use latest.
    @State private var availableProjects: [ProjectSummary] = []
    @State private var pickedProjectPath: String?
    @State private var availableMCPServers: [String] = []
    @State private var pickedMCPServers: Set<String> = []
    @State private var yaverMcpOn = false
    @State private var availableRunners: [AgentRunnerSummary] = []
    @State private var pickedRunner = ""
    @State private var pickedModel = ""
    @State private var settingsChanged = false
    @State private var showTaskSettings = false
    @State private var runnerControl: RunnerControlMode?
    @State private var runnerControlCatalog: TaskRunnerControlCatalog?
    @State private var runnerControlModel = ""
    @State private var runnerControlBusy = false
    @State private var runnerControlError: String?
    @State private var runnerControlNotice: String?

    private enum RunnerControlMode: String, Identifiable {
        case model, effort, exit
        var id: String { rawValue }
    }

    private enum ReplyFocus: Hashable { case field, settings, send }
    @FocusState private var replyFocus: ReplyFocus?

    private static let consoleCap = 512 * 1024

    init(task: TaskSummary) {
        _task = State(initialValue: task)
        _status = State(initialValue: task.status)
        _pickedRunner = State(initialValue: RegisteredRunner.canonical(task.runner ?? ""))
        _pickedModel = State(initialValue: task.model ?? "")
        _presentation = State(initialValue: task.presentation ?? [])
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            conversation
            composer
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
        .onAppear { InputStateReporter.shared.route = "task-detail" }
        .task { await loadConfiguration(); await refreshDetail() }
        .task(id: reattachNonce) { await startStream() }
        .onDisappear { stream?.cancel(); reattachTask?.cancel() }
        .onReceive(NotificationCenter.default.publisher(for: Notification.Name("UIKeyboardDidHideNotification"))) { _ in
            guard !activeComposerText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
            DispatchQueue.main.async { replyFocus = .send }
        }
        .onChange(of: replyFocus) { oldFocus, newFocus in
            guard oldFocus == .field, newFocus == nil,
                  !activeComposerText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
            DispatchQueue.main.async { replyFocus = .send }
        }
        .defaultFocus($replyFocus, .field)
        .sheet(isPresented: $showTaskSettings) { taskSettingsPanel }
    }

    private var header: some View {
        HStack(spacing: 14) {
            Circle().fill(color(for: status ?? task.status)).frame(width: 14, height: 14)
            VStack(alignment: .leading, spacing: 3) {
                Text(task.safeTitle).font(.system(size: 22, weight: .semibold)).lineLimit(2)
                Text([modelEffortLabel.isEmpty ? runnerLabel : modelEffortLabel, statusLabel].filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.system(size: 15)).foregroundStyle(.secondary)
            }
            Spacer()
            if runnerCoding {
                HStack(spacing: 8) {
                    EqualizerBars(barCount: 4, color: .green, active: true)
                    Text("LIVE").font(.system(size: 14, weight: .bold)).foregroundStyle(.green)
                }
            }
            Button(modelEffortLabel.isEmpty ? "Model" : modelEffortLabel) {
                openRunnerControl(.model)
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("chat.runner-model")
            Button("Exit") { openRunnerControl(.exit) }
                .buttonStyle(.bordered)
                .tint(.red)
                .accessibilityIdentifier("chat.runner-exit")
        }
        .padding(.horizontal, 48).padding(.vertical, 18)
    }

    private var conversation: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 16) {
                    // Keep the newest semantic narration prominent while the
                    // runner works. Tool/status prose is the fallback; raw
                    // terminal output remains behind Live console.
                    if let summary = (runnerCoding
                        ? presentation.last(where: { $0.kind == "message" && $0.role == "assistant" && !$0.text.isEmpty })
                        : nil) ?? presentation.last(where: { $0.kind != "message" && !$0.text.isEmpty }) {
                        VStack(alignment: .leading, spacing: 5) {
                            if summary.kind == "message" {
                                Text("LATEST UPDATE FROM YAVER")
                                    .font(.system(size: 12, weight: .bold))
                                    .foregroundStyle(.secondary)
                            }
                            Text(summary.text)
                                .font(.system(size: 17, weight: .semibold))
                                .lineLimit(summary.kind == "message" ? 4 : 2)
                            let meta = [summary.machine, summary.platform, summary.runner, summary.project]
                                .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
                            if !meta.isEmpty {
                                Text(meta).font(.system(size: 13)).foregroundStyle(.secondary)
                            }
                        }
                        .padding(16)
                        .background(Color.white.opacity(0.06), in: RoundedRectangle(cornerRadius: 14))
                        .accessibilityIdentifier("task.presentation-status")
                    }
                    ForEach(displayTurns) { turn in
                        bubble(turn)
                    }

                    if runnerCoding, displayTurns.last?.role != "assistant" {
                        HStack(spacing: 10) {
                            ProgressView()
                            Text("The runner is working…")
                                .foregroundStyle(.secondary)
                                .accessibilityIdentifier("chat.runner-working")
                        }
                        .padding(18)
                        .background(Color.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 16))
                    }

                    if let pendingQuestion {
                        taskQuestionCard(pendingQuestion)
                    }

                    if let runnerControl {
                        runnerControlCard(runnerControl)
                    }

                    if let streamMessage {
                        HStack(spacing: 10) {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                            Text(streamMessage).foregroundStyle(.orange)
                            // Automatic recovery needs no competing focusable
                            // control. The button appears only after the
                            // bounded ladder gives up.
                            if !streamRetrying {
                                Button("Reattach") {
                                    reattachTask?.cancel()
                                    reattachAttempt = 0
                                    streamRetrying = false
                                    self.streamMessage = nil
                                    reattachNonce += 1
                                }
                            }
                        }
                        .font(.system(size: 15))
                    }

                    if showConsole && (!cleanConsole.isEmpty || runnerCoding) {
                        VStack(alignment: .leading, spacing: 10) {
                            Text("Live console").font(.system(size: 15, weight: .semibold)).foregroundStyle(.secondary)
                            Text(cleanConsole.isEmpty ? "Waiting for output…" : cleanConsole)
                                .font(.system(size: 13, design: .monospaced))
                                .foregroundStyle(cleanConsole.isEmpty ? .secondary : .primary)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .padding(18)
                        .background(Color.white.opacity(0.05), in: RoundedRectangle(cornerRadius: 16))
                    }

                    Color.clear.frame(height: 1).id("chat-bottom")
                }
                .padding(.horizontal, 48).padding(.vertical, 20)
            }
            .onChange(of: displayTurns.count) { _, _ in
                withAnimation(.none) { proxy.scrollTo("chat-bottom", anchor: .bottom) }
            }
            .onChange(of: console.count) { _, _ in
                guard showConsole else { return }
                withAnimation(.none) { proxy.scrollTo("chat-bottom", anchor: .bottom) }
            }
        }
    }

    private var composer: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let sendError {
                Text(sendError).font(.system(size: 14)).foregroundStyle(.orange).lineLimit(2)
            }
            if let runnerControlNotice {
                Text(runnerControlNotice).font(.system(size: 14)).foregroundStyle(.green).lineLimit(2)
            }
            if taskScopeDenied {
                NavigationLink("Update the agent to continue Tasks") { UpdateAgentView() }
                    .buttonStyle(.borderedProminent)
            }
            if let parkedRunner {
                NavigationLink("Sign in to \(runnerDisplayName(parkedRunner))") { RuntimeDashboardView() }
                    .buttonStyle(.borderedProminent)
            }
            if pendingQuestion?.isSecret == true {
                Text("This answer may contain a credential. Answer it from Yaver Tasks on your phone or desktop.")
                    .font(.system(size: 15))
                    .foregroundStyle(.secondary)
            } else {
                HStack(spacing: 12) {
                // Match the new-vibe composer: vertical text fields trap the
                // Siri Remote's Down event, so a dictated reply could not
                // reach Send without backing out of the screen.
                // Shared dictation field (see YaverDictationField) — a single
                // text input target for the Siri Remote mic on every task
                // reply, matching the new-vibe composer.
                YaverDictationField(
                    text: composerBinding,
                    onSubmit: {
                        // The blue tvOS keyboard tick is the chat Send action,
                        // not merely a focus move. sendReply() is guarded
                        // against duplicate delegate callbacks.
                        DispatchQueue.main.async { sendComposerText() }
                    },
                    onEndEditing: {
                        // Apple TV Remote can end dictation without emitting
                        // return. Submit the already-transcribed follow-up on
                        // that same first tick so a second microphone press is
                        // never required.
                        DispatchQueue.main.async { sendComposerText() }
                    },
                    autoSubmitBatchInput: true,
                    placeholder: pendingQuestion == nil ? "Reply…" : "Answer the runner…",
                    font: .systemFont(ofSize: 20),
                    textColor: .white,
                    tint: .white,
                    fieldBackgroundColor: .black,
                    fieldCornerRadius: 16,
                    fieldContentInset: UIEdgeInsets(top: 0, left: 16, bottom: 0, right: 16),
                    accessibilityIdentifier: "chat.reply"
                )
                    .focused($replyFocus, equals: .field)
                    .frame(maxWidth: .infinity, minHeight: 58, maxHeight: 58)
                    .focusEffectDisabled()
                    .onMoveCommand { direction in
                        if direction == .down,
                           !activeComposerText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                            replyFocus = .send
                        }
                    }
                Button(composerBusy ? "Sending…" : (pendingQuestion == nil ? "Send" : "Answer")) {
                    sendComposerText()
                }
                    .buttonStyle(.borderedProminent)
                    .disabled(composerBusy || activeComposerText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .focused($replyFocus, equals: .send)
                    .accessibilityIdentifier("chat.send-reply")
                Button { showTaskSettings = true } label: {
                    Image(systemName: "ellipsis")
                        .font(.system(size: 22, weight: .bold))
                        .frame(width: 48, height: 48)
                }
                .buttonStyle(.bordered)
                .focused($replyFocus, equals: .settings)
                .accessibilityLabel("Task settings")
                .accessibilityIdentifier("chat.followup-settings")
                }
            }
            HStack(spacing: 10) {
                Label(runnerLabel, systemImage: "terminal.fill")
                    .font(.system(size: 14, weight: .semibold))
                Spacer()
                if !console.isEmpty || runnerCoding {
                    Button(showConsole ? "Hide live console" : "Show live console") { showConsole.toggle() }
                        .font(.system(size: 14, weight: .semibold))
                }
            }
        }
        .padding(.horizontal, 48).padding(.vertical, 16)
        .background(.ultraThinMaterial)
    }

    private var composerBinding: Binding<String> {
        Binding(
            get: { pendingQuestion == nil ? reply : questionReply },
            set: { value in
                if pendingQuestion == nil { reply = value }
                else { questionReply = value }
            }
        )
    }

    private var activeComposerText: String {
        pendingQuestion == nil ? reply : questionReply
    }

    private var composerBusy: Bool { sending || answeringQuestion }

    @ViewBuilder
    private func runnerControlCard(_ mode: RunnerControlMode) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(mode == .exit ? "Exit runner session?" : mode == .effort ? "Choose reasoning level" : "Choose this conversation's model")
                    .font(.system(size: 18, weight: .bold))
                Spacer()
                Button("Close") { runnerControl = nil }
            }
            if runnerControlBusy {
                HStack { ProgressView(); Text("Checking the runner on this machine…") }
                    .foregroundStyle(.secondary)
            }
            if let runnerControlError {
                Text(runnerControlError).foregroundStyle(.orange)
            }
            if mode == .model, let catalog = runnerControlCatalog {
                if catalog.isAdopted == true {
                    Text("This is an adopted terminal. Change its model in the live Details view so Yaver never guesses at terminal menu positions.")
                        .foregroundStyle(.orange)
                }
                ForEach(catalog.models) { model in
                    Button {
                        chooseRunnerControlModel(model)
                    } label: {
                        HStack {
                            VStack(alignment: .leading) {
                                Text(model.name ?? model.id).font(.system(size: 17, weight: .semibold))
                                if model.name != nil { Text(model.id).font(.system(size: 12, design: .monospaced)).foregroundStyle(.secondary) }
                            }
                            Spacer()
                            if model.id == catalog.model { Image(systemName: "checkmark") }
                        }
                    }
                    .buttonStyle(.bordered)
                    .disabled(catalog.isAdopted == true || runnerControlBusy)
                }
            }
            if mode == .effort, let catalog = runnerControlCatalog,
               let model = catalog.models.first(where: { $0.id == runnerControlModel }) {
                ForEach(model.supportedReasoningEfforts ?? []) { effort in
                    Button(effort.reasoningEffort) {
                        applyRunnerModel(model.id, effort: effort.reasoningEffort)
                    }
                    .buttonStyle(.bordered)
                    .disabled(runnerControlBusy)
                }
            }
            if mode == .exit {
                Text("Stops this task's real runner seat. Yaver verifies it is gone before reporting success.")
                    .foregroundStyle(.secondary)
                Button("Exit and verify", role: .destructive) { exitRunnerSession() }
                    .buttonStyle(.borderedProminent)
                    .disabled(runnerControlBusy)
            }
        }
        .padding(18)
        .background(Color.blue.opacity(0.12), in: RoundedRectangle(cornerRadius: 16))
        .accessibilityIdentifier("chat.runner-control")
    }

    @ViewBuilder
    private func taskQuestionCard(_ question: TaskAgentQuestion) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            Label((question.header?.isEmpty == false ? question.header : nil) ?? "Runner question",
                  systemImage: question.isSecret ? "lock.fill" : "questionmark.bubble.fill")
                .font(.system(size: 16, weight: .bold))
                .foregroundStyle(question.isSecret ? .orange : .blue)
            Text(redactHomePaths(question.prompt))
                .font(.system(size: 20, weight: .semibold))
                .frame(maxWidth: 900, alignment: .leading)
            if let questionError {
                Text(questionError).font(.system(size: 14)).foregroundStyle(.orange)
            }
            if question.isSecret {
                Text("For privacy, credentials are answered on your phone or desktop—not on a shared television.")
                    .font(.system(size: 15)).foregroundStyle(.secondary)
            } else if question.kind.lowercased() == "choice" {
                VStack(alignment: .leading, spacing: 10) {
                    ForEach(Array((question.choices ?? []).enumerated()), id: \.offset) { _, choice in
                        Button {
                            if question.allowsMultipleChoices {
                                if questionSelections.contains(choice) { questionSelections.remove(choice) }
                                else { questionSelections.insert(choice) }
                            } else {
                                submitQuestionAnswer(choice)
                            }
                        } label: {
                            Label(
                                choice,
                                systemImage: questionSelections.contains(choice) ? "checkmark.circle.fill" : "circle"
                            )
                            .frame(maxWidth: 850, alignment: .leading)
                        }
                        .buttonStyle(.bordered)
                        .disabled(answeringQuestion)
                    }
                    if question.allowsMultipleChoices {
                        Button(answeringQuestion ? "Sending…" : "Send selected") {
                            let ordered = (question.choices ?? []).filter(questionSelections.contains)
                            submitQuestionAnswer(ordered.joined(separator: "; "))
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(answeringQuestion || questionSelections.isEmpty)
                        .accessibilityIdentifier("chat.question-send-selected")
                    }
                }
            } else {
                Text("Answer below to let the runner continue.")
                    .font(.system(size: 15)).foregroundStyle(.secondary)
            }
        }
        .padding(20)
        .background(Color.blue.opacity(0.12), in: RoundedRectangle(cornerRadius: 16))
        .accessibilityIdentifier("chat.agent-question")
    }

    private var selectedProject: ProjectSummary? {
        guard let path = pickedProjectPath else { return nil }
        return availableProjects.first(where: { $0.path == path })
    }

    private var selectedRunner: AgentRunnerSummary? {
        availableRunners.first(where: { $0.canonicalId == RegisteredRunner.canonical(pickedRunner) })
    }

    private var selectedModel: AgentRunnerModel? {
        selectedRunner?.models.first(where: { $0.id == pickedModel })
    }

    private var taskSettingsPanel: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    settingsMenuRow(icon: "folder", title: "Project", value: selectedProject?.name ?? "No project") {
                        Button {
                            pickedProjectPath = nil
                            settingsChanged = true
                        } label: {
                            if pickedProjectPath == nil { Label("No project", systemImage: "checkmark") }
                            else { Text("No project") }
                        }
                        if let boxId = store.runnerBox()?.id,
                           let latest = store.lastProject(for: boxId, projects: availableProjects),
                           let path = latest.path {
                            Button("Use latest · \(latest.name)") {
                                pickedProjectPath = path
                                settingsChanged = true
                            }
                        }
                        if !availableProjects.isEmpty { Divider() }
                        ForEach(availableProjects) { project in
                            Button {
                                pickedProjectPath = project.path
                                settingsChanged = true
                            } label: {
                                if project.path == pickedProjectPath { Label(project.name, systemImage: "checkmark") }
                                else { Text(project.name) }
                            }
                        }
                    }

                    settingsMenuRow(icon: "cpu", title: "Runner", value: selectedRunner?.displayName ?? "Choose runner") {
                        ForEach(availableRunners.filter(\.installed)) { runner in
                            Button {
                                pickedRunner = runner.canonicalId
                                pickedModel = preferredModel(in: runner)?.id ?? ""
                                settingsChanged = true
                            } label: {
                                if runner.canonicalId == RegisteredRunner.canonical(pickedRunner) {
                                    Label(runner.displayName, systemImage: "checkmark")
                                } else { Text(runner.displayName) }
                            }
                        }
                    }

                    settingsMenuRow(icon: "sparkles", title: "Model", value: selectedModel?.name ?? (pickedModel.isEmpty ? "Runner default" : pickedModel)) {
                        Button {
                            pickedModel = ""
                            settingsChanged = true
                        } label: {
                            if pickedModel.isEmpty { Label("Runner default", systemImage: "checkmark") }
                            else { Text("Runner default") }
                        }
                        if selectedRunner?.models.isEmpty == false { Divider() }
                        ForEach(selectedRunner?.models ?? []) { model in
                            Button {
                                pickedModel = model.id
                                settingsChanged = true
                            } label: {
                                if model.id == pickedModel { Label(model.name, systemImage: "checkmark") }
                                else { Text(model.name) }
                            }
                        }
                    }

                    VStack(alignment: .leading, spacing: 10) {
                        HStack {
                            Label("MCP tools", systemImage: "point.3.connected.trianglepath.dotted")
                                .font(.system(size: 22, weight: .semibold))
                            Spacer()
                            if let boxId = store.runnerBox()?.id,
                               store.lastMCPServersByDevice[boxId] != nil {
                                Button("Use latest") { useLatestMCP(for: boxId) }
                            }
                            if yaverMcpOn || !pickedMCPServers.isEmpty {
                                Button("Clear all") {
                                    yaverMcpOn = false
                                    pickedMCPServers.removeAll()
                                    settingsChanged = true
                                }
                            }
                        }
                        Text("Optional. No MCP is selected unless you choose one or tap Use latest.")
                            .font(.system(size: 14)).foregroundStyle(.secondary)
                        mcpToggle("Yaver MCP", selected: yaverMcpOn) {
                            yaverMcpOn.toggle(); settingsChanged = true
                        }
                        ForEach(availableMCPServers, id: \.self) { name in
                            mcpToggle(name, selected: pickedMCPServers.contains(name)) {
                                if pickedMCPServers.contains(name) { pickedMCPServers.remove(name) }
                                else { pickedMCPServers.insert(name) }
                                settingsChanged = true
                            }
                        }
                    }
                    .padding(22)
                    .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
                }
                .padding(40)
            }
            .navigationTitle("Task settings")
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { showTaskSettings = false } } }
        }
    }

    private func settingsMenuRow<Content: View>(
        icon: String, title: String, value: String,
        @ViewBuilder content: () -> Content
    ) -> some View {
        HStack(spacing: 18) {
            Image(systemName: icon).font(.system(size: 24, weight: .semibold)).foregroundStyle(.blue).frame(width: 44)
            Text(title).font(.system(size: 22, weight: .semibold))
            Spacer()
            Menu(content: content) {
                HStack(spacing: 8) { Text(value).lineLimit(1); Image(systemName: "chevron.down") }
                    .font(.system(size: 18, weight: .semibold))
            }
            .disabled(title == "Runner" && availableRunners.filter(\.installed).isEmpty)
        }
        .padding(22)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
    }

    private func mcpToggle(_ title: String, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack {
                Text(title).font(.system(size: 18, weight: .medium)); Spacer()
                Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                    .foregroundStyle(selected ? .blue : .secondary)
            }
            .padding(.horizontal, 16).padding(.vertical, 12)
        }
        .buttonStyle(.plain)
    }

    private func preferredModel(in runner: AgentRunnerSummary) -> AgentRunnerModel? {
        runner.models.first(where: { $0.isDefault == true }) ?? runner.models.first
    }

    private func useLatestMCP(for boxId: String) {
        guard let pref = store.lastMCPServersByDevice[boxId] else { return }
        yaverMcpOn = pref.includeYaverMcp ?? false
        pickedMCPServers = Set(pref.mcpServers ?? []).intersection(Set(availableMCPServers))
        settingsChanged = true
    }

    private func sendComposerText() {
        if let pendingQuestion {
            submitQuestionAnswer(questionReply, question: pendingQuestion)
        } else {
            sendReply()
        }
    }

    private func submitQuestionAnswer(_ rawAnswer: String, question explicitQuestion: TaskAgentQuestion? = nil) {
        guard let question = explicitQuestion ?? pendingQuestion, !question.isSecret else { return }
        let answer = rawAnswer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !answer.isEmpty, !answeringQuestion else { return }
        guard let client = store.runnerClient() else {
            questionError = "No machine selected"
            return
        }
        answeringQuestion = true
        questionError = nil
        Task {
            do {
                try await client.answerTaskQuestion(task.id, questionId: question.id, answer: answer)
                await MainActor.run {
                    if pendingQuestion?.id == question.id { pendingQuestion = nil }
                    questionReply = ""
                    questionSelections.removeAll()
                    answeringQuestion = false
                    status = "running"
                    // The runner was parked on the question. Reattach even if
                    // the old stream ended while the TV was answering.
                    reattachNonce += 1
                }
            } catch {
                await MainActor.run {
                    questionError = error.localizedDescription
                    if FailureSignals.isSessionScopeDenied(error) {
                        taskScopeDenied = true
                    }
                    answeringQuestion = false
                }
            }
        }
    }

    private var displayTurns: [TaskConversationTurn] {
        var rows = task.turns ?? []
        if rows.isEmpty, let title = task.title, !title.isEmpty {
            rows.append(TaskConversationTurn(role: "user", content: title, timestamp: nil))
        }
                    if !rows.contains(where: { $0.role == "assistant" }) {
            let answer = (task.resultText?.isEmpty == false ? task.resultText : task.output) ?? ""
            if !answer.isEmpty {
                rows.append(TaskConversationTurn(role: "assistant", content: answer, timestamp: nil))
            }
        }
        if let failure = taskFailureNotice,
           !rows.contains(where: { $0.role == "assistant" && $0.content == failure }) {
            rows.append(TaskConversationTurn(role: "assistant", content: failure, timestamp: nil))
        }
        for pending in task.pendingFollowUps ?? [] where !rows.contains(where: { $0.role == "user" && $0.content == pending.input }) {
            rows.append(TaskConversationTurn(role: "user", content: pending.input, timestamp: nil))
        }
        for optimistic in optimisticTurns where !rows.contains(where: { $0.role == optimistic.role && $0.content == optimistic.content }) {
            rows.append(optimistic)
        }
        let semantic = presentation.last(where: { $0.kind == "message" && $0.role == "assistant" })?.text ?? ""
        let live = (semantic.isEmpty ? liveAssistantText : semantic).trimmingCharacters(in: .whitespacesAndNewlines)
        if !live.isEmpty,
           !rows.contains(where: { $0.role == "assistant" && $0.content == live }) {
            rows.append(TaskConversationTurn(role: "assistant", content: live, timestamp: nil))
        }
        return rows
    }

    /// A runner can accept POST /tasks and fail later (for example when the
    /// provider rejects an exhausted credit balance). Older agents put that
    /// refusal only in raw stdout, leaving the TV with a completed-looking
    /// conversation and no explanation. Promote known terminal failures into
    /// the same assistant lane used by mobile/web; do not route billing or
    /// model failures through sign-in.
    private var taskFailureNotice: String? {
        guard ["failed", "stopped"].contains((status ?? task.status ?? "").lowercased()) else { return nil }
        let raw = [task.resultText, task.output, console]
            .compactMap { $0 }
            .filter { !$0.isEmpty }
            .joined(separator: "\n")
        let kind = FailureSignals.classifyRunnerFailure(raw)
        if let explanation = FailureSignals.explainRunnerFailure(kind) {
            return "The task stopped: \(explanation.reason) \(explanation.action)"
        }
        guard !raw.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return "The task stopped before it produced a result. Open the live console for details, then retry once the runner is ready."
        }
        return "The task stopped: \(redactHomePaths(raw.split(separator: "\n").last.map(String.init) ?? raw))"
    }

    private func bubble(_ turn: TaskConversationTurn) -> some View {
        let user = turn.role == "user"
        return HStack {
            if user { Spacer(minLength: 180) }
            VStack(alignment: .leading, spacing: 5) {
                Text(user ? "You" : "Yaver")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundStyle(user ? .blue : .secondary)
                Text(redactHomePaths(turn.content))
                    .font(.system(size: 17))
                    .frame(maxWidth: 900, alignment: .leading)
                    .accessibilityIdentifier(user ? "chat.user-turn" : "chat.assistant-turn")
            }
            .padding(18)
            .background(user ? Color.blue.opacity(0.2) : Color.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 16))
            if !user { Spacer(minLength: 180) }
        }
    }

    private func loadConfiguration() async {
        guard store.runnerBox() != nil, let client = store.runnerClient() else { return }
        async let projectRows: [ProjectSummary]? = try? client.listProjects()
        async let serverRows: [McpServerSummary]? = try? client.listMCPServers()
        async let runnerRows: AgentRunnerList? = try? client.listRunners()
        let loadedProjects = (await projectRows) ?? []
        let loadedServers = (await serverRows) ?? []
        let loadedRunners = (await runnerRows)?.runners.filter(\.installed) ?? []
        availableProjects = loadedProjects
        availableMCPServers = loadedServers.map(\.name)
        availableRunners = loadedRunners
        if pickedRunner.isEmpty {
            pickedRunner = RegisteredRunner.canonical(task.runner ?? "")
        }
        if pickedRunner.isEmpty {
            pickedRunner = RegisteredRunner.canonical((await runnerRows)?.default ?? loadedRunners.first?.id ?? "")
        }
        if pickedModel.isEmpty, let runner = selectedRunner {
            pickedModel = preferredModel(in: runner)?.id ?? ""
        }
    }

    private func sendReply() {
        let text = reply.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !sending else { return }
        if text.lowercased() == "/model" {
            reply = ""
            openRunnerControl(.model)
            return
        }
        if text.lowercased() == "/exit" {
            reply = ""
            openRunnerControl(.exit)
            return
        }
        guard let client = store.runnerClient() else {
            sendError = store.machineSplitActive
                ? "Your AI runner machine needs the relay to be reachable from this TV."
                : "No machine selected"
            return
        }

        let optimistic = TaskConversationTurn(
            role: "user", content: text,
            timestamp: ISO8601DateFormatter().string(from: Date()))
        optimisticTurns.append(optimistic)
        reply = ""
        sending = true
        sendError = nil
        parkedRunner = nil
        taskScopeDenied = false

        Task {
            do {
                switch tvChatFollowUpAction(
                    status: status ?? task.status,
                    runner: task.runner,
                    selectedRunner: pickedRunner,
                    settingsChanged: settingsChanged
                ) {
                case .continueCurrent:
                    try await client.continueTask(task.id, input: text)
                    await MainActor.run {
                        status = "running"
                        // A stale terminal status can route a valid resume down
                        // the in-place path after its prior SSE already ended.
                        // Cursor-based replay makes this restart lossless.
                        reattachNonce += 1
                    }
                case .settingsChangeBlocked(let message):
                    await MainActor.run {
                        optimisticTurns.removeAll { $0.id == optimistic.id }
                        if reply.isEmpty { reply = text }
                        sendError = message
                        sending = false
                    }
                    return
                }
                try? await Task.sleep(nanoseconds: 250_000_000)
                await refreshDetail()
                await MainActor.run { sending = false }
            } catch {
                await MainActor.run {
                    if let agentError = error as? AgentError, agentError.parked {
                        // The box kept the words and will replay them after
                        // runner auth recovers. Keep the optimistic user turn;
                        // restoring the text invites a duplicate execution.
                        let runner = agentError.runner ?? task.runner
                        let notice = tvParkedTurnNotice(
                            code: agentError.code,
                            runner: runner,
                            reauthable: agentError.reauthable
                        )
                        parkedRunner = notice.offersRunnerSignIn ? runner : nil
                        sendError = notice.line
                        sending = false
                        return
                    }
                    optimisticTurns.removeAll { $0.id == optimistic.id }
                    if reply.isEmpty { reply = text }
                    sendError = error.localizedDescription
                    taskScopeDenied = FailureSignals.isSessionScopeDenied(error)
                    sending = false
                }
            }
        }
    }

    private func openRunnerControl(_ mode: RunnerControlMode) {
        runnerControl = mode
        runnerControlError = nil
        runnerControlNotice = nil
        runnerControlBusy = true
        guard let client = store.runnerClient() else {
            runnerControlBusy = false
            runnerControlError = "No machine selected"
            return
        }
        Task {
            do {
                let catalog = try await client.taskRunnerControls(task.id)
                await MainActor.run {
                    runnerControlCatalog = catalog
                    runnerControlModel = catalog.model
                        ?? catalog.models.first(where: { $0.isDefault == true })?.id
                        ?? catalog.models.first?.id
                        ?? ""
                    runnerControlBusy = false
                }
            } catch {
                await MainActor.run {
                    runnerControlError = error.localizedDescription
                    runnerControlBusy = false
                }
            }
        }
    }

    private func chooseRunnerControlModel(_ model: TaskRunnerControlModel) {
        runnerControlModel = model.id
        if runnerControlCatalog?.runnerId == "codex",
           model.supportedReasoningEfforts?.isEmpty == false {
            runnerControl = .effort
            return
        }
        applyRunnerModel(model.id, effort: nil)
    }

    private func applyRunnerModel(_ model: String, effort: String?) {
        guard let client = store.runnerClient() else {
            runnerControlError = "No machine selected"
            return
        }
        runnerControlBusy = true
        runnerControlError = nil
        Task {
            do {
                let result = try await client.applyTaskRunnerControl(
                    task.id, control: "model", model: model, reasoningEffort: effort)
                guard result.ok else { throw AgentError(message: result.error ?? "The model could not be changed.") }
                await refreshDetail()
                await MainActor.run {
                    runnerControl = nil
                    runnerControlBusy = false
                    runnerControlNotice = "Model set to \(result.display ?? [model, effort].compactMap { $0 }.joined(separator: " · ")) for the next turn."
                }
            } catch {
                await MainActor.run {
                    runnerControlError = error.localizedDescription
                    runnerControlBusy = false
                }
            }
        }
    }

    private func exitRunnerSession() {
        guard let client = store.runnerClient() else {
            runnerControlError = "No machine selected"
            return
        }
        runnerControlBusy = true
        runnerControlError = nil
        Task {
            do {
                let result = try await client.applyTaskRunnerControl(task.id, control: "exit", confirmed: true)
                guard result.ok, result.verified == true else {
                    throw AgentError(message: result.error ?? "The runner did not verify that it exited.")
                }
                await refreshDetail()
                await MainActor.run {
                    status = result.status ?? "stopped"
                    runnerControl = nil
                    runnerControlBusy = false
                    runnerControlNotice = result.alreadyExited == true
                        ? "Runner session was already exited; the agent verified no seat remains."
                        : "Runner session exited and verified."
                }
            } catch {
                await MainActor.run {
                    runnerControlError = error.localizedDescription
                    runnerControlBusy = false
                }
            }
        }
    }

    private func refreshDetail() async {
        guard let client = store.runnerClient() else { return }
        do {
            let detail = try await client.task(task.id)
            await MainActor.run {
                task = detail
                status = detail.status
                presentation = detail.presentation ?? presentation
                optimisticTurns.removeAll { optimistic in
                    (detail.turns ?? []).contains { $0.role == optimistic.role && $0.content == optimistic.content }
                        || (detail.pendingFollowUps ?? []).contains { $0.input == optimistic.content }
                }
            }
        } catch {
            // The SSE still carries the live operation. A detail refresh is
            // advisory and must never replace a working chat with an error.
        }
    }

    private func startStream() async {
        stream?.cancel()
        guard let client = store.runnerClient() else {
            streamMessage = "No machine selected"
            return
        }
        let since = rawCursor
        let currentID = task.id
        let s = await client.subscribeTaskOutput(
            taskId: currentID,
            since: transcriptCursor > 0 ? transcriptCursor : nil,
            rawSince: since,
            onRaw: { text, offset, full in
                Task { @MainActor in
                    if full { console = String(text.prefix(Self.consoleCap)) }
                    else { console = String((console + text).suffix(Self.consoleCap)) }
                    rawCursor = offset
                    streamMessage = nil
                    streamRetrying = false
                    reattachAttempt = 0
                }
            },
            onData: { text, offset, full in
                Task { @MainActor in
                    if full {
                        liveAssistantText = String(text.suffix(128 * 1024))
                    } else {
                        liveAssistantText = String((liveAssistantText + text).suffix(128 * 1024))
                    }
                    if let offset {
                        transcriptCursor = offset
                    } else {
                        // Older agents omitted groomed offsets. Still advance a
                        // byte cursor so a relay reattach cannot duplicate the
                        // assistant text already visible on the TV.
                        transcriptCursor += text.utf8.count
                    }
                    streamMessage = nil
                    streamRetrying = false
                }
            },
            onDone: { doneStatus in
                Task { @MainActor in
                    status = doneStatus
                    streamMessage = nil
                    streamRetrying = false
                    reattachAttempt = 0
                    await refreshDetail()
                    liveAssistantText = ""
                    // A queued follow-up rolls the agent onto a fresh output
                    // channel. Older agents close the old SSE with a nonterminal
                    // `done`; follow the task, not that obsolete channel.
                    if tvTaskStreamShouldReattachAfterDone(status ?? doneStatus) {
                        reattachNonce += 1
                    }
                }
            },
            onQuestion: { question in
                Task { @MainActor in
                    let isReplay = pendingQuestion?.id == question.id
                    pendingQuestion = question
                    // Re-subscribing replays a still-pending question. Keep a
                    // half-dictated answer or selected choices across that
                    // transport recovery; only a genuinely new ask resets UI.
                    if !isReplay {
                        questionReply = ""
                        questionSelections.removeAll()
                        questionError = nil
                    }
                }
            },
            onQuestionClosed: { questionId in
                Task { @MainActor in
                    guard questionId == nil || pendingQuestion?.id == questionId else { return }
                    pendingQuestion = nil
                    questionReply = ""
                    questionSelections.removeAll()
                    questionError = nil
                }
            },
            onPresentation: { event in
                Task { @MainActor in
                    if event.type == "presentation_snapshot" {
                        presentation = event.messages ?? []
                    } else if let message = event.message {
                        if let index = presentation.firstIndex(where: { $0.id == message.id }) {
                            if event.op == "append" {
                                let previous = presentation[index]
                                presentation[index] = TaskPresentationMessage(
                                    id: previous.id, kind: message.kind, role: message.role ?? previous.role,
                                    text: previous.text + message.text, phase: message.phase, state: message.state,
                                    runner: message.runner, project: message.project, machine: message.machine,
                                    platform: message.platform, surface: message.surface,
                                    createdAt: previous.createdAt, updatedAt: message.updatedAt
                                )
                            } else {
                                presentation[index] = message
                            }
                        } else {
                            presentation.append(message)
                        }
                    }
                }
            },
            onEnd: { kind, reason in
                Task { @MainActor in
                    await handleStreamEnd(kind, reason)
                }
            }
        )
        stream = s
    }

    /// Task streams can cross a relay and close after the runner succeeded but
    /// before the terminal `done` frame reaches the TV. The agent retains both
    /// output lanes, so re-subscribing with `rawSince` is lossless. Task chat
    /// used to ignore the shared recovery policy and strand the couch at a
    /// manual Reattach button even though WebPreview already self-healed.
    @MainActor
    private func handleStreamEnd(_ kind: FailureSignals.StreamEndKind, _ cause: String?) async {
        // Probe the real task operation before claiming its work is still
        // running. In the common dropped-final-frame case this refresh both
        // discovers the terminal status and seeds retained output.
        if kind == .interrupted {
            await refreshDetail()
            if !runnerCoding {
                reattachTask?.cancel()
                reattachAttempt = 0
                streamRetrying = false
                streamMessage = nil
                return
            }
        }
        let plan = FailureSignals.planStreamRecovery(
            end: kind,
            attempt: reattachAttempt,
            cause: cause
        )
        switch plan {
        case .idle:
            streamMessage = nil
            streamRetrying = false
        case let .reattach(_, delayMs, message):
            streamMessage = message
            streamRetrying = true
            reattachAttempt += 1
            reattachTask?.cancel()
            reattachTask = Task {
                try? await Task.sleep(nanoseconds: UInt64(delayMs) * 1_000_000)
                guard !Task.isCancelled else { return }
                await startStream()
            }
        case let .giveUp(message):
            streamMessage = message
            streamRetrying = false
        }
    }

    private var cleanConsole: String {
        Self.stripANSI(console)
    }

    private var runnerCoding: Bool {
        tvTaskIsRunnerCoding(status ?? task.status)
    }

    private static func stripANSI(_ s: String) -> String {
        var out = ""
        var state = 0 // 0=text 1=esc 2=csi 3=osc
        for ch in s.unicodeScalars {
            switch state {
            case 0:
                if ch == "\u{1B}" { state = 1 } else { out.unicodeScalars.append(ch) }
            case 1:
                if ch == "[" { state = 2 }
                else if ch == "]" { state = 3 }
                else { state = 0 }
            case 2:
                if ch.value >= 0x40 && ch.value <= 0x7E { state = 0 }
            case 3:
                if ch == "\u{07}" { state = 0 }
                else if ch == "\u{1B}" { state = 1 }
                else if ch == "\\" { state = 0 }
            default: state = 0
            }
        }
        return out
    }

    private func color(for value: String?) -> Color {
        switch (value ?? "").lowercased() {
        case "running": return .green
        case "queued": return .blue
        case "review": return .purple
        case "completed": return .gray
        case "failed", "stopped": return .red
        default: return .secondary
        }
    }

    private var runnerLabel: String {
        switch task.runner?.lowercased() {
        case "claude", "claude-code": return "Claude Code"
        case "codex": return "Codex"
        case "opencode": return "OpenCode"
        case .some(let value) where !value.isEmpty: return value
        default: return "Runner"
        }
    }

    private func runnerDisplayName(_ runner: String) -> String {
        switch RegisteredRunner.canonical(runner) {
        case "claude": return "Claude Code"
        case "codex": return "Codex"
        case "opencode": return "OpenCode"
        case let value where !value.isEmpty: return value
        default: return "the runner"
        }
    }

    private var modelLabel: String {
        guard let model = task.model, !model.isEmpty else { return "" }
        return model.split(separator: "/").last.map(String.init) ?? model
    }

    private var modelEffortLabel: String {
        [modelLabel, task.reasoningEffort].compactMap { value in
            guard let value, !value.isEmpty else { return nil }
            return value
        }.joined(separator: " · ")
    }

    private var statusLabel: String {
        let value = status ?? task.status ?? ""
        guard !value.isEmpty else { return "" }
        return value.prefix(1).uppercased() + value.dropFirst()
    }
}
