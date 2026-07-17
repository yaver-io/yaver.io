package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// `yaver ship` — the typed form of the utterance.
//
// The CLI runs the barrier in-process rather than posting to the daemon, because
// the deploy has to happen HERE: TestFlight cannot upload from anywhere else, and
// a ship driven from a terminal on this Mac is already on the right machine.
// Remote surfaces (phone, watch, web, voice) reach the same code through the
// `ship` ops verb, which is the path that actually matters — the couch, not the
// terminal.

type shipMachineList []string

func (s *shipMachineList) String() string { return strings.Join(*s, ",") }
func (s *shipMachineList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

func runShipCmd(args []string) {
	fs := flag.NewFlagSet("ship", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	toparlaTimeout := fs.Duration("toparla-timeout", shipDefaultToparlaTimeout,
		"how long to let runners reach a build-OK state before deploying anyway. Expiry is not an error: the deploy pins a SHA, so in-flight work lands on the next ship")
	prompt := fs.String("prompt", "toparla", "wrap-up prompt: a library name (toparla) or ad-hoc text")
	noPrompt := fs.Bool("no-prompt", false, "skip toparla/devam; rely on the gate alone (correct but slow — the drain then takes as long as the longest in-flight kick)")
	repair := fs.Bool("repair", true, "if main does not build, run a bounded autorun to fix it before deploying")
	repairIters := fs.Int("repair-max-iters", shipDefaultRepairIter, "maximum repair kicks")
	dryRun := fs.Bool("dry-run", false, "freeze, drain, detect and report — but do not deploy (still thaws)")
	var machines shipMachineList
	fs.Var(&machines, "freeze", "machine running autoruns to freeze (deviceId|alias|primary), repeatable or comma-separated. This machine is always frozen too")
	var targets shipMachineList
	fs.Var(&targets, "targets", "override detection: convex, web-cloudflare, cli-npm, testflight-ios, playstore-android")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: yaver ship [flags]

Freeze every autorun, converge main, deploy once, resume the fleet.

  1. toparla  — tell every live runner to reach a build-OK state (NOT to finish)
  2. freeze   — no loop can start a new iteration
  3. drain    — wait up to --toparla-timeout for them to park
  4. pin      — resolve main to a SHA; that exact commit is what deploys
  5. repair   — make it compile, if it does not
  6. detect   — which targets the diff since %s touched
  7. deploy   — once, coalesced
  8. thaw     — resume the fleet (on EVERY exit path, including failure)
  9. devam    — tell the runners main moved

Examples:
  yaver ship --freeze mini --dry-run
  yaver ship --freeze mini,hetzner
  yaver ship --targets convex,web-cloudflare      # first ship: set the watermark

Flags:
`, shipLastTag)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return
	}

	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ship:", err)
		os.Exit(1)
	}
	opts := shipOptions{
		FreezeMachines: machines,
		ToparlaTimeout: *toparlaTimeout,
		Prompt:         *prompt,
		NoPrompt:       *noPrompt,
		Repair:         *repair,
		RepairMaxIters: *repairIters,
		Targets:        targets,
		DryRun:         *dryRun,
		WorkDir:        workDir,
	}

	res := runShip(context.Background(), nil, opts)
	printShipResult(res)
	if !res.OK {
		os.Exit(1)
	}
}

func printShipResult(res shipResult) {
	for _, p := range res.Phases {
		mark := map[string]string{"ok": "✓", "skipped": "–", "warned": "!", "failed": "✗"}[p.Status]
		if mark == "" {
			mark = "?"
		}
		line := fmt.Sprintf("%s %-8s %s", mark, p.Name, p.Detail)
		if p.Elapsed >= 1 {
			line += fmt.Sprintf("  (%.0fs)", p.Elapsed)
		}
		fmt.Println(line)
	}
	// Only claim a deploy when one actually happened. Pinning a SHA is not
	// deploying it: a dry-run, a refused first ship, and an abort all pin
	// something, and printing "deployed:" for those reads as a lie the one time
	// it matters.
	if res.PinnedSHA != "" {
		if len(res.Deploy.Steps) > 0 && res.Deploy.OK {
			fmt.Printf("\ndeployed %s → %s\n", res.PinnedSHA, strings.Join(res.Plan.Targets, ", "))
		} else {
			fmt.Printf("\npinned (not deployed): %s\n", res.PinnedSHA)
		}
	}
	// Say what was dropped. A ship that silently omits a target reads as
	// "covered everything" when it did not.
	if len(res.Plan.Unmapped) > 0 {
		fmt.Printf("not deployable (no target): %s\n", strings.Join(res.Plan.Unmapped, ", "))
	}
	if !res.Drain.Drained && len(res.Drain.Draining) > 0 {
		fmt.Printf("still in flight at deploy time (lands next ship): %d loop(s)\n", len(res.Drain.Draining))
	}
	for _, tw := range res.Thawed {
		if !tw.OK {
			// The one failure a human must act on: a machine we could not thaw is
			// a machine whose autoruns are still held. The lease will free it, but
			// say so rather than let it look clean.
			fmt.Printf("WARNING: could not thaw %s (%s) — its lease will expire within %s\n", tw.Machine, tw.Detail, shipLeaseTTL)
		}
	}
	if res.Error != "" {
		fmt.Fprintf(os.Stderr, "\nship failed: %s\nthe fleet was resumed.\n", res.Error)
	}
}

// shipStatusJSON is the machine-readable form, for surfaces that poll.
func shipStatusJSON(res shipResult) string {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
