// Models.swift — Codable mirrors of the agent's appletv_/capture_ JSON shapes.
// Field names match ops_appletv.go / capture.go and mobile/src/lib/appletvClient.ts.

import Foundation

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

/// A task as it appears in the glanceable list (GET /tasks). Only the fields a
/// lean-back list needs; the full model (turns, cost, output) lives on mobile.
struct TaskSummary: Decodable, Identifiable {
    let id: String
    let title: String?
    let status: String?          // queued | running | review | completed | failed | stopped
    let runner: String?
    let tmuxSession: String?     // present → the task has a live session to drive

    /// The title is a raw prompt — it carries absolute paths. Redact for a TV.
    var safeTitle: String { redactHomePaths(title ?? "Untitled task") }
}

struct TaskList: Decodable { let tasks: [TaskSummary] }

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

    var id: String { name }

    /// How this project should be previewed on the TV.
    enum Kind { case android, web, flutter, unknown }
    var kind: Kind {
        switch (framework ?? "").lowercased() {
        case "expo", "react-native", "reactnative", "rn": return .android
        case "nextjs", "next", "vite", "react", "web", "remix", "astro", "svelte": return .web
        case "flutter": return .flutter
        default: return .unknown
        }
    }

    var frameworkLabel: String { framework?.isEmpty == false ? framework! : "unknown" }
}

struct ProjectList: Decodable { let projects: [ProjectSummary] }

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

    var id: String { name }

    /// "yaver-codex · codex" — what a lean-back surface should show.
    var label: String {
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

    enum CodingKeys: String, CodingKey {
        case id, runner, method, status, code, detail, error
        case openURL = "openUrl"
        case authConfigured
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

    /// True when this box can be resumed from the TV (managed + has a machineId).
    var wakeable: Bool { (managed ?? false) && (machineId?.isEmpty == false) }

    /// Ordered ops endpoints to try: LAN first, relay second.
    ///
    /// Direct-first / relay-fallback is Yaver's documented connection strategy
    /// (CLAUDE.md "Connection strategy"), and it matters for cost as well as
    /// latency — a LAN hop costs nothing, while every relay byte is metered
    /// against the device's daily allowance.
    var opsEndpoints: [URL] {
        var out: [URL] = []
        if !host.isEmpty, let lan = URL(string: "http://\(host):\(port)/ops") {
            out.append(lan)
        }
        if let base = relayBaseUrl?.trimmingCharacters(in: .whitespacesAndNewlines),
           !base.isEmpty, !id.isEmpty {
            let trimmed = base.hasSuffix("/") ? String(base.dropLast()) : base
            var path = "\(trimmed)/d/\(id)/ops"
            // The relay accepts its password as `__rp` when a header cannot be
            // set; we DO set the header too, this is the documented fallback
            // (relay/server.go handleProxy).
            if let pw = relayPassword, !pw.isEmpty,
               let esc = pw.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) {
                path += "?__rp=\(esc)"
            }
            if let relay = URL(string: path) { out.append(relay) }
        }
        return out
    }
}
