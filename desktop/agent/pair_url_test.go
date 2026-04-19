package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestBuildPairURL_DefaultsAndContents asserts the canonical URL is
// well-formed and carries the locator + metadata the mobile app needs.
// The trust anchor remains the code itself; the URL is just a
// convenience layer.
func TestBuildPairURL_DefaultsAndContents(t *testing.T) {
	session, err := StartPairingSession(10 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	defer EndPairingSession()

	got := buildPairURL(session, PairURLOptions{
		Mode:    "pair",
		Target:  "http://10.0.0.5:18080",
		BaseURL: "https://yaver.io/pair",
	})
	if got == "" {
		t.Fatal("buildPairURL returned empty")
	}
	if !strings.HasPrefix(got, "https://yaver.io/pair?") {
		t.Fatalf("expected canonical base, got %q", got)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := u.Query()
	if q.Get("sid") != session.Code {
		t.Errorf("sid: got %q want %q", q.Get("sid"), session.Code)
	}
	if q.Get("mode") != "pair" {
		t.Errorf("mode: got %q want pair", q.Get("mode"))
	}
	if q.Get("host") != session.Hostname {
		t.Errorf("host: got %q want %q", q.Get("host"), session.Hostname)
	}
	if q.Get("target") != "http://10.0.0.5:18080" {
		t.Errorf("target: got %q", q.Get("target"))
	}
	if q.Get("code") != session.Code {
		t.Errorf("code: got %q want %q", q.Get("code"), session.Code)
	}
	if q.Get("exp") == "" {
		t.Error("exp missing")
	}
}

// TestBuildPairURL_OmitCodeStripsSecret guards the wiki/share-by-text
// case — when the URL is being printed somewhere durable, the code
// shouldn't tag along.
func TestBuildPairURL_OmitCodeStripsSecret(t *testing.T) {
	session, err := StartPairingSession(time.Minute)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	defer EndPairingSession()
	got := buildPairURL(session, PairURLOptions{Mode: "pair", OmitCode: true})
	u, _ := url.Parse(got)
	if u.Query().Get("code") != "" {
		t.Errorf("expected code stripped, got %q", u.Query().Get("code"))
	}
	if u.Query().Get("sid") == "" {
		t.Error("sid should still be present even when code is omitted")
	}
}

// TestBuildPairURL_NilSessionReturnsEmpty — the helper must be
// safe to call before a pair session starts.
func TestBuildPairURL_NilSessionReturnsEmpty(t *testing.T) {
	if got := buildPairURL(nil, PairURLOptions{}); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// TestPairSessionEndpoint_ReturnsMetadata exercises the new
// /auth/pair/session endpoint. Lookup by sid, by code, and unkeyed
// (which is the active session).
func TestPairSessionEndpoint_ReturnsMetadata(t *testing.T) {
	session, err := StartPairingSession(10 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	defer EndPairingSession()
	srv := &HTTPServer{}

	// Unkeyed: should return the active session.
	req := httptest.NewRequest(http.MethodGet, "/auth/pair/session", nil)
	rec := httptest.NewRecorder()
	srv.handlePairSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unkeyed: status %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["sessionId"] != session.Code {
		t.Errorf("sessionId: got %v want %s", resp["sessionId"], session.Code)
	}
	if resp["canDirectSubmit"] != true {
		t.Errorf("canDirectSubmit: got %v want true", resp["canDirectSubmit"])
	}
	if resp["expiresAt"] == "" {
		t.Error("expiresAt missing")
	}

	// Keyed by sid (canonical).
	req = httptest.NewRequest(http.MethodGet, "/auth/pair/session?sid="+session.Code, nil)
	rec = httptest.NewRecorder()
	srv.handlePairSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("by sid: status %d body=%s", rec.Code, rec.Body.String())
	}

	// Keyed by code (back-compat).
	req = httptest.NewRequest(http.MethodGet, "/auth/pair/session?code="+session.Code, nil)
	rec = httptest.NewRecorder()
	srv.handlePairSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("by code: status %d body=%s", rec.Code, rec.Body.String())
	}

	// Wrong sid → 404 (not 200 — protects against confused locators).
	req = httptest.NewRequest(http.MethodGet, "/auth/pair/session?sid=ZZZZZZ", nil)
	rec = httptest.NewRecorder()
	srv.handlePairSession(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("wrong sid: status %d want 404", rec.Code)
	}
}

// TestPairSessionEndpoint_NoSession — when no pair session is open,
// the endpoint returns 404 just like /auth/pair/info already does.
func TestPairSessionEndpoint_NoSession(t *testing.T) {
	EndPairingSession() // make sure nothing is active
	req := httptest.NewRequest(http.MethodGet, "/auth/pair/session", nil)
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handlePairSession(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestPairSessionEndpoint_RejectsNonGET — same constraint as
// /auth/pair/info; never expand the verb surface accidentally.
func TestPairSessionEndpoint_RejectsNonGET(t *testing.T) {
	_, err := StartPairingSession(time.Minute)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	defer EndPairingSession()
	req := httptest.NewRequest(http.MethodPost, "/auth/pair/session", nil)
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handlePairSession(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d want 405", rec.Code)
	}
}

// TestPairSessionEndpoint_DoesNotReplaceInfo — guard against any
// regression that would lose the existing /auth/pair/info contract
// (host + expiresAt, no secrets). We verify both endpoints still work
// independently and shape-compatibly.
func TestPairSessionEndpoint_DoesNotReplaceInfo(t *testing.T) {
	session, err := StartPairingSession(time.Minute)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	defer EndPairingSession()
	srv := &HTTPServer{}

	// /auth/pair/info still works and never carries the secret.
	req := httptest.NewRequest(http.MethodGet, "/auth/pair/info", nil)
	rec := httptest.NewRecorder()
	srv.handlePairInfo(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("info status %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, session.Code) {
		t.Errorf("/auth/pair/info leaked code: %s", body)
	}
	if !strings.Contains(body, session.Hostname) {
		t.Errorf("/auth/pair/info missing host: %s", body)
	}

	// /auth/pair/session never returns the trust secret either.
	// (sessionId is a locator that for Slice A happens to equal
	//  the code, but that's still a deliberate "the URL the scanner
	//  already saw" — never invented or echoed for unauthenticated
	//  callers who didn't already know it.)
	req = httptest.NewRequest(http.MethodGet, "/auth/pair/session?sid="+session.Code, nil)
	rec = httptest.NewRecorder()
	srv.handlePairSession(rec, req)
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, forbidden := range []string{"token", "auth_token", "code"} {
		if _, present := resp[forbidden]; present {
			t.Errorf("/auth/pair/session leaked %q: %v", forbidden, resp)
		}
	}
}
