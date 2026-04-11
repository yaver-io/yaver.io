package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpecIncludePrependsMacroSteps(t *testing.T) {
	dir := t.TempDir()

	// Macro: a two-step "log in as testuser" flow.
	macro := `name: login-macro
target: web
url: http://x
steps:
  - goto: /login
  - fill:
      selector: 'input[type=email]'
      text: 'dev@example.test'
`
	if err := os.WriteFile(filepath.Join(dir, "login.test.yaml"), []byte(macro), 0o644); err != nil {
		t.Fatal(err)
	}

	// Main spec: includes the macro and adds one real step.
	main := `name: checkout
target: web
url: http://x
include:
  - login.test.yaml
steps:
  - click: 'button.buy'
`
	mainPath := filepath.Join(dir, "checkout.test.yaml")
	if err := os.WriteFile(mainPath, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadSpec(mainPath)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	// Setup should contain the two macro steps (goto + fill).
	if len(s.Setup) != 2 {
		t.Fatalf("setup len = %d, want 2", len(s.Setup))
	}
	if s.Setup[0].Goto != "/login" {
		t.Errorf("setup[0].Goto = %q", s.Setup[0].Goto)
	}
	if s.Setup[1].Fill == nil || s.Setup[1].Fill.Text != "dev@example.test" {
		t.Errorf("setup[1] fill step wrong: %+v", s.Setup[1].Fill)
	}

	// Main steps untouched.
	if len(s.Steps) != 1 || s.Steps[0].Click != "button.buy" {
		t.Errorf("main steps wrong: %+v", s.Steps)
	}
}

func TestSpecIncludeCycleGuarded(t *testing.T) {
	dir := t.TempDir()

	// a.test.yaml includes b, b includes a. Loader must not loop.
	aPath := filepath.Join(dir, "a.test.yaml")
	bPath := filepath.Join(dir, "b.test.yaml")
	a := `name: a
target: web
url: http://x
include: [b.test.yaml]
steps:
  - goto: /
`
	b := `name: b
target: web
url: http://x
include: [a.test.yaml]
steps:
  - goto: /
`
	_ = os.WriteFile(aPath, []byte(a), 0o644)
	_ = os.WriteFile(bPath, []byte(b), 0o644)

	_, err := LoadSpec(aPath)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestSpecIncludeRelativePath(t *testing.T) {
	dir := t.TempDir()
	macroDir := filepath.Join(dir, "macros")
	if err := os.MkdirAll(macroDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(macroDir, "auth.test.yaml"), []byte(`name: auth
target: web
url: http://x
steps:
  - goto: /auth
`), 0o644)

	mainPath := filepath.Join(dir, "main.test.yaml")
	_ = os.WriteFile(mainPath, []byte(`name: main
target: web
url: http://x
include:
  - macros/auth.test.yaml
steps:
  - click: a
`), 0o644)

	s, err := LoadSpec(mainPath)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if len(s.Setup) != 1 || s.Setup[0].Goto != "/auth" {
		t.Errorf("macro not inlined correctly: %+v", s.Setup)
	}
}
