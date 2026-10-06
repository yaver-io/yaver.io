// YaverStore.swift — app-wide session + selected-box state, persisted in
// UserDefaults. The session token is the 1-year token from device-code auth
// (same contract as the phone). Kept deliberately small: this is a lean-back
// control app, not the full account surface.

import Foundation
import SwiftUI

@MainActor
final class YaverStore: ObservableObject {
    // Global shipped-product policy. The dormant implementation remains in
    // source, but current clients must select a real user-owned machine.
    private static let boxlessUIEnabled = false
    @AppStorage("yaver.tv.token") private var legacyStoredToken: String = ""
    @AppStorage("yaver.tv.boxes") private var storedBoxesJSON: String = "[]"
    @AppStorage("yaver.tv.selectedBox") private var selectedBoxId: String = ""
    @AppStorage("yaver.tv.remoteless") private var storedRemotelessMode: Bool = false
    @AppStorage("yaver.tv.vibingResume") private var storedVibingResumeJSON: String = "[]"

    @Published var token: String = ""
    @Published var boxes: [BoxTarget] = []
    @Published var selectedBox: BoxTarget?
    @Published private(set) var appearanceTheme = "dark"
    private var appearanceSurface = "tvos"
    /// Explicitly stay in boxless mode. This is a user choice, not an
    /// inferred transport failure: Tasks can use Yaver Code while Vibing and
    /// rendering correctly remain unavailable without a render machine.
    @Published private(set) var remotelessMode = false
    /// Server-computed owner-preview bit. A cached mode never grants access.
    @Published private(set) var remotelessAllowed = false

    // Narrated auto-connect (Stream C parity with mobile). On launch, if no box
    // is picked yet, silently reach the account's best LIVE machine and connect,
    // narrating which one — instead of dropping the user on a "Choose machine"
    // wall while a connect could just happen. Cancellable. See AutoConnectStatus.
    @Published var autoConnecting: Bool = false
    @Published var autoConnectTarget: AutoConnectTarget?
    private var autoConnectStarted = false
    private var autoConnectCancelled = false
    /// UI/deep-link launch fixtures must stay process-local. Without this,
    /// refreshSelectedRelaySettings() calls addBox/select and writes a synthetic
    /// XCTest machine into normal simulator defaults for the next real launch.
    private var suppressMachinePersistence = false

    // Runner/render machine split — the account-wide favorite row from
    // userSettings.machineRolesByProject (same Convex rows web + mobile use;
    // key off the config, never a per-surface copy). Nil = single-box.
    @Published var machineRoles: MachineRegistry.MachineRolesRow?
    @Published var primaryDeviceId: String?
    @Published var workerDeviceIds: [String] = []
    @Published var showWorkerDevices = false
    @Published var opportunisticFleet = true
    /// deviceId → display name, cached from the registry fetches so the split
    /// badge can name boxes without a second network call.
    @Published var deviceNamesById: [String: String] = [:]
    @Published var deviceAliasesById: [String: String] = [:]

    // Last-project + MCP memory — the SAME Convex rows mobile
    // (taskComposerPrefs) and the web chat composer write
    // (defaultRuntimeProjectByDevice / mcpServersByDevice), so a project/MCP
    // set picked on the phone or the dashboard is remembered on the TV and
    // vice versa (2026-08-10). Rows carry no absolute path — {projectName,
    // gitRemote, branch} — matched against the box's live /projects at use.
    @Published var lastProjectByDevice: [String: MachineRegistry.RuntimeProjectPref] = [:]
    @Published var lastMCPServersByDevice: [String: MachineRegistry.MCPServersPref] = [:]
    @Published var primaryRunnerByDevice: [String: String] = [:]
    @Published var primaryModelByDevice: [String: String] = [:]
    @Published var primaryReasoningEffortByDevice: [String: String] = [:]
    @Published var primaryProviderByDevice: [String: String] = [:]

    /// Exact tvOS preview-page memory. The cross-surface settings row remembers
    /// the repository, but a monorepo can contain several runnable children.
    /// Persisting the child + form locally means selecting `web` once resumes
    /// `web` the next time instead of stopping at the monorepo chooser again.
    /// No token, address, or absolute path is stored here.
    struct VibingResumePreference: Codable, Equatable {
        let deviceId: String
        let repositoryName: String
        let targetName: String
        let form: String
    }

