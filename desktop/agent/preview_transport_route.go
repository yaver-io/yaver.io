package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// preview_transport_route.go — pixels or a URL?
//
// ─── The waste this file removes ────────────────────────────────────────────
//
// Every web-renderable stack currently resolves to `chrome-webrtc`
// (workspace_preview_strategy.go:142). That means: run headless Chromium on
// the workspace, render the page, capture frames, encode H.264, push them over
// WebRTC — so that a viewer's BROWSER can display a picture of a web page it
// could have loaded itself.
//
// The cost of that round trip is not small and it is per-session:
//
//	direct URL     ~0 vCPU      an HTTP proxy hop
//	chrome-webrtc  ~1 vCPU      Chromium + encoder, pinned for the whole session
//	redroid-webrtc ~2 vCPU      Android + Chromium + encoder, ~6.5 GB floor
//
// On a 2c/4GB box a single chrome-webrtc preview eats half the machine to do
// nothing but re-render something already renderable. Multiply by the
// scale-to-zero credit model and it is the difference between a workspace that
// costs pennies per idle hour and one that never fits the 70% margin floor.
//
// ─── The rule ───────────────────────────────────────────────────────────────
//
//	If the app renders in a browser AND the viewer has a browser AND the
//	viewer can reach the dev server — send the URL. Otherwise send pixels.
//
// All three conjuncts are load-bearing, which is why this cannot be decided
// from the stack alone:
//
//   - "renders in a browser" — SwiftWasm, Flutter web, RN-web, Next.js yes;
//     native Swift/Kotlin no, and never will.
//   - "the viewer has a browser" — a WATCH does not. Routing a Next.js app to a
//     watch as a URL renders nothing at all; a car must not render an
//     interactive app while moving. Stack-only routing cannot see this.
//   - "can reach the dev server" — a phone on cellular with no relay HTTP proxy
//     cannot open the box's :8080 however web the app is. Pixels are then the
//     only path that works, and falling back is correct rather than a defeat.
//
// So WebRTC stops being the default and becomes what it should always have
// been: the transport for things that genuinely cannot be a URL — native
// Swift, native Kotlin, and any viewer that cannot render.

// PreviewTransport is how the preview physically reaches the viewer.
type PreviewTransport string

const (
	// TransportDirectURL: the viewer loads the dev server itself. No encoder,
	// no Chromium, no TURN — a proxied HTTP hop and nothing more.
	TransportDirectURL PreviewTransport = "direct-url"
	// TransportWebRTCVideo: the workspace renders and streams encoded frames.
	TransportWebRTCVideo PreviewTransport = "webrtc-video"
	// TransportDeviceBundle: no stream at all — a real device runs the real
	// artifact (Hermes bundle pulled via /dev/build-native).
	TransportDeviceBundle PreviewTransport = "device-bundle"
	// TransportStatusOnly: the viewer cannot display the app in any form.
	// Reporting state honestly beats streaming video to something with a 1.9"
	// screen and no input model.
	TransportStatusOnly PreviewTransport = "status-only"
)

// ViewerSurface is where the human is looking.
type ViewerSurface string

const (
	ViewerWeb    ViewerSurface = "web"
	ViewerMobile ViewerSurface = "mobile"
	ViewerTablet ViewerSurface = "tablet"
	ViewerTV     ViewerSurface = "tv"
	ViewerCar    ViewerSurface = "car"
	ViewerGlass  ViewerSurface = "glass"
	ViewerWatch  ViewerSurface = "watch"
)

// viewerCanRenderWebApp reports whether this surface can host a real,
// interactive web view of someone's app under development.
//
// Deliberately conservative. A surface that renders *something* is not the
// same as one that renders an app usefully.
func viewerCanRenderWebApp(v ViewerSurface) bool {
	switch v {
	case ViewerWeb, ViewerMobile, ViewerTablet, ViewerGlass:
		return true
	case ViewerTV:
		// tvOS has no general-purpose browser and no pointer. Video plus a
		// remote is the only honest option.
		return false
	case ViewerCar:
		// Never an interactive app on a car screen. Not a capability question —
		// CarPlay templates forbid it and it would be wrong regardless.
		return false
	case ViewerWatch:
		return false
	}
	return false
}

