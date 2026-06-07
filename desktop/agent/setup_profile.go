package main

import (
	"encoding/json"
)

// setup_profile.go — "what's configurable on this machine?" inventory so
// the mobile app can show the user what it can replay onto a freshly
// provisioned box (git auth, coding-runner auth, cloud accounts, vault
// projects). The user installs the Yaver agent on their real dev PC,
// mobile fetches this inventory, and on a new managed/BYO box mobile
// orchestrates the copy.
//
// STRICTLY NON-SECRET: this verb returns booleans / hostnames / names /
// counts ONLY — never a token, key, or vault value. The actual secret
// copy to a new box happens device→device through the EXISTING paths
// (git_push_creds, runner-auth, vault) — never through mobile or Convex.
// Owner-only (AllowGuest:false).

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "setup_inventory",
		Description: "Inventory what this machine can configure on a new box — git auth (hosts only), coding-runner auth (claude/codex/opencode), connected cloud accounts, and vault project names. NON-SECRET (booleans/names/counts only — no tokens/keys/values). Mobile reads this from your dev PC, then replays the chosen pieces onto a freshly provisioned box via the existing device→device paths.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsSetupInventoryHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

type runnerLite struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Installed      bool   `json:"installed"`
	Ready          bool   `json:"ready"`
	AuthConfigured bool   `json:"authConfigured"`
}

func opsSetupInventoryHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	// Git auth — expose the HOSTS that have credentials, never the token.
	gitHosts := []string{}
	if creds, err := loadGitCredentials(); err == nil {
		seen := map[string]bool{}
		for _, cr := range creds {
			h := cr.Host
			if h != "" && !seen[h] {
				seen[h] = true
				gitHosts = append(gitHosts, h)
			}
		}
	}

	// Vault project NAMES only (never values). openVaultE (not openVault —
	// the latter os.Exit(1)s on a locked vault, which would kill the
	// agent). A locked/absent vault just yields an empty list.
	vaultProjects := []string{}
	if vs, err := openVaultE(); err == nil && vs != nil {
		vaultProjects = vs.ListProjects()
	}

	// Connected cloud-provider account ids only (e.g. ["hetzner"]) — NOT
	// the full catalog (which carries token-field descriptors + tokenURLs).
	connectedAccounts := []string{}
	for _, a := range globalAccountsManager.List() {
		if a.Connected {
			connectedAccounts = append(connectedAccounts, string(a.Provider))
		}
	}

	// Coding-runner auth — minimal {id,name,installed,ready,authConfigured}
	// projected out of the verbose status (whose error/detail strings
	// carry absolute paths — never expose those).
	runners := []runnerLite{}
	if raw, err := json.Marshal(mcpRunnerAuthStatus("")); err == nil {
		var rs struct {
			Runners []runnerLite `json:"runners"`
		}
		if json.Unmarshal(raw, &rs) == nil {
			runners = rs.Runners
		}
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"git":      map[string]interface{}{"providers": gitHosts},
		"runners":  runners,
		"accounts": connectedAccounts,
		"vault":    map[string]interface{}{"projects": vaultProjects},
	}}
}
