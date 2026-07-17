package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// forge_surface.go — provider-neutral forge verbs for both MCP and ops.
//
// The forge seam already knows how to resolve a repo, choose CLI-vs-REST,
// and speak GitHub or GitLab. This file exposes that seam on the two public
// control surfaces that need it: MCP tools and ops verbs.

type forgeSurfacePayload struct {
	Repo      string `json:"repo,omitempty"`
	Directory string `json:"directory,omitempty"`
	Host      string `json:"host,omitempty"`
	Kind      string `json:"kind,omitempty"`
	User      string `json:"user,omitempty"`
	Role      string `json:"role,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_members",
		Description: "List who can access a git forge repo. Payload {repo?, directory?, host?, kind?}. Resolves explicit repo > directory > cwd, and reports whether the call used gh/glab or direct REST.",
		Schema:      forgeRepoOnlySchema(),
		Handler:     gitMembersOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "git_member_invite",
		Description: "Invite a collaborator to a git forge repo. Payload {user, role?, repo?, directory?, host?, kind?}. Resolves explicit repo > directory > cwd, and reports whether the call used gh/glab or direct REST.",
		Schema:      forgeMemberInviteSchema(),
		Handler:     gitMemberInviteOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "git_member_remove",
		Description: "Remove a collaborator from a git forge repo. Payload {user, repo?, directory?, host?, kind?}. Resolves explicit repo > directory > cwd, and reports whether the call used gh/glab or direct REST.",
		Schema:      forgeMemberRemoveSchema(),
		Handler:     gitMemberRemoveOpsHandler,
	})
}

func forgeRepoOnlySchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"repo":      map[string]interface{}{"type": "string"},
			"directory": map[string]interface{}{"type": "string"},
			"host":      map[string]interface{}{"type": "string"},
			"kind":      map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab"}},
		},
		"additionalProperties": false,
	}
}

func forgeMemberInviteSchema() map[string]interface{} {
	schema := forgeRepoOnlySchema()
	schema["required"] = []string{"user"}
	props := schema["properties"].(map[string]interface{})
	props["user"] = map[string]interface{}{"type": "string"}
	props["role"] = map[string]interface{}{"type": "string", "enum": []string{"read", "triage", "write", "maintain", "admin"}}
	return schema
}

func forgeMemberRemoveSchema() map[string]interface{} {
	schema := forgeRepoOnlySchema()
	schema["required"] = []string{"user"}
	props := schema["properties"].(map[string]interface{})
	props["user"] = map[string]interface{}{"type": "string"}
	return schema
}

func parseForgeSurfacePayload(payload json.RawMessage) (forgeSurfacePayload, error) {
	var p forgeSurfacePayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (p forgeSurfacePayload) resolveRepo() (ForgeRepo, error) {
	var kind ForgeKind
	switch strings.ToLower(strings.TrimSpace(p.Kind)) {
	case "":
	case string(ForgeGitHub):
		kind = ForgeGitHub
	case string(ForgeGitLab):
		kind = ForgeGitLab
	default:
		return ForgeRepo{}, fmt.Errorf("kind must be github or gitlab")
	}
	return resolveForgeRepo(p.Repo, p.Directory, kind, p.Host)
}

func (p forgeSurfacePayload) resolveRole() (ForgeRole, error) {
	return normalizeForgeRole(p.Role)
}

func forgeSurfaceBase(repo ForgeRepo, f Forge) map[string]interface{} {
	return map[string]interface{}{
		"repo": repo.Path,
		"host": repo.Host.Host,
		"kind": repo.Host.Kind,
		"via":  f.Via(),
	}
}

func mcpGitMembers(repoSlug, dir, host, kind string) interface{} {
	p := forgeSurfacePayload{Repo: repoSlug, Directory: dir, Host: host, Kind: kind}
	return runForgeMembers(context.Background(), p)
}

func mcpGitMemberInvite(user, role, repoSlug, dir, host, kind string) interface{} {
	p := forgeSurfacePayload{User: user, Role: role, Repo: repoSlug, Directory: dir, Host: host, Kind: kind}
	return runForgeInvite(context.Background(), p)
}

func mcpGitMemberRemove(user, repoSlug, dir, host, kind string) interface{} {
	p := forgeSurfacePayload{User: user, Repo: repoSlug, Directory: dir, Host: host, Kind: kind}
	return runForgeRemove(context.Background(), p)
}

func gitMembersOpsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	p, err := parseForgeSurfacePayload(payload)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	raw := runForgeMembers(c.Ctx, p)
	return forgeSurfaceOpsResult(raw)
}

func gitMemberInviteOpsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	p, err := parseForgeSurfacePayload(payload)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	raw := runForgeInvite(c.Ctx, p)
	return forgeSurfaceOpsResult(raw)
}

func gitMemberRemoveOpsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	p, err := parseForgeSurfacePayload(payload)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	raw := runForgeRemove(c.Ctx, p)
	return forgeSurfaceOpsResult(raw)
}

func forgeSurfaceOpsResult(raw interface{}) OpsResult {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return OpsResult{OK: false, Code: "git_error", Error: "unexpected forge result"}
	}
	if errText := strings.TrimSpace(fmt.Sprint(m["error"])); errText != "" && errText != "<nil>" {
		code := "git_error"
		if strings.Contains(errText, "kind must be github or gitlab") {
			code = "bad_payload"
		}
		return OpsResult{OK: false, Code: code, Error: errText, Initial: m}
	}
	return OpsResult{OK: true, Initial: m}
}

func runForgeMembers(ctx context.Context, p forgeSurfacePayload) interface{} {
	repo, forge, err := resolveSurfaceForge(p)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	members, err := forge.ListMembers(ctx, repo)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "repo": repo.Path, "host": repo.Host.Host, "kind": repo.Host.Kind, "via": forge.Via()}
	}
	out := forgeSurfaceBase(repo, forge)
	out["members"] = members
	out["count"] = len(members)
	return out
}

func runForgeInvite(ctx context.Context, p forgeSurfacePayload) interface{} {
	user := strings.TrimSpace(p.User)
	if user == "" {
		return map[string]interface{}{"error": "user is required"}
	}
	role, err := p.resolveRole()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	repo, forge, err := resolveSurfaceForge(p)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	invite, err := forge.InviteMember(ctx, repo, user, role)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "repo": repo.Path, "host": repo.Host.Host, "kind": repo.Host.Kind, "via": forge.Via(), "user": user, "role": role}
	}
	out := forgeSurfaceBase(repo, forge)
	out["user"] = user
	out["role"] = role
	out["invite"] = invite
	return out
}

func runForgeRemove(ctx context.Context, p forgeSurfacePayload) interface{} {
	user := strings.TrimSpace(p.User)
	if user == "" {
		return map[string]interface{}{"error": "user is required"}
	}
	repo, forge, err := resolveSurfaceForge(p)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if err := forge.RemoveMember(ctx, repo, user); err != nil {
		return map[string]interface{}{"error": err.Error(), "repo": repo.Path, "host": repo.Host.Host, "kind": repo.Host.Kind, "via": forge.Via(), "user": user}
	}
	out := forgeSurfaceBase(repo, forge)
	out["user"] = user
	out["removed"] = true
	return out
}

func resolveSurfaceForge(p forgeSurfacePayload) (ForgeRepo, Forge, error) {
	repo, err := p.resolveRepo()
	if err != nil {
		return ForgeRepo{}, nil, err
	}
	forge, err := newForge(repo.Host)
	if err != nil {
		return ForgeRepo{}, nil, err
	}
	return repo, forge, nil
}
