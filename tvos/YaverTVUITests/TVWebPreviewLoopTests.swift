// TVWebPreviewLoopTests.swift — the REAL tvOS app, in the simulator, showing
// pixels streamed from a REAL box.
//
// ─── Why this target exists ────────────────────────────────────────────────
//
// Every "tvOS closed loop" until now sampled a frame the BOX captured at TV
// geometry. That proves the server half — runner edits, dev server rebuilds,
// render pipeline produces the right pixels — and `native-headless-vibe.mjs`
// says so on every run. It proves nothing about the TV app: not that it can
// fetch a frame, not that it decodes one, not that anything reaches a screen.
//
// That gap was not theoretical. `AgentClient.previewFrame` omitted `?project=`,
// so the endpoint answered `{"error":"project query param required"}` instead
// of PNG bytes, `UIImage(data:)` returned nil, and a bare `if let` with no else
// swallowed it. WebPreviewStreamView could never have displayed a frame in
// production, and no arc could see that, because no arc ran the app.
//
// This test closes that. The app runs in the tvOS simulator, points at a real
// machine, navigates to the web preview, and the frames it renders are captured
// from the SIMULATOR'S OWN SCREEN.
//
// ─── Division of labour ────────────────────────────────────────────────────
//
// This file drives the UI and writes screenshots. It does NOT decide colour.
//
// The colour verdict lives in web/lib/vibeVerdict.ts and is unit-tested there,
// because it has been wrong twice in a way that made a WORKING product look
// broken. Porting it to Swift would create a second implementation to drift —
// the exact failure this repo keeps paying for. So the orchestrator
// (e2e/tvos-sim-vibe-loop.mjs) classifies the frames this test leaves behind,
// with the same function the web and headless arcs use.
//
// ─── Configuration, not test hooks ─────────────────────────────────────────
//
// The box, token and project arrive through UserDefaults' argument domain,
// exactly as VisionDashboardUITests already does. No production code has a test
// hook. Note the argument domain PROPERTY-LIST-parses each `-key value` pair,
// so JSON must be wrapped as a quoted plist string or it is silently dropped
// and the app quietly falls back to persisted simulator state.

import XCTest

final class TVWebPreviewLoopTests: XCTestCase {

    /// Where to leave screenshots for the orchestrator to classify.
    private var shotDir: String {
        env("YAVER_SHOT_DIR", NSTemporaryDirectory())
    }
    private var boxHost: String { env("YAVER_BOX_HOST", "") }
    private var boxPort: Int { Int(env("YAVER_BOX_PORT", "18080")) ?? 18080 }
    private var boxId: String { env("YAVER_BOX_ID", "closed-loop-box") }
    private var token: String { env("YAVER_BOX_TOKEN", "") }
    private var projectName: String { env("YAVER_PROJECT", "mobile") }
    /// How long to sit on the preview capturing frames. The orchestrator starts
    /// the vibe, so this must outlast a runner turn plus a rebuild.
    private var captureSeconds: Int { Int(env("YAVER_CAPTURE_SECONDS", "420")) ?? 420 }

    /// Read configuration from the environment.
    ///
    /// xcodebuild does NOT forward its own environment to the test process. It
    /// forwards ONLY variables prefixed `TEST_RUNNER_`, with the prefix
    /// stripped. Setting `YAVER_BOX_TOKEN=…` on the xcodebuild command line
    /// therefore reaches nothing, the token reads empty, and setUpWithError
    /// skips — while the suite reports "Executed 1 test, with 1 test skipped
    /// and 0 failures" and xcodebuild EXITS 0.
    ///
    /// Measured on the first run of this test, 2026-08-03. A skipped arc that
    /// exits 0 is a false green with a receipt, so both halves are handled:
    /// callers pass TEST_RUNNER_YAVER_*, and the orchestrator treats "skipped"
    /// as not-a-pass rather than trusting the exit code.
    ///
    /// Both spellings are accepted so a human running this by hand from Xcode
    /// (where the scheme's environment IS forwarded verbatim) does not have to
    /// know the rule.
    private func env(_ k: String, _ fallback: String) -> String {
        let e = ProcessInfo.processInfo.environment
        let v = e[k] ?? e["TEST_RUNNER_" + k] ?? ""
        return v.isEmpty ? fallback : v
    }

