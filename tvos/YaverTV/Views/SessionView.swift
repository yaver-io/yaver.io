// SessionView.swift — the "lean-back coding session" screen.
//
// This is the product on tvOS (docs/yaver-tvos-surface.md §4): send a prompt
// to the live coding session, see the pane, pick from menus, hear the result.
//
// Layout:
//   ┌──────────────────────────────────────────┐
//   │  Session: yaver-codex (codex)            │
//   │  ──────────────────────────────────────  │
//   │  <pane tail, monospaced, scrollable>     │
//   │                                          │
//   │  ──────────────────────────────────────  │
//   │  [TextField: dictate a prompt]     [Send]│
//   │                                          │
//   │  (when awaitingChoice:)                  │
//   │  [1. Yes, I trust this folder]           │
//   │  [2. No, exit]                           │
//   └──────────────────────────────────────────┘
//
// Input: the Siri Remote's mic button on the TextField invokes tvOS system
// dictation — the ONLY speech input path on a TV (no AVAudioSession mic access,
// docs/yaver-tvos-surface.md §1.1/§1.2). No hold-to-talk, no custom STT.
//
// Output: AVSpeechSynthesizer speaks a one-sentence summary of the pane on
// every non-awaitingChoice reply (docs/yaver-tvos-surface.md §1.3). The full
// pane is shown for visual reading; the spoken summary is the lean-back layer.
//
// Menus: awaitingChoice + options[] renders as focusable buttons. A D-pad is
// good at exactly one thing — picking from a short list. Menus chain and
// renumber; we always render the current options[] from the server, never
// assume option 1 means yes (docs/yaver-tvos-surface.md §2.2).

import SwiftUI

struct SessionView: View {
    @EnvironmentObject var store: YaverStore

    @State private var prompt = ""
    @State private var pane = ""
    @State private var sessionName = ""
    @State private var runnerName = ""
    @State private var awaitingChoice = false
    @State private var options: [String] = []
    @State private var sent = false
    @State private var loading = false
    @State private var error: String?

    private var client: SessionClient? {
        guard let box = store.selectedBox else { return nil }
        return SessionClient(token: store.token, box: box)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider().background(.gray.opacity(0.3))
            paneView
            Divider().background(.gray.opacity(0.3))
            if awaitingChoice {
                optionsView
            } else {
                promptBar
            }
            if let error {
                Text(error)
                    .font(.system(size: 16, design: .monospaced))
                    .foregroundStyle(.red)
                    .padding(.horizontal, 48).padding(.bottom, 12)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: 12) {
            Image(systemName: "terminal")
                .font(.system(size: 28))
                .foregroundStyle(.green)
            VStack(alignment: .leading, spacing: 2) {
                Text("Session")
                    .font(.system(size: 28, weight: .bold))
                if !sessionName.isEmpty {
                    Text("\(sessionName)\(runnerName.isEmpty ? "" : " · \(runnerName)")")
                        .font(.system(size: 16))
                        .foregroundStyle(.secondary)
                } else {
                    Text(store.selectedBox.map { "on \($0.name)" } ?? "No box selected")
                        .font(.system(size: 16))
                        .foregroundStyle(.secondary)
                }
            }
            Spacer()
            if loading {
                ProgressView().scaleEffect(1.3)
            }
        }
        .padding(.horizontal, 48).padding(.vertical, 20)
    }

    // MARK: - Pane

    private var paneView: some View {
        ScrollView {
            Text(pane.isEmpty ? "Send a prompt to start the session." : pane)
                .font(.system(size: 20, design: .monospaced))
                .frame(maxWidth: .infinity, alignment: .leading)
                .textSelection(.enabled)
                .padding(24)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Prompt bar

    private var promptBar: some View {
        HStack(spacing: 16) {
            TextField("Dictate a prompt (press mic on Siri Remote)", text: $prompt)
                .textFieldStyle(.plain)
                .font(.system(size: 22))
                .padding(.horizontal, 20).padding(.vertical, 14)
                .background(.gray.opacity(0.15), in: RoundedRectangle(cornerRadius: 12))
            Button {
                Task { await sendPrompt() }
            } label: {
                Label("Send", systemImage: "paperplane.fill")
                    .font(.system(size: 20, weight: .semibold))
                    .padding(.horizontal, 28).padding(.vertical, 14)
            }
            .buttonStyle(.borderedProminent)
            .disabled(prompt.trimmingCharacters(in: .whitespaces).isEmpty || loading)
        }
        .padding(.horizontal, 48).padding(.vertical, 16)
    }

    // MARK: - Options (awaitingChoice)

    private var optionsView: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Choose an option")
                .font(.system(size: 18, weight: .semibold))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 48)
            ScrollView(.horizontal) {
                HStack(spacing: 16) {
                    ForEach(Array(options.enumerated()), id: \.offset) { idx, opt in
                        Button {
                            Task { await sendChoice(String(idx + 1)) }
                        } label: {
                            Text(opt)
                                .font(.system(size: 20))
                                .padding(.horizontal, 24).padding(.vertical, 14)
                        }
                        .buttonStyle(.bordered)
                    }
                }
                .padding(.horizontal, 48)
            }
        }
        .padding(.vertical, 16)
    }

    // MARK: - Actions

    private func sendPrompt() async {
        let text = prompt.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else { return }
        prompt = ""
        loading = true
        error = nil
        do {
            guard let client else { throw AgentError(message: "No box selected") }
            let result = try await client.sendText(text)
            applyResult(result)
        } catch {
            self.error = error.localizedDescription
            Speech.speak("Something went wrong.")
        }
        loading = false
    }

    private func sendChoice(_ choice: String) async {
        loading = true
        error = nil
        do {
            guard let client else { throw AgentError(message: "No box selected") }
            let result = try await client.sendChoice(choice)
            applyResult(result)
        } catch {
            self.error = error.localizedDescription
            Speech.speak("Something went wrong.")
        }
        loading = false
    }

    private func applyResult(_ result: SessionTurnResult) {
        sessionName = result.session ?? sessionName
        runnerName = result.runner ?? runnerName
        pane = result.pane ?? pane
        awaitingChoice = result.awaitingChoice ?? false
        options = result.options ?? []

        if let err = result.error, !err.isEmpty {
            error = err
        }

        if awaitingChoice {
            // Speak the options so the user can hear what to pick.
            let opts = options.isEmpty ? "Choose an option." : options.joined(separator: ". ")
            Speech.speak("Choose: \(opts)")
        } else if result.ok == true {
            // Speak a one-sentence summary of the pane.
            Speech.speakSummary(of: pane)
        } else if let err = result.error, !err.isEmpty {
            Speech.speak(err)
        }
    }
}
