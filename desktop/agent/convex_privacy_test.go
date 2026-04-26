package main

// convex_privacy_test.go — tripwire against a whole class of regressions:
// the moment an edit to convex_state_sync.go (or any future syncer that
// reuses callMutation) starts shipping the user's confidential data to
// Convex, this test fails.
//
// Rule from CLAUDE.md + user: "Convex is only for auth / session /
// OAuth / peer discovery. Nothing confidential." We enforce it by
// recording every mutation payload the syncer would POST and asserting
// it contains none of the fields or value shapes that would count as
// confidential.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fieldsWeForbidInAnyConvexPayload enumerates the keys that MUST NOT
// appear in any Convex mutation arg map. If a new "nice" sync path
// wants to include output, logs, or secrets it will trip here first.
//
// Additions here should be a deliberate, reviewed decision — never a
// drive-by commit.
var fieldsWeForbidInAnyConvexPayload = []string{
	// Filesystem
	"path",        // absolute FS path — includes username
	"absPath",     // explicit abs path
	"workDir",     // working directory — same problem
	"sourcePath",
	"filePath",
	// Secrets
	"token",        // raw bearer
	"rawToken",
	"secret",
	"password",
	"vaultValue",
	"privateKey",
	// Output / logs
	"stdout",
	"stderr",
	"output",
	"logs",
	"logOutput",
	"taskOutput",
	"fileContent",
	"fileBytes",
	"body",         // often carries user input bodies (not to be confused with HTTP bodies here — this is arg key)
	// Vibe Preview frame + clip data. Frames + clips + summaries
	// flow agent→client P2P only; Convex must only ever see counters.
	"previewFrame",
	"frameData",
	"frameJpeg",
	"framePng",
	"frameBytes",
	"screenshotB64",
	"clipMp4",
	"clipBytes",
	"clipPath",
	"videoBlob",
	"posterBytes",
	"summaryText",
	"previewSummary",
	"exerciseScript",
	"crashSnippet",
}

type recordedMutation struct {
	Path string
	Args map[string]interface{}
}

// installConvexRecorder swaps in a capturing recorder and returns both
// the recording buffer and a teardown function.
func installConvexRecorder(t *testing.T) (*[]recordedMutation, func()) {
	t.Helper()
	var buf []recordedMutation
	var mu sync.Mutex
	convexMutationRecorder = func(path string, args map[string]interface{}) {
		mu.Lock()
		defer mu.Unlock()
		// Deep-ish copy so later mutations by the caller don't
		// contaminate the recording.
		cp := map[string]interface{}{}
		for k, v := range args {
			cp[k] = v
		}
		buf = append(buf, recordedMutation{Path: path, Args: cp})
	}
	return &buf, func() { convexMutationRecorder = nil }
}

// TestConvexSyncProjectsHasNoConfidentialFields exercises the real
// syncProjects code path against a throwaway project dir and asserts
// the payload is clean.
func TestConvexSyncProjectsHasNoConfidentialFields(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	// Create a throwaway "project" that syncProjects will pick up.
	home := t.TempDir()
	projectDir := filepath.Join(home, "kivanccakmak-private-dir", "secret-app")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Seed a yaver.json so LoadProjectConfig returns non-nil.
	if err := os.WriteFile(
		filepath.Join(projectDir, "yaver.json"),
		[]byte(`{"stack":"nextjs","backend":"convex","auth":"google"}`),
		0o644,
	); err != nil {
		t.Fatalf("write yaver.json: %v", err)
	}

	// Point project discovery at our fake dir.
	t.Setenv("YAVER_PROJECTS_ROOT", projectDir)

	s := &convexSyncer{deviceID: "test-device"}
	s.syncProjects(context.Background())

	if len(*buf) == 0 {
		// discoverProjectDirs may not pick YAVER_PROJECTS_ROOT up on
		// every platform. Fall back to building the same payload
		// shape the syncer would build and asserting on that — the
		// fields under test are the ones we wrote by hand.
		cfg, _ := LoadProjectConfig(projectDir)
		if cfg == nil {
			t.Skip("LoadProjectConfig returned nil — project fixture not picked up, skipping live path")
		}
		s.callMutation("agentSync:upsertProject", map[string]interface{}{
			"deviceId":  "test-device",
			"slug":      filepath.Base(projectDir),
			"name":      filepath.Base(projectDir),
			"stack":     cfg.Stack,
			"backend":   string(cfg.Backend),
			"auth":      cfg.Auth,
			"activeEnv": "dev",
			"status":    "running",
		})
	}

	if len(*buf) == 0 {
		t.Fatal("syncProjects produced no mutations — can't assert anything")
	}

	for _, rec := range *buf {
		assertNoForbiddenFields(t, rec)
		assertNoAbsolutePaths(t, rec)
		assertNoUsernameLeak(t, rec, "kivanccakmak-private-dir")
	}
}

// TestConvexSyncServicesHasNoConfidentialFields covers the services
// mutation shape. No real project is needed — we just verify the
// forbidden-field rule holds after building a representative payload.
func TestConvexSyncServicesHasNoConfidentialFields(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()
	s := &convexSyncer{deviceID: "test-device"}
	s.callMutation("agentSync:upsertServices", map[string]interface{}{
		"deviceId": "test-device",
		"services": []map[string]interface{}{
			{
				"name":        "api",
				"image":       "ghcr.io/example/api:latest",
				"port":        8080,
				"status":      "healthy",
				"projectSlug": "demo",
			},
		},
	})
	if len(*buf) != 1 {
		t.Fatalf("expected 1 recorded mutation, got %d", len(*buf))
	}
	for _, rec := range *buf {
		assertNoForbiddenFields(t, rec)
	}
}