    override func setUpWithError() throws {
        continueAfterFailure = false
        try XCTSkipIf(token.isEmpty, "YAVER_BOX_TOKEN not set — this arc needs a real box session")
    }

    func testWebPreviewStreamsRealPixelsFromTheBox() throws {
        let app = XCUIApplication()
        let boxJSON = #"[{"id":"closed-loop-box","name":"\#(boxHost)","host":"\#(boxHost)","port":\#(boxPort)}]"#
        let plistQuoted = "\"" + boxJSON.replacingOccurrences(of: "\"", with: "\\\"") + "\""
        app.launchArguments = [
            "-yaver.tv.token", token,
            "-yaver.tv.boxes", plistQuoted,
            "-yaver.tv.selectedBox", "closed-loop-box",
            // Route straight to Projects instead of walking an ADAPTIVE grid
            // whose column count depends on width. Six runs proved that grid
            // cannot be driven reliably by remote presses; see
            // DashboardView.startAt for the full evidence.
            "-yaver.tv.startAt", "preview:\(projectName)",
        ]
        app.launch()

        // Capture the very first screen no matter what happens next: if the app
        // never gets past sign-in or a "couldn't reach" notice, the ORACLE reads
        // that off this frame and the run reports a named cause instead of a
        // bare timeout.
        snap(app, "0000-launch")

        // NAVIGATION, NOW DERIVED FROM EVIDENCE.
        //
        // The first run of this test recorded what the TV actually renders
        // rather than guessing a focus path — and the answer was not the
        // dashboard at all. The oracle read:
        //
        //   "Box asleep — <box> isn't answering, and it can't be woken
        //    from the TV, start it from your computer or phone."
        //
        // …for a box answering GET /info with 200 throughout. ATS was refusing
        // cleartext to 100.64/10 before a packet left the device. Guessed
        // selectors would have reported "element not found" and taught nobody
        // anything; the screenshot named a real product bug (fixed in
        // tvos/YaverTV/Info.plist).
        //
        // With that fixed the dashboard renders, and these are its real labels:
        //   Session · Tasks · Capture · Feedback · Projects · Android · Runtime
        // WHAT ELEMENT TYPE ARE THE MENU ITEMS?
        //
        // The sweep reported "never focused a control matching Projects" while
        // the oracle could plainly read "Projects | Browse & preview on the TV"
        // off the screenshot. So the labels are there and `app.buttons` is the
        // wrong collection — SwiftUI on tvOS does not necessarily expose a
        // NavigationLink card as a button.
        //
        // These are CHEAP per-type counts, not a tree snapshot: exactly the
        // mistake that timed the runner out a run earlier. Measure the type,
        // then query it — do not guess a second time.
        // The 13 button labels, and which one currently holds focus. `app.buttons`
        // is a narrow collection so enumerating it is cheap — unlike
        // descendants(matching: .any), which timed the runner out earlier.
        // Knowing the exact labels and the focus start point is what turns the
        // sweep below from a guess into a route.
        let btns = app.buttons.allElementsBoundByIndex
        let described = btns.prefix(20).map { "\($0.label)\($0.hasFocus ? "[FOCUSED]" : "")" }
        XCTContext.runActivity(named: "buttons(\(btns.count)) → \(described.joined(separator: " | "))") { _ in }

        // The app opens straight on the project's PREVIEW (yaver.tv.startAt
        // = "preview:<name>"), so there is no navigation in this test at all.
        // Both screens it used to walk are width- or data-dependent; routing by
        // name is stable against both. See DashboardView.startAt for the six
        // runs that established this.
        Thread.sleep(forTimeInterval: 8)
        snap(app, "0001-routed")

        // Sit on the preview and photograph the app's own screen. The
        // orchestrator triggers the vibe; this just records what the TV shows.
        let deadline = Date().addingTimeInterval(TimeInterval(captureSeconds))
        var i = 1
        while Date() < deadline {
            snap(app, String(format: "%04d-preview", i))
            i += 1
            Thread.sleep(forTimeInterval: 15)
        }
    }

