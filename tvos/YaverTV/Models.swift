// Models.swift — Codable mirrors of the agent's appletv_/capture_ JSON shapes.
// Field names match ops_appletv.go / capture.go and mobile/src/lib/appletvClient.ts.

import Foundation

func urlAuthorityHost(_ host: String) -> String {
    let value = host.trimmingCharacters(in: .whitespacesAndNewlines)
    if value.hasPrefix("[") && value.hasSuffix("]") { return value }
    return value.contains(":") ? "[\(value)]" : value
}

func agentHTTPBase(host: String, port: Int) -> String {
    "http://\(urlAuthorityHost(host)):\(port)"
}

struct NowPlaying: Decodable {
    var title: String?
    var artist: String?
    var album: String?
    var app: String?
    var state: String?
    var position: Double?
    var total: Double?
    var artworkB64: String?
    var mimetype: String?
    var error: String?

    enum CodingKeys: String, CodingKey {
        case title, artist, album, app, state, position, total, mimetype, error
        case artworkB64 = "artwork_b64"
    }
}

struct CaptureStatus: Decodable {
    var running: Bool
    var device: String?
    var fps: Double?
    var width: Int?
    var height: Int?
    var hasFrame: Bool?
    var blackHint: String?   // advisory only — Yaver still streams the (black) frames
    var warning: String?
    var error: String?
    var ffmpeg: Bool?
}

struct AgentInfo: Decodable {
    var hostname: String?
    var platform: String?
    var arch: String?
    var agentVersion: String?
    var deviceId: String?
    var cpuPercent: Double?
    var localIPs: [String]?
}

struct AgentStatus: Decodable {
    var agentVersion: String?
    var authExpired: Bool?
    var tasks: TaskCounts?
    var devServer: DevServerStatus?
}

struct TaskCounts: Decodable {
    var total: Int?
    var running: Int?
}

struct DevServerStatus: Decodable {
    var running: Bool?
    var framework: String?
    var url: String?
    var port: Int?
    var project: String?
    var workDir: String?
}

struct VoiceRuntimeStatus: Decodable {
    var enabled: Bool?
    var sttProvider: String?
    var ttsProvider: String?
    var sttReady: Bool?
    var ttsReady: Bool?
    var defaultProject: String?

    enum CodingKeys: String, CodingKey {
        case enabled
        case sttProvider = "stt_provider"
        case ttsProvider = "tts_provider"
        case sttReady = "stt_ready"
        case ttsReady = "tts_ready"
        case defaultProject = "default_project"
    }
}

/// The live coding sessions on a box — the tmux PTYs a runner wrap owns.
///
/// This mirrors the `runner_sessions` verb (`ops_runner_turn.go:148`), which is
/// the SAME set `/runner/session/turn` drives. The previous shape mirrored
/// `runner`/`agents_list` — a different concept (agent-graph tasks) that returns
/// `{"count":0,"sessions":[]}` on a box with a live runner. So every dashboard
/// reported "no active runner sessions" while the Session screen was busy
/// driving one. Wrong verb AND wrong shape: `agents_list` sends `id`/`agent`,
/// `runner_sessions` sends `name`/`runner`/`attached`, so decoding failed too.
///
/// Deliberately no `workDir`: it is an absolute path (`/Users/<name>/…`), and
/// these screens get pointed at by cameras and screen-shared into demo videos.
struct RunnerSessions: Decodable {
    var count: Int?
    var sessions: [RunnerSession]?
}

/// One privacy-narrow frame from GET /tmux/stream.
///
/// The agent's wire object also contains `currentPath`, PID and tmux topology.
/// A TV/headset needs none of those to paint the live session, and absolute
/// paths identify the local account. Deliberately omit them from the native
/// model so adding the stream cannot accidentally put them on a shared screen.
struct TmuxPaneFrame: Decodable {
    let paneId: String
    let sessionName: String
    let agent: String?
    let model: String?
    let status: String
    let statusReason: String?
    let options: [String]?
    let title: String?
    let preview: String?
}

/// One user/assistant turn from the same task transcript mobile and web render.
struct TaskConversationTurn: Decodable, Identifiable, Equatable {
    let role: String
    let content: String
    let timestamp: String?

    var id: String { "\(timestamp ?? ""):\(role):\(content)" }
}

