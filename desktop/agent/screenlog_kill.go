package main

// screenlog_kill.go — the panic button.
//
// `yaver screenlog kill` is the one-shot, irreversible-by-default STOP for the
// screen black box. Recording is sensitive enough (and, via --persist, durable
// enough — it survives reboot, sign-out, and an offline box) that there has to
// be a single command that provably ends ALL of it, not just the current
// session. `stop` ends the live session but leaves autostart armed and the
// feature enabled; `disable` flips the policy switch but leaves the live loop
// running until the process restarts. kill does everything, in order:
//
//   1. stop the live capture loop NOW (frames + input)
//   2. disarm the reboot-durable auto-resume marker (autostart.json)
//   3. flip the master kill-switch (policy.Enabled=false) so nothing — local,
//      remote, mesh, or autostart — can start recording again until the owner
//      explicitly runs `yaver screenlog enable`
//   4. (optional) --purge: delete every captured session off disk
//
// Design principle: STOPPING surveillance is always safe, so kill is NOT gated
// by screenlogEnforce — any caller who clears the agent's auth (the owner, any
// owner device, a granted peer) may kill. Only STARTING is gated. This is the
// opposite of the start path on purpose: you never want a permission check
// standing between a person and turning off a recording of them.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleScreenlogKill is POST /screenlog/kill[?purge=1]. Behind s.auth like
// every screenlog route, but NOT behind screenlogEnforce — stopping is always
// allowed. Returns a summary of exactly what was torn down.
func (s *HTTPServer) handleScreenlogKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	purge := r.URL.Query().Get("purge") == "1"
	if !purge {
		// Also accept {"purge":true} in the body.
		var body struct {
			Purge bool `json:"purge"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		purge = body.Purge
	}
	res := killScreenlog(purge)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "killed": res})
}

type screenlogKillResult struct {
	StoppedSession   string `json:"stoppedSession,omitempty"`
	StoppedFrames    int    `json:"stoppedFrames,omitempty"`
	WasRecording     bool   `json:"wasRecording"`
	AutostartCleared bool   `json:"autostartDisarmed"`
	PolicyDisabled   bool   `json:"policyDisabled"`
	Purged           bool   `json:"purged"`
	PurgedSessions   int    `json:"purgedSessions,omitempty"`
	PurgedBytes      int64  `json:"purgedBytes,omitempty"`
}

// killScreenlog runs the full stop sequence. Best-effort on every step — a
// failure in one (e.g. nothing was recording) must not block the others, so
// the kill-switch always lands even if there was no live session.
func killScreenlog(purge bool) screenlogKillResult {
	var res screenlogKillResult

	// 1. Stop the live loop. stopScreenlog also stops input capture.
	if sess, err := stopScreenlog(); err == nil && sess != nil {
		res.WasRecording = true
		res.StoppedSession = sess.ID
		res.StoppedFrames = len(sess.Frames)
	}
	stopInputCapture() // belt-and-suspenders if there was no frame loop

	// 2. Disarm the reboot-durable auto-resume so a future `yaver serve`
	//    start does NOT bring recording back. Leave the marker on disk with
	//    enabled=false (auditable) rather than deleting it.
	if a, ok := loadScreenlogAutostart(); ok {
		if a.Enabled {
			a.Enabled = false
			_ = saveScreenlogAutostart(a)
		}
	}
	res.AutostartCleared = true

	// 3. Flip the master kill-switch. After this, startScreenlogGuarded /
	//    resumeScreenlogIfEnabled refuse to start until `yaver screenlog
	//    enable`. This is the load-bearing step.
	pol := loadScreenlogPolicy()
	pol.Enabled = false
	_ = saveScreenlogPolicy(pol)
	res.PolicyDisabled = true

	// 4. Optional purge of captured data.
	if purge {
		n, b := purgeScreenlogSessions()
		res.Purged = true
		res.PurgedSessions = n
		res.PurgedBytes = b
	}

	appendScreenlogAudit(screenlogAuditEntry{
		Action: "kill",
		Note: fmt.Sprintf("panic stop — stopped=%q purge=%v policyDisabled=true autostartDisarmed=true",
			res.StoppedSession, purge),
	})
	return res
}

// purgeScreenlogSessions deletes every captured session directory under the
// screenlog root and returns (count, bytesFreed). It is deliberately narrow:
// it ONLY removes immediate child directories of the screenlog dir (the
// per-session slog-* folders) and never touches policy.json, audit.jsonl, or
// autostart.json — so a purge stays auditable and the kill-switch survives.
// Nothing outside screenlogDir() is ever considered.
func purgeScreenlogSessions() (int, int64) {
	base, err := screenlogDir()
	if err != nil || base == "" || base == "/" {
		return 0, 0
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0, 0
	}
	var count int
	var bytes int64
	for _, e := range entries {
		if !e.IsDir() {
			continue // never delete the top-level json files
		}
		name := e.Name()
		// Only session folders. The generated ids are "slog-<rand>"; refuse
		// anything that could escape or that isn't clearly a session dir.
		if !strings.HasPrefix(name, "slog-") || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
			continue
		}
		dir := filepath.Join(base, name)
		bytes += dirSizeBytes(dir)
		if os.RemoveAll(dir) == nil {
			count++
		}
	}
	return count, bytes
}

// dirSizeBytes sums file sizes under dir (best-effort, for reporting only).
func dirSizeBytes(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
