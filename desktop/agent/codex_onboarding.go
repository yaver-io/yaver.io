package main

// codex_onboarding.go — stop Codex's first-run screens from eating an autorun.
//
// The Claude equivalent (claude_onboarding.go) exists because a TUI sitting on
// a blocking dialog swallows the task instruction autorun types at it. Codex
// has the same hazard through a different door, and it cost us a live run on
// 2026-07-18: the loop started, the runner-check logged
// `codex at /opt/homebrew/bin/codex — codex-cli 0.128.0`, and then the pane sat
// on
//
//	✨ Update available! 0.128.0 -> 0.144.5
//	› 1. Update now (runs `npm install -g @openai/codex`)
//	  2. Skip
//	  3. Skip until next version
//	  Press enter to continue
//
// forever. Nothing was wrong with the task, the scope, the gate, or the
// credential. The run would have burned its 5-minute interval against a prompt
// until someone opened the pane and pressed a key — and autorun reports a
// blocked pane as a running session, so `status` says the run is fine.
//
// Codex records the dismissal in ~/.codex/version.json:
//
//	{"latest_version":"0.144.5","last_checked_at":"...","dismissed_version":"0.144.5"}
//
// Setting dismissed_version to the version it would nag about is exactly what
// choosing "Skip until next version" writes. We never update the binary — that
// is the user's call and npm's job; we only decline to be blocked by the offer.
//
// Codex also keeps its OWN per-directory trust in ~/.codex/config.toml
// (`[projects."<abs>"] trust_level = "trusted"`), which is a separate mechanism
// from Claude's. Verified on the mini: the worktree autorun creates for a run is
// NOT in that list. It survives today only because autorun launches codex in
// YOLO mode, which bypasses the prompt — so this is latent, not benign, and it
// costs nothing to close.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// codexHomeDir resolves Codex's state directory, honouring CODEX_HOME the way
// the CLI does, falling back to ~/.codex.
func codexHomeDir(home string) string {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}

// writeFileAtomic replaces path in one rename. A live codex process may be
// reading these files, and a truncated config.toml loses every project's trust
// level — a worse outcome than the prompt we are suppressing.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".yaver-*")
	if err != nil {
		return fmt.Errorf("temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename onto %s: %w", path, err)
	}
	return nil
}

// ensureCodexUpdatePromptDismissed pre-answers Codex's "Update available"
// prompt so a TUI runner opens on its normal input line.
//
// Only ever sets dismissed_version, and only to a version Codex itself already
// recorded as latest. If Codex has never checked (no file, or no latest_version)
// there is nothing to dismiss and we leave it alone rather than inventing a
// version — writing a guess here would suppress a future prompt we have not
// seen.
func ensureCodexUpdatePromptDismissed(home string) error {
	path := filepath.Join(codexHomeDir(home), "version.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // never run, or never checked — nothing to pre-answer
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	state := map[string]any{}
	if jerr := json.Unmarshal(data, &state); jerr != nil {
		// Refuse to clobber a file we cannot parse — a prompt is a much
		// smaller problem than a destroyed state file.
		return fmt.Errorf("parse %s: %w", path, jerr)
	}

	latest, _ := state["latest_version"].(string)
	latest = strings.TrimSpace(latest)
	if latest == "" {
		return nil
	}
	if dismissed, _ := state["dismissed_version"].(string); strings.TrimSpace(dismissed) == latest {
		return nil // already answered
	}
	state["dismissed_version"] = latest

	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := writeFileAtomic(path, encoded, 0o644); err != nil {
		return err
	}
	log.Printf("[codex-update] pre-dismissed the %s update prompt — the TUI would otherwise block on it (%s)", latest, path)
	return nil
}

// ensureCodexFolderTrusted marks workDir trusted in Codex's config.toml.
//
// Mirrors ensureClaudeFolderTrusted, for the directories autorun invents: a
// fresh clone or a git worktree is never in the trust list. Appends a project
// block only when the path is absent; an existing entry (whatever its level) is
// the user's and is left untouched.
//
// This is a deliberately minimal TOML edit — append-if-absent, never a
// parse-and-rewrite — because config.toml is hand-editable and round-tripping
// it through a serialiser would reformat and drop comments a user put there.
func ensureCodexFolderTrusted(home, workDir string) error {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return nil
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", workDir, err)
	}

	path := filepath.Join(codexHomeDir(home), "config.toml")
	existing := ""
	switch data, rerr := os.ReadFile(path); {
	case rerr == nil:
		existing = string(data)
	case os.IsNotExist(rerr):
		// Fresh machine — the file codex would create on first run.
	default:
		return fmt.Errorf("read %s: %w", path, rerr)
	}

	header := fmt.Sprintf("[projects.%q]", abs)
	if strings.Contains(existing, header) {
		return nil // already known to codex, at whatever level the user chose
	}

	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	if existing != "" {
		b.WriteString("\n")
	}
	b.WriteString(header)
	b.WriteString("\ntrust_level = \"trusted\"\n")

	if err := writeFileAtomic(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	log.Printf("[codex-trust] marked %s trusted — the TUI would otherwise prompt on it (%s)", abs, path)
	return nil
}

// prepareCodexForHeadlessRun runs both pre-answers before a codex TUI is
// launched. Best-effort by design: a runner that starts and prompts is strictly
// better than a runner that does not start, so failures are logged, not
// returned.
func prepareCodexForHeadlessRun(workDir string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	if err := ensureCodexUpdatePromptDismissed(home); err != nil {
		log.Printf("[codex-update] could not pre-dismiss the update prompt: %v", err)
	}
	if err := ensureCodexFolderTrusted(home, workDir); err != nil {
		log.Printf("[codex-trust] could not mark %s trusted: %v", workDir, err)
	}
}
