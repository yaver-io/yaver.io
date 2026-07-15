// VisionSessionView.swift — compact prompt surface for an existing runner
// session. It uses the shared SessionClient but keeps the UI visionOS-native
// and dependency-light.

import SwiftUI

struct VisionSessionView: View {
    @EnvironmentObject var store: YaverStore
    @Environment(\.dismiss) private var dismiss

    @State private var prompt = ""
    @State private var pane = ""
    @State private var sessionName = ""
    @State private var runnerName = ""
    @State private var sessions: [RunnerSession] = []
    @State private var selectedSession = ""
    @State private var awaitingChoice = false
    @State private var options: [String] = []
    @State private var loading = false
    @State private var error: String?

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
    }

    private var header: some View {
        HStack {
            VStack(alignment: .leading, spacing: 4) {
                Text("Live Session")
                    .font(.largeTitle.bold())
                Text(headerSubtitle)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
            if loading {
                ProgressView()
            }
            Button {
                dismiss()
            } label: {
                Label("Close", systemImage: "xmark")
            }
        }
    }

    private var paneView: some View {
        ScrollView {
            Text(pane.isEmpty ? emptyPaneText : pane)
                .font(.system(size: 15, design: .monospaced))
                .frame(maxWidth: .infinity, alignment: .leading)
                .textSelection(.enabled)
                .padding(18)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
    }

    private var composer: some View {
        HStack(spacing: 12) {
            TextField("Ask the active coding session...", text: $prompt, axis: .vertical)
                .lineLimit(1...4)
                .textFieldStyle(.roundedBorder)
                .onSubmit { Task { await sendPrompt() } }

            Button {
                Task { await sendPrompt() }
            } label: {
                Label("Send", systemImage: "paperplane.fill")
            }
            .buttonStyle(.borderedProminent)
            .disabled(prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || loading || selectedSession.isEmpty)
        }
    }

    private var sessionPicker: some View {
        HStack(spacing: 12) {
            Picker("Session", selection: $selectedSession) {
                if sessions.isEmpty {
                    Text("No sessions").tag("")
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
            Text("The session is waiting for a choice.")
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

    private var headerSubtitle: String {
        if !sessionName.isEmpty {
            return [sessionName, runnerName].filter { !$0.isEmpty }.joined(separator: " / ")
        }
        if let selected = sessions.first(where: { $0.name == selectedSession }) {
            return selected.label
        }
        return store.selectedBox.map { "on \($0.name)" } ?? "No machine selected"
    }

    private var emptyPaneText: String {
        if selectedSession.isEmpty {
            return "Select a live runner session on the selected machine."
        }
        return "Send a prompt to \(selectedSession)."
    }

    private func loadSessions() async {
        error = nil
        do {
            guard let agentClient else { throw AgentError(message: "No machine selected") }
            let result = try await agentClient.runnerSessions()
            sessions = result.sessions ?? []
            if selectedSession.isEmpty || !sessions.contains(where: { $0.name == selectedSession }) {
                selectedSession = sessions.first?.name ?? ""
            }
        } catch {
            sessions = []
            selectedSession = ""
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
        prompt = ""
        loading = true
        error = nil
        defer { loading = false }
        do {
            guard let sessionClient else { throw AgentError(message: "No machine selected") }
            apply(try await sessionClient.sendText(text, session: selectedSession))
        } catch {
            self.error = error.localizedDescription
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
            self.error = error.localizedDescription
        }
    }

    private func apply(_ result: SessionTurnResult) {
        if let session = result.session { sessionName = session }
        if let runner = result.runner { runnerName = runner }
        if let pane = result.pane { self.pane = pane }
        awaitingChoice = result.awaitingChoice == true
        options = result.options ?? []
        if let err = result.error, result.ok == false {
            error = err
        }
    }
}
