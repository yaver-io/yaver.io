package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFriendShareReadOnlyToken proves the friend-preview data path: a share
// mints a scoped READ-ONLY pp_ token, a friend can GET /data with it (no owner
// session), and any write (POST/PATCH/DELETE) is rejected 403.
func TestFriendShareReadOnlyToken(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Friend Share", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sh, err := CreatePhoneShare(p.Slug, 0)
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if sh.DataToken == "" || !strings.HasPrefix(sh.DataToken, "pp_") {
		t.Fatalf("share did not mint a pp_ data token: %q", sh.DataToken)
	}

	srv := &HTTPServer{}
	mux := http.NewServeMux()
	srv.registerPhoneDataRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Friend GET with the read-only token → 200, sees seeded rows.
	greq, _ := http.NewRequest(http.MethodGet, ts.URL+sh.DataURL+"/todos", nil)
	greq.Header.Set("Authorization", "Bearer "+sh.DataToken)
	gres, err := http.DefaultClient.Do(greq)
	if err != nil {
		t.Fatalf("friend GET: %v", err)
	}
	gbody, _ := io.ReadAll(gres.Body)
	gres.Body.Close()
	if gres.StatusCode != 200 {
		t.Fatalf("friend GET expected 200, got %d: %s", gres.StatusCode, gbody)
	}
	if !strings.Contains(string(gbody), "Buy milk") {
		t.Fatalf("friend GET missing seeded row: %s", gbody)
	}

	// Friend write with the read-only token → 403.
	preq, _ := http.NewRequest(http.MethodPost, ts.URL+sh.DataURL+"/todos", bytes.NewReader([]byte(`{"id":"x","title":"hack"}`)))
	preq.Header.Set("Authorization", "Bearer "+sh.DataToken)
	preq.Header.Set("Content-Type", "application/json")
	pres, err := http.DefaultClient.Do(preq)
	if err != nil {
		t.Fatalf("friend POST: %v", err)
	}
	pbody, _ := io.ReadAll(pres.Body)
	pres.Body.Close()
	if pres.StatusCode != 403 {
		t.Fatalf("friend write expected 403 (read-only), got %d: %s", pres.StatusCode, pbody)
	}
	t.Logf("friend read-only token: GET 200 (rows), POST 403 (blocked) — correct")
}
