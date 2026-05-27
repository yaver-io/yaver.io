package main

// runner_auth_ledger.go — metadata-only ledger of runner OAuth tokens
// (Claude Code, Codex) installed on this agent. Plaintext stays in
// the vault (vault.go); the ledger records sha256 hash + provenance +
// timestamps so the UI can show "Claude — mirrored from
// macbook-air-kivanc, expires 2027-05-26" without ever shipping the
// secret value.
//
// Modeled line-for-line on paired_tokens.go. Atomic write-then-rename
// so concurrent writes can't corrupt the file. Convex never sees the
// ledger — per CLAUDE.md privacy contract, runner tokens are
// host-local and P2P-mirrored only.
//
// Glass UX consumes this via /runner/auth/ledger (returns sanitized
// rows — never plaintext). MirrorRunnerToken (runner_auth_mirror.go)
// appends entries on accept. The vault's plaintext write is unchanged.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// RunnerTokenLedgerEntry is one metadata record of a runner OAuth
// token installed on this agent. The plaintext lives in the vault
// under CLAUDE_CODE_OAUTH_TOKEN / OPENAI_OAUTH_TOKEN — this struct
// is the index pointing at it.
type RunnerTokenLedgerEntry struct {
	// Runner is "claude" | "codex" | "opencode".
	Runner string `json:"runner"`
	// TokenHash is sha256(plaintext) hex-encoded. Stable identity for
	// dedup + revoke; never reversible to the secret.
	TokenHash string `json:"tokenHash"`
	// Source records where the token came from. Examples:
	//   "mirror:macbook-air-kivanc"  — pushed from another agent of ours
	//   "device-auth:phone-relay"    — phone-relay OAuth completion
	//   "local-browser"              — user ran the browser flow on this box
	Source string `json:"source"`
	// SavedAt is the agent's local timestamp when the entry was added.
	SavedAt time.Time `json:"savedAt"`
	// ExpiresAt is the provider-reported expiry. Zero value = unknown
	// (Anthropic OAuth ~1y, Codex device-auth ~30-90d).
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	// LastUsedAt updates when the runner successfully called the
	// provider with this token. Stale entries (>30d unused) become
	// candidates for pruning.
	LastUsedAt time.Time `json:"lastUsedAt,omitempty"`
}

// PublicView strips nothing meaningful — the entry has no plaintext
// to redact. We expose this name so future tightening (e.g. hiding
// source machine for shared environments) lands in one place.
func (e RunnerTokenLedgerEntry) PublicView() RunnerTokenLedgerEntry { return e }

var runnerTokenLedgerMu sync.RWMutex

func runnerTokenLedgerPath() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "runner_tokens.json"), nil
}

// HashRunnerToken returns the sha256 hex digest of a plaintext token.
// Used both for ledger writes and for comparing inbound mirror pushes
// against existing entries (so a second mirror of the same secret
// becomes an update, not a duplicate row).
func HashRunnerToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// LoadRunnerTokenLedger returns every recorded runner token, sorted
// most-recent-first. Safe against missing / malformed files (returns
// empty slice).
func LoadRunnerTokenLedger() []RunnerTokenLedgerEntry {
	runnerTokenLedgerMu.RLock()
	defer runnerTokenLedgerMu.RUnlock()
	return loadRunnerTokenLedgerLocked()
}

func loadRunnerTokenLedgerLocked() []RunnerTokenLedgerEntry {
	p, err := runnerTokenLedgerPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var payload struct {
		Tokens []RunnerTokenLedgerEntry `json:"tokens"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	sort.Slice(payload.Tokens, func(i, j int) bool {
		return payload.Tokens[i].SavedAt.After(payload.Tokens[j].SavedAt)
	})
	return payload.Tokens
}

// SaveRunnerTokenLedger writes the ledger atomically (tmp file +
// rename) so a crash mid-write can't corrupt the file.
func SaveRunnerTokenLedger(entries []RunnerTokenLedgerEntry) error {
	runnerTokenLedgerMu.Lock()
	defer runnerTokenLedgerMu.Unlock()
	return saveRunnerTokenLedgerLocked(entries)
}

func saveRunnerTokenLedgerLocked(entries []RunnerTokenLedgerEntry) error {
	p, err := runnerTokenLedgerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	payload := struct {
		Version int                       `json:"version"`
		Tokens  []RunnerTokenLedgerEntry  `json:"tokens"`
	}{Version: 1, Tokens: entries}
	buf, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, buf, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// UpsertRunnerToken adds or updates a ledger entry, keyed by
// (runner, tokenHash). Returns the updated ledger.
func UpsertRunnerToken(entry RunnerTokenLedgerEntry) ([]RunnerTokenLedgerEntry, error) {
	runnerTokenLedgerMu.Lock()
	defer runnerTokenLedgerMu.Unlock()
	if entry.Runner == "" || entry.TokenHash == "" {
		return nil, fmt.Errorf("runner + tokenHash required")
	}
	if entry.SavedAt.IsZero() {
		entry.SavedAt = time.Now()
	}
	existing := loadRunnerTokenLedgerLocked()
	merged := make([]RunnerTokenLedgerEntry, 0, len(existing)+1)
	replaced := false
	for _, e := range existing {
		if e.Runner == entry.Runner && e.TokenHash == entry.TokenHash {
			// preserve LastUsedAt across mirror refreshes
			if entry.LastUsedAt.IsZero() {
				entry.LastUsedAt = e.LastUsedAt
			}
			merged = append(merged, entry)
			replaced = true
			continue
		}
		merged = append(merged, e)
	}
	if !replaced {
		merged = append(merged, entry)
	}
	if err := saveRunnerTokenLedgerLocked(merged); err != nil {
		return nil, err
	}
	return merged, nil
}

// MarkRunnerTokenUsed updates LastUsedAt for the entry matching the
// given runner + plaintext. Called from the runner-auth health loop
// after a successful provider probe. Silent no-op when no row matches.
func MarkRunnerTokenUsed(runner, plaintext string) {
	if runner == "" || plaintext == "" {
		return
	}
	hash := HashRunnerToken(plaintext)
	runnerTokenLedgerMu.Lock()
	defer runnerTokenLedgerMu.Unlock()
	entries := loadRunnerTokenLedgerLocked()
	now := time.Now()
	mutated := false
	for i := range entries {
		if entries[i].Runner == runner && entries[i].TokenHash == hash {
			entries[i].LastUsedAt = now
			mutated = true
		}
	}
	if mutated {
		_ = saveRunnerTokenLedgerLocked(entries)
	}
}

// RevokeRunnerTokenByHash removes the entry matching the given hash.
// Does NOT clear the vault — the caller chooses whether to also delete
// the plaintext (you might keep it briefly to roll back a bad mirror).
func RevokeRunnerTokenByHash(runner, tokenHash string) ([]RunnerTokenLedgerEntry, error) {
	runnerTokenLedgerMu.Lock()
	defer runnerTokenLedgerMu.Unlock()
	existing := loadRunnerTokenLedgerLocked()
	merged := make([]RunnerTokenLedgerEntry, 0, len(existing))
	for _, e := range existing {
		if e.Runner == runner && e.TokenHash == tokenHash {
			continue
		}
		merged = append(merged, e)
	}
	if err := saveRunnerTokenLedgerLocked(merged); err != nil {
		return nil, err
	}
	return merged, nil
}
