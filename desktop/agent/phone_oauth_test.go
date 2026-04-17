package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPhoneOAuth_MissingFileReturnsEmpty(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "oauth-empty"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	cfg, err := LoadPhoneOAuth("oauth-empty")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Apple != nil || cfg.Google != nil || cfg.Microsoft != nil {
		t.Errorf("expected all nil, got %+v", cfg)
	}
}

func TestLoadPhoneOAuth_MissingProject(t *testing.T) {
	setupPhoneTestHome(t)
	_, err := LoadPhoneOAuth("never-existed")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "phone project not found") {
		t.Errorf("expected not-found, got %v", err)
	}
}

func TestSavePhoneOAuth_RoundTrip(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "oauth-roundtrip"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	patch := &PhoneOAuthConfig{
		Apple: &PhoneOAuthApple{
			TeamID:     "AB12CD34EF",
			ServicesID: "com.example.signin",
			KeyID:      "GH56IJ78KL",
			PrivateKey: "-----BEGIN PRIVATE KEY-----\nMIG...\n-----END PRIVATE KEY-----",
		},
		Google: &PhoneOAuthGoogle{
			ClientID:     "12345-abc.apps.googleusercontent.com",
			ClientSecret: "GOCSPX-secret-here",
		},
		Microsoft: &PhoneOAuthMicrosoft{
			TenantID:     "12345678-1234-1234-1234-123456789012",
			ClientID:     "87654321-4321-4321-4321-210987654321",
			ClientSecret: "azure-secret",
		},
	}

	saved, err := SavePhoneOAuth("oauth-roundtrip", patch)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Apple.TeamID != "AB12CD34EF" {
		t.Errorf("apple teamID not persisted")
	}

	reloaded, err := LoadPhoneOAuth("oauth-roundtrip")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Apple.TeamID != "AB12CD34EF" ||
		reloaded.Google.ClientID != "12345-abc.apps.googleusercontent.com" ||
		reloaded.Microsoft.TenantID != "12345678-1234-1234-1234-123456789012" {
		t.Errorf("reload mismatch: %+v", reloaded)
	}

	// Perms on disk must be 0600 — secrets inside.
	dir, _ := PhoneProjectDir("oauth-roundtrip")
	info, err := os.Stat(filepath.Join(dir, "oauth-providers.yaml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600, got %o", info.Mode().Perm())
	}
}

func TestSavePhoneOAuth_PartialPatchPreservesOtherProviders(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "oauth-partial"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Seed apple first.
	if _, err := SavePhoneOAuth("oauth-partial", &PhoneOAuthConfig{
		Apple: &PhoneOAuthApple{TeamID: "AB12CD34EF", ServicesID: "com.example", KeyID: "GH56IJ78KL"},
	}); err != nil {
		t.Fatalf("save apple: %v", err)
	}
	// Now patch just google — apple must survive.
	cfg, err := SavePhoneOAuth("oauth-partial", &PhoneOAuthConfig{
		Google: &PhoneOAuthGoogle{ClientID: "x.apps.googleusercontent.com", ClientSecret: "s"},
	})
	if err != nil {
		t.Fatalf("save google: %v", err)
	}
	if cfg.Apple == nil || cfg.Apple.TeamID != "AB12CD34EF" {
		t.Errorf("partial patch wiped apple: %+v", cfg.Apple)
	}
	if cfg.Google == nil || cfg.Google.ClientID == "" {
		t.Errorf("partial patch didn't apply google")
	}
}

func TestValidateAppleOAuth_Rejects(t *testing.T) {
	cases := []struct {
		name string
		a    PhoneOAuthApple
		msg  string
	}{
		{"bad team id", PhoneOAuthApple{TeamID: "notvalid"}, "teamId"},
		{"bad key id", PhoneOAuthApple{KeyID: "12345"}, "keyId"},
		{"bad private key", PhoneOAuthApple{PrivateKey: "junk"}, "PEM"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateAppleOAuth(&c.a); err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Errorf("expected %q in error, got %v", c.msg, err)
			}
		})
	}
}

