package main

import "testing"

// TestFilterTestableProjects proves the tester discovery core only surfaces
// projects in the tester's allowedProjects, tags the live dev server, and
// reflects the canVibe opt-in — never leaking projects outside the grant.
func TestFilterTestableProjects(t *testing.T) {
	tru := true
	mgr := &GuestConfigManager{
		configs: map[string]*GuestConfig{
			// Scoped to appA only, with vibe on.
			"tester": {GuestUserID: "tester", Scope: GuestScopeSDKProject, AllowedProjects: []string{"appA"}, CanVibe: &tru},
			// No project narrowing = all projects, vibe off.
			"wide": {GuestUserID: "wide", Scope: GuestScopeSDKProject},
		},
		projects: map[string][]string{},
	}
	projects := []MobileProject{
		{Name: "appA", Path: "/home/dev/Workspace/appA", Framework: "expo"},
		{Name: "appB", Path: "/home/dev/Workspace/appB", Framework: "flutter"},
	}

	// Scoped tester: only appA, dev server live for it, canVibe true.
	got := filterTestableProjects(projects, mgr, "tester", "/home/dev/Workspace/appA")
	if len(got) != 1 {
		t.Fatalf("scoped tester should see exactly 1 project, got %d: %v", len(got), got)
	}
	if got[0]["name"] != "appA" {
		t.Errorf("expected appA, got %v", got[0]["name"])
	}
	if got[0]["devServerActive"] != true {
		t.Errorf("appA dev server should be active (matches activeWorkDir)")
	}
	if got[0]["canVibe"] != true {
		t.Errorf("tester opted into vibe; canVibe should be true")
	}

	// appB must never leak to the appA-scoped tester.
	for _, p := range filterTestableProjects(projects, mgr, "tester", "") {
		if p["name"] == "appB" {
			t.Fatal("appB leaked to a tester scoped to appA")
		}
	}

	// Wide tester (no narrowing): sees both, no active server, vibe off.
	wide := filterTestableProjects(projects, mgr, "wide", "")
	if len(wide) != 2 {
		t.Fatalf("unscoped tester should see all 2 projects, got %d", len(wide))
	}
	for _, p := range wide {
		if p["devServerActive"] != false {
			t.Errorf("no active workdir → devServerActive should be false for %v", p["name"])
		}
		if p["canVibe"] != false {
			t.Errorf("wide tester didn't opt into vibe; canVibe should be false")
		}
	}

	// Nil manager / empty guest → empty, never panics.
	if len(filterTestableProjects(projects, nil, "x", "")) != 0 {
		t.Error("nil manager must return empty")
	}
	if len(filterTestableProjects(projects, mgr, "", "")) != 0 {
		t.Error("empty guestUID must return empty")
	}
}
