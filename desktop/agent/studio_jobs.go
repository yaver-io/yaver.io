package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/studio"
)

// studio_jobs.go — async Studio jobs with live status, so an agentic LLM (host
// Claude Code / the mobile assistant) can kick off app-compliance work
// (permission videos, later screenshots/preview videos) and the mobile + web UI
// can show progress while it runs. A job runs the capture layer in a goroutine
// and streams human-readable status the UI polls.

type studioJobState string

const (
	studioQueued    studioJobState = "queued"
	studioRunning   studioJobState = "running"
	studioCompleted studioJobState = "completed"
	studioFailed    studioJobState = "failed"
)

type studioJob struct {
	mu sync.Mutex

	ID         string
	Kind       string // "permission-video"
	Permission string
	State      studioJobState
	Phase      string   // current human phase, e.g. "booting redroid"
	LogLines   []string // chronological progress
	StartedAt  time.Time
	FinishedAt time.Time
	Error      string

	// results
	MP4Path           string
	CaptionedMP4Path  string
	JustificationPath string
	CaptionCount      int
	ShotCount         int
	ShotNames         []string
	Dir               string
}

func (j *studioJob) log(phase, line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if phase != "" {
		j.Phase = phase
	}
	ts := time.Now().Format("15:04:05")
	j.LogLines = append(j.LogLines, ts+"  "+line)
	if len(j.LogLines) > 200 {
		j.LogLines = j.LogLines[len(j.LogLines)-200:]
	}
}

func (j *studioJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := map[string]any{
		"id":         j.ID,
		"kind":       j.Kind,
		"permission": j.Permission,
		"state":      string(j.State),
		"phase":      j.Phase,
		"log":        append([]string(nil), j.LogLines...),
		"startedAt":  j.StartedAt.UnixMilli(),
	}
	if !j.FinishedAt.IsZero() {
		out["finishedAt"] = j.FinishedAt.UnixMilli()
		out["durationSec"] = int(j.FinishedAt.Sub(j.StartedAt).Seconds())
	}
	if j.Error != "" {
		out["error"] = j.Error
	}
	if j.State == studioCompleted {
		out["artifacts"] = map[string]any{
			"mp4":           j.MP4Path,
			"captionedMp4":  j.CaptionedMP4Path,
			"justification": j.JustificationPath,
			"captionCount":  j.CaptionCount,
			"shotCount":     j.ShotCount,
			"shots":         append([]string(nil), j.ShotNames...),
			"dir":           j.Dir,
		}
	}
	return out
}

type studioJobManager struct {
	mu   sync.Mutex
	jobs map[string]*studioJob
	seq  int
}

var studioJobs = &studioJobManager{jobs: map[string]*studioJob{}}

func (m *studioJobManager) get(id string) *studioJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

func (m *studioJobManager) list() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]map[string]any, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j.snapshot())
	}
	return out
}

func (m *studioJobManager) newJob(kind, permission string) *studioJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	id := fmt.Sprintf("studio-%d-%d", time.Now().Unix(), m.seq)
	j := &studioJob{ID: id, Kind: kind, Permission: permission, State: studioQueued, StartedAt: time.Now()}
	m.jobs[id] = j
	return j
}

// studioPermissionJobRequest is the start payload from HTTP / ops / UI.
type studioPermissionJobRequest struct {
	Permission  string `json:"permission"`
	Path        string `json:"path"`
	Manifest    string `json:"manifest"`
	App         string `json:"app"`
	What        string `json:"what"`
	APK         string `json:"apk"` // app artifact for the surface arch
	Package     string `json:"package"`
	Activity    string `json:"activity"`
	StartAction string `json:"startAction"`
	SSHHost     string `json:"sshHost"` // empty = local runner (managed cloud)
	SSHOpts     string `json:"sshOpts"`
	HostWorkDir string `json:"hostWorkDir"`
	Image       string `json:"image"`
	MaxSec      int    `json:"maxSec"`
}

// startPermissionVideo validates the request, creates a job, and runs the
// capture in a goroutine, returning the job immediately.
func (m *studioJobManager) startPermissionVideo(req studioPermissionJobRequest) (*studioJob, error) {
	if strings.TrimSpace(req.Permission) == "" {
		return nil, fmt.Errorf("permission required")
	}
	root := strings.TrimSpace(req.Path)
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	manifestPath := strings.TrimSpace(req.Manifest)
	if manifestPath == "" {
		manifestPath = findAndroidManifest(root)
	}
	if manifestPath == "" {
		return nil, fmt.Errorf("could not find AndroidManifest.xml under %s — pass manifest", root)
	}
	if strings.TrimSpace(req.APK) == "" || strings.TrimSpace(req.HostWorkDir) == "" {
		return nil, fmt.Errorf("apk and hostWorkDir are required to record")
	}
	pkg := strings.TrimSpace(req.Package)
	if pkg == "" {
		pkg = readAndroidPackage(manifestPath)
	}
	if pkg == "" {
		return nil, fmt.Errorf("could not determine package — pass package")
	}

	job := m.newJob("permission-video", studio.NormalizePermission(req.Permission))
	go m.runPermissionVideo(job, req, root, manifestPath, pkg)
	return job, nil
}

