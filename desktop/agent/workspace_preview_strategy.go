package main

import "strings"

// workspace_preview_strategy.go — how a Cloud Workspace previews the user's app.
//
// A Cloud Workspace is a LINUX box (Hetzner). That single fact decides most of
// this file, and it is the thing most easily forgotten when reasoning from the
// mobile app's point of view rather than the box's.
//
// The ladder, cheapest and most portable first:
//
//   1. chrome-webrtc   — run the app's WEB target in headless Chrome on the box
//                        and stream it over WebRTC. Works for React Native (web
//                        target) and Flutter (which the dev-server layer already
//                        classes as web). No emulator, no GPU, no device.
//                        Runs on the 2c/4GB default — this is WHY the default
//                        class can be 2c/4GB and the $29 tier holds ~71%.
//
//   2. hermes-bundle   — the box BUILDS a Hermes bundle (POST /dev/build-native)
//                        and the user's OWN phone pulls and loads it into the
//                        Yaver container. RN/Expo only (gated in the mobile Hot
//                        Reload tab). Costs us nothing on the device side and is
//                        a more honest test than any emulator, because it is
//                        real hardware.
//                        ⚠️ Agent-PULL, not push: the legacy `yaver push` to
//                        device port 8347 is dead on both platforms.
//
//   3. redroid-webrtc  — Android-in-a-container on the box, streamed over
//                        WebRTC. The fallback for Kotlin/native Android when the
//                        user has no device. Needs ~6.5 GB before the app under
//                        test loads, so it forces a bigger machine class and is
//                        strictly opt-in.
//
//   4. ios-simulator   — ⚠️ IMPOSSIBLE ON A CLOUD WORKSPACE. An iOS simulator
//                        requires macOS; a Hetzner Linux box cannot run one, and
//                        no amount of configuration changes that. Swift/iOS
//                        preview needs either the user's own Mac (BYO) or a
//                        Mac host. Saying "unsupported here" is the correct
//                        answer — silently downgrading a Swift project to a web
//                        preview would show the user something that is not
//                        their app.

// PreviewStrategy is how a workspace renders the app under development.
type PreviewStrategy string

const (
	// PreviewDirectURL is the LIGHTEST path and the correct default for
	// anything a browser can run: the viewer opens the dev server itself, so
	// the workspace spends no vCPU rendering or encoding. See
	// preview_transport_route.go for when it applies — it needs a
	// browser-renderable stack, a viewer that can host a web view, AND proven
	// reachability, so it is decided per (stack, viewer, network), never from
	// the stack alone.
	PreviewDirectURL PreviewStrategy = "direct-url"

	PreviewChromeWebRTC PreviewStrategy = "chrome-webrtc"
	// NOT a push. The agent BUILDS the Hermes bundle (POST /dev/build-native)
	// and the mobile container PULLS it. The old `yaver push` → device port
	// 8347 path is dead on both platforms; naming this "push" would send a
	// reader hunting for a transport that no longer exists.
	PreviewHermesBundle  PreviewStrategy = "hermes-bundle"
	PreviewRedroidWebRTC PreviewStrategy = "redroid-webrtc"
	PreviewIOSSimulator  PreviewStrategy = "ios-simulator"
	PreviewUnsupported   PreviewStrategy = "unsupported"
)

// FeedbackTransport is how the Yaver Feedback SDK loop reaches the app.
//
// This matters because there is NO native Kotlin or Swift feedback SDK —
// sdk/feedback/ ships react-native, web, flutter, unity and browser-extension
// only. Native apps get the loop a different way: the viewer pushes a
// `launch-feedback` control message down the WebRTC events DataChannel
// (remote_runtime.go), or a `shake` command that the box injects into the
// emulator as a hardware event. Pretending a native SDK exists is how a feature
// gets promised and then silently does nothing.
type FeedbackTransport string

