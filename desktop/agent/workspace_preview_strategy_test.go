package main

import (
	"strings"
	"testing"
)

func TestResolveWorkspacePreview(t *testing.T) {
	// RN with no device -> the viewer's browser loads the web target itself.
	// Streaming VIDEO of a web page to a browser costs ~1 vCPU for the whole
	// session to render something the client could already render; Chrome/WebRTC
	// stays as the fallback for viewers that cannot reach or cannot render.
	p := ResolveWorkspacePreview("react-native", false)
	if p.Primary != PreviewDirectURL || p.MachineClass != "standard" {
		t.Fatalf("rn/no-device: %+v", p)
	}
	if len(p.Fallbacks) == 0 || p.Fallbacks[0] != PreviewChromeWebRTC {
		t.Fatalf("rn/no-device must keep pixels as the fallback: %+v", p)
	}
	// RN WITH a paired device -> real hardware beats a browser render.
	p = ResolveWorkspacePreview("expo", true)
	if p.Primary != PreviewHermesBundle || p.Feedback != FeedbackDeviceSDK {
		t.Fatalf("rn/device: %+v", p)
	}
	// Flutter is a web dev server on the box, so the browser loads it directly.
	p = ResolveWorkspacePreview("flutter", false)
	if p.Primary != PreviewDirectURL || p.MachineClass != "standard" {
		t.Fatalf("flutter: %+v", p)
	}
	// SwiftWasm is the one Swift runtime that works on Linux — it compiles to
	// WebAssembly and runs in the viewer's browser. It must NOT fall into the
	// native-Swift branch, which refuses anything containing "swift" as
	// needing macOS.
	p = ResolveWorkspacePreview("swiftwasm", false)
	if !p.Supported || p.Primary != PreviewDirectURL {
		t.Fatalf("swiftwasm must be supported and served as a URL: %+v", p)
	}
	if p := ResolvePreviewForHost(ResolveWorkspacePreview("swiftwasm", false), HostLinux); !p.Supported {
		t.Fatalf("swiftwasm must stay supported on a LINUX host: %+v", p)
	}
	// Kotlin -> Redroid, and it MUST pull up the machine class.
	p = ResolveWorkspacePreview("kotlin", false)
	if p.Primary != PreviewRedroidWebRTC || p.MachineClass != "build" {
		t.Fatalf("kotlin: %+v", p)
	}
	// No native Kotlin SDK exists -> viewer-triggered, and no package name.
	if p.Feedback != FeedbackViewerTriggered || FeedbackSDKPackage("kotlin") != "" {
		t.Fatalf("kotlin feedback must be viewer-triggered with no SDK: %+v", p)
	}
	// iOS on a Linux workspace MUST refuse rather than degrade to web.
	p = ResolveWorkspacePreview("swift", false)
	if p.Supported || p.Primary != PreviewUnsupported {
		t.Fatalf("swift must be unsupported on a Linux workspace, got %+v", p)
	}
	if p.Primary == PreviewChromeWebRTC {
		t.Fatal("swift must never silently fall back to a web preview")
	}
	// Machine class must follow the strategy, not lag it.
	if PreviewStrategyMachineClass(PreviewRedroidWebRTC) != "build" {
		t.Fatal("redroid must require the build class")
	}
	// Only stacks with a real published SDK get a package name.
	for stack, want := range map[string]string{
		"react-native": "yaver-feedback-react-native",
		"flutter":      "yaver_feedback",
		"nextjs":       "yaver-feedback-web",
		"swift":        "",
		"kotlin":       "",
	} {
		if got := FeedbackSDKPackage(stack); got != want {
			t.Fatalf("FeedbackSDKPackage(%q)=%q want %q", stack, got, want)
		}
	}
}

func TestYaverSelfDevelopmentRecursionGuard(t *testing.T) {
	// Yaver-on-Yaver must be forced to chrome-webrtc EVEN WITH a paired device,
	// where Hermes would otherwise win. Pixels cannot trap the host.
	p := ResolveSelfDevelopmentPreview("yaver.io", "git@github.com:yaver-io/yaver.io.git", true)
	if p.Primary != PreviewChromeWebRTC {
		t.Fatalf("self-dev must force chrome-webrtc, got %q", p.Primary)
	}
	// Hermes must be REMOVED, not merely deprioritised: a fallback that can
	// trap the user is not a fallback.
	for _, f := range p.Fallbacks {
		if f == PreviewHermesBundle {
			t.Fatal("hermes must not remain a fallback for self-development")
		}
	}
	// The refusal must be explained, or it looks like a resolver bug.
	if !strings.Contains(p.Reason, "shake") {
		t.Fatalf("refusal must name the recursion cause, got: %s", p.Reason)
	}
	// A third-party RN app with a device still gets Hermes — the guard must be
	// narrow, not a blanket downgrade.
	q := ResolveSelfDevelopmentPreview("acme-todo", "git@github.com:acme/todo.git", true)
	if q.Primary != PreviewHermesBundle {
		t.Fatalf("third-party RN with a device should still use Hermes, got %q", q.Primary)
	}
}

func TestEscapeOwnership(t *testing.T) {
	// Streamed strategies are structurally safe.
	for _, s := range []PreviewStrategy{PreviewChromeWebRTC, PreviewRedroidWebRTC} {
		if EscapeOwnerFor(s, false) != EscapeNativeViewer {
			t.Fatalf("%q must be owned by the native viewer", s)
		}
		if EscapeOwnerFor(s, true) != EscapeNativeViewer {
			t.Fatalf("%q must stay safe under self-development", s)
		}
	}
	// Hermes is safe for a cooperating guest...
	if EscapeOwnerFor(PreviewHermesBundle, false) != EscapeContainerOverlay {
		t.Fatal("hermes guest should be owned by the container overlay")
	}
	// ...and AMBIGUOUS for Yaver-on-Yaver. That is the trap this guards.
	if EscapeOwnerFor(PreviewHermesBundle, true) != EscapeAmbiguous {
		t.Fatal("hermes self-development must report an ambiguous escape owner")
	}
}

func TestFeedbackBehaviourAcrossContexts(t *testing.T) {
	// Guest inside the container: container owns shake, guest SDK suppressed.
	b := ResolveFeedbackBehaviour("react-native", true, false)
	if b.Owner != EscapeContainerOverlay {
		t.Fatalf("in-container guest: %+v", b)
	}
	// SAME app standalone: its own SDK owns shake. The suppression rule must
	// not leak into this case.
	b = ResolveFeedbackBehaviour("react-native", false, false)
	if b.Transport != FeedbackInAppSDK || b.Owner != EscapeNativeViewer {
		t.Fatalf("standalone guest: %+v", b)
	}
	// Streamed: app's own SDK fires inside the stream, phone keeps its exit.
	b = ResolveFeedbackBehaviour("react-native", false, true)
	if b.Owner != EscapeNativeViewer {
		t.Fatalf("streamed: %+v", b)
	}
	// Native with no SDK, streamed: viewer-triggered over the events channel.
	b = ResolveFeedbackBehaviour("kotlin", false, true)
	if b.Transport != FeedbackViewerTriggered {
		t.Fatalf("kotlin streamed must be viewer-triggered: %+v", b)
	}
}
