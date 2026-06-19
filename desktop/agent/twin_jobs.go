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

	"github.com/chromedp/chromedp"
	"github.com/yaver-io/agent/studio"
	"github.com/yaver-io/agent/testkit"
)

// twin_jobs.go — generic Remote Dev Twin jobs. A twin job runs and records a
// target app on the remote development surface instead of on the controller
// phone. Surfaces are intentionally broader than Store Studio: Android redroid,
// web Playwright, web ChromeDP, and later iOS Simulator / desktop.

type twinJobState string

const (
	twinQueued    twinJobState = "queued"
	twinRunning   twinJobState = "running"
	twinCompleted twinJobState = "completed"
	twinFailed    twinJobState = "failed"
)

type twinJob struct {
	mu sync.Mutex

	ID         string
	Surface    string
	Mode       string
	State      twinJobState
	Phase      string
	LogLines   []string
	StartedAt  time.Time
	FinishedAt time.Time
	Error      string

	Dir         string
	VideoPath   string
	TracePath   string
	FramesDir   string
	LogPath     string
	CrashPath   string
	Screenshots []string
	Metadata    map[string]any
}

func (j *twinJob) log(phase, line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if phase != "" {
		j.Phase = phase
	}
	j.LogLines = append(j.LogLines, time.Now().Format("15:04:05")+"  "+line)
	if len(j.LogLines) > 200 {
		j.LogLines = j.LogLines[len(j.LogLines)-200:]
	}
}

func (j *twinJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := map[string]any{
		"id":        j.ID,
		"surface":   j.Surface,
		"mode":      j.Mode,
		"state":     string(j.State),
		"phase":     j.Phase,
		"log":       append([]string(nil), j.LogLines...),
		"startedAt": j.StartedAt.UnixMilli(),
	}
	if !j.FinishedAt.IsZero() {
		out["finishedAt"] = j.FinishedAt.UnixMilli()
		out["durationSec"] = int(j.FinishedAt.Sub(j.StartedAt).Seconds())
	}
	if j.Error != "" {
		out["error"] = j.Error
	}
	if j.State == twinCompleted {
		out["artifacts"] = map[string]any{
			"dir":         j.Dir,
			"video":       j.VideoPath,
			"trace":       j.TracePath,
			"frames":      j.FramesDir,
			"logs":        j.LogPath,
			"crash":       j.CrashPath,
			"screenshots": append([]string(nil), j.Screenshots...),
			"metadata":    j.Metadata,
		}
	}
	return out
}

type twinJobManager struct {
	mu   sync.Mutex
	jobs map[string]*twinJob
	seq  int
}

var twinJobs = &twinJobManager{jobs: map[string]*twinJob{}}

func (m *twinJobManager) get(id string) *twinJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

func (m *twinJobManager) list() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]map[string]any, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j.snapshot())
	}
	return out
}

func (m *twinJobManager) newJob(surface, mode string) *twinJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	id := fmt.Sprintf("twin-%d-%d", time.Now().Unix(), m.seq)
	j := &twinJob{ID: id, Surface: surface, Mode: mode, State: twinQueued, StartedAt: time.Now(), Metadata: map[string]any{}}
	m.jobs[id] = j
	return j
}

