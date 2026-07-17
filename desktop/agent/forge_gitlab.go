package main

// forge_gitlab.go — GitLab implementation of the Forge interface.
//
// Two things differ structurally from GitHub and drive the shape here:
//
//  1. GitLab identifies members by numeric user_id, not username, so an
//     invite-by-username costs an extra lookup.
//  2. GitLab has a real email-invite path (/invitations) for people who
//     don't have an account yet. GitHub has no repo-level equivalent.
//
// Access levels are integers (10/20/30/40/50) rather than names.

import (
	"context"
	"fmt"
	"strings"
)

type gitlabForge struct {
	host ForgeHost
	t    forgeTransport
}

func (f *gitlabForge) Kind() ForgeKind { return ForgeGitLab }
func (f *gitlabForge) Host() ForgeHost { return f.host }
func (f *gitlabForge) Via() string     { return f.t.name() }

// GitLab access levels. Named rather than inlined because a bare 30 in a
// request body is unreadable at the call site.
const (
	glGuest      = 10
	glReporter   = 20
	glDeveloper  = 30
	glMaintainer = 40
	glOwner      = 50
)

// gitlabAccessLevel maps a neutral role to GitLab's integer scale.
//
// read → Reporter (20), not Guest (10), on purpose: Guest cannot read
// repository code on most GitLab tiers, so mapping "read" to Guest would
// grant an access level that doesn't do what the word says.
func gitlabAccessLevel(r ForgeRole) int {
	switch r {
	case RoleRead, RoleTriage:
		return glReporter
	case RoleMaintain:
		return glMaintainer
	case RoleAdmin:
		return glOwner
	default:
		return glDeveloper
	}
}

func gitlabRoleFromLevel(lvl int) (ForgeRole, string) {
	switch {
	case lvl >= glOwner:
		return RoleAdmin, "owner"
	case lvl >= glMaintainer:
		return RoleMaintain, "maintainer"
	case lvl >= glDeveloper:
		return RoleWrite, "developer"
	case lvl >= glReporter:
		return RoleRead, "reporter"
	default:
		return RoleRead, "guest"
	}
}

type glMember struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Name        string `json:"name"`
	State       string `json:"state"`
	AvatarURL   string `json:"avatar_url"`
	WebURL      string `json:"web_url"`
	AccessLevel int    `json:"access_level"`
}

type glInvitation struct {
	InviteEmail string `json:"invite_email"`
	AccessLevel int    `json:"access_level"`
	UserName    string `json:"user_name"`
}

type glUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// ListMembers returns everyone with access, including members inherited
// from parent groups (members/all rather than members).
//
// Inherited members are included because the question a user is asking is
// "who can see this repo", and the direct-only list answers a narrower
// question that's usually misleading in a group-owned project. The tradeoff:
// RemoveMember on an inherited member will fail at the API, since the
// membership lives on the group.
func (f *gitlabForge) ListMembers(ctx context.Context, repo ForgeRepo) ([]ForgeMember, error) {
	var members []glMember
	if err := f.t.do(ctx, "GET",
		fmt.Sprintf("projects/%s/members/all?per_page=100", repo.encodedPath()), nil, &members); err != nil {
		return nil, err
	}
	out := make([]ForgeMember, 0, len(members))
	for _, m := range members {
		role, native := gitlabRoleFromLevel(m.AccessLevel)
		state := m.State
		if state == "" {
			state = "active"
		}
		out = append(out, ForgeMember{
			Username:   m.Username,
			Name:       m.Name,
			Role:       role,
			NativeRole: native,
			State:      state,
			AvatarURL:  m.AvatarURL,
			ProfileURL: m.WebURL,
			ID:         m.ID,
		})
	}

	// Pending email invitations for people without accounts yet. Needs
	// maintainer; a failure here is not fatal for the same reason as GitHub's.
	var invites []glInvitation
	if err := f.t.do(ctx, "GET",
		fmt.Sprintf("projects/%s/invitations?per_page=100", repo.encodedPath()), nil, &invites); err == nil {
		for _, iv := range invites {
			role, native := gitlabRoleFromLevel(iv.AccessLevel)
			out = append(out, ForgeMember{
				Username:   iv.InviteEmail,
				Name:       iv.UserName,
				Role:       role,
				NativeRole: native,
				State:      "pending",
			})
		}
	}
	return out, nil
}

