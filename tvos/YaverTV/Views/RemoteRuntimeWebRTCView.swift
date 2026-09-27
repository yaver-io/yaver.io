// RemoteRuntimeWebRTCView.swift — drive a remote phone/browser app from a TV.
//
// Media and input deliberately use separate planes:
//   • WebRTC receives H.264 when the target can encode it, or JPEG frames on a
//     data channel when it cannot.
//   • Authenticated HTTP sends tap/swipe/text/key actions through the agent's
//     single-writer control lease.
//
// That separation keeps Siri Remote input reliable over the free relay without
// coupling a click to a particular media codec.

import SwiftUI
import UIKit
import LiveKitWebRTC

private enum TVAppControlMode: String {
    case pointer = "Pointer"
    case scroll = "Scroll"

    var symbol: String { self == .pointer ? "cursorarrow.rays" : "arrow.up.and.down" }
}

#if os(tvOS)
/// A UIKit focus target that consumes directional presses before tvOS performs
/// default focus navigation. SwiftUI's `onMoveCommand` observes a move but does
/// not cancel the focus engine; that made DeepSeek flash and played a focus
/// click before the overlay reclaimed focus on every Right press.
private struct TVRemoteInputCapture: UIViewRepresentable {
    let accessibilityLabel: String
    let onMove: (MoveCommandDirection) -> Void
    let onSelect: () -> Void
    let onExit: () -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeUIView(context: Context) -> TVRemoteInputButton {
        let button = TVRemoteInputButton(type: .custom)
        button.backgroundColor = .clear
        button.isOpaque = false
        button.isAccessibilityElement = true
        button.accessibilityTraits = .button
        button.accessibilityIdentifier = "vibing.remote-input-capture"
        button.addTarget(context.coordinator, action: #selector(Coordinator.select), for: .primaryActionTriggered)
        context.coordinator.button = button
        apply(to: button, coordinator: context.coordinator)
        return button
    }

    func updateUIView(_ button: TVRemoteInputButton, context: Context) {
        context.coordinator.parent = self
        apply(to: button, coordinator: context.coordinator)
    }

    private func apply(to button: TVRemoteInputButton, coordinator: Coordinator) {
        button.accessibilityLabel = accessibilityLabel
        button.onMove = { [weak coordinator] direction in coordinator?.parent.onMove(direction) }
        button.onExit = { [weak coordinator] in coordinator?.parent.onExit() }
    }

    final class Coordinator: NSObject {
        var parent: TVRemoteInputCapture
        weak var button: TVRemoteInputButton?

        init(_ parent: TVRemoteInputCapture) { self.parent = parent }

        @objc func select() { parent.onSelect() }
    }
}

private final class TVRemoteInputButton: UIButton {
    var onMove: ((MoveCommandDirection) -> Void)?
    var onExit: (() -> Void)?

    override func shouldUpdateFocus(in context: UIFocusUpdateContext) -> Bool {
        let directional: UIFocusHeading = [.up, .down, .left, .right]
        if isFocused, !context.focusHeading.intersection(directional).isEmpty {
            // Consuming UIPress prevents the move callback from bubbling, but
            // tvOS can still ask the focus engine to move and play its click.
            // Veto that update at the focused UIKit environment. The same
            // arrow has already been delivered to `onMove` as remote input.
            return false
        }
        return super.shouldUpdateFocus(in: context)
    }

    override func pressesBegan(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        for press in presses {
            if let direction = moveDirection(for: press.type) {
                onMove?(direction)
            } else if press.type == .menu {
                onExit?()
            }
        }
        let uncaptured = Set(presses.filter { moveDirection(for: $0.type) == nil && $0.type != .menu })
        if !uncaptured.isEmpty { super.pressesBegan(uncaptured, with: event) }
    }

    override func pressesChanged(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        let uncaptured = Set(presses.filter { moveDirection(for: $0.type) == nil && $0.type != .menu })
        if !uncaptured.isEmpty { super.pressesChanged(uncaptured, with: event) }
    }

    override func pressesEnded(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        let uncaptured = Set(presses.filter { moveDirection(for: $0.type) == nil && $0.type != .menu })
        if !uncaptured.isEmpty { super.pressesEnded(uncaptured, with: event) }
    }

    override func pressesCancelled(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        let uncaptured = Set(presses.filter { moveDirection(for: $0.type) == nil && $0.type != .menu })
        if !uncaptured.isEmpty { super.pressesCancelled(uncaptured, with: event) }
    }

    private func moveDirection(for type: UIPress.PressType) -> MoveCommandDirection? {
        switch type {
        case .upArrow: return .up
        case .downArrow: return .down
        case .leftArrow: return .left
        case .rightArrow: return .right
        default: return nil
        }
    }
}
#endif

struct RemoteRuntimeWebRTCView: View {
    @EnvironmentObject private var store: YaverStore
    @Environment(\.dismiss) private var dismiss

    let project: ProjectSummary
    let form: PreviewForm
    let forcedTargetID: String?
    let launchGuest: Bool

    init(
        project: ProjectSummary,
        form: PreviewForm,
        forcedTargetID: String? = nil,
        launchGuest: Bool = false
    ) {
        self.project = project
        self.form = form
        self.forcedTargetID = forcedTargetID
        self.launchGuest = launchGuest
    }