const (
	// In-app SDK present: react-native, web, flutter.
	FeedbackInAppSDK FeedbackTransport = "in-app-sdk"
	// No in-app SDK: the viewer triggers over the WebRTC events channel.
	FeedbackViewerTriggered FeedbackTransport = "viewer-triggered"
	// Hermes bundle loaded on a real device — the guest app's own RN SDK fires,
	// and the Yaver container suppresses its own shake/feedback handling so the
	// two overlays do not collide (RN SDK 0.5.5+, via the YaverInfo module).
	FeedbackDeviceSDK FeedbackTransport = "device-sdk"
)

// WorkspacePreviewPlan is the complete answer for one project on one workspace.
type WorkspacePreviewPlan struct {
	Primary      PreviewStrategy   `json:"primary"`
	Fallbacks    []PreviewStrategy `json:"fallbacks,omitempty"`
	MachineClass string            `json:"machineClass"`
	Feedback     FeedbackTransport `json:"feedback"`
	// Reason is operator/user-facing: why this strategy, and when it is a
	// refusal rather than a choice.
	Reason string `json:"reason"`
	// Supported is false when a Cloud Workspace genuinely cannot do this and
	// the user needs different hardware. Never silently degrade instead.
	Supported bool `json:"supported"`
}

// ResolveWorkspacePreview picks the strategy ladder for a stack on a LINUX
// Cloud Workspace.
//
// `hasPairedDevice` matters because the Hermes path needs the user's real
// phone. If they have one it outranks an emulator: real hardware, zero server
// cost, and no extra machine class.
func ResolveWorkspacePreview(stack string, hasPairedDevice bool) WorkspacePreviewPlan {
	s := strings.ToLower(strings.TrimSpace(stack))

	switch {
	// ── React Native / Expo ──────────────────────────────────────────────
	case strings.Contains(s, "react-native") || strings.Contains(s, "expo") || s == "rn":
		plan := WorkspacePreviewPlan{
			Primary:      PreviewDirectURL,
			Fallbacks:    []PreviewStrategy{PreviewChromeWebRTC},
			MachineClass: "standard",
			Feedback:     FeedbackInAppSDK,
			Supported:    true,
			Reason:       "RN web target served straight to the viewer's browser — no encoder, no Chromium, ~0 vCPU; Chrome/WebRTC only when the viewer cannot reach the dev server or cannot render",
		}
		if hasPairedDevice {
			// A real phone beats a browser render of the web target: it is the
			// actual runtime, and the Hermes bundle path is what Yaver is best at.
			plan.Primary = PreviewHermesBundle
			plan.Feedback = FeedbackDeviceSDK
			plan.Fallbacks = []PreviewStrategy{PreviewChromeWebRTC, PreviewRedroidWebRTC}
			plan.Reason = "paired device present — agent builds a Hermes bundle (/dev/build-native) and the phone pulls it; Chrome/WebRTC as fallback"
		} else {
			plan.Fallbacks = []PreviewStrategy{PreviewChromeWebRTC, PreviewHermesBundle, PreviewRedroidWebRTC}
		}
		return plan

	// ── Flutter ──────────────────────────────────────────────────────────
	case strings.Contains(s, "flutter"):
		return WorkspacePreviewPlan{
			Primary:      PreviewDirectURL,
			Fallbacks:    []PreviewStrategy{PreviewChromeWebRTC, PreviewRedroidWebRTC},
			MachineClass: "standard",
			// yaver_feedback exists on pub.dev, so Flutter gets the in-app loop.
			Feedback:  FeedbackInAppSDK,
			Supported: true,
			Reason:    "Flutter runs as a web dev server on the box (devserver_kind classes it web); the viewer's browser loads it directly at ~0 vCPU",
		}

	// ── Plain web ────────────────────────────────────────────────────────
	case strings.Contains(s, "next") || strings.Contains(s, "vite") ||
		strings.Contains(s, "astro") || strings.Contains(s, "remix") || s == "web":
		return WorkspacePreviewPlan{
			Primary:      PreviewDirectURL,
			Fallbacks:    []PreviewStrategy{PreviewChromeWebRTC},
			MachineClass: "standard",
			Feedback:     FeedbackInAppSDK, // yaver-feedback-web
			Supported:    true,
			Reason:       "web dev server loaded directly by the viewer's browser — streaming video of a web page to a browser that can render it costs a vCPU to accomplish nothing",
		}

	// ── Native Android / Kotlin ──────────────────────────────────────────
	case strings.Contains(s, "kotlin") || strings.Contains(s, "android") || strings.Contains(s, "gradle"):
		return WorkspacePreviewPlan{
			Primary:   PreviewRedroidWebRTC,
			Supported: true,
			// Redroid needs ~6.5 GB before the app loads; 4 GB thrashes.
			MachineClass: "build",
			// NO native Kotlin feedback SDK exists. The viewer pushes
			// launch-feedback down the events channel instead.
			Feedback: FeedbackViewerTriggered,
			Reason:   "native Android needs a real Android runtime — Redroid on the box, streamed over WebRTC (forces a larger machine class)",
		}

	// ── Native iOS / Swift ───────────────────────────────────────────────
	// NOTE: this stack-string path is the COARSE answer, used when only a label
	// is known. When a directory is available, ResolveWorkspacePreviewForDir
	// inspects the project and can route Tokamak/SwiftWasm and server-side
	// Swift to Linux instead — "Swift" is four different runtimes, and a flat
	// refusal turns away developers whose loop would work here today.
	// ── SwiftWasm / Tokamak — Swift that runs in a BROWSER ───────────────
	//
	// Must precede the native-Swift case below, which matches any string
	// containing "swift" and refuses it as needing macOS. That ordering bug
	// made the one Swift runtime which provably works on Linux report
	// "unsupported" on Linux: the Swift todo fixture compiles to a 9.6 MB wasm
	// artifact in 5.42 s in a swift:6.3.0-jammy container, and the result is a
	// URL a browser opens — no simulator, no Mac, no encoder.
	case strings.Contains(s, "swiftwasm") || strings.Contains(s, "tokamak") ||
		(strings.Contains(s, "swift") && strings.Contains(s, "wasm")):
		return WorkspacePreviewPlan{
			Primary:      PreviewDirectURL,
			Fallbacks:    []PreviewStrategy{PreviewChromeWebRTC},
			MachineClass: "standard",
			// The app is Swift but it runs in a browser, so the WEB SDK is the
			// one that applies. There is no native Swift feedback SDK and none
			// is needed here.
			Feedback:  FeedbackInAppSDK,
			Supported: true,
			Reason:    "SwiftWasm compiles to WebAssembly and runs in the viewer's browser — fully supported on a Linux workspace, no Mac and no simulator required",
		}

	case strings.Contains(s, "swift") || strings.Contains(s, "ios") || strings.Contains(s, "xcode"):
		// State what the STACK NEEDS — an iOS simulator — and let
		// ResolvePreviewForHost decide whether this machine has one. That
		// separation is this file's stated contract ("what does this stack
		// need" vs "what can this machine do"), and returning PreviewUnsupported
		// here broke it: ResolvePreviewForHost only upgrades a plan whose
		// Primary is PreviewIOSSimulator, so a hardcoded "unsupported" could
		// never be reconsidered. On a Mac with a booted simulator and
		// ios-simulator=true in the WebRTC doctor, Swift still reported
		// supported=false — and said "cannot run on a Linux Cloud Workspace"
		// while running on macOS. Verified on this machine 2026-07-22.
		//
		// The refusal is not lost, it MOVES to the layer that can see the host:
		// on Linux, ResolvePreviewForHost still returns Supported=false with
		// the specific remedy. Falling back to a web preview remains wrong —
		// it would render something that is NOT the user's app.
		return WorkspacePreviewPlan{
			Primary:      PreviewIOSSimulator,
			MachineClass: "standard",
			Feedback:     FeedbackViewerTriggered,
			Supported:    true,
			Reason:       "native Apple UI — needs an iOS simulator, which exists only on a macOS host (or use a paired iPhone via `yaver wire push`)",
		}

	default:
		return WorkspacePreviewPlan{
			Primary:      PreviewDirectURL,
			Fallbacks:    []PreviewStrategy{PreviewChromeWebRTC},
			MachineClass: "standard",
			Feedback:     FeedbackInAppSDK,
			Supported:    true,
			Reason:       "unknown stack — defaulting to the lightest path (web dev server loaded directly by the viewer)",
		}
	}
}

