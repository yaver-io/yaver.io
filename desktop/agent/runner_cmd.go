package main

// runner_cmd.go — `yaver runner` CLI subcommand. Phase 1 implements
// the read-first surface (list, pools, runs, logs) plus the
// authoring-side trigger / add / remove / pause. Stays thin: every
// command is a small wrapper around the local agent's HTTP route, so
// behavioural drift between CLI and HTTP is impossible.
//
//   yaver runner                           # show usage
//   yaver runner list [--pool <p>]
//   yaver runner pools
//   yaver runner show <name>
//   yaver runner add  <name> --command='...' [--pool=p] [--project=app] [--workdir=.] [--timeout=N] [--env=K=V]
//   yaver runner remove <name>
//   yaver runner trigger <name>
//   yaver runner pause <name> [--off]      # default pauses; --off resumes
//   yaver runner runs [<name>] [--limit=N]
//   yaver runner logs <run-id>             # full log to stdout
//
// All requests use opsLocalRequest from ops_cmd.go so the daemon is
// auto-spawned if it isn't running.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func runRunner(args []string) {
	if len(args) == 0 {
		runnerUsage()
		return
	}
	if target, runner, extra, ok := parseRunnerAuthQuickFlow(args); ok {
		runRunnerQuickFlow(target, runner, extra)
		return
	}
	// `yaver runner <hint> status [--json]` — same dispatch shape as
	// the runner-auth quick flow but `status` instead of a runner name.
	// Must come BEFORE the switch below so the `<hint>` token isn't
	// mistaken for a top-level runner subcommand.
	if hint, sub, extra, ok := parseRunnerStatusFlow(args); ok && sub == "status" {
		runRemoteAgentStatusByHint(hint, runnerHasFlag(extra, "--json"))
		return
	}
	switch args[0] {
	case "help", "-h", "--help":
		runnerUsage()
	case "list", "ls":
		runRunnerList(args[1:])
	case "pools":
		runRunnerPools()
	case "show", "get":
		runRunnerShow(args[1:])
	case "add", "create", "set":
		runRunnerAdd(args[1:])
	case "remove", "rm", "delete":
		runRunnerRemove(args[1:])
	case "trigger", "run", "fire":
		runRunnerTrigger(args[1:])
	case "pause", "resume":
		runRunnerPause(args[0], args[1:])
	case "runs", "history":
		runRunnerRuns(args[1:])
	case "logs", "log":
		runRunnerLogs(args[1:])
	case "sandbox":
		runRunnerSandbox(args[1:])
	case "agent":
		runRunnerAgent(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown runner subcommand %q — try `yaver runner help`\n", args[0])
		os.Exit(2)
	}
}

func runnerUsage() {
	fmt.Print(`yaver runner — unified self-hosted runner (RUNNER_DEV.md)

Usage:
  yaver runner <deviceId|name|alias> <claude|claude-code|codex>
  yaver runner <deviceId|name|alias> status [--json]
                                          Show that device's live agent
                                          status (version, lifecycle,
                                          runners, dev-server)
  yaver runner list [--pool=<p>]
  yaver runner pools
  yaver runner show <name>
  yaver runner add <name> --command='...' [--pool=p] [--project=app] [--workdir=.] [--timeout=N] [--env=K=V ...]
  yaver runner remove <name>
  yaver runner trigger <name>
  yaver runner pause <name> [--off]
  yaver runner runs [<name>] [--limit=N]
  yaver runner logs <run-id>

  # Phase 2: long-lived Docker sandboxes (e2b/Modal substitute).
  yaver runner sandbox start [--label=tag] [--workdir=/path] [--ttl=900] [--image=name]
  yaver runner sandbox list
  yaver runner sandbox exec <id> -- <command>
  yaver runner sandbox stop <id>

  # Phase 2: Devin-shape coding-agent sessions.
  yaver runner agent start --prompt='...' [--workdir=/path] [--runner=claude-code|codex|aider|hybrid]
  yaver runner agent list
  yaver runner agent show <id>
  yaver runner agent message <id> --text='...'
  yaver runner agent cancel <id>
  yaver runner agent delete <id>

Remote coding-agent shortcut:
  yaver runner test codex
  yaver runner test claude-code

  Resolves the target machine alias/device, checks local Yaver auth,
  checks the target machine's Yaver auth, runs remote 'yaver auth
  --headless' over 'yaver ssh' when needed, starts the remote Codex /
  Claude auth flow, and switches that machine's active coding runner.

Phase 1 ships shell jobs; Phase 2 adds sandboxes + agent sessions on
top of container_runner.go and the existing TaskManager runners.
Future kinds (docker / playwright / gpu) reserved in the API and
refuse to execute until later phases enable them.
`)
}