    @StateObject private var runtime = TVRemoteRuntimeController()
    @State private var cursor = CGPoint(x: 0.5, y: 0.5)
    @State private var mode: TVAppControlMode = .pointer
    @State private var keyboardText = ""
    @State private var showingKeyboard = false
    @State private var showingConsoleLogs = false
    @State private var selectedModelLabel = "DeepSeek V4 Flash"
    @State private var modelFocusRequest = 0
    @FocusState private var keyboardFieldFocused: Bool
    @State private var vibePrefill = ""
    @State private var chatFocusRequest = 0
    @State private var domMode = false
    @State private var selectingElement = false
    @State private var selectedElementSummary: String?
    @State private var domError: String?
    @State private var domHoverTask: Task<Void, Never>?
    @State private var lastMoveAt = Date.distantPast
    @State private var repeatedMoves = 0
    /// Explicit input mode, separate from SwiftUI focus. tvOS is allowed to
    /// recalculate focus during a remote swipe; it is never allowed to turn an
    /// active remote mouse session back into Vibe navigation.
    @State private var overlayInput = TVOverlayInputState()
    @State private var overlayFocusLosses = 0
    @FocusState private var streamFocused: Bool
    @Namespace private var defaultFocus

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            GeometryReader { layout in
            HStack(alignment: .top, spacing: 22) {
                streamSurface
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                .frame(width: mobileAppLayout ? layout.size.width * 0.40 : nil)
                .frame(maxWidth: mobileAppLayout ? nil : .infinity, maxHeight: .infinity)
                .focusSection()

                VStack(alignment: .leading, spacing: 14) {
                    HStack(alignment: .center, spacing: 12) {
                        Label("Vibe", systemImage: "wand.and.stars")
                            .font(.system(size: 24, weight: .bold))
                        Spacer(minLength: 8)
                        header
                    }
                    controlRail
                    // The recovery card already owns terminal render failures.
                    // Repeating status + cause above the chat created two large
                    // diagnostic rows for one failure and pushed the work down.
                    if runtime.error == nil,
                       let note = runtime.controlNote, !note.isEmpty, !runtime.connected {
                        Text(note)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                    }
                    if domMode {
                        if let selectedElementSummary {
                            selectedElementChip(selectedElementSummary)
                        } else if let domError {
                            Text(domError)
                                .font(.caption)
                                .foregroundStyle(.orange)
                                .lineLimit(2)
                        }
                    }
                    VibeTurnPanel(
                        project: project,
                        prefill: $vibePrefill,
                        startsExpanded: true,
                        focusRequest: chatFocusRequest,
                        showConsolePopup: $showingConsoleLogs,
                        modelLabel: $selectedModelLabel,
                        modelFocusRequest: modelFocusRequest
                    )
                    Spacer(minLength: 0)
                }
                .padding(18)
                .frame(width: mobileAppLayout ? layout.size.width * 0.58 : 560)
                .frame(maxHeight: .infinity, alignment: .topLeading)
                .focusSection()
            }
            }
            .padding(.horizontal, 34)
            .padding(.vertical, 24)
        }
        .task(id: project.id) {
            guard let client = store.renderClient() ?? store.runnerClient() else {
                runtime.fail("No reachable render machine is selected.")
                return
            }
            await runtime.start(
                client: client,
                project: project,
                // WebRTC is the primary interactive lane on every form factor.
                // The controller already falls back to authenticated frames
                // after a bounded no-pixels watchdog; forcing every phone app
                // straight to polling made the UI claim “interactive WebRTC”
                // while never attempting WebRTC at all.
                preferAuthenticatedFrames: false,
                forcedTargetID: forcedTargetID,
                launchGuest: launchGuest
            )
            // Vibing's primary action is the next prompt. Giving the viewport
            // default focus trapped every arrow in soft-pointer movement and
            // made the visible Runner/Model controls unreachable. App control
            // remains one Left/Select away; Chat owns initial focus.
            streamFocused = false
            overlayInput.deactivate()
            chatFocusRequest += 1
        }
        .onChange(of: domMode) { _, enabled in
            selectedElementSummary = nil
            domError = nil
            Task {
                do {
                    try await runtime.setDOMMode(enabled, project: project)
                } catch {
                    await MainActor.run {
                        domMode = false
                        domError = error.localizedDescription
                    }
                }
            }
        }
        .onChange(of: runtime.textInputFocusRequest) { _, _ in
            // Browser-window taps have no way to summon tvOS' keyboard across
            // WebRTC. The agent measured document.activeElement after the tap,
            // so this opens only for a real editable target.
            showingKeyboard = true
        }
        .onChange(of: streamFocused) { _, focused in
            // A normal Left focus move into the WebRTC hit target starts the
            // overlay input state too; all subsequent arrows remain remote
            // mouse/scroll commands until Back explicitly clears it.
            if focused && !overlayInput.exitHandoffPending {
                overlayInput.enter()
            } else if !focused, overlayInput.isActive, !overlayInput.exitHandoffPending {
                // A consumed overlay arrow must never produce a transient
                // SwiftUI focus hop. Count it so the closed loop catches the
                // blink even if focus is reclaimed before XCTest samples it.
                overlayFocusLosses += 1
                DispatchQueue.main.async { streamFocused = true }
            }
        }
        .onDisappear {
            domHoverTask?.cancel()
            if domMode, let client = store.renderClient() ?? store.runnerClient() {
                Task {
                    _ = try? await client.setPreviewDomMode(
                        project: project.name,
                        enabled: false,
                        workDir: project.path
                    )
                }
            }
            runtime.stop()
        }
        .sheet(isPresented: $showingKeyboard) { keyboardSheet }
        #if os(tvOS)
        .onPlayPauseCommand {
            if domMode {
                selectDOMElement()
            } else {
                mode = mode == .pointer ? .scroll : .pointer
            }
            enterRemoteOverlay()
        }
        .onExitCommand {
            handleVibeExit()
        }
        .onMoveCommand { direction in
            // Handle directional input at the surface level. The transparent
            // child hit target can lose the tvOS focus transaction before its
            // own handler sees Right, which makes the pointer appear stuck at
            // the stream's left/center edge. While the WebRTC surface owns
            // focus, every direction belongs to the remote mouse/scroll plane.
            guard overlayInput.isActive else { return }
            handleStreamMove(direction)
        }
        #endif
        .accessibilityIdentifier("vibing.interactive-webrtc")
    }

    private var mobileAppLayout: Bool {
        let framework = (project.framework ?? "").lowercased()
        return form == .phone && ["expo", "react-native", "reactnative", "rn", "flutter", "kotlin", "android"].contains(framework)
    }

    private var header: some View {
        HStack(spacing: 12) {
            Label("\(project.name) · interactive \(form.rawValue)", systemImage: "iphone.gen3.radiowaves.left.and.right")
                .font(.system(size: 17, weight: .semibold))
                .lineLimit(1)
                .minimumScaleFactor(0.72)
                .padding(.horizontal, 14)
                .padding(.vertical, 9)
                .background(.ultraThinMaterial, in: Capsule())

            if runtime.connected {
                Label(runtime.transportLabel, systemImage: "checkmark.circle.fill")
                    .font(.system(size: 17, weight: .semibold))
                    .foregroundStyle(.green)
                    .lineLimit(1)
                    .minimumScaleFactor(0.72)
                    .accessibilityIdentifier("vibing.runtime-connected")
            }
        }
        .fixedSize(horizontal: true, vertical: false)
    }

    private var streamSurface: some View {
        GeometryReader { geometry in
            let source = runtime.sourceSize
            // Keep focused tvOS content inside the visible stream chrome. A
            // focused Button can grow subtly; fitting to the raw outer bounds
            // let portrait phone pixels cross the rounded frame at top/bottom.
            let safeBounds = CGRect(origin: .zero, size: geometry.size).insetBy(dx: 16, dy: 16)
            let fit = tvRemoteAspectFitRect(imageSize: source, in: safeBounds)
            let phoneLike = source.height > source.width * 1.25
            let mediaCorner: CGFloat = phoneLike ? 30 : 18
            let deviceFrame = fit.insetBy(dx: phoneLike ? -14 : -6, dy: phoneLike ? -14 : -6)

            ZStack {
                RoundedRectangle(cornerRadius: 28)
                    .fill(Color(white: 0.045))
                    .overlay {
                        RoundedRectangle(cornerRadius: 28)
                            .stroke(streamFocused ? Color.accentColor.opacity(0.85) : Color.white.opacity(0.14), lineWidth: streamFocused ? 4 : 2)
                    }

                if form == .phone || runtime.hasMedia {
                    RoundedRectangle(cornerRadius: mediaCorner + 14)
                        .fill(Color.black)
                        .overlay {
                            RoundedRectangle(cornerRadius: mediaCorner + 14)
                                .stroke(Color.white.opacity(0.2), lineWidth: 2)
                        }
                        .shadow(color: .black.opacity(0.8), radius: 18, y: 8)
                        .frame(width: deviceFrame.width, height: deviceFrame.height)
                        .position(x: deviceFrame.midX, y: deviceFrame.midY)
                }

                Group {
                    if let error = runtime.error {
                        runtimeFailurePanel(error)
                    } else {
                        ZStack {
                            if runtime.connected, let track = runtime.videoTrack {
                                RemoteVideoTrackView(track: track)
                            } else if let image = runtime.frame {
                                Image(uiImage: image)
                                    .resizable()
                                    .aspectRatio(contentMode: .fit)
                            } else {
                                VStack(spacing: 16) {
                                    ProgressView().scaleEffect(1.4)
                                    Text(runtime.status).foregroundStyle(.secondary)
                                }
                            }
                        }
                        .frame(width: fit.width, height: fit.height)
                        .clipShape(RoundedRectangle(cornerRadius: mediaCorner))
                        .contentShape(Rectangle())
                        .focusEffectDisabled()
                        .prefersDefaultFocus(true, in: defaultFocus)
                        // A focused UIView/WebRTC surface does not reliably
                        // turn the Siri Remote Select press into SwiftUI's
                        // onTapGesture. Use a real Button as the transparent
                        // hit target so the circle button always reaches the
                        // remote runtime.
                        .overlay {
                            // A transparent Button still receives tvOS' focus
                            // treatment and can wash the entire remote app
                            // white on hover. This is a focus target, not a
                            // button: keep it visually inert and handle the
                            // Select gesture directly.
                            TVRemoteInputCapture(
                                accessibilityLabel: domMode ? "Select remote element" : "Activate remote app",
                                onMove: handleStreamMove,
                                onSelect: activateRemoteSurface,
                                onExit: exitOverlayToVibe
                            )
                            .focusEffectDisabled()
                            .focused($streamFocused)
                        }
                    }
                }
                .frame(width: fit.width, height: fit.height)
                .position(x: fit.midX, y: fit.midY)

                if mode == .pointer, runtime.hasMedia {
                    softCursor(in: fit)
                }

                Color.clear
                    .frame(width: 1, height: 1)
                    .accessibilityElement()
                    .accessibilityIdentifier("vibing.overlay-focus-losses")
                    .accessibilityLabel("Overlay focus losses")
                    .accessibilityValue(String(overlayFocusLosses))

            }
            .clipShape(RoundedRectangle(cornerRadius: 28))
        }
    }

    private func runtimeFailurePanel(_ message: String) -> some View {
        // A scope 403 is deterministic — Retry render would 403 forever and
        // Fix with AI edits a project that is fine. The box's agent predates
        // the TV scope rows; the ONLY route is updating the agent, exactly as
        // DroidStreamView / WebPreviewStreamView already do for this verdict.
        // The code is preserved through TVRemoteRuntimeController.fail; the
        // prose shim covers agents old enough to emit no code at all.
        if runtime.errorCode == FailureSignals.sessionScopeDenied
            || message.contains("scoped token cannot access this endpoint") {
            return AnyView(
                VStack(spacing: 16) {
                    Image(systemName: "arrow.down.circle.dotted")
                        .font(.system(size: 56))
                        .foregroundStyle(.orange)
                    Text("This box needs an agent update")
                        .font(.system(size: 24, weight: .bold))
                    Text(FailureSignals.explainSessionScopeDenied())
                        .font(.system(size: 16))
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .frame(maxWidth: 640)
                    NavigationLink("Update the agent") { UpdateAgentView() }
                        .buttonStyle(.borderedProminent)
                }
                .padding(26)
                .background(Color.black.opacity(0.86), in: RoundedRectangle(cornerRadius: 18))
                .accessibilityIdentifier("vibing.runtime-recovery")
            )
        }
        return AnyView(runtimeRetryPanel(message))
    }

    private func runtimeRetryPanel(_ message: String) -> some View {
        VStack(spacing: 14) {
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 44))
                .foregroundStyle(.orange)
            Text("Render unavailable")
                .font(.system(size: 22, weight: .bold))
            Text(message)
                .font(.system(size: 15))
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 560)
            HStack(spacing: 12) {
                Button("Retry render") { restartRuntime() }
                    .buttonStyle(.borderedProminent)
                Button("Fix with AI") {
                    let request = "The interactive render for \(project.name) failed with this measured error: \(message). Diagnose the app and dev-server logs, fix the project, and restart the preview."
                    // Clear before re-seeding so two consecutive failures with
                    // the same message still trigger Vibing's auto-submit
                    // observer. The next run is deferred one main-loop turn
                    // so SwiftUI observes a real value transition.
                    vibePrefill = ""
                    DispatchQueue.main.async {
                        vibePrefill = request
                    }
                    // Move focus into the same VibeTurnPanel that owns the
                    // task SSE. Without this request the prompt could be
                    // submitted while the recovery card still held focus,
                    // leaving the runner working but no agent-log lane visible.
                    chatFocusRequest += 1
                    streamFocused = false
                    overlayInput.deactivate()
                }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("vibing.fix-with-ai")
            }
        }
        .padding(26)
        .background(Color.black.opacity(0.86), in: RoundedRectangle(cornerRadius: 18))
        .accessibilityIdentifier("vibing.runtime-recovery")
    }

    private func restartRuntime() {
        Task {
            guard let client = store.renderClient() ?? store.runnerClient() else {
                runtime.fail("No reachable render machine is selected.")
                return
            }
            await runtime.start(client: client, project: project, preferAuthenticatedFrames: false)
            enterRemoteOverlay()
        }
    }

    private func reloadRuntime() {
        Task {
            guard let client = store.renderClient() ?? store.runnerClient() else {
                runtime.fail("No reachable machine is selected for reload.")
                return
            }
            runtime.status = "Reloading \(project.name)…"
            do {
                // A phone target needs the Hermes/native bundle lane; browser
                // targets need the dev-server reload lane. Restart the viewer
                // after the operation so a stale/black frame cannot remain on
                // screen while the remote app has already reloaded.
                let mode = form == .phone ? "bundle" : "dev"
                _ = try await client.reload(mode: mode, workDir: project.path)
                await runtime.start(client: client, project: project, preferAuthenticatedFrames: false)
                enterRemoteOverlay()
            } catch {
                runtime.fail(error, prefix: "Reload failed: ")
            }
        }
    }

    private func activateRemoteSurface() {
        enterRemoteOverlay()
        if domMode {
            selectDOMElement()
        } else if mode == .pointer {
            runtime.tap(normalized: cursor)
        } else {
            mode = .pointer
        }
    }

    private func softCursor(in fit: CGRect) -> some View {
        let x = fit.minX + cursor.x * fit.width
        let y = fit.minY + cursor.y * fit.height
        return Image(systemName: domMode ? "scope" : "cursorarrow")
            .font(.system(size: domMode ? 34 : 40, weight: .bold))
            .foregroundStyle(domMode ? Color.purple : Color.white)
            .shadow(color: .black, radius: 2, x: 1, y: 2)
            .shadow(color: .black.opacity(0.9), radius: 6)
        .position(x: x, y: y)
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    private var controlRail: some View {
        HStack(spacing: 4) {
            Menu {
                Button {
                    mode = mode == .pointer ? .scroll : .pointer
                    enterRemoteOverlay()
                } label: {
                    Label(mode == .pointer ? "Use scroll mode" : "Use pointer mode", systemImage: mode.symbol)
                }
                Button { runtime.sendKey("back", action: "back") } label: {
                    Label("Back", systemImage: "chevron.backward")
                }
                Button { runtime.sendKey("home", action: "home") } label: {
                    Label("Home", systemImage: "house")
                }
                if runtime.supportsDOMInspection {
                    Button {
                        domMode.toggle()
                        mode = .pointer
                        enterRemoteOverlay()
                    } label: {
                        Label(domMode ? "Stop inspecting" : "Inspect", systemImage: domMode ? "scope" : "viewfinder")
                    }
                }
                if !runtime.connected || runtime.error != nil {
                    Button { restartRuntime() } label: {
                        Label("Reconnect", systemImage: "arrow.clockwise")
                    }
                }
                Button { reloadRuntime() } label: {
                    Label("Reload app", systemImage: "arrow.triangle.2.circlepath")
                }
            } label: {
                Label("Controls", systemImage: "slider.horizontal.3")
            }
            .accessibilityIdentifier("vibing.controls")

            Button {
                mode = .pointer
                enterRemoteOverlay()
            } label: {
                Label("Mouse mode", systemImage: "cursorarrow.rays")
            }
            .buttonStyle(.bordered)
            .accessibilityIdentifier("vibing.mouse-mode")

            // Text entry is the only app action that must remain one tap away:
            // a headless remote browser cannot summon tvOS' native keyboard
            // when its input gains focus. Keep it beside Controls, focus the
            // local field immediately, then inject into the remote active
            // element on Send.
            Button { showingKeyboard = true } label: {
                Label("Type", systemImage: "keyboard")
            }
            .accessibilityIdentifier("vibing.type")
            Button { showingConsoleLogs = true } label: {
                Label("Console logs", systemImage: "terminal")
            }
            .accessibilityIdentifier("vibing.console-logs")
            Spacer(minLength: 0)
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .font(.system(size: 12, weight: .semibold))
        .lineLimit(1)
        .minimumScaleFactor(0.55)
        .fixedSize(horizontal: false, vertical: true)
        .font(.system(size: 16, weight: .semibold))
        .lineLimit(1)
        .minimumScaleFactor(0.72)
    }

    private var keyboardSheet: some View {
        VStack(alignment: .leading, spacing: 24) {
            Label("Type in the app", systemImage: "keyboard")
                .font(.title2.bold())
            Text("Focus a field with the soft pointer first. Text is sent to that field on the remote phone or browser.")
                .foregroundStyle(.secondary)
            TextField("Text to send", text: $keyboardText)
                .textFieldStyle(.plain)
                .padding(18)
                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 14))
                .focused($keyboardFieldFocused)
                .onSubmit { sendKeyboardText() }
            HStack {
                Button("Cancel") { showingKeyboard = false }
                Spacer()
                Button("Send") { sendKeyboardText() }
                .buttonStyle(.borderedProminent)
                .disabled(keyboardText.isEmpty)
            }
        }
        .padding(60)
        .frame(width: 900, height: 430)
        .onAppear {
            DispatchQueue.main.async { keyboardFieldFocused = true }
        }
    }

    private func sendKeyboardText() {
        let value = keyboardText
        guard !value.isEmpty else { return }
        keyboardText = ""
        showingKeyboard = false
        runtime.sendText(value)
        enterRemoteOverlay()
    }

    private func enterRemoteOverlay() {
        overlayInput.enter()
        streamFocused = true
    }

    private func movePointer(_ direction: MoveCommandDirection) {
        // The WebRTC surface is a modal control plane. Directional presses
        // must never hand focus to the chat rail or leave the overlay; Menu
        // is the only escape hatch (see onExitCommand above).
        enterRemoteOverlay()
        let now = Date()
        if now.timeIntervalSince(lastMoveAt) < 0.24 {
            repeatedMoves = min(repeatedMoves + 1, 7)
        } else {
            repeatedMoves = 0
        }
        lastMoveAt = now
        // Repeated touch-surface moves accelerate, while the first D-pad nudge
        // remains precise enough to hit a phone-sized button from the couch.
        // A Siri Remote directional press is a pointer nudge, not a focus
        // hop. 2.5% was too small on a phone-sized SFMG viewport, making the
        // cursor appear unable to reach the right-hand controls from the
        // couch. Use a visible first nudge and accelerate repeated presses.
        let step = 0.07 + Double(repeatedMoves) * 0.012
        switch direction {
        case .up: cursor.y = max(0, cursor.y - step)
        case .down: cursor.y = min(1, cursor.y + step)
        case .left: cursor.x = max(0, cursor.x - step)
        case .right: cursor.x = min(domCursorMaximumX, cursor.x + step)
        @unknown default: break
        }
        if domMode { sendDOMHover() }
    }

    #if os(tvOS)
    /// Overlay focus is modal: every directional command moves the remote
    /// pointer or scroll plane. Back is the sole exit, so Right at the edge
    /// must remain a mouse move rather than changing SwiftUI focus.
    private func exitOverlayToVibe() {
        handleVibeExit()
    }

    private func handleVibeExit() {
        switch overlayInput.requestExit() {
        case .leaveOverlay:
            // Defer the focus update one event turn. If this focused child
            // handler bubbles the same Menu press to its enclosing Vibe route,
            // the reducer still classifies it as the same overlay handoff.
            DispatchQueue.main.async {
                streamFocused = false
                chatFocusRequest += 1
                overlayInput.completeExitHandoff()
            }
        case .dismissVibing:
            // Outside the overlay, Vibing is a pushed dashboard route.
            dismiss()
        case .ignoreDuplicate:
            break
        }
    }

    private func handleStreamMove(_ direction: MoveCommandDirection) {
        guard overlayInput.claimMove() else { return }
        DispatchQueue.main.async { overlayInput.completeMoveDelivery() }
        // Reassert the transparent overlay target if tvOS tried to recompute
        // focus at an edge; the current arrow still goes to the remote app.
        enterRemoteOverlay()
        switch mode {
        case .pointer:
            movePointer(direction)
        case .scroll:
            runtime.scroll(direction, at: cursor)
        }
    }
    #endif

    private var domCursorMaximumX: CGFloat {
        let width = runtime.sourceSize.width
        // SFMG can deliver its first authenticated frame before metadata has
        // populated sourceSize. A zero/unknown width must not clamp the mouse
        // to x=0 and make every Right press look broken.
        guard width > 1 else { return 1 }
        return min(1, max(0, (width - 1) / width))
    }

    private func sendDOMHover() {
        guard domMode else { return }
        let point = tvRemoteDOMPoint(normalized: cursor, sourceSize: runtime.sourceSize)
        domHoverTask?.cancel()
        domHoverTask = Task {
            try? await Task.sleep(nanoseconds: 90_000_000)
            guard !Task.isCancelled, domMode else { return }
            try? await runtime.moveDOMCursor(point, project: project)
        }
    }

    private func selectDOMElement() {
        guard domMode, !selectingElement else { return }
        selectingElement = true
        domError = nil
        let point = tvRemoteDOMPoint(normalized: cursor, sourceSize: runtime.sourceSize)
        Task {
            do {
                let result = try await runtime.selectDOMElement(point, project: project)
                await MainActor.run {
                    selectingElement = false
                    if result.ok == true, let summary = result.summary, !summary.isEmpty {
                        selectedElementSummary = summary
                        // Keep the selected DOM node as first-class Vibing
                        // context. The panel's prefill hook sends this as a
                        // normal turn, so the runner receives the same
                        // element block as the browser surface.
                        vibePrefill = "Deep audit the selected element: \(summary)"
                        chatFocusRequest += 1
                        streamFocused = false
                        overlayInput.deactivate()
                    } else {
                        domError = "No element at that spot — move the cursor and try again."
                    }
                }
            } catch {
                await MainActor.run {
                    selectingElement = false
                    domError = error.localizedDescription
                }
            }
        }
    }

    private func selectedElementChip(_ summary: String) -> some View {
        HStack(spacing: 10) {
            Text("Element · \(summary)")
                .font(.system(size: 14, weight: .semibold))
                .lineLimit(1)
            Button("Select another") {
                selectedElementSummary = nil
                domError = nil
                domMode = true
                enterRemoteOverlay()
            }
            .buttonStyle(.bordered)
            Button("Done") {
                selectedElementSummary = nil
                domMode = false
            }
                .buttonStyle(.bordered)
        }
        .lineLimit(1)
        .padding(10)
        .background(Color.white.opacity(0.06), in: RoundedRectangle(cornerRadius: 14))
    }

}

