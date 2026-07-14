// Backend.swift — Convex origin + RFC 8628 device-code sign-in for tvOS.
//
// Mirrors mobile/src/lib/tvSignIn.ts exactly (same Convex HTTP contract that
// `yaver auth` and the CLI device-code flow use):
//   POST /auth/device-code                      -> { userCode, deviceCode, expiresAt }
//   GET  /auth/device-code/poll?device_code=... -> { status, token? }
// A phone already signed in approves via app/approve-device.tsx.

import Foundation

enum Backend {
    // Public Convex deployment origin. Mirrors mobile/src/_core/constants.ts
    // CONVEX_SITE_URL — not a secret (it's the public backend host); bump here
    // and in the mobile constant together if the deployment ever moves.
    static let convexSiteURL = URL(string: "https://perceptive-minnow-557.eu-west-1.convex.site")!
    static let webBaseURL = URL(string: "https://yaver.io")!
    static let agentPort = 18080

    /// This frontend's surface, sent as X-Yaver-Surface on every request so the
    /// agent can adapt per surface (tv vs watch vs car vs vision). See the Go
    /// agent's surface.go.
    static let surface = "tv"
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
    /// `platform`/`environment` are what the device registers itself as. They
    /// default to tvOS because this file was the TV's first, and visionOS imports
    /// it — a headset that took the defaults registered in Convex as an Apple TV,
    /// so the user's own device list lied about what they were wearing. The
    /// backend takes these as free-form strings (deviceCode.ts: v.optional
    /// (v.string())), so a surface just has to say what it actually is.
    static func start(
        machineName: String = "Apple TV",
        platform: String = "tvos",
        environment: String = "tv"
    ) async throws -> DeviceCodeStart {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("auth/device-code"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: [
            "machineName": machineName,
            "platform": platform,
            "environment": environment,
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
