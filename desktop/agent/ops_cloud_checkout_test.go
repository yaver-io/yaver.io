package main

import (
	"encoding/json"
	"testing"
)

// cloud_checkout is the MCP/ops front door to buying a managed box.
// We assert it is registered with the right guards and that input
// validation fails fast — we deliberately do NOT exercise the
// network/LoadConfig path here: the handler is a thin authed proxy to
// the prod Convex checkout route, and a unit test must never fire a
// real billing call.
func TestOpsCloudCheckoutRegistered(t *testing.T) {
	opsRegistryMu.RLock()
	spec, ok := opsRegistry["cloud_checkout"]
	opsRegistryMu.RUnlock()
	if !ok {
		t.Fatal("cloud_checkout ops verb not registered")
	}
	if spec.Handler == nil {
		t.Fatal("cloud_checkout has no handler")
	}
	if spec.Streaming {
		t.Error("cloud_checkout should not be streaming")
	}
	if spec.AllowGuest {
		t.Error("cloud_checkout must not be guest-allowed (it spends/auths as the owner)")
	}
}

func TestOpsCloudCheckoutBadPayload(t *testing.T) {
	res := opsCloudCheckoutHandler(OpsContext{}, json.RawMessage(`{not json`))
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("invalid JSON must be bad_payload, got %+v", res)
	}
}
