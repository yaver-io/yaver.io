package main

// deploy_history.go — in-memory record of recent /deploy/ship runs.
// Lets the host (and, subject to the usual owner/guest scoping, a
// guest) see "what happened with the last N deploys on this machine"
// without needing to re-open the SSE stream for a finished run.
//
// Intentionally *not* persistent across agent restart: the run
// fingerprint (exit code, duration) is all you need to decide to
// retry; the full log lives in your terminal scrollback. If we add
// persistence later it belongs on the host's filesystem under
// ~/.yaver/deploys/<id>/stdout, never in Convex.
//
// Access model:
//
//   - Owner: sees every run.
//   - Guest (scope=full or scope=deploy): sees runs they themselves
//     initiated. Runs initiated by the owner or by another guest are
//     hidden — a guest-triggered TestFlight on your Mac mini is your
//     business, not another guest's.

import (
	cryptorand "crypto/rand"
	"encoding/json"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// deployOutputTailCap is how much of the subprocess output we keep
// per run. Enough to show a failing tail without letting a runaway
// xcodebuild eat all the agent's RAM.
const deployOutputTailCap = 8 * 1024

// deployDiskQuotaBytes is the soft cap on ~/.yaver/deploys/. Oldest
// run directories are evicted until usage drops below this cap. A
// 20-minute xcodebuild produces a few megabytes of log per run, so
// 500 MB buys ~100 runs of headroom.
const deployDiskQuotaBytes = 500 * 1024 * 1024

// DeployRun is one historical entry. Kept flat + JSON-friendly so
// the endpoint just serializes it verbatim.
type DeployRun struct {
	ID          string           `json:"id"`
	Slot        string           `json:"slot,omitempty"`
	App         string           `json:"app"`
	Target      string           `json:"target"`
	Stack       string           `json:"stack,omitempty"`
	Path        string           `json:"path,omitempty"`
	Status      string           `json:"status,omitempty"`
	RequestedBy string           `json:"requested_by,omitempty"` // "owner" or guest userID
	IsGuest     bool             `json:"is_guest,omitempty"`
	StartedAt   int64            `json:"started_at"` // unix millis
	FinishedAt  int64            `json:"finished_at,omitempty"`
	DurationMs  int64            `json:"duration_ms,omitempty"`
	ExitCode    int              `json:"exit_code"`
	OK          bool             `json:"ok,omitempty"`
	InProgress  bool             `json:"in_progress,omitempty"`
	OutputTail  string           `json:"output_tail,omitempty"` // last ~8KB of stdout+stderr
	ErrorClass  DeployErrorClass `json:"error_class,omitempty"` // set by Finish() via classifier
	TimedOut    bool             `json:"timed_out,omitempty"`
	LogPath     string           `json:"log_path,omitempty"`    // on-disk full log (server-side; not surfaced to guests)
	LogBytes    int64            `json:"log_bytes,omitempty"`
	outputBuf   []byte           // ring buffer backing OutputTail (internal)
	logFile     *os.File         // open handle during the run
}

// DeployHistory is a bounded FIFO of DeployRun. Safe for concurrent
// use; writes cost an O(1) per run plus a mutex hop per output line,
// which is fine for anything a human hits via `yaver deploy ship`.
type DeployHistory struct {
	mu      sync.Mutex
	runs    []*DeployRun
	byID    map[string]*DeployRun
	maxLen  int
	logRoot string // on-disk logs. empty means disk persistence disabled.
}

// NewDeployHistory builds an empty buffer with room for maxLen
// entries. Oldest entries drop off the front as new ones arrive.
// Per-run full-output logs are written to `~/.yaver/deploys/<id>/output.log`
// for later inspection via /deploy/runs/{id}/output; the in-memory
// 8 KB tail stays alongside for quick lookups.
func NewDeployHistory(maxLen int) *DeployHistory {
	if maxLen <= 0 {
		maxLen = 100
	}
	h := &DeployHistory{
		runs:   make([]*DeployRun, 0, maxLen),
		byID:   make(map[string]*DeployRun, maxLen),
		maxLen: maxLen,
	}
	if dir, err := ConfigDir(); err == nil {
		h.logRoot = filepath.Join(dir, "deploys")
		if err := os.MkdirAll(h.logRoot, 0700); err != nil {
			log.Printf("[deploy-history] mkdir %s failed: %v (disk persistence disabled)", h.logRoot, err)
			h.logRoot = ""
		}
	}
	h.loadPersistedRuns()
	return h
}

// LogRoot returns the absolute directory used for on-disk run logs
// (empty string when disk persistence is disabled — typically only in
// unit tests that build a DeployHistory without a config dir).
func (h *DeployHistory) LogRoot() string {
	return h.logRoot
}

// NewRunID generates a compact ID (8 random bytes → 16 hex). Unique
// enough for a few hundred runs per agent lifetime.
func NewRunID() string {
	b := make([]byte, 8)
	if _, err := cryptorand.Read(b); err != nil {
		// Fallback: nanosecond clock. Never happens on any real OS,
		// but we don't want to panic in a deploy path.
		t := time.Now().UnixNano()
		for i := 7; i >= 0; i-- {
			b[i] = byte(t & 0xff)
			t >>= 8
		}
	}
	return hex.EncodeToString(b)
}

// Start records a new in-progress run. Returns the stored pointer so
// the caller can mutate OutputTail / ExitCode later. Also opens an
// on-disk log file at `<logRoot>/<id>/output.log` if logRoot is set.
func (h *DeployHistory) Start(run DeployRun) *DeployRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	if run.ID == "" {
		run.ID = NewRunID()
	}
	if run.StartedAt == 0 {
		run.StartedAt = time.Now().UnixMilli()
	}
	if strings.TrimSpace(run.Slot) == "" {
		run.Slot = deploySlot(run.Target)
	}
	if strings.TrimSpace(run.Status) == "" {
		run.Status = runStatusRunning
	}
	run.InProgress = deployStatusInProgress(run.Status)
	run.OK = deployStatusOK(run.Status)
	rp := &run
	if h.logRoot != "" {
		runDir := filepath.Join(h.logRoot, rp.ID)
		if err := os.MkdirAll(runDir, 0700); err != nil {
			log.Printf("[deploy=%s] mkdir %s: %v — full log disabled for this run", rp.ID, runDir, err)
		} else {
			logPath := filepath.Join(runDir, "output.log")
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				log.Printf("[deploy=%s] open %s: %v — full log disabled for this run", rp.ID, logPath, err)
			} else {
				rp.logFile = f
				rp.LogPath = logPath
			}
		}
	}
	h.runs = append(h.runs, rp)
	h.byID[rp.ID] = rp
	h.persistRunLocked(rp)
	// h.runs stays in INSERTION order, so runs[0] is genuinely the oldest and
	// the ring is FIFO. Do not sort the storage here: List() already sorts its
	// own copy for presentation (sortDeployRuns), so the only thing a storage
	// sort achieved was destroying the one ordering eviction can rely on.
	//
	// It cannot be reconstructed afterwards either — StartedAt is unix millis,
	// so a burst of runs shares a timestamp and no comparison can separate
	// them. Sorting here made the ring run backwards: sortRunsLocked orders
	// slot-major then NEWEST-first, so runs[0] became the run just created and
	// Start deleted the log you were about to read, while the oldest run stayed
	// forever.
	if len(h.runs) > h.maxLen {
		evicted := h.runs[0]
		delete(h.byID, evicted.ID)
		// Drop the on-disk log too — ring buffer semantics apply.
		if evicted.LogPath != "" {
			_ = os.RemoveAll(filepath.Dir(evicted.LogPath))
		}
		h.runs = h.runs[1:]
	}
	return rp
}

