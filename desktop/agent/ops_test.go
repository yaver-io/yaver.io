package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDispatchOpsUnknownVerb — an agent calling an undefined verb should
// get a structured error with the "unknown_verb" code, not a panic
// and not a generic 500. Stable code is the contract agents branch on.
func TestDispatchOpsUnknownVerb(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "does_not_exist"})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "unknown_verb" {
		t.Fatalf("want code=unknown_verb, got %q (err=%q)", res.Code, res.Error)
	}
	if !strings.Contains(res.Error, "does_not_exist") {
		t.Fatalf("error should mention the verb, got %q", res.Error)
	}
}

// TestDispatchOpsRemoteRouting — cross-machine routing now tries to
// proxy through the existing peer-proxy plumbing. In a unit test
// (no Convex, no relay) that resolution fails, and the dispatcher
// must return a typed `remote_failed` error rather than leaking the
// underlying transport message or panicking. Agents branch on the
// stable code to decide whether to retry / use a different machine.
func TestDispatchOpsRemoteRouting(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "some-device-id", Verb: "info"})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	// Either remote_failed (transport error — the common case in tests)
	// or remote_malformed (if a proxy somehow returned junk). Both are
	// acceptable fail-fast modes; what we want to prove is that the
	// dispatcher doesn't fall through to local execution silently.
	if res.Code != "remote_failed" && res.Code != "remote_malformed" && res.Code != "unauthorized" {
		t.Fatalf("want remote_failed|remote_malformed|unauthorized, got %q (err=%q)", res.Code, res.Error)
	}
}

// TestDispatchOpsPrimaryAliasUnset — asking for the primary device
// when the user hasn't set one returns a typed error rather than
// falling through to local. This is the default state for fresh
// installs and the check catches the "forgot to set primary" UX.
func TestDispatchOpsPrimaryAliasUnset(t *testing.T) {
	// No HTTPServer context — resolver fails cleanly.
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "primary", Verb: "info"})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "invalid_machine" {
		t.Fatalf("want invalid_machine, got %q", res.Code)
	}
}

// TestOpsInfoLocal — the built-in info verb should work against a nil
// server (specs only), returning the baseline machine fingerprint so
// agents can orient themselves before any further ops.
func TestOpsInfoLocal(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "info"})
	if !res.OK {
		t.Fatalf("info failed: %s (%s)", res.Error, res.Code)
	}
	m, ok := res.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("initial not a map, got %T", res.Initial)
	}
	for _, k := range []string{"hostname", "platform", "arch", "numCPU", "localIPs"} {
		if _, present := m[k]; !present {
			t.Errorf("info output missing %q", k)
		}
	}
}

func TestOpsInfoAutoFallsBackLocal(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "auto", Verb: "info"})
	if !res.OK {
		t.Fatalf("info failed: %s (%s)", res.Error, res.Code)
	}
	m, ok := res.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("initial not a map, got %T", res.Initial)
	}
	if got := strings.TrimSpace(fmt.Sprint(m["selectedMachine"])); got != "local" {
		t.Fatalf("expected selectedMachine=local, got %q", got)
	}
	if !strings.Contains(strings.ToLower(fmt.Sprint(m["selectionReason"])), "local") {
		t.Fatalf("expected local fallback reason, got %v", m["selectionReason"])
	}
}

func TestBuildOpsExecutionPlanAutoFallsBackLocal(t *testing.T) {
	plan := buildOpsExecutionPlan(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "auto",
		Verb:    "info",
	})
	if !plan.OK {
		t.Fatal("expected OK=true")
	}
	if plan.ResolvedMachine != "local" {
		t.Fatalf("expected local machine, got %q", plan.ResolvedMachine)
	}
	if !strings.Contains(strings.ToLower(plan.SelectionReason), "local") {
		t.Fatalf("expected local selection reason, got %q", plan.SelectionReason)
	}
}

func TestBuildOpsExecutionPlanIncludesGuestPolicy(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "sample-app")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := NewGuestConfigManager(tmp)
	mgr.UpdateConfigs([]GuestConfig{{
		GuestUserID:               "guest-1",
		GuestEmail:                "guest@example.com",
		Scope:                     GuestScopeDeploy,
		AllowedProjects:           []string{"sample-app"},
		AllowedRunners:            []string{"codex"},
		ResourcePreset:            "balanced",
		AllowDesktopControl:       boolPtr(true),
		AllowBrowserControl:       boolPtr(true),
		AllowTunnelForward:        boolPtr(false),
		RequireIsolation:          boolPtr(true),
		UseHostAPIKeys:            boolPtr(true),
		AllowGuestProvidedAPIKeys: boolPtr(false),
	}})
	server := &HTTPServer{guestConfigMgr: mgr}
	headers := http.Header{}
	headers.Set("X-Yaver-GuestScope", GuestScopeDeploy)
	plan := buildOpsExecutionPlan(OpsContext{
		Ctx:            context.Background(),
		Server:         server,
		RequestHeaders: headers,
		ActorUserID:    "guest-1",
		Caller:         "guest",
	}, OpsRequest{
		Machine: "auto",
		Verb:    "deploy",
		Payload: json.RawMessage(fmt.Sprintf(`{"target":"vercel","workDir":%q}`, workDir)),
	})
	if plan.Access.Caller != "guest" {
		t.Fatalf("expected guest caller, got %q", plan.Access.Caller)
	}
	if plan.Access.GuestScope != GuestScopeDeploy {
		t.Fatalf("expected guest scope %q, got %q", GuestScopeDeploy, plan.Access.GuestScope)
	}
	if len(plan.Access.AllowedProjects) != 1 || plan.Access.AllowedProjects[0] != "sample-app" {
		t.Fatalf("unexpected allowed projects: %#v", plan.Access.AllowedProjects)
	}
	if len(plan.Access.AllowedRunners) != 1 || plan.Access.AllowedRunners[0] != "codex" {
		t.Fatalf("unexpected allowed runners: %#v", plan.Access.AllowedRunners)
	}
	if plan.Project == nil || plan.Project.WorkDir != workDir {
		t.Fatalf("expected project workDir %q, got %#v", workDir, plan.Project)
	}
	if plan.Project == nil || plan.Project.Name != "sample-app" {
		t.Fatalf("expected project name sample-app, got %#v", plan.Project)
	}
	if !plan.Access.RequireIsolation {
		t.Fatal("expected requireIsolation=true")
	}
}

