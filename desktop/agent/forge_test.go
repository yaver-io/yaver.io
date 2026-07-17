package main

// forge_test.go — the forge seam's pure logic (host/GHE resolution, remote
// URL parsing, role mapping) plus the REST transport against a real HTTP
// server on a random port, per the house pattern: no mocks, no network.
//
// Everything here is prefixed TestForge so it can be run scoped.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForgeResolveHost(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		kind    ForgeKind
		wantAPI string
		wantErr bool
	}{
		{name: "github.com public", host: "github.com", wantAPI: "https://api.github.com"},
		{name: "gitlab.com public", host: "gitlab.com", wantAPI: "https://gitlab.com/api/v4"},
		{name: "kind only defaults host", kind: ForgeGitHub, wantAPI: "https://api.github.com"},
		// The regression this whole field exists for: before forge.go, a GHE
		// host resolved to api.github.com and silently hit the wrong server.
		{name: "GHE gets /api/v3", host: "ghe.acme.com", kind: ForgeGitHub, wantAPI: "https://ghe.acme.com/api/v3"},
		{name: "self-hosted gitlab", host: "gitlab.acme.com", wantAPI: "https://gitlab.acme.com/api/v4"},
		{name: "scheme is stripped", host: "https://gitlab.acme.com/", wantAPI: "https://gitlab.acme.com/api/v4"},
		{name: "inferred from substring", host: "github.acme.com", wantAPI: "https://github.acme.com/api/v3"},
		// A neutral host with no kind must fail loudly rather than guess:
		// guessing wrong sends a GitLab token to a GitHub URL.
		{name: "ambiguous host errors", host: "git.acme.com", wantErr: true},
		{name: "empty host and kind errors", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveForgeHost(tc.host, tc.kind)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.APIBase != tc.wantAPI {
				t.Errorf("APIBase = %q, want %q", got.APIBase, tc.wantAPI)
			}
		})
	}
}

func TestForgeParseRepoURL(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantPath string
		wantHost string
		wantKind ForgeKind
		wantErr  bool
	}{
		{name: "https with .git", raw: "https://github.com/kivanccakmak/yaver.io.git",
			wantPath: "kivanccakmak/yaver.io", wantHost: "github.com", wantKind: ForgeGitHub},
		{name: "https without .git", raw: "https://github.com/owner/repo",
			wantPath: "owner/repo", wantHost: "github.com", wantKind: ForgeGitHub},
		{name: "scp-like", raw: "git@github.com:owner/repo.git",
			wantPath: "owner/repo", wantHost: "github.com", wantKind: ForgeGitHub},
		{name: "ssh scheme with port", raw: "ssh://git@gitlab.acme.com:2222/group/sub/repo.git",
			wantPath: "group/sub/repo", wantHost: "gitlab.acme.com", wantKind: ForgeGitLab},
		// GitLab subgroups are why ForgeRepo carries a Path, not owner+name.
		{name: "gitlab subgroup", raw: "https://gitlab.com/group/sub/deep/repo.git",
			wantPath: "group/sub/deep/repo", wantHost: "gitlab.com", wantKind: ForgeGitLab},
		{name: "not a repo url", raw: "https://github.com/", wantErr: true},
		{name: "no namespace", raw: "https://github.com/justrepo", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseForgeRepoURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Path != tc.wantPath {
				t.Errorf("Path = %q, want %q", got.Path, tc.wantPath)
			}
			if got.Host.Host != tc.wantHost {
				t.Errorf("Host = %q, want %q", got.Host.Host, tc.wantHost)
			}
			if got.Host.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", got.Host.Kind, tc.wantKind)
			}
		})
	}
}

func TestForgeRepoOwnerName(t *testing.T) {
	r := ForgeRepo{Path: "group/sub/repo"}
	if r.Owner() != "group/sub" {
		t.Errorf("Owner() = %q, want group/sub", r.Owner())
	}
	if r.Name() != "repo" {
		t.Errorf("Name() = %q, want repo", r.Name())
	}
	if got := (ForgeRepo{Path: "group/sub/repo"}).encodedPath(); got != "group%2Fsub%2Frepo" {
		t.Errorf("encodedPath() = %q, want group%%2Fsub%%2Frepo", got)
	}
}

