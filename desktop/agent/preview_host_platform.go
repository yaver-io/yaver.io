package main

import "runtime"

// preview_host_platform.go — WHERE is this workspace running?
//
// Every preview decision in workspace_preview_strategy.go was written assuming
// a LINUX Cloud Workspace, because that is what Hetzner gives us. That
// assumption is correct for the paid product and WRONG for two real cases:
//
//   - a BYO workspace on the user's own Mac (free tier, very common)
//   - a Mac host added specifically to serve iOS previews
//
// On those, `ios-simulator` stops being an impossibility and becomes the best
// available strategy. Returning "unsupported" there would turn away the one
// host that CAN do the thing being asked for.
//
// So the platform is DETECTED, never assumed. The agent knows what it is
// running on; nothing else needs configuring, and a config flag would be one
// more thing that can disagree with reality.

// HostPlatform is the OS the workspace agent is running on.
type HostPlatform string

const (
	HostLinux   HostPlatform = "linux"
	HostMacOS   HostPlatform = "macos"
	HostWindows HostPlatform = "windows"
	HostOther   HostPlatform = "other"
)

// DetectHostPlatform reports the platform this agent is running on.
func DetectHostPlatform() HostPlatform {
	switch runtime.GOOS {
	case "linux":
		return HostLinux
	case "darwin":
		return HostMacOS
	case "windows":
		return HostWindows
	default:
		return HostOther
	}
}

// HostCanRunIOSSimulator reports whether an iOS simulator is even possible.
//
// macOS ONLY, and that is a licence and technical fact rather than a gap we
// could close: the simulator ships inside Xcode, is Apple-proprietary, and
// Apple's terms restrict running the toolchain off Apple hardware. Darling does
// not cover Xcode. No amount of engineering changes this.
func HostCanRunIOSSimulator(p HostPlatform) bool { return p == HostMacOS }

// HostCanRunRedroid reports whether Android-in-a-container is possible.
//
// Redroid shares the HOST KERNEL — it needs real Linux, not a VM shim. On macOS
// Docker runs inside a Linux VM, and Redroid's kernel-module requirements
// (binder/ashmem) are not reliably available there, so this reports false
// rather than promising a container that will not start.
func HostCanRunRedroid(p HostPlatform) bool { return p == HostLinux }

// ResolvePreviewForHost applies host-platform reality to a strategy plan.
//
// This is the layer that was missing: workspace_preview_strategy.go answers
// "what does this STACK need", and this answers "what can this MACHINE do".
// Both are required, and conflating them is how a Mac workspace gets told its
// Swift app is unsupported while an iOS simulator sits idle on the same box.
func ResolvePreviewForHost(plan WorkspacePreviewPlan, host HostPlatform) WorkspacePreviewPlan {
	switch plan.Primary {
	case PreviewIOSSimulator:
		if HostCanRunIOSSimulator(host) {
			// The stack wanted a simulator and the host has one. This is the
			// case the Linux-only resolver refused outright.
			plan.Supported = true
			plan.Reason = "macOS host — iOS simulator available; Apple UI frameworks render natively here"
			return plan
		}
		plan.Supported = false
		plan.Reason = "iOS simulators require macOS and cannot run on a " + string(host) +
			" workspace. Compile and `swift test` DO run here — only the UI preview needs a Mac " +
			"host or your own device (`yaver wire push`)."
		return plan

	case PreviewRedroidWebRTC:
		if HostCanRunRedroid(host) {
			return plan
		}
		// Say what IS possible rather than only what is not: a paired Android
		// device does the same job and costs nothing.
		plan.Supported = false
		plan.Reason = "Redroid needs a real Linux kernel (binder/ashmem) and cannot run on a " +
			string(host) + " host. Use a Linux workspace, or a paired Android device."
		return plan
	}

	// Browser and Hermes strategies are host-agnostic: Chromium and Node run
	// everywhere, so nothing to adjust.
	return plan
}

// ResolveWorkspacePreviewForHost is the full resolution: stack, then directory
// (which matters only for Swift), then host reality.
//
// Order is deliberate. Stack detection says what the project NEEDS; directory
// inspection refines it for Swift's four runtimes; the host says what is
// POSSIBLE. Applying the host first would discard information — a Swift project
// on a Mac should still be told it is Tokamak rather than being routed straight
// to a simulator it does not need.
func ResolveWorkspacePreviewForHost(stack, dir string, hasPairedDevice bool) WorkspacePreviewPlan {
	plan := ResolveWorkspacePreviewForDir(stack, dir, hasPairedDevice)
	return ResolvePreviewForHost(plan, DetectHostPlatform())
}
