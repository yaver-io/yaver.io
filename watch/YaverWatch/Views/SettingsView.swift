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
    }
}
