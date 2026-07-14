package main

// console_processes.go — a structured process table, i.e. the data half of
// "htop, but on my phone".
//
// The ten existing MCP verbs (ps_aux, top_snapshot, process_list, …) all
// shell out and hand back a raw text blob under one JSON key. That is fine
// for an LLM to read and useless for a UI to render: you cannot sort a
// string by CPU. gopsutil is already a dependency (console_metrics.go uses
// cpu/mem/disk/net), so the process table costs us one more import and gives
// every surface — phone, web, runner — the same typed rows.
//
// CPU percent note: gopsutil's Percent(0, false) is "CPU time used since the
// process started, over its lifetime", not "right now". For a live view that
// reads wrong (a long-lived daemon looks idle even while it pegs a core), so
// we sample twice with a short gap and report the delta — the number htop
// shows.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// errProtectedProcess is returned rather than a generic failure so the UI can
// say "you can't kill the agent you're talking through" instead of "kill
// failed".
var errProtectedProcess = errors.New("refusing to kill a protected process (the Yaver agent, its parent, or init)")

// ProcessInfo is one row of the process table.
type ProcessInfo struct {
	PID     int32   `json:"pid"`
	PPID    int32   `json:"ppid"`
	Name    string  `json:"name"`
	Cmd     string  `json:"cmd,omitempty"`
	User    string  `json:"user,omitempty"`
	CPUPct  float64 `json:"cpuPct"`
	RSSMb   float64 `json:"rssMb"`
	MemPct  float64 `json:"memPct"`
	Status  string  `json:"status,omitempty"`
	Threads int32   `json:"threads,omitempty"`
	// Protected marks processes this endpoint refuses to kill, so the UI can
	// grey out the button instead of offering an action that will fail.
	Protected bool `json:"protected,omitempty"`
}

// ProcessTable is what the phone renders.
type ProcessTable struct {
	SampledAt string        `json:"sampledAt"`
	Count     int           `json:"count"`
	Processes []ProcessInfo `json:"processes"`
}

// cpuSampleGap is how long we wait between the two CPU readings. Long enough
// to be meaningful, short enough that the phone doesn't feel it.
const cpuSampleGap = 300 * time.Millisecond

// sampleProcesses returns the top `limit` processes by the requested sort key.
// Sorting server-side keeps the phone from pulling 800 rows over a relay to
// show 20.
func sampleProcesses(ctx context.Context, sortBy string, limit int) (ProcessTable, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return ProcessTable{}, err
	}

	// First CPU reading — primes gopsutil's per-process counters.
	prev := make(map[int32]float64, len(procs))
	for _, p := range procs {
		if t, err := p.TimesWithContext(ctx); err == nil {
			prev[p.Pid] = t.User + t.System
		}
	}

	select {
	case <-time.After(cpuSampleGap):
	case <-ctx.Done():
		return ProcessTable{}, ctx.Err()
	}

	self := int32(os.Getpid())
	gap := cpuSampleGap.Seconds()

	rows := make([]ProcessInfo, 0, len(procs))
	for _, p := range procs {
		name, err := p.NameWithContext(ctx)
		if err != nil || name == "" {
			continue // process exited between enumeration and read — normal
		}
		row := ProcessInfo{PID: p.Pid, Name: name}

		if t, err := p.TimesWithContext(ctx); err == nil {
			if before, ok := prev[p.Pid]; ok {
				delta := (t.User + t.System) - before
				if delta > 0 {
					row.CPUPct = round1(delta / gap * 100)
				}
			}
		}
		if mi, err := p.MemoryInfoWithContext(ctx); err == nil && mi != nil {
			row.RSSMb = round1(float64(mi.RSS) / (1 << 20))
		}
		if mp, err := p.MemoryPercentWithContext(ctx); err == nil {
			row.MemPct = round1(float64(mp))
		}
		if ppid, err := p.PpidWithContext(ctx); err == nil {
			row.PPID = ppid
		}
		if u, err := p.UsernameWithContext(ctx); err == nil {
			row.User = u
		}
		if st, err := p.StatusWithContext(ctx); err == nil && len(st) > 0 {
			row.Status = strings.Join(st, ",")
		}
		if n, err := p.NumThreadsWithContext(ctx); err == nil {
			row.Threads = n
		}
		if cmd, err := p.CmdlineWithContext(ctx); err == nil {
			// Full command lines can be kilobytes (JVM, node). Truncate for
			// transport; the UI shows the head anyway.
			if len(cmd) > 300 {
				cmd = cmd[:300] + "…"
			}
			row.Cmd = cmd
		}
		row.Protected = processIsProtected(p.Pid, self, name)
		rows = append(rows, row)
	}

	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "mem", "memory", "rss":
		sort.Slice(rows, func(i, j int) bool { return rows[i].RSSMb > rows[j].RSSMb })
	case "pid":
		sort.Slice(rows, func(i, j int) bool { return rows[i].PID < rows[j].PID })
	case "name":
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	default: // cpu
		sort.Slice(rows, func(i, j int) bool { return rows[i].CPUPct > rows[j].CPUPct })
	}

	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return ProcessTable{
		SampledAt: time.Now().UTC().Format(time.RFC3339),
		Count:     total,
		Processes: rows,
	}, nil
}

// processIsProtected refuses the kills that would be self-harm: the agent
// itself (you'd sever the very connection you're using to kill it, from the
// phone, with no way back in), and init.
func processIsProtected(pid, self int32, name string) bool {
	if pid == self || pid == 1 || pid <= 0 {
		return true
	}
	// The agent may be running as a child (systemd unit, launchd job) whose
	// parent we'd also be killing.
	if pid == int32(os.Getppid()) {
		return true
	}
	switch strings.ToLower(name) {
	case "yaver", "yaver.exe", "launchd", "systemd", "init", "kernel_task":
		return true
	}
	return false
}

// killProcess terminates a pid after the protection check. SIGTERM first —
// this is a dev's own box, not a hostile process; give it a chance to flush.
func killProcess(pid int32, force bool) error {
	if processIsProtected(pid, int32(os.Getpid()), processNameFor(pid)) {
		return errProtectedProcess
	}
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	if force {
		return p.Kill()
	}
	return p.Terminate()
}

func processNameFor(pid int32) string {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ""
	}
	n, err := p.Name()
	if err != nil {
		return ""
	}
	return n
}

// --- HTTP -----------------------------------------------------------------

// handleConsoleProcesses serves the process table.
//
//	GET /console/processes?sort=cpu|mem|pid|name&limit=50
func (s *HTTPServer) handleConsoleProcesses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	table, err := sampleProcesses(ctx, r.URL.Query().Get("sort"), limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "table": table})
}

// handleConsoleProcessKill kills a pid.
//
//	POST /console/processes/kill {"pid": 1234, "force": false}
func (s *HTTPServer) handleConsoleProcessKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		PID   int32 `json:"pid"`
		Force bool  `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := killProcess(req.PID, req.Force); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errProtectedProcess) {
			code = http.StatusForbidden
		}
		jsonError(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "pid": req.PID})
}
