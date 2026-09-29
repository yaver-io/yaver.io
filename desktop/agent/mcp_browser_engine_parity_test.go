package main

import (
	"os"
	"strings"
	"testing"
)

// browserCaseBlock returns the source from `case "name":` up to the next
// `\n\tcase "` at the same indentation. Good enough for a structural check.
func browserCaseBlock(src, name string) string {
	marker := "case \"" + name + "\":"
	i := strings.Index(src, marker)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	tail := rest[len(marker):]
	if j := strings.Index(tail, "\n\tcase \""); j >= 0 {
		return rest[:len(marker)+j]
	}
	return rest
}

// TestBrowserToolsRouteBothEngines is the guard for browser-automation parity
// across engines. browser_open advertises "one session_id used by the same
// browser_* actions regardless of engine" — Chrome via CDP, Firefox/Safari via
// W3C WebDriver (the selenium lane). If a session-bound tool forgets to consult
// the WebDriver lane, a Safari/Firefox session silently fails with "no such
// session" and the advertised parity is a lie. This reads httpserver.go and
// fails when any such tool is Chrome-only.
func TestBrowserToolsRouteBothEngines(t *testing.T) {
	src, err := os.ReadFile("httpserver.go")
	if err != nil {
		t.Fatalf("read httpserver.go: %v", err)
	}
	// Every session-bound browser tool must reach BOTH lanes.
	sessionBound := []string{
		"browser_navigate", "browser_click", "browser_type", "browser_select",
		"browser_scroll", "browser_wait", "browser_wait_navigation",
		"browser_screenshot", "browser_extract_text", "browser_extract_attribute",
		"browser_get_dom", "browser_snapshot", "browser_evaluate", "browser_close",
	}
	for _, name := range sessionBound {
		block := browserCaseBlock(string(src), name)
		if block == "" {
			t.Errorf("case %q not found in httpserver.go", name)
			continue
		}
		if !strings.Contains(block, "seleniumMCP.") {
			t.Errorf("%s does not route to the WebDriver (Firefox/Safari) lane — a Safari/Firefox session would fail", name)
		}
	}
}

// TestSeleniumReadinessNamesEveryEngine proves the readiness report covers
// Chrome, Firefox and Safari with a route to the fix, so browser_open can be
// diagnosed instead of failing at session start.
func TestSeleniumReadinessNamesEveryEngine(t *testing.T) {
	st := seleniumReadiness()
	engines, ok := st["engines"].(map[string]interface{})
	if !ok {
		t.Fatalf("seleniumReadiness() missing engines map: %#v", st["engines"])
	}
	for _, name := range []string{"chrome", "firefox", "safari"} {
		e, ok := engines[name].(map[string]interface{})
		if !ok {
			t.Fatalf("engines[%q] missing", name)
		}
		if _, hasReady := e["ready"]; !hasReady {
			t.Errorf("engines[%q] has no ready flag", name)
		}
	}
}

// TestDriverReadinessFirefoxNamesInstall guards the four-layer rule: a missing
// geckodriver must name the install, not just report absence.
func TestDriverReadinessFirefoxNamesInstall(t *testing.T) {
	e := driverReadiness("firefox")
	if e["browser"] != "firefox" || e["driver"] != "geckodriver" {
		t.Fatalf("unexpected: %#v", e)
	}
	// On a machine without geckodriver, a fix hint must be present.
	if ready, _ := e["ready"].(bool); !ready {
		if hint, _ := e["install_hint"].(string); hint == "" {
			t.Fatal("missing geckodriver must carry an install_hint")
		}
	}
}

func TestDriverReadinessUnknownEngine(t *testing.T) {
	e := driverReadiness("webkit")
	if ready, _ := e["ready"].(bool); ready {
		t.Fatal("unknown engine must not report ready")
	}
}
