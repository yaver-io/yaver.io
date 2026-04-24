package main

// diagnose.go — `yaver diagnose`: a one-command self-check that
// surfaces the common breakages we've hit on production boxes.
//
// Design:
//   - Each check is a `diagCheck` with a Name + Run(ctx, emit) signature.
//   - Run emits `diagEvent` values (start / status / finding / done)
//     that the CLI renders as streaming output and the HTTP SSE
//     handler forwards to mobile / web subscribers verbatim.
//   - A final summary rolls findings into ok / warning / failure
//     buckets so callers can act without parsing every line.
//
// Current checks (v1):
//   1. binary-paths   — every `yaver` on disk + running-process binary
//                       versions; flags drift (the 1.99.25 vs 1.99.31
//                       footgun).
//   2. running-procs  — pgrep + /proc/<pid>/exe for each live agent.
//   3. ports          — 18080 (HTTP), 4433 (QUIC), 18443 (TLS).
//   4. auth-state     — config.json auth_token presence + remote
//                       ValidateToken call against Convex.
//   5. workspace      — does yaver.workspace.yaml exist at work-dir
//                       and does it parse?
//   6. systemd-unit   — if linux, pick up the yaver unit file +
//                       report --work-dir + ExecStart binary path.
//   7. runtime-deps   — node, git, docker, claude, codex, aider, ollama.
//
// Future additions (not in v1): DNS + Convex + relay reachability,
// self-update last-check-at, mobile-app HTTP-server port 8347 probe.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DiagSeverity tags each finding. Callers can decide which level is
// worth paging on.
type DiagSeverity string

const (
	DiagOK      DiagSeverity = "ok"
	DiagInfo    DiagSeverity = "info"
	DiagWarning DiagSeverity = "warning"
	DiagFailure DiagSeverity = "failure"
)

// DiagEvent is the JSON shape streamed to SSE subscribers and printed
// by the CLI. Exactly one type per frame.
type DiagEvent struct {
	Type      string                 `json:"type"` // "start", "check_start", "finding", "check_end", "done"
	Check     string                 `json:"check,omitempty"`
	Severity  DiagSeverity           `json:"severity,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Timestamp string                 `json:"timestamp"`
}

// DiagEmit is the callback a check uses to stream events.
type DiagEmit func(DiagEvent)

type diagCheck struct {
	Name string
	Run  func(ctx context.Context, emit DiagEmit)
}

// DiagnoseOptions tweaks which checks run. All checks run by default;
// Only runs just the named set; Skip subtracts from the default.
type DiagnoseOptions struct {
	Only []string
	Skip []string
	// Fix: when true, checks that can auto-remediate (binary-paths
	// symlink cleanup) will do so. Otherwise they just report.
	Fix bool
	// Agent: used to look up the agent's current auth state,
	// workspace workDir, and listening ports. Optional.
	Agent *HTTPServer
}

// RunDiagnose executes the selected checks in order, streaming events
// through emit. Blocks until every check completes or ctx is done.
func RunDiagnose(ctx context.Context, opts DiagnoseOptions, emit DiagEmit) DiagReport {
	start := time.Now().UTC()
	emit(DiagEvent{Type: "start", Timestamp: start.Format(time.RFC3339)})

	all := []diagCheck{
		{Name: "binary-paths", Run: checkBinaryPaths(opts.Fix)},
		{Name: "running-procs", Run: checkRunningProcs},
		{Name: "ports", Run: checkPorts},
		{Name: "auth-state", Run: checkAuthState(opts.Agent)},
		{Name: "workspace", Run: checkWorkspace(opts.Agent)},
		{Name: "systemd-unit", Run: checkSystemdUnit},
		{Name: "runtime-deps", Run: checkRuntimeDeps},
	}
	// v2 checks (cloudflared / tailscale / relay / vpn / convex / runners)
	// live in diagnose_checks_v2.go and register themselves into
	// extraDiagChecks at init time. Appended here so the v1 local-box
	// smoke is unchanged and --skip can drop any of them.
	all = append(all, extraDiagChecks...)

	wanted := map[string]bool{}
	for _, c := range all {
		wanted[c.Name] = true
	}
	if len(opts.Only) > 0 {
		wanted = map[string]bool{}
		for _, name := range opts.Only {
			wanted[name] = true
		}
	}
	for _, name := range opts.Skip {
		delete(wanted, name)
	}

	counts := map[DiagSeverity]int{}
	var mu sync.Mutex
	wrappedEmit := func(ev DiagEvent) {
		mu.Lock()
		if ev.Severity != "" {
			counts[ev.Severity]++
		}
		if ev.Timestamp == "" {
			ev.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}
		mu.Unlock()
		emit(ev)
	}

	for _, c := range all {
		if !wanted[c.Name] {
			continue
		}
		wrappedEmit(DiagEvent{Type: "check_start", Check: c.Name})
		c.Run(ctx, wrappedEmit)
		wrappedEmit(DiagEvent{Type: "check_end", Check: c.Name})
	}

	done := time.Now().UTC()
	report := DiagReport{
		StartedAt: start.Format(time.RFC3339),
		EndedAt:   done.Format(time.RFC3339),
		OK:        counts[DiagOK],
		Info:      counts[DiagInfo],
		Warnings:  counts[DiagWarning],
		Failures:  counts[DiagFailure],
	}
	emit(DiagEvent{
		Type:      "done",
		Timestamp: done.Format(time.RFC3339),
		Data: map[string]interface{}{
			"ok":       report.OK,
			"info":     report.Info,
			"warnings": report.Warnings,
			"failures": report.Failures,
		},
	})
	return report
}

// DiagReport is the summary RunDiagnose returns after emitting all events.
type DiagReport struct {
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt"`
	OK        int    `json:"ok"`
	Info      int    `json:"info"`
	Warnings  int    `json:"warnings"`
	Failures  int    `json:"failures"`
}

