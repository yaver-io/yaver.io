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
    let hosting: String?
    let machineId: String?
    let lastHeartbeat: Double? // ms epoch
    let runners: [RegisteredRunner]?
    let installedRunnerIds: [String]?

    // Compatibility bit used only to discard legacy non-owner rows returned by
    // a stale backend. It is never rendered or made selectable.

    var id: String { deviceId }

    /// Stable machine name from the agent (for example
    /// `ubuntu-4gb-hel1-1`). Aliases are account-local labels and must not
    /// replace this identity on a TV: doing so made the same box look like a
    /// different machine than it did in WebUI.
    var realName: String {
        if let n = name, !n.isEmpty { return n }
        if let a = alias, !a.isEmpty { return a }
        return String(deviceId.prefix(8))
    }

    /// Account alias, rendered beside/below the real name as `@alias`.
    var aliasLabel: String? {
        guard let alias, !alias.isEmpty, alias != realName else { return nil }
        return alias.hasPrefix("@") ? alias : "@\(alias)"
    }

    /// Compatibility label for sorting/narration. The real hostname leads;
    /// UI that has room also renders `aliasLabel` explicitly.
    var displayName: String { realName }

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

/// Coding-runner capability reported in the machine heartbeat. Settings uses
/// this live inventory so it never offers a default the selected machine does
/// not actually have installed.
struct RegisteredRunner: Decodable, Identifiable {
    let runnerId: String
    let installed: Bool?
    let ready: Bool?
    let authConfigured: Bool?
    let status: String?

    var id: String { Self.canonical(runnerId) }

    static func canonical(_ value: String) -> String {
        switch value.lowercased() {
        case "claude-code", "claude_code": return "claude"
        default: return value.lowercased()
        }
    }

    var label: String {
        switch id {
        case "claude": return "Claude Code"
        case "codex": return "Codex"
        case "opencode": return "OpenCode"
        default: return runnerId
        }
    }
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
    struct UserSettingsEnvelope: Decodable { let settings: UserSettings? }

    // ── Relay resolution (2026-08-13) ─────────────────────────────────────
    // The TV used settings.relayUrl as its ONLY relay source. For most
    // accounts that field is empty — it is a user override, not the default —
    // so a TV could only reach LAN boxes, and a remote box (Hetzner etc.)
    // that every other surface reaches over the free relay was unreachable.
    // The authoritative relay list is GET /config (the SAME source the web
    // dashboard's refreshRelayTopology reads); settings only ever supplies
    // the per-user relay password (and an optional URL override).
    struct RelayServer: Decodable {
        /// Current control-plane payload name.
        let httpUrl: String?
        /// Older payloads used `url`; keep accepting it during rollout.
        let url: String?

        var endpoint: String? { httpUrl ?? url }
    }
    struct RelayConfigEnvelope: Decodable { let relayServers: [RelayServer]? }

