package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFleetDeployOptions_LocalOnly is the headline test: we hit
// /fleet/deploy-options on a freshly-initialised agent (no Convex, so the
// fan-out short-circuits to local) and verify the local row has the right
// per-platform capability flags. This is the contract the mobile pane
// depends on — "Linux machines must not look deployable to TestFlight."
func TestFleetDeployOptions_LocalOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "/bin:/usr/bin") // strip Xcode etc. so probe is deterministic
	if err := os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestWorkspace(t, tmp, "myapp", "react-native-expo", "mobile")

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	vs, err := NewVaultStoreWithDevice("p", "test-dev")
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	srv := &HTTPServer{
		token:      "t",
		deviceID:   "test-dev",
		hostname:   "test-host",
		vaultStore: vs,
	}

	ts := httptest.NewServer(http.HandlerFunc(srv.handleFleetDeployOptions))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "?app=myapp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(raw))
	}
	var got FleetDeployOptions
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.App != "myapp" {
		t.Errorf("App = %q; want myapp", got.App)
	}
	wantTargets := []string{"testflight", "playstore"}
	if len(got.Targets) != len(wantTargets) {
		t.Fatalf("Targets = %v; want %v", got.Targets, wantTargets)
	}
	for i, w := range wantTargets {
		if got.Targets[i] != w {
			t.Errorf("Targets[%d] = %q; want %q", i, got.Targets[i], w)
		}
	}

	if len(got.Devices) < 1 {
		t.Fatalf("Devices empty")
	}
	local := got.Devices[0]
	if !local.IsLocal {
		t.Error("first row should be IsLocal=true")
	}
	if local.DeviceID != "test-dev" {
		t.Errorf("DeviceID = %q; want test-dev", local.DeviceID)
	}
	wantPlatform := runtime.GOOS + "/" + runtime.GOARCH
	if local.Platform != wantPlatform {
		t.Errorf("Platform = %q; want %q", local.Platform, wantPlatform)
	}

	caps := map[string]FleetDeployTargetCap{}
	for _, c := range local.Capabilities {
		caps[c.Target] = c
	}
	tf, ok := caps["testflight"]
	if !ok {
		t.Fatal("missing testflight cap")
	}
	ps, ok := caps["playstore"]
	if !ok {
		t.Fatal("missing playstore cap")
	}

	// The contract: this test runs on whatever CI/dev OS we have. Verify
	// the platform gate matches the runtime — Linux must fail TestFlight
	// with a "needs darwin" reason. macOS may pass it (depends on whether
	// Xcode is installed), so we only assert the negative invariant.
	if runtime.GOOS != "darwin" {
		if tf.OK {
			t.Errorf("non-darwin host marked TestFlight OK; reason=%q", tf.Reason)
		}
		if tf.Reason == "" {
			t.Error("non-darwin TestFlight should have a reason explaining the block")
		}
		if !strings.Contains(tf.Reason, "darwin") {
			t.Errorf("TestFlight reason on %s should mention darwin; got %q", runtime.GOOS, tf.Reason)
		}
	}
	// playstore is in the response regardless of whether tools are found
	// — secrets are warnings, not hard blockers, so OK can swing either
	// way depending on host. We just assert the row exists with a stable
	// shape; per-target gating is unit-tested in TestFirstBlockerFromReport.
	_ = ps
}

// TestFleetDeployOptions_TargetsFilter narrows the targets via query and
// verifies only the requested target is probed + returned in stable order.
func TestFleetDeployOptions_TargetsFilter(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "/bin:/usr/bin")
	os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700)
	writeTestWorkspace(t, tmp, "myapp", "react-native-expo", "mobile")
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(tmp)

	vs, _ := NewVaultStoreWithDevice("p", "test-dev")
	srv := &HTTPServer{token: "t", deviceID: "d", hostname: "h", vaultStore: vs}
	ts := httptest.NewServer(http.HandlerFunc(srv.handleFleetDeployOptions))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "?app=myapp&targets=playstore")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(raw))
	}
	var got FleetDeployOptions
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Targets) != 1 || got.Targets[0] != "playstore" {
		t.Errorf("Targets = %v; want [playstore]", got.Targets)
	}
	if got.Devices[0].Capabilities[0].Target != "playstore" {
		t.Errorf("local cap target = %q; want playstore", got.Devices[0].Capabilities[0].Target)
	}
	if len(got.Devices[0].Capabilities) != 1 {
		t.Errorf("expected 1 capability per device when narrowed; got %d", len(got.Devices[0].Capabilities))
	}
}

// TestFleetDeployOptions_UnknownTarget rejects bad input with 400 and the
// known-list, matching how /doctor/build behaves. Mobile clients use the
// "known" array to render error toasts.
//
// This used to probe with "cloudflare" — chosen back when the picker was
// mobile-only and cloudflare was the canonical example of something it would
// not accept. The picker now accepts every target /deploy/ship can run, so
// that input asserts the opposite of the intended contract. Uses a string
// that is not a build target at all, which is what the test was always about.
func TestFleetDeployOptions_UnknownTarget(t *testing.T) {
	srv := &HTTPServer{token: "t", deviceID: "d", hostname: "h"}
	ts := httptest.NewServer(http.HandlerFunc(srv.handleFleetDeployOptions))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "?app=foo&targets=not-a-real-target")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", resp.StatusCode)
	}
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] == nil {
		t.Error("missing error field")
	}
	known, _ := body["known"].([]interface{})
	if len(known) == 0 {
		t.Error("known list should be present so UI can render the allowed targets")
	}
}

