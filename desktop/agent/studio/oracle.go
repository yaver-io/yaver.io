package studio

import (
	"fmt"
	"strings"
)

// oracle.go — the deterministic oracle bank for the app-test agent's catch-only
// mode (docs/yaver-ai-app-test-agent.md §16). After every action the runner
// hands each oracle a Frame (the rendered screen + view hierarchy + log tail);
// an oracle returns any Bugs it detects. These oracles need NO model — they are
// pure string/structure checks, so they run free on every step and are unit-
// tested with fixtures. The model-backed oracle (the VLM "is this screen
// broken / does it match the goal?" verdict) lives in the agent package because
// it needs the inference lane; it implements the same Oracle interface.

// Bug is one issue caught while the agent drove the app.
type Bug struct {
	Title    string `json:"title"`
	Severity string `json:"severity"` // "low" | "medium" | "high" | "critical"
	Oracle   string `json:"oracle"`   // which detector fired
	Detail   string `json:"detail"`
	Step     int    `json:"step"`    // step index where caught
	ShotIdx  int    `json:"shotIdx"` // screenshot index into the run, -1 if none

	// Set by fix mode (qa_flow.go); "caught" in catch-only.
	Outcome    string `json:"outcome,omitempty"`    // "caught" | "fixed" | "attempted-unresolved"
	FixSummary string `json:"fixSummary,omitempty"` // the coding agent's change summary (draft, never committed)
}

// Key identifies a bug for dedup / fix-verification (oracle + title).
func (b Bug) Key() string { return b.Oracle + "|" + b.Title }

// Frame is what an oracle inspects after an action.
type Frame struct {
	Step       int
	Screenshot []byte
	ViewTree   string // UIAutomator dump XML
	Logcat     string // recent logcat tail
	Route      string // current route/screen hint, if known
	ShotIdx    int    // index of Screenshot in the run's capture list
}

// Oracle inspects a Frame and reports any bugs. Stateful oracles dedup across
// frames internally; Scan may return nil.
type Oracle interface {
	Name() string
	Scan(f Frame) []Bug
}

// DefaultOracles returns the deterministic oracle bank. Each is conservative —
// it only flags real, unambiguous breakage (a crash, a red box, a dead screen)
// to keep the signal high. Add the model-backed VLM oracle on top in the agent.
func DefaultOracles() []Oracle {
	return []Oracle{
		newSeenOracle(&RedBoxOracle{}),
		newSeenOracle(&CrashOracle{}),
		newSeenOracle(&BlankScreenOracle{}),
	}
}

// ScanAll runs every oracle over a frame and concatenates their bugs.
func ScanAll(oracles []Oracle, f Frame) []Bug {
	var out []Bug
	for _, o := range oracles {
		out = append(out, o.Scan(f)...)
	}
	return out
}

// --- RedBoxOracle: RN red-screen / unhandled JS exception ---

type RedBoxOracle struct{}

func (o *RedBoxOracle) Name() string { return "redbox" }

// redBoxLogMarkers are substrings that, on a logcat line, indicate the JS layer
// threw — the thing that paints React Native's red box.
var redBoxLogMarkers = []string{
	"Unhandled JS Exception",
	"Unhandled Promise Rejection",
	"ReactNativeJS: Error",
	"com.facebook.react.common.JavascriptException",
	"RedBox",
}

// redBoxViewMarkers indicate the red box is actually on screen right now.
var redBoxViewMarkers = []string{"RedBox", "Render Error", "redbox"}

