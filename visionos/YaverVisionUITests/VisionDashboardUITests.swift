// VisionDashboardUITests.swift — drives the real visionOS app in the simulator
// against a stub agent that speaks the agent's /ops wire protocol.
//
// Why these exist: every interesting behaviour on this surface is a REACTION to
// something awkward the backend said — "nothing was listening", "no dev server
// is running" — and none of it is reachable from a Go test or a compile. The
// whole class of bug this surface had was buttons that looked inert because the
// refusal never made it to a pixel. That is a UI fact and it needs a UI test.
//
// The stub is driven from the test via POST /__scenario, so a single run can
// walk the app through states a healthy machine never produces.
//
// The app is pointed at the stub, and signed in, purely through UserDefaults'
// argument domain (`-key value` launch arguments outrank the standard domain).
// No production code has a test hook in it, and nothing here touches the
// keychain.

import XCTest

private let stubHost = "127.0.0.1"
private let stubPort = 18099

final class VisionDashboardUITests: XCTestCase {

    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    // MARK: - Harness

    /// Point the stub at a scenario. Synchronous on purpose: the app must not be
    /// launched until the backend is in the state the test is about to assert on.
    private func setScenario(_ name: String) throws {
        var req = URLRequest(url: URL(string: "http://\(stubHost):\(stubPort)/__scenario")!)
        req.httpMethod = "POST"
        req.httpBody = try JSONSerialization.data(withJSONObject: ["name": name])
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")

        let done = expectation(description: "scenario \(name) applied")
        var failure: Error?
        URLSession.shared.dataTask(with: req) { _, resp, err in
            failure = err
            if let http = resp as? HTTPURLResponse, http.statusCode != 200 {
                failure = NSError(domain: "stub", code: http.statusCode)
            }
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 10)

        if let failure {
            throw XCTSkip("""
                stub agent unreachable on \(stubHost):\(stubPort) (\(failure)).
                Start it first — these tests drive the app against it:
                  cd <scratchpad>/stubagent && ./stubagent 18099
                """)
        }
    }

    /// Launch the app already signed in and already pointed at the stub box.
    ///
    /// The box list is injected through UserDefaults' argument domain, which
    /// PROPERTY-LIST-parses each `-key value` pair. A bare JSON array (`[{...}]`)
    /// is not a valid plist, so the pair is silently dropped and the app quietly
    /// falls back to whatever box the simulator still has persisted from an
    /// earlier session — which is exactly what made the first run of these tests
    /// sit on an unreachable machine and time out. Wrapping the JSON as a quoted
    /// plist string makes the parser hand back the string verbatim.
    ///
    /// Note this also means the injection OVERRIDES stale simulator state rather
    /// than merging with it: prefs live in the simulator's shared cfprefsd store
    /// and survive an app uninstall, so a test that merely wrote defaults could
    /// still be fighting a leftover box.
    @discardableResult
    private func launchApp() -> XCUIApplication {
        let boxJSON = #"[{"id":"stub-device-01","name":"Stub Box","host":"127.0.0.1","port":18099}]"#
        let plistQuoted = "\"" + boxJSON.replacingOccurrences(of: "\"", with: "\\\"") + "\""

        let app = XCUIApplication()
        app.launchArguments = [
            "-yaver.tv.token", "stub-session-token",
            "-yaver.tv.boxes", plistQuoted,
            "-yaver.tv.selectedBox", "stub-device-01",
        ]
        app.launch()
        return app
    }

    /// The dashboard's notices are SwiftUI Labels, so they surface as static
    /// text. Match on a substring — asserting the full sentence would make this
    /// a copy-editing test rather than a behaviour test.
    private func waitForText(_ app: XCUIApplication, containing needle: String, timeout: TimeInterval = 15) -> Bool {
        let predicate = NSPredicate(format: "label CONTAINS[c] %@", needle)
        let element = app.descendants(matching: .any).matching(predicate).firstMatch
        return element.waitForExistence(timeout: timeout)
    }

