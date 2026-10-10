// DashboardView.swift — lean-back launcher, shaped like the web dashboard.
//
// The tile grid used to be twelve boxes (Session, Tasks, Projects, Runtime,
// Apple TV, Capture, Feedback, Android, Switch, Update agent, Shared with,
// Sign out). On a TV that is a wall of inventory; the web's sidebar is five
// items — Devices, Chat, Projects, Vibing — and sign-out lives in the profile.
// This surface is the same five, with defaults collected in Settings:
//
//   Chat     → TasksView          (tasks & vibes — the web/mobile chat)
//   Vibing   → VibingView         (project → available target → live preview)
//   Devices  → MachinePickerView  (your account's machines, switch/wake)
//   Storage  → SharedStorageView (NAS / SMB / S3 / WebDAV browse + search)
//   Settings → account defaults  (device · runner · project · MCP)
//
// The profile menu (top-right) owns the account-level actions: Sign out,
// Update agent. The connected PC is shown as a first-class card
// under the header — name, host, live status — so "which box am I on" is the
// first thing the initial screen answers, exactly like web/mobile.

import SwiftUI

struct DashboardView: View {
    @EnvironmentObject var store: YaverStore
    @State private var showUpdateAgent = false
    @StateObject private var lifecycle = BoxLifecycle()
    @FocusState private var dashboardFocus: DashboardDestination?

    private enum DashboardDestination: Hashable {
        case chat, vibing, studio, ssh, devices, storage, settings, profile, machineSwitch
    }

    /// Open the TV directly on a screen instead of the tile grid.
    ///
    /// Same class of configuration as `yaver.tv.selectedBox` and
    /// `yaver.tv.boxes` above it — read from UserDefaults, so the argument
    /// domain sets it too. Values: "" (normal), "chat", or "vibing".
    ///
    /// This is a ROUTE, not a test hook, and it earns its place twice:
    ///
    ///  1. Product. A TV is the one surface where getting somewhere costs the
    ///     most — every screen is a focus walk with a remote. "Open Yaver on
    ///     the TV at my projects" is the handoff the phone should be able to
    ///     perform, and CLAUDE.md's failure-plumbing rule asks for exactly this
    ///     shape: a route the surface can be sent to, not a sentence describing
    ///     where to go.
    ///
    ///  2. Testability. The dashboard now has fixed focus geometry and a
    ///     default Chat focus; deep routes still avoid needless remote presses.
    @AppStorage("yaver.tv.startAt") private var startAt: String = ""
    @State private var startRoute: StartRoute?
    @State private var didHandleStartRoute = false

    private enum StartRoute: String, Hashable {
        case chat, vibing
    }

    private var requestedStartRoute: StartRoute? {
        if startAt == "chat" { return .chat }
        if startAt == "vibing" { return .vibing }
        return nil
    }

