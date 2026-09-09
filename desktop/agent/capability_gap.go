package main

// capability_gap.go — ONE producer for "this machine cannot do that, and here
// is the tap that fixes it".
//
// THE INCIDENT (2026-07-26, e-mobile on a Hetzner arm64 box):
// the agent said, correctly, `exec flutter: executable file not found in
// $PATH`. The phone said *"Waiting for the dev server to report its
// address…"* — a spinner over a fact the agent had already stated. `POST
// /install/flutter` worked the whole time (install_http.go), including
// flutter_install.go's git-clone path for linux/arm64 where Flutter ships no
// tarball. The remedy string the agent produced even read *"or use Install on
// the preview panel, which streams the download"* — and there was no Install
// button on any preview panel on any surface.
//
// Three structural causes, all of which this file exists to remove:
//
//  1. The one structured refusal in the product (the 412 at
//     devserver_http.go) was gated behind readProjectPackageManifest, i.e.
//     behind package.json. Flutter has pubspec.yaml. Go, Rust, Python, Swift
//     and Kotlin have none of the above. For every non-Node project on earth
//     the structured refusal was unreachable by construction.
//  2. mgr.Start returns BEFORE the process is spawned (devserver.go, "Launch
//     start in background"), so /dev/start answered 200 OK on a start that was
//     already doomed. Every synchronous refusal lane was bypassed.
//  3. The remedy the async path did produce was flattened into prose appended
//     to a string. The agent had the tool name, the endpoint, the stream name
//     and the arch resolution in typed form, and threw all four away to build
//     a sentence.
//
// THE SHAPE. A remedy is a ROUTE, not a sentence: `method + path + stream` so
// a UI can render a button without knowing what the failure is. The agent has
// seven different JSON key names for "the remedy" (Remedy, SuggestedAction,
// HelpHint, Hint, Fix, InstallHint, NextAction) and not one of them carries
// that triple. GapFix is the triple.
//
// THE RULES THIS SERVES (CLAUDE.md, "a missing toolchain is a product
// requirement, not a user error"): state it → offer the fix if the fix exists
// → stream the fix → name the constraint if it does not. And: never advertise
// a remedy the product refuses — every Fix below is validated against the same
// tables `yaver install <name>` consults, in the same order, so a Fix this
// file emits can never 404.
//
// ADDING A GAP is meant to be one row here plus one recipe in the install
// table. Layers B (signal), C (UI) and D (route) then come for free on every
// surface, because they all key off Code and render Fix generically.

import (
	"fmt"
	"regexp"
	"strings"
)

// GapFix is the ROUTE — the thing the seven existing remedy fields all lack.
//
// Stream is the log-stream NAME ("install:flutter"), served at
// GET /streams/<stream>. It is DERIVED from Path by the same helper the 412
// hint uses (installStreamNameForEndpoint), never typed by hand: the previous
// generation of this advice named "/streams/install", a path no install ever
// opens, and a user who followed it watched a 404 and concluded the install
// was hung while it streamed perfectly one path over.
type GapFix struct {
	Label  string `json:"label"`         // "Install Flutter"
	Method string `json:"method"`        // "POST"
	Path   string `json:"path"`          // "/install/flutter"
	Stream string `json:"stream"`        // "install:flutter" → GET /streams/<stream>
	Est    string `json:"est,omitempty"` // "~1.2 GB · usually 3–10 min · 42 GB free on /opt"
	Retry  bool   `json:"retry"`         // re-issue the original request on success

	// Body is the request body the route REQUIRES, already filled in by the
	// producer. A route whose arguments the client has to guess is not
	// invocable: POST /vibing/preview/stop with no body answers 400 "project is
	// required", so a takeover button built from method+path alone would be one
	// more action that cannot succeed — the exact defect this type exists to
	// abolish. Nil for routes that need no body (every /install/<tool>).
	Body map[string]interface{} `json:"body,omitempty"`

	// Instant marks a fix that answers SYNCHRONOUSLY and has nothing to stream:
	// stopping a session, releasing a lock, flipping a setting. The no-stream
	// guard (see the renderers' parseGapFix) exists to stop a 1.2 GB download
	// hiding behind a silent spinner — but applying it to a 20 ms POST would
	// DROP the button instead of rendering it, which is how a correct signal
	// ends up with no consumer. Retry is what makes an instant fix visible: the
	// original operation runs again and the user lands back where they were.
	//
	// Exactly one of Stream / Instant / Confirm must be set. A fix with none of
	// the three is an action the user could start and never see.
	Instant bool `json:"instant,omitempty"`

	// Confirm marks a DESTRUCTIVE fix as two-step. Set only by the reclaim
	// route today (capability_resources.go): the client must call the preview
	// first and show the user exactly what would be deleted, with sizes,
	// before the apply route will do anything. The gate is enforced
	// server-side too — this field tells the UI to render the preview, it does
	// not grant permission.
	//
	// A confirm-gated fix is one of two legal cases of an empty Stream (the
	// other is Instant): it answers synchronously and its preview is what makes
	// it visible, so the "no stream = a 1.2 GB download nobody can watch" rule
	// does not apply.
	Confirm *GapConfirm `json:"confirm,omitempty"`
}