// PreviewStrategyMachineClass is the class a strategy REQUIRES.
//
// Kept separate from the plan so the placement layer can ask the question
// directly, and so a strategy change cannot silently leave the machine class
// behind — a Redroid session on a 4 GB box does not fail cleanly, it thrashes.
func PreviewStrategyMachineClass(p PreviewStrategy) string {
	switch p {
	case PreviewRedroidWebRTC:
		return "build" // 8c/16GB — Android runtime plus the app under test
	case PreviewChromeWebRTC, PreviewHermesBundle:
		// 2c/4GB — headless Chrome and Metro both fit for a normal project.
		// Metro on a large MONOREPO is the known ceiling; that is handled by
		// detecting it and offering the upgrade, not by pre-provisioning.
		return "standard"
	default:
		return "standard"
	}
}

// FeedbackSDKPackage names the SDK a stack should install, or "" when none
// exists and the loop is viewer-triggered.
//
// Returning "" for Kotlin/Swift is deliberate and load-bearing: there is no
// yaver-feedback-kotlin or -swift, and inventing a name here would send a user
// hunting for a package that does not exist.
func FeedbackSDKPackage(stack string) string {
	s := strings.ToLower(strings.TrimSpace(stack))
	switch {
	case strings.Contains(s, "react-native") || strings.Contains(s, "expo"):
		return "yaver-feedback-react-native"
	case strings.Contains(s, "flutter"):
		return "yaver_feedback"
	case strings.Contains(s, "next") || strings.Contains(s, "vite") ||
		strings.Contains(s, "astro") || strings.Contains(s, "remix") || s == "web":
		return "yaver-feedback-web"
	case strings.Contains(s, "unity"):
		return "yaver-feedback-unity"
	default:
		return "" // kotlin/swift/native — viewer-triggered, no in-app SDK
	}
}