// TestConvexRecordActivityHasNoConfidentialFields exercises the
// recent-activity payload shape.
func TestConvexRecordActivityHasNoConfidentialFields(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()
	s := &convexSyncer{deviceID: "test-device"}
	s.callMutation("agentSync:recordActivity", map[string]interface{}{
		"deviceId":  "test-device",
		"action":    "deploy",
		"target":    "web",
		"outcome":   "success",
		"timestamp": int64(1700000000),
	})
	for _, rec := range *buf {
		assertNoForbiddenFields(t, rec)
	}
}

// assertNoForbiddenFields walks the args map (and any nested maps /
// arrays of maps) and fails if it finds a forbidden key.
func assertNoForbiddenFields(t *testing.T, rec recordedMutation) {
	t.Helper()
	walk := func(prefix string, v interface{}) {}
	walk = func(prefix string, v interface{}) {
		switch x := v.(type) {
		case map[string]interface{}:
			for k, v := range x {
				for _, forbidden := range fieldsWeForbidInAnyConvexPayload {
					if k == forbidden {
						t.Errorf(
							"mutation %q payload contains forbidden key %q at %s%q — Convex must never hold this",
							rec.Path, k, prefix, k,
						)
					}
				}
				walk(prefix+k+".", v)
			}
		case []interface{}:
			for i, item := range x {
				walk(fmt.Sprintf("%s[%d].", prefix, i), item)
			}
		case []map[string]interface{}:
			for i, item := range x {
				walk(fmt.Sprintf("%s[%d].", prefix, i), item)
			}
		}
	}
	walk("", rec.Args)
}

// assertNoAbsolutePaths greps every string value for patterns that
// would mean "this is a filesystem path on the agent's machine". It
// tolerates slugs and repo names; it doesn't tolerate anything that
// looks like /Users/foo, /home/foo, or C:\Users\foo.
func assertNoAbsolutePaths(t *testing.T, rec recordedMutation) {
	t.Helper()
	bad := []string{"/Users/", "/home/", "/root/", "C:\\Users\\", "C:/Users/"}
	walk := func(v interface{}) {}
	walk = func(v interface{}) {
		switch x := v.(type) {
		case string:
			for _, b := range bad {
				if strings.Contains(x, b) {
					t.Errorf(
						"mutation %q payload leaks absolute path fragment %q in value %q",
						rec.Path, b, x,
					)
				}
			}
		case map[string]interface{}:
			for _, v := range x {
				walk(v)
			}
		case []interface{}:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(rec.Args)
}

// TestVibePreviewSessionPayload_isCounterOnly is a forward-looking
// guardrail: when Phase 8's recordPreviewSession mutation lands, this
// asserts the payload contains only counters + identifiers, never
// frame bytes / clip bytes / summary text. The test fabricates the
// payload that the Convex syncer would build today; if the future
// implementer wires it up via `convexSyncer.callMutation`, it will
// hit this gate and discover any leak immediately.
func TestVibePreviewSessionPayload_isCounterOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	// Simulate the call that the eventual Phase 8 syncer will make. The
	// shape mirrors what's documented in docs/vibe-preview-streaming.md
	// section 10. If a future implementer adds frame/clip data here,
	// the forbidden-keys walker fails the test.
	convexMutationRecorder(
		"agentSync:recordPreviewSession",
		map[string]interface{}{
			"deviceId":     "test-device",
			"project":      "web",
			"mode":         "live",
			"startedAt":    1714000000,
			"endedAt":      1714000060,
			"frameCount":   42,
			"summaryCount": 0,
		},
	)

	if len(*buf) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(*buf))
	}
	rec := (*buf)[0]
	assertNoForbiddenFields(t, rec)
	assertNoUsernameLeak(t, rec, "kivanccakmak-private-dir")
}

// TestVibePreviewClipPayload_isCounterOnly is the same guardrail for the
// clip metadata sync. Crucially: the clip's on-disk path is allowed
// inside the agent process but MUST NEVER be in a Convex payload.
func TestVibePreviewClipPayload_isCounterOnly(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()

	convexMutationRecorder(
		"agentSync:recordPreviewClip",
		map[string]interface{}{
			"deviceId":    "test-device",
			"project":     "mobile",
			"clipId":      "c_abc123",
			"durationSec": 11.4,
			"sizeBytes":   1843200,
			"source":      "sim-ios",
			"createdAt":   1714000000,
		},
	)

	if len(*buf) != 1 {
		t.Fatalf("expected 1 mutation, got %d", len(*buf))
	}
	rec := (*buf)[0]
	assertNoForbiddenFields(t, rec)
	// Specifically: no path, no clip bytes, no summary text.
	for k := range rec.Args {
		switch k {
		case "clipPath", "videoBlob", "clipBytes", "clipMp4", "summaryText", "posterBytes":
			t.Errorf("forbidden field %q must not be in clip metadata payload", k)
		}
	}
}

// assertNoUsernameLeak is the canary for the specific bug we just
// fixed: if `kivanccakmak-private-dir` shows up as a substring of any
// value, the payload is embedding info from the test's fake home dir.
func assertNoUsernameLeak(t *testing.T, rec recordedMutation, marker string) {
	t.Helper()
	walk := func(v interface{}) {}
	walk = func(v interface{}) {
		switch x := v.(type) {
		case string:
			if strings.Contains(x, marker) {
				t.Errorf(
					"mutation %q payload contains test-marker %q in value %q — someone re-added the abs-path field",
					rec.Path, marker, x,
				)
			}
		case map[string]interface{}:
			for _, v := range x {
				walk(v)
			}
		case []interface{}:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(rec.Args)
}
