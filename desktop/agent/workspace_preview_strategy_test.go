package main

import "testing"

func TestResolveWorkspacePreview(t *testing.T) {
	// RN with no device -> Chrome/WebRTC on the cheap box (the whole reason
	// the default class can be 2c/4GB).
	p := ResolveWorkspacePreview("react-native", false)
	if p.Primary != PreviewChromeWebRTC || p.MachineClass != "standard" {
		t.Fatalf("rn/no-device: %+v", p)
	}
	// RN WITH a paired device -> real hardware beats a browser render.
	p = ResolveWorkspacePreview("expo", true)
	if p.Primary != PreviewHermesBundle || p.Feedback != FeedbackDeviceSDK {
		t.Fatalf("rn/device: %+v", p)
	}
	// Flutter is a web dev server on the box.
	p = ResolveWorkspacePreview("flutter", false)
	if p.Primary != PreviewChromeWebRTC || p.MachineClass != "standard" {
		t.Fatalf("flutter: %+v", p)
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
