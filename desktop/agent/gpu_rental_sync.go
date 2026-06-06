package main

// gpu_rental_sync.go — the single privacy seam between the GPU-rental layer and
// Convex. Convex stores bookkeeping ONLY: provider, opaque resource id, kind,
// gpu class, the PUBLIC inference endpoint (host-only, userinfo/query stripped),
// model id, the vault PROJECT NAME the app reads (never its values), voiceSafe,
// and status. No API keys, no vault values, no absolute paths ever reach here —
// convex_privacy_test.go runs buildGpuRentalUpsertPayload's output through the
// forbidden-field / abs-path asserts so a careless edit trips at test time.
//
// Same posture as companion_sync.go: the endpoint here is a clean public base
// URL (Salad groups are created auth:false; the DeepInfra base is public), but
// we still sanitize it so no credential can ever ride along in a query/userinfo.

import (
	"net/url"
	"strings"
)

// sanitizeInferenceEndpoint reduces an endpoint to scheme://host[/path],
// dropping any userinfo, query, or fragment that could carry a credential.
// Returns "" for an unparseable or non-http(s) value.
func sanitizeInferenceEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	clean := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	return strings.TrimRight(clean.String(), "/")
}

// GPURentalSummary is the privacy-safe projection of one GPU/inference rental,
// synced to Convex for cross-device dashboard visibility. Deliberately has NO
// api key, token, or vault value.
type GPURentalSummary struct {
	DeviceID    string // dispatcher box that owns this rental
	Provider    string // "salad" | "deepinfra"
	ResourceID  string // salad group id | "deepinfra:<model>"
	Kind        string // "gpu-group" | "serverless-binding"
	GPUClass    string // "" for serverless
	Endpoint    string // public base URL (sanitized)
	Model       string
	BindProject string // vault project NAME (never its values)
	VoiceSafe   bool
	Status      string // "provisioning" | "running" | "draining" | "stopped"
	HoursUsed   float64
	TokensUsed  float64
	CostCents   float64
}

// buildGpuRentalUpsertPayload assembles the bookkeeping mutation args. Every
// value is a provider id, an opaque resource id, a sanitized public URL, a
// model/slug, a flag, a status, or a counter — never a key, secret, or path.
func buildGpuRentalUpsertPayload(s GPURentalSummary) map[string]interface{} {
	payload := map[string]interface{}{
		"deviceId":   s.DeviceID,
		"provider":   s.Provider,
		"resourceId": s.ResourceID,
		"kind":       s.Kind,
		"voiceSafe":  s.VoiceSafe,
		"status":     s.Status,
	}
	if s.GPUClass != "" {
		payload["gpuClass"] = s.GPUClass
	}
	if ep := sanitizeInferenceEndpoint(s.Endpoint); ep != "" {
		payload["endpoint"] = ep
	}
	if s.Model != "" {
		payload["model"] = s.Model
	}
	// The vault project NAME only (sanitized to a slug) — never its contents.
	if s.BindProject != "" {
		payload["bindProject"] = sanitizeCompanionName(s.BindProject)
	}
	if s.HoursUsed > 0 {
		payload["hoursUsed"] = s.HoursUsed
	}
	if s.TokensUsed > 0 {
		payload["tokensUsed"] = s.TokensUsed
	}
	if s.CostCents > 0 {
		payload["costCents"] = s.CostCents
	}
	return payload
}

// syncGpuRental emits the bookkeeping mutation if a syncer is mounted. No-op
// when syncer is nil (BYO / offline-first — Convex bookkeeping is optional).
func syncGpuRental(syncer *convexSyncer, s GPURentalSummary) {
	if syncer == nil {
		return
	}
	syncer.callMutation("gpuRentals:upsertGpuRental", buildGpuRentalUpsertPayload(s))
}

// attachGpuRentalBookkeeping wires an autoscaler's transition hook to emit
// privacy-safe Convex rows on burst/reap. deviceID + bindProject are static
// for a given dispatcher; provider is always salad for the burst tier.
func attachGpuRentalBookkeeping(a *GPUAutoscaler, syncer *convexSyncer, deviceID, bindProject string) {
	if a == nil || syncer == nil {
		return
	}
	a.OnTransition = func(action GPUAutoAction, snap GPUAutoscalerSnapshot) {
		status := "running"
		switch action {
		case ActProvision, ActWaitEndpoint:
			status = "provisioning"
		case ActDrainStart:
			status = "draining"
		case ActReap:
			status = "stopped"
		}
		syncGpuRental(syncer, GPURentalSummary{
			DeviceID:    deviceID,
			Provider:    "salad",
			ResourceID:  snap.BurstID,
			Kind:        "gpu-group",
			GPUClass:    snap.BurstGPUClass,
			Endpoint:    snap.BurstEndpoint,
			BindProject: bindProject,
			VoiceSafe:   true,
			Status:      status,
		})
	}
}