/// Aspect-fit in a real bounded rect, not an origin-zero size. This keeps a
/// portrait app centered inside the TV's rounded stream chrome and makes the
/// containment invariant directly testable without pixels or LiveKit.
func tvRemoteAspectFitRect(imageSize: CGSize, in container: CGRect) -> CGRect {
        guard imageSize.width > 0, imageSize.height > 0,
              container.width > 0, container.height > 0 else {
            return container
        }
        let scale = min(container.width / imageSize.width, container.height / imageSize.height)
        let size = CGSize(width: imageSize.width * scale, height: imageSize.height * scale)
        return CGRect(
            x: container.minX + (container.width - size.width) / 2,
            y: container.minY + (container.height - size.height) / 2,
            width: size.width,
            height: size.height
        )
}

/// DOM controls exist only for the browser-window target. A native simulator
/// can stream pixels over WebRTC, but it has no browser DOM to inspect.
func tvRemoteDOMInspectionAvailable(targetId: String?) -> Bool {
    targetId == "browser-window"
}

/// Convert the normalized TV cursor into the captured browser viewport. Keep
/// this shared by hover and select so the highlight and chosen element cannot
/// drift apart at letterboxed phone aspect ratios.
func tvRemoteDOMPoint(normalized: CGPoint, sourceSize: CGSize) -> CGPoint {
    let x = min(max(normalized.x, 0), 1)
    let y = min(max(normalized.y, 0), 1)
    // CDP coordinates are inside the viewport, not on its exclusive bottom or
    // right edge. A normalized cursor of exactly 1 otherwise sends x=width,
    // which is outside the DOM and makes rightward inspection appear to leave
    // the preview.
    let maxX = max(sourceSize.width - 1, 0)
    let maxY = max(sourceSize.height - 1, 0)
    return CGPoint(
        x: min((x * maxX).rounded(), maxX),
        y: min((y * maxY).rounded(), maxY)
    )
}

