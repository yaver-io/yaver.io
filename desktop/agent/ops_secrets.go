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
	"os"
	"path/filepath"
	"strings"
	"time"
)

type opsSecretsPayload struct {
	// Op: "list" | "get" | "set" | "delete" | "env" | "projects" |
	// "check_passphrase" | "unlock" | "lock" | "reset". Writes are
	// vault-only.
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
	// Passphrase: used by check_passphrase/unlock, and as the current
	// passphrase for lock if the runtime vault is not already open.
	Passphrase string `json:"passphrase,omitempty"`
	// NewPassphrase: manual passphrase for lock/reset.
	NewPassphrase string `json:"new_passphrase,omitempty"`
	// Confirm: required for destructive reset.
	Confirm bool `json:"confirm,omitempty"`
	// ManualPassphrase: reset under NewPassphrase instead of current auth token.
	ManualPassphrase bool `json:"manual_passphrase,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "secrets",
		Description: "Unified secrets API across the local vault (owner-only NaCl secretbox + Argon2id, per-project grouping) and 1Password. scope=\"vault\" for writable local; scope=\"op\" for read-only 1Password items. ops: list, get, set (vault only), delete (vault only), env (shell export lines), projects, check_passphrase, unlock, lock, reset.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]interface{}{
				"op":              map[string]interface{}{"type": "string", "enum": []string{"list", "get", "set", "delete", "env", "projects", "check_passphrase", "unlock", "lock", "reset"}},
				"scope":           map[string]interface{}{"type": "string", "enum": []string{"vault", "op"}, "default": "vault"},
				"project":         map[string]interface{}{"type": "string", "description": "Project group name. \"\" = global, \"*\" = every project (list only)."},
				"name":            map[string]interface{}{"type": "string"},
				"value":           map[string]interface{}{"type": "string"},
				"category":        map[string]interface{}{"type": "string"},
				"notes":           map[string]interface{}{"type": "string"},
				"include_globals": map[string]interface{}{"type": "boolean"},
				"passphrase":      map[string]interface{}{"type": "string", "description": "Vault passphrase for check_passphrase/unlock. Owner-only; never returned."},
				"new_passphrase":  map[string]interface{}{"type": "string", "description": "New manual passphrase for lock/reset. Owner-only; never returned."},
				"confirm":         map[string]interface{}{"type": "boolean", "description": "Required true for reset."},
				"manual_passphrase": map[string]interface{}{
					"type":        "boolean",
					"description": "For reset, create the new empty vault under new_passphrase instead of current auth token.",
				},
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
		if c.Server == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "agent server unavailable"}
		}
		store := c.Server.vaultStore
		switch p.Op {
		case "check_passphrase":
			return opsVaultCheckPassphrase(p)
		case "unlock":
			return opsVaultUnlock(c, p)
		case "lock":
			return opsVaultLock(c, p)
		case "reset":
			return opsVaultReset(c, p)
		case "list":
			if store == nil {
				return OpsResult{OK: false, Code: "unavailable", Error: "vault not initialised on this agent"}
			}
			project := p.Project
			if project == "" {
				project = "*" // listing with no project defaults to "everything"
			}
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"entries":  store.List(project),
				"projects": store.ListProjects(),
			}}
		case "projects":
			if store == nil {
				return OpsResult{OK: false, Code: "unavailable", Error: "vault not initialised on this agent"}
			}
			return OpsResult{OK: true, Initial: map[string]interface{}{"projects": store.ListProjects()}}
		case "get":
			if store == nil {
				return OpsResult{OK: false, Code: "unavailable", Error: "vault not initialised on this agent"}
			}
			if p.Name == "" {
				return OpsResult{OK: false, Code: "bad_payload", Error: "name required for get"}
			}
			entry, err := store.Get(p.Project, p.Name)
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: entry}
		case "set":
			if store == nil {
				return OpsResult{OK: false, Code: "unavailable", Error: "vault not initialised on this agent"}
			}
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
			if store == nil {
				return OpsResult{OK: false, Code: "unavailable", Error: "vault not initialised on this agent"}
			}
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
			if store == nil {
				return OpsResult{OK: false, Code: "unavailable", Error: "vault not initialised on this agent"}
			}
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

func opsVaultPathExists() (string, OpsResult, bool) {
	path, err := VaultPath()
	if err != nil {
		return "", OpsResult{OK: false, Code: "io_error", Error: err.Error()}, false
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", OpsResult{OK: false, Code: "not_found", Error: "no vault file at " + path}, false
		}
		return "", OpsResult{OK: false, Code: "io_error", Error: err.Error()}, false
	}
	return path, OpsResult{}, true
}

func opsVaultAuthConfig() (*Config, OpsResult, bool) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, OpsResult{OK: false, Code: "unauthorized", Error: "not authenticated. Run 'yaver auth' first"}, false
	}
	return cfg, OpsResult{}, true
}

func opsVaultArchive(path, reason string) (string, error) {
	if reason == "" {
		reason = "archived"
	}
	stamp := time.Now().Format("20060102-150405")
	archive := filepath.Join(filepath.Dir(path), fmt.Sprintf("%s.%s.%s", filepath.Base(path), reason, stamp))
	return archive, os.Rename(path, archive)
}

func opsVaultCheckPassphrase(p opsSecretsPayload) OpsResult {
	if strings.TrimSpace(p.Passphrase) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "passphrase required for check_passphrase"}
	}
	if _, res, ok := opsVaultPathExists(); !ok {
		return res
	}
	vs, err := NewVaultStore(p.Passphrase)
	if err != nil {
		return OpsResult{OK: false, Code: "invalid_passphrase", Error: "invalid vault passphrase or corrupted vault"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"valid":    true,
		"entries":  len(vs.List("*")),
		"projects": vs.ListProjects(),
	}}
}

func opsVaultUnlock(c OpsContext, p opsSecretsPayload) OpsResult {
	if strings.TrimSpace(p.Passphrase) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "passphrase required for unlock"}
	}
	if _, res, ok := opsVaultPathExists(); !ok {
		return res
	}
	cfg, res, ok := opsVaultAuthConfig()
	if !ok {
		return res
	}
	vs, err := NewVaultStoreWithDevice(p.Passphrase, cfg.DeviceID)
	if err != nil {
		return OpsResult{OK: false, Code: "invalid_passphrase", Error: "invalid vault passphrase; vault was not changed"}
	}
	if err := vs.RekeyTo(DerivePassphraseFromToken(cfg.AuthToken)); err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
	}
	cfg.PreviousAuthToken = ""
	cfg.PreviousAuthTokens = nil
	if err := SaveConfig(cfg); err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: "vault rekeyed, but could not clear previous-token state: " + err.Error()}
	}
	c.Server.vaultStore = vs
	return OpsResult{OK: true, Initial: map[string]interface{}{"unlocked": true, "rekeyed_to": "auth-token"}}
}

func opsVaultLock(c OpsContext, p opsSecretsPayload) OpsResult {
	if strings.TrimSpace(p.NewPassphrase) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "new_passphrase required for lock"}
	}
	if _, res, ok := opsVaultPathExists(); !ok {
		return res
	}
	var vs *VaultStore
	if c.Server.vaultStore != nil {
		vs = c.Server.vaultStore
	} else {
		if strings.TrimSpace(p.Passphrase) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "passphrase required when runtime vault is not already open"}
		}
		cfg, _, _ := opsVaultAuthConfig()
		deviceID := ""
		if cfg != nil {
			deviceID = cfg.DeviceID
		}
		var err error
		vs, err = NewVaultStoreWithDevice(p.Passphrase, deviceID)
		if err != nil {
			return OpsResult{OK: false, Code: "invalid_passphrase", Error: "invalid current vault passphrase; vault was not changed"}
		}
	}
	if err := vs.RekeyTo(p.NewPassphrase); err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
	}
	c.Server.vaultStore = vs
	return OpsResult{OK: true, Initial: map[string]interface{}{"locked": true, "rekeyed_to": "manual-passphrase"}}
}

func opsVaultReset(c OpsContext, p opsSecretsPayload) OpsResult {
	if !p.Confirm {
		return OpsResult{OK: false, Code: "confirm_required", Error: "confirm=true required for reset"}
	}
	path, res, ok := opsVaultPathExists()
	if !ok {
		return res
	}
	var pass, deviceID string
	if p.ManualPassphrase {
		if strings.TrimSpace(p.NewPassphrase) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "new_passphrase required for manual_passphrase reset"}
		}
		pass = p.NewPassphrase
	} else {
		cfg, res, ok := opsVaultAuthConfig()
		if !ok {
			return res
		}
		pass = DerivePassphraseFromToken(cfg.AuthToken)
		deviceID = cfg.DeviceID
	}
	archive, err := opsVaultArchive(path, "reset-bak")
	if err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
	}
	vs, err := NewVaultStoreWithDevice(pass, deviceID)
	if err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: "archived old vault at " + archive + " but failed to create fresh vault: " + err.Error()}
	}
	c.Server.vaultStore = vs
	return OpsResult{OK: true, Initial: map[string]interface{}{"reset": true, "archive": archive}}
}
