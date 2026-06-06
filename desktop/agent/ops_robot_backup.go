package main

// Optional Talos backup for the robot cell. OFF by default — Yaver is fully
// usable standalone (vault-local config + edge-file programs). When the user
// configures a Talos target, robot_backup pushes the config + program list there
// for safekeeping. This is the ONLY Talos touch-point and it is entirely opt-in;
// nothing here runs unless a target is set. See docs/yaver-talos-open-core-strategy.md.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// robotBackupTarget resolves the optional Talos backup URL: env first, then a
// vault entry the user sets once. Empty ⇒ backup disabled.
func robotBackupTarget() string {
	if v := strings.TrimSpace(os.Getenv("YAVER_TALOS_BACKUP_URL")); v != "" {
		return v
	}
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(robotVaultProject, "talos_backup_url"); gerr == nil && e != nil {
			return strings.TrimSpace(e.Value)
		}
	}
	return ""
}

// robotBackupToken is an optional bearer for the Talos endpoint (vault entry).
func robotBackupToken() string {
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(robotVaultProject, "talos_backup_token"); gerr == nil && e != nil {
			return strings.TrimSpace(e.Value)
		}
	}
	return strings.TrimSpace(os.Getenv("YAVER_TALOS_BACKUP_TOKEN"))
}

// robotBackup pushes config + programs to the configured Talos target. No-op
// (clear "not configured") when no target is set — Yaver works as is without it.
func robotBackup(c OpsContext, _ json.RawMessage) OpsResult {
	target := robotBackupTarget()
	if target == "" {
		return OpsResult{OK: false, Code: "not_configured",
			Error: "Talos backup is optional and not configured; set vault robot/talos_backup_url (or YAVER_TALOS_BACKUP_URL)"}
	}
	cfg := robotConfigGet()
	progs := robotStore.List()
	body, _ := json.Marshal(map[string]any{
		"config":   cfg,
		"programs": progs,
		"at":       time.Now().UnixMilli(),
	})
	req, err := http.NewRequestWithContext(c.Ctx, "POST", strings.TrimRight(target, "/")+"/robot/backup", bytes.NewReader(body))
	if err != nil {
		return OpsResult{OK: false, Code: "backup_failed", Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := robotBackupToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return OpsResult{OK: false, Code: "backup_failed", Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return OpsResult{OK: false, Code: "backup_failed", Error: fmt.Sprintf("talos returned %d", resp.StatusCode)}
	}
	return OpsResult{OK: true, Initial: map[string]any{
		"backedUp": true, "programs": len(progs), "target": target,
	}}
}
