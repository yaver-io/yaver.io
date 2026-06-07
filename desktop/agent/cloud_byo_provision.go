package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// cloud_byo_provision.go — one-tap "spin up a box on YOUR OWN Hetzner"
// from mobile/web. Creates a server on the user's account via their
// vault token (accountField — never the platform token, never the
// payload), optionally from a prebuilt Yaver image (fast boot) and
// optionally shallow-cloning a git repo on first boot. Plus
// cloud_snapshots to list stopped boxes / recovery points for restart.
//
// ISOLATED FILE (prefer-new-files; sibling to cloud_stopstart.go): own
// init() registering cloud_provision + cloud_snapshots; reuses the
// existing *CloudDeployManager + accountField read-only; ZERO edits to
// cloud_deploy.go / ops_cloud.go (parallel-session territory).
//
// MONEY-SAFETY: a real Hetzner create fires ONLY when confirm=true (an
// explicit user tap on "Spin up"). Without confirm a PLAN is returned
// (dry-run; nothing is created). It's the user's OWN account + token, so
// they pay Hetzner directly per-hour — Yaver's wallet/meter is NOT
// involved (this is the BYO plane, not the managed plane).

// gitURLRe only accepts http(s):// and git@ forms — keeps a repo URL
// that gets embedded into the box's first-boot script from carrying
// shell metacharacters (it's also shellQuote'd; this is defence in
// depth + a clear "that's not a git URL" rejection).
var gitURLRe = regexp.MustCompile(`^(https?://|git@)[A-Za-z0-9@:/._~%+-]+$`)

