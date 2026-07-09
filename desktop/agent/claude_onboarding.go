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

// ensureClaudeOnboardingComplete sets hasCompletedOnboarding=true in Claude's
// config, preserving every other key. It is idempotent and safe to call on
// every launch: an already-onboarded config short-circuits without a write.
//
// The write is atomic (temp file + rename in the same directory) because a
// live `claude` process may be reading the file concurrently, and a truncated
// ~/.claude.json loses the user's project history and MCP config.
func ensureClaudeOnboardingComplete(home string) error {
	path := claudeConfigJSONPath(home)

	cfg := map[string]any{}
	switch data, err := os.ReadFile(path); {
	case err == nil:
		if len(strings.TrimSpace(string(data))) > 0 {
			if jerr := json.Unmarshal(data, &cfg); jerr != nil {
				// Refuse to clobber a file we cannot parse — Claude may
				// understand a shape we don't, and the wizard is a far
				// smaller problem than a destroyed config.
				return fmt.Errorf("parse %s: %w", path, jerr)
			}
		}
	case os.IsNotExist(err):
		// Fresh machine — the file Claude would have created on first run.
	default:
		return fmt.Errorf("read %s: %w", path, err)
	}

	if done, ok := cfg["hasCompletedOnboarding"].(bool); ok && done {
		return nil
	}
	cfg["hasCompletedOnboarding"] = true

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
	log.Printf("[claude-onboarding] marked onboarding complete in %s — the TUI will skip its sign-in wizard", path)
	return nil
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
