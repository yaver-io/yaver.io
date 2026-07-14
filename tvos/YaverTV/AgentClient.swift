// AgentClient.swift — calls a Yaver agent's /ops endpoint over LAN HTTP.
//
// Mirrors mobile/src/lib/appletvClient.ts::atvOps: POST http://<host>:<port>/ops
// with body { verb, payload, machine:"local" } + Authorization: Bearer <token>.
// The agent returns either the result object directly or { initial: <result> }
// for streaming verbs; we unwrap `initial` like the RN client does.

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
        cfg.timeoutIntervalForRequest = 30
        self.session = URLSession(configuration: cfg)
    }

    /// Low-level call: returns the decoded result for `verb`.
    func ops<T: Decodable>(_ verb: String, _ payload: [String: Any] = [:], as type: T.Type) async throws -> T {
        let data = try await rawOps(verb, payload)
        // Unwrap { initial: ... } if present (streaming verbs), else decode whole.
        if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
            if let ok = obj["ok"] as? Bool, !ok {
                throw AgentError(message: obj["error"] as? String ?? "\(verb) failed")
            }
            if let initial = obj["initial"] {
                let inner = try JSONSerialization.data(withJSONObject: initial)
                return try JSONDecoder().decode(T.self, from: inner)
            }
        }
        return try JSONDecoder().decode(T.self, from: data)
    }

    /// Fire-and-check verbs that only report ok/error.
    ///
    /// A refused verb comes back as HTTP 200 with `{"ok":false,"error":"…"}` —
    /// not a 4xx — so rawOps lets it through. Returning that `false` to a caller
    /// that writes `_ = try await client.call("reload")` threw the reason away
    /// and left the button looking dead: the agent said "no dev server is
    /// currently running", and the headset said nothing at all. `ok == false` is
    /// a failure; raise it so the surface can show why.
    @discardableResult
    func call(_ verb: String, _ payload: [String: Any] = [:]) async throws -> Bool {
        let data = try await rawOps(verb, payload)
        if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
            if let ok = obj["ok"] as? Bool, !ok {
                throw AgentError(message: obj["error"] as? String ?? "\(verb) failed")
            }
            if let err = obj["error"] as? String { throw AgentError(message: err) }
            if let ok = obj["ok"] as? Bool { return ok }
        }
        return true
    }

    private func rawOps(_ verb: String, _ payload: [String: Any]) async throws -> Data {
        guard let url = URL(string: "http://\(box.host):\(box.port)/ops") else {
            throw AgentError(message: "bad box host")
        }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.httpBody = try JSONSerialization.data(withJSONObject: [
            "verb": verb,
            "payload": payload,
            "machine": "local",
        ])
        let (data, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "no response") }
        // The agent returns 200 for results and also 4xx with an {error} body;
        // surface the error message when present, like the RN client.
        if !(200..<300).contains(http.statusCode) {
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String {
                throw AgentError(message: err)
            }
            throw AgentError(message: "ops \(verb) failed (\(http.statusCode))")
        }
        return data
    }

    // ---- Typed convenience wrappers for the lean-back surfaces -------------

    func nowPlaying(device: String? = nil) async throws -> NowPlaying {
        try await ops("appletv_now_playing", device.map { ["device": $0] } ?? [:], as: NowPlaying.self)
    }

    func sendKey(_ key: RemoteKey, device: String? = nil) async throws {
        var p: [String: Any] = ["key": key.rawValue]
        if let d = device { p["device"] = d }
        try await call("appletv_remote_key", p)
    }

    func transport(_ action: RemoteKey, device: String? = nil) async throws {
        var p: [String: Any] = ["action": action.rawValue]
        if let d = device { p["device"] = d }
        try await call("appletv_transport", p)
    }

    func launchApp(_ bundleId: String, device: String? = nil) async throws {
        var p: [String: Any] = ["bundle_id": bundleId]
        if let d = device { p["device"] = d }
        try await call("appletv_launch_app", p)
    }

    func captureStatus() async throws -> CaptureStatus {
        try await ops("capture_status", [:], as: CaptureStatus.self)
    }

    func info() async throws -> AgentInfo {
        try await ops("info", [:], as: AgentInfo.self)
    }

    func status() async throws -> AgentStatus {
        try await ops("status", [:], as: AgentStatus.self)
    }

    func voiceStatus() async throws -> VoiceRuntimeStatus {
        try await ops("voice", ["op": "status"], as: VoiceRuntimeStatus.self)
    }

    /// The live runner PTYs on the box — the same set `/runner/session/turn`
    /// drives, so a picker built from this can always name a session the turn
    /// endpoint will accept. NOT `runner`/`agents_list`: that lists agent-graph
    /// tasks and answers 0 on a box with a runner running.
    func runnerSessions() async throws -> RunnerSessions {
        try await ops("runner_sessions", [:], as: RunnerSessions.self)
    }

    func platformMatrix() async throws -> PlatformMatrixEnvelope {
        try await ops("mobile_platform_matrix", [:], as: PlatformMatrixEnvelope.self)
    }

    /// The task queue on the box (GET /tasks). REST, not an ops verb — a glance
    /// list for the TV; the full task lifecycle stays on phone/web.
    func listTasks() async throws -> [TaskSummary] {
        guard let url = URL(string: "http://\(box.host):\(box.port)/tasks") else {
            throw AgentError(message: "bad box host")
        }
        var req = URLRequest(url: url)
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        let (data, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "no response") }
        guard (200..<300).contains(http.statusCode) else {
            throw AgentError(message: "couldn't load tasks (\(http.statusCode))")
        }
        return (try JSONDecoder().decode(TaskList.self, from: data)).tasks
    }

    /// Feedback reports the box has collected (GET /feedback → a bare array).
    func listFeedback() async throws -> [FeedbackReport] {
        guard let url = URL(string: "http://\(box.host):\(box.port)/feedback") else {
            throw AgentError(message: "bad box host")
        }
        var req = URLRequest(url: url)
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        let (data, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "no response") }
        guard (200..<300).contains(http.statusCode) else {
            throw AgentError(message: "couldn't load feedback (\(http.statusCode))")
        }
        return (try? JSONDecoder().decode([FeedbackReport].self, from: data)) ?? []
    }

    func startRunnerAuth(_ runner: String) async throws -> RunnerAuthStartResult {
        try await ops("runner_auth", ["op": "browser_start", "runner": runner], as: RunnerAuthStartResult.self)
    }

    func runnerAuthStatus(sessionId: String) async throws -> RunnerAuthStartResult {
        try await ops("runner_auth", ["op": "browser_status", "sessionId": sessionId], as: RunnerAuthStartResult.self)
    }

    func startGitAuth(_ provider: String, host: String? = nil) async throws -> GitAuthSession {
        var payload: [String: Any] = ["provider": provider]
        if let host, !host.isEmpty { payload["host"] = host }
        return try await ops("git_connect", payload, as: GitAuthSession.self)
    }

    func gitAuthStatus(sessionId: String) async throws -> GitAuthSession {
        try await ops("git_connect_status", ["sessionId": sessionId], as: GitAuthSession.self)
    }

    func reload(mode: String = "dev", workDir: String? = nil) async throws -> ReloadResult {
        var payload: [String: Any] = ["mode": mode]
        if let workDir, !workDir.isEmpty { payload["workDir"] = workDir }
        return try await ops("reload", payload, as: ReloadResult.self)
    }

    /// MJPEG frame URL for the capture card — same `/capture/frame.jpg` the RN
    /// client polls. Bearer goes in the header on fetch; tvOS `AsyncImage` can't
    /// set headers, so callers fetch via `frameData()` instead.
    func captureFrameURL() -> URL? {
        URL(string: "http://\(box.host):\(box.port)/capture/frame.jpg")
    }

    /// A capture frame, or a real error — never a JSON error body dressed as JPEG.
    ///
    /// This discarded the HTTP response and returned whatever bytes arrived. When
    /// capture isn't running the agent answers `503` with a 43-byte JSON body
    /// (`{"error":"capture not running"}`); those bytes went straight to
    /// `UIImage(data:)`, which returns nil — so the tile showed no frame and no
    /// reason, forever. Check the status and carry the message out.
    func frameData() async throws -> Data {
        guard let url = captureFrameURL() else { throw AgentError(message: "bad host") }
        var req = URLRequest(url: url)
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        let (data, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "no response") }
        guard (200..<300).contains(http.statusCode) else {
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String {
                throw AgentError(message: err)
            }
            throw AgentError(message: "capture frame unavailable (\(http.statusCode))")
        }
        return data
    }
}
