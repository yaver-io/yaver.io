package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAndroidCloneProvisionDryRunByDefault(t *testing.T) {
	t.Setenv("YAVER_CLOUD_STOPSTART_LIVE", "")
	res := opsAndroidCloneProvisionHandler(OpsContext{}, json.RawMessage(`{"plan":"starter","confirm":true,"name":"clone-1"}`))
	if !res.OK {
		t.Fatalf("dry-run should be OK: %+v", res)
	}
	plan, _ := res.Initial.(androidClonePlan)
	if !plan.DryRun || plan.ServerType != "cax11" || !plan.Dedicated {
		t.Fatalf("bad dry-run plan: %+v", plan)
	}
}

func TestAndroidClonePlanRejectsBadPlanRegionName(t *testing.T) {
	tests := []androidCloneProvisionRequest{
		{Plan: "tiny"},
		{Region: "moon"},
		{Name: "bad;name"},
	}
	for _, tc := range tests {
		if _, err := buildAndroidClonePlan(tc); err == nil {
			t.Fatalf("expected invalid plan for %+v", tc)
		}
	}
}

func TestAndroidCloneBootstrapIncludesBinderAndRedroid(t *testing.T) {
	plan, err := buildAndroidClonePlan(androidCloneProvisionRequest{Name: "clone-1"})
	if err != nil {
		t.Fatal(err)
	}
	s := androidCloneBootstrapScript(plan)
	for _, want := range []string{"modprobe binder_linux", "docker pull redroid/redroid:13.0.0-latest", "yaver-redroid-up", "--privileged"} {
		if !strings.Contains(s, want) {
			t.Fatalf("bootstrap missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "0.0.0.0:5555") || strings.Contains(s, "-p 5555:5555") {
		t.Fatalf("bootstrap must not expose ADB on a public port:\n%s", s)
	}
}

func TestHetznerCreateAndroidCloneServerPayload(t *testing.T) {
	var got map[string]interface{}
	defer withRawFakeHetzner(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"server":{"id":4242,"public_net":{"ipv4":{"ip":"203.0.113.7"}}}}`))
	})()
	plan, err := buildAndroidClonePlan(androidCloneProvisionRequest{Plan: "pro", Region: "eu", Name: "clone-1"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := NewCloudDeployManager(".")
	ip, id, err := m.hetznerCreateAndroidCloneServer("tok", plan)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ip != "203.0.113.7" || id != "4242" {
		t.Fatalf("ip/id = %s/%s", ip, id)
	}
	if got["server_type"] != "cax21" {
		t.Fatalf("server_type = %#v, want cax21", got["server_type"])
	}
	if got["image"] != "ubuntu-22.04" || got["location"] != "nbg1" {
		t.Fatalf("image/location wrong: %#v", got)
	}
	labels, _ := got["labels"].(map[string]interface{})
	if labels["yaver_resource"] != "android-clone" || labels["yaver_dedicated"] != "true" {
		t.Fatalf("missing dedicated labels: %#v", got["labels"])
	}
	userData, _ := got["user_data"].(string)
	if !strings.Contains(userData, "yaver-redroid-up") {
		t.Fatalf("user_data missing redroid bootstrap:\n%s", userData)
	}
}

func TestAndroidCloneProvisionSchemaHasNoCredentialField(t *testing.T) {
	opsRegistryMu.RLock()
	spec, ok := opsRegistry["android_clone_provision"]
	opsRegistryMu.RUnlock()
	if !ok {
		t.Fatal("android_clone_provision not registered")
	}
	props, _ := spec.Schema["properties"].(map[string]interface{})
	for k := range props {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "token") || strings.Contains(lk, "secret") || strings.Contains(lk, "password") {
			t.Fatalf("schema exposes credential-like field %q", k)
		}
	}
	if spec.AllowGuest {
		t.Fatal("android_clone_provision must be owner-only")
	}
}
