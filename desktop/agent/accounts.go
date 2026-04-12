package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AccountProvider identifies a cloud provider the user can connect.
type AccountProvider string

const (
	ProviderHetzner      AccountProvider = "hetzner"
	ProviderNeon         AccountProvider = "neon"
	ProviderVercel       AccountProvider = "vercel"
	ProviderCloudflare   AccountProvider = "cloudflare"
	ProviderSupabase     AccountProvider = "supabase"
	ProviderConvex       AccountProvider = "convex"
	ProviderTurso        AccountProvider = "turso"
	ProviderRailway      AccountProvider = "railway"
	ProviderFly          AccountProvider = "fly"
	ProviderRender       AccountProvider = "render"
	ProviderAWS          AccountProvider = "aws"
	ProviderGCP          AccountProvider = "gcp"
	ProviderDigitalOcean AccountProvider = "digitalocean"
	ProviderYaver        AccountProvider = "yaver"
)

// AccountProviderMeta describes a provider's auth requirements for UI rendering.
type AccountProviderMeta struct {
	ID        AccountProvider `json:"id"`
	Label     string          `json:"label"`
	AuthType  string          `json:"authType"`          // "token" | "browser" | "key+secret" | "json"
	Fields    []string        `json:"fields"`             // e.g. ["token"], ["accessKey","secretKey"]
	SignupURL string          `json:"signupURL"`
	TokenURL  string          `json:"tokenURL"`           // link to create the token
	Notes     string          `json:"notes,omitempty"`
}

func AccountProviders() []AccountProviderMeta {
	return []AccountProviderMeta{
		{ID: ProviderHetzner, Label: "Hetzner", AuthType: "token", Fields: []string{"token"}, SignupURL: "https://www.hetzner.com/cloud", TokenURL: "https://console.hetzner.cloud/projects → Security → API Tokens"},
		{ID: ProviderNeon, Label: "Neon", AuthType: "token", Fields: []string{"token"}, SignupURL: "https://neon.tech", TokenURL: "https://console.neon.tech/app/settings/api-keys"},
		{ID: ProviderVercel, Label: "Vercel", AuthType: "token", Fields: []string{"token"}, SignupURL: "https://vercel.com", TokenURL: "https://vercel.com/account/tokens", Notes: "Or run `npx vercel login` instead — the CLI stores its own token at ~/.vercel/"},
		{ID: ProviderCloudflare, Label: "Cloudflare", AuthType: "token", Fields: []string{"token", "accountId"}, SignupURL: "https://cloudflare.com", TokenURL: "https://dash.cloudflare.com/profile/api-tokens"},
		{ID: ProviderSupabase, Label: "Supabase Cloud", AuthType: "token", Fields: []string{"token"}, SignupURL: "https://supabase.com", TokenURL: "https://supabase.com/dashboard/account/tokens"},
		{ID: ProviderConvex, Label: "Convex Cloud", AuthType: "browser", Fields: []string{}, SignupURL: "https://convex.dev", Notes: "Run `npx convex login` — Yaver reads token from ~/.convex/"},
		{ID: ProviderTurso, Label: "Turso", AuthType: "token", Fields: []string{"token"}, SignupURL: "https://turso.tech", TokenURL: "turso auth login then `turso auth token`"},
		{ID: ProviderRailway, Label: "Railway", AuthType: "token", Fields: []string{"token"}, SignupURL: "https://railway.app", TokenURL: "https://railway.app/account/tokens"},
		{ID: ProviderFly, Label: "Fly.io", AuthType: "browser", Fields: []string{}, SignupURL: "https://fly.io", Notes: "Run `fly auth login` — Yaver reads token from ~/.fly/"},
		{ID: ProviderRender, Label: "Render", AuthType: "token", Fields: []string{"token"}, SignupURL: "https://render.com", TokenURL: "https://dashboard.render.com/u/settings#api-keys"},
		{ID: ProviderAWS, Label: "AWS", AuthType: "key+secret", Fields: []string{"accessKey", "secretKey", "region"}, SignupURL: "https://aws.amazon.com", TokenURL: "https://console.aws.amazon.com/iam/home#/users"},
		{ID: ProviderGCP, Label: "Google Cloud", AuthType: "browser", Fields: []string{}, SignupURL: "https://cloud.google.com", Notes: "Run `gcloud auth login` — Yaver reads token from gcloud CLI"},
		{ID: ProviderDigitalOcean, Label: "DigitalOcean", AuthType: "token", Fields: []string{"token"}, SignupURL: "https://digitalocean.com", TokenURL: "https://cloud.digitalocean.com/account/api/tokens"},
		{ID: ProviderYaver, Label: "Yaver Cloud", AuthType: "token", Fields: []string{"token"}, SignupURL: "https://yaver.io", TokenURL: "Yaver Cloud onboarding flow"},
	}
}

// AccountRecord is the stored envelope for a connected provider.
type AccountRecord struct {
	Provider    AccountProvider   `json:"provider"`
	Label       string            `json:"label,omitempty"`
	Fields      map[string]string `json:"fields"`
	ConnectedAt string            `json:"connectedAt"`
	LastUsedAt  string            `json:"lastUsedAt,omitempty"`
}

// AccountSummary is the public (non-secret) view of an account.
type AccountSummary struct {
	Provider    AccountProvider `json:"provider"`
	Label       string          `json:"label,omitempty"`
	Connected   bool            `json:"connected"`
	ConnectedAt string          `json:"connectedAt,omitempty"`
	LastUsedAt  string          `json:"lastUsedAt,omitempty"`
	HasSecret   bool            `json:"hasSecret"`
	Hint        string          `json:"hint,omitempty"`
}

