// PhoneSession.swift — DEFAULT-mode transport: a WCSession delegate wrapper that
// ships a WatchRequest to the paired iPhone Yaver app and decodes the WatchReply.
//
// This is the PRIMARY topology (docs/yaver-smartwatch-voice-terminal.md §3 A):
// the watch never talks to the runner directly. It sends the transcript /
// confirm / intent to the phone, which runs the real carVoiceCoding loop and
// replies with a single spoken sentence. The watch holds nothing sensitive —
// no token, no box host, no task state. That's the thin-terminal payoff.
//
// Two delivery paths:
//   • sendMessage(reply:) — interactive, reachable phone, immediate reply
//     (ack / confirm-needed / working). Used for the foreground turn.
//   • transferUserInfo — queued background delivery for the phone→watch
//     completion wake ("Done. Tests pass.") when a long task finishes while the
//     watch app isn't frontmost. The phone polls the task to terminal and
//     pushes this; the watch can't background-poll itself (§8).

import Foundation
import WatchConnectivity

@MainActor
final class PhoneSession: NSObject, ObservableObject {
    static let shared = PhoneSession()

    /// True when the iPhone Yaver app is reachable right now for an interactive
    /// sendMessage. Drives the UI's "paired" vs "fall back to standalone" choice.
    @Published private(set) var isReachable = false
    @Published private(set) var isActivated = false

    /// Late-arriving phone→watch pushes (task completion) land here so the UI can
    /// speak + show them even if they weren't a direct reply to a turn.
    @Published var lastPushedReply: WatchReply?

    private var session: WCSession? {
        WCSession.isSupported() ? WCSession.default : nil
    }

    func activate() {
        guard let session else { return }
        session.delegate = self
        session.activate()
    }

    /// Whether phone-paired transport is currently viable.
    var canUsePhone: Bool { isActivated && isReachable }

    // MARK: - Outbound turns

    /// Send a transcript; the phone runs the loop and replies with a WatchReply.
    func sendTranscript(_ text: String) async throws -> WatchReply {
        try await send(.transcript(text))
    }

    /// Answer a confirm-needed prompt.
    func sendConfirm(token: String, reply: ConfirmReply) async throws -> WatchReply {
        try await send(.confirm(token: token, reply: reply))
    }

    /// Fire a complication quick-action intent.
    func sendIntent(_ intent: WatchIntent) async throws -> WatchReply {
        try await send(.intent(intent))
    }

    // MARK: - Control channel (wake box)

    /// Outcome of a wake-box request routed through the phone.
    enum WakeDispatch {
        case sent               // phone acked; the box is (re)starting
        case phoneUnreachable   // no phone in reach to carry the request
        case failed(String)     // phone reached but couldn't wake the box
    }

    /// Ask the paired iPhone to wake a self-parked managed box. The watch does
    /// NOT hold the control-plane token, so the phone (already signed in) is the
    /// one that can call the resume API — the watch only carries the intent.
    ///
    /// ─────────────────────────────────────────────────────────────────────────
    /// The wake rides the SAME "yaverWatch" envelope as every other request, so
    /// the phone's existing WCSession bridge (YaverWatchBridge.swift → JS
    /// watchEntry → watchBridge `wake` handler → startManagedCloudMachine) picks
    /// it up with no bespoke control channel. Wire shape:
    ///
    ///   Watch → Phone   {"v":1,"kind":"wake","machineId":"<optional>"}
    ///
    /// The phone resolves the managed machineId (from `machineId`, else its
    /// current/primary managed box) and calls its existing wake capability
    /// (startManagedCloudMachine — the same one behind mobile `wakeManagedDevice`),
    /// replying with a normal WatchReply (`working` on accept, `error` on
    /// failure). The box then boots + re-registers over the relay asynchronously;
    /// the watch drives the rest by polling /health. An unreachable phone throws
    /// (→ `phoneUnreachable`, the "open Yaver" hint).
    /// ─────────────────────────────────────────────────────────────────────────
    func requestWakeBox(machineId: String?, deviceId: String) async -> WakeDispatch {
        do {
            let reply = try await send(.wake(machineId: machineId))
            if reply.kind == .error {
                return .failed(reply.spoken ?? "Your phone couldn't wake the box.")
            }
            return .sent
        } catch {
            // Delivery failed (phone dropped out, or an older build without the
            // wake handler). Tell the user to open the app rather than spinning a
            // ladder that will never advance.
            return .phoneUnreachable
        }
    }

    // MARK: - Outbound turns (core)

    /// Core: encode the request into the "yaverWatch" envelope, sendMessage, and
    /// decode the reply. No force-unwraps on the network path.
    private func send(_ req: WatchRequest) async throws -> WatchReply {
        guard let session, session.activationState == .activated else {
            throw WatchProtocolError.malformed
        }
        guard session.isReachable else {
            // Caller should fall back to standalone (SessionClient) or surface
            // "open the Yaver app on your phone".
            throw PhoneSessionError.notReachable
        }
        let envelope = try WatchCodec.envelope(req)
        return try await withCheckedThrowingContinuation { continuation in
            session.sendMessage(envelope, replyHandler: { dict in
                do {
                    let reply = try WatchCodec.reply(from: dict)
                    continuation.resume(returning: reply)
                } catch {
                    continuation.resume(throwing: error)
                }
            }, errorHandler: { error in
                continuation.resume(throwing: error)
            })
        }
    }
}

enum PhoneSessionError: Error, LocalizedError {
    case notReachable
    var errorDescription: String? { "Your phone isn't reachable. Open the Yaver app on your phone, or use the watch without your phone." }
}

// MARK: - WCSessionDelegate

extension PhoneSession: WCSessionDelegate {
    nonisolated func session(_ session: WCSession,
                             activationDidCompleteWith state: WCSessionActivationState,
                             error: Error?) {
        Task { @MainActor in
            self.isActivated = (state == .activated)
            self.isReachable = session.isReachable
        }
    }

    nonisolated func sessionReachabilityDidChange(_ session: WCSession) {
        Task { @MainActor in self.isReachable = session.isReachable }
    }

    /// Phone→watch interactive message (rare; we mostly reply to our own turns).
    nonisolated func session(_ session: WCSession, didReceiveMessage message: [String: Any]) {
        ingest(message)
    }

    /// Phone→watch queued background delivery — the task-completion wake.
    nonisolated func session(_ session: WCSession, didReceiveUserInfo userInfo: [String: Any]) {
        ingest(userInfo)
    }

    nonisolated private func ingest(_ dict: [String: Any]) {
        guard let reply = try? WatchCodec.reply(from: dict) else { return }
        Task { @MainActor in self.lastPushedReply = reply }
    }
}
