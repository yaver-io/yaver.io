// YaverTVApp.swift — @main entry. Gates on auth: email/password or phone-approved QR until a
// session token exists, then the lean-back dashboard.

import SwiftUI
import PlainSSH

@main
struct YaverTVApp: App {
    @StateObject private var store = YaverStore(appearanceSurface: "tvos")

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(store)
                .preferredColorScheme(store.appearanceTheme == "light" ? .light : .dark)
        }
    }
}

struct RootView: View {
    @State private var plainSSH = false
    @EnvironmentObject var store: YaverStore

    var body: some View {
        Group {
            if store.isAuthenticated {
                DashboardView()
            } else {
                SignInView()
            }
        }
        .safeAreaInset(edge: .bottom) { Button("SSH · no Yaver account required") { plainSSH = true }.padding(8) }
        .sheet(isPresented: $plainSSH) { PlainSSHView() }
        .task(id: store.token) { await store.refreshAppearanceSettings() }
    }
}
