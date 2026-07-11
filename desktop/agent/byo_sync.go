package main

import (
	"encoding/json"
	"strings"
)

// byo_sync.go — emit BYO box lifecycle state to Convex (bookkeeping ONLY:
// id/state/timestamps; the provider token NEVER goes here — it stays in
// the agent vault). Plus a reconcile verb that lists the user's real
// servers and refreshes their "active" state so Convex stays truthful
// even if a direct emit was missed. Best-effort — a sync failure never
// blocks the underlying cloud op.

// syncByoMachine upserts one box's state. extra carries optional
// non-secret descriptors (name/region/plan/serverIp/imageId/
// snapshotImageId); empty/nil values are dropped.
func syncByoMachine(provider, serverID, state string, extra map[string]interface{}) {
	if globalConvexSync == nil || strings.TrimSpace(serverID) == "" {
		return
	}
	args := map[string]interface{}{
		"provider": provider,
		"serverId": serverID,
		"state":    state,
	}
	for k, v := range extra {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		args[k] = v
	}
	globalConvexSync.callMutation("byoMachines:upsert", args)
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_reconcile",
		Description: "Reconcile Convex BYO lifecycle state with reality: list the user's real Hetzner servers (vault token) and mark each 'active' in byoMachines. Read-on-Hetzner / bookkeeping-write only; never deletes or mutates a server. Self-heals missed state emits.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsCloudReconcileHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsCloudReconcileHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — connect it in Settings first (BYO token)"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	servers, lerr := m.hetznerListServers(token)
	if lerr != nil {
		return OpsResult{OK: false, Code: "list_failed", Error: lerr.Error()}
	}
	for _, s := range servers {
		syncByoMachine("hetzner", s.ID, "active", map[string]interface{}{
			"name":     s.Name,
			"serverIp": s.IP,
			"region":   s.Location,
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"reconciled": len(servers)}}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_seed",
		Description: "Seed Convex byoMachines from your REAL provider inventory (vault Hetzner token) so existing boxes show up with the right hosting tier — AND link THIS box to its own row by matching its public IP, which is what makes the byo tier resolve per-device (auto scale-to-zero needs the device↔box link). Read-on-provider / bookkeeping-write only; never creates, deletes, or mutates a server. Safe to re-run. Managed boxes keep precedence (a cloudMachines row still reads as managed).",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsMachineSeedHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsMachineSeedHandler(c OpsContext, _ json.RawMessage) OpsResult {
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — connect it (BYO token) first"}
	}
	m, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: err.Error()}
	}
	servers, lerr := m.hetznerListServers(token)
	if lerr != nil {
		return OpsResult{OK: false, Code: "list_failed", Error: lerr.Error()}
	}
	localIP := detectAutoPublicIP(c.Ctx)
	localDev := localDeviceID()
	linkedServer := ""
	for _, s := range servers {
		extra := map[string]interface{}{
			"name":     s.Name,
			"serverIp": s.IP,
			"region":   s.Location,
		}
		// Link THIS box to its own byoMachines row so the byo tier resolves
		// per-device. Without a deviceId on the row, hostingForDevice can't
		// match it and the box degrades to self-hosted (hands-off) — safe, but
		// it would never auto scale-to-zero. Match on the agent's public IP.
		if localDev != "" && localIP != "" && s.IP == localIP {
			extra["deviceId"] = localDev
			linkedServer = s.ID
		}
		syncByoMachine("hetzner", s.ID, "active", extra)
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"seeded":        len(servers),
		"linkedDevice":  localDev,
		"linkedServer":  linkedServer,
		"linkedThisBox": linkedServer != "",
	}}
}
