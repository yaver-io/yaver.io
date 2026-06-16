package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
//   project_test_specs  — list the Features (specs) discovered for a project
//   project_test_run    — run the suite (async job), records video, returns jobId
//   project_test_report — feature-based report + highlight clips for a finished run
//   project_test_grow   — self-grow: plan + ledger of uncovered Features for the
//                          runner to author (the "tests write themselves" loop)

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

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
		"Self-grow the test suite: scan the project's routes/components, diff against the coverage ledger (yaver-tests/.coverage.json), and return an author plan of uncovered Features for the Yaver runner to write as new *.test.yaml specs. {dir? (repo root), apply? (write/refresh the ledger)}. The runner consumes the plan; no specs are deleted. Synchronous.",
		func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Dir   string `json:"dir"`
				Apply bool   `json:"apply"`
			}
			if len(payload) > 0 {
				_ = json.Unmarshal(payload, &req)
			}
			plan, err := growTestPlan(req.Dir, req.Apply)
			if err != nil {
				return OpsResult{OK: false, Code: "grow_failed", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: plan}
		})
}

// ── request + report shapes ──────────────────────────────────────────────────

type testkitRunRequest struct {
	Project     string            `json:"project"`     // label for the report
	Dir         string            `json:"dir"`         // repo root (yaver-tests resolved under it)
	Root        string            `json:"root"`        // explicit specs dir (overrides Dir/yaver-tests)
	Only        string            `json:"only"`        // run a single Feature by name
	Env         map[string]string `json:"env"`         // injected as process env for ${ENV} spec expansion
	Concurrency int               `json:"concurrency"` // default 1
	Headful     bool              `json:"headful"`
	Video       *bool             `json:"video"` // default true
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
	ClipPath    string   `json:"clipPath,omitempty"` // per-Feature highlight mp4
}

type testkitReport struct {
	Project    string           `json:"project,omitempty"`
	Total      int              `json:"total"`
	Passed     int              `json:"passed"`
	Failed     int              `json:"failed"`
	DurationMs int64            `json:"durationMs"`
	Features   []testkitFeature `json:"features"`
	ReelPath   string           `json:"reelPath,omitempty"` // concatenated highlight reel
	Dir        string           `json:"dir"`
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
		opts := testkit.RunOptions{Headful: req.Headful, ForceVideo: video}
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
			}
			if st.Err != nil && f.FailStep == 0 {
				f.FailStep = st.Index
			}
		}
		if r.VideoFramesDir != "" {
			f.FramesDir = r.VideoFramesDir
			clip := filepath.Join(dir, safeFileName(r.Spec.Name)+".mp4")
			if err := stitchFramesToMP4(r.VideoFramesDir, clip); err == nil {
				f.ClipPath = clip
				clips = append(clips, clip)
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
		}
	}
	return rep
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
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y",
		"-framerate", "8", "-pattern_type", "glob", "-i", filepath.Join(framesDir, "*.png"),
		"-pix_fmt", "yuv420p", "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, string(b))
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

// readTestkitArtifact serves a clip/reel/screenshot for a finished run, but only
// files the stored report actually references (no arbitrary filesystem reads).
func readTestkitArtifact(jobID, path string) (map[string]any, error) {
	rep := getTestkitReport(jobID)
	if rep == nil {
		return nil, fmt.Errorf("no report for that jobId")
	}
	allowed := map[string]bool{}
	if rep.ReelPath != "" {
		allowed[rep.ReelPath] = true
	}
	for _, f := range rep.Features {
		if f.ClipPath != "" {
			allowed[f.ClipPath] = true
		}
		for _, s := range f.Screenshots {
			allowed[s] = true
		}
	}
	if !allowed[path] {
		return nil, fmt.Errorf("artifact not referenced by this run")
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
	default:
		return "application/octet-stream"
	}
}
