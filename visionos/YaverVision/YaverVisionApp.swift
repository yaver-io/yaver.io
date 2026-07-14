// YaverVisionApp.swift — @main entry for the visionOS app.
//
// Same gate as tvOS: device-code sign-in until a session token exists, then the
// runtime dashboard. The window is sized for a comfortable reading distance
// rather than maximised — a control surface you glance at, not a screen you
// stare into.

import SwiftUI

@main
struct YaverVisionApp: App {
    @StateObject private var store = YaverStore()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(store)
        }
        .defaultSize(width: 900, height: 620)
    }
}

struct RootView: View {
    @EnvironmentObject var store: YaverStore

    var body: some View {
        if store.isAuthenticated {
            VisionDashboardView()
        } else {
            VisionSignInView()
        }
    }
}