// Append captures a line into the output-tail ring buffer of the run
// and also appends it to the on-disk log (if logRoot is enabled).
// Lines are stored newline-terminated so a joined OutputTail string
// is human-readable.
func (h *DeployHistory) Append(id string, text string) {
	if text == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.byID[id]
	if !ok {
		return
	}
	line := text
	if line[len(line)-1] != '\n' {
		line += "\n"
	}
	r.outputBuf = append(r.outputBuf, []byte(line)...)
	if len(r.outputBuf) > deployOutputTailCap {
		r.outputBuf = r.outputBuf[len(r.outputBuf)-deployOutputTailCap:]
	}
	r.OutputTail = string(r.outputBuf)
	if r.logFile != nil {
		n, err := r.logFile.WriteString(line)
		if err != nil {
			log.Printf("[deploy=%s] log write failed: %v", r.ID, err)
		}
		r.LogBytes += int64(n)
	}
	h.persistRunLocked(r)
}

// Finish marks a run complete and runs error classification against
// the captured output tail. Idempotent — a second call wins.
//
// The classifier may rewrite OK from false→true for the
// "already_uploaded" class (TestFlight's "Redundant Binary Upload"
// is a success masquerading as an error). exitCode is kept as
// originally reported so a curious operator can still see the raw
// signal.
func (h *DeployHistory) Finish(id string, exitCode int, timedOut bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.byID[id]
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
	class, treatAsOK := ClassifyDeployOutput(r.OutputTail, exitCode, timedOut)
	r.ErrorClass = class
	r.OK = exitCode == 0 || treatAsOK
	r.InProgress = false
	if r.OK {
		r.Status = runStatusCompleted
	} else if timedOut {
		r.Status = runStatusBlocked
	} else {
		r.Status = runStatusFailed
	}
	h.persistRunLocked(r)
	// Opportunistic GC: if the on-disk logs are getting chunky, drop
	// oldest until we're under quota again. Runs on Finish because
	// that's when we know the final size of the just-closed log.
	h.gcLogsLocked()
}

