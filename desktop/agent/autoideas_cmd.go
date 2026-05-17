package main

// autoideas_cmd.go — `yaver autoideas <project>` is a detached,
// timer-based idea-generator. Each tick appends a fresh
// `- [ ] <title>` line to ideas.md.
//
// Mobile picks up the file and renders checkboxes; the user selects
// the ones they want and triggers a follow-up task with the curated
// subset. Generation continues in parallel — the user can keep
// checking new ideas while previously-checked ones get worked on.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func runAutoIdeas(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			printAutoIdeasHelp()
			return
		case "status", "ls", "list":
			runAutoIdeasStatus()
			return
		}
	}

	fs := flag.NewFlagSet("autoideas", flag.ExitOnError)
	hours := fs.String("hours", "8", "Duration hours, or 'inf'/'infinite'")
	infinite := fs.Bool("infinite", false, "Run until SIGINT (same as --hours inf)")
	load := fs.String("load", "lite", "lite|high (lite respects AI session windows)")
	lite := fs.Bool("lite", false, "Shortcut for --load lite (default)")
	heavy := fs.Bool("heavy", false, "Shortcut for --load high (burst)")
	prompt := fs.String("prompt", "", "Roof theme — generated ideas stay within this focus area")
	harden := fs.String("harden", "", "Hardening preset — security|memory|perf|quality|all")
	engine := fs.String("engine", "", "claude|hybrid (default: claude)")
	runner := fs.String("runner", "", "Override the generator runner (e.g. claude:sonnet, codex, opencode, ollama:qwen2.5-coder:14b)")
	hybrid := fs.Bool("hybrid", false, "Shortcut for --engine hybrid")
	output := fs.String("output", "ideas.md", "Output file inside the project for generated ideas")
	maxBatches := fs.Int("max-batches", 0, "Hard cap on idea batches (0 = no cap; deadline still applies)")
	tickSec := fs.Int("tick", 0, "Seconds between generation batches (0 = lite=300s, high=60s)")
	showPlan := fs.Bool("plan", false, "Print plan and exit (dry-run)")
	to := fs.String("to", "", "Run on a remote yaver agent (device id / hostname / Tailscale alias). Routes via P2P or relay using existing handoff transport.")
	fs.Usage = printAutoIdeasHelp

	positional, flagArgs := splitAutodevArgs(args)
	_ = fs.Parse(flagArgs)
	if *heavy {
		*load = "high"
	}
	if *lite {
		*load = "lite"
	}
	if *infinite {
		*hours = "infinite"
	}
	if *hybrid {
		*engine = "hybrid"
	}

	wd, _ := os.Getwd()
	project := ""
	if len(positional) > 0 {
		project = positional[0]
	}
	if project == "" {
		project = filepath.Base(wd)
	}

	// --to <device>: ship the request to a remote yaver agent and
	// exit. The remote daemon spawns the autoideas loop; the user
	// can later `yaver stream autodev:<project>-autoideas --to <device>`
	// to tail it.
	if strings.TrimSpace(*to) != "" {
		body := map[string]interface{}{
			"project":     project,
			"work_dir":    wd,
			"hours":       *hours,
			"load":        *load,
			"prompt":      *prompt,
			"harden":      *harden,
			"engine":      *engine,
			"runner":      *runner,
			"output":      *output,
			"max_batches": *maxBatches,
			"tick":        *tickSec,
		}
		out := remoteYaverPOST(*to, "/autoideas/start", body)
		fmt.Printf("autoideas: started on %s — loop=%v stream=%v\n",
			*to, out["loop_name"], out["stream_name"])
		return
	}

	// Roof theme: --harden preset prepends to --prompt, same rules
	// as autodev. Empty after that = open-ended.
	if hp := autodevHardenPrompt(*harden); hp != "" {
		if strings.TrimSpace(*prompt) == "" {
			*prompt = hp
		} else {
			*prompt = hp + "\n\n" + *prompt
		}
	}

	tick := *tickSec
	if tick <= 0 {
		if *load == "lite" {
			tick = 300
		} else {
			tick = 60
		}
	}

	infiniteRun := *hours == "inf" || *hours == "infinite" || *hours == ""
	var deadline time.Time
	if !infiniteRun {
		n, err := strconv.Atoi(*hours)
		if err != nil || n <= 0 {
			n = 8
		}
		deadline = time.Now().Add(time.Duration(n) * time.Hour)
	}

	outputPath := *output
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(wd, outputPath)
	}
	loopName := project + "-autoideas"
	streamName := "autodev:" + loopName

	if *showPlan {
		fmt.Println()
		fmt.Println("yaver autoideas plan")
		fmt.Println("---------------")
		fmt.Printf("  project:        %s\n", project)
		fmt.Printf("  output:         %s\n", outputPath)
		fmt.Printf("  duration:       %s\n", durationLabel(*hours, deadline))
		fmt.Printf("  load:           %s (tick=%ds)\n", *load, tick)
		fmt.Printf("  engine:         %s\n", defaultStr(*engine, "claude"))
		fmt.Printf("  runner:         %s\n", defaultStr(*runner, "auto"))
		fmt.Printf("  roof theme:     %s\n", oneLineAutodev(*prompt, 80))
		if *maxBatches > 0 {
			fmt.Printf("  max batches:    %d\n", *maxBatches)
		}
		fmt.Println()
		fmt.Printf("  Watch live: yaver stream %s\n", streamName)
		fmt.Printf("  Output:     tail -f %s\n", outputPath)
		fmt.Println()
		return
	}

	// Detach + tail. The detached child re-enters runAutoIdeas with
	// YAVER_AUTODEV_DETACHED=1 and runs the actual generation loop.
	if !autodevDetachActive() {
		_, sn := spawnDetachedAutodev("autoideas", args, loopName)
		if sn != "" {
			tailDetachedAutodev(sn)
			return
		}
		fmt.Fprintln(os.Stderr, "[autoideas] detach failed — running in foreground")
	}

	stopStream := teeStdoutToStream(streamName)
	defer stopStream()

	fmt.Printf("autoideas: starting %s → %s (load=%s, tick=%ds)\n",
		project, outputPath, *load, tick)
	if *prompt != "" {
		fmt.Printf("autoideas: roof theme = %s\n", oneLineAutodev(*prompt, 120))
	}

	batch := 0
	killFile, _ := loopKillFilePath(loopName)
	for {
		if !infiniteRun && time.Now().After(deadline) {
			fmt.Printf("autoideas: deadline reached — stopping\n")
			break
		}
		if *maxBatches > 0 && batch >= *maxBatches {
			fmt.Printf("autoideas: hit max-batches cap of %d — stopping\n", *maxBatches)
			break
		}
		if killFile != "" {
			if _, err := os.Stat(killFile); err == nil {
				fmt.Printf("autoideas: STOP file detected — exiting\n")
				break
			}
		}

		batch++
		fmt.Printf("autoideas: generating batch %d…\n", batch)
		AutodevPublishYaverSay(fmt.Sprintf("Generate batch %d of fresh ideas", batch))

		if err := autoIdeasGenerate(*engine, *runner, *prompt, outputPath, wd); err != nil {
			fmt.Fprintf(os.Stderr, "autoideas: batch %d failed: %v — sleeping then retry\n", batch, err)
		} else {
			fmt.Printf("autoideas: batch %d done\n", batch)
		}

		// Cancellable sleep until the next tick.
		nextAt := time.Now().Add(time.Duration(tick) * time.Second)
		fmt.Printf("autoideas: next batch at %s (in %s)\n",
			nextAt.Format("15:04:05"), time.Duration(tick)*time.Second)
		stopAt := nextAt
		for time.Now().Before(stopAt) {
			if killFile != "" {
				if _, err := os.Stat(killFile); err == nil {
					goto endLoop
				}
			}
			time.Sleep(1 * time.Second)
		}
	}
