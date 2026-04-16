package main

// autodev_reports_http.go — HTTP surface for the autodev per-run
// JSON reports produced by autodev_cmd.go. All endpoints are
// authenticated via the existing auth() middleware and served
// over the agent's normal P2P/relay path, so mobile, desktop
// Electron, and the web dashboard all read the same data without
// any Convex roundtrip.
//
// GET  /autodev/reports             — list every saved report (summary)
// GET  /autodev/reports?name=X      — one report in full (kicks + deploy)
// POST /autodev/reports/revert      — body {name, commit_shas:[...]}
//                                     runs `git revert --no-edit` for each
//                                     SHA in the loop's work dir, then push.
//
// Handler registration lives in httpserver.go (mux.HandleFunc),
// same pattern as /auth/pair/*.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *HTTPServer) handleAutodevReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	name := r.URL.Query().Get("name")
	if name != "" {
		rep, err := LoadAutodevReport(name)
		if err != nil {
			jsonError(w, http.StatusNotFound, "no report for "+name)
			return
		}
		jsonReply(w, http.StatusOK, rep)
		return
	}
	reports, err := ListAutodevReports()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"reports": reports,
	})
}

func (s *HTTPServer) handleAutodevRevert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Name       string   `json:"name"`
		CommitSHAs []string `json:"commit_shas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name == "" || len(body.CommitSHAs) == 0 {
		jsonError(w, http.StatusBadRequest, "name and commit_shas required")
		return
	}
	// Reject anything that doesn't look like a git SHA to avoid
	// passing shell metacharacters or unrelated refs to git revert.
	for _, sha := range body.CommitSHAs {
		if !isProbableGitSHA(sha) {
			jsonError(w, http.StatusBadRequest, "not a git SHA: "+sha)
			return
		}
	}
	if err := RevertAutodevCommits(body.Name, body.CommitSHAs); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"reverted": body.CommitSHAs,
	})
}

func isProbableGitSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	s = strings.ToLower(s)
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

// handleAutodevStart is the entry point used by mobile, desktop,
// web, and MCP clients to kick off an autodev run without shelling
// to the CLI. Body shape:
//
//	{
//	  "project":          "sfmg",                // loop name suffix
//	  "work_dir":         "/Users/me/Workspace/sfmg", // repo to run in
//	  "hours":            "8",                   // int hours or "infinite"
//	  "load":             "lite",                // lite | high
//	  "prompt":           "focus on purchase flow",
//	  "deploy":           "testflight",          // auto | testflight | playstore | both | none
//	  "runner":           "claude-code",
//	  "branch":           "main",
//	  "remained_content": "- [ ] item 1\n- [ ] item 2\n", // optional
//	  "remained_path":    "remained.md",                 // optional, default remained.md
//	  "no_autotest":      false
//	}
//
// On success returns {loop_name, work_dir}; the run is spawned in
// a goroutine so the HTTP response is immediate. Progress surfaces
// via /autodev/loops and /autodev/reports — watchable from mobile
// / web / another machine just like any other loop.
func (s *HTTPServer) handleAutodevStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Project         string `json:"project"`
		WorkDir         string `json:"work_dir"`
		Hours           string `json:"hours"`
		Load            string `json:"load"`
		Prompt          string `json:"prompt"`
		Deploy          string `json:"deploy"`
		Runner          string `json:"runner"`
		Branch          string `json:"branch"`
		Target          string `json:"target"`
		RemainedPath    string `json:"remained_path"`
		RemainedContent string `json:"remained_content"`
		NoAutotest      bool   `json:"no_autotest"`
		MaxIterations   int    `json:"max_iterations"`
		Engine          string `json:"engine"`      // "" | "claude" | "hybrid"
		AutoIdeas       *int   `json:"auto_ideas"`  // nil = default (999); 0 disables; N caps refills
		AutoBranch      bool   `json:"auto_branch"` // true = work on autodev/<project>-autodev-<YYYYMMDD>
		Harden          string `json:"harden"`      // ""|security|memory|perf|quality|all
		Model           string `json:"model"`       // sonnet|opus|haiku|<full-id>
		Planner         string `json:"planner"`     // hybrid layering: agent[:model]
		Implementer    string `json:"implementer"`  // hybrid layering: agent[:model]
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.WorkDir == "" {
		jsonError(w, http.StatusBadRequest, "work_dir required")
		return
	}
	if _, err := os.Stat(body.WorkDir); err != nil {
		jsonError(w, http.StatusBadRequest, "work_dir does not exist: "+body.WorkDir)
		return
	}
	// If a remained_content was supplied, write it to the target
	// path inside the workdir BEFORE spawning the run — that way
	// the loop starts with the checklist already in place and the
	// runner sees it on the first kick.
	remainedPath := body.RemainedPath
	if body.RemainedContent != "" && remainedPath == "" {
		remainedPath = "remained.md"
	}
	if body.RemainedContent != "" {
		full := remainedPath
		if !filepath.IsAbs(full) {
			full = filepath.Join(body.WorkDir, full)
		}
		if err := os.WriteFile(full, []byte(body.RemainedContent), 0o644); err != nil {
			jsonError(w, http.StatusInternalServerError, "write remained file: "+err.Error())
			return
		}
	}

	project := body.Project
	if project == "" {
		project = filepath.Base(body.WorkDir)
	}

	// Engine resolution: "hybrid" overrides Runner so phaseThink picks
	// the planner+implementer adapter. "" / "claude" / "claude-code"
	// is a no-op and leaves Runner alone.
	runnerOverride := body.Runner
	switch strings.ToLower(strings.TrimSpace(body.Engine)) {
	case "", "claude", "claude-code":
		// keep runner as supplied (or empty -> default)
	case "codex":
		runnerOverride = "codex"
	case "hybrid":
		runnerOverride = "hybrid"
	default:
		jsonError(w, http.StatusBadRequest, "unknown engine: "+body.Engine+" (want claude|codex|hybrid)")
		return
	}
	// Hybrid layering: planner / implementer per-tier override.
	// Either set forces engine=hybrid; spawnHybrid reads the env
	// vars below. Default (both empty) leaves the user's chosen
	// engine alone — no slicing unless asked for.
	if body.Planner != "" || body.Implementer != "" {
		runnerOverride = "hybrid"
		if body.Planner != "" {
			os.Setenv("YAVER_HYBRID_PLANNER", body.Planner)
		}
		if body.Implementer != "" {
			os.Setenv("YAVER_HYBRID_IMPLEMENTER", body.Implementer)
		}
	}
	if body.Model != "" {
		os.Setenv("YAVER_CLAUDE_MODEL", body.Model)
	}
	autoIdeas := 999
	if body.AutoIdeas != nil {
		autoIdeas = *body.AutoIdeas
		if autoIdeas < 0 {
			autoIdeas = 0
		}
	}

	resolvedPrompt := body.Prompt
	if hp := autodevHardenPrompt(body.Harden); hp != "" {
		if strings.TrimSpace(resolvedPrompt) == "" {
			resolvedPrompt = hp
		} else {
			resolvedPrompt = hp + "\n\n" + resolvedPrompt
		}
	}

	resolvedBranch := body.Branch
	if body.AutoBranch && resolvedBranch == "" {
		resolvedBranch = "autodev/" + project + "-autodev-" + time.Now().Format("20060102")
		ensureAutodevBranch(body.WorkDir, resolvedBranch)
	}

	d := autodevDefaults{
		hours:      body.Hours,
		load:       body.Load,
		deploy:     body.Deploy,
		prompt:     resolvedPrompt,
		project:    project,
		runner:     runnerOverride,
		branch:     resolvedBranch,
		target:     body.Target,
		maxIter:    body.MaxIterations,
		noAutotest: body.NoAutotest,
		remained:   remainedPath,
		autoIdeas:  autoIdeas,
	}
	if d.hours == "" {
		d.hours = autodevSleepHours
	}
	if d.load == "" {
		d.load = autodevSleepLoad
	}
	d = applyAutodevDefaults(d, "autodev", body.WorkDir)
	plan := buildAutodevPlan("autodev", d, body.WorkDir)

	// Scaffold under the repo's workdir so the specs land where
	// the CLI would have put them.
	origWd, _ := os.Getwd()
	if err := os.Chdir(body.WorkDir); err != nil {
		jsonError(w, http.StatusInternalServerError, "chdir: "+err.Error())
		return
	}
	if err := ensureAutodevSpec(plan); err != nil {
		_ = os.Chdir(origWd)
		jsonError(w, http.StatusInternalServerError, "scaffold spec: "+err.Error())
		return
	}
	if plan.IncludeAutotest {
		if err := ensureAutodevRegressionSpec(plan); err != nil {
			_ = os.Chdir(origWd)
			jsonError(w, http.StatusInternalServerError, "scaffold regression: "+err.Error())
			return
		}
	}
	if d.prompt != "" {
		loopPrompt([]string{"set", plan.LoopName, d.prompt})
	}
	// Spawn the run in a goroutine so the HTTP response is fast.
	// The goroutine locks its own cwd via syscall in runAutodevLoop
	// indirectly — loopRun already cds into the loop's workdir.
	go func(workDir string, p autodevPlan) {
		// The goroutine inherits cwd from the parent; lock it
		// to the repo for the duration so git calls land there.
		_ = os.Chdir(workDir)
		runAutodevLoop(p)
		runAutodevDeploy(p)
	}(body.WorkDir, plan)
	_ = os.Chdir(origWd)

	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":        true,
		"loop_name": plan.LoopName,
		"work_dir":  body.WorkDir,
		"hours":     plan.Hours,
		"deploy":    plan.Deploy,
		"remained":  plan.RemainedFile,
	})
}
