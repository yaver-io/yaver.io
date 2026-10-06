// Backend.swift — Convex origin + RFC 8628 device-code sign-in for tvOS.
//
// Mirrors mobile/src/lib/tvSignIn.ts exactly (same Convex HTTP contract that
// `yaver auth` and the CLI device-code flow use):
//   POST /auth/device-code                      -> { userCode, deviceCode, expiresAt }
//   GET  /auth/device-code/poll?device_code=... -> { status, token? }
// A phone already signed in approves via app/approve-device.tsx.

import Foundation

enum Backend {
    static let privateVPSKey = "yaver.privateVpsUrl"
    private static let hostedConvexSiteURL = URL(string: "https://perceptive-minnow-557.eu-west-1.convex.site")!
    // Public Convex deployment origin. Mirrors mobile/src/_core/constants.ts
    // CONVEX_SITE_URL — not a secret (it's the public backend host); bump here
    // and in the mobile constant together if the deployment ever moves.
    static var convexSiteURL: URL {
        #if DEBUG
        // The argument-domain override lets the tvOS UI suite exercise the
        // production HTTP path against a real loopback server. Apple TV users
        // cannot set process launch arguments. Compile the seam out of release
        // builds as well, so no persisted preference can ever redirect account
        // arbitrary launch arguments from becoming a release-build redirect.
        // The user-controlled private VPS preference below is validated and
        // intentionally supported in release builds.
        if let raw = UserDefaults.standard.string(forKey: "yaver.tv.backendURL"),
           let override = URL(string: raw),
           let scheme = override.scheme,
           scheme == "http" || scheme == "https" {
            return override
        }
        #endif
        if let raw = UserDefaults.standard.string(forKey: privateVPSKey),
           let override = normalizedPrivateVPSURL(raw) {
            return override
        }
        return hostedConvexSiteURL
    }
    static var webBaseURL: URL { privateVPSURL ?? URL(string: "https://yaver.io")! }
    static let agentPort = 18080

    static var privateVPSURL: URL? {
        guard let raw = UserDefaults.standard.string(forKey: privateVPSKey) else { return nil }
        return normalizedPrivateVPSURL(raw)
    }

    static func normalizedPrivateVPSURL(_ raw: String) -> URL? {
        let value = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard var parts = URLComponents(string: value),
              parts.scheme == "https",
              parts.host != nil,
              parts.user == nil, parts.password == nil,
              parts.query == nil, parts.fragment == nil else { return nil }
        while parts.path.count > 1 && parts.path.hasSuffix("/") { parts.path.removeLast() }
        return parts.url
    }

    static func privateVPSURL(fromQRCode raw: String) -> URL? {
        if let direct = normalizedPrivateVPSURL(raw) { return direct }
        if let parts = URLComponents(string: raw), parts.scheme == "yaver", parts.host == "private-vps",
           let value = parts.queryItems?.first(where: { $0.name == "url" })?.value {
            return normalizedPrivateVPSURL(value)
        }
        if let data = raw.data(using: .utf8),
           let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let value = (object["privateVpsUrl"] ?? object["url"]) as? String {
            return normalizedPrivateVPSURL(value)
        }
        return nil
    }

    static func setPrivateVPSURL(_ url: URL?) {
        if let url { UserDefaults.standard.set(url.absoluteString, forKey: privateVPSKey) }
        else { UserDefaults.standard.removeObject(forKey: privateVPSKey) }
    }

    /// This frontend's surface, sent as X-Yaver-Surface on every request so the
    /// agent can adapt per surface (tv vs watch vs car vs vision). See the Go
    /// agent's surface.go.
    static var surface: String {
        let configured = Bundle.main.object(forInfoDictionaryKey: "YaverNativeSurface") as? String
        let trimmed = configured?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? "tv" : trimmed
    }
}

struct DeviceCodeStart: Decodable {
    let userCode: String
    let deviceCode: String
    let expiresAt: Double
    /// LAN-approval material (2026-08-13): random nonce + 3-digit match code
    /// minted server-side and returned with the code. The TV broadcasts them
    /// on UDP 19837 (LanApprovalBeacon) so same-network surfaces can approve
    /// without scanning the QR. Both may be absent for very old backends —
    /// the QR path never depends on them.
    let approveNonce: String?
    let matchCode: String?
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

enum DeviceCodeDeliveryDecision: Equatable {
    case signIn(String)
    case claim
    case wait
    case rotate

