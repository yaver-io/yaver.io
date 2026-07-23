package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests exist because the enforcement they cover was ADVERTISED and
// ABSENT for months (2026-07-23 multiplayer audit). backend/convex/
// projectShares.ts claimed "branch-pin / PR-only / deploy-gate for 'normie'
// are enforced agent-side off membership.role" while the agent had no role
// field at all, and scopeForRole() mapped "dev" and "normie" to the same
// scope="full" — so a host who picked the restricted role got an unrestricted
// teammate.
//
// TestDeployShipRefusesGuestWhoseProjectRoleCannotDeploy is the one that
// matters: it drives the real handler, so deleting the gate in deploy_run.go
// fails the build's tests rather than quietly restoring the old false green.
// A unit test of GuestCanDeployProject alone would keep passing.

func guestMgrWithRole(guestID string, roles []GuestProjectRole) *GuestConfigManager {
	return &GuestConfigManager{configs: map[string]*GuestConfig{
		guestID: {GuestUserID: guestID, Scope: GuestScopeFull, ProjectRoles: roles},
	}}
}

func TestProjectRoleLookupIsCaseInsensitiveAndScopedToProject(t *testing.T) {
	no := false
	mgr := guestMgrWithRole("g1", []GuestProjectRole{
		{Project: "API", Role: "dev"},
		{Project: "marketing-site", Role: "normie", CanDeploy: &no},
	})

	if got := mgr.ProjectRole("g1", "api"); got == nil || got.Role != "dev" {
		t.Fatalf("expected case-insensitive match on 'api', got %+v", got)
	}
	if got := mgr.ProjectRole("g1", "  marketing-site "); got == nil || got.Role != "normie" {
		t.Fatalf("expected whitespace-trimmed match, got %+v", got)
	}
	if got := mgr.ProjectRole("g1", "some-other-repo"); got != nil {
		t.Fatalf("a project with no membership must not inherit another project's role, got %+v", got)
	}
	if got := mgr.ProjectRole("g1", ""); got != nil {
		t.Fatalf("empty project must not match any role, got %+v", got)
	}
}

func TestGuestCanDeployDefaultsOpenForGrantsWithoutRoles(t *testing.T) {
	// Legacy grants (plain `yaver guests invite`, host-share, support link)
	// carry no projectRoles. They must keep behaving exactly as before —
	// a silent mid-session downgrade would be its own incident.
	mgr := &GuestConfigManager{configs: map[string]*GuestConfig{
		"legacy": {GuestUserID: "legacy", Scope: GuestScopeFull},
	}}
	if ok, reason := mgr.GuestCanDeployProject("legacy", "api"); !ok {
		t.Fatalf("legacy grant must not be downgraded, got refusal: %s", reason)
	}

	// A role entry that carries no canDeploy flag is equally unrestricted.
	mgr = guestMgrWithRole("g1", []GuestProjectRole{{Project: "api", Role: "dev"}})
	if ok, reason := mgr.GuestCanDeployProject("g1", "api"); !ok {
		t.Fatalf("absent flag means unrestricted, got refusal: %s", reason)
	}
}

func TestGuestCanDeployHonoursExplicitFlags(t *testing.T) {
	no, yes := false, true
	mgr := guestMgrWithRole("g1", []GuestProjectRole{
		{Project: "api", Role: "dev", CanDeploy: &yes},
		{Project: "site", Role: "normie", CanDeploy: &no},
	})

	if ok, _ := mgr.GuestCanDeployProject("g1", "api"); !ok {
		t.Fatal("dev with canDeploy=true must be allowed")
	}
	ok, reason := mgr.GuestCanDeployProject("g1", "site")
	if ok {
		t.Fatal("normie with canDeploy=false must be refused")
	}
	// The refusal must name the role, not just say no. A guest told "403"
	// files a bug; a guest told which role they hold asks for a better one.
	if !strings.Contains(reason, "normie") {
		t.Fatalf("refusal must carry the role label so the guest knows what to ask for, got %q", reason)
	}
}

func TestGuestPushAndBranchFlagsReadBack(t *testing.T) {
	no := false
	branch := "yaver/ayse"
	mgr := guestMgrWithRole("g1", []GuestProjectRole{{
		Project:            "api",
		Role:               "normie",
		CanPush:            &no,
		RequirePullRequest: &no,
		PinnedBranch:       &branch,
	}})

	if mgr.GuestCanPushProject("g1", "api") {
		t.Fatal("canPush=false must read back as false")
	}
	if mgr.GuestRequiresPullRequest("g1", "api") {
		t.Fatal("requirePullRequest=false must read back as false")
	}
	if got := mgr.GuestPinnedBranch("g1", "api"); got != branch {
		t.Fatalf("pinned branch = %q, want %q", got, branch)
	}
	// Unrestricted defaults for a project with no entry.
	if !mgr.GuestCanPushProject("g1", "other") {
		t.Fatal("no entry must default to unrestricted push")
	}
	if mgr.GuestPinnedBranch("g1", "other") != "" {
		t.Fatal("no entry must mean no branch pin")
	}
}

// The enforcement test. Drives the real /deploy/ship handler as a guest.
func TestDeployShipRefusesGuestWhoseProjectRoleCannotDeploy(t *testing.T) {
	no := false
	srv := &HTTPServer{
		token: "t",
		guestConfigMgr: guestMgrWithRole("g1", []GuestProjectRole{
			{Project: "site", Role: "normie", CanDeploy: &no},
		}),
	}

	body := `{"app":"site","target":"cloudflare"}`
	req := httptest.NewRequest("POST", "/deploy/ship", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("X-Yaver-Guest", "true")
	req.Header.Set("X-Yaver-GuestUserID", "g1")
	w := httptest.NewRecorder()
	srv.handleDeployShip(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403 for a role that cannot deploy, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "normie") {
		t.Fatalf("403 body must explain which role was refused, got %s", w.Body.String())
	}
}

// The counterpart: a role that MAY deploy must get past the role gate. It can
// still fail later for unrelated reasons (no such project on this host), so
// this asserts only that the refusal is not the role one — otherwise a gate
// that refused everybody would pass the test above and look correct.
func TestDeployShipAllowsGuestWhoseProjectRoleCanDeploy(t *testing.T) {
	yes := true
	srv := &HTTPServer{
		token: "t",
		guestConfigMgr: guestMgrWithRole("g1", []GuestProjectRole{
			{Project: "site", Role: "dev", CanDeploy: &yes},
		}),
	}

	body := `{"app":"site","target":"cloudflare"}`
	req := httptest.NewRequest("POST", "/deploy/ship", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("X-Yaver-Guest", "true")
	req.Header.Set("X-Yaver-GuestUserID", "g1")
	w := httptest.NewRecorder()
	srv.handleDeployShip(w, req)

	if strings.Contains(w.Body.String(), "cannot deploy") {
		t.Fatalf("a deploy-capable role must not be refused by the role gate, got %d: %s", w.Code, w.Body.String())
	}
}
