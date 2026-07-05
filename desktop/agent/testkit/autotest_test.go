package testkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAutoTestWritesLocalPlanAndResults(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "yaver-tests")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := `name: smoke web
target: web
url: http://127.0.0.1:9
steps:
  - goto: /
`
	if err := os.WriteFile(filepath.Join(specDir, "smoke.test.yaml"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}

	var phases []string
	res, err := RunAutoTest(context.Background(), AutoTestRequest{
		WorkDir:  dir,
		Driver:   "selenium",
		Viewport: "pixel7",
		Propose:  true,
	}, func(ev AutoTestEvent) {
		phases = append(phases, ev.Phase)
	})
	if err != nil {
		t.Fatalf("RunAutoTest returned top-level error: %v", err)
	}
	if res == nil || res.Passed {
		t.Fatalf("RunAutoTest should report a failed flow for selenium stub: %+v", res)
	}
	if res.BugsFound != 1 || res.Proposed != 1 {
		t.Fatalf("bugs/proposed = %d/%d, want 1/1", res.BugsFound, res.Proposed)
	}
	if len(res.Flows) != 1 || (!strings.Contains(res.Flows[0].Error, "chromedriver") && !strings.Contains(res.Flows[0].Error, "selenium")) {
		t.Fatalf("unexpected flow result: %+v", res.Flows)
	}
	if _, err := os.Stat(filepath.Join(dir, ".yaver", "tests", "plan.json")); err != nil {
		t.Fatalf("plan.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.ResultsDir, "results.json")); err != nil {
		t.Fatalf("results.json not written: %v", err)
	}
	if !containsPhase(phases, "DISCOVER") || !containsPhase(phases, "REPORT") {
		t.Fatalf("missing expected phases: %v", phases)
	}
}

func containsPhase(phases []string, want string) bool {
	for _, p := range phases {
		if p == want {
			return true
		}
	}
	return false
}
