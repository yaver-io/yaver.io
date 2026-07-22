package main

// preview_surface_matrix_test.go — the preview loop is the thing Yaver IS, so
// this pins it across every stack and every surface at once.
//
// Two whole classes of bug motivated this file, both found by auditing
// docs/handoff/yaver-self-development-webrtc-preview.md against the code:
//
//  1. A GUARD THAT ONLY EXISTED IN A UI. workspace_preview_strategy.go called
//     the Yaver-in-Yaver recursion block "a REFUSAL, not a preference", but
//     nothing in production ever called IsYaverSelfDevelopment /
//     ResolveSelfDevelopmentPreview. The sole enforcement was the mobile
//     Projects action sheet, which hides buttons — so the web dashboard, MCP
//     verbs, the CLI, tvOS, a second phone, and the feedback→vibe auto-fix path
//     all still reached /dev/build-native and could trap the user. Hiding a
//     button is not a guard.
//
//  2. A SILENT DOWNGRADE FOR WEARABLES AND CAR. watchOS, Wear OS, CarPlay and
//     Android Auto matched no case and fell through to `default:`, which
//     answers "supported — web dev server". A watchOS app rendered as a web
//     page is not the user's app, and the file explicitly forbids exactly this
//     downgrade for Swift.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Yaver-in-Yaver recursion, enforced at the EXECUTION layer ───────────────

func writePreviewProject(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestIsYaverSelfDevelopmentDir_DetectsYaverMobileByIdentity(t *testing.T) {
	dir := writePreviewProject(t, filepath.Join(t.TempDir(), "anything"), map[string]string{
		"package.json": `{"name":"yaver-mobile","dependencies":{"expo":"*"}}`,
	})
	if !IsYaverSelfDevelopmentDir(dir) {
		t.Fatalf("yaver-mobile package not detected as self-development")
	}
}

func TestIsYaverSelfDevelopmentDir_DetectsByBundleIdentifier(t *testing.T) {
	dir := writePreviewProject(t, filepath.Join(t.TempDir(), "renamed"), map[string]string{
		"package.json": `{"name":"totally-renamed"}`,
		"app.json":     `{"expo":{"slug":"yaver","ios":{"bundleIdentifier":"io.yaver.mobile"}}}`,
	})
	if !IsYaverSelfDevelopmentDir(dir) {
		t.Fatalf("io.yaver.mobile bundle id not detected as self-development")
	}
}

func TestIsYaverSelfDevelopmentDir_DetectsMonorepoRoot(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"desktop/agent", "mobile", "relay"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	if !IsYaverSelfDevelopmentDir(root) {
		t.Fatalf("monorepo root not detected as self-development")
	}
}

// THE REGRESSION THAT MATTERS. The repo ships third-party RN fixtures under
// demo/. Detecting self-development from an ancestor path component would
// refuse Hermes for them — breaking the exact validation loop they exist for.
func TestIsYaverSelfDevelopmentDir_ThirdPartyFixtureInsideTheRepoIsNotSelfDev(t *testing.T) {
	// Mirrors the real layout: <checkout>/yaver.io/demo/mobile/todo-rn
	root := filepath.Join(t.TempDir(), "yaver.io")
	dir := writePreviewProject(t, filepath.Join(root, "demo", "mobile", "todo-rn"), map[string]string{
		"package.json": `{"name":"todo-rn","dependencies":{"expo":"*"}}`,
		"app.json":     `{"expo":{"slug":"todo-rn","ios":{"bundleIdentifier":"io.yaver.todorn"}}}`,
	})
	if IsYaverSelfDevelopmentDir(dir) {
		t.Fatalf("third-party fixture under a yaver.io checkout was misdetected as Yaver itself — "+
			"Hermes would be refused for a legitimate RN app (%s)", dir)
	}
}

func TestIsYaverSelfDevelopmentDir_EmptyAndUnknownAreNotSelfDev(t *testing.T) {
	if IsYaverSelfDevelopmentDir("") {
		t.Fatalf("empty dir reported as self-development")
	}
	dir := writePreviewProject(t, filepath.Join(t.TempDir(), "acme"), map[string]string{
		"package.json": `{"name":"acme-todo","dependencies":{"react-native":"*"}}`,
	})
	if IsYaverSelfDevelopmentDir(dir) {
		t.Fatalf("unrelated project reported as self-development")
	}
}

// The execution-layer refusal: ANY surface hitting /dev/build-native for the
// Yaver app must be turned away, not just the one whose UI hides the button.
func TestBuildNativeRefusesYaverSelfDevelopmentFromAnySurface(t *testing.T) {
	dir := writePreviewProject(t, filepath.Join(t.TempDir(), "yaverapp"), map[string]string{
		"package.json": `{"name":"yaver-mobile","dependencies":{"expo":"*"}}`,
	})
	s := &HTTPServer{devServerMgr: NewDevServerManager()}

	// Every caller that could reach this endpoint, not just mobile.
	for _, caller := range []string{
		"web-dashboard/1.1.163", "mcp/ops", "cli/1.99.344", "tvos/1.0", "mobile/1.18.154", "",
	} {
		// Explicit target: a web-dashboard caller defaults to web-js-bundle,
		// which is deliberately NOT guarded. The dangerous request is a
		// mobile-hermes build, and any surface can ask for one.
		body := `{"target":"mobile-hermes","platform":"ios","projectPath":` + jsonQuote(dir) + `}`
		req := httptest.NewRequest(http.MethodPost, "/dev/build-native", strings.NewReader(body))
		if caller != "" {
			req.Header.Set("X-Yaver-Caller", caller)
		}
		rec := httptest.NewRecorder()
		s.handleBuildNativeBundle(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("caller %q: status = %d, want 409 — the recursion trap is reachable from this surface",
				caller, rec.Code)
		}
		var out map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("caller %q: bad body: %v", caller, err)
		}
		if out["code"] != "YAVER_SELF_DEVELOPMENT_RECURSION" {
			t.Fatalf("caller %q: code = %q", caller, out["code"])
		}
		// A refusal that doesn't name the alternative just blocks the user.
		if !strings.Contains(strings.ToLower(out["error"]), "webrtc") {
			t.Fatalf("caller %q: refusal does not point at the WebRTC route: %q", caller, out["error"])
		}
	}
}

