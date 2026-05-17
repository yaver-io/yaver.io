package main

// runner.go — unified runner abstraction (Phase 1 foundation, see RUNNER_DEV.md).
//
// Replaces the per-vertical SaaS runners (GitHub Actions self-hosted,
// EAS Build, e2b sandbox, Modal, Checkly, Cronitor, Devin, ...) with
// one self-hosted primitive that schedules a Job onto a Pool of
// user-owned machines, persists the Run, fires Notify, and exposes a
// uniform HTTP/MCP/CLI surface.
//
// Phase 1 ships only the read-write skeleton + shell job execution.
// Future phases attach docker / playwright / agent / gpu job kinds,
// multi-machine claim from a shared queue, and the GHA self-hosted
// runner adapter — all on top of this core without breaking the API.
//
// On-disk shape (mirrors deploy_history.go intentionally so operators
// only learn one layout):
//
//   ~/.yaver/runner/
//     jobs.json                 — declarative job specs
//     runs/<id>/output.log      — full stdout+stderr per run
//     runs/<id>/meta.json       — run metadata (also held in memory)
//     runs/<id>/artifacts/*     — declared outputs (size-capped)
//
// Privacy contract:
//   - The on-disk run output never crosses the Convex boundary.
//   - convex_privacy_test.go's forbiddenKeys gains runner_output /
//     runner_log / runner_workdir so a future syncer that touches
//     this path trips the test.
//   - Cross-machine sync is metadata-only (id, status, durationMs,
//     exitCode, errorClass).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// runnerOutputTailCap is how many bytes of subprocess output we keep
// in memory per run. 8 KB matches deploy_history's tail cap so the
// UIs can render either source uniformly.
const runnerOutputTailCap = 8 * 1024

// runnerDiskQuotaBytes is the soft cap on ~/.yaver/runner/runs/.
// Oldest run directories are evicted on Finish() until usage drops
// back under the cap.
const runnerDiskQuotaBytes = 500 * 1024 * 1024

// RunnerJobKind enumerates the executor types a job spec can pick.
// Phase 1 supports `shell` only; the rest are reserved so payloads
// authored against future versions of the agent fail with a clean
// "kind not yet supported" rather than parse errors.
type RunnerJobKind string

const (
	RunnerJobShell      RunnerJobKind = "shell"
	RunnerJobDocker     RunnerJobKind = "docker"     // Phase 2
	RunnerJobWorkflow   RunnerJobKind = "workflow"   // Phase 4
	RunnerJobAgent      RunnerJobKind = "agent"      // Phase 2
	RunnerJobPlaywright RunnerJobKind = "playwright" // Phase 5
	RunnerJobGPU        RunnerJobKind = "gpu"        // Phase 5
)

// RunnerScheduleKind enumerates the trigger types a job may carry.
// Phase 1 ships `manual` (POST /runner/jobs/{name}/trigger) and
// records `cron` / `interval` for downstream wiring without firing
// them from this file — the existing scheduler.go already has cron.
type RunnerScheduleKind string

const (
	RunnerScheduleManual   RunnerScheduleKind = "manual"
	RunnerScheduleCron     RunnerScheduleKind = "cron"
	RunnerScheduleInterval RunnerScheduleKind = "interval"
	RunnerScheduleWebhook  RunnerScheduleKind = "webhook"
)

// RunnerNotifyKind selects what happens after a run finishes.
// Wired in later phases — Phase 1 records the spec only.
type RunnerNotifyKind string

const (
	RunnerNotifyNone    RunnerNotifyKind = "none"
	RunnerNotifyMobile  RunnerNotifyKind = "mobile_push"
	RunnerNotifyWebhook RunnerNotifyKind = "webhook"
	RunnerNotifyEmail   RunnerNotifyKind = "email"
)

