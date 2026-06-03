package main

// data_policy.go — runtime enforcement of the company `dataPolicy` (see
// backend/convex/companyAIOptions.ts). The policy is RESOLVED in Convex but
// must be ENFORCED on the runtime, or the corp-privacy claim is decorative.
//
// Two enforced controls live here:
//
//   - redactPII   → RedactPII() scrubs emails / secrets / card numbers / IPs
//                   out of a prompt before any runner (Claude Code / Codex /
//                   OpenCode / a local model) sees it.
//   - retentionDays → tasksToPrune() selects finished tasks older than the
//                   retention window so the task store can drop them.
//
// Redaction is deliberately CONSERVATIVE: prompts here are usually coding
// tasks, so the patterns avoid eating ordinary source (loop counters, version
// numbers, hex colors). It targets high-confidence PII/secret shapes only.

import (
	"regexp"
	"time"
)

// DataPolicy mirrors the enforced subset of the Convex dataPolicy. Only the
// fields the runtime can act on locally are represented.
type DataPolicy struct {
	RedactPII     bool
	RetentionDays int
}

var (
	// Email addresses.
	reEmail = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
	// Common secret/token shapes — high-confidence prefixes + long opaque
	// bodies. Conservative: requires a recognisable prefix so it doesn't eat
	// random identifiers.
	reSecret = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_\-]{16,}|ghp_[A-Za-z0-9]{20,}|gho_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9\-]{10,}|AKIA[0-9A-Z]{12,}|AIza[0-9A-Za-z_\-]{20,})\b`)
	// Bearer / Authorization header values (capture the token after the keyword).
	reBearer = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+|bearer\s+)[A-Za-z0-9._\-]{12,}`)
	// PEM private-key blocks.
	rePEM = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	// 13–16 digit card-like runs (allowing space/dash groups). Followed by a
	// Luhn check so plain long integers aren't redacted.
	reCardLike = regexp.MustCompile(`\b(?:\d[ \-]?){13,16}\b`)
	// IPv4 (excludes obvious version-number false positives by requiring 4
	// octets each ≤255 via the post-filter below).
	reIPv4 = regexp.MustCompile(`\b(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\b`)
	// Phone numbers — require a leading + and country code to avoid eating
	// code constants. e.g. +1 415-555-0132, +90 555 555 55 55.
	rePhone = regexp.MustCompile(`\+\d{1,3}[ \-]\d[\d \-]{6,}\d`)
	reDigit = regexp.MustCompile(`\d`)
)

// luhnValid reports whether the digits of s (ignoring non-digits) pass the
// Luhn checksum — the standard credit-card validity test.
func luhnValid(s string) bool {
	sum, alt, n := 0, false, 0
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c < '0' || c > '9' {
			continue
		}
		n++
		d := int(c - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return n >= 13 && sum%10 == 0
}

func ipOctetsValid(m []string) bool {
	for _, o := range m[1:] {
		// 1–3 digits already; reject > 255.
		if len(o) > 3 {
			return false
		}
		v := 0
		for _, c := range o {
			v = v*10 + int(c-'0')
		}
		if v > 255 {
			return false
		}
	}
	return true
}

// RedactPII returns text with high-confidence PII/secret spans replaced by
// typed placeholders, and the number of spans redacted. Safe to call on any
// prompt; returns the input unchanged (count 0) when nothing matches.
func RedactPII(text string) (string, int) {
	count := 0
	bump := func(repl string) string { count++; return repl }

	out := rePEM.ReplaceAllStringFunc(text, func(string) string { return bump("[redacted-private-key]") })
	out = reBearer.ReplaceAllStringFunc(out, func(m string) string {
		// Preserve the "Bearer " keyword, redact the token.
		loc := reBearer.FindStringSubmatchIndex(m)
		if loc == nil {
			return bump("[redacted-token]")
		}
		prefix := m[loc[2]:loc[3]]
		return prefix + bump("[redacted-token]")
	})
	out = reSecret.ReplaceAllStringFunc(out, func(string) string { return bump("[redacted-secret]") })
	out = reEmail.ReplaceAllStringFunc(out, func(string) string { return bump("[redacted-email]") })
	out = rePhone.ReplaceAllStringFunc(out, func(string) string { return bump("[redacted-phone]") })
	out = reCardLike.ReplaceAllStringFunc(out, func(m string) string {
		if luhnValid(m) {
			return bump("[redacted-card]")
		}
		return m
	})
	out = reIPv4.ReplaceAllStringFunc(out, func(m string) string {
		sub := reIPv4.FindStringSubmatch(m)
		if sub != nil && ipOctetsValid(sub) {
			return bump("[redacted-ip]")
		}
		return m
	})
	_ = reDigit
	return out, count
}

// ApplyToPrompt redacts the prompt when the policy enables redactPII; otherwise
// returns it untouched. The chokepoint right before a prompt is handed to a
// runner.
func (p DataPolicy) ApplyToPrompt(prompt string) string {
	if !p.RedactPII {
		return prompt
	}
	out, _ := RedactPII(prompt)
	return out
}

// tasksToPrune returns the indices of tasks that exceed the retention window:
// finished (Completed/Failed/Cancelled) tasks whose FinishedAt is older than
// retentionDays before `now`. retentionDays <= 0 disables pruning (returns
// nil). Running/pending tasks are never pruned regardless of age.
func tasksToPrune(tasks []*Task, retentionDays int, now time.Time) []int {
	if retentionDays <= 0 {
		return nil
	}
	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
	var out []int
	for i, t := range tasks {
		if t == nil || t.FinishedAt == nil {
			continue
		}
		if t.FinishedAt.Before(cutoff) {
			out = append(out, i)
		}
	}
	return out
}
