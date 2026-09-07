// VisionSessionView.swift — compact task conversation surface. The Go agent's
// /tasks index is authoritative; tmux remains an implementation detail used
// only to stream raw runner evidence for that task.

import SwiftUI

struct VisionSessionView: View {
    @EnvironmentObject var store: YaverStore
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase

    @State private var prompt = ""
    @State private var pane = ""
    @State private var narrative = ""
    @State private var runnerDetailsOpen = false
    @State private var sessionName = ""
    @State private var runnerName = ""
    @State private var runnerModel = ""
    @State private var runnerEffort = ""
    @State private var sessions: [RunnerSession] = []
    @State private var selectedSession = ""
    @State private var awaitingChoice = false
    @State private var options: [String] = []
    @State private var loading = false
    @State private var error: String?
    @State private var runnerControl: String?
    @State private var runnerControlCatalog: TaskRunnerControlCatalog?
    @State private var runnerControlModel = ""
    @State private var runnerControlBusy = false
    @StateObject private var dictation = DictationSession()
    /// What the user had typed before the mic opened, so a transcript APPENDS
    /// rather than overwriting work they already did by hand.
    @State private var typedBeforeDictation = ""

    private var sessionClient: SessionClient? {
        guard let box = store.selectedBox else { return nil }
        return SessionClient(token: store.token, box: box)
    }