func (o *RedBoxOracle) Scan(f Frame) []Bug {
	var bugs []Bug
	// One JS error spans several log lines — report the first marker hit once.
logloop:
	for _, line := range strings.Split(f.Logcat, "\n") {
		for _, m := range redBoxLogMarkers {
			if strings.Contains(line, m) {
				bugs = append(bugs, Bug{
					Title:    "JavaScript exception",
					Severity: "high",
					Oracle:   o.Name(),
					Detail:   strings.TrimSpace(line),
					Step:     f.Step, ShotIdx: f.ShotIdx,
				})
				break logloop
			}
		}
	}
	for _, m := range redBoxViewMarkers {
		if strings.Contains(f.ViewTree, m) {
			bugs = append(bugs, Bug{
				Title:    "React Native red box visible on screen",
				Severity: "critical",
				Oracle:   o.Name(),
				Detail:   "view hierarchy contains a red-box error overlay",
				Step:     f.Step, ShotIdx: f.ShotIdx,
			})
			break
		}
	}
	return bugs
}

// --- CrashOracle: native crash / ANR ---

type CrashOracle struct{}

func (o *CrashOracle) Name() string { return "crash" }

// crashLogMarkers are ordered strongest-first. One crash dumps many lines, so
// Scan reports only the FIRST marker hit per frame — a single, clean bug rather
// than one per stack-trace line.
var crashLogMarkers = []struct {
	marker   string
	title    string
	severity string
}{
	{"FATAL EXCEPTION", "Native crash (FATAL EXCEPTION)", "critical"},
	{"ANR in ", "App Not Responding (ANR)", "high"},
	{"Force finishing activity", "Activity force-finished after error", "high"},
}

func (o *CrashOracle) Scan(f Frame) []Bug {
	for _, line := range strings.Split(f.Logcat, "\n") {
		for _, m := range crashLogMarkers {
			if strings.Contains(line, m.marker) {
				return []Bug{{
					Title:    m.title,
					Severity: m.severity,
					Oracle:   o.Name(),
					Detail:   strings.TrimSpace(line),
					Step:     f.Step, ShotIdx: f.ShotIdx,
				}}
			}
		}
	}
	return nil
}

// --- BlankScreenOracle: dead / stuck / blank screen ---

type BlankScreenOracle struct{}

func (o *BlankScreenOracle) Name() string { return "blank-screen" }

func (o *BlankScreenOracle) Scan(f Frame) []Bug {
	if strings.TrimSpace(f.ViewTree) == "" {
		return nil // no dump available — can't judge, don't false-positive
	}
	nodes := strings.Count(f.ViewTree, "<node")
	hasText := strings.Contains(f.ViewTree, `text="`) &&
		!onlyEmptyText(f.ViewTree)
	// A real screen has structure + at least one non-empty text/label. A near-
	// empty hierarchy after an action usually means a blank or stuck render.
	if nodes <= 2 && !hasText {
		return []Bug{{
			Title:    "Blank or stuck screen (no content rendered)",
			Severity: "medium",
			Oracle:   o.Name(),
			Detail:   fmt.Sprintf("view hierarchy has %d node(s) and no visible text", nodes),
			Step:     f.Step, ShotIdx: f.ShotIdx,
		}}
	}
	return nil
}

// onlyEmptyText reports whether every text="" attribute in the dump is empty.
func onlyEmptyText(xml string) bool {
	for _, frag := range strings.Split(xml, `text="`)[1:] {
		end := strings.IndexByte(frag, '"')
		if end > 0 {
			return false // found a non-empty text value
		}
	}
	return true
}

// --- seenOracle: dedup wrapper so a persistent error doesn't re-report every step ---

type seenOracle struct {
	inner Oracle
	seen  map[string]bool
}

func newSeenOracle(inner Oracle) *seenOracle {
	return &seenOracle{inner: inner, seen: map[string]bool{}}
}

func (s *seenOracle) Name() string { return s.inner.Name() }

func (s *seenOracle) Scan(f Frame) []Bug {
	var fresh []Bug
	for _, b := range s.inner.Scan(f) {
		key := b.Oracle + "|" + b.Title + "|" + b.Detail
		if s.seen[key] {
			continue
		}
		s.seen[key] = true
		fresh = append(fresh, b)
	}
	return fresh
}
