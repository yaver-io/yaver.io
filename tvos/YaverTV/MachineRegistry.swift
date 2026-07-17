// MachineRegistry.swift — the user's machines, straight from their account.
//
// The whole reason the TV showed "No box selected" with no way to see existing
// machines: it never asked the backend what machines the account HAS. Mobile
// does — `GET /devices/list` returns the device registry with connectable
// addresses (quicHost + localIps), and mobile builds its picker from that
// (mobile/src/context/DeviceContext.tsx). The token the TV already holds from
// device-code sign-in is sufficient to make the same call.
//
// (The Convex privacy contract forbids task/exec payloads and path leaks from
// Convex — it does NOT forbid the device registry's own address fields, which
// exist precisely so a client with no LAN beacon can still find its boxes. See
// backend/convex/schema.ts devices.quicHost/localIps.)

import Foundation

/// One machine as the account knows it. Mirrors the fields mobile consumes in
/// DeviceContext.tsx:1153-1228 — enough to list, show liveness, resolve an
/// address, and offer Wake for a managed box.
struct RegisteredDevice: Decodable, Identifiable {
    let deviceId: String
    let name: String?
    let alias: String?
    let platform: String?
    let isOnline: Bool?
    let quicHost: String?
    let quicPort: Int?
    let localIps: [String]?
    let relayConnected: Bool?
    let agentVersion: String?
    let managed: Bool?
    let machineId: String?
    let lastHeartbeat: Double? // ms epoch

    // Sharing. A box someone else owns and shared with this account comes back
    // from /devices/list looking exactly like an owned one unless we decode
    // these (devices.ts:1795, :1863). Without them the TV silently renders
    // another person's machine as if it were yours.
    let isGuest: Bool?
    let hostName: String?
    let hostEmail: String?
    let hostUserIdString: String?
    let accessScope: String? // "owner" | "shared-scoped" | "shared-legacy"

    var id: String { deviceId }

    var shared: Bool { isGuest ?? false }

    /// Who shared it, best-effort — name, else email, else a generic. Shown on
    /// the row, so it must never be empty on a shared box.
    var hostLabel: String {
        if let n = hostName, !n.isEmpty { return n }
        if let e = hostEmail, !e.isEmpty { return e }
        return "another account"
    }

    var displayName: String {
        if let a = alias, !a.isEmpty { return a }
        if let n = name, !n.isEmpty { return n }
        return String(deviceId.prefix(8))
    }

    /// Heartbeat fresh within 15 min — the same window mobile uses
    /// (HEARTBEAT_STALE_MS = 900_000, devices.ts). We can't call Date.now in a
    /// pure model, so liveness is computed by the store against a captured now.
    static let heartbeatStaleMs: Double = 900_000

    /// Address candidates to try, best-first: private LAN IPs (the TV is on a
    /// LAN), then Tailscale (100.64/10), then the primary quicHost. The relay is
    /// the off-LAN fallback and is handled at the client layer, not here.
    var addressCandidates: [String] {
        var out: [String] = []
        let ips = localIps ?? []
        let privates = ips.filter { isPrivateLAN($0) }
        let tailscale = ips.filter { $0.hasPrefix("100.") && !privates.contains($0) }
        out.append(contentsOf: privates)
        out.append(contentsOf: tailscale)
        if let h = quicHost, !h.isEmpty, !out.contains(h) { out.append(h) }
        // De-dupe, drop docker bridge noise (172.17.x) to the back.
        let ranked = out.sorted { a, b in dockerish(a) == dockerish(b) ? false : !dockerish(a) }
        return ranked
    }

    var wakeable: Bool { (managed ?? false) && (machineId?.isEmpty == false) }
    var port: Int { quicPort ?? Backend.agentPort }
}