// GapConfirm is the preview half of a destructive route.
type GapConfirm struct {
	Method string `json:"method"` // "GET"
	Path   string `json:"path"`   // "/storage/scan"
	Field  string `json:"field"`  // the JSON key the apply body must set true
	Prompt string `json:"prompt"` // the sentence shown above the preview list
}

// CapabilityGap is a capability the operation needs and this machine does not
// have. Deliberately shaped after CapabilityTargetReadiness
// (capabilities_snapshot.go) — the existing type with the right idea — PLUS
// the missing route.
//
// Exactly one of Fix / Constraint is set. A gap with neither is a dead end
// with a sentence, which is the defect this type exists to make impossible.
type CapabilityGap struct {
	Code       string  `json:"code"`                 // reason_codes.go value — the wire contract
	Capability string  `json:"capability"`           // "flutter", "bun", "xcode-simulator"
	Summary    string  `json:"summary"`              // one sentence, user-facing (layer C)
	Detail     string  `json:"detail,omitempty"`     // what tapping Fix will do
	Fix        *GapFix `json:"fix,omitempty"`        // nil ⇒ no fixer; Constraint MUST be set
	Constraint string  `json:"constraint,omitempty"` // why no fix exists on THIS machine

	// Warning is a named advisory that rides BESIDE a Fix: the operation can
	// start, and here is what may still go wrong. Added 2026-07-27 for the
	// resource lane — "3.1 GB free, the SDK needs 1.2 GB and the first build
	// another 2 GB" is a thing the user must hear BEFORE waiting ten minutes,
	// not a reason to refuse. Warning and Fix are both set; Warning and
	// Constraint never are (a constrained gap has nothing to warn about).
	Warning string `json:"warning,omitempty"`

	// Resource is the headroom measurement behind Warning/Constraint, in bytes
	// AND pre-formatted, so no surface invents its own byte formatter.
	Resource *CapabilityResource `json:"resource,omitempty"`

	// AIFix is the LAST-RESORT route: hand this failure to a coding agent.
	//
	// Offered only when no deterministic fixer exists. "Fix in Yaver" costs an
	// LLM run; POST /install/flutter costs one command, so spending the former
	// on a class that has the latter is the most expensive possible answer to
	// the cheapest possible question. Fix (deterministic) always wins; AIFix is
	// what a Constraint used to be alone with.
	//
	// WHY IT IS PART OF THIS TYPE. "Fix with <runner>" already existed — as a
	// hand-rolled widget in ONE web component, wired to ONE error path
	// (RuntimeLabView's renderFixWithRunnerRow → dispatchBuildFix). So mobile,
	// tvOS and visionOS could not offer it at all, and no other failure class
	// could either. Putting it on the gap makes it available to every surface
	// and every failure through the renderer they already have.
	AIFix *GapFix `json:"aiFix,omitempty"`

	// Reclaim is the space-freeing route offered whenever disk is the blocker
	// or nearly is. A refusal that is only a refusal is a dead end with a
	// sentence — the exact thing CapabilityGap exists to make impossible — so
	// "not enough space" ships with the caches that would fix it.
	Reclaim *GapFix `json:"reclaim,omitempty"`
}

// CapabilityGapContext is everything the detector may look at. Callers fill
// what they know: the 412 preflight knows MissingTools, the async start
// failure knows only Err.
type CapabilityGapContext struct {
	Framework    string
	WorkDir      string
	MissingTools []string
	Err          string
}