    private func applyStartRouteIfReady() {
        guard store.selectedBox != nil, !didHandleStartRoute,
              let requestedStartRoute else { return }
        // Mark it handled before pushing. Otherwise popping back makes
        // Dashboard.onAppear immediately push again and traps the remote in a
        // navigation loop.
        didHandleStartRoute = true
        startRoute = requestedStartRoute
    }

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
                        // Chat remains reachable even before a box is picked.
                        // Tasks can then explain the missing runner and offer
                        // the next repair in-place instead of making the
                        // dashboard look empty or silently hiding Chat.
                        HStack(spacing: 16) {
                            NavigationLink(destination: TasksView()) {
                                Tile(icon: "bubble.left.and.bubble.right.fill", title: "Chat", outerWidth: 216)
                            }
                            .accessibilityIdentifier("dashboard.chat.no-box")
                            NavigationLink(destination: MachinePickerView()) {
                                Tile(icon: "laptopcomputer", title: "Devices", outerWidth: 216)
                            }
                            .accessibilityIdentifier("dashboard.devices.no-box")
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    } else {
                        selectedMachinePanel
                        wakePanel

                        // One predictable horizontal rail: every primary
                        // surface is visible at once and Left/Right is the only
                        // navigation axis. These are deliberately single-layer
                        // icon + name controls. Explanations duplicated what
                        // the destinations already say and forced each label to
                        // scale differently; an inner material card also fought
                        // tvOS' own focus surface and looked double-framed.
                        HStack(spacing: 16) {
                            NavigationLink(destination: TasksView()) {
                                Tile(icon: "bubble.left.and.bubble.right.fill", title: "Chat", outerWidth: 216)
                            }
                            .focused($dashboardFocus, equals: .chat)
                            .accessibilityIdentifier("dashboard.chat")
                            NavigationLink(destination: VibingView()) {
                                Tile(icon: "play.rectangle.fill", title: "Vibing", outerWidth: 216)
                            }
                            .focused($dashboardFocus, equals: .vibing)
                            .accessibilityIdentifier("dashboard.vibing")
                            NavigationLink(destination: TVStudioView()) {
                                Tile(icon: "rectangle.split.2x1.fill", title: "Studio", outerWidth: 216)
                            }
                            .focused($dashboardFocus, equals: .studio)
                            .accessibilityIdentifier("dashboard.studio")
                            NavigationLink(destination: TVSSHView()) {
                                Tile(icon: "terminal.fill", title: "SSH", outerWidth: 216)
                            }
                            .focused($dashboardFocus, equals: .ssh)
                            .accessibilityIdentifier("dashboard.ssh")
                            NavigationLink(destination: MachinePickerView()) {
                                Tile(icon: "laptopcomputer", title: "Devices", outerWidth: 216)
                            }
                            .focused($dashboardFocus, equals: .devices)
                            .accessibilityIdentifier("dashboard.devices")
                            NavigationLink(destination: SharedStorageView()) {
                                Tile(icon: "externaldrive.fill", title: "Storage", outerWidth: 216)
                            }
                            .focused($dashboardFocus, equals: .storage)
                            .accessibilityIdentifier("dashboard.storage")
                            NavigationLink(destination: TVSettingsView()) {
                                Tile(icon: "gearshape.fill", title: "Settings", outerWidth: 216)
                            }
                            .focused($dashboardFocus, equals: .settings)
                            .accessibilityIdentifier("dashboard.settings")
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 10)
                        .frame(height: 142)
                    }
                }
                .padding(56)
                // Scope-wide default: Chat owns initial focus even though the
                // machine card is now the first focusable element above the
                // rail (it must stay reachable but must not steal the couch's
                // first destination).
                .defaultFocus($dashboardFocus, .chat)
            }
            // Programmatic route from `yaver.tv.startAt` — see the property.
            // Fires once, and only when a box is actually selected: routing into
            // Projects before there is a machine would show an empty screen and
            // read as a broken deep link rather than as "pick a box first".
            .navigationDestination(item: $startRoute) { route in
                switch route {
                case .chat: TasksView()
                case .vibing: VibingView()
                }
            }
            .onChange(of: store.selectedBox?.id) { _, id in
                guard id != nil else { return }
                applyStartRouteIfReady()
            }
            .onAppear {
                applyStartRouteIfReady()
                // Chat owns the couch's first destination even though the
                // machine card is now the first focusable element in the scope
                // (it must stay reachable, not steal initial focus).
                DispatchQueue.main.async { dashboardFocus = .chat }
            }
            .sheet(isPresented: $showUpdateAgent) { UpdateAgentView() }
            .task(id: store.selectedBox?.id) {
                await store.refreshSelectedRelaySettings()
                guard let box = store.selectedBox else { return }
                lifecycle.refreshReachability(box)
                // Seamless connectivity self-heal (tvOS analog of mobile's relay
                // self-heal): if the box isn't answering over direct/relay and
                // it isn't a parkable managed box (which has its own Wake path),
                // re-resolve a fresh reachable address once and re-probe. The
                // task id is the deviceId, which a host swap doesn't change, so
                // this can't loop.
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
        } else if let blocked = lifecycle.clientBlocked {
            // NOT ASLEEP — THIS DEVICE REFUSED THE REQUEST.
            //
            // Measured 2026-08-03: the TV showed "Box asleep — start it from
            // your computer or phone" while that box answered GET /info with
            // 200 throughout. ATS had refused the connection before a packet
            // left the headset. Sending someone to go start a machine that is
            // already running is worse than saying nothing: it costs a trip and
            // teaches them the product cannot be trusted.
            //
            // Deliberately offers NO Wake button. Waking cannot fix a
            // client-side policy, and a button that cannot work is the defect
            // this file family keeps paying for.
            VStack(alignment: .leading, spacing: 16) {
                Label("This device blocked the connection", systemImage: "hand.raised.fill")
                    .font(.system(size: 28, weight: .bold))
                    .foregroundStyle(.orange)
                Text(blocked)
                    .font(.system(size: 19)).foregroundStyle(.secondary)
                    .frame(maxWidth: 820, alignment: .leading)
            }
            .padding(28)
            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 20))
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

    /// Header: brand on the left, the PROFILE menu on the right. Sign-out is
    /// an account action, so it lives here — never buried in a tile grid.
    private var header: some View {
        HStack(alignment: .center, spacing: 18) {
            VStack(alignment: .leading, spacing: 6) {
                Text("Yaver").font(.system(size: 48, weight: .heavy))
                Text(store.selectedBox.map { "Remote runtime on \($0.name)" } ?? "No box selected")
                    .font(.system(size: 20)).foregroundStyle(.secondary)
            }
            Spacer()
            Menu {
                Button(role: .destructive) { store.signOut() } label: {
                    Label("Sign out", systemImage: "rectangle.portrait.and.arrow.right")
                }
                Button { showUpdateAgent = true } label: {
                    Label("Update agent", systemImage: "arrow.down.circle")
                }
            } label: {
                Image(systemName: "person.crop.circle.fill")
                    .font(.system(size: 40))
                    .foregroundStyle(.secondary)
            }
            .focusable()
            .focused($dashboardFocus, equals: .profile)
            .accessibilityIdentifier("dashboard.profile")
        }
    }

    /// The connected PC, named and alive — the first thing the initial screen
    /// answers. Status dot: green = reachable, orange = unreachable, gray =
    /// checking. Detail names host, relay fallback and the runner/render split
    /// badge so a split is never two silent sources.
    ///
    /// The WHOLE card is a focus target. Measured 2026-08-19: the tile rail is
    /// full-width at the bottom, but the Switch button and profile menu sit at
    /// the far right, so the tvOS focus engine could not move Up from the left
    /// or centre tiles — no horizontally-overlapping target existed above, and
    /// the dashboard looked like "only 4 clickable boxes". A full-width
    /// focusable card restores the Up chain: any tile → card → profile.
    private var selectedMachinePanel: some View {
        NavigationLink(destination: MachinePickerView()) {
            HStack(spacing: 22) {
                Image(systemName: "server.rack")
                    .font(.system(size: 34, weight: .semibold))
                    .foregroundStyle(connectivityColor)
                    .frame(width: 54, height: 54)
                    .background(connectivityColor.opacity(0.16), in: RoundedRectangle(cornerRadius: 12))
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 12) {
                        Text(store.selectedBox?.name ?? "Selected machine")
                            .font(.system(size: 26, weight: .bold))
                            .lineLimit(1)
                        if let alias = store.selectedBox?.aliasLabel {
                            Text(alias)
                                .font(.system(size: 19, weight: .semibold))
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                        statusChip
                    }
                    Text(machineDetail)
                        .font(.system(size: 17))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer()
                Label("Switch", systemImage: "rectangle.2.swap")
                    .font(.system(size: 19, weight: .semibold))
                    .padding(.horizontal, 22).padding(.vertical, 10)
            }
            .padding(24)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 18))
        }
        .buttonStyle(.plain)
        .focused($dashboardFocus, equals: .machineSwitch)
        .overlay {
            RoundedRectangle(cornerRadius: 18)
                .stroke(dashboardFocus == .machineSwitch ? Color.white.opacity(0.6) : .clear, lineWidth: 3)
        }
        .accessibilityIdentifier("dashboard.machine-card")
    }

    private var connectivityColor: Color {
        switch lifecycle.reachable {
        case .some(true): return .green
        case .some(false): return .orange
        case nil: return .gray
        }
    }

    @ViewBuilder private var statusChip: some View {
        switch lifecycle.reachable {
        case .some(true):
            chip("Connected", .green)
        case .some(false):
            chip("Unreachable", .orange)
        case nil:
            chip("Checking…", .gray)
        }
    }

    private func chip(_ text: String, _ color: Color) -> some View {
        HStack(spacing: 6) {
            Circle().fill(color).frame(width: 9, height: 9)
            Text(text)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(color)
        }
        .padding(.horizontal, 12).padding(.vertical, 5)
        .background(color.opacity(0.14), in: Capsule())
    }

    private var machineDetail: String {
        guard let box = store.selectedBox else { return "No machine selected" }
        var parts: [String] = []
        if !box.host.isEmpty { parts.append(box.host) }
        if box.wakeable { parts.append("wakeable") }
        if box.relayBaseUrl?.isEmpty == false { parts.append("relay fallback") }
        let runnerDeviceId = store.runnerBox()?.id ?? box.id
        if let runner = store.primaryRunnerByDevice[runnerDeviceId], !runner.isEmpty {
            parts.append("default runner: \(runner)")
        }
        // Runner/render split badge — two silent sources are two
        // unfalsifiable states; the dashboard names both boxes.
        if let badge = store.machineRolesBadge { parts.append(badge) }
        return parts.isEmpty ? "Account relay" : parts.joined(separator: " · ")
    }

    private var emptyBoxPrompt: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("No remote runner connected")
                .font(.system(size: 26, weight: .semibold))
            Text("Chat is still available, but OpenCode + DeepSeek coding tasks need a runner machine. Rendering is a separate optional capability.")
                .font(.system(size: 19)).foregroundStyle(.secondary).frame(maxWidth: 720, alignment: .leading)
            HStack(spacing: 16) {
                NavigationLink("Choose machine", destination: MachinePickerView())
                    .buttonStyle(.borderedProminent)
                NavigationLink("Add LAN box", destination: AddBoxView())
                    .buttonStyle(.bordered)
            }
            Text("To use the optional boxless Git+coding lane, configure its task executor from a supported client. This TV currently reports that capability honestly rather than pretending a task ran.")
                .font(.system(size: 15)).foregroundStyle(.tertiary).frame(maxWidth: 820, alignment: .leading)
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
            NavigationLink(destination: MachinePickerView()) {
                Text("Choose a machine myself")
            }
            .simultaneousGesture(TapGesture().onEnded { store.cancelAutoConnect() })
            .padding(.top, 8)
        }
    }
}

private struct Tile: View {
    let icon: String
    let title: String
    let outerWidth: CGFloat

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: icon)
                .font(.system(size: 30, weight: .semibold))
                .frame(width: 38)
            Text(title)
                .font(.system(size: 22, weight: .semibold))
                .lineLimit(1)
                .allowsTightening(true)
        }
        .frame(width: max(130, outerWidth - 36), height: 82, alignment: .leading)
        .padding(.horizontal, 18)
    }
}

// AddBoxView moved to ../AddBoxView.swift — the shared client layer — so the
// visionOS target can present it too. Sharing UI is intentionally absent
// from every v1 client surface.
