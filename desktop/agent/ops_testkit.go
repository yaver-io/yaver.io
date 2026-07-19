package main

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/testkit"
)

// ops_testkit.go — exposes the embedded yaver-test-sdk (chromedp web driver) as
// project-aware OPS verbs so the mobile app + web UI can run a project's
// yaver-tests/ suite on ANY machine (this box, or a remote PC via ops machine
// routing) and get a feature-based, video-highlight report back.
//
// Web specs run in-process via chromedp wherever THIS agent runs, so "run on
// magara" = dispatch the verb to magara's agent (OpsRequest.Machine = deviceId);
// no SSHRunner needed for web. Mobile (redroid) specs stay on qa_run, which
// already carries the ssh-host surface seam.
//
// Verbs:
//   playwright_status  — check Node/Playwright/Chromium readiness on this box
//   playwright_run     — run web specs via Playwright on this box
//   playwright_profiles/profile_delete/artifact — profile and artifact helpers
//   project_test_specs  — list the Features (specs) discovered for a project
//   project_test_run    — run the suite (async job), records video, returns jobId
//   project_test_report — feature-based report + highlight clips for a finished run
//   project_test_grow   — self-grow: plan + ledger of uncovered Features for the
//                          runner to author (the "tests write themselves" loop)

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("playwright_status",
		"Inspect Playwright readiness on this machine: node version, playwright package resolution, Chromium executable, and suggested install commands. Target a remote machine with ops.machine.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Dir string `json:"dir"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			status := playwrightReadiness(req.Dir)
			ok, _ := status["ok"].(bool)
			res := OpsResult{OK: ok, Initial: status}
			if !ok {
				res.Code = "deps_missing"
				res.Error = "Playwright is not ready on this machine"
			}
			return res
		})

	reg("playwright_repair",
		"Install/repair Playwright dependencies on this machine using the existing testkit dependency installer. Long-running — returns a jobId; poll studio_job_status, then call playwright_status. Payload: {include? default [node,playwright,ffmpeg]}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Include []string `json:"include"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			include := req.Include
			if len(include) == 0 {
				include = []string{"node", "playwright", "ffmpeg"}
			}
			job, err := studioJobs.startDepsInstall(include, "")
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			out := job.snapshot()
			out["playwrightRepair"] = true
			out["include"] = include
			return OpsResult{OK: true, Initial: out}
		})

	reg("playwright_run",
		"Run a project's web specs through Playwright on this machine. This is the Talos/Yaver remote-browser path: set ops.machine to the Hetzner device/alias. Payload mirrors project_test_run plus {headed?, trace?, profile?, storageState?, devCommand?, waitURL?}; target:web specs are forced to web-playwright at runtime.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req testkitRunRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			if req.Headed {
				req.Headful = true
			}
			req.ForcePlaywright = true
			if req.Project == "" {
				req.Project = "playwright"
			}
			if strings.TrimSpace(req.StorageState) == "" && strings.TrimSpace(req.Profile) != "" {
				path, err := playwrightStorageStatePath(req.Profile)
				if err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
				req.StorageState = path
			}
			if strings.TrimSpace(req.StorageState) != "" {
				if err := os.MkdirAll(filepath.Dir(req.StorageState), 0o700); err != nil {
					return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
				}
			}
			job, err := studioJobs.startTestkitRun(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			out := job.snapshot()
			out["playwright"] = true
			if req.Profile != "" {
				out["profile"] = req.Profile
			}
			if req.StorageState != "" {
				out["storageState"] = req.StorageState
			}
			return OpsResult{OK: true, Initial: out}
		})

	reg("playwright_profile_auth",
		"Open a headed Playwright browser on this machine so a user can sign in and save a named storage-state profile. Payload: {dir?, url, profile|storageState, successURL?, timeoutSec?, headless?}. Long-running — returns a jobId; poll studio_job_status.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req playwrightProfileAuthRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := studioJobs.startPlaywrightProfileAuth(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			out := job.snapshot()
			out["profile"] = req.Profile
			return OpsResult{OK: true, Initial: out}
		})

	reg("playwright_profile_auth_finish",
		"Finish a running playwright_profile_auth job and save storage state immediately. Payload: {jobId}. Useful after the user completes 2FA in the headed browser.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			path, err := signalPlaywrightProfileAuth(req.JobID, "finish")
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]any{"jobId": req.JobID, "signal": "finish", "path": path}}
		})

	reg("playwright_profile_auth_cancel",
		"Cancel a running playwright_profile_auth job without saving new storage state. Payload: {jobId}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			path, err := signalPlaywrightProfileAuth(req.JobID, "cancel")
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]any{"jobId": req.JobID, "signal": "cancel", "path": path}}
		})

	reg("playwright_native_run",
		"Run a project's native Playwright suite (`npx playwright test`) on this machine. Use this when the app already has playwright.config.* and .spec.ts tests. Payload: {dir, config?, project?, grep?, workers?, headed?, trace?, reporter?, args?, env?, devCommand?, waitURL?}. Long-running — returns a jobId; poll studio_job_status, then project_test_report.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req playwrightNativeRunRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := studioJobs.startPlaywrightNativeRun(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			out := job.snapshot()
			out["playwrightNative"] = true
			return OpsResult{OK: true, Initial: out}
		})

	reg("playwright_profiles",
		"List named Playwright browser profiles saved by Yaver on this machine. Profiles are storageState JSON files under ~/.yaver/playwright-storage and can be reused by playwright_run {profile}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			dir, profiles, err := playwrightProfiles()
			if err != nil {
				return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]any{"dir": dir, "profiles": profiles}}
		})

	reg("playwright_profile_delete",
		"Delete a saved Playwright browser profile from this machine. Payload: {profile} for a named profile, or {storageState} for an explicit path inside ~/.yaver/playwright-storage.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Profile      string `json:"profile"`
				StorageState string `json:"storageState"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			path, existed, err := playwrightDeleteProfile(req.Profile, req.StorageState)
			if err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]any{"path": path, "deleted": existed}}
		})

	reg("playwright_artifact",
		"Fetch a Playwright run artifact for mobile/web playback: trace zip, screenshot, poster, clip, or highlight reel. Payload matches project_test_artifact: {jobId, path}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
				Path  string `json:"path"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			art, err := readTestkitArtifact(strings.TrimSpace(req.JobID), strings.TrimSpace(req.Path))
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: art}
		})

	reg("playwright_artifacts",
		"List artifacts for a finished playwright_run or playwright_native_run without inlining bytes. Payload: {jobId}.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			rep := getTestkitReport(strings.TrimSpace(req.JobID))
			if rep == nil {
				return OpsResult{OK: false, Code: "not_found", Error: "no report for that jobId"}
			}
			return OpsResult{OK: true, Initial: map[string]any{"jobId": req.JobID, "dir": rep.Dir, "artifacts": rep.Artifacts}}
		})

	reg("playwright_trace_inspect",
		"Inspect a Playwright trace.zip artifact referenced by a completed run without extracting it. Payload: {jobId, path}. Returns entry names/sizes and trace metadata.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
				Path  string `json:"path"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			out, err := inspectTestkitTraceArtifact(strings.TrimSpace(req.JobID), strings.TrimSpace(req.Path))
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: out}
		})

	reg("playwright_runs",
		"List local Playwright run/auth artifact directories on this machine. Payload: {limit?}. Useful for mobile/Talos run history and Hetzner disk audits.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Limit int `json:"limit"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			runs := listPlaywrightRuns(req.Limit)
			return OpsResult{OK: true, Initial: map[string]any{"runs": runs, "count": len(runs)}}
		})

	reg("playwright_gc",
		"Delete old local Playwright artifacts from ~/.yaver/playwright-native, ~/.yaver/playwright-auth, and ~/.yaver/testkit. Payload: {olderThanHours?, dryRun?}. Defaults to dryRun=true.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				OlderThanHours int   `json:"olderThanHours"`
				DryRun         *bool `json:"dryRun"`
			}
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			dryRun := true
			if req.DryRun != nil {
				dryRun = *req.DryRun
			}
			res, err := gcPlaywrightArtifacts(req.OlderThanHours, dryRun)
			if err != nil {
				return OpsResult{OK: false, Code: "gc_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: res}
		})

	reg("project_test_specs",
		"List the test Features (yaver-tests/*.test.yaml specs) for a project. {dir? (repo root; default cwd), root? (override specs dir)}. Synchronous.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req testkitRunRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			root, err := resolveTestkitRoot(req)
			if err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
			specs, err := testkit.DiscoverSpecs(root)
			if err != nil {
				return OpsResult{OK: false, Code: "discover_failed", Error: err.Error()}
			}
			out := make([]map[string]any, 0, len(specs))
			for _, sp := range specs {
				out = append(out, map[string]any{
					"name": sp.Name, "target": string(sp.Target),
					"url": sp.URL, "steps": len(sp.Steps), "path": sp.Path,
				})
			}
			return OpsResult{OK: true, Initial: map[string]any{"root": root, "features": out}}
		})

	reg("project_test_run",
		"Run a project's web test suite (yaver-tests/*.test.yaml) via the embedded chromedp runner, recording video for a highlight reel. Runs on THIS machine — target a remote PC by setting OpsRequest.machine to that device. {dir? (repo root), root?, only? (single Feature name), env? (map injected as ${ENV} for spec cookies/secrets, e.g. {TALOS_SESSION_TOKEN: \"...\"}), concurrency?, headful?, video? (default true), project?}. Long-running — returns a jobId; poll studio_job_status then project_test_report.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req testkitRunRequest
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &req); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			job, err := studioJobs.startTestkitRun(req)
			if err != nil {
				return OpsResult{OK: false, Code: "start_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: job.snapshot()}
		})

	reg("project_test_report",
		"Feature-based report for a finished project_test_run: per-Feature pass/fail, duration, screenshots, and a highlight clip (mp4) per Feature plus a combined reel. {jobId}. Run project_test_run first and wait for completion.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
			}
			if len(payload) > 0 {
				_ = json.Unmarshal(payload, &req)
			}
			rep := getTestkitReport(strings.TrimSpace(req.JobID))
			if rep == nil {
				return OpsResult{OK: false, Code: "not_found", Error: "no report for that jobId (still running, or unknown)"}
			}
			return OpsResult{OK: true, Initial: rep}
		})

	reg("project_test_artifact",
		"Fetch a test artifact (a Feature highlight clip mp4, the combined reel, or a step screenshot png) for a finished run, base64-encoded, so the mobile/web UI can play short success/fail videos. {jobId, path (must be one referenced by project_test_report)}. Only files the report itself references are served.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				JobID string `json:"jobId"`
				Path  string `json:"path"`
			}
			if len(payload) > 0 {
				_ = json.Unmarshal(payload, &req)
			}
			art, err := readTestkitArtifact(strings.TrimSpace(req.JobID), strings.TrimSpace(req.Path))
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: art}
		})

	reg("project_test_grow",
		"Self-grow the test suite: scan the project's routes/components, diff against the coverage ledger (yaver-tests/.coverage.json), and return an author plan of uncovered Features. With author:true, ALSO enqueue the Yaver runner to actually write the new *.test.yaml specs (the tests grow themselves, no user prompt). {dir? (repo root), apply? (write/refresh ledger), author? (dispatch the runner), runner? (claude|codex|opencode)}. No specs are deleted.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Dir    string `json:"dir"`
				Apply  bool   `json:"apply"`
				Author bool   `json:"author"`
				Runner string `json:"runner"`
			}
			if len(payload) > 0 {
				_ = json.Unmarshal(payload, &req)
			}
			plan, err := growTestPlan(req.Dir, req.Apply || req.Author)
			if err != nil {
				return OpsResult{OK: false, Code: "grow_failed", Error: err.Error()}
			}
			// Autonomous authoring: hand the plan to the runner as a task so it
			// writes the specs itself. Best-effort — the plan is still returned.
			if req.Author && len(plan.Uncovered) > 0 && c.Server != nil && c.Server.taskMgr != nil {
				title := "Grow tests: " + filepath.Base(plan.ProjectDir)
				ctx := c.Ctx
				if ctx == nil {
					ctx = context.Background()
				}
				if deferral, deferred, derr := c.Server.deferIngressTaskToCloudWorkspace(ctx, "testkit-grow", "test", req.Runner, plan.ProjectDir); deferred {
					if deferral != nil {
						plan.TaskID = deferral.PendingTaskID
					}
					if derr != nil {
						plan.AuthorPrompt = ""
						return OpsResult{OK: false, Code: "cloud_workspace_required", Error: derr.Error(), Initial: plan}
					}
					plan.AuthorPrompt = ""
					return OpsResult{OK: true, Initial: plan}
				}
				if t, terr := c.Server.taskMgr.CreateTask(title, plan.AuthorPrompt, "", "testkit-grow", req.Runner, "", nil); terr == nil && t != nil {
					plan.TaskID = t.ID
				}
			}
			return OpsResult{OK: true, Initial: plan}
		})
}

