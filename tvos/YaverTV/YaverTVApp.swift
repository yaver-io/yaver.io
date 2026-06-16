// YaverTVApp.swift — @main entry. Gates on auth: device-code sign-in until a
// session token exists, then the lean-back dashboard.

import SwiftUI

@main
struct YaverTVApp: App {
    @StateObject private var store = YaverStore()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(store)
        }
    }
}

struct RootView: View {
    @EnvironmentObject var store: YaverStore

    var body: some View {
        if store.isAuthenticated {
            DashboardView()
        } else {
            SignInView()
        }
    }
}
