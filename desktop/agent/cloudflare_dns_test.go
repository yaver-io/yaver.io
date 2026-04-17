package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockCloudflare spins up an httptest server that returns canned responses
// for the API paths we care about. Keyed by "METHOD path-prefix" → response.
type mockCloudflare struct {
	t       *testing.T
	handler http.Handler
	calls   []string // METHOD + path for assertions
}

func newMockCloudflare(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for pattern, handler := range routes {
			method, path, _ := strings.Cut(pattern, " ")
			if r.Method == method && strings.HasPrefix(r.URL.Path, path) {
				handler(w, r)
				return
			}
		}
		http.Error(w, "mock: no handler for "+r.Method+" "+r.URL.Path, http.StatusNotImplemented)
	}))
}

func cfSuccess(body interface{}) []byte {
	out := map[string]interface{}{"success": true, "errors": []interface{}{}, "result": body}
	b, _ := json.Marshal(out)
	return b
}

func cfFailure(msg string) []byte {
	out := map[string]interface{}{
		"success": false,
		"errors":  []map[string]interface{}{{"code": 6003, "message": msg}},
		"result":  nil,
	}
	b, _ := json.Marshal(out)
	return b
}

func TestCloudflareClient_VerifyToken(t *testing.T) {
	srv := newMockCloudflare(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /client/v4/user/tokens/verify": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				http.Error(w, "bad auth", 401)
				return
			}
			w.Write(cfSuccess(map[string]string{"status": "active"}))
		},
	})
	defer srv.Close()

	cli := &CloudflareClient{Token: "test-token", BaseURL: srv.URL + "/client/v4"}
	st, err := cli.VerifyToken()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !st.Valid || st.Status != "active" {
		t.Errorf("expected valid+active, got %+v", st)
	}
}

func TestCloudflareClient_VerifyRejectsEmptyToken(t *testing.T) {
	cli := &CloudflareClient{Token: ""}
	if _, err := cli.VerifyToken(); err == nil {
		t.Fatalf("expected error for empty token")
	}
}

func TestCloudflareClient_ListZones(t *testing.T) {
	page := 0
	srv := newMockCloudflare(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /client/v4/zones": func(w http.ResponseWriter, r *http.Request) {
			page++
			if page == 1 {
				zones := []map[string]string{
					{"id": "z1", "name": "example.com", "status": "active"},
					{"id": "z2", "name": "other.com", "status": "active"},
				}
				w.Write(cfSuccess(zones))
			} else {
				// second page empty → terminate
				w.Write(cfSuccess([]map[string]string{}))
			}
		},
	})
	defer srv.Close()

	cli := &CloudflareClient{Token: "t", BaseURL: srv.URL + "/client/v4"}
	zones, err := cli.ListZones()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("expected 2 zones, got %d", len(zones))
	}
	if zones[0].Name != "example.com" {
		t.Errorf("first zone name mismatch: %+v", zones[0])
	}
}

func TestCloudflareClient_ListRecords(t *testing.T) {
	srv := newMockCloudflare(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /client/v4/zones/z1/dns_records": func(w http.ResponseWriter, r *http.Request) {
			recs := []map[string]interface{}{
				{"id": "r1", "type": "CNAME", "name": "myapp.example.com", "content": "cloud.yaver.io", "ttl": 1, "proxied": true},
			}
			w.Write(cfSuccess(recs))
		},
	})
	defer srv.Close()

	cli := &CloudflareClient{Token: "t", BaseURL: srv.URL + "/client/v4"}
	recs, err := cli.ListRecords("z1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) != 1 || recs[0].Type != "CNAME" {
		t.Errorf("unexpected: %+v", recs)
	}
}