// ── request + report shapes ──────────────────────────────────────────────────

type testkitRunRequest struct {
	Project         string            `json:"project"`     // label for the report
	Dir             string            `json:"dir"`         // repo root (yaver-tests resolved under it)
	Root            string            `json:"root"`        // explicit specs dir (overrides Dir/yaver-tests)
	Only            string            `json:"only"`        // run a single Feature by name
	Env             map[string]string `json:"env"`         // injected as process env for ${ENV} spec expansion
	Concurrency     int               `json:"concurrency"` // default 1
	Headful         bool              `json:"headful"`
	Headed          bool              `json:"headed"` // Playwright-style alias for headful
	Video           *bool             `json:"video"`  // default true
	Trace           bool              `json:"trace"`
	Profile         string            `json:"profile"`      // named Playwright storage-state profile
	StorageState    string            `json:"storageState"` // explicit Playwright storage-state JSON path
	ForcePlaywright bool              `json:"forcePlaywright"`
	DevCommand      string            `json:"devCommand"`    // optional app server command to run before tests
	WaitURL         string            `json:"waitURL"`       // URL to probe before tests; defaults to first spec URL
	DevTimeoutSec   int               `json:"devTimeoutSec"` // default 60
	KeepDevServer   bool              `json:"keepDevServer"` // leave devCommand running after tests
}

type playwrightProfileAuthRequest struct {
	Dir          string `json:"dir"`          // project dir for local node_modules resolution
	URL          string `json:"url"`          // login/start URL
	SuccessURL   string `json:"successURL"`   // optional URL substring that means login finished
	Profile      string `json:"profile"`      // named Playwright storage-state profile
	StorageState string `json:"storageState"` // explicit storage-state JSON path
	TimeoutSec   int    `json:"timeoutSec"`   // default 180
	Headless     bool   `json:"headless"`     // default false; true is useful for scripted auth URLs
	FinishPath   string `json:"-"`
	CancelPath   string `json:"-"`
}

