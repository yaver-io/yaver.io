// AgentClient.swift — LEGACY standalone transport: POST a watch protocol message
// to the agent's /watch/turn endpoint over LAN HTTP with a Bearer token.
//
// ⚠️ SUPERSEDED by SessionClient.swift, which drives a LIVE coding session via
// POST /runner/session/turn (docs/yaver-watch-surface.md §4.2). This file is
// kept because:
//   1. It defines `AgentError`, which SessionClient also throws.
//   2. It's a valid fallback if /runner/session/turn is unavailable on an older
//      agent — /watch/turn still works (it just spawns a new task instead of
//      driving the live session).
// WatchStore no longer routes to this client; SessionClient is the standalone
// transport. See docs/yaver-watch-surface.md §8 build order #1.
//
// Original docstring preserved below:
// ---------------------------------------------------------------------------
// STANDALONE-mode transport: POST a watch protocol message to the agent's
// /watch/turn endpoint over LAN HTTP with a Bearer token, and decode the
// protocol reply.
//
// This is the SECONDARY mode (docs/yaver-smartwatch-voice-terminal.md §3 B/C).
// The DEFAULT is phone-paired (PhoneSession.swift), where the watch holds no
// token and this file is never used. Mirrors tvos/YaverTV/AgentClient.swift and
// mobile/src/lib/appletvClient.ts (LAN-first, Bearer auth) — but instead of the
// generic /ops verb router, the watch posts the same WatchRequest JSON the phone
// would have received over WCSession, so the wire protocol is identical in both
// transports.

import Foundation

struct AgentError: Error, LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

actor AgentClient {
    private let token: String
    private let box: BoxTarget
    private let session: URLSession

    init(token: String, box: BoxTarget) {
        self.token = token
        self.box = box
        let cfg = URLSessionConfiguration.default
        // The turn returns fast: the agent ACKs / asks-to-confirm / starts a task
        // and hands back a `working` reply; it does NOT block until a long task
        // finishes (async-by-design). 30s is generous headroom.
        cfg.timeoutIntervalForRequest = 30
        self.session = URLSession(configuration: cfg)
    }

    /// Send one watch turn (transcript / confirm / intent) and get the reply.
    /// Same WatchRequest/WatchReply pair the phone exchanges over WCSession.
    func turn(_ req: WatchRequest) async throws -> WatchReply {
        guard let url = URL(string: "http://\(box.host):\(box.port)/watch/turn") else {
            throw AgentError(message: "bad box host")
        }
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.httpBody = try WatchCodec.encodeData(req)

        let (data, resp) = try await session.data(for: request)
        guard let http = resp as? HTTPURLResponse else {
            throw AgentError(message: "no response")
        }
        // The agent returns 200 with a protocol reply for results, and may use
        // 4xx with an {error} body. Prefer decoding a protocol reply; fall back
        // to an error message, like the appletvClient does.
        if (200..<300).contains(http.statusCode) {
            return try WatchCodec.decodeReply(data)
        }
        if let reply = try? WatchCodec.decodeReply(data) { return reply }
        if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let err = obj["error"] as? String {
            throw AgentError(message: err)
        }
        throw AgentError(message: "watch turn failed (\(http.statusCode))")
    }
}
