package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The whole point of this provisioner is that it NEVER spends money
// by accident. These tests assert every fail-closed gate, with no
// network and no Robot creds.
func TestRobotProvisionerRegistered(t *testing.T) {
	if _, ok := provisionerRegistry()[HostHetznerRobot]; !ok {
		t.Fatal("HostHetznerRobot must be in provisionerRegistry")
	}
}

func TestRobotBlockedWithoutCreds(t *testing.T) {
	t.Setenv("HROBOT_USER", "")
	t.Setenv("HROBOT_PASS", "")
	res, err := provisionHetznerRobot("box", map[string]string{"confirmPaidOrder": "true", "live": "true"})
	if err != nil {
		t.Fatalf("missing creds must be a soft Manual, not error: %v", err)
	}
	if res.OK || res.Manual == "" || !strings.Contains(res.Manual, "HROBOT_USER") {
		t.Fatalf("missing creds must return actionable Manual, got %+v", res)
	}
}

func TestRobotBlockedWithoutConfirm(t *testing.T) {
	t.Setenv("HROBOT_USER", "u")
	t.Setenv("HROBOT_PASS", "p")
	res, err := provisionHetznerRobot("box", map[string]string{}) // no confirmPaidOrder
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK || !strings.Contains(res.Manual, "confirmPaidOrder=true") {
		t.Fatalf("must refuse paid order without confirm, got %+v", res)
	}
}

func TestRobotConfirmedIsDryRunByDefault(t *testing.T) {
	t.Setenv("HROBOT_USER", "u")
	t.Setenv("HROBOT_PASS", "p")
	// confirmed but NOT live → plan only, never an order/charge.
	res, err := provisionHetznerRobot("box", map[string]string{"confirmPaidOrder": "true"})
	if err != nil {
		t.Fatalf("dry-run must not error: %v", err)
	}
	if !res.OK || res.Details["mode"] == "" || !strings.Contains(res.Details["mode"], "dry-run") {
		t.Fatalf("confirmed-but-not-live must be a dry-run plan, got %+v", res)
	}
}

// Live path against a FAKE Robot API (httptest) — never the real
// robot-ws.your-server.de, never a real paid order. Verifies the
// documented contract: list server_market → pick cheapest in-band →
// POST transaction → return its id.
func TestRobotLiveOrderAgainstFakeAPI(t *testing.T) {
	var sawProductList, sawTxn bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "ru" || p != "rp" {
			http.Error(w, "auth", 401)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/order/server_market/product":
			sawProductList = true
			io.WriteString(w, `[{"product":{"id":"991","name":"AX41 (too pricey)","price":{"recurring":"89.00"}}},
			                    {"product":{"id":"42","name":"KVM box","price":{"recurring":"38.50"}}}]`)
		case r.Method == "POST" && r.URL.Path == "/order/server_market/transaction":
			sawTxn = true
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), "product_id=42") {
				t.Errorf("expected cheapest in-band product_id=42, body=%s", b)
			}
			io.WriteString(w, `{"transaction":{"id":"B20240517-1","status":"in process"}}`)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 400)
		}
	}))
	t.Cleanup(srv.Close)
	orig := hetznerRobotAPIBase
	hetznerRobotAPIBase = srv.URL
	t.Cleanup(func() { hetznerRobotAPIBase = orig })

	t.Setenv("HROBOT_USER", "ru")
	t.Setenv("HROBOT_PASS", "rp")
	res, err := provisionHetznerRobot("box", map[string]string{"confirmPaidOrder": "true", "live": "true"})
	if err != nil {
		t.Fatalf("live order against fake API failed: %v", err)
	}
	if !sawProductList || !sawTxn {
		t.Fatalf("expected product list + transaction calls (list=%v txn=%v)", sawProductList, sawTxn)
	}
	if res == nil || !res.OK || res.ID != "B20240517-1" || res.Details["product"] != "KVM box" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// Belt-and-braces: even with creds+confirm, live MUST NOT be reached
// without opts.live (no order, no charge).
func TestRobotConfirmedNoLiveNeverOrders(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	t.Cleanup(srv.Close)
	orig := hetznerRobotAPIBase
	hetznerRobotAPIBase = srv.URL
	t.Cleanup(func() { hetznerRobotAPIBase = orig })
	t.Setenv("HROBOT_USER", "ru")
	t.Setenv("HROBOT_PASS", "rp")
	res, err := provisionHetznerRobot("box", map[string]string{"confirmPaidOrder": "true"}) // no live
	if err != nil || res == nil || !strings.Contains(res.Details["mode"], "dry-run") {
		t.Fatalf("confirmed-no-live must be dry-run, got res=%+v err=%v", res, err)
	}
	if hits != 0 {
		t.Fatalf("dry-run must make ZERO Robot API calls, got %d", hits)
	}
}
