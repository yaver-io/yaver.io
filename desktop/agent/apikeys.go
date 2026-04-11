package main

// apikeys.go — `yaver apikey` facade for managing SDK tokens
// as first-class API keys. Replaces Unkey / Kinde for the
// solo-dev case where the dev needs a labeled API key per
// shipped app + a usage counter so they can see which key is
// hammering the agent.
//
// This is a thin management layer on top of the existing
// SDK-token system (sdk_token.go / CreateSdkToken) — it adds:
//
//   - A local on-disk registry (~/.yaver/apikeys/registry.json)
//     mapping token-hash → {label, createdAt, usageCount,
//     lastUsedAt, rateLimitPerMin}.
//   - Usage tracking that increments on every request that
//     presents the token.
//   - A `yaver apikey` CLI for the common flows.
//   - /apikeys HTTP endpoints for the mobile Monitor tab.
//
// The actual token + scope validation still lives in the SDK
// token system. This file is bookkeeping on top of that.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// APIKeyRecord is one registered key's metadata.
type APIKeyRecord struct {
	TokenHash        string `json:"tokenHash"` // sha256 of the raw token
	Label            string `json:"label"`
	CreatedAt        string `json:"createdAt"`
	LastUsedAt       string `json:"lastUsedAt,omitempty"`
	UsageCount       int64  `json:"usageCount"`
	RateLimitPerMin  int    `json:"rateLimitPerMin,omitempty"`
	Disabled         bool   `json:"disabled,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
}

var apiKeyMu sync.Mutex

func apiKeysPath() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "apikeys")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "registry.json"), nil
}

func loadAPIKeys() (map[string]*APIKeyRecord, error) {
	p, err := apiKeysPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*APIKeyRecord{}, nil
		}
		return nil, err
	}
	var payload struct {
		Keys map[string]*APIKeyRecord `json:"keys"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Keys == nil {
		payload.Keys = map[string]*APIKeyRecord{}
	}
	return payload.Keys, nil
}

func saveAPIKeys(keys map[string]*APIKeyRecord) error {
	p, err := apiKeysPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]interface{}{"keys": keys}, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:32]
}

// RegisterAPIKey stores metadata for a newly-created SDK
// token. Called from the SDK-token creation path in a
// follow-up; for now exposed as an explicit `yaver apikey
// register` so the dev can retroactively label existing
// tokens.
func RegisterAPIKey(token, label string, scopes []string) error {
	apiKeyMu.Lock()
	defer apiKeyMu.Unlock()

	keys, err := loadAPIKeys()
	if err != nil {
		return err
	}
	hash := hashToken(token)
	if existing := keys[hash]; existing != nil {
		existing.Label = label
		existing.Scopes = scopes
		return saveAPIKeys(keys)
	}
	keys[hash] = &APIKeyRecord{
		TokenHash: hash,
		Label:     label,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Scopes:    scopes,
	}
	return saveAPIKeys(keys)
}

// RecordAPIKeyUsage bumps the usage counter. Called from the
// auth middleware; best-effort, never blocks the request path.
func RecordAPIKeyUsage(token string) {
	if token == "" {
		return
	}
	hash := hashToken(token)
	go func() {
		apiKeyMu.Lock()
		defer apiKeyMu.Unlock()
		keys, err := loadAPIKeys()
		if err != nil {
			return
		}
		rec := keys[hash]
		if rec == nil {
			return
		}
		rec.UsageCount++
		rec.LastUsedAt = time.Now().UTC().Format(time.RFC3339)
		_ = saveAPIKeys(keys)
	}()
}

// DisableAPIKey flips the disabled flag so the HTTP auth
// middleware can deny presentations of this token. The token
// itself still exists in the SDK-token store; this is a
// logical kill-switch layered on top.
func DisableAPIKey(hashOrLabel string) error {
	apiKeyMu.Lock()
	defer apiKeyMu.Unlock()
	keys, err := loadAPIKeys()
	if err != nil {
		return err
	}
	for h, rec := range keys {
		if h == hashOrLabel || rec.Label == hashOrLabel || h[:8] == hashOrLabel {
			rec.Disabled = true
			return saveAPIKeys(keys)
		}
	}
	return fmt.Errorf("api key %q not found", hashOrLabel)
}

