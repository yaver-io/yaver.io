package main

// ops_backup.go — verb "backup": snapshot / restore / list backups
// across whatever subsystem the agent is managing. Routes to the
// per-provider tools (db_backup, cloud_backup, morning backup, etc.)
// instead of duplicating storage logic.

import "encoding/json"

type opsBackupPayload struct {
	Op        string `json:"op"`               // list | create | restore
	Target    string `json:"target"`           // db | cloud | project | vault
	ID        string `json:"id,omitempty"`     // backup id for restore
	Directory string `json:"directory,omitempty"` // for project target
	DryRun    bool   `json:"dryRun,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "backup",
		Description: "Snapshot / restore / list backups. target=db (db_backup/db_restore), cloud (cloud_backup), project (filesystem snapshot), vault (local AES-GCM vault.enc).",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op", "target"},
			"properties": map[string]interface{}{
				"op":        map[string]interface{}{"type": "string", "enum": []string{"list", "create", "restore"}},
				"target":    map[string]interface{}{"type": "string", "enum": []string{"db", "cloud", "project", "vault"}},
				"id":        map[string]interface{}{"type": "string"},
				"directory": map[string]interface{}{"type": "string"},
				"dryRun":    map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsBackupHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsBackupHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsBackupPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Op == "" || p.Target == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "op and target are required"}
	}

	var tool string
	switch p.Target {
	case "db":
		switch p.Op {
		case "create":
			tool = "db_backup"
		case "restore":
			tool = "db_restore"
		case "list":
			// db_backup lists backups when called with no id; no
			// dedicated list tool. Hand back a pointer.
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"hint": "db_backup with op:\"list\" returns existing snapshots; db_restore needs a path",
			}}
		}
	case "cloud":
		tool = "cloud_backup"
	case "vault":
		// Vault backup is a file copy of ~/.yaver/vault.enc — no MCP
		// tool for it because the file is self-contained. Return the
		// recommended paths so agents can issue a files-op copy.
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"source": "~/.yaver/vault.enc",
			"hint":   "vault.enc is AES-GCM + Argon2id; copy the file anywhere — restoring requires the same passphrase. Use ops files { op:\"read\" } + { op:\"write\" } pair or plain cp.",
		}}
	case "project":
		// Project-level backup is a tarball of the working directory.
		// Implemented via ops run + tar; surface the canonical cmd.
		dir := p.Directory
		if dir == "" {
			dir = "."
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":    "use ops run to create a timestamped tarball:",
			"command": "tar -czf yaver-backup-$(date +%Y%m%d-%H%M%S).tgz --exclude=node_modules --exclude=.git " + dir,
		}}
	}
	if tool == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "unsupported (op, target) combination"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"hint":    "call " + tool + " — handles provider resolution",
		"mcpTool": tool,
		"args":    p,
	}}
}
