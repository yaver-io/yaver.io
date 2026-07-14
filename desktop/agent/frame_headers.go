package main

import "strings"

// stripFrameBlockingHeaders removes response headers that prevent /dev/*
// previews from being embedded in the dashboard. This mirrors relay/
// frame_headers.go; keep the two helpers in sync until the tunnel protocol is
// moved into a shared package.
func stripFrameBlockingHeaders(h map[string]string) {
	if len(h) == 0 {
		return
	}
	for key := range h {
		lower := strings.ToLower(key)
		switch lower {
		case "x-frame-options",
			"cross-origin-opener-policy",
			"cross-origin-embedder-policy",
			"cross-origin-resource-policy":
			delete(h, key)
		case "content-security-policy", "content-security-policy-report-only":
			if stripped := stripCSPFrameAncestors(h[key]); stripped != "" {
				h[key] = stripped
			} else {
				delete(h, key)
			}
		}
	}
}

func stripCSPFrameAncestors(csp string) string {
	if csp == "" {
		return ""
	}
	parts := strings.Split(csp, ";")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		if strings.EqualFold(fields[0], "frame-ancestors") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "; ")
}