func TestDispatchOpsGuestDeployScopeRejectsNonDeployVerb(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Yaver-GuestScope", GuestScopeDeploy)
	res := dispatchOps(OpsContext{
		Ctx:            context.Background(),
		Caller:         "guest",
		RequestHeaders: headers,
	}, OpsRequest{
		Machine: "local",
		Verb:    "reload",
	})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "unauthorized" {
		t.Fatalf("expected unauthorized, got %q", res.Code)
	}
}

func TestDispatchOpsGuestDeployScopeAllowsDeploy(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "sample-app")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr := NewGuestConfigManager(tmp)
	mgr.UpdateConfigs([]GuestConfig{{
		GuestUserID:     "guest-1",
		Scope:           GuestScopeDeploy,
		AllowedProjects: []string{"sample-app"},
	}})
	server := &HTTPServer{guestConfigMgr: mgr}
	headers := http.Header{}
	headers.Set("X-Yaver-GuestScope", GuestScopeDeploy)
	res := dispatchOps(OpsContext{
		Ctx:            context.Background(),
		Server:         server,
		Caller:         "guest",
		ActorUserID:    "guest-1",
		RequestHeaders: headers,
	}, OpsRequest{
		Machine: "auto",
		Verb:    "deploy",
		Payload: json.RawMessage(fmt.Sprintf(`{"target":"cloud","workDir":%q}`, workDir)),
	})
	if !res.OK {
		t.Fatalf("expected OK=true, got %s (%s)", res.Error, res.Code)
	}
}

func TestDispatchOpsHostShareRejectsDisallowedVerb(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Yaver-HostShare", "true")
	res := dispatchOps(OpsContext{
		Ctx:            context.Background(),
		Caller:         "host-share",
		RequestHeaders: headers,
	}, OpsRequest{
		Machine: "local",
		Verb:    "run",
	})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "unauthorized" {
		t.Fatalf("expected unauthorized, got %q", res.Code)
	}
}

func TestDispatchOpsHostShareDeployHonorsProjectAndInfraPolicy(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "sample-app")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	headers := http.Header{}
	headers.Set("X-Yaver-HostShare", "true")
	headers.Set("X-Yaver-HostShareAllowInfra", "true")
	headers.Set("X-Yaver-HostShareAllowedProjects", "sample-app")
	res := dispatchOps(OpsContext{
		Ctx:            context.Background(),
		Caller:         "host-share",
		RequestHeaders: headers,
	}, OpsRequest{
		Machine: "auto",
		Verb:    "deploy",
		Payload: json.RawMessage(fmt.Sprintf(`{"target":"cloud","workDir":%q}`, workDir)),
	})
	if !res.OK {
		t.Fatalf("expected OK=true, got %s (%s)", res.Error, res.Code)
	}
}

// TestOpsGuestScope — verbs that don't opt in to AllowGuest must be
// refused for guest callers with the "unauthorized" code. The default
// is owner-only; opt-in AllowGuest is how we get case-by-case sharing
// without accidentally giving guests a shell.
func TestOpsGuestScope(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "guest"}
	res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "run"})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "unauthorized" {
		t.Fatalf("want code=unauthorized, got %q", res.Code)
	}
}

// TestListOpsVerbs — the registry must be deterministic (sorted) so
// mobile-headless and the CLI render a stable list, and it must
// include the verbs this cycle ships.
func TestListOpsVerbs(t *testing.T) {
	got := map[string]bool{}
	prev := ""
	for _, v := range listOpsVerbs() {
		if prev != "" && v.Name <= prev {
			t.Errorf("verbs not sorted: %q came after %q", v.Name, prev)
		}
		prev = v.Name
		got[v.Name] = true
	}
	for _, want := range []string{"info", "run"} {
		if !got[want] {
			t.Errorf("verb %q missing from registry", want)
		}
	}
}

// TestOpsBadPayload — run with malformed JSON payload returns a
// typed bad_payload error rather than a 500 or a panic.
func TestOpsBadPayload(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	// Payload is "garbage" — not valid JSON for opsRunPayload.
	res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "run", Payload: json.RawMessage(`"garbage"`)})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "bad_payload" {
		t.Fatalf("want code=bad_payload, got %q (err=%q)", res.Code, res.Error)
	}
}
