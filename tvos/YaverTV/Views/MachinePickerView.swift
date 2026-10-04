// MachinePickerView.swift — pick a machine from the account, not by typing an IP.
//
// This is the fix for the empty "No box selected → Add box" state: the TV now
// lists the machines the account already has (GET /devices/list) with liveness,
// and one tap resolves a reachable address and selects it. Typing a LAN IP by
// hand (AddBoxView) stays as the fallback for an off-account / LAN-only box.
//
// Yaver-hosted Cloud Workspace rows are filtered at the registry boundary.
// User-owned Macs, PCs and VPS nodes remain available here.

import SwiftUI

struct MachinePickerView: View {
    @EnvironmentObject var store: YaverStore
    @Environment(\.dismiss) private var dismiss

    @State private var devices: [RegisteredDevice] = []
    @State private var loading = true
    @State private var error: String?
    @State private var connecting: String?   // deviceId being resolved
    @State private var removing: String?     // deviceId being removed
    @State private var removalCandidate: RegisteredDevice?
    @StateObject private var lifecycle = BoxLifecycle()
    @State private var relaySettings: MachineRegistry.UserSettings?
    /// Resolved relay leg (2026-08-13): settings.relayUrl is a user OVERRIDE,
    /// usually empty — the authoritative relay list is GET /config. Without
    /// this a remote box (Hetzner etc.) had no relay leg at all on the TV.
    @State private var resolvedRelayUrl: String?
    @State private var resolvedRelayPassword: String?
    @FocusState private var focusedDeviceID: String?

    // Captured once per load so liveness is a pure comparison (no Date.now in the model).
    @State private var nowMs: Double = 0