    func rememberedVibingTarget(
        for deviceId: String?,
        repository: ProjectSummary,
        targets: [ProjectSummary]
    ) -> (target: ProjectSummary, form: PreviewForm)? {
        guard let deviceId else { return nil }
        let rows = (try? JSONDecoder().decode(
            [VibingResumePreference].self,
            from: Data(storedVibingResumeJSON.utf8)
        )) ?? []
        guard let row = rows.last(where: {
            $0.deviceId == deviceId && $0.repositoryName == repository.name
        }), let target = targets.first(where: { $0.name == row.targetName }) else { return nil }
        return (target, PreviewForm(rawValue: row.form) ?? .phone)
    }

    func rememberVibingTarget(
        _ target: ProjectSummary,
        repository: ProjectSummary,
        form: PreviewForm,
        for deviceId: String?
    ) {
        guard !suppressMachinePersistence, let deviceId else { return }
        var rows = (try? JSONDecoder().decode(
            [VibingResumePreference].self,
            from: Data(storedVibingResumeJSON.utf8)
        )) ?? []
        rows.removeAll { $0.deviceId == deviceId && $0.repositoryName == repository.name }
        rows.append(VibingResumePreference(
            deviceId: deviceId,
            repositoryName: repository.name,
            targetName: target.name,
            form: form.rawValue
        ))
        // Bound stale rows: this is navigation memory, not an activity log.
        if rows.count > 32 { rows.removeFirst(rows.count - 32) }
        if let data = try? JSONEncoder().encode(rows),
           let value = String(data: data, encoding: .utf8) {
            storedVibingResumeJSON = value
        }
    }

    /// The remembered project for a box, matched against the box's live project
    /// list (the Convex row names the project; the path comes from /projects).
    func lastProject(for boxId: String?, projects: [ProjectSummary]) -> ProjectSummary? {
        guard let boxId, let pref = lastProjectByDevice[boxId], let name = pref.projectName else { return nil }
        return projects.first(where: { $0.name == name })
            ?? projects.first(where: { p in
                guard let prefRemote = pref.gitRemote, let pRemote = p.gitRemote else { return false }
                return pRemote == prefRemote
            })
    }

    /// Remember a picked project for a box and write it to Convex — the same
    /// row the phone/dashboard read, so the next surface sees it. Fire-and-
    /// forget: a failed settings write never blocks anything.
    func rememberProject(_ project: ProjectSummary, for boxId: String) {
        let pref = MachineRegistry.RuntimeProjectPref(
            deviceId: boxId, projectName: project.name,
            gitRemote: project.gitRemote, branch: project.branch)
        lastProjectByDevice[boxId] = pref
        guard isAuthenticated else { return }
        Task { [weak self] in
            await MachineRegistry.saveRuntimeProject(token: self?.token ?? "", pref: pref)
        }
    }

    /// Remember the external-MCP selection + the yaver doorway toggle for a
    /// box and write it to Convex (same mcpServersByDevice row mobile/web write).
    func rememberMCPServers(_ servers: [String], includeYaverMcp: Bool, for boxId: String) {
        let pref = MachineRegistry.MCPServersPref(
            deviceId: boxId, mcpServers: servers, includeYaverMcp: includeYaverMcp)
        lastMCPServersByDevice[boxId] = pref
        guard isAuthenticated else { return }
        Task { [weak self] in
            await MachineRegistry.saveMCPServers(token: self?.token ?? "", pref: pref)
        }
    }

    /// Persist the account-wide primary machine used by web, mobile and TV.
    /// Settings awaits the write so it can keep an honest saved/error state.
    func setPrimaryDevice(_ deviceId: String?) async throws {
        let previous = primaryDeviceId
        primaryDeviceId = deviceId
        do {
            try await MachineRegistry.savePrimaryDevice(token: token, deviceId: deviceId)
        } catch {
            primaryDeviceId = previous
            throw error
        }
    }

    /// Persist the selected machine's default coding runner. Runner changes
    /// clear the prior runner-specific model on the server (MachineRegistry),
    /// matching mobile and preventing an OpenCode model reaching Codex.
    func setPrimaryRunner(_ runnerId: String?, for deviceId: String) async throws {
        try await setPrimaryRunnerPreference(
            runnerId, model: nil, reasoningEffort: nil, provider: nil, for: deviceId)
    }

