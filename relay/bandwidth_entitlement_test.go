package main

import "testing"

// A request that could not RESOLVE an entitlement must never downgrade a
// device a previously-resolved request marked unmetered.
//
// Live incident 2026-07-27: the owner's phone was refused with "bandwidth
// limit exceeded: 1911MB of 1500MB" while the relay's own store recorded
// unmetered:true for that same device. Cause: SetDeviceTier ran on EVERY
// proxy request carrying THAT request's verdict, and the webview-cookie
// auth path (no password, no signature, so no plan is resolved) passed
// isPaid=false, unmetered=false — flipping the device back to metered,
// refusing the request, until the next password-authenticated request
// flipped it on again. Preview subresources ARE the cookie-authorized
// traffic, so the browser lane refused itself while the store said the
// owner was exempt.
//
// Entitlement belongs to the ACCOUNT, not to whichever request happens to
// arrive. A request that cannot resolve it must leave it alone.
func TestUnknownEntitlementNeverDowngrades(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.ApplyEntitlement("owner-box", deviceEntitlement{Known: true, IsPaid: true, Unmetered: true})
	bm.RecordBytes("owner-box", 0, 100*1024*1024*1024, true) // far past any cap

	bm.ApplyEntitlement("owner-box", entitlementUnknown) // cookie-authorized request

	if err := bm.CheckAllowed("owner-box", 1); err != nil {
		t.Fatalf("an unknowing request downgraded a verified-unmetered device: %v", err)
	}
	if got := bm.RemainingBytes("owner-box"); got != 0 {
		t.Fatalf("RemainingBytes = %d, want 0 (still unmetered)", got)
	}
}

// A RESOLVED verdict still applies, including a downgrade — otherwise a
// cancelled plan would stay exempt forever.
func TestResolvedEntitlementStillApplies(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.ApplyEntitlement("box", deviceEntitlement{Known: true, IsPaid: true, Unmetered: true})
	bm.ApplyEntitlement("box", deviceEntitlement{Known: true, IsPaid: false, Unmetered: false})

	over := int64(bm.config.FreeDeviceLimitMB)*1024*1024*int64(bm.config.RelaxMultiplier) + 1
	bm.RecordBytes("box", 0, over, false)
	if err := bm.CheckAllowed("box", 1); err == nil {
		t.Fatal("a resolved free-tier verdict must re-meter the device")
	}
}

func TestUsageRecordWithoutResolvedPlanDoesNotDowngradePaidAccount(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.ApplyEntitlement("box", deviceEntitlement{Known: true, IsPaid: true})
	// A cookie/subresource lane records bytes without resolving a plan. That is
	// not evidence that the account became free.
	used := int64(bm.config.FreeDeviceLimitMB)*1024*1024*int64(bm.config.RelaxMultiplier) + 1
	bm.RecordBytes("box", 0, used, false)
	if err := bm.CheckAllowed("box", 1); err != nil {
		t.Fatalf("an unknowing usage record downgraded the paid tier: %v", err)
	}
}

// The owner's exemption belongs to the OWNER, so it must reach every device
// that owner has — including one whose own requests never resolved a plan
// (a box reached only through cookie-authorized preview subresources).
func TestOwnerEntitlementAppliesToEveryDeviceOfThatOwner(t *testing.T) {
	s := &RelayServer{userEntitlements: map[string]deviceEntitlement{}}
	s.rememberUserEntitlement("user-1", deviceEntitlement{Known: true, IsPaid: true, Unmetered: true})

	// A different device of the same owner, never seen with a password.
	got := s.entitlementForUser("user-1")
	if !got.Known || !got.Unmetered {
		t.Fatalf("owner entitlement did not carry to a sibling device: %+v", got)
	}
	if other := s.entitlementForUser("user-2"); other.Known {
		t.Fatalf("entitlement leaked across accounts: %+v", other)
	}
}