func runRunnerList(args []string) {
	pool := runnerFlag(args, "--pool")
	path := "/runner/jobs"
	if pool != "" {
		path += "?pool=" + pool
	}
	body, status := runnerLocalGet(path)
	if status != 200 {
		runnerExitWithBody(status, body)
	}
	var parsed struct {
		Jobs  []RunnerJob `json:"jobs"`
		Count int         `json:"count"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		fmt.Println(string(body))
		return
	}
	if parsed.Count == 0 {
		fmt.Println("no jobs registered")
		return
	}
	fmt.Printf("%-24s %-10s %-12s %-12s %s\n", "NAME", "KIND", "POOL", "PROJECT", "STATUS")
	for _, j := range parsed.Jobs {
		status := "active"
		if j.Paused {
			status = "paused"
		}
		fmt.Printf("%-24s %-10s %-12s %-12s %s\n", runnerTrunc(j.Name, 24), string(j.Kind), runnerTrunc(j.Pool, 12), runnerTrunc(j.Project, 12), status)
	}
}

func runRunnerPools() {
	body, status := runnerLocalGet("/runner/pools")
	if status != 200 {
		runnerExitWithBody(status, body)
	}
	runnerPrintJSON(body)
}

func runRunnerShow(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner show <name>")
		os.Exit(2)
	}
	body, status := runnerLocalGet("/runner/jobs/" + args[0])
	if status != 200 {
		runnerExitWithBody(status, body)
	}
	runnerPrintJSON(body)
}

func runRunnerAdd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner add <name> --command='...' [flags]")
		os.Exit(2)
	}
	name := args[0]
	rest := args[1:]
	job := RunnerJob{
		Name:    name,
		Kind:    RunnerJobShell,
		Command: runnerFlag(rest, "--command"),
		Pool:    runnerFlagDefault(rest, "--pool", "any"),
		Project: runnerFlag(rest, "--project"),
		WorkDir: runnerFlag(rest, "--workdir"),
	}
	if to := runnerFlag(rest, "--timeout"); to != "" {
		fmt.Sscanf(to, "%d", &job.TimeoutSec)
	}
	envs := runnerFlagAll(rest, "--env")
	if len(envs) > 0 {
		job.Env = map[string]string{}
		for _, kv := range envs {
			eq := strings.IndexByte(kv, '=')
			if eq <= 0 {
				continue
			}
			job.Env[kv[:eq]] = kv[eq+1:]
		}
	}
	if job.Command == "" {
		fmt.Fprintln(os.Stderr, "--command is required")
		os.Exit(2)
	}
	body, _ := json.Marshal(job)
	resp, status := runnerLocalRequest("POST", "/runner/jobs", body)
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	fmt.Println("ok")
}

func runRunnerRemove(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner remove <name>")
		os.Exit(2)
	}
	resp, status := runnerLocalRequest("DELETE", "/runner/jobs/"+args[0], nil)
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	fmt.Println("removed")
}

func runRunnerTrigger(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner trigger <name>")
		os.Exit(2)
	}
	resp, status := runnerLocalRequest("POST", "/runner/jobs/"+args[0]+"/trigger", []byte("{}"))
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	runnerPrintJSON(resp)
	// Exit 1 on a non-OK run so script wrappers can branch.
	var parsed struct {
		OK bool `json:"ok"`
	}
	_ = json.Unmarshal(resp, &parsed)
	if !parsed.OK {
		os.Exit(1)
	}
}

func runRunnerPause(verb string, args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: yaver runner %s <name>\n", verb)
		os.Exit(2)
	}
	paused := verb == "pause"
	if runnerHasFlag(args, "--off") {
		paused = false
	}
	body, _ := json.Marshal(map[string]bool{"paused": paused})
	resp, status := runnerLocalRequest("POST", "/runner/jobs/"+args[0]+"/pause", body)
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	if paused {
		fmt.Println("paused")
	} else {
		fmt.Println("resumed")
	}
}

func runRunnerRuns(args []string) {
	limitArg := runnerFlag(args, "--limit")
	jobName := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			jobName = a
			break
		}
	}
	path := "/runner/runs"
	if jobName != "" {
		path = "/runner/jobs/" + jobName + "/runs"
	}
	if limitArg != "" {
		if strings.Contains(path, "?") {
			path += "&limit=" + limitArg
		} else {
			path += "?limit=" + limitArg
		}
	}
	body, status := runnerLocalGet(path)
	if status != 200 {
		runnerExitWithBody(status, body)
	}
	var parsed struct {
		Runs  []RunnerRun `json:"runs"`
		Count int         `json:"count"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		fmt.Println(string(body))
		return
	}
	if parsed.Count == 0 {
		fmt.Println("no runs")
		return
	}
	fmt.Printf("%-18s %-22s %-7s %-10s %s\n", "ID", "JOB", "STATUS", "DURATION", "STARTED")
	for _, r := range parsed.Runs {
		status := "ok"
		if !r.OK {
			if r.InProgress {
				status = "run.."
			} else {
				status = "fail"
			}
		}
		fmt.Printf("%-18s %-22s %-7s %-10s %s\n",
			r.ID,
			runnerTrunc(r.JobName, 22),
			status,
			fmt.Sprintf("%dms", r.DurationMs),
			runnerFmtUnixMs(r.StartedAt))
	}
}

