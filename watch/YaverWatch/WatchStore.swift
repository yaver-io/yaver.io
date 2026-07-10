// WatchStore.swift — app-wide state + the one place that decides WHICH transport
// a turn takes (phone-paired default, standalone fallback) and reduces a
// WatchReply into "the line to show + the haptic to play".
//
// Deliberately small. The watch OWNS NOTHING and SHOWS NOTHING complex
// (docs/yaver-smartwatch-voice-terminal.md §0): one summary line, one pending
// confirm, a transport mode, and (only for standalone) a token + selected box.
// No task list, no history, no code. The phone/runner is the brain-of-record.

import Foundation
import SwiftUI

@MainActor
final class WatchStore: ObservableObject {
    // MARK: Persisted (UserDefaults via @AppStorage)

    // Standalone-only: a session token (from device-code auth) the watch holds
    // when it must reach the agent WITHOUT the phone. Empty in the default
    // phone-paired topology — that's the whole point of paired mode.
    @AppStorage("yaver.watch.token") private var storedToken: String = ""
    @AppStorage("yaver.watch.box") private var storedBoxJSON: String = ""
    // User opt-in to "use without your phone" (mode B/C). Off by default so the
    // watch holds nothing sensitive unless the user asks.
    @AppStorage("yaver.watch.standaloneOptIn") var standaloneOptIn: Bool = false

    // MARK: Live state

    @Published var token: String = ""
    @Published var box: BoxTarget?

    /// The last one-glance line we showed (the reply spoken/displayed). Big and
    /// legible in RootView; never code.
    @Published var lastLine: String = ""

    /// A pending confirm-needed prompt; when set, ConfirmView takes over.
    @Published var pendingConfirm: PendingConfirm?

    /// Coarse UI phase for the record button / spinner.
    enum Phase: Equatable { case idle, listening, dispatching, working }
    @Published var phase: Phase = .idle

    let phone = PhoneSession.shared

    struct PendingConfirm: Equatable {
        let token: String
        let prompt: String
    }

    init() {
        token = storedToken
        if !storedBoxJSON.isEmpty {
            box = try? JSONDecoder().decode(BoxTarget.self, from: Data(storedBoxJSON.utf8))
        }
        // Pick up phone→watch background pushes (task-completion wake) and fold
        // them into the same reduce path as a direct reply.
        // (RootView observes phone.lastPushedReply and calls absorb().)
    }

    func activate() { phone.activate() }

    // MARK: Standalone credentials

    func signInStandalone(token: String, box: BoxTarget) {
        self.token = token
        storedToken = token
        self.box = box
        if let data = try? JSONEncoder().encode(box), let s = String(data: data, encoding: .utf8) {
            storedBoxJSON = s
        }
    }

    func signOutStandalone() {
        token = ""
        storedToken = ""
        box = nil
        storedBoxJSON = ""
    }

    var hasStandaloneCreds: Bool { !token.isEmpty && box != nil }

    /// Whether we can run a turn at all right now (either transport).
    var canDispatch: Bool {
        phone.canUsePhone || (standaloneOptIn && hasStandaloneCreds)
    }

    // MARK: - Turn routing (the one decision point)

    /// Send a transcript. Phone-paired first; standalone fallback if opted-in.
    /// In standalone mode the transcript goes to /runner/session/turn (the LIVE
    /// session endpoint) via SessionClient, NOT /watch/turn (which spawns a new
    /// task). See docs/yaver-watch-surface.md §4.2.
    func sendTranscript(_ text: String) async {
        await run { transport in
            switch transport {
            case .phone: return try await self.phone.sendTranscript(text)
            case .session(let client): return try await client.sendText(text)
            }
        }
    }

    /// Answer the pending confirm (or any confirm by token).
    /// In standalone mode, confirm/cancel maps to session choice "1"/"2" —
    /// a lossy fallback. The voice path (speak the number) is preferred.
    func sendConfirm(token: String, reply: ConfirmReply) async {
        pendingConfirm = nil
        await run { transport in
            switch transport {
            case .phone: return try await self.phone.sendConfirm(token: token, reply: reply)
            case .session(let client): return try await client.sendConfirm(reply: reply)
            }
        }
    }

