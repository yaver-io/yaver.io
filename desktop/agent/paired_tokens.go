package main

// paired_tokens.go — additive multi-user auth for a shared
// headless machine. Solves the follow-up to `yaver auth pair`:
// "My spouse and I both want to use the upstairs Mac mini from
// our phones with separate Apple accounts."
//
// Design:
//
//   - The original `yaver auth` owner (`cfg.AuthToken`) stays
//     the primary. The cfg.json never gets overwritten by a
//     pair operation unless the dev explicitly asks with
//     `yaver auth pair --replace`.
//   - Every subsequent `yaver auth pair` submission appends
//     to ~/.yaver/paired_tokens.json as an additional accepted
//     token, keyed by a stable hash + labeled with the source
//     hostname and a user-friendly tag.
//   - The HTTP auth middleware accepts any token present in
//     either cfg.AuthToken or the paired-tokens ledger.
//   - `yaver auth pair list` + `yaver auth pair revoke <label>`
//     let the owner audit and prune access.
//
// Storage is a single JSON file with atomic write-then-rename
// so concurrent pair accepts can't corrupt the ledger.

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

// PairedToken is one additional accepted auth token.
type PairedToken struct {
	TokenHash  string `json:"tokenHash"`         // sha256 prefix, used as id
	Token      string `json:"token"`             // actual bearer token
	Label      string `json:"label,omitempty"`   // "laptop", "partner-iphone", etc
	ConvexURL  string `json:"convexUrl,omitempty"`
	AddedAt    string `json:"addedAt"`
	SourceHost string `json:"sourceHost,omitempty"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
}

var pairedTokensMu sync.RWMutex

func pairedTokensPath() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "paired_tokens.json"), nil
}

// loadPairedTokens returns every accepted token, ordered by
// most recently added. Safe against missing / malformed
// files.
func loadPairedTokens() []PairedToken {
	p, err := pairedTokensPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var payload struct {
		Tokens []PairedToken `json:"tokens"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	return payload.Tokens
}

func savePairedTokens(tokens []PairedToken) error {
	p, err := pairedTokensPath()
	if err != nil {
		return err
	}
	sort.Slice(tokens, func(i, j int) bool {
		return tokens[i].AddedAt > tokens[j].AddedAt
	})
	data, err := json.MarshalIndent(map[string]interface{}{
		"tokens":    tokens,
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// pairedTokenFingerprint is the stable id used to dedupe paired
// tokens. Uses the first 16 hex chars of sha256 so the ledger
// stays readable in logs.
func pairedTokenFingerprint(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

// AddPairedToken upserts one entry. If the token is already
// present it bumps AddedAt + SourceHost + Label.
func AddPairedToken(token, label, convexURL, sourceHost string) error {
	if token == "" {
		return fmt.Errorf("token required")
	}
	pairedTokensMu.Lock()
	defer pairedTokensMu.Unlock()
	tokens := loadPairedTokens()
	fp := pairedTokenFingerprint(token)
	now := time.Now().UTC().Format(time.RFC3339)
	for i, t := range tokens {
		if t.TokenHash == fp {
			tokens[i].AddedAt = now
			if label != "" {
				tokens[i].Label = label
			}
			if sourceHost != "" {
				tokens[i].SourceHost = sourceHost
			}
			if convexURL != "" {
				tokens[i].ConvexURL = convexURL
			}
			return savePairedTokens(tokens)
		}
	}
	tokens = append(tokens, PairedToken{
		TokenHash:  fp,
		Token:      token,
		Label:      label,
		ConvexURL:  convexURL,
		AddedAt:    now,
		SourceHost: sourceHost,
	})
	return savePairedTokens(tokens)
}

// RevokePairedToken removes a single entry by label OR by
// fingerprint prefix. Returns the number of tokens removed.
func RevokePairedToken(labelOrFp string) int {
	pairedTokensMu.Lock()
	defer pairedTokensMu.Unlock()
	tokens := loadPairedTokens()
	filtered := tokens[:0]
	removed := 0
	for _, t := range tokens {
		if t.Label == labelOrFp || t.TokenHash == labelOrFp ||
			(len(labelOrFp) >= 4 && len(t.TokenHash) >= len(labelOrFp) && t.TokenHash[:len(labelOrFp)] == labelOrFp) {
			removed++
			continue
		}
		filtered = append(filtered, t)
	}
	if removed > 0 {
		_ = savePairedTokens(filtered)
	}
	return removed
}

// ListPairedTokens returns the ledger with tokens masked so
// accidental `cat` of the file doesn't leak the bearer.
func ListPairedTokens() []PairedToken {
	pairedTokensMu.RLock()
	defer pairedTokensMu.RUnlock()
	tokens := loadPairedTokens()
	out := make([]PairedToken, len(tokens))
	for i, t := range tokens {
		out[i] = t
		out[i].Token = "…" + t.TokenHash
	}
	return out
}

// IsPairedToken reports whether a bearer token is in the
// paired ledger. Used by the HTTP auth middleware to allow
// additional users without touching cfg.AuthToken. The fingerprint
// compare is constant-time (defense in depth — fp is a SHA prefix
// so a timing oracle leaks at most "this hash starts with these
// bytes", but we close it anyway).
func IsPairedToken(token string) bool {
	if token == "" {
		return false
	}
	fp := pairedTokenFingerprint(token)
	pairedTokensMu.RLock()
	defer pairedTokensMu.RUnlock()
	for _, t := range loadPairedTokens() {
		if secretEqual(t.TokenHash, fp) {
			return true
		}
	}
	return false
}

// TouchPairedToken records a usage timestamp. Called from the
// auth middleware after a token validates so the owner can see
// "which of my paired users hit me recently" via `yaver auth
// pair list`.
func TouchPairedToken(token string) {
	if token == "" {
		return
	}
	go func() {
		pairedTokensMu.Lock()
		defer pairedTokensMu.Unlock()
		tokens := loadPairedTokens()
		fp := pairedTokenFingerprint(token)
		changed := false
		for i, t := range tokens {
			if secretEqual(t.TokenHash, fp) {
				tokens[i].LastUsedAt = time.Now().UTC().Format(time.RFC3339)
				changed = true
				break
			}
		}
		if changed {
			_ = savePairedTokens(tokens)
		}
	}()
}
