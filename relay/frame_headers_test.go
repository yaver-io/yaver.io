package main

import "testing"

func TestStripFrameBlockingHeaders_removesXFrameOptions(t *testing.T) {
	h := map[string]string{
		"X-Frame-Options": "DENY",
		"Content-Type":    "text/html",
	}
	stripFrameBlockingHeaders(h)
	if _, ok := h["X-Frame-Options"]; ok {
		t.Fatalf("X-Frame-Options was not removed: %v", h)
	}
	if h["Content-Type"] != "text/html" {
		t.Fatalf("Content-Type should survive: got %q", h["Content-Type"])
	}
}

func TestStripFrameBlockingHeaders_caseInsensitive(t *testing.T) {
	h := map[string]string{
		"x-frame-options":            "SAMEORIGIN",
		"Cross-Origin-Opener-Policy": "same-origin",
		"cross-origin-embedder-policy": "require-corp",
	}
	stripFrameBlockingHeaders(h)
	for _, k := range []string{"x-frame-options", "Cross-Origin-Opener-Policy", "cross-origin-embedder-policy"} {
		if _, ok := h[k]; ok {
			t.Fatalf("%s was not removed: %v", k, h)
		}
	}
}

func TestStripFrameBlockingHeaders_stripsFrameAncestorsOnly(t *testing.T) {
	csp := "default-src 'self'; frame-ancestors 'self'; script-src 'self' 'unsafe-inline'"
	h := map[string]string{"Content-Security-Policy": csp}
	stripFrameBlockingHeaders(h)
	got := h["Content-Security-Policy"]
	if got == "" {
		t.Fatalf("CSP was deleted when other directives existed")
	}
	if contains(got, "frame-ancestors") {
		t.Fatalf("frame-ancestors directive survived: %q", got)
	}
	if !contains(got, "default-src 'self'") {
		t.Fatalf("default-src was lost: %q", got)
	}
	if !contains(got, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("script-src was lost: %q", got)
	}
}

func TestStripFrameBlockingHeaders_deletesCSPWhenOnlyFrameAncestors(t *testing.T) {
	h := map[string]string{"Content-Security-Policy": "frame-ancestors 'none'"}
	stripFrameBlockingHeaders(h)
	if _, ok := h["Content-Security-Policy"]; ok {
		t.Fatalf("CSP with only frame-ancestors should be deleted: %v", h)
	}
}

func TestStripFrameBlockingHeaders_preservesUnrelatedHeaders(t *testing.T) {
	h := map[string]string{
		"Content-Type":              "text/html",
		"Cache-Control":             "no-store",
		"Strict-Transport-Security": "max-age=31536000",
	}
	stripFrameBlockingHeaders(h)
	if len(h) != 3 {
		t.Fatalf("unrelated headers should survive untouched: %v", h)
	}
}

func TestStripFrameBlockingHeaders_emptyMap(t *testing.T) {
	var h map[string]string
	stripFrameBlockingHeaders(h) // must not panic
}

func TestStripCSPFrameAncestors_variants(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"frame-ancestors 'none'", ""},
		{"default-src 'self'", "default-src 'self'"},
		{"default-src 'self'; frame-ancestors https:", "default-src 'self'"},
		{"FRAME-ANCESTORS 'self'; default-src 'self'", "default-src 'self'"}, // case-insensitive directive
		{"default-src 'self';; frame-ancestors 'self'", "default-src 'self'"},
	}
	for _, c := range cases {
		if got := stripCSPFrameAncestors(c.in); got != c.want {
			t.Errorf("stripCSPFrameAncestors(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