    /// Fire a complication / quick-action intent.
    func sendIntent(_ intent: WatchIntent) async {
        await run { transport in
            switch transport {
            case .phone: return try await self.phone.sendIntent(intent)
            case .session(let client):
                // Expand the intent to a transcript and send it as a session prompt.
                let text = WatchStore.intentToTranscript(intent)
                return try await client.sendText(text)
            }
        }
    }

    /// A reply that arrived OUT of band (phone pushed a completion). Fold it in.
    func absorb(_ reply: WatchReply) {
        reduce(reply)
    }

    // MARK: - Internals

    private enum Transport {
        case phone
        /// Standalone transport: drives a LIVE coding session via
        /// POST /runner/session/turn (docs/yaver-watch-surface.md §4.2).
        case session(SessionClient)
    }

    /// Resolve the transport, run the closure, reduce the reply (or the error).
    private func run(_ op: @escaping (Transport) async throws -> WatchReply) async {
        guard let transport = resolveTransport() else {
            reduce(WatchReply(kind: .error,
                              spoken: "Open the Yaver app on your phone, or turn on use-without-phone."))
            return
        }
        phase = .dispatching
        do {
            let reply = try await op(transport)
            reduce(reply)
        } catch {
            reduce(WatchReply(kind: .error, spoken: friendly(error)))
        }
    }

    /// Phone-paired wins when reachable; otherwise standalone (session) if
    /// opted-in. The standalone path uses SessionClient (/runner/session/turn),
    /// NOT AgentClient (/watch/turn) — driving the live session is the product.
    private func resolveTransport() -> Transport? {
        if phone.canUsePhone { return .phone }
        if standaloneOptIn, hasStandaloneCreds, let box {
            return .session(SessionClient(token: token, box: box))
        }
        return nil
    }

    /// Expand a complication intent to a transcript the session can send as a
    /// prompt. Mirrors watch_risk.go::watchIntentToTranscript.
    private static func intentToTranscript(_ intent: WatchIntent) -> String {
        switch intent {
        case .runTests: return "run the tests on the primary device and tell me if they pass"
        case .deploy: return "deploy"
        case .status: return "give me a one-line status of the current work"
        }
    }

    /// The single reduce path: reply -> (line shown, haptic, spoken, phase, confirm).
    /// This is where "voice in, one sentence + haptic + voice out" is enforced.
    /// Speech.forReply speaks the `spoken` field aloud via AVSpeechSynthesizer
    /// (docs/yaver-watch-surface.md §6 — the single highest-value addition).
    private func reduce(_ reply: WatchReply) {
        Haptics.forReply(reply)
        Speech.forReply(reply)
        switch reply.kind {
        case .ack:
            lastLine = reply.spoken ?? "On it."
            phase = .idle
        case .working:
            lastLine = reply.spoken ?? "Working…"
            phase = .working   // wait for the phone/agent to wake us with a summary
        case .confirmNeeded:
            // Surface the confirm UI; do NOT auto-decide on the wrist.
            if let token = reply.token {
                pendingConfirm = PendingConfirm(token: token,
                                                prompt: reply.prompt ?? "Confirm this action?")
            }
            lastLine = reply.prompt ?? "Confirm?"
            phase = .idle
        case .summary:
            lastLine = reply.spoken ?? "Done."
            phase = .idle
        case .error:
            lastLine = reply.spoken ?? "Something went wrong."
            phase = .idle
        case .handoff:
            lastLine = reply.spoken ?? "Sent it to your phone."
            phase = .idle
        }
    }

    private func friendly(_ error: Error) -> String {
        if let p = error as? PhoneSessionError { return p.errorDescription ?? "Your phone isn't reachable." }
        if let a = error as? AgentError { return a.message }
        return "I couldn't reach your box."
    }
}