// ─── Recursion guard: developing Yaver with Yaver ───────────────────────────
//
// Yaver's mobile app is a CONTAINER: it loads third-party RN apps in-process
// from a Hermes bundle, and it owns the shake gesture because shake is how you
// reach the "Reload / Back to Yaver" overlay. Guest apps cooperate — the RN
// feedback SDK sees YaverInfo.isYaver and suppresses its own shake handler so
// the two overlays cannot collide.
//
// Load YAVER INTO YAVER and that contract has no answer. Both layers are the
// same code, both believe they own shake, and neither can tell from inside the
// process which one is the host. The failure is not cosmetic — it is being
// STUCK: a preview you cannot exit, because the only escape gesture is being
// consumed by the thing you are trying to escape. Force-quit becomes the user's
// remaining option, which is the one experience a dogfooding tool must never
// produce.
//
// The rule that resolves it:
//
//	THE ESCAPE HATCH MUST LIVE IN A LAYER THE PREVIEWED APP CANNOT REACH.
//
// chrome-webrtc satisfies that structurally: the inner Yaver renders into a
// browser ON THE BOX and arrives at the phone as VIDEO. It cannot register a
// gesture handler on the host, cannot draw over the exit button, and cannot
// wedge the outer shell. The recursion collapses because the two layers are no
// longer in the same process.
//
// So this is a REFUSAL, not a preference — and it must be loud, because a
// silent downgrade would look like a bug in the strategy resolver.