func (m *studioJobManager) runPermissionVideo(job *studioJob, req studioPermissionJobRequest, root, manifestPath, pkg string) {
	defer func() {
		if r := recover(); r != nil {
			job.mu.Lock()
			job.State, job.Error, job.FinishedAt = studioFailed, fmt.Sprintf("panic: %v", r), time.Now()
			job.mu.Unlock()
		}
	}()

	job.mu.Lock()
	job.State = studioRunning
	job.mu.Unlock()
	job.log("analyzing", "analyzing "+filepath.Base(manifestPath))

	facts, err := studio.AnalyzeAndroidManifest(manifestPath, req.Permission)
	if err != nil {
		m.fail(job, "analyze: "+err.Error())
		return
	}
	facts.TriggerHint = studio.FindTrigger(root, facts)
	appName := strings.TrimSpace(req.App)
	if appName == "" {
		appName = "The app"
	}

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".yaver", "studio", job.ID)
	_ = os.MkdirAll(dir, 0o755)
	job.mu.Lock()
	job.Dir = dir
	job.mu.Unlock()

	// runner: ssh (on-prem) or local (managed-cloud farm box)
	var runner studio.Runner = studio.LocalRunner{}
	if h := strings.TrimSpace(req.SSHHost); h != "" {
		runner = studio.SSHRunner{Host: h, Opts: strings.Fields(req.SSHOpts)}
	}
	job.log("provisioning", "capture on "+runner.Label())

	surface := &studio.RedroidSurface{
		R: runner, Image: req.Image, HostWorkDir: req.HostWorkDir,
		Log: func(line string) { job.log("", line) },
	}
	spec := studio.PermissionVideoSpec{
		App:          studio.App{Package: pkg, Activity: studioOrDefault(req.Activity, ".MainActivity")},
		ArtifactPath: req.APK,
		Facts:        facts,
		StartAction:  req.StartAction,
		MaxSec:       req.MaxSec,
	}

	ctx := context.Background()
	mp4, cues, j2, err := studio.CapturePermissionVideo(ctx, surface, spec, appName, req.What)

	// always write the prose
	jp := filepath.Join(dir, "justification.md")
	_ = os.WriteFile(jp, []byte(j2.Markdown(facts.Permission)), 0o644)
	job.mu.Lock()
	job.JustificationPath = jp
	job.mu.Unlock()

	if err != nil {
		m.fail(job, "capture: "+err.Error())
		return
	}

	mp4Path := filepath.Join(dir, "permission-demo.mp4")
	if werr := os.WriteFile(mp4Path, mp4, 0o644); werr != nil {
		m.fail(job, "write mp4: "+werr.Error())
		return
	}
	cuesJSON, _ := json.MarshalIndent(cues, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "captions.json"), cuesJSON, 0o644)
	job.log("captioning", fmt.Sprintf("recorded %d bytes, %d caption cues", len(mp4), len(cues)))

	job.mu.Lock()
	job.MP4Path, job.CaptionCount = mp4Path, len(cues)
	job.mu.Unlock()

	if capped, cerr := studio.CaptionMP4(ctx, mp4, cues, "", ""); cerr == nil {
		cp := filepath.Join(dir, "permission-demo-captioned.mp4")
		if os.WriteFile(cp, capped, 0o644) == nil {
			job.mu.Lock()
			job.CaptionedMP4Path = cp
			job.mu.Unlock()
		}
	} else {
		job.log("", "captioning skipped: "+cerr.Error())
	}

	job.mu.Lock()
	job.State, job.FinishedAt = studioCompleted, time.Now()
	job.mu.Unlock()
	job.log("done", "complete")
}

func (m *studioJobManager) fail(job *studioJob, msg string) {
	job.mu.Lock()
	job.State, job.Error, job.FinishedAt = studioFailed, msg, time.Now()
	job.mu.Unlock()
	job.log("failed", msg)
}

func studioOrDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// --- screenshots job ---

type studioScene struct {
	Name     string   `json:"name"`
	TapTexts []string `json:"tapTexts"` // visible texts to tap (in order) to reach the screen
}

type studioScreenshotsRequest struct {
	Platform    string        `json:"platform"` // "android" (redroid) | "ios" (simulator, macOS)
	Path        string        `json:"path"`
	APK         string        `json:"apk"`     // android: APK; ios: built .app bundle
	Package     string        `json:"package"` // android package / iOS bundle id
	Activity    string        `json:"activity"`
	Device      string        `json:"device"` // iOS simulator udid/name (empty = booted)
	SSHHost     string        `json:"sshHost"`
	SSHOpts     string        `json:"sshOpts"`
	HostWorkDir string        `json:"hostWorkDir"`
	Image       string        `json:"image"`
	Scenes      []studioScene `json:"scenes"`
	// optional auto-upload after capture
	Upload  bool   `json:"upload"`
	Locale  string `json:"locale"`
	Submit  bool   `json:"submit"`
	Version string `json:"version"`
}

