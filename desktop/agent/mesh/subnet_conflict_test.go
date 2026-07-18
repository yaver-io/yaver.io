package mesh

import (
	"net/netip"
	"strings"
	"testing"
)

// TestMeshSubnetIsInsideTailscaleRange locks in the correction made on
// 2026-07-18. MeshSubnetCIDR was documented for a long time as "deliberately
// OUTSIDE Tailscale's 100.64.0.0/10 so both can run side by side". That is
// false, and the false claim is what made a real "phone can't reach my Mac"
// bug hard to find.
//
// This test exists so nobody can restore the comfortable version of the story
// without a red test. If mesh's range is ever genuinely relocated outside
// CGNAT, this test SHOULD fail — and the fix is to update it together with the
// coexistence logic, not to delete it.
func TestMeshSubnetIsInsideTailscaleRange(t *testing.T) {
	mesh := netip.MustParsePrefix(MeshSubnetCIDR)
	ts := netip.MustParsePrefix(TailscaleCGNATCIDR)

	if !ts.Overlaps(mesh) {
		t.Fatalf("expected %s to overlap %s — if mesh was deliberately relocated "+
			"outside CGNAT, update the coexistence logic in SubnetRouteConflict too",
			MeshSubnetCIDR, TailscaleCGNATCIDR)
	}

	// Containment is the strong claim: every mesh address is a Tailscale-range
	// address. Check the boundaries explicitly rather than trusting Overlaps.
	for _, s := range []string{"100.96.0.0", "100.111.255.255", "100.96.0.1"} {
		addr := netip.MustParseAddr(s)
		if !mesh.Contains(addr) {
			t.Fatalf("%s should be inside mesh subnet %s", s, MeshSubnetCIDR)
		}
		if !ts.Contains(addr) {
			t.Fatalf("%s is in mesh subnet %s but NOT in %s — the ranges are no "+
				"longer nested; the 'both can run side by side' claim would need "+
				"re-verifying, not assuming", s, MeshSubnetCIDR, TailscaleCGNATCIDR)
		}
	}

	// And the converse: Tailscale's range is strictly wider, so there are
	// addresses it covers that mesh does not. This is what makes a host-wide
	// 100.64/10 route capture our /12.
	outsideMeshInsideTS := netip.MustParseAddr("100.64.0.1")
	if mesh.Contains(outsideMeshInsideTS) {
		t.Fatalf("100.64.0.1 must NOT be in mesh subnet %s", MeshSubnetCIDR)
	}
	if !ts.Contains(outsideMeshInsideTS) {
		t.Fatalf("100.64.0.1 must be in %s", TailscaleCGNATCIDR)
	}
}

// TestConflictReasonExplainsItself checks the user-facing string actually names
// the interface and both ranges. A conflict the user cannot act on is not much
// better than a silent one — this is the message that replaces "mesh is off"
// with "mesh is off BECAUSE".
func TestConflictReasonExplainsItself(t *testing.T) {
	c := &Conflict{Interface: "utun4", Address: "100.89.0.2", OverlapsMeshSubnet: false}
	got := c.Reason()
	for _, want := range []string{"utun4", "100.89.0.2", TailscaleCGNATCIDR, MeshSubnetCIDR} {
		if !strings.Contains(got, want) {
			t.Errorf("Reason() missing %q\n  got: %s", want, got)
		}
	}
	// Must not promise to remove the user's VPN.
	for _, forbidden := range []string{"disable Tailscale", "remove", "uninstall"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("Reason() should never tell the user to tear down their own VPN; contains %q", forbidden)
		}
	}

	// A direct clash reads differently from a mere range overlap.
	direct := &Conflict{Interface: "utun4", Address: "100.96.0.5", OverlapsMeshSubnet: true}
	if direct.Reason() == got {
		t.Error("a direct in-range clash should not read identically to a wider-range overlap")
	}

	// nil is a valid "no conflict" and must not panic.
	var none *Conflict
	if none.Reason() != "" {
		t.Error("nil conflict should render empty")
	}
}

// TestSubnetRouteConflictSkipsOwnInterface guards the obvious self-own: once
// mesh IS up, its own device holds a 100.96.x address, and reporting that as a
// conflict would make mesh permanently refuse to run after first start.
func TestSubnetRouteConflictSkipsOwnInterface(t *testing.T) {
	// Enumerating real interfaces is environment-dependent, so assert only the
	// invariant we control: whatever the host looks like, passing the name of
	// an interface must never return THAT interface as the conflict.
	c, err := SubnetRouteConflict("utun-yaver")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != nil && c.Interface == "utun-yaver" {
		t.Fatalf("SubnetRouteConflict reported its own interface as a conflict: %+v", c)
	}
}