// RunnerJob is the declarative spec the user submits via POST
// /runner/jobs. Stored verbatim in jobs.json and rendered back on
// list. Forward-compatible: unknown JobKinds are persisted but
// refuse to execute until the agent learns them.
type RunnerJob struct {
	Name string        `json:"name"`
	Kind RunnerJobKind `json:"kind"`

	// Pool is the capability expression used to pick a machine.
	// "any" matches every agent. Phase 1 only honours single
	// labels (no AND/OR); a heartbeat-tagged capability list
	// suffices for self-hosted single-machine usage.
	Pool string `json:"pool,omitempty"`

	// Project is the workspace app slug this job belongs to.
	// Used for vault scoping + guest project filtering. Empty =
	// no project; vault env falls back to globals only.
	Project string `json:"project,omitempty"`

	// Shell job fields.
	Command string            `json:"command,omitempty"`
	WorkDir string            `json:"workDir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// Schedule.
	Schedule RunnerSchedule `json:"schedule,omitempty"`

	// Notify pipeline (records-only in Phase 1).
	Notify []RunnerNotifySpec `json:"notify,omitempty"`

	// Concurrency policy: skip / queue / replace. Default skip.
	Concurrency string `json:"concurrency,omitempty"`

	// TimeoutSec is a hard subprocess kill. 0 = no timeout.
	TimeoutSec int `json:"timeoutSec,omitempty"`

	// CreatedAt / UpdatedAt — unix millis. Stamped by the store.
	CreatedAt int64 `json:"createdAt,omitempty"`
	UpdatedAt int64 `json:"updatedAt,omitempty"`

	// Paused: when true the scheduler skips this job. Manual triggers
	// still work — pause is "stop the cron, keep the entry so I can
	// debug it."
	Paused bool `json:"paused,omitempty"`
}

// RunnerSchedule is the trigger declaration. Phase 1 reads but does
// not fire these; integrate with scheduler.go in Phase 3.
type RunnerSchedule struct {
	Kind          RunnerScheduleKind `json:"kind,omitempty"`
	Cron          string             `json:"cron,omitempty"`
	IntervalSec   int                `json:"intervalSec,omitempty"`
	WebhookSecret string             `json:"webhookSecret,omitempty"`
}

// RunnerNotifySpec is one item of the notify pipeline.
type RunnerNotifySpec struct {
	Kind   RunnerNotifyKind `json:"kind"`
	On     string           `json:"on,omitempty"`     // "fail" | "success" | "always" — default "fail"
	Target string           `json:"target,omitempty"` // webhook URL / email
}

// RunnerRun is one historical execution of a RunnerJob. Same
// JSON-friendly flat shape as DeployRun so the mobile + web UIs can
// render either with a shared component.
type RunnerRun struct {
	ID          string        `json:"id"`
	JobName     string        `json:"jobName"`
	Kind        RunnerJobKind `json:"kind"`
	Pool        string        `json:"pool,omitempty"`
	Project     string        `json:"project,omitempty"`
	StartedAt   int64         `json:"startedAt"` // unix millis
	FinishedAt  int64         `json:"finishedAt,omitempty"`
	DurationMs  int64         `json:"durationMs,omitempty"`
	ExitCode    int           `json:"exitCode"`
	OK          bool          `json:"ok,omitempty"`
	InProgress  bool          `json:"inProgress,omitempty"`
	OutputTail  string        `json:"outputTail,omitempty"` // last ~8KB
	TimedOut    bool          `json:"timedOut,omitempty"`
	TriggeredBy string        `json:"triggeredBy,omitempty"` // "owner" | guestUserID | "schedule" | "webhook"
	IsGuest     bool          `json:"isGuest,omitempty"`
	LogPath     string        `json:"logPath,omitempty"` // server-side only; stripped before guest replies
	LogBytes    int64         `json:"logBytes,omitempty"`

	outputBuf []byte
	logFile   *os.File
}

// RunnerStore is the persistent home for jobs and run history. Live
// runs are kept in memory; finished runs are flushed to
// runs/<id>/meta.json so they survive an agent restart.
type RunnerStore struct {
	mu      sync.Mutex
	jobs    map[string]*RunnerJob
	runs    []*RunnerRun
	byID    map[string]*RunnerRun
	maxRuns int
	root    string // ~/.yaver/runner — empty disables disk persistence
	jobPath string // root/jobs.json
	runRoot string // root/runs
}

// NewRunnerStore opens or creates the runner store. maxRuns caps the
// in-memory ring buffer — older runs are evicted (along with their
// on-disk dir) when the buffer overflows.
func NewRunnerStore(maxRuns int) *RunnerStore {
	if maxRuns <= 0 {
		maxRuns = 500
	}
	s := &RunnerStore{
		jobs:    map[string]*RunnerJob{},
		runs:    make([]*RunnerRun, 0, maxRuns),
		byID:    map[string]*RunnerRun{},
		maxRuns: maxRuns,
	}
	if dir, err := ConfigDir(); err == nil {
		s.root = filepath.Join(dir, "runner")
		s.jobPath = filepath.Join(s.root, "jobs.json")
		s.runRoot = filepath.Join(s.root, "runs")
		if err := os.MkdirAll(s.runRoot, 0700); err != nil {
			log.Printf("[runner] mkdir %s failed: %v (disk persistence disabled)", s.runRoot, err)
			s.root = ""
			s.runRoot = ""
		}
	}
	s.loadJobs()
	s.loadRuns()
	return s
}

// loadJobs reads jobs.json into memory. Missing file is fine — empty
// store is the "no jobs registered yet" state.
func (s *RunnerStore) loadJobs() {
	if s.jobPath == "" {
		return
	}
	data, err := os.ReadFile(s.jobPath)
	if err != nil {
		return
	}
	var jobs map[string]*RunnerJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		log.Printf("[runner] failed to parse jobs.json: %v — starting empty", err)
		return
	}
	s.jobs = jobs
}

// saveJobsLocked persists jobs.json. Caller holds s.mu.
func (s *RunnerStore) saveJobsLocked() {
	if s.jobPath == "" {
		return
	}
	data, err := json.MarshalIndent(s.jobs, "", "  ")
	if err != nil {
		return
	}
	tmp := s.jobPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		log.Printf("[runner] write %s failed: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, s.jobPath); err != nil {
		log.Printf("[runner] rename %s -> %s failed: %v", tmp, s.jobPath, err)
	}
}

// loadRuns rebuilds the in-memory ring buffer from any meta.json
// files the previous agent left behind. Bounded read — we only keep
// the most recent maxRuns even if the disk has more (gcLogs cleans
// the rest opportunistically on Finish).
func (s *RunnerStore) loadRuns() {
	if s.runRoot == "" {
		return
	}
	entries, err := os.ReadDir(s.runRoot)
	if err != nil {
		return
	}
	type loaded struct {
		run *RunnerRun
		mt  time.Time
	}
	var all []loaded
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(s.runRoot, e.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var r RunnerRun
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		// In-progress flag is stale across restarts — the subprocess
		// was killed when the agent died.
		if r.InProgress {
			r.InProgress = false
			r.ExitCode = -1
			r.OK = false
		}
		info, _ := e.Info()
		mt := time.Unix(0, r.StartedAt*int64(time.Millisecond))
		if info != nil {
			mt = info.ModTime()
		}
		all = append(all, loaded{&r, mt})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mt.Before(all[j].mt) })
	for _, l := range all {
		s.runs = append(s.runs, l.run)
		s.byID[l.run.ID] = l.run
		if len(s.runs) > s.maxRuns {
			s.runs = s.runs[1:]
		}
	}
}

// AddJob upserts a job by Name. New jobs get a CreatedAt; updates
// keep CreatedAt and refresh UpdatedAt. Returns the stored copy.
func (s *RunnerStore) AddJob(j RunnerJob) (RunnerJob, error) {
	if strings.TrimSpace(j.Name) == "" {
		return RunnerJob{}, fmt.Errorf("job name is required")
	}
	if j.Kind == "" {
		j.Kind = RunnerJobShell
	}
	if j.Kind == RunnerJobShell && strings.TrimSpace(j.Command) == "" {
		return RunnerJob{}, fmt.Errorf("shell job %q requires a command", j.Name)
	}
	if j.Pool == "" {
		j.Pool = "any"
	}
	if j.Concurrency == "" {
		j.Concurrency = "skip"
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.jobs[j.Name]; ok {
		j.CreatedAt = existing.CreatedAt
	} else {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	stored := j
	s.jobs[j.Name] = &stored
	s.saveJobsLocked()
	return stored, nil
}

// RemoveJob deletes a job. Run history is left untouched — historical
// runs reference the job by name, not by pointer.
func (s *RunnerStore) RemoveJob(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[name]; !ok {
		return fmt.Errorf("job %q not found", name)
	}
	delete(s.jobs, name)
	s.saveJobsLocked()
	return nil
}

// GetJob returns a job copy by name. Second return is false on miss.
func (s *RunnerStore) GetJob(name string) (RunnerJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[name]
	if !ok {
		return RunnerJob{}, false
	}
	return *j, true
}

// ListJobs returns every job sorted by name. Pool filter is optional;
// empty matches all.
func (s *RunnerStore) ListJobs(pool string) []RunnerJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RunnerJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		if pool != "" && pool != "any" && j.Pool != pool {
			continue
		}
		out = append(out, *j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SetPaused toggles a job's paused flag.
func (s *RunnerStore) SetPaused(name string, paused bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[name]
	if !ok {
		return fmt.Errorf("job %q not found", name)
	}
	j.Paused = paused
	j.UpdatedAt = time.Now().UnixMilli()
	s.saveJobsLocked()
	return nil
}

// NewRunnerRunID returns a 16-hex compact run identifier. Same shape
// as deploy NewRunID — distinct ID space, no collision concern.
func NewRunnerRunID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t := time.Now().UnixNano()
		for i := 7; i >= 0; i-- {
			b[i] = byte(t & 0xff)
			t >>= 8
		}
	}
	return hex.EncodeToString(b)
}

// Start records a new in-progress run, allocates the on-disk dir,
// and opens the log file. Returns the stored pointer so the caller
// can mutate it through the lifecycle.
func (s *RunnerStore) Start(run RunnerRun) *RunnerRun {
	if run.ID == "" {
		run.ID = NewRunnerRunID()
	}
	if run.StartedAt == 0 {
		run.StartedAt = time.Now().UnixMilli()
	}
	run.InProgress = true
	rp := &run
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runRoot != "" {
		runDir := filepath.Join(s.runRoot, rp.ID)
		if err := os.MkdirAll(runDir, 0700); err != nil {
			log.Printf("[runner=%s] mkdir %s: %v — log disabled", rp.ID, runDir, err)
		} else {
			logPath := filepath.Join(runDir, "output.log")
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				log.Printf("[runner=%s] open %s: %v — log disabled", rp.ID, logPath, err)
			} else {
				rp.logFile = f
				rp.LogPath = logPath
			}
		}
	}
	s.runs = append(s.runs, rp)
	s.byID[rp.ID] = rp
	if len(s.runs) > s.maxRuns {
		evicted := s.runs[0]
		delete(s.byID, evicted.ID)
		if evicted.LogPath != "" {
			_ = os.RemoveAll(filepath.Dir(evicted.LogPath))
		}
		s.runs = s.runs[1:]
	}
	s.persistMetaLocked(rp)
	return rp
}

// Append captures one line into the in-memory tail and flushes it to
// the on-disk log. Lines are stored newline-terminated.
func (s *RunnerStore) Append(id, text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[id]
	if !ok {
		return
	}
	line := text
	if line[len(line)-1] != '\n' {
		line += "\n"
	}
	r.outputBuf = append(r.outputBuf, []byte(line)...)
	if len(r.outputBuf) > runnerOutputTailCap {
		r.outputBuf = r.outputBuf[len(r.outputBuf)-runnerOutputTailCap:]
	}
	r.OutputTail = string(r.outputBuf)
	if r.logFile != nil {
		n, err := r.logFile.WriteString(line)
		if err != nil {
			log.Printf("[runner=%s] log write failed: %v", r.ID, err)
		}
		r.LogBytes += int64(n)
	}
}

// Finish marks a run done, closes the log file, persists the final
// meta.json, and runs disk-quota GC.
func (s *RunnerStore) Finish(id string, exitCode int, timedOut bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[id]
	if !ok {
		return
	}
	if r.logFile != nil {
		_ = r.logFile.Sync()
		_ = r.logFile.Close()
		r.logFile = nil
	}
	now := time.Now().UnixMilli()
	r.FinishedAt = now
	r.DurationMs = now - r.StartedAt
	r.ExitCode = exitCode
	r.TimedOut = timedOut
	r.OK = exitCode == 0 && !timedOut
	r.InProgress = false
	s.persistMetaLocked(r)
	s.gcLogsLocked()
}

// persistMetaLocked writes meta.json for one run. Caller holds s.mu.
// Empties LogPath in the on-disk copy too — the absolute path is
// $HOME-prefixed and we treat it as host-private.
func (s *RunnerStore) persistMetaLocked(r *RunnerRun) {
	if r == nil || r.LogPath == "" || s.runRoot == "" {
		return
	}
	cp := *r
	cp.outputBuf = nil
	cp.logFile = nil
	cp.LogPath = "" // never persist the absolute path; recompute on load
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return
	}
	metaPath := filepath.Join(s.runRoot, r.ID, "meta.json")
	tmp := metaPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, metaPath)
}

// gcLogsLocked enforces runnerDiskQuotaBytes by oldest-first eviction.
// Caller holds s.mu.
func (s *RunnerStore) gcLogsLocked() {
	if s.runRoot == "" {
		return
	}
	type entry struct {
		path  string
		mtime time.Time
		size  int64
	}
	var dirs []entry
	var total int64
	items, err := os.ReadDir(s.runRoot)
	if err != nil {
		return
	}
	for _, item := range items {
		if !item.IsDir() {
			continue
		}
		full := filepath.Join(s.runRoot, item.Name())
		info, err := item.Info()
		if err != nil {
			continue
		}
		size := runnerLogDirSize(full)
		dirs = append(dirs, entry{path: full, mtime: info.ModTime(), size: size})
		total += size
	}
	if total <= runnerDiskQuotaBytes {
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mtime.Before(dirs[j].mtime) })
	for _, d := range dirs {
		if total <= runnerDiskQuotaBytes {
			return
		}
		if err := os.RemoveAll(d.path); err == nil {
			total -= d.size
		}
	}
}

func runnerLogDirSize(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// ListRuns returns up to `limit` runs (most recent first) optionally
// filtered to a single jobName and/or to one TriggeredBy identity
// (used by the guest filter so a guest only sees their own runs).
// limit=0 returns every run.
func (s *RunnerStore) ListRuns(jobName, triggeredBy string, limit int) []RunnerRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RunnerRun, 0, len(s.runs))
	for i := len(s.runs) - 1; i >= 0; i-- {
		r := s.runs[i]
		if jobName != "" && r.JobName != jobName {
			continue
		}
		if triggeredBy != "" && r.TriggeredBy != triggeredBy {
			continue
		}
		out = append(out, *r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// GetRun returns one run by ID. Guest filter behaves identically to
// DeployHistory.Get — non-matching guests get a "not found" so the
// existence of someone else's run doesn't leak.
func (s *RunnerStore) GetRun(id, triggeredBy string) (RunnerRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[id]
	if !ok {
		return RunnerRun{}, false
	}
	if triggeredBy != "" && r.TriggeredBy != triggeredBy {
		return RunnerRun{}, false
	}
	return *r, true
}

// LogPathFor returns the absolute log path for a run id, or "" when
// the run is unknown or its log is missing. Used by /runner/runs/{id}/log.
func (s *RunnerStore) LogPathFor(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[id]
	if !ok || r.LogPath == "" {
		return ""
	}
	return r.LogPath
}

// runnerLimits are the per-caller in-flight caps. Mirrors
// deployShipLimits — owner gets headroom for parallel work, guests
// get a tight cap so a misbehaving end-user can't saturate the box.
var runnerLimits = struct {
	Owner int
	Guest int
}{Owner: 8, Guest: 2}

// runnerLimiter tracks per-caller in-flight runs. Same shape as
// deployLimiter — kept separate so the two surfaces don't fight for
// the same slot.
type runnerLimiter struct {
	mu       sync.Mutex
	inFlight map[string]int
}

func newRunnerLimiter() *runnerLimiter { return &runnerLimiter{inFlight: map[string]int{}} }

func (l *runnerLimiter) tryAcquire(key string, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[key] >= max {
		return false
	}
	l.inFlight[key]++
	return true
}

func (l *runnerLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[key] > 0 {
		l.inFlight[key]--
	}
}

// PoolMatches answers whether `caps` (the local agent's capability
// tags) satisfy the job's pool selector. Phase 1 grammar: a single
// label, with the special-case "any". Phase 5 will extend to AND/OR
// expressions; doing it now would be over-engineering.
func PoolMatches(pool string, caps []string) bool {
	pool = strings.TrimSpace(pool)
	if pool == "" || pool == "any" {
		return true
	}
	for _, c := range caps {
		if strings.EqualFold(strings.TrimSpace(c), pool) {
			return true
		}
	}
	return false
}

// LocalCapabilities returns the labels this agent advertises. Wired
// to the existing capabilities snapshot so a runner-pool selector
// matches the same identity heartbeat clients already see. Phase 1
// keeps it minimal — `any`, `os:<linux|darwin|windows>`,
// `arch:<amd64|arm64>`, `host:<hostname>`. Future phases extend this
// from capabilities_snapshot.go on the same machine.
func LocalCapabilities() []string {
	return localRunnerCapabilities()
}

// localRunnerCapabilities builds the capability label list for this
// process. Cheap and dependency-free so the HTTP /runner/pools
// handler can call it on every request without a cache.
func localRunnerCapabilities() []string {
	caps := []string{
		"any",
		"os:" + runtime.GOOS,
		"arch:" + runtime.GOARCH,
		runtime.GOOS + "-" + runtime.GOARCH,
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		caps = append(caps, "host:"+strings.ToLower(h))
	}
	return caps
}

// runJobShell is the Phase 1 executor — straight `sh -c` with the
// job's env merged. Concurrency limit + project access gating happen
// at the HTTP layer.
//
// `triggeredBy` lands on the run as TriggeredBy ("owner" / guestUID
// / "schedule" / "webhook"). `vault` is the agent-wide vault so the
// job's env gets its project-scoped secrets without callers having
// to recompute them.
func runJobShell(ctx context.Context, store *RunnerStore, job RunnerJob, triggeredBy string, isGuest bool, vault *VaultStore) (RunnerRun, error) {
	if job.Kind != RunnerJobShell {
		return RunnerRun{}, fmt.Errorf("runJobShell called with kind %q", job.Kind)
	}
	if strings.TrimSpace(job.Command) == "" {
		return RunnerRun{}, fmt.Errorf("shell job %q has no command", job.Name)
	}
	rec := store.Start(RunnerRun{
		JobName:     job.Name,
		Kind:        job.Kind,
		Pool:        job.Pool,
		Project:     job.Project,
		TriggeredBy: triggeredBy,
		IsGuest:     isGuest,
	})

	timeout := job.TimeoutSec
	cctx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		cctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(cctx, "sh", "-c", job.Command)
	cmd.Env = composeRunnerEnv(vault, job.Project, job.Env, isGuest)
	if job.WorkDir != "" {
		cmd.Dir = job.WorkDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		store.Append(rec.ID, "[runner] stdout pipe: "+err.Error())
		store.Finish(rec.ID, -1, false)
		final, _ := store.GetRun(rec.ID, "")
		return final, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		store.Append(rec.ID, "[runner] stderr pipe: "+err.Error())
		store.Finish(rec.ID, -1, false)
		final, _ := store.GetRun(rec.ID, "")
		return final, err
	}
	if err := cmd.Start(); err != nil {
		store.Append(rec.ID, "[runner] spawn: "+err.Error())
		store.Finish(rec.ID, -1, false)
		final, _ := store.GetRun(rec.ID, "")
		return final, err
	}

	var wg sync.WaitGroup
	pump := func(rd interface{ Read([]byte) (int, error) }) {
		defer wg.Done()
		buf := make([]byte, 4096)
		var line []byte
		for {
			n, err := rd.Read(buf)
			if n > 0 {
				line = append(line, buf[:n]...)
				for {
					idx := -1
					for i, b := range line {
						if b == '\n' {
							idx = i
							break
						}
					}
					if idx < 0 {
						break
					}
					txt := strings.TrimRight(string(line[:idx]), "\r")
					// Yaver-action sentinel interception. If the runner
					// emitted `<<yaver-action: <verb> <args>>>` on this
					// line, fire the side-effect (e.g. broadcast open_app
					// to the paired phone) AND let the line through to
					// the user's chat — the LLM usually wraps the
					// sentinel inside a sentence ("Reloading sfmg now.
					// <<yaver-action: reload sfmg>>") so suppressing it
					// would leave a confusing trailing fragment, and
					// the sentinel itself is short + readable.
					if verb, args, ok := ParseYaverActionSentinel(txt); ok {
						go DispatchYaverAction(verb, args, rec.ID)
					}
					store.Append(rec.ID, txt)
					line = line[idx+1:]
				}
			}
			if err != nil {
				if len(line) > 0 {
					store.Append(rec.ID, string(line))
				}
				return
			}
		}
	}
	wg.Add(2)
	go pump(stdout)
	go pump(stderr)

	waitErr := cmd.Wait()
	wg.Wait()

	exitCode := 0
	timedOut := false
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
		if cctx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		}
	}
	store.Finish(rec.ID, exitCode, timedOut)
	final, _ := store.GetRun(rec.ID, "")
	return final, nil
}

// composeRunnerEnv builds the subprocess env using the same shape
// buildDeployShipEnv uses: optional vault overlay, guest sandbox of
// the parent env. Owner inherits the full agent env; guest gets only
// the safe-system whitelist (defined in deploy_run.go) plus vault.
func composeRunnerEnv(vs *VaultStore, project string, jobEnv map[string]string, isGuest bool) []string {
	var base []string
	if isGuest {
		for _, kv := range os.Environ() {
			if eq := strings.IndexByte(kv, '='); eq > 0 {
				key := kv[:eq]
				if safeSystemEnvKeys[key] || strings.HasPrefix(key, "LC_") {
					base = append(base, kv)
				}
			}
		}
	} else {
		base = append(base, os.Environ()...)
	}
	seen := map[string]int{}
	for i, kv := range base {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			seen[kv[:eq]] = i
		}
	}
	set := func(k, v string) {
		kv := k + "=" + v
		if idx, ok := seen[k]; ok {
			base[idx] = kv
		} else {
			seen[k] = len(base)
			base = append(base, kv)
		}
	}
	if vs != nil {
		// Globals first, then project — project wins on collision.
		for _, sum := range vs.List("") {
			if entry, err := vs.Get("", sum.Name); err == nil && entry != nil {
				set(entry.Name, entry.Value)
			}
		}
		if project != "" {
			for _, sum := range vs.List(project) {
				if entry, err := vs.Get(project, sum.Name); err == nil && entry != nil {
					set(entry.Name, entry.Value)
				}
			}
		}
	}
	for k, v := range jobEnv {
		if k == "" {
			continue
		}
		set(k, v)
	}
	return base
}
