package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Per-project API tokens for third-party app developers.
//
// Owner-bearer auth (the CLI's `yaver auth` token) belongs to the developer
// building the project. Their end users — and the RN/web apps those users
// run — must NEVER see that token. Instead each phone-project mints its
// own scoped tokens (`pp_<slug>_<rand>`) that only authorise CRUD against
// that single project's `/data/<slug>/*` surface. Revoking one doesn't
// touch the others; revoking them all doesn't lock the developer out of
// their agent.
//
// Tokens are stored as SHA-256 hashes in the project dir
// (`oauth-providers.yaml`-peer: `tokens.yaml`, 0600). Plaintext is returned
// exactly once on mint — same contract as Convex's sdkTokens. Loss =
// rotation, not recovery.

const phoneTokenPrefix = "pp_"

// PhoneProjectToken is one issued key — shipped over the wire WITHOUT the
// raw token. Use PhoneProjectTokenMint for the one-time mint response.
type PhoneProjectToken struct {
	ID        string `yaml:"id" json:"id"`                   // short random id (not the secret)
	Label     string `yaml:"label" json:"label"`             // user-supplied human name
	Hash      string `yaml:"hash,omitempty" json:"-"`        // sha256(raw), never JSON-serialized
	CreatedAt string `yaml:"createdAt" json:"createdAt"`
	LastUsed  string `yaml:"lastUsed,omitempty" json:"lastUsed,omitempty"`
}

// PhoneProjectTokenMint is the one-shot response when a new token is
// created. `Raw` is shown once and never returned again.
type PhoneProjectTokenMint struct {
	Token PhoneProjectToken `json:"token"`
	Raw   string            `json:"raw"` // Only included in the mint response
}

type phoneTokensFile struct {
	Version int                 `yaml:"version"`
	Tokens  []PhoneProjectToken `yaml:"tokens"`
}

func phoneTokensPath(dir string) string { return filepath.Join(dir, "tokens.yaml") }

func loadPhoneTokens(dir string) (*phoneTokensFile, error) {
	data, err := os.ReadFile(phoneTokensPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return &phoneTokensFile{Version: 1}, nil
		}
		return nil, err
	}
	var f phoneTokensFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse tokens.yaml: %w", err)
	}
	if f.Version == 0 {
		f.Version = 1
	}
	return &f, nil
}

func savePhoneTokens(dir string, f *phoneTokensFile) error {
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(phoneTokensPath(dir), data, 0o600)
}

func hashRawToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// MintPhoneProjectToken issues a new token. Returns the raw token in the
// response ONCE — callers must display it to the user immediately; Yaver
// never stores or re-surfaces the plaintext.
func MintPhoneProjectToken(slug, label string) (*PhoneProjectTokenMint, error) {
	slug = Slugify(slug)
	if slug == "" {
		return nil, errors.New("slug required")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "untitled"
	}
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(phoneMetaPath(dir)); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrPhoneProjectNotFound, slug)
		}
		return nil, err
	}

	// 32 bytes of entropy is plenty — hex-encoded it's 64 chars, prefixed
	// with pp_<slug>_ so operators can spot the source at a glance.
	raw := randomHex(32)
	if raw == "" {
		return nil, fmt.Errorf("random source unavailable")
	}
	full := phoneTokenPrefix + slug + "_" + raw

	idBytes := randomHex(6)
	if idBytes == "" {
		return nil, fmt.Errorf("random source unavailable")
	}

	tok := PhoneProjectToken{
		ID:        idBytes,
		Label:     label,
		Hash:      hashRawToken(full),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	f, err := loadPhoneTokens(dir)
	if err != nil {
		return nil, err
	}
	f.Tokens = append(f.Tokens, tok)
	if err := savePhoneTokens(dir, f); err != nil {
		return nil, err
	}
	// Redact Hash from the wire — clients never need it.
	wireTok := tok
	wireTok.Hash = ""
	return &PhoneProjectTokenMint{Token: wireTok, Raw: full}, nil
}

// ListPhoneProjectTokens returns token summaries (no raw, no hash). The
// UI renders this to show the user which keys they've minted and when.
func ListPhoneProjectTokens(slug string) ([]PhoneProjectToken, error) {
	slug = Slugify(slug)
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(phoneMetaPath(dir)); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrPhoneProjectNotFound, slug)
		}
		return nil, err
	}
	f, err := loadPhoneTokens(dir)
	if err != nil {
		return nil, err
	}
	out := make([]PhoneProjectToken, 0, len(f.Tokens))
	for _, t := range f.Tokens {
		t.Hash = "" // never leak
		out = append(out, t)
	}
	return out, nil
}