func TestForgeNormalizeRole(t *testing.T) {
	cases := map[string]ForgeRole{
		"":           RoleWrite, // the role people mean when they don't say
		"write":      RoleWrite,
		"push":       RoleWrite, // GitHub's native spelling
		"developer":  RoleWrite, // GitLab's native spelling
		"read":       RoleRead,
		"pull":       RoleRead,
		"reporter":   RoleRead,
		"MAINTAIN":   RoleMaintain,
		"maintainer": RoleMaintain,
		"admin":      RoleAdmin,
		"owner":      RoleAdmin,
	}
	for in, want := range cases {
		got, err := normalizeForgeRole(in)
		if err != nil {
			t.Errorf("normalizeForgeRole(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeForgeRole(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := normalizeForgeRole("wizard"); err == nil {
		t.Error("expected error for unknown role")
	}
}

func TestForgeRoleMapping(t *testing.T) {
	// The two vocabularies must round-trip through the neutral one.
	if got := githubPermission(RoleRead); got != "pull" {
		t.Errorf("githubPermission(read) = %q, want pull", got)
	}
	if got := githubPermission(RoleWrite); got != "push" {
		t.Errorf("githubPermission(write) = %q, want push", got)
	}
	// read → Reporter(20), NOT Guest(10): Guest cannot read code on most
	// GitLab tiers, so mapping read→guest would grant a level that does not
	// do what the word says.
	if got := gitlabAccessLevel(RoleRead); got != glReporter {
		t.Errorf("gitlabAccessLevel(read) = %d, want %d (reporter)", got, glReporter)
	}
	if got := gitlabAccessLevel(RoleWrite); got != glDeveloper {
		t.Errorf("gitlabAccessLevel(write) = %d, want %d (developer)", got, glDeveloper)
	}
	if got := gitlabAccessLevel(RoleAdmin); got != glOwner {
		t.Errorf("gitlabAccessLevel(admin) = %d, want %d (owner)", got, glOwner)
	}
	if role, native := gitlabRoleFromLevel(30); role != RoleWrite || native != "developer" {
		t.Errorf("gitlabRoleFromLevel(30) = %q/%q, want write/developer", role, native)
	}
	if role, _ := gitlabRoleFromLevel(50); role != RoleAdmin {
		t.Errorf("gitlabRoleFromLevel(50) = %q, want admin", role)
	}
}

func TestForgeCLIExitStatus(t *testing.T) {
	cases := map[string]int{
		"gh: Not Found (HTTP 404)":          404,
		"HTTP 403: Resource not accessible": 403,
		"error connecting to host":          0,
		"HTTP 999 nonsense":                 0, // out of range → unknown
	}
	for in, want := range cases {
		if got := cliExitStatus(in); got != want {
			t.Errorf("cliExitStatus(%q) = %d, want %d", in, got, want)
		}
	}
}

// --- REST transport + GitHub/GitLab impls against a real server ---

// newForgeTestServer stands up a real HTTP server and returns a githubForge
// wired to it, so the impl is exercised end-to-end over the wire.
func newForgeTestGitHub(t *testing.T, h http.HandlerFunc) *githubForge {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &githubForge{
		host: ForgeHost{Host: "ghe.test", Kind: ForgeGitHub, APIBase: srv.URL},
		t:    &forgeRESTTransport{apiBase: srv.URL, token: "t0ken", kind: ForgeGitHub},
	}
}

func TestForgeGitHubInviteSendsCorrectRequest(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]any
	f := newForgeTestGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":42,"html_url":"https://ghe.test/inv/42","invitee":{"login":"alice"}}`))
	})

	inv, err := f.InviteMember(context.Background(),
		ForgeRepo{Path: "acme/widget"}, "alice", RoleWrite)
	if err != nil {
		t.Fatalf("InviteMember: %v", err)
	}
	if gotMethod != "PUT" {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/repos/acme/widget/collaborators/alice" {
		t.Errorf("path = %s", gotPath)
	}
	if gotAuth != "Bearer t0ken" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody["permission"] != "push" {
		t.Errorf("permission = %v, want push", gotBody["permission"])
	}
	if inv.State != "invited" {
		t.Errorf("state = %q, want invited", inv.State)
	}
}

