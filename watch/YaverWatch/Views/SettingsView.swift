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
    @State private var confirmRemoval = false
    @State private var removalError: String?
    @State private var removing = false
    @State private var appearanceError: String?
    @State private var privateVPSDraft = UserDefaults.standard.string(forKey: Backend.privateVPSKey) ?? ""
    @State private var privateVPSError: String?

    /// The update request's lifecycle as far as we can HONESTLY observe it: we
    /// see it accepted, never applied. There is deliberately no `.updating`.
    private enum UpdateState: Equatable {
        case idle
        case requesting
        case requested(String)   // the version the backend recorded
        case failed(String)
    }

    var body: some View {
        // Its OWN NavigationStack: RootView presents this view with
        // `.sheet(isPresented:)`, and a sheet starts a fresh view hierarchy.
        NavigationStack {
            settings
        }
    }

    private var settings: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                VStack(alignment: .leading, spacing: 6) {
                    Label("Private VPS URL", systemImage: "server.rack").font(.footnote.bold())
                    TextField("https://vps.example.com", text: $privateVPSDraft)
                        .textContentType(.URL)
                    Button("Use this VPS") { savePrivateVPS() }
                        .font(.footnote).disabled(privateVPSDraft.isEmpty)
                    if UserDefaults.standard.string(forKey: Backend.privateVPSKey) != nil {
                        Button("Use Yaver hosted", role: .destructive) {
                            store.signOutStandalone()
                            UserDefaults.standard.removeObject(forKey: Backend.privateVPSKey)
                            privateVPSDraft = ""
                        }.font(.footnote)
                    }
                    if let privateVPSError { Text(privateVPSError).font(.caption2).foregroundStyle(.orange) }
                    Text("Local to this watch. Use your paired iPhone to read the VPS QR, then dictate or type the URL here.")
                        .font(.caption2).foregroundStyle(.secondary)
                }

                Divider()

                // Phone-paired status (the default, preferred transport).
                HStack {
                    Image(systemName: store.phone.canUsePhone ? "iphone" : "iphone.slash")
                        .foregroundStyle(store.phone.canUsePhone ? .green : .secondary)
                    Text(store.phone.canUsePhone ? "Paired with iPhone" : "iPhone not reachable")
                        .font(.footnote)
                }

                Divider()

                Picker("Appearance", selection: Binding(
                    get: { store.appearanceTheme },
                    set: { value in Task { await saveAppearance(value) } }
                )) {
                    Text("Dark").tag("dark")
                    Text("Light").tag("light")
                }
                if let appearanceError {
                    Text(appearanceError).font(.caption2).foregroundStyle(.orange)
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
                    } else {
                        Button("Sign in to a box") { showSignIn = true }
                            .font(.footnote)
                    }

                    if store.hasStandaloneCreds {
                        Divider()
                        Button("Sign out of box", role: .destructive) {
                            store.signOutStandalone()
                        }
                        .font(.footnote)

                        if store.box?.deviceId != nil {
                            Button("Remove box from Yaver", role: .destructive) {
                                confirmRemoval = true
                            }
                            .font(.footnote)
                            .disabled(removing)
                            Text("Removes it from every Yaver device list. A repaired or reset box can pair again.")
                                .font(.caption2).foregroundStyle(.secondary)
                        }
                        if removing { ProgressView() }
                        if let removalError {
                            Text(removalError).font(.caption2).foregroundStyle(.orange)
                        }
                    }
                }
            }
            .padding(.horizontal, 6)
        }
        .sheet(isPresented: $showSignIn) { SignInView() }
        // Backfill the deviceId for a box signed in before it was captured. Here
        // because Settings is where the button lives — if it resolves, the button
        // appears in place; if not, the explanation below does.
        .task {
            await store.syncAppearance()
            await store.resolveDeviceIdIfNeeded()
        }
        .confirmationDialog("Remove this box from Yaver?", isPresented: $confirmRemoval) {
            Button("Remove", role: .destructive) { Task { await removeBox() } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("It disappears from every Yaver surface immediately. This does not delete your operating system or repositories.")
        }
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

    private func savePrivateVPS() {
        guard let url = Backend.normalizedPrivateVPSURL(privateVPSDraft) else {
            privateVPSError = "Enter a valid HTTPS URL."
            return
        }
        store.signOutStandalone()
        UserDefaults.standard.set(url.absoluteString, forKey: Backend.privateVPSKey)
        privateVPSDraft = url.absoluteString
        privateVPSError = nil
    }

    private func saveAppearance(_ theme: String) async {
        appearanceError = nil
        do {
            try await store.setAppearanceTheme(theme)
        } catch {
            appearanceError = "Open Yaver on your iPhone to save this watch's appearance."
        }
    }

    private func removeBox() async {
        guard let deviceId = store.box?.deviceId else { return }
        removing = true
        removalError = nil
        defer { removing = false }
        do {
            try await DeviceRemoval.remove(deviceId: deviceId, token: store.token)
            store.signOutStandalone()
            dismiss()
        } catch {
            removalError = error.localizedDescription
        }
    }
}
