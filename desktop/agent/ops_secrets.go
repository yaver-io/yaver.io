package main

// ops_secrets.go — verb "secrets": unified read/write across Yaver's
// on-device vault (NaCl secretbox + Argon2id at rest, never touches
// Convex) and the user's 1Password account (via the existing op_get /
// op_list CLI wrappers). Agents don't have to know which storage a
// given credential lives in — they pick a scope and a key.
//
// Scopes:
//   "vault" — local encrypted store at ~/.yaver/vault.enc. Supports
//             project-scoped entries: pass project="" (default) for
//             globals, or project="<name>" for app-scoped secrets.
//             Use project="*" on list to see every project.
//   "op"    — 1Password via the `op` CLI (read-only for now).
//
// Guest and support sessions are refused — secrets are owner-private.

import (
	"encoding/json"
	"fmt"
)

type opsSecretsPayload struct {
	// Op: "list" | "get" | "set" | "delete" | "env" | "projects". Writes
	// are vault-only.
	Op string `json:"op"`
	// Scope: "vault" | "op". Defaults to vault.
	Scope string `json:"scope,omitempty"`
	// Project: for vault scope, the project group ("" = global, "*" = all
	// projects on list).
	Project string `json:"project,omitempty"`
	// Name: secret key. Required for get/set/delete.
	Name string `json:"name,omitempty"`
	// Value: plaintext for op=set. Stored secretbox-sealed in vault.
	Value string `json:"value,omitempty"`
	// Category: free-form tag for vault entries. Defaults "custom".
	Category string `json:"category,omitempty"`
	// Notes: free-form description — surfaced by vault list.
	Notes string `json:"notes,omitempty"`
	// IncludeGlobals: only used by op=env. Defaults true (when false,
	// the emitted script excludes global entries).
	IncludeGlobals *bool `json:"include_globals,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "secrets",
		Description: "Unified secrets API across the local vault (owner-only NaCl secretbox + Argon2id, per-project grouping) and 1Password. scope=\"vault\" for writable local; scope=\"op\" for read-only 1Password items. ops: list, get, set (vault only), delete (vault only), env (shell export lines), projects (list distinct projects).",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]interface{}{
				"op":              map[string]interface{}{"type": "string", "enum": []string{"list", "get", "set", "delete", "env", "projects"}},
				"scope":           map[string]interface{}{"type": "string", "enum": []string{"vault", "op"}, "default": "vault"},
				"project":         map[string]interface{}{"type": "string", "description": "Project group name. \"\" = global, \"*\" = every project (list only)."},
				"name":            map[string]interface{}{"type": "string"},
				"value":           map[string]interface{}{"type": "string"},
				"category":        map[string]interface{}{"type": "string"},
				"notes":           map[string]interface{}{"type": "string"},
				"include_globals": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsSecretsHandler,
		Streaming:  false,
		AllowGuest: false, // secrets are always owner-only
	})
}

func opsSecretsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsSecretsPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Op == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "op is required"}
	}
	scope := p.Scope
	if scope == "" {
		scope = "vault"
	}

	switch scope {
	case "vault":
		if c.Server == nil || c.Server.vaultStore == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "vault not initialised on this agent"}
		}
		store := c.Server.vaultStore
		switch p.Op {
		case "list":
			project := p.Project
			if project == "" {
				project = "*" // listing with no project defaults to "everything"
			}
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"entries":  store.List(project),
				"projects": store.ListProjects(),
			}}
		case "projects":
			return OpsResult{OK: true, Initial: map[string]interface{}{"projects": store.ListProjects()}}
		case "get":
			if p.Name == "" {
				return OpsResult{OK: false, Code: "bad_payload", Error: "name required for get"}
			}
			entry, err := store.Get(p.Project, p.Name)
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: entry}
		case "set":
			if p.Name == "" || p.Value == "" {
				return OpsResult{OK: false, Code: "bad_payload", Error: "name and value required for set"}
			}
			cat := p.Category
			if cat == "" {
				cat = "custom"
			}
			entry := VaultEntry{
				Name:     p.Name,
				Project:  p.Project,
				Value:    p.Value,
				Category: cat,
				Notes:    p.Notes,
			}
			if err := store.Set(entry); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"name":    p.Name,
				"project": p.Project,
				"stored":  true,
			}}
		case "delete":
			if p.Name == "" {
				return OpsResult{OK: false, Code: "bad_payload", Error: "name required for delete"}
			}
			if err := store.Delete(p.Project, p.Name); err != nil {
				return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"name":    p.Name,
				"project": p.Project,
				"deleted": true,
			}}
		case "env":
			if p.Project == "" {
				return OpsResult{OK: false, Code: "bad_payload", Error: "project is required for env"}
			}
			include := true
			if p.IncludeGlobals != nil {
				include = *p.IncludeGlobals
			}
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"project": p.Project,
				"script":  store.EnvExport(p.Project, include),
			}}
		default:
			return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + p.Op}
		}

	case "op":
		switch p.Op {
		case "list":
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"hint":    "call op_list MCP tool (requires `op` CLI installed + signed in); this verb defers read-only ops to avoid duplicating 1Password's auth flow",
				"mcpTool": "op_list",
			}}
		case "get":
			if p.Name == "" {
				return OpsResult{OK: false, Code: "bad_payload", Error: "name required"}
			}
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"hint":    fmt.Sprintf("call op_get MCP tool with item=%q", p.Name),
				"mcpTool": "op_get",
			}}
		case "set", "delete":
			return OpsResult{OK: false, Code: "unauthorized", Error: "1Password writes are intentionally not exposed via ops secrets; use the 1Password app or `op` CLI directly"}
		default:
			return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + p.Op}
		}

	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown scope: " + scope}
	}
}
