package main

// recovery_transport_test.go — locks in C-5: the X-Relay-Password header
// must be validated by VALUE before granting "private relay" status.
// Pre-fix, any non-empty header bypassed the public-recovery block.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyRecoveryIngressRelayPasswordValidation(t *testing.T) {
	cfg := &Config{
		RelayPassword:                  "the-real-password",
		CachedRelayPassword:            "old-cached-password",
		RequirePrivateRecoveryTransport: true,
	}

	cases := []struct {
		name         string
		header       string
		remote       string
		wantAllowed  bool
		wantTransport string
	}{
		{
			name:          "correct relay password unlocks",
			header:        "the-real-password",
			remote:        "8.8.8.8:55555", // public IP
			wantAllowed:   true,
			wantTransport: "relay",
		},
		{
			name:          "cached relay password also unlocks",
			header:        "old-cached-password",
			remote:        "8.8.8.8:55555",
			wantAllowed:   true,
			wantTransport: "relay",
		},
		{
			name:        "empty header — falls through to IP classification (public, blocked)",
			header:      "",
			remote:      "8.8.8.8:55555",
			wantAllowed: false,
		},
		{
			name:        "non-empty WRONG header — must NOT bypass (was the C-5 bug)",
			header:      "x",
			remote:      "8.8.8.8:55555",
			wantAllowed: false,
		},
		{
			name:        "non-empty wrong header on private LAN — still allowed via LAN classification",
			header:      "garbage",
			remote:      "192.168.1.5:1234",
			wantAllowed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/recover", nil)
			if tc.header != "" {
				req.Header.Set("X-Relay-Password", tc.header)
			}
			req.RemoteAddr = tc.remote
			verdict := classifyRecoveryIngress(req, cfg)
			if verdict.Allowed != tc.wantAllowed {
				t.Fatalf("Allowed=%v reason=%q transport=%q, want Allowed=%v",
					verdict.Allowed, verdict.Reason, verdict.Transport, tc.wantAllowed)
			}
			if tc.wantTransport != "" && verdict.Transport != tc.wantTransport {
				t.Errorf("Transport=%q, want %q", verdict.Transport, tc.wantTransport)
			}
		})
	}
}

func TestRecoveryRelayPasswordMatches(t *testing.T) {
	cfg := &Config{RelayPassword: "abc123"}
	if !recoveryRelayPasswordMatches(cfg, "abc123") {
		t.Error("exact match must succeed")
	}
	if recoveryRelayPasswordMatches(cfg, "abc124") {
		t.Error("close-but-wrong must fail")
	}
	if recoveryRelayPasswordMatches(cfg, "") {
		t.Error("empty input must fail even when stored is empty")
	}
	if recoveryRelayPasswordMatches(nil, "anything") {
		t.Error("nil cfg must fail")
	}
	// When live RelayPassword is empty but cached is set, cached should win.
	cfg2 := &Config{CachedRelayPassword: "xyz"}
	if !recoveryRelayPasswordMatches(cfg2, "xyz") {
		t.Error("cached match must succeed when live is empty")
	}
	// Empty stored on both sides — never match anything.
	cfg3 := &Config{}
	if recoveryRelayPasswordMatches(cfg3, "anything") {
		t.Error("must not match when both stored values are empty")
	}
}