// The "allow" side is asserted on the pure decision rather than through the
// handler: letting the handler proceed would run a real npm install + Metro
// build (50s and a network fetch in an earlier revision of this file). The
// refusal path above still goes through the real handler, because it returns
// before any build starts.
func TestSelfDevelopmentGuardAllowsThirdPartyRNInsideTheRepo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "yaver.io")
	dir := writePreviewProject(t, filepath.Join(root, "demo", "mobile", "todo-rn"), map[string]string{
		"package.json": `{"name":"todo-rn","dependencies":{"expo":"~52.0.0","react-native":"0.76.0"}}`,
	})
	if ShouldRefuseYaverSelfDevelopmentHermes("mobile-hermes", dir, "todo-rn", "io.yaver.todorn") {
		t.Fatalf("third-party RN fixture under a yaver.io checkout was refused as self-development")
	}
}

// Web targets are pixels-in-a-browser and cannot trap anyone, so the guard must
// NOT block them — Yaver-on-Yaver over WebRTC is the whole recommended path.
func TestSelfDevelopmentGuardAllowsWebTargetsForYaverItself(t *testing.T) {
	dir := writePreviewProject(t, filepath.Join(t.TempDir(), "yaverapp"), map[string]string{
		"package.json": `{"name":"yaver-mobile","dependencies":{"expo":"*"}}`,
	})
	if !ShouldRefuseYaverSelfDevelopmentHermes("mobile-hermes", dir, "", "") {
		t.Fatalf("mobile-hermes for Yaver itself must be refused")
	}
	for _, target := range []string{"web-js-bundle", "web-hermes-wasm"} {
		if ShouldRefuseYaverSelfDevelopmentHermes(target, dir, "", "") {
			t.Fatalf("target %q blocked by the recursion guard — that route is the recommended one", target)
		}
	}
}