var (
	// `exec: "flutter": executable file not found in $PATH` (Go's os/exec)
	rxExecQuotedNotFound = regexp.MustCompile(`exec:?\s+"([^"]+)":\s*executable file not found`)
	// `exec flutter: executable file not found in $PATH` (our own wrapping)
	rxExecBareNotFound = regexp.MustCompile(`exec:?\s+([A-Za-z0-9._+-]+):\s*executable file not found`)
	// `flutter: command not found` (a shell that ran the spawn)
	rxCommandNotFound = regexp.MustCompile(`([A-Za-z0-9._+-]+):\s*command not found`)

	// THE PROSE FAMILY — added 2026-07-27 after measuring how often the three
	// regexes above can actually fire.
	//
	// The three above only ever match RAW os/exec output. But almost every
	// spawn site in this tree calls exec.LookPath FIRST and substitutes its
	// own sentence — `carton not found on PATH — SwiftWasm previews need…`
	// (devserver_swiftwasm.go), `claude not found in PATH or common
	// locations` (CheckRunnerBinary, tasks.go), `adb not on PATH — run
	// \`yaver install remote-runtime\`` (remote_runtime_video_track.go),
	// `tmux not found in PATH`. Every one of those destroys the only text the
	// detector could parse, so the detector was an inventory of one package's
	// error strings rather than of the failure.
	//
	// Anchored on PATH deliberately. A bare `([A-Za-z0-9._+-]+) not found`
	// would capture "Module" out of `Module not found: Can't resolve
	// 'react-dom'` — a bundler failure whose remedy is `npm install`, not an
	// SDK download. A wrong tool name is worse than no tool name: it sends
	// the user to install something they already have, the install
	// "succeeds", and the real failure repeats unchanged.
	rxNotFoundOnPath = regexp.MustCompile(`\b([A-Za-z0-9._+-]+)\s+(?:is\s+)?not\s+(?:found\s+on|found\s+in|on|in)\s+(?:\$|%)?PATH\b`)
)

// notAToolName are words that can legitimately sit in front of "not found in
// PATH" without being a binary. Kept tiny on purpose: this is a guard against
// the handful of English nouns our own prose uses, not a dictionary.
var notAToolName = map[string]bool{
	"exec": true, "executable": true, "binary": true, "command": true,
	"module": true, "file": true, "it": true, "tool": true, "runner": true,
}

// devStartToolchainBinary maps a dev-server framework to the executable its
// dev server must spawn, for frameworks whose readiness is NOT covered by the
// package.json-driven Node preflight.
//
// This table is the whole reason the Flutter case can be refused
// synchronously: detectProjectPreparation can only ever emit
// node/npm/npx/yarn/pnpm/bun/bunx, and it never runs at all without a
// package.json. Adding a row here is how a new non-Node toolchain gets a
// structured refusal, an Install button and a streamed fix on every surface.
//
// Only frameworks whose spawn name is KNOWN belong here. Guessing a binary
// that does not match what Start() execs would refuse a start that would have
// worked — wrong in the other direction, and just as much a defect.
func devStartToolchainBinary(framework string) string {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "flutter":
		// devserver.go FlutterDevServer.Start → resolveSpawnPath("flutter").
		return "flutter"
	case "swiftwasm":
		// devserver_swiftwasm.go Start → startProcess(ctx, "carton", …).
		//
		// carton has no recipe in either install table, so this row produces
		// a gap with a CONSTRAINT rather than a button — which is the point.
		// Before it existed, a SwiftWasm start on a box without the SwiftWasm
		// toolchain answered 200 OK, then failed asynchronously with prose
		// ("carton not found on PATH — …") that matched no detector, so the
		// preview sat on "Waiting for the dev server to report its address…"
		// over a constraint the agent had already stated. Naming an
		// impossible thing is a route too: it ends the wait.
		return "carton"
	}
	return ""
}

