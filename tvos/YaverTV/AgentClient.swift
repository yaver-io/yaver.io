// AgentClient.swift — calls a Yaver agent's /ops endpoint over LAN HTTP.
//
// Mirrors mobile/src/lib/appletvClient.ts::atvOps: POST http://<host>:<port>/ops
// with body { verb, payload, machine:"local" } + Authorization: Bearer <token>.
// The agent returns either the result object directly or { initial: <result> }
// for streaming verbs; we unwrap `initial` like the RN client does.

import Foundation

struct AgentError: AgentErrorCoded, LocalizedError {
    let message: String
    /// The structured capability gap the agent attached to this refusal, when
    /// it attached one. `message` stays exactly what it always was — a shipped
    /// view that renders only the message must not lose a word — and `gap` is
    /// the additive route a view can turn into a button.
    ///
    /// Without this, every 412 from /dev/start arrived as the flat sentence
    /// "flutter is not installed", the `fix` object was discarded by the
    /// transport, and the TV showed a spinner over a fact the agent had
    /// already stated. Same shape as the 2026-07-26 phone incident.
    var gap: CapabilityGap? = nil
    /// Stable reason code from the agent's error body (`code` key —
    /// reason_codes.go vocabulary, e.g. auth.session.scope_denied). Lets views
    /// classify a refusal without regexing prose. nil on old agents.
    var code: String? = nil
    /// A 409 from /tasks/{id}/continue can mean the agent KEPT the user's
    /// words because the selected runner needs authentication. That is queued
    /// work, not a failed send; callers must not restore it into the composer.
    var parked: Bool = false
    var reauthable: Bool = false
    var runner: String? = nil
    /// True when this refusal came back over the RELAY leg with a credential
    /// deny ("invalid relay password" & friends) — i.e. the account's stored
    /// relay password drifted, not the box being down. The client normally
    /// self-heals this via /settings/repair-relay before the view ever sees
    /// it; when repair fails, the view can render a named "Repair relay"
    /// route instead of a dead "Try again".
    var relayDeny: Bool = false
    var errorDescription: String? { message }

    /// Decode the structured refusal envelope shared by task, preview, and
    /// runtime routes. Keeping this pure makes the parked-turn promise
    /// regression-testable without standing up a network fixture.
    static func fromHTTPBody(_ data: Data, gap: CapabilityGap? = nil) -> AgentError? {
        guard let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let message = obj["error"] as? String, !message.isEmpty else { return nil }
        return AgentError(
            message: message,
            gap: gap,
            code: obj["code"] as? String,
            parked: obj["parked"] as? Bool ?? false,
            reauthable: obj["reauthable"] as? Bool ?? false,
            runner: obj["runner"] as? String
        )
    }
}