    /// Save one coherent runner preference. Model and reasoning belong to the
    /// selected runner and travel in the same Convex row, so another surface
    /// never observes a half-updated favorite.
    func setPrimaryRunnerPreference(
        _ runnerId: String?,
        model: String?,
        reasoningEffort: String?,
        provider: String?,
        for deviceId: String
    ) async throws {
        let canonical = runnerId.map(RegisteredRunner.canonical)
        let previousRunner = primaryRunnerByDevice[deviceId]
        let previousModel = primaryModelByDevice[deviceId]
        let previousEffort = primaryReasoningEffortByDevice[deviceId]
        let previousProvider = primaryProviderByDevice[deviceId]
        if let canonical, !canonical.isEmpty {
            primaryRunnerByDevice[deviceId] = canonical
        } else {
            primaryRunnerByDevice.removeValue(forKey: deviceId)
        }
        if let model, !model.isEmpty { primaryModelByDevice[deviceId] = model }
        else { primaryModelByDevice.removeValue(forKey: deviceId) }
        if canonical == "codex", let reasoningEffort, !reasoningEffort.isEmpty {
            primaryReasoningEffortByDevice[deviceId] = reasoningEffort
        } else {
            primaryReasoningEffortByDevice.removeValue(forKey: deviceId)
        }
        if canonical == "opencode", let provider, !provider.isEmpty {
            primaryProviderByDevice[deviceId] = provider
        } else {
            primaryProviderByDevice.removeValue(forKey: deviceId)
        }
        do {
            try await MachineRegistry.savePrimaryRunnerPreference(
                token: token, deviceId: deviceId, runnerId: canonical,
                model: model, reasoningEffort: canonical == "codex" ? reasoningEffort : nil,
                provider: canonical == "opencode" ? provider : nil)
        } catch {
            if let previousRunner { primaryRunnerByDevice[deviceId] = previousRunner }
            else { primaryRunnerByDevice.removeValue(forKey: deviceId) }
            if let previousModel { primaryModelByDevice[deviceId] = previousModel }
            else { primaryModelByDevice.removeValue(forKey: deviceId) }
            if let previousEffort { primaryReasoningEffortByDevice[deviceId] = previousEffort }
            else { primaryReasoningEffortByDevice.removeValue(forKey: deviceId) }
            if let previousProvider { primaryProviderByDevice[deviceId] = previousProvider }
            else { primaryProviderByDevice.removeValue(forKey: deviceId) }
            throw error
        }
    }

    /// Appearance is intentionally surface-scoped: changing Apple TV must not
    /// unexpectedly recolor the phone or dashboard. UserDefaults gives launch
    /// an immediate/offline value; Convex remains the signed-in authority.
    func setAppearanceTheme(_ theme: String) async throws {
        let next = theme == "light" ? "light" : "dark"
        let previous = appearanceTheme
        appearanceTheme = next
        UserDefaults.standard.set(next, forKey: "yaver.appearance.\(appearanceSurface)")
        do {
            try await MachineRegistry.saveAppearanceTheme(
                token: token, surface: appearanceSurface, theme: next)
        } catch {
            appearanceTheme = previous
            UserDefaults.standard.set(previous, forKey: "yaver.appearance.\(appearanceSurface)")
            throw error
        }
    }

    /// True when the favorite row genuinely splits work across two machines.
    var machineSplitActive: Bool {
        guard let r = machineRoles else { return false }
        guard let render = r.renderDeviceId, !render.isEmpty else { return false }
        return render != r.runnerDeviceId
    }

    /// "AI: ubuntu · Render: mac mini" — nil when no split. Render on the
    /// dashboard + runtime rows; a split with no badge is two silent sources.
    var machineRolesBadge: String? {
        guard machineSplitActive, let r = machineRoles else { return nil }
        let runner = deviceNamesById[r.runnerDeviceId] ?? String(r.runnerDeviceId.prefix(8))
        let renderId = r.renderDeviceId ?? r.runnerDeviceId
        let render = deviceNamesById[renderId] ?? String(renderId.prefix(8))
        return "AI: \(runner) · Render: \(render)"
    }

