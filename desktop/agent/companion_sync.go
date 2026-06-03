package main

import (
	"regexp"
	"strings"
)

// companion_sync.go is the single privacy seam between the on-device companion
// engine and Convex. The companion manifest references env-interpolated
// endpoint URLs (which embed cron auth tokens) and vault secrets; NONE of that
// may reach Convex. Convex stores bookkeeping ONLY: project slug, bound
// deviceId, cron names+schedules, and last/next-run status — enough to show a
// cross-device status table, nothing confidential.
//
// buildCompanionUpsertPayload is the ONLY function that constructs the
// agent→Convex companion payload. convex_privacy_test.go runs its output
// through the forbidden-field / abs-path / username-leak asserts, so a careless
// future edit that tries to ship a URL/token/path trips the guard at test time.

// CompanionCronSummary is the privacy-safe projection of one armed cron.
// Deliberately has NO url, token, headers, body, or workdir.
type CompanionCronSummary struct {
	Name        string // sanitized [a-z0-9-]
	Schedule    string // cron expression (not secret)
	LastOutcome string // "ok" | "failed" | ""
	LastRunAt   int64  // epoch ms, 0 = never
	NextRunAt   int64  // epoch ms, 0 = unknown
}

var companionNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitizeCompanionName lowercases and strips a name to [a-z0-9-]. This is the
// guard that stops a careless manifest from smuggling a path or token into the
// cron `name` field that gets synced to Convex.
func sanitizeCompanionName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = companionNameSanitizer.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// buildCompanionUpsertPayload assembles the bookkeeping mutation args. Every
// value here is a slug, a cron expression, a counter, a status string, or a
// timestamp — never a URL, token, secret, or absolute path.
func buildCompanionUpsertPayload(deviceID, slug string, enabled bool, crons []CompanionCronSummary, serviceCount int) map[string]interface{} {
	cronArgs := make([]map[string]interface{}, 0, len(crons))
	for _, c := range crons {
		entry := map[string]interface{}{
			"name":     sanitizeCompanionName(c.Name),
			"schedule": c.Schedule,
		}
		if c.LastOutcome != "" {
			entry["lastOutcome"] = c.LastOutcome
		}
		if c.LastRunAt > 0 {
			entry["lastRunAt"] = c.LastRunAt
		}
		if c.NextRunAt > 0 {
			entry["nextRunAt"] = c.NextRunAt
		}
		cronArgs = append(cronArgs, entry)
	}
	return map[string]interface{}{
		"deviceId":     deviceID,
		"slug":         sanitizeCompanionName(slug),
		"enabled":      enabled,
		"crons":        cronArgs,
		"serviceCount": serviceCount,
	}
}
