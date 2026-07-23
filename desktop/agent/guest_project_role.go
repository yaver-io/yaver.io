package main

// guest_project_role.go — per-project collaboration permissions for a guest.
//
// Incident this closes (2026-07-23 multiplayer audit):
//
//   - backend/convex/projectShares.ts advertised four collaboration roles
//     (owner / dev / normie / viewer) in the UI on web AND mobile, and its
//     header comment claimed "branch-pin / PR-only / deploy-gate for 'normie'
//     are enforced agent-side off membership.role".
//   - Nothing role-shaped was ever sent to the agent. GuestConfig had scope,
//     allowedProjects, canVibe — no role, no capabilities. And scopeForRole()
//     collapsed BOTH "dev" and "normie" onto scope="full" before the agent
//     saw anything, so the two were indistinguishable at the only layer that
//     enforces.
//   - Net effect: a host who deliberately picked the restricted role got an
//     unrestricted teammate, and the product told them otherwise. A false
//     green in the worst place — an access-control promise.
//
// The fix is NOT a role→permission table in this file. Roles are presets
// resolved server-side (projectShares.ts::roleCapabilityPreset) into explicit
// capability flags that travel on the grant; this file only reads flags. That
// boundary is deliberate: hardcoding "normie cannot deploy" into the agent
// would freeze one opinion of what a role means into every user's install, and
// widening it for a single host would require an agent release. Yaver is not
// single-user, and a policy compiled into the binary is a policy nobody can
// change.
//
// Enforced today: CanDeploy (deploy_run.go).
// Carried and readable, NOT yet enforced: CanPush, RequirePullRequest,
// PinnedBranch — see the TODO at the bottom of this file. They are listed
// there rather than claimed in a comment, because claiming enforcement that
// does not exist is the bug this file was written to fix.

import "strings"

// GuestProjectRole is one member's effective permissions on one shared project.
//
// Every capability is a *bool, and nil means "not restricted". That default is
// deliberate: legacy grants written before this field existed carry no flags,
// and must keep behaving exactly as they did rather than being silently
// downgraded mid-session. Same reasoning as the legacy scope default in
// guest_scope.go. A restriction only ever exists because Convex wrote it.
type GuestProjectRole struct {
	Project string `json:"project"`
	// Role is a LABEL — the preset these flags came from, kept for audit
	// messages and UI. It is never consulted to decide access; the flags are.
	Role               string  `json:"role,omitempty"`
	CanDeploy          *bool   `json:"canDeploy,omitempty"`
	CanPush            *bool   `json:"canPush,omitempty"`
	RequirePullRequest *bool   `json:"requirePullRequest,omitempty"`
	PinnedBranch       *string `json:"pinnedBranch,omitempty"`
}

// ProjectRole returns the permissions this guest holds on `project`, or nil
// when the grant carries none (legacy row, or a guest that arrived by a path
// other than projectShares — a plain `yaver guests invite`, a host-share, a
// support link). nil means "no per-project restrictions", NOT "denied": the
// scope allow-list in guest_scope.go remains the gate for those callers.
func (m *GuestConfigManager) ProjectRole(guestUserID, project string) *GuestProjectRole {
	if m == nil {
		return nil
	}
	cfg := m.GetConfig(guestUserID)
	if cfg == nil || len(cfg.ProjectRoles) == 0 {
		return nil
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return nil
	}
	for i := range cfg.ProjectRoles {
		if strings.EqualFold(strings.TrimSpace(cfg.ProjectRoles[i].Project), project) {
			return &cfg.ProjectRoles[i]
		}
	}
	return nil
}

// GuestCanDeployProject answers whether this guest may trigger a deploy for
// `project`. True when no per-project role applies (unchanged legacy
// behavior) or when the role explicitly permits it.
//
// The second return is a human-readable reason for the refusal, carrying the
// role label so the error says *why* rather than "forbidden" — a guest who is
// told "your role on this project is 'normie', which cannot deploy" knows to
// ask the owner for 'dev'; a guest told "403" files a bug.
func (m *GuestConfigManager) GuestCanDeployProject(guestUserID, project string) (bool, string) {
	role := m.ProjectRole(guestUserID, project)
	if role == nil || role.CanDeploy == nil {
		return true, ""
	}
	if *role.CanDeploy {
		return true, ""
	}
	label := strings.TrimSpace(role.Role)
	if label == "" {
		label = "restricted"
	}
	return false, "your role on this project (" + label + ") cannot deploy — ask the project owner to change your role"
}

// GuestPinnedBranch returns the branch this guest's work is pinned to on
// `project`, or "" when unpinned.
func (m *GuestConfigManager) GuestPinnedBranch(guestUserID, project string) string {
	role := m.ProjectRole(guestUserID, project)
	if role == nil || role.PinnedBranch == nil {
		return ""
	}
	return strings.TrimSpace(*role.PinnedBranch)
}

// GuestRequiresPullRequest reports whether this guest's changes on `project`
// must land as a pull request rather than a direct push.
func (m *GuestConfigManager) GuestRequiresPullRequest(guestUserID, project string) bool {
	role := m.ProjectRole(guestUserID, project)
	if role == nil || role.RequirePullRequest == nil {
		return false
	}
	return *role.RequirePullRequest
}

// GuestCanPushProject reports whether this guest may push directly on
// `project`. Defaults to true (unrestricted) when no flag is carried.
func (m *GuestConfigManager) GuestCanPushProject(guestUserID, project string) bool {
	role := m.ProjectRole(guestUserID, project)
	if role == nil || role.CanPush == nil {
		return true
	}
	return *role.CanPush
}

// TODO(multiplayer-audit): CanPush / RequirePullRequest / PinnedBranch are
// plumbed end-to-end and readable above, but no git seam consults them yet.
// The enforcement points are the push paths in git_pr.go and managed_git.go.
// Until that lands, do NOT describe them as enforced in any doc, comment, or
// UI string — the whole reason this file exists is that someone did exactly
// that. When wiring them, add the refusal test alongside, in the same change.