// A 204 means "already had access" — GitHub sends no invitation. Reporting
// "invited" there would promise an email that never arrives.
func TestForgeGitHubInviteAlreadyMemberReportsAdded(t *testing.T) {
	f := newForgeTestGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	inv, err := f.InviteMember(context.Background(), ForgeRepo{Path: "acme/widget"}, "bob", RoleRead)
	if err != nil {
		t.Fatalf("InviteMember: %v", err)
	}
	if inv.State != "added" {
		t.Errorf("state = %q, want added", inv.State)
	}
	if !strings.Contains(inv.Message, "no invitation email sent") {
		t.Errorf("message should say no email was sent, got %q", inv.Message)
	}
}

// GitHub returns 404 both for "no such user" and "you lack admin". The error
// must say so rather than parroting a bare 404.
func TestForgeGitHubInvite404ExplainsBothCauses(t *testing.T) {
	f := newForgeTestGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	})
	_, err := f.InviteMember(context.Background(), ForgeRepo{Path: "acme/widget"}, "ghost", RoleWrite)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "admin") {
		t.Errorf("404 error should mention the permission cause, got: %v", err)
	}
}

func TestForgeGitHubRejectsEmail(t *testing.T) {
	f := newForgeTestGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("must not reach the API for an email address")
	})
	_, err := f.InviteMember(context.Background(), ForgeRepo{Path: "a/b"}, "alice@acme.com", RoleWrite)
	if err == nil || !strings.Contains(err.Error(), "username") {
		t.Errorf("want a username-not-email error, got: %v", err)
	}
}

// Pending invitations must appear in the list, or invite-then-list looks
// like a silent failure.
func TestForgeGitHubListMergesPendingInvites(t *testing.T) {
	f := newForgeTestGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/collaborators"):
			_, _ = w.Write([]byte(`[{"login":"alice","id":1,"role_name":"admin"}]`))
		case strings.HasSuffix(r.URL.Path, "/invitations"):
			_, _ = w.Write([]byte(`[{"id":9,"permissions":"push","invitee":{"login":"bob"}}]`))
		default:
			w.WriteHeader(404)
		}
	})
	members, err := f.ListMembers(context.Background(), ForgeRepo{Path: "acme/widget"})
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2 (1 active + 1 pending)", len(members))
	}
	if members[0].Username != "alice" || members[0].State != "active" || members[0].Role != RoleAdmin {
		t.Errorf("active member wrong: %+v", members[0])
	}
	if members[1].Username != "bob" || members[1].State != "pending" || members[1].Role != RoleWrite {
		t.Errorf("pending member wrong: %+v", members[1])
	}
}

// Reading invitations needs admin; a 403 there must not fail the whole list.
func TestForgeGitHubListSurvivesForbiddenInvitations(t *testing.T) {
	f := newForgeTestGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/invitations") {
			w.WriteHeader(403)
			return
		}
		_, _ = w.Write([]byte(`[{"login":"alice","id":1,"role_name":"read"}]`))
	})
	members, err := f.ListMembers(context.Background(), ForgeRepo{Path: "acme/widget"})
	if err != nil {
		t.Fatalf("a 403 on invitations must not fail the list: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("got %d members, want 1", len(members))
	}
}

func newForgeTestGitLab(t *testing.T, h http.HandlerFunc) *gitlabForge {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &gitlabForge{
		host: ForgeHost{Host: "gitlab.test", Kind: ForgeGitLab, APIBase: srv.URL},
		t:    &forgeRESTTransport{apiBase: srv.URL, token: "t0ken", kind: ForgeGitLab},
	}
}