// ─── Checks ────────────────────────────────────────────────────────

func checkBinaryPaths(fix bool) func(context.Context, DiagEmit) {
	return func(ctx context.Context, emit DiagEmit) {
		candidates := diagYaverBinaryCandidates()
		seen := map[string]string{} // path → version
		for _, path := range candidates {
			st, err := os.Stat(path)
			if err != nil || st.IsDir() {
				continue
			}
			ver := diagRunVersion(ctx, path)
			seen[path] = ver
			emit(DiagEvent{
				Type:     "finding",
				Check:    "binary-paths",
				Severity: DiagInfo,
				Message:  fmt.Sprintf("%s — %s", path, ver),
				Data:     map[string]interface{}{"path": path, "version": ver},
			})
		}

		// Unique versions ignoring empty strings.
		versions := map[string]bool{}
		for _, v := range seen {
			if v != "" && v != "unknown" {
				versions[v] = true
			}
		}
		switch {
		case len(seen) == 0:
			emit(DiagEvent{
				Type:     "finding",
				Check:    "binary-paths",
				Severity: DiagFailure,
				Message:  "No yaver binary found in any known install path.",
			})
		case len(versions) > 1:
			emit(DiagEvent{
				Type:     "finding",
				Check:    "binary-paths",
				Severity: DiagWarning,
				Message:  fmt.Sprintf("Multiple yaver versions on disk: %v", mapKeys(versions)),
			})
			if fix {
				diagNormaliseBinaries(seen, emit)
			}
		default:
			emit(DiagEvent{
				Type:     "finding",
				Check:    "binary-paths",
				Severity: DiagOK,
				Message:  fmt.Sprintf("All yaver binaries agree on version %s.", firstKey(versions)),
			})
		}
	}
}

func checkRunningProcs(ctx context.Context, emit DiagEmit) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		emit(DiagEvent{Type: "finding", Check: "running-procs", Severity: DiagInfo, Message: "running-procs check skipped on " + runtime.GOOS})
		return
	}
	out, err := exec.CommandContext(ctx, "pgrep", "-af", "yaver serve").CombinedOutput()
	if err != nil {
		// pgrep returns exit 1 when no matches — that's OK.
		emit(DiagEvent{Type: "finding", Check: "running-procs", Severity: DiagInfo, Message: "No running yaver serve processes detected."})
		return
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	procs := []map[string]interface{}{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		pid := parts[0]
		cmdline := parts[1]
		exe := ""
		if runtime.GOOS == "linux" {
			exe, _ = os.Readlink(filepath.Join("/proc", pid, "exe"))
		}
		ver := ""
		if exe != "" {
			ver = diagRunVersion(ctx, exe)
		}
		procs = append(procs, map[string]interface{}{"pid": pid, "cmdline": cmdline, "exe": exe, "version": ver})
		emit(DiagEvent{
			Type:     "finding",
			Check:    "running-procs",
			Severity: DiagInfo,
			Message:  fmt.Sprintf("pid=%s exe=%s version=%s", pid, exe, ver),
			Data:     map[string]interface{}{"pid": pid, "exe": exe, "version": ver, "cmdline": cmdline},
		})
	}
	if len(procs) == 0 {
		emit(DiagEvent{Type: "finding", Check: "running-procs", Severity: DiagWarning, Message: "pgrep succeeded but yielded no parseable processes."})
	} else if len(procs) > 1 {
		emit(DiagEvent{
			Type:     "finding",
			Check:    "running-procs",
			Severity: DiagWarning,
			Message:  fmt.Sprintf("%d yaver serve processes — expected 1.", len(procs)),
		})
	} else {
		emit(DiagEvent{Type: "finding", Check: "running-procs", Severity: DiagOK, Message: "Exactly one yaver serve process running."})
	}
}