    /// Entering Vibing must stop on a compact searchable project grid, with the
    /// remembered project first/focused. The TV must not consume render/stream
    /// resources until Select confirms that card; one Select then opens the
    /// interactive WebRTC runtime.
    func testVibingFocusesLatestProjectThenOpensOnSelect() throws {
        let app = XCUIApplication()
        let boxJSON = #"[{"id":"\#(boxId)","name":"\#(boxHost)","host":"\#(boxHost)","port":\#(boxPort)}]"#
        let plistQuoted = "\"" + boxJSON.replacingOccurrences(of: "\"", with: "\\\"") + "\""
        app.launchArguments = [
            "-yaver.tv.token", token,
            "-yaver.tv.boxes", plistQuoted,
            "-yaver.tv.selectedBox", boxId,
            "-yaver.tv.startAt", "vibing",
        ]
        app.launch()
        snap(app, "vibing-0000-launch")

        XCTAssertTrue(app.staticTexts["Vibing"].waitForExistence(timeout: 20))
        XCTAssertTrue(
            app.textFields["vibing.project-search"].exists,
            "Vibing must expose project search before the project grid"
        )
        let latest = app.buttons.matching(
            NSPredicate(format: "label CONTAINS[c] %@", "latest project")
        ).firstMatch
        XCTAssertTrue(latest.waitForExistence(timeout: 20), "The remembered project must be labelled Latest")
        XCTAssertTrue(latest.hasFocus, "The remembered project must receive initial focus")

        XCTAssertFalse(app.buttons["Activate remote app"].exists, "Entering Vibing must not start streaming before Select")
        snap(app, "vibing-0001-project-choice")

        XCUIRemote.shared.press(.select)
        let opened = app.buttons["Activate remote app"].waitForExistence(timeout: 20)
        snap(app, opened ? "vibing-0002-opened" : "vibing-0002-open-failed")
        XCTAssertTrue(opened, "One Select on Latest must open the interactive runtime")
        XCTAssertTrue(app.buttons["Vibe"].exists)
        XCTAssertTrue(app.buttons["Reconnect"].exists)
        XCTAssertTrue(app.buttons["Inspect"].exists, "Interactive browser WebRTC must expose the DOM inspector it negotiates")
        XCTAssertFalse(app.staticTexts["Choose the app to open live"].exists)

        let connected = app.descendants(matching: .any)["vibing.runtime-connected"]
        let mediaArrived = connected.waitForExistence(timeout: 35)
        snap(app, mediaArrived ? "vibing-0003-connected" : "vibing-0003-media-failed")
        XCTAssertTrue(mediaArrived, "The interactive runtime opened but no WebRTC/fallback media arrived")

        // Vibing opens on its primary action: Chat. Down reaches the single
        // progressively-disclosed Options menu. The viewport used to claim
        // default focus and consume every arrow as pointer movement, leaving
        // the task-authority choices inoperable.
        let prompt = app.textFields["What should change?"]
        XCTAssertTrue(prompt.waitForExistence(timeout: 5))
        XCTAssertTrue(prompt.hasFocus, "Interactive Vibing must initially focus Chat")
        XCUIRemote.shared.press(.down)
        XCTAssertTrue(app.buttons["vibe.context"].hasFocus, "Down from Chat must focus Options")
        XCUIRemote.shared.press(.up)
        XCTAssertTrue(prompt.hasFocus, "Up from Options must restore the Vibe prompt before entering overlay mode")

        // Enter the transparent remote-app overlay, then prove every arrow
        // remains mouse input—even Right at its edge. A former
        // `cursor.x >= 0.94` escape made Right leave the overlay, so users
        // could never move the pointer to controls on the right of the app.
        let overlay = app.buttons["Activate remote app"]
        XCUIRemote.shared.press(.left)
        XCTAssertTrue(waitForFocus(overlay), "Left from the Vibe prompt must enter the remote-app overlay")
        XCUIRemote.shared.press(.select)
        for _ in 0..<12 {
            XCUIRemote.shared.press(.right)
        }
        XCTAssertTrue(waitForFocus(overlay), "Right must stay in overlay mode and move the remote mouse, even at its edge")
        for direction in [XCUIRemote.Button.up, .down, .left, .right] {
            XCUIRemote.shared.press(direction)
            XCTAssertTrue(waitForFocus(overlay), "\(direction) must remain a remote mouse/scroll command while overlay mode is active")
        }
        XCTAssertEqual(
            app.descendants(matching: .any)["vibing.overlay-focus-losses"].value as? String,
            "0",
            "Overlay arrows must be consumed before tvOS transiently highlights a Vibe chip"
        )

        // Back is the only overlay escape. The first press returns focus to
        // the Vibe widget; the next, non-overlay Back returns to Dashboard.
        XCUIRemote.shared.press(.menu)
        XCTAssertTrue(prompt.waitForExistence(timeout: 3))
        XCTAssertTrue(waitForFocus(prompt), "Back from overlay mode must stay in Vibing")
        XCTAssertFalse(app.buttons["dashboard.vibing"].exists,
                       "The overlay's Back event must not bubble into a Dashboard dismissal")
        XCUIRemote.shared.press(.menu)
        XCTAssertTrue(app.buttons["dashboard.vibing"].waitForExistence(timeout: 5), "Back outside overlay mode must return to Dashboard")

        Thread.sleep(forTimeInterval: 12)
        snap(app, "vibing-0004-interactive")
    }