struct TaskPendingFollowUp: Decodable, Identifiable, Equatable {
    let input: String
    var id: String { input }
}

struct TaskPresentationMessage: Decodable, Identifiable, Equatable {
    let id: String
    let kind: String
    let role: String?
    let text: String
    let phase: String?
    let state: String?
    let runner: String?
    let project: String?
    let machine: String?
    let platform: String?
    let surface: String?
    let createdAt: String?
    let updatedAt: String?
}

struct TaskPresentationWireEvent: Decodable {
    let type: String
    let schema: Int?
    let op: String?
    let seq: Int64?
    let message: TaskPresentationMessage?
    let messages: [TaskPresentationMessage]?
}

/// A runner question carried on the task SSE stream. This is part of the task
/// conversation, not a second chat system: answering it unblocks the same
/// `/tasks/{id}` turn and the runner keeps coding in place.
struct TaskAgentQuestion: Decodable, Identifiable, Equatable {
    let id: String
    let taskId: String
    let prompt: String
    let header: String?
    let kind: String                 // text | choice | secret
    let choices: [String]?
    let multi: Bool?
    let vaultHint: String?
    let createdAtMs: Int64?
    let timeoutSec: Int?
    let screenshot: String?
    let step: String?

    var isSecret: Bool { kind.lowercased() == "secret" }
    var allowsMultipleChoices: Bool { multi == true }
}

/// A task as it appears in the list and chat detail.
///
/// tvOS used to decode only a title/status and present the raw runner console as
/// "Chat". That is not the mobile mechanic: mobile renders the conversation,
/// shows the user's message immediately, and lets a finished task continue in
/// its exact runner conversation and task-owned tmux seat.
///
/// POST /tasks answers `taskId`, while GET /tasks answers `id`. Decoding both is
/// load-bearing: reporting a decode error after POST has already started work
/// invites a retry and creates a duplicate task.
struct TaskExecutionIdentity: Decodable {
    let yaverSessionId: String
    let taskId: String
    let remoteBoxId: String?
    let runnerName: String?
    let runnerId: String?
    let runnerSessionId: String?
    let startedFrom: String?
    let initialSurface: String?
    let sessionStartedAt: String?
    let lastSurface: String?
    let lastActiveAt: String?
}

struct TaskSummary: Decodable, Identifiable {
    let id: String
    let title: String?
    let status: String?          // queued | running | review | completed | failed | stopped
    let runner: String?          // `runnerId` on current agents; `runner` on older ones
    let model: String?
    let reasoningEffort: String?
    let workDir: String?
    let projectName: String?
    let sessionId: String?
    let output: String?
    let resultText: String?
    let presentation: [TaskPresentationMessage]?
    let turns: [TaskConversationTurn]?
    let pendingFollowUps: [TaskPendingFollowUp]?
    let tmuxSession: String?     // present → the task has a live session to drive
    let executionSession: TaskExecutionIdentity?

    enum CodingKeys: String, CodingKey {
        case id, taskId, title, status, runner, runnerId, model, reasoningEffort, workDir, projectName, sessionId
        case output, resultText, presentation, turns, pendingFollowUps, tmuxSession, executionSession
    }

