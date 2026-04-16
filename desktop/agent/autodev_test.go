package main

// autodev_test.go — unit tests for the small, deterministic pieces
// of the autodev surface added in the Apr 2026 wave: the hardening
// preset prompts, the deploy-target resolver, the AI-output JSON
// extractor used by idea refills, the log-stream ring buffer +
// subscriber fan-out, and the safe-filename helpers used by the
// detach machinery.
//
// We stay away from anything that spawns subprocesses, hits the
// daemon, or talks to Claude — those belong in e2e.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- autodevHardenPrompt -----------------------------------------------------

func TestAutodevHardenPrompt(t *testing.T) {
	cases := []struct {
		in   string
		want string // substring that must appear; "" means empty result
	}{
		{"", ""},
		{"unknown-area", ""},
		{"security", "input validation"},
		{"sec", "input validation"}, // alias
		{"memory", "leaks"},
		{"mem", "leaks"},
		{"perf", "bundle size"},
		{"performance", "bundle size"},
		{"quality", "dead code"},
		{"all", "SECURITY"},
		{"  Security  ", "input validation"}, // whitespace + case
	}
	for _, c := range cases {
		got := autodevHardenPrompt(c.in)
		if c.want == "" {
			if got != "" {
				t.Errorf("harden(%q): want empty, got %q", c.in, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("harden(%q): want substring %q, got %q", c.in, c.want, got)
		}
	}
}

// --- resolveAutodevDeployTargets ---------------------------------------------

func TestResolveAutodevDeployTargets(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"none", nil},
		{"both", []string{"testflight", "playstore"}},
		{"testflight", []string{"testflight"}},
		{"convex", []string{"convex"}},
		{"vercel", []string{"vercel"}},
		// "auto" depends on cwd contents — we don't drive that here;
		// the resolver path through "auto" is exercised by an
		// integration test elsewhere.
		// Unknown values pass through as-is for forward-compat.
		{"my-custom-deploy", []string{"my-custom-deploy"}},
	}
	for _, c := range cases {
		got := resolveAutodevDeployTargets(c.in)
		if !sliceEq(got, c.want) {
			t.Errorf("resolveDeploy(%q): want %v, got %v", c.in, c.want, got)
		}
	}
}

func TestEnsureAutodevSpecReRegistersExistingSpec(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	specPath := filepath.Join(workDir, ".autodev.loop.yaml")
	if err := os.WriteFile(specPath, []byte(`name: demo-autodev
mode: develop
target: web
schedule:
  every: 30s
think:
  runner: codex
ship:
  branch: main
budget:
  max_iterations_per_day: 10
`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	origWd, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	p := autodevPlan{LoopName: "demo-autodev", SpecPath: specPath}
	if err := ensureAutodevSpec(p); err != nil {
		t.Fatalf("ensureAutodevSpec: %v", err)
	}
	loops, err := loadLoops()
	if err != nil {
		t.Fatalf("loadLoops: %v", err)
	}
	l, ok := loops[p.LoopName]
	if !ok {
		t.Fatalf("expected loop %q to be registered", p.LoopName)
	}
	wantWorkDir, _ := filepath.EvalSymlinks(workDir)
	gotWorkDir, _ := filepath.EvalSymlinks(l.WorkDir)
	if wantWorkDir == "" {
		wantWorkDir = workDir
	}
	if gotWorkDir == "" {
		gotWorkDir = l.WorkDir
	}
	if gotWorkDir != wantWorkDir {
		t.Fatalf("expected workdir %q, got %q", wantWorkDir, gotWorkDir)
	}
}

func TestEnsureAutodevSpecRewritesStaleLoopName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	specPath := filepath.Join(workDir, ".autodev.loop.yaml")
	if err := os.WriteFile(specPath, []byte(`name: stale-loop
mode: develop
target: web
schedule:
  every: 30s
think:
  runner: codex
ship:
  branch: main
budget:
  max_iterations_per_day: 10
`), 0o644); err != nil {
		t.Fatalf("write stale spec: %v", err)
	}
	origWd, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	p := autodevPlan{
		LoopName:   "demo-autodev",
		SpecPath:   specPath,
		Kind:       "autodev",
		Target:     "web",
		Runner:     "codex",
		Branch:     "main",
		MaxIterDay: 10,
	}
	if err := ensureAutodevSpec(p); err != nil {
		t.Fatalf("ensureAutodevSpec: %v", err)
	}
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read rewritten spec: %v", err)
	}
	if !strings.Contains(string(data), "name: demo-autodev") {
		t.Fatalf("expected rewritten spec to contain updated loop name, got %q", string(data))
	}
	loops, err := loadLoops()
	if err != nil {
		t.Fatalf("loadLoops: %v", err)
	}
	if _, ok := loops[p.LoopName]; !ok {
		t.Fatalf("expected loop %q to be registered after stale spec rewrite", p.LoopName)
	}
}

// --- extractRefillTitles -----------------------------------------------------

