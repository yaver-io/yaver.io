package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// OAuth providers per phone-project. Stored as a sibling YAML next to schema /
// auth / seed inside ~/.yaver/phone-projects/<slug>/. This is the developer's
// OWN OAuth credentials — Team ID, Services ID, Client ID, Tenant ID, etc. —
// that end-users of their app will sign in against. Distinct from the agent's
// mail-fetch credentials in config.go, which are the developer's personal
// credentials for reading their own mailbox.
//
// Secrets (Apple .p8 private key, Google/Microsoft client secrets) travel
// with the project when pushed to another of the developer's own boxes —
// that's intentional, the target agent needs them to run the OAuth flow.
// Follow-up (PHONE_EXPORT_PIPELINE.md §Handoff 3): add an ExportOptions.
// NoSecrets flag so the switch-engine promote path (to Supabase, Convex,
// etc.) can strip them.

// PhoneOAuthApple is the Sign in with Apple config. Reference:
// https://developer.apple.com/documentation/sign_in_with_apple/sign_in_with_apple_rest_api
type PhoneOAuthApple struct {
	TeamID       string   `yaml:"teamId,omitempty" json:"teamId,omitempty"`             // 10 uppercase alphanumeric
	ServicesID   string   `yaml:"servicesId,omitempty" json:"servicesId,omitempty"`     // reverse-DNS format
	KeyID        string   `yaml:"keyId,omitempty" json:"keyId,omitempty"`               // 10 uppercase alphanumeric
	PrivateKey   string   `yaml:"privateKey,omitempty" json:"privateKey,omitempty"`     // .p8 contents (multi-line PEM)
	RedirectURIs []string `yaml:"redirectURIs,omitempty" json:"redirectURIs,omitempty"` // registered return URLs
}

// PhoneOAuthGoogle is the Google Sign-In / Gmail config. Client ID ends with
// `.apps.googleusercontent.com`.
type PhoneOAuthGoogle struct {
	ClientID     string   `yaml:"clientId,omitempty" json:"clientId,omitempty"`
	ClientSecret string   `yaml:"clientSecret,omitempty" json:"clientSecret,omitempty"`
	RedirectURIs []string `yaml:"redirectURIs,omitempty" json:"redirectURIs,omitempty"`
}

// PhoneOAuthMicrosoft is the Microsoft / O365 config. Client + tenant IDs
// are GUIDs.
type PhoneOAuthMicrosoft struct {
	TenantID     string   `yaml:"tenantId,omitempty" json:"tenantId,omitempty"`
	ClientID     string   `yaml:"clientId,omitempty" json:"clientId,omitempty"`
	ClientSecret string   `yaml:"clientSecret,omitempty" json:"clientSecret,omitempty"`
	RedirectURIs []string `yaml:"redirectURIs,omitempty" json:"redirectURIs,omitempty"`
}

// PhoneOAuthConfig is the top-level payload for GET/POST /phone/projects/oauth.
type PhoneOAuthConfig struct {
	Apple     *PhoneOAuthApple     `yaml:"apple,omitempty" json:"apple,omitempty"`
	Google    *PhoneOAuthGoogle    `yaml:"google,omitempty" json:"google,omitempty"`
	Microsoft *PhoneOAuthMicrosoft `yaml:"microsoft,omitempty" json:"microsoft,omitempty"`
}

// PhoneOAuthStatus reports which providers have been filled in. Safe to
// return over HTTP even if the underlying config holds secrets — the status
// surface is a summary, not the raw values.
type PhoneOAuthStatus struct {
	Apple     bool `json:"apple"`
	Google    bool `json:"google"`
	Microsoft bool `json:"microsoft"`
}

func phoneOAuthPath(dir string) string { return filepath.Join(dir, "oauth-providers.yaml") }

// LoadPhoneOAuth reads oauth-providers.yaml. Missing file is NOT an error —
// returns a zero-value config so callers can treat "never configured" the
// same as "all empty".
func LoadPhoneOAuth(slug string) (*PhoneOAuthConfig, error) {
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
	data, err := os.ReadFile(phoneOAuthPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return &PhoneOAuthConfig{}, nil
		}
		return nil, err
	}
	var cfg PhoneOAuthConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse oauth-providers.yaml: %w", err)
	}
	return &cfg, nil
}

