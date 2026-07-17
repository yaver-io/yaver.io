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

// MARK: - Guest access (Convex-direct, token-only)

/// A pending invitation someone sent to this account.
struct GuestPendingInvite: Decodable, Identifiable, Equatable {
    let inviteId: String
    let inviteCode: String
    let hostName: String
    let hostEmail: String
    let expiresAt: Double
    var id: String { inviteId }
}

/// A host whose machines this account currently has access to.
struct GuestActiveHost: Decodable, Identifiable, Equatable {
    struct Device: Decodable, Equatable {
        let deviceId: String
        let name: String
    }
    let hostName: String
    let hostEmail: String
    let grantedAt: Double
    let devices: [Device]?

    /// Identity for the list AND the only key we may `leave` by — see
    /// GuestAccess.leave for why `hostUserId` is deliberately not decoded.
    var id: String { hostEmail }
    var deviceCount: Int { devices?.count ?? 0 }
}

struct GuestHosts: Decodable, Equatable {
    let pending: [GuestPendingInvite]
    let active: [GuestActiveHost]

    var isEmpty: Bool { pending.isEmpty && active.isEmpty }
}

/// One guest on the HOST side of sharing (`GET /guests/list`).
struct HostGuest: Decodable, Identifiable, Equatable {
    let email: String?
    let userId: String?
    let fullName: String?
    let status: String?
    let createdAt: Double?
    let acceptedAt: Double?
    let revokedAt: Double?

    var id: String {
        if let userId, !userId.isEmpty { return userId }
        if let email, !email.isEmpty { return email }
        return "\(createdAt ?? 0)-\(acceptedAt ?? 0)-\(revokedAt ?? 0)"
    }

    var displayName: String {
        if let fullName, !fullName.isEmpty { return fullName }
        if let email, !email.isEmpty { return email }
        if let userId, !userId.isEmpty { return userId }
        return "Unknown guest"
    }

    var detail: String {
        if let email, !email.isEmpty { return email }
        if let userId, !userId.isEmpty { return userId }
        return "No email available"
    }

    var isAccepted: Bool { status == "accepted" }
}

struct HostGuests: Decodable, Equatable {
    let guests: [HostGuest]
}

/// Guest-side access management: see who shared machines with me, accept an
/// invite, and drop my own access.
///
/// Convex-direct and keyed ONLY by the session token — this is why it works on a
/// wrist that has no device registry. Guest access is anchored to a HOST (a
/// person), not to a box: `/guests/hosts` returns a short list of people, so the
/// watch's "one box by address" model is simply not involved. Nothing here needs
/// a route to any box, exactly like AgentUpdate above.
///
/// Host-side revoke is present because it needs no typing: the list already
/// names the guest. Host-side invite stays absent because typing an email on a
/// wrist is hostile, and there is no email-less invite backend verb.
enum GuestAccess {
    /// Shared request builder — every call is Bearer + the watch surface tag.
    private static func request(_ path: String, method: String, token: String) -> URLRequest {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent(path))
        req.httpMethod = method
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("watch", forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 12
        return req
    }

    /// Turn a non-2xx into the backend's own `{error: "…"}` reason where it has one.
    private static func check(_ data: Data, _ resp: URLResponse, fallback: String) throws {
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "No response from Yaver.") }
        if http.statusCode == 401 || http.statusCode == 403 {
            throw AgentError(message: "Session expired — sign in again.")
        }
        guard (200..<300).contains(http.statusCode) else {
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String {
                throw AgentError(message: err)
            }
            throw AgentError(message: "\(fallback) (\(http.statusCode)).")
        }
    }

    /// GET /guests/hosts → { pending, active } from the GUEST's perspective.
    static func hosts(token: String) async throws -> GuestHosts {
        let (data, resp) = try await URLSession.shared.data(for: request("guests/hosts", method: "GET", token: token))
        try check(data, resp, fallback: "Couldn't load shared access")
        return try JSONDecoder().decode(GuestHosts.self, from: data)
    }

    /// GET /guests/list — who this account has shared with as the host.
    static func guests(token: String) async throws -> HostGuests {
        let (data, resp) = try await URLSession.shared.data(for: request("guests/list", method: "GET", token: token))
        try check(data, resp, fallback: "Couldn't load people you shared with")
        return try JSONDecoder().decode(HostGuests.self, from: data)
    }

    /// POST /guests/accept-code — accept a pending invite by its 6-char code.
    ///
    /// The code comes off the pending row we just listed, so the wrist never has
    /// to type it. `approvedDeviceIds` is omitted deliberately: the watch has no
    /// device registry to choose from, and omitting it accepts the invitation as
    /// the host proposed it.
    static func acceptCode(_ code: String, token: String) async throws {
        var req = request("guests/accept-code", method: "POST", token: token)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: ["code": code])
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(data, resp, fallback: "Couldn't accept the invitation")
    }

    /// POST /guests/leave — drop MY OWN access to everything `hostEmail` shared.
    ///
    /// Keyed by email, NOT by the `hostUserId` that `/guests/hosts` reports for an
    /// active host. That field is the host's internal Convex document id, whereas
    /// `guests.leave` resolves `hostUserId` against the PUBLIC `users.userId`
    /// string — feeding it the doc id fails with "No Yaver user found for that
    /// host". The active rows carry no public userId (only the pending rows do,
    /// as `hostUserIdString`), so email is the one identifier that is both
    /// present and correct here. The endpoint accepts either.
    ///
    /// Guest-only by construction: the session user IS the guest, so this can
    /// never remove anyone else's access.
    static func leave(hostEmail: String, token: String) async throws {
        var req = request("guests/leave", method: "POST", token: token)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: ["hostEmail": hostEmail])
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(data, resp, fallback: "Couldn't remove your access")
    }

    /// POST /guests/revoke — remove a guest from every machine this account
    /// shared with them. Use the identifiers /guests/list already returns.
    static func revoke(email: String?, userId: String?, token: String) async throws {
        var body: [String: Any] = [:]
        if let email, !email.isEmpty { body["email"] = email }
        if let userId, !userId.isEmpty { body["userId"] = userId }
        guard !body.isEmpty else {
            throw AgentError(message: "Can't tell which guest this is.")
        }
        var req = request("guests/revoke", method: "POST", token: token)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(data, resp, fallback: "Couldn't remove access")
    }
}

/// A box (device) the watch can drive in standalone mode. For the LAN MVP the
/// user supplies the host; later this can be populated from the Convex device
/// registry / LAN beacon. Mirrors tvos/YaverTV/Models.swift::BoxTarget.
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