    /// Launch signed OUT, to land on the sign-in screen.
    private func launchSignedOut() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-yaver.tv.token", ""]
        app.launch()
        return app
    }

    // MARK: - Sign-in must be possible while WEARING the headset
    //
    // The TV's sign-in says "scan this QR with your phone". Carried onto a
    // headset that is a physical impossibility: the QR is on a virtual plane
    // inside the display, and the phone's camera is pointed at the room. You are
    // wearing the thing you are being told to photograph. It was the first screen
    // a new Vision Pro user ever saw, and no user could ever complete it.
    //
    // This asserts the instruction is gone and that a path a seated, headset-
    // wearing human can actually finish is offered in its place. No network is
    // needed: this copy renders before the device code arrives.

    func testSignInDoesNotAskTheUserToScanAQRCodeTheyAreWearing() throws {
        let app = launchSignedOut()

        XCTAssertTrue(waitForText(app, containing: "Sign in to Yaver"), "should land on sign-in when signed out")
        XCTAssertFalse(waitForText(app, containing: "Scan this code", timeout: 3),
                       "a headset cannot photograph its own virtual display — this instruction is impossible to follow")
        XCTAssertFalse(waitForText(app, containing: "Scan", timeout: 2),
                       "no scanning instruction of any kind belongs on a headset")
    }

    // Three paths, and every one of them is completable by someone wearing a
    // headset. They are not redundant — each covers a case the others cannot:
    //
    //   Apple  — fastest, but useless to a Google/GitHub account, and refused
    //            outright when the account has 2FA.
    //   Safari — serves every provider AND 2FA, but takes longer.
    //   Phone  — the escape hatch when the headset's browser is the problem.
    //
    // Drop any one of them and a real user is stranded, so the test names all three.
    func testSignInOffersAPathThatWorksWithoutTakingTheHeadsetOff() throws {
        let app = launchSignedOut()

        // Fast path: the Apple ID already on the headset. No second device at all.
        XCTAssertTrue(waitForText(app, containing: "you're already signed in to Apple"),
                      "the headset holds an Apple ID — the one-look sign-in should lead")

        // Everything Apple can't serve: a Google/Microsoft/GitHub/GitLab account,
        // or any account with 2FA. These run the SAME OAuth endpoints the phone
        // does, in the headset's own browser — a Vision Pro has no need to borrow
        // a phone to render a consent screen.
        for provider in ["Google", "Microsoft", "GitHub", "GitLab"] {
            XCTAssertTrue(app.buttons["Continue with \(provider)"].waitForExistence(timeout: 10),
                          "\(provider) accounts must have a way in that doesn't fork them into an empty Apple account")
        }

        // Last resort: read the code, type it on a phone seen through passthrough.
        XCTAssertTrue(waitForText(app, containing: "Or approve from your phone"),
                      "typing a code into a phone is possible in passthrough; photographing the panel is not")
    }

    // Tapping "Sign in with Apple" must never be a no-op.
    //
    // It IS allowed to fail — a Simulator with no Apple ID signed in, or a build
    // without the Sign-in-with-Apple entitlement (every CODE_SIGNING_ALLOWED=NO
    // build strips entitlements), both make ASAuthorizationController error out.
    // What is not allowed is failing silently. The first version of this screen
    // rendered its error label BELOW three stacked panels, off the bottom of the
    // scroll view, so a failed Apple sign-in looked exactly like a dead button —
    // the same bug this whole surface keeps growing back.
    //
    // Deliberately asserts the DISJUNCTION rather than the failure: on a machine
    // that does have an Apple ID this signs in for real, and a test that demanded
    // an error would then fail for the best possible reason. Either outcome is
    // fine. Silence is not.
    func testTappingSignInWithAppleAlwaysSaysSomething() throws {
        let app = launchSignedOut()

        let appleButton = app.buttons["Sign in with Apple"]
        XCTAssertTrue(appleButton.waitForExistence(timeout: 15))
        appleButton.tap()

        // Either an error surfaced where it can be read (top of the screen, above
        // the panels), or we actually got in.
        let explained = waitForText(app, containing: "Apple sign-in isn't available", timeout: 20)
            || waitForText(app, containing: "two-factor", timeout: 1)
            || waitForText(app, containing: "already signs in to Yaver with", timeout: 1)
            || app.buttons["Hot Reload"].exists          // signed in for real
            || waitForText(app, containing: "Add Your Machine", timeout: 1)

        XCTAssertTrue(explained,
                      "tapping Sign in with Apple produced neither a session nor a visible reason — "
                      + "that is a dead button, which is precisely what this screen must never ship again")
    }

    // MARK: - The dashboard renders real machine state

    func testDashboardRendersMachineAndRuntimeFromAgent() throws {
        try setScenario("delivered")
        let app = launchApp()

        XCTAssertTrue(waitForText(app, containing: "Stub Box"), "hero should name the selected machine")
        XCTAssertTrue(waitForText(app, containing: "1.99.304"), "Machine panel should show the agent version the box reported")
        XCTAssertTrue(waitForText(app, containing: "expo"), "Runtime panel should show the live dev server's framework")
        XCTAssertTrue(waitForText(app, containing: "sfmg"), "Preview Target should show the active project")
    }

    // MARK: - Reload: the happy path

    func testHotReloadReportsSuccessWhenListenersReceiveIt() throws {
        try setScenario("delivered")
        let app = launchApp()

        let hotReload = app.buttons["Hot Reload"]
        XCTAssertTrue(hotReload.waitForExistence(timeout: 15))
        XCTAssertTrue(hotReload.isEnabled, "a running dev server should enable Hot Reload")
        hotReload.tap()

        XCTAssertTrue(waitForText(app, containing: "Hot reload command sent"),
                      "a reload that reached listeners should report success")
    }

    // MARK: - Reload: built, but nobody was listening
    //
    // This is the path that could not happen before the backend change. A bundle
    // push used to return no deliveredTo at all, so the headset said "pushed"
    // into an empty room. Now the count comes back 0 and the surface must say so.

    func testHermesPushWarnsWhenNothingReceivedTheBundle() throws {
        try setScenario("nobody")
        let app = launchApp()

        let hermesPush = app.buttons["Hermes Push"]
        XCTAssertTrue(hermesPush.waitForExistence(timeout: 15))
        XCTAssertTrue(hermesPush.isEnabled, "a non-empty work dir should enable Hermes Push")
        hermesPush.tap()

        XCTAssertTrue(waitForText(app, containing: "no connected phone"),
                      "deliveredTo == 0 must surface as a warning, not a success")
        XCTAssertFalse(waitForText(app, containing: "bundle built and push command sent", timeout: 2),
                       "a push nothing received must not be reported as success")
    }

    // MARK: - Reload: the backend refused
    //
    // The agent answers a refusal with HTTP 200 + {ok:false,error}. A client that
    // reads only the status code treats that as success and shows nothing, which
    // is exactly what made these buttons look dead. The reason must reach a pixel.

    func testBackendRefusalIsShownInsteadOfLookingLikeADeadButton() throws {
        try setScenario("refused")
        let app = launchApp()

        let hotReload = app.buttons["Hot Reload"]
        XCTAssertTrue(hotReload.waitForExistence(timeout: 15))
        hotReload.tap()

        XCTAssertTrue(waitForText(app, containing: "no dev server is currently running"),
                      "the agent's refusal must be shown verbatim, not swallowed")
    }

    // MARK: - Coding agents

    func testRunnerSessionsAreListedOnTheDashboard() throws {
        try setScenario("delivered")
        let app = launchApp()

        // runner_sessions returns name/runner/attached. The old client asked the
        // wrong verb and decoded a different shape, so this panel always said
        // "No active runner sessions" while a runner was live.
        XCTAssertTrue(waitForText(app, containing: "yaver-codex"),
                      "Coding Agents should list the live runner PTYs")
        XCTAssertFalse(waitForText(app, containing: "No active runner sessions", timeout: 2),
                       "sessions are live; the panel must not claim otherwise")
    }

    // The session sheet must drive a NAMED session. The stub refuses a turn that
    // arrives without one ("name the one you mean") — the same way the agent does
    // when more than one runner is live — so a reply in the pane is proof the
    // session name was actually sent.
    func testSessionSheetSendsPromptToTheNamedSession() throws {
        try setScenario("delivered")
        let app = launchApp()

        let openSession = app.buttons["Open Session"]
        XCTAssertTrue(openSession.waitForExistence(timeout: 15))
        openSession.tap()

        XCTAssertTrue(waitForText(app, containing: "Live Session"), "the session sheet should open")

        let field = app.textFields.firstMatch.exists
            ? app.textFields.firstMatch
            : app.textViews.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the prompt composer should be present")
        field.tap()
        field.typeText("run the tests")

        app.buttons["Send"].tap()

        XCTAssertTrue(waitForText(app, containing: "I can apply this change"),
                      "the pane should show the session's reply — which only arrives if the turn named a session")
        XCTAssertFalse(waitForText(app, containing: "name the one you mean", timeout: 2),
                       "the turn must not be sent without a session name")

        // The reply is awaiting a choice; the sheet must offer it.
        XCTAssertTrue(waitForText(app, containing: "Yes, apply the patch"),
                      "an awaiting-choice turn should render its options as buttons")
    }
}