// SavePhoneOAuth validates + writes oauth-providers.yaml. Partial updates are
// supported — a nil sub-struct means "leave that provider alone", NOT "wipe
// it". Callers clear a provider by sending an explicit empty struct.
func SavePhoneOAuth(slug string, patch *PhoneOAuthConfig) (*PhoneOAuthConfig, error) {
	if patch == nil {
		return nil, fmt.Errorf("oauth patch required")
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
	cur, err := LoadPhoneOAuth(slug)
	if err != nil {
		return nil, err
	}
	if patch.Apple != nil {
		if err := validateAppleOAuth(patch.Apple); err != nil {
			return nil, fmt.Errorf("apple: %w", err)
		}
		cur.Apple = patch.Apple
	}
	if patch.Google != nil {
		if err := validateGoogleOAuth(patch.Google); err != nil {
			return nil, fmt.Errorf("google: %w", err)
		}
		cur.Google = patch.Google
	}
	if patch.Microsoft != nil {
		if err := validateMicrosoftOAuth(patch.Microsoft); err != nil {
			return nil, fmt.Errorf("microsoft: %w", err)
		}
		cur.Microsoft = patch.Microsoft
	}
	data, err := yaml.Marshal(cur)
	if err != nil {
		return nil, err
	}
	// 0600 — secrets inside. The phone-project dir is already 0700 so the
	// file perms are defence-in-depth, not the primary barrier.
	if err := os.WriteFile(phoneOAuthPath(dir), data, 0o600); err != nil {
		return nil, err
	}
	return cur, nil
}

// PhoneOAuthStatusFor returns which providers have enough populated fields
// to actually run an OAuth flow. Used by the mobile Deploy section to show a
// "auth configured" checkmark without exposing the raw values.
func PhoneOAuthStatusFor(cfg *PhoneOAuthConfig) PhoneOAuthStatus {
	if cfg == nil {
		return PhoneOAuthStatus{}
	}
	return PhoneOAuthStatus{
		Apple: cfg.Apple != nil &&
			cfg.Apple.TeamID != "" &&
			cfg.Apple.ServicesID != "" &&
			cfg.Apple.KeyID != "" &&
			cfg.Apple.PrivateKey != "",
		Google: cfg.Google != nil &&
			cfg.Google.ClientID != "" &&
			cfg.Google.ClientSecret != "",
		Microsoft: cfg.Microsoft != nil &&
			cfg.Microsoft.TenantID != "" &&
			cfg.Microsoft.ClientID != "" &&
			cfg.Microsoft.ClientSecret != "",
	}
}

// ---- Validation ----

var (
	appleIDRE    = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	appleServRE  = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)
	googleClient = regexp.MustCompile(`\.apps\.googleusercontent\.com$`)
	guidRE       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func validateAppleOAuth(a *PhoneOAuthApple) error {
	if a.TeamID != "" && !appleIDRE.MatchString(a.TeamID) {
		return fmt.Errorf("teamId must be 10 uppercase alphanumeric chars, got %q", a.TeamID)
	}
	if a.KeyID != "" && !appleIDRE.MatchString(a.KeyID) {
		return fmt.Errorf("keyId must be 10 uppercase alphanumeric chars, got %q", a.KeyID)
	}
	if a.ServicesID != "" && !appleServRE.MatchString(a.ServicesID) {
		return fmt.Errorf("servicesId must be reverse-DNS format, got %q", a.ServicesID)
	}
	if a.PrivateKey != "" && !strings.Contains(a.PrivateKey, "BEGIN PRIVATE KEY") {
		return fmt.Errorf("privateKey must be a PEM-encoded .p8 (starts with '-----BEGIN PRIVATE KEY-----')")
	}
	return nil
}

func validateGoogleOAuth(g *PhoneOAuthGoogle) error {
	if g.ClientID != "" && !googleClient.MatchString(g.ClientID) {
		return fmt.Errorf("clientId must end with .apps.googleusercontent.com, got %q", g.ClientID)
	}
	return nil
}

func validateMicrosoftOAuth(m *PhoneOAuthMicrosoft) error {
	if m.TenantID != "" && !guidRE.MatchString(m.TenantID) && m.TenantID != "common" && m.TenantID != "organizations" && m.TenantID != "consumers" {
		return fmt.Errorf("tenantId must be a GUID or one of 'common'/'organizations'/'consumers', got %q", m.TenantID)
	}
	if m.ClientID != "" && !guidRE.MatchString(m.ClientID) {
		return fmt.Errorf("clientId must be a GUID, got %q", m.ClientID)
	}
	return nil
}

// ---- HTTP handlers ----

// handlePhoneOAuth serves GET + POST /phone/projects/oauth?slug=X. GET returns
// the full config (owner-auth only — secrets inside). POST merges a partial
// patch and returns the resulting config.
func (s *HTTPServer) handlePhoneOAuth(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		slug := r.URL.Query().Get("slug")
		if slug == "" {
			jsonError(w, http.StatusBadRequest, "slug required")
			return
		}
		cfg, err := LoadPhoneOAuth(slug)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "phone project not found") {
				status = http.StatusNotFound
			}
			jsonError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"config": cfg,
			"status": PhoneOAuthStatusFor(cfg),
		})
	case http.MethodPost:
		var body struct {
			Slug   string            `json:"slug"`
			Config *PhoneOAuthConfig `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		if body.Slug == "" || body.Config == nil {
			jsonError(w, http.StatusBadRequest, "slug and config required")
			return
		}
		cfg, err := SavePhoneOAuth(body.Slug, body.Config)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "phone project not found") {
				status = http.StatusNotFound
			}
			jsonError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"config": cfg,
			"status": PhoneOAuthStatusFor(cfg),
		})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}
