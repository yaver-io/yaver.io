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
			Primary:      PreviewChromeWebRTC,
			MachineClass: "standard",
			Feedback:     FeedbackInAppSDK,
			Supported:    true,
			Reason:       "RN web target in headless Chrome, streamed over WebRTC — lightest path, runs on the default 2c/4GB box",
		}
		if hasPairedDevice {
			// A real phone beats a browser render of the web target: it is the
			// actual runtime, and the Hermes bundle path is what Yaver is best at.
			plan.Primary = PreviewHermesBundle
			plan.Feedback = FeedbackDeviceSDK
			plan.Fallbacks = []PreviewStrategy{PreviewChromeWebRTC, PreviewRedroidWebRTC}
			plan.Reason = "paired device present — agent builds a Hermes bundle (/dev/build-native) and the phone pulls it; Chrome/WebRTC as fallback"
		} else {
			plan.Fallbacks = []PreviewStrategy{PreviewHermesBundle, PreviewRedroidWebRTC}
		}
		return plan

	// ── Flutter ──────────────────────────────────────────────────────────
	case strings.Contains(s, "flutter"):
		return WorkspacePreviewPlan{
			Primary:      PreviewChromeWebRTC,
			Fallbacks:    []PreviewStrategy{PreviewRedroidWebRTC},
			MachineClass: "standard",
			// yaver_feedback exists on pub.dev, so Flutter gets the in-app loop.
			Feedback:  FeedbackInAppSDK,
			Supported: true,
			Reason:    "Flutter runs as a web dev server on the box (devserver_kind classes it web) and streams over WebRTC",
		}

	// ── Plain web ────────────────────────────────────────────────────────
	case strings.Contains(s, "next") || strings.Contains(s, "vite") ||
		strings.Contains(s, "astro") || strings.Contains(s, "remix") || s == "web":
		return WorkspacePreviewPlan{
			Primary:      PreviewChromeWebRTC,
			MachineClass: "standard",
			Feedback:     FeedbackInAppSDK, // yaver-feedback-web
			Supported:    true,
			Reason:       "web dev server streamed over WebRTC",
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
	case strings.Contains(s, "swift") || strings.Contains(s, "ios") || strings.Contains(s, "xcode"):
		return WorkspacePreviewPlan{
			Primary:      PreviewUnsupported,
			MachineClass: "standard",
			Feedback:     FeedbackViewerTriggered,
			Supported:    false,
			// Refuse, loudly and specifically. Falling back to a web preview
			// would render something that is NOT the user's app, which is worse
			// than an honest "this needs a Mac".
			Reason: "iOS simulators require macOS and cannot run on a Linux Cloud Workspace. Use a paired iPhone (yaver wire push) or a Mac host; a web preview would not be your app",
		}

	default:
		return WorkspacePreviewPlan{
			Primary:      PreviewChromeWebRTC,
			MachineClass: "standard",
			Feedback:     FeedbackInAppSDK,
			Supported:    true,
			Reason:       "unknown stack — defaulting to the lightest path (web dev server over WebRTC)",
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