// capabilityDisplayName is how a tool is named to a human. Falls back to the
// raw tool name, which is right for lowercase CLI names (bun, pnpm, adb).
// Display names now come from capabilityToolMatrix (capability_platform.go) —
// ONE row per tool declaring its name, its platform limits and its cost. The
// hand-maintained switch this replaced was the second table, and a second table
// is how a tool ends up platform-aware in one place and not the other.
func capabilityDisplayName(tool string) string {
	key := strings.ToLower(strings.TrimSpace(tool))
	switch key {
	case "claude", "codex", "opencode", "glm":
		// ONE source of truth for how a runner is named to a human. A second
		// spelling here would put "claude isn't installed" on the Tasks card
		// next to "Claude Code" everywhere else in the same app.
		return runnerCapabilityName(tool)
	case "java":
		return "Java"
	}
	if spec, ok := capabilityToolSpecFor(key); ok && strings.TrimSpace(spec.Display) != "" {
		return spec.Display
	}
	return strings.TrimSpace(tool)
}

// installEstimateForTool is the honest size/time a user is committing to.
// CLAUDE.md: "a 2 GB SDK behind a silent spinner is the same defect as a
// silent serve — the user cannot tell fetching from hung." Empty when we have
// no defensible number; a made-up estimate is worse than none.
func installEstimateForTool(tool string) string {
	if spec, ok := capabilityToolSpecFor(tool); ok {
		return spec.Est
	}
	return ""
}

// installToolFromEndpoint recovers the tool name from an /install/<tool> path.
func installToolFromEndpoint(endpoint string) string {
	return strings.Trim(strings.TrimPrefix(strings.TrimSpace(endpoint), "/install/"), "/")
}