    func adoptSettings(_ settings: MachineRegistry.UserSettings?, devices: [RegisteredDevice] = []) {
        if let settings {
            primaryDeviceId = settings.primaryDeviceId
            workerDeviceIds = settings.workerDeviceIds ?? []
            showWorkerDevices = settings.showWorkerDevices == true
            opportunisticFleet = settings.opportunisticFleet ?? true
        }
        if let theme = settings?.appearanceThemeBySurface?
            .last(where: { $0.surface == appearanceSurface })?.theme,
           theme == "light" || theme == "dark" {
            appearanceTheme = theme
            UserDefaults.standard.set(theme, forKey: "yaver.appearance.\(appearanceSurface)")
        }
        if let rows = settings?.machineRolesByProject {
            machineRoles = rows.first(where: { ($0.projectName ?? "").isEmpty && !$0.runnerDeviceId.isEmpty })
        }
        if let rows = settings?.defaultRuntimeProjectByDevice {
            var next: [String: MachineRegistry.RuntimeProjectPref] = [:]
            for row in rows {
                if let id = row.deviceId, !id.isEmpty { next[id] = row }
            }
            lastProjectByDevice = next
        }
        if let rows = settings?.mcpServersByDevice {
            var next: [String: MachineRegistry.MCPServersPref] = [:]
            for row in rows {
                if let id = row.deviceId, !id.isEmpty { next[id] = row }
            }
            lastMCPServersByDevice = next
        }
        if let rows = settings?.primaryRunnerByDevice {
            var next: [String: String] = [:]
            var nextModels: [String: String] = [:]
            var nextEfforts: [String: String] = [:]
            var nextProviders: [String: String] = [:]
            for row in rows {
                guard let deviceId = row.deviceId, let runnerId = row.runnerId,
                      !deviceId.isEmpty, !runnerId.isEmpty else { continue }
                // Replace-by-device rows should be unique. Last-wins keeps a
                // malformed/partially migrated response from crashing launch.
                next[deviceId] = runnerId
                if let model = row.model, !model.isEmpty { nextModels[deviceId] = model }
                if RegisteredRunner.canonical(runnerId) == "codex",
                   let effort = row.reasoningEffort, !effort.isEmpty {
                    nextEfforts[deviceId] = effort
                }
                if RegisteredRunner.canonical(runnerId) == "opencode",
                   let provider = row.provider, !provider.isEmpty {
                    nextProviders[deviceId] = provider
                }
            }
            primaryRunnerByDevice = next
            primaryModelByDevice = nextModels
            primaryReasoningEffortByDevice = nextEfforts
            primaryProviderByDevice = nextProviders
        }
        if !devices.isEmpty {
            var names = deviceNamesById
            var aliases = deviceAliasesById
            for d in devices {
                names[d.deviceId] = d.realName
                if let alias = d.aliasLabel { aliases[d.deviceId] = alias }
                else { aliases.removeValue(forKey: d.deviceId) }
            }
            deviceNamesById = names
            deviceAliasesById = aliases
        }
    }

    /// Box addressed as the AI-task RUNNER: the machine-roles runner when a
    /// split is active, else the selected box. Cross-machine addressing rides
    /// the relay `/d/<deviceId>` path ONLY — host is cleared so a stale LAN
    /// address can never hit the wrong machine first. Returns nil (refuse by
    /// name at the call site) when a split is active but no relay is wired.
    func runnerBox() -> BoxTarget? { roleBox(machineRoles?.runnerDeviceId) }

    /// Box addressed for previews/streams/builds — the render machine.
    func renderBox() -> BoxTarget? { roleBox(machineRoles?.renderDeviceId) }

    private func roleBox(_ roleDeviceId: String?) -> BoxTarget? {
        guard let box = selectedBox else { return nil }
        guard let id = roleDeviceId, !id.isEmpty, id != box.id else { return box }
        guard let relay = box.relayBaseUrl, !relay.isEmpty else { return nil }
        return BoxTarget(id: id,
                         name: deviceNamesById[id] ?? String(id.prefix(8)),
                         alias: deviceAliasesById[id],
                         host: "", // relay-only: never let a stale LAN host win
                         port: box.port,
                         managed: box.managed,
                         machineId: nil,
                         relayBaseUrl: relay,
                         relayPassword: box.relayPassword)
    }

    /// Client for AI-task dispatch (sessions/tasks). Nil when signed out or a
    /// split is configured but unreachable — callers surface the named cause.
    func runnerClient() -> AgentClient? {
        guard isAuthenticated, !remotelessMode, let box = runnerBox() else { return nil }
        return AgentClient(token: token, box: box, relayRepair: relayRepairClosure)
    }

    /// Task/chat authority is intentionally independent from render state.
    /// Callers handling tasks must use this plan (and runnerClient), never
    /// renderClient, so a render box is optional for Git/coding work.
    func taskRuntimePlan() -> TVTaskRuntimePlan {
        TVTaskRuntimePlan.resolve(
            authenticated: isAuthenticated,
            runnerDeviceID: machineRoles?.runnerDeviceId,
            hasRunnerBox: runnerBox() != nil
        )
    }

    /// Client for preview/stream/build flows — the render box.
    func renderClient() -> AgentClient? {
        guard isAuthenticated, !remotelessMode, let box = renderBox() else { return nil }
        return AgentClient(token: token, box: box, relayRepair: relayRepairClosure)
    }

    func useRemotelessMode() {
        guard remotelessAllowed else { return }
        remotelessMode = true
        storedRemotelessMode = true
        selectedBox = nil
        selectedBoxId = ""
    }

