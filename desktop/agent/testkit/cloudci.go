package testkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Cloud-CI short-circuit markers.
//
// When the dev runs `yaver test run` and everything passes, the agent
// writes a tiny marker file recording (sha, branch, passed_at) into
// the project's `.yaver-test-results/markers/` directory. A future
// `yaver test sync` command — or a step in a GH Actions workflow —
// can read these markers and skip re-running tests that already
// passed locally on the same SHA.
//
// Markers are local-first by default. They're plain JSON, gitignored
// per the existing `.gitignore` template, and the dev decides whether
// to share them via the existing P2P transport between their own
// devices. Nothing is sent to Convex.

// PassMarker is the on-disk record for a successful run.
type PassMarker struct {
	SHA       string    `json:"sha"`
	Branch    string    `json:"branch,omitempty"`
	PassedAt  time.Time `json:"passed_at"`
	HostOS    string    `json:"host_os"`
	Total     int       `json:"total"`
	DurationS float64   `json:"duration_s"`
}

// MarkersDir returns the directory where pass markers live for a
// given spec root.
func MarkersDir(specRoot string) string {
	return filepath.Join(specRoot, ".yaver-test-results", "markers")
}

// WritePassMarker drops a marker JSON file. Quietly returns nil if
// `sha` is empty (e.g. the project isn't a git repo).
func WritePassMarker(specRoot, sha, branch, hostOS string, total int, duration time.Duration) error {
	if sha == "" {
		return nil
	}
	dir := MarkersDir(specRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	m := PassMarker{
		SHA:       sha,
		Branch:    branch,
		PassedAt:  time.Now(),
		HostOS:    hostOS,
		Total:     total,
		DurationS: duration.Seconds(),
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sanitizeName(sha)+".json"), body, 0o644)
}

// HasPassMarker checks whether a passing run for `sha` exists in the
// project. Used by `yaver test sync` and (eventually) by GH Actions
// workflows that want to short-circuit a redundant cloud run.
func HasPassMarker(specRoot, sha string) bool {
	if sha == "" {
		return false
	}
	p := filepath.Join(MarkersDir(specRoot), sanitizeName(sha)+".json")
	_, err := os.Stat(p)
	return err == nil
}

// LatestPassMarkers returns up to N most recent pass markers, newest
// first. Used by the mobile "Runs" tab to show "this SHA passed
// 3 minutes ago — no need to re-run."
func LatestPassMarkers(specRoot string, n int) ([]PassMarker, error) {
	dir := MarkersDir(specRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []PassMarker{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m PassMarker
		if err := json.Unmarshal(body, &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	// Newest first by PassedAt.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].PassedAt.Before(out[j].PassedAt); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// Sentinel "all passed" check used by reporters and CLI.
func suiteAllPassed(s *Suite) bool {
	if s == nil {
		return false
	}
	for _, r := range s.Results {
		if r == nil || !r.Passed {
			return false
		}
	}
	return true
}

// FormatMarker is a small helper for the CLI's `yaver test sync`
// output. Kept here so the CLI doesn't have to know JSON shapes.
func FormatMarker(m PassMarker) string {
	return fmt.Sprintf("%s %s passed %s ago (%d specs, %.1fs)",
		m.SHA[:7], m.Branch, time.Since(m.PassedAt).Round(time.Second), m.Total, m.DurationS)
}
