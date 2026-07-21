package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubProbe lets the report tests run without real network; the live probe is
// exercised separately against a real httptest server.
func stubProbe(reachable bool) func(context.Context, string) (bool, int64, string) {
	return func(context.Context, string) (bool, int64, string) {
		if reachable {
			return true, 5, ""
		}
		return false, 0, "unreachable"
	}
}

func TestProbeTransport_RemedyBranches(t *testing.T) {
	ctx := context.Background()
	relays := []RelayServerConfig{{ID: "public-free", HttpURL: "https://relay.example/"}}

	// No relays → "no relays configured".
	if r := probeTransport(ctx, "d1", false, "pw", nil, stubProbe(true)); r.Remedy == "" || r.RelayCount != 0 {
		t.Errorf("no relays: got remedy %q count %d", r.Remedy, r.RelayCount)
	}
	// Missing password → "relay password missing".
	if r := probeTransport(ctx, "d1", false, "", relays, stubProbe(true)); !strings.Contains(r.Remedy, "password missing") {
		t.Errorf("missing pw remedy wrong: %q", r.Remedy)
	}
	// Password + none reachable → "no relay reachable".
	if r := probeTransport(ctx, "d1", false, "pw", relays, stubProbe(false)); r.AnyRelayReachable || !strings.Contains(r.Remedy, "no relay reachable") {
		t.Errorf("unreachable remedy wrong: reachable=%v remedy=%q", r.AnyRelayReachable, r.Remedy)
	}
	// Password + reachable → "stale registration" hint.
	if r := probeTransport(ctx, "d1", false, "pw", relays, stubProbe(true)); !r.AnyRelayReachable || !strings.Contains(r.Remedy, "re-register") {
		t.Errorf("reachable remedy wrong: reachable=%v remedy=%q", r.AnyRelayReachable, r.Remedy)
	}
}

// The REAL probe against a live server: a 401 (like the relay proxy actually
// returns) still counts as reachable — the leg is alive, auth is a separate axis.
func TestHttpRelayProbe_401IsReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	ok, ms, errStr := httpRelayProbe(context.Background(), srv.URL)
	if !ok {
		t.Fatalf("a 401 relay must count as REACHABLE (leg alive), got err=%q", errStr)
	}
	if ms < 0 {
		t.Fatalf("latency should be non-negative, got %d", ms)
	}
}

func TestHttpRelayProbe_DeadHostUnreachable(t *testing.T) {
	// Reserved TEST-NET-1 address that won't answer — must probe as unreachable.
	ok, _, errStr := httpRelayProbe(context.Background(), "http://192.0.2.1:9/")
	if ok {
		t.Fatal("a dead host must probe as unreachable")
	}
	if errStr == "" {
		t.Error("unreachable probe should carry an error string")
	}
}

