package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestSandboxLiveHTTPDeployRemove runs the full AUTHENTICATED serverless
// lifecycle over real HTTP: a browser-built .yaver.tgz is POSTed to
// /phone/projects/receive on an httptest server wired with the REAL agent
// handlers + auth wrapper, the bearer is a REAL session token validated against
// REAL Convex (convexURL + ownerUserID), the hosted /data API is read back, and
// the project is removed via /phone/projects/delete. Proves the deploy→serve→
// remove transport+auth+data path end to end. No Hetzner, no always-on server.
//
// Inputs (created by the run harness): /tmp/sbx-acct.json {token,userId,site}
// and /tmp/sbx-export.tgz (from e2e/sandbox-deep-test.cjs).
func TestSandboxLiveHTTPDeployRemove(t *testing.T) {
	acctRaw, err := os.ReadFile("/tmp/sbx-acct.json")
	if err != nil {
		t.Skipf("no test account at /tmp/sbx-acct.json: %v", err)
	}
	bundle, err := os.ReadFile("/tmp/sbx-export.tgz")
	if err != nil {
		t.Skipf("no browser bundle at /tmp/sbx-export.tgz: %v", err)
	}
	var acct struct {
		Token  string `json:"token"`
		UserID string `json:"userId"`
		Site   string `json:"site"`
	}
	if err := json.Unmarshal(acctRaw, &acct); err != nil {
		t.Fatalf("parse acct: %v", err)
	}

	setupPhoneTestHome(t) // isolate ~/.yaver to a temp dir

	// Agent configured with a REAL Convex URL + the account's userId as owner.
	// The agent's own token is something else, so the incoming bearer can only
	// pass by being validated against Convex and matching ownerUserID — the
	// genuine authenticated owner path.
	srv := &HTTPServer{
		token:       "agent-local-not-the-bearer",
		convexURL:   acct.Site,
		ownerUserID: acct.UserID,
	}
	mux := http.NewServeMux()
	srv.registerPhoneRoutes(mux)
	srv.registerPhoneDataRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	bearer := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+acct.Token) }
	const slug = "sbx-live"

	// ── DEPLOY: POST the browser bundle to the serverless receive endpoint ──
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/phone/projects/receive?slug="+slug+"&onConflict=overwrite", bytes.NewReader(bundle))
	req.Header.Set("Content-Type", "application/octet-stream")
	bearer(req)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("deploy POST failed: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("deploy expected 200, got %d: %s", res.StatusCode, string(body))
	}
	t.Logf("DEPLOY ok (authenticated as real Convex user %s): %s", acct.UserID, strings.TrimSpace(string(body)))

	// ── SERVE: read the hosted data API back ────────────────────────────────
	dreq, _ := http.NewRequest(http.MethodGet, ts.URL+"/data/"+slug+"/todos", nil)
	bearer(dreq)
	dres, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatalf("data GET failed: %v", err)
	}
	dbody, _ := io.ReadAll(dres.Body)
	dres.Body.Close()
	if dres.StatusCode != 200 {
		t.Fatalf("data GET expected 200, got %d: %s", dres.StatusCode, string(dbody))
	}
	if !strings.Contains(string(dbody), "deep1") {
		t.Fatalf("hosted data missing the browser-inserted row 'deep1': %s", string(dbody))
	}
	t.Logf("SERVE ok — hosted /data/%s/todos returned live rows incl. 'deep1'", slug)

	// ── REMOVE: delete the deployed project ─────────────────────────────────
	xreq, _ := http.NewRequest(http.MethodPost, ts.URL+"/phone/projects/delete?slug="+slug, nil)
	bearer(xreq)
	xres, err := http.DefaultClient.Do(xreq)
	if err != nil {
		t.Fatalf("delete POST failed: %v", err)
	}
	xbody, _ := io.ReadAll(xres.Body)
	xres.Body.Close()
	if xres.StatusCode != 200 {
		t.Fatalf("delete expected 200, got %d: %s", xres.StatusCode, string(xbody))
	}

	// ── VERIFY GONE: data API now 404s ──────────────────────────────────────
	greq, _ := http.NewRequest(http.MethodGet, ts.URL+"/data/"+slug+"/todos", nil)
	bearer(greq)
	gres, _ := http.DefaultClient.Do(greq)
	gbody, _ := io.ReadAll(gres.Body)
	gres.Body.Close()
	if gres.StatusCode == 200 {
		t.Fatalf("project still served after delete: %s", string(gbody))
	}
	t.Logf("REMOVE ok — /data/%s now %d, project gone", slug, gres.StatusCode)
}
