package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// cloud_stopstart.go — managed-cloud STOP (=snapshot+destroy) / START
// (=recreate-from-snapshot) primitives + ops verbs. P1 of
// docs/managed-cloud-metered-stopstart-plan.md.
//
// ISOLATED FILE on purpose: own init() registering cloud_stop/
// cloud_start, sibling create-from-image fn — ZERO edits to
// cloud_deploy.go / ops_cloud.go (parallel-refactor + prefer-new-files
// per feedback_other_sessions_prune_untested). Reuses the existing
// *CloudDeployManager methods (same package) read-only.
//
// MONEY-SAFETY (scope decision: owner-gated + DRY-RUN, no real spend):
// real Hetzner mutate calls fire ONLY when confirm=true AND env
// YAVER_CLOUD_STOPSTART_LIVE=="1". Missing either → a PLAN is returned
// and nothing is created/snapshotted/deleted. Fail-closed: default is
// dry-run even with confirm (mirrors the robot 3-guard /
// host_recycle dry-run pattern). BYO vault token only (accountField) —
// never the platform token, never the payload.
//
// Fail-closed invariant (CLAUDE.md): a box is NEVER deleted without a
// recoverable snapshot first — if the snapshot fails, the delete is
// ABORTED.

func cloudStopStartLive() bool {
	return strings.TrimSpace(os.Getenv("YAVER_CLOUD_STOPSTART_LIVE")) == "1"
}