/// A transport is not healthy merely because it decoded a JPEG. Browser and
/// simulator capture can return valid, uniformly black/white images while the
/// app failed before first paint. Sample a tiny luminance grid so this check is
/// cheap enough for the live lane; the pure predicate below is unit-testable.
func tvRemoteImageLooksBlank(_ image: UIImage) -> Bool {
    guard let source = image.cgImage else { return false }
    let width = 12
    let height = 12
    var pixels = [UInt8](repeating: 0, count: width * height * 4)
    guard let context = CGContext(
        data: &pixels,
        width: width,
        height: height,
        bitsPerComponent: 8,
        bytesPerRow: width * 4,
        space: CGColorSpaceCreateDeviceRGB(),
        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
    ) else { return false }
    context.interpolationQuality = .low
    context.draw(source, in: CGRect(x: 0, y: 0, width: width, height: height))
    var luminances: [UInt8] = []
    luminances.reserveCapacity(width * height)
    for index in stride(from: 0, to: pixels.count, by: 4) {
        let value = (Int(pixels[index]) * 299
            + Int(pixels[index + 1]) * 587
            + Int(pixels[index + 2]) * 114) / 1000
        luminances.append(UInt8(clamping: value))
    }
    return tvRemoteFrameSamplesAreBlank(luminances)
}

func tvRemoteFrameSamplesAreBlank(_ samples: [UInt8]) -> Bool {
    guard let darkest = samples.min(), let brightest = samples.max() else { return false }
    let average = samples.reduce(0) { $0 + Int($1) } / samples.count
    let nearlyUniform = Int(brightest) - Int(darkest) <= 6
    return nearlyUniform && (average <= 12 || average >= 243)
}

private struct RemoteVideoTrackView: UIViewRepresentable {
    let track: LKRTCVideoTrack

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> LKRTCMTLVideoView {
        let view = LKRTCMTLVideoView(frame: .zero)
        view.videoContentMode = .scaleAspectFit
        view.isEnabled = true
        // Metal-backed video views default to a white backing surface on
        // tvOS. During the mobile overlay handoff there can be a frame or two
        // before the guest paints; keep that transient state inside the
        // preview's black frame instead of flashing a full white panel.
        view.backgroundColor = .black
        view.isOpaque = true
        context.coordinator.attach(track, to: view)
        return view
    }

    func updateUIView(_ view: LKRTCMTLVideoView, context: Context) {
        context.coordinator.attach(track, to: view)
    }

