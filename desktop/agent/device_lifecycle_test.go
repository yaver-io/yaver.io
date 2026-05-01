package main

import "testing"

func TestBootstrapLifecycleInfoFreshBoxRequiresFirstPair(t *testing.T) {
	EndPairingSession()
	got := bootstrapLifecycleInfo(&Config{})
	if got.State != AgentLifecycleBootstrap {
		t.Fatalf("state = %q, want %q", got.State, AgentLifecycleBootstrap)
	}
	if !got.Recoverable {
		t.Fatalf("expected bootstrap lifecycle to be recoverable")
	}
	if got.Usable {
		t.Fatalf("expected bootstrap lifecycle to be unusable")
	}
	if !got.RequiresFirstPair {
		t.Fatalf("expected fresh bootstrap to require first pair")
	}
	if got.SupportsOwnerClaim {
		t.Fatalf("fresh bootstrap should not support owner claim")
	}
	if got.OwnerClaimReady {
		t.Fatalf("fresh bootstrap should not report owner claim ready")
	}
	if got.RecoveryMode != AgentLifecycleFreshBootstrap {
		t.Fatalf("recoveryMode = %q, want %q", got.RecoveryMode, AgentLifecycleFreshBootstrap)
	}
}

func TestBootstrapLifecycleInfoOwnedBoxCanOwnerClaim(t *testing.T) {
	EndPairingSession()
	if _, err := StartPairingSession(bootstrapPairingTTL); err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	t.Cleanup(EndPairingSession)

	got := bootstrapLifecycleInfo(&Config{
		DeviceID:      "device-123",
		ConvexSiteURL: "https://example.convex.cloud",
	})
	if got.State != AgentLifecycleBootstrap {
		t.Fatalf("state = %q, want %q", got.State, AgentLifecycleBootstrap)
	}
	if got.RequiresFirstPair {
		t.Fatalf("expected previously-owned bootstrap to not require first pair")
	}
	if !got.SupportsOwnerClaim {
		t.Fatalf("expected owner claim support")
	}
	if !got.OwnerClaimReady {
		t.Fatalf("expected owner claim readiness when pairing session exists")
	}
	if got.RecoveryMode != AgentLifecycleBootstrapRecover {
		t.Fatalf("recoveryMode = %q, want %q", got.RecoveryMode, AgentLifecycleBootstrapRecover)
	}
}

func TestAuthenticatedLifecycleInfoReflectsAuthExpired(t *testing.T) {
	srv := &HTTPServer{}
	gotReady := srv.lifecycleInfo()
	if gotReady.State != AgentLifecycleReadyToConnect {
		t.Fatalf("ready state = %q, want %q", gotReady.State, AgentLifecycleReadyToConnect)
	}
	if !gotReady.Usable {
		t.Fatalf("ready state should be usable")
	}

	srv.authExpired.Store(true)
	gotExpired := srv.lifecycleInfo()
	if gotExpired.State != AgentLifecycleAuthExpired {
		t.Fatalf("expired state = %q, want %q", gotExpired.State, AgentLifecycleAuthExpired)
	}
	if gotExpired.Usable {
		t.Fatalf("expired state should not be usable")
	}
	if !gotExpired.Recoverable {
		t.Fatalf("expired state should be recoverable")
	}
	if gotExpired.RecoveryMode != AgentLifecycleReauthRecover {
		t.Fatalf("recoveryMode = %q, want %q", gotExpired.RecoveryMode, AgentLifecycleReauthRecover)
	}
}