    /// The relay-credential self-heal, injected into every client so the TV
    /// auto-repairs a stale relay password exactly like mobile/web — the
    /// "in tasks: invalid relay password" fix. The client calls it once per
    /// failing relay leg, adopts the REPAIRED box (new password in the relay
    /// endpoints), and retries — one repair per streak, never a loop.
    private var relayRepairClosure: @Sendable () async -> BoxTarget? {
        { [weak self] in await self?.repairRelay() }
    }

    /// POST /settings/repair-relay, then re-fetch /settings and adopt the
    /// corrected relay metadata onto the selected box. Returns the REPAIRED
    /// box so the client can swap its endpoints and retry; nil when repair
    /// failed or there is nothing to repair. Mirrors mobile's repairRelay.
    func repairRelay() async -> BoxTarget? {
        guard isAuthenticated, let box = selectedBox else { return nil }
        do {
            try await MachineRegistry.repairRelay(token: token)
        } catch {
            return nil
        }
        guard let settings = try? await MachineRegistry.fetchSettings(token: token) else { return nil }
        adoptSettings(settings)
        let relay = await MachineRegistry.resolvedRelay(token: token, settings: settings)
        guard relay.url?.isEmpty == false else { return nil }
        let repaired = BoxTarget(id: box.id, name: box.name, alias: box.alias,
                                 host: box.host, port: box.port,
                                 managed: box.managed, machineId: box.machineId,
                                 relayBaseUrl: relay.url, relayPassword: relay.password)
        addBox(repaired)
        select(repaired)
        return repaired
    }

    var isAuthenticated: Bool { !token.isEmpty }

    init(appearanceSurface: String = "tvos") {
        self.appearanceSurface = appearanceSurface
        let cached = UserDefaults.standard.string(forKey: "yaver.appearance.\(appearanceSurface)")
        self.appearanceTheme = cached == "light" ? "light" : "dark"
        // UI tests inject a token through NSArgumentDomain. It must win for
        // that process but must never migrate into Keychain: doing so left the
        // next normal simulator launch signed in as the synthetic test fixture.
        let argumentDomain = UserDefaults.standard.volatileDomain(forName: UserDefaults.argumentDomain)
        let hasLaunchTokenOverride = argumentDomain["yaver.tv.token"] != nil
        let launchToken = argumentDomain["yaver.tv.token"] as? String ?? ""
        suppressMachinePersistence = argumentDomain["yaver.tv.boxes"] != nil
            || argumentDomain["yaver.tv.selectedBox"] != nil
        let keychainToken = TokenStore.load()
        if hasLaunchTokenOverride {
            // An explicitly empty argument is the UI suite's signed-out
            // fixture. It must override a token left in the simulator
            // Keychain by a preceding successful-login test.
            token = launchToken
        } else if !keychainToken.isEmpty {
            token = keychainToken
            if !legacyStoredToken.isEmpty { legacyStoredToken = "" }
        } else if !legacyStoredToken.isEmpty {
            token = legacyStoredToken
            TokenStore.save(legacyStoredToken)
            legacyStoredToken = ""
        }
        let decodedBoxes = (try? JSONDecoder().decode([BoxTarget].self, from: Data(storedBoxesJSON.utf8))) ?? []
        // Remove legacy hosted-compute selections persisted by older builds.
        // A Yaver-managed row carried both fields; manually added/BYO machines
        // carry neither and remain available, including a user's own VPS.
        boxes = decodedBoxes.filter { !(($0.managed ?? false) && $0.machineId?.isEmpty == false) }
        // Fail closed until the live session refresh proves owner preview
        // access. Older builds may have persisted this choice for any account.
        remotelessMode = false
        if storedRemotelessMode { storedRemotelessMode = false }
        selectedBox = remotelessMode ? nil : (boxes.first(where: { $0.id == selectedBoxId }) ?? boxes.first)
        refreshSessionOnLaunch()
    }

    /// Netflix-on-AppleTV contract: extend the 1-year session every launch so a
    /// signed-in TV never re-prompts for OAuth. No-op when signed out. See
    /// Backend.refreshSession for the extend-only (no-rotation) rationale.
    private func refreshSessionOnLaunch() {
        let current = token
        guard !current.isEmpty else { return }
        Task { [weak self] in
            guard let refresh = await DeviceCodeAuth.refreshSession(token: current) else { return }
            await MainActor.run {
                guard let self else { return }
                // Only adopt the rotated token if we're still on the same one —
                // the user may have signed out/in while the refresh was in flight.
                guard self.token == current else { return }
                self.remotelessAllowed = Self.boxlessUIEnabled && refresh.isOwner
                if !refresh.isOwner {
                    self.remotelessMode = false
                    self.storedRemotelessMode = false
                }
                if let rotated = refresh.token, !rotated.isEmpty {
                    self.token = rotated
                    TokenStore.save(rotated)
                }
            }
        }
    }