    static func dismantleUIView(_ view: LKRTCMTLVideoView, coordinator: Coordinator) {
        coordinator.detach(from: view)
    }

    final class Coordinator {
        private var track: LKRTCVideoTrack?

        func attach(_ next: LKRTCVideoTrack, to view: LKRTCMTLVideoView) {
            guard track !== next else { return }
            if let track { track.remove(view) }
            track = next
            next.add(view)
        }

        func detach(from view: LKRTCMTLVideoView) {
            track?.remove(view)
            track = nil
        }
    }
}

@MainActor
private final class TVRemoteRuntimeController: NSObject, ObservableObject {
    @Published var frame: UIImage?
    @Published var videoTrack: LKRTCVideoTrack?
    @Published var session: RemoteRuntimeSession?
    @Published var status = "Preparing the remote app…"
    @Published var transportLabel = "WebRTC"
    @Published var connected = false
    @Published var error: String?
    /// Stable reason code from the agent's refusal (`code` key —
    /// reason_codes.go vocabulary, e.g. auth.session.scope_denied), preserved
    /// through `fail` so the view can route a deterministic verdict instead of
    /// regexing prose. nil for transport failures and old agents.
    @Published var errorCode: String?
    @Published var controlNote: String?
    @Published private(set) var textInputFocusRequest = 0
    @Published private(set) var receivedUsableFrame = false

    var hasMedia: Bool { frame != nil || videoTrack != nil }
    var supportsDOMInspection: Bool {
        tvRemoteDOMInspectionAvailable(targetId: session?.targetId)
    }
    var sourceSize: CGSize {
        if let dims = session?.deviceDims, dims.width > 0, dims.height > 0 {
            return CGSize(width: dims.width, height: dims.height)
        }
        if let viewport = session?.viewport, viewport.width > 0, viewport.height > 0 {
            return CGSize(width: viewport.width, height: viewport.height)
        }
        if let frame { return frame.size }
        return CGSize(width: 393, height: 852)
    }

    private struct JPEGChunks {
        let total: Int
        var parts: [String?]
    }

    private let factory = LKRTCPeerConnectionFactory()
    private let clientId = TVRemoteRuntimeController.loadClientId()
    private var client: AgentClient?
    private var peer: LKRTCPeerConnection?
    private var primerChannel: LKRTCDataChannel?
    private var eventsChannel: LKRTCDataChannel?
    private var framesChannel: LKRTCDataChannel?
    private var project: ProjectSummary?
    private var iceContinuation: CheckedContinuation<Void, Never>?
    private var iceTimeoutTask: Task<Void, Never>?
    private var fallbackTask: Task<Void, Never>?
    private var watchdogTask: Task<Void, Never>?
    private var heartbeatTask: Task<Void, Never>?
    private var jpegChunks: [String: JPEGChunks] = [:]
    private var generation = UUID()
    private var frameFallbackForced = false
    private var webrtcICEReady = false
    private var blankFrameStartedAt: Date?
    private var vibingCapabilities = Set<String>()
    private var pendingVibingAcks: [String: CheckedContinuation<[String: Any], Error>] = [:]

    func fail(_ message: String, code: String? = nil) {
        error = message
        errorCode = code
        status = "Interactive runtime unavailable"
    }

    /// Preserve the agent's structured refusal (`code`, `gap`, relay flag)
    /// instead of flattening to `localizedDescription`. A scope/relay denial
    /// is deterministic and must reach the view as itself; prose forces a
    /// regex nobody keeps in sync (AGENTS.md "SIGNAL — structured and named").
    func fail(_ failure: Error, prefix: String = "") {
        if let agentError = failure as? AgentErrorCoded {
            fail(prefix.isEmpty ? agentError.message : "\(prefix)\(agentError.message)",
                 code: agentError.code)
        } else {
            fail(prefix.isEmpty ? failure.localizedDescription : "\(prefix)\(failure.localizedDescription)")
        }
    }

    func start(
        client: AgentClient,
        project: ProjectSummary,
        preferAuthenticatedFrames: Bool = false,
        forcedTargetID: String? = nil,
        launchGuest: Bool = false
    ) async {
        stop(closeSession: true)
        let thisGeneration = UUID()
        generation = thisGeneration
        self.client = client
        self.project = project
        // Preserve the last good pixels while transport renegotiates. A new
        // frame replaces them only after blank-frame validation succeeds.
        videoTrack = nil
        session = nil
        error = nil
        errorCode = nil
        controlNote = nil
        connected = false
        transportLabel = "WebRTC"
        frameFallbackForced = false
        webrtcICEReady = false
        receivedUsableFrame = false
        blankFrameStartedAt = nil
        vibingCapabilities.removeAll()

        do {
            status = "Starting \(project.name)…"
            if forcedTargetID == nil {
                try await prepareBrowserLaneIfNeeded(client: client, project: project)
            }
            guard generation == thisGeneration else { return }

            status = "Finding an interactive target…"
            let capabilities = try await client.remoteRuntimeCapabilities(for: project, refresh: true)
            guard capabilities.remoteRuntimeEligible else {
                throw AgentError(message: "This project does not expose an interactive remote runtime.")
            }
            guard let target = preferredTarget(in: capabilities.targets, forcedTargetID: forcedTargetID) else {
                if let forcedTargetID {
                    let known = capabilities.targets.first(where: { $0.id == forcedTargetID })
                    throw AgentError(message: known?.reason ?? "The selected render box did not expose the required \(forcedTargetID) target.")
                }
                let reason = capabilities.targets.first?.reason ?? "No compatible phone, simulator, device, or browser target is available on this machine."
                throw AgentError(message: reason)
            }

            // REJOIN FIRST — the vibe-room contract. If a live session for this
            // project already exists on the box (started on the phone, web, or
            // another TV), attach to it instead of creating a second capture.
            // This is what makes "left it on the TV, kept vibing from the car,
            // came home and the TV shows the same room" work: the roster is the
            // box's answer to "is this project already live?"
            status = "Looking for a live session on this machine…"
            var created: RemoteRuntimeSession
            let live = (try? await client.listRemoteRuntimeSessions(project: project.path)) ?? []
            if let existing = live.first(where: {
                $0.status != "closed" && $0.status != "error" &&
                    (forcedTargetID == nil || $0.targetId == forcedTargetID)
            }) {
                status = "Rejoining the live \(project.name) session…"
                created = existing
            } else {
                status = "Opening \(target.label)…"
                created = try await client.startRemoteRuntimeSession(
                    for: project,
                    targetId: target.id,
                    transportMode: preferAuthenticatedFrames ? "relay-jpeg-poll" : "direct-webrtc",
                    // Attribute the creator in the shared-session roster: the
                    // TV's stable installation id + its surface, so a phone or
                    // web viewer can see "TV started this room" and a returning
                    // TV can find its own room by id.
                    clientId: TokenStore.installationID(),
                    surface: Backend.surface
                )
            }
            guard generation == thisGeneration else {
                try? await client.closeRemoteRuntimeSession(created.id)
                return
            }
            session = created
            if let note = created.note { controlNote = note }
            if launchGuest {
                status = "Building and launching \(project.name) in the Apple TV Simulator…"
                _ = try await client.sendRemoteRuntimeCommand(
                    sessionId: created.id,
                    command: "run-guest",
                    source: "tvos-dogfood",
                    workDir: project.path
                )
            }
            if preferAuthenticatedFrames {
                // Phone previews must not put an H.264 surface on screen before
                // it has produced usable app pixels. The authenticated frame
                // lane is the same fallback used after a failed WebRTC probe,
                // but selecting it up front prevents a persistent black mobile
                // overlay (observed with SFMG on tvOS, 2026-08-20).
                frameFallbackForced = true
                status = "Opening the authenticated mobile viewport…"
                transportLabel = "Authenticated frames"
                startFrameFallback(sessionId: created.id, generation: thisGeneration)
                return
            }
            status = "Negotiating WebRTC…"
            try await negotiate(client: client, session: created)
            guard generation == thisGeneration else { return }

            watchdogTask = Task { [weak self] in
                // Do not leave a black H.264 surface on screen for eight
                // seconds. If no usable pixels have arrived, hand off to the
                // authenticated frame lane quickly; the last good frame (if
                // any) remains visible during the handoff.
                try? await Task.sleep(nanoseconds: 4_000_000_000)
                guard !Task.isCancelled, let self, self.generation == thisGeneration, !self.receivedUsableFrame else { return }
                self.activateFrameFallback("WebRTC did not deliver a usable viewport in time. Using authenticated browser frames instead.")
            }
        } catch is CancellationError {
            return
        } catch {
            guard generation == thisGeneration else { return }
            fail(error)
        }
    }

