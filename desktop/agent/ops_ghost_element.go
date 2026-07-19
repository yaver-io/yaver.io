package main

// ops_ghost_element.go — act on the accessibility tree BY NAME instead of by
// guessed screen coordinate.
//
// WHY THIS EXISTS:
// Before this file, every ghost click was a blind pixel. `ghost_locate`
// (ghost_vision.go) asks a vision model for a single {x,y} and clicks it —
// one shot, no verification, and the model is guessing from a JPEG. Meanwhile
// the OS already knows exactly where every button is: ghost.Tree returns a real
// semantic tree on ALL THREE OSes (AX API on macOS, UIAutomation on Windows,
// AT-SPI2 on Linux) with role, name and bounds — and until now nothing acted on
// it. This is the difference between "clicks approximately where it thinks a
// button is" and "clicks the button".
//
// THE RELIABILITY CONTRACT — read before changing behaviour here:
// When a query matches more than one element, these verbs REFUSE and return the
// candidate list. They do not pick the first match. Guessing is exactly the
// failure mode this file exists to remove, and a wrong click in a CAD app or an
// ERP is destructive in a way a wrong click in a browser is not. Callers
// disambiguate with `role` or an explicit `index`.
//
// KNOWN LIMITATION (uniform across all three OSes, verified in the impls):
// Tree.ElementTree(window) IGNORES its argument and always returns the FOCUSED
// application's tree (ghost/tree_darwin.go:136 axFocusedApp,
// tree_windows.go:109 psFocusedTree, tree_linux.go:121 pyActiveTree). So these
// verbs operate on the frontmost app. Reaching a background app means focusing
// it first — which is why ghost_focus_app lives here too.
//
// Depth/breadth are bounded by the platform walkers (depth 5-6, 40 children per
// level), so very deep elements are invisible to Find. That is a ghost-package
// limit, not one introduced here; a miss reports "not found", never a wrong hit.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/yaver-io/agent/ghost"
)