    private var agentClient: AgentClient? {
        store.client()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            header
            sessionPicker
            paneView

            if let runnerControl {
                runnerControlView(runnerControl)
            }

            if awaitingChoice {
                choices
            } else {
                composer
            }

            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                    .font(.footnote)
            }
        }
        .padding(28)
        .frame(minWidth: 760, minHeight: 620)
        .glassBackgroundEffect()
        .task(id: store.selectedBox?.id) { await loadSessions() }
        .task(id: selectedSession) { await streamSelectedSession() }
        .onDisappear { dictation.stop() }
        .onChange(of: scenePhase) { _, phase in
            if phase != .active { dictation.stop() }
        }
    }

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: 4) {
                Text("Task")
                    .font(.largeTitle.bold())
                Text(headerSubtitle)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
            if loading {
                ProgressView()
            }
            Button(modelEffortLabel.isEmpty ? "Model" : modelEffortLabel) {
                Task { await openRunnerControl("model") }
            }
            .disabled(selectedSession.isEmpty)
            Button("Exit", role: .destructive) {
                runnerControl = "exit"
            }
            .disabled(selectedSession.isEmpty)
            Button {
                dismiss()
            } label: {
                Label("Close", systemImage: "xmark")
            }
        }
    }

    private var paneView: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Text(narrative.isEmpty ? emptyPaneText : narrative)
                    .font(.body)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .textSelection(.enabled)

                if !pane.isEmpty {
                    DisclosureGroup(isExpanded: $runnerDetailsOpen) {
                        Text(pane)
                            .font(.system(size: 15, design: .monospaced))
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .textSelection(.enabled)
                            .padding(.top, 10)
                    } label: {
                        Label("Runner details", systemImage: "terminal")
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .padding(18)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
    }

    private var composer: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 12) {
                TextField("Continue this task...", text: $prompt, axis: .vertical)
                    .lineLimit(1...4)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { Task { await sendPrompt() } }

                // DICTATION. In a headset the alternative is the floating
                // virtual keyboard, for the longest strings Yaver asks anyone
                // to type — so this is the primary input here, not a nicety.
                //
                // Shown ONLY when this headset can transcribe without sending
                // audio anywhere (canDictatePrivately). An offered control that
                // will refuse is worse than no control: it teaches the user the
                // product is unreliable rather than that their language pack is
                // missing. When it is hidden, typing still works exactly as before.
                if dictation.canDictatePrivately {
                    Button {
                        Task { await toggleDictation() }
                    } label: {
                        Label(dictation.listening ? "Stop" : "Speak",
                              systemImage: dictation.listening ? "stop.circle.fill" : "mic.fill")
                            .labelStyle(.iconOnly)
                    }
                    .buttonStyle(.bordered)
                    .tint(dictation.listening ? .red : nil)
                    .accessibilityLabel(dictation.listening ? "Stop dictation" : "Dictate a prompt")
                    .disabled(loading || selectedSession.isEmpty)
                } else if !selectedSession.isEmpty {
                    Label("On-device dictation is unavailable for this language on this headset. Download the language in Settings › General › Keyboard › Dictation, or use the virtual keyboard.", systemImage: "mic.slash")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Button {
                    Task { await sendPrompt() }
                } label: {
                    Label("Send", systemImage: "paperplane.fill")
                }
                .buttonStyle(.borderedProminent)
                .disabled(prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || loading || selectedSession.isEmpty)
            }

            // Narrate the wait. A mic that is listening with no visible state is
            // the same defect as a spinner with no elapsed time.
            if dictation.listening {
                Label("Listening — on-device only, audio never leaves this headset",
                      systemImage: "waveform")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let speechError = dictation.error {
                Text(speechError)
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
        }
        // The transcript APPENDS to whatever was typed by hand. Assigning it
        // straight to `prompt` would delete a half-written prompt the moment the
        // user reached for the mic to finish it — destroying work is a far worse
        // failure than a clumsy concatenation.
        .onChange(of: dictation.transcript) { _, spoken in
            guard dictation.listening || !spoken.isEmpty else { return }
            let base = typedBeforeDictation.trimmingCharacters(in: .whitespacesAndNewlines)
            prompt = base.isEmpty ? spoken : base + " " + spoken
        }
    }

    /// Start/stop dictation, moving the transcript into the prompt field.
    ///
    /// The transcript REPLACES nothing the user typed by hand: it appends, so a
    /// half-typed prompt finished by voice is not silently destroyed.
    private func toggleDictation() async {
        if dictation.listening {
            dictation.stop()
            return
        }
        typedBeforeDictation = prompt
        await dictation.start()
    }

    private var sessionPicker: some View {
        HStack(spacing: 12) {
            Picker("Task", selection: $selectedSession) {
                if sessions.isEmpty {
                    Text("No tasks").tag("")
                } else {
                    ForEach(sessions) { session in
                        Text(session.label).tag(session.name)
                    }
                }
            }
            .pickerStyle(.menu)
            .disabled(sessions.isEmpty || loading)

            Button {
                Task { await loadSessions() }
            } label: {
                Label("Refresh", systemImage: "arrow.clockwise")
            }
            .disabled(loading)
        }
    }

    private var choices: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("The task is waiting for a choice.")
                .foregroundStyle(.secondary)
            ScrollView(.horizontal) {
                HStack(spacing: 12) {
                    ForEach(Array(options.enumerated()), id: \.offset) { index, option in
                        Button {
                            Task { await sendChoice(String(index + 1)) }
                        } label: {
                            Text(option)
                                .lineLimit(2)
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(loading)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func runnerControlView(_ mode: String) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(mode == "exit" ? "Exit runner session?" : mode == "effort" ? "Choose reasoning level" : "Choose this conversation's model")
                    .font(.headline)
                Spacer()
                Button("Close") { runnerControl = nil }
            }
            if runnerControlBusy { ProgressView("Checking the runner on this machine…") }
            if mode == "model", let catalog = runnerControlCatalog {
                if catalog.isAdopted == true {
                    Text("This terminal was adopted. Change its model in Runner details so Yaver never guesses at terminal menu positions.")
                        .font(.footnote).foregroundStyle(.orange)
                }
                ScrollView(.horizontal) {
                    HStack {
                        ForEach(catalog.models) { model in
                            Button(model.name ?? model.id) {
                                runnerControlModel = model.id
                                if catalog.runnerId == "codex", model.supportedReasoningEfforts?.isEmpty == false {
                                    runnerControl = "effort"
                                } else {
                                    Task { await applyRunnerModel(model.id, effort: nil) }
                                }
                            }
                            .disabled(catalog.isAdopted == true || runnerControlBusy)
                        }
                    }
                }
            }
            if mode == "effort", let catalog = runnerControlCatalog,
               let model = catalog.models.first(where: { $0.id == runnerControlModel }) {
                HStack {
                    ForEach(model.supportedReasoningEfforts ?? []) { effort in
                        Button(effort.reasoningEffort) {
                            Task { await applyRunnerModel(model.id, effort: effort.reasoningEffort) }
                        }
                    }
                }
            }
            if mode == "exit" {
                Text("Stops the real runner seat. Yaver verifies it is gone before reporting success.")
                    .font(.footnote).foregroundStyle(.secondary)
                Button("Exit and verify", role: .destructive) { Task { await exitRunnerSession() } }
                    .buttonStyle(.borderedProminent)
                    .disabled(runnerControlBusy)
            }
        }
        .padding(14)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 14))
    }

    private var headerSubtitle: String {
        if !sessionName.isEmpty {
            return [sessionName, modelEffortLabel.isEmpty ? runnerName : modelEffortLabel].filter { !$0.isEmpty }.joined(separator: " / ")
        }
        if let selected = sessions.first(where: { $0.name == selectedSession }) {
            return selected.label
        }
        return store.selectedBox.map { "on \($0.name)" } ?? "No machine selected"
    }

    private var modelEffortLabel: String {
        [runnerModel, runnerEffort].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    private var emptyPaneText: String {
        if selectedSession.isEmpty {
            return "Select a task on the selected machine."
        }
        if loading { return "The runner is working…" }
        if !pane.isEmpty {
            return "The runner is active. Open Runner details for its live terminal output."
        }
        return "Continue this task."
    }

    private func loadSessions() async {
        error = nil
        do {
            guard let agentClient else { throw AgentError(message: "No machine selected") }
            let tasks = try await agentClient.listTasks()
            sessions = tasks.compactMap { task in
                guard let tmux = task.tmuxSession, !tmux.isEmpty else { return nil }
                return RunnerSession(
                    name: tmux,
                    runner: task.runner,
                    attached: true,
                    taskId: task.id,
                    model: task.model,
                    taskTitle: task.safeTitle
                )
            }
            if selectedSession.isEmpty || !sessions.contains(where: { $0.name == selectedSession }) {
                selectedSession = sessions.first?.name ?? ""
            }
        } catch {
            sessions = []
            selectedSession = ""
            if store.handleAuthenticationFailure(error) { return }
            self.error = error.localizedDescription
        }
    }

    private func sendPrompt() async {
        let text = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        guard !selectedSession.isEmpty else {
            error = "Select a live runner session first."
            return
        }
        if text.lowercased() == "/model" {
            prompt = ""
            await openRunnerControl("model")
            return
        }
        if text.lowercased() == "/exit" {
            prompt = ""
            runnerControl = "exit"
            return
        }
        prompt = ""
        loading = true
        error = nil
        defer { loading = false }
        do {
            guard let taskId = selectedTaskID, let agentClient else {
                throw AgentError(message: "This task no longer has an owning runner conversation.")
            }
            try await agentClient.continueTask(taskId, input: text, mode: "")
            narrative = "Follow-up sent to the same Yaver task."
        } catch {
            if store.handleAuthenticationFailure(error) { return }
            self.error = error.localizedDescription
        }
    }

    private func streamSelectedSession() async {
        guard !selectedSession.isEmpty, let agentClient else { return }
        let watched = selectedSession
        let stream = await agentClient.subscribeTmuxPane(
            session: watched,
            onPane: { frame in
                Task { @MainActor in
                    guard self.selectedSession == frame.sessionName else { return }
                    self.sessionName = frame.sessionName
                    self.runnerName = frame.agent ?? self.runnerName
                    self.runnerModel = frame.model ?? self.runnerModel
                    self.pane = frame.preview.map(redactHomePaths) ?? self.pane
                    self.awaitingChoice = frame.status == "awaiting-input"
                    self.options = (frame.options ?? []).map(redactHomePaths)
                    if frame.status == "dead" {
                        self.error = frame.statusReason ?? "The coding session closed."
                    }
                }
            },
            onDone: { reason in
                Task { @MainActor in
                    self.error = reason ?? "The coding session closed."
                }
            },
            onEnd: { kind, reason in
                guard case .interrupted = kind else { return }
                Task { @MainActor in
                    self.error = reason ?? "The live session stream was interrupted."
                }
            }
        )
        await withTaskCancellationHandler {
            await stream.value
        } onCancel: {
            stream.cancel()
        }
    }

    private func sendChoice(_ choice: String) async {
        guard !selectedSession.isEmpty else {
            error = "Select a live runner session first."
            return
        }
        loading = true
        error = nil
        defer { loading = false }
        do {
            guard let sessionClient else { throw AgentError(message: "No machine selected") }
            apply(try await sessionClient.sendChoice(choice, session: selectedSession))
        } catch {
            if store.handleAuthenticationFailure(error) { return }
            self.error = error.localizedDescription
        }
    }

    private func apply(_ result: SessionTurnResult) {
        if let session = result.session { sessionName = session }
        if let runner = result.runner { runnerName = runner }
        if let spoken = result.spoken { narrative = redactHomePaths(spoken) }
        if let pane = result.pane { self.pane = redactHomePaths(pane) }
        awaitingChoice = result.awaitingChoice == true
        options = result.options ?? []
        if let err = result.error, result.ok == false {
            error = err
        }
    }

    private var selectedTaskID: String? {
        sessions.first(where: { $0.name == selectedSession })?.taskId.flatMap { $0.isEmpty ? nil : $0 }
    }

    private func openRunnerControl(_ mode: String) async {
        runnerControl = mode
        runnerControlCatalog = nil
        error = nil
        guard mode == "model" else { return }
        guard let taskId = selectedTaskID else {
            error = "This is an adopted live terminal. Open Runner details to use its own model menu; Yaver will not guess at menu positions."
            return
        }
        guard let agentClient else {
            error = "No machine selected"
            return
        }
        runnerControlBusy = true
        defer { runnerControlBusy = false }
        do {
            let catalog = try await agentClient.taskRunnerControls(taskId)
            runnerControlCatalog = catalog
            runnerControlModel = catalog.model
                ?? catalog.models.first(where: { $0.isDefault == true })?.id
                ?? catalog.models.first?.id
                ?? ""
            runnerModel = catalog.model ?? runnerModel
            runnerEffort = catalog.reasoningEffort ?? runnerEffort
        } catch {
            if store.handleAuthenticationFailure(error) { return }
            self.error = error.localizedDescription
        }
    }

    private func applyRunnerModel(_ model: String, effort: String?) async {
        guard let taskId = selectedTaskID, let agentClient else {
            error = "This terminal does not have a task-scoped model control."
            return
        }
        runnerControlBusy = true
        defer { runnerControlBusy = false }
        do {
            let result = try await agentClient.applyTaskRunnerControl(
                taskId, control: "model", model: model, reasoningEffort: effort)
            guard result.ok else { throw AgentError(message: result.error ?? "The model could not be changed.") }
            runnerModel = result.model ?? model
            runnerEffort = result.reasoningEffort ?? effort ?? ""
            narrative = "Model set to \(result.display ?? model) for the next turn."
            runnerControl = nil
        } catch {
            if store.handleAuthenticationFailure(error) { return }
            self.error = error.localizedDescription
        }
    }

    private func exitRunnerSession() async {
        runnerControlBusy = true
        defer { runnerControlBusy = false }
        do {
            if let taskId = selectedTaskID, let agentClient {
                let result = try await agentClient.applyTaskRunnerControl(taskId, control: "exit", confirmed: true)
                guard result.ok, result.verified == true else {
                    throw AgentError(message: result.error ?? "The runner did not verify that it exited.")
                }
            } else {
                guard let sessionClient else { throw AgentError(message: "No machine selected") }
                let result = try await sessionClient.closeSession(selectedSession)
                guard result.ok == true, result.sent == "close" else {
                    throw AgentError(message: result.error ?? "The runner did not verify that it exited.")
                }
            }
            narrative = "Runner session exited and verified."
            runnerControl = nil
            await loadSessions()
        } catch {
            if store.handleAuthenticationFailure(error) { return }
            self.error = error.localizedDescription
        }
    }
}