    func stop() { stop(closeSession: true) }

    private func stop(closeSession: Bool) {
        generation = UUID()
        iceTimeoutTask?.cancel()
        fallbackTask?.cancel()
        watchdogTask?.cancel()
        heartbeatTask?.cancel()
        iceTimeoutTask = nil
        fallbackTask = nil
        watchdogTask = nil
        heartbeatTask = nil
        iceContinuation?.resume()
        iceContinuation = nil
        eventsChannel?.close()
        framesChannel?.close()
        primerChannel?.close()
        peer?.close()
        peer = nil
        eventsChannel = nil
        framesChannel = nil
        primerChannel = nil
        jpegChunks.removeAll()
        let pending = pendingVibingAcks.values
        pendingVibingAcks.removeAll()
        for continuation in pending {
            continuation.resume(throwing: AgentError(message: "The WebRTC control channel closed."))
        }
        let id = session?.id
        let client = self.client
        session = nil
        if closeSession, let id, let client {
            Task { try? await client.closeRemoteRuntimeSession(id) }
        }
    }

    func tap(normalized point: CGPoint) {
        let size = sourceSize
        sendControl(
            action: "tap",
            x: Int((point.x * size.width).rounded()),
            y: Int((point.y * size.height).rounded())
        )
    }

    func scroll(_ direction: MoveCommandDirection, at normalizedPoint: CGPoint? = nil) {
        let size = sourceSize
        let anchorX = Int(size.width * min(max(normalizedPoint?.x ?? 0.5, 0), 1))
        let anchorY = Int(size.height * min(max(normalizedPoint?.y ?? 0.5, 0), 1))
        let dx = Int(size.width * 0.28)
        let dy = Int(size.height * 0.28)
        // Anchor the gesture under the pointer. This matters for nested chat
        // and log scrollers: a wheel/swipe at the frame centre can miss the
        // scroll container currently under the pointer. Clamp the endpoints
        // so edge positions (top/bottom of the phone) remain valid gestures.
        let clampX = { (value: Int) in min(max(value, 0), Int(size.width)) }
        let clampY = { (value: Int) in min(max(value, 0), Int(size.height)) }
        let centerX = clampX(anchorX)
        let centerY = clampY(anchorY)
        // The browser target translates y1 - y2 into wheel deltaY. Positive
        // deltaY is downward page movement, so Down uses the lower-to-upper
        // gesture and Up uses the opposite polarity.
        let upStartY = clampY(centerY - dy)
        let upEndY = clampY(centerY + dy)
        let downStartY = clampY(centerY + dy)
        let downEndY = clampY(centerY - dy)
        switch direction {
        case .up:
            sendControl(action: "swipe", x: centerX, y: upStartY, x2: centerX, y2: upEndY, durationMs: 260)
        case .down:
            sendControl(action: "swipe", x: centerX, y: downStartY, x2: centerX, y2: downEndY, durationMs: 260)
        case .left:
            sendControl(action: "swipe", x: clampX(centerX + dx), y: centerY, x2: clampX(centerX - dx), y2: centerY, durationMs: 260)
        case .right:
            sendControl(action: "swipe", x: clampX(centerX - dx), y: centerY, x2: clampX(centerX + dx), y2: centerY, durationMs: 260)
        @unknown default: break
        }
    }

    func sendText(_ text: String) {
        guard !text.isEmpty else { return }
        sendControl(action: "text", text: text)
    }

    func sendKey(_ key: String, action: String = "key") {
        sendControl(action: action, key: action == "key" ? key : nil)
    }

    func setDOMMode(_ enabled: Bool, project: ProjectSummary) async throws {
        guard supportsDOMInspection else {
            throw AgentError(message: "Element inspection is available for browser-window previews; this target only exposes pixels.")
        }
        let body: [String: Any] = [
            "project": project.name,
            "workDir": project.path ?? "",
            "enabled": enabled,
        ]
        if canSendVibingControl("vibing.dom.mode") {
            _ = try await sendVibingControl(type: "vibing.dom.mode", body: body)
        } else if let client {
            _ = try await client.setPreviewDomMode(project: project.name, enabled: enabled, workDir: project.path)
        } else {
            throw AgentError(message: "No authenticated render connection is available for element inspection.")
        }
    }

    func moveDOMCursor(_ point: CGPoint, project: ProjectSummary) async throws {
        let body: [String: Any] = [
            "project": project.name,
            "x": Int(point.x),
            "y": Int(point.y),
        ]
        if canSendVibingControl("vibing.dom.cursor") {
            _ = try await sendVibingControl(type: "vibing.dom.cursor", body: body)
        } else if let client {
            try await client.movePreviewCursor(project: project.name, x: Int(point.x), y: Int(point.y))
        }
    }

    func selectDOMElement(_ point: CGPoint, project: ProjectSummary) async throws -> AgentClient.PreviewSelectResult {
        let body: [String: Any] = [
            "project": project.name,
            "workDir": project.path ?? "",
            "x": Int(point.x),
            "y": Int(point.y),
        ]
        if canSendVibingControl("vibing.dom.select") {
            var result = try await sendVibingControl(type: "vibing.dom.select", body: body)
            result["ok"] = true
            let data = try JSONSerialization.data(withJSONObject: result)
            return try JSONDecoder().decode(AgentClient.PreviewSelectResult.self, from: data)
        }
        guard let client else {
            throw AgentError(message: "No authenticated render connection is available for element inspection.")
        }
        return try await client.selectPreviewElement(
            project: project.name,
            x: Int(point.x),
            y: Int(point.y),
            workDir: project.path
        )
    }

    private func canSendVibingControl(_ type: String) -> Bool {
        vibingCapabilities.contains(type) && eventsChannel?.readyState == .open
    }

    /// Send DOM control over the reliable, ordered WebRTC events channel. HTTP
    /// remains the negotiated fallback only when the channel/capability is not
    /// available; after a send starts we never retry through HTTP because a
    /// timed-out select may already have clicked and stored the element.
    private func sendVibingControl(type: String, body: [String: Any]) async throws -> [String: Any] {
        guard let channel = eventsChannel, channel.readyState == .open else {
            throw AgentError(message: "The WebRTC DOM control channel is not open.")
        }
        let id = "tvos-\(UUID().uuidString.lowercased())"
        var payload = body
        payload["v"] = 1
        payload["id"] = id
        payload["type"] = type
        let data = try JSONSerialization.data(withJSONObject: payload)
        return try await withCheckedThrowingContinuation { continuation in
            pendingVibingAcks[id] = continuation
            let sent = channel.sendData(LKRTCDataBuffer(data: data, isBinary: false))
            guard sent else {
                pendingVibingAcks.removeValue(forKey: id)
                continuation.resume(throwing: AgentError(message: "The WebRTC DOM control channel rejected the command."))
                return
            }
            Task { @MainActor [weak self] in
                try? await Task.sleep(nanoseconds: 11_000_000_000)
                guard let self, let waiting = self.pendingVibingAcks.removeValue(forKey: id) else { return }
                waiting.resume(throwing: AgentError(message: "Element inspection timed out after 10 seconds. Retry when the browser preview is responsive."))
            }
        }
    }

    private func sendControl(
        action: String,
        x: Int? = nil,
        y: Int? = nil,
        x2: Int? = nil,
        y2: Int? = nil,
        durationMs: Int? = nil,
        text: String? = nil,
        key: String? = nil
    ) {
        guard let client, let id = session?.id else { return }
        Task {
            do {
                let updated = try await client.sendRemoteRuntimeControl(
                    sessionId: id,
                    action: action,
                    x: x,
                    y: y,
                    x2: x2,
                    y2: y2,
                    durationMs: durationMs,
                    text: text,
                    key: key,
                    clientId: clientId
                )
                session = updated
                controlNote = updated.note
                if action == "tap", updated.textInputFocused == true {
                    textInputFocusRequest &+= 1
                }
            } catch {
                controlNote = error.localizedDescription
            }
        }
    }