// IsAPIKeyDisabled reports whether a token has been disabled
// via the local registry. Auth middleware calls this on every
// request after the upstream token validation succeeds.
func IsAPIKeyDisabled(token string) bool {
	hash := hashToken(token)
	apiKeyMu.Lock()
	defer apiKeyMu.Unlock()
	keys, err := loadAPIKeys()
	if err != nil {
		return false
	}
	rec := keys[hash]
	return rec != nil && rec.Disabled
}

// ListAPIKeys returns the registry sorted by label.
func ListAPIKeys() []*APIKeyRecord {
	apiKeyMu.Lock()
	defer apiKeyMu.Unlock()
	keys, _ := loadAPIKeys()
	out := make([]*APIKeyRecord, 0, len(keys))
	for _, r := range keys {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// --- CLI ------------------------------------------------------------------

func runAPIKey(args []string) {
	if len(args) == 0 {
		printAPIKeyUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "list", "ls":
		apiKeyListCmd()
	case "register":
		apiKeyRegisterCmd(args[1:])
	case "disable":
		apiKeyDisableCmd(args[1:])
	case "enable":
		apiKeyEnableCmd(args[1:])
	case "help", "--help", "-h":
		printAPIKeyUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown apikey subcommand: %s\n\n", args[0])
		printAPIKeyUsage()
		os.Exit(1)
	}
}

func printAPIKeyUsage() {
	fmt.Print(`Yaver apikey — manage SDK tokens as labeled API keys.

Usage:
  yaver apikey list                               List every registered key + usage
  yaver apikey register <token> --label <name>   Tag an existing token with a label
  yaver apikey disable <label|hash-prefix>        Revoke a key without deleting it
  yaver apikey enable <label|hash-prefix>         Re-enable a disabled key

Creation of new SDK tokens still goes through:
  yaver sdk-token create --label "..."

This command is the management layer on top of that — labels,
usage counts, disable/enable without nuking the underlying token.
`)
}

func apiKeyListCmd() {
	keys := ListAPIKeys()
	if len(keys) == 0 {
		fmt.Println("No API keys registered. `yaver apikey register <token> --label name` to add one.")
		return
	}
	for _, k := range keys {
		state := "active"
		if k.Disabled {
			state = "disabled"
		}
		fmt.Printf("  %s  [%s]  %s  usage=%d",
			k.TokenHash[:8], state, k.Label, k.UsageCount)
		if k.LastUsedAt != "" {
			fmt.Printf("  last=%s", k.LastUsedAt)
		}
		fmt.Println()
		if len(k.Scopes) > 0 {
			fmt.Printf("    scopes: %v\n", k.Scopes)
		}
	}
}

func apiKeyRegisterCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver apikey register <token> --label <name>")
		os.Exit(1)
	}
	token := args[0]
	label := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--label" && i+1 < len(args) {
			label = args[i+1]
			i++
		}
	}
	if label == "" {
		label = "key-" + randomID()
	}
	if err := RegisterAPIKey(token, label, nil); err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ registered %s\n", label)
}

func apiKeyDisableCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver apikey disable <label|hash>")
		os.Exit(1)
	}
	if err := DisableAPIKey(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "disable: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ disabled %s\n", args[0])
}

func apiKeyEnableCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver apikey enable <label|hash>")
		os.Exit(1)
	}
	apiKeyMu.Lock()
	defer apiKeyMu.Unlock()
	keys, err := loadAPIKeys()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	needle := args[0]
	for h, rec := range keys {
		if h == needle || rec.Label == needle || h[:8] == needle {
			rec.Disabled = false
			_ = saveAPIKeys(keys)
			fmt.Printf("✓ enabled %s\n", needle)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "api key %q not found\n", needle)
	os.Exit(2)
}

// --- HTTP -----------------------------------------------------------------

func (s *HTTPServer) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"keys": ListAPIKeys(),
	})
}
