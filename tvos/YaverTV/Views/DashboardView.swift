// DashboardView.swift — lean-back tile launcher. Picks/registers a box (the
// LAN host running `yaver serve`) and routes into the control surfaces.

import SwiftUI

struct DashboardView: View {
    @EnvironmentObject var store: YaverStore
    @State private var showAddBox = false
    @StateObject private var lifecycle = BoxLifecycle()

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 36) {
                    header

                    if store.selectedBox == nil {
                        emptyBoxPrompt
                    } else {
                        wakePanel

                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 300), spacing: 24)], spacing: 24) {
                            NavigationLink(destination: SessionView()) {
                                Tile(icon: "terminal.fill", title: "Session", detail: "Drive a live coding session")
                            }
                            // "Yaver Catalog" used to sit here. It navigated to
                            // RuntimeDashboardView — the same destination as the
                            // Runtime tile below it — under the subtitle
                            // "SFMG · Carrotbet · Personal Runtime": three of the
                            // author's own projects, hardcoded, shipped to every
                            // install. There is no catalog. A tile that lies about
                            // where it goes and advertises a stranger's side
                            // projects is worse than no tile.
                            NavigationLink(destination: RuntimeDashboardView()) {
                                Tile(icon: "terminal", title: "Runtime", detail: "Claude · Codex · reload")
                            }
                            NavigationLink(destination: AppleTVRemoteView()) {
                                Tile(icon: "appletv", title: "Apple TV", detail: "Remote · now playing")
                            }
                            NavigationLink(destination: AppleTVRemoteView(captureFirst: true)) {
                                Tile(icon: "video", title: "Capture", detail: "Capture card view")
                            }
                            Button { showAddBox = true } label: {
                                Tile(icon: "server.rack", title: store.selectedBox?.name ?? "Box", detail: "Change box")
                            }
                            Button { store.signOut() } label: {
                                Tile(icon: "rectangle.portrait.and.arrow.right", title: "Sign out", detail: "")
                            }
                        }
                    }
                }
                .padding(56)
            }
            .sheet(isPresented: $showAddBox) { AddBoxView() }
            .task(id: store.selectedBox?.id) {
                if let box = store.selectedBox { lifecycle.refreshReachability(box) }
            }
        }
    }

    // Shown above the tiles when the selected box is unreachable, and while a
    // wake is running. A reachable box shows nothing here.
    @ViewBuilder private var wakePanel: some View {
        if lifecycle.isRunning {
            WakeProgressView(lifecycle: lifecycle, boxName: store.selectedBox?.name)
        } else if (lifecycle.needsWake || lifecycle.error != nil), let box = store.selectedBox {
            VStack(alignment: .leading, spacing: 16) {
                Label("Box asleep", systemImage: "moon.zzz.fill")
                    .font(.system(size: 28, weight: .bold))
                    .foregroundStyle(.orange)
                if box.wakeable {
                    Text("\(box.name) isn't answering. It may have parked itself to save cost. Wake it to keep working.")
                        .font(.system(size: 19)).foregroundStyle(.secondary).frame(maxWidth: 820, alignment: .leading)
                    Button {
                        lifecycle.wake(box, token: store.token)
                    } label: {
                        Label(lifecycle.error == nil ? "Wake" : "Try again", systemImage: "power")
                            .font(.system(size: 22, weight: .semibold))
                            .padding(.horizontal, 28).padding(.vertical, 12)
                    }
                    .buttonStyle(.borderedProminent)
                } else {
                    Text("\(box.name) isn't answering, and it can't be woken from the TV — start it from your computer or phone.")
                        .font(.system(size: 19)).foregroundStyle(.secondary).frame(maxWidth: 820, alignment: .leading)
                }
                if let err = lifecycle.error {
                    Text(err).font(.system(size: 16, design: .monospaced)).foregroundStyle(.red)
                }
            }
            .padding(28)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 20))
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Yaver").font(.system(size: 48, weight: .heavy))
            Text(store.selectedBox.map { "Remote runtime on \($0.name) · \($0.host)" } ?? "No box selected")
                .font(.system(size: 20)).foregroundStyle(.secondary)
        }
    }

    private var emptyBoxPrompt: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Add the box running Yaver")
                .font(.system(size: 26, weight: .semibold))
            Text("Enter the LAN address of a machine running `yaver serve` (e.g. a Raspberry Pi or your Mac). The Apple TV must be on the same network.")
                .font(.system(size: 19)).foregroundStyle(.secondary).frame(maxWidth: 720, alignment: .leading)
            Button("Add box") { showAddBox = true }.padding(.top, 8)
        }
    }
}

private struct Tile: View {
    let icon: String
    let title: String
    let detail: String
    @Environment(\.isFocused) private var isFocused

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Image(systemName: icon).font(.system(size: 40))
            Spacer(minLength: 0)
            Text(title).font(.system(size: 24, weight: .bold))
            if !detail.isEmpty {
                Text(detail).font(.system(size: 16)).foregroundStyle(.secondary)
            }
        }
        .frame(width: 280, height: 180, alignment: .leading)
        .padding(24)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 20))
    }
}

// AddBoxView moved to ../AddBoxView.swift — the shared client layer — so the
// visionOS target can present it too. It was the only caller of store.addBox()
// in the repo, and living here made it unreachable from the headset.