// RevokePhoneProjectToken removes a token by id. Revoked tokens stop
// working on the next request — the validator re-reads the file each time
// (cheap, the project is already on disk).
func RevokePhoneProjectToken(slug, tokenID string) error {
	slug = Slugify(slug)
	if slug == "" || tokenID == "" {
		return errors.New("slug and tokenId required")
	}
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return err
	}
	f, err := loadPhoneTokens(dir)
	if err != nil {
		return err
	}
	kept := f.Tokens[:0]
	var found bool
	for _, t := range f.Tokens {
		if t.ID == tokenID {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		return fmt.Errorf("token %q not found", tokenID)
	}
	f.Tokens = kept
	return savePhoneTokens(dir, f)
}

// ValidatePhoneProjectToken checks a raw `pp_<slug>_...` token against the
// project's stored hashes. Returns the resolved token summary (no hash) on
// success; a sentinel error otherwise. Updates LastUsed best-effort.
var ErrInvalidPhoneProjectToken = errors.New("invalid phone-project token")

func ValidatePhoneProjectToken(rawToken string) (*PhoneProjectToken, string, error) {
	if !strings.HasPrefix(rawToken, phoneTokenPrefix) {
		return nil, "", ErrInvalidPhoneProjectToken
	}
	// Extract the slug so we know which project's tokens.yaml to read.
	// Format: pp_<slug>_<hex>. Slug may contain hyphens; we find the last
	// underscore to split.
	body := strings.TrimPrefix(rawToken, phoneTokenPrefix)
	cut := strings.LastIndex(body, "_")
	if cut <= 0 {
		return nil, "", ErrInvalidPhoneProjectToken
	}
	slug := body[:cut]
	dir, err := PhoneProjectDir(slug)
	if err != nil {
		return nil, "", ErrInvalidPhoneProjectToken
	}
	if _, err := os.Stat(phoneMetaPath(dir)); err != nil {
		return nil, "", ErrInvalidPhoneProjectToken
	}
	f, err := loadPhoneTokens(dir)
	if err != nil {
		return nil, "", ErrInvalidPhoneProjectToken
	}
	hashed := hashRawToken(rawToken)
	for i, t := range f.Tokens {
		if t.Hash == hashed {
			// Update LastUsed — best-effort; ignore write errors so a
			// read-only filesystem doesn't block auth.
			f.Tokens[i].LastUsed = time.Now().UTC().Format(time.RFC3339)
			_ = savePhoneTokens(dir, f)
			clean := f.Tokens[i]
			clean.Hash = ""
			return &clean, slug, nil
		}
	}
	return nil, "", ErrInvalidPhoneProjectToken
}

// ---- HTTP handlers ----

// handlePhoneTokens serves CRUD for /phone/projects/tokens.
//
// GET  ?slug=X             → list (summaries only)
// POST                     → {slug, label} → mint (returns raw ONCE)
// DELETE ?slug=X&tokenId=Y → revoke
func (s *HTTPServer) handlePhoneTokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		slug := r.URL.Query().Get("slug")
		if slug == "" {
			jsonError(w, http.StatusBadRequest, "slug required")
			return
		}
		tokens, err := ListPhoneProjectTokens(slug)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "phone project not found") {
				status = http.StatusNotFound
			}
			jsonError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"tokens": tokens})

	case http.MethodPost:
		var body struct {
			Slug  string `json:"slug"`
			Label string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		if body.Slug == "" {
			jsonError(w, http.StatusBadRequest, "slug required")
			return
		}
		mint, err := MintPhoneProjectToken(body.Slug, body.Label)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "phone project not found") {
				status = http.StatusNotFound
			}
			jsonError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, mint)

	case http.MethodDelete:
		slug := r.URL.Query().Get("slug")
		tokenID := r.URL.Query().Get("tokenId")
		if slug == "" || tokenID == "" {
			jsonError(w, http.StatusBadRequest, "slug and tokenId required")
			return
		}
		if err := RevokePhoneProjectToken(slug, tokenID); err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			jsonError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})

	default:
		jsonError(w, http.StatusMethodNotAllowed, "GET / POST / DELETE")
	}
}