    init(
        id: String,
        title: String? = nil,
        status: String? = nil,
        runner: String? = nil,
        model: String? = nil,
        reasoningEffort: String? = nil,
        workDir: String? = nil,
        projectName: String? = nil,
        sessionId: String? = nil,
        output: String? = nil,
        resultText: String? = nil,
        presentation: [TaskPresentationMessage]? = nil,
        turns: [TaskConversationTurn]? = nil,
        pendingFollowUps: [TaskPendingFollowUp]? = nil,
        tmuxSession: String? = nil,
        executionSession: TaskExecutionIdentity? = nil
    ) {
        self.id = id
        self.title = title
        self.status = status
        self.runner = runner
        self.model = model
        self.reasoningEffort = reasoningEffort
        self.workDir = workDir
        self.projectName = projectName
        self.sessionId = sessionId
        self.output = output
        self.resultText = resultText
        self.presentation = presentation
        self.turns = turns
        self.pendingFollowUps = pendingFollowUps
        self.tmuxSession = tmuxSession
        self.executionSession = executionSession
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id)
            ?? c.decode(String.self, forKey: .taskId)
        title = try c.decodeIfPresent(String.self, forKey: .title)
        status = try c.decodeIfPresent(String.self, forKey: .status)
        runner = try c.decodeIfPresent(String.self, forKey: .runnerId)
            ?? c.decodeIfPresent(String.self, forKey: .runner)
        model = try c.decodeIfPresent(String.self, forKey: .model)
        reasoningEffort = try c.decodeIfPresent(String.self, forKey: .reasoningEffort)
        workDir = try c.decodeIfPresent(String.self, forKey: .workDir)
        projectName = try c.decodeIfPresent(String.self, forKey: .projectName)
        sessionId = try c.decodeIfPresent(String.self, forKey: .sessionId)
        output = try c.decodeIfPresent(String.self, forKey: .output)
        resultText = try c.decodeIfPresent(String.self, forKey: .resultText)
        presentation = try c.decodeIfPresent([TaskPresentationMessage].self, forKey: .presentation)
        turns = try c.decodeIfPresent([TaskConversationTurn].self, forKey: .turns)
        pendingFollowUps = try c.decodeIfPresent([TaskPendingFollowUp].self, forKey: .pendingFollowUps)
        tmuxSession = try c.decodeIfPresent(String.self, forKey: .tmuxSession)
        executionSession = try c.decodeIfPresent(TaskExecutionIdentity.self, forKey: .executionSession)
    }

    /// The title is a raw prompt — it carries absolute paths. Redact for a TV.
    var safeTitle: String { redactHomePaths(title ?? "Untitled task") }
}

struct TaskRunnerReasoningEffort: Decodable, Identifiable, Equatable {
    let reasoningEffort: String
    let description: String?
    var id: String { reasoningEffort }
}

struct TaskRunnerControlModel: Decodable, Identifiable {
    let id: String
    let name: String?
    let description: String?
    let provider: String?
    let isDefault: Bool?
    let defaultReasoningEffort: String?
    let supportedReasoningEfforts: [TaskRunnerReasoningEffort]?
}

struct TaskRunnerControlCatalog: Decodable {
    let ok: Bool
    let taskId: String
    let runnerId: String
    let model: String?
    let reasoningEffort: String?
    let modelSource: String?
    let models: [TaskRunnerControlModel]
    let isAdopted: Bool?
}

struct TaskRunnerControlResult: Decodable {
    let ok: Bool
    let taskId: String?
    let control: String?
    let model: String?
    let reasoningEffort: String?
    let display: String?
    let status: String?
    let verified: Bool?
    let alreadyExited: Bool?
    let error: String?
    let code: String?
}

struct TaskList: Decodable { let tasks: [TaskSummary] }

/// POST /tasks answers either the bare task or `{task:{…}}` depending on route
/// age. Accepting both is not defensiveness for its own sake: the task has
/// ALREADY been created by the time we decode, so failing on envelope shape
/// would report an error for work that is running — the worst possible lie,
/// because the user retries and gets two.
struct TaskEnvelope: Decodable { let task: TaskSummary }

struct ProjectStartEnvelope: Decodable {
    let directory: String
    let gitProvider: String
    let palette: String
    let task: TaskSummary
}

struct TaskForkResult: Decodable {
    let taskId: String
    let runnerId: String
    let status: String?
    let parentTaskId: String?
    let contextWordsUsed: Int?
}

/// A project the box knows about (GET /projects). The TV lists these to pick one
/// to preview. `framework` decides how it renders on the TV: an RN/Android app
/// runs in redroid and streams via /droid/frame; a web app (next/vite) is
/// captured headless and streamed as frames (tvOS has no WebKit, so it's always
/// pixels, never a real webview).
struct ProjectSummary: Decodable, Identifiable {
    let name: String
    let path: String?
    let framework: String?
    let branch: String?
    /// Origin remote URL — used to match the Convex last-project row
    /// (defaultRuntimeProjectByDevice carries {projectName, gitRemote, branch},
    /// never an absolute path) against the live /projects list.
    let gitRemote: String?
    /// Repository-wide capability inventory from /projects. Monorepos use
    /// /workspace/apps for the actionable child list; single apps use these
    /// fields directly in Vibing's project-first picker.
    let frameworks: [String]?
    let surfaces: [String]?
    let testSurfaces: [String]?
    let isMonorepo: Bool?
    let subframeworks: [String]?

    var id: String { name }