// hetznerCreateServerCustom creates a server from imageID (a prebuilt
// snapshot — fast boot) or ubuntu-22.04 (imageID==""), with a bootstrap
// that installs Docker/Git and — when repoURL is set — shallow-clones
// the repo into /root/workspace on first boot. Returns as soon as
// Hetzner assigns an IP (no SSH ready-wait) so the mobile call is
// snappy; the box self-installs via user_data and appears as a pending
// device to claim.
func (m *CloudDeployManager) hetznerCreateServerCustom(token, name, plan, region, imageID, repoURL string) (string, string, error) {
	serverTypeMap := map[string]string{"starter": "cx21", "pro": "cx31", "scale": "cx41"}
	serverType, ok := serverTypeMap[plan]
	if !ok {
		serverType = "cx21"
	}
	locationMap := map[string]string{"eu": "nbg1", "us": "ash"}
	location, ok := locationMap[region]
	if !ok {
		location = "nbg1"
	}
	// Prebuilt snapshot id is numeric; the base image is a name.
	var image interface{} = "ubuntu-22.04"
	if strings.TrimSpace(imageID) != "" {
		image = imageID
		if n, perr := strconv.Atoi(imageID); perr == nil {
			image = n
		}
	}
	userData := cloudBootstrapScript()
	if repoURL != "" {
		userData += fmt.Sprintf(
			"\n# Yaver: shallow-clone the user's repo on first boot (super fast)\ngit clone --depth 1 %s /root/workspace || echo '[yaver] repo clone skipped'\n",
			shellQuote(repoURL),
		)
	}
	payload := map[string]interface{}{
		"name":        name,
		"server_type": serverType,
		"image":       image,
		"location":    location,
		"user_data":   userData,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", hetznerAPIBase+"/servers", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("hetzner API: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Server struct {
			ID        int `json:"id"`
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("parse hetzner response: %w", err)
	}
	if result.Error != nil {
		return "", "", fmt.Errorf("hetzner error %s: %s", result.Error.Code, result.Error.Message)
	}
	ip := result.Server.PublicNet.IPv4.IP
	if ip == "" {
		return "", "", fmt.Errorf("hetzner returned no IP for new server")
	}
	return ip, strconv.Itoa(result.Server.ID), nil
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_provision",
		Description: "Spin up a box on YOUR OWN Hetzner account (vault token) — optionally from a prebuilt Yaver image id (fast boot) and optionally shallow-cloning a git repo on first boot. You pay Hetzner directly per-hour; Yaver's wallet is NOT involved (BYO plane). confirm=true creates for real; without it returns the plan (dry-run, nothing created). The new box self-installs and appears as a pending device to claim.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"plan":    map[string]interface{}{"type": "string", "description": "starter|pro|scale (default starter = cx21)"},
				"region":  map[string]interface{}{"type": "string", "description": "eu|us (default eu)"},
				"imageId": map[string]interface{}{"type": "string", "description": "Optional prebuilt Yaver snapshot id on YOUR account for fast boot. Omit ⇒ ubuntu-22.04 + first-boot install."},
				"repoUrl": map[string]interface{}{"type": "string", "description": "Optional git URL to shallow-clone on the new box (https:// or git@). Private repos need creds pushed via git_push_creds."},
				"name":    map[string]interface{}{"type": "string", "description": "Optional server name (auto-generated if omitted)."},
				"confirm": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCloudProvisionHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_snapshots",
		Description: "List snapshot images on YOUR Hetzner account (stopped boxes from cloud_stop + recovery points) so a UI can offer restart (cloud_start) or cleanup (cloud_snapshot_delete). Read-only; vault token; cannot mutate anything.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsCloudSnapshotsHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsCloudProvisionHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Plan    string `json:"plan"`
		Region  string `json:"region"`
		ImageID string `json:"imageId"`
		RepoURL string `json:"repoUrl"`
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	plan := strings.TrimSpace(p.Plan)
	if plan == "" {
		plan = "starter"
	}
	region := strings.TrimSpace(p.Region)
	if region == "" {
		region = "eu"
	}
	repoURL := strings.TrimSpace(p.RepoURL)
	if repoURL != "" && !gitURLRe.MatchString(repoURL) {
		return OpsResult{OK: false, Code: "bad_payload", Error: "repoUrl must be a git URL (https://… or git@…)"}
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "yaver-box-" + strconv.FormatInt(time.Now().Unix(), 36)
	}
	// Gate real creation on the SAME switch as cloud_stop/cloud_start
	// (confirm=true AND YAVER_CLOUD_STOPSTART_LIVE=1). This keeps the BYO
	// lifecycle consistent: one flag enables provision + stop + start
	// together, so a user can never create a real billing box they then
	// can't stop from the app. Without it, every action is a safe
	// dry-run preview (no real spend).
	// Image selection: explicit imageId wins; otherwise prefer the cached
	// per-account golden image (bake-once → fast boot); else ubuntu base.
	imageID := strings.TrimSpace(p.ImageID)
	imageSource := "explicit"
	if imageID == "" {
		if g := readGoldenImage("hetzner"); g != "" {
			imageID = g
			imageSource = "golden"
		}
	}
	if !p.Confirm || !cloudStopStartLive() {
		image := "ubuntu-22.04 (first-boot install)"
		if imageID != "" {
			tag := "prebuilt, fast boot"
			if imageSource == "golden" {
				tag = "your baked golden image, fast boot"
			}
			image = fmt.Sprintf("image %s (%s)", imageID, tag)
		}
		clone := ""
		if repoURL != "" {
			clone = fmt.Sprintf(", clone %s", repoURL)
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"dryRun": true,
			"plan":   fmt.Sprintf("would create %q (%s/%s) from %s on your Hetzner account%s", name, plan, region, image, clone),
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
	ip, id, perr := m.hetznerCreateServerCustom(token, name, plan, region, imageID, repoURL)
	if perr != nil {
		return OpsResult{OK: false, Code: "provision_failed", Error: perr.Error()}
	}
	// Bookkeeping: record the new box as active in Convex (id/state/ts
	// only — token never synced).
	syncByoMachine("hetzner", id, "active", map[string]interface{}{
		"name": name, "region": region, "plan": plan, "serverIp": ip,
		"imageId": imageID,
	})
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"provisioned": id, "ip": ip, "name": name, "plan": plan, "region": region,
		"note": "Box booting on your Hetzner account — it self-installs the Yaver agent and will appear as a pending device to claim. Stop it anytime (cloud_stop) to halt hourly billing.",
	}}
}

func opsCloudSnapshotsHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — connect it in Settings first (BYO token)"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	snaps, lerr := m.hetznerListSnapshots(token)
	if lerr != nil {
		return OpsResult{OK: false, Code: "list_failed", Error: lerr.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"snapshots": snaps}}
}