    /// Arbitrate the long-event and fallback-poll lanes. A token-bearing result
    /// always wins, even when the sibling lane is already awaiting /claim.
    static func decide(status: DevicePollStatus, token: String?, claimInFlight: Bool) -> Self {
        if status == .expired { return .rotate }
        guard status == .authorized else { return .wait }
        if let token, !token.isEmpty { return .signIn(token) }
        return claimInFlight ? .wait : .claim
    }
}

struct DevicePollResult: Decodable {
    let status: DevicePollStatus
    let token: String?
    let claimHandle: String?
    let claimRequired: Bool?
    /// LAN approval, phase 1 (2026-08-13): an authenticated same-network
    /// surface requested approval (they saw this TV's UDP beacon). Non-nil
    /// while the 60s server-side window is open — the TV renders
    /// "Approve sign-in from <email>?" with Allow/Deny, then calls
    /// DeviceCodeAuth.lanConfirm(deviceCode:allow:).
    let lanPending: LanPendingInfo?
    /// Set when the poll never got an answer (offline, DNS, 5xx, bad JSON).
    ///
    /// Without this, a transport failure was reported as `.pending` — which the
    /// UI renders as "Waiting for approval…", i.e. "we're fine, YOU haven't
    /// approved yet". A TV that cannot reach Convex at all showed exactly the
    /// same screen as a TV waiting on the user, for as long as the user cared to
    /// stare at it. Keep polling (it usually IS transient), but say so.
    var unreachableReason: String? = nil
}

struct LanPendingInfo: Decodable {
    let approverEmail: String?
    let matchCode: String?
    let expiresAt: Double?
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
        var body: [String: Any] = [
            "machineName": machineName,
            "platform": platform,
            "environment": environment,
            "deviceId": TokenStore.installationID(),
        ]
        // Owner hint ("mobil onay"): if this device remembers its last owner,
        // say so — the owner's signed-in phone then gets a proactive approve
        // event (number-matched against the code on THIS screen) instead of
        // needing a QR scan. The hint grants nothing; approval still happens
        // on the phone's authenticated session.
        if let ownerHint = DeviceCodeAuth.lastOwnerUserId {
            body["ownerUserIdHint"] = ownerHint
        }
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            throw DeviceCodeError.createFailed((resp as? HTTPURLResponse)?.statusCode ?? -1)
        }
        // Decode the raw fields, then synthesize the struct (verifyURL is computed).
        struct Raw: Decodable {
            let userCode: String
            let deviceCode: String
            let expiresAt: Double
            let approveNonce: String?
            let matchCode: String?
        }
        let raw = try JSONDecoder().decode(Raw.self, from: data)
        return DeviceCodeStart(userCode: raw.userCode, deviceCode: raw.deviceCode, expiresAt: raw.expiresAt,
                               approveNonce: raw.approveNonce, matchCode: raw.matchCode)
    }

    static func poll(deviceCode: String) async -> DevicePollResult {
        var comps = URLComponents(url: Backend.convexSiteURL.appendingPathComponent("auth/device-code/poll"),
                                  resolvingAgainstBaseURL: false)!
        comps.queryItems = [URLQueryItem(name: "device_code", value: deviceCode)]
        guard let url = comps.url else {
            return DevicePollResult(status: .pending, token: nil, claimHandle: nil, claimRequired: nil, lanPending: nil,
                                    unreachableReason: "bad poll URL")
        }
        do {
            let (data, resp) = try await URLSession.shared.data(from: url)
            guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
                return DevicePollResult(status: .pending, token: nil, claimHandle: nil, claimRequired: nil, lanPending: nil,
                                    unreachableReason: "server returned HTTP \(code)")
            }
            return try JSONDecoder().decode(DevicePollResult.self, from: data)
        } catch {
            return DevicePollResult(status: .pending, token: nil, claimHandle: nil, claimRequired: nil, lanPending: nil,
                                    unreachableReason: error.localizedDescription)
        }
    }

    /// Event-first wait. The backend keeps this request open until the code
    /// changes (or times out), then closes it. We parse the last SSE data line.
    static func waitEvent(deviceCode: String) async -> DevicePollResult {
        var comps = URLComponents(url: Backend.convexSiteURL.appendingPathComponent("auth/device-code/events"),
                                  resolvingAgainstBaseURL: false)!
        comps.queryItems = [URLQueryItem(name: "device_code", value: deviceCode)]
        guard let url = comps.url else {
            return DevicePollResult(status: .pending, token: nil, claimHandle: nil, claimRequired: nil, lanPending: nil, unreachableReason: "bad events URL")
        }
        do {
            let (data, resp) = try await URLSession.shared.data(from: url)
            guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
                return DevicePollResult(status: .pending, token: nil, claimHandle: nil, claimRequired: nil, lanPending: nil,
                                    unreachableReason: "server returned HTTP \(code)")
            }
            let text = String(data: data, encoding: .utf8) ?? ""
            let payload = text
                .split(whereSeparator: \.isNewline)
                .filter { $0.hasPrefix("data:") }
                .map { String($0.dropFirst("data:".count)).trimmingCharacters(in: .whitespaces) }
                .last
            guard let payload, let body = payload.data(using: .utf8) else {
                return DevicePollResult(status: .pending, token: nil, claimHandle: nil, claimRequired: nil, lanPending: nil)
            }
            return try JSONDecoder().decode(DevicePollResult.self, from: body)
        } catch {
            return DevicePollResult(status: .pending, token: nil, claimHandle: nil, claimRequired: nil, lanPending: nil,
                                    unreachableReason: error.localizedDescription)
        }
    }

    static func claim(deviceCode: String, claimHandle: String?) async -> DevicePollResult {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("auth/device-code/claim"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        var body: [String: Any] = ["deviceCode": deviceCode]
        if let claimHandle, !claimHandle.isEmpty { body["claimHandle"] = claimHandle }
        req.httpBody = try? JSONSerialization.data(withJSONObject: body)
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                let code = (resp as? HTTPURLResponse)?.statusCode ?? -1
                return DevicePollResult(status: .pending, token: nil, claimHandle: nil, claimRequired: nil, lanPending: nil,
                                    unreachableReason: "server returned HTTP \(code)")
            }
            return try JSONDecoder().decode(DevicePollResult.self, from: data)
        } catch {
            return DevicePollResult(status: .pending, token: nil, claimHandle: nil, claimRequired: nil, lanPending: nil,
                                    unreachableReason: error.localizedDescription)
        }
    }

    /// LAN approval, phase 2 (2026-08-13): the TV shows "Approve sign-in from
    /// <email>?" and the user presses Allow/Deny. The deviceCode is the TV's
    /// secret (same trust anchor as /claim); Allow binds the pending LAN
    /// approver's account, Deny clears the pending window and the code stays
    /// usable for the QR path.
    struct LanConfirmResult: Decodable {
        let ok: Bool?
        let denied: Bool?
        let claimHandle: String?
        let reason: String?
    }

    static func lanConfirm(deviceCode: String, allow: Bool) async -> LanConfirmResult {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("auth/device-code/lan-confirm"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try? JSONSerialization.data(withJSONObject: [
            "deviceCode": deviceCode,
            "allow": allow,
        ])
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            guard let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                return LanConfirmResult(ok: false, denied: nil, claimHandle: nil, reason: "HTTP \((resp as? HTTPURLResponse)?.statusCode ?? -1)")
            }
            return (try? JSONDecoder().decode(LanConfirmResult.self, from: data))
                ?? LanConfirmResult(ok: false, denied: nil, claimHandle: nil, reason: "bad response")
        } catch {
            return LanConfirmResult(ok: false, denied: nil, claimHandle: nil, reason: error.localizedDescription)
        }
    }

    /// Extend the 1-year session on launch so a lean-back device opened at least
    /// once a year NEVER re-prompts for OAuth — the Netflix-on-AppleTV contract.
    /// Device-code auth mints a 1-year token but nothing extends it; without this
    /// the token silently hard-expires and forces a fresh sign-in.
    ///
    /// Extend-only, NO rotation (no X-Yaver-Rotate-Token): a lean-back device
    /// routinely loses the response (sleep / flaky Wi-Fi), and rotating would
    /// strand it on a dead token → a false logout of a live session. Mirrors
    /// mobile's deliberate no-rotate decision (mobile/src/lib/auth.ts,
    /// root-caused 2026-07-15). Security: this does NOT widen the blast radius —
    /// the token already lives a year and is held in the device's own store; we
    /// only reset the existing clock. Returns a rotated token IF the server ever
    /// returns one (it won't without opt-in), else nil. Any failure is a silent
    /// no-op; the existing token stays valid.
    struct SessionRefresh {
        let token: String?
        let isOwner: Bool
    }

    static func refreshSession(token: String) async -> SessionRefresh? {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("auth/refresh"))
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        guard let (data, resp) = try? await URLSession.shared.data(for: req),
              let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            return nil
        }
        struct Raw: Decodable { let token: String?; let userId: String?; let isOwner: Bool? }
        let raw = try? JSONDecoder().decode(Raw.self, from: data)
        // Remember WHO this TV belongs to (opaque user doc id, grants
        // nothing). It survives sign-out ON PURPOSE: it is the owner HINT the
        // next device-code sign-in sends so the owner's phone gets a
        // proactive approve event instead of requiring a QR scan.
        if let uid = raw?.userId, !uid.isEmpty {
            UserDefaults.standard.set(uid, forKey: lastOwnerUserIdKey)
        }
        guard let raw else { return nil }
        return SessionRefresh(token: raw.token, isOwner: raw.isOwner == true)
    }

    /// Best-effort server revocation for a shared-room device. Local Keychain
    /// deletion happens regardless, but sign-out should not leave the QR-minted
    /// one-year bearer usable in the backend until natural expiry.
    static func revokeSession(token: String) async {
        guard !token.isEmpty else { return }
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("auth/logout"))
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        _ = try? await URLSession.shared.data(for: req)
    }

    /// UserDefaults key for the remembered owner hint (see refreshSession).
    static let lastOwnerUserIdKey = "yaver.tv.lastOwnerUserId"
    static var lastOwnerUserId: String? {
        let v = UserDefaults.standard.string(forKey: lastOwnerUserIdKey)
        return (v?.isEmpty == false) ? v : nil
    }
}

