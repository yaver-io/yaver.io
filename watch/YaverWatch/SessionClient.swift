// SessionClient.swift — standalone-mode transport that drives a LIVE coding
// session via POST /runner/session/turn.
//
// This is the endpoint a WATCH should call (docs/yaver-watch-surface.md §4.2).
// The older /watch/turn (AgentClient.swift) spawns a NEW task; this one drives
// the session the user already has running — "keep developing this" means the
// ubuntu session, not a fresh task.
//
// Maps the session endpoint's response (awaitingChoice + options[] + pane) into
// the SAME WatchReply the watch already renders:
//   awaitingChoice: true  → confirmNeeded (prompt = options list, token = sentinel)
//   awaitingChoice: false → summary (spoken = first clean line of pane)
//   error / unreachable    → error
//
// Choice loop: the client tracks `lastAwaitingChoice`. When the user speaks a
// number after a menu, sendText() recognizes it as a choice and sends {choice}
// instead of {text}. ConfirmView's confirm/cancel maps to choice "1"/"2" as a
// fallback (voice is the preferred path for picking a specific option).
//
// The four server-side guards (docs/yaver-watch-surface.md §4.2) are honored
// by the endpoint itself — this client just maps the response. Never type a
// prompt into a menu, never send a choice without a menu, never append Enter
// to a digit, always re-read after settling. The 409s this client may receive
// are the guards firing; they map to confirm-needed or error, not a retry.

import Foundation

/// Sentinel token for session-choice confirms. Distinguishes a confirm-needed
/// that came from the session endpoint (awaitingChoice) from one that came from
/// the phone's risk gate. When the watch sees this token, ConfirmView's
/// confirm/cancel maps to choice "1"/"2" instead of a transcript echo.
let sessionChoiceToken = "__yaver_session_choice__"

