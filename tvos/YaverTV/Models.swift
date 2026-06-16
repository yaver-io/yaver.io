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
}
