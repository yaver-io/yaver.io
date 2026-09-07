import XCTest
@testable import YaverTV

final class VibingPlanTests: XCTestCase {
    private func project(_ framework: String) -> ProjectSummary {
        ProjectSummary(
            name: "fixture",
            path: "/tmp/fixture",
            framework: framework,
            branch: "main",
            gitRemote: nil,
            frameworks: [framework],
            surfaces: ["mobile", "web"],
            testSurfaces: ["browser", "webrtc"],
            isMonorepo: false,
            subframeworks: nil
        )
    }

    private var expoCapabilities: ProjectPreviewCapabilities {
        ProjectPreviewCapabilities(
            workDir: "/tmp/fixture",
            framework: "expo",
            selfDevelopment: false,
            hasPairedDevice: false,
            options: [
                ProjectPreviewOption(id: "dev-server", label: "Browser Reload", supported: true, primary: true, reason: nil, framework: "expo"),
                ProjectPreviewOption(id: "remote-runtime", label: "Stream over WebRTC", supported: true, primary: false, reason: nil, framework: "expo"),
            ],
            reason: nil
        )
    }

    private func registryDevice(
        id: String,
        name: String,
        online: Bool,
        lastHeartbeat: Double
    ) -> RegisteredDevice {
        RegisteredDevice(
            deviceId: id, name: name, alias: nil, platform: "linux",
            isOnline: online, quicHost: "127.0.0.1", quicPort: 18080,
            localIps: [], relayConnected: online, agentVersion: nil,
            managed: false, hosting: nil, machineId: nil, lastHeartbeat: lastHeartbeat,
            runners: nil, installedRunnerIds: nil
        )
    }

    private func userSettings(primary: String?, secondary: String?) throws -> MachineRegistry.UserSettings {
        var raw: [String: Any] = [:]
        if let primary { raw["primaryDeviceId"] = primary }
        if let secondary { raw["secondaryDeviceId"] = secondary }
        return try JSONDecoder().decode(
            MachineRegistry.UserSettings.self,
            from: JSONSerialization.data(withJSONObject: raw)
        )
    }

    func testExpoOffersFramesAndInteractiveWebRTCOnTV() {
        let choices = tvPreviewChoices(project: project("expo"), capabilities: expoCapabilities)
        XCTAssertEqual(choices.first?.destination, .webFrames)
        XCTAssertEqual(choices.first?.available, true)
        let webrtc = choices.first { $0.id == "remote-runtime" }
        XCTAssertEqual(webrtc?.available, true)
        XCTAssertEqual(webrtc?.destination, .interactiveWebRTC)
        XCTAssertTrue(webrtc?.detail.contains("Siri Remote pointer") == true)
        XCTAssertEqual(tvOSRenderLaneVerdicts.first { $0.id == "webrtc" }?.usable, true)
    }

    func testFlutterUsesTheBrowserFrameLane() {
        let flutter = project("flutter")
        XCTAssertEqual(flutter.kind, .web)
    }

    func testRelayWorksWithOrWithoutDirectOrTailscaleHostAndNeverLeaksPasswordInURL() {
        let relayOnly = BoxTarget(
            id: "box-id", name: "fixture", host: "", port: 18080,
            relayBaseUrl: "https://relay.example", relayPassword: "super-secret"
        )
        XCTAssertEqual(relayOnly.requestEndpoints(path: "/health").count, 1)
        XCTAssertFalse(relayOnly.requestEndpoints(path: "/health")[0].url.absoluteString.contains("super-secret"))

        let withDirect = BoxTarget(
            id: "box-id", name: "fixture", host: "100.64.1.2", port: 18080,
            relayBaseUrl: "https://relay.example", relayPassword: "super-secret"
        )
        XCTAssertEqual(withDirect.requestEndpoints(path: "/health").count, 2)
        XCTAssertFalse(withDirect.opsEndpoints.contains { $0.url.absoluteString.contains("super-secret") })
        XCTAssertEqual(withDirect.opsEndpoints.map(\.relay), [false, true])

        XCTAssertEqual(relayOnly.opsEndpoints.count, 1)
        XCTAssertEqual(relayOnly.opsEndpoints.first?.relay, true)
    }