/// RFC1918 — the ranges a TV on a home/office LAN can actually reach directly.
func isPrivateLAN(_ ip: String) -> Bool {
    if ip.hasPrefix("10.") || ip.hasPrefix("192.168.") { return true }
    if ip.hasPrefix("172.") {
        let parts = ip.split(separator: ".")
        if parts.count > 1, let second = Int(parts[1]), (16...31).contains(second) { return true }
    }
    return false
}

private func dockerish(_ ip: String) -> Bool { ip.hasPrefix("172.17.") || ip.hasPrefix("172.18.") }

enum MachineRegistry {
    struct DeviceList: Decodable { let devices: [RegisteredDevice] }
    struct GuestList: Decodable { let guests: [HostGuest] }

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

    /// Fetch the account's machines. Throws AgentError with a readable message so
    /// the picker can show WHY it's empty (expired token, offline, etc.) instead
    /// of a silent blank.
    static func fetch(token: String) async throws -> [RegisteredDevice] {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("devices/list"))
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 12
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "no response from Yaver") }
        if http.statusCode == 401 || http.statusCode == 403 {
            throw AgentError(message: "Your TV session expired — sign in again.")
        }
        guard (200..<300).contains(http.statusCode) else {
            throw AgentError(message: "Couldn't load your machines (\(http.statusCode)).")
        }
        return (try JSONDecoder().decode(DeviceList.self, from: data)).devices
    }

    /// GET /guests/list — who this account has shared with. The host-side TV
    /// surface only acts on accepted guests; pending/revoked rows stay out of
    /// view because there is nothing useful to do with them from the couch.
    static func listGuests(token: String) async throws -> [HostGuest] {
        let data = try await getGuest(path: "guests/list", token: token,
                                      failure: "Couldn't load shared access")
        return (try JSONDecoder().decode(GuestList.self, from: data)).guests
    }

    /// Ask a machine to update its agent, WITHOUT reaching it.
    ///
    /// This is the only update trigger this surface can honestly offer. The TV
    /// talks to a box over direct LAN only (YaverStore has no relay), so it
    /// cannot POST /agent/update to a box on another network — or to one that is
    /// asleep on this one. `/devices/request-update` instead writes desired state
    /// onto the device row; the agent reads it off its own next heartbeat and
    /// updates itself. Owner-only, and it never expires.
    ///
    /// The consequence for the UI: there is NO progress to show. We know the
    /// request was accepted, not that the box applied it — so the surface says
    /// "requested", never "updating". `version` nil means "latest".
    ///
    /// Convex-direct, like fetch(token:) above — AgentClient can't serve this: its
    /// postJSON is hardwired to http://<box.host>:<box.port>, which is exactly the
    /// address we may have no route to.
    @discardableResult
    static func requestUpdate(deviceId: String, version: String? = nil, token: String) async throws -> String {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("devices/request-update"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 12
        var body: [String: Any] = ["deviceId": deviceId]
        if let version, !version.isEmpty { body["version"] = version }
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "no response from Yaver") }
        if http.statusCode == 401 || http.statusCode == 403 {
            throw AgentError(message: "Your TV session expired — sign in again.")
        }
        guard (200..<300).contains(http.statusCode) else {
            // The backend answers {error: "…"} — carry the real reason ("Device
            // not found", "Unauthorized") rather than a bare status code.
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String {
                throw AgentError(message: err)
            }
            throw AgentError(message: "Couldn't request the update (\(http.statusCode)).")
        }
        struct Ack: Decodable { let requestedVersion: String? }
        return (try? JSONDecoder().decode(Ack.self, from: data))?.requestedVersion ?? "latest"
    }

    /// Drop this account's OWN access to everything a host shared.
    ///
    /// Guest-side only: the backend takes the guest from the session token, so
    /// this can never remove anyone else's access (http.ts:6480). Note the blast
    /// radius the UI must state plainly — it is keyed on the HOST, not the row,
    /// so it removes every machine that host shared, not just the one in hand.
    /// Reversible: the host can invite again and the guest can accept again.
    ///
    /// Convex-direct for the same reason as requestUpdate: a shared box may be
    /// on someone else's LAN, so there is no address to POST to.
    static func leaveHost(hostUserId: String?, hostEmail: String?, token: String) async throws {
        var body: [String: Any] = [:]
        if let hostUserId, !hostUserId.isEmpty { body["hostUserId"] = hostUserId }
        if let hostEmail, !hostEmail.isEmpty { body["hostEmail"] = hostEmail }
        guard !body.isEmpty else {
            throw AgentError(message: "Can't tell who shared this machine — try again from your phone or the web.")
        }
        _ = try await postGuest(path: "guests/leave", body: body, token: token,
                               failure: "Couldn't remove your access")
    }

    /// Redeem a 6-char invitation code. The TV has no real keyboard, so the code
    /// is the ONLY guest-side entry we offer here — the host does the inviting on
    /// a surface where typing an email isn't hostile.
    static func acceptInviteCode(_ code: String, token: String) async throws {
        let clean = code.trimmingCharacters(in: .whitespacesAndNewlines).uppercased()
        guard !clean.isEmpty else { throw AgentError(message: "Enter the invitation code.") }
        _ = try await postGuest(path: "guests/accept-code", body: ["code": clean], token: token,
                                failure: "Couldn't accept the invitation")
    }

    /// POST /guests/revoke — remove someone else's access to every machine this
    /// account shared with them. Key by the identifiers /guests/list already
    /// returns; email is preferred because it is the human-visible identity.
    static func revokeGuest(email: String?, userId: String?, token: String) async throws {
        var body: [String: Any] = [:]
        if let email, !email.isEmpty { body["email"] = email }
        if let userId, !userId.isEmpty { body["userId"] = userId }
        guard !body.isEmpty else {
            throw AgentError(message: "Can't tell which guest this is — try again from your phone or the web.")
        }
        _ = try await postGuest(path: "guests/revoke", body: body, token: token,
                                failure: "Couldn't remove access")
    }

    /// Shared shape of the guest POSTs: bearer token to Convex, {error} carried
    /// through verbatim (the backend's reasons — "Invitation not found",
    /// "No shared access found" — beat a bare status code).
    @discardableResult
    private static func postGuest(path: String, body: [String: Any], token: String,
                                  failure: String) async throws -> Data {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent(path))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 12
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "no response from Yaver") }
        if http.statusCode == 401 || http.statusCode == 403 {
            throw AgentError(message: "Your TV session expired — sign in again.")
        }
        guard (200..<300).contains(http.statusCode) else {
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String {
                throw AgentError(message: err)
            }
            throw AgentError(message: "\(failure) (\(http.statusCode)).")
        }
        return data
    }

    private static func getGuest(path: String, token: String, failure: String) async throws -> Data {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent(path))
        req.httpMethod = "GET"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 12
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "no response from Yaver") }
        if http.statusCode == 401 || http.statusCode == 403 {
            throw AgentError(message: "Your TV session expired — sign in again.")
        }
        guard (200..<300).contains(http.statusCode) else {
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String {
                throw AgentError(message: err)
            }
            throw AgentError(message: "\(failure) (\(http.statusCode)).")
        }
        return data
    }

    /// Probe address candidates and return the first that answers /info within a
    /// short deadline, so selecting a machine lands on an address that actually
    /// works — mirrors mobile's raceDirectCandidates (quic.ts:5993), sequential
    /// for simplicity. Returns nil if none answer (caller can still add it and
    /// let the relay/manual path take over).
    static func firstReachable(_ candidates: [String], port: Int, token: String) async -> String? {
        for host in candidates {
            var req = URLRequest(url: URL(string: "http://\(host):\(port)/info")!)
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
            req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
            req.timeoutInterval = 2
            if let (_, resp) = try? await URLSession.shared.data(for: req),
               let http = resp as? HTTPURLResponse, (200..<500).contains(http.statusCode) {
                return host // any HTTP answer means the port is open and it's the agent
            }
        }
        return nil
    }
}