func TestExtractRefillTitles(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "clean json",
			in:   `["one","two","three"]`,
			want: []string{"one", "two", "three"},
		},
		{
			name: "with prose preamble",
			in:   `Here are the items:\n["alpha","beta"]`,
			want: []string{"alpha", "beta"},
		},
		{
			name: "with code fence",
			in:   "```json\n[\"x\",\"y\"]\n```",
			want: []string{"x", "y"},
		},
		{
			name: "with trailing commentary",
			in:   `["a","b","c"]\n\nLet me know if you want more.`,
			want: []string{"a", "b", "c"},
		},
		{
			name: "picks last array if multiple",
			in:   `["old","stale"] ... actually use ["fresh","items"]`,
			want: []string{"fresh", "items"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := extractRefillTitles(c.in)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !sliceEq(got, c.want) {
				t.Errorf("want %v, got %v", c.want, got)
			}
		})
	}
}

func TestExtractRefillTitlesEmpty(t *testing.T) {
	if _, err := extractRefillTitles("just prose, no array"); err == nil {
		t.Error("expected error for output with no array")
	}
}

// --- LogStream + LogStreamRegistry -------------------------------------------

func TestLogStreamHistorySnapshot(t *testing.T) {
	s := newLogStream("test")
	s.Append("first")
	s.Append("second")

	_, snapshot, cancel := s.Subscribe()
	defer cancel()
	if len(snapshot) != 2 {
		t.Fatalf("want 2 entries, got %d", len(snapshot))
	}
	for i, want := range []string{"first", "second"} {
		if !strings.Contains(snapshot[i], `"line"`) ||
			!strings.Contains(snapshot[i], want) {
			t.Errorf("snapshot[%d] missing line/%q: %q", i, want, snapshot[i])
		}
	}
}

func TestLogStreamLiveDelivery(t *testing.T) {
	s := newLogStream("live")
	ch, _, cancel := s.Subscribe()
	defer cancel()

	s.Append("hello")
	select {
	case got := <-ch:
		if !strings.Contains(got, `"line"`) || !strings.Contains(got, "hello") {
			t.Errorf("payload mismatch: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for line")
	}
}

func TestLogStreamSlowSubscriberDoesNotBlock(t *testing.T) {
	s := newLogStream("slow")
	ch, _, cancel := s.Subscribe()
	defer cancel()
	_ = ch // intentionally never read

	// Publishing more than the subscriber buffer (256) must not
	// block — the publisher should drop and move on.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			s.Append("burst")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked on slow subscriber")
	}
}

func TestLogStreamHistoryCap(t *testing.T) {
	s := newLogStream("cap")
	for i := 0; i < 600; i++ {
		s.Append("line")
	}
	_, snapshot, cancel := s.Subscribe()
	defer cancel()
	if len(snapshot) != 500 {
		t.Errorf("history cap: want 500, got %d", len(snapshot))
	}
}

func TestLogStreamRegistryReuse(t *testing.T) {
	r := NewLogStreamRegistry()
	a := r.Get("foo")
	b := r.Get("foo")
	if a != b {
		t.Error("Get should return same stream instance for same name")
	}
}

func TestLogStreamRegistryConcurrent(t *testing.T) {
	r := NewLogStreamRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := r.Get("hot")
			s.Append("x")
		}()
	}
	wg.Wait()
	if len(r.Names()) != 1 {
		t.Errorf("want 1 stream registered, got %d", len(r.Names()))
	}
}

// --- safeFileSegment / safeStreamName ----------------------------------------

func TestSafeFileSegment(t *testing.T) {
	cases := map[string]string{
		"plain":             "plain",
		"with spaces":       "with_spaces",
		"path/with/slashes": "path_with_slashes",
		"colon:in:name":     "colon_in_name",
		"a/b:c d":           "a_b_c_d",
	}
	for in, want := range cases {
		if got := safeFileSegment(in); got != want {
			t.Errorf("safeFileSegment(%q): want %q, got %q", in, want, got)
		}
	}
}

func TestSafeStreamName(t *testing.T) {
	if got := safeStreamName("autodev:sfmg-autodev"); got != "autodev_sfmg-autodev" {
		t.Errorf("safeStreamName: got %q", got)
	}
}

// --- structured event publishing ---------------------------------------------

func TestLogStreamAppendEventStoresJSON(t *testing.T) {
	s := newLogStream("ev")
	s.AppendEvent(map[string]interface{}{
		"type":   "yaver_say",
		"text":   "hello",
		"runner": "claude",
	})
	_, snapshot, cancel := s.Subscribe()
	defer cancel()
	if len(snapshot) != 1 {
		t.Fatalf("want 1 event in history, got %d", len(snapshot))
	}
	// The history payload must be valid JSON containing both fields.
	if !strings.Contains(snapshot[0], `"yaver_say"`) ||
		!strings.Contains(snapshot[0], `"hello"`) {
		t.Errorf("payload missing fields: %q", snapshot[0])
	}
}

func TestLogStreamAppendEventDeliversLive(t *testing.T) {
	s := newLogStream("ev2")
	ch, _, cancel := s.Subscribe()
	defer cancel()
	s.AppendEvent(map[string]interface{}{"type": "runner_text", "text": "x"})
	select {
	case got := <-ch:
		if !strings.Contains(got, `"runner_text"`) {
			t.Errorf("payload mismatch: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestLogStreamAppendEventNilSafe(t *testing.T) {
	s := newLogStream("ev3")
	s.AppendEvent(nil) // must not panic
	_, snapshot, cancel := s.Subscribe()
	defer cancel()
	if len(snapshot) != 0 {
		t.Errorf("nil event should not be stored, got %d", len(snapshot))
	}
}

// --- helpers -----------------------------------------------------------------

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
