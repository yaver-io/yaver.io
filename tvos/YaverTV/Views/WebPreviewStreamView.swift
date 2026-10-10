// WebPreviewStreamView.swift — render a web project on the TV as a pixel stream.
//
// tvOS has no WebKit, so a web app can't run in-process. The box captures it
// headless at the chosen viewport (phone/tablet/desktop) and the TV polls the
// frames. A Rebuild button re-triggers the box's reload and the stream keeps
// flowing — the vibe loop, lean-back: watch → tweak (on your machine) → rebuild
// → watch again, on the big screen.

import SwiftUI
import UIKit

struct WebPreviewStreamView: View {
    @EnvironmentObject var store: YaverStore
    let project: ProjectSummary
    let form: PreviewForm
    var studioMode = false

    @State private var frame: UIImage?
    @State private var status = "Starting preview…"
    @State private var error: String?
    @State private var started = false
    @State private var pollTask: Task<Void, Never>?
    @State private var logTask: Task<Void, Never>?
    @State private var logLines: [String] = []
    @State private var rebuilding = false

    // ── DOM mode ("kumanda" element select) ─────────────────────────────
    // The TV has no pointer; the remote is a touch surface. When the user
    // turns DOM mode ON, a cursor renders over the captured frame, the remote
    // touch surface moves it (DragGesture on the frame), and Play/Pause sends
    // the viewport coordinate to /vibing/preview/select — the box dispatches a
    // real click in the headless Chrome, captures the element, and the
    // per-turn hook attaches it to the next prompt. See
    // VibePreviewManager.SelectElement.
    @State private var domMode = false
    /// Cursor position in DISPLAY space (points within the image view).
    @State private var cursor = CGPoint(x: 0.5, y: 0.5) // normalized 0-1
    @State private var selectSummary: String?
    @State private var selecting = false
    @State private var selectError: String?
    /// The last select's surface metadata (frame size vs viewport). Used to
    /// convert the cursor's normalized position into viewport coordinates the
    /// box understands; the agent reports the REAL captured frame size, which
    /// can differ from the requested profile (letterbox / DPR / pre-viewport
    /// frame).
    @State private var selectMeta: AgentClient.PreviewSelectMeta?
    /// Coalesces hover moves: each remote step cancels the in-flight cursor
    /// move to the box, so a fast swipe sends the LAST position, not a burst.
    @State private var hoverTask: Task<Void, Never>?
    /// Seeded into VibeTurnPanel by the "Deep audit this element" button.
    @State private var auditPrefill = ""

    // The named capability gap, and the state of the fix we are running for it.
    @State private var gap: CapabilityGap?
    @State private var fixing = false
    @State private var fixStartedAt: Date?
    @State private var fixTask: Task<Void, Never>?
    @State private var fixTicker = Date()
    /// auth.session.scope_denied from the box: not retryable, route to update.
    @State private var scopeDenied = false
    /// Set when the render-leg preflight failed and the preview is being
    /// served by the RUNNER box instead. Start and stop must hit the same box.
    @State private var fellBackToRunner = false
    // Reattach bookkeeping for the /dev/events log stream.
    @State private var reattachAttempt = 0
    @State private var streamNotice: String?

