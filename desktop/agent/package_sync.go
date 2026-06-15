package main

// package_sync.go — the single privacy seam between the on-device Task Package
// store and Convex. Convex holds BOOKKEEPING ONLY so an owner and a runner (a
// different user) can discover / share / track a package across devices: name
// slug, public hostnames (for the consent screen), schedule intent, consent
// text, coarse status. The collector spec (selectors, full URLs with tokens),
// output endpoints, secrets, IPs, and absolute paths NEVER leave the device.
//
// buildTaskPackagePayload is the ONLY function that constructs the agent→Convex
// package payload; desktop/agent/convex_privacy_test.go runs its output through
// the forbidden-field walker so a careless future edit trips at test time.

import (
	"net/url"
	"regexp"
	"strings"
)

var packageNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitizePackageName mirrors sanitizeCompanionName: lowercases and strips to
// [a-z0-9-] so a careless name can't smuggle a path/token into Convex.
func sanitizePackageName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = packageNameSanitizer.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// packageDomains extracts unique HOSTNAMES from the package's sources + MCP
// bindings — for the runner's consent screen. Hostnames only: a full URL can
// carry a query token, a hostname cannot, so this is the privacy boundary.
func packageDomains(p *TaskPackage) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(raw string) {
		if raw == "" {
			return
		}
		if u, err := url.Parse(raw); err == nil {
			h := strings.ToLower(u.Hostname())
			if h != "" && !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	for _, src := range p.Spec.Task.Sources {
		add(src.URL)
	}
	for _, b := range p.Spec.Task.MCP {
		add(b.URL)
	}
	return out
}

func safeStringList(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// buildTaskPackagePayload assembles the privacy-safe upsert args. Every value
// is a slug, a hostname, a short token, a counter, a status, or owner-authored
// display text — never a full URL, selector, secret, IP, or absolute path.
func buildTaskPackagePayload(deviceID string, p *TaskPackage) map[string]interface{} {
	return map[string]interface{}{
		"deviceId":           deviceID,
		"name":               sanitizePackageName(p.Metadata.Name),
		"version":            p.Metadata.Version,
		"kind":               p.Spec.Task.Kind,
		"tier":               p.effectiveTier(),
		"description":        p.Metadata.Description,
		"domains":            packageDomains(p),
		"runtimes":           safeStringList(p.Spec.Runtimes),
		"engines":            safeStringList(p.Spec.Task.Engines),
		"vantageGeo":         safeStringList(p.Spec.Vantage.Geo),
		"vantageResidential": p.Spec.Vantage.Residential,
		"schedule":           p.Spec.Schedule.Every,
		"consentSummary":     p.Spec.Consent.Summary,
		"willNot":            safeStringList(p.Spec.Consent.WillNot),
		"dataShown":          safeStringList(p.Spec.Consent.DataShown),
		"status":             "published",
	}
}

// syncTaskPackage pushes a package's bookkeeping to Convex, best-effort. Uses
// the process-global syncer (set after the agent authes); nil before then. Never
// fatal — a publish succeeds locally even if Convex is unreachable.
func syncTaskPackage(deviceID string, p *TaskPackage) {
	if globalConvexSync == nil || deviceID == "" {
		return
	}
	globalConvexSync.callMutation("taskPackages:upsertPackage", buildTaskPackagePayload(deviceID, p))
}