type twinStep struct {
	Action     string `json:"action"`
	URL        string `json:"url"`
	Selector   string `json:"selector"`
	Text       string `json:"text"`
	Key        string `json:"key"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	HoldSec    int    `json:"holdSec"`
	TimeoutSec int    `json:"timeoutSec"`
	Name       string `json:"name"`
}

type twinJobRequest struct {
	Surface string     `json:"surface"` // android-redroid | web-playwright | web-chromedp
	Mode    string     `json:"mode"`    // manual | scripted | sdk-declared
	Steps   []twinStep `json:"steps"`
	Record  *bool      `json:"record"`
	MaxSec  int        `json:"maxSec"`
	Headful bool       `json:"headful"`
	Trace   bool       `json:"trace"`

	// Runner placement. Empty SSHHost means this agent host.
	SSHHost string `json:"sshHost"`
	SSHOpts string `json:"sshOpts"`
	WorkDir string `json:"workDir"` // remote work dir; defaults to ~/.yaver/twin/<job>

	// Android.
	APK         string `json:"apk"`
	Package     string `json:"package"`
	Activity    string `json:"activity"`
	HostWorkDir string `json:"hostWorkDir"`
	Image       string `json:"image"`
	KeepSurface bool   `json:"keepSurface"`

	// Web.
	URL                string `json:"url"`
	Browser            string `json:"browser"`
	RemoteDebuggingURL string `json:"remoteDebuggingUrl"`
	ViewportWidth      int    `json:"viewportWidth"`
	ViewportHeight     int    `json:"viewportHeight"`
}

func (m *twinJobManager) start(req twinJobRequest) (*twinJob, error) {
	req.Surface = strings.TrimSpace(req.Surface)
	if req.Surface == "" {
		return nil, fmt.Errorf("surface required")
	}
	if req.Mode == "" {
		req.Mode = "scripted"
	}
	job := m.newJob(req.Surface, req.Mode)
	go m.run(job, req)
	return job, nil
}

func (m *twinJobManager) run(job *twinJob, req twinJobRequest) {
	defer func() {
		if r := recover(); r != nil {
			m.fail(job, fmt.Sprintf("panic: %v", r))
		}
	}()

	job.mu.Lock()
	job.State = twinRunning
	job.mu.Unlock()

	home, _ := os.UserHomeDir()
	localDir := filepath.Join(home, ".yaver", "twin", job.ID)
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		m.fail(job, "mkdir artifacts: "+err.Error())
		return
	}
	job.mu.Lock()
	job.Dir = localDir
	job.mu.Unlock()

	switch strings.ToLower(req.Surface) {
	case "android-redroid", "redroid":
		m.runAndroidRedroid(job, req, localDir)
	case "web-playwright", "playwright":
		m.runWebPlaywright(job, req, localDir)
	case "web-chromedp", "chromedp", "web":
		m.runWebChromeDP(job, req, localDir)
	default:
		m.fail(job, "unsupported surface "+req.Surface)
	}
}

func twinRunner(req twinJobRequest) studio.Runner {
	if h := strings.TrimSpace(req.SSHHost); h != "" {
		return studio.SSHRunner{Host: h, Opts: strings.Fields(req.SSHOpts)}
	}
	return studio.LocalRunner{}
}

func (m *twinJobManager) runAndroidRedroid(job *twinJob, req twinJobRequest, localDir string) {
	if strings.TrimSpace(req.HostWorkDir) == "" {
		m.fail(job, "hostWorkDir required for android-redroid")
		return
	}
	if strings.TrimSpace(req.Package) == "" {
		m.fail(job, "package required for android-redroid")
		return
	}
	runner := twinRunner(req)
	job.log("provisioning", "android-redroid on "+runner.Label())
	surface := &studio.RedroidSurface{
		R: runner, Image: req.Image, HostWorkDir: req.HostWorkDir,
		Log: func(line string) { job.log("", line) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), twinTimeout(req.MaxSec, 8*time.Minute))
	defer cancel()
	if err := surface.Provision(ctx); err != nil {
		m.fail(job, "provision: "+err.Error())
		return
	}
	if !req.KeepSurface {
		defer surface.Teardown(context.WithoutCancel(ctx)) //nolint:errcheck
	}
	if strings.TrimSpace(req.APK) != "" {
		job.log("installing", filepath.Base(req.APK))
		if err := surface.Install(ctx, req.APK); err != nil {
			m.fail(job, "install: "+err.Error())
			return
		}
	}
	driver := surface.Driver()
	app := studio.App{Package: req.Package, Activity: req.Activity}
	job.log("launching", req.Package)
	if err := driver.Launch(ctx, app); err != nil {
		m.fail(job, "launch: "+err.Error())
		return
	}
	record := req.Record == nil || *req.Record
	if record {
		job.log("recording", "screenrecord start")
		if err := driver.RecordStart(ctx, req.MaxSec); err != nil {
			m.fail(job, "record start: "+err.Error())
			return
		}
	}
	if err := runTwinAndroidSteps(ctx, driver, app, req.Steps, job, localDir); err != nil {
		job.log("step-error", err.Error())
	}
	if record {
		mp4, err := driver.RecordStop(ctx)
		if err != nil {
			m.fail(job, "record stop: "+err.Error())
			return
		}
		p := filepath.Join(localDir, "remote-twin-android.mp4")
		if err := os.WriteFile(p, mp4, 0o644); err != nil {
			m.fail(job, "write video: "+err.Error())
			return
		}
		job.mu.Lock()
		job.VideoPath = p
		job.mu.Unlock()
	}
	if txt, err := driver.NotificationText(ctx); err == nil && strings.TrimSpace(txt) != "" {
		p := filepath.Join(localDir, "notifications.txt")
		_ = os.WriteFile(p, []byte(txt), 0o644)
		job.mu.Lock()
		job.Metadata["notifications"] = p
		job.mu.Unlock()
	}
	if lr, ok := driver.(studio.LogReader); ok {
		if logs, err := lr.Logcat(ctx, 800); err == nil && strings.TrimSpace(logs) != "" {
			p := filepath.Join(localDir, "logcat.txt")
			_ = os.WriteFile(p, []byte(logs), 0o644)
			job.mu.Lock()
			job.LogPath = p
			if crash := extractAndroidCrash(logs); crash != "" {
				cp := filepath.Join(localDir, "crash.txt")
				_ = os.WriteFile(cp, []byte(crash), 0o644)
				job.CrashPath = cp
			}
			job.mu.Unlock()
		}
	}
	m.complete(job)
}

func runTwinAndroidSteps(ctx context.Context, d studio.Driver, app studio.App, steps []twinStep, job *twinJob, localDir string) error {
	for i, st := range steps {
		a := strings.ToLower(strings.TrimSpace(st.Action))
		if a == "" {
			continue
		}
		job.log("step", fmt.Sprintf("%d. %s", i+1, a))
		var err error
		switch a {
		case "launch":
			err = d.Launch(ctx, app)
		case "tap":
			err = d.Tap(ctx, st.X, st.Y)
		case "taptext", "tap_text":
			err = d.TapText(ctx, st.Text)
		case "type", "fill":
			err = d.Type(ctx, st.Text)
		case "key", "press":
			err = d.Key(ctx, st.Key)
		case "back":
			err = d.Back(ctx)
		case "home":
			err = d.Home(ctx)
		case "waittext", "wait_text":
			err = d.WaitText(ctx, st.Text, maxInt(st.TimeoutSec, 15))
		case "expand_notifications":
			err = d.ExpandNotifications(ctx)
		case "collapse_notifications":
			err = d.CollapseNotifications(ctx)
		case "screenshot":
			var png []byte
			png, err = d.Screenshot(ctx)
			if err == nil {
				name := st.Name
				if name == "" {
					name = fmt.Sprintf("step-%02d.png", i+1)
				}
				p := filepath.Join(localDir, name)
				if werr := os.WriteFile(p, png, 0o644); werr == nil {
					job.mu.Lock()
					job.Screenshots = append(job.Screenshots, p)
					job.mu.Unlock()
				}
			}
		}
		if st.HoldSec > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(st.HoldSec) * time.Second):
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *twinJobManager) runWebPlaywright(job *twinJob, req twinJobRequest, localDir string) {
	if strings.TrimSpace(req.URL) == "" {
		m.fail(job, "url required for web-playwright")
		return
	}
	runner := twinRunner(req)
	remoteDir := strings.TrimSpace(req.WorkDir)
	if remoteDir == "" {
		if strings.TrimSpace(req.SSHHost) != "" {
			remoteDir = ".yaver/twin/" + job.ID
		} else {
			remoteDir = localDir
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), twinTimeout(req.MaxSec, 6*time.Minute))
	defer cancel()
	job.log("preparing", "web-playwright on "+runner.Label())
	if _, err := runner.Exec(ctx, "mkdir -p "+shellQuote(remoteDir)); err != nil {
		m.fail(job, "mkdir remote: "+err.Error())
		return
	}
	script := buildTwinPlaywrightScript(req, remoteDir)
	localScript := filepath.Join(localDir, "twin-playwright.mjs")
	if err := os.WriteFile(localScript, []byte(script), 0o644); err != nil {
		m.fail(job, "write script: "+err.Error())
		return
	}
	remoteScript := filepath.Join(remoteDir, "twin-playwright.mjs")
	if err := runner.PutFile(ctx, localScript, remoteScript); err != nil {
		m.fail(job, "put script: "+err.Error())
		return
	}
	job.log("running", "node playwright script")
	out, err := runner.Exec(ctx, "node "+shellQuote(remoteScript))
	_ = os.WriteFile(filepath.Join(localDir, "playwright.log"), out, 0o644)
	if err != nil {
		m.fail(job, fmt.Sprintf("playwright: %v: %s", err, lastNLines(string(out), 8)))
		return
	}
	logPath := filepath.Join(localDir, "playwright.log")
	job.mu.Lock()
	job.LogPath = logPath
	if crash := extractWebCrash(string(out)); crash != "" {
		cp := filepath.Join(localDir, "crash.txt")
		_ = os.WriteFile(cp, []byte(crash), 0o644)
		job.CrashPath = cp
	}
	job.mu.Unlock()
	art := parseTwinArtifactLine(string(out))
	if v := strings.TrimSpace(art["video"]); v != "" {
		localVideo := filepath.Join(localDir, filepath.Base(v))
		if err := runner.GetFile(ctx, v, localVideo); err == nil {
			job.mu.Lock()
			job.VideoPath = localVideo
			job.mu.Unlock()
		} else {
			job.log("", "video retrieve failed: "+err.Error())
		}
	}
	if t := strings.TrimSpace(art["trace"]); t != "" {
		localTrace := filepath.Join(localDir, filepath.Base(t))
		if err := runner.GetFile(ctx, t, localTrace); err == nil {
			job.mu.Lock()
			job.TracePath = localTrace
			job.mu.Unlock()
		}
	}
	m.complete(job)
}

func buildTwinPlaywrightScript(req twinJobRequest, dir string) string {
	w, h := req.ViewportWidth, req.ViewportHeight
	if w <= 0 {
		w = 1280
	}
	if h <= 0 {
		h = 800
	}
	browser := "chromium"
	if req.Browser == "firefox" || req.Browser == "webkit" {
		browser = req.Browser
	}
	record := req.Record == nil || *req.Record
	var b strings.Builder
	fmt.Fprintf(&b, "import { %s } from 'playwright';\n", browser)
	b.WriteString("const outDir = " + jsString(dir) + ";\n")
	b.WriteString("const browser = await " + browser + ".launch({ headless: " + fmt.Sprintf("%t", !req.Headful) + " });\n")
	b.WriteString(fmt.Sprintf("const ctx = await browser.newContext({ viewport: { width: %d, height: %d }", w, h))
	if record {
		b.WriteString(", recordVideo: { dir: outDir, size: { width: " + fmt.Sprint(w) + ", height: " + fmt.Sprint(h) + " } }")
	}
	b.WriteString(" });\n")
	if req.Trace {
		b.WriteString("await ctx.tracing.start({ screenshots: true, snapshots: true, sources: true });\n")
	}
	b.WriteString("const page = await ctx.newPage();\n")
	b.WriteString("let videoPath = '';\n")
	b.WriteString("page.on('console', msg => console.log('@@TWIN_CONSOLE '+msg.type()+': '+msg.text()));\n")
	b.WriteString("page.on('pageerror', err => console.log('@@TWIN_PAGE_ERROR '+(err && err.stack || err)));\n")
	b.WriteString("page.on('crash', () => console.log('@@TWIN_PAGE_CRASH page crashed'));\n")
	b.WriteString("try {\n")
	b.WriteString("await page.goto(" + jsString(req.URL) + ", { waitUntil: 'networkidle' });\n")
	for _, st := range req.Steps {
		switch strings.ToLower(strings.TrimSpace(st.Action)) {
		case "goto":
			b.WriteString("await page.goto(" + jsString(st.URL) + ", { waitUntil: 'networkidle' });\n")
		case "click":
			if st.Selector != "" {
				b.WriteString("await page.click(" + jsString(st.Selector) + ");\n")
			} else if st.Text != "" {
				b.WriteString("await page.getByText(" + jsString(st.Text) + ").click();\n")
			}
		case "fill", "type":
			b.WriteString("await page.fill(" + jsString(st.Selector) + ", " + jsString(st.Text) + ");\n")
		case "press", "key":
			b.WriteString("await page.keyboard.press(" + jsString(st.Key) + ");\n")
		case "waitforselector", "wait_selector":
			b.WriteString("await page.waitForSelector(" + jsString(st.Selector) + ");\n")
		case "waitfortext", "wait_text":
			b.WriteString("await page.getByText(" + jsString(st.Text) + ").waitFor();\n")
		case "screenshot":
			name := st.Name
			if name == "" {
				name = "screenshot.png"
			}
			b.WriteString("await page.screenshot({ path: outDir + '/' + " + jsString(name) + " });\n")
		}
		if st.HoldSec > 0 {
			b.WriteString(fmt.Sprintf("await page.waitForTimeout(%d);\n", st.HoldSec*1000))
		}
	}
	b.WriteString("if (page.video()) videoPath = await page.video().path().catch(()=> '');\n")
	b.WriteString("} finally {\n")
	if req.Trace {
		b.WriteString("await ctx.tracing.stop({ path: outDir + '/trace.zip' }).catch(()=>{});\n")
	}
	b.WriteString("await ctx.close(); await browser.close();\n")
	b.WriteString("}\n")
	b.WriteString("console.log('@@TWIN_ARTIFACT '+JSON.stringify({ video: videoPath, trace: " + jsString(filepath.Join(dir, "trace.zip")) + " }));\n")
	return b.String()
}

func (m *twinJobManager) runWebChromeDP(job *twinJob, req twinJobRequest, localDir string) {
	if strings.TrimSpace(req.URL) == "" {
		m.fail(job, "url required for web-chromedp")
		return
	}
	base := context.Background()
	var allocCancel context.CancelFunc
	if u := strings.TrimSpace(req.RemoteDebuggingURL); u != "" {
		base, allocCancel = chromedp.NewRemoteAllocator(base, u)
		job.log("attaching", "web-chromedp "+u)
	} else {
		base, allocCancel = chromedp.NewExecAllocator(base, chromedp.DefaultExecAllocatorOptions[:]...)
		job.log("running", "web-chromedp local browser")
	}
	defer allocCancel()
	ctx, cancel := chromedp.NewContext(base)
	defer cancel()
	ctx, timeoutCancel := context.WithTimeout(ctx, twinTimeout(req.MaxSec, 5*time.Minute))
	defer timeoutCancel()
	ring := testkit.NewFrameRing(180)
	var webEventsMu sync.Mutex
	var webEvents []string
	chromedp.ListenTarget(ctx, func(ev any) {
		if ev == nil {
			return
		}
		s := fmt.Sprintf("%T %v", ev, ev)
		if strings.Contains(s, "Exception") || strings.Contains(s, "Console") || strings.Contains(s, "Crashed") {
			webEventsMu.Lock()
			webEvents = append(webEvents, s)
			webEventsMu.Unlock()
		}
	})
	if req.Record == nil || *req.Record {
		if stop, err := testkit.StartScreencast(ctx, ring); err == nil {
			defer stop()
		} else {
			job.log("", "screencast unavailable: "+err.Error())
		}
	}
	actions := []chromedp.Action{chromedp.Navigate(req.URL)}
	for _, st := range req.Steps {
		switch strings.ToLower(strings.TrimSpace(st.Action)) {
		case "goto":
			actions = append(actions, chromedp.Navigate(st.URL))
		case "click":
			if st.Selector != "" {
				actions = append(actions, chromedp.Click(st.Selector, chromedp.ByQuery))
			}
		case "fill", "type":
			if st.Selector != "" {
				actions = append(actions, chromedp.SetValue(st.Selector, st.Text, chromedp.ByQuery))
			}
		case "waitforselector", "wait_selector":
			if st.Selector != "" {
				actions = append(actions, chromedp.WaitVisible(st.Selector, chromedp.ByQuery))
			}
		case "waitfortext", "wait_text":
			if st.Text != "" {
				actions = append(actions, chromedp.WaitReady("body", chromedp.ByQuery))
			}
		}
		if st.HoldSec > 0 {
			actions = append(actions, chromedp.Sleep(time.Duration(st.HoldSec)*time.Second))
		}
	}
	if err := chromedp.Run(ctx, actions...); err != nil {
		m.fail(job, "chromedp: "+err.Error())
		return
	}
	if frames, err := testkit.FlushFrames(localDir, "chromedp", ring); err == nil && frames != "" {
		job.mu.Lock()
		job.FramesDir = frames
		job.mu.Unlock()
	}
	webEventsMu.Lock()
	if len(webEvents) > 0 {
		logs := strings.Join(webEvents, "\n\n")
		p := filepath.Join(localDir, "chromedp-events.txt")
		_ = os.WriteFile(p, []byte(logs), 0o644)
		job.mu.Lock()
		job.LogPath = p
		if crash := extractWebCrash(logs); crash != "" {
			cp := filepath.Join(localDir, "crash.txt")
			_ = os.WriteFile(cp, []byte(crash), 0o644)
			job.CrashPath = cp
		}
		job.mu.Unlock()
	}
	webEventsMu.Unlock()
	m.complete(job)
}

func (m *twinJobManager) fail(job *twinJob, msg string) {
	job.mu.Lock()
	if job.State == twinCompleted {
		job.mu.Unlock()
		return
	}
	job.State, job.Error, job.FinishedAt = twinFailed, msg, time.Now()
	job.mu.Unlock()
	job.log("failed", msg)
}

func (m *twinJobManager) complete(job *twinJob) {
	job.mu.Lock()
	job.State, job.FinishedAt = twinCompleted, time.Now()
	job.mu.Unlock()
	job.log("done", "complete")
}

func twinTimeout(maxSec int, def time.Duration) time.Duration {
	if maxSec > 0 {
		return time.Duration(maxSec+90) * time.Second
	}
	return def
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func parseTwinArtifactLine(out string) map[string]string {
	res := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "@@TWIN_ARTIFACT ") {
			continue
		}
		_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "@@TWIN_ARTIFACT ")), &res)
	}
	return res
}

func extractAndroidCrash(logs string) string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(line, "FATAL EXCEPTION") ||
			strings.Contains(line, "AndroidRuntime") ||
			strings.Contains(line, "ReactNativeJS") ||
			strings.Contains(lower, "fatal signal") ||
			strings.Contains(lower, "process crashed") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func extractWebCrash(logs string) string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(line, "@@TWIN_PAGE_ERROR") ||
			strings.Contains(line, "@@TWIN_PAGE_CRASH") ||
			strings.Contains(lower, "uncaught") ||
			strings.Contains(lower, "exception") ||
			strings.Contains(lower, "page crashed") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
