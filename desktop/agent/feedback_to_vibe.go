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
	"vibing":             {}, // /vibing/execute origin, included so OnTaskDone catches it
}

func isFeedbackOrVibingSource(source string) bool {
	_, ok := feedbackSourceVariants[strings.TrimSpace(strings.ToLower(source))]
	return ok
}

// shouldVibingifyFeedbackTask reports whether an incoming /tasks request
// should be reshaped into a vibing-style task before CreateTaskWithOptions
// runs. We only reshape sources that the SDK + mobile send for feedback —
// /vibing/execute already does its own reshaping, so it is excluded.
func shouldVibingifyFeedbackTask(source string) bool {
	s := strings.TrimSpace(strings.ToLower(source))
	return s == "feedback-console" || s == "feedback-sdk" || s == "native-guest-shake"
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

	// Pick a runner that's ready. Mirrors handleVibingExecute exactly:
	// the configured primary wins when ready, otherwise the first
	// builtin that passes CheckRunnerReady. Avoids the "task hangs
	// forever because codex isn't auth'd on this box" footgun.
	if runner != nil && strings.TrimSpace(*runner) == "" {
		if picked := pickReadyVibingRunner(s); picked != "" {
			*runner = picked
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
