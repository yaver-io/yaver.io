package main

import (
	"context"
	"strings"
	"testing"

	"github.com/yaver-io/agent/mesh"
)

// The bill guarantee, asserted against the real function rather than the struct:
// a device whose shape has not changed must add NOTHING to the heartbeat.
func TestConnStatusOmittedWhenUnchanged(t *testing.T) {
	connStatusMu.Lock()
	connStatusLast = nil
	connStatusIntent, connStatusTier = mesh.IntentOnline, "relay"
	connStatusMu.Unlock()

	ctx := context.Background()
	if first := connStatusForHeartbeat(ctx); first == nil {
		t.Fatal("the first beat must publish — Convex has nothing yet")
	}
	if second := connStatusForHeartbeat(ctx); second != nil {
		t.Fatalf("an UNCHANGED device must add nothing to the heartbeat, got %v", second)
	}
	// Only the clock moved between those two calls, which is precisely the case
	// that would have made every beat a write.

	SetConnIntent(mesh.IntentWantsPeers, "relay")
	if third := connStatusForHeartbeat(ctx); third == nil {
		t.Fatal("a real intent change must publish — peers act on it")
	}
	if fourth := connStatusForHeartbeat(ctx); fourth != nil {
		t.Fatal("and then go quiet again")
	}
}

// The privacy contract: this payload may carry the SHAPE of the network and
// never its addresses. Endpoints travel peer-to-peer over the relay.
func TestConnStatusCarriesNoAddresses(t *testing.T) {
	connStatusMu.Lock()
	connStatusLast = nil
	connStatusMu.Unlock()

	got := connStatusForHeartbeat(context.Background())
	if got == nil {
		t.Fatal("expected a first publish")
	}
	allowed := map[string]bool{"intent": true, "tier": true, "onTailnet": true, "meshOk": true, "nat": true, "at": true}
	for k, v := range got {
		if !allowed[k] {
			t.Errorf("unexpected field %q in a Convex payload — only the network SHAPE may go here", k)
		}
		if s, ok := v.(string); ok {
			for _, bad := range []string{".", "/Users/", "/home/"} {
				if bad == "." && !strings.Contains(s, ".") {
					continue
				}
				if strings.Contains(s, "/Users/") || strings.Contains(s, "/home/") {
					t.Errorf("field %q leaks a path: %q", k, s)
				}
			}
			// An IP-looking value is the specific thing forbidden here.
			if strings.Count(s, ".") == 3 && strings.IndexFunc(s, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 {
				t.Errorf("field %q looks like an IP address (%q) — addresses go peer-to-peer, never to Convex", k, s)
			}
		}
	}
}
