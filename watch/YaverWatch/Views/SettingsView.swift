// SettingsView.swift — the small settings surface. Its main job is the explicit
// "use without your phone" opt-in (mode B/C), which is the ONLY place the watch
// starts holding a session token (docs/yaver-smartwatch-voice-terminal.md §8:
// standalone token custody is the one place the watch stops being "holds nothing
// sensitive"). Off by default.

import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var store: WatchStore
    @Environment(\.dismiss) private var dismiss
    @State private var showSignIn = false
    @State private var state: UpdateState = .idle

    /// The update request's lifecycle as far as we can HONESTLY observe it: we
    /// see it accepted, never applied. There is deliberately no `.updating`.
    private enum UpdateState: Equatable {
        case idle
        case requesting
        case requested(String)   // the version the backend recorded
        case failed(String)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                // Phone-paired status (the default, preferred transport).
                HStack {
                    Image(systemName: store.phone.canUsePhone ? "iphone" : "iphone.slash")
                        .foregroundStyle(store.phone.canUsePhone ? .green : .secondary)
                    Text(store.phone.canUsePhone ? "Paired with iPhone" : "iPhone not reachable")
                        .font(.footnote)
                }

                Divider()

                Toggle("Use without phone", isOn: $store.standaloneOptIn)
                    .font(.system(size: 15, weight: .semibold))
                Text("Lets the watch reach your box directly over your network when your phone isn't around. Stores a session token on the watch.")
                    .font(.caption2).foregroundStyle(.secondary)

                if store.standaloneOptIn {
                    if store.hasStandaloneCreds, let box = store.box {
                        Label(box.name, systemImage: "server.rack").font(.footnote)

                        Divider()
                        updateAgent(box)

                        Divider()
                        Button("Sign out of box", role: .destructive) {
                            store.signOutStandalone()
                        }
                        .font(.footnote)
                    } else {
                        Button("Sign in to a box") { showSignIn = true }
                            .font(.footnote)
                    }
                }
            }
            .padding(.horizontal, 6)
        }
        .sheet(isPresented: $showSignIn) { SignInView() }
        // Backfill the deviceId for a box signed in before it was captured. Here
        // because Settings is where the button lives — if it resolves, the button
        // appears in place; if not, the explanation below does.
        .task { await store.resolveDeviceIdIfNeeded() }
    }

    /// "Update agent" — Convex-direct desired state, NOT a command to the box.
    ///
    /// This asks the ACCOUNT to record that the box should update; the box reads
    /// it off its own next heartbeat. So it works when the watch has no route to
    /// the box at all — which, on a wrist, is most of the time. The flip side is
    /// that we never learn whether it applied: there is no progress signal, so
    /// there is no progress bar. "Requested" is the whole truth.
    @ViewBuilder private func updateAgent(_ box: BoxTarget) -> some View {
        if let deviceId = box.deviceId {
            switch state {
            case .requested(let version):
                Label("Update requested", systemImage: "checkmark.circle.fill")
                    .font(.footnote).foregroundStyle(.green)
                Text("\(version) applies when \(box.name) next checks in.")
                    .font(.caption2).foregroundStyle(.secondary)
            case .requesting:
                HStack(spacing: 6) {
                    ProgressView()
                    Text("Requesting…").font(.footnote)
                }
            default:
                Button("Update agent") { Task { await request(deviceId: deviceId) } }
                    .font(.footnote)
                Text("Asks \(box.name) to update to the latest agent. Applies at its next check-in.")
                    .font(.caption2).foregroundStyle(.secondary)
                if case .failed(let message) = state {
                    Text(message).font(.caption2).foregroundStyle(.orange)
                }
            }
        } else {
            // No deviceId → no honest way to name the box to the backend. Say
            // what's missing and how to fix it, rather than shipping a button
            // that would send a LAN IP as a deviceId and get "Device not found".
            Text("Update agent needs to identify this box. Open this screen on \(box.name)'s network once.")
                .font(.caption2).foregroundStyle(.secondary)
        }
    }

    private func request(deviceId: String) async {
        state = .requesting
        do {
            let version = try await AgentUpdate.request(deviceId: deviceId, token: store.token)
            state = .requested(version)
        } catch {
            state = .failed(error.localizedDescription)
        }
    }
}
