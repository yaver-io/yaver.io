package main

// hermes_isolation_test.go — load-bearing regression gate.
//
// Phase 9 of the WebRTC remote-runtime work (see
// docs/native-webrtc-web-streaming.md §12). The Hermes hot-reload
// path lives in `devserver_*.go` + `native_modules_compat.go` +
// `mobile/app/(tabs)/apps.tsx`. The WebRTC remote-runtime path
// lives in the family of files matched below. They share the same
// Go package (`main`), so nothing at the language level prevents a
// future refactor from reaching across the boundary.
//
// This test asserts that no WebRTC-region file references any
// symbol whose home is the Hermes region. If a developer ever
// genuinely needs to cross the boundary they must edit the
// hermesSymbolsForbiddenInWebRTC list explicitly — at which point
// the change shows up in code review and we can decide whether the
// coupling is intentional or worth fighting.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// webrtcRegionFiles is the explicit allow-list of WebRTC-side
// source files. New files added to the WebRTC family should be
// appended here. Test files are excluded so we don't accidentally
// flag a `// references handleDevServer*` comment in a fixture.
var webrtcRegionFiles = []string{
	"remote_runtime.go",
	"remote_runtime_webrtc.go",
	"remote_runtime_dims.go",
	"remote_runtime_video_track.go",
	"remote_builder.go",
	"remote_builder_cmd.go",
	"h264_extract.go",
	"doctor_webrtc.go",
	"turn_credentials.go",
	"swift_toolchain.go",
	"swift_cmd.go",
}

// hermesSymbolsForbiddenInWebRTC enumerates Hermes-only entry points
// + helpers. A WebRTC-region file referencing any of these is the
// signal we want to catch. Drawn from grep of devserver_*.go and
// native_modules_compat.go; extend when new Hermes endpoints are
// added.
var hermesSymbolsForbiddenInWebRTC = []string{
	// devserver_http.go handler functions
	"handleDevServerStatus",
	"handleDevServerTarget",
	"handleDevServerStart",
	"handleDevServerStop",
	"handleDevServerReload",
	"handleDevServerEvents",
	"handleDevWebProxy",
	"handleDevWebPreviewStart",
	"handleDevWebPreviewStop",
	"handleDevServerProxy",
	"handleDevServerBuilds",
	"handleDevServerCompatibility",
	// Hermes bundle compat machinery
	"BuildNativeModuleCompatReport",
	"BuildNativeModuleCompatReportWithFamilies",
	"isLikelyNativeModule",
}

func TestWebRTCFilesDoNotReferenceHermesSymbols(t *testing.T) {
	for _, f := range webrtcRegionFiles {
		path := filepath.Join(".", f)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v (test allow-list is stale — remove or rename the entry in webrtcRegionFiles)", path, err)
			continue
		}
		for _, sym := range hermesSymbolsForbiddenInWebRTC {
			// Word-boundary-ish check: we want
			//   `handleDevServerStatus(`  → match
			//   `myhandleDevServerStatusFoo` → no match
			// A simple suffix/prefix-byte check is enough — Go
			// identifiers can't start with a digit, so adjacent
			// alphanumerics on either side are the only way the
			// substring would be embedded in a different token.
			occurrences := allWordOccurrences(body, []byte(sym))
			if len(occurrences) > 0 {
				t.Errorf("%s references Hermes symbol %q — boundary breach (line(s): %v).\n"+
					"If this coupling is intentional, edit hermesSymbolsForbiddenInWebRTC\n"+
					"AND document the reason in the PR (the WebRTC region is supposed to\n"+
					"stay isolated from the Hermes hot-reload path).",
					path, sym, occurrences)
			}
		}
	}
}

// allWordOccurrences returns the 1-based line numbers where needle
// appears as a whole-word match in body. Whole-word means: bytes
// adjacent to the match are NOT Go identifier continuation
// characters (letters, digits, '_'). Eliminates false positives
// from substrings embedded in larger identifiers.
func allWordOccurrences(body, needle []byte) []int {
	var lines []int
	idx := 0
	line := 1
	// Pre-compute newline positions so we can map a byte offset to
	// a line number in O(log n) — but for files under ~5k lines a
	// linear scan is plenty fast.
	for {
		pos := bytes.Index(body[idx:], needle)
		if pos < 0 {
			break
		}
		abs := idx + pos
		// Word-boundary check.
		var leftOK, rightOK bool
		if abs == 0 {
			leftOK = true
		} else {
			leftOK = !isIdentByte(body[abs-1])
		}
		end := abs + len(needle)
		if end >= len(body) {
			rightOK = true
		} else {
			rightOK = !isIdentByte(body[end])
		}
		if leftOK && rightOK {
			// Count newlines from current scan head to abs.
			for i := idx; i < abs; i++ {
				if body[i] == '\n' {
					line++
				}
			}
			lines = append(lines, line)
			idx = end
		} else {
			// Skip past this byte without re-scanning it.
			idx = abs + 1
		}
	}
	return lines
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_'
}

func TestHermesIsolationGate_ItselfDoesNotFlagFalsePositives(t *testing.T) {
	// Sanity check on the gate's own logic. The string
	// "handleDevServerStatus" embedded in a larger word should NOT
	// match. The bare token, surrounded by non-identifier bytes,
	// should match. This protects against accidental tightening
	// (e.g. switching to a strstr-style scan) that would surface
	// false positives in a comment like "see also
	// foohandleDevServerStatusBar".
	body := []byte(`
// see myhandleDevServerStatusFoo for context — should not match
result = handleDevServerStatus(...)  // should match this line
`)
	got := allWordOccurrences(body, []byte("handleDevServerStatus"))
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 hit, got %v", got)
	}
}