func TestValidateGoogleOAuth_Rejects(t *testing.T) {
	err := validateGoogleOAuth(&PhoneOAuthGoogle{ClientID: "not-google-shaped"})
	if err == nil || !strings.Contains(err.Error(), "googleusercontent") {
		t.Errorf("expected googleusercontent error, got %v", err)
	}
}

func TestValidateMicrosoftOAuth_AllowsCommonTenant(t *testing.T) {
	if err := validateMicrosoftOAuth(&PhoneOAuthMicrosoft{TenantID: "common"}); err != nil {
		t.Errorf("'common' tenant should be allowed, got %v", err)
	}
	if err := validateMicrosoftOAuth(&PhoneOAuthMicrosoft{TenantID: "not-a-guid-not-common"}); err == nil {
		t.Errorf("expected error for bad tenant")
	}
}

func TestPhoneOAuthStatusFor(t *testing.T) {
	st := PhoneOAuthStatusFor(nil)
	if st.Apple || st.Google || st.Microsoft {
		t.Errorf("nil config should yield all-false")
	}

	partial := &PhoneOAuthConfig{
		Apple:  &PhoneOAuthApple{TeamID: "AB12CD34EF"},                            // missing servicesId+keyId+privateKey
		Google: &PhoneOAuthGoogle{ClientID: "x.apps.googleusercontent.com"},       // missing secret
		Microsoft: &PhoneOAuthMicrosoft{TenantID: "common", ClientID: "11111111-1111-1111-1111-111111111111", ClientSecret: "s"},
	}
	st = PhoneOAuthStatusFor(partial)
	if st.Apple {
		t.Errorf("apple partial should report not-ready")
	}
	if st.Google {
		t.Errorf("google missing secret should report not-ready")
	}
	if !st.Microsoft {
		t.Errorf("microsoft fully populated should report ready")
	}
}

func TestHandlePhoneOAuth_GetPost(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "oauth-http"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	srv := &HTTPServer{}

	// GET empty
	req := httptest.NewRequest(http.MethodGet, "/phone/projects/oauth?slug=oauth-http", nil)
	w := httptest.NewRecorder()
	srv.handlePhoneOAuth(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET empty: %d %s", w.Code, w.Body.String())
	}

	// POST apple config
	payload := map[string]interface{}{
		"slug": "oauth-http",
		"config": map[string]interface{}{
			"apple": map[string]interface{}{
				"teamId":     "AB12CD34EF",
				"servicesId": "com.example.signin",
				"keyId":      "GH56IJ78KL",
				"privateKey": "-----BEGIN PRIVATE KEY-----\n-----END PRIVATE KEY-----",
			},
		},
	}
	body, _ := json.Marshal(payload)
	req = httptest.NewRequest(http.MethodPost, "/phone/projects/oauth", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handlePhoneOAuth(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Status PhoneOAuthStatus `json:"status"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !out.Status.Apple {
		t.Errorf("expected apple configured, got %+v", out.Status)
	}

	// POST bad apple teamID
	bad := map[string]interface{}{
		"slug":   "oauth-http",
		"config": map[string]interface{}{"apple": map[string]interface{}{"teamId": "not-valid"}},
	}
	body, _ = json.Marshal(bad)
	req = httptest.NewRequest(http.MethodPost, "/phone/projects/oauth", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handlePhoneOAuth(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad teamId, got %d", w.Code)
	}
}

func TestExportIncludesOAuthProviders(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "oauth-export"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := SavePhoneOAuth("oauth-export", &PhoneOAuthConfig{
		Google: &PhoneOAuthGoogle{
			ClientID: "12345.apps.googleusercontent.com", ClientSecret: "secret",
		},
	}); err != nil {
		t.Fatalf("save oauth: %v", err)
	}
	data, err := ExportPhoneProject("oauth-export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	names, err := readTarNames(data)
	if err != nil {
		t.Fatalf("read tar: %v", err)
	}
	found := false
	for _, n := range names {
		if strings.HasSuffix(n, "oauth-providers.yaml") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected oauth-providers.yaml in tarball, got %v", names)
	}
}
