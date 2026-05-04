package main

// feedback_to_vibe.go — auto-route feedback-source tasks through the
// /vibing/execute pipeline and fire a Hermes-bundle reload after they
// commit.
//
// Without this glue, a `POST /tasks {source: "feedback-console"}` from
// the mobile FeedbackOverlay (after a shake-while-guest-is-loaded) was
// just a one-shot prompt. Claude got the typed message but no project
// context, no auto-reload-on-commit, and no bundle rebuild — the user
// would shake, type "fix this bug", see "done", and the loaded sfmg
// guest bundle would still show the broken version because nothing
// asked the SDK to swap to a fresh bundle.
//
// Two hooks:
//
//  1. `vibingifyFeedbackTaskBody` mutates an inbound /tasks request body
//     in place when its source is feedback-flavored: prepends the same
//     vibing execution context the /vibing/execute handler injects,
//     resolves the project from projectName/bundleId/last-loaded-guest,
//     and picks a ready runner. The handler then proceeds with the
//     normal CreateTaskWithOptions flow.
//
//  2. `autoReloadAfterFeedbackVibingTask` fires from the OnTaskDone
//     callback when a vibing-or-feedback task completes successfully.
//     It triggers a fresh /dev/build-native, then broadcasts
//     `reload_bundle` to BlackBox sessions so the loaded guest bundle
//     swaps to the new HBC without manual user action.

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// feedbackSourceVariants contains the source values that should be
// auto-routed through the vibing pipeline. Keep in sync with the SDK
// + mobile side — if you add a new feedback origin, add it here.
var feedbackSourceVariants = map[string]struct{}{
	"feedback-console":   {}, // mobile FeedbackOverlay typed message
	"feedback-sdk":       {}, // SDK's modal "Hot Reload" / "Fix" buttons
	"native-guest-shake": {}, // shake-inside-Yaver-host that escalated to feedback
	"mobile-feedback":    {}, // YaverFeedbackPane.swift native send (host-mode bottom sheet inside the Yaver mobile container)
	"vibing":             {}, // /vibing/execute origin, included so OnTaskDone catches it
}

func isFeedbackOrVibingSource(source string) bool {
	_, ok := feedbackSourceVariants[strings.TrimSpace(strings.ToLower(source))]
	return ok
}

// feedbackPromptRequestsReload returns true when the user's feedback
// prompt explicitly asked the runner to push the change to the running
// guest bundle. The keyword set is intentionally tight — a casual
// mention of "reload" inside a code reference shouldn't trigger a
// rebuild. Matched substrings are evaluated against the task title
// and description (lower-cased + trimmed); first hit wins.
//
// The AI agent on the remote can ALSO call /dev/reload directly via
// the MCP runner when its judgement says so — that path is preferred
// because the AI sees the diff. This helper is just the cheap fast
// path so explicit user intent is honoured without a round-trip.
func feedbackPromptRequestsReload(task *Task) bool {
	if task == nil {
		return false
	}
	combined := strings.ToLower(strings.TrimSpace(task.Title + "\n" + task.Description))
	if combined == "" {
		return false
	}
	for _, marker := range []string{
		"reload",
		"hot reload",
		"rebuild",
		"refresh the app",
		"refresh app",
		"push to phone",
		"push it",
		"test it",
		"try it",
		"see it",
		"show me",
		"apply it",
		"update the app",
		"update app",
	} {
		if strings.Contains(combined, marker) {
			return true
		}
	}
	return false
}

// shouldVibingifyFeedbackTask reports whether an incoming /tasks request
// should be reshaped into a vibing-style task before CreateTaskWithOptions
// runs. We only reshape sources that the SDK + mobile send for feedback —
// /vibing/execute already does its own reshaping, so it is excluded.
func shouldVibingifyFeedbackTask(source string) bool {
	s := strings.TrimSpace(strings.ToLower(source))
	return s == "feedback-console" || s == "feedback-sdk" || s == "native-guest-shake" || s == "mobile-feedback"
}