// Email/password sign-in for the TV (2026-08-13). The TV already holds an
// Apple ID, but not every Yaver account is an Apple account — and even an
// Apple account with 2FA can't finish natively on a TV. So the sign-in
// screen offers email+password typed with the remote, hitting the SAME
// POST /auth/login the web + CLI use. Rate-limited server-side (429), and
// gated by the deployment's email-password allowlist (403 carries the
// server's message verbatim — the TV must not invent its own story).
enum EmailAuthError: Error, LocalizedError {
    case invalidCredentials
    case lockedOut
    case requiresTwoFactor
    case server(String)

    var errorDescription: String? {
        switch self {
        case .invalidCredentials:
            return "Invalid email or password."
        case .lockedOut:
            return "Too many failed attempts. Wait a bit, then try again."
        case .requiresTwoFactor:
            return "Two-factor authentication is on. Approve with the QR code above from your phone, or in a browser."
        case .server(let message):
            return message
        }
    }
}

enum EmailAuth {
    /// POST /auth/login {email, password} → {token} | {requires2fa} | error.
    static func signIn(email: String, password: String) async throws -> String {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("auth/login"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        // A focused button that changes to "Signing in…" and then waits for
        // URLSession's long default timeout still reads as unresponsive from a
        // couch. Bound the operation and return control with a named error.
        req.timeoutInterval = 15
        req.httpBody = try JSONSerialization.data(withJSONObject: [
            "email": email.trimmingCharacters(in: .whitespacesAndNewlines),
            "password": password,
        ])
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else {
            throw EmailAuthError.server("No response from Yaver.")
        }
        let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        if http.statusCode == 429 { throw EmailAuthError.lockedOut }
        if http.statusCode == 401 { throw EmailAuthError.invalidCredentials }
        guard (200..<300).contains(http.statusCode) else {
            // 403 = email-password disabled / email not allowlisted — say
            // exactly what the server said, never a generic failure.
            throw EmailAuthError.server((obj?["error"] as? String) ?? "Email sign-in failed (\(http.statusCode)).")
        }
        if obj?["requires2fa"] as? Bool == true {
            throw EmailAuthError.requiresTwoFactor
        }
        guard let token = obj?["token"] as? String, !token.isEmpty else {
            throw EmailAuthError.server("Yaver didn't return a session token.")
        }
        return token
    }
}
