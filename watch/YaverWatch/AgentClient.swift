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

/// Ask a box who it is (GET /info → {deviceId, …}).
///
/// Static, and takes host/port rather than a BoxTarget, because it runs BEFORE
/// the BoxTarget exists — resolving the deviceId is part of constructing one.
///
/// This is the ONE call that needs a live route to the box, and it is deliberately
/// made at sign-in, when the user has just typed the address and is provably on
/// its network. The deviceId is then persisted forever, so the thing that needs
/// it later (`/devices/request-update`, for a box we can't reach) never has to
/// ask. Matching the host against a registry's localIps would be a guess and
/// could target the WRONG box; the box naming itself cannot.
enum BoxIdentity {
    private struct Info: Decodable { let deviceId: String? }

    /// Returns the box's deviceId, or throws a readable reason. Short timeout —
    /// this runs inline in sign-in and must not hang the wrist.
    static func fetchDeviceId(host: String, port: Int, token: String) async throws -> String {
        guard let url = URL(string: "http://\(host):\(port)/info") else {
            throw AgentError(message: "Bad box address.")
        }
        var req = URLRequest(url: url)
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("watch", forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 6
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "No response from the box.") }
        if http.statusCode == 401 || http.statusCode == 403 {
            throw AgentError(message: "The box rejected this session.")
        }
        guard (200..<300).contains(http.statusCode) else {
            throw AgentError(message: "The box didn't answer (\(http.statusCode)).")
        }
        guard let id = (try? JSONDecoder().decode(Info.self, from: data))?.deviceId, !id.isEmpty else {
            // An agent too old to report a deviceId. Say so rather than
            // inventing one — every caller of this is better off with nothing.
            throw AgentError(message: "This box's agent is too old to identify itself.")
        }
        return id
    }
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
