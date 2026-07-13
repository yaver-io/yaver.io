package main

import (
	"net/http/httptest"
	"testing"
)

// Pentest regression (audit 2026-07-13): secret-bearing ops verbs (secrets/env/
// runner_auth) must never execute for a caller on another machine. The old
// client-side layer4Tools denylist keyed on names that don't exist at runtime
// (verbs proxy as "ops:<verb>"), so a same-user remote worker could exfiltrate
// the owner's vault/env plaintext. The holder-side gate closes that.
func TestOpsSecretBoundary(t *testing.T) {
	for _, verb := range []string{"secrets", "env", "runner_auth"} {
		if !opsVerbIsLocalOnlySecret(verb) {
			t.Errorf("verb %q must be treated as local-only secret", verb)
		}
	}
	for _, verb := range []string{"build", "deploy", "status", "runner"} {
		if opsVerbIsLocalOnlySecret(verb) {
			t.Errorf("verb %q must NOT be blocked as a secret verb", verb)
		}
	}

	// Local owner MCP call: loopback, not bridged, not proxied → allowed.
	local := httptest.NewRequest("POST", "/ops", nil)
	local.RemoteAddr = "127.0.0.1:53344"
	if opsCallIsRemote(local) {
		t.Fatal("VULNERABLE-INVERSE: a loopback owner call was flagged remote (would break local secrets)")
	}

	// Relay-bridged call: RemoteAddr is loopback but the bridge stamped the
	// marker → must be treated as remote.
	bridged := httptest.NewRequest("POST", "/ops", nil)
	bridged.RemoteAddr = "127.0.0.1:53344"
	bridged.Header.Set("X-Yaver-Via-Relay", "1")
	if !opsCallIsRemote(bridged) {
		t.Fatal("VULNERABLE: relay-bridged call not detected as remote → secret verbs would leak")
	}

	// Proxied-by another agent → remote.
	proxied := httptest.NewRequest("POST", "/ops", nil)
	proxied.RemoteAddr = "127.0.0.1:53344"
	proxied.Header.Set("X-Yaver-Proxied-By", "some-device")
	if !opsCallIsRemote(proxied) {
		t.Fatal("VULNERABLE: agent-proxied call not detected as remote")
	}

	// Direct non-loopback peer → remote.
	lan := httptest.NewRequest("POST", "/ops", nil)
	lan.RemoteAddr = "192.168.1.50:41000"
	if !opsCallIsRemote(lan) {
		t.Fatal("VULNERABLE: non-loopback LAN peer not detected as remote")
	}
}
