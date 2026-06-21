package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"testing"
)

// TestServeLocalServerlessTarget runs a REAL Yaver Serverless target (the actual
// agent phone + data handlers, real auth wrapper, real CORS) on :18099 for the
// zero-to-hero demo/video. Gated by YAVER_LOCAL_TARGET so normal test runs skip
// it. It blocks until killed. Reads the owner identity from /tmp/sbx-acct.json
// so the deploy/share bearer validates as owner against real Convex.
func TestServeLocalServerlessTarget(t *testing.T) {
	if os.Getenv("YAVER_LOCAL_TARGET") == "" {
		t.Skip("set YAVER_LOCAL_TARGET=1 to run the demo target")
	}
	raw, err := os.ReadFile("/tmp/sbx-acct.json")
	if err != nil {
		t.Fatalf("need /tmp/sbx-acct.json: %v", err)
	}
	var acct struct{ Token, UserID, Site string }
	if err := json.Unmarshal(raw, &acct); err != nil {
		t.Fatalf("parse acct: %v", err)
	}

	// Persistent HOME so the deployed project survives for the server's life.
	home := "/tmp/yaver-target-home"
	_ = os.MkdirAll(home, 0o700)
	os.Setenv("HOME", home)

	srv := &HTTPServer{token: "local-target-agent", convexURL: acct.Site, ownerUserID: acct.UserID}
	mux := http.NewServeMux()
	srv.registerPhoneRoutes(mux)
	srv.registerPhoneDataRoutes(mux)

	log.Printf("LOCAL SERVERLESS TARGET listening on :18099 (owner %s)", acct.UserID)
	if err := http.ListenAndServe(":18099", withCORS(mux)); err != nil {
		t.Fatalf("listen: %v", err)
	}
}
