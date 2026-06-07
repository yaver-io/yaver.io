package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// byo_golden.go — per-account "bake once, boot many" golden image.
//
// A Hetzner snapshot is PRIVATE to the account that created it, so we
// can't ship Yaver's golden image into a BYO user's own account. Instead:
// bake a ready BYO box into a snapshot in THE USER'S OWN account once
// (cloud_bake), cache the resulting image id here, and have
// cloud_provision boot from it thereafter (seconds instead of a 3–5 min
// first-boot install). The cache holds only an image id (NOT a secret),
// keyed by provider, at ~/.yaver/byo-golden.json.

func goldenImagePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".yaver", "byo-golden.json")
}

// readGoldenImage returns the cached golden image id for a provider, or
// "" if none has been baked yet.
func readGoldenImage(provider string) string {
	p := goldenImagePath()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return strings.TrimSpace(m[provider])
}

func writeGoldenImage(provider, imageID string) error {
	p := goldenImagePath()
	if p == "" {
		return fmt.Errorf("no home dir for golden-image cache")
	}
	m := map[string]string{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	m[provider] = imageID
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_bake",
		Description: "Bake a READY BYO box into a reusable golden image in YOUR OWN Hetzner account (snapshot; the box keeps running) and cache its id so future cloud_provision boots from it in seconds instead of a 3–5 min first-boot install. confirm=true + YAVER_CLOUD_STOPSTART_LIVE=1 to run; else dry-run. Vault token only.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"serverId", "confirm"},
			"properties": map[string]interface{}{
				"serverId": map[string]interface{}{"type": "string", "description": "Hetzner numeric id of a ready box to capture as the golden image"},
				"confirm":  map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCloudBakeHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsCloudBakeHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ServerID string `json:"serverId"`
		Confirm  bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.ServerID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "serverId required (Hetzner numeric id)"}
	}
	if !p.Confirm || !cloudStopStartLive() {
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"dryRun": true,
			"plan":   fmt.Sprintf("would snapshot server %s into a reusable golden image on your account", p.ServerID),
			"why":    planGateReason(p.Confirm),
		}}
	}
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — connect it in Settings first (BYO token)"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	label := fmt.Sprintf("yaver-golden-%d", time.Now().Unix())
	imageID, serr := m.hetznerSnapshotServerReturningID(token, strings.TrimSpace(p.ServerID), label)
	if serr != nil {
		return OpsResult{OK: false, Code: "bake_failed", Error: serr.Error()}
	}
	if werr := writeGoldenImage("hetzner", imageID); werr != nil {
		// Image exists; cache write failed — surface it but still report
		// the id so the user can pass it explicitly.
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"baked": imageID, "cached": false, "warning": werr.Error(),
		}}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"baked": imageID, "cached": true,
		"note": "future spin-ups boot from this image (fast). Re-bake after upgrading the box.",
	}}
}