// missingToolFromError pulls the executable name out of a spawn failure.
// Returns "" when the text is not a missing-executable failure — never a
// guess, because a wrong tool name produces a Fix that installs the wrong
// thing and still leaves the user stuck.
func missingToolFromError(errText string) string {
	text := strings.TrimSpace(errText)
	if text == "" {
		return ""
	}
	for _, rx := range []*regexp.Regexp{rxExecQuotedNotFound, rxExecBareNotFound, rxCommandNotFound, rxNotFoundOnPath} {
		if m := rx.FindStringSubmatch(text); len(m) == 2 {
			tool := strings.TrimSpace(m[1])
			// os/exec sometimes hands back an absolute path; the install
			// tables are keyed by base name.
			if i := strings.LastIndexAny(tool, `/\`); i >= 0 {
				tool = tool[i+1:]
			}
			if tool != "" && !notAToolName[strings.ToLower(tool)] {
				return tool
			}
		}
	}
	return ""
}

// looksLikeMissingExecutable reports whether the text is a spawn failure of
// the "the binary is not there" family, even when the binary name could not
// be extracted.
//
// The last three needles are the PROSE family (see rxNotFoundOnPath): sites
// that call exec.LookPath themselves and write their own sentence. Kept
// PATH-anchored for the same reason the regex is — `not found` on its own
// matches a bundler's "Module not found", whose remedy is not an install.
func looksLikeMissingExecutable(errText string) bool {
	lower := strings.ToLower(errText)
	return strings.Contains(lower, "executable file not found") ||
		strings.Contains(lower, "command not found") ||
		strings.Contains(lower, "not found in path") ||
		strings.Contains(lower, "not found on path") ||
		strings.Contains(lower, "not on path")
}

// DetectTaskCapabilityGap is the TASKS-lane producer — the same object the
// preview lane carries, on the surface where a user types a prompt.
//
// Before this existed, POST /tasks answered 500 with
// `failed to create task: runner not ready: claude not found in PATH or
// common locations` while `POST /install/claude` worked and streamed. The
// phone cannot type `yaver install claude`; the 500 was a dead end with a
// sentence, on the busiest lane in the product.
//
// runnerCommand is the binary the task was about to spawn. It is used ONLY as
// the fallback when the text proves a binary was missing without naming it —
// the task knows which one, so that is a resolution, not a guess. Returns nil
// for every other failure shape (not signed in, incompatible model, workDir
// not writable, permission denied): those are real failures with real, and
// DIFFERENT, remedies, and offering an install for them teaches the user that
// Yaver's buttons do not work.
func DetectTaskCapabilityGap(runnerCommand, errText string) *CapabilityGap {
	if gap := DetectCapabilityGap(CapabilityGapContext{Err: errText}); gap != nil {
		return gap
	}
	cmd := strings.TrimSpace(runnerCommand)
	if cmd == "" || !looksLikeMissingExecutable(errText) {
		return nil
	}
	if i := strings.LastIndexAny(cmd, `/\`); i >= 0 {
		cmd = cmd[i+1:]
	}
	if cmd == "" {
		return nil
	}
	return capabilityGapForMissingTools([]string{cmd})
}

// DetectCapabilityGap is THE producer. Every carrier (the /dev/start 412, the
// /dev/events SSE error frame, /dev/status) gets its gap from here, so a new
// capability is taught to one function and appears on all of them.
//
// Returns nil when the failure is not a capability gap — a compile error, a
// port clash and a missing pubspec asset are all real failures with their own
// (different) remedies, and claiming a toolchain gap for them would send the
// user to install something they already have.
func DetectCapabilityGap(ctx CapabilityGapContext) *CapabilityGap {
	tools := make([]string, 0, len(ctx.MissingTools))
	for _, t := range ctx.MissingTools {
		if t = strings.TrimSpace(t); t != "" {
			tools = append(tools, t)
		}
	}
	if len(tools) == 0 {
		if t := missingToolFromError(ctx.Err); t != "" {
			tools = []string{t}
		}
	}
	if len(tools) == 0 && looksLikeMissingExecutable(ctx.Err) {
		// The text says a binary was missing but did not name it. The
		// framework's own table does — that is a resolution, not a guess.
		if t := devStartToolchainBinary(ctx.Framework); t != "" {
			tools = []string{t}
		}
	}
	if len(tools) == 0 {
		return nil
	}
	return capabilityGapForMissingTools(tools)
}

// capabilityGapForMissingTools builds the gap for a known-missing tool set.
//
// Resolution order matches `yaver install <name>` exactly (installableViaAgent
// → integrations → meta plans), which is what makes "never advertise a remedy
// the product refuses" a property rather than a hope.
func capabilityGapForMissingTools(tools []string) *CapabilityGap {
	primary := tools[0]
	gap := &CapabilityGap{
		Code:       ReasonCapabilityToolchainMissing,
		Capability: primary,
		Summary:    capabilityGapSummary(tools),
	}

	// PLATFORM FIRST — before we ever ask whether an install recipe exists.
	// A recipe that cannot work on THIS GOOS/GOARCH is not a fix, and the
	// registry does not know that: it answers "is there a recipe", which is the
	// inventory, not the operation. Refusing here is what keeps "Install
	// Flutter" off a Windows box whose install would report success and leave
	// nothing on PATH, and — just as importantly — what keeps it ON a
	// linux/arm64 box, where the git-clone path works and the naive "no
	// tarball ⇒ impossible" reading would have withheld it.
	for _, t := range tools {
		if ok, constraint := capabilityFixSupportedHere(t); !ok {
			gap.Constraint = constraint
			gap.Detail = constraint
			return gap
		}
	}

	endpoint := installEndpointForTool(tools)
	if !canInstallMissingTool(tools) || endpoint == "" {
		// WebDriverAgent is a named Apple-only control dependency, not an
		// arbitrary executable. The generic "install it yourself" dead end hid
		// the two Yaver routes that actually work when the current host cannot
		// provide it: select a Mac as primary, or use the WebRTC native-preview
		// lane. Keep this before the generic constraint so every surface gets a
		// concrete route instead of an unactionable acronym.
		if len(tools) == 1 && strings.EqualFold(primary, "wda") {
			gap.Constraint = "WebDriverAgent is unavailable on this machine. Point Yaver at a Mac with Xcode using `yaver primary set <device>`, or use the WebRTC native-preview lane instead."
			gap.Detail = gap.Constraint
			return gap
		}
		// No fix on THIS machine — say which one specifically, and say what
		// the user can do instead. A gap with no Fix and no Constraint is a
		// dead end with a sentence.
		gap.Constraint = fmt.Sprintf(
			"Yaver has no install recipe for %s on this machine, so there is nothing to tap here. "+
				"Install it on the box yourself (GET /install/list shows everything the agent can "+
				"provision), then start the preview again.",
			strings.Join(tools, ", "))
		gap.Detail = gap.Constraint
		return gap
	}

	streamName := installStreamNameForEndpoint(endpoint)
	if streamName == "" {
		// The endpoint resolved but its stream name did not — refuse to
		// advertise a button whose progress the user could not watch.
		gap.Constraint = fmt.Sprintf(
			"Yaver resolved an installer for %s but could not name its progress stream, so the "+
				"install would run invisibly. Run `yaver install %s` on the box instead.",
			strings.Join(tools, ", "), primary)
		gap.Detail = gap.Constraint
		return gap
	}

	endpointTool := installToolFromEndpoint(endpoint)

	// RESOURCES SECOND — measure the volume the install will actually write
	// to, not "/" and not the agent's CWD. Three outcomes, and only one of them
	// is a refusal (see capability_resources.go's header for why fits/doesn't
	// is the wrong shape).
	verdict := evaluateCapabilityResources(endpointTool, probeHeadroomFn(capabilityInstallRoot(endpointTool)))
	gap.Resource = verdict.Resource
	if verdict.Level == capabilityResourceInsufficient {
		gap.Code = ReasonCapabilityInsufficientDisk
		gap.Constraint = verdict.Refusal
		gap.Detail = verdict.Refusal
		gap.Reclaim = capabilityReclaimFix(verdict.Resource)
		return gap
	}

	gap.Fix = &GapFix{
		Label:  "Install " + capabilityDisplayName(primary),
		Method: "POST",
		Path:   endpoint,
		Stream: streamName,
		Est:    joinEstimate(installEstimateForTool(endpointTool), verdict.EstSuffix),
		Retry:  true,
	}
	gap.Detail = fmt.Sprintf(
		"Yaver can install it here, no sudo needed. The download streams into this panel, and "+
			"the preview starts by itself when it finishes. Same thing from a terminal: "+
			"`yaver install %s`.", endpointTool)

	// A user who already pressed Install once and is reading "isn't installed"
	// a second time deserves to know WHY. A partial tree from a killed install
	// is the commonest reason, and silence about it is how someone concludes
	// the button does nothing (capability_partial.go).
	if partial := partialInstallSummary(endpointTool, detectPartialInstall(endpointTool, nil)); partial != "" {
		gap.Detail = partial + " " + gap.Detail
	}

	if verdict.Level == capabilityResourceTight {
		// Warning is NOT refusal: the button stays, the wait is narrated. A
		// user told "you may run out mid-build" before a ten-minute download
		// can decide; a user told nothing finds out at minute nine.
		gap.Warning = verdict.Warning
		gap.Reclaim = capabilityReclaimFix(verdict.Resource)
	}
	return gap
}

// joinEstimate puts the headroom on the button next to the size. Both halves
// are optional: an unmeasured volume contributes nothing rather than "unknown".
func joinEstimate(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

// capabilityGapSummary is the sentence the user reads. Deliberately about the
// MACHINE, not about Yaver: "Flutter isn't installed on this machine" is a
// fact the user can act on; "exec flutter: executable file not found in $PATH"
// is a fact only a developer can.
func capabilityGapSummary(tools []string) string {
	if len(tools) == 1 {
		return fmt.Sprintf("%s isn't installed on this machine.", capabilityDisplayName(tools[0]))
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, capabilityDisplayName(t))
	}
	return fmt.Sprintf("%s aren't installed on this machine.", strings.Join(names, ", "))
}

// aiFixRoute builds the "hand it to a coding agent" route for a failure that
// has no deterministic fixer.
//
// The body is pre-filled — same rule as every other route here: a route whose
// arguments the caller must invent is not invocable, and POST /tasks refuses a
// body with no prompt (ReasonTaskPromptMissing), so a half-built AI route would
// 400 on every surface that pressed it.
//
// Instant, because the POST answers immediately with the created task; the WORK
// is then visible wherever that surface already shows tasks. It is deliberately
// NOT Retry: re-issuing the original operation the moment the task is created
// would race the runner that has not started yet.
func aiFixRoute(runner, title, prompt string) *GapFix {
	runner = strings.TrimSpace(runner)
	if runner == "" || strings.TrimSpace(prompt) == "" || strings.TrimSpace(title) == "" {
		// Never advertise a route we cannot fill in. A "Fix with" button with no
		// runner is the dead button this whole file exists to abolish.
		return nil
	}
	return &GapFix{
		Label:  "Fix with " + runnerCapabilityName(runner),
		Method: "POST",
		Path:   "/tasks",
		Body: map[string]interface{}{
			"title":       title,
			"description": prompt,
			"runner":      runner,
		},
		Instant: true,
	}
}

// compileFailureGap turns "your app failed to compile" into a NAMED cause with
// the only route that can actually help: a coding agent.
//
// WHY THIS ONE GETS AIFix AND MOST GAPS DO NOT. Yaver has a deterministic fixer
// for a missing toolchain (install it), for a full disk (reclaim), for a held
// preview lock (stop it). It has none for `The class 'IconData' can't be
// extended outside of its library` — that is the USER'S SOURCE, and no command
// the agent can run repairs it. This is precisely the case the escalation rule
// reserves an LLM run for.
//
// Before this, a compile failure emitted a `type:"error"` dev event carrying a
// detail string and NOTHING else — no code to switch on, no route to press. A
// Flutter preview sat blank while /dev/status reported perfect health
// (2026-07-25), and even after that was fixed the surface could only render the
// sentence. The user then had to retype the error into the chat themselves,
// which is the manual step this route removes.
//
// Degrades honestly: with no runner installed, aiFixRoute returns nil and the
// gap is a named Constraint — still far better than an unlabelled error, and it
// never advertises a button that cannot work.
func compileFailureGap(framework, detail string, installedRunners []string) *CapabilityGap {
	summary := "Your app failed to compile, so there is nothing to preview."
	if f := strings.TrimSpace(framework); f != "" {
		summary = fmt.Sprintf("The %s app failed to compile, so there is nothing to preview.", f)
	}
	gap := &CapabilityGap{
		Code:       ReasonBuildCompileFailed,
		Capability: "project-compiles",
		Summary:    summary,
		Detail:     strings.TrimSpace(detail),
		// The dev server is FINE — saying so stops the user restarting a healthy
		// process, which is the first thing anyone tries when a preview is blank.
		Constraint: "The dev server is running; the project's own source does not compile. " +
			"Yaver has no command that fixes source code, so this is not something the agent can repair for you.",
	}
	// Preserve the user's installed/configured runner order. The hosted
	// DeepSeek lane is a fallback, never a reason to bypass a usable primary
	// runner merely because it is available.
	for _, r := range appendRemotelessFallback(installedRunners) {
		if fix := aiFixRoute(r, "Fix the compile error", compileFixPrompt(framework, detail)); fix != nil {
			gap.AIFix = fix
			break
		}
	}
	return gap
}

// remotelessAIAvailable reports whether the hosted-model lane can run on
// this box right now (interim backend: opencode binary + DeepSeek key; see
// docs/architecture/REMOTELESS_AI.md). The in-process loop later removes the
// binary requirement without changing this contract.
func remotelessAIAvailable() bool {
	st := detectRemotelessStatus("")
	return st.Ready && st.AuthConfigured
}

// appendRemotelessFallback preserves the caller's probed runner preference and
// appends the hosted lane only when usable. Remoteless is fallback capacity;
// availability never grants it precedence over a configured device runner.
func appendRemotelessFallback(installed []string) []string {
	return appendRemotelessFallbackList(remotelessAIAvailable(), installed)
}

// appendRemotelessFallbackList is the pure decision: keep the existing order,
// dedup remoteless, then append it when usable. Kept pure
// so the precedence is hermetically testable without a binary on PATH.
func appendRemotelessFallbackList(remotelessUsable bool, installed []string) []string {
	out := make([]string, 0, len(installed)+1)
	for _, r := range installed {
		if r != "remoteless" {
			out = append(out, r)
		}
	}
	if remotelessUsable {
		out = append(out, "remoteless")
	}
	return out
}

// compileFixPrompt is what the coding agent is actually asked. It carries the
// compiler's own words: a prompt that says "fix the build" without them makes
// the runner re-derive what the agent already knew.
func compileFixPrompt(framework, detail string) string {
	body := strings.TrimSpace(detail)
	if body == "" {
		return ""
	}
	lead := "The project fails to compile. Fix the cause and change nothing else."
	if f := strings.TrimSpace(framework); f != "" {
		lead = fmt.Sprintf("The %s project fails to compile. Fix the cause and change nothing else.", f)
	}
	return lead + "\n\nThe compiler said:\n" + body
}