    func signIn(token: String) {
        self.token = token
        legacyStoredToken = ""
        TokenStore.save(token)
        refreshSessionOnLaunch()
    }

    func signOut() {
        let current = token
        clearLocalSession()
        Task { await DeviceCodeAuth.revokeSession(token: current) }
    }

    /// Switching control planes clears only this device. Revoking after the
    /// origin changes could leak the old bearer to the new server.
    func signOutForServerSwitch() {
        clearLocalSession()
    }

    private func clearLocalSession() {
        token = ""
        remotelessAllowed = false
        remotelessMode = false
        storedRemotelessMode = false
        legacyStoredToken = ""
        TokenStore.clear()
        // Clear the machine list too. On a family Apple TV, leaving boxes behind
        // hands the next person the previous user's machine names and LAN IPs.
        boxes = []
        selectedBox = nil
        selectedBoxId = ""
        storedBoxesJSON = "[]"
        appearanceTheme = "dark"
        UserDefaults.standard.removeObject(forKey: "yaver.appearance.\(appearanceSurface)")
    }

    /// Clear a persisted session only when the server has proved it is no
    /// longer usable. A non-empty Keychain token is not proof of liveness: a
    /// reinstall can restore a revoked one-year token and strand native
    /// surfaces on their machine picker. Keep transport failures recoverable,
    /// but make definitive auth failures return to Sign in on every target
    /// that shares YaverStore (tvOS and visionOS today).
    @discardableResult
    func handleAuthenticationFailure(_ error: Error) -> Bool {
        let normalized = error.localizedDescription.lowercased()
        let definitive = normalized.contains("session expired")
            || normalized.contains("sign in first")
            || normalized.contains("(401)")
            || normalized.contains("(403)")
            || normalized.contains("missing or invalid authorization")
            || normalized.contains("invalid token")
        guard definitive else { return false }
        signOut()
        return true
    }

    /// Remove a box (a typo'd address, a decommissioned machine). Without this a
    /// bad entry was permanent — the dashboard could only ever ADD.
    func removeBox(_ box: BoxTarget) {
        boxes.removeAll { $0.id == box.id }
        if selectedBox?.id == box.id {
            selectedBox = boxes.first
            selectedBoxId = boxes.first?.id ?? ""
        }
        persistBoxes()
    }

    func addBox(_ box: BoxTarget) {
        if let idx = boxes.firstIndex(where: { $0.id == box.id }) {
            boxes[idx] = box
        } else {
            boxes.append(box)
        }
        persistBoxes()
        if selectedBox == nil { select(box) }
    }

    func select(_ box: BoxTarget) {
        remotelessMode = false
        storedRemotelessMode = false
        selectedBox = box
        if !suppressMachinePersistence { selectedBoxId = box.id }
    }

    // MARK: - Connectivity self-heal (tvOS analog of mobile's relay self-heal)

    /// tvOS connects DIRECT to a box's host — there's no platform relay or
    /// per-user relay password here, so mobile's `/settings/repair-relay` heal
    /// doesn't apply. The equivalent staleness on this surface is a CACHED host
    /// that's no longer reachable (the box changed IP or moved networks). When a
    /// call fails, re-resolve the selected box's best reachable address from the
    /// registry and swap it in, so the next call succeeds without the user
    /// re-picking a machine. Idempotent; no-op when signed out, no box selected,
    /// the box is gone, or nothing better resolves.
    func healReachability() async {
        guard isAuthenticated, let box = selectedBox else { return }
        let list = (try? await MachineRegistry.fetch(token: token)) ?? []
        let settings = try? await MachineRegistry.fetchSettings(token: token)
        adoptSettings(settings, devices: list)
        guard let dev = list.first(where: { $0.deviceId == box.id }) else { return }
        let host = await MachineRegistry.firstReachable(dev.addressCandidates, port: dev.port, token: token)
        guard let host, !host.isEmpty, host != box.host else { return }
        let healed = BoxTarget(id: dev.deviceId, name: dev.realName, alias: dev.alias,
                               host: host,
                               port: dev.port, managed: dev.managed, machineId: dev.machineId,
                               relayBaseUrl: settings?.relayUrl, relayPassword: settings?.relayPassword)
        addBox(healed)
        select(healed)
    }