// TestFleetDeployOptions_RequiresApp is a sanity check on the input
// validation — keeps the contract that mobile clients always pass app.
func TestFleetDeployOptions_RequiresApp(t *testing.T) {
	srv := &HTTPServer{token: "t", deviceID: "d", hostname: "h"}
	ts := httptest.NewServer(http.HandlerFunc(srv.handleFleetDeployOptions))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", resp.StatusCode)
	}
}

// TestFirstBlockerFromReport spot-checks the wording for each gate type
// since the iOS/Android UI shows it verbatim.
func TestFirstBlockerFromReport(t *testing.T) {
	cases := []struct {
		name string
		in   BuildDoctorReport
		want string
	}{
		{
			name: "platform skip beats anything else",
			in: BuildDoctorReport{
				OK: false,
				Tools: []BuildToolResult{
					{Name: "xcodebuild", Required: true, Skipped: true, SkipReason: "only on darwin (this host: linux)"},
					{Name: "node", Required: true, Found: false},
				},
			},
			want: "xcodebuild: only on darwin (this host: linux)",
		},
		{
			name: "missing required tool",
			in: BuildDoctorReport{
				OK: false,
				Tools: []BuildToolResult{
					{Name: "java", Required: true, Found: false, InstallHint: "brew install openjdk@17"},
				},
			},
			want: "missing java — brew install openjdk@17",
		},
		{
			name: "missing secret",
			in: BuildDoctorReport{
				OK: false,
				Tools: []BuildToolResult{
					{Name: "node", Required: true, Found: true},
				},
				Secrets: []BuildSecretResult{
					{Name: "APPLE_TEAM_ID", Found: false},
				},
			},
			want: "missing secret APPLE_TEAM_ID (yaver vault add APPLE_TEAM_ID)",
		},
		{
			name: "all good",
			in:   BuildDoctorReport{OK: true},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firstBlockerFromReport(tc.in)
			if got != tc.want {
				t.Errorf("firstBlockerFromReport = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestDefaultFleetTargetsForStack pins the join between the workspace
// manifest's per-app stack and each buildTarget's declared stack.
//
// Before this, every app defaulted to {testflight, playstore}. Opening the
// picker on a Next.js frontend or a Convex backend probed two mobile targets
// that could not apply, reported them blocked, and offered nothing that would
// actually ship — which is why the two halves of a web project could never be
// deployed from the phone.
func TestDefaultFleetTargetsForStack(t *testing.T) {
	cases := []struct {
		stack string
		want  []string
	}{
		// yaver.workspace.yaml declares talos-web as nextjs.
		{"nextjs", []string{"cloudflare", "vercel"}},
		// ...talos-cloud as convex. convex-selfhosted is a different
		// deployment mode, not an alternative — requestable, not default.
		{"convex", []string{"convex"}},
		// ...talos-mobile as react-native-expo. Unchanged by this work:
		// adding web support must not start probing a third mobile target
		// (playstore-production) that this path never probed before.
		{"react-native-expo", []string{"testflight", "playstore"}},
		// Unknown and empty both fall back to the historical mobile pair:
		// when we don't know what the project is, guessing web targets would
		// be worse than the status quo.
		{"node", []string{"testflight", "playstore"}},
		{"", []string{"testflight", "playstore"}},
	}
	for _, tc := range cases {
		got := defaultFleetTargetsForStack(tc.stack)
		if len(got) != len(tc.want) {
			t.Errorf("stack %q: got %v; want %v", tc.stack, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("stack %q: got %v; want %v", tc.stack, got, tc.want)
				break
			}
		}
	}
}

// TestOrderFleetTargets: unlisted targets must still appear. A target added
// to buildTargets but forgotten in fleetDeployTargetOrder should degrade to
// "sorted last", never to "silently dropped" — dropping it would make a
// deployable target invisible with nothing reporting why, which is the
// failure mode this whole area keeps producing.
func TestOrderFleetTargets(t *testing.T) {
	got := orderFleetTargets([]string{"zzz-unlisted", "convex", "testflight", "aaa-unlisted"})
	want := []string{"testflight", "convex", "aaa-unlisted", "zzz-unlisted"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("got %v; want %v", got, want)
		}
	}
}

// TestValidDeployTargetsCoversShippableTargets: the picker's allowlist is
// derived from buildTargets, so anything /deploy/ship can run is requestable.
func TestValidDeployTargetsCoversShippableTargets(t *testing.T) {
	for _, name := range []string{"cloudflare", "convex", "vercel", "testflight", "playstore"} {
		if !validDeployTargetsForFleet[name] {
			t.Errorf("%s is a build target but the picker rejects it", name)
		}
	}
	if validDeployTargetsForFleet["not-a-real-target"] {
		t.Error("allowlist must not accept a non-target")
	}
}
