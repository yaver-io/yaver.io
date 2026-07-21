package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrialBootstrapFailsSpecifically(t *testing.T) {
	// A missing sample must name the SAMPLE, not fail three steps later at
	// "chrome" and send someone debugging the browser.
	b := NewTrialBootstrapper(t.TempDir())
	err := b.Run(context.Background())
	if err == nil {
		t.Fatal("expected failure on an empty work dir")
	}
	if !strings.Contains(err.Error(), "verify-sample") {
		t.Fatalf("error must name the failed step, got: %v", err)
	}
	// And it must say the sample is BAKED, not cloned — that is the design
	// constraint a reader needs when the image is built wrong.
	if !strings.Contains(err.Error(), "BAKED IN") {
		t.Fatalf("error should explain the baking requirement, got: %v", err)
	}
}

func TestTrialBootstrapCatchesMissingDeps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// package.json present but node_modules absent => must fail at the sample
	// step, because an npm install at boot would put the network in the
	// critical path.
	b := NewTrialBootstrapper(dir)
	err := b.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "node_modules") {
		t.Fatalf("missing node_modules must be caught, got: %v", err)
	}
}

func TestTrialStepsOrderAndProgress(t *testing.T) {
	steps := TrialSteps()
	if len(steps) == 0 {
		t.Fatal("no steps")
	}
	// The feedback SDK is verified LAST: it is the one promise the user cannot
	// check for themselves until they shake.
	if steps[len(steps)-1].Name != "feedback-sdk" {
		t.Fatalf("feedback-sdk must be last, got %q", steps[len(steps)-1].Name)
	}
	// Progress must be monotonic, or the UI bar goes backwards.
	prev := -1
	for _, s := range steps {
		if s.Progress <= prev {
			t.Fatalf("progress not monotonic at %q: %d after %d", s.Name, s.Progress, prev)
		}
		prev = s.Progress
	}
	if prev != 100 {
		t.Fatalf("chain must end at 100, got %d", prev)
	}
}
