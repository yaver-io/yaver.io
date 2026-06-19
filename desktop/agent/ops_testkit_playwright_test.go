package main

import (
	"archive/zip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yaver-io/agent/testkit"
)

func TestForceWebSpecsToPlaywright(t *testing.T) {
	specs := []*testkit.Spec{
		{Name: "default"},
		{Name: "web", Target: testkit.TargetWeb},
		{Name: "pw", Target: testkit.TargetWebPlaywright},
		{Name: "device", Target: testkit.TargetDevice},
		nil,
	}
	got := forceWebSpecsToPlaywright(specs)
	if len(got) != 4 {
		t.Fatalf("expected nil specs to be dropped, got %d", len(got))
	}
	for _, idx := range []int{0, 1, 2} {
		if got[idx].Target != testkit.TargetWebPlaywright {
			t.Fatalf("spec %d target = %q, want web-playwright", idx, got[idx].Target)
		}
	}
	if got[3].Target != testkit.TargetDevice {
		t.Fatalf("device target should remain unchanged, got %q", got[3].Target)
	}
	if specs[1].Target != testkit.TargetWeb {
		t.Fatal("forceWebSpecsToPlaywright mutated the caller's spec")
	}
}

func TestPlaywrightProfilesListAndDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := playwrightStorageStatePath("talos admin")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".yaver", "playwright-storage", "talos-admin.json")
	if path != want {
		t.Fatalf("profile path = %q, want %q", path, want)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"cookies":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, profiles, err := playwrightProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Dir(path) {
		t.Fatalf("profiles dir = %q, want %q", dir, filepath.Dir(path))
	}
	if len(profiles) != 1 || profiles[0]["name"] != "talos-admin" || profiles[0]["path"] != path {
		t.Fatalf("unexpected profiles: %#v", profiles)
	}

	deletedPath, existed, err := playwrightDeleteProfile("talos admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if deletedPath != path || !existed {
		t.Fatalf("delete = (%q, %v), want (%q, true)", deletedPath, existed, path)
	}
	_, existed, err = playwrightDeleteProfile("talos admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Fatal("second delete should report deleted=false")
	}
}

func TestPlaywrightDeleteProfileRejectsOutsidePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	outside := filepath.Join(t.TempDir(), "state.json")
	if _, _, err := playwrightDeleteProfile("", outside); err == nil {
		t.Fatal("expected outside storageState path to be rejected")
	}
}

func TestPlaywrightDevHelpers(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "yaver-tests")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	workDir, err := resolveTestkitWorkDir(testkitRunRequest{Root: root}, root)
	if err != nil {
		t.Fatal(err)
	}
	if workDir != tmp {
		t.Fatalf("workDir = %q, want %q", workDir, tmp)
	}

	specs := []*testkit.Spec{{URL: ""}, {URL: " http://127.0.0.1:3000 "}}
	if got := firstSpecURL(specs); got != "http://127.0.0.1:3000" {
		t.Fatalf("firstSpecURL = %q", got)
	}
}

func TestWaitForPlaywrightURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForPlaywrightURL(ctx, srv.URL, done); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForPlaywrightURLSeesEarlyProcessExit(t *testing.T) {
	done := make(chan error, 1)
	done <- fmt.Errorf("boom")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := waitForPlaywrightURL(ctx, "http://127.0.0.1:1", done)
	if err == nil || !strings.Contains(err.Error(), "exited before") {
		t.Fatalf("expected early exit error, got %v", err)
	}
}

func TestBuildPlaywrightProfileAuthScript(t *testing.T) {
	script := buildPlaywrightProfileAuthScript(playwrightProfileAuthRequest{
		URL:          "https://talos.example/login",
		SuccessURL:   "/dashboard",
		StorageState: "/tmp/talos-admin.json",
		TimeoutSec:   12,
		FinishPath:   "/tmp/finish.signal",
		CancelPath:   "/tmp/cancel.signal",
	})
	for _, want := range []string{
		"import { chromium } from 'playwright';",
		"headless: false",
		"ctxOpts.storageState = storageStatePath",
		"fs.existsSync(cancelPath)",
		"fs.existsSync(finishPath)",
		"page.url().includes(successURL)",
		"timeoutMs = 12000",
		"ctx.storageState({ path: storageStatePath })",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("auth script missing %q:\n%s", want, script)
		}
	}
}

