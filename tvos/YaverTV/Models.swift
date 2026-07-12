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

struct RunnerSessions: Decodable {
    var count: Int?
    var sessions: [RunnerSession]?
}

struct RunnerSession: Decodable, Identifiable {
    var id: String
    var agent: String?
    var title: String?
    var status: String?
    var workDir: String?
    var updatedAt: String?
    var createdAt: String?

    enum CodingKeys: String, CodingKey {
        case id, agent, title, status
        case workDir = "workDir"
        case updatedAt = "updatedAt"
        case createdAt = "createdAt"
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

    /// True when this box can be resumed from the TV (managed + has a machineId).
    var wakeable: Bool { (managed ?? false) && (machineId?.isEmpty == false) }
}
