package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// morning_cmd.go — `yaver morning` and `yaver record` CLIs, plus the
// doctor hook that lists recording driver availability. Thin over the
// store / manager; every behaviour is also reachable via HTTP + MCP
// so the feature is never CLI-only.

func runMorning(args []string) {
	if len(args) == 0 {
		args = []string{"latest"}
	}
	switch args[0] {
	case "list", "ls":
		morningList()
	case "latest":
		morningLatest()
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver morning show <run-id>")
			os.Exit(1)
		}
		morningShow(args[1])
	case "rollback":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: yaver morning rollback <run-id> <task-id>")
			os.Exit(1)
		}
		morningRollback(args[1], args[2])
	case "help", "-h", "--help":
		printMorningHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown morning subcommand: %s\n", args[0])
		printMorningHelp()
		os.Exit(1)
	}
}

func printMorningHelp() {
	fmt.Println(`yaver morning — match-report of what shipped overnight

Usage:
  yaver morning latest               Show the most recent run
  yaver morning list                 List recent runs
  yaver morning show <run-id>        Show one run's task cards
  yaver morning rollback <run> <t>   Revert the commits of a single task

Notes:
  - Runs are stored under ~/.yaver/autodev-runs/<id>/summary.json.
  - Recordings live next to them under ~/.yaver/recordings/.
  - The same data is available to mobile + web via /morning/runs and
    streamed to mobile via byte-range GET /recordings/.../video.mp4.`)
}

func morningList() {
	runs := DefaultMorningStore().List(20)
	if len(runs) == 0 {
		fmt.Println("No runs yet. Try `yaver autodev --morning`.")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tPROJECT\tTASKS\tSHIPPED\tCOST\tSTARTED")
	for _, r := range runs {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t$%.2f\t%s\n",
			r.RunID, r.Project, r.Stats.TasksTotal, r.Stats.TasksShipped,
			r.Stats.TotalCostUSD, r.StartedAt.Local().Format("2006-01-02 15:04"))
	}
	_ = tw.Flush()
}

func morningLatest() {
	runs := DefaultMorningStore().List(1)
	if len(runs) == 0 {
		fmt.Println("No runs yet. Try `yaver autodev --morning`.")
		return
	}
	renderMorningRun(runs[0])
}

func morningShow(runID string) {
	r, ok := DefaultMorningStore().Load(runID)
	if !ok {
		fmt.Fprintf(os.Stderr, "run not found: %s\n", runID)
		os.Exit(1)
	}
	renderMorningRun(r)
}

func renderMorningRun(r *MorningSummary) {
	fmt.Printf("☀ %s — %s\n", r.Project, r.RunID)
	fmt.Printf("  started: %s\n", r.StartedAt.Local().Format("2006-01-02 15:04"))
	if r.FinishedAt != nil {
		fmt.Printf("  finished: %s (%dm)\n", r.FinishedAt.Local().Format("15:04"), r.Stats.TotalMinutes)
	}
	fmt.Printf("  stats: %d shipped · %d failed · %d rolled-back · $%.2f\n\n",
		r.Stats.TasksShipped, r.Stats.TasksFailed, r.Stats.TasksRolledBack, r.Stats.TotalCostUSD)

	if len(r.Tasks) == 0 {
		fmt.Println("  (no tasks recorded yet)")
		return
	}
	for _, t := range r.Tasks {
		fmt.Printf("── %s ── [%s]\n", truncateString(t.Title, 60), t.Status)
		fmt.Printf("   %s · %d files · +%d / -%d\n", t.TaskID, t.FilesChanged, t.LinesAdded, t.LinesRemoved)
		if t.HeadSHA != "" {
			fmt.Printf("   head %s\n", shortSHA(t.HeadSHA))
		}
		if t.HasVideo {
			fmt.Printf("   video: %dms (%dB)\n", t.VideoDurationMs, t.VideoSizeBytes)
		}
		if t.OneLineSummary != "" {
			fmt.Printf("   %s\n", t.OneLineSummary)
		}
		if t.RolledBackAt != nil {
			fmt.Printf("   rolled back at %s (%s)\n", t.RolledBackAt.Local().Format("15:04:05"), shortSHA(t.RevertSHA))
		}
		fmt.Println()
	}
}

func morningRollback(runID, taskID string) {
	r, ok := DefaultMorningStore().Load(runID)
	if !ok {
		fmt.Fprintf(os.Stderr, "run not found: %s\n", runID)
		os.Exit(1)
	}
	var task *TaskHighlight
	for i := range r.Tasks {
		if r.Tasks[i].TaskID == taskID {
			task = &r.Tasks[i]
			break
		}
	}
	if task == nil {
		fmt.Fprintf(os.Stderr, "task not found: %s\n", taskID)
		os.Exit(1)
	}
	if task.Status == TaskStatusHighlightRolledBack {
		fmt.Fprintln(os.Stderr, "task already rolled back")
		os.Exit(1)
	}
	if len(task.CommitSHAs) == 0 {
		fmt.Fprintln(os.Stderr, "no commits recorded for this task — nothing to revert")
		os.Exit(1)
	}
	workDir := task.WorkDir
	if workDir == "" {
		workDir = r.WorkDir
	}
	if workDir == "" {
		fmt.Fprintln(os.Stderr, "no workDir recorded — cannot revert")
		os.Exit(1)
	}
	revertSHA, err := gitRevertCommits(context.Background(), workDir, task.CommitSHAs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rollback failed: %v\n", err)
		os.Exit(1)
	}
	if _, err := DefaultMorningStore().MarkRollback(runID, taskID, revertSHA); err != nil {
		fmt.Fprintf(os.Stderr, "rolled back at %s but could not update summary: %v\n", revertSHA, err)
		os.Exit(1)
	}
	fmt.Printf("✓ rolled back task %s (new revert commit %s)\n", taskID, shortSHA(revertSHA))
}

