// DesktopVoiceClient.swift — drive a remote Windows/Linux/Mac DESKTOP from the
// wrist, by voice, with no video stream at all.
//
// WHY THIS FILE EXISTS:
// In standalone mode the watch had no `/ops` client. SessionClient speaks only
// /runner/session/turn and AgentClient only /watch/turn, and both carry the
// bespoke WatchRequest shape rather than the generic verb router — so the whole
// ops surface, and therefore desktop control, was unreachable from a standalone
// watch. (Phone-paired mode is unaffected: it rides the phone, which has full
// ops reach.) This is the standalone half of that gap.
//
// WHY SPEECH-ONLY IS THE RIGHT MODE HERE, NOT A DEGRADED ONE:
// A watch cannot usefully render a 1440x900 desktop. `desktop_voice` never
// needs one — it resolves the spoken phrase against the target machine's OS
// accessibility tree (AX / UIAutomation / AT-SPI) and returns ONE short
// sentence meant to be spoken: "what's on screen" → "Save, Cancel, and 4 more".
// It is also why this is cheap: no video means effectively no relay egress,
// which is what keeps it usable on the free relay tier.
//
// Ambiguity is surfaced, never guessed: when a phrase matches several controls
// the agent refuses and answers "2 matches: Save, Save As. Which one?" and the
// user says the fuller name. On a screen you would disambiguate by looking;
// here the question IS the interface.
//
// Mirrors wear/app/src/main/kotlin/io/yaver/wear/DesktopVoiceClient.kt — keep
// the two in step (CLAUDE.md cross-surface parity: native surfaces do NOT
// inherit each other's fixes).
//
// Never throws — failures become a `.error` WatchReply so the wrist always
// shows a line (same contract as SessionClient/AgentClient).

import Foundation

struct DesktopVoiceClient {
    /// e.g. "http://192.168.1.50:18080" — host:port of the box on the LAN.
    let boxBaseUrl: String
    let token: String

    private var session: URLSession {
        let cfg = URLSessionConfiguration.default
        // Desktop actions are local to the target machine (a click, a
        // keystroke, a tree read), so they return fast. A short ceiling keeps a
        // wedged box from freezing the wrist.
        cfg.timeoutIntervalForRequest = 20
        cfg.waitsForConnectivity = false
        return URLSession(configuration: cfg)
    }

    /// Send a spoken phrase to a desktop.
    ///
    /// - Parameters:
    ///   - transcript: what the user said, verbatim ("click Save").
    ///   - machine: which box to drive. "local" is the box at `boxBaseUrl`; any
    ///     other device id/alias is proxied by the agent's dispatchOps, so the
    ///     watch can drive a Windows tower through whichever box it can reach.
    func speak(_ transcript: String, machine: String = "local") async -> WatchReply {
        let trimmed = transcript.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return WatchReply(kind: .error, spoken: "I didn't catch that.")
        }
        guard let url = URL(string: "\(boxBaseUrl)/ops") else {
            return WatchReply(kind: .error, spoken: "Bad box address.")
        }

        // Serialized, never concatenated — a spoken phrase can contain quotes.
        let body: [String: Any] = [
            "verb": "desktop_voice",
            "machine": machine,
            "payload": ["transcript": trimmed],
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: body) else {
            return WatchReply(kind: .error, spoken: "Couldn't build that request.")
        }

        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("watch", forHTTPHeaderField: "X-Yaver-Surface")
        req.httpBody = data

        do {
            let (respData, _) = try await session.data(for: req)
            return Self.parseReply(respData)
        } catch {
            return WatchReply(kind: .error, spoken: "I couldn't reach your box.")
        }
    }

    /// Map an OpsResult into a wrist reply.
    ///
    /// The agent always attaches a `spoken` sentence — on success AND on
    /// refusal — precisely so thin surfaces need not interpret the structured
    /// result. Prefer it over `error`, which is developer-facing.
    static func parseReply(_ data: Data) -> WatchReply {
        guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return WatchReply(kind: .error, spoken: "Unreadable answer from the box.")
        }
        let initial = root["initial"] as? [String: Any]
        let spoken = (initial?["spoken"] as? String)?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""

        if root["ok"] as? Bool == true {
            return WatchReply(
                kind: .summary,
                spoken: spoken.isEmpty ? "Done." : spoken,
                taskId: "",
                status: "done"
            )
        }
        if !spoken.isEmpty {
            // Includes the ambiguity question, which is a prompt for the user's
            // next utterance rather than a dead end.
            return WatchReply(kind: .error, spoken: spoken)
        }
        let err = (root["error"] as? String) ?? ""
        return WatchReply(kind: .error, spoken: err.isEmpty ? "That didn't work." : err)
    }
}
