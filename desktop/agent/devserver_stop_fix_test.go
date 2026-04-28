package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestStaleNativeIncidentClearedOnSuccess proves the fix for the
// "Bundle validation failed" pill that stuck even after subsequent
// successful builds. Open a ReasonBuildNativeFailed incident, then
// run the same ResolveOpenByKey call the success path now makes,
// and assert the incident is resolved.
func TestStaleNativeIncidentClearedOnSuccess(t *testing.T) {
	store := GlobalIncidentStore()
	if store == nil {
		t.Fatal("GlobalIncidentStore() returned nil")
	}

	const (
		project  = "/tmp/test-project-stale-native"
		platform = "ios"
	)

	stale := store.Append(IncidentEvent{
		Timestamp:     time.Now().UnixMilli(),
		Severity:      IncidentSeverityError,
		Category:      "build",
		Code:          ReasonBuildNativeFailed,
		Source:        "devserver",
		Title:         "Bundle validation failed",
		UserMessage:   "The bundle was built, but the final native bundle failed validation.",
		TechnicalInfo: "Bundle validation failed: cannot open bundle (no such file or directory)",
		ProjectPath:   project,
		Target:        platform,
		Recoverable:   true,
	})
	t.Cleanup(func() { store.Resolve(stale.ID, "test cleanup") })

	if stale.Resolved {
		t.Fatalf("freshly-appended incident reported resolved=true: %+v", stale)
	}

	// This is exactly the call the success path now makes.
	resolved := store.ResolveOpenByKey(IncidentKey{
		Category:    "build",
		Code:        ReasonBuildNativeFailed,
		ProjectPath: project,
		Target:      platform,
	}, "Native bundle build recovered.")
	if !resolved {
		t.Fatal("ResolveOpenByKey returned false — incident not resolved")
	}

	got := store.Get(stale.ID)
	if got.ID == "" {
		t.Fatalf("incident %s vanished after resolve", stale.ID)
	}
	if !got.Resolved {
		t.Fatalf("incident still resolved=false after ResolveOpenByKey: %+v", got)
	}
	if !strings.Contains(got.ResolutionNote, "recovered") {
		t.Errorf("expected resolution note to mention recovery, got %q", got.ResolutionNote)
	}
}

// TestActiveBuildsRegistry exercises the registry that /dev/stop uses
// to cancel in-flight Hermes builds. Register two builds, verify
// cancelAllActiveBuilds cancels both contexts.
func TestActiveBuildsRegistry(t *testing.T) {
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())

	releaseA := registerActiveBuild("/proj/a", "ios", cancelA)
	releaseB := registerActiveBuild("/proj/b", "android", cancelB)
	defer releaseA()
	defer releaseB()

	if got := cancelAllActiveBuilds(); got != 2 {
		t.Fatalf("cancelAllActiveBuilds returned %d, want 2", got)
	}

	select {
	case <-ctxA.Done():
	case <-time.After(time.Second):
		t.Fatal("build A context not cancelled within 1s")
	}
	select {
	case <-ctxB.Done():
	case <-time.After(time.Second):
		t.Fatal("build B context not cancelled within 1s")
	}

	// Calling cancel after release should be a no-op (no panic, no count).
	if got := cancelAllActiveBuilds(); got != 0 {
		t.Fatalf("second cancelAllActiveBuilds returned %d, want 0", got)
	}
}

// TestActiveBuildsRegistryReplacesStale ensures a duplicate register
// for the same (workDir, platform) cancels the prior cancel func
// before storing the new one. The mobile app retries POST
// /dev/build-native if it loses confidence in a hung first attempt;
// without this, /dev/stop would only cancel the newer attempt.
func TestActiveBuildsRegistryReplacesStale(t *testing.T) {
	var firstCancelled int32
	firstCancel := func() { atomic.StoreInt32(&firstCancelled, 1) }

	release1 := registerActiveBuild("/proj", "ios", firstCancel)
	defer release1()

	_, cancel2 := context.WithCancel(context.Background())
	release2 := registerActiveBuild("/proj", "ios", cancel2)
	defer release2()

	if atomic.LoadInt32(&firstCancelled) != 1 {
		t.Fatal("first cancel was not invoked when a second build registered for the same key")
	}

	// Only the second cancel is in the map now.
	if got := cancelAllActiveBuilds(); got != 1 {
		t.Fatalf("cancelAllActiveBuilds returned %d, want 1 (only the most recent registration)", got)
	}
}