actor SessionClient {
    private let token: String
    private let box: BoxTarget
    private let session: URLSession

    /// Tracks whether the last response from the box had `awaitingChoice: true`.
    /// When the user speaks a number after a menu, sendText() uses this to send
    /// `{choice}` instead of `{text}` — the user doesn't have to know the
    /// protocol changed under them.
    private var lastAwaitingChoice = false

    init(token: String, box: BoxTarget) {
        self.token = token
        self.box = box
        let cfg = URLSessionConfiguration.default
        // The session endpoint waits up to `waitMs` for the runner to react,
        // then reads the pane. 30s headroom covers the 6s default wait + settle.
        cfg.timeoutIntervalForRequest = 30
        self.session = URLSession(configuration: cfg)
    }

    // MARK: - Public API (mirrors AgentClient's turn surface)

    /// Send a spoken transcript. If the box was awaiting a choice and the text
    /// looks like a number or number-word, send it as `{choice}` instead.
    func sendText(_ text: String) async throws -> WatchReply {
        if lastAwaitingChoice, let choice = SessionClient.parseChoice(text) {
            return try await turn(text: nil, choice: choice)
        }
        return try await turn(text: text, choice: nil)
    }

    /// Send a menu choice directly (e.g. from ConfirmView confirm → "1").
    func sendChoice(_ choice: String) async throws -> WatchReply {
        try await turn(text: nil, choice: choice)
    }

    /// Map a ConfirmReply (from ConfirmView) to a session choice.
    /// confirm → option "1" (first option), cancel → option "2" (second).
    /// This is a lossy fallback — the voice path (speak the number) is preferred
    /// because menus renumber and option 1 isn't always "yes".
    func sendConfirm(reply: ConfirmReply) async throws -> WatchReply {
        let choice = reply == .confirm ? "1" : "2"
        return try await turn(text: nil, choice: choice)
    }

    // MARK: - Core POST

    private struct SessionResponse: Decodable {
        let ok: Bool?
        let session: String?
        let runner: String?
        let sent: String?
        let awaitingChoice: Bool?
        let options: [String]?
        let pane: String?
        let error: String?
    }

    private func turn(text: String?, choice: String?) async throws -> WatchReply {
        guard let url = URL(string: "http://\(box.host):\(box.port)/runner/session/turn") else {
            throw AgentError(message: "bad box host")
        }
        var body: [String: Any] = [
            "waitMs": 6000, // short + snappy for a wrist; the agent default
        ]
        if let text { body["text"] = text }
        if let choice { body["choice"] = choice }

        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse else {
            throw AgentError(message: "no response")
        }

        // 409 is the guards firing — the pane is showing a menu (can't take a
        // prompt) or isn't showing one (can't take a choice). The response body
        // still carries awaitingChoice + options + pane, so decode and map it
        // the same way as a 200.
        guard (200..<300).contains(http.statusCode) || http.statusCode == 409 else {
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String {
                throw AgentError(message: err)
            }
            throw AgentError(message: "session turn failed (\(http.statusCode))")
        }

        let sr: SessionResponse
        do {
            sr = try JSONDecoder().decode(SessionResponse.self, from: data)
        } catch {
            throw AgentError(message: "bad response from box")
        }

        // Track whether the pane is on a menu so the next sendText can map a
        // spoken number to a choice automatically.
        lastAwaitingChoice = sr.awaitingChoice ?? false

        // Map the session response → WatchReply (same kinds the watch renders).
        if sr.awaitingChoice == true {
            // The pane is showing a menu. Show the options as the prompt; the
            // user speaks a number (or taps Confirm for option 1).
            let options = sr.options ?? []
            let prompt = options.isEmpty
                ? "Choose an option."
                : options.joined(separator: "\n")
            return WatchReply(
                kind: .confirmNeeded,
                spoken: options.isEmpty ? "Pick an option." : SessionClient.speakOptions(options),
                token: sessionChoiceToken,
                prompt: prompt
            )
        }

        // Not awaiting a choice → summarize the pane to one sentence.
        let pane = sr.pane ?? ""
        let spoken = SessionClient.summarize(pane)
        if sr.ok == false || (sr.error?.isEmpty == false && spoken.isEmpty) {
            return WatchReply(kind: .error, spoken: sr.error ?? "Something went wrong.")
        }
        return WatchReply(kind: .summary, spoken: spoken)
    }

    // MARK: - Pane summarization (mirrors watch_risk.go::watchFirstStatusClause)

    /// Pull the first short, status-shaped clause out of a pane tail. Refuses
    /// code/markup/path-dump lines (the watch must never speak code). Clamps to
    /// 120 chars — the same ceiling the agent's watch summariser uses.
    private static let codePattern = try! NSRegularExpression(
        pattern: #"[{}<>;=]|```|\b(function|const|class|def|import|return)\b|/\w+/"#
    )
    private static let sentencePattern = try! NSRegularExpression(
        pattern: #"^(.{1,120}?[.!?])(\s|$)"#
    )
    private static let markdownPattern = try! NSRegularExpression(
        pattern: "[#*`_~]"
    )

    static func summarize(_ pane: String) -> String {
        let lines = pane.split(separator: "\n", omittingEmptySubsequences: true)
        guard let first = lines.first else { return "Done." }
        let firstLine = String(first).trimmingCharacters(in: .whitespaces)
        if firstLine.isEmpty { return "Done." }

        // Refuse code/markup/path-dump shaped lines.
        let range = NSRange(firstLine.startIndex..., in: firstLine)
        if codePattern.firstMatch(in: firstLine, range: range) != nil {
            // Fall through to the next clean line if any, else default.
            for line in lines.dropFirst() {
                let l = String(line).trimmingCharacters(in: .whitespaces)
                if l.isEmpty { continue }
                let r = NSRange(l.startIndex..., in: l)
                if codePattern.firstMatch(in: l, range: r) == nil {
                    return clampSentence(stripMarkdown(l))
                }
            }
            return "Done."
        }

        return clampSentence(stripMarkdown(firstLine))
    }

    private static func clampSentence(_ s: String) -> String {
        // First sentence only.
        let r = NSRange(s.startIndex..., in: s)
        if let m = sentencePattern.firstMatch(in: s, range: r),
           let sentenceRange = Range(m.range(at: 1), in: s) {
            let clause = String(s[sentenceRange])
            return clause.count <= 120 ? clause : String(clause.prefix(119)) + "…"
        }
        // No sentence terminator — clamp the whole line.
        if s.count <= 120 { return s }
        return String(s.prefix(119)) + "…"
    }

    private static func stripMarkdown(_ s: String) -> String {
        let r = NSRange(s.startIndex..., in: s)
        return markdownPattern.stringByReplacingMatches(
            in: s, range: r, withTemplate: ""
        ).trimmingCharacters(in: .whitespaces)
    }

    /// Speak the options as a short voice prompt: "Choose: 1. Yes. 2. No."
    /// Clamped so a long menu doesn't drone on — the user can glance at the
    /// screen for the full list.
    private static func speakOptions(_ options: [String]) -> String {
        let short = options.prefix(4).map { opt in
            // "1. Yes, I trust this folder" → "1. Yes I trust this folder"
            // (strip the comma-clause after 40 chars for speech brevity)
            let trimmed = opt.trimmingCharacters(in: .whitespaces)
            if trimmed.count <= 60 { return trimmed }
            return String(trimmed.prefix(59)) + "…"
        }.joined(separator: ". ")
        return "Choose: \(short)."
    }

    /// Map a spoken word/number to a bare digit string the session endpoint
    /// accepts (`isTmuxChoiceAnswer` = `^\s*\d{1,2}\s*$`).
    private static func parseChoice(_ text: String) -> String? {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if t.isEmpty { return nil }
        // Bare digit ("1", "2", "12").
        if t.allSatisfy(\.isNumber) { return t }
        // Common number words.
        let wordMap: [String: String] = [
            "one": "1", "two": "2", "three": "3", "four": "4", "five": "5",
            "six": "6", "seven": "7", "eight": "8", "nine": "9",
            "first": "1", "second": "2", "third": "3",
            "yes": "1", "no": "2", // common menu shapes
        ]
        return wordMap[t]
    }
}