    /// How this project should be previewed on the TV.
    enum Kind { case android, web, tvOS, unknown }
    var kind: Kind {
        let declaredSurfaces = Set((surfaces ?? []) + (testSurfaces ?? []))
        if declaredSurfaces.contains("tv") || declaredSurfaces.contains("tvos-simulator") {
            return .tvOS
        }
        switch (framework ?? "").lowercased() {
        // RN/Expo takes the BROWSER lane, matching the agent's own default
        // (defaultStreamingSurface: RN → browser): RN-Web in headless Chromium,
        // sub-second HMR, no emulator — WebPreviewStreamView already boots the
        // Expo web sibling. Routing RN to redroid meant the preview needed an
        // Android container that usually wasn't running; redroid stays the
        // lane for native Android projects.
        case "expo", "react-native", "reactnative", "rn": return .web
        case "kotlin", "android": return .android
        case "nextjs", "next", "vite", "react", "web", "remix", "astro", "svelte": return .web
        // The agent runs Flutter's web target on Linux and captures it in the
        // same headless browser lane as Next/Vite. Calling Flutter
        // "unstreamable" on TV contradicted /workspace/apps and hid a working
        // option from users with a Flutter-only repository.
        case "flutter": return .web
        default: return .unknown
        }
    }

    var frameworkLabel: String { framework?.isEmpty == false ? framework! : "unknown" }
}

struct ProjectList: Decodable { let projects: [ProjectSummary] }

/// One app declared by a monorepo's yaver.workspace.yaml.
///
/// A repository such as Talos can contain mobile + web + backend apps. Vibing
/// asks /workspace/apps after the repository is selected so it never displays
/// unrelated projects on the second screen and never guesses from tags.
struct WorkspaceAppSummary: Decodable, Identifiable {
    let name: String
    let path: String?
    let absPath: String?
    let stack: String?
    let surfaces: [String]?
    let testSurfaces: [String]?
    let kind: String?
    let framework: String?
    let envMissing: [String]?
    let exists: Bool

    var id: String { absPath ?? name }
    var isPreviewable: Bool {
        guard exists else { return false }
        return ["web", "hybrid", "mobile"].contains((kind ?? "").lowercased())
            || ["expo", "react-native", "flutter", "kotlin", "android", "nextjs", "vite", "react", "web"]
                .contains((framework ?? "").lowercased())
    }

    func asProject(in repository: ProjectSummary) -> ProjectSummary {
        ProjectSummary(
            name: name,
            path: absPath,
            framework: framework,
            branch: repository.branch,
            gitRemote: repository.gitRemote,
            frameworks: framework.map { [$0] },
            surfaces: surfaces,
            testSurfaces: testSurfaces,
            isMonorepo: false,
            subframeworks: nil
        )
    }
}

struct WorkspaceAppList: Decodable {
    let ok: Bool?
    let root: String?
    let apps: [WorkspaceAppSummary]
}

/// Agent-owned project/box capability answer. Every surface renders this list
/// instead of reimplementing framework conditionals.
struct ProjectPreviewOption: Decodable, Identifiable {
    let id: String
    let label: String
    let supported: Bool
    let primary: Bool?
    let reason: String?
    let framework: String?
}

struct ProjectPreviewCapabilities: Decodable {
    let workDir: String?
    let framework: String
    let selfDevelopment: Bool
    let hasPairedDevice: Bool
    let options: [ProjectPreviewOption]
    let reason: String?
}

struct RemoteRuntimeCommandEnvelope: Decodable {
    let ok: Bool
    let status: String?
    let note: String?
}

/// An external MCP server the box exposes — name is the identity the task body
/// carries (`mcpServers: [name]`), same contract as mobile/web.
struct McpServerSummary: Decodable, Identifiable {
    let name: String
    let url: String?
    let toolCount: Int?
    var id: String { name }
}

/// Live coding choices from GET /agent/runners. The TV must use this measured
/// list rather than copying a runner/model catalogue: provider configuration
/// differs per box, and a copied model id is exactly how a task gets sent to a
/// CLI that cannot run it.
struct AgentRunnerModel: Decodable, Identifiable, Equatable {
    let id: String
    let name: String
    let provider: String?
    let isDefault: Bool?
    let defaultReasoningEffort: String?
    let supportedReasoningEfforts: [TaskRunnerReasoningEffort]?
}