func TestTalosQualityRejectsInvalidOrEmptyRun(t *testing.T) {
	if _, err := studioJobs.startTalosQualityRun(talosQualityRunRequest{BrowserMode: "bad"}); err == nil {
		t.Fatal("expected invalid browser mode to be rejected")
	}
	if _, err := studioJobs.startTalosQualityRun(talosQualityRunRequest{BrowserMode: "skip"}); err == nil {
		t.Fatal("expected empty quality run to be rejected")
	}
}

func TestTalosQualityReportStore(t *testing.T) {
	jobID := "quality-test"
	storeTalosQualityReport(jobID, &talosQualityReport{
		JobID:       jobID,
		Passed:      false,
		BrowserMode: "playwright-yaml",
		Web:         &testkitReport{Total: 2, Passed: 1, Failed: 1},
		Android:     &qaReport{Caught: 1, Passed: false},
		Summary:     []string{"web: 1 passed / 1 failed", "android: 1 caught / 0 fixed"},
	})
	got := getTalosQualityReport(jobID)
	if got == nil || got.Passed || got.Web.Failed != 1 || got.Android.Caught != 1 || len(got.Summary) != 2 {
		t.Fatalf("unexpected quality report: %#v", got)
	}
}

func TestTalosQualityPreflightIncludesRedroid(t *testing.T) {
	p := buildTalosQualityPreflight(talosQualityRunRequest{RunQA: true}, "chromedp")
	if p["redroid"] == nil {
		t.Fatalf("expected redroid preflight: %#v", p)
	}
	if p["deps"] == nil || p["pkgManager"] == "" {
		t.Fatalf("expected dependency preflight fields: %#v", p)
	}
}

