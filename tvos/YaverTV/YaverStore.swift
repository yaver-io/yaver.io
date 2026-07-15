// YaverStore.swift — app-wide session + selected-box state, persisted in
// UserDefaults. The session token is the 1-year token from device-code auth
// (same contract as the phone). Kept deliberately small: this is a lean-back
// control app, not the full account surface.

import Foundation
import SwiftUI

@MainActor
final class YaverStore: ObservableObject {
    @AppStorage("yaver.tv.token") private var storedToken: String = ""
    @AppStorage("yaver.tv.boxes") private var storedBoxesJSON: String = "[]"
    @AppStorage("yaver.tv.selectedBox") private var selectedBoxId: String = ""

    @Published var token: String = ""
    @Published var boxes: [BoxTarget] = []
    @Published var selectedBox: BoxTarget?

    // Narrated auto-connect (Stream C parity with mobile). On launch, if no box
    // is picked yet, silently reach the account's best LIVE machine and connect,
    // narrating which one — instead of dropping the user on a "Choose machine"
    // wall while a connect could just happen. Cancellable. See AutoConnectStatus.
    @Published var autoConnecting: Bool = false
    @Published var autoConnectTarget: AutoConnectTarget?
    private var autoConnectStarted = false
    private var autoConnectCancelled = false

    var isAuthenticated: Bool { !token.isEmpty }

    init() {
        token = storedToken
        boxes = (try? JSONDecoder().decode([BoxTarget].self, from: Data(storedBoxesJSON.utf8))) ?? []
        selectedBox = boxes.first(where: { $0.id == selectedBoxId }) ?? boxes.first
        refreshSessionOnLaunch()
    }

    /// Netflix-on-AppleTV contract: extend the 1-year session every launch so a
    /// signed-in TV never re-prompts for OAuth. No-op when signed out. See
    /// Backend.refreshSession for the extend-only (no-rotation) rationale.
    private func refreshSessionOnLaunch() {
        let current = storedToken
        guard !current.isEmpty else { return }
        Task { [weak self] in
            let rotated = await DeviceCodeAuth.refreshSession(token: current)
            guard let rotated, !rotated.isEmpty else { return }
            await MainActor.run {
                guard let self else { return }
                // Only adopt the rotated token if we're still on the same one —
                // the user may have signed out/in while the refresh was in flight.
                guard self.token == current else { return }
                self.token = rotated
                self.storedToken = rotated
            }
        }
    }

    func signIn(token: String) {
        self.token = token
        storedToken = token
    }

    func signOut() {
        token = ""
        storedToken = ""
        // Clear the machine list too. On a family Apple TV, leaving boxes behind
        // hands the next person the previous user's machine names and LAN IPs.
        boxes = []
        selectedBox = nil
        selectedBoxId = ""
        storedBoxesJSON = "[]"
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
        selectedBox = box
        selectedBoxId = box.id
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
        guard let dev = list.first(where: { $0.deviceId == box.id }) else { return }
        let host = await MachineRegistry.firstReachable(dev.addressCandidates, port: dev.port, token: token)
        guard let host, !host.isEmpty, host != box.host else { return }
        let healed = BoxTarget(id: dev.deviceId, name: dev.displayName, host: host,
                               port: dev.port, managed: dev.managed, machineId: dev.machineId)
        addBox(healed)
        select(healed)
    }

    // MARK: - Narrated auto-connect (Stream C)

    /// Kick the launch auto-connect once. No-op if signed out, a box is already
    /// picked (a sticky choice always wins), or it already ran this launch.
    func autoConnectOnLaunch() {
        guard isAuthenticated, selectedBox == nil, !autoConnectStarted else { return }
        autoConnectStarted = true
        autoConnectCancelled = false
        Task { await runAutoConnect() }
    }

    /// User bailed out of the sweep to pick a machine themselves.
    func cancelAutoConnect() {
        autoConnectCancelled = true
        autoConnecting = false
        autoConnectTarget = nil
    }

    /// Fetch the account's machines, pick the best LIVE one (live-first, then by
    /// name — same rule as MachinePickerView), resolve a reachable address, and
    /// select it. Narrates the target before probing. If nothing is live, quietly
    /// yield to the picker prompt (NOT an error — the boxes may just be asleep).
    private func runAutoConnect() async {
        autoConnecting = true
        defer {
            autoConnecting = false
            autoConnectTarget = nil
        }
        let list = (try? await MachineRegistry.fetch(token: token)) ?? []
        if autoConnectCancelled { return }
        let nowMs = Date().timeIntervalSince1970 * 1000
        func isLive(_ d: RegisteredDevice) -> Bool {
            guard d.isOnline == true else { return false }
            guard let hb = d.lastHeartbeat else { return true }
            return (nowMs - hb) < RegisteredDevice.heartbeatStaleMs
        }
        let target = list
            .filter(isLive)
            .sorted { $0.displayName.localizedCaseInsensitiveCompare($1.displayName) == .orderedAscending }
            .first
        guard let target else { return }
        // Narrate BEFORE probing so the surface shows which box we're reaching for.
        autoConnectTarget = AutoConnectTarget(name: target.displayName, role: .machine)
        if autoConnectCancelled { return }
        let host = await MachineRegistry.firstReachable(target.addressCandidates, port: target.port, token: token)
            ?? target.addressCandidates.first
            ?? target.quicHost
        if autoConnectCancelled { return }
        guard let host, !host.isEmpty else { return }
        let box = BoxTarget(id: target.deviceId, name: target.displayName, host: host,
                            port: target.port, managed: target.managed, machineId: target.machineId)
        addBox(box)
        select(box)
    }

    func client() -> AgentClient? {
        guard isAuthenticated, let box = selectedBox else { return nil }
        return AgentClient(token: token, box: box)
    }

    private func persistBoxes() {
        if let data = try? JSONEncoder().encode(boxes), let s = String(data: data, encoding: .utf8) {
            storedBoxesJSON = s
        }
    }
}

// MARK: - Auto-connect narration (mirrors mobile/src/lib/autoConnectStatus.ts)

enum AutoConnectRole {
    case primary
    case secondary
    /// We know the machine but not its primary/secondary role — tvOS doesn't yet
    /// fetch userSettings, so narrate by name only. Honest, not a false "Primary".
    /// (Fetching primaryDeviceId to upgrade this to full role narration is a
    /// small follow-up: GET /settings, same as web/mobile.)
    case machine
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