// IsYaverSelfDevelopment reports whether the project under development is Yaver
// itself. Detected from the repo identity rather than a flag: a flag can be
// forgotten, and the consequence of missing this is a trapped user.
func IsYaverSelfDevelopment(projectSlug, repoURL string) bool {
	needle := strings.ToLower(projectSlug + " " + repoURL)
	return strings.Contains(needle, "yaver.io") ||
		strings.Contains(needle, "yaver-io/yaver") ||
		strings.Contains(needle, "io.yaver.mobile")
}

// EscapeOwner is the layer that owns the exit affordance for a preview.
type EscapeOwner string

const (
	// The phone's NATIVE chrome, outside the streamed surface. The previewed
	// app is pixels and can never reach it.
	EscapeNativeViewer EscapeOwner = "native-viewer"
	// The Yaver container's ShakeDetector + overlay. Safe ONLY while the guest
	// honours the suppression contract.
	EscapeContainerOverlay EscapeOwner = "container-overlay"
	// No safe owner — the previewed app can capture the escape. Never ship this.
	EscapeAmbiguous EscapeOwner = "ambiguous"
)

// EscapeOwnerFor returns who owns the exit for a strategy.
func EscapeOwnerFor(p PreviewStrategy, selfDev bool) EscapeOwner {
	switch p {
	case PreviewChromeWebRTC, PreviewRedroidWebRTC:
		// Pixels. Structurally safe, self-development or not.
		return EscapeNativeViewer
	case PreviewHermesBundle:
		if selfDev {
			// Two identical layers, both claiming shake. This is the trap.
			return EscapeAmbiguous
		}
		return EscapeContainerOverlay
	default:
		return EscapeNativeViewer
	}
}

// ResolveSelfDevelopmentPreview is ResolveWorkspacePreview with the recursion
// guard applied.
//
// Yaver-on-Yaver is forced onto chrome-webrtc even when a device is paired and
// Hermes would otherwise win. Two independent reasons, either sufficient:
//
//  1. SAFETY — Hermes puts two identical shake-owning layers in one process
//     (see above). Pixels cannot trap the host.
//  2. SPEED — the chrome-webrtc reload chain is save → HMR → repaint → frame,
//     roughly 200-600ms. The Hermes chain is save → Metro rebuild → HBC compile
//     → device pull → bridge reload, roughly 2-6s. An order of magnitude, on the
//     loop we run most.
//
// Accepted limitation, stated rather than hidden: this exercises the RN WEB
// target, not the native container. Hermes loader, ShakeDetectingWindow and
// YaverHTTPServer are NOT covered — native-container changes still need
// `yaver wire push` to a real device.
func ResolveSelfDevelopmentPreview(projectSlug, repoURL string, hasPairedDevice bool) WorkspacePreviewPlan {
	plan := ResolveWorkspacePreview("react-native", hasPairedDevice)
	if !IsYaverSelfDevelopment(projectSlug, repoURL) {
		return plan
	}
	plan.Primary = PreviewChromeWebRTC
	// Hermes is REMOVED from the fallbacks, not merely deprioritised. A
	// fallback that can trap the user is not a fallback.
	plan.Fallbacks = nil
	plan.MachineClass = "standard"
	plan.Feedback = FeedbackInAppSDK
	plan.Supported = true
	plan.Reason = "Yaver developing Yaver: forced to chrome-webrtc. Hermes would load Yaver " +
		"into Yaver — two identical layers both owning shake, so the preview could not be " +
		"exited. Streaming pixels keeps the escape in the phone's native chrome, where the " +
		"previewed app cannot reach it. Covers the RN web target; native-container changes " +
		"still need `yaver wire push`."
	return plan
}

// FeedbackBehaviour describes what shake does for one app in one context.
//
// The same gesture means different things depending on who is listening, and
// conflating them is how "feedback works everywhere" silently stops being true.
type FeedbackBehaviour struct {
	Transport FeedbackTransport
	Owner     EscapeOwner
	Detail    string
}

