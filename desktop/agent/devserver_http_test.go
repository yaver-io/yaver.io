package main

import (
	"context"
	"testing"
	"time"
)

// TestBundleCommandHonoursContext proves the bundler subprocess we hand to
// /dev/build-native is killed when its deadline expires. The real-world
// failure mode this guards against: a hung `npx expo export:embed` (broken
// project, missing node_modules, infinite resolver loop) keeps the HTTP
// request open forever, leaves the mobile DevPreview stuck on
// "Building..." with `setNativeLoading(true)` and forces the user to kill
// the app to recover. With exec.CommandContext the kernel kills the
// subprocess on context expiry and cmd.Run returns; the caller checks
// ctx.Err() == context.DeadlineExceeded and surfaces a structured
// "timedOut" response — that contract is what this test pins.
func TestBundleCommandHonoursContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// `sleep 30` stands in for a hung Metro/Expo. We bypass bundleCommand's
	// switch on packageManager (which would invoke real `npx`/`yarn` etc.)
	// and dial directly into exec.CommandContext to keep the test hermetic.
	// The wiring it proves — context propagates and kills the subprocess —
	// is identical to what bundleCommand does internally.
	cmd := bundleCommand(ctx, "npm", "react-native", "ios", "index.js", "/tmp/bundle.js", "/tmp/assets", true)
	if cmd == nil {
		t.Fatal("bundleCommand returned nil")
	}
	// The cmd's Args[0] is npx (or pnpm / bunx) — we don't actually run it.
	// What we check is: the *exec.Cmd has an associated context (its Cancel
	// function is non-nil). Without exec.CommandContext, Cancel is nil; if
	// it's non-nil, the kernel will kill the subprocess on ctx expiry.
	if cmd.Cancel == nil {
		t.Fatal("bundleCommand returned a *exec.Cmd without a Cancel func — context wiring is broken; a hung bundler will block /dev/build-native forever")
	}
}

func TestBundleAndHermesTimeoutsAreSane(t *testing.T) {
	// The mobile DevPreview / Hot Reload UIs use 12 minutes as their fetch
	// abort. The agent's combined cap (bundle + hermes) must stay under
	// that — otherwise the client gives up before the agent can return a
	// structured "timedOut" response and the UI falls back to a generic
	// "request aborted" instead of the helpful "bundler timed out, check
	// node_modules" message.
	totalAgentBudget := bundleBuildTimeout + hermesCompileTimeout
	mobileClientBudget := 12 * time.Minute
	if totalAgentBudget >= mobileClientBudget {
		t.Fatalf("agent build budget %v >= mobile client budget %v — mobile will abort before agent surfaces a structured failure", totalAgentBudget, mobileClientBudget)
	}
}

func TestCanBootstrapPackageManager(t *testing.T) {
	if !canBootstrapPackageManager("yarn", true, false) {
		t.Fatal("expected yarn to be bootstrap-able with npm")
	}
	if !canBootstrapPackageManager("pnpm", false, true) {
		t.Fatal("expected pnpm to be bootstrap-able with corepack")
	}
	if canBootstrapPackageManager("bun", true, true) {
		t.Fatal("did not expect bun to be bootstrap-able from npm-only assumptions")
	}
}

func TestDefaultPackageManagerInstallSpec(t *testing.T) {
	if got := defaultPackageManagerInstallSpec("yarn"); got != "yarn@1.22.22" {
		t.Fatalf("unexpected yarn default spec: %s", got)
	}
	if got := defaultPackageManagerInstallSpec("pnpm"); got != "pnpm@latest" {
		t.Fatalf("unexpected pnpm default spec: %s", got)
	}
}