struct AgentRunnerSummary: Decodable, Identifiable, Equatable {
    let id: String
    let name: String
    let installed: Bool
    let ready: Bool
    let isDefault: Bool
    let warning: String?
    let error: String?
    let models: [AgentRunnerModel]

    private enum CodingKeys: String, CodingKey {
        case id, name, installed, ready, isDefault, warning, error, models
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        installed = try c.decodeIfPresent(Bool.self, forKey: .installed) ?? false
        ready = try c.decodeIfPresent(Bool.self, forKey: .ready) ?? false
        isDefault = try c.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
        warning = try c.decodeIfPresent(String.self, forKey: .warning)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        // Go encodes a nil slice as JSON null. One unavailable runner must not
        // make the TV discard every healthy runner and all of their models.
        models = try c.decodeIfPresent([AgentRunnerModel].self, forKey: .models) ?? []
    }

    var canonicalId: String { RegisteredRunner.canonical(id) }

    var displayName: String {
        switch canonicalId {
        case "claude": return "Claude Code"
        case "codex": return "Codex"
        case "opencode": return "OpenCode"
        default: return name.isEmpty ? id : name
        }
    }
}

struct AgentRunnerList: Decodable {
    let runners: [AgentRunnerSummary]
    let `default`: String?
}

/// A feedback report the box has collected (GET /feedback). The TV shows them
/// to review from the couch — the SDK captures video/voice/screenshots on the
/// device under test; here we list source, transcript, version, and how many
/// shots/errors came with it.
struct FeedbackReport: Decodable, Identifiable {
    let id: String
    let source: String?
    let transcript: String?
    let screenshots: [String]?
    let videoPath: String?
    let appVersion: String?
    let buildId: String?
    let createdAt: String?
    let errors: [FeedbackError]?

    var shotCount: Int { screenshots?.count ?? 0 }
    var errorCount: Int { errors?.count ?? 0 }
    var hasVideo: Bool { videoPath?.isEmpty == false }
    var safeTranscript: String { redactHomePaths(transcript ?? "") }
}

struct FeedbackError: Decodable { let message: String? }

/// Strip absolute home paths (/Users/<name>, /home/<name> → ~) from any string
/// shown on a television or spoken aloud. Shared by the Session pane and the
/// task list; the path carries the user's login name and filesystem layout, and
/// these screens get filmed and screen-shared. Mirrors the Convex privacy rule
/// that keeps absolute paths off the wire.
func redactHomePaths(_ text: String) -> String {
    var out = text
    for root in ["/Users/", "/home/"] {
        while let r = out.range(of: root) {
            let rest = out[r.upperBound...]
            let name = rest.prefix { !$0.isWhitespace && $0 != "/" }
            guard !name.isEmpty else { break }
            out.replaceSubrange(r.lowerBound..<name.endIndex, with: "~")
        }
    }
    return out
}

struct RunnerSession: Decodable, Identifiable {
    /// The tmux session name ("yaver-codex", or "0" for a hand-rolled one).
    /// This is exactly what `/runner/session/turn` wants in its `session` field.
    var name: String
    var runner: String?
    var attached: Bool?
    var taskId: String?
    var model: String?
    var taskTitle: String?

    var id: String { name }

    /// The model is the useful identity once known; the runner name is only a
    /// fallback because the user already knows which coding-agent family owns
    /// the conversation.
    var label: String {
        if let taskTitle, !taskTitle.isEmpty { return redactHomePaths(taskTitle) }
        if let model, !model.isEmpty { return "\(name) · \(model)" }
        guard let runner, !runner.isEmpty, runner != name else { return name }
        return "\(name) · \(runner)"
    }
}

struct ReloadResult: Decodable {
    var mode: String?
    var framework: String?
    var reloaded: Bool?
    var workDir: String?
    var deliveredTo: Int?
    var changeClass: String?
    var nativeChangesDetected: Bool?
}

struct PlatformMatrixEnvelope: Decodable {
    var ok: Bool?
    var matrix: PlatformMatrixReport?
}

struct PlatformMatrixReport: Decodable {
    var devicePlatform: String?
    var deviceArch: String?
    var surfaces: [PlatformSurface]?

    enum CodingKeys: String, CodingKey {
        case devicePlatform = "device_platform"
        case deviceArch = "device_arch"
        case surfaces
    }
}

