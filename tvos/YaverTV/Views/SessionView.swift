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
    @State private var boxUnreachable = false
    @StateObject private var lifecycle = BoxLifecycle()

    /// The runner PTYs live on the box right now, and which one we drive.
    /// Loaded before the first turn: a surface that cannot name its target has
    /// to let the agent guess, and the agent only guesses safely when exactly
    /// one session exists (see SessionClient.sendText).
    @State private var sessions: [RunnerSession] = []
    @State private var selected: String?
    @State private var sessionsLoaded = false

    /// When opened from the Tasks list, the tmux session to drive directly —
    /// skips the picker even if several runners are live.
    private let preselect: String?
    init(preselect: String? = nil) {
        self.preselect = preselect
        _selected = State(initialValue: (preselect?.isEmpty == false) ? preselect : nil)
    }

    private var client: SessionClient? {
        guard let box = store.selectedBox else { return nil }
        return SessionClient(token: store.token, box: box)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider().background(.gray.opacity(0.3))

            if sessionsLoaded && sessions.isEmpty {
                noRunnerView
            } else if selected == nil && sessions.count > 1 {
                pickerView
            } else {
                paneView
                Divider().background(.gray.opacity(0.3))
                if awaitingChoice {
                    optionsView
                } else {
                    promptBar
                }
            }

            if boxUnreachable {
                wakeBar
            } else if let error {
                Text(error)
                    .font(.system(size: 16, design: .monospaced))
                    .foregroundStyle(.red)
                    .padding(.horizontal, 48).padding(.bottom, 12)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
        .task(id: store.selectedBox?.id) { await loadSessions() }
        .onChange(of: lifecycle.phase) { _, phase in
            // Box came back — clear the asleep state so the prompt bar returns.
            if phase == .ready {
                boxUnreachable = false
                Task { await loadSessions() }
            }
        }
    }

    // MARK: - No runner on the box

    /// The honest empty state. Previously the pane just said "Send a prompt to
    /// start the session." on a box with no runner — so the first prompt came
    /// back "no live runner sessions on this machine" and the screen looked
    /// broken. Say what is missing and exactly how to fix it, before they type.
    private var noRunnerView: some View {
        VStack(alignment: .leading, spacing: 14) {
            Label("No coding session on \(store.selectedBox?.name ?? "this box")",
                  systemImage: "moon.zzz")
                .font(.system(size: 26, weight: .semibold))
                .foregroundStyle(.orange)
            Text("Start one on that machine, then come back:")
                .font(.system(size: 20))
                .foregroundStyle(.secondary)
            Text("yaver wrap codex")
                .font(.system(size: 24, design: .monospaced))
                .padding(.horizontal, 20).padding(.vertical, 12)
                .background(.gray.opacity(0.18), in: RoundedRectangle(cornerRadius: 10))
            Button {
                Task { await loadSessions() }
            } label: {
                Label("Check again", systemImage: "arrow.clockwise")
                    .font(.system(size: 20, weight: .semibold))
                    .padding(.horizontal, 26).padding(.vertical, 12)
            }
            .buttonStyle(.borderedProminent)
            .padding(.top, 6)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(48)
    }

    // MARK: - Picker (several runners live)

    /// A D-pad is good at exactly one thing: picking from a short list.
    private var pickerView: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Which session?")
                .font(.system(size: 26, weight: .semibold))
            Text("Several coding sessions are live on \(store.selectedBox?.name ?? "this box").")
                .font(.system(size: 18))
                .foregroundStyle(.secondary)
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    ForEach(sessions) { s in
                        Button {
                            selected = s.name
                            sessionName = s.name
                            runnerName = s.runner ?? ""
                        } label: {
                            HStack {
                                Image(systemName: "terminal")
                                Text(s.label).font(.system(size: 22, design: .monospaced))
                                Spacer()
                                if s.attached == true {
                                    Text("attached").font(.system(size: 16)).foregroundStyle(.secondary)
                                }
                            }
                            .padding(.horizontal, 24).padding(.vertical, 16)
                        }
                        .buttonStyle(.bordered)
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(48)
    }

    // MARK: - Wake bar (box asleep)

    @ViewBuilder private var wakeBar: some View {
        if lifecycle.isRunning {
            WakeProgressView(lifecycle: lifecycle, boxName: store.selectedBox?.name)
                .padding(.horizontal, 48).padding(.bottom, 20)
        } else {
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 20) {
                    Label("Box asleep", systemImage: "moon.zzz.fill")
                        .font(.system(size: 22, weight: .semibold))
                        .foregroundStyle(.orange)
                    Spacer()
                    if let box = store.selectedBox, box.wakeable {
                        Button {
                            lifecycle.wake(box, token: store.token)
                        } label: {
                            Label(lifecycle.error == nil ? "Wake" : "Try again", systemImage: "power")
                                .font(.system(size: 20, weight: .semibold))
                                .padding(.horizontal, 26).padding(.vertical, 12)
                        }
                        .buttonStyle(.borderedProminent)
                    } else {
                        Text("Start it from your computer or phone.")
                            .font(.system(size: 18)).foregroundStyle(.secondary)
                    }
                }
                if let err = lifecycle.error {
                    Text(err)
                        .font(.system(size: 16, design: .monospaced))
                        .foregroundStyle(.red)
                }
            }
            .padding(.horizontal, 48).padding(.vertical, 18)
        }
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
            // Only offered when there is actually another session to switch to —
            // a "Change" button on a box with one runner is a dead control.
            if sessions.count > 1, selected != nil {
                Button {
                    selected = nil
                    pane = ""
                    awaitingChoice = false
                    options = []
                } label: {
                    Label("Change", systemImage: "rectangle.2.swap")
                        .font(.system(size: 18))
                }
                .buttonStyle(.bordered)
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
                            Task { await sendChoice(Self.optionNumber(opt, fallbackIndex: idx)) }
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

    /// The number to SEND for a menu line — read out of the line itself.
    ///
    /// Never the array index. The agent scans only the last 12 pane lines
    /// (`tmuxChoiceScanLines`), so when a menu has scrolled, `options[0]` can
    /// already be the line "2. No, exit" — and sending `1` for it answers a
    /// different item than the one the user focused. `runner_session_turn.go`
    /// records where that ends: claude renumbers its next modal so that `1`
    /// means "No, exit", and the session quit. The digit is right there in the
    /// text; use it, and fall back to the index only for an unnumbered line.
    static func optionNumber(_ line: String, fallbackIndex: Int) -> String {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        let digits = trimmed.prefix { $0.isNumber }
        if !digits.isEmpty, digits.count <= 2 { return String(digits) }
        return String(fallbackIndex + 1)
    }

    /// Strip absolute home paths out of anything we put on a television.
    ///
    /// The pane is raw tmux output: it carries `/Users/<name>/Workspace/…`, i.e.
    /// the user's login name and filesystem layout. This screen is designed to be
    /// looked at from a sofa — and, right now, filmed for an App Store review. The
    /// same redaction runs before TTS, because `Speech.speakSummary` will happily
    /// read a path out loud. Matches the repo's Convex privacy rule, which already
    /// forbids absolute paths from ever leaving the machine.
    static func redact(_ text: String) -> String {
        var out = text
        for root in ["/Users/", "/home/"] {
            while let r = out.range(of: root) {
                let rest = out[r.upperBound...]
                let name = rest.prefix { !$0.isWhitespace && $0 != "/" }
                guard !name.isEmpty else { break }
                out.replaceSubrange(r.lowerBound..<(name.endIndex), with: "~")
            }
        }
        return out
    }

    /// Ask the box which runner PTYs are live, BEFORE the first prompt.
    ///
    /// One session → drive it. Several → make the user pick (the agent refuses to
    /// guess, and it is right to). None → say so, with the command that fixes it.
    private func loadSessions() async {
        guard let box = store.selectedBox, let agent = store.client() else { return }
        loading = true
        defer { loading = false }
        do {
            let live = try await agent.runnerSessions().sessions ?? []
            sessions = live
            sessionsLoaded = true
            error = nil
            boxUnreachable = false

            if let selected, live.contains(where: { $0.name == selected }) {
                return                      // keep the user's choice across refreshes
            }
            if live.count == 1 {
                selected = live[0].name
                sessionName = live[0].name
                runnerName = live[0].runner ?? ""
            } else {
                selected = nil              // none, or several — no silent guess
            }
        } catch {
            sessionsLoaded = true
            if error is URLError {
                boxUnreachable = true
                lifecycle.markUnreachable(box)
            } else {
                self.error = error.localizedDescription
            }
        }
    }

    private func sendPrompt() async {
        let text = prompt.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else { return }
        prompt = ""
        loading = true
        error = nil
        do {
            guard let client else { throw AgentError(message: "No box selected") }
            let result = try await client.sendText(text, session: selected)
            applyResult(result)
        } catch {
            handleTurnError(error)
        }
        loading = false
    }

    private func sendChoice(_ choice: String) async {
        loading = true
        error = nil
        do {
            guard let client else { throw AgentError(message: "No box selected") }
            let result = try await client.sendChoice(choice, session: selected)
            applyResult(result)
        } catch {
            handleTurnError(error)
        }
        loading = false
    }

    /// A connection-level failure (box down / parked) surfaces the Wake
    /// affordance instead of a bare error; anything else stays a plain error.
    private func handleTurnError(_ error: Error) {
        if error is URLError, let box = store.selectedBox {
            boxUnreachable = true
            lifecycle.markUnreachable(box)
            Speech.speak("Your box is asleep.")
        } else {
            self.error = error.localizedDescription
            Speech.speak("Something went wrong.")
        }
    }

    private func applyResult(_ result: SessionTurnResult) {
        sessionName = result.session ?? sessionName
        runnerName = result.runner ?? runnerName
        // Redact BEFORE it reaches @State: `pane` is what the TV renders and what
        // Speech reads aloud, so anything unredacted here is already on screen.
        pane = result.pane.map(Self.redact) ?? pane
        awaitingChoice = result.awaitingChoice ?? false
        options = (result.options ?? []).map(Self.redact)

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