func (m *studioJobManager) startScreenshots(req studioScreenshotsRequest) (*studioJob, error) {
	ios := strings.EqualFold(req.Platform, "ios")
	if strings.TrimSpace(req.APK) == "" {
		return nil, fmt.Errorf("artifact required (apk for android, .app bundle for ios)")
	}
	if !ios && strings.TrimSpace(req.HostWorkDir) == "" {
		return nil, fmt.Errorf("hostWorkDir is required for android (redroid /data mount)")
	}
	pkg := strings.TrimSpace(req.Package)
	if pkg == "" && !ios {
		root := studioOrDefault(req.Path, ".")
		if mp := findAndroidManifest(root); mp != "" {
			pkg = readAndroidPackage(mp)
		}
	}
	if pkg == "" {
		return nil, fmt.Errorf("could not determine app id — pass package (android package / iOS bundle id)")
	}
	job := m.newJob("screenshots", "")
	go m.runScreenshots(job, req, pkg)
	return job, nil
}

func (m *studioJobManager) runScreenshots(job *studioJob, req studioScreenshotsRequest, pkg string) {
	defer func() {
		if r := recover(); r != nil {
			m.fail(job, fmt.Sprintf("panic: %v", r))
		}
	}()
	job.mu.Lock()
	job.State = studioRunning
	job.mu.Unlock()

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".yaver", "studio", job.ID)
	_ = os.MkdirAll(dir, 0o755)
	job.mu.Lock()
	job.Dir = dir
	job.mu.Unlock()

	var runner studio.Runner = studio.LocalRunner{}
	if h := strings.TrimSpace(req.SSHHost); h != "" {
		runner = studio.SSHRunner{Host: h, Opts: strings.Fields(req.SSHOpts)}
	}
	ios := strings.EqualFold(req.Platform, "ios")
	job.log("provisioning", fmt.Sprintf("screenshots on %s (%s)", runner.Label(), studioOrDefault(req.Platform, "android")))

	// iOS uses the Simulator (macOS-only) via the same Driver interface; Android
	// uses redroid. One job code path, two surfaces.
	var surface studio.CaptureSurface
	if ios {
		surface = &studio.IOSSimSurface{
			R: runner, Device: req.Device,
			Log: func(line string) { job.log("", line) },
		}
	} else {
		surface = &studio.RedroidSurface{
			R: runner, Image: req.Image, HostWorkDir: req.HostWorkDir,
			Log: func(line string) { job.log("", line) },
		}
	}

	scenes := make([]studio.ScreenshotScene, 0, len(req.Scenes))
	for _, sc := range req.Scenes {
		var steps []studio.Step
		for _, txt := range sc.TapTexts {
			t := txt
			steps = append(steps, studio.Step{
				Run:     func(ctx context.Context, d studio.Driver) error { return d.TapText(ctx, t) },
				HoldSec: 2,
			})
		}
		scenes = append(scenes, studio.ScreenshotScene{Name: sc.Name, Steps: steps})
	}

	spec := studio.ScreenshotSpec{
		App:          studio.App{Package: pkg, Activity: studioOrDefault(req.Activity, ".MainActivity")},
		ArtifactPath: req.APK,
		Scenes:       scenes,
	}

	shots, err := studio.CaptureScreenshots(context.Background(), surface, spec)
	// write whatever we got
	var names []string
	for _, s := range shots {
		p := filepath.Join(dir, s.Name+".png")
		if os.WriteFile(p, s.PNG, 0o644) == nil {
			names = append(names, s.Name+".png")
		}
	}
	job.mu.Lock()
	job.ShotCount, job.ShotNames = len(names), names
	job.mu.Unlock()
	job.log("captured", fmt.Sprintf("%d screenshots", len(names)))

	if err != nil {
		m.fail(job, "screenshots: "+err.Error())
		return
	}

	// optional auto-upload to the store
	if req.Upload && len(names) > 0 {
		platform := studioOrDefault(req.Platform, "android")
		job.log("uploading", "uploading "+fmt.Sprint(len(names))+" screenshots to "+platform)
		res, uerr := studioUploadScreenshots(platform, dir, pkg, req.Locale, req.Version, req.Submit)
		if uerr != nil {
			// capture still succeeded — surface upload failure without failing the job
			job.log("", "upload failed: "+uerr.Error())
		} else {
			job.log("", fmt.Sprintf("uploaded: %v", res))
		}
	}

	job.mu.Lock()
	job.State, job.FinishedAt = studioCompleted, time.Now()
	job.mu.Unlock()
	job.log("done", "complete")
}