// A caller that names the app without a resolvable path must still be caught.
func TestSelfDevelopmentGuardCatchesIdentityWithoutAPath(t *testing.T) {
	if !ShouldRefuseYaverSelfDevelopmentHermes("mobile-hermes", "", "yaver.io", "io.yaver.mobile") {
		t.Fatalf("self-development by name/bundle id was not refused")
	}
	if ShouldRefuseYaverSelfDevelopmentHermes("mobile-hermes", "", "acme-todo", "com.acme.todo") {
		t.Fatalf("unrelated app refused")
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ── The framework matrix: RN / Flutter / Swift / Kotlin ────────────────────

func TestPreviewMatrixPerFramework(t *testing.T) {
	cases := []struct {
		name        string
		stack       string
		paired      bool
		wantPrimary PreviewStrategy
		wantFeed    FeedbackTransport
		// Hermes is RN/Expo ONLY. Anything else offering it is a bug — the
		// mobile Hot Reload tab gates on this and a wrong plan strands a user
		// waiting for a bundle that can never load.
		hermesAllowed bool
	}{
		{"rn-no-device", "react-native", false, PreviewDirectURL, FeedbackInAppSDK, true},
		{"rn-paired-device", "react-native", true, PreviewHermesBundle, FeedbackDeviceSDK, true},
		{"expo-paired", "expo", true, PreviewHermesBundle, FeedbackDeviceSDK, true},
		{"flutter", "flutter", true, PreviewDirectURL, FeedbackInAppSDK, false},
		{"kotlin", "kotlin", false, PreviewRedroidWebRTC, FeedbackViewerTriggered, false},
		{"android-gradle", "gradle", false, PreviewRedroidWebRTC, FeedbackViewerTriggered, false},
		{"swift-native", "swift", false, PreviewIOSSimulator, FeedbackViewerTriggered, false},
		{"swiftwasm", "swiftwasm", false, PreviewDirectURL, FeedbackInAppSDK, false},
		{"web-next", "next", false, PreviewDirectURL, FeedbackInAppSDK, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := ResolveWorkspacePreview(tc.stack, tc.paired)
			if plan.Primary != tc.wantPrimary {
				t.Fatalf("primary = %q, want %q", plan.Primary, tc.wantPrimary)
			}
			if plan.Feedback != tc.wantFeed {
				t.Fatalf("feedback = %q, want %q", plan.Feedback, tc.wantFeed)
			}
			if !plan.Supported {
				t.Fatalf("stack reported unsupported: %s", plan.Reason)
			}
			if plan.Reason == "" {
				t.Fatalf("no reason given — an unexplained plan is unactionable")
			}
			if !tc.hermesAllowed {
				if plan.Primary == PreviewHermesBundle {
					t.Fatalf("non-RN stack %q got a Hermes primary", tc.stack)
				}
				for _, f := range plan.Fallbacks {
					if f == PreviewHermesBundle {
						t.Fatalf("non-RN stack %q offers Hermes as a fallback — it can never load", tc.stack)
					}
				}
			}
		})
	}
}

// There is NO native Kotlin or Swift feedback SDK. Claiming an in-app SDK for
// them promises a loop that silently does nothing.
func TestNativeStacksNeverClaimAnInAppFeedbackSDK(t *testing.T) {
	for _, stack := range []string{"kotlin", "android", "gradle", "swift", "ios", "xcode", "watchos", "carplay", "wearos"} {
		plan := ResolveWorkspacePreview(stack, false)
		if plan.Feedback == FeedbackInAppSDK {
			t.Fatalf("stack %q claims an in-app feedback SDK; none exists for native", stack)
		}
	}
}

// ── Wearables + car: develop FOR them honestly ─────────────────────────────

func TestWearableAndCarStacksAreNotSilentlyDowngradedToWeb(t *testing.T) {
	cases := []struct {
		stack       string
		wantPrimary PreviewStrategy
	}{
		{"watchos", PreviewIOSSimulator},
		{"watchkit", PreviewIOSSimulator},
		{"carplay", PreviewIOSSimulator},
		{"wearos", PreviewAndroidEmulator},
		{"wear-os", PreviewAndroidEmulator},
		{"android-wear", PreviewAndroidEmulator},
		{"androidauto", PreviewAndroidEmulator},
		{"android auto", PreviewAndroidEmulator},
	}
	for _, tc := range cases {
		t.Run(tc.stack, func(t *testing.T) {
			plan := ResolveWorkspacePreview(tc.stack, false)
			if plan.Primary == PreviewDirectURL || plan.Primary == PreviewChromeWebRTC {
				t.Fatalf("%q previews as a web page (%q) — that is not the user's app",
					tc.stack, plan.Primary)
			}
			if plan.Primary != tc.wantPrimary {
				t.Fatalf("%q primary = %q, want %q", tc.stack, plan.Primary, tc.wantPrimary)
			}
			if strings.Contains(plan.Reason, "unknown stack") {
				t.Fatalf("%q fell through to the default arm: %q", tc.stack, plan.Reason)
			}
		})
	}
}

// Wear OS is Android, so it must not be answered with an Apple simulator, and
// vice versa. Getting this backwards sends the user to buy the wrong machine.
func TestWearableStacksRouteToTheCorrectPlatformRuntime(t *testing.T) {
	if p := ResolveWorkspacePreview("wearos", false).Primary; p == PreviewIOSSimulator {
		t.Fatalf("Wear OS routed to an Apple simulator")
	}
	if p := ResolveWorkspacePreview("watchos", false).Primary; p == PreviewAndroidEmulator || p == PreviewRedroidWebRTC {
		t.Fatalf("watchOS routed to an Android runtime")
	}
}

// ── Surface viewports: every surface Yaver ships must be expressible ───────

func TestEveryShippedSurfaceResolvesAViewport(t *testing.T) {
	// The surfaces named in CLAUDE.md's cross-surface parity rule.
	cases := []struct {
		surface   string
		wantVoice bool
		// A car must never be handed a visual budget that invites reading.
		maxVisual string
	}{
		{"watch", true, "glance"},
		{"car", true, "none"},
		{"glass", true, "panel"},
		{"vr", true, "panel"},
		{"tvos", false, "panel"},
		{"mobile-phone", false, ""},
		{"tablet", false, ""},
		{"web", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.surface, func(t *testing.T) {
			vp := runtimeViewportFromSurface(RuntimeTurnSurface{Class: tc.surface})
			if vp == nil {
				t.Fatalf("surface %q produced no viewport", tc.surface)
			}
			if vp.Surface == "" {
				t.Fatalf("surface %q lost its identity", tc.surface)
			}
			if tc.wantVoice && !vp.Voice {
				t.Fatalf("surface %q should be voice-led", tc.surface)
			}
			if tc.maxVisual != "" && vp.VisualBudget != tc.maxVisual {
				t.Fatalf("surface %q visual budget = %q, want %q", tc.surface, vp.VisualBudget, tc.maxVisual)
			}
		})
	}
}

// The car is the one surface where getting this wrong is a safety problem.
func TestCarSurfaceStaysAudioOnlyUnderDrivingPolicy(t *testing.T) {
	for _, alias := range []string{"car", "carplay", "androidauto", "car-audio"} {
		vp := runtimeViewportFromSurface(RuntimeTurnSurface{Class: alias})
		if vp.VisualBudget != "none" {
			t.Fatalf("%q visual budget = %q, want none — a driver must not be given something to read",
				alias, vp.VisualBudget)
		}
		if vp.RiskPolicy != "driving" {
			t.Fatalf("%q risk policy = %q, want driving", alias, vp.RiskPolicy)
		}
	}
}

func TestWatchSurfaceKeepsAGlanceBudgetAndShortSpeech(t *testing.T) {
	vp := runtimeViewportFromSurface(RuntimeTurnSurface{Class: "watch"})
	if vp.VisualBudget != "glance" {
		t.Fatalf("watch visual budget = %q", vp.VisualBudget)
	}
	if vp.TTSBudget == 0 || vp.TTSBudget > 200 {
		t.Fatalf("watch TTS budget = %d — a watch reply must stay short", vp.TTSBudget)
	}
}

// A shared TV is in a room with other people.
func TestSharedTVCarriesTheSharedRiskPolicy(t *testing.T) {
	vp := runtimeViewportFromSurface(RuntimeTurnSurface{Class: "tvos"})
	if vp.RiskPolicy != "shared-tv" {
		t.Fatalf("tv risk policy = %q, want shared-tv", vp.RiskPolicy)
	}
}