struct PlatformSurface: Decodable, Identifiable {
    var id: String
    var label: String?
    var family: String?
    var surface: String?
    var status: String?
    var buildSupported: Bool?
    var submitSupported: Bool?
    var deployTarget: String?
    var scriptPresent: Bool?
    var notes: [String]?
    var limitations: [String]?
    var nextSteps: [String]?

    enum CodingKeys: String, CodingKey {
        case id, label, family, surface, status, notes, limitations
        case buildSupported = "build_supported"
        case submitSupported = "submit_supported"
        case deployTarget = "deploy_target"
        case scriptPresent = "script_present"
        case nextSteps = "next_steps"
    }
}

struct RunnerAuthStartResult: Decodable {
    var ok: Bool?
    var session: RunnerAuthSession?
    /// What the agent DID: "start" (a session was spawned), "reuse" (one was
    /// already in flight — another surface asked first), or "noop" (declined,
    /// because the runner is already signed in).
    ///
    /// Spawning a sign-in is destructive: the agent reaps any live session for
    /// that runner, burns a PKCE flow, and for claude can REPLACE a working
    /// credential. On 2026-07-27 the user was shown sign-in dialogs repeatedly
    /// for runners that were fine, so the agent now decides and tvOS must
    /// render the answer rather than treating a no-op as a broken response.
    var action: String?
    /// The sentence to show. Always present on "noop"/"reuse".
    var reason: String?
    /// True when the user may override by confirming — switching accounts is
    /// the one legitimate reason to replace a working credential.
    var reauthable: Bool?
}

struct RunnerAuthSession: Decodable, Identifiable {
    var id: String
    var runner: String?
    var method: String?
    var status: String?
    var openURL: String?
    var code: String?
    var detail: String?
    var authConfigured: Bool?
    var error: String?
    /// Epoch millis. The agent has stamped both the whole time; tvOS decoded
    /// neither, so a pending session rendered as an undifferentiated "pending"
    /// row with no way to tell "working" from "wedged". Every wait the product
    /// imposes must narrate itself.
    var startedAt: Double?
    var lastOutputAt: Double?

    enum CodingKeys: String, CodingKey {
        case id, runner, method, status, code, detail, error
        case openURL = "openUrl"
        case authConfigured
        case startedAt, lastOutputAt
    }
}

struct GitAuthSession: Decodable, Identifiable {
    var sessionId: String
    var id: String { sessionId }
    var ok: Bool?
    var provider: String?
    var host: String?
    var state: String?
    var username: String?
    var userCode: String?
    var verificationURI: String?
    var interval: Int?
    var expiresAt: Int?
    var error: String?

    enum CodingKeys: String, CodingKey {
        case ok, provider, host, state, username, interval, error
        case sessionId
        case snakeSessionId = "session_id"
        case userCode
        case snakeUserCode = "user_code"
        case verificationURI
        case snakeVerificationURI = "verification_uri"
        case expiresAt
        case snakeExpiresAt = "expires_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionId = try c.decodeIfPresent(String.self, forKey: .sessionId)
            ?? c.decodeIfPresent(String.self, forKey: .snakeSessionId)
            ?? ""
        ok = try c.decodeIfPresent(Bool.self, forKey: .ok)
        provider = try c.decodeIfPresent(String.self, forKey: .provider)
        host = try c.decodeIfPresent(String.self, forKey: .host)
        state = try c.decodeIfPresent(String.self, forKey: .state)
        username = try c.decodeIfPresent(String.self, forKey: .username)
        userCode = try c.decodeIfPresent(String.self, forKey: .userCode)
            ?? c.decodeIfPresent(String.self, forKey: .snakeUserCode)
        verificationURI = try c.decodeIfPresent(String.self, forKey: .verificationURI)
            ?? c.decodeIfPresent(String.self, forKey: .snakeVerificationURI)
        interval = try c.decodeIfPresent(Int.self, forKey: .interval)
        expiresAt = try c.decodeIfPresent(Int.self, forKey: .expiresAt)
            ?? c.decodeIfPresent(Int.self, forKey: .snakeExpiresAt)
        error = try c.decodeIfPresent(String.self, forKey: .error)
    }
}