// TestStopServingPreviewResultIdleAgent proves the new shape: even when
// nothing is being served, /dev/stop returns ok=true, verified=true,
// and clears any open build/reload incidents — including ones opened
// against unrelated projects (wildcard sweep on user-initiated Stop).
func TestStopServingPreviewResultIdleAgent(t *testing.T) {
	store := GlobalIncidentStore()
	if store == nil {
		t.Fatal("GlobalIncidentStore() returned nil")
	}

	// Two stale incidents from different projects — the user-Stop
	// wildcard sweep must clear BOTH, not just one. Before the bug
	// fix, ResolveOpenByKey with empty (path, target) only matched
	// the (rare) entries that also had empty (path, target).
	staleA := store.Append(IncidentEvent{
		Timestamp:   time.Now().UnixMilli(),
		Severity:    IncidentSeverityError,
		Category:    "build",
		Code:        ReasonBuildNativeFailed,
		Source:      "devserver",
		Title:       "Bundle validation failed (project A)",
		UserMessage: "fixture A",
		ProjectPath: "/var/projects/sfmg",
		Target:      "ios",
		Recoverable: true,
	})
	staleB := store.Append(IncidentEvent{
		Timestamp:   time.Now().UnixMilli(),
		Severity:    IncidentSeverityError,
		Category:    "reload",
		Code:        ReasonReloadDevServerUnavailable,
		Source:      "devserver",
		Title:       "Hot reload failed (project B)",
		UserMessage: "fixture B",
		ProjectPath: "/var/projects/talos",
		Target:      "android",
		Recoverable: true,
	})
	t.Cleanup(func() {
		store.Resolve(staleA.ID, "test cleanup")
		store.Resolve(staleB.ID, "test cleanup")
	})

	mgr := NewDevServerManager()
	srv := &HTTPServer{devServerMgr: mgr}

	res := srv.stopServingPreviewResult()
	if res == nil {
		t.Fatal("stopServingPreviewResult returned nil")
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Errorf("res.ok=%v, want true (no server running is not an error)", res["ok"])
	}
	if v, _ := res["verified"].(bool); !v {
		t.Errorf("res.verified=%v, want true", res["verified"])
	}
	if n, _ := res["buildsCancelled"].(int); n != 0 {
		t.Errorf("res.buildsCancelled=%v, want 0", res["buildsCancelled"])
	}

	for _, fix := range []struct {
		label string
		id    string
	}{{"A", staleA.ID}, {"B", staleB.ID}} {
		got := store.Get(fix.id)
		if !got.Resolved {
			t.Errorf("user-initiated Stop should wildcard-clear stale incident %s (%s) — still open", fix.id, fix.label)
		}
	}
}

// TestDevStopHTTPResponseShape boots the real handler via httptest and
// asserts the JSON body the mobile / web clients now consume contains
// the agent-1.99.93+ fields. This is the wire-level contract test —
// a TS-side regression in MobileClient.devServer.stop() will surface
// here with a JSON shape mismatch.
func TestDevStopHTTPResponseShape(t *testing.T) {
	store := GlobalIncidentStore()

	stale := store.Append(IncidentEvent{
		Timestamp:   time.Now().UnixMilli(),
		Severity:    IncidentSeverityError,
		Category:    "build",
		Code:        ReasonBuildNativeFailed,
		Source:      "devserver",
		Title:       "Bundle validation failed",
		UserMessage: "fixture for TestDevStopHTTPResponseShape",
		Recoverable: true,
	})
	t.Cleanup(func() { store.Resolve(stale.ID, "test cleanup") })

	srv := &HTTPServer{devServerMgr: NewDevServerManager()}
	ts := httptest.NewServer(http.HandlerFunc(srv.handleDevServerStop))
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /dev/stop: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	// New fields the mobile + web Stop UX depends on.
	for _, key := range []string{"ok", "verified", "buildsCancelled"} {
		if _, ok := body[key]; !ok {
			t.Errorf("response missing new field %q — mobile/web won't show post-stop confirmation. body=%+v", key, body)
		}
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("body.ok=%v, want true", body["ok"])
	}
	if v, _ := body["verified"].(bool); !v {
		t.Errorf("body.verified=%v, want true", body["verified"])
	}
	// json.Decode turns numbers into float64 by default.
	if n, _ := body["buildsCancelled"].(float64); n != 0 {
		t.Errorf("body.buildsCancelled=%v, want 0", body["buildsCancelled"])
	}

	// And the stale incident is resolved as a side-effect of the user Stop.
	got := store.Get(stale.ID)
	if !got.Resolved {
		t.Errorf("user-initiated /dev/stop should clear stale build incidents — incident %s still open", stale.ID)
	}
}

// TestStopServingPreviewResultCancelsBuild proves /dev/stop also kills
// any registered in-flight build context.
func TestStopServingPreviewResultCancelsBuild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	releaseBuild := registerActiveBuild("/proj-stopping", "ios", cancel)
	defer releaseBuild()

	mgr := NewDevServerManager()
	srv := &HTTPServer{devServerMgr: mgr}

	res := srv.stopServingPreviewResult()
	if n, _ := res["buildsCancelled"].(int); n != 1 {
		t.Errorf("res.buildsCancelled=%v, want 1", res["buildsCancelled"])
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("registered build context not cancelled by /dev/stop within 1s")
	}
}
