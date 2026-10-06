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
    static let privateVPSKey = "yaver.privateVpsUrl"
    static var convexSiteURL: URL {
        if let raw = UserDefaults.standard.string(forKey: privateVPSKey),
           let url = normalizedPrivateVPSURL(raw) { return url }
        return URL(string: "https://perceptive-minnow-557.eu-west-1.convex.site")!
    }
    static var webBaseURL: URL {
        if let raw = UserDefaults.standard.string(forKey: privateVPSKey),
           let url = normalizedPrivateVPSURL(raw) { return url }
        return URL(string: "https://yaver.io")!
    }
    static let agentPort = 18080

    static func normalizedPrivateVPSURL(_ raw: String) -> URL? {
        guard var parts = URLComponents(string: raw.trimmingCharacters(in: .whitespacesAndNewlines)),
              parts.scheme == "https", parts.host != nil,
              parts.user == nil, parts.password == nil, parts.query == nil, parts.fragment == nil else { return nil }
        while parts.path.count > 1 && parts.path.hasSuffix("/") { parts.path.removeLast() }
        return parts.url
    }
}

/// Surface-scoped appearance for standalone mode. Paired mode routes through
/// the phone and never gives the watch its token; standalone already owns a
/// token explicitly, so making it open the phone would be a false dependency.
enum WatchAppearanceSettings {
    static func load(token: String) async throws -> String {
        var request = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("settings"))
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("watchos", forHTTPHeaderField: "X-Yaver-Surface")
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            throw PhoneSessionError.appearanceSyncFailed("Couldn't load appearance.")
        }
        let root = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        let settings = root?["settings"] as? [String: Any]
        let rows = settings?["appearanceThemeBySurface"] as? [[String: Any]] ?? []
        let theme = rows.last(where: { $0["surface"] as? String == "watchos" })?["theme"] as? String
        return theme == "light" ? "light" : "dark"
    }

    static func save(token: String, theme: String) async throws -> String {
        let next = theme == "light" ? "light" : "dark"
        var request = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("settings"))
        request.httpMethod = "POST"
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("watchos", forHTTPHeaderField: "X-Yaver-Surface")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: [
            "appearanceThemeForSurface": ["surface": "watchos", "theme": next],
        ])
        let (_, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            throw PhoneSessionError.appearanceSyncFailed("Couldn't save appearance.")
        }
        return next
    }
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
    /// Set when the poll never got an answer (offline, DNS, 5xx, bad JSON).
    ///
    /// Without it, a transport failure was reported as `.pending` — which the UI
    /// renders as "waiting for you to approve", i.e. we're fine, YOU haven't
    /// approved yet. A watch that could not reach Convex at all looked identical
    /// to one waiting on the user. Same fix as tvos/YaverTV/Backend.swift; ported
    /// here per the cross-surface parity rule.
    var unreachableReason: String? = nil
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
        guard let url = comps.url else {
            return DevicePollResult(status: .pending, token: nil, unreachableReason: "bad poll URL")
        }
        do {
            let (data, resp) = try await URLSession.shared.data(from: url)
            guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
                return DevicePollResult(status: .pending, token: nil,
                                        unreachableReason: "server returned HTTP \(code)")
            }
            return try JSONDecoder().decode(DevicePollResult.self, from: data)
        } catch {
            return DevicePollResult(status: .pending, token: nil,
                                    unreachableReason: error.localizedDescription)
        }
    }

    /// Extend the standalone 1-year session on launch so an opted-in watch never
    /// re-prompts for OAuth — the Netflix contract. Only relevant in standalone
    /// mode (phone-paired mode holds no token). Extend-only, NO rotation: a wrist
    /// on flaky Wi-Fi routinely loses the response, and rotating would strand it
    /// on a dead token → a false logout. Mirrors mobile's no-rotate decision
    /// (mobile/src/lib/auth.ts, root-caused 2026-07-15) and tvOS's
    /// Backend.refreshSession. Security: no wider blast radius — the token
    /// already lives a year in the watch's own store; we only reset the clock.
    /// Returns a rotated token if the server ever returns one (it won't without
    /// opt-in), else nil. Any failure is a silent no-op.
    static func refreshSession(token: String) async -> String? {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("auth/refresh"))
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("watch", forHTTPHeaderField: "X-Yaver-Surface")
        guard let (data, resp) = try? await URLSession.shared.data(for: req),
              let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            return nil
        }
        struct Raw: Decodable { let token: String? }
        return (try? JSONDecoder().decode(Raw.self, from: data))?.token
    }
}

