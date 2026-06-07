package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// setup_inventory must be a safe, non-secret read: it returns ok with the
// four sections even on a fresh machine (empty vault, no git creds), and
// must NEVER leak a token/value. Isolated HOME so it reads nothing real.
func TestSetupInventory_NonSecretAndSafe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	res := opsSetupInventoryHandler(OpsContext{}, json.RawMessage(`{}`))
	if !res.OK {
		t.Fatalf("setup_inventory should be OK on a fresh machine, got %+v", res)
	}
	mm, _ := res.Initial.(map[string]interface{})
	for _, k := range []string{"git", "runners", "accounts", "vault"} {
		if _, ok := mm[k]; !ok {
			t.Fatalf("inventory missing %q section: %#v", k, mm)
		}
	}
	// No secret-shaped key anywhere in the serialized inventory.
	blob, _ := json.Marshal(mm)
	low := strings.ToLower(string(blob))
	for _, bad := range []string{"\"token\"", "\"secret\"", "\"password\"", "\"privatekey\"", "\"apikey\""} {
		if strings.Contains(low, bad) {
			t.Fatalf("inventory leaks a secret-shaped field %q: %s", bad, blob)
		}
	}
}

func TestSetupInventory_OwnerOnly(t *testing.T) {
	opsRegistryMu.RLock()
	spec, ok := opsRegistry["setup_inventory"]
	opsRegistryMu.RUnlock()
	if !ok {
		t.Fatal("setup_inventory not registered")
	}
	if spec.AllowGuest {
		t.Fatal("setup_inventory must be AllowGuest:false (a guest must not enumerate the owner's config)")
	}
}
