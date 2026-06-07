package main

// ops_cloud.go — cloud lifecycle verbs: provision / scale / destroy.
// Each one is a thin hand-off to the existing cloud_* MCP tools so
// agents calling ops uniformly never need to know the domain tool
// names. The handler returns the domain tool + payload template;
// mobile-headless / desktop wiring inside the same session can then
// dispatch the domain tool directly.
//
// We don't just fire-and-forget the domain tool because provision +
// destroy take minutes and deserve to surface a streamId directly.
// The follow-up expansion will have this verb actually call the
// handler in-process rather than pointing at the domain tool. For
// now the pointer pattern is enough for an agent to wire a flow.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type opsProvisionPayload struct {
	Plan    string `json:"plan"`
	Region  string `json:"region,omitempty"`
	SSHKey  string `json:"sshKey,omitempty"`
	Label   string `json:"label,omitempty"`
}

type opsDestroyPayload struct {
	DeviceID string `json:"deviceId"`
	Confirm  bool   `json:"confirm"`
}

type opsScalePayload struct {
	DeviceID string `json:"deviceId"`
	CPU      int    `json:"cpu,omitempty"`
	RAMGb    int    `json:"ramGb,omitempty"`
	GPU      string `json:"gpu,omitempty"`
	// GPU-rental "change inference backend" path (gpu_rental.go). When a
	// provider/model/baseUrl is present, scale performs an in-process
	// rebind of the app's inference config instead of a VM resize:
	// DeepInfra model swap (zero infra) or repoint at a Salad endpoint.
	Provider string `json:"provider,omitempty"` // "deepinfra" → fill baseUrl + key from account
	Model    string `json:"model,omitempty"`
	BaseURL  string `json:"baseUrl,omitempty"`
	Project  string `json:"project,omitempty"` // vault project the app companion reads
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "provision",
		Description: "Provision a new Yaver-managed cloud machine (Hetzner CPU or GPU, pre-loaded with node/go/rust/docker/expo/eas/yaver). Routes to cloud_provision MCP tool; minutes-long — subscribe to its stream.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"plan"},
			"properties": map[string]interface{}{
				"plan":   map[string]interface{}{"type": "string", "description": "Plan id (cpu-small, gpu-4000, ...). Call cloud_plans to list."},
				"region": map[string]interface{}{"type": "string"},
				"sshKey": map[string]interface{}{"type": "string"},
				"label":  map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsProvisionHandler,
		Streaming:  true,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "scale",
		Description: "Resize a provisioned cloud machine (CPU / RAM / GPU). Routes to cloud_scale MCP tool.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"deviceId"},
			"properties": map[string]interface{}{
				"deviceId": map[string]interface{}{"type": "string"},
				"cpu":      map[string]interface{}{"type": "integer"},
				"ramGb":    map[string]interface{}{"type": "integer"},
				"gpu":      map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsScaleHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "destroy",
		Description: "Decommission a provisioned cloud machine. Requires confirm=true to guard against accidental calls. Routes to cloud_destroy MCP tool.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"deviceId", "confirm"},
			"properties": map[string]interface{}{
				"deviceId": map[string]interface{}{"type": "string"},
				"confirm":  map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsDestroyHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "recycle",
		Description: "Recycle a BYO Hetzner box: create a fresh box, health-check it, then snapshot+delete the old one (zero-downtime; rolls back keeping the old box if the new one is unhealthy). Refuses to recycle the device this agent runs on. Destructive — confirm=true required; without it returns the plan (dry-run).",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"targetDeviceId", "oldServerId", "newName"},
			"properties": map[string]interface{}{
				"targetDeviceId": map[string]interface{}{"type": "string"},
				"oldServerId":    map[string]interface{}{"type": "string", "description": "Hetzner numeric id of the box being retired (explicit — never fuzzy-matched)"},
				"newName":        map[string]interface{}{"type": "string"},
				"plan":           map[string]interface{}{"type": "string"},
				"region":         map[string]interface{}{"type": "string"},
				"confirm":        map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRecycleHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_checkout",
		Description: "Start buying a Yaver managed-cloud box: returns a LemonSqueezy checkout URL to open in a browser. machineType=cpu (RN/Hermes + web + deploy, default) | gpu. Proxies the Convex /billing/yaver-cloud/checkout route with the user's token; the token never appears in the payload.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"machineType": map[string]interface{}{"type": "string", "description": "cpu (default) | gpu"},
				"region":      map[string]interface{}{"type": "string", "description": "eu (default) | us"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCloudCheckoutHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_status",
		Description: "List the user's Yaver managed-cloud boxes + subscription. Read-side counterpart to cloud_checkout: authed proxy to the Convex /subscription route (token from agent config, never payload). Returns {machines, subscription, relay}.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsCloudStatusHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_destroy",
		Description: "Clean REMOVE of a BYO box (no replacement): optionally snapshot, then delete, via the caller's OWN vault provider token. Distinct from `recycle` (which creates a new box first). Requires serverId + confirm=true. snapshot defaults FALSE (a snapshot is a paid lingering image; opt in for a recovery point). BYO-token-only — cannot touch another user's resources.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"serverId", "confirm"},
			"properties": map[string]interface{}{
				"serverId":       map[string]interface{}{"type": "string", "description": "Provider numeric server id (explicit — never fuzzy-matched)"},
				"targetDeviceId": map[string]interface{}{"type": "string", "description": "Optional: Yaver deviceId of the box this resource belongs to. If it equals the agent running this verb, the remove is refused (self-destruct guard) — decommission must run from a different control device."},
				"snapshot":       map[string]interface{}{"type": "boolean", "description": "Take a recovery snapshot before deleting. Default false — a snapshot is a paid, lingering disk image; opt in only if you want a recovery point."},
				"confirm":        map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCloudDestroyHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_list",
		Description: "List the user's real Hetzner servers (id, name, ip, status, type, location) via the vault Hetzner token. Read-only; no payload. Lets a UI resolve the EXACT server id to recycle/remove from the live account instead of asking the human to recall it — the user still picks the exact row, so this is resolution, not a fuzzy guess.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsCloudListHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_snapshot_delete",
		Description: "Delete one leftover snapshot image (stop its recurring bill) via the caller's OWN vault Hetzner token. Used to clear a recovery image orphaned by a server delete. Requires imageId + confirm=true. BYO-token-only — cannot touch another user's resources.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"imageId", "confirm"},
			"properties": map[string]interface{}{
				"imageId": map[string]interface{}{"type": "string", "description": "Provider numeric snapshot/image id (explicit — never fuzzy-matched)"},
				"confirm": map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCloudSnapshotDeleteHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

// opsCloudSnapshotDeleteHandler removes one snapshot image by id using
// the caller's own vault Hetzner token (never from the payload), so a
// leftover recovery image surfaced by cloud_destroy can be cleared in
// one click. Mirrors cloud_destroy's BYO-only safety: this token can
// only ever see/delete the caller's own resources.
func opsCloudSnapshotDeleteHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ImageID string `json:"imageId"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.ImageID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "imageId required (provider numeric id — never fuzzy-matched)"}
	}
	if !p.Confirm {
		return OpsResult{OK: false, Code: "unauthorized", Error: "snapshot delete requires confirm=true"}
	}
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — /accounts/connect first (BYO token)"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	if derr := m.hetznerDeleteImage(token, strings.TrimSpace(p.ImageID)); derr != nil {
		return OpsResult{OK: false, Code: "delete_failed", Error: derr.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"deleted": p.ImageID}}
}

// opsCloudListHandler returns the account's real servers so the
// Recycle/Remove dialog can offer a pick-the-box UI. Read-only — the
// vault Hetzner token never appears in the payload, and listing
// cannot mutate or delete anything.
func opsCloudListHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — /accounts/connect first (BYO token)"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	servers, err := m.hetznerListServers(token)
	if err != nil {
		return OpsResult{OK: false, Code: "list_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"servers": servers}}
}

// opsCloudDestroyHandler runs the BYO snapshot+delete in-process
// (unlike the older `destroy` verb which only returns a hint). This
// is the clean "remove this box" path — no new box, snapshot first.
func opsCloudDestroyHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ServerID       string `json:"serverId"`
		TargetDeviceID string `json:"targetDeviceId"`
		// Optional, default OFF. A Hetzner snapshot is a paid, lingering
		// disk image; for a disposable box the user usually doesn't want
		// one. Opt in explicitly. SECURITY: this verb only ever uses the
		// caller's OWN BYO token (accountField below — per-agent vault,
		// never the shared managed/platform token), so one developer can
		// never list, snapshot, or delete another developer's resources
		// through it. Managed-cloud teardown is a separate, Convex-
		// ownership-checked path — never this raw verb.
		Snapshot       bool `json:"snapshot"`
		Confirm        bool `json:"confirm"`
		// Convex device row (deviceId UUID) to deregister as part of
		// teardown. The cloud server is about to be destroyed, so the
		// row must be removed by whoever runs this — there is no
		// "agent deregisters itself afterward" in the self-decommission
		// case (the box IS the thing being deleted, and selfMode means
		// no other control device exists to clean up). We deregister
		// FIRST, while the box still has network, THEN delete the
		// server. Without this the row is orphaned forever and the dead
		// box lingers as a ghost in the device list. This is the Convex
		// row only — independent of the self-destruct *resource* guard
		// above, which protects the cloud resource, not the registry.
		RemoveDeviceRow string `json:"removeDeviceRow"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.ServerID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "serverId required (Hetzner numeric id — never fuzzy-matched for a delete)"}
	}
	// Self-destruct guard (mirrors recycle): refuse to delete the cloud
	// resource of the very box this agent runs on. Decommission must run
	// from a different control device that holds the cloud account.
	if tgt := strings.TrimSpace(p.TargetDeviceID); tgt != "" {
		if self := strings.TrimSpace(localDeviceID()); self != "" && self == tgt {
			return OpsResult{OK: false, Code: "unauthorized", Error: "refusing to remove the cloud resource of the device this agent runs on (self-destruct guard) — run remove from a different control device"}
		}
	}
	if !p.Confirm {
		return OpsResult{OK: false, Code: "unauthorized", Error: "remove requires confirm=true"}
	}
	skipSnapshot := "true"
	if p.Snapshot {
		skipSnapshot = "false"
	}
	// Deregister the Convex device row BEFORE deleting the server, so
	// the box still has network to make the call. A failure here must
	// NOT abort the destroy — a stranded, billed cloud server is worse
	// than a ghost row (which decays once heartbeats go stale and can
	// be cleared by hand). Surface it as a warning instead.
	deregWarn := ""
	if row := strings.TrimSpace(p.RemoveDeviceRow); row != "" {
		if cfg, err := LoadConfig(); err != nil || cfg == nil ||
			strings.TrimSpace(cfg.ConvexSiteURL) == "" || strings.TrimSpace(cfg.AuthToken) == "" {
			deregWarn = "agent not authenticated to Convex"
		} else if derr := RemoveDeviceShutdown(cfg.ConvexSiteURL, cfg.AuthToken, row); derr != nil {
			deregWarn = derr.Error()
		}
	}
	res := mcpCloudDestroy(string(HostHetzner), strings.TrimSpace(p.ServerID),
		fmt.Sprintf(`{"confirm":"true","skipSnapshot":"%s"}`, skipSnapshot))
	if m, ok := res.(map[string]interface{}); ok {
		if e, has := m["error"]; has && e != nil {
			return OpsResult{OK: false, Code: "destroy_failed", Error: fmt.Sprintf("%v", e), Initial: res}
		}
	}
	// Carry the provider result plus the deregister outcome so the web
	// dialog can tell the user whether the row was actually removed
	// (rather than optimistically claiming "deleted").
	out := map[string]interface{}{"result": res, "deregistered": deregWarn == ""}
	if deregWarn != "" {
		out["deregisterWarning"] = deregWarn
	}
	// Lift orphaned snapshots to the top level so the web dialog can
	// surface "left behind, still billing — [delete]" without digging
	// into the nested provider result.
	if pr, ok := res.(*ProvisionResult); ok && pr != nil && len(pr.OrphanSnapshots) > 0 {
		out["orphanSnapshots"] = pr.OrphanSnapshots
	}
	// Bookkeeping: tombstone the box in Convex BYO state (id/ts only).
	syncByoMachine("hetzner", strings.TrimSpace(p.ServerID), "deleted", nil)
	return OpsResult{OK: true, Initial: out}
}

// opsCloudStatusHandler proxies GET /subscription so MCP/mobile can
// show managed boxes (status, origin tag, errorMessage) without their
// own Convex URL/token. Same auth model as cloud_checkout.
func opsCloudStatusHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.ConvexSiteURL) == "" || strings.TrimSpace(cfg.AuthToken) == "" {
		return OpsResult{OK: false, Code: "not_authed", Error: "agent not authed (missing convex site url / token) — run `yaver auth`"}
	}
	req, err := newBearerRequest("GET", cfg.ConvexSiteURL+"/subscription", cfg.AuthToken, nil)
	if err != nil {
		return OpsResult{OK: false, Code: "request_error", Error: err.Error()}
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return OpsResult{OK: false, Code: "convex_unreachable", Error: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OpsResult{OK: false, Code: "status_failed", Error: fmt.Sprintf("subscription HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))}
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return OpsResult{OK: false, Code: "status_failed", Error: "subscription returned bad json"}
	}
	return OpsResult{OK: true, Initial: out}
}

// opsCloudCheckoutHandler proxies the Convex checkout route so an
// agent can hand the user a pay link. The Convex route is owner/
// preview-gated (isCloudPreviewUser) + needs LemonSqueezy env — a 403
// or config error is surfaced verbatim, never swallowed.
func opsCloudCheckoutHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		MachineType string `json:"machineType"`
		Region      string `json:"region"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.ConvexSiteURL) == "" || strings.TrimSpace(cfg.AuthToken) == "" {
		return OpsResult{OK: false, Code: "not_authed", Error: "agent not authed (missing convex site url / token) — run `yaver auth`"}
	}
	body, _ := json.Marshal(map[string]string{
		"machineType": p.MachineType,
		"region":      p.Region,
	})
	req, err := newBearerRequest("POST", cfg.ConvexSiteURL+"/billing/yaver-cloud/checkout", cfg.AuthToken, bytes.NewReader(body))
	if err != nil {
		return OpsResult{OK: false, Code: "request_error", Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return OpsResult{OK: false, Code: "convex_unreachable", Error: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OpsResult{OK: false, Code: "checkout_failed", Error: fmt.Sprintf("checkout HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))}
	}
	var out struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.URL == "" {
		return OpsResult{OK: false, Code: "checkout_failed", Error: "checkout returned no url"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"checkoutUrl": out.URL,
		"mode":        out.Mode,
		"hint":        "open checkoutUrl in a browser to pay; the box auto-provisions on the LemonSqueezy webhook",
	}}
}

// opsRecycleHandler runs the BYO host-recycle state machine in-process
// (unlike provision/destroy which hand off to MCP tools — recycle is
// the whole orchestration and its safety guards must run server-side,
// never be re-implemented by each UI). Token is the user's vault
// Hetzner account token, never a payload field.
func opsRecycleHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var req recycleRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — /accounts/connect first (BYO token)"}
	}
	res := recycleHost(liveRecycleBackend{token: token}, req)
	if !res.OK {
		return OpsResult{OK: false, Code: "recycle_failed", Error: res.Error, Initial: res}
	}
	return OpsResult{OK: true, Initial: res}
}

func opsProvisionHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsProvisionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Plan == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "plan is required"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"hint":    "call cloud_provision MCP tool with these args — returns a streamId; subscribe via /streams/<id> for bring-up progress",
		"mcpTool": "cloud_provision",
		"args":    p,
	}}
}

func opsScaleHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsScalePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	// GPU-rental "change inference backend" path: a DeepInfra model swap or
	// a repoint at a new endpoint is zero-infra — do it in-process by
	// rewriting the app's vault binding (the seam the companion reads). This
	// is the "change GPU/model type" operation for serverless inference.
	if strings.EqualFold(p.Provider, "deepinfra") || strings.TrimSpace(p.BaseURL) != "" || strings.TrimSpace(p.Model) != "" {
		baseURL := strings.TrimSpace(p.BaseURL)
		apiKey := ""
		if strings.EqualFold(p.Provider, "deepinfra") {
			if baseURL == "" {
				baseURL = deepInfraOpenAIBase
			}
			apiKey = accountField(ProviderDeepInfra, "token")
			if apiKey == "" {
				return OpsResult{OK: false, Code: "no_account", Error: "DeepInfra not connected — /accounts/connect first"}
			}
		}
		if baseURL == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "scale-inference needs provider=deepinfra or an explicit baseUrl"}
		}
		if m := strings.TrimSpace(p.Model); m != "" && !VoiceSafeModel(m) {
			return OpsResult{OK: false, Code: "not_voice_safe", Error: "model " + m + " is reasoning/batch (not voice-safe) — use gpu_bind with allowUnsafe=true to force"}
		}
		if currentRuntimeVaultStore() == nil {
			return OpsResult{OK: false, Code: "no_vault", Error: "no runtime vault mounted — cannot rebind inference"}
		}
		written := rebindInference(strings.TrimSpace(p.Project), baseURL, apiKey, strings.TrimSpace(p.Model))
		proj := strings.TrimSpace(p.Project)
		if proj == "" {
			proj = inferenceVaultDefaultProject
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"rebound":     true,
			"project":     proj,
			"keysWritten": written,
			"hint":        "inference backend changed in-process (no VM resize); restart the app's companion to apply, in-flight calls finish on the old endpoint",
		}}
	}
	if p.DeviceID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "deviceId required (or pass provider/model/baseUrl to change the inference backend)"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"hint":    "call cloud_scale MCP tool",
		"mcpTool": "cloud_scale",
		"args":    p,
	}}
}

func opsDestroyHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsDestroyPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.DeviceID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "deviceId required"}
	}
	if !p.Confirm {
		return OpsResult{OK: false, Code: "unauthorized", Error: "destroy requires confirm=true"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"hint":    "call cloud_destroy MCP tool",
		"mcpTool": "cloud_destroy",
		"args":    p,
	}}
}