    /// Backfill relay metadata onto an already-selected/stored box. Older tvOS
    /// builds persisted only the LAN host, so after upgrade the user could still
    /// be stuck LAN-only until they re-picked the machine. This is idempotent and
    /// only changes transport metadata, not the selected device.
    func refreshSelectedRelaySettings() async {
        guard isAuthenticated, let box = selectedBox else { return }
        guard box.relayBaseUrl?.isEmpty != false || box.relayPassword?.isEmpty != false else { return }
        guard let settings = try? await MachineRegistry.fetchSettings(token: token) else { return }
        adoptSettings(settings)
        // settings.relayUrl is only a user override — merge the authoritative
        // GET /config relay list (2026-08-13) so a remote box gets a relay leg
        // even for accounts that never set a relayUrl override.
        let resolved = await MachineRegistry.resolvedRelay(token: token, settings: settings)
        guard let url = resolved.url, !url.isEmpty else { return }
        let updated = BoxTarget(id: box.id, name: box.name, alias: box.alias,
                                host: box.host, port: box.port,
                                managed: box.managed, machineId: box.machineId,
                                relayBaseUrl: url, relayPassword: resolved.password)
        addBox(updated)
        select(updated)
    }

    /// Appearance sync cannot be coupled to relay repair: a healthy relay (or
    /// a boxless session) would make that transport-specific method return
    /// before fetching settings and leave this surface on a stale local color.
    func refreshAppearanceSettings() async {
        guard isAuthenticated,
              let settings = try? await MachineRegistry.fetchSettings(token: token) else { return }
        adoptSettings(settings)
    }

    // MARK: - Narrated auto-connect (Stream C)

    /// Kick the launch reconciliation once. An explicit account primary wins
    /// over stale local selection, matching web/mobile. With no primary, the
    /// last locally selected box remains sticky.
    ///
    /// `autoConnectStarted` is set here but cleared again by `cancelAutoConnect`
    /// and by every early return in `runAutoConnect` — see the note there. It
    /// marks "a sweep COMPLETED", not "a sweep was attempted".
    func autoConnectOnLaunch() {
        // `!autoConnecting` is the in-flight guard. It used to be implicit in
        // `autoConnectStarted` (which was never cleared); now that the flag
        // re-arms on failure, concurrent `.onAppear` calls need their own guard
        // or a re-arm could start a second overlapping sweep.
        guard isAuthenticated, !suppressMachinePersistence,
              !autoConnectStarted, !autoConnecting else { return }
        autoConnectStarted = true
        autoConnectCancelled = false
        Task { await runAutoConnect() }
    }

    /// User bailed out of the sweep to pick a machine themselves.
    ///
    /// Re-arms the launch sweep. Previously this left `autoConnectStarted` true,
    /// so bailing out and then dismissing the picker WITHOUT choosing a machine
    /// left the surface permanently unconnected: `selectedBox` was still nil, so
    /// `.onAppear` kept firing, but the flag made every call a no-op until the
    /// app was relaunched. Same class of bug as the mobile auto-connect sweep
    /// (see mobile/src/context/DeviceContext.tsx) — a retry token burned on
    /// entry can never distinguish "already succeeded" from "was interrupted".
    func cancelAutoConnect() {
        autoConnectCancelled = true
        autoConnecting = false
        autoConnectTarget = nil
        autoConnectStarted = false
    }

