package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Closed-loop: a fake Convex serving the same /subscription + /billing/checkout
// contract the web UI uses. No mocks — a real httptest server on a random port.
func newFakeConvexRelay(t *testing.T, relayJSON string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/subscription", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("subscription: missing bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"relay":` + relayJSON + `}`))
	})
	mux.HandleFunc("/billing/checkout", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["productId"] != "relay-pro" {
			t.Errorf("checkout: expected productId relay-pro, got %q", body["productId"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"url":"https://pay.example/relay","productId":"relay-pro","mode":"sandbox"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRelayProStatus_Active(t *testing.T) {
	srv := newFakeConvexRelay(t, `{"status":"active","domain":"abc123.relay.yaver.io","region":"eu","quicPort":4433,"httpPort":443}`)
	cfg := &Config{
		ConvexSiteURL: srv.URL,
		AuthToken:     "tok",
		RelayServers:  []RelayServerConfig{{ID: "r1", QuicAddr: "abc123.relay.yaver.io:4433"}},
	}
	res := doRelayProStatus(cfg, srv.Client())
	if !res.OK {
		t.Fatalf("status failed: %s", res.Error)
	}
	init, _ := res.Initial.(map[string]interface{})
	if active, _ := init["active"].(bool); !active {
		t.Error("expected active=true")
	}
	if wired, _ := init["agentWiredToIt"].(bool); !wired {
		t.Error("expected agentWiredToIt=true (relay domain matches configured relay)")
	}
	if init["domain"] != "abc123.relay.yaver.io" {
		t.Errorf("unexpected domain: %v", init["domain"])
	}
}

func TestRelayProStatus_None(t *testing.T) {
	srv := newFakeConvexRelay(t, `null`)
	cfg := &Config{ConvexSiteURL: srv.URL, AuthToken: "tok"}
	res := doRelayProStatus(cfg, srv.Client())
	if !res.OK {
		t.Fatalf("status failed: %s", res.Error)
	}
	init, _ := res.Initial.(map[string]interface{})
	if active, _ := init["active"].(bool); active {
		t.Error("expected active=false when no managed relay")
	}
}

func TestRelayProCheckout_ReturnsPayURL(t *testing.T) {
	srv := newFakeConvexRelay(t, `null`)
	cfg := &Config{ConvexSiteURL: srv.URL, AuthToken: "tok"}
	res := doRelayProCheckout(cfg, "eu", srv.Client())
	if !res.OK {
		t.Fatalf("checkout failed: %s", res.Error)
	}
	init, _ := res.Initial.(map[string]interface{})
	if init["checkoutUrl"] != "https://pay.example/relay" {
		t.Errorf("unexpected checkoutUrl: %v", init["checkoutUrl"])
	}
}

// The cost guard: without accept_cost the handler must NOT hit the network —
// it returns a dry-run plan. (No fake Convex needed; a network call would fail.)
func TestRelayProCheckout_CostGuardDryRun(t *testing.T) {
	res := opsRelayProCheckoutHandler(OpsContext{}, json.RawMessage(`{"region":"eu"}`))
	if !res.OK {
		t.Fatalf("dry-run should be OK, got: %s", res.Error)
	}
	init, _ := res.Initial.(map[string]interface{})
	if dry, _ := init["dryRun"].(bool); !dry {
		t.Error("expected dryRun=true without accept_cost")
	}
}

func TestRelayProVerbsRegistered(t *testing.T) {
	for _, v := range []string{"relay_pro_status", "relay_pro_checkout"} {
		if _, ok := lookupOpsVerb(v); !ok {
			t.Errorf("verb %q not registered", v)
		}
	}
}