    var body: some View {
        NavigationStack {
            Group {
                if loading {
                    VStack(spacing: 16) {
                        ProgressView().scaleEffect(1.5)
                        Text("Loading your machines…").foregroundStyle(.secondary)
                    }
                } else if let error {
                    errorView(error)
                } else if devices.isEmpty {
                    emptyView
                } else {
                    list
                }
            }
            .navigationTitle("Your machines")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Back") { dismiss() }
                }
                ToolbarItem(placement: .primaryAction) {
                    NavigationLink("Type an address", destination: AddBoxView())
                }
                if store.primaryDeviceId != nil && devices.contains(where: { $0.deviceId != store.primaryDeviceId }) {
                    ToolbarItem(placement: .primaryAction) {
                        Button(store.showWorkerDevices
                               ? "Hide workers · \(devices.filter { $0.deviceId != store.primaryDeviceId && isLive($0) }.count) ready"
                               : "Show workers · \(devices.filter { $0.deviceId != store.primaryDeviceId && isLive($0) }.count)/\(devices.filter { $0.deviceId != store.primaryDeviceId }.count) ready") {
                            let next = !store.showWorkerDevices
                            store.showWorkerDevices = next
                            Task {
                                guard !store.token.isEmpty else { return }
                                try? await MachineRegistry.saveShowWorkerDevices(token: store.token, enabled: next)
                            }
                        }
                    }
                }
            }
        }
        .task { await load() }
        .onChange(of: devices.map(\.deviceId)) { _, ids in
            guard focusedDeviceID == nil, let first = sortedDevices.first else { return }
            DispatchQueue.main.async { focusedDeviceID = first.deviceId }
        }
        .alert(item: $removalCandidate) { device in
            Alert(
                title: Text("Remove \(device.displayName)?"),
                message: Text("It disappears from every Yaver surface immediately. If it is repaired or reset, pairing Yaver again recreates it."),
                primaryButton: .destructive(Text("Remove")) {
                    Task { await remove(device) }
                },
                secondaryButton: .cancel()
            )
        }
    }

    private var list: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) {
                pickerHero
                // Machines wrap into focusable rows instead of leaking a half
                // card past the TV's trailing safe area. The old nested
                // horizontal carousel showed the first five-and-a-sliver of
                // six devices; that looked like layout overflow and hid the
                // remaining status badge. The outer ScrollView already owns
                // vertical movement, so a four-column grid stays remote-native.
                LazyVGrid(
                    columns: Array(
                        repeating: GridItem(.flexible(minimum: 250), spacing: 22, alignment: .top),
                        count: 4
                    ),
                    alignment: .leading,
                    spacing: 22
                ) {
                    ForEach(sortedDevices) { d in
                        Button {
                            Task { await connect(d) }
                        } label: {
                            MachineRow(device: d, nowMs: nowMs,
                                       connecting: connecting == d.deviceId,
                                       selected: store.selectedBox?.id == d.deviceId,
                                       primary: store.primaryDeviceId == d.deviceId)
                        }
                        .buttonStyle(.card)
                        .focused($focusedDeviceID, equals: d.deviceId)
                        .disabled(connecting != nil || removing != nil)
                        .contextMenu {
                            Button(role: .destructive) {
                                removalCandidate = d
                            } label: {
                                Label("Remove from Yaver", systemImage: "trash")
                            }
                        }
                        .accessibilityIdentifier("devices.machine.\(d.deviceId)")
                    }
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 18)

                remotelessOption
            }
            .padding(32)
        }
    }

    private var pickerHero: some View {
        HStack(spacing: 22) {
            Image(systemName: store.selectedBox == nil ? "antenna.radiowaves.left.and.right" : "checkmark.circle.fill")
                .font(.system(size: 34, weight: .semibold))
                .foregroundStyle(store.selectedBox == nil ? .orange : .green)
                .frame(width: 58, height: 58)
                .background((store.selectedBox == nil ? Color.orange : Color.green).opacity(0.16),
                            in: RoundedRectangle(cornerRadius: 12))
            VStack(alignment: .leading, spacing: 5) {
                Text(store.selectedBox == nil ? "Choose where the TV connects" : "Connected to \(store.selectedBox?.name ?? "a machine")")
                    .font(.system(size: 28, weight: .bold))
                Text("Your machines connect directly when possible and use your relay as fallback.")
                    .font(.system(size: 17))
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer()
            Button {
                Task { await load() }
            } label: {
                Label("Refresh", systemImage: "arrow.clockwise")
                    .font(.system(size: 18, weight: .semibold))
                    .padding(.horizontal, 20).padding(.vertical, 10)
            }
            .buttonStyle(.bordered)
            .disabled(loading || connecting != nil)
        }
        .padding(26)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
    }

    // Reachable + fresh first; parked/managed next; stale/offline last.
    private var sortedDevices: [RegisteredDevice] {
        let visible = store.primaryDeviceId != nil && !store.showWorkerDevices
            ? devices.filter { $0.deviceId == store.primaryDeviceId }
            : devices
        return visible.sorted { a, b in
            let (la, lb) = (isLive(a), isLive(b))
            if la != lb { return la }
            return a.displayName.localizedCaseInsensitiveCompare(b.displayName) == .orderedAscending
        }
    }

    private func isLive(_ d: RegisteredDevice) -> Bool {
        guard d.isOnline == true else { return false }
        guard let hb = d.lastHeartbeat, nowMs > 0 else { return d.isOnline == true }
        return (nowMs - hb) < RegisteredDevice.heartbeatStaleMs
    }

    private var emptyView: some View {
        VStack(spacing: 16) {
            Image(systemName: "server.rack").font(.system(size: 56)).foregroundStyle(.secondary)
            Text("No machines on your account yet").font(.title2)
            Text("Run `yaver serve` on a computer signed in as you, and it appears here. Or type a LAN address.")
                .foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 640)
            NavigationLink("Type an address", destination: AddBoxView()).padding(.top, 8)
            if store.remotelessAllowed { remotelessOption }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var remotelessOption: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("No machine right now?")
                .font(.system(size: 18, weight: .semibold))
            Text("Remoteless fallback supports DeepSeek analysis/chat only on this TV. Git edits, shell commands, builds, tests, deploys, Vibing, and live previews still need your primary/secondary machine.")
                .font(.system(size: 15))
                .foregroundStyle(.secondary)
                .lineLimit(3)
            Button {
                store.useRemotelessMode()
                dismiss()
            } label: {
                Label("Use analysis fallback", systemImage: "arrow.forward.circle")
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("devices.continue-remoteless")
        }
        .padding(20)
        .frame(maxWidth: 620, alignment: .leading)
        .background(Color.white.opacity(0.05), in: RoundedRectangle(cornerRadius: 14))
    }

    private func errorView(_ msg: String) -> some View {
        VStack(spacing: 16) {
            Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 48)).foregroundStyle(.orange)
            Text(msg).multilineTextAlignment(.center).frame(maxWidth: 640)
            Button("Try again") { Task { await load() } }
            NavigationLink("Type an address", destination: AddBoxView())
            if store.remotelessAllowed { remotelessOption }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Actions

    private func load() async {
        loading = true
        error = nil
        do {
            let list = try await MachineRegistry.fetch(token: store.token)
            relaySettings = try? await MachineRegistry.fetchSettings(token: store.token)
            store.adoptSettings(relaySettings, devices: list)
            // Merge /config relay servers into the picker's relay settings so a
            // box added here gets a working relay leg even when the user never
            // set a relayUrl override (the Hetzner/remote-box case).
            if let settings = relaySettings {
                let resolved = await MachineRegistry.resolvedRelay(token: store.token, settings: settings)
                resolvedRelayUrl = resolved.url
                resolvedRelayPassword = resolved.password
            }
            nowMs = Date().timeIntervalSince1970 * 1000
            // The owner-only registry contract already excludes shared rows.
            devices = list
        } catch {
            let message = error.localizedDescription
            // A reinstall can preserve a revoked Keychain token. The shared
            // store owns the auth-failure contract so visionOS cannot drift.
            if store.handleAuthenticationFailure(error) {
                return
            }
            self.error = message
        }
        loading = false
    }

    private func connect(_ d: RegisteredDevice) async {
        connecting = d.deviceId
        defer { connecting = nil }

        if d.needsAuth == true || d.controlPlaneStatus?.state == "needs-auth" {
            error = "(d.displayName) is running but signed out. Approve its sign-in from your phone or run `yaver auth` on the machine."
            return
        }

        // A parked managed box has no live address — wake it, don't try to reach it.
        if d.wakeable, d.isOnline != true {
            let box = boxTarget(for: d, host: d.quicHost ?? "")
            store.addBox(box)
            store.select(box)
            lifecycle.wake(box, token: store.token)
            dismiss()   // dashboard shows the wake ladder
            return
        }

        // Find an address that actually answers; fall back to the first candidate
        // so an added box is never address-less (relay/manual can still take over).
        let candidates = d.addressCandidates
        let host = await MachineRegistry.firstReachable(candidates, port: d.port, token: store.token)
            ?? candidates.first
            ?? d.quicHost
        // Relay-only machines intentionally have no direct address. They are
        // still selectable when the account has a resolved relay endpoint.
        let directHost = host ?? ""
        guard !directHost.isEmpty || resolvedRelayUrl?.isEmpty == false else {
            error = "\(d.displayName) has no reachable address. Type one manually."
            return
        }
        let box = boxTarget(for: d, host: directHost)
        store.addBox(box)
        store.select(box)
        dismiss()
    }

    private func remove(_ device: RegisteredDevice) async {
        removing = device.deviceId
        error = nil
        defer { removing = nil }
        do {
            // Companion tokens may unregister the account row, but must never
            // invoke the destructive local-machine removal route. Hosted rows
            // never reach this Apple surface.
            try await MachineRegistry.removeDevice(deviceId: device.deviceId, token: store.token)
            devices.removeAll { $0.deviceId == device.deviceId }
            if let cached = store.boxes.first(where: { $0.id == device.deviceId }) {
                store.removeBox(cached)
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func boxTarget(for d: RegisteredDevice, host: String) -> BoxTarget {
        BoxTarget(id: d.deviceId, name: d.realName, alias: d.alias,
                  host: host, port: d.port,
                  managed: d.managed, machineId: d.machineId,
                  relayBaseUrl: resolvedRelayUrl ?? relaySettings?.relayUrl,
                  relayPassword: resolvedRelayPassword ?? relaySettings?.relayPassword)
    }
}

private struct MachineRow: View {
    let device: RegisteredDevice
    let nowMs: Double
    let connecting: Bool
    let selected: Bool
    let primary: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 14) {
                Image(systemName: platformIcon)
                    .font(.system(size: 32))
                    .frame(width: 48, height: 48)
                    .foregroundStyle(selected ? .green : .primary)
                Spacer(minLength: 12)
                if connecting {
                    ProgressView()
                } else {
                    statusBadge
                }
            }

            VStack(alignment: .leading, spacing: 6) {
                Text(device.realName)
                    .font(.system(size: 25, weight: .semibold))
                    .lineLimit(1)
                    .truncationMode(.tail)
                if let alias = device.aliasLabel {
                    Text(alias)
                        .font(.system(size: 18, weight: .medium))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
                Text(subtitle)
                    .font(.system(size: 16))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(24)
        .frame(maxWidth: .infinity, minHeight: 210, maxHeight: 210, alignment: .leading)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 20))
    }

    private var platformIcon: String {
        switch device.platform?.lowercased() {
        case "macos", "darwin": return "desktopcomputer"
        case "linux": return "server.rack"
        case "windows": return "pc"
        default: return "cpu"
        }
    }

    private var subtitle: String {
        var parts: [String] = []
        if let p = device.platform { parts.append(p) }
        if let v = device.agentVersion { parts.append(v) }
        if device.wakeable { parts.append("managed") }
        return parts.joined(separator: " · ")
    }

    private var fresh: Bool {
        guard device.isOnline == true else { return false }
        guard let hb = device.lastHeartbeat, nowMs > 0 else { return true }
        return (nowMs - hb) < RegisteredDevice.heartbeatStaleMs
    }

    @ViewBuilder private var statusBadge: some View {
        if selected {
            badge("Selected", .blue)
        } else if primary {
            badge("Primary", .blue)
        } else if device.needsAuth == true || device.controlPlaneStatus?.state == "needs-auth" {
            badge("Sign in", .orange)
        } else if device.wakeable && device.isOnline != true {
            badge("Wake", .orange)
        } else if fresh && (device.controlPlaneStatus?.relayPath == "unavailable" || device.relayConnected == false) {
            badge("LAN only", .orange)
        } else if fresh {
            badge(device.relayConnected == false ? "LAN-only" : "Online", .green)
        } else if device.isOnline == true {
            badge("Stale", .yellow)
        } else {
            badge("Offline", .gray)
        }
    }

    private func badge(_ text: String, _ color: Color) -> some View {
        Text(text)
            .font(.system(size: 16, weight: .semibold))
            .padding(.horizontal, 16).padding(.vertical, 8)
            .background(color.opacity(0.2), in: Capsule())
            .foregroundStyle(color)
    }
}