    /// Fetch owned machines and reconcile the selected box with the explicit
    /// account primary even when its heartbeat is stale. Reachability belongs
    /// on the dashboard (direct → relay → repair), not in front of it as a
    /// "Choose machine" wall.
    private func runAutoConnect() async {
        autoConnecting = selectedBox == nil
        // Re-arm unless we actually selected a box. A sweep that was cancelled,
        // found nothing live, or failed to resolve an address must leave the
        // launch trigger available — otherwise the surface sits disconnected
        // with no way back short of a relaunch. `select(_:)` sets `selectedBox`,
        // which is the honest "we're done" signal.
        defer {
            autoConnecting = false
            autoConnectTarget = nil
            if selectedBox == nil { autoConnectStarted = false }
        }
        let list = (try? await MachineRegistry.fetch(token: token)) ?? []
        let settings = try? await MachineRegistry.fetchSettings(token: token)
        adoptSettings(settings, devices: list)
        if autoConnectCancelled { return }
        let nowMs = Date().timeIntervalSince1970 * 1000
        let owned = list
        let primary = settings?.primaryDeviceId.flatMap { id in
            owned.first(where: { $0.deviceId == id }).map { ($0, AutoConnectRole.primary) }
        }
        let remembered = selectedBox.flatMap { selected in
            owned.first(where: { $0.deviceId == selected.id }).map { ($0, AutoConnectRole.machine) }
        }
        let fallback = selectedBox == nil
            ? rankedAutoConnectTargets(devices: owned, settings: settings, nowMs: nowMs).first
            : nil
        guard let (target, role) = primary ?? remembered ?? fallback else { return }
        // Narrate BEFORE probing so the surface shows which box we're reaching for.
        autoConnectTarget = AutoConnectTarget(name: target.displayName, role: role)
        if autoConnectCancelled { return }
        async let relayLookup = MachineRegistry.resolvedRelay(token: token, settings: settings)
        async let reachableHost = MachineRegistry.firstReachable(
            target.addressCandidates, port: target.port, token: token)
        let relay = await relayLookup
        let host = await reachableHost
            ?? target.addressCandidates.first
            ?? target.quicHost
        if autoConnectCancelled { return }
        // A remote runner connected to the free relay legitimately publishes
        // no LAN/quicHost address. Relay reachability alone is sufficient;
        // requiring a non-empty direct host is what stranded the primary
        // Ubuntu runner on "No box selected" despite a live relay session.
        let directHost = host ?? ""
        guard !directHost.isEmpty || relay.url?.isEmpty == false else { return }
        let box = BoxTarget(id: target.deviceId, name: target.realName, alias: target.alias,
                            host: directHost,
                            port: target.port, managed: target.managed, machineId: target.machineId,
                            relayBaseUrl: relay.url, relayPassword: relay.password)
        addBox(box)
        select(box)
    }

    func client() -> AgentClient? {
        guard isAuthenticated, let box = selectedBox else { return nil }
        return AgentClient(token: token, box: box, relayRepair: relayRepairClosure)
    }

    private func persistBoxes() {
        guard !suppressMachinePersistence else { return }
        if let data = try? JSONEncoder().encode(boxes), let s = String(data: data, encoding: .utf8) {
            storedBoxesJSON = s
        }
    }
}

// MARK: - Auto-connect narration (mirrors mobile/src/lib/autoConnectStatus.ts)

enum AutoConnectRole: Equatable {
    case primary
    case secondary
    /// No explicit primary/secondary preference exists yet.
    case machine
}

/// Primary/secondary are user intent, not health probes. Preserve that order
/// even when presence is stale; the selected box's direct+relay probe owns the
/// connectivity truth and can show Wake/repair without removing the dashboard.
func rankedAutoConnectTargets(
    devices: [RegisteredDevice],
    settings: MachineRegistry.UserSettings?,
    nowMs: Double
) -> [(RegisteredDevice, AutoConnectRole)] {
    let owned = devices
    let byId = Dictionary(uniqueKeysWithValues: owned.map { ($0.deviceId, $0) })
    var preferred: [(RegisteredDevice, AutoConnectRole)] = []
    for (id, role) in [
        (settings?.primaryDeviceId, AutoConnectRole.primary),
        (settings?.secondaryDeviceId, AutoConnectRole.secondary),
    ] {
        guard let id, let device = byId[id], !preferred.contains(where: { $0.0.deviceId == id }) else { continue }
        preferred.append((device, role))
    }

    func isLive(_ device: RegisteredDevice) -> Bool {
        guard device.isOnline == true else { return false }
        guard let heartbeat = device.lastHeartbeat else { return true }
        return (nowMs - heartbeat) < RegisteredDevice.heartbeatStaleMs
    }

    if !preferred.isEmpty { return preferred }
    return owned
        .sorted {
            let (lhsLive, rhsLive) = (isLive($0), isLive($1))
            if lhsLive != rhsLive { return lhsLive }
            return $0.displayName.localizedCaseInsensitiveCompare($1.displayName) == .orderedAscending
        }
        .map { ($0, .machine) }
}

struct AutoConnectTarget: Equatable {
    let name: String
    let role: AutoConnectRole
}

enum AutoConnectStatus {
    static func roleWord(_ r: AutoConnectRole) -> String {
        switch r {
        case .primary: return "Primary"
        case .secondary: return "Secondary"
        case .machine: return "Your machine"
        }
    }

    /// Full sentence for the large lean-back surface. Matches autoConnectSentence.
    static func sentence(_ t: AutoConnectTarget?) -> String {
        guard let t else { return "Reaching your machines…" }
        switch t.role {
        case .machine: return "Connecting to \(t.name)…"
        case .primary, .secondary: return "\(roleWord(t.role)) (\(t.name)) is online — connecting…"
        }
    }
}