    private func prepareBrowserLaneIfNeeded(client: AgentClient, project: ProjectSummary) async throws {
        let framework = (project.framework ?? "").lowercased()
        let webCapable = project.kind == .web || ["expo", "react-native", "reactnative", "rn", "flutter", "vite", "react", "next", "nextjs"].contains(framework)
        guard webCapable else { return }
        var current = try await client.startDevServer(for: project)
        let deadline = Date().addingTimeInterval(150)
        while !Task.isCancelled {
            if let failure = current.error?.trimmingCharacters(in: .whitespacesAndNewlines), !failure.isEmpty {
                throw AgentError(message: failure)
            }
            let selectedProjectIsActive = tvRuntimeWorkDirMatchesProject(
                active: current.workDir,
                selected: project.path
            )
            if selectedProjectIsActive,
               (current.serving == true || (current.running == true && current.building != true)) {
                break
            }
            guard Date() < deadline else {
                throw AgentError(message: "The \(project.name) dev server did not become ready within 2½ minutes.")
            }
            status = current.servingLabel?.isEmpty == false ? current.servingLabel! : "Starting \(project.name)…"
            try await Task.sleep(nanoseconds: 600_000_000)
            current = try await client.devServerStatus()
        }
        guard tvRuntimeWorkDirMatchesProject(active: current.workDir, selected: project.path) else {
            throw AgentError(message: "The render machine did not switch its dev server to \(project.name). It is still serving another checkout.")
        }
        if ["expo", "react-native", "reactnative", "rn"].contains(framework) {
            // Modern agents start the Expo web sibling as part of /dev/start
            // when platform=web and report its real port in /dev/status. Calling
            // the legacy start route anyway turned a healthy launch into a 404
            // on older route sets and left the TV showing Expo's spinner. Only
            // invoke the compatibility route when the measured operation says
            // no web sibling is listening yet.
            if (current.webPort ?? 0) <= 0 {
                status = "Starting the mobile web runtime…"
                _ = try await client.startWebServer()
            }

            // The start endpoints are admission calls: a returned port is not
            // proof that Expo is accepting HTTP yet. Do not create the
            // browser-window session until the actual web lane is reported;
            // otherwise chromedp navigates once into a transient blank/503
            // page and never retries, leaving SFMG looking connected on tvOS
            // while showing no app pixels.
            let webDeadline = Date().addingTimeInterval(150)
            while !Task.isCancelled && Date() < webDeadline {
                current = try await client.devServerStatus()
                if let failure = current.error?.trimmingCharacters(in: .whitespacesAndNewlines), !failure.isEmpty {
                    throw AgentError(message: failure)
                }
                if (current.webPort ?? 0) > 0 && current.serving != false { break }
                status = current.servingLabel?.isEmpty == false
                    ? current.servingLabel!
                    : "Waiting for the mobile web runtime…"
                try await Task.sleep(nanoseconds: 600_000_000)
            }
            guard (current.webPort ?? 0) > 0 else {
                throw AgentError(message: "The \(project.name) mobile web runtime did not open a listening port within 2½ minutes.")
            }
        }
    }

    private func preferredTarget(in targets: [RemoteRuntimeTarget], forcedTargetID: String? = nil) -> RemoteRuntimeTarget? {
        let enabled = targets.filter(\.enabled)
        if let forcedTargetID {
            return enabled.first(where: { $0.id == forcedTargetID })
        }
        return enabled.first(where: { $0.id == "browser-window" })
            ?? enabled.first(where: { $0.id == "ios-simulator" || $0.id == "android-emulator" })
            ?? enabled.first
    }

    private func negotiate(client: AgentClient, session: RemoteRuntimeSession) async throws {
        let credentials = try? await client.remoteRuntimeICECredentials()
        let configuration = LKRTCConfiguration()
        configuration.sdpSemantics = .unifiedPlan
        let servers = (credentials?.iceServers ?? []).compactMap { server -> LKRTCIceServer? in
            guard !server.urls.isEmpty else { return nil }
            return LKRTCIceServer(urlStrings: server.urls, username: server.username, credential: server.credential)
        }
        configuration.iceServers = servers.isEmpty
            ? [LKRTCIceServer(urlStrings: ["stun:stun.l.google.com:19302"])]
            : servers
        let constraints = LKRTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil)
        guard let peer = factory.peerConnection(with: configuration, constraints: constraints, delegate: self) else {
            throw AgentError(message: "tvOS could not create a WebRTC peer connection.")
        }
        self.peer = peer
        let primerConfig = LKRTCDataChannelConfiguration()
        primerChannel = peer.dataChannel(forLabel: "primer", configuration: primerConfig)
        let receive = LKRTCRtpTransceiverInit()
        receive.direction = .recvOnly
        guard peer.addTransceiver(of: .video, init: receive) != nil else {
            throw AgentError(message: "tvOS could not request the remote video track.")
        }

