package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// playClient is constructed directly with a fake token so the test exercises
// the edits/track/testers logic without the OAuth exchange (covered by the
// Store Studio's buildGoogleJWTGrant tests).
func TestPlayPromoteAndTesters(t *testing.T) {
	committed := false
	var setGroups []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("bad auth: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case r.Method == "POST" && strings.HasSuffix(p, "/edits"):
			w.Write([]byte(`{"id":"edit-1"}`))
		case r.Method == "GET" && strings.Contains(p, "/tracks/internal"):
			w.Write([]byte(`{"track":"internal","releases":[{"versionCodes":["267"],"status":"draft"}]}`))
		case r.Method == "PUT" && strings.Contains(p, "/tracks/internal"):
			var tr PlayTrack
			json.NewDecoder(r.Body).Decode(&tr)
			if len(tr.Releases) == 0 || tr.Releases[0].Status != "completed" {
				t.Errorf("expected completed status, got %+v", tr.Releases)
			}
			b, _ := json.Marshal(tr)
			w.Write(b)
		case r.Method == "GET" && strings.Contains(p, "/testers/internal"):
			w.Write([]byte(`{"googleGroups":["existing@acme.com"]}`))
		case r.Method == "PUT" && strings.Contains(p, "/testers/internal"):
			var body struct {
				GoogleGroups []string `json:"googleGroups"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			setGroups = body.GoogleGroups
			gj, _ := json.Marshal(body.GoogleGroups)
			w.Write([]byte(`{"googleGroups":` + string(gj) + `}`))
		case r.Method == "POST" && strings.HasSuffix(p, ":commit"):
			committed = true
			w.Write([]byte(`{"id":"edit-1"}`))
		case r.Method == "DELETE":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected %s %s", r.Method, p)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	old := playAPIBase
	playAPIBase = srv.URL
	defer func() { playAPIBase = old }()

	cl := &playClient{pkg: "com.acme.app", token: "test-token", http: srv.Client()}

	// promote draft -> completed
	tr, err := cl.PromoteRelease("internal", "completed", 0)
	if err != nil {
		t.Fatalf("PromoteRelease: %v", err)
	}
	if tr.Releases[0].Status != "completed" {
		t.Fatalf("status = %q", tr.Releases[0].Status)
	}
	if !committed {
		t.Fatal("edit was not committed")
	}

	// bind a google group additively
	committed = false
	cur, err := cl.GetTesters("internal")
	if err != nil {
		t.Fatalf("GetTesters: %v", err)
	}
	merged := mergeUnique(cur.GoogleGroups, "newteam@acme.com")
	if _, err := cl.SetTesters("internal", merged); err != nil {
		t.Fatalf("SetTesters: %v", err)
	}
	if len(setGroups) != 2 {
		t.Fatalf("expected 2 groups, got %v", setGroups)
	}
	if !committed {
		t.Fatal("testers edit not committed")
	}
}