// lookupUserID resolves a username to GitLab's numeric ID.
func (f *gitlabForge) lookupUserID(ctx context.Context, username string) (glUser, error) {
	var users []glUser
	if err := f.t.do(ctx, "GET", "users?username="+username, nil, &users); err != nil {
		return glUser{}, err
	}
	for _, u := range users {
		if strings.EqualFold(u.Username, username) {
			return u, nil
		}
	}
	return glUser{}, fmt.Errorf("no GitLab user named %q on %s", username, f.host.Host)
}

// InviteMember adds a member by username, or emails an invitation.
//
// The email path is genuinely different from the username path — it invites
// someone who may not have an account at all — so it gets its own endpoint
// rather than being forced through a lookup that would fail.
func (f *gitlabForge) InviteMember(ctx context.Context, repo ForgeRepo, userOrEmail string, role ForgeRole) (ForgeInvite, error) {
	target := strings.TrimSpace(userOrEmail)
	if target == "" {
		return ForgeInvite{}, fmt.Errorf("username or email is required")
	}
	level := gitlabAccessLevel(role)
	_, native := gitlabRoleFromLevel(level)

	if strings.Contains(target, "@") {
		var resp struct {
			Status  string `json:"status"`
			Message any    `json:"message"`
		}
		if err := f.t.do(ctx, "POST",
			fmt.Sprintf("projects/%s/invitations", repo.encodedPath()),
			map[string]any{"email": target, "access_level": level}, &resp); err != nil {
			return ForgeInvite{}, err
		}
		// GitLab returns 201 with {"status":"error","message":{...}} for
		// per-email failures — a 201 here does not mean it worked.
		if strings.EqualFold(resp.Status, "error") {
			return ForgeInvite{}, fmt.Errorf("GitLab rejected the invite for %s: %v", target, resp.Message)
		}
		return ForgeInvite{
			Email:   target,
			Role:    role,
			State:   "invited",
			Message: fmt.Sprintf("emailed an invitation to %s for %s as %s", target, repo.Path, native),
		}, nil
	}

	u, err := f.lookupUserID(ctx, target)
	if err != nil {
		return ForgeInvite{}, err
	}
	var m glMember
	err = f.t.do(ctx, "POST",
		fmt.Sprintf("projects/%s/members", repo.encodedPath()),
		map[string]any{"user_id": u.ID, "access_level": level}, &m)
	if err != nil {
		// 409 = already a member. That's the goal state, not a failure.
		if forgeErrStatus(err) == 409 {
			return ForgeInvite{
				Username: target,
				Role:     role,
				State:    "already_member",
				InviteID: u.ID,
				Message:  fmt.Sprintf("%s is already a member of %s", target, repo.Path),
			}, nil
		}
		return ForgeInvite{}, err
	}
	return ForgeInvite{
		Username: target,
		Role:     role,
		State:    "added",
		InviteID: u.ID,
		URL:      m.WebURL,
		Message:  fmt.Sprintf("added %s to %s as %s", target, repo.Path, native),
	}, nil
}

// RemoveMember revokes access by username.
func (f *gitlabForge) RemoveMember(ctx context.Context, repo ForgeRepo, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username is required")
	}
	u, err := f.lookupUserID(ctx, username)
	if err != nil {
		return err
	}
	err = f.t.do(ctx, "DELETE",
		fmt.Sprintf("projects/%s/members/%d", repo.encodedPath(), u.ID), nil, nil)
	if err != nil && forgeErrStatus(err) == 404 {
		return fmt.Errorf("%s is not a direct member of %s — they may inherit access from a parent group, which must be changed on the group", username, repo.Path)
	}
	return err
}
