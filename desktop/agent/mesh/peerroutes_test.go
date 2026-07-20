package mesh

import (
	"fmt"
	"testing"
)

func fakeTable() (*PeerRouteTable, *[]string) {
	var log []string
	t := NewPeerRouteTable("utun9")
	t.addRoute = func(iface, ip string) error { log = append(log, "add "+ip); return nil }
	t.delRoute = func(ip string) error { log = append(log, "del "+ip); return nil }
	return t, &log
}

// Rule 1: never route a peer the incumbent already reaches. Violating this is
// how you break a user's Tailscale.
func TestNeverRoutesAPeerTheIncumbentReaches(t *testing.T) {
	routes, skipped := SelectPeerRoutes([]PeerRouteDecision{
		{DeviceID: "A", MeshIP: "100.96.0.1", IncumbentReaches: true, Validated: true},
	})
	if len(routes) != 0 {
		t.Fatal("routed a peer Tailscale already carries — this competes with the user's VPN")
	}
	if skipped["A"] == "" {
		t.Fatal("an exclusion with no stated reason is the failure mode being removed")
	}
}

// Rule 2: an unvalidated /32 is a blackhole — traffic leaves for an interface
// that cannot deliver it, and it looks like the peer is down.
func TestNeverRoutesAnUnvalidatedPeer(t *testing.T) {
	routes, skipped := SelectPeerRoutes([]PeerRouteDecision{
		{DeviceID: "B", MeshIP: "100.96.0.2", Validated: false},
	})
	if len(routes) != 0 {
		t.Fatal("routed an unvalidated address — that is a blackhole, the relay is better")
	}
	if skipped["B"] == "" {
		t.Fatal("missing reason")
	}
}

// The hybrid case: incumbent cannot reach it, path validated -> route it.
func TestRoutesTheHybridPeer(t *testing.T) {
	routes, _ := SelectPeerRoutes([]PeerRouteDecision{
		{DeviceID: "phone", MeshIP: "100.96.0.9", IncumbentReaches: false, Validated: true},
	})
	if len(routes) != 1 || routes[0].MeshIP != "100.96.0.9" {
		t.Fatalf("the peer Tailscale cannot carry must get a mesh route, got %+v", routes)
	}
}

func TestReconcileAddsRemovesAndIsIdempotent(t *testing.T) {
	tb, log := fakeTable()

	added, removed, errs := tb.Reconcile([]PeerRoute{{DeviceID: "A", MeshIP: "100.96.0.1"}})
	if len(errs) != 0 || len(added) != 1 || len(removed) != 0 {
		t.Fatalf("first install: added=%v removed=%v errs=%v", added, removed, errs)
	}
	// Same desired set again must be a no-op — a reconciler that re-adds churns
	// the kernel table on every peer refresh.
	added, removed, _ = tb.Reconcile([]PeerRoute{{DeviceID: "A", MeshIP: "100.96.0.1"}})
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("reconcile is not idempotent: added=%v removed=%v", added, removed)
	}
	// Dropping the peer removes the route.
	_, removed, _ = tb.Reconcile(nil)
	if len(removed) != 1 {
		t.Fatalf("a peer that disappeared must have its route removed, got %v", removed)
	}
	if len(tb.Installed()) != 0 {
		t.Fatal("table still claims routes it removed")
	}
	_ = log
}

// A changed address must DELETE before it ADDs, or we briefly own two routes
// for one peer and the kernel picks, not us.
func TestChangedAddressDeletesBeforeAdding(t *testing.T) {
	tb, log := fakeTable()
	tb.Reconcile([]PeerRoute{{DeviceID: "A", MeshIP: "100.96.0.1"}})
	*log = (*log)[:0]
	tb.Reconcile([]PeerRoute{{DeviceID: "A", MeshIP: "100.96.0.5"}})
	if len(*log) != 2 || (*log)[0] != "del 100.96.0.1" || (*log)[1] != "add 100.96.0.5" {
		t.Fatalf("expected delete-then-add, got %v", *log)
	}
}

// A failed install must not be recorded as installed, or Clear() will leave a
// real route behind while believing it removed everything.
func TestFailedInstallIsNotRecorded(t *testing.T) {
	tb, _ := fakeTable()
	tb.addRoute = func(string, string) error { return fmt.Errorf("permission denied") }
	added, _, errs := tb.Reconcile([]PeerRoute{{DeviceID: "A", MeshIP: "100.96.0.1"}})
	if len(added) != 0 || len(errs) != 1 {
		t.Fatalf("added=%v errs=%v", added, errs)
	}
	if len(tb.Installed()) != 0 {
		t.Fatal("a route that failed to install must not be tracked as installed")
	}
}

func TestClearRemovesEverything(t *testing.T) {
	tb, _ := fakeTable()
	tb.Reconcile([]PeerRoute{{DeviceID: "A", MeshIP: "100.96.0.1"}, {DeviceID: "B", MeshIP: "100.96.0.2"}})
	if errs := tb.Clear(); len(errs) != 0 {
		t.Fatalf("clear: %v", errs)
	}
	if len(tb.Installed()) != 0 {
		t.Fatal("shutdown must not leave the kernel pointing at a dead interface")
	}
}