type playwrightNativeRunRequest struct {
	Dir           string            `json:"dir"`     // project dir with playwright.config.*
	Config        string            `json:"config"`  // optional config path
	Project       string            `json:"project"` // Playwright project name
	Grep          string            `json:"grep"`
	Workers       int               `json:"workers"`
	Headed        bool              `json:"headed"`
	Trace         string            `json:"trace"`    // on|off|retain-on-failure|on-first-retry
	Reporter      string            `json:"reporter"` // default json
	Args          []string          `json:"args"`
	Env           map[string]string `json:"env"`
	TimeoutSec    int               `json:"timeoutSec"` // default 1800
	DevCommand    string            `json:"devCommand"`
	WaitURL       string            `json:"waitURL"`
	DevTimeoutSec int               `json:"devTimeoutSec"`
	KeepDevServer bool              `json:"keepDevServer"`
	Label         string            `json:"label"`
}

// testkitFeature is one spec's result, shaped for the highlights UI.
type testkitFeature struct {
	Name        string   `json:"name"`
	Status      string   `json:"status"` // "pass" | "fail"
	Target      string   `json:"target"`
	URL         string   `json:"url,omitempty"`
	DurationMs  int64    `json:"durationMs"`
	Steps       int      `json:"steps"`
	Error       string   `json:"error,omitempty"`
	FailStep    int      `json:"failStep,omitempty"`
	Screenshots []string `json:"screenshots,omitempty"`
	FramesDir   string   `json:"framesDir,omitempty"`
	ClipPath    string   `json:"clipPath,omitempty"`   // per-Feature highlight mp4 (downscaled/compressed)
	PosterPath  string   `json:"posterPath,omitempty"` // tiny jpg first-frame thumbnail (cheap on weak links)
	TracePath   string   `json:"tracePath,omitempty"`  // Playwright trace.zip, when trace capture is enabled
}