endLoop:
	fmt.Printf("autoideas: %d batches generated → %s\n", batch, outputPath)
}

// autoIdeasGenerate is a thin wrapper around RunAIGenerator that
// formats the prompt, parses the JSON array of titles, and appends
// them to the output file. Runner-agnostic — works with claude /
// codex / opencode via the picker in ai_generator.go.
func autoIdeasGenerate(engine, runner, focus, outputPath, wd string) error {
	focusBlock := ""
	f := strings.TrimSpace(focus)
	if f != "" {
		focusBlock = "\nROOF THEME (every item must serve this goal):\n" + f + "\n"
	}

	prompt := fmt.Sprintf(`You are generating the next batch of small, single-PR-sized improvements for an autonomous coding loop in this project (%s).
%s
Read recent git log (git log --oneline -20), open TODO/FIXME comments, half-finished components, missing tests, broken UX, and dead code. Pick %d items the project actually needs next.

Output ONLY a JSON array of strings — one short imperative title per item, no other text, no code fences. Example:
["Fix N+1 query in DealList.tsx","Add empty state to PortfolioEmpty.tsx","Persist tweets to Convex"]

Do not write any file. Do not commit. Just print the JSON array and stop.`,
		wd, focusBlock, autodevRefillBatchSize)

	body, err := RunAIGenerator(AIGeneratorSpec{
		Engine:  engine,
		Runner:  runner,
		WorkDir: wd,
		Prompt:  prompt,
		Timeout: 5 * time.Minute,
	})
	if err != nil {
		return err
	}
	titles, err := extractRefillTitles(body)
	if err != nil {
		return err
	}
	if len(titles) == 0 {
		return fmt.Errorf("no items extracted")
	}

	// Append "- [ ] <title>" lines, leading blank line if needed.
	prefix := ""
	if existing, err := os.ReadFile(outputPath); err == nil && len(existing) > 0 {
		if !strings.HasSuffix(string(existing), "\n") {
			prefix = "\n\n"
		} else if !strings.HasSuffix(string(existing), "\n\n") {
			prefix = "\n"
		}
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	for _, t := range titles {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		sb.WriteString("- [ ] ")
		sb.WriteString(t)
		sb.WriteByte('\n')
	}
	f2, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", outputPath, err)
	}
	defer f2.Close()
	if _, err := io.WriteString(f2, sb.String()); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	fmt.Fprintf(os.Stderr, "[autoideas] appended %d items to %s\n", len(titles), outputPath)
	return nil
}