func TestInspectTestkitTraceArtifact(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.zip")
	zf, err := os.Create(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("trace.trace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	w, err = zw.Create("resources/abc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("resource")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	jobID := "trace-job"
	storeTestkitReport(jobID, &testkitReport{
		Dir:       dir,
		Artifacts: []testkitArtifactRef{{Kind: "trace", Path: tracePath}},
	})
	got, err := inspectTestkitTraceArtifact(jobID, tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if got["entryCount"] != 2 || got["traceFiles"] != 1 || got["resources"] != 1 {
		t.Fatalf("unexpected trace summary: %#v", got)
	}
	if _, err := inspectTestkitTraceArtifact(jobID, filepath.Join(dir, "other.zip")); err == nil {
		t.Fatal("expected unreferenced trace to be rejected")
	}
}

func TestPlaywrightRunsAndGCDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldDir := filepath.Join(home, ".yaver", "playwright-native", "old-run")
	newDir := filepath.Join(home, ".yaver", "playwright-native", "new-run")
	for _, dir := range []string{oldDir, newDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "run.log"), []byte("log"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	runs := listPlaywrightRuns(10)
	if len(runs) != 2 {
		t.Fatalf("runs = %#v", runs)
	}
	res, err := gcPlaywrightArtifacts(24, true)
	if err != nil {
		t.Fatal(err)
	}
	deleted := res["deleted"].([]map[string]any)
	if len(deleted) != 1 || deleted[0]["path"] != oldDir {
		t.Fatalf("dry-run deleted = %#v", deleted)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("dry-run should not delete old dir: %v", err)
	}
	res, err = gcPlaywrightArtifacts(24, false)
	if err != nil {
		t.Fatal(err)
	}
	deleted = res["deleted"].([]map[string]any)
	if len(deleted) != 1 {
		t.Fatalf("delete deleted = %#v", deleted)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expected old dir deleted, err=%v", err)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("new dir should remain: %v", err)
	}
}

func TestBuildPlaywrightNativeCommand(t *testing.T) {
	cmd := buildPlaywrightNativeCommand(playwrightNativeRunRequest{
		Config:  "playwright.config.ts",
		Project: "chromium",
		Grep:    "checkout flow",
		Workers: 2,
		Headed:  true,
		Trace:   "retain-on-failure",
		Args:    []string{"tests/checkout.spec.ts"},
	})
	for _, want := range []string{
		"'npx'",
		"'playwright'",
		"'test'",
		"'--reporter=json'",
		"'--config=playwright.config.ts'",
		"'--project=chromium'",
		"'--grep=checkout flow'",
		"'--workers=2'",
		"'--headed'",
		"'--trace=retain-on-failure'",
		"'tests/checkout.spec.ts'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("native command missing %q: %s", want, cmd)
		}
	}
}

func TestBuildPlaywrightNativeReportFromJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "results.json")
	logPath := filepath.Join(dir, "run.log")
	if err := os.WriteFile(logPath, []byte("native log"), 0o644); err != nil {
		t.Fatal(err)
	}
	data := `{
	  "stats": {"expected": 1, "unexpected": 1, "skipped": 0, "flaky": 0, "duration": 1234},
	  "suites": [{
	    "title": "root",
	    "specs": [
	      {"title": "passes", "tests": [{"results": [{"status": "passed", "duration": 10}]}]},
	      {"title": "fails", "tests": [{"results": [{"status": "failed", "duration": 20, "error": {"message": "boom"}}]}]}
	    ]
	  }]
	}`
	if err := os.WriteFile(jsonPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trace.zip"), []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := buildPlaywrightNativeReport(playwrightNativeRunRequest{Label: "talos native"}, dir, jsonPath, logPath, nil)
	if rep.Project != "talos native" || rep.Total != 2 || rep.Passed != 1 || rep.Failed != 1 || rep.DurationMs != 1234 {
		t.Fatalf("unexpected native report summary: %#v", rep)
	}
	if len(rep.Features) != 2 || rep.Features[1].Name != "fails" || rep.Features[1].Status != "fail" || rep.Features[1].Error != "boom" {
		t.Fatalf("unexpected native features: %#v", rep.Features)
	}
	var foundLog, foundJSON, foundTrace bool
	for _, art := range rep.Artifacts {
		switch {
		case art.Kind == "log" && art.Path == logPath:
			foundLog = true
		case art.Kind == "json" && art.Path == jsonPath:
			foundJSON = true
		case art.Kind == "trace" && strings.HasSuffix(art.Path, "trace.zip"):
			foundTrace = true
		}
	}
	if !foundLog || !foundJSON || !foundTrace {
		t.Fatalf("missing native artifact refs: %#v", rep.Artifacts)
	}
}

func TestBuildTestkitReportIncludesPlaywrightTraceArtifact(t *testing.T) {
	dir := t.TempDir()
	artifactDir := filepath.Join(dir, "feature")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(artifactDir, "pw-step-1.png")
	trace := filepath.Join(artifactDir, "trace.zip")
	if err := os.WriteFile(shot, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trace, []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	rep := buildTestkitReport("talos", dir, &testkit.Suite{
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		Results: []*testkit.Result{{
			Spec:       &testkit.Spec{Name: "feature", Target: testkit.TargetWebPlaywright, URL: "https://example.test"},
			Passed:     true,
			StartedAt:  now,
			FinishedAt: now.Add(time.Second),
			Steps:      []testkit.StepResult{{Index: 1, ScreenshotPath: shot}},
		}},
	})
	if len(rep.Features) != 1 {
		t.Fatalf("features = %d", len(rep.Features))
	}
	if rep.Features[0].TracePath != trace {
		t.Fatalf("trace path = %q, want %q", rep.Features[0].TracePath, trace)
	}
	var foundTrace, foundShot bool
	for _, art := range rep.Artifacts {
		if art.Kind == "trace" && art.Path == trace && art.Mime == "application/zip" && art.Bytes == 3 {
			foundTrace = true
		}
		if art.Kind == "screenshot" && art.Path == shot && art.Mime == "image/png" && art.Feature == "feature" && art.Step == 1 {
			foundShot = true
		}
	}
	if !foundTrace || !foundShot {
		t.Fatalf("missing trace/screenshot artifact refs: %#v", rep.Artifacts)
	}

	storeTestkitReport("trace-job", rep)
	art, err := readTestkitArtifact("trace-job", trace)
	if err != nil {
		t.Fatal(err)
	}
	if art["mimeType"] != "application/zip" || art["bytes"] != 3 {
		t.Fatalf("unexpected artifact metadata: %#v", art)
	}
}