// AccountsManager handles encrypted at-rest storage for provider credentials.
type AccountsManager struct {
	mu      sync.Mutex
	baseDir string
	key     []byte
}

func NewAccountsManager() *AccountsManager {
	home, _ := os.UserHomeDir()
	return &AccountsManager{baseDir: filepath.Join(home, ".yaver", "secrets")}
}

var globalAccountsManager = NewAccountsManager()

func (a *AccountsManager) ensureKey() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.key) == 32 {
		return a.key, nil
	}
	if err := os.MkdirAll(a.baseDir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(filepath.Dir(a.baseDir), "master.key")
	if data, err := os.ReadFile(keyPath); err == nil {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("master.key invalid; delete it to regenerate (will orphan secrets)")
		}
		a.key = key
		return a.key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	enc := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(keyPath, []byte(enc), 0o600); err != nil {
		return nil, err
	}
	a.key = key
	return a.key, nil
}

func (a *AccountsManager) encrypt(plaintext []byte) (string, error) {
	key, err := a.ensureKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (a *AccountsManager) decrypt(enc string) ([]byte, error) {
	key, err := a.ensureKey()
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, nil)
}

func (a *AccountsManager) path(provider AccountProvider) string {
	return filepath.Join(a.baseDir, string(provider)+".enc")
}

// Connect stores credentials for a provider.
func (a *AccountsManager) Connect(provider AccountProvider, label string, fields map[string]string) error {
	if err := validateAccountFields(provider, fields); err != nil {
		return err
	}
	rec := AccountRecord{
		Provider:    provider,
		Label:       label,
		Fields:      fields,
		ConnectedAt: time.Now().Format(time.RFC3339),
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	enc, err := a.encrypt(body)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.baseDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(a.path(provider), []byte(enc), 0o600)
}

// Get returns decrypted credentials for a provider (or nil if not connected).
func (a *AccountsManager) Get(provider AccountProvider) (*AccountRecord, error) {
	data, err := os.ReadFile(a.path(provider))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	body, err := a.decrypt(string(data))
	if err != nil {
		return nil, err
	}
	var rec AccountRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// Disconnect removes stored credentials.
func (a *AccountsManager) Disconnect(provider AccountProvider) error {
	err := os.Remove(a.path(provider))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List returns every provider's connection state. Does NOT decrypt secrets.
func (a *AccountsManager) List() []AccountSummary {
	var out []AccountSummary
	for _, meta := range AccountProviders() {
		summary := AccountSummary{Provider: meta.ID}
		if rec, err := a.Get(meta.ID); err == nil && rec != nil {
			summary.Connected = true
			summary.Label = rec.Label
			summary.ConnectedAt = rec.ConnectedAt
			summary.LastUsedAt = rec.LastUsedAt
			summary.HasSecret = len(rec.Fields) > 0
		} else {
			summary.Hint = meta.TokenURL
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Connected != out[j].Connected {
			return out[i].Connected
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}

// MarkUsed updates last-used timestamp. Best-effort.
func (a *AccountsManager) MarkUsed(provider AccountProvider) {
	rec, err := a.Get(provider)
	if err != nil || rec == nil {
		return
	}
	rec.LastUsedAt = time.Now().Format(time.RFC3339)
	body, _ := json.Marshal(rec)
	enc, err := a.encrypt(body)
	if err != nil {
		return
	}
	_ = os.WriteFile(a.path(provider), []byte(enc), 0o600)
}

func validateAccountFields(provider AccountProvider, fields map[string]string) error {
	for _, meta := range AccountProviders() {
		if meta.ID != provider {
			continue
		}
		for _, f := range meta.Fields {
			if fields[f] == "" {
				return fmt.Errorf("missing required field %q for %s", f, provider)
			}
		}
		return nil
	}
	return fmt.Errorf("unknown provider %s", provider)
}

// ---------- MCP / HTTP handlers ----------

func mcpAccountList() interface{} {
	return map[string]interface{}{
		"accounts":  globalAccountsManager.List(),
		"providers": AccountProviders(),
	}
}

func mcpAccountConnect(provider, label, fieldsJSON string) interface{} {
	fields := parseJSONArgs(fieldsJSON)
	str := make(map[string]string, len(fields))
	for k, v := range fields {
		str[k] = fmt.Sprintf("%v", v)
	}
	if err := globalAccountsManager.Connect(AccountProvider(provider), label, str); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "provider": provider}
}

func mcpAccountDisconnect(provider string) interface{} {
	if err := globalAccountsManager.Disconnect(AccountProvider(provider)); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true}
}

func mcpAccountStatus(provider string) interface{} {
	rec, err := globalAccountsManager.Get(AccountProvider(provider))
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if rec == nil {
		return map[string]interface{}{"connected": false, "provider": provider}
	}
	return map[string]interface{}{
		"connected":   true,
		"provider":    provider,
		"label":       rec.Label,
		"connectedAt": rec.ConnectedAt,
		"lastUsedAt":  rec.LastUsedAt,
		// Never return fields — that's the whole point of encrypting them.
	}
}

// accountField pulls a single credential field (e.g. "token"). Returns "" if
// the account is not connected.
func accountField(provider AccountProvider, field string) string {
	rec, err := globalAccountsManager.Get(provider)
	if err != nil || rec == nil {
		return ""
	}
	globalAccountsManager.MarkUsed(provider)
	return rec.Fields[field]
}
