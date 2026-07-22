package main

import "testing"

// The cost claim is the whole point of the direct-URL route, so it is the
// first thing tested: a web app viewed in a browser must not spin up an
// encoder. If this test ever goes green-but-wrong, the workspace silently
// costs a vCPU per session and the 70% margin floor quietly stops holding.
func TestWebAppInBrowserCostsNoVCPU(t *testing.T) {
	for _, stack := range []string{"next", "vite", "flutter", "expo", "react-native", "swiftwasm"} {
		route := ResolvePreviewRoute(stack, ViewerWeb, true)
		if route.Transport != TransportDirectURL {
			t.Errorf("%s in a browser: transport = %q, want direct-url — streaming video to a browser that could load the page is the exact waste this route exists to remove",
				stack, route.Transport)
		}
		if route.EstimatedVCPU != 0 {
			t.Errorf("%s direct-url: estimatedVcpu = %v, want 0", stack, route.EstimatedVCPU)
		}
		if route.Feedback != FeedbackInAppSDK {
			t.Errorf("%s direct-url: feedback = %q, want in-app-sdk — the viewer loads the app, so the real SDK is present",
				stack, route.Feedback)
		}
	}
}

// Native Swift and native Kotlin can NEVER be a URL. If a future edit lets
// them fall into the direct-URL branch, the user is shown something that is
// not their app — worse than showing nothing.
func TestNativeStacksNeverRouteToDirectURL(t *testing.T) {
	for _, stack := range []string{"swift", "ios", "kotlin", "android"} {
		route := ResolvePreviewRoute(stack, ViewerWeb, true)
		if route.Transport == TransportDirectURL {
			t.Fatalf("%s routed to direct-url — native UI does not render in a browser; this would show the user a different app than the one they are building", stack)
		}
	}
}

// A watch must not receive a video stream. Encoding frames for a 1.9" screen
// with no input model burns a vCPU to produce something unreadable.
func TestWatchGetsStatusOnlyNotVideo(t *testing.T) {
	for _, stack := range []string{"next", "swift", "kotlin", "flutter"} {
		route := ResolvePreviewRoute(stack, ViewerWatch, true)
		if route.Transport != TransportStatusOnly {
			t.Errorf("%s on a watch: transport = %q, want status-only", stack, route.Transport)
		}
		if route.EstimatedVCPU != 0 {
			t.Errorf("%s on a watch: burning %v vCPU for a surface that cannot display it", stack, route.EstimatedVCPU)
		}
	}
}

// A car must never render an interactive app, however web the stack is. This
// is not a capability limit that better code could lift.
func TestCarNeverRendersInteractiveApp(t *testing.T) {
	route := ResolvePreviewRoute("next", ViewerCar, true)
	if route.Transport == TransportDirectURL {
		t.Fatal("car routed to an interactive web view — CarPlay templates forbid it and it would be unsafe regardless")
	}
}

// Unreachable dev server must degrade to video AND say so. A silent fallback
// leaves the user paying for an encoder without knowing a network fix would
// make it free.
func TestUnreachableDevServerDegradesAudibly(t *testing.T) {
	route := ResolvePreviewRoute("next", ViewerMobile, false)
	if route.Transport != TransportWebRTCVideo {
		t.Fatalf("transport = %q, want webrtc-video when the dev server is unreachable", route.Transport)
	}
	if !route.Degraded {
		t.Error("degraded = false — the UI cannot tell the user that fixing reachability makes this free")
	}
	if route.Feedback != FeedbackViewerTriggered {
		t.Errorf("feedback = %q, want viewer-triggered — the viewer is not loading the app, so no in-app SDK is present in its context", route.Feedback)
	}
}

// Same app, same viewer, only reachability differs — the route must flip. This
// is the assertion that stack-only routing cannot satisfy, and the reason
// reachability is a parameter rather than a config flag.
func TestReachabilityAloneFlipsTheRoute(t *testing.T) {
	reachable := ResolvePreviewRoute("flutter", ViewerMobile, true)
	unreachable := ResolvePreviewRoute("flutter", ViewerMobile, false)
	if reachable.Transport == unreachable.Transport {
		t.Fatal("reachability did not change the route — then it is not being consulted, and every session pays the encoder cost")
	}
	if reachable.EstimatedVCPU >= unreachable.EstimatedVCPU {
		t.Errorf("reachable cost %v is not cheaper than unreachable %v", reachable.EstimatedVCPU, unreachable.EstimatedVCPU)
	}
}

// SwiftWasm is the load-bearing case for selling Swift previews on Linux: it
// must reach the FREE path, because that is the entire argument for compiling
// Swift to wasm rather than renting a Mac.
func TestSwiftWasmReachesTheFreePath(t *testing.T) {
	route := ResolvePreviewRoute("swiftwasm", ViewerWeb, true)
	if route.Transport != TransportDirectURL {
		t.Fatalf("swiftwasm transport = %q, want direct-url — a 9.6 MB wasm bundle loads in the viewer's own browser", route.Transport)
	}
	if PreviewRouteSavings(route) != 1.0 {
		t.Error("savings not reported — cost telemetry is a product requirement, not a nicety")
	}
	// Native Swift on the same viewer must NOT get the free path, or the
	// distinction being sold is fictional.
	native := ResolvePreviewRoute("swift", ViewerWeb, true)
	if native.Transport == TransportDirectURL {
		t.Fatal("native swift also got direct-url — then the swiftwasm result proves nothing")
	}
}

// TV has no general-purpose browser: it gets video, not a URL.
func TestTVGetsVideoNotURL(t *testing.T) {
	route := ResolvePreviewRoute("next", ViewerTV, true)
	if route.Transport != TransportWebRTCVideo {
		t.Errorf("tv transport = %q, want webrtc-video — tvOS has no browser and no pointer", route.Transport)
	}
}

// Every route must carry a reason and a watch-length summary. A diagnosis the
// UI cannot render is a diagnosis the user never sees.
func TestEveryRouteExplainsItself(t *testing.T) {
	viewers := []ViewerSurface{ViewerWeb, ViewerMobile, ViewerTablet, ViewerTV, ViewerCar, ViewerGlass, ViewerWatch}
	for _, v := range viewers {
		for _, stack := range []string{"next", "swift", "kotlin", "swiftwasm", "mystery-stack"} {
			route := ResolvePreviewRoute(stack, v, true)
			if route.Reason == "" {
				t.Errorf("%s/%s: empty reason", stack, v)
			}
			if route.Summary == "" {
				t.Errorf("%s/%s: empty summary", stack, v)
			}
			if len(route.Summary) > 60 {
				t.Errorf("%s/%s: summary %q is %d chars — too long for a watch, so too long for anyone",
					stack, v, route.Summary, len(route.Summary))
			}
			if _, err := route.JSON(); err != nil {
				t.Errorf("%s/%s: JSON failed: %v", stack, v, err)
			}
		}
	}
}
