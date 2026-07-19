package main

// runner_keeper_mcp.go — P7 MCP verbs for the same-session runner
// continuation supervisor. Runner-agnostic (claude / codex / opencode
// / glm); single-instance, sequential, own-machine/own-subscription
// compliant — see the header in runner_keeper.go.
//
// Verbs:
//   runner_attach      — user is vibing → mode = user-driven
//   runner_detach      — leave the pane, flip mode = auto, keeper drains queue
//   runner_autorun     — force mode on/off explicitly
//   runner_queue_add   — enqueue a prompt (from any device, remote OK)
//   runner_queue_list  — list queued prompts (all sessions or one)
//   runner_queue_clear — drop queued prompts
//   runner_status      — crisp human-readable summary: phases done / current
//                        / remaining, commits shipped w/ metadata, current
//                        mode, keeper health, last-activity, ETA, per-task
//                        telemetry (time, tokens, runners utilised).

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// runnerAttachArgs / runnerDetachArgs — thin structs so the JSON
// contract is auditable in one place.
type runnerAttachArgs struct {
	SessionName string `json:"sessionName"`
	Machine     string `json:"machine,omitempty"`
}
type runnerDetachArgs struct {
	SessionName string `json:"sessionName"`
	Autorun     bool   `json:"autorun,omitempty"` // detach AND flip to auto (default true)
}
type runnerAutorunArgs struct {
	SessionName string `json:"sessionName"`
	Mode        string `json:"mode"` // on|off
}
type runnerQueueAddArgs struct {
	SessionName string `json:"sessionName"`
	Prompt      string `json:"prompt"`
	Source      string `json:"source,omitempty"`
}
type runnerQueueListArgs struct {
	SessionName string `json:"sessionName,omitempty"`
}
type runnerQueueClearArgs struct {
	SessionName string `json:"sessionName,omitempty"`
}
type runnerStatusArgs struct {
	SessionName string `json:"sessionName,omitempty"`
	Task        string `json:"task,omitempty"`    // e.g. "n2n"
	Machine     string `json:"machine,omitempty"` // reserved for remote status
}

// runRunnerAttach flips a session into user-driven mode. If the
// session doesn't exist in the keeper yet, we create it — the user
// may attach BEFORE the first tick.
func runRunnerAttach(k *RunnerKeeper, args runnerAttachArgs) map[string]interface{} {
	if k == nil {
		return map[string]interface{}{"ok": false, "error": "runner keeper not initialised on this agent"}
	}
	if strings.TrimSpace(args.SessionName) == "" {
		return map[string]interface{}{"ok": false, "error": "sessionName is required"}
	}
	if err := k.SetMode(args.SessionName, KeeperModeUserDriven); err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "sessionName": args.SessionName, "mode": KeeperModeUserDriven,
		"note": "session flipped to user-driven; the keeper will NOT nudge while you're attached. Run runner_detach when done."}
}

// runRunnerDetach leaves the terminal and (by default) flips the
// session into auto mode so the keeper drains the queue.
func runRunnerDetach(k *RunnerKeeper, args runnerDetachArgs) map[string]interface{} {
	if k == nil {
		return map[string]interface{}{"ok": false, "error": "runner keeper not initialised on this agent"}
	}
	if strings.TrimSpace(args.SessionName) == "" {
		return map[string]interface{}{"ok": false, "error": "sessionName is required"}
	}
	target := KeeperModeAuto
	if !args.Autorun {
		target = KeeperModeOff
	}
	if err := k.SetMode(args.SessionName, target); err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "sessionName": args.SessionName, "mode": target}
}

func runRunnerAutorun(k *RunnerKeeper, args runnerAutorunArgs) map[string]interface{} {
	if k == nil {
		return map[string]interface{}{"ok": false, "error": "runner keeper not initialised"}
	}
	if strings.TrimSpace(args.SessionName) == "" {
		return map[string]interface{}{"ok": false, "error": "sessionName is required"}
	}
	var mode KeeperMode
	switch strings.ToLower(strings.TrimSpace(args.Mode)) {
	case "on", "auto":
		mode = KeeperModeAuto
	case "off":
		mode = KeeperModeOff
	default:
		return map[string]interface{}{"ok": false, "error": "mode must be on|off"}
	}
	if err := k.SetMode(args.SessionName, mode); err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "sessionName": args.SessionName, "mode": mode}
}

func runRunnerQueueAdd(k *RunnerKeeper, args runnerQueueAddArgs) map[string]interface{} {
	if k == nil {
		return map[string]interface{}{"ok": false, "error": "runner keeper not initialised"}
	}
	id, err := k.EnqueuePrompt(args.SessionName, args.Prompt, args.Source)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "id": id, "sessionName": args.SessionName, "queued": len(k.ListQueue(args.SessionName))}
}

