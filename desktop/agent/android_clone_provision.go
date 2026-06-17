package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// android_clone_provision.go — one-tap dedicated Android clone resource on the
// user's own Hetzner account. This is distinct from generic cloud_provision:
// it chooses ARM CAX plans and bootstraps Docker + binder + redroid so the UI
// can say "Your Android clone" instead of exposing Docker/redroid details.
//
// Money safety matches cloud_provision: real spend only happens with
// confirm=true AND YAVER_CLOUD_STOPSTART_LIVE=1. Otherwise the verb returns a
// dry-run plan and bootstrap summary.

type androidCloneProvisionRequest struct {
	Plan    string `json:"plan"`
	Region  string `json:"region"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	Confirm bool   `json:"confirm"`
}

type androidClonePlan struct {
	Provider     string   `json:"provider"`
	Resource     string   `json:"resource"`
	Dedicated    bool     `json:"dedicated"`
	Plan         string   `json:"plan"`
	ServerType   string   `json:"serverType"`
	Region       string   `json:"region"`
	Location     string   `json:"location"`
	Name         string   `json:"name"`
	Image        string   `json:"image"`
	RedroidImage string   `json:"redroidImage"`
	HostWorkDir  string   `json:"hostWorkDir"`
	DryRun       bool     `json:"dryRun"`
	Why          string   `json:"why,omitempty"`
	NextActions  []string `json:"nextActions"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "android_clone_provision",
		Description: "Plan or create a dedicated private Android clone on the user's own Hetzner account. Uses ARM CAX plans and bootstraps Docker + binder + redroid. confirm=true plus YAVER_CLOUD_STOPSTART_LIVE=1 is required for real spend; otherwise this is a dry-run.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"plan":    map[string]interface{}{"type": "string", "description": "starter|pro|scale. Defaults starter."},
				"region":  map[string]interface{}{"type": "string", "description": "eu|us. Defaults eu."},
				"name":    map[string]interface{}{"type": "string", "description": "Optional Hetzner server name."},
				"image":   map[string]interface{}{"type": "string", "description": "Optional redroid image. Defaults redroid/redroid:13.0.0-latest."},
				"confirm": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsAndroidCloneProvisionHandler,
		AllowGuest: false,
	})
}

func opsAndroidCloneProvisionHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var req androidCloneProvisionRequest
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	plan, err := buildAndroidClonePlan(req)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if !req.Confirm || !cloudStopStartLive() {
		plan.DryRun = true
		plan.Why = planGateReason(req.Confirm)
		return OpsResult{OK: true, Initial: plan}
	}

	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — connect it in Settings first (BYO token)"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	ip, id, err := m.hetznerCreateAndroidCloneServer(token, plan)
	if err != nil {
		return OpsResult{OK: false, Code: "provision_failed", Error: err.Error()}
	}
	syncByoMachine("hetzner", id, "active", map[string]interface{}{
		"name": plan.Name, "region": plan.Region, "serverIp": ip,
		"plan": plan.Plan, "resource": plan.Resource,
		"serverType": plan.ServerType,
	})
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"provisioned": id,
		"ip":          ip,
		"name":        plan.Name,
		"resource":    plan.Resource,
		"dedicated":   true,
		"plan":        plan.Plan,
		"serverType":  plan.ServerType,
		"region":      plan.Region,
		"nextActions": []string{
			"Wait for cloud-init to finish installing Docker and redroid.",
			"Run redroid_resource_status on the new device after it appears online.",
			"Run qa_base_build to create a warm private Android base.",
		},
	}}
}

func buildAndroidClonePlan(req androidCloneProvisionRequest) (androidClonePlan, error) {
	plan := strings.TrimSpace(req.Plan)
	if plan == "" {
		plan = "starter"
	}
	serverType, ok := androidCloneServerTypes()[plan]
	if !ok {
		return androidClonePlan{}, fmt.Errorf("plan must be one of starter, pro, scale")
	}
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = "eu"
	}
	location, ok := androidCloneLocations()[region]
	if !ok {
		return androidClonePlan{}, fmt.Errorf("region must be eu or us")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "yaver-android-clone-" + strconv.FormatInt(time.Now().Unix(), 36)
	}
	if !safeHetznerResourceName(name) {
		return androidClonePlan{}, fmt.Errorf("name may contain only letters, numbers, dot, underscore, and hyphen")
	}
	image := strings.TrimSpace(req.Image)
	if image == "" {
		image = defaultRedroidImage
	}
	return androidClonePlan{
		Provider:     "hetzner",
		Resource:     "android-clone",
		Dedicated:    true,
		Plan:         plan,
		ServerType:   serverType,
		Region:       region,
		Location:     location,
		Name:         name,
		Image:        "ubuntu-22.04",
		RedroidImage: image,
		HostWorkDir:  "/var/lib/yaver/redroid/default",
		NextActions: []string{
			"Create a private ARM Hetzner server for this user's Android clone.",
			"Install Docker, Android binder support, and pull the redroid image.",
			"Run redroid_resource_status once the device appears online.",
			"Build a warm Yaver Base image for fast future QA runs.",
		},
	}, nil
}

func androidCloneServerTypes() map[string]string {
	return map[string]string{"starter": "cax11", "pro": "cax21", "scale": "cax31"}
}

func androidCloneLocations() map[string]string {
	return map[string]string{"eu": "nbg1", "us": "ash"}
}

func safeHetznerResourceName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func androidCloneBootstrapScript(plan androidClonePlan) string {
	return cloudBootstrapScript() + fmt.Sprintf(`
# Yaver Android clone resource: Docker + binder + redroid.
install -d -o yaver -g yaver -m 0755 %[1]s
apt-get install -y -q kmod linux-modules-extra-$(uname -r) || apt-get install -y -q kmod || true
modprobe binder_linux devices=binder,hwbinder,vndbinder || modprobe binder_linux || true
docker pull %[2]s || true
cat > /usr/local/bin/yaver-redroid-up <<'YAVER_REDROID_UP'
#!/usr/bin/env bash
set -euo pipefail
NAME="${1:-yaver-android-clone}"
IMAGE="${REDROID_IMAGE:-%[2]s}"
WORK="${REDROID_WORKDIR:-%[1]s}"
mkdir -p "$WORK"
docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -itd --privileged --name "$NAME" -v "$WORK:/data" "$IMAGE" \
  androidboot.redroid_width=1080 androidboot.redroid_height=2340 androidboot.redroid_dpi=440
YAVER_REDROID_UP
chmod +x /usr/local/bin/yaver-redroid-up
`, shellQuote(plan.HostWorkDir), shellQuote(plan.RedroidImage))
}

func (m *CloudDeployManager) hetznerCreateAndroidCloneServer(token string, plan androidClonePlan) (string, string, error) {
	payload := map[string]interface{}{
		"name":        plan.Name,
		"server_type": plan.ServerType,
		"image":       plan.Image,
		"location":    plan.Location,
		"user_data":   androidCloneBootstrapScript(plan),
		"labels": map[string]string{
			"yaver_resource":  "android-clone",
			"yaver_dedicated": "true",
		},
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
		return "", "", fmt.Errorf("hetzner returned no IP for Android clone server")
	}
	return ip, strconv.Itoa(result.Server.ID), nil
}