/// Ask a box to update its agent, WITHOUT reaching it.
///
/// Convex-direct, and the only update trigger this surface can honestly offer.
/// In the DEFAULT phone-paired mode the watch holds no token and no box, so this
/// is standalone-only — it's called from SettingsView behind the opt-in.
///
/// `/devices/request-update` writes desired state onto the device row; the agent
/// reads it off its own next heartbeat and updates itself. Owner-only, never
/// expires. Nothing here needs a route to the box, which is the entire point on
/// a wrist: SessionClient can only reach a box on the same LAN, and a box that
/// needs updating is often one we can't reach at all.
///
/// The consequence for the UI: there is NO progress to read. We learn the
/// request was ACCEPTED, not that it was applied — so the surface says
/// "requested", never "updating". Mirrors tvos MachineRegistry.requestUpdate.
enum AgentUpdate {
    /// Returns the version the backend recorded ("latest" unless pinned).
    @discardableResult
    static func request(deviceId: String, version: String? = nil, token: String) async throws -> String {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("devices/request-update"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("watch", forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 12
        var body: [String: Any] = ["deviceId": deviceId]
        if let version, !version.isEmpty { body["version"] = version }
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "No response from Yaver.") }
        if http.statusCode == 401 || http.statusCode == 403 {
            throw AgentError(message: "Session expired — sign in again.")
        }
        guard (200..<300).contains(http.statusCode) else {
            // The backend answers {error: "…"} — carry the real reason.
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String {
                throw AgentError(message: err)
            }
            throw AgentError(message: "Couldn't request the update (\(http.statusCode)).")
        }
        struct Ack: Decodable { let requestedVersion: String? }
        return (try? JSONDecoder().decode(Ack.self, from: data))?.requestedVersion ?? "latest"
    }
}

/// Account-level removal for the standalone watch Settings surface. The
/// backend refuses this route for an active Yaver-hosted cloud box, so the
/// wrist cannot orphan provider resources or billing.
enum DeviceRemoval {
    static func remove(deviceId: String, token: String) async throws {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("devices/remove"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("watch", forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 12
        req.httpBody = try JSONSerialization.data(withJSONObject: ["deviceId": deviceId])
        let (data, response) = try await URLSession.shared.data(for: req)
        guard let http = response as? HTTPURLResponse else {
            throw AgentError(message: "No response from Yaver.")
        }
        guard (200..<300).contains(http.statusCode) else {
            let message = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String
            throw AgentError(message: message ?? "Couldn't remove this box (\(http.statusCode)).")
        }
    }
}

struct BoxTarget: Codable, Identifiable, Equatable {
    /// Stable local identity. In practice this is the HOST STRING the user typed
    /// (SignInView's AddBoxView is the only construction site) — NOT a deviceId,
    /// despite what this field was once documented to be. It stays the host: it
    /// is the Identifiable key, and changing it would churn persisted rows for
    /// no gain.
    var id: String
    var name: String
    var host: String        // LAN IP / hostname running `yaver serve`
    var port: Int = Backend.agentPort

    /// The box's REAL deviceId, as the account knows it — resolved from the
    /// box's own `/info` at sign-in and persisted from then on.
    ///
    /// Why it's captured at setup and not on demand: the one caller that needs
    /// it (`/devices/request-update`) exists precisely for a box we CANNOT
    /// reach — asleep, moved networks, on cellular. Asking the box who it is at
    /// that moment would fail exactly when it matters. At sign-in the user is
    /// standing on the box's LAN by construction (they just typed its address
    /// and the transport works), so that is the one moment identity is free.
    ///
    /// Optional because boxes persisted before this field existed decode to nil
    /// (synthesized Codable uses decodeIfPresent for Optionals — the same
    /// forward-compat trick tvOS's BoxTarget.managed/machineId rely on). Those
    /// installs re-resolve lazily; see WatchStore.resolveDeviceIdIfNeeded.
    var deviceId: String? = nil
}