// GitLab needs a username→id lookup before it can add a member.
func TestForgeGitLabInviteByUsernameLooksUpID(t *testing.T) {
	var memberBody map[string]any
	f := newForgeTestGitLab(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/users"):
			if r.URL.Query().Get("username") != "alice" {
				t.Errorf("lookup query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"id":77,"username":"alice","name":"Alice"}]`))
		case strings.HasSuffix(r.URL.Path, "/members"):
			_ = json.NewDecoder(r.Body).Decode(&memberBody)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":77,"username":"alice","access_level":30}`))
		default:
			w.WriteHeader(404)
		}
	})
	inv, err := f.InviteMember(context.Background(), ForgeRepo{Path: "grp/proj"}, "alice", RoleWrite)
	if err != nil {
		t.Fatalf("InviteMember: %v", err)
	}
	if memberBody["user_id"] != float64(77) {
		t.Errorf("user_id = %v, want 77", memberBody["user_id"])
	}
	if memberBody["access_level"] != float64(glDeveloper) {
		t.Errorf("access_level = %v, want %d", memberBody["access_level"], glDeveloper)
	}
	if inv.State != "added" {
		t.Errorf("state = %q, want added", inv.State)
	}
}

// An email goes to /invitations, not through a user lookup that would fail.
func TestForgeGitLabInviteByEmailUsesInvitations(t *testing.T) {
	var hitUsers bool
	f := newForgeTestGitLab(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/users") {
			hitUsers = true
		}
		if strings.HasSuffix(r.URL.Path, "/invitations") {
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"status":"success"}`))
			return
		}
		w.WriteHeader(404)
	})
	inv, err := f.InviteMember(context.Background(), ForgeRepo{Path: "grp/proj"}, "new@acme.com", RoleRead)
	if err != nil {
		t.Fatalf("InviteMember: %v", err)
	}
	if hitUsers {
		t.Error("must not look up a user for an email invite")
	}
	if inv.State != "invited" || inv.Email != "new@acme.com" {
		t.Errorf("unexpected invite: %+v", inv)
	}
}

// GitLab returns 201 with {"status":"error"} for a rejected email invite.
// Treating that as success is the trap this guards.
func TestForgeGitLabEmailInviteErrorStatusIsFailure(t *testing.T) {
	f := newForgeTestGitLab(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"status":"error","message":{"new@acme.com":"Already invited"}}`))
	})
	_, err := f.InviteMember(context.Background(), ForgeRepo{Path: "grp/proj"}, "new@acme.com", RoleRead)
	if err == nil {
		t.Fatal("a 201 with status=error must be reported as a failure")
	}
	if !strings.Contains(err.Error(), "Already invited") {
		t.Errorf("error should carry GitLab's message, got: %v", err)
	}
}

// 409 = already a member = the goal state, not an error.
func TestForgeGitLabInviteConflictIsAlreadyMember(t *testing.T) {
	f := newForgeTestGitLab(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/users") {
			_, _ = w.Write([]byte(`[{"id":5,"username":"alice"}]`))
			return
		}
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"message":"Member already exists"}`))
	})
	inv, err := f.InviteMember(context.Background(), ForgeRepo{Path: "grp/proj"}, "alice", RoleWrite)
	if err != nil {
		t.Fatalf("409 should not be an error: %v", err)
	}
	if inv.State != "already_member" {
		t.Errorf("state = %q, want already_member", inv.State)
	}
}

// A subgroup project must be URL-encoded into the path.
func TestForgeGitLabEncodesSubgroupPath(t *testing.T) {
	var gotPath string
	f := newForgeTestGitLab(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := f.ListMembers(context.Background(), ForgeRepo{Path: "grp/sub/proj"}); err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if !strings.Contains(gotPath, "grp%2Fsub%2Fproj") {
		t.Errorf("path = %q, want an encoded subgroup path", gotPath)
	}
}

// Removing an inherited member 404s at the API; the error must explain why
// rather than claiming the user doesn't exist.
func TestForgeGitLabRemoveInheritedExplains(t *testing.T) {
	f := newForgeTestGitLab(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/users") {
			_, _ = w.Write([]byte(`[{"id":5,"username":"alice"}]`))
			return
		}
		w.WriteHeader(404)
	})
	err := f.RemoveMember(context.Background(), ForgeRepo{Path: "grp/proj"}, "alice")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "parent group") {
		t.Errorf("error should explain group inheritance, got: %v", err)
	}
}
