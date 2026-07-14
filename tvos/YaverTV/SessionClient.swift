// SessionClient.swift — drives a LIVE coding session via POST /runner/session/turn.
//
// This is the endpoint a TV should call (docs/yaver-tvos-surface.md §2.2 / §4).
// Unlike /watch/turn (which spawns a new task), this drives the session the user
// already has running — "keep developing this" means the ubuntu session, not a
// fresh task.
//
// The TV can render more than a watch: it shows the pane as monospaced text,
// renders options as focusable D-pad buttons, and speaks a summary via
// AVSpeechSynthesizer. So this client returns the FULL session response (pane +
// options + awaitingChoice) rather than collapsing it to one line.
//
// The four server-side guards (docs/yaver-tvos-surface.md §2.2) are honored by
// the endpoint — 409s carry the same body as 200 (awaitingChoice + options +
// pane), so we decode them the same way.

import Foundation

struct SessionTurnResult: Codable {
    let ok: Bool?
    let session: String?
    let runner: String?
    let sent: String?           // "prompt" or "choice"
    let awaitingChoice: Bool?
    let options: [String]?
    let pane: String?
    let error: String?
}

actor SessionClient {
    private let token: String
    private let box: BoxTarget
    private let session: URLSession

    init(token: String, box: BoxTarget) {
        self.token = token
        self.box = box
        let cfg = URLSessionConfiguration.default
        cfg.timeoutIntervalForRequest = 30
        self.session = URLSession(configuration: cfg)
    }

    /// Send a prompt to a named session.
    ///
    /// `session` is not optional-by-accident: omitting it makes the agent guess,
    /// and `resolveRunnerSession` (`runner_session_turn.go:81`) only guesses when
    /// EXACTLY one runner PTY is live. On a box with none it errors; on a box with
    /// two it errors with "several runner sessions are live — name the one you
    /// mean" — and a caller that cannot name one is simply stuck. Worse, when the
    /// single live session happened to be the user's own hand-rolled tmux window,
    /// the guess drove THAT: a prompt typed into a personal Claude Code session,
    /// its private scrollback rendered back onto a television. Name the session.
    func sendText(_ text: String, session: String?, waitMs: Int = 6000) async throws -> SessionTurnResult {
        try await turn(text: text, choice: nil, session: session, waitMs: waitMs)
    }

    /// Answer a menu the pane is showing.
    func sendChoice(_ choice: String, session: String?, waitMs: Int = 6000) async throws -> SessionTurnResult {
        try await turn(text: nil, choice: choice, session: session, waitMs: waitMs)
    }

    private func turn(text: String?, choice: String?, session: String?, waitMs: Int) async throws -> SessionTurnResult {
        guard let url = URL(string: "http://\(box.host):\(box.port)/runner/session/turn") else {
            throw AgentError(message: "bad box host")
        }
        var body: [String: Any] = ["waitMs": waitMs]
        if let text { body["text"] = text }
        if let choice { body["choice"] = choice }
        if let session, !session.isEmpty { body["session"] = session }

        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, resp) = try await self.session.data(for: req)
        guard let http = resp as? HTTPURLResponse else {
            throw AgentError(message: "no response")
        }
        // 409 is the guards firing — decode the same as 200.
        guard (200..<300).contains(http.statusCode) || http.statusCode == 409 else {
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String {
                throw AgentError(message: err)
            }
            throw AgentError(message: "session turn failed (\(http.statusCode))")
        }
        return try JSONDecoder().decode(SessionTurnResult.self, from: data)
    }
}