func runAutoIdeasStatus() {
	// autoideas reuses the loop registry under the "<project>-autoideas"
	// name + the daemon stream registry, so status piggybacks on the
	// existing autodev/loop status surfaces. Just point the user at
	// them rather than re-implementing.
	fmt.Println("autoideas runs share infra with autodev — query status via:")
	fmt.Println("  yaver autodev status                 (loop registry)")
	fmt.Println("  yaver stream autodev:<project>-autoideas   (live tail)")
	fmt.Println("  ls /tmp/yaver/autodev_*-autoideas-*.log    (per-run logs)")
}

func printAutoIdeasHelp() {
	fmt.Println(`yaver autoideas — overnight idea generator (no implementation)

Usage:
  yaver autoideas <project> [flags]
  yaver autoideas status              show how to query state
  yaver autoideas help                this help

What it does:
  Runs a long-lived loop that asks the AI runner for fresh single-
  PR-sized ideas every tick and appends them as "- [ ] <title>"
  lines to ideas.md (or whichever file --output names). The mobile
  Auto Dev tab renders them as checkboxes; the user selects the
  ones to implement and triggers yaver autodev with the curated
  subset as --remained.

Flags (mirror yaver autodev):
  --hours N            duration in hours; accepts "inf"/"infinite".
  --infinite           run until SIGINT.
  --lite | --heavy     respect AI session windows (default) | burst.
  --prompt "..."       roof theme — generated ideas stay focused.
  --harden AREA        security|memory|perf|quality|all preset.
  --engine claude|hybrid
  --runner SPEC        generator runner override (claude:sonnet|codex|opencode|ollama:MODEL).
  --hybrid             shortcut for --engine hybrid.
  --output PATH        ideas file (default ideas.md inside the project).
  --max-batches N      hard cap on batches (0 = no cap).
  --tick SECS          seconds between batches (default lite=300, high=60).
  --plan               print plan and exit (dry-run).

The run is detached — Ctrl-C only releases the tail, generation
keeps going. Re-attach with:
  yaver stream autodev:<project>-autoideas
Stop with:
  yaver loop stop <project>-autoideas`)
}

func durationLabel(hours string, deadline time.Time) string {
	if hours == "inf" || hours == "infinite" || hours == "" {
		return "infinite (SIGINT to stop)"
	}
	return fmt.Sprintf("%s hour(s) — until %s", hours, deadline.Format("Mon 15:04"))
}

// loopKillFilePath returns the per-loop STOP sentinel path; touch the
// file to cooperatively stop a detached run.
func loopKillFilePath(name string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	loopDir := filepath.Join(dir, "loops", name)
	if err := os.MkdirAll(loopDir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(loopDir, "STOP"), nil
}

// oneLineAutodev compresses whitespace and truncates s for log lines.
func oneLineAutodev(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}
