package main

import (
	"strings"
	"testing"
)

// codexModelMigrationPane is the REAL pane captured from the mac mini on
// 2026-07-19, from session yaver-autorun-connectivity-mesh-harden-codex, which
// had been sitting on this dialog for 1h43m while its peer finished the task.
const codexModelMigrationPane = `
  GPT-5.4 is no longer available

  Codex now uses GPT-5.6 Terra in place of GPT-5.4. Switch to GPT-5.6 Terra to
  continue.

  Choose how you'd like Codex to proceed.

› 1. Try new model
  2. Use existing model

  Use ↑/↓ to move, press enter to confirm
`

func TestDetectsCodexModelMigration(t *testing.T) {
	got := autorunDetectBlockingPrompt(codexModelMigrationPane)
	if got == nil {
		t.Fatal("the model-migration dialog that silently ate a 1h43m run must be detected")
	}
	if got.Name != "codex-model-migration" {
		t.Fatalf("Name = %q, want codex-model-migration", got.Name)
	}
}

func TestBlockedPromptErrorQuotesThePane(t *testing.T) {
	p := autorunDetectBlockingPrompt(codexModelMigrationPane)
	if p == nil {
		t.Fatal("precondition: pane must be detected")
	}
	err := autorunBlockedPromptError("sess-1", p, codexModelMigrationPane)
	msg := err.Error()
	// The operator must be able to answer the question without going to look
	// for it — that is the whole reason the pane is embedded.
	for _, want := range []string{"sess-1", "codex-model-migration", "REMEDY", "no longer available", "Try new model"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error text missing %q:\n%s", want, msg)
		}
	}
}

func TestHealthyPaneIsNotBlocked(t *testing.T) {
	// A working session, including one whose scrollback contains the word
	// "proceed" — prose must not trip the detector.
	healthy := []struct {
		name string
		pane string
	}{
		{"busy runner", "· Working (12s · 3.1k tokens)\n  editing desktop/agent/quic.go"},
		{"idle composer", "❯ \n─────────────\n  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		{"prose mentioning a dialog", "I asked whether to proceed and the update available note is no longer available in the log."},
		{"empty", ""},
	}
	for _, tc := range healthy {
		if got := autorunDetectBlockingPrompt(tc.pane); got != nil {
			t.Errorf("%s: false positive %q — a false positive stalls a healthy run", tc.name, got.Name)
		}
	}
}

func TestAnsweredDialogScrolledUpIsNotBlocked(t *testing.T) {
	// The dialog stays in the tmux scrollback forever after it is answered.
	// Matching on it would block every later turn of a healthy session.
	pane := codexModelMigrationPane + `
  model:       gpt-5.6-terra medium   /model to change
  directory:   ~/.yaver/worktrees/connectivity-mesh-harden-claude
  permissions: YOLO mode

· Working (4s)
  reading desktop/agent/autorun.go
  editing relay/server.go
  running go build ./...
  ok github.com/yaver-io/agent
  committing changes
  pushing to origin
  done — summarising
  ❯
`
	if got := autorunDetectBlockingPrompt(pane); got != nil {
		t.Fatalf("answered dialog in scrollback reported as blocking (%q) — every later turn would fail", got.Name)
	}
}

func TestDetectsTrustAndUsageLimitModals(t *testing.T) {
	cases := []struct {
		name string
		pane string
		want string
	}{
		{
			name: "claude folder trust",
			pane: "Do you trust the files in this folder?\n/Users/x/.yaver/worktrees/w\n❯ 1. Yes, proceed\n  2. No\npress enter to confirm",
			want: "claude-folder-trust",
		},
		{
			name: "usage limit",
			pane: "You have hit your usage limit. It will reset at 3pm.\n› 1. Wait\n  2. Exit\nUse ↑/↓ to move, press enter to confirm",
			want: "runner-usage-limit",
		},
	}
	for _, tc := range cases {
		got := autorunDetectBlockingPrompt(tc.pane)
		if got == nil {
			t.Errorf("%s: not detected", tc.name)
			continue
		}
		if got.Name != tc.want {
			t.Errorf("%s: Name = %q, want %q", tc.name, got.Name, tc.want)
		}
	}
}

func TestModalCueRequired(t *testing.T) {
	// Signature words without a confirmation cue must NOT match: that is a
	// runner talking about a migration, not a runner blocked on one.
	pane := "The codex model gpt-5.4 is no longer available so I will proceed with the fallback."
	if got := autorunDetectBlockingPrompt(pane); got != nil {
		t.Fatalf("matched %q without any modal cue — this would stall healthy runs", got.Name)
	}
}