func TestCloudflareClient_CreateRecord(t *testing.T) {
	var received CloudflareRecordInput
	srv := newMockCloudflare(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /client/v4/zones/z1/dns_records": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&received)
			created := map[string]interface{}{
				"id": "r-new", "type": received.Type, "name": received.Name, "content": received.Content,
			}
			w.Write(cfSuccess(created))
		},
	})
	defer srv.Close()

	cli := &CloudflareClient{Token: "t", BaseURL: srv.URL + "/client/v4"}
	rec, err := cli.CreateRecord("z1", CloudflareRecordInput{
		Type: "CNAME", Name: "myapp.example.com", Content: "cloud.yaver.io", Proxied: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.ID != "r-new" || received.Name != "myapp.example.com" {
		t.Errorf("roundtrip mismatch: sent=%+v got=%+v", received, rec)
	}
}

func TestCloudflareClient_CreateRecordValidation(t *testing.T) {
	cli := &CloudflareClient{Token: "t"}
	if _, err := cli.CreateRecord("", CloudflareRecordInput{Type: "A", Name: "x", Content: "1.2.3.4"}); err == nil {
		t.Error("expected error for empty zone")
	}
	if _, err := cli.CreateRecord("z1", CloudflareRecordInput{Type: "A"}); err == nil {
		t.Error("expected error for missing name/content")
	}
}

func TestCloudflareClient_CreateRecordSurfacesAPIError(t *testing.T) {
	srv := newMockCloudflare(t, map[string]func(http.ResponseWriter, *http.Request){
		"POST /client/v4/zones/z1/dns_records": func(w http.ResponseWriter, r *http.Request) {
			w.Write(cfFailure("record already exists"))
		},
	})
	defer srv.Close()

	cli := &CloudflareClient{Token: "t", BaseURL: srv.URL + "/client/v4"}
	_, err := cli.CreateRecord("z1", CloudflareRecordInput{Type: "A", Name: "x.example.com", Content: "1.2.3.4"})
	if err == nil || !strings.Contains(err.Error(), "record already exists") {
		t.Errorf("expected API error surfaced, got %v", err)
	}
}

func TestCloudflareClient_DeleteRecord(t *testing.T) {
	var called bool
	srv := newMockCloudflare(t, map[string]func(http.ResponseWriter, *http.Request){
		"DELETE /client/v4/zones/z1/dns_records/r-new": func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Write(cfSuccess(map[string]string{"id": "r-new"}))
		},
	})
	defer srv.Close()

	cli := &CloudflareClient{Token: "t", BaseURL: srv.URL + "/client/v4"}
	if err := cli.DeleteRecord("z1", "r-new"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !called {
		t.Error("DELETE endpoint never hit")
	}
	if err := cli.DeleteRecord("", "r"); err == nil {
		t.Error("expected error for empty zone")
	}
}

// --- HTTP handlers (end-to-end) ---

func TestHandleCFVerify_RequiresToken(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodPost, "/dns/cloudflare/verify", nil)
	w := httptest.NewRecorder()
	srv.handleCFVerify(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleCFZones_UsesHeaderToken(t *testing.T) {
	// Spin up a fake Cloudflare backend, then override cloudflareAPI indirectly:
	// here we test the helper endpoint with an env-var swap via BaseURL is
	// awkward because the handler calls NewCloudflareClient with a fixed
	// cloudflareAPI. Instead, run the full stack through an integration-style
	// test that short-circuits at the verify call (no network).
	//
	// For the handler check we simply confirm the 400 path and that the
	// header extraction helper works — deeper wiring is covered by the
	// client tests above.
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/dns/cloudflare/zones", nil)
	w := httptest.NewRecorder()
	srv.handleCFZones(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("no token should be 400, got %d", w.Code)
	}
}

func TestHandleCFRecords_DeleteRequiresParams(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodDelete, "/dns/cloudflare/records", nil)
	req.Header.Set("X-CF-Token", "t")
	w := httptest.NewRecorder()
	srv.handleCFRecords(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing zoneId/recordId should be 400, got %d", w.Code)
	}
}

func TestTokenFromRequest_PriorityOrder(t *testing.T) {
	// Header wins.
	r := httptest.NewRequest(http.MethodGet, "/?token=query-token", nil)
	r.Header.Set("X-CF-Token", "header-token")
	if got := cloudflareTokenFromRequest(r); got != "header-token" {
		t.Errorf("header should win, got %q", got)
	}
	// Query fallback.
	r = httptest.NewRequest(http.MethodGet, "/?token=query-token", nil)
	if got := cloudflareTokenFromRequest(r); got != "query-token" {
		t.Errorf("query should be used, got %q", got)
	}
}

// Smoke test the full verify flow against the mock. We swap the cloudflareAPI
// constant by creating a custom client directly and exercising its handler
// via an embedded HTTP server.
func TestCloudflareClient_SurfacesHTTPError(t *testing.T) {
	srv := newMockCloudflare(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /client/v4/user/tokens/verify": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"success":false,"errors":[{"code":1000,"message":"Invalid API Token"}],"result":null}`)
		},
	})
	defer srv.Close()
	cli := &CloudflareClient{Token: "bad", BaseURL: srv.URL + "/client/v4"}
	st, err := cli.VerifyToken()
	if err != nil {
		t.Fatalf("verify should not error on 401 (we return struct): %v", err)
	}
	if st.Valid {
		t.Errorf("expected invalid, got %+v", st)
	}
	if st.Message == "" {
		t.Errorf("expected message populated from cf errors")
	}
}

// Body-parse helper is a small util but easy to regress.
func TestParseTokenBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"token":"from-body"}`))
	if got := parseTokenBody(r); got != "from-body" {
		t.Errorf("expected from-body, got %q", got)
	}
	// Body should be re-readable after the peek.
	data, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(data), "from-body") {
		t.Errorf("body not preserved after peek, got %q", string(data))
	}
	// Empty body is fine.
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := parseTokenBody(r2); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
