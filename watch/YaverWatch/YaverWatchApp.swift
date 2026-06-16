// YaverWatchApp.swift — @main entry. Injects the WatchStore and activates the
// WCSession on launch so the phone-paired transport is ready immediately.
//
// Unlike the tvOS app, the watch does NOT gate the whole UI behind auth: in the
// DEFAULT phone-paired mode the watch holds no token and relies entirely on the
// already-signed-in phone. SignInView is only reached from Settings when the
// user opts into standalone "use without your phone" (mode B/C).

import SwiftUI

@main
struct YaverWatchApp: App {
    @StateObject private var store = WatchStore()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(store)
                .onAppear { store.activate() }
                // Complication quick-actions deep-link in as
                // yaverwatch://intent/<run-tests|status|deploy>; map and dispatch.
                .onOpenURL { url in
                    if let intent = WatchDeepLink.intent(from: url) {
                        Task { await store.sendIntent(intent) }
                    }
                }
        }
    }
}