    /// Relay server URLs from GET /config (relayServers[].url). Empty on any
    /// failure — the caller then has no relay leg and stays LAN-only, exactly
    /// as before this source existed.
    static func fetchRelayServers(token: String) async -> [String] {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("config"))
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        guard
            let (data, resp) = try? await URLSession.shared.data(for: req),
            let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode),
            let env = try? JSONDecoder().decode(RelayConfigEnvelope.self, from: data)
        else { return [] }
        return (env.relayServers ?? [])
            .compactMap { $0.endpoint?.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }

    /// The relay leg a BoxTarget should use: settings.relayUrl when the user
    /// overrode it, else the first /config relay server. The password ONLY
    /// ever comes from /settings.
    static func resolvedRelay(token: String, settings: UserSettings?) async -> (url: String?, password: String?) {
        if let url = settings?.relayUrl, !url.isEmpty {
            return (url, settings?.relayPassword)
        }
        let servers = await fetchRelayServers(token: token)
        return (servers.first, settings?.relayPassword)
    }

    struct UserSettings: Decodable {
        let relayUrl: String?
        let relayPassword: String?
        /// Explicit auto-connect order shared with mobile/web.
        let primaryDeviceId: String?
        let secondaryDeviceId: String?
        /// Per-device default coding runner shared with mobile/web.
        let primaryRunnerByDevice: [PrimaryRunnerPref]?
        /// Runner/render machine split rows (same Convex rows the web edits).
        /// Additive decode — older payload shapes leave it nil.
        let machineRolesByProject: [MachineRolesRow]?
        /// Connection fan-out preference: "all" (default) or "single". Same
        /// userSettings field web and mobile read, out of the SAME /settings
        /// response this struct already decodes — a preference that cost an
        /// extra call per surface would be a poor trade for a preference.
        /// Additive: nil on older payloads, which correctly means "all".
        let connectionMode: String?
        /// Last-project memory — the SAME Convex row mobile (taskComposerPrefs)
        /// and the web chat composer write (defaultRuntimeProjectByDevice),
        /// so a project picked on the phone or the dashboard is remembered on
        /// the TV and vice versa. Row carries no absolute path — only
        /// {projectName, gitRemote, branch} — matched against the live
        /// /projects list. (2026-08-10)
        let defaultRuntimeProjectByDevice: [RuntimeProjectPref]?
        /// Per-device external-MCP selection + the yaver doorway toggle — the
        /// same mcpServersByDevice row mobile and web write.
        let mcpServersByDevice: [MCPServersPref]?
        let appearanceThemeBySurface: [AppearanceThemePref]?
    }

    struct AppearanceThemePref: Decodable {
        let surface: String
        let theme: String
    }

    struct PrimaryRunnerPref: Decodable {
        let deviceId: String?
        let runnerId: String?
        let model: String?
        let reasoningEffort: String?
        let mode: String?
        let provider: String?
    }

    /// One defaultRuntimeProjectByDevice row (Convex userSettings).
    struct RuntimeProjectPref: Decodable {
        let deviceId: String?
        let projectName: String?
        let gitRemote: String?
        let branch: String?
    }

    /// One mcpServersByDevice row.
    struct MCPServersPref: Decodable {
        let deviceId: String?
        let mcpServers: [String]?
        let includeYaverMcp: Bool?
    }

    /// The fan-out preference as a decision, not a raw string.
    ///
    /// Mirrors web/lib/connectionFanout.ts::fanoutModeFromSettings, including
    /// its asymmetry: ONLY the exact value "single" downgrades. Unset, unknown
    /// or malformed all mean "all", because fan-out is the product default and
    /// a value nobody recognises must never silently become a downgrade the
    /// user did not ask for.
    static func fanoutIsSingle(_ settings: UserSettings?) -> Bool {
        settings?.connectionMode == "single"
    }

    /// One runner/render split row. Row without projectName = the account-wide
    /// favorite; per-project rows override. Mirrors web/lib/useMachineRoles.ts
    /// and mobile DeviceContext's MachineRolesRow — key off the Convex config,
    /// never a per-surface copy.
    struct MachineRolesRow: Decodable, Equatable {
        let projectName: String?
        let runnerDeviceId: String
        let renderDeviceId: String?
        let workspace: String?
        let autoPush: String?
        let updatedAt: Double?
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

    /// Account removal for BYO/self-hosted devices. The shared backend
    /// tombstones the row, revokes old sessions, and hides it on every surface.
    static func removeDevice(deviceId: String, token: String) async throws {
        try await postRemoval(path: "devices/remove", token: token, body: ["deviceId": deviceId])
    }

    /// Provider-aware removal for Yaver-hosted boxes. This cancels linked
    /// billing and schedules the full cloud-resource purge without a snapshot.
    static func decommissionCloudMachine(machineId: String, token: String) async throws {
        try await postRemoval(path: "billing/yaver-cloud/dev-deprovision",
                              token: token, body: ["machineId": machineId])
    }

    private static func postRemoval(path: String, token: String, body: [String: Any]) async throws {
        guard !token.isEmpty else { throw AgentError(message: "Sign in first.") }
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent(path))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 20
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, response) = try await URLSession.shared.data(for: req)
        guard let http = response as? HTTPURLResponse else {
            throw AgentError(message: "No response from Yaver.")
        }
        guard (200..<300).contains(http.statusCode) else {
            let message = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String
            throw AgentError(message: message ?? "Couldn't remove this machine (\(http.statusCode)).")
        }
    }

    /// GET /settings — per-user transport metadata. Mirrors mobile's
    /// DeviceContext load: `/devices/list` is the inventory, `/settings` carries
    /// the user's relay URL/password. Keeping the password out of every device
    /// row avoids widening the device list payload, while still letting thin
    /// clients build a relay fallback for the selected machine.
    static func fetchSettings(token: String) async throws -> UserSettings {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("settings"))
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.timeoutInterval = 12
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "no response from Yaver") }
        if http.statusCode == 401 || http.statusCode == 403 {
            throw AgentError(message: "Your TV session expired — sign in again.")
        }
        guard (200..<300).contains(http.statusCode) else {
            throw AgentError(message: "Couldn't load relay settings (\(http.statusCode)).")
        }
        return (try? JSONDecoder().decode(UserSettingsEnvelope.self, from: data).settings)
            ?? UserSettings(relayUrl: nil, relayPassword: nil,
                            primaryDeviceId: nil, secondaryDeviceId: nil,
                            primaryRunnerByDevice: nil,
                            machineRolesByProject: nil,
                            connectionMode: nil, defaultRuntimeProjectByDevice: nil,
                            mcpServersByDevice: nil, appearanceThemeBySurface: nil)
    }

    /// POST /settings/repair-relay — re-sync this account's per-user relay
    /// password with the platform-managed value. The tvOS twin of mobile's
    /// DeviceContext.repairRelay and the web dashboard's auto-repair: when the
    /// relay keeps answering 401 "invalid relay password", the stored password
    /// drifted from what the relay expects, and this re-copies the current
    /// value (backend userSettings.repairRelayPassword — never generates new
    /// secrets, only re-copies what every synced user has). Safe on the shared
    /// free relay; cannot touch other tenants. Throws with the backend's own
    /// reason (session expired → the caller should surface re-auth).
    static func repairRelay(token: String) async throws {
        guard !token.isEmpty else { throw AgentError(message: "Sign in first.") }
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("settings/repair-relay"))
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.timeoutInterval = 12
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else {
            throw AgentError(message: "No response from Yaver while repairing the relay.")
        }
        if http.statusCode == 401 || http.statusCode == 403 {
            throw AgentError(message: "Your TV session expired — sign in again to repair the relay.")
        }
        guard (200..<300).contains(http.statusCode) else {
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String, !err.isEmpty {
                throw AgentError(message: err)
            }
            throw AgentError(message: "Relay repair failed (\(http.statusCode)).")
        }
    }

    /// POST /settings — write a last-project row to Convex. Same
    /// `defaultRuntimeProjectForDevice` (replace-by-deviceId) the phone and
    /// web dashboard write, so a project picked on the TV is remembered on the
    /// phone and vice versa. Fire-and-forget at call sites: a failed settings
    /// write must never block a vibe turn. Privacy-limited like the row itself:
    /// no absolute paths, only {projectName, gitRemote, branch}.
    static func saveRuntimeProject(token: String, pref: RuntimeProjectPref) async {
        guard let deviceId = pref.deviceId, !deviceId.isEmpty else { return }
        var body: [String: Any] = ["deviceId": deviceId]
        if let name = pref.projectName, !name.isEmpty { body["projectName"] = name }
        if let remote = pref.gitRemote, !remote.isEmpty { body["gitRemote"] = remote }
        if let branch = pref.branch, !branch.isEmpty { body["branch"] = branch }
        await postSettings(token: token,
                           key: "defaultRuntimeProjectForDevice",
                           value: body)
    }

    /// POST /settings — write the MCP selection row. Same
    /// `mcpServersForDevice` (replace-by-deviceId) mobile/web write.
    static func saveMCPServers(token: String, pref: MCPServersPref) async {
        guard let deviceId = pref.deviceId, !deviceId.isEmpty else { return }
        var body: [String: Any] = ["deviceId": deviceId]
        if let servers = pref.mcpServers, !servers.isEmpty { body["mcpServers"] = servers }
        if let includeYaverMcp = pref.includeYaverMcp { body["includeYaverMcp"] = includeYaverMcp }
        await postSettings(token: token,
                           key: "mcpServersForDevice",
                           value: body)
    }

    /// Explicit account defaults edited from the tvOS Settings surface. These
    /// throw so a settings screen can report a failed save instead of showing
    /// an optimistic value that never reached the shared mobile/web row.
    static func savePrimaryDevice(token: String, deviceId: String?) async throws {
        try await postSettingsChecked(token: token, body: ["primaryDeviceId": deviceId ?? NSNull()])
    }

    static func savePrimaryRunner(token: String, deviceId: String, runnerId: String?) async throws {
        try await savePrimaryRunnerPreference(
            token: token, deviceId: deviceId, runnerId: runnerId,
            model: nil, reasoningEffort: nil, provider: nil)
    }

    static func savePrimaryRunnerPreference(
        token: String,
        deviceId: String,
        runnerId: String?,
        model: String?,
        reasoningEffort: String?,
        provider: String?
    ) async throws {
        try await postSettingsChecked(token: token, body: [
            "primaryRunnerForDevice": [
                "deviceId": deviceId,
                "runnerId": runnerId ?? NSNull(),
                "model": model ?? NSNull(),
                "reasoningEffort": reasoningEffort ?? NSNull(),
                "mode": NSNull(),
                "provider": provider ?? NSNull(),
            ],
        ])
    }

    static func saveAppearanceTheme(token: String, surface: String, theme: String) async throws {
        try await postSettingsChecked(token: token, body: [
            "appearanceThemeForSurface": ["surface": surface, "theme": theme],
        ])
    }

    private static func postSettingsChecked(token: String, body: [String: Any]) async throws {
        guard !token.isEmpty else { throw AgentError(message: "Sign in first.") }
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("settings"))
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.timeoutInterval = 12
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, response) = try await URLSession.shared.data(for: req)
        guard let http = response as? HTTPURLResponse else {
            throw AgentError(message: "No response while saving Settings.")
        }
        guard (200..<300).contains(http.statusCode) else {
            let message = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String
            throw AgentError(message: message ?? "Couldn't save Settings (\(http.statusCode)).")
        }
    }

    /// Shared POST /settings writer. Deliberately silent on failure — a
    /// preference write must never surface as an error on a lean-back surface.
    private static func postSettings(token: String, key: String, value: [String: Any]) async {
        guard !token.isEmpty else { return }
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("settings"))
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue(Backend.surface, forHTTPHeaderField: "X-Yaver-Surface")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.timeoutInterval = 12
        do {
            req.httpBody = try JSONSerialization.data(withJSONObject: [key: value])
            _ = try await URLSession.shared.data(for: req)
        } catch {
            // Silent: rememberProject/rememberMCPServers already keep the local
            // value; Convex sync is best-effort.
        }
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

    /// Probe address candidates and return the first that answers /info within a
    /// short deadline, so selecting a machine lands on an address that actually
    /// works — mirrors mobile's raceDirectCandidates (quic.ts:5993), sequential
    /// for simplicity. Returns nil if none answer (caller can still add it and
    /// let the relay/manual path take over).
    static func firstReachable(_ candidates: [String], port: Int, token: String) async -> String? {
        for host in candidates {
            var req = URLRequest(url: URL(string: "\(agentHTTPBase(host: host, port: port))/info")!)
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
