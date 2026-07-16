package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseBootstrapPasskey(t *testing.T) {
	t.Run("bootstrap agent yields an upper-cased code", func(t *testing.T) {
		raw := []byte(`{"bootstrapPasskey":"t992vh","mode":"bootstrap","needsAuth":true,"ok":true}`)
		code, err := parseBootstrapPasskey(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if code != "T992VH" {
			t.Fatalf("code = %q, want T992VH", code)
		}
	})

	// An adopted agent requires auth on /info, so an unauthenticated GET
	// gets a non-JSON body. That is the SUCCESS signal for the box being
	// signed in already — it must read as "nothing to do", not "parse bug".
	t.Run("non-JSON body means already signed in", func(t *testing.T) {
		_, err := parseBootstrapPasskey([]byte("unauthorized\n"))
		if err == nil {
			t.Fatal("want an error for a non-JSON body")
		}
		if !strings.Contains(err.Error(), "already be signed in") {
			t.Fatalf("error should point at the signed-in case, got: %v", err)
		}
	})

	t.Run("healthy agent JSON without a passkey", func(t *testing.T) {
		raw := []byte(`{"ok":true,"lifecycleState":"ready-to-connect","version":"1.99.307"}`)
		_, err := parseBootstrapPasskey(raw)
		if err == nil {
			t.Fatal("want an error when no pair window is open")
		}
		if !strings.Contains(err.Error(), "not in bootstrap mode") {
			t.Fatalf("error should name the missing pair window, got: %v", err)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		if _, err := parseBootstrapPasskey(nil); err == nil {
			t.Fatal("want an error for an empty body")
		}
	})
}

func TestPairSubmitPayload(t *testing.T) {
	t.Run("carries token and backend url", func(t *testing.T) {
		raw, err := pairSubmitPayload(&Config{AuthToken: "tok-abc", ConvexSiteURL: "https://example.convex.site"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		if body["token"] != "tok-abc" {
			t.Fatalf("token = %v, want tok-abc", body["token"])
		}
		if body["convexSiteUrl"] != "https://example.convex.site" {
			t.Fatalf("convexSiteUrl = %v", body["convexSiteUrl"])
		}
	})

	// Pushing a blank token would hand the target an empty session — the
	// exact shape of the bug this whole file exists to recover from.
	t.Run("refuses to push a blank token", func(t *testing.T) {
		if _, err := pairSubmitPayload(&Config{ConvexSiteURL: "https://example.convex.site"}); err == nil {
			t.Fatal("want an error when this machine has no token")
		}
		if _, err := pairSubmitPayload(&Config{AuthToken: "   ", ConvexSiteURL: "https://x.site"}); err == nil {
			t.Fatal("whitespace token must not count as signed in")
		}
	})

	t.Run("refuses without a backend url", func(t *testing.T) {
		if _, err := pairSubmitPayload(&Config{AuthToken: "tok"}); err == nil {
			t.Fatal("want an error when there is no backend URL to hand over")
		}
	})

	t.Run("nil config", func(t *testing.T) {
		if _, err := pairSubmitPayload(nil); err == nil {
			t.Fatal("want an error for a nil config")
		}
	})
}

func TestSSHTargetHint(t *testing.T) {
	cases := []struct {
		name string
		in   *DeviceInfo
		want string
	}{
		{"alias wins", &DeviceInfo{Alias: "mac-mini", DeviceID: "229aeb03", Name: "Mobiles-Mac-mini.local"}, "mac-mini"},
		{"device id when no alias", &DeviceInfo{DeviceID: "229aeb03", Name: "Mobiles-Mac-mini.local"}, "229aeb03"},
		{"name as last resort", &DeviceInfo{Name: "Mobiles-Mac-mini.local"}, "Mobiles-Mac-mini.local"},
		{"blank alias is skipped", &DeviceInfo{Alias: "  ", DeviceID: "229aeb03"}, "229aeb03"},
		{"nothing to go on", &DeviceInfo{}, ""},
		{"nil device", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sshTargetHint(tc.in); got != tc.want {
				t.Fatalf("sshTargetHint = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgentLoopbackURL(t *testing.T) {
	// Always loopback: we run curl inside the target's own shell, so a
	// routed address would be both wrong and a way to leak the token off-box.
	if got := agentLoopbackURL(0); got != "http://127.0.0.1:18080" {
		t.Fatalf("default = %q", got)
	}
	if got := agentLoopbackURL(-1); got != "http://127.0.0.1:18080" {
		t.Fatalf("negative port should fall back to the default, got %q", got)
	}
	if got := agentLoopbackURL(19000); got != "http://127.0.0.1:19000" {
		t.Fatalf("explicit port = %q", got)
	}
}

func TestFirstErrLine(t *testing.T) {
	if got := firstErrLine("boom\nsecond line\n"); got != "boom" {
		t.Fatalf("got %q", got)
	}
	if got := firstErrLine("   \n"); got != "" {
		t.Fatalf("blank stderr should stay blank so callers can substitute, got %q", got)
	}
	long := strings.Repeat("x", 200)
	got := firstErrLine(long)
	if len([]rune(got)) != 121 {
		t.Fatalf("want 120 runes + ellipsis, got %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("want an ellipsis suffix, got %q", got)
	}
}

func TestRecoverDeviceAuthOverSSHRejectsUnusableTargets(t *testing.T) {
	// Both guards must fire before any ssh is attempted.
	if _, err := recoverDeviceAuthOverSSH(t.Context(), &Config{AuthToken: "t", ConvexSiteURL: "https://x"}, &DeviceInfo{}); err == nil {
		t.Fatal("want an error when there is no hint to ssh to")
	}
	if _, err := recoverDeviceAuthOverSSH(t.Context(), &Config{}, &DeviceInfo{Alias: "mac-mini"}); err == nil {
		t.Fatal("want an error when this machine isn't signed in")
	}
}
