package main

// gateway_creds.go — credential persistence for the gateway broker.
//
// All gateway credentials (OAuth access/refresh tokens, expiry, etc.) live in
// the encrypted vault under project "gateway" — NEVER inline in a manifest,
// NEVER in Convex. The CredStore interface abstracts that storage so the broker
// and handlers don't touch vault.go directly, and so tests can use an in-memory
// store that never prompts the macOS login keychain (the maintainer asked to
// keep tests off the keychain — see MEMORY).

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// gatewayVaultProject is the vault project all gateway creds live under.
const gatewayVaultProject = "gateway"

// OAuthCreds is the persisted credential blob for an oauth_code connector.
// Stored as JSON in a single vault entry referenced by the connector's CredRef.
type OAuthCreds struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiryUnix is the access-token expiry in Unix seconds. 0 = unknown
	// (treated as expired so the broker refreshes before use).
	ExpiryUnix int64  `json:"expiry_unix,omitempty"`
	Scope      string `json:"scope,omitempty"`
	TokenURL   string `json:"token_url,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	TokenType  string `json:"token_type,omitempty"`
}

// CredStore reads and writes credential blobs keyed by (project, name). The
// gateway always uses project = "gateway"; the interface keeps project explicit
// so the same store can back other subsystems without collision.
type CredStore interface {
	// GetCreds returns the raw JSON blob for (project, name), or an error if
	// absent.
	GetCreds(project, name string) ([]byte, error)
	// SetCreds persists the raw JSON blob for (project, name).
	SetCreds(project, name string, blob []byte) error
}

// credNameFromRef turns a manifest CredRef into the (project, name) pair the
// store uses. Accepted forms:
//
//	"gateway/<connector>/oauth" -> project "gateway", name "<connector>_oauth"
//	"<connector>/oauth"          -> project "gateway", name "<connector>_oauth"
//	"<name>"                     -> project "gateway", name "<name>"
//
// Vault names disallow "/", so the path tail is flattened with "_". The project
// is always "gateway" for the gateway subsystem regardless of any leading
// "gateway/" in the ref.
func credNameFromRef(ref string) (project, name string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("empty credRef")
	}
	parts := strings.Split(strings.Trim(ref, "/"), "/")
	// Drop a leading "gateway" segment — the project is implied.
	if len(parts) > 1 && parts[0] == gatewayVaultProject {
		parts = parts[1:]
	}
	flat := strings.Join(parts, "_")
	if flat == "" {
		return "", "", fmt.Errorf("invalid credRef %q", ref)
	}
	return gatewayVaultProject, flat, nil
}

// ── vault-backed CredStore ───────────────────────────────────────────────────

// vaultCredStore persists credentials in the encrypted vault (vault.go). This
// is the real implementation used by the running agent.
type vaultCredStore struct {
	vs *VaultStore
}

// newVaultCredStore opens the vault (v2 master-key path with v1 fallback, via
// openVaultOptional) and wraps it. Returns an error if the agent is not
// authenticated / the vault can't be opened.
func newVaultCredStore() (*vaultCredStore, error) {
	vs, err := openVaultOptional()
	if err != nil {
		return nil, fmt.Errorf("gateway: open vault: %w", err)
	}
	if vs == nil {
		return nil, fmt.Errorf("gateway: vault unavailable")
	}
	return &vaultCredStore{vs: vs}, nil
}

func (s *vaultCredStore) GetCreds(project, name string) ([]byte, error) {
	e, err := s.vs.Get(project, name)
	if err != nil {
		return nil, err
	}
	return []byte(e.Value), nil
}

func (s *vaultCredStore) SetCreds(project, name string, blob []byte) error {
	return s.vs.Set(VaultEntry{
		Project:  project,
		Name:     name,
		Category: "gateway-credential",
		Value:    string(blob),
		Notes:    "Personal Agent Gateway credential (auto-managed). Do not edit by hand.",
	})
}

// ── in-memory CredStore (tests) ──────────────────────────────────────────────

// memCredStore is an in-memory CredStore for tests — keeps the test suite off
// the macOS login keychain entirely.
type memCredStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemCredStore() *memCredStore {
	return &memCredStore{m: map[string][]byte{}}
}

func (s *memCredStore) key(project, name string) string { return project + "\x00" + name }

func (s *memCredStore) GetCreds(project, name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[s.key(project, name)]
	if !ok {
		return nil, fmt.Errorf("creds %q/%q not found", project, name)
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (s *memCredStore) SetCreds(project, name string, blob []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(blob))
	copy(cp, blob)
	s.m[s.key(project, name)] = cp
	return nil
}

// ── typed OAuth helpers over a CredStore ─────────────────────────────────────

// loadOAuthCreds reads + decodes the OAuthCreds blob referenced by ref.
func loadOAuthCreds(store CredStore, ref string) (*OAuthCreds, error) {
	project, name, err := credNameFromRef(ref)
	if err != nil {
		return nil, err
	}
	blob, err := store.GetCreds(project, name)
	if err != nil {
		return nil, err
	}
	var c OAuthCreds
	if err := json.Unmarshal(blob, &c); err != nil {
		return nil, fmt.Errorf("decode oauth creds: %w", err)
	}
	return &c, nil
}

// saveOAuthCreds encodes + persists the OAuthCreds blob referenced by ref.
func saveOAuthCreds(store CredStore, ref string, c *OAuthCreds) error {
	project, name, err := credNameFromRef(ref)
	if err != nil {
		return err
	}
	blob, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode oauth creds: %w", err)
	}
	return store.SetCreds(project, name, blob)
}