func checkPorts(ctx context.Context, emit DiagEmit) {
	ports := []int{18080, 4433, 18443}
	for _, p := range ports {
		addr := fmt.Sprintf("127.0.0.1:%d", p)
		d := net.Dialer{Timeout: 500 * time.Millisecond}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			emit(DiagEvent{
				Type:     "finding",
				Check:    "ports",
				Severity: DiagWarning,
				Message:  fmt.Sprintf("%s — not reachable (%s)", addr, err.Error()),
				Data:     map[string]interface{}{"port": p, "reachable": false},
			})
			continue
		}
		conn.Close()
		emit(DiagEvent{
			Type:     "finding",
			Check:    "ports",
			Severity: DiagOK,
			Message:  fmt.Sprintf("%s — listening.", addr),
			Data:     map[string]interface{}{"port": p, "reachable": true},
		})
	}
}

func checkAuthState(agent *HTTPServer) func(context.Context, DiagEmit) {
	return func(ctx context.Context, emit DiagEmit) {
		cfg, err := LoadConfig()
		if err != nil || cfg == nil {
			emit(DiagEvent{Type: "finding", Check: "auth-state", Severity: DiagFailure, Message: fmt.Sprintf("config load failed: %v", err)})
			return
		}
		if strings.TrimSpace(cfg.AuthToken) == "" {
			emit(DiagEvent{Type: "finding", Check: "auth-state", Severity: DiagWarning, Message: "No auth_token on disk. Run `yaver auth` to sign in."})
			return
		}
		if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
			emit(DiagEvent{Type: "finding", Check: "auth-state", Severity: DiagWarning, Message: "auth_token present but convex_site_url is empty — cannot validate remotely."})
			return
		}
		if err := ValidateToken(cfg.ConvexSiteURL, cfg.AuthToken); err != nil {
			emit(DiagEvent{Type: "finding", Check: "auth-state", Severity: DiagFailure, Message: fmt.Sprintf("Token rejected by Convex: %v", err)})
			return
		}
		emit(DiagEvent{
			Type:     "finding",
			Check:    "auth-state",
			Severity: DiagOK,
			Message:  fmt.Sprintf("Auth token valid against %s.", cfg.ConvexSiteURL),
			Data:     map[string]interface{}{"convexSiteUrl": cfg.ConvexSiteURL, "deviceId": cfg.DeviceID},
		})
	}
}

func checkWorkspace(agent *HTTPServer) func(context.Context, DiagEmit) {
	return func(ctx context.Context, emit DiagEmit) {
		root := ""
		if agent != nil && agent.taskMgr != nil {
			root = strings.TrimSpace(agent.taskMgr.workDir)
		}
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		if root == "" {
			emit(DiagEvent{Type: "finding", Check: "workspace", Severity: DiagWarning, Message: "No work-dir resolvable."})
			return
		}
		manifestPath := filepath.Join(root, WorkspaceManifestPath)
		if _, err := os.Stat(manifestPath); err != nil {
			emit(DiagEvent{Type: "finding", Check: "workspace", Severity: DiagWarning, Message: fmt.Sprintf("No yaver.workspace.yaml at %s — vibing by projectName will not resolve.", root)})
			return
		}
		m, err := LoadWorkspaceManifest(root)
		if err != nil {
			emit(DiagEvent{Type: "finding", Check: "workspace", Severity: DiagFailure, Message: fmt.Sprintf("yaver.workspace.yaml failed to parse: %v", err)})
			return
		}
		names := make([]string, 0, len(m.Apps))
		for _, a := range m.Apps {
			names = append(names, a.Name)
		}
		emit(DiagEvent{
			Type:     "finding",
			Check:    "workspace",
			Severity: DiagOK,
			Message:  fmt.Sprintf("Workspace at %s declares %d apps: %s.", manifestPath, len(m.Apps), strings.Join(names, ", ")),
			Data:     map[string]interface{}{"root": root, "manifest": manifestPath, "apps": names},
		})
	}
}