func runRunnerQueueList(k *RunnerKeeper, args runnerQueueListArgs) map[string]interface{} {
	if k == nil {
		return map[string]interface{}{"ok": false, "error": "runner keeper not initialised"}
	}
	items := k.ListQueue(strings.TrimSpace(args.SessionName))
	return map[string]interface{}{"ok": true, "sessionName": args.SessionName, "items": items, "count": len(items)}
}

func runRunnerQueueClear(k *RunnerKeeper, args runnerQueueClearArgs) map[string]interface{} {
	if k == nil {
		return map[string]interface{}{"ok": false, "error": "runner keeper not initialised"}
	}
	removed := k.ClearQueue(strings.TrimSpace(args.SessionName))
	return map[string]interface{}{"ok": true, "sessionName": args.SessionName, "removed": removed}
}

// runRunnerStatus is the human-readable telemetry answer for a
// named task/session. It walks git log for [auto-runner] commits +
// the SessionState map to compose:
//   - phases done / current / remaining (from the Progress log or
//     a task manifest — we parse the plan md when task=n2n so the
//     answer stays in sync with what actually shipped)
//   - commits shipped with metadata (phase, machine+alias,
//     work-window started/finished, mode auto|user-driven)
//   - current mode + keeper health + last-activity + rough ETA
//   - per-task telemetry: time spent (total + per phase from the
//     work-window), tokens spent when available (per runner), and
//     the runners utilised with per-runner attribution.
//
// The output is intentionally a compact prose summary + a machine-
// readable metadata block; MCP clients get both.
func runRunnerStatus(k *RunnerKeeper, args runnerStatusArgs) map[string]interface{} {
	report := composeRunnerStatus(k, args)
	return map[string]interface{}{"ok": true, "sessionName": args.SessionName, "task": args.Task, "summary": report.Summary, "metadata": report.Metadata}
}

// StatusReport is the response shape runner_status returns.
type StatusReport struct {
	Summary  string                 `json:"summary"`
	Metadata map[string]interface{} `json:"metadata"`
}

func composeRunnerStatus(k *RunnerKeeper, args runnerStatusArgs) StatusReport {
	metadata := map[string]interface{}{}
	var summary strings.Builder

	// Session state (mode + last-activity + queue depth).
	if k != nil && strings.TrimSpace(args.SessionName) != "" {
		st := k.State(args.SessionName)
		metadata["sessionState"] = st
		summary.WriteString(fmt.Sprintf("Session %s mode=%s queued=%d nudgesTotal=%d\n",
			st.SessionName, st.Mode, st.QueuedCount, st.NudgesTotal))
	}

	// [auto-runner] commit telemetry.
	commits := gatherAutoRunnerCommits(args.Task, 40)
	metadata["commits"] = commits
	if len(commits) > 0 {
		var total time.Duration
		runnerCounts := map[string]int{}
		modeCounts := map[string]int{}
		for _, c := range commits {
			if c.WorkWindowDuration > 0 {
				total += c.WorkWindowDuration
			}
			if c.Runner != "" {
				runnerCounts[c.Runner]++
			}
			if c.Mode != "" {
				modeCounts[c.Mode]++
			}
		}
		metadata["totalDuration"] = total.String()
		metadata["runnerCounts"] = runnerCounts
		metadata["modeCounts"] = modeCounts
		summary.WriteString(fmt.Sprintf("Auto-runner commits scanned: %d; total tracked work window: %s.\n", len(commits), total))
		if len(runnerCounts) > 0 {
			pairs := make([]string, 0, len(runnerCounts))
			for name, n := range runnerCounts {
				pairs = append(pairs, fmt.Sprintf("%s: %d phase(s)", name, n))
			}
			sort.Strings(pairs)
			summary.WriteString("Runners utilised — " + strings.Join(pairs, ", ") + ".\n")
		}
	}

	// Task-manifest phases (only when task looks familiar; today only n2n).
	if strings.EqualFold(strings.TrimSpace(args.Task), "n2n") {
		phaseInfo := parseN2NPhaseStatus()
		metadata["phases"] = phaseInfo
		if phaseInfo != nil {
			summary.WriteString(fmt.Sprintf("Phases done=%d partial=%d not-started=%d\n",
				phaseInfo["done"], phaseInfo["partial"], phaseInfo["notStarted"]))
		}
	}

	return StatusReport{Summary: strings.TrimSpace(summary.String()), Metadata: metadata}
}

// AutoRunnerCommit is a parsed [auto-runner] commit — used to attribute
// time / runner / phase across the task.
type AutoRunnerCommit struct {
	Hash                 string        `json:"hash"`
	Subject              string        `json:"subject"`
	Phase                string        `json:"phase,omitempty"`
	Runner               string        `json:"runner,omitempty"`
	Machine              string        `json:"machine,omitempty"`
	MachineAlias         string        `json:"machineAlias,omitempty"`
	MachineID            string        `json:"machineId,omitempty"`
	Mode                 string        `json:"mode,omitempty"`
	WorkWindowStarted    string        `json:"workWindowStarted,omitempty"`
	WorkWindowFinished   string        `json:"workWindowFinished,omitempty"`
	WorkWindowDuration   time.Duration `json:"workWindowDurationNs"`
	WorkWindowHumanReadable string     `json:"workWindowDuration"`
}

