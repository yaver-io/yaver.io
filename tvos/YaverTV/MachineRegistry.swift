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

    var id: String { deviceId }

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
