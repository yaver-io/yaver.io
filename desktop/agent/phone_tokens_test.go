package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMintPhoneProjectToken_RoundTrip(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "tokens-rt"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	mint, err := MintPhoneProjectToken("tokens-rt", "web client")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !strings.HasPrefix(mint.Raw, "pp_tokens-rt_") {
		t.Errorf("raw should start with pp_<slug>_, got %q", mint.Raw)
	}
	if mint.Token.Hash != "" {
		t.Errorf("Hash must NOT be in the wire response (was %q)", mint.Token.Hash)
	}
	if mint.Token.ID == "" || mint.Token.Label != "web client" {
		t.Errorf("unexpected token summary: %+v", mint.Token)
	}

	// List returns the token with no hash / raw.
	list, err := ListPhoneProjectTokens("tokens-rt")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 token, got %d", len(list))
	}
	if list[0].Hash != "" {
		t.Errorf("list leaked hash")
	}
}

func TestMintPhoneProjectToken_MissingProject(t *testing.T) {
	setupPhoneTestHome(t)
	_, err := MintPhoneProjectToken("never-existed", "x")
	if err == nil {
		t.Fatal("expected error for missing project")
	}
	if !strings.Contains(err.Error(), "phone project not found") {
		t.Errorf("expected not-found, got %v", err)
	}
}

func TestValidatePhoneProjectToken_Success(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "tokens-validate"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	mint, err := MintPhoneProjectToken("tokens-validate", "app")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	got, slug, err := ValidatePhoneProjectToken(mint.Raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if slug != "tokens-validate" {
		t.Errorf("slug mismatch: %q", slug)
	}
	if got.ID != mint.Token.ID {
		t.Errorf("id mismatch")
	}
}

func TestValidatePhoneProjectToken_WrongShape(t *testing.T) {
	setupPhoneTestHome(t)
	for _, bad := range []string{"", "bearer abc", "pp_onlyoneunderscore", "random"} {
		_, _, err := ValidatePhoneProjectToken(bad)
		if !errors.Is(err, ErrInvalidPhoneProjectToken) {
			t.Errorf("expected ErrInvalidPhoneProjectToken for %q, got %v", bad, err)
		}
	}
}

func TestValidatePhoneProjectToken_RevokedRejected(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "revoke-test"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	mint, _ := MintPhoneProjectToken("revoke-test", "tmp")
	if err := RevokePhoneProjectToken("revoke-test", mint.Token.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := ValidatePhoneProjectToken(mint.Raw); !errors.Is(err, ErrInvalidPhoneProjectToken) {
		t.Errorf("revoked token should be invalid, got %v", err)
	}
	// Revoking a missing id errors.
	if err := RevokePhoneProjectToken("revoke-test", "nonexistent"); err == nil {
		t.Error("expected error revoking unknown id")
	}
}

func TestValidatePhoneProjectToken_WrongSlugSameHash(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "a"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "b"}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	mintA, _ := MintPhoneProjectToken("a", "")
	// A token minted for 'a' must not authenticate against 'b' even if we
	// rewrite the embedded slug — the stored hash for 'b' is different.
	forged := strings.Replace(mintA.Raw, "pp_a_", "pp_b_", 1)
	if _, _, err := ValidatePhoneProjectToken(forged); !errors.Is(err, ErrInvalidPhoneProjectToken) {
		t.Errorf("forged cross-project token should be rejected, got %v", err)
	}
}

// ---- HTTP handler contract ----

func TestHandlePhoneTokens_PostThenGetThenDelete(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "httpflow"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	srv := &HTTPServer{}

	// POST mint
	body, _ := json.Marshal(map[string]string{"slug": "httpflow", "label": "ios-app"})
	req := httptest.NewRequest(http.MethodPost, "/phone/projects/tokens", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePhoneTokens(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mint: %d %s", w.Code, w.Body.String())
	}
	var minted PhoneProjectTokenMint
	_ = json.Unmarshal(w.Body.Bytes(), &minted)
	if minted.Raw == "" || minted.Token.ID == "" {
		t.Fatalf("unexpected mint payload: %+v", minted)
	}

	// GET list
	req = httptest.NewRequest(http.MethodGet, "/phone/projects/tokens?slug=httpflow", nil)
	w = httptest.NewRecorder()
	srv.handlePhoneTokens(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var listed struct {
		Tokens []PhoneProjectToken `json:"tokens"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listed)
	if len(listed.Tokens) != 1 || listed.Tokens[0].Hash != "" {
		t.Errorf("list response should show 1 token with empty hash: %+v", listed.Tokens)
	}

	// DELETE revoke
	req = httptest.NewRequest(http.MethodDelete,
		"/phone/projects/tokens?slug=httpflow&tokenId="+minted.Token.ID, nil)
	w = httptest.NewRecorder()
	srv.handlePhoneTokens(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}

	// Token should no longer validate.
	if _, _, err := ValidatePhoneProjectToken(minted.Raw); !errors.Is(err, ErrInvalidPhoneProjectToken) {
		t.Errorf("revoked token should be invalid, got %v", err)
	}
}

func TestHandlePhoneTokens_PostMissingSlug(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodPost, "/phone/projects/tokens",
		strings.NewReader(`{"label":"x"}`))
	w := httptest.NewRecorder()
	srv.handlePhoneTokens(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 without slug, got %d", w.Code)
	}
}
