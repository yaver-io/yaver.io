package main

// forge_github.go — GitHub implementation of the Forge interface.
//
// Everything here is about GitHub's API shape only. Auth, transport choice,
// GHE base URLs, and role vocabulary live in forge.go, so this file has no
// idea whether it is talking through `gh api` or raw REST.

import (
	"context"
	"fmt"
	"strings"
)

type githubForge struct {
	host ForgeHost
	t    forgeTransport
}

func (f *githubForge) Kind() ForgeKind { return ForgeGitHub }
func (f *githubForge) Host() ForgeHost { return f.host }
func (f *githubForge) Via() string     { return f.t.name() }

// githubPermission maps a neutral role to GitHub's `permission` field.
func githubPermission(r ForgeRole) string {
	switch r {
	case RoleRead:
		return "pull"
	case RoleTriage:
		return "triage"
	case RoleMaintain:
		return "maintain"
	case RoleAdmin:
		return "admin"
	default:
		return "push"
	}
}

// githubRoleFromName maps GitHub's role_name back to a neutral role.
func githubRoleFromName(name string) ForgeRole {
	switch strings.ToLower(name) {
	case "read", "pull":
		return RoleRead
	case "triage":
		return RoleTriage
	case "maintain":
		return RoleMaintain
	case "admin":
		return RoleAdmin
	default:
		return RoleWrite
	}
}

type ghCollaborator struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
	RoleName  string `json:"role_name"`
}

type ghInvitation struct {
	ID      int64 `json:"id"`
	Invitee struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
		HTMLURL   string `json:"html_url"`
	} `json:"invitee"`
	Permissions string `json:"permissions"`
	HTMLURL     string `json:"html_url"`
}

// ListMembers returns active collaborators plus pending invitations.
//
// Pending invites are included deliberately: without them, inviting someone
// and immediately listing shows nothing, which reads as a silent failure.
// They're distinguished by State rather than omitted.
func (f *githubForge) ListMembers(ctx context.Context, repo ForgeRepo) ([]ForgeMember, error) {
	var collabs []ghCollaborator
	if err := f.t.do(ctx, "GET", fmt.Sprintf("repos/%s/collaborators?per_page=100", repo.Path), nil, &collabs); err != nil {
		return nil, err
	}
	out := make([]ForgeMember, 0, len(collabs))
	for _, c := range collabs {
		out = append(out, ForgeMember{
			Username:   c.Login,
			Name:       c.Name,
			Role:       githubRoleFromName(c.RoleName),
			NativeRole: c.RoleName,
			State:      "active",
			AvatarURL:  c.AvatarURL,
			ProfileURL: c.HTMLURL,
			ID:         c.ID,
		})
	}

	// Pending invitations are a separate endpoint. A 403 here is common and
	// benign — reading invitations needs admin on the repo while reading
	// collaborators does not. Degrade to the active list rather than failing
	// the whole call over a list we couldn't see.
	var invites []ghInvitation
	if err := f.t.do(ctx, "GET", fmt.Sprintf("repos/%s/invitations?per_page=100", repo.Path), nil, &invites); err == nil {
		for _, iv := range invites {
			out = append(out, ForgeMember{
				Username:   iv.Invitee.Login,
				Role:       githubRoleFromName(iv.Permissions),
				NativeRole: iv.Permissions,
				State:      "pending",
				AvatarURL:  iv.Invitee.AvatarURL,
				ProfileURL: iv.Invitee.HTMLURL,
				ID:         iv.ID,
			})
		}
	}
	return out, nil
}

// InviteMember adds a collaborator to a repo.
//
// GitHub has no email-invite path for repo collaborators (that's an org-level
// feature), so an email here is a caller error worth naming explicitly rather
// than letting the API return a confusing 404 on a username that looks like
// an address.
func (f *githubForge) InviteMember(ctx context.Context, repo ForgeRepo, userOrEmail string, role ForgeRole) (ForgeInvite, error) {
	user := strings.TrimSpace(userOrEmail)
	if user == "" {
		return ForgeInvite{}, fmt.Errorf("username is required")
	}
	if strings.Contains(user, "@") {
		return ForgeInvite{}, fmt.Errorf("GitHub invites repo collaborators by username, not email — pass the GitHub username for %s", user)
	}

	var iv ghInvitation
	err := f.t.do(ctx, "PUT",
		fmt.Sprintf("repos/%s/collaborators/%s", repo.Path, user),
		map[string]any{"permission": githubPermission(role)}, &iv)
	if err != nil {
		if st := forgeErrStatus(err); st == 404 {
			return ForgeInvite{}, fmt.Errorf("cannot invite %s to %s — either the user doesn't exist or you lack admin on the repo (GitHub returns 404 for both)", user, repo.Path)
		}
		return ForgeInvite{}, err
	}

	// 204 (empty body) means they already had access, or the repo is in an
	// org where they're already a member — GitHub applies the permission and
	// returns no invitation. Report that honestly instead of claiming we
	// sent an invite that nobody will receive.
	if iv.ID == 0 {
		return ForgeInvite{
			Username: user,
			Role:     role,
			State:    "added",
			Message:  fmt.Sprintf("%s already had access to %s; permission set to %s (no invitation email sent)", user, repo.Path, githubPermission(role)),
		}, nil
	}
	return ForgeInvite{
		Username: user,
		Role:     role,
		State:    "invited",
		InviteID: iv.ID,
		URL:      iv.HTMLURL,
		Message:  fmt.Sprintf("invited %s to %s as %s — pending their acceptance", user, repo.Path, githubPermission(role)),
	}, nil
}

// RemoveMember revokes access. GitHub's DELETE is idempotent (204 whether or
// not they were a collaborator), so this is safe to call twice.
func (f *githubForge) RemoveMember(ctx context.Context, repo ForgeRepo, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username is required")
	}
	return f.t.do(ctx, "DELETE", fmt.Sprintf("repos/%s/collaborators/%s", repo.Path, username), nil, nil)
}