struct PairedATV: Decodable, Identifiable {
    let identifier: String
    let name: String
    let address: String
    var `default`: Bool?
    var protocols: [String]?
    var id: String { identifier }
}

/// Remote keys accepted by appletv_remote_key (ops_appletv.go).
enum RemoteKey: String, CaseIterable {
    case up, down, left, right, select, menu, home
    case play, pause, stop, next, previous, playPause = "play_pause"
    case volumeUp = "volume_up", volumeDown = "volume_down"
}

/// A box (device) the TV can drive. For the LAN MVP the user supplies the host;
/// later this is populated from the Convex device registry.
struct BoxTarget: Codable, Identifiable, Equatable {
    var id: String          // deviceId (or a stable local id)
    var name: String
    /// Account-local alias. Optional/additive so old persisted boxes decode.
    var alias: String? = nil
    var host: String        // LAN IP / hostname running `yaver serve`
    var port: Int = Backend.agentPort
    /// Set for a managed cloud box that can be woken from the control plane.
    /// Optional because the manual Add-Box flow only knows host/port; a future
    /// Convex device-registry sync would populate these automatically. When a
    /// machineId is present the box can be resumed from the TV; otherwise wake
    /// is unavailable (start it from a computer/phone). Both decode to nil for
    /// boxes persisted before these fields existed.
    var managed: Bool? = nil
    var machineId: String? = nil

    /// Relay reachability. Until these existed the TV was LAN-ONLY: AgentClient
    /// hardcoded `http://<host>:<port>/ops`, so a box on another network — or
    /// this Apple TV on a different subnet from the box — was simply
    /// unreachable, even though every other surface could get there over the
    /// relay. Both decode to nil for boxes persisted before these fields
    /// existed, and a nil relay just means "LAN only", exactly as before.
    ///
    /// `relayBaseUrl` is the relay's HTTPS origin (e.g. "https://relay.yaver.io");
    /// the proxy path `/d/<deviceId>/ops` is built from [id].
    var relayBaseUrl: String? = nil
    var relayPassword: String? = nil

    var aliasLabel: String? {
        guard let alias, !alias.isEmpty, alias != name else { return nil }
        return alias.hasPrefix("@") ? alias : "@\(alias)"
    }

    /// True when this box can be resumed from the TV (managed + has a machineId).
    var wakeable: Bool { (managed ?? false) && (machineId?.isEmpty == false) }

    /// Ordered ops endpoints to try: direct first, relay second.
    ///
    /// Direct-first / relay-fallback is Yaver's documented connection strategy
    /// (CLAUDE.md "Connection strategy"), and it matters for cost as well as
    /// latency — a LAN hop costs nothing, while every relay byte is metered
    /// against the device's daily allowance.
    var opsEndpoints: [(url: URL, relay: Bool)] {
        var out: [(url: URL, relay: Bool)] = []
        if !host.isEmpty, let lan = URL(string: "\(agentHTTPBase(host: host, port: port))/ops") {
            out.append((lan, false))
        }
        if let base = relayBaseUrl?.trimmingCharacters(in: .whitespacesAndNewlines),
           !base.isEmpty, !id.isEmpty {
            let trimmed = base.hasSuffix("/") ? String(base.dropLast()) : base
            // AgentClient can set X-Relay-Password on tvOS, so credentials do
            // not belong in a URL. Query strings leak into proxy/access logs,
            // screenshots and crash reports; the header is attached only to
            // the relay leg below.
            let path = "\(trimmed)/d/\(id)/ops"
            if let relay = URL(string: path) { out.append((relay, true)) }
        }
        return out
    }

    func requestEndpoints(path rawPath: String) -> [(url: URL, relay: Bool)] {
        let path = rawPath.hasPrefix("/") ? rawPath : "/\(rawPath)"
        var out: [(url: URL, relay: Bool)] = []
        if !host.isEmpty, let lan = URL(string: "\(agentHTTPBase(host: host, port: port))\(path)") {
            out.append((lan, false))
        }
        if let base = relayBaseUrl?.trimmingCharacters(in: .whitespacesAndNewlines),
           !base.isEmpty, !id.isEmpty {
            let trimmed = base.hasSuffix("/") ? String(base.dropLast()) : base
            let relayPath = "\(trimmed)/d/\(id)\(path)"
            if let relay = URL(string: relayPath) { out.append((relay, true)) }
        }
        return out
    }
}
