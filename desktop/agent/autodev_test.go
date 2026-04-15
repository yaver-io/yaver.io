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
	if !sliceEq(snapshot, []string{"first", "second"}) {
		t.Errorf("snapshot: want [first second], got %v", snapshot)
	}
}

func TestLogStreamLiveDelivery(t *testing.T) {
	s := newLogStream("live")
	ch, _, cancel := s.Subscribe()
	defer cancel()

	s.Append("hello")
	select {
	case got := <-ch:
		if got != "hello" {
			t.Errorf("want hello, got %q", got)
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
		"plain":              "plain",
		"with spaces":        "with_spaces",
		"path/with/slashes":  "path_with_slashes",
		"colon:in:name":      "colon_in_name",
		"a/b:c d":            "a_b_c_d",
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
