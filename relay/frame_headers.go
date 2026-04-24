package main

// frame_headers.go — strip frame-blocking response headers from /dev/*
// proxied responses so the Web Reload dashboard tab can iframe them.
//
// Scope is narrow by design: these functions are only called for
// request paths that start with /dev/, and the agent itself only
// proxies /dev/ to the user's own dev server. Production traffic on
// any other path is untouched. The helper lives in its own file so
// the stripping logic is testable in isolation from the tunnel I/O.

import "strings"

// stripFrameBlockingHeaders removes headers that prevent the dev
// server's HTML from being embedded inside an iframe on the web
// dashboard, while leaving other security-relevant headers intact.
func stripFrameBlockingHeaders(h map[string]string) {
	if len(h) == 0 {
		return
	}
	// Walk the map once. Keys may arrive in any casing (upstream
	// libraries vary), so match case-insensitively and remove by the
	// actual stored key.
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

// stripCSPFrameAncestors returns the CSP string with the
// `frame-ancestors` directive removed. All other directives
// (default-src, script-src, …) are preserved verbatim.
//
// CSP syntax: "directive-name value value; directive-name value; …"
// Whitespace around `;` is insignificant.
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