    func testBoxTargetBracketsIPv6DirectEndpoint() {
        let box = BoxTarget(id: "box-v6", name: "IPv6 box", host: "2001:db8::10", port: 18080)
        XCTAssertEqual(
            box.requestEndpoints(path: "/health").first?.url.absoluteString,
            "http://[2001:db8::10]:18080/health"
        )
    }

    func testTaskCreateResponseAcceptsTaskIdAndRunnerId() throws {
        let payload = Data(#"{"taskId":"task-new","status":"queued","runnerId":"codex"}"#.utf8)
        let task = try JSONDecoder().decode(TaskSummary.self, from: payload)
        XCTAssertEqual(task.id, "task-new")
        XCTAssertEqual(task.runner, "codex")
        XCTAssertNil(task.title)
    }

    func testTaskDetailDecodesConversationTurns() throws {
        let payload = Data(#"{"id":"task-live","status":"running","runner":"claude","turns":[{"role":"user","content":"Ship it","timestamp":"2026-08-15T10:00:00Z"},{"role":"assistant","content":"Working","timestamp":null}]}"#.utf8)
        let task = try JSONDecoder().decode(TaskSummary.self, from: payload)
        XCTAssertEqual(task.turns?.map(\.role), ["user", "assistant"])
        XCTAssertEqual(task.turns?.last?.content, "Working")
    }

    func testTaskQuestionDecodesFromSSEShape() throws {
        let payload = Data(#"{"id":"q1","taskId":"task-live","prompt":"Which approach?","header":"Approach","kind":"choice","choices":["Safe","Fast"],"multi":false,"createdAtMs":1,"timeoutSec":300}"#.utf8)
        let question = try JSONDecoder().decode(TaskAgentQuestion.self, from: payload)
        XCTAssertEqual(question.id, "q1")
        XCTAssertEqual(question.choices, ["Safe", "Fast"])
        XCTAssertFalse(question.isSecret)
    }

    func testTaskQuestionDecodesInsideActualSSEEnvelope() throws {
        let payload = Data(#"{"type":"agent_question","question":{"id":"q2","taskId":"task-live","prompt":"Proceed?","kind":"text","createdAtMs":1,"timeoutSec":300}}"#.utf8)
        let event = try JSONDecoder().decode(AgentClient.TaskOutputEvent.self, from: payload)
        XCTAssertEqual(event.type, "agent_question")
        XCTAssertEqual(event.question?.id, "q2")
    }

    func testParkedFollowUpEnvelopeKeepsStructuredRecoveryFields() throws {
        let payload = Data(#"{"ok":false,"error":"Sign in again","code":"runner.codex.not_authenticated","parked":true,"reauthable":true,"runner":"codex"}"#.utf8)
        let error = try XCTUnwrap(AgentError.fromHTTPBody(payload))
        XCTAssertTrue(error.parked)
        XCTAssertTrue(error.reauthable)
        XCTAssertEqual(error.runner, "codex")
        XCTAssertEqual(error.code, "runner.codex.not_authenticated")
    }

    func testRunnerCatalogueSurvivesNullModelsOnAnotherRunner() throws {
        let payload = Data(#"{"runners":[{"id":"claude","name":"Claude Code","installed":true,"ready":true,"isDefault":false,"models":[{"id":"claude-sonnet-4-6","name":"Claude Sonnet 4.6","isDefault":true}]},{"id":"missing","name":"Missing","installed":false,"ready":false,"isDefault":false,"models":null}],"default":"claude"}"#.utf8)
        let list = try JSONDecoder().decode(AgentRunnerList.self, from: payload)
        XCTAssertEqual(list.runners.count, 2)
        XCTAssertEqual(list.runners[0].models.first?.id, "claude-sonnet-4-6")
        XCTAssertTrue(list.runners[1].models.isEmpty)
    }

    func testDroppedFinalFrameRefreshStopsReattachForTerminalTask() {
        XCTAssertTrue(tvTaskIsRunnerCoding("queued"))
        XCTAssertTrue(tvTaskIsRunnerCoding("running"))
        for terminal in ["review", "completed", "failed", "stopped"] {
            XCTAssertFalse(
                tvTaskIsRunnerCoding(terminal),
                "a terminal task must render retained output instead of retrying a dead SSE stream"
            )
        }
    }

    func testNonterminalDoneFollowsQueuedFollowUpOntoNextStream() {
        XCTAssertTrue(tvTaskStreamShouldReattachAfterDone("queued"))
        XCTAssertTrue(tvTaskStreamShouldReattachAfterDone("running"))
        for terminal in ["review", "completed", "failed", "stopped"] {
            XCTAssertFalse(tvTaskStreamShouldReattachAfterDone(terminal))
        }
    }

    func testParkedFollowUpOnlyOffersSignInWhenItCanHelp() {
        let auth = tvParkedTurnNotice(
            code: "runner.codex.not_authenticated",
            runner: "codex",
            reauthable: true
        )
        XCTAssertTrue(auth.offersRunnerSignIn)

        let sandbox = tvParkedTurnNotice(
            code: "runner.codex.linux_sandbox_blocked",
            runner: "codex",
            reauthable: false
        )
        XCTAssertFalse(sandbox.offersRunnerSignIn)
        XCTAssertTrue(sandbox.line.contains("host"))

        XCTAssertFalse(tvParkedTurnNotice(code: nil, runner: "codex", reauthable: true).offersRunnerSignIn)
    }

    func testInterruptedTaskStreamUsesBoundedAutomaticReattach() {
        XCTAssertEqual(
            FailureSignals.planStreamRecovery(end: .interrupted, attempt: 0),
            .reattach(
                attempt: 0,
                delayMs: 1_000,
                message: "Live output stopped — reattaching (1 of 5)… The work is still running on the box."
            )
        )
        if case .giveUp = FailureSignals.planStreamRecovery(end: .interrupted, attempt: 5) {
            // expected
        } else {
            XCTFail("task stream recovery must stop after its bounded ladder")
        }
    }

    func testLiveChatContinuesInPlace() {
        XCTAssertEqual(tvChatFollowUpAction(status: "running", runner: "codex"), .continueCurrent)
        XCTAssertEqual(tvChatFollowUpAction(status: "queued", runner: "codex"), .continueCurrent)
    }

    func testTerminalChatContinuesItsExactTaskSession() {
        for status in ["completed", "review", "failed", "stopped"] {
            XCTAssertEqual(tvChatFollowUpAction(status: status, runner: "opencode"), .continueCurrent)
        }
    }

    func testTerminalChatDoesNotInventARunnerFallback() {
        XCTAssertEqual(tvChatFollowUpAction(status: "completed", runner: nil), .continueCurrent)
    }

    func testChangingHiddenChatSettingsRequiresANewTask() {
        XCTAssertEqual(
            tvChatFollowUpAction(
                status: "running",
                runner: "opencode",
                selectedRunner: "codex",
                settingsChanged: true
            ),
            .settingsChangeBlocked(
                "This vibe stays in its existing runner and tmux session. Start a new task to use codex or different settings."
            )
        )
    }

    func testPortraitRuntimeAlwaysFitsInsideInsetTVViewport() {
        let bounds = CGRect(x: 16, y: 16, width: 1180, height: 820)
        let fit = tvRemoteAspectFitRect(
            imageSize: CGSize(width: 393, height: 852),
            in: bounds
        )
        XCTAssertGreaterThanOrEqual(fit.minX, bounds.minX)
        XCTAssertGreaterThanOrEqual(fit.minY, bounds.minY)
        XCTAssertLessThanOrEqual(fit.maxX, bounds.maxX)
        XCTAssertLessThanOrEqual(fit.maxY, bounds.maxY)
        XCTAssertEqual(fit.midX, bounds.midX, accuracy: 0.01)
        XCTAssertEqual(fit.midY, bounds.midY, accuracy: 0.01)
    }

    func testDecodedFrameMustContainUsablePixelsBeforeTransportIsGreen() {
        XCTAssertTrue(tvRemoteFrameSamplesAreBlank(Array(repeating: 0, count: 144)))
        XCTAssertTrue(tvRemoteFrameSamplesAreBlank(Array(repeating: 255, count: 144)))
        XCTAssertFalse(tvRemoteFrameSamplesAreBlank([0, 0, 4, 18, 64, 128, 220, 255]))
        XCTAssertFalse(tvRemoteFrameSamplesAreBlank(Array(repeating: 80, count: 144)),
                       "a deliberately solid app background is not automatically a failed capture")
    }

    func testInteractiveBrowserTargetCarriesDOMInspectionButNativePixelTargetsDoNot() {
        XCTAssertTrue(tvRemoteDOMInspectionAvailable(targetId: "browser-window"))
        XCTAssertFalse(tvRemoteDOMInspectionAvailable(targetId: "ios-simulator"))
        XCTAssertFalse(tvRemoteDOMInspectionAvailable(targetId: "android-emulator"))
        XCTAssertFalse(tvRemoteDOMInspectionAvailable(targetId: nil))
    }

    func testDOMHoverAndSelectUseTheSameClampedViewportCoordinates() {
        let size = CGSize(width: 393, height: 852)
        XCTAssertEqual(
            tvRemoteDOMPoint(normalized: CGPoint(x: 0.5, y: 0.25), sourceSize: size),
            CGPoint(x: 197, y: 213)
        )
        XCTAssertEqual(
            tvRemoteDOMPoint(normalized: CGPoint(x: -2, y: 4), sourceSize: size),
            CGPoint(x: 0, y: 852)
        )
    }

    func testRunnerCatalogueCarriesMeasuredDeepSeekChoice() throws {
        let data = Data(#"""
        {
          "runners": [{
            "id": "opencode", "name": "OpenCode", "installed": true,
            "ready": true, "isDefault": true,
            "models": [{
              "id": "deepseek/deepseek-v4-flash",
              "name": "DeepSeek V4 Flash",
              "provider": "deepseek",
              "isDefault": true
            }]
          }],
          "default": "opencode"
        }
        """#.utf8)

        let decoded = try JSONDecoder().decode(AgentRunnerList.self, from: data)
        XCTAssertEqual(decoded.default, "opencode")
        XCTAssertEqual(decoded.runners.first?.displayName, "OpenCode")
        XCTAssertEqual(decoded.runners.first?.models.first?.id, "deepseek/deepseek-v4-flash")
        XCTAssertEqual(decoded.runners.first?.models.first?.isDefault, true)
    }

    func testTVAutoConnectUsesPrimaryBeforeAlphabeticalOrder() throws {
        let now = 10_000_000.0
        let alphabetical = registryDevice(id: "a", name: "A Mac", online: true, lastHeartbeat: now)
        let primary = registryDevice(id: "ubuntu", name: "Ubuntu 4 GB", online: true, lastHeartbeat: now)

        let ranked = rankedAutoConnectTargets(
            devices: [alphabetical, primary],
            settings: try userSettings(primary: "ubuntu", secondary: nil),
            nowMs: now
        )

        XCTAssertEqual(ranked.first?.0.deviceId, "ubuntu")
        XCTAssertEqual(ranked.first?.1, .primary)
    }

    func testTVAutoConnectPreservesPrimaryThenSecondaryDespiteStalePresence() throws {
        let now = 10_000_000.0
        let primary = registryDevice(id: "primary", name: "Primary", online: false, lastHeartbeat: now)
        let secondary = registryDevice(id: "secondary", name: "Secondary", online: true, lastHeartbeat: now)
        let unrelated = registryDevice(id: "other", name: "A Different Box", online: true, lastHeartbeat: now)

        let ranked = rankedAutoConnectTargets(
            devices: [unrelated, primary, secondary],
            settings: try userSettings(primary: "primary", secondary: "secondary"),
            nowMs: now
        )

        XCTAssertEqual(ranked.map { $0.0.deviceId }, ["primary", "secondary"])
        XCTAssertEqual(ranked.map(\.1), [.primary, .secondary])
    }

    func testTVAutoConnectRanksLiveOwnerBeforeOfflineOwner() throws {
        let now = 10_000_000.0
        let offline = registryDevice(id: "offline", name: "A Offline Box", online: false, lastHeartbeat: now)
        let live = registryDevice(id: "live", name: "Z Live Box", online: true, lastHeartbeat: now)

        let ranked = rankedAutoConnectTargets(
            devices: [offline, live],
            settings: try userSettings(primary: nil, secondary: nil),
            nowMs: now
        )

        XCTAssertEqual(ranked.map { $0.0.deviceId }, ["live", "offline"])
        XCTAssertEqual(ranked.first?.1, .machine)
    }
}