// viewerCanRenderVideo reports whether the surface can show a video stream.
func viewerCanRenderVideo(v ViewerSurface) bool {
	return v != ViewerWatch
}

// PreviewRoute is the decision, with its cost and its reasoning attached.
type PreviewRoute struct {
	Transport PreviewTransport `json:"transport"`
	Strategy  PreviewStrategy  `json:"strategy"`
	Viewer    ViewerSurface    `json:"viewer"`

	// EstimatedVCPU is the steady-state workspace cost of holding this preview
	// open. It exists so the credit model can price a session before starting
	// it rather than discovering the burn afterwards.
	EstimatedVCPU float64 `json:"estimatedVcpu"`

	// Feedback is how the Yaver Feedback SDK loop reaches the app on this
	// route. It changes WITH the transport, which is the part that is easy to
	// get wrong: the same app has an in-app SDK when it is a URL and a
	// viewer-triggered control message when it is video.
	Feedback FeedbackTransport `json:"feedback"`

	// Summary is one watch-sized sentence.
	Summary string `json:"summary"`
	Reason  string `json:"reason"`

	// Degraded marks a route taken because the preferred one was unavailable,
	// so a UI can say why the user is looking at video instead of their app.
	Degraded bool `json:"degraded,omitempty"`
}

// stackRendersInBrowser reports whether the stack produces something a browser
// can run directly.
//
// SwiftWasm is in this set on the strength of a measured build, not a hope:
// the Swift todo fixture compiles to a 9.6 MB wasm artifact in 5.42 s on
// Linux. Native Swift and native Kotlin are not in it and cannot be.
func stackRendersInBrowser(stack string) bool {
	s := strings.ToLower(strings.TrimSpace(stack))
	switch {
	case strings.Contains(s, "swiftwasm"), strings.Contains(s, "wasm"):
		return true
	case strings.Contains(s, "next"), strings.Contains(s, "vite"),
		strings.Contains(s, "astro"), strings.Contains(s, "remix"), s == "web":
		return true
	case strings.Contains(s, "flutter"):
		// Flutter is classed DevServerKindWeb (devserver_kind.go:37).
		return true
	case strings.Contains(s, "react-native"), strings.Contains(s, "expo"), s == "rn":
		// RN has a web target; it is a faithful-enough render for vibing, and
		// it is what chrome-webrtc was screenshotting anyway.
		return true
	}
	return false
}

// feedbackForStackOnURL picks the Feedback SDK flavour for a direct-URL route.
//
// Every browser-renderable stack has a real SDK to load, so this is never a
// promise the code cannot keep:
//
//	RN / Expo   yaver-feedback-react-native (web target loads the web SDK)
//	Flutter     yaver_feedback (pub.dev)
//	web/wasm    yaver-feedback-web
//
// SwiftWasm is the interesting case: the app is Swift, but it is running in a
// browser, so the WEB SDK applies. There is no Swift feedback SDK for a native
// app and there is no need for one here.
func feedbackForStackOnURL(stack string) FeedbackTransport {
	return FeedbackInAppSDK
}