// vibingifyFeedbackTaskBody applies the same project resolution + prompt
// shaping the /vibing/execute handler does, so a /tasks POST with a
// feedback source ends up with: (a) the right workDir, (b) the
// vibing-execution-context prefix on the prompt, (c) a runner that's
// actually ready. Returns true when it modified the body so the caller
// can log/branch on it. Mutates `title`, `projectName`, `runner`, and
// `workDir` in place via the pointer args — same field semantics as
// httpserver.go::createTask uses.
func (s *HTTPServer) vibingifyFeedbackTaskBody(
	r *http.Request,
	source string,
	title *string,
	projectName *string,
	workDir *string,
	runner *string,
	model *string,
	bundleID string,
) bool {
	if !shouldVibingifyFeedbackTask(source) {
		return false
	}
	if title == nil || *title == "" {
		return false
	}

	// Resolve project from explicit fields → bundleId → name → guest workspace.
	resolvedPath, resolvedName := s.resolveVibingProjectForRequest(
		strDeref(workDir),
		strDeref(projectName),
		bundleID,
	)
	if projectName != nil && resolvedName != "" {
		*projectName = resolvedName
	}
	if workDir != nil && resolvedPath != "" {
		*workDir = resolvedPath
	}

	// Inject vibing execution context so Claude/Codex understand they're
	// patching a running guest bundle, not running a generic shell task.
	info := DetectProjectInfo(resolvedPath)
	target := DevServerTarget{}
	if s.devServerMgr != nil {
		target = s.devServerMgr.PreferredTarget()
	}
	if ctx := vibingExecutionContext(resolvedPath, info.Framework, target, isDirectConnection(r)); ctx != "" {
		*title = ctx + "\n\nUser request:\n" + *title
	}

	// Pick a runner that's ready. Source-of-truth order:
	//   1. Inbound `runner` from the /tasks payload (mobile / SDK).
	//   2. Convex `userSettings.primaryRunnerByDevice` for THIS device
	//      (authoritative — the user's pick from DeviceDetailsModal).
	//   3. pickReadyVibingRunner — walks supportedRunnerIDs in order,
	//      claude → codex → opencode.
	//
	// Why we need #2: the mobile pushes the per-device runner into
	// UserDefaults (`yaverPreferredRunner`), and the feedback POST
	// forwards it. But the JS push only fires once activeDevice + the
	// userSettings sub-row is loaded; if the user shakes/sends before
	// that settles, UserDefaults is empty and the POST has runner="".
	// Without #2, the agent fell into pickReadyVibingRunner whose old
	// implementation walked the FULL builtinRunners map (populated
	// from Convex with aider/goose/amp/...) in randomized order, so
	// feedback tasks ended up running on aider — visible to the user
	// as "OpenRouter sign-in" while their picked runner (codex) sat
	// idle. We now consult Convex directly so the agent agrees with
	// what the user picked, even when the mobile hint is missing.
	convexRunner, convexModel := resolvePrimaryRunnerForSelf(r.Context(), s)
	if model != nil && strings.TrimSpace(*model) == "" && convexModel != "" {
		// Model is forwarded to the runner via task.Model →
		// effectiveModel splice in tasks.go (`--model X`). Without
		// this fallback, an empty mobile model on the POST means
		// runner default (codex falls back to o3-mini, which fails
		// on ChatGPT-account auth) — instead pull from Convex's
		// per-device pin so codex runs with the user's picked
		// model (e.g. gpt-5.4) even when the mobile UserDefault
		// hint is empty.
		*model = convexModel
		log.Printf("[feedback→vibe] picked model %q from Convex primaryRunnerByDevice", convexModel)
	}
	// Defense-in-depth: if the inbound runner+model combo is
	// incompatible (stale model from a previous runner pick that the
	// mobile didn't reset), drop the model so the agent falls
	// through to the runner's default. The mobile DEFAULT_MODEL_BY_RUNNER
	// fix in DeviceContext should auto-correct this on switch, but a
	// stale Convex row could still ship a sonnet-on-codex combo —
	// let the agent be the second line of defense.
	if model != nil && runner != nil {
		current := strings.TrimSpace(*runner)
		curModel := strings.TrimSpace(*model)
		if current != "" && curModel != "" && !runnerModelCompatible(current, curModel) {
			log.Printf("[feedback→vibe] dropping incompatible model %q for runner %q (will use runner default)", curModel, current)
			*model = ""
		}
	}

	if runner != nil {
		current := strings.TrimSpace(*runner)
		needsRepick := current == ""
		if !needsRepick {
			if err := CheckRunnerReady(GetRunnerConfig(current), strDeref(workDir)); err != nil {
				log.Printf("[feedback→vibe] inbound runner %q not ready (%v) — re-picking", current, err)
				needsRepick = true
			}
		}
		if needsRepick && convexRunner != "" {
			if IsSupportedRunner(convexRunner) {
				if err := CheckRunnerReady(GetRunnerConfig(convexRunner), strDeref(workDir)); err == nil {
					*runner = convexRunner
					needsRepick = false
					log.Printf("[feedback→vibe] picked %q from Convex primaryRunnerByDevice", convexRunner)
				} else {
					log.Printf("[feedback→vibe] Convex primary %q not ready (%v) — falling through", convexRunner, err)
				}
			}
		}
		if needsRepick {
			if picked := pickReadyVibingRunner(s); picked != "" {
				*runner = picked
			}
		}
	}

	log.Printf("[feedback→vibe] reshaped %s task → project=%q runner=%q", source, resolvedName, strDeref(runner))
	return true
}

