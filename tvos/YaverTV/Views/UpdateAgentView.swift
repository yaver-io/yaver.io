// UpdateAgentView.swift — ask a machine to update its agent, from the couch.
//
// Why this is a REGISTRY screen and not a button on the selected box:
//
//  1. The deviceId is only real here. `/devices/request-update` addresses a box
//     by deviceId, and BoxTarget.id is a deviceId ONLY when the box came from
//     the registry (MachinePickerView / auto-connect / heal). The manual
//     "type an address" path stores `id = host` (AddBoxView.swift:36) — a box
//     added that way would send a LAN IP as a deviceId and get "Device not
//     found". Reading the deviceId off the registry means it is never a guess.
//
//  2. The machines that most need this are the ones the TV cannot reach. tvOS is
//     direct-LAN only (no relay — YaverStore.healReachability), so a box on
//     another network can't be selected, driven, or updated over HTTP. The
//     request-update path doesn't care: it writes desired state onto the device
//     row, and the box picks it up on its next heartbeat. Scoping this to the
//     selected box would exclude exactly the boxes with no other trigger.
//
// The honest contract: we learn the request was ACCEPTED, never that it was
// applied — the box may be asleep for a week. So a row says "Update requested",
// and there is no progress bar, because there is no progress signal to read.

import SwiftUI

struct UpdateAgentView: View {
    @EnvironmentObject var store: YaverStore
    @Environment(\.dismiss) private var dismiss

    @State private var devices: [RegisteredDevice] = []
    @State private var loading = true
    @State private var error: String?
    @State private var requesting: String?              // deviceId in flight
    @State private var requested: [String: String] = [:] // deviceId -> version asked for
    @State private var rowErrors: [String: String] = [:] // deviceId -> why it failed

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
            .navigationTitle("Update agents")
        }
        .task { await load() }
    }

    private var list: some View {
        ScrollView {
            LazyVStack(spacing: 14) {
                explainer
                ForEach(sortedDevices) { d in
                    Button {
                        Task { await request(d) }
                    } label: {
                        UpdateRow(device: d,
                                  nowMs: nowMs,
                                  requesting: requesting == d.deviceId,
                                  requestedVersion: requested[d.deviceId],
                                  rowError: rowErrors[d.deviceId])
                    }
                    .buttonStyle(.card)
                    .disabled(requesting != nil)
                }
            }
            .padding(32)
        }
    }

    // Says plainly what tapping does — and what it does NOT do. A user who
    // expects a live install and gets a silent row would call this broken.
    private var explainer: some View {
        Text("Pick a machine to update to the latest agent. The request waits on your account, so it works even if the machine is asleep or on another network — it applies the next time that machine checks in.")
            .font(.system(size: 19)).foregroundStyle(.secondary)
            .frame(maxWidth: 900, alignment: .leading)
            .padding(.bottom, 8)
    }

    // Live first, then by name — the same rule as MachinePickerView.
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
            Text("Run `yaver serve` on a computer signed in as you, and it appears here.")
                .foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 640)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func errorView(_ msg: String) -> some View {
        VStack(spacing: 16) {
            Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 48)).foregroundStyle(.orange)
            Text(msg).multilineTextAlignment(.center).frame(maxWidth: 640)
            Button("Try again") { Task { await load() } }
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

    /// Ask for "latest". Pinning a concrete version is a typing job, and this is
    /// a remote with a D-pad — the CLI is where you pin.
    private func request(_ d: RegisteredDevice) async {
        requesting = d.deviceId
        rowErrors[d.deviceId] = nil
        defer { requesting = nil }
        do {
            let version = try await MachineRegistry.requestUpdate(deviceId: d.deviceId, token: store.token)
            requested[d.deviceId] = version
        } catch {
            rowErrors[d.deviceId] = error.localizedDescription
        }
    }
}

private struct UpdateRow: View {
    let device: RegisteredDevice
    let nowMs: Double
    let requesting: Bool
    let requestedVersion: String?
    let rowError: String?

    var body: some View {
        HStack(spacing: 20) {
            Image(systemName: platformIcon).font(.system(size: 30)).frame(width: 44)
            VStack(alignment: .leading, spacing: 4) {
                Text(device.displayName).font(.system(size: 26, weight: .semibold))
                Text(subtitle).font(.system(size: 16)).foregroundStyle(.secondary).lineLimit(1)
                if let rowError {
                    Text(rowError).font(.system(size: 16, design: .monospaced))
                        .foregroundStyle(.red).lineLimit(2)
                }
            }
            Spacer()
            if requesting {
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
        // After a request, say what happens next in the row itself — the box may
        // be days away from checking in, and "Requested" alone reads as "stuck".
        if let requestedVersion {
            let now = device.agentVersion.map { "on \($0)" } ?? "current version unknown"
            return fresh
                ? "\(now) → \(requestedVersion) · applies at its next check-in (about a minute)"
                : "\(now) → \(requestedVersion) · applies when it next checks in"
        }
        var parts: [String] = []
        if let p = device.platform { parts.append(p) }
        parts.append(device.agentVersion.map { "agent \($0)" } ?? "agent version unknown")
        if device.wakeable { parts.append("managed") }
        return parts.joined(separator: " · ")
    }

    private var fresh: Bool {
        guard device.isOnline == true else { return false }
        guard let hb = device.lastHeartbeat, nowMs > 0 else { return true }
        return (nowMs - hb) < RegisteredDevice.heartbeatStaleMs
    }

    @ViewBuilder private var statusBadge: some View {
        if requestedVersion != nil {
            badge("Requested", .blue)
        } else if rowError != nil {
            badge("Try again", .red)
        } else if fresh {
            badge("Online", .green)
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
