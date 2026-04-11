package testkit

import (
	"path/filepath"
	"testing"
	"time"
)

func makeSuite(specs ...specCase) *Suite {
	s := &Suite{StartedAt: time.Now()}
	for _, sc := range specs {
		r := &Result{
			Spec:       &Spec{Name: sc.name, Path: "/tmp/" + sc.name + ".test.yaml", Target: TargetWeb},
			Passed:     sc.passed,
			StartedAt:  time.Now(),
			FinishedAt: time.Now(),
			Attempt:    1,
		}
		if sc.flaky {
			r.Flaky = true
		}
		s.Results = append(s.Results, r)
	}
	s.FinishedAt = time.Now()
	return s
}

type specCase struct {
	name   string
	passed bool
	flaky  bool
}

func TestHistoryAppendAndTail(t *testing.T) {
	dir := t.TempDir()
	hist := &History{Path: filepath.Join(dir, ".history.jsonl")}

	suite := makeSuite(
		specCase{name: "a", passed: true},
		specCase{name: "b", passed: false},
	)
	if err := hist.AppendSuite(suite, "abc1234", "main", "darwin"); err != nil {
		t.Fatalf("AppendSuite: %v", err)
	}

	got, err := hist.Tail(10)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	e := got[0]
	if e.Total != 2 || e.Passed != 1 || e.Failed != 1 {
		t.Errorf("counts wrong: %+v", e)
	}
	if e.GitBranch != "main" || e.GitSHA != "abc1234" {
		t.Errorf("git info missing: %+v", e)
	}
}

func TestHistoryFlakeReport(t *testing.T) {
	dir := t.TempDir()
	hist := &History{Path: filepath.Join(dir, ".history.jsonl")}

	// Spec "stable" passes 5 times.
	for i := 0; i < 5; i++ {
		_ = hist.AppendSuite(makeSuite(specCase{name: "stable", passed: true}), "", "", "linux")
	}
	// Spec "flaky" passes 3, fails 2.
	for i := 0; i < 5; i++ {
		_ = hist.AppendSuite(makeSuite(specCase{name: "flaky", passed: i%2 == 0}), "", "", "linux")
	}

	stats, err := hist.FlakeReport(20)
	if err != nil {
		t.Fatalf("FlakeReport: %v", err)
	}
	// Worst-first order: flaky should come first.
	if len(stats) != 2 {
		t.Fatalf("len = %d", len(stats))
	}
	if stats[0].Name != "flaky" {
		t.Errorf("expected flaky first, got %s", stats[0].Name)
	}
	if stats[0].Failed != 2 {
		t.Errorf("flaky.Failed = %d, want 2", stats[0].Failed)
	}
	if stats[1].Failed != 0 {
		t.Errorf("stable.Failed = %d, want 0", stats[1].Failed)
	}
}

func TestHistoryRotation(t *testing.T) {
	// Tail returns empty for a missing file (rotation case).
	dir := t.TempDir()
	hist := &History{Path: filepath.Join(dir, "missing.jsonl")}
	got, err := hist.Tail(5)
	if err != nil {
		t.Fatalf("Tail on missing: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}
