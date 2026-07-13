package main

import "testing"

// Pentest regression (audit 2026-07-13, A3): the http_request MCP tool must not
// be usable to read cloud-instance metadata or to abuse non-http schemes.
func TestGuardOutboundHTTPURL(t *testing.T) {
	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",              // AWS/Hetzner metadata
		"http://169.254.169.254/computeMetadata/v1/",            // GCP metadata
		"https://169.254.169.254/",                              // https metadata
		"http://[fe80::1]/",                                     // IPv6 link-local
		"file:///etc/passwd",                                    // arbitrary file read via curl
		"gopher://127.0.0.1:6379/_INFO",                         // raw-socket SSRF
		"dict://attacker/",                                      // scheme abuse
	}
	for _, u := range blocked {
		if err := guardOutboundHTTPURL(u); err == nil {
			t.Errorf("VULNERABLE: %q was allowed (expected refusal)", u)
		}
	}

	allowed := []string{
		"http://localhost:3000/health",   // owner dev — must still work
		"http://127.0.0.1:8080/",         // loopback dev
		"https://api.github.com/repos",   // normal public request
		"http://192.168.1.10/",           // owner LAN dev
	}
	for _, u := range allowed {
		if err := guardOutboundHTTPURL(u); err != nil {
			t.Errorf("REGRESSION: %q was refused (%v); legitimate use must pass", u, err)
		}
	}
}