// hetznerSnapshotServerReturningID snapshots a server and returns the
// created image (snapshot) id so START can recreate from it. Distinct
// from hetznerSnapshotServer (which returns only error) so the
// existing delete path is untouched.
func (m *CloudDeployManager) hetznerSnapshotServerReturningID(token, serverID, label string) (string, error) {
	payload := map[string]interface{}{"type": "snapshot", "description": label}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", hetznerAPIBase+"/servers/"+serverID+"/actions/create_image", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("hetzner snapshot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("hetzner snapshot returned %d", resp.StatusCode)
	}
	var result struct {
		Image struct {
			ID int `json:"id"`
		} `json:"image"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse hetzner snapshot response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("hetzner snapshot error %s: %s", result.Error.Code, result.Error.Message)
	}
	if result.Image.ID == 0 {
		return "", fmt.Errorf("hetzner snapshot returned no image id")
	}
	return strconv.Itoa(result.Image.ID), nil
}

// hetznerCreateServerFromImage mirrors hetznerCreateServer but boots
// from an explicit snapshot/image id instead of the hardcoded base
// image. Kept separate so hetznerCreateServer's signature + callers
// are untouched.
func (m *CloudDeployManager) hetznerCreateServerFromImage(token, name, plan, region, imageID string) (string, string, error) {
	// Resume path — capacity-resilient too (scale-to-zero DEPENDS on resume
	// succeeding; a single-DC stock-out must not strand a paused box). arm
	// (cax*) MUST match the golden snapshot's arch. See cloud_capacity.go.
	serverType := hetznerServerTypeForPlan(plan)
	// Hetzner accepts `image` as a numeric id or a name. A snapshot id is
	// numeric — send it as a number when it parses, else as-is.
	var image interface{} = imageID
	if n, perr := strconv.Atoi(imageID); perr == nil {
		image = n
	}
	ip, id, err := m.hetznerCreateResilient(token, serverType, region, map[string]interface{}{
		"name":      name,
		"image":     image,
		"user_data": cloudBootstrapScript(),
	})
	if err != nil {
		return "", "", err
	}
	m.hetznerWaitSSH(ip)
	return ip, id, nil
}

// hetznerStopServer = snapshot (capturing id) THEN delete. Fail-closed:
// a failed snapshot ABORTS the delete (never lose the box's data).
func (m *CloudDeployManager) hetznerStopServer(token, serverID, label string) (string, error) {
	snapID, err := m.hetznerSnapshotServerReturningID(token, serverID, label)
	if err != nil {
		return "", fmt.Errorf("snapshot failed — NOT deleting (recover-safety): %w", err)
	}
	if derr := m.hetznerDeleteServer(token, serverID); derr != nil {
		// Snapshot succeeded but delete failed: box still billing, but
		// data is safe. Surface the snapshot id so the caller can retry
		// the delete without re-snapshotting.
		return snapID, fmt.Errorf("snapshot ok (image %s) but delete failed: %w", snapID, derr)
	}
	return snapID, nil
}

// hetznerStartServer = recreate from a prior stop's snapshot id.
func (m *CloudDeployManager) hetznerStartServer(token, name, plan, region, snapshotImageID string) (string, string, error) {
	return m.hetznerCreateServerFromImage(token, name, plan, region, snapshotImageID)
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_stop",
		Description: "STOP a managed box: snapshot (recover-safe) then delete the server so Hetzner billing halts (a powered-off server still bills full price — only delete stops it). Requires serverId + confirm=true. Returns the snapshot image id needed by cloud_start. DRY-RUN unless YAVER_CLOUD_STOPSTART_LIVE=1 (fail-closed, no real spend). BYO vault token only.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"serverId", "confirm"},
			"properties": map[string]interface{}{
				"serverId": map[string]interface{}{"type": "string", "description": "Hetzner numeric server id (explicit — never fuzzy-matched)"},
				"label":    map[string]interface{}{"type": "string", "description": "Snapshot description/label. Default yaver-stop-<serverId>."},
				"confirm":  map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCloudStopHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_start",
		Description: "START a previously-stopped managed box: recreate a server from the stop snapshot image id. Requires snapshotImageId + name + confirm=true. DRY-RUN unless YAVER_CLOUD_STOPSTART_LIVE=1 (fail-closed). BYO vault token only.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"snapshotImageId", "name", "confirm"},
			"properties": map[string]interface{}{
				"snapshotImageId": map[string]interface{}{"type": "string", "description": "Hetzner image id returned by cloud_stop"},
				"name":            map[string]interface{}{"type": "string", "description": "New server name"},
				"plan":            map[string]interface{}{"type": "string", "description": "starter|pro|scale (default starter)"},
				"region":          map[string]interface{}{"type": "string", "description": "eu|us (default eu)"},
				"confirm":         map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCloudStartHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsCloudStopHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ServerID string `json:"serverId"`
		Label    string `json:"label"`
		Confirm  bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.ServerID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "serverId required (Hetzner numeric id — never fuzzy-matched)"}
	}
	label := strings.TrimSpace(p.Label)
	if label == "" {
		label = "yaver-stop-" + p.ServerID
	}
	if !p.Confirm || !cloudStopStartLive() {
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"dryRun": true,
			"plan":   fmt.Sprintf("would snapshot server %s (label %q) then delete it", p.ServerID, label),
			"why":    planGateReason(p.Confirm),
		}}
	}
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — /accounts/connect first (BYO token)"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	snapID, serr := m.hetznerStopServer(token, strings.TrimSpace(p.ServerID), label)
	if serr != nil {
		code := "stop_failed"
		if snapID != "" {
			code = "delete_failed_snapshot_ok"
		}
		return OpsResult{OK: false, Code: code, Error: serr.Error(),
			Initial: map[string]interface{}{"snapshotImageId": snapID}}
	}
	// Bookkeeping: box is now sleeping (snapshot kept, server deleted).
	syncByoMachine("hetzner", p.ServerID, "stopped", map[string]interface{}{"snapshotImageId": snapID})
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"stopped": p.ServerID, "snapshotImageId": snapID,
	}}
}

func opsCloudStartHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		SnapshotImageID string `json:"snapshotImageId"`
		Name            string `json:"name"`
		Plan            string `json:"plan"`
		Region          string `json:"region"`
		Confirm         bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.SnapshotImageID) == "" || strings.TrimSpace(p.Name) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "snapshotImageId and name required"}
	}
	plan := strings.TrimSpace(p.Plan)
	if plan == "" {
		plan = "starter"
	}
	region := strings.TrimSpace(p.Region)
	if region == "" {
		region = "eu"
	}
	if !p.Confirm || !cloudStopStartLive() {
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"dryRun": true,
			"plan":   fmt.Sprintf("would recreate server %q (%s/%s) from snapshot image %s", p.Name, plan, region, p.SnapshotImageID),
			"why":    planGateReason(p.Confirm),
		}}
	}
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — /accounts/connect first (BYO token)"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	ip, id, serr := m.hetznerStartServer(token, strings.TrimSpace(p.Name), plan, region, strings.TrimSpace(p.SnapshotImageID))
	if serr != nil {
		return OpsResult{OK: false, Code: "start_failed", Error: serr.Error()}
	}
	// Bookkeeping: box is alive again at a new id/ip.
	syncByoMachine("hetzner", id, "active", map[string]interface{}{"name": p.Name, "serverIp": ip})
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"started": id, "ip": ip, "fromSnapshot": p.SnapshotImageID,
	}}
}

func planGateReason(confirm bool) string {
	if !confirm {
		return "confirm=true not set"
	}
	return "YAVER_CLOUD_STOPSTART_LIVE != 1 (fail-closed dry-run; no real spend)"
}