// gcLogsLocked evicts oldest on-disk log dirs until total bytes drop
// below deployDiskQuotaBytes. Safe to call with the mutex held.
func (h *DeployHistory) gcLogsLocked() {
	if h.logRoot == "" {
		return
	}
	type entry struct {
		path  string
		mtime time.Time
		size  int64
	}
	var dirs []entry
	var total int64
	items, err := os.ReadDir(h.logRoot)
	if err != nil {
		return
	}
	for _, item := range items {
		if !item.IsDir() {
			continue
		}
		full := filepath.Join(h.logRoot, item.Name())
		info, err := item.Info()
		if err != nil {
			continue
		}
		size := deployLogDirSize(full)
		dirs = append(dirs, entry{path: full, mtime: info.ModTime(), size: size})
		total += size
	}
	if total <= deployDiskQuotaBytes {
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mtime.Before(dirs[j].mtime) })
	for _, d := range dirs {
		if total <= deployDiskQuotaBytes {
			return
		}
		if err := os.RemoveAll(d.path); err == nil {
			total -= d.size
		}
	}
}

// deployLogDirSize sums the size of every regular file under `root`.
// Suffix-named to avoid clashing with `dirSize` in clean.go.
func deployLogDirSize(root string) int64 {
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

// List returns up to `limit` runs (most recent first). limit=0 means
// every run. If guestFilter is non-empty, only runs where
// RequestedBy == guestFilter are returned — this is how we keep one
// guest's deploys invisible to another guest.
func (h *DeployHistory) List(limit int, guestFilter string) []DeployRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]DeployRun, 0, len(h.runs))
	for i := len(h.runs) - 1; i >= 0; i-- {
		r := h.runs[i]
		if guestFilter != "" && r.RequestedBy != guestFilter {
			continue
		}
		out = append(out, *r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	sortDeployRuns(out)
	return out
}

// Get returns one run by ID. The second return is false when the ID
// is unknown. If guestFilter is non-empty, also returns false when
// the run belongs to someone else — handler layer turns this into a
// 404 (indistinguishable from "unknown", which is correct for
// information-hiding).
func (h *DeployHistory) Get(id string, guestFilter string) (DeployRun, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.byID[id]
	if !ok {
		return DeployRun{}, false
	}
	if guestFilter != "" && r.RequestedBy != guestFilter {
		return DeployRun{}, false
	}
	return *r, true
}

func (h *DeployHistory) metaPath(id string) string {
	if h.logRoot == "" || strings.TrimSpace(id) == "" {
		return ""
	}
	return filepath.Join(h.logRoot, id, "meta.json")
}

func (h *DeployHistory) persistRunLocked(run *DeployRun) {
	path := h.metaPath(run.ID)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	meta := *run
	meta.outputBuf = nil
	meta.logFile = nil
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}

func (h *DeployHistory) loadPersistedRuns() {
	if h.logRoot == "" {
		return
	}
	items, err := os.ReadDir(h.logRoot)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, item := range items {
		if !item.IsDir() {
			continue
		}
		metaPath := filepath.Join(h.logRoot, item.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var run DeployRun
		if err := json.Unmarshal(data, &run); err != nil {
			continue
		}
		if run.ID == "" {
			run.ID = item.Name()
		}
		if strings.TrimSpace(run.Slot) == "" {
			run.Slot = deploySlot(run.Target)
		}
		if strings.TrimSpace(run.Status) == "" {
			switch {
			case run.InProgress:
				run.Status = runStatusRunning
			case run.OK:
				run.Status = runStatusCompleted
			default:
				run.Status = runStatusFailed
			}
		}
		run.InProgress = deployStatusInProgress(run.Status)
		run.OK = deployStatusOK(run.Status)
		rp := run
		h.runs = append(h.runs, &rp)
		h.byID[rp.ID] = &rp
	}
	// Persisted runs come off disk in readdir order, so establish the ring's
	// invariant explicitly: oldest first. Ascending, NOT sortRunsLocked — that
	// one is slot-major and newest-first, which is a presentation order and
	// would leave runs[0] pointing at the newest run for every later eviction.
	sort.SliceStable(h.runs, func(i, j int) bool { return h.runs[i].StartedAt < h.runs[j].StartedAt })
	if len(h.runs) > h.maxLen {
		// Keep the NEWEST maxLen — the tail. Slicing the other end (or slicing
		// this end of a newest-first sort) brought a restarted box back showing
		// its most ancient deploys. Drop the evicted byID entries too; they used
		// to leak, leaving Get() able to return a run List() no longer had.
		for _, ev := range h.runs[:len(h.runs)-h.maxLen] {
			delete(h.byID, ev.ID)
		}
		h.runs = h.runs[len(h.runs)-h.maxLen:]
	}
}

func (h *DeployHistory) sortRunsLocked() {
	sort.SliceStable(h.runs, func(i, j int) bool {
		if h.runs[i].Slot != h.runs[j].Slot {
			return h.runs[i].Slot < h.runs[j].Slot
		}
		if h.runs[i].StartedAt != h.runs[j].StartedAt {
			return h.runs[i].StartedAt > h.runs[j].StartedAt
		}
		return h.runs[i].ID < h.runs[j].ID
	})
}

func sortDeployRuns(runs []DeployRun) {
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].Slot != runs[j].Slot {
			return runs[i].Slot < runs[j].Slot
		}
		if runs[i].StartedAt != runs[j].StartedAt {
			return runs[i].StartedAt > runs[j].StartedAt
		}
		return runs[i].ID < runs[j].ID
	})
}