// autoReloadAfterFeedbackVibingTask fires from OnTaskDone when a vibing-
// or feedback-derived task completes successfully. Triggers a fresh
// /dev/build-native compile, then broadcasts reload_bundle so the loaded
// guest bundle on the phone swaps to the new HBC. No-op when no preview
// target is registered (i.e. nothing is loaded inside the Yaver host on
// any paired phone).
//
// Gated by intent detection: a successful feedback task only triggers
// auto-reload when the user's prompt explicitly requested it. If the
// user only said "make the background green" we want them to be able
// to review the diff before the bundle reloads on their device — the
// running guest still works against the old bytecode and the user can
// pull the change manually when ready. If the prompt explicitly says
// "and reload" / "test it" / similar, the AI's response will land on
// the new commit and we kick the reload as a courtesy. The AI on the
// remote can also call /dev/reload itself via the MCP runner, which
// is the authoritative path when the AI judges a reload is required.
func (s *HTTPServer) autoReloadAfterFeedbackVibingTask(task *Task) {
	if task == nil || task.Status != TaskStatusFinished {
		return
	}
	if !isFeedbackOrVibingSource(task.Source) {
		return
	}
	if s.blackboxMgr == nil || s.devServerMgr == nil {
		return
	}
	// No paired phone listening on BlackBox? Nothing to reload — bail
	// quietly. (CLI-driven `yaver vibing` runs with no phone in the
	// loop and would otherwise log noise on every commit.)
	if len(s.blackboxMgr.ListSessions()) == 0 {
		return
	}
	// Intent gate: skip auto-reload unless the user asked for it.
	// Without this, every feedback task spawns a build + bundle push
	// — even pure "explore the code" feedback ("show me how X works",
	// "rename this variable") that doesn't need to land on device
	// immediately. Side effect: build queue clogs on rapid back-to-
	// back feedback, and the user loses the ability to review diffs.
	if !feedbackPromptRequestsReload(task) {
		log.Printf("[feedback→vibe] auto-reload skipped for task %s — prompt did not request a reload", task.ID)
		return
	}

	// Trigger a fresh native bundle compile. Reuses the same handler the
	// /dev/reload-app mode=bundle path drives: dependency check, Metro
	// bundle, hermesc compile, atomic file swap, broadcasts incident on
	// failure. We ignore the response body — the broadcast happens in
	// sendPreviewWorkerReloadCommand() below regardless.
	buildBody, _ := json.Marshal(map[string]string{
		"platform":    "ios",
		"projectName": "",
		"projectPath": task.WorkDir,
	})
	buildReq, err := http.NewRequest("POST", "/dev/build-native", bytes.NewReader(buildBody))
	if err != nil {
		log.Printf("[feedback→vibe] auto-reload build request: %v", err)
		return
	}
	buildReq.Header.Set("Content-Type", "application/json")
	rec := newCapturingResponseWriter()
	s.handleBuildNativeBundle(rec, buildReq)
	if rec.Status() >= 400 {
		log.Printf("[feedback→vibe] auto-reload skipped: build-native failed with %d", rec.Status())
		return
	}

	// Build succeeded — push the reload command. Targets the preview
	// worker session if one is registered, falls back to broadcast.
	if !s.sendPreviewWorkerReloadCommand() {
		bundleURL, assetsURL := signedNativeBundleURLs(s)
		s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
			Command: "reload_bundle",
			Data: map[string]interface{}{
				"bundleUrl":  bundleURL,
				"assetsUrl":  assetsURL,
				"moduleName": "main",
				"reason":     "task " + task.ID + " (vibing) completed",
			},
		})
		log.Printf("[feedback→vibe] auto-reload: broadcast reload_bundle for task %s", task.ID)
	} else {
		log.Printf("[feedback→vibe] auto-reload: targeted reload_bundle to preview worker for task %s", task.ID)
	}
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
