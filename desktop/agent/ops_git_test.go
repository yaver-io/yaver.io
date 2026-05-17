package main

import (
	"encoding/json"
	"testing"
)

func TestOpsGitPushRegistered(t *testing.T) {
	opsRegistryMu.RLock()
	spec, ok := opsRegistry["git_push"]
	opsRegistryMu.RUnlock()
	if !ok || spec.Handler == nil {
		t.Fatal("git_push ops verb not registered / no handler")
	}
	if spec.AllowGuest {
		t.Error("git_push must not be guest-allowed (it moves credentials)")
	}
}

func TestOpsGitPushRequiresDeviceId(t *testing.T) {
	// Empty deviceId must fail BEFORE any credential detection/forward.
	res := opsGitPushHandler(OpsContext{}, json.RawMessage(`{}`))
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("missing deviceId must be bad_payload, got %+v", res)
	}
	bad := opsGitPushHandler(OpsContext{}, json.RawMessage(`{not json`))
	if bad.OK || bad.Code != "bad_payload" {
		t.Fatalf("invalid JSON must be bad_payload, got %+v", bad)
	}
}
