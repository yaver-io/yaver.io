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
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// deployOutputTailCap is how much of the subprocess output we keep
// per run. Enough to show a failing tail without letting a runaway
// xcodebuild eat all the agent's RAM.
const deployOutputTailCap = 8 * 1024

// DeployRun is one historical entry. Kept flat + JSON-friendly so
// the endpoint just serializes it verbatim.
type DeployRun struct {
	ID           string `json:"id"`
	App          string `json:"app"`
	Target       string `json:"target"`
	Stack        string `json:"stack,omitempty"`
	Path         string `json:"path,omitempty"`
	RequestedBy  string `json:"requested_by,omitempty"` // "owner" or guest userID
	IsGuest      bool   `json:"is_guest,omitempty"`
	StartedAt    int64  `json:"started_at"`  // unix millis
	FinishedAt   int64  `json:"finished_at,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	ExitCode     int    `json:"exit_code"`
	OK           bool   `json:"ok,omitempty"`
	InProgress   bool   `json:"in_progress,omitempty"`
	OutputTail   string `json:"output_tail,omitempty"` // last ~8KB of stdout+stderr
	outputBuf    []byte // ring buffer backing OutputTail (internal)
}

// DeployHistory is a bounded FIFO of DeployRun. Safe for concurrent
// use; writes cost an O(1) per run plus a mutex hop per output line,
// which is fine for anything a human hits via `yaver deploy ship`.
type DeployHistory struct {
	mu     sync.Mutex
	runs   []*DeployRun
	byID   map[string]*DeployRun
	maxLen int
}

// NewDeployHistory builds an empty buffer with room for maxLen
// entries. Oldest entries drop off the front as new ones arrive.
func NewDeployHistory(maxLen int) *DeployHistory {
	if maxLen <= 0 {
		maxLen = 100
	}
	return &DeployHistory{
		runs:   make([]*DeployRun, 0, maxLen),
		byID:   make(map[string]*DeployRun, maxLen),
		maxLen: maxLen,
	}
}

// NewRunID generates a compact ID (8 random bytes → 16 hex). Unique
// enough for a few hundred runs per agent lifetime.
func NewRunID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
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
// the caller can mutate OutputTail / ExitCode later.
func (h *DeployHistory) Start(run DeployRun) *DeployRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	if run.ID == "" {
		run.ID = NewRunID()
	}
	if run.StartedAt == 0 {
		run.StartedAt = time.Now().UnixMilli()
	}
	run.InProgress = true
	rp := &run
	h.runs = append(h.runs, rp)
	h.byID[rp.ID] = rp
	if len(h.runs) > h.maxLen {
		evicted := h.runs[0]
		delete(h.byID, evicted.ID)
		// Shift — maxLen is small so O(N) is fine.
		h.runs = h.runs[1:]
	}
	return rp
}

// Append captures a line into the output-tail ring buffer of the run.
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
		// Trim from the front — last deployOutputTailCap bytes win.
		r.outputBuf = r.outputBuf[len(r.outputBuf)-deployOutputTailCap:]
	}
	r.OutputTail = string(r.outputBuf)
}

// Finish marks a run complete. Safe to call on an already-finished
// run (idempotent — second call wins).
func (h *DeployHistory) Finish(id string, exitCode int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.byID[id]
	if !ok {
		return
	}
	now := time.Now().UnixMilli()
	r.FinishedAt = now
	r.DurationMs = now - r.StartedAt
	r.ExitCode = exitCode
	r.OK = exitCode == 0
	r.InProgress = false
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
	// Already descending by insertion order; re-sort defensively in
	// case a future path mutates StartedAt mid-flight.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt > out[j].StartedAt
	})
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