func checkSystemdUnit(ctx context.Context, emit DiagEmit) {
	if runtime.GOOS != "linux" {
		return
	}
	candidates := []string{
		"/etc/systemd/system/yaver.service",
		filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user", "yaver.service"),
		"/root/.config/systemd/user/yaver.service",
	}
	for _, unit := range candidates {
		data, err := os.ReadFile(unit)
		if err != nil {
			continue
		}
		var exec string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "ExecStart=") {
				exec = strings.TrimSpace(line)
				break
			}
		}
		emit(DiagEvent{
			Type:     "finding",
			Check:    "systemd-unit",
			Severity: DiagInfo,
			Message:  fmt.Sprintf("%s — %s", unit, exec),
			Data:     map[string]interface{}{"unit": unit, "execStart": exec},
		})
	}
}

func checkRuntimeDeps(ctx context.Context, emit DiagEmit) {
	deps := []string{"git", "node", "docker", "claude", "codex", "aider", "ollama"}
	missing := []string{}
	for _, d := range deps {
		if _, err := exec.LookPath(d); err != nil {
			missing = append(missing, d)
			emit(DiagEvent{Type: "finding", Check: "runtime-deps", Severity: DiagInfo, Message: fmt.Sprintf("%s — not on PATH", d)})
		} else {
			emit(DiagEvent{Type: "finding", Check: "runtime-deps", Severity: DiagOK, Message: fmt.Sprintf("%s — present", d)})
		}
	}
	if len(missing) > 0 {
		emit(DiagEvent{
			Type:     "finding",
			Check:    "runtime-deps",
			Severity: DiagInfo,
			Message:  fmt.Sprintf("Missing on PATH: %s. Run `yaver install %s` if relevant.", strings.Join(missing, ", "), strings.Join(missing, ",")),
		})
	}
}

// ─── Helpers ───────────────────────────────────────────────────────

func diagYaverBinaryCandidates() []string {
	paths := []string{
		"/usr/bin/yaver",
		"/usr/local/bin/yaver",
		"/opt/homebrew/bin/yaver",
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".local", "bin", "yaver"))
		// Self-update stashes versioned binaries under ~/.yaver/bin/<ver>/<platform>/yaver
		if matches, _ := filepath.Glob(filepath.Join(home, ".yaver", "bin", "*", "*", "yaver")); len(matches) > 0 {
			paths = append(paths, matches...)
		}
	}
	if p, err := exec.LookPath("yaver"); err == nil {
		paths = append(paths, p)
	}
	// Dedupe (Go maps, ordered).
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err == nil {
			abs = resolved
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}

func diagRunVersion(ctx context.Context, path string) string {
	cmd := exec.CommandContext(ctx, path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// diagNormaliseBinaries is a best-effort fix-up used when --fix is
// passed. Makes `/usr/bin/yaver` a symlink to whichever newer binary
// the agent found (assumed to be `/usr/local/bin/yaver` on Debian/Ubuntu
// .deb installs).
func diagNormaliseBinaries(seen map[string]string, emit DiagEmit) {
	local := "/usr/local/bin/yaver"
	targetVer, ok := seen[local]
	if !ok {
		emit(DiagEvent{Type: "finding", Check: "binary-paths", Severity: DiagInfo, Message: "fix: skipped — no /usr/local/bin/yaver to symlink from"})
		return
	}
	if seen["/usr/bin/yaver"] == targetVer {
		emit(DiagEvent{Type: "finding", Check: "binary-paths", Severity: DiagInfo, Message: "fix: /usr/bin/yaver already matches /usr/local/bin/yaver"})
		return
	}
	// Need root to write to /usr/bin; if we can't, just say so.
	tmp := "/usr/bin/yaver.yaver-diag.tmp"
	if err := os.Symlink(local, tmp); err != nil {
		emit(DiagEvent{Type: "finding", Check: "binary-paths", Severity: DiagWarning, Message: fmt.Sprintf("fix: could not create symlink (need root?): %v", err)})
		return
	}
	if err := os.Rename(tmp, "/usr/bin/yaver"); err != nil {
		os.Remove(tmp)
		emit(DiagEvent{Type: "finding", Check: "binary-paths", Severity: DiagWarning, Message: fmt.Sprintf("fix: rename failed: %v", err)})
		return
	}
	emit(DiagEvent{Type: "finding", Check: "binary-paths", Severity: DiagOK, Message: fmt.Sprintf("fix: /usr/bin/yaver now -> /usr/local/bin/yaver (%s)", targetVer)})
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func firstKey(m map[string]bool) string {
	for k := range m {
		return k
	}
	return ""
}

// WriteDiagnoseJSONLines writes each event as a line-delimited JSON
// frame to w. Used by the CLI when --json is set and by the SSE
// handler with w wrapped in an SSE framing writer.
func WriteDiagnoseJSONLines(w io.Writer, ev DiagEvent) {
	enc := json.NewEncoder(w)
	_ = enc.Encode(ev)
	if f, ok := w.(interface{ Flush() }); ok {
		f.Flush()
	}
}
