// MachinePickerView.swift — pick a machine from the account, not by typing an IP.
//
// This is the fix for the empty "No box selected → Add box" state: the TV now
// lists the machines the account already has (GET /devices/list) with liveness,
// and one tap resolves a reachable address and selects it. Typing a LAN IP by
// hand (AddBoxView) stays as the fallback for an off-account / LAN-only box.
//
// Managed, parked boxes appear too, with Wake — a scale-to-zero machine should
// be reachable from the sofa without walking to a computer.

import SwiftUI

struct MachinePickerView: View {
    @EnvironmentObject var store: YaverStore
    @Environment(\.dismiss) private var dismiss

    @State private var devices: [RegisteredDevice] = []
    @State private var loading = true
    @State private var error: String?
    @State private var connecting: String?   // deviceId being resolved
    @State private var showManualAdd = false
    @State private var showAcceptCode = false
    @State private var leaveTarget: RegisteredDevice?   // non-nil ⇒ confirming
    @StateObject private var lifecycle = BoxLifecycle()

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
                    Button("Type an address") { showManualAdd = true }
                }
                ToolbarItem(placement: .primaryAction) {
                    Button("Accept an invitation") { showAcceptCode = true }
                }
            }
            .sheet(isPresented: $showManualAdd, onDismiss: {
                // AddBoxView selects the box it adds; if it did, we're done.
                if store.selectedBox != nil { dismiss() }
            }) { AddBoxView() }
            .sheet(isPresented: $showAcceptCode) {
                AcceptInviteView(token: store.token) { Task { await load() } }
            }
            // Leaving is keyed on the HOST, not the row — say so before firing,
            // and say that it's undoable, because a TV remote makes a misclick
            // cheap and a re-invite is the only way back.
            .confirmationDialog("Remove your access?",
                                isPresented: Binding(get: { leaveTarget != nil },
                                                     set: { if !$0 { leaveTarget = nil } }),
                                titleVisibility: .visible) {
                Button("Remove my access", role: .destructive) {
                    if let d = leaveTarget { Task { await leave(d) } }
                }
                Button("Cancel", role: .cancel) { leaveTarget = nil }
            } message: {
                if let d = leaveTarget {
                    Text("This removes every machine \(d.hostLabel) shared with you — not just \(d.displayName). Nothing on the machine is deleted, and it's reversible: \(d.hostLabel) can share again and you can accept a new invitation.")
                }
            }
        }
        .task { await load() }
    }

    private var list: some View {
        ScrollView {
            LazyVStack(spacing: 14) {
                ForEach(sortedDevices) { d in
                    Button {
                        Task { await connect(d) }
                    } label: {
                        MachineRow(device: d, nowMs: nowMs,
                                   connecting: connecting == d.deviceId,
                                   selected: store.selectedBox?.id == d.deviceId)
                    }
                    .buttonStyle(.card)
                    .disabled(connecting != nil)
                    // tvOS has no swipe and no room for a second button in the
                    // card — long-press (the platform's own secondary gesture)
                    // is where a destructive per-row action belongs.
                    .contextMenu {
                        if d.shared {
                            Button("Remove my access", role: .destructive) { leaveTarget = d }
                        }
                    }
                }
            }
            .padding(32)
        }
    }

    // Reachable + fresh first; parked/managed next; stale/offline last.
    private var sortedDevices: [RegisteredDevice] {
        devices.sorted { a, b in
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
            Button("Type an address") { showManualAdd = true }.padding(.top, 8)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func errorView(_ msg: String) -> some View {
        VStack(spacing: 16) {
            Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 48)).foregroundStyle(.orange)
            Text(msg).multilineTextAlignment(.center).frame(maxWidth: 640)
            Button("Try again") { Task { await load() } }
            Button("Type an address") { showManualAdd = true }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Actions

    private func load() async {
        loading = true
        error = nil
        do {
            let list = try await MachineRegistry.fetch(token: store.token)
            nowMs = Date().timeIntervalSince1970 * 1000
            devices = list
        } catch {
            self.error = error.localizedDescription
        }
        loading = false
    }

    private func connect(_ d: RegisteredDevice) async {
        connecting = d.deviceId
        defer { connecting = nil }

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
        guard let host, !host.isEmpty else {
            error = "\(d.displayName) has no reachable address. Type one manually."
            return
        }
        let box = boxTarget(for: d, host: host)
        store.addBox(box)
        store.select(box)
        dismiss()
    }

    /// Drop our access to everything this host shared, then reload so the rows
    /// that just went away actually go away. If the box we were pointed at was
    /// one of them, deselect it — a selected box we can no longer reach is worse
    /// than none.
    private func leave(_ d: RegisteredDevice) async {
        leaveTarget = nil
        connecting = d.deviceId
        defer { connecting = nil }
        do {
            try await MachineRegistry.leaveHost(hostUserId: d.hostUserIdString,
                                                hostEmail: d.hostEmail,
                                                token: store.token)
        } catch {
            self.error = error.localizedDescription
            return
        }
        // Drop the local entries too. The rows vanish from /devices/list on
        // reload, but a box we already added lives in the store — left behind it
        // would keep a dead machine (and its host's LAN address) on the TV.
        let gone = Set(devices.filter { $0.shared && $0.hostLabel == d.hostLabel }.map(\.deviceId))
            .union([d.deviceId])
        for box in store.boxes where gone.contains(box.id) { store.removeBox(box) }
        await load()
    }

    private func boxTarget(for d: RegisteredDevice, host: String) -> BoxTarget {
        BoxTarget(id: d.deviceId, name: d.displayName, host: host, port: d.port,
                  managed: d.managed, machineId: d.machineId)
    }
}

/// Redeem a 6-char invitation code.
///
/// Deliberately the only guest-side entry on this surface: inviting means typing
/// an email on a TV remote, which is hostile — the host does that on a phone or
/// the web. Six characters is about the most we can ask of a remote, and the
/// code is uppercased/trimmed for the user so the on-screen keyboard's case and
/// stray spaces never cause a bogus "not found".
private struct AcceptInviteView: View {
    let token: String
    let onAccepted: () -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var code = ""
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            VStack(spacing: 24) {
                Image(systemName: "envelope.open").font(.system(size: 48)).foregroundStyle(.secondary)
                Text("Enter the 6-character code from the person sharing their machine with you.")
                    .foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 640)

                TextField("ABC123", text: $code)
                    .textInputAutocapitalization(.characters)
                    .autocorrectionDisabled()
                    .frame(maxWidth: 420)

                if let error {
                    Text(error).foregroundStyle(.orange).multilineTextAlignment(.center).frame(maxWidth: 640)
                }

                Button(busy ? "Accepting…" : "Accept") { Task { await accept() } }
                    .disabled(busy || code.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .navigationTitle("Accept an invitation")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }.disabled(busy)
                }
            }
        }
    }

    private func accept() async {
        busy = true
        error = nil
        do {
            try await MachineRegistry.acceptInviteCode(code, token: token)
            onAccepted()   // the shared machines only exist after a refetch
            dismiss()
        } catch {
            self.error = error.localizedDescription
        }
        busy = false
    }
}

private struct MachineRow: View {
    let device: RegisteredDevice
    let nowMs: Double
    let connecting: Bool
    let selected: Bool

    var body: some View {
        HStack(spacing: 20) {
            Image(systemName: platformIcon).font(.system(size: 30)).frame(width: 44)
            VStack(alignment: .leading, spacing: 4) {
                Text(device.displayName).font(.system(size: 26, weight: .semibold))
                // A shared box is someone else's machine. Saying so on the row —
                // not buried in a detail screen — is the whole point: owned and
                // borrowed rendered identically is how you act on the wrong box.
                if device.shared {
                    Text("SHARED · \(device.hostLabel)")
                        .font(.system(size: 15, weight: .bold))
                        .foregroundStyle(.purple)
                        .lineLimit(1)
                }
                Text(subtitle).font(.system(size: 16)).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            if connecting {
                ProgressView()
            } else {
                statusBadge
            }
        }
        .padding(.horizontal, 28).padding(.vertical, 20)
        .frame(maxWidth: .infinity, alignment: .leading)
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
        } else if device.wakeable && device.isOnline != true {
            badge("Wake", .orange)
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