// ghostElementMatch is one candidate, flattened for JSON. Center is
// precomputed because it is what every caller actually wants — and computing
// it here keeps the "where do I click" rule in exactly one place.
type ghostElementMatch struct {
	Index        int    `json:"index"`
	Role         string `json:"role,omitempty"`
	Name         string `json:"name,omitempty"`
	Value        string `json:"value,omitempty"`
	AutomationID string `json:"automationId,omitempty"`
	X            int    `json:"x"`
	Y            int    `json:"y"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	CenterX      int    `json:"centerX"`
	CenterY      int    `json:"centerY"`
	Path         string `json:"path,omitempty"` // "Window > Toolbar > Save"
}

// ghostElementQuery is the shared selector for every verb here.
//
// Index is a *int, not an int, on purpose: with a plain int there is no way to
// distinguish "the caller chose match 0" from "the caller said nothing", so
// index 0 — the most likely choice in an ambiguous set — would be unselectable.
type ghostElementQuery struct {
	Query string `json:"query"`
	Role  string `json:"role"`
	Exact bool   `json:"exact"`
	Index *int   `json:"index"`
}

// collectGhostElements walks the focused app's tree and returns every node
// matching the selector.
//
// Matching unifies the three platforms deliberately: each OS's own Find checks
// a different field set (darwin Name+Value, windows Name+AutomationID, linux
// Name only). Matching the superset here means one predictable behaviour
// everywhere instead of three, and it is why this walks the tree itself rather
// than calling Tree.Find — which additionally only ever returns the FIRST hit,
// making the ambiguity check above impossible.
func collectGhostElements(root ghost.Node, q ghostElementQuery) []ghostElementMatch {
	needle := strings.ToLower(strings.TrimSpace(q.Query))
	roleFilter := strings.ToLower(strings.TrimSpace(q.Role))

	var out []ghostElementMatch
	var walk func(n *ghost.Node, trail []string)
	walk = func(n *ghost.Node, trail []string) {
		if n == nil {
			return
		}
		label := n.Name
		if label == "" {
			label = n.Role
		}
		here := trail
		if label != "" {
			here = append(append([]string{}, trail...), label)
		}

		if ghostNodeMatches(n, needle, roleFilter, q.Exact) {
			// A zero-area node has no clickable point. Enumerating it would
			// invite a click at (0,0) — the top-left of the screen, which on
			// macOS is the Apple menu. Skip rather than offer it.
			if n.Width > 0 && n.Height > 0 {
				out = append(out, ghostElementMatch{
					Index:        len(out),
					Role:         n.Role,
					Name:         n.Name,
					Value:        n.Value,
					AutomationID: n.AutomationID,
					X:            n.X, Y: n.Y, Width: n.Width, Height: n.Height,
					CenterX: n.X + n.Width/2,
					CenterY: n.Y + n.Height/2,
					Path:    strings.Join(here, " > "),
				})
			}
		}
		for i := range n.Children {
			walk(&n.Children[i], here)
		}
	}
	walk(&root, nil)
	return out
}

func ghostNodeMatches(n *ghost.Node, needle, roleFilter string, exact bool) bool {
	if roleFilter != "" && !strings.EqualFold(strings.TrimSpace(n.Role), roleFilter) {
		return false
	}
	if needle == "" {
		// No query + a role filter = "list every button". No query and no role
		// would be the whole tree, which ghost_tree already does better.
		return roleFilter != ""
	}
	for _, field := range []string{n.Name, n.Value, n.AutomationID} {
		f := strings.ToLower(strings.TrimSpace(field))
		if f == "" {
			continue
		}
		if exact {
			if f == needle {
				return true
			}
			continue
		}
		if strings.Contains(f, needle) {
			return true
		}
	}
	return false
}

// resolveGhostElement enforces the no-guessing contract: exactly one match, or
// an explicit index into an ambiguous set. Returns an OpsResult on refusal so
// callers can return it verbatim.
func resolveGhostElement(eng *ghost.Engine, q ghostElementQuery) (*ghostElementMatch, *OpsResult) {
	root, err := eng.Tree.ElementTree("")
	if err != nil {
		return nil, &OpsResult{
			OK:    false,
			Code:  "unsupported",
			Error: "accessibility tree unavailable: " + err.Error() + ghostTreeHint(),
		}
	}
	matches := collectGhostElements(root, q)
	if len(matches) == 0 {
		return nil, &OpsResult{
			OK:      false,
			Code:    "not_found",
			Error:   fmt.Sprintf("no element matching %q in the focused application", q.Query),
			Initial: map[string]interface{}{"query": q.Query, "role": q.Role},
		}
	}
	if q.Index != nil {
		idx := *q.Index
		if idx < 0 || idx >= len(matches) {
			return nil, &OpsResult{
				OK:      false,
				Code:    "bad_payload",
				Error:   fmt.Sprintf("index %d out of range (%d matches)", idx, len(matches)),
				Initial: map[string]interface{}{"matches": matches},
			}
		}
		m := matches[idx]
		return &m, nil
	}
	if len(matches) > 1 {
		// Refuse and hand back the candidates. This is the contract.
		return nil, &OpsResult{
			OK:   false,
			Code: "ambiguous",
			Error: fmt.Sprintf("%d elements match %q — pass `index` (or narrow with `role`/`exact`) to choose one",
				len(matches), q.Query),
			Initial: map[string]interface{}{"matches": matches, "count": len(matches)},
		}
	}
	m := matches[0]
	return &m, nil
}

// ---- closed loop ---------------------------------------------------------

// verifyGhostElement re-reads the accessibility tree AFTER an action and reports
// whether the expected post-condition now holds.
//
// THIS IS THE CLOSED LOOP. Every GUI action in this codebase was previously
// fire-and-forget: ghost's Engine.Act is capture → locate → execute with no
// re-screenshot (ghost/vision.go:83-85 explicitly delegates the verify loop to
// an out-of-repo caller). Fire-and-forget is fine for a browser and unacceptable
// for AutoCAD or an ERP, where "I typed it" and "it went in" are different
// claims and only the second one is worth reporting to a user who cannot see
// the screen.
//
// The tree is ground truth — far better than re-describing a JPEG to a vision
// model — because AX/UIAutomation/AT-SPI expose an element's VALUE. After
// typing we can read the field back and know, not guess.
//
// wantValue is matched case-insensitively as a substring: a text field may
// reformat what it was given (trimming, autocomplete, currency symbols), so
// demanding equality would report false failures on a correct action.
func verifyGhostElement(eng *ghost.Engine, q ghostElementQuery, wantValue string) (bool, string) {
	root, err := eng.Tree.ElementTree("")
	if err != nil {
		// Cannot see, so cannot confirm. Report unverified rather than
		// claiming success — an unverifiable action is not a successful one.
		return false, "could not re-read the screen: " + err.Error()
	}
	matches := collectGhostElements(root, q)
	if len(matches) == 0 {
		return false, "the element is no longer on screen"
	}
	if wantValue == "" {
		return true, ""
	}
	want := strings.ToLower(strings.TrimSpace(wantValue))
	for _, m := range matches {
		if strings.Contains(strings.ToLower(m.Value), want) ||
			strings.Contains(strings.ToLower(m.Name), want) {
			return true, ""
		}
	}
	got := matches[0].Value
	if got == "" {
		got = "(empty)"
	}
	return false, "the field reads " + got
}

// ghostTreeHint appends the per-OS reason a tree read usually fails, so the
// caller gets a fix rather than just a failure.
func ghostTreeHint() string {
	switch runtime.GOOS {
	case "darwin":
		return " — grant Accessibility permission to the agent in System Settings > Privacy & Security > Accessibility"
	case "linux":
		return " — install python3-pyatspi and ensure the AT-SPI bus is running (X11 only; Wayland is unsupported)"
	case "windows":
		return " — requires an unlocked interactive desktop session"
	}
	return ""
}

// ---- verbs ---------------------------------------------------------------

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "ghost_elements",
		Description: "List accessibility elements in the FOCUSED application matching a query and/or role, " +
			"with bounds and a precomputed click point. Use this to discover what is on screen before acting. " +
			"Far more reliable than ghost_screenshot + vision guessing. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "Case-insensitive substring of the element's name, value or automationId."},
			"role":  map[string]interface{}{"type": "string", "description": "Filter by role, e.g. AXButton / Button / push button. Exact, case-insensitive."},
			"exact": map[string]interface{}{"type": "boolean", "description": "Require a whole-field match instead of substring."},
		}),
		Handler:    ghostElementsHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "ghost_click_element",
		Description: "Click an element BY NAME in the focused application, resolved through the OS accessibility " +
			"tree (no coordinate guessing). Refuses with the candidate list if the query is ambiguous — pass `index` to choose. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"query":  map[string]interface{}{"type": "string"},
			"role":   map[string]interface{}{"type": "string"},
			"exact":  map[string]interface{}{"type": "boolean"},
			"index":  map[string]interface{}{"type": "integer", "description": "Which match to click when several tie (from ghost_elements)."},
			"button": map[string]interface{}{"type": "string", "enum": []string{"left", "right", "middle"}},
			"double": map[string]interface{}{"type": "boolean"},
			"expect": map[string]interface{}{"type": "string", "description": "CLOSED LOOP: an element name that must appear after the click. Verified by re-reading the accessibility tree; the verb fails with code=unverified if it does not."},
		}, "query"),
		Handler:    ghostClickElementHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "ghost_type_into_element",
		Description: "Focus an element by name (click it) then type text into it. Same disambiguation contract as " +
			"ghost_click_element. Optionally clears the field first. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"query":    map[string]interface{}{"type": "string"},
			"role":     map[string]interface{}{"type": "string"},
			"exact":    map[string]interface{}{"type": "boolean"},
			"index":    map[string]interface{}{"type": "integer"},
			"text":     map[string]interface{}{"type": "string"},
			"clear":    map[string]interface{}{"type": "boolean", "description": "Select-all then overwrite (cmd/ctrl+a)."},
			"noVerify": map[string]interface{}{"type": "boolean", "description": "Skip the read-back check. Default false: the value is re-read from the tree and a mismatch fails with code=unverified."},
		}, "query", "text"),
		Handler:    ghostTypeIntoElementHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "ghost_launch_app",
		Description: "Start an application by name on the target machine ('Safari', 'AutoCAD'). Use ghost_focus_app " +
			"to raise one that is already running. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"app": map[string]interface{}{"type": "string"},
		}, "app"),
		Handler:    ghostLaunchAppHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "ghost_focus_app",
		Description: "Bring an application to the foreground by name. The accessibility tree only ever exposes the " +
			"FOCUSED app, so this is the prerequisite for driving anything that is not already frontmost. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"app": map[string]interface{}{"type": "string", "description": "Application name, e.g. \"Safari\", \"AutoCAD\"."},
		}, "app"),
		Handler:    ghostFocusAppHandler,
		AllowGuest: false,
	})
}

func ghostElementsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var q ghostElementQuery
	if r := ghostUnmarshal(payload, &q); r != nil {
		return *r
	}
	if strings.TrimSpace(q.Query) == "" && strings.TrimSpace(q.Role) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "provide `query` and/or `role`"}
	}
	root, err := eng.Tree.ElementTree("")
	if err != nil {
		return OpsResult{OK: false, Code: "unsupported", Error: "accessibility tree unavailable: " + err.Error() + ghostTreeHint()}
	}
	matches := collectGhostElements(root, q)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"matches": matches,
		"count":   len(matches),
	}}
}

func ghostClickElementHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		ghostElementQuery
		Button string `json:"button"`
		Double bool   `json:"double"`
		Expect string `json:"expect"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if strings.TrimSpace(p.Query) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "`query` is required"}
	}
	m, deny := resolveGhostElement(eng, p.ghostElementQuery)
	if deny != nil {
		return *deny
	}
	btn := ghost.Button(p.Button)
	if btn == "" {
		btn = ghost.ButtonLeft
	}
	var err error
	if p.Double {
		err = eng.Input.DoubleClick(btn, m.CenterX, m.CenterY)
	} else {
		err = eng.Input.Click(btn, m.CenterX, m.CenterY)
	}
	if err != nil {
		return OpsResult{OK: false, Code: "failed", Error: err.Error()}
	}
	// Echo what was actually clicked. An agent driving a GUI needs to be able
	// to log/verify the target it hit, not just that a click was emitted.
	out := map[string]interface{}{"clicked": m}

	// CLOSED LOOP (opt-in): a click has no universal post-condition — it may
	// open a dialog, dismiss one, or toggle something — so the caller states
	// what should be true afterwards and we confirm it. Without `expect` this
	// stays fire-and-forget, which is the honest default rather than inventing
	// a success signal we cannot actually observe.
	if exp := strings.TrimSpace(p.Expect); exp != "" {
		time.Sleep(400 * time.Millisecond) // let the UI settle
		ok, why := verifyGhostElement(eng, ghostElementQuery{Query: exp}, "")
		out["expected"] = exp
		out["verified"] = ok
		if !ok {
			out["verifyError"] = why
			return OpsResult{
				OK:      false,
				Code:    "unverified",
				Error:   "clicked, but " + exp + " did not appear: " + why,
				Initial: out,
			}
		}
	}
	return OpsResult{OK: true, Initial: out}
}

func ghostTypeIntoElementHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		ghostElementQuery
		Text     string `json:"text"`
		Clear    bool   `json:"clear"`
		NoVerify bool   `json:"noVerify"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if strings.TrimSpace(p.Query) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "`query` is required"}
	}
	m, deny := resolveGhostElement(eng, p.ghostElementQuery)
	if deny != nil {
		return *deny
	}
	if err := eng.Input.Click(ghost.ButtonLeft, m.CenterX, m.CenterY); err != nil {
		return OpsResult{OK: false, Code: "failed", Error: "focus click failed: " + err.Error()}
	}
	if p.Clear {
		mod := "ctrl"
		if runtime.GOOS == "darwin" {
			mod = "cmd"
		}
		if err := eng.Input.KeyCombo(mod, "a"); err != nil {
			return OpsResult{OK: false, Code: "failed", Error: "select-all failed: " + err.Error()}
		}
	}
	if err := eng.Input.TypeText(p.Text); err != nil {
		return OpsResult{OK: false, Code: "failed", Error: err.Error()}
	}

	out := map[string]interface{}{"typedInto": m, "chars": len([]rune(p.Text))}
	if p.NoVerify {
		out["verified"] = false
		out["verifySkipped"] = true
		return OpsResult{OK: true, Initial: out}
	}

	// CLOSED LOOP: read the field back. One retry, because a slow app may not
	// have committed the keystrokes when the first read lands — retrying a READ
	// is safe, whereas retrying the TYPE would double the text.
	ok, why := verifyGhostElement(eng, p.ghostElementQuery, p.Text)
	if !ok {
		time.Sleep(400 * time.Millisecond)
		ok, why = verifyGhostElement(eng, p.ghostElementQuery, p.Text)
	}
	out["verified"] = ok
	if !ok {
		out["verifyError"] = why
		// The keystrokes were emitted but the value did not land. Reporting
		// success here is what makes a GUI agent untrustworthy, so this is a
		// failure with a distinct code the caller can branch on.
		return OpsResult{
			OK:      false,
			Code:    "unverified",
			Error:   "typed, but the value did not take: " + why,
			Initial: out,
		}
	}
	return OpsResult{OK: true, Initial: out}
}

func ghostLaunchAppHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if _, deny := ghostEngineForOps(c); deny != nil {
		return *deny
	}
	var p struct {
		App string `json:"app"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	// launchDesktopApp carries its own metacharacter guard and consent check
	// (remote_runtime_desktop.go) — the same code the runtime session path uses,
	// so both entry points enforce identically.
	if err := launchDesktopApp(c.Ctx, p.App); err != nil {
		return OpsResult{OK: false, Code: "failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"launched": p.App}}
}

func ghostFocusAppHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if _, deny := ghostEngineForOps(c); deny != nil {
		return *deny
	}
	var p struct {
		App string `json:"app"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if err := focusDesktopApp(context.Background(), p.App); err != nil {
		return OpsResult{OK: false, Code: "failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"focused": p.App}}
}

// focusDesktopApp raises an application to the foreground.
//
// Shares launchDesktopApp's metacharacter guard: `app` reaches here from an ops
// payload and must never be able to grow into a command. Every branch execs a
// binary directly — no shell — so this is defence in depth.
func focusDesktopApp(ctx context.Context, app string) error {
	app = strings.TrimSpace(app)
	if app == "" {
		return fmt.Errorf("focus: empty application name")
	}
	if strings.ContainsAny(app, ";|&$`\n\r<>\"'") {
		return fmt.Errorf("focus: illegal characters in application name %q", app)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// `open -a` on an already-running app raises it rather than relaunching.
		cmd = exec.CommandContext(ctx, "open", "-a", app)
	case "linux":
		if _, err := exec.LookPath("wmctrl"); err != nil {
			return fmt.Errorf("focus: wmctrl not installed (apt install wmctrl) — required to raise a window on X11")
		}
		cmd = exec.CommandContext(ctx, "wmctrl", "-a", app)
	case "windows":
		// AppActivate matches on window title; -Name filters the process list
		// first so a bare app name works the way it does on the other two OSes.
		ps := fmt.Sprintf(
			`$p = Get-Process -Name %q -ErrorAction SilentlyContinue | Select-Object -First 1; `+
				`if (-not $p) { Write-Error "no running process named %s"; exit 1 }; `+
				`(New-Object -ComObject WScript.Shell).AppActivate($p.MainWindowTitle) | Out-Null`,
			app, app)
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	default:
		return fmt.Errorf("focus: unsupported on %s", runtime.GOOS)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("focus %q failed: %s", app, msg)
	}
	return nil
}