    private func waitForFocus(_ element: XCUIElement, timeout: TimeInterval = 3) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if element.hasFocus { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.05))
        }
        return element.hasFocus
    }


    /// Move focus to the element whose label contains `label`, then press
    /// Select on the Siri Remote.
    ///
    /// tvOS has no tap() and no XCUIElement.select() — both are compiled out.
    /// The ONLY way to activate something is to give it focus and press the
    /// remote, so focus has to be walked to. This presses .down then .right in
    /// a bounded sweep, checking `hasFocus` after each step.
    ///
    /// Bounded, and it does NOT fail the test when the element never focuses.
    /// A missing control is far better diagnosed from the screenshot the caller
    /// takes next — "no such element" says less than a picture of the screen
    /// that does not contain it.
    /// Press `.down` a measured number of times, then Select.
    ///
    /// ── Why a fixed count, and not a search ────────────────────────────────
    ///
    /// Five runs went into learning this, so the evidence stays in the file.
    ///
    /// The tvOS dashboard is a TWO-COLUMN GRID, and the accessibility tree
    /// order is NOT the focus order:
    ///
    ///   tree   → Switch | Session | Tasks | Projects | Runtime | Apple TV | Capture | Feedback …
    ///   screen → Session  Capture
    ///            Tasks    Feedback
    ///            Projects …
    ///
    /// So `Projects` is index 3 in the tree and TWO `.down` presses on screen.
    /// Counting presses from a tree index put focus on "Shared with" instead —
    /// the screenshot proved it while the assertion could not.
    ///
    /// `hasFocus` cannot rescue this either: it reads false for an element while
    /// focus is passing through it, so polling it during a walk never matched,
    /// and using it to find the STARTING index silently returned 0 instead of
    /// the real position — the off-by-one that sent the walk a row too far.
    ///
    /// What is left is the only thing that proved stable: the on-screen
    /// geometry, measured once. Verification does not live here at all — the
    /// caller screenshots, and the oracle reads what is actually displayed.
    /// Pixels over predicates.
    private func pressDown(_ times: Int, thenSelect: Bool = true) {
        for _ in 0..<times {
            XCUIRemote.shared.press(.down)
            Thread.sleep(forTimeInterval: 0.6)
        }
        if thenSelect {
            XCUIRemote.shared.press(.select)
            Thread.sleep(forTimeInterval: 3)
        }
    }

    /// Screenshot the SIMULATOR SCREEN (not a view hierarchy render) and write
    /// it where the orchestrator can find it.
    ///
    /// XCUIScreen.main.screenshot() is what a viewer would see, which is the
    /// point: a verdict taken from anything else would be a statement about the
    /// app's internals rather than about pixels.
    private func snap(_ app: XCUIApplication, _ name: String) {
        let shot = XCUIScreen.main.screenshot()
        let url = URL(fileURLWithPath: shotDir).appendingPathComponent("\(name).png")
        do {
            try FileManager.default.createDirectory(
                at: URL(fileURLWithPath: shotDir), withIntermediateDirectories: true)
            try shot.pngRepresentation.write(to: url)
        } catch {
            // An artifact failure must never change a verdict, but it must not
            // be silent either — a run with no frames and no explanation is the
            // SILENT verdict this suite exists to remove.
            XCTContext.runActivity(named: "could not write \(url.lastPathComponent): \(error)") { _ in }
        }
        // Also attach, so a failed run is readable from the .xcresult alone.
        let att = XCTAttachment(screenshot: shot)
        att.name = name
        att.lifetime = .keepAlways
        add(att)
    }
}