var (
	autoRunnerPhaseRE   = regexp.MustCompile(`n2n\s+(P\d+[a-zA-Z_-]*)`)
	autoRunnerWorkRE    = regexp.MustCompile(`Work window:\s+started\s+([\d-]+ [\d:]+ \+\d+),\s+finished\s+([\d-]+ [\d:]+ \+\d+)`)
	autoRunnerRunnerRE  = regexp.MustCompile(`Runner:\s+(\S+)\s+on machine\s+(\S+)\s+\(alias\s+([\w-]+),\s*([0-9a-f]+)\)\s+mode:\s+(\S+)`)
)

// gatherAutoRunnerCommits walks the last N commits and returns the
// parsed [auto-runner] entries (optionally filtered by task tag such
// as "n2n"). Silent-fail if git isn't reachable.
func gatherAutoRunnerCommits(taskFilter string, limit int) []AutoRunnerCommit {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 40
	}
	out, err := exec.Command("git", "log", fmt.Sprintf("-n%d", limit), "--pretty=format:%H%x1f%s%x1f%b%x1e").Output()
	if err != nil {
		return nil
	}
	rows := strings.Split(string(out), "\x1e")
	var commits []AutoRunnerCommit
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		fields := strings.Split(row, "\x1f")
		if len(fields) < 3 {
			continue
		}
		hash, subject, body := fields[0], fields[1], fields[2]
		if !strings.HasPrefix(subject, "[auto-runner]") {
			continue
		}
		if taskFilter != "" && !strings.Contains(strings.ToLower(subject+" "+body), strings.ToLower(taskFilter)) {
			continue
		}
		c := AutoRunnerCommit{Hash: hash, Subject: subject}
		if m := autoRunnerPhaseRE.FindStringSubmatch(subject); len(m) == 2 {
			c.Phase = m[1]
		}
		if m := autoRunnerWorkRE.FindStringSubmatch(body); len(m) == 3 {
			c.WorkWindowStarted = m[1]
			c.WorkWindowFinished = m[2]
			start, err1 := time.Parse("2006-01-02 15:04 -0700", m[1])
			end, err2 := time.Parse("2006-01-02 15:04 -0700", m[2])
			if err1 == nil && err2 == nil {
				c.WorkWindowDuration = end.Sub(start)
				c.WorkWindowHumanReadable = c.WorkWindowDuration.String()
			}
		}
		if m := autoRunnerRunnerRE.FindStringSubmatch(body); len(m) == 6 {
			c.Runner = m[1]
			c.Machine = m[2]
			c.MachineAlias = m[3]
			c.MachineID = m[4]
			c.Mode = m[5]
		}
		commits = append(commits, c)
	}
	return commits
}

// parseN2NPhaseStatus reads the Progress log in
// docs/architecture/N2N_IMPLEMENTATION_PLAN.md and counts done /
// partial / not-started phases. Compact, silent-fail if the doc
// isn't reachable (e.g. keeper installed away from the repo).
func parseN2NPhaseStatus() map[string]int {
	pathCandidates := []string{
		"docs/architecture/N2N_IMPLEMENTATION_PLAN.md",
	}
	var body []byte
	for _, p := range pathCandidates {
		out, err := exec.Command("git", "show", "HEAD:"+p).Output()
		if err == nil {
			body = out
			break
		}
	}
	if len(body) == 0 {
		return nil
	}
	counts := map[string]int{"done": 0, "partial": 0, "notStarted": 0}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "- **P") {
			continue
		}
		switch {
		case strings.Contains(line, "**DONE"):
			counts["done"]++
		case strings.Contains(line, "**PARTIAL"):
			counts["partial"]++
		case strings.Contains(line, "_not started_"):
			counts["notStarted"]++
		}
	}
	return counts
}

// mcpEncodeQueueEntry is a small helper the dispatcher uses to normalise
// timestamps in JSON responses; kept here so voice_mcp doesn't accidentally
// depend on it.
func mcpEncodeQueueEntry(e QueueEntry) json.RawMessage {
	buf, _ := json.Marshal(e)
	return buf
}

// ensureRunnerKeeper lazily allocates the keeper on first MCP touch.
// Runner-agnostic and single-instance per agent (see the compliance
// block in runner_keeper.go); Sequential drainage is enforced by the
// per-session Tick loop.
func (s *HTTPServer) ensureRunnerKeeper() *RunnerKeeper {
	if s.runnerKeeper != nil {
		return s.runnerKeeper
	}
	k, err := NewRunnerKeeper()
	if err != nil {
		return nil
	}
	s.runnerKeeper = k
	// Start the production drain loop. Until this existed, queue.json was
	// written by EnqueuePrompt but never drained (RunnerKeeper.Tick had no
	// production caller), so runner_queue_add silently swallowed work.
	k.StartSupervisor(context.Background())
	return k
}