        let offer = try await createOffer(peer: peer, constraints: constraints)
        guard offer.sdp.contains("m=video") else {
            throw AgentError(message: "The tvOS WebRTC offer did not include a video lane.")
        }
        try await setLocal(offer, peer: peer)
        await waitForIceGathering(peer)
        guard let local = peer.localDescription else {
            throw AgentError(message: "tvOS did not produce a local WebRTC offer.")
        }
        let answer = try await client.answerRemoteRuntimeWebRTC(sessionId: session.id, offerSDP: local.sdp)
        self.session = answer.session
        if let transport = answer.transport { transportLabel = label(for: transport) }
        if let note = answer.note { controlNote = note }
        guard let answerSDP = answer.answer.sdp, !answerSDP.isEmpty else {
            throw AgentError(message: "The remote runtime returned an empty WebRTC answer.")
        }
        try await setRemote(LKRTCSessionDescription(type: .answer, sdp: answerSDP), peer: peer)
    }

    private func createOffer(peer: LKRTCPeerConnection, constraints: LKRTCMediaConstraints) async throws -> LKRTCSessionDescription {
        try await withCheckedThrowingContinuation { continuation in
            peer.offer(for: constraints) { offer, error in
                if let error { continuation.resume(throwing: error) }
                else if let offer { continuation.resume(returning: offer) }
                else { continuation.resume(throwing: AgentError(message: "WebRTC offer creation returned no SDP.")) }
            }
        }
    }

    private func setLocal(_ description: LKRTCSessionDescription, peer: LKRTCPeerConnection) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            peer.setLocalDescription(description) { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume(returning: ()) }
            }
        }
    }

    private func setRemote(_ description: LKRTCSessionDescription, peer: LKRTCPeerConnection) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            peer.setRemoteDescription(description) { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume(returning: ()) }
            }
        }
    }

    private func waitForIceGathering(_ peer: LKRTCPeerConnection) async {
        guard peer.iceGatheringState != .complete else { return }
        await withCheckedContinuation { continuation in
            iceContinuation = continuation
            iceTimeoutTask?.cancel()
            iceTimeoutTask = Task { [weak self] in
                try? await Task.sleep(nanoseconds: 3_000_000_000)
                guard !Task.isCancelled, let self else { return }
                self.finishIceWait()
            }
        }
    }

    private func finishIceWait() {
        iceTimeoutTask?.cancel()
        iceTimeoutTask = nil
        iceContinuation?.resume()
        iceContinuation = nil
    }

    private func startFrameFallback(sessionId: String, generation: UUID) {
        guard fallbackTask == nil else { return }
        fallbackTask = Task { [weak self] in
            let blankDeadline = Date().addingTimeInterval(8)
            while !Task.isCancelled {
                guard let self, self.generation == generation, let client = self.client else { return }
                do {
                    let data = try await client.remoteRuntimeFrame(sessionId: sessionId)
                    if let image = UIImage(data: data) {
                        let usable = self.acceptFrame(image, transport: "Authenticated frames")
                        if !usable, Date() >= blankDeadline {
                            self.fail("The render transport connected, but the app returned only blank frames. The browser or mobile runtime did not paint usable pixels. Open App console for the underlying dev-server error, then retry or use Fix with AI.")
                            return
                        }
                    }
                } catch {
                    if self.frame == nil { self.controlNote = error.localizedDescription }
                }
                try? await Task.sleep(nanoseconds: 850_000_000)
            }
        }
    }

    private func activateFrameFallback(_ reason: String) {
        guard let session else {
            fail(reason)
            return
        }
        // A receiver object can exist before ICE ever carries a pixel. Keeping
        // that empty H.264 view above a healthy HTTP frame fallback produced a
        // permanent white phone with a green Connected badge.
        videoTrack = nil
        if !receivedUsableFrame { frame = nil }
        frameFallbackForced = true
        webrtcICEReady = false
        connected = false
        status = "Switching to the browser viewport…"
        transportLabel = "Authenticated frames"
        controlNote = reason
        startFrameFallback(sessionId: session.id, generation: generation)
    }

    private func acceptJPEG(_ data: Data, fromWebRTC: Bool) {
        guard let image = UIImage(data: data) else {
            controlNote = "A remote frame arrived but tvOS could not decode it."
            return
        }
        let usable = acceptFrame(image, transport: fromWebRTC ? "WebRTC · JPEG" : transportLabel)
        if fromWebRTC {
            transportLabel = "WebRTC · JPEG"
            if usable {
                fallbackTask?.cancel()
                fallbackTask = nil
            }
        }
    }

    @discardableResult
    private func acceptFrame(_ image: UIImage, transport: String) -> Bool {
        guard !tvRemoteImageLooksBlank(image) else {
            if blankFrameStartedAt == nil { blankFrameStartedAt = Date() }
            connected = false
            status = "Connected, waiting for usable app pixels…"
            controlNote = "The capture operation is returning uniformly blank frames."
            return false
        }
        blankFrameStartedAt = nil
        receivedUsableFrame = true
        frame = image
        connected = true
        error = nil
        status = "Interactive stream ready"
        transportLabel = transport
        return true
    }

    private func handleData(_ data: Data, binary: Bool, channelLabel: String) {
        if channelLabel == "frames" {
            if binary {
                acceptJPEG(data, fromWebRTC: true)
                return
            }
            guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  object["type"] as? String == "jpeg-chunk",
                  let id = object["id"] as? String,
                  let index = object["index"] as? Int,
                  let total = object["total"] as? Int,
                  let encoded = object["data"] as? String,
                  index >= 0, total > 0, index < total else { return }
            var chunk = jpegChunks[id] ?? JPEGChunks(total: total, parts: Array(repeating: nil, count: total))
            guard chunk.total == total, chunk.parts.count == total else {
                jpegChunks.removeValue(forKey: id)
                return
            }
            chunk.parts[index] = encoded
            jpegChunks[id] = chunk
            guard chunk.parts.allSatisfy({ $0 != nil }) else { return }
            jpegChunks.removeValue(forKey: id)
            if let joined = Data(base64Encoded: chunk.parts.compactMap { $0 }.joined()) {
                acceptJPEG(joined, fromWebRTC: true)
            }
            return
        }

        guard channelLabel == "events",
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }
        switch object["type"] as? String {
        case "vibing.protocol":
            guard object["v"] as? Int == 1 else { break }
            vibingCapabilities = Set(object["capabilities"] as? [String] ?? [])
        case "vibing.ack":
            guard let id = object["id"] as? String,
                  let continuation = pendingVibingAcks.removeValue(forKey: id) else { break }
            if object["ok"] as? Bool == true {
                continuation.resume(returning: object["result"] as? [String: Any] ?? [:])
            } else {
                let failure = object["error"] as? [String: Any]
                let message = failure?["message"] as? String ?? "The browser rejected the element-inspection command."
                continuation.resume(throwing: AgentError(message: message))
            }
        case "ready":
            if let transport = object["transport"] as? String { transportLabel = label(for: transport) }
        case "dims", "rotation":
            guard let current = session,
                  let width = object["width"] as? Int,
                  let height = object["height"] as? Int else { break }
            session = RemoteRuntimeSession(
                id: current.id,
                workDir: current.workDir,
                framework: current.framework,
                targetId: current.targetId,
                targetLabel: current.targetLabel,
                platform: current.platform,
                deviceId: current.deviceId,
                displaySurface: current.displaySurface,
                viewport: current.viewport,
                transportMode: current.transportMode,
                frameTransport: current.frameTransport,
                status: current.status,
                lastCommand: current.lastCommand,
                textInputFocused: current.textInputFocused,
                note: current.note,
                deviceDims: RemoteRuntimeDeviceDims(
                    width: width,
                    height: height,
                    scale: object["scale"] as? Double,
                    rotation: object["rotation"] as? String
                ),
                viewerCount: current.viewerCount,
                startedBy: current.startedBy,
                sourceSurface: current.sourceSurface
            )
        case "taken-over":
            controlNote = "Another viewer took over this remote session."
            peer?.close()
        case "frame-error":
            if let message = object["error"] as? String { controlNote = message }
        default:
            if let message = object["error"] as? String { controlNote = message }
        }
    }

    private func label(for transport: String) -> String {
        if transport.hasPrefix("webrtc-rtp-h264") { return "WebRTC · H.264" }
        if transport.hasPrefix("webrtc-datachannel-jpeg") { return "WebRTC · JPEG" }
        if transport.hasPrefix("relay-jpeg-poll") { return "Relay · JPEG" }
        return transport
    }

    private func attachVideo(_ track: LKRTCVideoTrack) {
        guard !frameFallbackForced else { return }
        videoTrack = track
        status = "Connecting the live video…"
        transportLabel = "WebRTC · H.264"
        markWebRTCConnected()
    }

    private func markWebRTCConnected() {
        guard !frameFallbackForced, webrtcICEReady, videoTrack != nil else { return }
        connected = true
        status = "Interactive stream ready"
        controlNote = nil
        fallbackTask?.cancel()
        fallbackTask = nil
    }

    private func beginHeartbeat() {
        guard heartbeatTask == nil else { return }
        heartbeatTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                guard !Task.isCancelled, let self, let channel = self.eventsChannel,
                      channel.readyState == .open else { continue }
                let data = try? JSONSerialization.data(withJSONObject: [
                    "type": "ping",
                    "ts": Int(Date().timeIntervalSince1970 * 1000),
                ])
                if let data { _ = channel.sendData(LKRTCDataBuffer(data: data, isBinary: false)) }
            }
        }
    }

    private static func loadClientId() -> String {
        let key = "yaver.tv.remote-runtime.client-id"
        if let existing = UserDefaults.standard.string(forKey: key), !existing.isEmpty { return existing }
        let created = "tvos-\(UUID().uuidString.lowercased())"
        UserDefaults.standard.set(created, forKey: key)
        return created
    }
}

extension TVRemoteRuntimeController: LKRTCPeerConnectionDelegate {
    nonisolated func peerConnection(_ peerConnection: LKRTCPeerConnection, didChange stateChanged: LKRTCSignalingState) {}

    nonisolated func peerConnection(_ peerConnection: LKRTCPeerConnection, didAdd stream: LKRTCMediaStream) {
        guard let track = stream.videoTracks.first else { return }
        Task { @MainActor [weak self] in self?.attachVideo(track) }
    }

    nonisolated func peerConnection(_ peerConnection: LKRTCPeerConnection, didRemove stream: LKRTCMediaStream) {}
    nonisolated func peerConnectionShouldNegotiate(_ peerConnection: LKRTCPeerConnection) {}

    nonisolated func peerConnection(_ peerConnection: LKRTCPeerConnection, didChange newState: LKRTCIceConnectionState) {
        Task { @MainActor [weak self] in
            guard let self else { return }
            if newState == .connected || newState == .completed {
                self.webrtcICEReady = true
                self.markWebRTCConnected()
            } else if newState == .failed {
                self.activateFrameFallback("WebRTC could not reach this TV directly. Using the authenticated browser viewport instead.")
            }
        }
    }

    nonisolated func peerConnection(_ peerConnection: LKRTCPeerConnection, didChange newState: LKRTCIceGatheringState) {
        guard newState == .complete else { return }
        Task { @MainActor [weak self] in self?.finishIceWait() }
    }

    nonisolated func peerConnection(_ peerConnection: LKRTCPeerConnection, didGenerate candidate: LKRTCIceCandidate) {}
    nonisolated func peerConnection(_ peerConnection: LKRTCPeerConnection, didRemove candidates: [LKRTCIceCandidate]) {}

    nonisolated func peerConnection(_ peerConnection: LKRTCPeerConnection, didOpen dataChannel: LKRTCDataChannel) {
        Task { @MainActor [weak self] in
            guard let self else { return }
            dataChannel.delegate = self
            if dataChannel.label == "events" {
                self.eventsChannel = dataChannel
                self.beginHeartbeat()
            } else if dataChannel.label == "frames" {
                self.framesChannel = dataChannel
            }
        }
    }

    nonisolated func peerConnection(
        _ peerConnection: LKRTCPeerConnection,
        didAdd rtpReceiver: LKRTCRtpReceiver,
        streams: [LKRTCMediaStream]
    ) {
        guard let track = rtpReceiver.track as? LKRTCVideoTrack else { return }
        Task { @MainActor [weak self] in self?.attachVideo(track) }
    }
}

extension TVRemoteRuntimeController: LKRTCDataChannelDelegate {
    nonisolated func dataChannelDidChangeState(_ dataChannel: LKRTCDataChannel) {}

    nonisolated func dataChannel(_ dataChannel: LKRTCDataChannel, didReceiveMessageWith buffer: LKRTCDataBuffer) {
        let data = buffer.data
        let binary = buffer.isBinary
        let label = dataChannel.label
        Task { @MainActor [weak self] in self?.handleData(data, binary: binary, channelLabel: label) }
    }
}
