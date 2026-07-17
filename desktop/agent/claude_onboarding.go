package main

// claude_onboarding.go — make a headless box's Claude Code skip its first-run
// wizard.
//
// A valid credential is NOT sufficient to get a usable `claude` TUI. Claude
// Code gates its wizard on `hasCompletedOnboarding` in ~/.claude.json, not on
// whether it can authenticate. On a machine that has never run `claude`
// interactively — which is every box Yaver provisions, and every box that
// received its credential by mirror — the wizard runs anyway:
//
//	theme picker  →  "Select login method"  →  browser OAuth URL
//	                                           "Paste code here if prompted >"
//
// …even while `claude auth status --json` reports {"loggedIn": true}. That
// third screen is the browser sign-in a headless box cannot satisfy, and it is
// what `yaver claude --machine <box>` used to land the user on.
//
// Verified against Claude Code 2.1.165 on Linux: writing
// {"hasCompletedOnboarding": true} and nothing else turns THEME-PICKER into a
// normal prompt. The theme step is gated by the same flag, so no theme needs to
// be fabricated.
//
// We set the flag only where Yaver has just established that the runner IS
// authenticated. Marking a signed-out box as onboarded would suppress the one
// screen that could still help its owner.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// claudeConfigJSONPath resolves the file Claude Code keeps its onboarding flag
// in. With CLAUDE_CONFIG_DIR set, Claude writes <dir>/.claude.json and ignores
// $HOME/.claude.json entirely (confirmed empirically); otherwise it is
// $HOME/.claude.json. Note this is NOT the credentials file, which lives at
// <dir>/.credentials.json.
func claudeConfigJSONPath(home string) string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// mutateClaudeConfig reads Claude's config, hands it to mutate, and writes it
// back only if mutate reports a change. Every other key is preserved.
//
// The write is atomic (temp file + rename in the same directory) because a
// live `claude` process may be reading the file concurrently, and a truncated
// ~/.claude.json loses the user's project history and MCP config.
func mutateClaudeConfig(home, logTag string, mutate func(cfg map[string]any) (changed bool, note string)) error {
	path := claudeConfigJSONPath(home)

	cfg := map[string]any{}
	switch data, err := os.ReadFile(path); {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if jerr := json.Unmarshal(data, &cfg); jerr != nil {
				// Refuse to clobber a file we cannot parse — Claude may
				// understand a shape we don't, and a wizard is a far
				// smaller problem than a destroyed config.
				return fmt.Errorf("parse %s: %w", path, jerr)
			}
		}
	case os.IsNotExist(err):
		// Fresh machine — the file Claude would have created on first run.
	default:
		return fmt.Errorf("read %s: %w", path, err)
	}

	changed, note := mutate(cfg)
	if !changed {
		return nil
	}

	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".claude.json.yaver-*")
	if err != nil {
		return fmt.Errorf("temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename succeeds
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename onto %s: %w", path, err)
	}
	log.Printf("[%s] %s (%s)", logTag, note, path)
	return nil
}

// ensureClaudeOnboardingComplete sets hasCompletedOnboarding=true in Claude's
// config. Idempotent and safe on every launch: an already-onboarded config
// short-circuits without a write.
func ensureClaudeOnboardingComplete(home string) error {
	return mutateClaudeConfig(home, "claude-onboarding", func(cfg map[string]any) (bool, string) {
		if done, ok := cfg["hasCompletedOnboarding"].(bool); ok && done {
			return false, ""
		}
		cfg["hasCompletedOnboarding"] = true
		return true, "marked onboarding complete — the TUI will skip its sign-in wizard"
	})
}

// ensureClaudeFolderTrusted pre-accepts Claude Code's folder-trust dialog for
// workDir.
//
// Onboarding is not the only blocking first-run screen. In a directory Claude
// has never seen it opens on:
//
//	Quick safety check: Is this a project you created or one you trust?
//	❯ 1. Yes, I trust this folder
//	  2. No, exit
//
// `--dangerously-skip-permissions` does NOT skip this — that flag governs tool
// permissions, not folder trust. This is latent everywhere and fatal for
// autorun specifically, because autorun's whole job is running in directories
// nobody has opened before: a fresh clone, a git worktree, a rebuilt box.
//
// It fails silently rather than loudly, which is worse. autorun sends the task
// instruction right after `tmux new-session`, so if the TUI is sitting on this
// dialog the instruction gets typed INTO THE DIALOG — answering a safety
// question with a paragraph of task text. Verified live: the worktree Yaver
// creates for a run is never in `projects`, so every claude-seat autorun on a
// fresh worktree hits this.
//
// Only ever sets the trust flag, and only for a directory Yaver itself just
// created or was pointed at. Never touches credentials, never enables a tool.
func ensureClaudeFolderTrusted(home, workDir string) error {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return nil
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", workDir, err)
	}
	return mutateClaudeConfig(home, "claude-trust", func(cfg map[string]any) (bool, string) {
		projects, _ := cfg["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			cfg["projects"] = projects
		}
		entry, _ := projects[abs].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
			projects[abs] = entry
		}
		if trusted, ok := entry["hasTrustDialogAccepted"].(bool); ok && trusted {
			return false, ""
		}
		entry["hasTrustDialogAccepted"] = true
		return true, fmt.Sprintf("pre-accepted the folder-trust dialog for %s — the TUI would otherwise block on it", abs)
	})
}

// ensureClaudeOnboardingForLocalHome is the $HOME-relative convenience wrapper
// used by the credential-write paths.
func ensureClaudeOnboardingForLocalHome() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	if err := ensureClaudeOnboardingComplete(home); err != nil {
		log.Printf("[claude-onboarding] could not mark onboarding complete: %v", err)
	}
}

// ensureClaudeFolderTrustedForLocalHome is the $HOME-relative wrapper for the
// callers that are about to launch a TUI in a directory they just created.
func ensureClaudeFolderTrustedForLocalHome(workDir string) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return ensureClaudeFolderTrusted(home, workDir)
}