// ── `yaver record` ────────────────────────────────────────────────────

func runRecord(args []string) {
	if len(args) == 0 {
		args = []string{"status"}
	}
	switch args[0] {
	case "start":
		recordStart(args[1:])
	case "stop":
		recordStop(args[1:])
	case "status", "list":
		recordStatus()
	case "drivers":
		recordDrivers()
	case "help", "-h", "--help":
		printRecordHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown record subcommand: %s\n", args[0])
		printRecordHelp()
		os.Exit(1)
	}
}

func printRecordHelp() {
	fmt.Println(`yaver record — capture a short video for the morning match report

Usage:
  yaver record start <run> <task> [target]   Start recording (target: screen|ios-sim|android-emu)
  yaver record stop  <run> <task>            Stop recording
  yaver record status                        List active recordings
  yaver record drivers                       Show available drivers on this host

Notes:
  - 'screen' uses ffmpeg; install with 'yaver install ffmpeg' if missing.
  - 'ios-sim' uses xcrun simctl; only works on macOS with a booted simulator.
  - 'android-emu' uses adb screenrecord; requires an attached device.
  - On a host with none available, 'yaver record start' returns an error.`)
}

func recordStart(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver record start <run-id> <task-id> [target]")
		os.Exit(1)
	}
	target := RecordingTarget("")
	if len(args) >= 3 {
		target = RecordingTarget(args[2])
	}
	handle, err := DefaultRecordingManager().Start(context.Background(), args[0], args[1], target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "record start: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("● recording %s/%s via %s → %s\n", handle.RunID, handle.TaskID, handle.Driver, handle.Path)
}

func recordStop(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver record stop <run-id> <task-id>")
		os.Exit(1)
	}
	result, err := DefaultRecordingManager().Stop(args[0], args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "record stop: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ saved %s (%dms, %s)\n", result.Handle.Path, result.DurationMs, morningHumanBytes(result.SizeBytes))
}

func recordStatus() {
	active := DefaultRecordingManager().Active()
	if len(active) == 0 {
		fmt.Println("No active recordings.")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tTASK\tDRIVER\tELAPSED\tPATH")
	for _, h := range active {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", h.RunID, h.TaskID, h.Driver, time.Since(h.Started).Round(time.Second), h.Path)
	}
	_ = tw.Flush()
}

func recordDrivers() {
	drivers := DefaultRecordingManager().Drivers()
	if len(drivers) == 0 {
		fmt.Println("No drivers registered.")
		return
	}
	fmt.Printf("Platform: %s\n\n", platformDescription())
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TARGET\tDRIVER\tAVAILABLE\tDETAIL")
	for _, d := range drivers {
		avail := "no"
		if d.Available {
			avail = "yes"
		}
		detail := d.Reason
		if detail == "" && d.Available {
			detail = "ready"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", d.Target, d.Driver, avail, detail)
	}
	_ = tw.Flush()
}

// runDoctorRecording is called from runDoctor to surface driver
// availability next to the rest of the environment checks.
func runDoctorRecording(check func(string), pass func(string), warning func(string), failed func(string)) {
	fmt.Println("\n── Recording ──")
	for _, d := range DefaultRecordingManager().Drivers() {
		check(d.Target + " (" + d.Driver + ")")
		switch {
		case d.Available && d.Reason != "":
			warning(d.Reason)
		case d.Available:
			pass("ready")
		case strings.Contains(strings.ToLower(d.Reason), "not found"):
			failed(d.Reason + " — run `yaver install ffmpeg`")
		default:
			warning(d.Reason)
		}
	}
}

// ── MCP helpers (used by httpserver dispatch) ────────────────────────

func morningLatestMCP() ([]byte, error) {
	runs := DefaultMorningStore().List(1)
	if len(runs) == 0 {
		return json.Marshal(map[string]interface{}{"ok": true, "message": "no runs yet"})
	}
	return json.Marshal(map[string]interface{}{"ok": true, "run": runs[0]})
}

func morningShowMCP(runID string) ([]byte, error) {
	r, ok := DefaultMorningStore().Load(runID)
	if !ok {
		return nil, fmt.Errorf("run not found: %s", runID)
	}
	return json.Marshal(map[string]interface{}{"ok": true, "run": r})
}

// ── formatting helpers ──────────────────────────────────────────────

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func morningHumanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
}

// used for /recordings/{limit} query parsing elsewhere
var _ = strconv.Atoi