func runRunnerLogs(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner logs <run-id>")
		os.Exit(2)
	}
	body, status := runnerLocalGet("/runner/runs/" + args[0] + "/log")
	if status != 200 {
		runnerExitWithBody(status, body)
	}
	os.Stdout.Write(body)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		os.Stdout.WriteString("\n")
	}
}

// --- helpers ---

func runnerLocalGet(path string) ([]byte, int) {
	return runnerLocalRequest("GET", path, nil)
}

func runnerLocalRequest(method, path string, body []byte) ([]byte, int) {
	token, err := opsLoadToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return opsLocalRequest(context.Background(), method, path, token, body)
}

func runnerExitWithBody(status int, body []byte) {
	fmt.Fprintf(os.Stderr, "HTTP %d\n%s\n", status, string(body))
	os.Exit(1)
}

func runnerPrintJSON(body []byte) {
	var v interface{}
	if err := json.Unmarshal(body, &v); err != nil {
		os.Stdout.Write(body)
		os.Stdout.WriteString("\n")
		return
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	os.Stdout.Write(out)
	os.Stdout.WriteString("\n")
}

func runnerFlag(args []string, name string) string {
	for i, a := range args {
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func runnerFlagDefault(args []string, name, def string) string {
	if v := runnerFlag(args, name); v != "" {
		return v
	}
	return def
}

func runnerFlagAll(args []string, name string) []string {
	var out []string
	for i, a := range args {
		if strings.HasPrefix(a, name+"=") {
			out = append(out, strings.TrimPrefix(a, name+"="))
		} else if a == name && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

// parseRunnerStatusFlow recognizes `yaver runner <hint> status [flags]`
// — the alias-targeted status pull. Returns ok=false when args[1] isn't
// the literal token "status", so the runner-auth quick flow keeps
// `yaver runner <hint> claude-code` and friends.
func parseRunnerStatusFlow(args []string) (target string, sub string, extra []string, ok bool) {
	if len(args) < 2 {
		return "", "", nil, false
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "status", "ls", "list", "set", "setup", "help", "-h", "--help",
		"add", "create", "remove", "rm", "delete", "trigger", "run", "fire",
		"pause", "resume", "runs", "history", "logs", "log", "sandbox", "agent":
		return "", "", nil, false
	}
	if strings.ToLower(strings.TrimSpace(args[1])) != "status" {
		return "", "", nil, false
	}
	return strings.TrimSpace(args[0]), "status", args[2:], true
}

func runnerHasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func runnerTrunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func runnerFmtUnixMs(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.Unix(ms/1000, (ms%1000)*int64(time.Millisecond)).Format("2006-01-02 15:04:05")
}

// --- Phase 2: sandbox CLI ---

func runRunnerSandbox(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner sandbox {start|list|exec|stop} ...")
		os.Exit(2)
	}
	switch args[0] {
	case "start":
		runRunnerSandboxStart(args[1:])
	case "list", "ls":
		runRunnerSandboxList()
	case "exec":
		runRunnerSandboxExec(args[1:])
	case "stop", "kill", "rm":
		runRunnerSandboxStop(args[1:])
	case "show", "get":
		runRunnerSandboxShow(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown sandbox subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func runRunnerSandboxStart(args []string) {
	body := map[string]interface{}{
		"label":        runnerFlag(args, "--label"),
		"workspaceDir": runnerFlag(args, "--workdir"),
		"image":        runnerFlag(args, "--image"),
		"networkMode":  runnerFlag(args, "--network"),
	}
	if ttl := runnerFlag(args, "--ttl"); ttl != "" {
		var n int
		fmt.Sscanf(ttl, "%d", &n)
		body["ttlSec"] = n
	}
	if runnerHasFlag(args, "--readonly") {
		body["readOnly"] = true
	}
	buf, _ := json.Marshal(body)
	resp, status := runnerLocalRequest("POST", "/runner/sandboxes", buf)
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	runnerPrintJSON(resp)
}

func runRunnerSandboxList() {
	body, status := runnerLocalGet("/runner/sandboxes")
	if status != 200 {
		runnerExitWithBody(status, body)
	}
	var parsed struct {
		Sessions []SandboxSession `json:"sessions"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		fmt.Println(string(body))
		return
	}
	if parsed.Count == 0 {
		fmt.Println("no sandboxes")
		return
	}
	fmt.Printf("%-18s %-14s %-22s %-12s %s\n", "ID", "LABEL", "IMAGE", "NETWORK", "CREATED")
	for _, s := range parsed.Sessions {
		fmt.Printf("%-18s %-14s %-22s %-12s %s\n",
			s.ID,
			runnerTrunc(s.Label, 14),
			runnerTrunc(s.Image, 22),
			runnerTrunc(s.NetworkMode, 12),
			runnerFmtUnixMs(s.CreatedAt))
	}
}

func runRunnerSandboxExec(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner sandbox exec <id> -- <command>")
		os.Exit(2)
	}
	id := args[0]
	// Everything after `--` is the command; if no `--`, join args[1:].
	rest := args[1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "command is required after sandbox id")
		os.Exit(2)
	}
	body, _ := json.Marshal(map[string]interface{}{"command": strings.Join(rest, " ")})
	resp, status := runnerLocalRequest("POST", "/runner/sandboxes/"+id+"/exec", body)
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	var parsed SandboxExecResult
	if err := json.Unmarshal(resp, &parsed); err != nil {
		fmt.Println(string(resp))
		return
	}
	if parsed.Stdout != "" {
		os.Stdout.WriteString(parsed.Stdout)
		if !strings.HasSuffix(parsed.Stdout, "\n") {
			os.Stdout.WriteString("\n")
		}
	}
	if parsed.Stderr != "" {
		os.Stderr.WriteString(parsed.Stderr)
		if !strings.HasSuffix(parsed.Stderr, "\n") {
			os.Stderr.WriteString("\n")
		}
	}
	if !parsed.OK {
		os.Exit(parsed.ExitCode)
	}
}

func runRunnerSandboxStop(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner sandbox stop <id>")
		os.Exit(2)
	}
	resp, status := runnerLocalRequest("DELETE", "/runner/sandboxes/"+args[0], nil)
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	fmt.Println("stopped")
}

func runRunnerSandboxShow(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner sandbox show <id>")
		os.Exit(2)
	}
	body, status := runnerLocalGet("/runner/sandboxes/" + args[0])
	if status != 200 {
		runnerExitWithBody(status, body)
	}
	runnerPrintJSON(body)
}

// --- Phase 2: agent session CLI ---

func runRunnerAgent(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner agent {start|list|show|message|cancel|delete} ...")
		os.Exit(2)
	}
	switch args[0] {
	case "start", "create":
		runRunnerAgentStart(args[1:])
	case "list", "ls":
		runRunnerAgentList()
	case "show", "get":
		runRunnerAgentShow(args[1:])
	case "message", "msg", "send":
		runRunnerAgentMessage(args[1:])
	case "cancel":
		runRunnerAgentCancel(args[1:])
	case "delete", "rm":
		runRunnerAgentDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown agent subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func runRunnerAgentStart(args []string) {
	prompt := runnerFlag(args, "--prompt")
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "--prompt is required")
		os.Exit(2)
	}
	body := map[string]interface{}{
		"prompt":  prompt,
		"workDir": runnerFlag(args, "--workdir"),
		"runner":  runnerFlag(args, "--runner"),
		"engine":  runnerFlag(args, "--engine"),
		"model":   runnerFlag(args, "--model"),
		"project": runnerFlag(args, "--project"),
		"title":   runnerFlag(args, "--title"),
	}
	buf, _ := json.Marshal(body)
	resp, status := runnerLocalRequest("POST", "/runner/agent/sessions", buf)
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	runnerPrintJSON(resp)
}

func runRunnerAgentList() {
	body, status := runnerLocalGet("/runner/agent/sessions")
	if status != 200 {
		runnerExitWithBody(status, body)
	}
	var parsed struct {
		Sessions []AgentSession `json:"sessions"`
		Count    int            `json:"count"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		fmt.Println(string(body))
		return
	}
	if parsed.Count == 0 {
		fmt.Println("no agent sessions")
		return
	}
	fmt.Printf("%-22s %-14s %-12s %-22s %s\n", "ID", "RUNNER", "STATUS", "TITLE", "UPDATED")
	for _, s := range parsed.Sessions {
		fmt.Printf("%-22s %-14s %-12s %-22s %s\n",
			s.ID,
			runnerTrunc(s.Runner, 14),
			string(s.Status),
			runnerTrunc(s.Title, 22),
			runnerFmtUnixMs(s.UpdatedAt))
	}
}

func runRunnerAgentShow(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner agent show <id>")
		os.Exit(2)
	}
	body, status := runnerLocalGet("/runner/agent/sessions/" + args[0])
	if status != 200 {
		runnerExitWithBody(status, body)
	}
	runnerPrintJSON(body)
}

func runRunnerAgentMessage(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner agent message <id> --text='...'")
		os.Exit(2)
	}
	id := args[0]
	text := runnerFlag(args[1:], "--text")
	if text == "" {
		fmt.Fprintln(os.Stderr, "--text is required")
		os.Exit(2)
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	resp, status := runnerLocalRequest("POST", "/runner/agent/sessions/"+id+"/message", body)
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	runnerPrintJSON(resp)
}

func runRunnerAgentCancel(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner agent cancel <id>")
		os.Exit(2)
	}
	resp, status := runnerLocalRequest("POST", "/runner/agent/sessions/"+args[0]+"/cancel", []byte("{}"))
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	fmt.Println("cancelled")
}

func runRunnerAgentDelete(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver runner agent delete <id>")
		os.Exit(2)
	}
	resp, status := runnerLocalRequest("DELETE", "/runner/agent/sessions/"+args[0], nil)
	if status != 200 {
		runnerExitWithBody(status, resp)
	}
	fmt.Println("deleted")
}
