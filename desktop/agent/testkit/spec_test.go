package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSpec(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestLoadSpecMinimal(t *testing.T) {
	dir := t.TempDir()
	p := writeSpec(t, dir, "minimal.test.yaml", `
name: minimal
target: web
url: http://127.0.0.1:3000
steps:
  - goto: /
  - assert.title: Hello
`)
	s, err := LoadSpec(p)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if s.Name != "minimal" {
		t.Errorf("Name = %q, want minimal", s.Name)
	}
	if s.Target != TargetWeb {
		t.Errorf("Target = %q, want web", s.Target)
	}
	if s.URL != "http://127.0.0.1:3000" {
		t.Errorf("URL = %q", s.URL)
	}
	if len(s.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(s.Steps))
	}
	if s.Steps[0].Goto != "/" {
		t.Errorf("Steps[0].Goto = %q", s.Steps[0].Goto)
	}
	if s.Steps[1].AssertTitle != "Hello" {
		t.Errorf("Steps[1].AssertTitle = %q", s.Steps[1].AssertTitle)
	}
	if s.TimeoutMS != 7000 {
		t.Errorf("default TimeoutMS = %d, want 7000", s.TimeoutMS)
	}
	if s.Artifacts.On != "failure" {
		t.Errorf("default Artifacts.On = %q, want failure", s.Artifacts.On)
	}
	if s.Artifacts.Screenshot == nil || *s.Artifacts.Screenshot != true {
		t.Errorf("default Artifacts.Screenshot should be true")
	}
}

func TestLoadSpecDefaultsName(t *testing.T) {
	dir := t.TempDir()
	p := writeSpec(t, dir, "auto-name.test.yaml", `
target: web
url: http://localhost
steps:
  - goto: /
`)
	s, err := LoadSpec(p)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	// .test is left as the inner extension by Base+TrimExt; we strip
	// only the final .yaml, which gives "auto-name.test". That's still
	// a unique label so we keep it.
	if s.Name != "auto-name.test" {
		t.Errorf("Name = %q, want auto-name.test", s.Name)
	}
}

func TestLoadSpecFillStep(t *testing.T) {
	dir := t.TempDir()
	p := writeSpec(t, dir, "fill.test.yaml", `
name: fill
target: web
url: http://localhost
steps:
  - fill:
      selector: 'input[type=email]'
      text: 'foo@bar.test'
`)
	s, err := LoadSpec(p)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if s.Steps[0].Fill == nil {
		t.Fatal("Fill step not parsed")
	}
	if s.Steps[0].Fill.Selector != "input[type=email]" {
		t.Errorf("Fill.Selector = %q", s.Steps[0].Fill.Selector)
	}
	if s.Steps[0].Fill.Text != "foo@bar.test" {
		t.Errorf("Fill.Text = %q", s.Steps[0].Fill.Text)
	}
}

func TestValidateRejectsRelativeGotoWithoutURL(t *testing.T) {
	s := &Spec{
		Name:   "broken",
		Target: TargetWeb,
		Steps: []Step{
			{Goto: "/auth"},
		},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for relative goto without url")
	}
}

func TestValidateAcceptsAbsoluteGotoWithoutURL(t *testing.T) {
	s := &Spec{
		Name:   "ok",
		Target: TargetWeb,
		Steps: []Step{
			{Goto: "https://example.com"},
		},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRequiresSteps(t *testing.T) {
	s := &Spec{
		Name:   "empty",
		Target: TargetWeb,
		URL:    "http://localhost",
	}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for empty steps")
	}
}

func TestValidateRejectsUnknownTarget(t *testing.T) {
	s := &Spec{
		Name:   "weird",
		Target: Target("magic"),
		Steps:  []Step{{Goto: "https://x"}},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for unknown target")
	}
}

func TestDiscoverSpecs(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSpec(t, root, "a.test.yaml", "name: a\ntarget: web\nurl: http://x\nsteps:\n  - goto: /\n")
	writeSpec(t, sub, "b.test.yml", "name: b\ntarget: web\nurl: http://x\nsteps:\n  - goto: /\n")
	writeSpec(t, root, "ignored.txt", "not a spec")
	writeSpec(t, root, "README.md", "not a spec either")

	specs, err := DiscoverSpecs(root)
	if err != nil {
		t.Fatalf("DiscoverSpecs: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("len = %d, want 2", len(specs))
	}
	gotNames := []string{specs[0].Name, specs[1].Name}
	wantA, wantB := false, false
	for _, n := range gotNames {
		if n == "a" {
			wantA = true
		}
		if n == "b" {
			wantB = true
		}
	}
	if !wantA || !wantB {
		t.Errorf("expected to find both a and b, got %v", gotNames)
	}
}

func TestRunUnknownTarget(t *testing.T) {
	// Sanity: Run() returns an error result for unimplemented targets
	// rather than panicking.
	s := &Spec{
		Name:    "ios",
		Target:  TargetIOSSim,
		URL:     "http://x",
		Steps:   []Step{{Goto: "/"}},
		Path:    "/tmp/ios.test.yaml",
	}
	res := Run(t.Context(), s, RunOptions{})
	if res.Err == nil {
		t.Fatal("expected error result for ios-sim (M5)")
	}
	if res.Passed {
		t.Error("Passed should be false")
	}
}
