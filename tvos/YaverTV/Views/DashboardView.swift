// DashboardView.swift — lean-back tile launcher. Picks/registers a box (the
// LAN host running `yaver serve`) and routes into the control surfaces.

import SwiftUI

struct DashboardView: View {
    @EnvironmentObject var store: YaverStore
    @State private var showPicker = false
    @StateObject private var lifecycle = BoxLifecycle()

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 36) {
                    header

                    if store.selectedBox == nil {
                        if store.autoConnecting {
                            autoConnectPanel
                        } else {
                            emptyBoxPrompt
                        }
                    } else {
                        wakePanel

                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 300), spacing: 24)], spacing: 24) {
                            NavigationLink(destination: SessionView()) {
                                Tile(icon: "terminal.fill", title: "Session", detail: "Drive a live coding session")
                            }
                            NavigationLink(destination: TasksView()) {
                                Tile(icon: "checklist", title: "Tasks", detail: "What's running & in review")
                            }
                            NavigationLink(destination: ProjectsView()) {
                                Tile(icon: "folder.fill", title: "Projects", detail: "Browse & preview on the TV")
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
                            NavigationLink(destination: FeedbackView()) {
                                Tile(icon: "bubble.left.and.text.bubble.right", title: "Feedback", detail: "Reports from test devices")
                            }
                            NavigationLink(destination: DroidStreamView()) {
                                Tile(icon: "iphone.gen3", title: "Android", detail: "Watch the redroid screen live")
                            }
                            Button { showPicker = true } label: {
                                Tile(icon: "server.rack", title: store.selectedBox?.name ?? "Box", detail: "Switch machine")
                            }
                            Button { store.signOut() } label: {
                                Tile(icon: "rectangle.portrait.and.arrow.right", title: "Sign out", detail: "")
                            }
                        }
                    }
                }
                .padding(56)
            }
            .sheet(isPresented: $showPicker) { MachinePickerView() }
            .task(id: store.selectedBox?.id) {
                guard let box = store.selectedBox else { return }
                lifecycle.refreshReachability(box)
                // Seamless connectivity self-heal (tvOS analog of mobile's relay
                // self-heal): if the box isn't answering at its cached host and it
                // isn't a parkable managed box (which has its own Wake path),
                // re-resolve a fresh reachable address once and re-probe. The task
                // id is the deviceId, which a host swap doesn't change, so this
                // can't loop.
                try? await Task.sleep(nanoseconds: 2_500_000_000)
                if lifecycle.reachable == false, !(box.managed ?? false) {
                    await store.healReachability()
                    if let healed = store.selectedBox, healed.host != box.host {
                        lifecycle.refreshReachability(healed)
                    }
                }
            }
            // Stream C: on launch, silently connect to a live machine + narrate,
            // rather than dropping the user on the "Choose machine" wall.
            .onAppear { store.autoConnectOnLaunch() }
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
            Text("Pick a machine")
                .font(.system(size: 26, weight: .semibold))
            Text("Choose one of the machines on your account, or type a LAN address. A machine appears here once it's running `yaver serve` signed in as you.")
                .font(.system(size: 19)).foregroundStyle(.secondary).frame(maxWidth: 720, alignment: .leading)
            Button("Choose machine") { showPicker = true }.padding(.top, 8)
        }
    }

    // Narrated auto-connect (Stream C): while the launch sweep is in flight, show
    // WHICH machine we're reaching for + a way to bail, instead of the static
    // "Choose machine" wall. Mirrors mobile's NoMachineEmpty auto-connect branch.
    private var autoConnectPanel: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack(spacing: 16) {
                ProgressView().scaleEffect(1.3)
                Text(AutoConnectStatus.sentence(store.autoConnectTarget))
                    .font(.system(size: 26, weight: .semibold))
            }
            Text("Connecting automatically. This opens the moment your machine is ready.")
                .font(.system(size: 19)).foregroundStyle(.secondary).frame(maxWidth: 720, alignment: .leading)
            Button("Choose a machine myself") {
                store.cancelAutoConnect()
                showPicker = true
            }
            .padding(.top, 8)
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