type testkitArtifactRef struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Name    string `json:"name,omitempty"`
	Mime    string `json:"mimeType,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Feature string `json:"feature,omitempty"`
	Step    int    `json:"step,omitempty"`
}

type testkitReport struct {
	Project    string               `json:"project,omitempty"`
	Total      int                  `json:"total"`
	Passed     int                  `json:"passed"`
	Failed     int                  `json:"failed"`
	DurationMs int64                `json:"durationMs"`
	Features   []testkitFeature     `json:"features"`
	Artifacts  []testkitArtifactRef `json:"artifacts,omitempty"`
	ReelPath   string               `json:"reelPath,omitempty"` // concatenated highlight reel
	Dir        string               `json:"dir"`
}

var testkitReports = struct {
	sync.Mutex
	m map[string]*testkitReport
}{m: map[string]*testkitReport{}}

func storeTestkitReport(jobID string, r *testkitReport) {
	testkitReports.Lock()
	defer testkitReports.Unlock()
	testkitReports.m[jobID] = r
}

func getTestkitReport(jobID string) *testkitReport {
	testkitReports.Lock()
	defer testkitReports.Unlock()
	return testkitReports.m[jobID]
}

// testkitEnvMu serializes the (Setenv → DiscoverSpecs) critical section so two
// concurrent project_test_run jobs with different secrets can't cross-pollinate
// the process env during spec expansion.
var testkitEnvMu sync.Mutex

func resolveTestkitRoot(req testkitRunRequest) (string, error) {
	if r := strings.TrimSpace(req.Root); r != "" {
		return filepath.Abs(r)
	}
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	return filepath.Abs(filepath.Join(dir, "yaver-tests"))
}

func resolveTestkitWorkDir(req testkitRunRequest, root string) (string, error) {
	if dir := strings.TrimSpace(req.Dir); dir != "" {
		return filepath.Abs(dir)
	}
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			return filepath.Dir(abs), nil
		}
	}
	return os.Getwd()
}

func firstSpecURL(specs []*testkit.Spec) string {
	for _, sp := range specs {
		if sp != nil && strings.TrimSpace(sp.URL) != "" {
			return strings.TrimSpace(sp.URL)
		}
	}
	return ""
}

func startPlaywrightDevServer(job *studioJob, req testkitRunRequest, workDir, waitURL string) (func(), error) {
	devCommand := strings.TrimSpace(req.DevCommand)
	if devCommand == "" {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", devCommand)
	} else {
		cmd = exec.CommandContext(ctx, preferredUnixShell(), "-c", devCommand)
	}
	cmd.Dir = workDir
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	setProcGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	job.log("dev", fmt.Sprintf("started dev server pid=%d in %s: %s", cmd.Process.Pid, workDir, devCommand))
	go scanPlaywrightDevLog(job, "dev stdout", stdout)
	go scanPlaywrightDevLog(job, "dev stderr", stderr)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	cleanup := func() {
		cancel()
		if cmd.Process != nil {
			select {
			case <-done:
				job.log("dev", "dev server already stopped")
				return
			default:
			}
			_ = killProcessGroup(cmd.Process.Pid, "TERM")
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = killProcessGroup(cmd.Process.Pid, "KILL")
				select {
				case <-done:
				case <-time.After(2 * time.Second):
				}
			}
		}
		job.log("dev", "stopped dev server")
	}

	waitURL = strings.TrimSpace(waitURL)
	if waitURL == "" {
		job.log("dev", "no waitURL or spec URL found; continuing without readiness probe")
		return cleanup, nil
	}
	timeout := time.Duration(req.DevTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), timeout)
	defer readyCancel()
	if err := waitForPlaywrightURL(readyCtx, waitURL, done); err != nil {
		cleanup()
		return nil, err
	}
	job.log("dev", "ready: "+waitURL)
	return cleanup, nil
}

func scanPlaywrightDevLog(job *studioJob, label string, r interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			job.log("", label+": "+line)
		}
	}
}

func waitForPlaywrightURL(ctx context.Context, rawURL string, processDone <-chan error) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastErr string
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return nil
			}
			lastErr = fmt.Sprintf("status %d", resp.StatusCode)
		} else {
			lastErr = err.Error()
		}
		select {
		case err := <-processDone:
			if err != nil {
				return fmt.Errorf("dev command exited before %s became ready: %v", rawURL, err)
			}
			return fmt.Errorf("dev command exited before %s became ready", rawURL)
		case <-ctx.Done():
			if lastErr != "" {
				return fmt.Errorf("timed out waiting for %s: %s", rawURL, lastErr)
			}
			return fmt.Errorf("timed out waiting for %s", rawURL)
		case <-ticker.C:
		}
	}
}

func forceWebSpecsToPlaywright(specs []*testkit.Spec) []*testkit.Spec {
	out := make([]*testkit.Spec, 0, len(specs))
	for _, sp := range specs {
		if sp == nil {
			continue
		}
		cp := *sp
		if cp.Target == "" || cp.Target == testkit.TargetWeb || cp.Target == testkit.TargetWebPlaywright {
			cp.Target = testkit.TargetWebPlaywright
		}
		out = append(out, &cp)
	}
	return out
}

func playwrightStorageStatePath(profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "", nil
	}
	name := safeFileName(profile)
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid profile name")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("cannot resolve home directory for Playwright profile")
	}
	return filepath.Join(home, ".yaver", "playwright-storage", name+".json"), nil
}

func playwrightStorageStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("cannot resolve home directory for Playwright profiles")
	}
	return filepath.Join(home, ".yaver", "playwright-storage"), nil
}

func playwrightProfiles() (string, []map[string]any, error) {
	dir, err := playwrightStorageStateDir()
	if err != nil {
		return "", nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return dir, []map[string]any{}, nil
	}
	if err != nil {
		return dir, nil, err
	}
	profiles := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		profiles = append(profiles, map[string]any{
			"name":    name,
			"path":    filepath.Join(dir, entry.Name()),
			"bytes":   info.Size(),
			"modTime": info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		return fmt.Sprint(profiles[i]["name"]) < fmt.Sprint(profiles[j]["name"])
	})
	return dir, profiles, nil
}

func playwrightDeleteProfile(profile, storageState string) (string, bool, error) {
	path := strings.TrimSpace(storageState)
	var err error
	if path == "" {
		path, err = playwrightStorageStatePath(profile)
		if err != nil {
			return "", false, err
		}
	}
	if path == "" {
		return "", false, fmt.Errorf("profile or storageState is required")
	}
	dir, err := playwrightStorageStateDir()
	if err != nil {
		return "", false, err
	}
	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		return "", false, err
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", false, fmt.Errorf("storageState must be inside %s", cleanDir)
	}
	if strings.ToLower(filepath.Ext(cleanPath)) != ".json" {
		return "", false, fmt.Errorf("storageState must be a .json file")
	}
	err = os.Remove(cleanPath)
	if os.IsNotExist(err) {
		return cleanPath, false, nil
	}
	if err != nil {
		return cleanPath, false, err
	}
	return cleanPath, true, nil
}

func (m *studioJobManager) startPlaywrightProfileAuth(req playwrightProfileAuthRequest) (*studioJob, error) {
	req.Profile = strings.TrimSpace(req.Profile)
	req.StorageState = strings.TrimSpace(req.StorageState)
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if req.StorageState == "" {
		path, err := playwrightStorageStatePath(req.Profile)
		if err != nil {
			return nil, err
		}
		req.StorageState = path
	}
	if req.StorageState == "" {
		return nil, fmt.Errorf("profile or storageState is required")
	}
	if err := os.MkdirAll(filepath.Dir(req.StorageState), 0o700); err != nil {
		return nil, err
	}
	workDir := strings.TrimSpace(req.Dir)
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		workDir = cwd
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("node not found on PATH: %w", err)
	}

	job := m.newJob("playwright-profile-auth", "")
	authDir := filepath.Join(playwrightAuthScratchBase(), job.ID)
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		return nil, err
	}
	req.FinishPath = filepath.Join(authDir, "finish.signal")
	req.CancelPath = filepath.Join(authDir, "cancel.signal")
	scriptPath := filepath.Join(authDir, "profile-auth.mjs")
	if err := os.WriteFile(scriptPath, []byte(buildPlaywrightProfileAuthScript(req)), 0o600); err != nil {
		return nil, err
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.fail(job, fmt.Sprintf("panic: %v", r))
			}
		}()
		job.mu.Lock()
		job.State = studioRunning
		job.Dir = authDir
		job.mu.Unlock()
		job.log("auth", "opening Playwright auth browser for "+req.URL)

		timeout := time.Duration(req.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = 180 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout+15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, nodePath, scriptPath)
		cmd.Dir = absWorkDir
		out, err := cmd.CombinedOutput()
		if tail := playwrightLastNonEmptyLines(string(out), 8); tail != "" {
			job.log("", tail)
		}
		if err != nil {
			m.fail(job, "profile auth failed: "+err.Error())
			return
		}
		if info, statErr := os.Stat(req.StorageState); statErr != nil || info.IsDir() {
			m.fail(job, "profile auth did not write storage state")
			return
		}
		job.mu.Lock()
		job.Dir = filepath.Dir(req.StorageState)
		job.ShotNames = []string{filepath.Base(req.StorageState)}
		job.State = studioCompleted
		job.FinishedAt = time.Now()
		job.mu.Unlock()
		job.log("done", "saved Playwright profile state to "+req.StorageState)
	}()
	return job, nil
}

func playwrightAuthScratchBase() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "yaver-playwright-auth")
	}
	return filepath.Join(home, ".yaver", "playwright-auth")
}

func signalPlaywrightProfileAuth(jobID, signal string) (string, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return "", fmt.Errorf("jobId is required")
	}
	job := studioJobs.get(jobID)
	if job == nil {
		return "", fmt.Errorf("unknown jobId")
	}
	job.mu.Lock()
	dir := job.Dir
	state := job.State
	kind := job.Kind
	job.mu.Unlock()
	if kind != "playwright-profile-auth" {
		return "", fmt.Errorf("job is not a playwright-profile-auth job")
	}
	if state != studioRunning {
		return "", fmt.Errorf("job is not running")
	}
	if dir == "" {
		return "", fmt.Errorf("job has no auth directory")
	}
	base, err := filepath.Abs(playwrightAuthScratchBase())
	if err != nil {
		return "", err
	}
	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, cleanDir)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("auth directory is outside %s", base)
	}
	name := "finish.signal"
	if signal == "cancel" {
		name = "cancel.signal"
	}
	path := filepath.Join(cleanDir, name)
	if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func buildPlaywrightProfileAuthScript(req playwrightProfileAuthRequest) string {
	timeout := req.TimeoutSec
	if timeout <= 0 {
		timeout = 180
	}
	var b strings.Builder
	b.WriteString("import { chromium } from 'playwright';\n")
	b.WriteString("import fs from 'node:fs';\n")
	b.WriteString("const storageStatePath = " + playwrightJSString(req.StorageState) + ";\n")
	b.WriteString("const startURL = " + playwrightJSString(req.URL) + ";\n")
	b.WriteString("const successURL = " + playwrightJSString(strings.TrimSpace(req.SuccessURL)) + ";\n")
	b.WriteString("const finishPath = " + playwrightJSString(strings.TrimSpace(req.FinishPath)) + ";\n")
	b.WriteString("const cancelPath = " + playwrightJSString(strings.TrimSpace(req.CancelPath)) + ";\n")
	b.WriteString(fmt.Sprintf("const timeoutMs = %d;\n", timeout*1000))
	b.WriteString(fmt.Sprintf("const browser = await chromium.launch({ headless: %t });\n", req.Headless))
	b.WriteString("const ctxOpts = {};\n")
	b.WriteString("if (fs.existsSync(storageStatePath)) ctxOpts.storageState = storageStatePath;\n")
	b.WriteString("const ctx = await browser.newContext(ctxOpts);\n")
	b.WriteString("const page = await ctx.newPage();\n")
	b.WriteString("await page.goto(startURL, { waitUntil: 'domcontentloaded' });\n")
	b.WriteString("const deadline = Date.now() + timeoutMs;\n")
	b.WriteString("while (Date.now() < deadline) {\n")
	b.WriteString("  if (cancelPath && fs.existsSync(cancelPath)) { await browser.close(); throw new Error('profile auth cancelled'); }\n")
	b.WriteString("  if (finishPath && fs.existsSync(finishPath)) break;\n")
	b.WriteString("  if (successURL && page.url().includes(successURL)) break;\n")
	b.WriteString("  await page.waitForTimeout(1000);\n")
	b.WriteString("}\n")
	b.WriteString("await ctx.storageState({ path: storageStatePath });\n")
	b.WriteString("await browser.close();\n")
	b.WriteString("console.log('saved storage state to ' + storageStatePath);\n")
	return b.String()
}

func playwrightJSString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (m *studioJobManager) startPlaywrightNativeRun(req playwrightNativeRunRequest) (*studioJob, error) {
	workDir := strings.TrimSpace(req.Dir)
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		workDir = cwd
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absWorkDir); err != nil {
		return nil, err
	}
	if !have("npx") {
		return nil, fmt.Errorf("npx not found on PATH")
	}

	job := m.newJob("playwright-native-run", "")
	artifactDir := filepath.Join(playwrightNativeArtifactBase(), job.ID)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, err
	}
	jsonPath := filepath.Join(artifactDir, "results.json")
	cmdline := buildPlaywrightNativeCommand(req)
	env := map[string]string{
		"CI":                           "1",
		"PLAYWRIGHT_JSON_OUTPUT_NAME":  jsonPath,
		"PLAYWRIGHT_HTML_REPORT":       filepath.Join(artifactDir, "html-report"),
		"PLAYWRIGHT_JUNIT_OUTPUT_NAME": filepath.Join(artifactDir, "junit.xml"),
		"PLAYWRIGHT_BLOB_OUTPUT_DIR":   filepath.Join(artifactDir, "blob-report"),
	}
	for k, v := range req.Env {
		env[k] = v
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.fail(job, fmt.Sprintf("panic: %v", r))
			}
		}()
		job.mu.Lock()
		job.State = studioRunning
		job.Dir = artifactDir
		job.mu.Unlock()

		if strings.TrimSpace(req.DevCommand) != "" {
			cleanup, err := startPlaywrightDevServer(job, testkitRunRequest{
				Dir:           absWorkDir,
				DevCommand:    req.DevCommand,
				WaitURL:       req.WaitURL,
				DevTimeoutSec: req.DevTimeoutSec,
				KeepDevServer: req.KeepDevServer,
				Env:           req.Env,
			}, absWorkDir, req.WaitURL)
			if err != nil {
				m.fail(job, "dev server: "+err.Error())
				return
			}
			if cleanup != nil && !req.KeepDevServer {
				defer cleanup()
			}
		}

		job.log("running", "$ "+cmdline)
		timeout := time.Duration(req.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = 30 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, preferredUnixShell(), "-c", cmdline)
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/c", cmdline)
		}
		cmd.Dir = absWorkDir
		cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		setProcGroup(cmd)
		out, runErr := cmd.CombinedOutput()
		logPath := filepath.Join(artifactDir, "run.log")
		_ = os.WriteFile(logPath, out, 0o644)
		if tail := playwrightLastNonEmptyLines(string(out), 12); tail != "" {
			job.log("", tail)
		}

		rep := buildPlaywrightNativeReport(req, artifactDir, jsonPath, logPath, runErr)
		storeTestkitReport(job.ID, rep)
		job.mu.Lock()
		job.Dir = artifactDir
		job.State = studioCompleted
		if runErr != nil {
			job.State = studioFailed
			job.Error = runErr.Error()
		}
		job.FinishedAt = time.Now()
		job.mu.Unlock()
		if runErr != nil {
			job.log("failed", fmt.Sprintf("%d passed / %d failed of %d test(s)", rep.Passed, rep.Failed, rep.Total))
			return
		}
		job.log("done", fmt.Sprintf("%d passed / %d failed of %d test(s)", rep.Passed, rep.Failed, rep.Total))
	}()
	return job, nil
}

func playwrightNativeArtifactBase() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "yaver-playwright-native")
	}
	return filepath.Join(home, ".yaver", "playwright-native")
}

func playwrightManagedArtifactBases() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return []string{
			playwrightNativeArtifactBase(),
			playwrightAuthScratchBase(),
			filepath.Join(os.TempDir(), "yaver-testkit"),
		}
	}
	return []string{
		playwrightNativeArtifactBase(),
		playwrightAuthScratchBase(),
		filepath.Join(home, ".yaver", "testkit"),
	}
}

func listPlaywrightRuns(limit int) []map[string]any {
	var runs []map[string]any
	for _, base := range playwrightManagedArtifactBases() {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		kind := "testkit"
		if strings.Contains(base, "playwright-native") {
			kind = "native"
		} else if strings.Contains(base, "playwright-auth") {
			kind = "profile-auth"
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(base, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}
			runs = append(runs, map[string]any{
				"id":      entry.Name(),
				"kind":    kind,
				"path":    path,
				"bytes":   playwrightDirSize(path),
				"modTime": info.ModTime().UTC().Format(time.RFC3339),
			})
		}
	}
	sort.SliceStable(runs, func(i, j int) bool {
		return fmt.Sprint(runs[i]["modTime"]) > fmt.Sprint(runs[j]["modTime"])
	})
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs
}

func gcPlaywrightArtifacts(olderThanHours int, dryRun bool) (map[string]any, error) {
	if olderThanHours <= 0 {
		olderThanHours = 24 * 7
	}
	cutoff := time.Now().Add(-time.Duration(olderThanHours) * time.Hour)
	var deleted []map[string]any
	var kept []map[string]any
	var bytes int64
	for _, base := range playwrightManagedArtifactBases() {
		cleanBase, err := filepath.Abs(base)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(cleanBase)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(cleanBase, entry.Name())
			cleanPath, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(cleanBase, cleanPath)
			if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			size := playwrightDirSize(cleanPath)
			item := map[string]any{
				"path": cleanPath, "bytes": size,
				"modTime": info.ModTime().UTC().Format(time.RFC3339),
			}
			if info.ModTime().After(cutoff) {
				kept = append(kept, item)
				continue
			}
			deleted = append(deleted, item)
			bytes += size
			if !dryRun {
				if err := os.RemoveAll(cleanPath); err != nil {
					return nil, err
				}
			}
		}
	}
	return map[string]any{
		"dryRun":         dryRun,
		"olderThanHours": olderThanHours,
		"deleted":        deleted,
		"kept":           kept,
		"bytes":          bytes,
	}, nil
}

func playwrightDirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func buildPlaywrightNativeCommand(req playwrightNativeRunRequest) string {
	reporter := strings.TrimSpace(req.Reporter)
	if reporter == "" {
		reporter = "json"
	}
	args := []string{"npx", "playwright", "test", "--reporter=" + reporter}
	if req.Config != "" {
		args = append(args, "--config="+req.Config)
	}
	if req.Project != "" {
		args = append(args, "--project="+req.Project)
	}
	if req.Grep != "" {
		args = append(args, "--grep="+req.Grep)
	}
	if req.Workers > 0 {
		args = append(args, fmt.Sprintf("--workers=%d", req.Workers))
	}
	if req.Headed {
		args = append(args, "--headed")
	}
	if req.Trace != "" {
		args = append(args, "--trace="+req.Trace)
	}
	args = append(args, req.Args...)
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, opsShellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func buildPlaywrightNativeReport(req playwrightNativeRunRequest, artifactDir, jsonPath, logPath string, runErr error) *testkitReport {
	project := strings.TrimSpace(req.Label)
	if project == "" {
		project = "playwright-native"
	}
	rep := &testkitReport{Project: project, Dir: artifactDir}
	started := time.Now()
	rep.DurationMs = 0
	if logPath != "" {
		appendTestkitArtifact(rep, "log", logPath, "", 0)
	}
	if jsonPath != "" {
		appendTestkitArtifact(rep, "json", jsonPath, "", 0)
	}
	if err := fillPlaywrightNativeReportFromJSON(rep, jsonPath); err != nil {
		status := "pass"
		if runErr != nil {
			status = "fail"
			rep.Failed = 1
		} else {
			rep.Passed = 1
		}
		rep.Total = 1
		rep.Features = []testkitFeature{{
			Name:       "native playwright",
			Status:     status,
			Target:     "playwright-native",
			DurationMs: time.Since(started).Milliseconds(),
			Error:      strings.TrimSpace(fmt.Sprint(runErr)),
		}}
	}
	scanPlaywrightNativeArtifacts(rep, artifactDir)
	return rep
}

func fillPlaywrightNativeReportFromJSON(rep *testkitReport, jsonPath string) error {
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		return err
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return err
	}
	if stats, ok := root["stats"].(map[string]any); ok {
		rep.Passed = int(jsonNumber(stats["expected"]))
		rep.Failed = int(jsonNumber(stats["unexpected"]))
		rep.DurationMs = int64(jsonNumber(stats["duration"]))
		skipped := int(jsonNumber(stats["skipped"]))
		flaky := int(jsonNumber(stats["flaky"]))
		rep.Total = rep.Passed + rep.Failed + skipped + flaky
	}
	var features []testkitFeature
	collectPlaywrightJSONSpecs(root["suites"], &features)
	if len(features) > 0 {
		rep.Features = features
		if rep.Total == 0 {
			rep.Total = len(features)
			for _, f := range features {
				if f.Status == "fail" {
					rep.Failed++
				} else {
					rep.Passed++
				}
			}
		}
	}
	return nil
}

func collectPlaywrightJSONSpecs(v any, out *[]testkitFeature) {
	suites, ok := v.([]any)
	if !ok {
		return
	}
	for _, item := range suites {
		suite, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if specs, ok := suite["specs"].([]any); ok {
			for _, rawSpec := range specs {
				spec, ok := rawSpec.(map[string]any)
				if !ok {
					continue
				}
				title := strings.TrimSpace(fmt.Sprint(spec["title"]))
				tests, _ := spec["tests"].([]any)
				status := "pass"
				var errText string
				var duration int64
				for _, rawTest := range tests {
					test, _ := rawTest.(map[string]any)
					results, _ := test["results"].([]any)
					for _, rawResult := range results {
						result, _ := rawResult.(map[string]any)
						duration += int64(jsonNumber(result["duration"]))
						if s := strings.TrimSpace(fmt.Sprint(result["status"])); s != "" && s != "passed" {
							status = "fail"
						}
						if e, ok := result["error"].(map[string]any); ok && errText == "" {
							errText = strings.TrimSpace(fmt.Sprint(e["message"]))
						}
					}
				}
				if title == "" {
					title = "playwright spec"
				}
				*out = append(*out, testkitFeature{
					Name: title, Status: status, Target: "playwright-native",
					DurationMs: duration, Error: errText,
				})
			}
		}
		collectPlaywrightJSONSpecs(suite["suites"], out)
	}
}

func jsonNumber(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func scanPlaywrightNativeArtifacts(rep *testkitReport, artifactDir string) {
	seen := map[string]bool{}
	for _, a := range rep.Artifacts {
		seen[a.Path] = true
	}
	_ = filepath.WalkDir(artifactDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || seen[path] {
			return nil
		}
		rel, relErr := filepath.Rel(artifactDir, path)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		kind := "artifact"
		lower := strings.ToLower(rel)
		switch {
		case strings.HasSuffix(lower, ".png"):
			kind = "screenshot"
		case strings.HasSuffix(lower, ".webm"), strings.HasSuffix(lower, ".mp4"):
			kind = "video"
		case strings.HasSuffix(lower, ".zip"):
			kind = "trace"
		case strings.HasSuffix(lower, ".html"):
			kind = "html"
		case strings.HasSuffix(lower, ".xml"):
			kind = "xml"
		case strings.HasSuffix(lower, ".json"):
			kind = "json"
		case strings.HasSuffix(lower, ".log"), strings.HasSuffix(lower, ".txt"):
			kind = "log"
		}
		appendTestkitArtifact(rep, kind, path, "", 0)
		seen[path] = true
		return nil
	})
}

func playwrightReadiness(dir string) map[string]any {
	workDir := strings.TrimSpace(dir)
	if workDir == "" {
		workDir = "."
	}
	if abs, err := filepath.Abs(workDir); err == nil {
		workDir = abs
	}
	out := map[string]any{
		"ok":      false,
		"workDir": workDir,
		"fix": []string{
			"npm install -D playwright",
			"npx playwright install chromium",
		},
	}

	nodePath, err := exec.LookPath("node")
	if err != nil {
		out["node"] = map[string]any{"ok": false, "error": "node not found on PATH"}
		return out
	}
	out["node"] = map[string]any{
		"ok":      true,
		"path":    nodePath,
		"version": strings.TrimSpace(runToolOutput(workDir, nodePath, "--version")),
	}

	resolveScript := "console.log(require.resolve('playwright'))"
	pwPath, pwErr := runNodeEval(workDir, resolveScript)
	if pwErr != nil {
		out["playwright"] = map[string]any{"ok": false, "error": strings.TrimSpace(pwErr.Error())}
		return out
	}
	out["playwright"] = map[string]any{"ok": true, "module": strings.TrimSpace(pwPath)}

	chromiumScript := "const { chromium } = require('playwright'); console.log(chromium.executablePath())"
	chromiumPath, chromiumErr := runNodeEval(workDir, chromiumScript)
	chromiumPath = strings.TrimSpace(chromiumPath)
	chromium := map[string]any{"ok": false}
	if chromiumErr != nil {
		chromium["error"] = strings.TrimSpace(chromiumErr.Error())
	} else if chromiumPath == "" {
		chromium["error"] = "playwright returned an empty Chromium executable path"
	} else if _, statErr := os.Stat(chromiumPath); statErr != nil {
		chromium["path"] = chromiumPath
		chromium["error"] = "Chromium executable is missing; run `npx playwright install chromium`"
	} else {
		chromium["ok"] = true
		chromium["path"] = chromiumPath
	}
	out["chromium"] = chromium
	out["ok"] = chromium["ok"] == true
	return out
}

func runNodeEval(workDir, script string) (string, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "-e", script)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, playwrightLastNonEmptyLines(string(out), 4))
	}
	return string(out), nil
}

func runToolOutput(workDir string, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

func playwrightLastNonEmptyLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			kept = append(kept, line)
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return strings.Join(kept, "\n")
}

// startTestkitRun runs the web suite in a studioJob goroutine.
func (m *studioJobManager) startTestkitRun(req testkitRunRequest) (*studioJob, error) {
	root, err := resolveTestkitRoot(req)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("specs dir not found: %s", root)
	}
	job := m.newJob("testkit-run", "")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.fail(job, fmt.Sprintf("panic: %v", r))
			}
		}()
		job.mu.Lock()
		job.State = studioRunning
		job.mu.Unlock()

		// Inject env (secrets/tokens) and discover under the serialized lock so
		// ${ENV} expansion is deterministic across concurrent runs.
		testkitEnvMu.Lock()
		var restore []func()
		for k, v := range req.Env {
			old, had := os.LookupEnv(k)
			_ = os.Setenv(k, v)
			kk, oo, hh := k, old, had
			restore = append(restore, func() {
				if hh {
					_ = os.Setenv(kk, oo)
				} else {
					_ = os.Unsetenv(kk)
				}
			})
		}
		specs, derr := testkit.DiscoverSpecs(root)
		for _, f := range restore {
			f()
		}
		testkitEnvMu.Unlock()
		if derr != nil {
			m.fail(job, "discover specs: "+derr.Error())
			return
		}
		if req.ForcePlaywright {
			specs = forceWebSpecsToPlaywright(specs)
		}
		if only := strings.TrimSpace(req.Only); only != "" {
			filtered := specs[:0]
			for _, sp := range specs {
				if sp.Name == only {
					filtered = append(filtered, sp)
				}
			}
			specs = filtered
		}
		if len(specs) == 0 {
			m.fail(job, "no specs to run under "+root)
			return
		}

		if strings.TrimSpace(req.DevCommand) != "" {
			workDir, err := resolveTestkitWorkDir(req, root)
			if err != nil {
				m.fail(job, "resolve dev workdir: "+err.Error())
				return
			}
			waitURL := strings.TrimSpace(req.WaitURL)
			if waitURL == "" {
				waitURL = firstSpecURL(specs)
			}
			cleanup, err := startPlaywrightDevServer(job, req, workDir, waitURL)
			if err != nil {
				m.fail(job, "dev server: "+err.Error())
				return
			}
			if cleanup != nil && !req.KeepDevServer {
				defer cleanup()
			}
		}

		job.log("running", fmt.Sprintf("%d feature(s) from %s", len(specs), root))

		conc := req.Concurrency
		if conc < 1 {
			conc = 1
		}
		video := true
		if req.Video != nil {
			video = *req.Video
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		opts := testkit.RunOptions{
			Headful:                req.Headful || req.Headed,
			ForceVideo:             video,
			PlaywrightStorageState: strings.TrimSpace(req.StorageState),
			PlaywrightTrace:        req.Trace,
		}
		suite := testkit.RunSuite(ctx, specs, opts, conc)

		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".yaver", "testkit", job.ID)
		_ = os.MkdirAll(dir, 0o755)

		rep := buildTestkitReport(req.Project, dir, suite)
		storeTestkitReport(job.ID, rep)

		job.mu.Lock()
		job.Dir = dir
		job.State = studioCompleted
		job.FinishedAt = time.Now()
		job.mu.Unlock()
		job.log("done", fmt.Sprintf("%d passed / %d failed of %d feature(s)", rep.Passed, rep.Failed, rep.Total))
	}()
	return job, nil
}

// buildTestkitReport turns a testkit Suite into the feature-based, highlight
// report shape and stitches a per-Feature mp4 clip (best-effort, needs ffmpeg).
func buildTestkitReport(project, dir string, suite *testkit.Suite) *testkitReport {
	total, passed, failed := suite.Counts()
	rep := &testkitReport{
		Project: project, Total: total, Passed: passed, Failed: failed,
		DurationMs: suite.FinishedAt.Sub(suite.StartedAt).Milliseconds(),
		Dir:        dir,
	}
	var clips []string
	for _, r := range suite.Results {
		if r == nil {
			continue
		}
		f := testkitFeature{
			Name: r.Spec.Name, Target: string(r.Spec.Target), URL: r.Spec.URL,
			Status: "pass", DurationMs: r.Duration().Milliseconds(), Steps: len(r.Steps),
		}
		if !r.Passed {
			f.Status = "fail"
			if r.Err != nil {
				f.Error = r.Err.Error()
			}
		}
		for _, st := range r.Steps {
			if st.ScreenshotPath != "" {
				f.Screenshots = append(f.Screenshots, st.ScreenshotPath)
				appendTestkitArtifact(rep, "screenshot", st.ScreenshotPath, r.Spec.Name, st.Index)
			}
			if st.Err != nil && f.FailStep == 0 {
				f.FailStep = st.Index
			}
		}
		// Keep the report JSON lean for weak/LAN links — last few shots are the
		// useful ones (final state + the failure). The poster covers the thumbnail.
		if len(f.Screenshots) > 3 {
			f.Screenshots = f.Screenshots[len(f.Screenshots)-3:]
		}
		for _, st := range r.Steps {
			if st.ScreenshotPath == "" {
				continue
			}
			trace := filepath.Join(filepath.Dir(st.ScreenshotPath), "trace.zip")
			if info, err := os.Stat(trace); err == nil && !info.IsDir() {
				f.TracePath = trace
				appendTestkitArtifact(rep, "trace", trace, r.Spec.Name, 0)
				break
			}
		}
		if r.VideoFramesDir != "" {
			f.FramesDir = r.VideoFramesDir
			clip := filepath.Join(dir, safeFileName(r.Spec.Name)+".mp4")
			if err := stitchFramesToMP4(r.VideoFramesDir, clip); err == nil {
				f.ClipPath = clip
				clips = append(clips, clip)
				appendTestkitArtifact(rep, "clip", clip, r.Spec.Name, 0)
				// Tiny poster so the mobile UI shows the result instantly on a
				// weak link without pulling the whole clip.
				poster := filepath.Join(dir, safeFileName(r.Spec.Name)+".jpg")
				if makePoster(clip, poster) == nil {
					f.PosterPath = poster
					appendTestkitArtifact(rep, "poster", poster, r.Spec.Name, 0)
				}
			}
		}
		rep.Features = append(rep.Features, f)
	}
	sort.SliceStable(rep.Features, func(i, j int) bool {
		// failures first, so the reel leads with what broke
		return rep.Features[i].Status == "fail" && rep.Features[j].Status != "fail"
	})
	if len(clips) > 0 {
		reel := filepath.Join(dir, "highlights.mp4")
		if err := concatMP4(clips, reel); err == nil {
			rep.ReelPath = reel
			appendTestkitArtifact(rep, "reel", reel, "", 0)
		}
	}
	return rep
}

func appendTestkitArtifact(rep *testkitReport, kind, path, feature string, step int) {
	if rep == nil || strings.TrimSpace(path) == "" {
		return
	}
	ref := testkitArtifactRef{
		Kind:    kind,
		Path:    path,
		Name:    filepath.Base(path),
		Mime:    artifactMime(path),
		Feature: feature,
		Step:    step,
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		ref.Bytes = info.Size()
	} else {
		return
	}
	rep.Artifacts = append(rep.Artifacts, ref)
}

// stitchFramesToMP4 builds an mp4 from a directory of screencast PNG frames.
// Best-effort: if ffmpeg is absent or frames are missing, returns an error and
// the caller falls back to the frames dir / screenshots.
func stitchFramesToMP4(framesDir, out string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found")
	}
	entries, err := os.ReadDir(framesDir)
	if err != nil {
		return err
	}
	hasPNG := false
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			hasPNG = true
			break
		}
	}
	if !hasPNG {
		return fmt.Errorf("no png frames in %s", framesDir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Downscale to <=640px wide + H.264 CRF so highlight clips are small enough
	// to pull over a weak/LAN link (a phone on poor internet). -2 keeps height
	// even for yuv420p.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y",
		"-framerate", "8", "-pattern_type", "glob", "-i", filepath.Join(framesDir, "*.png"),
		"-vf", "scale='min(640,iw)':-2", "-c:v", "libx264", "-preset", "veryfast",
		"-crf", "32", "-pix_fmt", "yuv420p", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, string(b))
	}
	return nil
}

// makePoster grabs the first frame of a clip as a small jpg thumbnail — a few
// KB, so the result is visible instantly even before the clip is fetched.
func makePoster(clip, out string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", clip,
		"-frames:v", "1", "-vf", "scale=360:-2", "-q:v", "6", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg poster: %v: %s", err, string(b))
	}
	return nil
}

// concatMP4 concatenates per-Feature clips into a single highlight reel.
func concatMP4(clips []string, out string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found")
	}
	if len(clips) == 0 {
		return fmt.Errorf("no clips")
	}
	list := out + ".txt"
	var sb strings.Builder
	for _, c := range clips {
		sb.WriteString("file '" + c + "'\n")
	}
	if err := os.WriteFile(list, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	defer os.Remove(list)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-f", "concat", "-safe", "0",
		"-i", list, "-c", "copy", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg concat: %v: %s", err, string(b))
	}
	return nil
}

func safeFileName(s string) string {
	r := strings.NewReplacer("/", "-", " ", "-", ":", "-", "\\", "-")
	return r.Replace(s)
}

func testkitArtifactAllowed(jobID, path string) (*testkitReport, error) {
	rep := getTestkitReport(jobID)
	if rep == nil {
		return nil, fmt.Errorf("no report for that jobId")
	}
	allowed := map[string]bool{}
	if rep.ReelPath != "" {
		allowed[rep.ReelPath] = true
	}
	for _, a := range rep.Artifacts {
		if a.Path != "" {
			allowed[a.Path] = true
		}
	}
	for _, f := range rep.Features {
		if f.ClipPath != "" {
			allowed[f.ClipPath] = true
		}
		if f.PosterPath != "" {
			allowed[f.PosterPath] = true
		}
		if f.TracePath != "" {
			allowed[f.TracePath] = true
		}
		for _, s := range f.Screenshots {
			allowed[s] = true
		}
	}
	if !allowed[path] {
		return nil, fmt.Errorf("artifact not referenced by this run")
	}
	return rep, nil
}

// readTestkitArtifact serves a clip/reel/screenshot for a finished run, but only
// files the stored report actually references (no arbitrary filesystem reads).
func readTestkitArtifact(jobID, path string) (map[string]any, error) {
	if _, err := testkitArtifactAllowed(jobID, path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Cap base64-over-ops payloads; short highlight clips are small.
	if len(b) > 24*1024*1024 {
		return nil, fmt.Errorf("artifact too large to inline (%d bytes)", len(b))
	}
	return map[string]any{
		"name":     filepath.Base(path),
		"mimeType": artifactMime(path),
		"bytes":    len(b),
		"base64":   base64.StdEncoding.EncodeToString(b),
	}, nil
}

func inspectTestkitTraceArtifact(jobID, path string) (map[string]any, error) {
	if _, err := testkitArtifactAllowed(jobID, path); err != nil {
		return nil, err
	}
	if strings.ToLower(filepath.Ext(path)) != ".zip" {
		return nil, fmt.Errorf("artifact is not a trace zip")
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var entries []map[string]any
	var timeline []map[string]any
	var totalBytes int64
	var traceEvents, resources, screenshots, sourceFiles int
	for i, f := range zr.File {
		totalBytes += int64(f.UncompressedSize64)
		name := f.Name
		lower := strings.ToLower(name)
		switch {
		case strings.Contains(lower, "trace") && strings.HasSuffix(lower, ".trace"):
			traceEvents++
			timeline = append(timeline, inspectPlaywrightTraceEvents(f)...)
		case strings.Contains(lower, "resources/"):
			resources++
		case strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".webp"):
			screenshots++
		case strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".html"):
			sourceFiles++
		}
		if i < 200 {
			entries = append(entries, map[string]any{
				"name":       name,
				"bytes":      f.UncompressedSize64,
				"compressed": f.CompressedSize64,
			})
		}
	}
	return map[string]any{
		"name":        filepath.Base(path),
		"path":        path,
		"bytes":       fileSize(path),
		"entryCount":  len(zr.File),
		"shown":       len(entries),
		"entries":     entries,
		"totalBytes":  totalBytes,
		"traceFiles":  traceEvents,
		"resources":   resources,
		"screenshots": screenshots,
		"sourceFiles": sourceFiles,
		"timeline":    timeline,
	}, nil
}

func inspectPlaywrightTraceEvents(f *zip.File) []map[string]any {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()

	type traceCall struct {
		ID        string
		APIName   string
		Method    string
		Class     string
		StartTime float64
		EndTime   float64
		PageID    string
		Error     string
		Params    map[string]any
	}
	calls := map[string]*traceCall{}
	var order []string
	var loose []map[string]any

	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		typ := strings.TrimSpace(fmt.Sprint(ev["type"]))
		callID := firstTraceString(ev, "callId", "callID", "id")
		apiName := firstTraceString(ev, "apiName", "method")
		if callID == "" && apiName == "" && typ != "event" {
			continue
		}
		if callID == "" {
			loose = append(loose, compactTraceEvent(ev))
			continue
		}
		c := calls[callID]
		if c == nil {
			c = &traceCall{ID: callID}
			calls[callID] = c
			order = append(order, callID)
		}
		if apiName != "" {
			c.APIName = apiName
		}
		if method := firstTraceString(ev, "method"); method != "" {
			c.Method = method
		}
		if class := firstTraceString(ev, "class"); class != "" {
			c.Class = class
		}
		if pageID := firstTraceString(ev, "pageId", "pageID"); pageID != "" {
			c.PageID = pageID
		}
		if params, ok := ev["params"].(map[string]any); ok {
			c.Params = params
		}
		if start := jsonNumber(ev["startTime"]); start > 0 {
			c.StartTime = start
		}
		if end := jsonNumber(ev["endTime"]); end > 0 {
			c.EndTime = end
		}
		if typ == "after" && c.EndTime == 0 {
			c.EndTime = jsonNumber(ev["time"])
		}
		if errObj, ok := ev["error"].(map[string]any); ok {
			c.Error = strings.TrimSpace(fmt.Sprint(errObj["message"]))
		} else if errText := firstTraceString(ev, "error"); errText != "" {
			c.Error = errText
		}
	}

	out := make([]map[string]any, 0, len(order)+len(loose))
	for _, id := range order {
		c := calls[id]
		item := map[string]any{
			"callId":    c.ID,
			"apiName":   c.APIName,
			"method":    c.Method,
			"class":     c.Class,
			"pageId":    c.PageID,
			"startTime": c.StartTime,
			"endTime":   c.EndTime,
			"duration":  c.EndTime - c.StartTime,
			"error":     c.Error,
		}
		if c.Params != nil {
			item["params"] = summarizeTraceParams(c.Params)
		}
		out = append(out, item)
		if len(out) >= 300 {
			return out
		}
	}
	for _, item := range loose {
		out = append(out, item)
		if len(out) >= 300 {
			return out
		}
	}
	return out
}

func firstTraceString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func compactTraceEvent(ev map[string]any) map[string]any {
	return map[string]any{
		"type":      firstTraceString(ev, "type"),
		"method":    firstTraceString(ev, "method"),
		"class":     firstTraceString(ev, "class"),
		"time":      jsonNumber(ev["time"]),
		"startTime": jsonNumber(ev["startTime"]),
	}
}

func summarizeTraceParams(params map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"selector", "url", "text", "key", "button", "name", "title"} {
		if v, ok := params[k]; ok {
			s := strings.TrimSpace(fmt.Sprint(v))
			if len(s) > 200 {
				s = s[:200] + "..."
			}
			out[k] = s
		}
	}
	return out
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func artifactMime(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "video/mp4"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webm":
		return "video/webm"
	case ".zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}