// ResolveFeedbackBehaviour answers the four cases that actually occur.
//
// `insideContainer` distinguishes a guest app loaded into the Yaver container
// (Hermes) from the SAME app installed standalone from TestFlight/Play — the
// suppression rule inverts between them, and getting it backwards means either
// two overlays fight or none appears at all.
func ResolveFeedbackBehaviour(stack string, insideContainer bool, streamed bool) FeedbackBehaviour {
	pkg := FeedbackSDKPackage(stack)

	switch {
	case streamed:
		// WebRTC: the app runs on the box. A phone shake becomes a `shake`
		// session command that the box injects as a synthetic event, so the
		// app's OWN SDK fires inside the stream. The phone keeps its exit —
		// two loops, no collision, because one is pixels.
		if pkg == "" {
			return FeedbackBehaviour{
				Transport: FeedbackViewerTriggered,
				Owner:     EscapeNativeViewer,
				Detail:    "no in-app SDK for this stack — viewer pushes launch-feedback down the events channel",
			}
		}
		return FeedbackBehaviour{
			Transport: FeedbackInAppSDK,
			Owner:     EscapeNativeViewer,
			Detail:    "app's own SDK fires inside the streamed surface; the phone's exit stays native",
		}

	case insideContainer:
		// Hermes guest: the CONTAINER owns shake, and the guest SDK suppresses
		// itself via YaverInfo.isYaver (RN SDK 0.5.5+). Without that
		// suppression both overlays fire and neither is usable.
		return FeedbackBehaviour{
			Transport: FeedbackViewerTriggered,
			Owner:     EscapeContainerOverlay,
			Detail:    "guest SDK suppressed by YaverInfo.isYaver; container overlay owns shake (Reload / Back to Yaver)",
		}

	default:
		// Standalone install (TestFlight / Play). No container, so the guest's
		// own SDK owns shake outright — this is the case the suppression rule
		// must NOT affect, and the reason suppression is runtime-detected
		// rather than a build flag.
		if pkg == "" {
			return FeedbackBehaviour{
				Transport: FeedbackViewerTriggered,
				Owner:     EscapeNativeViewer,
				Detail:    "standalone native app with no SDK — feedback requires a Yaver session",
			}
		}
		return FeedbackBehaviour{
			Transport: FeedbackInAppSDK,
			Owner:     EscapeNativeViewer,
			Detail:    "standalone: the app's own SDK owns shake, unaffected by container suppression",
		}
	}
}

// ResolveWorkspacePreviewForDir is ResolveWorkspacePreview with the project on
// disk available, which matters for exactly one stack: Swift.
//
// "Swift" is not one runtime. Tokamak/SwiftWasm compiles to WebAssembly and
// renders in a browser; server-side Swift IS a web server; Apple SwiftUI and
// UIKit need macOS; a plain package has no UI at all. Only the last two are
// Mac-bound, and every one of them compiles and tests on Linux.
//
// Routing on the stack LABEL alone cannot distinguish them, so a label-only
// caller gets the conservative answer (Mac required) while a caller with a
// directory gets the accurate one. Preferring the accurate answer when we can
// have it is the difference between "Swift: unsupported" and a workspace a
// Swift developer can actually use.
func ResolveWorkspacePreviewForDir(stack, dir string, hasPairedDevice bool) WorkspacePreviewPlan {
	s := strings.ToLower(strings.TrimSpace(stack))
	isSwift := strings.Contains(s, "swift") || strings.Contains(s, "ios") || strings.Contains(s, "xcode")
	if !isSwift || strings.TrimSpace(dir) == "" {
		return ResolveWorkspacePreview(stack, hasPairedDevice)
	}
	detection := DetectSwiftProject(dir)
	if detection.Kind == SwiftKindUnknown {
		// Nothing identifiable on disk — fall back to the label-based answer
		// rather than inventing a render target.
		return ResolveWorkspacePreview(stack, hasPairedDevice)
	}
	return ResolveSwiftPreview(detection)
}
