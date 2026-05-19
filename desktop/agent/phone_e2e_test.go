package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFullSandboxLoop_E2E drives the ENTIRE normie loop through the
// real HTTP handlers (no mocks, real archive + SQLite, repo test
// convention): create → export(.zip + AGENTS.md) → receive over SSE on
// a simulated hosted box → share → join → zero-BYOK selfhosted deploy
// script. This is the integrated proof the per-layer unit tests don't
// give on their own; it permanently guards the chain.
func TestFullSandboxLoop_E2E(t *testing.T) {
	setupPhoneTestHome(t)
	srv := &HTTPServer{token: "t"}

	// Simulate a hosted-tier box: the cred file Phase-1 cloud-init
	// would have written. Drives the Phase-3 bundle env, the SSE
	// "hosted" event, the share's hostedConvexUrl.
	credDir := t.TempDir()
	credFile := filepath.Join(credDir, "convex-selfhosted.json")
	const hostedURL = "https://box-e2e.cloud.yaver.io/_convex-api"
	if err := os.WriteFile(credFile,
		[]byte(`{"url":"`+hostedURL+`","adminKey":"E2E-ADMIN-KEY-SECRET"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONVEX_SELFHOSTED_FILE", credFile)

	auth := func(r *http.Request) *http.Request {
		r.Header.Set("Authorization", "Bearer t")
		return r
	}

	// 1. Create the project (todos template).
	w := httptest.NewRecorder()
	srv.handlePhoneCreate(w, auth(httptest.NewRequest("POST", "/phone/projects/create",
		strings.NewReader(`{"name":"E2E Loop","template":"todos"}`))))
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created PhoneProject
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("create decode: %v", err)
	}
	slug := created.Slug

	// 2. Export as .zip (the coding-agent / OS-friendly twin).
	w = httptest.NewRecorder()
	srv.handlePhoneExport(w, auth(httptest.NewRequest("GET",
		"/phone/projects/export?slug="+slug+"&format=zip&includeData=1", nil)))
	if w.Code != 200 {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("export Content-Type = %q, want application/zip", ct)
	}
	zipBytes := w.Body.Bytes()
	if bundleFormat(zipBytes) != "zip" {
		t.Fatalf("export did not produce a zip (magic=%v)", zipBytes[:4])
	}
	zf := readZip(t, zipBytes)
	if _, ok := zf[filepath.ToSlash(filepath.Join(slug, "AGENTS.md"))]; !ok {
		t.Fatalf("export .zip missing AGENTS.md (have %v)", keysOf(zf))
	}

	// 3. Receive the .zip back over the SSE stream (as a hosted box).
	w = httptest.NewRecorder()
	rr := httptest.NewRequest("POST",
		"/phone/projects/receive?stream=1&slug=e2e-clone&onConflict=rename",
		bytes.NewReader(zipBytes))
	rr.Header.Set("Content-Type", "application/zip")
	srv.handlePhoneReceive(w, auth(rr))
	if w.Code != 200 {
		t.Fatalf("receive: %d %s", w.Code, w.Body.String())
	}
	seen := map[string]bool{}
	var hostedConvexInStream string
	sc := bufio.NewScanner(bytes.NewReader(w.Body.Bytes()))
	for sc.Scan() {
		line := strings.TrimPrefix(sc.Text(), "data: ")
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev map[string]interface{}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		typ, _ := ev["type"].(string)
		seen[typ] = true
		if typ == "hosted" {
			hostedConvexInStream, _ = ev["convexUrl"].(string)
		}
		if typ == "error" {
			t.Fatalf("receive stream error event: %v", ev["error"])
		}
	}
	for _, want := range []string{"received", "unpacking", "materialized", "hosted", "ready"} {
		if !seen[want] {
			t.Errorf("SSE stream missing %q event (saw %v)", want, seen)
		}
	}
	if hostedConvexInStream != hostedURL {
		t.Errorf("hosted event convexUrl = %q, want %q", hostedConvexInStream, hostedURL)
	}
	// The admin key must NEVER appear anywhere in the stream.
	if strings.Contains(w.Body.String(), "E2E-ADMIN-KEY-SECRET") {
		t.Fatal("admin key leaked into the receive SSE stream")
	}

	// 4. Share the original project → join code carries the host URL.
	w = httptest.NewRecorder()
	srv.handlePhoneShare(w, auth(httptest.NewRequest("POST", "/phone/projects/share",
		strings.NewReader(`{"slug":"`+slug+`"}`))))
	if w.Code != 200 {
		t.Fatalf("share: %d %s", w.Code, w.Body.String())
	}
	var sh PhoneShare
	if err := json.Unmarshal(w.Body.Bytes(), &sh); err != nil {
		t.Fatalf("share decode: %v", err)
	}
	if sh.Code == "" || sh.HostedConvexURL != hostedURL {
		t.Fatalf("bad share: %+v", sh)
	}

	// 5. A friend joins with the code.
	w = httptest.NewRecorder()
	srv.handlePhoneJoin(w, auth(httptest.NewRequest("GET",
		"/phone/projects/join?code="+sh.Code, nil)))
	if w.Code != 200 {
		t.Fatalf("join: %d %s", w.Code, w.Body.String())
	}
	var joined PhoneShare
	if err := json.Unmarshal(w.Body.Bytes(), &joined); err != nil {
		t.Fatalf("join decode: %v", err)
	}
	if joined.Slug != slug || joined.HostedConvexURL != hostedURL || joined.BundleURL == "" {
		t.Fatalf("join resolved wrong: %+v", joined)
	}

	// 6. The zero-BYOK selfhosted deploy script ties it together.
	script, err := GenerateDeployScript(DeployScriptSpec{
		App: slug, Stack: "convex", Target: "selfhosted", Path: "/srv/yaver/workspace",
	})
	if err != nil {
		t.Fatalf("selfhosted deploy script: %v", err)
	}
	for _, want := range []string{"/etc/yaver/convex-selfhosted.json", "npx convex deploy --yes"} {
		if !strings.Contains(script, want) {
			t.Errorf("deploy script missing %q", want)
		}
	}
	if strings.Contains(script, "CONVEX_DEPLOY_KEY") {
		t.Error("selfhosted deploy must be zero-BYOK (no CONVEX_DEPLOY_KEY)")
	}
}