// ResolvePreviewRoute decides pixels-or-URL for one (stack, viewer,
// reachability) triple.
//
// devServerReachable must come from an actual connection attempt, not a
// config flag. Half this session's bugs were inventory-says-yes /
// operation-says-no, and "the viewer can reach :8080" is exactly that shape —
// see probeDevServerReachable in preview_capability_probe.go.
func ResolvePreviewRoute(stack string, viewer ViewerSurface, devServerReachable bool) PreviewRoute {
	route := PreviewRoute{Viewer: viewer}

	// A watch can display neither an app nor a video usefully. Say so instead
	// of burning a vCPU encoding frames nobody can read.
	if !viewerCanRenderVideo(viewer) {
		route.Transport = TransportStatusOnly
		route.Strategy = PreviewUnsupported
		route.EstimatedVCPU = 0
		route.Feedback = FeedbackViewerTriggered
		route.Summary = "Build status only on this surface"
		route.Reason = "a watch cannot usefully render an app or a video stream; it shows build/preview STATE and can trigger actions"
		return route
	}

	browserRenderable := stackRendersInBrowser(stack)

	// ── The lightweight path ────────────────────────────────────────────
	if browserRenderable && viewerCanRenderWebApp(viewer) && devServerReachable {
		route.Transport = TransportDirectURL
		route.Strategy = PreviewDirectURL
		route.EstimatedVCPU = 0
		route.Feedback = feedbackForStackOnURL(stack)
		route.Summary = "Opens your app directly — no video"
		route.Reason = "the app renders in a browser and this viewer can reach the dev server, so the viewer loads it directly: no Chromium, no encoder, ~0 vCPU on the workspace"
		return route
	}

	// ── Pixels, and the reason we fell back to them ─────────────────────
	if browserRenderable {
		route.Transport = TransportWebRTCVideo
		route.Strategy = PreviewChromeWebRTC
		route.EstimatedVCPU = 1.0
		// The app is a web app but the viewer is not loading it, so the in-app
		// SDK is not present in the viewer's context — feedback must come
		// through the events DataChannel.
		route.Feedback = FeedbackViewerTriggered
		route.Degraded = true
		switch {
		case !viewerCanRenderWebApp(viewer):
			route.Summary = "Streaming video — this surface can't run the app"
			route.Reason = fmt.Sprintf("%s cannot host an interactive web view, so the workspace renders in headless Chromium and streams frames (~1 vCPU)", viewer)
		default:
			route.Summary = "Streaming video — can't reach the dev server"
			route.Reason = "the viewer cannot reach the dev server directly (cellular, no relay HTTP proxy, or firewalled), so the workspace renders and streams instead (~1 vCPU). Fix reachability to drop back to the free path"
		}
		return route
	}

	// ── Native: pixels are the only truthful option ─────────────────────
	s := strings.ToLower(stack)
	switch {
	case strings.Contains(s, "kotlin"), strings.Contains(s, "android"):
		route.Transport = TransportWebRTCVideo
		route.Strategy = PreviewRedroidWebRTC
		route.EstimatedVCPU = 2.0
		route.Feedback = FeedbackViewerTriggered
		route.Summary = "Streaming a real Android runtime"
		route.Reason = "native Kotlin cannot render in a browser; Redroid runs the real app and streams it (~6.5 GB floor, so a larger machine class). No native Kotlin feedback SDK exists — the viewer triggers feedback over the events DataChannel"
		return route
	case strings.Contains(s, "swift"), strings.Contains(s, "ios"):
		route.Transport = TransportWebRTCVideo
		route.Strategy = PreviewIOSSimulator
		route.EstimatedVCPU = 2.0
		route.Feedback = FeedbackViewerTriggered
		route.Summary = "Needs a Mac host"
		route.Reason = "native Swift UI needs an iOS simulator, which requires macOS — a Linux workspace cannot run one. Use a Mac host, or build the SwiftWasm target for the free direct-URL path"
		return route
	}

	route.Transport = TransportWebRTCVideo
	route.Strategy = PreviewChromeWebRTC
	route.EstimatedVCPU = 1.0
	route.Feedback = FeedbackViewerTriggered
	route.Summary = "Streaming video"
	route.Reason = "unrecognised stack — defaulting to a rendered stream, which works for anything that draws to a screen"
	return route
}

// PreviewRouteSavings reports the vCPU avoided by taking the URL path, for the
// cost telemetry the product is supposed to surface (`remote_cost`).
//
// Cost-awareness is a product requirement here, not a house rule: "lower dev
// opex" is the entire wedge, so a route that saves a vCPU should be able to
// prove it.
func PreviewRouteSavings(route PreviewRoute) float64 {
	if route.Transport == TransportDirectURL {
		return 1.0 // versus the chrome-webrtc it replaced
	}
	return 0
}

// JSON renders the route for every client surface — one payload for web,
// mobile, tablet, AR/VR, car and watch.
func (r PreviewRoute) JSON() ([]byte, error) { return json.Marshal(r) }
