// Backend.swift — Convex origin + RFC 8628 device-code sign-in, used ONLY in
// standalone mode (the watch reaching the agent directly, without the phone).
//
// In the DEFAULT phone-paired mode the watch holds NO token and never talks to
// Convex — the phone is already signed in and is the brain-of-record. This file
// exists for the secondary "use without your phone" opt-in.
//
// Mirrors tvos/YaverTV/Backend.swift exactly (same Convex HTTP contract that
// `yaver auth` and the tvOS app use):
//   POST /auth/device-code                      -> { userCode, deviceCode, expiresAt }
//   GET  /auth/device-code/poll?device_code=... -> { status, token? }
// A phone already signed in approves via app/approve-device.tsx.

import Foundation

enum Backend {
    // Public Convex deployment origin. Mirrors mobile/src/_core/constants.ts
    // CONVEX_SITE_URL — not a secret (it's the public backend host); bump here
    // and in the mobile/tvOS constants together if the deployment ever moves.
    static let convexSiteURL = URL(string: "https://perceptive-minnow-557.eu-west-1.convex.site")!
    static let webBaseURL = URL(string: "https://yaver.io")!
    static let agentPort = 18080
}

struct DeviceCodeStart: Decodable {
    let userCode: String
    let deviceCode: String
    let expiresAt: Double
    /// QR target that routes a scan into the phone approver.
    var verifyURL: URL {
        var comps = URLComponents(url: Backend.webBaseURL.appendingPathComponent("auth/device"),
                                  resolvingAgainstBaseURL: false)!
        comps.queryItems = [URLQueryItem(name: "code", value: userCode)]
        return comps.url!
    }
}

enum DevicePollStatus: String, Decodable {
    case pending, authorized, expired
}

struct DevicePollResult: Decodable {
    let status: DevicePollStatus
    let token: String?
}

enum DeviceCodeError: Error, LocalizedError {
    case createFailed(Int)
    var errorDescription: String? {
        switch self {
        case .createFailed(let code): return "Couldn't start sign-in (\(code)). Check your connection."
        }
    }
}

enum DeviceCodeAuth {
    static func start(machineName: String = "Apple Watch") async throws -> DeviceCodeStart {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("auth/device-code"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: [
            "machineName": machineName,
            "platform": "watchos",
            "environment": "watch",
        ])
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            throw DeviceCodeError.createFailed((resp as? HTTPURLResponse)?.statusCode ?? -1)
        }
        // Decode the raw fields, then synthesize the struct (verifyURL is computed).
        struct Raw: Decodable { let userCode: String; let deviceCode: String; let expiresAt: Double }
        let raw = try JSONDecoder().decode(Raw.self, from: data)
        return DeviceCodeStart(userCode: raw.userCode, deviceCode: raw.deviceCode, expiresAt: raw.expiresAt)
    }

    static func poll(deviceCode: String) async -> DevicePollResult {
        var comps = URLComponents(url: Backend.convexSiteURL.appendingPathComponent("auth/device-code/poll"),
                                  resolvingAgainstBaseURL: false)!
        comps.queryItems = [URLQueryItem(name: "device_code", value: deviceCode)]
        guard let url = comps.url else { return DevicePollResult(status: .pending, token: nil) }
        do {
            let (data, resp) = try await URLSession.shared.data(from: url)
            guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                return DevicePollResult(status: .pending, token: nil)
            }
            return try JSONDecoder().decode(DevicePollResult.self, from: data)
        } catch {
            return DevicePollResult(status: .pending, token: nil)
        }
    }
}

/// A box (device) the watch can drive in standalone mode. For the LAN MVP the
/// user supplies the host; later this can be populated from the Convex device
/// registry / LAN beacon. Mirrors tvos/YaverTV/Models.swift::BoxTarget.
struct BoxTarget: Codable, Identifiable, Equatable {
    var id: String          // deviceId (or a stable local id)
    var name: String
    var host: String        // LAN IP / hostname running `yaver serve`
    var port: Int = Backend.agentPort
}