    private let ticker = Timer.publish(every: 1, on: .main, in: .common).autoconnect()

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            if let frame {
                frameView(frame)
            } else if let gap {
                // A NAMED gap outranks the raw error string: it says which tool
                // is missing and, when the box can install it, carries the
                // route. This is the panel whose absence made the phone show
                // "Waiting for the dev server to report its address…" over an
                // agent that had already said `flutter: executable file not
                // found in $PATH`.
                gapPanel(gap)
            } else if scopeDenied {
                // Deterministic 403: the box's agent predates the TV scope
                // rows. Retrying cannot help; updating the agent is the route.
                VStack(spacing: 16) {
                    Image(systemName: "arrow.down.circle.dotted").font(.system(size: 56)).foregroundStyle(.orange)
                    Text("This box needs an agent update").font(.title2)
                    Text(FailureSignals.explainSessionScopeDenied())
                        .foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 720)
                    NavigationLink("Update the agent") { UpdateAgentView() }
                }
            } else if let error {
                VStack(spacing: 16) {
                    Image(systemName: "globe.badge.chevron.backward").font(.system(size: 56)).foregroundStyle(.secondary)
                    Text("Preview unavailable").font(.title2)
                    Text(error).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 680)
                    if let remedy = FailureSignals.explainRelayDeny(error) {
                        // A relay device_mismatch can never self-heal. Saying
                        // "Try again" over it is the loop this sentence ends.
                        Text(remedy)
                            .font(.callout)
                            .foregroundStyle(.orange)
                            .multilineTextAlignment(.center)
                            .frame(maxWidth: 680)
                    } else if let limit = FailureSignals.classifyRelayLimit(error) {
                        VStack(spacing: 6) {
                            Text(limit.title).font(.callout.bold()).foregroundStyle(.orange)
                            Text(limit.detail).font(.caption).foregroundStyle(.secondary)
                                .multilineTextAlignment(.center)
                        }
                        .frame(maxWidth: 680)
                    }
                    if FailureSignals.explainRelayDeny(error) == nil {
                        Button("Try again") { restart() }
                    }
                }
            } else {
                VStack(spacing: 14) {
                    ProgressView().scaleEffect(1.5)
                    Text(status).foregroundStyle(.secondary)
                }
            }

            VStack {
                HStack(spacing: 14) {
                    Label("\(project.name) · \(form.rawValue)", systemImage: form.icon)
                        .font(.system(size: 16, weight: .semibold))
                        .padding(.horizontal, 14).padding(.vertical, 8)
                        .background(.ultraThinMaterial, in: Capsule())
                    Spacer()
                    // DOM mode: pick an element in the preview with the remote.
                    // The button is focusable (hasTVPreferredFocus is on the
                    // project label); while domMode is on, the Play/Pause
                    // button on the remote (or a second press here) sends the
                    // cursor coordinate to the box.
                    Button {
                        domMode.toggle()
                        if !domMode { selectSummary = nil; selectError = nil }
                    } label: {
                        Label(domMode ? "Element: ON" : "Element", systemImage: domMode ? "scope" : "viewfinder")
                            .font(.system(size: 16, weight: .semibold))
                    }
                    .buttonStyle(.bordered)
                    .tint(domMode ? .accentColor : .secondary)
                    if domMode {
                        Button {
                            Task {
                                guard let client = fellBackToRunner ? store.runnerClient() : store.renderClient(),
                                      let img = frame else { return }
                                await sendSelection(client, imageSize: img.size)
                            }
                        } label: {
                            Label(selecting ? "Selecting…" : "Select", systemImage: "circle.circle")
                                .font(.system(size: 16, weight: .semibold))
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(selecting || frame == nil)
                    }
                    Button { Task { await rebuild() } } label: {
                        Label(rebuilding ? "Rebuilding…" : "Rebuild", systemImage: "arrow.triangle.2.circlepath")
                            .font(.system(size: 16, weight: .semibold))
                    }
                    .disabled(rebuilding)
                }
                .padding(32)
                if domMode {
                    HStack(spacing: 10) {
                        Image(systemName: "hand.draw.fill")
                        Text("Move the cursor with the remote (D-pad / touch surface), then press Play/Pause or Select to send the element to the next prompt.")
                        if let selectError {
                            Text(selectError).foregroundStyle(.orange)
                        } else if let selectSummary {
                            Text("Attached: \(selectSummary)").foregroundStyle(.green)
                        }
                    }
                    .font(.system(size: 14))
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .center)
                    .padding(.bottom, 8)
                }
                Spacer()
                // The vibe loop lives ON the preview: prompt → runner turn →
                // HMR lands in the frame stream that never stopped polling.
                // The full project travels so the panel can seed the workDir
                // picker + remember the choice to Convex (2026-08-10).
                if !studioMode {
                    VibeTurnPanel(project: project, prefill: $auditPrefill)
                        .padding(.horizontal, 32)
                        .padding(.bottom, logLines.isEmpty ? 30 : 8)
                }
                if !studioMode && !logLines.isEmpty {
                    logPanel
                        .padding(.horizontal, 32)
                        .padding(.bottom, 30)
                }
            }
        }
        .onAppear { if !started { restart() } }
        .accessibilityIdentifier("vibing.preview-surface")
        .onReceive(ticker) { now in if fixing { fixTicker = now } }
        // The Browse|Inspect contract, tvOS-style: flipping DOM mode ON arms
        // the probe in the captured page (hover highlight tracks in the frame
        // stream); flipping it OFF disarms it AND clears the stored element
        // ("off means the agent holds nothing" — the same rule the web/mobile
        // radio enforces with DELETE /dom-inspect).
        .onChange(of: domMode) { _, on in
            guard let client = fellBackToRunner ? store.runnerClient() : store.renderClient() else { return }
            Task {
                _ = try? await client.setPreviewDomMode(project: project.name, enabled: on, workDir: project.path)
            }
        }
        // The Siri Remote's Play/Pause is the natural "OK" for a cursor you
        // are moving with the touch surface. Only intercept it while DOM mode
        // is on; otherwise leave it to any existing transport handling.
        // tvOS-only input (MoveCommandDirection / onPlayPauseCommand /
        // onMoveCommand are unavailable on visionOS, which shares this file —
        // the headset selects via the on-screen Select button until spatial
        // gestures land).
        #if os(tvOS)
        .onPlayPauseCommand {
            guard domMode, !selecting, let img = frame else { return }
            let client = fellBackToRunner ? store.runnerClient() : store.renderClient()
            guard let client else { return }
            Task { @MainActor in
                await sendSelection(client, imageSize: img.size)
            }
        }
        // D-pad / touch-surface moves steer the cursor while DOM mode is on.
        // Applied unconditionally but gated inside: when domMode is off the
        // moves are ignored so focus navigation keeps working normally.
        .onMoveCommand { dir in
            guard domMode else { return }
            moveCursor(dir)
        }
        #endif
        .onDisappear {
            pollTask?.cancel()
            logTask?.cancel()
            fixTask?.cancel()
            hoverTask?.cancel()
            // Leaving the preview must clear any held element — the agent is
            // not allowed to keep a selection for a screen nobody is looking
            // at (same rule as turning DOM mode off).
            if domMode {
                let client = fellBackToRunner ? store.runnerClient() : store.renderClient()
                Task {
                    _ = try? await client?.setPreviewDomMode(project: project.name, enabled: false, workDir: project.path)
                }
            }
            // Role parity: stop the preview on the SAME box that served it —
            // the render box normally, the runner box when the preflight fell
            // back. Stop-on-a-different-box orphans a dev server.
            let stopOnRunner = fellBackToRunner
            Task {
                let client = stopOnRunner ? store.runnerClient() : store.renderClient()
                await client?.stopWebPreview(project: project.name)
            }
        }
    }

    /// The captured frame, plus the DOM-mode cursor overlay when enabled.
    ///
    /// The cursor lives in NORMALIZED space (0-1) so it survives any
    /// letterboxing: `aspectRatio(.fit)` scales the image to fit while keeping
    /// its aspect, so display points are not viewport points unless the frame
    /// fills the view exactly. We render the cursor at a normalized offset of
    /// the IMAGE (not the view) and convert back to viewport coordinates by
    /// multiplying by the captured frame's pixel dimensions — the agent's
    /// /vibing/preview/select expects viewport-space coordinates in the
    /// captured frame's own resolution.
    ///
    /// tvOS INPUT: SwiftUI has no DragGesture on tvOS (verified against the
    /// tvOS 26.2 SDK — DragGesture is iOS/macOS only). The Siri Remote's touch
    /// surface is exposed as directional moves, so the cursor is moved with
    /// `onMoveCommand` (D-pad arrows; the touch surface swipe maps to arrows)
    /// and the selection fires on `onSelectGesture` (tap/click on the surface)
    /// or Play/Pause. This is the standard 10-foot "mouse" idiom.
    @ViewBuilder
    private func frameView(_ img: UIImage) -> some View {
        let size = img.size
        GeometryReader { geo in
            let fit = aspectFitRect(imageSize: size, in: geo.size)
            ZStack {
                Image(uiImage: img)
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: fit.width, height: fit.height)
                    .position(x: fit.midX, y: fit.midY)
                if domMode {
                    cursorOverlay(imageSize: size, fit: fit)
                }
            }
            .contentShape(Rectangle())
            // The chip lives OUTSIDE the crosshair's allowsHitTesting(false)
            // subtree — SwiftUI cannot re-enable hits under a disabled parent.
            .overlay(alignment: .topLeading) {
                if domMode, let selectSummary {
                    selectionChip(summary: selectSummary)
                        .padding(12)
                }
            }
        }
        .padding(24)
    }

    /// The crosshair + optional selected-element chip, drawn over the image.
    @ViewBuilder
    private func cursorOverlay(imageSize: CGSize, fit: CGRect) -> some View {
        let px = fit.minX + cursor.x * fit.width
        let py = fit.minY + cursor.y * fit.height
        ZStack {
            Circle()
                .stroke(Color.accentColor, lineWidth: 3)
                .frame(width: 44, height: 44)
                .position(x: px, y: py)
            Image(systemName: "plus")
                .font(.system(size: 22, weight: .bold))
                .foregroundStyle(Color.accentColor)
                .position(x: px, y: py)
            // A short horizontal rule so the user can aim at a precise line
            // (10-foot accuracy is coarse; the crosshair plus rule reads
            // better than a dot alone).
            Rectangle()
                .fill(Color.accentColor)
                .frame(width: 18, height: 2)
                .position(x: px, y: py)
        }
        .allowsHitTesting(false)
    }

    /// The named-selection chip: what the runner will receive, plus the two
    /// next taps — "Deep audit this element" (seeds the vibe panel, which
    /// sends with the element already attached by the per-turn hook) and
    /// "Done" (back to Browse, clearing the selection).
    @ViewBuilder
    private func selectionChip(summary: String) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("element: \(summary)")
                .font(.system(size: 15, weight: .semibold))
                .lineLimit(1)
                .padding(.horizontal, 12).padding(.vertical, 6)
                .background(.ultraThinMaterial, in: Capsule())
            HStack(spacing: 8) {
                Button {
                    auditPrefill = "Deep audit this element"
                } label: {
                    Label("Deep audit this element", systemImage: "scope")
                        .font(.system(size: 14, weight: .semibold))
                }
                .buttonStyle(.borderedProminent)
                Button {
                    selectSummary = nil
                    selectError = nil
                    domMode = false
                } label: {
                    Label("Done", systemImage: "checkmark")
                        .font(.system(size: 14, weight: .semibold))
                }
                .buttonStyle(.bordered)
            }
        }
        .padding(12)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 16))
    }

    #if os(tvOS)
    /// Step the cursor with a D-pad direction (the remote touch surface swipes
    /// arrive as MoveCommandDirections). Step size is a fraction of the frame
    /// so a coarse flick can cross the whole screen in a few moves.
    @MainActor
    private func moveCursor(_ dir: MoveCommandDirection) {
        guard domMode else { return }
        let step: CGFloat = 0.04
        switch dir {
        case .up: cursor = CGPoint(x: cursor.x, y: clamp01(cursor.y - step))
        case .down: cursor = CGPoint(x: cursor.x, y: clamp01(cursor.y + step))
        case .left: cursor = CGPoint(x: clamp01(cursor.x - step), y: cursor.y)
        case .right: cursor = CGPoint(x: clamp01(cursor.x + step), y: cursor.y)
        @unknown default: break
        }
        sendHover()
    }

    /// Send the cursor position to the box as a pure MOUSE MOVE (no click, no
    /// store) so the probe's hover highlight tracks the remote in the next
    /// captured frame. Coalesced: every step cancels the previous send, so a
    /// swipe ships only its final position.
    @MainActor
    private func sendHover() {
        guard domMode, let img = frame else { return }
        hoverTask?.cancel()
        hoverTask = Task { @MainActor in
            try? await Task.sleep(nanoseconds: 90_000_000)
            guard !Task.isCancelled, domMode,
                  let client = fellBackToRunner ? store.runnerClient() : store.renderClient() else { return }
            let x = Int((cursor.x * img.size.width).rounded())
            let y = Int((cursor.y * img.size.height).rounded())
            try? await client.movePreviewCursor(project: project.name, x: x, y: y)
        }
    }
    #endif

    /// Send the cursor's viewport coordinate to the box: real click →
    /// element capture → shared domInspect store → next-prompt attachment.
    ///
    /// The coordinate is computed from the AGENT-REPORTED frame size (selectMeta
    /// carries the real captured pixels, which can differ from the image the
    /// UIImage holds after decoding — e.g. a PNG whose header size differs from
    /// the UIImage's scale) — falling back to the UIImage's own size when the
    /// box predates the metadata field.
    @MainActor
    private func sendSelection(_ client: AgentClient, imageSize: CGSize) async {
        guard domMode, !selecting else { return }
        selecting = true
        selectError = nil
        defer { selecting = false }
        let frameW = CGFloat(selectMeta?.frameW ?? Int(imageSize.width))
        let frameH = CGFloat(selectMeta?.frameH ?? Int(imageSize.height))
        let viewportX = Int((cursor.x * frameW).rounded())
        let viewportY = Int((cursor.y * frameH).rounded())
        do {
            let result = try await client.selectPreviewElement(project: project.name, x: viewportX, y: viewportY, workDir: project.path)
            selectMeta = result.meta
            if result.ok == true {
                selectSummary = result.summary
            } else {
                selectError = "No element at that spot — try again."
            }
        } catch {
            selectError = error.localizedDescription
        }
    }

    private func clamp01(_ v: CGFloat) -> CGFloat { min(max(v, 0), 1) }

    /// The rect `aspectRatio(.fit)` actually paints for an image of `imageSize`
    /// inside a view of `viewSize` — mirror of the layout math, used to convert
    /// cursor display positions into viewport coordinates.
    private func aspectFitRect(imageSize: CGSize, in viewSize: CGSize) -> CGRect {
        guard imageSize.width > 0, imageSize.height > 0, viewSize.width > 0, viewSize.height > 0 else {
            return CGRect(x: 0, y: 0, width: viewSize.width, height: viewSize.height)
        }
        let scale = min(viewSize.width / imageSize.width, viewSize.height / imageSize.height)
        let w = imageSize.width * scale
        let h = imageSize.height * scale
        return CGRect(x: (viewSize.width - w) / 2, y: (viewSize.height - h) / 2, width: w, height: h)
    }

    /// The capability gap, rendered as what it is: a named missing tool, and
    /// either the button that installs it or the reason there is no button.
    @ViewBuilder
    private func gapPanel(_ gap: CapabilityGap) -> some View {
        VStack(spacing: 18) {
            // The icon follows the CODE, not the type name. A shipping box over
            // "your TV is already previewing sfmg" reads as a download, which is
            // the wrong story about a lock someone else is holding.
            Image(systemName: FailureSignals.isPreviewSessionActive(gap)
                ? "rectangle.on.rectangle.angled"
                : "shippingbox.and.arrow.backward")
                .font(.system(size: 52))
                .foregroundStyle(.orange)
            Text(FailureSignals.gapTitle(gap))
                .font(.title2)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 820)
            let body = FailureSignals.gapBody(gap)
            if !body.isEmpty {
                Text(body)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 820)
            }

            if fixing {
                // Streaming the fix IS part of the fix: a multi-GB SDK behind a
                // silent spinner is indistinguishable from a hang, so the
                // elapsed time keeps moving even while the download is quiet.
                VStack(spacing: 8) {
                    ProgressView()
                    Text(fixElapsedLine())
                        .font(.callout.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            } else if let label = FailureSignals.gapFixLabel(gap) {
                Button(label) { startFix(gap) }
                    .buttonStyle(.borderedProminent)
            } else {
                // No route. Say why, and do NOT offer a button that cannot
                // work — an install we know will fail teaches the user that
                // Yaver lies.
                Text(gap.constraint ?? "This machine cannot install it automatically.")
                    .font(.callout)
                    .foregroundStyle(.orange)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 820)
                // A CONSTRAINED gap is a settled fact about this machine, and a
                // retry cannot change a settled fact. Offering one here was the
                // same defect as the preview-lock "Try again": an action that
                // reads as hope and can only ever fail.
                if !FailureSignals.gapSuppressesRetry(gap) {
                    Button("Try again") { restart() }
                }
            }
        }
    }

    private func fixElapsedLine() -> String {
        guard let fixStartedAt else { return "starting…" }
        let elapsed = FailureSignals.shortDuration(fixTicker.timeIntervalSince(fixStartedAt) * 1000)
        let tail = logLines.last.map { " · \($0)" } ?? ""
        return "\(elapsed) elapsed\(tail)"
    }

    /// POST the gap's route, stream its output into the log panel, and — when
    /// the gap says the original operation should be retried — return the user
    /// to what they were doing instead of making them find the button again.
    /// POST a synchronous route, then re-run what the user originally asked for.
    /// The takeover case: the other surface's preview stops, this one starts, and
    /// the next thing on screen is the frame — which is the confirmation.
    private func startInstantFix(_ gap: CapabilityGap, _ fix: GapFix) {
        fixing = true
        fixStartedAt = Date()
        fixTicker = Date()
        appendLog("\(fix.method) \(fix.path) …")
        fixTask?.cancel()
        fixTask = Task {
            // Same box that refused us. A takeover applied to a different box
            // stops a preview nobody was watching and leaves this one blocked.
            guard let client = fellBackToRunner ? store.runnerClient() : store.renderClient() else {
                await MainActor.run {
                    fixing = false
                    error = store.machineSplitActive
                        ? "Your render machine needs the relay to be reachable from this TV."
                        : "No machine selected"
                }
                return
            }
            do {
                try await client.invokeGapFix(fix)
            } catch {
                await MainActor.run {
                    fixing = false
                    self.error = "\(fix.label) failed: \(error.localizedDescription)"
                }
                return
            }
            await MainActor.run {
                fixing = false
                self.gap = nil
                if FailureSignals.gapRetriesAfterFix(gap) {
                    restart()
                } else {
                    // No retry requested: say it worked rather than leaving the
                    // panel on a cleared gap with nothing in its place.
                    appendLog("\(fix.label) done.")
                }
            }
        }
    }

    private func startFix(_ gap: CapabilityGap) {
        // An INSTANT route (stop a session, release a lock) answers in
        // milliseconds and has nothing to stream, so it takes the short path:
        // POST exactly what the agent said, then return the user to what they
        // were doing. This is the branch the preview-lock takeover uses; without
        // it the button below refused every non-install remedy with "This gap
        // carries no install route" — a button that argues with itself.
        if FailureSignals.gapFixIsInstant(gap), let fix = gap.fix {
            startInstantFix(gap, fix)
            return
        }
        guard let tool = FailureSignals.gapInstallTool(gap) else {
            if let fix = gap.fix {
                startGeneralFix(gap, fix)
            } else {
                error = gap.constraint ?? "This gap carries no runnable route."
            }
            return
        }
        fixing = true
        fixStartedAt = Date()
        fixTicker = Date()
        appendLog("POST \(gap.fix?.path ?? "/install/\(tool)") …")
        fixTask?.cancel()
        fixTask = Task {
            // The gap came from the box that served /dev/start — the install
            // must land on the SAME box, or the fix "succeeds" somewhere the
            // preview never runs.
            guard let client = fellBackToRunner ? store.runnerClient() : store.renderClient() else {
                await MainActor.run {
                    fixing = false
                    error = store.machineSplitActive
                        ? "Your render machine needs the relay to be reachable from this TV."
                        : "No machine selected"
                }
                return
            }
            let started: AgentClient.InstallStarted
            do {
                started = try await client.installTool(tool)
            } catch {
                await MainActor.run {
                    fixing = false
                    self.error = "install \(tool) failed: \(error.localizedDescription)"
                }
                return
            }
            let streamName = started.stream ?? gap.fix?.stream ?? "install:\(tool)"
            await MainActor.run { appendLog("streaming /streams/\(streamName)") }
            let stream = await client.subscribeInstallStream(streamName) { line in
                Task { @MainActor in appendLog(line) }
            } onDone: { ok, err in
                Task { @MainActor in
                    fixing = false
                    if ok {
                        self.gap = nil
                        appendLog("\(tool) installed.")
                        if FailureSignals.gapRetriesAfterFix(gap) { restart() }
                    } else {
                        self.error = "\(tool) install failed: \(err ?? "unknown error")"
                    }
                }
            }
            await stream.value
        }
    }

    /// Run a non-install asynchronous remedy such as `/dev/start`. Dev-server
    /// progress is already carried by the `/dev/events` stream attached to this
    /// view, so subscribing to the install-only `/streams/install:*` protocol
    /// would wait on a channel this route never produces. Invoke the route,
    /// keep the existing event panel alive, and re-enter the bounded readiness
    /// loop when the agent asks for a retry.
    private func startGeneralFix(_ gap: CapabilityGap, _ fix: GapFix) {
        fixing = true
        fixStartedAt = Date()
        fixTicker = Date()
        appendLog("\(fix.method) \(fix.path) …")
        fixTask?.cancel()
        fixTask = Task {
            guard let client = fellBackToRunner ? store.runnerClient() : store.renderClient() else {
                await MainActor.run {
                    fixing = false
                    error = store.machineSplitActive
                        ? "Your render machine needs the relay to be reachable from this TV."
                        : "No machine selected"
                }
                return
            }
            do {
                try await client.invokeGapFix(fix)
            } catch {
                await MainActor.run {
                    fixing = false
                    self.error = "\(fix.label) failed: \(error.localizedDescription)"
                }
                return
            }
            await MainActor.run {
                fixing = false
                self.gap = nil
                appendLog("\(fix.label) started.")
                if FailureSignals.gapRetriesAfterFix(gap) { restart() }
            }
        }
    }

    private var logPanel: some View {
        VStack(alignment: .leading, spacing: 4) {
            Label("Agent activity", systemImage: "text.alignleft")
                .font(.caption.bold())
                .foregroundStyle(.secondary)
            if let streamNotice {
                Text(streamNotice)
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            ForEach(Array(logLines.suffix(7).enumerated()), id: \.offset) { _, line in
                Text(line)
                    .font(.system(size: 13, design: .monospaced))
                    .foregroundStyle(color(for: line))
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(14)
        .frame(maxWidth: 960, alignment: .leading)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 18))
    }

    private func restart() {
        error = nil
        gap = nil
        scopeDenied = false
        fellBackToRunner = false
        streamNotice = nil
        reattachAttempt = 0
        started = true
        logLines = []
        pollTask?.cancel()
        logTask?.cancel()
        fixTask?.cancel()
        fixing = false
        pollTask = Task { await run() }
    }

    private func run() async {
        // Runner/render split: previews build + stream from the RENDER box.
        guard var client = store.renderClient() else {
            error = store.machineSplitActive
                ? "Your render machine needs the relay to be reachable from this TV."
                : "No machine selected"
            return
        }
        // Preflight the render leg BEFORE wiring streams — same policy as the
        // web Route editor's per-role Test connection. A render box that fell
        // off the relay must name itself and fall back to the AI machine, not
        // discover itself as a stuck spinner three requests in.
        if store.machineSplitActive {
            do {
                _ = try await client.info()
            } catch {
                let plan = FailureSignals.classifyTargetProbeFailure(error.localizedDescription)
                if plan.useRunnerFallback, let runner = store.runnerClient() {
                    fellBackToRunner = true
                    streamNotice = "Render box isn't reachable right now (\(plan.kind.rawValue)) — previewing on your AI machine instead."
                    appendLog("render leg probe failed: \(error.localizedDescription)")
                    client = runner
                } else {
                    self.error = "Your render machine didn't answer: \(error.localizedDescription)"
                    return
                }
            }
        }
        startLogStream(client)
        do {
            status = "Starting \(project.name)…"
            let starting = try await client.startDevServer(for: project)
            let dev = try await waitForDevServer(client, starting: starting)
            status = "Booting the browser lane…"
            let server = try await maybeStartExpoWebSibling(client)
            let target = captureTarget(dev: dev, server: server)
            status = "Capturing at \(form.rawValue) size…"
            try await client.startWebPreview(project: project.name, targetUrl: target,
                                              width: form.width, height: form.height)
            await poll(client)
        } catch let agentError as AgentError {
            if FailureSignals.isSessionScopeDenied(agentError) {
                scopeDenied = true
                return
            }
            // The 412 refusal carries the route; keep the sentence too, so a
            // gap-less failure still reads exactly as it always did.
            if let carried = agentError.gap {
                self.gap = carried
                appendLog(carried.summary)
            }
            self.error = agentError.message
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// `/dev/start` returns while the framework is still compiling. Starting
    /// capture immediately raced Chrome against an unopened port and produced
    /// a false "Start the dev server" page even though the server was already
    /// starting in the background. Poll the shared status endpoint, bounded,
    /// and only proceed when it reports a real serving process.
    private func waitForDevServer(
        _ client: AgentClient,
        starting initial: AgentClient.DevStartResult
    ) async throws -> AgentClient.DevStartResult {
        var current = initial
        let deadline = Date().addingTimeInterval(150)
        while !Task.isCancelled {
            if let failure = current.error?.trimmingCharacters(in: .whitespacesAndNewlines),
               !failure.isEmpty {
                throw AgentError(message: failure)
            }
            let ready = current.serving == true
                || (current.running == true && current.building != true)
            if ready { return current }
            guard Date() < deadline else {
                throw AgentError(message: "The (project.name) dev server did not become ready within 2½ minutes.")
            }
            status = current.servingLabel?.isEmpty == false
                ? current.servingLabel!
                : "Starting (project.name)…"
            try await Task.sleep(nanoseconds: 600_000_000)
            current = try await client.devServerStatus()
        }
        throw CancellationError()
    }

    private func startLogStream(_ client: AgentClient) {
        logTask?.cancel()
        logTask = Task {
            let stream = await client.subscribeDevEvents { ev in
                let line = ev.logLine ?? ev.message
                guard let line, !line.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
                Task { @MainActor in
                    // Output flowing again IS the proof the reattach worked —
                    // clear the notice and reset the ladder so the next drop
                    // gets a full five attempts rather than starting exhausted.
                    if streamNotice != nil { streamNotice = nil; reattachAttempt = 0 }
                    appendLog(line)
                }
            } onGap: { carried in
                // A gap can also arrive mid-compile on the event bus, long
                // after /dev/start returned 200 — e.g. the framework CLI is
                // present but a platform SDK it shells out to is not.
                Task { @MainActor in
                    gap = carried
                    appendLog(carried.summary)
                }
            } onEnd: { kind, cause in
                Task { @MainActor in handleStreamEnd(kind, cause, client) }
            } onError: { message in
                Task { @MainActor in appendLog("[stream] \(message)") }
            }
            await stream.value
        }
    }

    /// The log panel used to freeze in silence when the tunnel dropped: the box
    /// kept compiling, the TV kept showing the last line it happened to get,
    /// and nothing said the difference. Name it, reattach on a bounded ladder,
    /// and stop with a sentence rather than a spinner.
    @MainActor
    private func handleStreamEnd(_ kind: FailureSignals.StreamEndKind, _ cause: String?, _ client: AgentClient) {
        let plan = FailureSignals.planStreamRecovery(end: kind, attempt: reattachAttempt, cause: cause)
        switch plan {
        case .idle:
            streamNotice = nil
        case let .reattach(_, delayMs, message):
            streamNotice = message
            reattachAttempt += 1
            logTask = Task {
                try? await Task.sleep(nanoseconds: UInt64(delayMs) * 1_000_000)
                if Task.isCancelled { return }
                await MainActor.run { startLogStream(client) }
            }
        case let .giveUp(message):
            streamNotice = message
        }
    }

    @MainActor
    private func appendLog(_ line: String) {
        logLines.append(line)
        if logLines.count > 50 {
            logLines.removeFirst(logLines.count - 50)
        }
    }

    private func poll(_ client: AgentClient) async {
        var lastHash = ""
        var misses = 0
        while !Task.isCancelled {
            do {
                let meta = try await client.previewSnapshot(project: project.name)
                if let hash = meta.hash, !hash.isEmpty, hash != lastHash {
                    let data = try await client.previewFrame(hash: hash, project: project.name)
                    if let img = UIImage(data: data) {
                        frame = img; lastHash = hash; error = nil; misses = 0
                    } else {
                        // BYTES THAT ARE NOT AN IMAGE MUST SAY SO.
                        //
                        // This used to be a bare `if let` with no else: the poll
                        // succeeded, the decode failed, and nothing changed on
                        // screen or in state. That is why the missing
                        // `?project=` above was invisible — the endpoint was
                        // answering `{"error":"project query param required"}`,
                        // UIImage returned nil, and the TV showed "Starting
                        // preview…" indefinitely with no error anywhere.
                        //
                        // A silent decode failure is the same defect class as a
                        // silent serve: unfalsifiable from the outside. Show the
                        // first bytes so a reader can tell JSON from a truncated
                        // PNG at a glance.
                        let head = String(decoding: data.prefix(120), as: UTF8.self)
                            .trimmingCharacters(in: .whitespacesAndNewlines)
                        error = "The box returned \(data.count) bytes that are not an image"
                            + (head.isEmpty ? "." : ": \(head)")
                    }
                }
            } catch {
                if FailureSignals.isSessionScopeDenied(error) {
                    scopeDenied = true
                    frame = nil
                    return
                }
                misses += 1
                if misses >= 4 && frame == nil { self.error = error.localizedDescription }
            }
            // 300 ms: the snapshot answer is a tiny hash-only JSON unless the
            // frame actually changed, so polling faster costs almost nothing
            // and roughly halves perceived HMR latency vs the old 700 ms.
            try? await Task.sleep(nanoseconds: 300_000_000)
        }
    }

    private func color(for line: String) -> Color {
        let lower = line.lowercased()
        if lower.contains("error") || lower.contains("failed") || lower.contains("exception") || lower.contains("cannot ") {
            return .red
        }
        if lower.contains("warning") || lower.contains("warn") || lower.contains("deprecated") || lower.contains("expected version") {
            return .orange
        }
        if lower.contains("ready") || lower.contains("listening") || lower.contains("bundled") || lower.contains("waiting on") {
            return .blue
        }
        return .secondary
    }

    /// URL for the headless browser running ON THE BOX, not for the Apple TV.
    ///
    /// `/dev-web/` is proxied through the local agent and intentionally needs no
    /// bearer header, so Chromium can load it directly. The previous URL mixed
    /// the TV-visible LAN host with the sibling process port and kept the
    /// `/dev-web/` path, which points at nothing.
    private func maybeStartExpoWebSibling(_ client: AgentClient) async throws -> AgentClient.WebPreviewStart? {
        let fw = (project.framework ?? "").lowercased()
        guard fw == "expo" || fw == "react-native" || fw == "reactnative" || fw == "rn" else {
            return nil
        }
        return try await client.startWebServer()
    }

    private func captureTarget(dev: AgentClient.DevStartResult, server: AgentClient.WebPreviewStart?) -> String {
        if let webUrl = server?.webUrl, webUrl.hasPrefix("/") {
            return "http://127.0.0.1:\(Backend.agentPort)\(webUrl)"
        }
        if let webUrl = server?.webUrl, webUrl.hasPrefix("http://") || webUrl.hasPrefix("https://") {
            return webUrl
        }
        // The agent's /dev/ proxy is the canonical browser lane for Vite,
        // Next, Flutter web, etc. Besides surviving substituted framework
        // ports, it supplies a loopback Host header; hitting Next's raw port
        // made Yaver's HTTPS middleware redirect the headless browser to TLS
        // on a plain-HTTP dev port.
        if let bundleUrl = dev.bundleUrl, bundleUrl.hasPrefix("/") {
            return "http://127.0.0.1:\(Backend.agentPort)\(bundleUrl)"
        }
        if let url = dev.url, url.hasPrefix("http://") || url.hasPrefix("https://") {
            return url
        }
        if let url = dev.directUrl, url.hasPrefix("http://") || url.hasPrefix("https://") {
            // Chrome runs on this same box. Preserve the port but use loopback
            // so a public/LAN address policy cannot block its own preview.
            if let parsed = URL(string: url), let port = parsed.port {
                return "http://127.0.0.1:\(port)"
            }
        }
        if let port = dev.port ?? server?.port, port > 0 {
            return "http://127.0.0.1:\(port)"
        }
        return "http://127.0.0.1:3000"
    }

    private func rebuild() async {
        // Reload the dev server where it runs — the render box, or the runner
        // box when the preflight fell back there.
        guard let client = fellBackToRunner ? store.runnerClient() : store.renderClient() else { return }
        rebuilding = true
        defer { rebuilding = false }
        do {
            _ = try await client.reload(mode: "dev", workDir: project.path)
        } catch {
            self.error = error.localizedDescription
        }
    }
}
