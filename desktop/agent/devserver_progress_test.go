package main

import "testing"

// TestRxMetroPctMatchesModernAndLegacyFormats locks in coverage of both
// the modern Metro (RN 0.76+) progress line and the legacy "Bundling"
// form so a future regex tweak can't silently regress the dashboard's
// progress bar back to stuck-at-0%.
func TestRxMetroPctMatchesModernAndLegacyFormats(t *testing.T) {
	cases := []struct {
		name         string
		line         string
		wantPct      string
		wantDone     string
		wantTotal    string
		wantPlatform string // matched group 0 covers prefix; the regex itself doesn't capture platform separately
	}{
		{
			name:      "modern iOS path-with-bar",
			line:      "iOS ./index.ts ▓▓▓░░░░░░░░░░░░░ 21.8% (294/692)",
			wantPct:   "21.8",
			wantDone:  "294",
			wantTotal: "692",
		},
		{
			name:      "modern iOS expo-router entry",
			line:      "iOS node_modules/expo-router/entry.js ▓▓▓▓▓▓▓ 44.4% ( 667/1025)",
			wantPct:   "44.4",
			wantDone:  "667",
			wantTotal: "1025",
		},
		{
			name:      "modern Web path-with-bar",
			line:      "Web ./App.tsx ▓░░░░░░░░░░░░░░░ 5.0% (10/200)",
			wantPct:   "5.0",
			wantDone:  "10",
			wantTotal: "200",
		},
		{
			name:      "legacy iOS Bundling form",
			line:      "iOS Bundling 67.3% (1247/2390)",
			wantPct:   "67.3",
			wantDone:  "1247",
			wantTotal: "2390",
		},
		{
			name:      "legacy Android Bundling form",
			line:      "Android Bundling 12% (3/100)",
			wantPct:   "12",
			wantDone:  "3",
			wantTotal: "100",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := rxMetroPct.FindStringSubmatch(tc.line)
			if m == nil {
				t.Fatalf("regex did not match line: %q", tc.line)
			}
			if m[1] != tc.wantPct {
				t.Errorf("pct = %q, want %q", m[1], tc.wantPct)
			}
			if m[2] != tc.wantDone {
				t.Errorf("done = %q, want %q", m[2], tc.wantDone)
			}
			if m[3] != tc.wantTotal {
				t.Errorf("total = %q, want %q", m[3], tc.wantTotal)
			}
		})
	}
}

// TestRxBundleCompleteMatchesModernAndLegacy locks in both Metro
// completion shapes (`iOS Bundled 1283ms ...` modern, `iOS Bundling
// complete 5678ms` legacy) so a fast cached build always emits the
// "ready" phase event the dashboard needs to clear the bar.
func TestRxBundleCompleteMatchesModernAndLegacy(t *testing.T) {
	cases := []struct {
		name string
		line string
		ok   bool
	}{
		{"modern iOS Bundled", "iOS Bundled 1283ms index.ts (1088 modules)", true},
		{"modern Web Bundled", "Web Bundled 8421ms App.tsx (532 modules)", true},
		{"legacy Bundling complete", "iOS Bundling complete 5678ms", true},
		{"non-completion line", "iOS ./index.ts ▓░░░ 5.0% (10/200)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := rxBundleComplete.FindStringSubmatch(tc.line)
			got := m != nil
			if got != tc.ok {
				t.Errorf("matched = %v, want %v (line: %q)", got, tc.ok, tc.line)
			}
		})
	}
}

// TestFlutterWebStagesEmitSummarizedPhases is a closed-loop check that Flutter's
// stdout (which has NO percentages) is translated into the summarized phase
// progression the mobile preview shows: pub get → launching → compiling →
// serving(ready). It locks the "black screen with no signal" incident shut:
// before this, Flutter output produced ZERO phase events, so the phone had
// nothing to show while `flutter run -d web-server` compiled for ~30s.
func TestFlutterWebStagesEmitSummarizedPhases(t *testing.T) {
	var phases []string
	emit := func(e DevServerEvent) {
		if e.Type == "phase" && e.Phase != "" {
			phases = append(phases, e.Phase)
		}
	}
	tr := newProgressTracker(emit, "flutter", "dev/start", "web-reload")
	// Verbatim lines from a real `flutter run -d web-server` session.
	for _, l := range []string{
		`Running "flutter pub get" in e-mobile...`,
		`Launching lib/main.dart on Web Server in debug mode...`,
		`Compiling lib/main.dart for the Web...`,
		`Waiting for connection from debug service on Web Server...`, // still compiling; must NOT double-emit
		`lib/main.dart is being served at http://127.0.0.1:9100`,
	} {
		tr.FeedLine(l)
	}
	want := []string{"installing_deps", "launching", "compiling", "ready"}
	if !isSubsequence(phases, want) {
		t.Fatalf("flutter phases = %v, want it to contain subsequence %v", phases, want)
	}
	// "ready" must be the terminal phase.
	if phases[len(phases)-1] != "ready" {
		t.Fatalf("last flutter phase = %q, want \"ready\"", phases[len(phases)-1])
	}
}

// TestFlutterServedIsRecognizedAcrossPorts guards the readiness marker — the
// exact line Flutter prints when the web build is finally available.
func TestFlutterServedIsRecognizedAcrossPorts(t *testing.T) {
	for _, l := range []string{
		`lib/main.dart is being served at http://127.0.0.1:9100`,
		`lib/main.dart is being served at http://localhost:8080`,
	} {
		if !rxFlutterServed.MatchString(l) {
			t.Fatalf("rxFlutterServed did not match served line: %q", l)
		}
	}
	// A compile-error line must NOT be mistaken for "served".
	if rxFlutterServed.MatchString(`No file or variants found for asset: .env.`) {
		t.Fatal("rxFlutterServed matched a compile-error line")
	}
}

func isSubsequence(haystack, needle []string) bool {
	i := 0
	for _, h := range haystack {
		if i < len(needle) && h == needle[i] {
			i++
		}
	}
	return i == len(needle)
}