actor AgentClient {
    private let token: String
    private var box: BoxTarget
    private let session: URLSession

    private func clientSessionSettings() -> [String: Any] {
        #if os(visionOS)
        let platform = "visionos"
        let surface = "vision-pro"
        let deviceClass = "xr"
        #elseif os(tvOS)
        let platform = "tvos"
        let surface = "apple-tv"
        let deviceClass = "tv"
        #elseif os(watchOS)
        let platform = "watchos"
        let surface = "apple-watch"
        let deviceClass = "watch"
        #else
        let platform = "ios"
        let surface = Backend.surface
        let deviceClass = "phone"
        #endif
        return [
            "appName": "Yaver",
            "appVersion": Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "",
            "buildNumber": Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "",
            "surface": surface,
            "clientSurface": surface,
            "platform": platform,
            "deviceClass": deviceClass,
            "lane": "yaver-native",
            "runtimeMode": "native",
            "dogfood": false,
            "usageMode": "chat-only",
            "chatEnabled": true,
            "renderEnabled": false,
        ]
    }

    /// The relay-credential self-heal injected by YaverStore: POST
    /// /settings/repair-relay, adopt the corrected password, and hand back the
    /// REPAIRED box (nil when repair failed or there is nothing to repair).
    /// Called at most ONCE per request streak — a repair that does not fix the
    /// relay is a real failure, and looping on it is how a TV hammers the
    /// platform while a stale token re-repairs nothing.
    private let relayRepair: (@Sendable () async -> BoxTarget?)?

    private struct Endpoint {
        let url: URL
        let relay: Bool
    }

    init(token: String, box: BoxTarget, relayRepair: (@Sendable () async -> BoxTarget?)? = nil) {
        self.token = token
        self.box = box
        self.relayRepair = relayRepair
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
                // Preserve the structured refusal envelope — `code`,
                // `capabilityGap`, relay flag — instead of flattening a
                // deterministic verdict (auth.session.scope_denied & friends)
                // to prose that a view must regex.
                if let refusal = AgentError.fromHTTPBody(data) { throw refusal }
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
                if let refusal = AgentError.fromHTTPBody(data) { throw refusal }
                throw AgentError(message: obj["error"] as? String ?? "\(verb) failed")
            }
            if let err = obj["error"] as? String {
                if let refusal = AgentError.fromHTTPBody(data) { throw refusal }
                throw AgentError(message: err)
            }
            if let ok = obj["ok"] as? Bool { return ok }
        }
        return true
    }

    /// Run an ops verb, trying LAN first and the relay second.
    ///
    /// `machine` selects the TARGET of the verb once a reachable agent is
    /// found: "local" drives the box we connected to, any other device id or
    /// alias is proxied onward by the agent's dispatchOps. That is what lets an
    /// Apple TV drive a Windows tower through whichever box it can actually
    /// reach.
    private func rawOps(_ verb: String, _ payload: [String: Any], machine: String = "local") async throws -> Data {
        let body = try JSONSerialization.data(withJSONObject: [
            "verb": verb,
            "payload": payload,
            "machine": machine,
        ])

        // Two passes at most: the original, then one re-run after a relay
        // credential self-heal (a stale per-user relay password is the #1
        // reason a TV's relay leg 401s while the box is fine — mobile and web
        // auto-repair it, and "in tasks: invalid relay password" was the TV
        // reporting the same drift with no repair). The endpoints are
        // RECOMPUTED each pass so the repaired password rides the retry.
        var repairedOnce = false
        var finalError: Error = AgentError(message: "ops \(verb) failed")
        for _ in 0..<2 {
            // Preserve the relay bit even when relay is the only endpoint.
            // Split runner boxes intentionally have no direct host, so their
            // relay is index zero and still requires X-Relay-Password.
            let endpoints = box.requestEndpoints(path: "/ops")
            guard !endpoints.isEmpty else { throw AgentError(message: "bad box host") }

            var lastError: Error = AgentError(message: "ops \(verb) failed")
            for endpoint in endpoints {
                let url = endpoint.url
                var req = URLRequest(url: url)
                req.httpMethod = "POST"
                req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
                req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
                if let pw = box.relayPassword, !pw.isEmpty, endpoint.relay {
                    req.setValue(pw, forHTTPHeaderField: "X-Relay-Password")
                }
                req.httpBody = body

                do {
                    let (data, resp) = try await session.data(for: req)
                    guard let http = resp as? HTTPURLResponse else {
                        throw AgentError(message: "no response")
                    }
                    // The agent returns 200 for results and also 4xx with an
                    // {error} body; surface the error message when present, like
                    // the RN client.
                    if !(200..<300).contains(http.statusCode) {
                        if let refusal = AgentError.fromHTTPBody(data) {
                            // A real answer from a reachable agent — do NOT
                            // retry the next endpoint. Retrying would re-run a
                            // verb that already executed and merely reported a
                            // refusal. The structured envelope travels so the
                            // TV can classify scope/relay denials without
                            // regexing prose.
                            throw refusal
                        }
                        if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                           let err = obj["error"] as? String {
                            // A real answer from a reachable agent — do NOT retry
                            // the next endpoint. Retrying would re-run a verb that
                            // already executed and merely reported a refusal.
                            throw AgentError(message: err)
                        }
                        lastError = AgentError(message: "ops \(verb) failed (\(http.statusCode))")
                        continue // transport-level failure: try the relay
                    }
                    return data
                } catch let err as AgentError {
                    // A relay credential deny is self-healable: repair once and
                    // re-run the whole leg. Any other refusal is final.
                    if endpoint.relay, FailureSignals.isRelayCredentialDeny(err.message), !repairedOnce {
                        if let repaired = await relayRepair?() {
                            self.box = repaired
                            repairedOnce = true
                            lastError = err
                            break // recompute endpoints with the new password
                        }
                    }
                    var flagged = err
                    if endpoint.relay, FailureSignals.isRelayCredentialDeny(err.message) {
                        flagged.relayDeny = true
                    }
                    throw flagged
                } catch {
                    // Connection refused / timeout / DNS — this leg is dead, try
                    // the next one.
                    lastError = error
                    continue
                }
            }
            if !repairedOnce { throw lastError }
            finalError = lastError
        }
        throw finalError
    }

    /// Speak-to-control a desktop from the TV. Reads the target machine's
    /// accessibility tree and returns ONE spoken sentence — no video stream, so
    /// it works on a lean-back surface and costs effectively no relay egress.
    func desktopVoice(_ transcript: String, machine: String = "local") async throws -> String {
        let data = try await rawOps("desktop_voice", ["transcript": transcript], machine: machine)
        guard let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let initial = obj["initial"] as? [String: Any],
              let spoken = initial["spoken"] as? String,
              !spoken.isEmpty
        else { return "Done." }
        return spoken
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

    /// Stream the classified tail of ONE live tmux session.
    ///
    /// This is the constrained-surface lane: a TV/headset does not own a PTY
    /// or terminal emulator, but it must see output and menu transitions that
    /// happen after a turn. Polling `runner_sessions` cannot answer either.
    /// LAN is attempted first and relay second with the same bearer/password
    /// boundary as every other native SSE stream.
    func subscribeTmuxPane(
        session sessionName: String,
        onPane: @escaping @Sendable (TmuxPaneFrame) -> Void,
        onDone: (@Sendable (String?) -> Void)? = nil,
        onEnd: (@Sendable (FailureSignals.StreamEndKind, String?) -> Void)? = nil
    ) -> Task<Void, Never> {
        var components = URLComponents()
        components.path = "/tmux/stream"
        components.queryItems = [URLQueryItem(name: "session", value: sessionName)]
        let path = components.string ?? "/tmux/stream"
        let endpoints = requestEndpoints(path: path)
        let token = self.token
        let relayPassword = box.relayPassword
        let urlSession = self.session

        return Task {
            var lastError = "live session stream unavailable"
            var connected = false
            for endpoint in endpoints {
                if Task.isCancelled { onEnd?(.cancelled, nil); return }
                var req = URLRequest(url: endpoint.url)
                req.httpMethod = "GET"
                // This is an SSE subscription, not an ordinary API request.
                // The shared session's 30-second request timeout is correct
                // for verbs but turns a quiet coding phase into a false dead
                // console. The server owns stream completion via `done`.
                req.timeoutInterval = 24 * 60 * 60
                req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
                req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
                if endpoint.relay, let relayPassword, !relayPassword.isEmpty {
                    req.setValue(relayPassword, forHTTPHeaderField: "X-Relay-Password")
                }
                do {
                    let (bytes, response) = try await urlSession.bytes(for: req)
                    guard let http = response as? HTTPURLResponse,
                          (200..<300).contains(http.statusCode) else {
                        lastError = Self.sseErrorText(
                            (response as? HTTPURLResponse)?.statusCode ?? -1,
                            fallback: "live session stream unavailable"
                        )
                        continue
                    }
                    connected = true
                    var eventName = "message"
                    var dataLines: [String] = []
                    for try await line in bytes.lines {
                        if Task.isCancelled { onEnd?(.cancelled, nil); return }
                        if line.isEmpty {
                            if Self.emitTmuxPaneEvent(eventName, dataLines, onPane: onPane, onDone: onDone) {
                                onEnd?(.done, nil)
                                return
                            }
                            eventName = "message"
                            dataLines.removeAll(keepingCapacity: true)
                            continue
                        }
                        if line.hasPrefix("event:") {
                            eventName = String(line.dropFirst(6)).trimmingCharacters(in: .whitespaces)
                        } else if line.hasPrefix("data:") {
                            dataLines.append(String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces))
                        }
                    }
                    if Self.emitTmuxPaneEvent(eventName, dataLines, onPane: onPane, onDone: onDone) {
                        onEnd?(.done, nil)
                    } else {
                        onEnd?(.interrupted, "the box closed the live session stream")
                    }
                    return
                } catch {
                    if Task.isCancelled { onEnd?(.cancelled, nil); return }
                    lastError = error.localizedDescription
                    if connected {
                        onEnd?(.interrupted, lastError)
                        return
                    }
                }
            }
            onEnd?(.interrupted, lastError)
        }
    }

    /// Returns true only for the stream's explicit terminal event.
    private nonisolated static func emitTmuxPaneEvent(
        _ eventName: String,
        _ dataLines: [String],
        onPane: @escaping @Sendable (TmuxPaneFrame) -> Void,
        onDone: (@Sendable (String?) -> Void)?
    ) -> Bool {
        guard !dataLines.isEmpty else { return false }
        let payload = dataLines.joined(separator: "\n")
        guard let data = payload.data(using: .utf8) else { return false }
        switch eventName {
        case "pane":
            // `null` is the all-mode empty snapshot. A session-targeted stream
            // never emits it, but decoding it as no frame keeps this parser
            // correct if the endpoint is reused by a list later.
            if let frame = try? JSONDecoder().decode(TmuxPaneFrame.self, from: data) {
                onPane(frame)
            }
            return false
        case "done":
            let reason = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["reason"] as? String
            onDone?(reason)
            return true
        default:
            return false // ping and future additive events
        }
    }

    func platformMatrix() async throws -> PlatformMatrixEnvelope {
        try await ops("mobile_platform_matrix", [:], as: PlatformMatrixEnvelope.self)
    }

    /// The task queue on the box (GET /tasks). REST, not an ops verb.
    func listTasks() async throws -> [TaskSummary] {
        let data = try await request("GET", path: "/tasks", failure: "couldn't load tasks")
        return (try JSONDecoder().decode(TaskList.self, from: data)).tasks
    }

    /// Runners and models the selected machine can actually execute. This is
    /// the same operation mobile/web use; heartbeat inventory does not carry
    /// the machine's live provider/model catalogue.
    func listRunners() async throws -> AgentRunnerList {
        let data = try await request("GET", path: "/agent/runners",
                                     failure: "couldn't load coding agents")
        return try JSONDecoder().decode(AgentRunnerList.self, from: data)
    }

    /// Full task detail — transcript + result — for the native Chat view.
    func task(_ id: String) async throws -> TaskSummary {
        let data = try await request("GET", path: "/tasks/\(id)", failure: "couldn't load the conversation")
        if let wrapped = try? JSONDecoder().decode(TaskEnvelope.self, from: data) {
            return wrapped.task
        }
        return try JSONDecoder().decode(TaskSummary.self, from: data)
    }

    /// Runner-native controls for this exact task and machine. A surface must
    /// not reuse the global runner picker because provider/model inventories
    /// can differ per box and `/exit` must be verified by the owning agent.
    func taskRunnerControls(_ id: String) async throws -> TaskRunnerControlCatalog {
        let data = try await request("GET", path: "/tasks/\(id)/control",
                                     failure: "couldn't load this runner's controls")
        return try JSONDecoder().decode(TaskRunnerControlCatalog.self, from: data)
    }

    func applyTaskRunnerControl(
        _ id: String,
        control: String,
        model: String? = nil,
        reasoningEffort: String? = nil,
        confirmed: Bool = false
    ) async throws -> TaskRunnerControlResult {
        var body: [String: Any] = ["control": control]
        if let model, !model.isEmpty { body["model"] = model }
        if let reasoningEffort, !reasoningEffort.isEmpty { body["reasoningEffort"] = reasoningEffort }
        if confirmed { body["confirmed"] = true }
        let data = try await request("POST", path: "/tasks/\(id)/control", jsonBody: body,
                                     failure: "the runner control failed")
        return try JSONDecoder().decode(TaskRunnerControlResult.self, from: data)
    }

    /// Continue a live task in place, matching mobile's `continueTask` path.
    func continueTask(_ id: String, input: String, mode: String = "") async throws {
        var body: [String: Any] = ["input": input, "sessionSettings": clientSessionSettings()]
        if !mode.isEmpty { body["mode"] = mode }
        let data = try await request("POST", path: "/tasks/\(id)/continue", jsonBody: body,
                                     failure: "couldn't continue the conversation")
        guard let result = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              result["taskId"] as? String == id,
              result["sameTask"] as? Bool != false,
              let execution = result["executionSession"] as? [String: Any],
              execution["taskId"] as? String == id else {
            throw AgentError(message: "The agent did not confirm the same task and runner session for this follow-up.")
        }
    }

    /// Answer a structured question raised by the runner in this task. TV
    /// sessions may answer text/choice questions; the agent rejects secret
    /// questions for scoped tokens so credentials stay on a private surface.
    func answerTaskQuestion(_ taskId: String, questionId: String, answer: String) async throws {
        _ = try await request(
            "POST",
            path: "/tasks/\(taskId)/answer",
            jsonBody: ["questionId": questionId, "answer": answer],
            failure: "couldn't deliver the answer"
        )
    }

    /// Explicit context handoff endpoint. Ordinary replies never call this:
    /// they continue the task-owned runner/tmux session through continueTask.
    func forkTask(
        _ id: String,
        runner: String,
        model: String = "",
        mode: String = "",
        input: String,
        projectDir: String? = nil,
        mcpServers: [String] = [],
        includeYaverMcp: Bool = false
    ) async throws -> TaskForkResult {
        var body: [String: Any] = [
            "runner": runner,
            "input": input,
            "contextWords": 1200,
            "allowLocalFallback": true,
            "mcpServers": mcpServers,
            "includeYaverMcp": includeYaverMcp,
        ]
        if !model.isEmpty { body["model"] = model }
        if !mode.isEmpty { body["mode"] = mode }
        if let projectDir, !projectDir.isEmpty { body["projectDir"] = projectDir }
        let data = try await request("POST", path: "/tasks/\(id)/fork", jsonBody: body,
                                     failure: "couldn't continue the finished conversation")
        return try JSONDecoder().decode(TaskForkResult.self, from: data)
    }

    /// Stream a task's live output (GET /tasks/{id}/output?rawSince=…).
    ///
    /// Same SSE frame vocabulary the phone consumes (quic.ts::streamTaskOutput):
    ///   - {type:"output", text}       groomed transcript chunk
    ///   - {type:"raw", text, offset}  raw runner stdout (ANSI intact), live
    ///   - {type:"raw_replay", text, offset, full} one-shot seed of the raw tail
    ///                                 at subscribe time (full=true → replace)
    ///   - {type:"done", status}       terminal state
    ///   - {type:"agent_question", question} runner is asking the human
    ///
    /// `rawSince: 0` seeds a terminal with the full retained tail; pass the
    /// `offset` from the previous `raw`/`raw_replay` frame to resume without
    /// gaps. `onDone` fires exactly once with the terminal status; `onEnd`
    /// fires when the stream ends for ANY other reason (drop, relay bounce,
    /// cancel) so a frozen console is never silent — same discipline as
    /// subscribeDevEvents.
    struct TaskOutputEvent: Decodable {
        let type: String?
        let text: String?
        let status: String?
        let offset: Int?
        let full: Bool?
        let question: TaskAgentQuestion?
        let questionId: String?
        let schema: Int?
        let op: String?
        let seq: Int64?
        let message: TaskPresentationMessage?
        let messages: [TaskPresentationMessage]?
    }

    /// Compatibility bridge for existing consumers which only need appended
    /// groomed text. The richer overload below owns resume offsets/full-replay
    /// semantics for conversation surfaces; keeping this adapter makes that
    /// transport evolution additive instead of forcing unrelated panels to
    /// change in the same release.
    func subscribeTaskOutput(
        taskId: String,
        rawSince: Int? = nil,
        onRaw: (@Sendable (String, Int, Bool) -> Void)? = nil,
        onData: (@Sendable (String) -> Void)?,
        onDone: (@Sendable (String) -> Void)? = nil,
        onQuestion: (@Sendable (TaskAgentQuestion) -> Void)? = nil,
        onQuestionClosed: (@Sendable (String?) -> Void)? = nil,
        onPresentation: (@Sendable (TaskPresentationWireEvent) -> Void)? = nil,
        onEnd: (@Sendable (FailureSignals.StreamEndKind, String?) -> Void)? = nil
    ) -> Task<Void, Never> {
        let bridgedOnData: (@Sendable (String, Int?, Bool) -> Void)?
        if let onData {
            bridgedOnData = { text, _, _ in onData(text) }
        } else {
            bridgedOnData = nil
        }
        return subscribeTaskOutput(
            taskId: taskId,
            since: nil,
            rawSince: rawSince,
            onRaw: onRaw,
            onData: bridgedOnData,
            onDone: onDone,
            onQuestion: onQuestion,
            onQuestionClosed: onQuestionClosed,
            onPresentation: onPresentation,
            onEnd: onEnd
        )
    }

    func subscribeTaskOutput(
        taskId: String,
        since: Int? = nil,
        rawSince: Int? = nil,
        onRaw: (@Sendable (String, Int, Bool) -> Void)? = nil,
        onData: (@Sendable (String, Int?, Bool) -> Void)? = nil,
        onDone: (@Sendable (String) -> Void)? = nil,
        onQuestion: (@Sendable (TaskAgentQuestion) -> Void)? = nil,
        onQuestionClosed: (@Sendable (String?) -> Void)? = nil,
        onPresentation: (@Sendable (TaskPresentationWireEvent) -> Void)? = nil,
        onEnd: (@Sendable (FailureSignals.StreamEndKind, String?) -> Void)? = nil
    ) -> Task<Void, Never> {
        var queryItems: [URLQueryItem] = []
        if let since, since >= 0 {
            queryItems.append(URLQueryItem(name: "since", value: String(since)))
        }
        if let rawSince, rawSince >= 0 {
            queryItems.append(URLQueryItem(name: "rawSince", value: String(rawSince)))
        }
        var components = URLComponents()
        components.path = "/tasks/\(taskId)/output"
        components.queryItems = queryItems.isEmpty ? nil : queryItems
        let query = components.string ?? "/tasks/\(taskId)/output"
        let endpoints = requestEndpoints(path: query)
        let token = self.token
        let relayPassword = box.relayPassword
        let urlSession = self.session
        return Task {
            var lastError = "task output stream unavailable"
            var connected = false
            var sawDone = false
            var replaceNextOutput = false
            for endpoint in endpoints {
                if Task.isCancelled { onEnd?(.cancelled, nil); return }
                var req = URLRequest(url: endpoint.url)
                req.httpMethod = "GET"
                req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
                req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
                if endpoint.relay, let relayPassword, !relayPassword.isEmpty {
                    req.setValue(relayPassword, forHTTPHeaderField: "X-Relay-Password")
                }
                do {
                    let (bytes, resp) = try await urlSession.bytes(for: req)
                    guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                        // Name the relay's verdicts when it refused us: a stale
                        // per-user password 401s with "invalid relay password"
                        // and the relay rate-limits 429 — a bare status code
                        // told the couch nothing about either.
                        lastError = Self.sseErrorText((resp as? HTTPURLResponse)?.statusCode ?? -1,
                                                      fallback: "task output stream unavailable")
                        continue
                    }
                    connected = true
                    var dataLines: [String] = []
                    for try await line in bytes.lines {
                        if Task.isCancelled { onEnd?(.cancelled, nil); return }
                        if line.isEmpty {
                            emitTaskOutput(dataLines, onRaw: onRaw, onData: onData, onDone: onDone,
                                           onQuestion: onQuestion, onQuestionClosed: onQuestionClosed,
                                           onPresentation: onPresentation,
                                           sawDone: &sawDone, replaceNextOutput: &replaceNextOutput)
                            dataLines.removeAll(keepingCapacity: true)
                            continue
                        }
                        if line.hasPrefix("data:") {
                            dataLines.append(String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces))
                        }
                    }
                    emitTaskOutput(dataLines, onRaw: onRaw, onData: onData, onDone: onDone,
                                   onQuestion: onQuestion, onQuestionClosed: onQuestionClosed,
                                   onPresentation: onPresentation,
                                   sawDone: &sawDone, replaceNextOutput: &replaceNextOutput)
                    // The body ended. If we never saw `done`, this is an
                    // interruption — the box closed the stream or the relay
                    // dropped it — and saying nothing froze the console.
                    if !sawDone {
                        onEnd?(.interrupted, "the box closed the task output stream")
                    } else {
                        onEnd?(.done, nil)
                    }
                    return
                } catch {
                    if Task.isCancelled { onEnd?(.cancelled, nil); return }
                    lastError = error.localizedDescription
                    if connected {
                        onEnd?(.interrupted, lastError)
                        return
                    }
                    continue
                }
            }
            onEnd?(.interrupted, lastError)
        }
    }

    private nonisolated func emitTaskOutput(
        _ dataLines: [String],
        onRaw: (@Sendable (String, Int, Bool) -> Void)?,
        onData: (@Sendable (String, Int?, Bool) -> Void)?,
        onDone: (@Sendable (String) -> Void)?,
        onQuestion: (@Sendable (TaskAgentQuestion) -> Void)?,
        onQuestionClosed: (@Sendable (String?) -> Void)?,
        onPresentation: (@Sendable (TaskPresentationWireEvent) -> Void)?,
        sawDone: inout Bool,
        replaceNextOutput: inout Bool
    ) {
        guard !dataLines.isEmpty else { return }
        let payload = dataLines.joined(separator: "\n")
        guard let data = payload.data(using: .utf8),
              let event = try? JSONDecoder().decode(TaskOutputEvent.self, from: data)
        else { return }
        switch event.type ?? "" {
        case "raw":
            if let onRaw, let text = event.text, !text.isEmpty {
                onRaw(text, event.offset ?? 0, event.full ?? false)
            }
        case "raw_replay":
            if let onRaw, let text = event.text, !text.isEmpty {
                onRaw(text, event.offset ?? 0, event.full ?? true)
            }
        case "resume":
            replaceNextOutput = event.full ?? false
        case "output":
            if let onData, let text = event.text, !text.isEmpty {
                onData(text, event.offset, replaceNextOutput)
                replaceNextOutput = false
            }
        case "done":
            if !sawDone, let onDone {
                sawDone = true
                onDone(event.status ?? "completed")
            }
        case "agent_question":
            if let question = event.question { onQuestion?(question) }
        case "agent_answered", "agent_question_cancelled":
            onQuestionClosed?(event.questionId)
        case "presentation", "presentation_snapshot":
            onPresentation?(TaskPresentationWireEvent(
                type: event.type ?? "presentation",
                schema: event.schema,
                op: event.op,
                seq: event.seq,
                message: event.message,
                messages: event.messages
            ))
        default:
            break // Future event types remain additive.
        }
    }

    /// START a vibe from a native surface (POST /tasks).
    ///
    /// THE ONE CAPABILITY THAT MADE EVERY NATIVE SURFACE UNVIBEABLE (2026-08-03).
    /// Before this, `AgentClient` had `listTasks()` and NO POST verb at all, on
    /// tvOS, visionOS (which shares this file), watch and Wear. Those surfaces
    /// could WATCH work happen and never START it — which is why the coverage
    /// audit reads "untested" when the honest word is "unable". It is also why
    /// the colour closed loop cannot run there: step one of the loop is
    /// "send: change the login background to red".
    ///
    /// Deliberately mirrors the web dispatch funnel's body
    /// (`buildCreateTaskBody`) so a task started from a TV is indistinguishable
    /// from one started in the dashboard — same runner/model resolution on the
    /// agent, same failure classification coming back. Leaving `runner`/`model`
    /// empty is the CORRECT default: the agent then applies the account's
    /// per-device primary (userSettings.primaryRunnerByDevice), which is the
    /// same precedence the phone gets. A TV inventing its own default is how
    /// surfaces drift onto a model the subscription cannot run.
    @discardableResult
    func createTask(
        title: String,
        description: String,
        workDir: String,
        projectName: String = "",
        runner: String = "",
        model: String = "",
        reasoningEffort: String = "",
        mode: String = "",
        goal: String = "",
        askMode: Bool = false,
        mcpServers: [String] = [],
        includeYaverMcp: Bool = false,
        sessionStartedFrom: String = "tasks"
    ) async throws -> TaskSummary {
        var body: [String: Any] = [
            "title": title,
            "description": description,
            "workDir": workDir,
        ]
        if !projectName.isEmpty { body["projectName"] = projectName }
        if !runner.isEmpty { body["runner"] = runner }
        if !model.isEmpty { body["model"] = model }
        if !reasoningEffort.isEmpty { body["reasoningEffort"] = reasoningEffort }
        // opencode agent mode (build/plan/custom — maps to `opencode run
        // --agent <mode>`) and goal-mode (persistent opencode-goal-plugin
        // objective) travel on the body exactly like mobile/web so a
        // TV-started task is indistinguishable from a dashboard one.
        if !mode.isEmpty { body["mode"] = mode }
        if !goal.isEmpty { body["goal"] = goal }
        // askMode opts the task into the grounded explain-first preamble
        // (askModePreamble) — the "deep audit this" frame from the couch.
        if askMode { body["askMode"] = true }
        // MCP doorway parity with mobile/web: external servers + the yaver
        // toggle travel on the task body so a TV-started task is
        // indistinguishable from one started in the dashboard (2026-08-10).
        if !mcpServers.isEmpty { body["mcpServers"] = mcpServers }
        body["includeYaverMcp"] = includeYaverMcp
        body["sessionStartedFrom"] = sessionStartedFrom
        body["sessionSettings"] = clientSessionSettings()

        let data = try await request("POST", path: "/tasks", jsonBody: body,
                                     failure: "couldn't start the task")
        // The agent answers either the bare task or {task:{…}} depending on
        // route age; accept both rather than fail a started task on shape.
        let decoded: TaskSummary
        if let wrapped = try? JSONDecoder().decode(TaskEnvelope.self, from: data) {
            decoded = wrapped.task
        } else {
            decoded = try JSONDecoder().decode(TaskSummary.self, from: data)
        }
        // Current POST /tasks replies with taskId/status/runnerId but omits the
        // display title. The work is already running, so enrich the response
        // locally instead of claiming a decode/display failure and inviting a
        // duplicate retry.
        guard decoded.title == nil else { return decoded }
        return TaskSummary(
            id: decoded.id,
            title: title,
            status: decoded.status,
            runner: decoded.runner,
            model: decoded.model,
            reasoningEffort: decoded.reasoningEffort,
            workDir: decoded.workDir,
            projectName: decoded.projectName,
            sessionId: decoded.sessionId,
            output: decoded.output,
            resultText: decoded.resultText,
            presentation: decoded.presentation,
            turns: decoded.turns,
            pendingFollowUps: decoded.pendingFollowUps,
            tmuxSession: decoded.tmuxSession,
            executionSession: decoded.executionSession
        )
    }

    /// One project-start operation shared with phone, web, desktop, and
    /// spatial clients. The kickoff prompt is hidden by the agent, so the
    /// first visible turn is Developing asking what the app should do.
    func startProject(
        name: String,
        gitProvider: String = "yaver-git",
        palette: String = "ocean"
    ) async throws -> ProjectStartEnvelope {
        let data = try await request(
            "POST",
            path: "/project/start",
            jsonBody: ["name": name, "gitProvider": gitProvider, "palette": palette],
            failure: "couldn't start the project"
        )
        return try JSONDecoder().decode(ProjectStartEnvelope.self, from: data)
    }

    /// Projects the box knows about (GET /projects → {projects:[…]} or a bare
    /// array). For the TV to browse and pick one to preview.
    func listProjects() async throws -> [ProjectSummary] {
        let data: Data
        do {
            data = try await request("GET", path: "/projects", failure: "couldn't load projects")
        } catch {
            // A stale/empty discovery cache must not strand Vibing behind a
            // generic error. Ask the agent to rescan once, then read the same
            // canonical endpoint again. If the box is genuinely unreachable,
            // the original transport error is preserved.
            // Companion scopes may browse the canonical list, but must not
            // mutate the box's project inventory as a refresh side effect.
            // Retry the read once; the agent can refresh its cache on its own.
            data = try await request("GET", path: "/projects", failure: "couldn't load projects after refresh")
        }
        if let wrapped = try? JSONDecoder().decode(ProjectList.self, from: data) { return wrapped.projects }
        return (try? JSONDecoder().decode([ProjectSummary].self, from: data)) ?? []
    }

    /// Apps declared inside a selected monorepo. This is queried only after the
    /// repository is selected, so the next screen contains that repository's
    /// mobile/web/native targets rather than another global project picker.
    func workspaceApps(root: String) async throws -> [WorkspaceAppSummary] {
        var components = URLComponents()
        components.path = "/workspace/apps"
        components.queryItems = [URLQueryItem(name: "root", value: root)]
        guard let path = components.string else { throw AgentError(message: "invalid workspace path") }
        let data = try await request("GET", path: path, failure: "couldn't inspect this workspace")
        return try JSONDecoder().decode(WorkspaceAppList.self, from: data).apps
    }

    /// What this repository/app and this box can actually preview, filtered by
    /// the client surface. `probe=true` attempts the browser/toolchain instead
    /// of trusting PATH inventory; a stub browser must not become a live button.
    func previewCapabilities(for project: ProjectSummary, probe: Bool = true) async throws -> ProjectPreviewCapabilities {
        guard let workDir = project.path, !workDir.isEmpty else {
            throw AgentError(message: "This project has no path on the selected machine.")
        }
        var components = URLComponents()
        components.path = "/project/preview-capabilities"
        components.queryItems = [
            URLQueryItem(name: "workDir", value: workDir),
            URLQueryItem(name: "framework", value: project.framework ?? ""),
            URLQueryItem(name: "surface", value: Backend.surface),
            URLQueryItem(name: "probe", value: probe ? "true" : "false"),
        ]
        guard let path = components.string else { throw AgentError(message: "invalid project path") }
        let data = try await request("GET", path: path, failure: "couldn't inspect preview options")
        return try JSONDecoder().decode(ProjectPreviewCapabilities.self, from: data)
    }

    /// External MCP servers the box exposes (GET /mcp/servers or the ops
    /// surface). The TV shows these as toggles beside the yaver doorway; the
    /// chosen set rides on task bodies exactly like mobile/web.
    func listMCPServers() async throws -> [McpServerSummary] {
        struct McpEnvelope: Decodable { let servers: [McpServerSummary]? }
        let data = try await request("GET", path: "/mcp/servers", failure: "couldn't load MCP servers")
        if let env = try? JSONDecoder().decode(McpEnvelope.self, from: data), let servers = env.servers {
            return servers
        }
        return (try? JSONDecoder().decode([McpServerSummary].self, from: data)) ?? []
    }

    // ---- Interactive remote runtime (WebRTC media + HTTP control) --------

    func remoteRuntimeCapabilities(for project: ProjectSummary, refresh: Bool = false) async throws -> RemoteRuntimeCapabilities {
        guard let workDir = project.path, !workDir.isEmpty else {
            throw AgentError(message: "This project has no path on the selected machine.")
        }
        var components = URLComponents()
        components.path = "/remote-runtime/capabilities"
        components.queryItems = [
            URLQueryItem(name: "workDir", value: workDir),
            URLQueryItem(name: "framework", value: project.framework ?? ""),
        ]
        if refresh { components.queryItems?.append(URLQueryItem(name: "refresh", value: "1")) }
        guard let path = components.string else { throw AgentError(message: "invalid remote runtime query") }
        let data = try await request("GET", path: path, failure: "couldn't inspect interactive runtime targets")
        return try JSONDecoder().decode(RemoteRuntimeCapabilities.self, from: data)
    }

    func startRemoteRuntimeSession(
        for project: ProjectSummary,
        targetId: String,
        transportMode: String = "direct-webrtc",
        clientId: String? = nil,
        surface: String? = nil
    ) async throws -> RemoteRuntimeSession {
        guard let workDir = project.path, !workDir.isEmpty else {
            throw AgentError(message: "This project has no path on the selected machine.")
        }
        var body: [String: Any] = [
            "workDir": workDir,
            "framework": project.framework ?? "",
            "targetId": targetId,
            "transportMode": transportMode,
        ]
        if let clientId { body["clientId"] = clientId }
        if let surface { body["surface"] = surface }
        let data = try await request(
            "POST",
            path: "/remote-runtime/sessions",
            jsonBody: body,
            failure: "couldn't start the interactive runtime"
        )
        return try JSONDecoder().decode(RemoteRuntimeSession.self, from: data)
    }

    /// List the live shared-session roster, optionally filtered to one project.
    /// A returning surface polls this to find "the room that was vibing while I
    /// was gone" and rejoin it by id instead of creating a second capture.
    /// The agent stamps each entry with viewerCount + startedBy + sourceSurface.
    func listRemoteRuntimeSessions(project: String? = nil) async throws -> [RemoteRuntimeSession] {
        var path = "/remote-runtime/sessions"
        if let project, !project.isEmpty {
            let escaped = project.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? project
            path += "?project=\(escaped)"
        }
        let data = try await request("GET", path: path, failure: "couldn't list live sessions")
        let decoded = try JSONDecoder().decode(RemoteRuntimeRoster.self, from: data)
        return decoded.sessions ?? []
    }

    func remoteRuntimeICECredentials() async throws -> RemoteRuntimeICECredentials {
        let data = try await request(
            "GET",
            path: "/remote-runtime/turn-credentials",
            failure: "couldn't load WebRTC relay credentials"
        )
        return try JSONDecoder().decode(RemoteRuntimeICECredentials.self, from: data)
    }

    func answerRemoteRuntimeWebRTC(sessionId: String, offerSDP: String) async throws -> RemoteRuntimeWebRTCAnswer {
        let data = try await request(
            "POST",
            path: "/remote-runtime/sessions/\(sessionId)/webrtc/offer",
            jsonBody: ["type": "offer", "sdp": offerSDP],
            failure: "couldn't negotiate the interactive WebRTC stream"
        )
        return try JSONDecoder().decode(RemoteRuntimeWebRTCAnswer.self, from: data)
    }

    func remoteRuntimeFrame(sessionId: String) async throws -> Data {
        try await request(
            "GET",
            path: "/remote-runtime/sessions/\(sessionId)/frame?ts=\(Int(Date().timeIntervalSince1970 * 1000))",
            failure: "interactive frame unavailable"
        )
    }

    @discardableResult
    func sendRemoteRuntimeControl(
        sessionId: String,
        action: String,
        x: Int? = nil,
        y: Int? = nil,
        x2: Int? = nil,
        y2: Int? = nil,
        durationMs: Int? = nil,
        text: String? = nil,
        key: String? = nil,
        clientId: String,
        clientLabel: String = "Apple TV"
    ) async throws -> RemoteRuntimeSession {
        var body: [String: Any] = [
            "action": action,
            "clientId": clientId,
            "clientLabel": clientLabel,
        ]
        if let x { body["x"] = x }
        if let y { body["y"] = y }
        if let x2 { body["x2"] = x2 }
        if let y2 { body["y2"] = y2 }
        if let durationMs { body["durationMs"] = durationMs }
        if let text { body["text"] = text }
        if let key { body["key"] = key }
        let data = try await request(
            "POST",
            path: "/remote-runtime/sessions/\(sessionId)/control",
            jsonBody: body,
            failure: "the app didn't accept that remote action"
        )
        if let wrapped = try? JSONDecoder().decode(RemoteRuntimeControlEnvelope.self, from: data) {
            return wrapped.session
        }
        return try JSONDecoder().decode(RemoteRuntimeSession.self, from: data)
    }

    func closeRemoteRuntimeSession(_ sessionId: String) async throws {
        _ = try await request(
            "DELETE",
            path: "/remote-runtime/sessions/\(sessionId)",
            failure: "couldn't close the interactive runtime"
        )
    }

    @discardableResult
    func sendRemoteRuntimeCommand(
        sessionId: String,
        command: String,
        source: String,
        workDir: String?
    ) async throws -> RemoteRuntimeCommandEnvelope {
        var body: [String: Any] = ["command": command, "source": source]
        if let workDir, !workDir.isEmpty { body["workDir"] = workDir }
        let data = try await request(
            "POST",
            path: "/remote-runtime/sessions/\(sessionId)/command",
            jsonBody: body,
            failure: "the remote runtime command was rejected"
        )
        return try JSONDecoder().decode(RemoteRuntimeCommandEnvelope.self, from: data)
    }

    // ---- Web preview streaming (headless capture → frames) ----------------
    //
    // tvOS has no WebKit, so a web project can't be rendered in-process — it's
    // captured headless on the box at a chosen viewport and streamed as frames.
    // Flow: /dev/web-preview/start (boot a static server) → /vibing/preview/start
    // (headless Chrome captures it) → poll /vibing/preview/snapshot for the newest
    // frame hash → GET /vibing/preview/frames/{hash} for the bytes.

    struct WebPreviewStart: Decodable { let ok: Bool?; let port: Int?; let webUrl: String? }
    struct DevServerEvent: Decodable {
        let type: String?
        let framework: String?
        let logLine: String?
        let message: String?
        let timestamp: String?
        let bundleUrl: String?
        let deepLink: String?
    }
    struct DevStartResult: Decodable {
        let ok: Bool?
        let mode: String?
        let running: Bool?
        let serving: Bool?
        let building: Bool?
        let framework: String?
        let url: String?
        let directUrl: String?
        let bundleUrl: String?
        let port: Int?
        let webPort: Int?
        let error: String?
        let recentLogs: [String]?
        let servingLabel: String?
    }

    /// Start the selected project's web lane. `/dev/web-preview/start` only
    /// starts an Expo web sibling for the active dev server; this is the call
    /// that makes the selected project become active in the first place.
    func startDevServer(for project: ProjectSummary) async throws -> DevStartResult {
        var body: [String: Any] = [
            "caller": "web-ui",
            "platform": "web",
            "projectName": project.name,
        ]
        // Do not send `surface=web-reload` here. That surface is the web
        // dashboard's iframe lane, where Expo/RN deliberately resolves to a
        // static bundle. tvOS owns a live captured-pixel lane: start the real
        // Expo server here, then `/dev/web-preview/start` below supplies its
        // browser sibling for the headless capture. Tagging this as Web Reload
        // returned `mode=static-bundle`, left an older web project active, and
        // made an SFMG launch capture the wrong project (or time out cold).
        if let workDir = project.path, !workDir.isEmpty { body["workDir"] = workDir }
        if let framework = project.framework, !framework.isEmpty { body["framework"] = framework }
        let data = try await postJSON("/dev/start", body)
        return (try? JSONDecoder().decode(DevStartResult.self, from: data))
            ?? DevStartResult(ok: true, mode: nil, running: nil, serving: nil,
                              building: nil, framework: project.framework,
                              url: nil, directUrl: nil, bundleUrl: nil,
                              port: nil, webPort: nil,
                              error: nil, recentLogs: nil, servingLabel: nil)
    }

    /// The start endpoint is intentionally asynchronous. A 200 means the
    /// compiler was admitted, not that its port is already accepting browser
    /// traffic. Every preview surface must wait on this real readiness answer
    /// before asking headless Chrome to navigate.
    func devServerStatus() async throws -> DevStartResult {
        let data = try await request("GET", path: "/dev/status", failure: "couldn't read dev server status")
        return try JSONDecoder().decode(DevStartResult.self, from: data)
    }

    /// Start capturing a project's web preview at the given viewport. Returns
    /// when the vibe session is up (first frame may lag a beat).
    func startWebPreview(project: String, targetUrl: String, width: Int, height: Int, workDir: String? = nil) async throws {
        var body: [String: Any] = [
            "project": project, "targetUrl": targetUrl,
            "mode": "live", "width": width, "height": height,
        ]
        if let workDir, !workDir.isEmpty { body["workDir"] = workDir }
        _ = try await postJSON("/vibing/preview/start", body)
    }

    /// Boot the box's static web-preview server; returns its URL to capture.
    func startWebServer() async throws -> WebPreviewStart {
        let data = try await postJSON("/dev/web-preview/start", [:])
        return (try? JSONDecoder().decode(WebPreviewStart.self, from: data)) ?? WebPreviewStart(ok: true, port: nil, webUrl: nil)
    }

    struct SnapshotMeta: Decodable { let hash: String?; let seq: Int?; let size: Int? }

    /// The newest captured frame's hash (POST /vibing/preview/snapshot).
    func previewSnapshot(project: String) async throws -> SnapshotMeta {
        let data = try await postJSON("/vibing/preview/snapshot", ["project": project])
        return try JSONDecoder().decode(SnapshotMeta.self, from: data)
    }

    /// Fetch a captured frame's bytes by hash.
    ///
    /// `?project=` is REQUIRED and was missing. Frames are stored per project on
    /// the box, so without it the endpoint answers
    /// `{"error":"project query param required"}` — a JSON body, HTTP-shaped
    /// like success — instead of PNG bytes. UIImage(data:) then returns nil and
    /// WebPreviewStreamView shows nothing, forever, with no error to read.
    ///
    /// Measured against ubuntu-4gb, 2026-08-03. The e2e arc hit the identical
    /// bug on its side the same day; this is the app's half of it, which means
    /// the TV's web preview could never have rendered a single frame.
    func previewFrame(hash: String, project: String) async throws -> Data {
        let escaped = project.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? project
        return try await request("GET", path: "/vibing/preview/frames/\(hash)?project=\(escaped)", failure: "frame unavailable")
    }

    /// DOM-select the element at viewport (x, y) in the project's preview
    /// (POST /vibing/preview/select — the tvOS "kumanda" path).
    ///
    /// The TV draws a cursor over the captured frame and sends the cursor's
    /// viewport coordinate; the box dispatches a REAL click at that point in
    /// the headless Chrome that produced the frame, captures the element
    /// (html/css/rect/screenshot — the same payload the in-page DOM probe
    /// builds), and registers it in the shared domInspect store so the
    /// per-turn hook attaches it to the next prompt. "Deep audit this element"
    /// from the couch works because the runner receives the element, not a
    /// grep request.
    ///
    /// Coordinates are in the CAPTURED frame's viewport space. The TV scales
    /// its cursor position by frameSize/viewportSize before sending (the box
    /// captured at the profile's requested width/height; the Image view may
    /// letterbox). Returns the stored element summary so the UI can render the
    /// chip without re-reading anything.
    struct PreviewSelectResult: Decodable {
        let ok: Bool?
        let summary: String?
        let element: DomElementPayload?
        /// Surface-side metadata: the box's requested viewport and the REAL
        /// captured frame size, so the TV can verify its cursor mapping
        /// instead of assuming the frame filled the requested profile.
        let meta: PreviewSelectMeta?
    }

    /// Viewport vs actual-frame metadata for a selection (see
    /// PreviewSelectMeta in vibe_preview.go).
    struct PreviewSelectMeta: Decodable {
        let project: String?
        let viewportW: Int?
        let viewportH: Int?
        let frameW: Int?
        let frameH: Int?
    }

    /// The normalized stored element, as the agent returns it.
    struct DomElementPayload: Decodable {
        let selector: String?
        let tag: String?
        let id: String?
        let text: String?
        let rect: String?
        let workDir: String?
        let capturedAt: Int?
    }

    func selectPreviewElement(project: String, x: Int, y: Int, workDir: String? = nil) async throws -> PreviewSelectResult {
        var body: [String: Any] = ["project": project, "x": x, "y": y]
        if let workDir, !workDir.isEmpty { body["workDir"] = workDir }
        let data = try await postJSON("/vibing/preview/select", body)
        return (try? JSONDecoder().decode(PreviewSelectResult.self, from: data)) ?? PreviewSelectResult(ok: false, summary: nil, element: nil, meta: nil)
    }

    /// Enable or disable DOM mode in the CAPTURED page (POST
    /// /vibing/preview/dom-mode). tvOS has no WebKit, so the probe lives in the
    /// box's headless Chrome and this is the tvOS equivalent of the web/mobile
    /// Browse|Inspect radio: while ON the hover highlight tracks the cursor in
    /// the frame stream; turning OFF clears the stored element ("off means the
    /// agent holds nothing"). Returns the workDir the mode was scoped to.
    struct DomModeResult: Decodable {
        let ok: Bool?
        let enabled: Bool?
        let workDir: String?
    }

    func setPreviewDomMode(project: String, enabled: Bool, workDir: String? = nil) async throws -> DomModeResult {
        var body: [String: Any] = ["project": project, "enabled": enabled]
        if let workDir, !workDir.isEmpty { body["workDir"] = workDir }
        let data = try await postJSON("/vibing/preview/dom-mode", body)
        return (try? JSONDecoder().decode(DomModeResult.self, from: data)) ?? DomModeResult(ok: false, enabled: nil, workDir: nil)
    }

    /// Move the box's mouse to a viewport coordinate WITHOUT clicking and
    /// WITHOUT storing (POST /vibing/preview/cursor) — the live hover half of
    /// the cursor. Each Siri Remote swipe makes the next captured frame show
    /// the probe's highlight tracking the cursor. The tap is the select call.
    func movePreviewCursor(project: String, x: Int, y: Int) async throws {
        _ = try? await postJSON("/vibing/preview/cursor", ["project": project, "x": x, "y": y])
    }

    func stopWebPreview(project: String) async {
        _ = try? await postJSON("/vibing/preview/stop", ["project": project])
    }

    /// Subscribe to `/dev/events` and parse the same SSE stream the phone and
    /// web dashboard use for Metro/Expo/Flutter progress.
    ///
    /// This is intentionally LAN/relay HTTP, not Convex: startup logs can be
    /// chatty, and sending every bundler line through the multi-tenant backend
    /// would turn a local preview problem into a billable cloud log stream.
    /// The agent already retains a bounded replay window, so late subscribers
    /// still get the recent tail without another storage surface.
    ///
    /// `onGap` fires when a frame carries a structured capability gap, and
    /// `onEnd` fires EXACTLY ONCE when the stream stops for any reason.
    ///
    /// THE BUG onEnd EXISTS TO KILL: this function used to `return` silently
    /// when the SSE body ended. `/dev/events` is a bus that should never close,
    /// so a clean EOF is what a dropped relay tunnel looks like — and the log
    /// panel simply stopped growing, with the box still compiling happily. A
    /// stream that ends without saying so is the same defect as a silent
    /// `serve`. The caller classifies with FailureSignals.classifyStreamEnd and
    /// decides whether to reattach; this function only reports the truth.
    func subscribeDevEvents(
        onEvent: @escaping @Sendable (DevServerEvent) -> Void,
        onGap: (@Sendable (CapabilityGap) -> Void)? = nil,
        onEnd: (@Sendable (FailureSignals.StreamEndKind, String?) -> Void)? = nil,
        onError: (@Sendable (String) -> Void)? = nil
    ) -> Task<Void, Never> {
        let endpoints = requestEndpoints(path: "/dev/events")
        let token = self.token
        let relayPassword = box.relayPassword
        let urlSession = self.session
        return Task {
            var lastError = "dev event stream unavailable"
            var connected = false
            for endpoint in endpoints {
                if Task.isCancelled { onEnd?(.cancelled, nil); return }
                var req = URLRequest(url: endpoint.url)
                req.httpMethod = "GET"
                req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
                req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
                if endpoint.relay, let relayPassword, !relayPassword.isEmpty {
                    req.setValue(relayPassword, forHTTPHeaderField: "X-Relay-Password")
                }
                do {
                    let (bytes, resp) = try await urlSession.bytes(for: req)
                    guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                        lastError = Self.sseErrorText((resp as? HTTPURLResponse)?.statusCode ?? -1,
                                                      fallback: "dev event stream unavailable")
                        continue
                    }
                    connected = true
                    var dataLines: [String] = []
                    for try await line in bytes.lines {
                        if Task.isCancelled { onEnd?(.cancelled, nil); return }
                        if line.isEmpty {
                            emitDevEvent(dataLines, onEvent: onEvent, onGap: onGap)
                            dataLines.removeAll(keepingCapacity: true)
                            continue
                        }
                        if line.hasPrefix("data:") {
                            dataLines.append(String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces))
                        }
                    }
                    emitDevEvent(dataLines, onEvent: onEvent, onGap: onGap)
                    // The body ended and nobody asked it to. /dev/events has no
                    // terminal frame, so there is no such thing as a stream that
                    // finished on purpose — this is an interruption, and saying
                    // "done" here is exactly how the frozen panel shipped.
                    onEnd?(.interrupted, "the box closed the event stream")
                    return
                } catch {
                    if Task.isCancelled { onEnd?(.cancelled, nil); return }
                    lastError = error.localizedDescription
                    // A mid-stream throw AFTER a successful connect is a drop,
                    // not "this endpoint is dead, try the next one" — walking on
                    // to the relay would restart from zero and lose the tail.
                    if connected {
                        onEnd?(.interrupted, lastError)
                        return
                    }
                    continue
                }
            }
            onError?(lastError)
            onEnd?(.interrupted, lastError)
        }
    }

    private nonisolated func emitDevEvent(
        _ dataLines: [String],
        onEvent: @escaping @Sendable (DevServerEvent) -> Void,
        onGap: (@Sendable (CapabilityGap) -> Void)? = nil
    ) {
        guard !dataLines.isEmpty else { return }
        let payload = dataLines.joined(separator: "\n")
        guard let data = payload.data(using: .utf8) else { return }
        // The gap rides the SAME frame as the log line (`{type:"error",
        // gap:{…}}`), and DevServerEvent is a fixed Decodable that cannot see
        // it. Parse the raw object alongside rather than widening the struct.
        if let onGap,
           let obj = try? JSONSerialization.jsonObject(with: data),
           let gap = FailureSignals.capabilityGapFromDevEvent(obj) {
            onGap(gap)
        }
        guard let event = try? JSONDecoder().decode(DevServerEvent.self, from: data) else { return }
        onEvent(event)
    }

    // ---- Capability-gap fix: run the route the gap carries ----------------

    struct InstallStarted: Decodable { let ok: Bool?; let tool: String?; let stream: String? }

    /// POST /install/<tool>. The agent answers 202 with the log-stream name to
    /// watch; prefer ITS name over our copy so a server-side rename cannot
    /// leave the TV subscribed to nothing.
    func installTool(_ tool: String) async throws -> InstallStarted {
        let data = try await postJSON("/install/\(tool)", [:])
        return (try? JSONDecoder().decode(InstallStarted.self, from: data))
            ?? InstallStarted(ok: true, tool: tool, stream: "install:\(tool)")
    }

    /// Invoke a gap's route AS GIVEN — method, path and the body the agent
    /// pre-filled. The generic form of installTool: a `GapFix` is a route, and a
    /// UI must be able to press it without knowing what the failure was.
    ///
    /// Before this existed, the TV's fix button called `gapInstallTool` and gave
    /// up on anything that was not `/install/<tool>` ("This gap carries no
    /// install route."), so the first non-install remedy the agent produced —
    /// the preview takeover — would have rendered a button that refused itself.
    func invokeGapFix(_ fix: GapFix) async throws {
        _ = try await request(
            fix.method.isEmpty ? "POST" : fix.method,
            path: fix.path,
            jsonBody: fix.body.isEmpty ? [:] : fix.body,
            failure: fix.label
        )
    }

    /// Tail GET /streams/<name>. A 1.2 GB SDK behind a silent spinner is the
    /// same defect as a silent `serve` — the user cannot tell fetching from
    /// hung — so every line goes to the surface as it arrives.
    func subscribeInstallStream(
        _ name: String,
        onLine: @escaping @Sendable (String) -> Void,
        onDone: @escaping @Sendable (Bool, String?) -> Void
    ) -> Task<Void, Never> {
        let endpoints = requestEndpoints(path: "/streams/\(name)")
        let token = self.token
        let relayPassword = box.relayPassword
        let urlSession = self.session
        return Task {
            for endpoint in endpoints {
                if Task.isCancelled { return }
                var req = URLRequest(url: endpoint.url)
                req.httpMethod = "GET"
                req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
                req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
                if endpoint.relay, let relayPassword, !relayPassword.isEmpty {
                    req.setValue(relayPassword, forHTTPHeaderField: "X-Relay-Password")
                }
                do {
                    let (bytes, resp) = try await urlSession.bytes(for: req)
                    guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                        continue
                    }
                    for try await line in bytes.lines {
                        if Task.isCancelled { return }
                        guard line.hasPrefix("data:") else { continue }
                        let payload = String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)
                        guard let data = payload.data(using: .utf8),
                              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
                        else { continue }
                        // Same frame vocabulary the phone reads (see
                        // mobile/src/lib/quic.ts::subscribeStream): {type:"line",
                        // text} for output, {type:"result", status, error} for
                        // the verdict.
                        let kind = obj["type"] as? String ?? ""
                        if kind == "line", let text = obj["text"] as? String, !text.isEmpty {
                            onLine(text)
                        } else if kind == "result" {
                            let status = obj["status"] as? String ?? ""
                            onDone(status == "ok", obj["error"] as? String)
                            return
                        }
                    }
                    // The install stream DOES have a terminal frame, so an end
                    // without one means we never learned the verdict. Say that
                    // instead of implying success.
                    onDone(false, "the install stream ended before reporting a result")
                    return
                } catch {
                    if Task.isCancelled { return }
                    continue
                }
            }
            onDone(false, "could not reach the install log stream")
        }
    }

    /// Small POST helper for the JSON endpoints above.
    private func postJSON(_ path: String, _ body: [String: Any]) async throws -> Data {
        try await request("POST", path: path, jsonBody: body, failure: path)
    }

    /// A live redroid / Android screen frame (GET /droid/frame → PNG). Throws a
    /// readable message on 503 ("no android device attached") so the viewer can
    /// say so instead of showing nothing.
    func droidFrame(device: String? = nil) async throws -> Data {
        let path = device?.isEmpty == false
            ? "/droid/frame?device=\(device!.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? device!)"
            : "/droid/frame"
        return try await request("GET", path: path, failure: "no Android screen")
    }

    /// Feedback reports the box has collected (GET /feedback → a bare array).
    func listFeedback() async throws -> [FeedbackReport] {
        let data = try await request("GET", path: "/feedback", failure: "couldn't load feedback")
        return (try? JSONDecoder().decode([FeedbackReport].self, from: data)) ?? []
    }

    /// `confirm: true` is the user's second, deliberate tap after being told the
    /// runner already looks signed in — the only path allowed to reap a healthy
    /// session. Everything else is answered by the agent, not obeyed.
    func startRunnerAuth(_ runner: String, confirm: Bool = false) async throws -> RunnerAuthStartResult {
        try await ops(
            "runner_auth",
            [
                "op": "browser_start",
                "runner": runner,
                "trigger": confirm ? "confirmed" : "explicit",
                "confirm": confirm,
            ],
            as: RunnerAuthStartResult.self
        )
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
        URL(string: "\(agentHTTPBase(host: box.host, port: box.port))/capture/frame.jpg")
    }

    /// A capture frame, or a real error — never a JSON error body dressed as JPEG.
    ///
    /// This discarded the HTTP response and returned whatever bytes arrived. When
    /// capture isn't running the agent answers `503` with a 43-byte JSON body
    /// (`{"error":"capture not running"}`); those bytes went straight to
    /// `UIImage(data:)`, which returns nil — so the tile showed no frame and no
    /// reason, forever. Check the status and carry the message out.
    func frameData() async throws -> Data {
        try await request("GET", path: "/capture/frame.jpg", failure: "capture frame unavailable")
    }

    private func request(_ method: String, path: String, jsonBody: [String: Any]? = nil, failure: String) async throws -> Data {
        let body = try jsonBody.map { try JSONSerialization.data(withJSONObject: $0) }

        // Same relay-credential self-heal as rawOps: a stale per-user relay
        // password 401s the relay leg while the box is fine. Repair once per
        // streak and re-run with the corrected password; the endpoints are
        // recomputed each pass.
        var repairedOnce = false
        var finalError: Error = AgentError(message: failure)
        for _ in 0..<2 {
            let endpoints = requestEndpoints(path: path)
            guard !endpoints.isEmpty else { throw AgentError(message: "bad box host") }

            var lastError: Error = AgentError(message: failure)
            for endpoint in endpoints {
                var req = URLRequest(url: endpoint.url)
                req.httpMethod = method
                req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
                req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
                if let body {
                    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                    req.httpBody = body
                }
                if endpoint.relay, let pw = box.relayPassword, !pw.isEmpty {
                    req.setValue(pw, forHTTPHeaderField: "X-Relay-Password")
                }
                do {
                    let (data, resp) = try await session.data(for: req)
                    guard let http = resp as? HTTPURLResponse else {
                        throw AgentError(message: "no response")
                    }
                    if !(200..<300).contains(http.statusCode) {
                        // The agent carries a structured `capabilityGap` alongside
                        // `error` on a 412 refusal (and on a /tasks 500). Carry BOTH
                        // out: the string for every existing call site, the gap for
                        // the ones that can render a fix.
                        let gap = FailureSignals.capabilityGapFromData(data)
                        if let refusal = AgentError.fromHTTPBody(data, gap: gap) { throw refusal }
                        if let gap {
                            throw AgentError(message: gap.summary, gap: gap)
                        }
                        lastError = AgentError(message: "\(failure) (\(http.statusCode))")
                        continue
                    }
                    return data
                } catch let err as AgentError {
                    // A relay credential deny is self-healable: repair once and
                    // re-run the whole leg. Any other refusal is final.
                    if endpoint.relay, FailureSignals.isRelayCredentialDeny(err.message), !repairedOnce {
                        if let repaired = await relayRepair?() {
                            self.box = repaired
                            repairedOnce = true
                            lastError = err
                            break // recompute endpoints with the new password
                        }
                    }
                    var flagged = err
                    if endpoint.relay, FailureSignals.isRelayCredentialDeny(err.message) {
                        flagged.relayDeny = true
                    }
                    throw flagged
                } catch {
                    lastError = error
                    continue
                }
            }
            if !repairedOnce { throw lastError }
            finalError = lastError
        }
        throw finalError
    }

    private func requestEndpoints(path rawPath: String) -> [Endpoint] {
        box.requestEndpoints(path: rawPath).map { Endpoint(url: $0.url, relay: $0.relay) }
    }

    /// Name a refused SSE stream by the status the relay/agent returned.
    ///
    /// The relay's 401 ("invalid relay password" — stale per-user credential)
    /// and 429 ("too many invalid relay password attempts" — rate limit) are
    /// the two verdicts that told the couch NOTHING as a bare HTTP code; the
    /// TV kept showing "invalid relay password" reports with no route. Every
    /// other status stays a plain code + fallback.
    private nonisolated static func sseErrorText(_ status: Int, fallback: String) -> String {
        switch status {
        case 401:
            return "the relay refused this account's credentials — the stored relay password drifted. Reconnect or sign in again to repair it."
        case 429:
            return "the relay rate-limited this account — wait a moment and retry."
        default:
            return "\(fallback) (HTTP \(status))"
        }
    }
}
