package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The ship barrier: freeze the fleet, converge main, deploy once, thaw.
//
// It exists to serve one utterance, said from the couch:
//
//	"stop all autoruns on the mini, make it compilable, commit push deploy to
//	 all platforms, then keep autoruns working after all deploys"
//
// Design doc: docs/architecture/SHIP_BARRIER.md. The three things that shape
// everything here:
//
//  1. N loops would otherwise cause N deploys. Each autorun commits and pushes
//     every iteration. "One deploy per converged change" is a house rule today;
//     the barrier makes it structural, because a parked loop cannot trigger one.
//  2. Main is regularly red. Deploying an unverified main ships a brick, and
//     TestFlight has no rollback — only supersede, at ~15-20 uploads/day.
//  3. The freeze and the deploy happen on DIFFERENT machines. Autoruns run on
//     the mini; TestFlight can only upload from the user's Mac. The coordinator
//     is always remote from at least one gate it holds. That is what makes this
//     a protocol rather than a script, and it is why the freeze needs a lease.

const (
	shipDefaultToparlaTimeout = 10 * time.Minute
	// shipLeaseTTL is the dead-man lease on every freeze this coordinator takes.
	// Comfortably longer than a toparla drain, far shorter than a human's absence.
	// See autorunGate.expiry: if this coordinator dies, the remote fleet thaws
	// itself rather than staying frozen forever.
	shipLeaseTTL = 20 * time.Minute
	// shipLeaseRenew is the heartbeat interval. Must be well under shipLeaseTTL
	// so an ordinary slow deploy never trips the dead-man switch.
	shipLeaseRenew        = 5 * time.Minute
	shipDefaultRepairIter = 3
)

type shipOptions struct {
	// FreezeMachines are the machines running autoruns ("the mini"). Empty means
	// this machine only. Distinct from the deploy host on purpose — see (3) above.
	FreezeMachines []string
	ToparlaTimeout time.Duration
	// Prompt is a name from shipPromptLibrary or ad-hoc text. Empty with
	// NoPrompt=false means toparla.
	Prompt   string
	NoPrompt bool
	// Repair runs a bounded autorun to make a red main compile. See shipRepair.
	Repair         bool
	RepairMaxIters int
	// Targets overrides detection. Empty means detect from the diff.
	Targets []string
	DryRun  bool
	WorkDir string
}

type shipPhase struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | skipped | warned | failed
	Detail string `json:"detail,omitempty"`
	// Elapsed is wall-clock, which for a barrier is the number that matters:
	// it is how long the fleet was held.
	Elapsed float64 `json:"elapsedSeconds"`
}

type shipResult struct {
	OK     bool        `json:"ok"`
	Phases []shipPhase `json:"phases"`
	// PinnedSHA is what was deployed. The deploy pins a SHA rather than tracking
	// a moving main, which is exactly what makes a toparla timeout safe: a runner
	// landing work mid-deploy is not a race, its commit simply is not in this
	// build and ships on the next one.
	PinnedSHA string         `json:"pinnedSha,omitempty"`
	Plan      shipTargetPlan `json:"plan"`
	// Placement records where each detected step can run, and why. Kept on the
	// result even when nothing blocks, so a ship that routed a step to CI says
	// so rather than leaving the user to infer it from a silent success.
	Placement []shipPlacementCheck `json:"placement,omitempty"`
	Deploy    DeployAllResult      `json:"deploy"`
	Toparla   shipPromptResult     `json:"toparla"`
	Devam     shipPromptResult     `json:"devam"`
	Drain     autorunDrainState    `json:"drain"`
	// Frozen/Thawed track fan-out per machine so a partial freeze is never
	// mistaken for a whole one.
	Frozen []shipMachineState `json:"frozen"`
	Thawed []shipMachineState `json:"thawed"`
	Error  string             `json:"error,omitempty"`
}

type shipMachineState struct {
	Machine string `json:"machine"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
}

func (r *shipResult) phase(name, status, detail string, started time.Time) {
	r.Phases = append(r.Phases, shipPhase{
		Name: name, Status: status, Detail: detail,
		Elapsed: time.Since(started).Seconds(),
	})
}

// runShip executes the barrier. It is written so that every early return passes
// through thaw: the fleet must never stay frozen because ship gave up.
func runShip(ctx context.Context, s *HTTPServer, opts shipOptions) shipResult {
	res := shipResult{Frozen: []shipMachineState{}, Thawed: []shipMachineState{}}
	if opts.ToparlaTimeout <= 0 {
		opts.ToparlaTimeout = shipDefaultToparlaTimeout
	}
	if opts.RepairMaxIters <= 0 {
		opts.RepairMaxIters = shipDefaultRepairIter
	}
	if strings.TrimSpace(opts.WorkDir) == "" {
		opts.WorkDir = repoRootFromCWD()
	}

	// Phase 1 — toparla. BEFORE the freeze, not after.
	//
	// Freezing first would be worse than useless: a frozen loop that finishes its
	// kick parks immediately, so it never reaches the point where it reads the
	// queued prompt. The prompt would sit in the queue until after the deploy,
	// arriving as "wrap up, we're deploying" about a deploy that already happened.
	t := time.Now()
	if !opts.NoPrompt {
		prompt := resolveShipPrompt(firstNonEmpty(opts.Prompt, "toparla"))
		res.Toparla = broadcastShipPrompt(ctx, keeperFor(s), prompt, "ship:toparla")
		res.phase("toparla", "ok", res.Toparla.summary(), t)
	} else {
		res.phase("toparla", "skipped", "--no-prompt: coercive drain only", t)
	}

	// Phase 2 — freeze. Fan out to every machine running autoruns.
	t = time.Now()
	frozen, freezeErr := shipFreezeAll(ctx, s, opts)
	res.Frozen = frozen
	if freezeErr != nil {
		// An unreachable freezeTarget is not a partial success, it is a false
		// sense of one: that machine's loops will push into the middle of the
		// deploy. Abort, and thaw whatever we did freeze.
		res.phase("freeze", "failed", freezeErr.Error(), t)
		res.Error = freezeErr.Error()
		res.Thawed = shipThawAll(context.WithoutCancel(ctx), s, opts, frozen)
		shipNotify(s, "ship aborted before deploying: "+freezeErr.Error())
		return res
	}
	res.phase("freeze", "ok", fmt.Sprintf("%d machine(s) frozen", len(frozen)), t)

	// From here on, every exit thaws. The lease (shipLeaseTTL) is the backstop if
	// this process dies outright; this defer is the backstop if it merely returns.
	defer func() {
		res.Thawed = shipThawAll(context.WithoutCancel(ctx), s, opts, frozen)
	}()

	// Keep the dead-man lease alive for as long as we are actually here.
	stopRenew := shipRenewLease(ctx, s, opts, frozen)
	defer stopRenew()

	// Phase 3 — drain, bounded by the toparla timeout.
	//
	// Expiry is NOT a failure and must not stop any run. Killing a runner to force
	// the issue would dump a live iteration into a diagnostic stash, trading a
	// one-ship delay for a human cleanup. Let it finish; it lands next ship.
	t = time.Now()
	res.Drain = autorunAwaitDrain(ctx, opts.ToparlaTimeout)
	if res.Drain.Drained {
		res.phase("drain", "ok", fmt.Sprintf("%d loop(s) parked", len(res.Drain.Parked)), t)
	} else {
		res.phase("drain", "warned", fmt.Sprintf(
			"toparla timeout after %s — %d loop(s) still in flight; their work lands on the next ship",
			opts.ToparlaTimeout, len(res.Drain.Draining)), t)
	}

	// Phase 4 — pin. Resolve main to a SHA and deploy exactly that.
	t = time.Now()
	sha, err := shipPinHead(ctx, opts.WorkDir)
	if err != nil {
		res.phase("pin", "failed", err.Error(), t)
		res.Error = err.Error()
		shipNotify(s, "ship aborted: could not pin a commit — "+err.Error())
		return res
	}
	res.PinnedSHA = sha
	res.phase("pin", "ok", sha, t)

	// Phase 5 — repair. "make it compilable".
	t = time.Now()
	if opts.Repair {
		status, detail, rerr := shipRepair(ctx, opts, sha)
		res.phase("repair", status, detail, t)
		if rerr != nil {
			res.Error = rerr.Error()
			shipNotify(s, "ship aborted: main does not build and repair failed — "+rerr.Error())
			return res
		}
		// Repair commits move main, so re-pin.
		if status == "ok" && strings.Contains(detail, "repaired") {
			if sha2, err := shipPinHead(ctx, opts.WorkDir); err == nil {
				res.PinnedSHA = sha2
				sha = sha2
			}
		}
	} else {
		res.phase("repair", "skipped", "--no-repair", t)
	}

	// Phase 6 — detect.
	t = time.Now()
	plan, err := detectShipTargets(ctx, opts.WorkDir, sha)
	if err != nil {
		res.phase("detect", "failed", err.Error(), t)
		res.Error = err.Error()
		shipNotify(s, "ship aborted: could not detect targets — "+err.Error())
		return res
	}
	if len(opts.Targets) > 0 {
		plan.Targets = sortShipTargets(opts.Targets)
		plan.Since = "(overridden)"
	}
	res.Plan = plan
	switch {
	case len(plan.Targets) == 0 && plan.Since == "":
		res.phase("detect", "warned", fmt.Sprintf(
			"no %s marker yet — refusing to infer a first ship. Pass explicit targets once to set the watermark.", shipLastTag), t)
		res.OK = true
		return res
	case len(plan.Targets) == 0:
		res.phase("detect", "ok", "nothing deployable changed since the last ship", t)
		res.OK = true
		return res
	}
	res.phase("detect", "ok", strings.Join(plan.Targets, ", "), t)

	// Phase 6.5 — placement. Ask whether each detected step CAN run, and where,
	// before spending the deploy on finding out.
	//
	// checkShipPlacement was written for exactly this and then shipped with no
	// call site, so every capability and quota check it performs has been dead
	// since it landed. The failure it exists to prevent is expensive and
	// asymmetric: freeze the whole fleet, drain it, pin a SHA, then die at an
	// App Store upload because the day's quota was already spent — having held
	// every autorun still for the duration, and, because TestFlight has no
	// rollback, with a retry that spends tomorrow's slot on the same mistake.
	//
	// Blocking is deliberately narrow. A CI route is NOT a failure, it is a
	// routing decision (web and npm deploy from CI because their tokens are
	// GitHub secrets); treating it as one would send someone to debug a healthy
	// fleet. Only "nothing can run this" and "quota spent" stop the barrier.
	//
	// quotaExhausted is nil because NO quota tracking exists in the agent today
	// — grep found no producer. Passing nil rather than inventing one keeps the
	// capability half honest (it works now) and leaves the quota half wired but
	// inert until something real feeds it. A fabricated "not exhausted" would be
	// worse than nil: it would read as a check that passed.
	t = time.Now()
	placementChecks := checkShipPlacement(plan.Targets, listAllMachines(ctx), nil)
	res.Placement = placementChecks
	var blocked []string
	for _, c := range placementChecks {
		if c.Blocking {
			blocked = append(blocked, fmt.Sprintf("%s: %s", c.Step, c.Reason))
		}
	}
	if len(blocked) > 0 {
		detail := strings.Join(blocked, "; ")
		res.phase("placement", "failed", detail, t)
		res.Error = "placement: " + detail
		// Thaw before returning — the fleet is frozen at this point and a
		// blocked placement is a decision, not a crash. Leaving loops parked
		// because we declined to deploy would be a worse outcome than the
		// deploy we skipped.
		res.Thawed = shipThawAll(context.WithoutCancel(ctx), s, opts, frozen)
		shipNotify(s, "ship aborted before deploying — "+detail)
		return res
	}
	res.phase("placement", "ok", shipPlacementSummary(placementChecks), t)

	// Phase 7 — deploy, once, coalesced. This is the whole point of the barrier.
	t = time.Now()
	if opts.DryRun {
		res.phase("deploy", "skipped", "dryRun: would deploy "+strings.Join(plan.Targets, ", "), t)
		res.OK = true
		return res
	}
	// Pass the pinned SHA so the deploy refuses if the tree moved after the
	// gate. Pinning without enforcing it is bookkeeping, not a guarantee.
	res.Deploy = RunDeployAll(ctx, DeployAllRequest{Only: plan.Targets, PinnedSHA: res.PinnedSHA})
	if !res.Deploy.OK {
		res.phase("deploy", "failed", res.Deploy.Note, t)
		res.Error = "deploy failed: " + res.Deploy.Note
		// Per the failure contract: auto-thaw (the defer) and notify. The user is
		// outside; a fleet frozen forever is worse than a failed deploy.
		shipNotify(s, "ship: deploy FAILED ("+res.Deploy.Note+"). Autoruns resumed.")
		return res
	}
	res.phase("deploy", "ok", fmt.Sprintf("%d target(s)", len(res.Deploy.Steps)), t)

	// The watermark moves only on success, so a failed ship re-detects the same
	// targets next time — which is what you want from a retry.
	t = time.Now()
	if err := markShipped(ctx, opts.WorkDir, sha); err != nil {
		res.phase("mark", "warned", err.Error(), t)
	} else {
		res.phase("mark", "ok", shipLastTag+" → "+sha, t)
	}

	res.OK = true
	shipNotify(s, fmt.Sprintf("ship OK: %s → %s", sha[:min(8, len(sha))], strings.Join(plan.Targets, ", ")))
	return res
}

// shipRepair makes a red main compile, within bounds.
//
// "Make it compilable" is naturally an autorun: kick a runner until a gate passes
// is exactly what autorunLoop already does. But ship has just frozen every
// autorun on this machine, so a repair loop started here would park itself
// instantly and ship would wait forever for a repair that is waiting for ship.
// Hence the gate exemption — narrow, one ID, cleared on the way out.
//
// Bounds matter: an unbounded repair at 3am is how you wake to 200 commits. Small
// maxIters, the real build as the gate, and a hard refusal to deploy a main that
// is still red afterwards.
func shipRepair(ctx context.Context, opts shipOptions, sha string) (status, detail string, err error) {
	if buildErr := deployPreflight(ctx, opts.WorkDir); buildErr == nil {
		return "skipped", "main already builds", nil
	}
	// Main is red. Repair is only reachable when a task file says how; without
	// one, refuse rather than improvise a runner prompt against a red tree.
	taskPath := shipRepairTaskPath(opts.WorkDir)
	if taskPath == "" {
		return "failed", "", fmt.Errorf("main does not build and no repair task is configured; refusing to deploy a red main")
	}
	repairID := fmt.Sprintf("ship-repair-%d", time.Now().UTC().UnixNano())
	autorunFreeze.exempt(repairID)
	defer autorunFreeze.unexempt(repairID)

	view, startErr := autorunSessions.start(ctx, autorunOptions{
		SessionID: repairID,
		TaskPath:  taskPath,
		Runner:    "auto",
		Interval:  0,
		MaxIters:  opts.RepairMaxIters,
		Gate:      "go build ./...",
		Push:      true,
		Scopes:    []string{"desktop/agent/"},
		WorkDir:   opts.WorkDir,
	})
	if startErr != nil {
		return "failed", "", fmt.Errorf("main does not build and repair could not start: %w", startErr)
	}
	if err := shipWaitAutorun(ctx, view.ID, shipLeaseTTL); err != nil {
		return "failed", "", fmt.Errorf("main does not build and repair did not finish: %w", err)
	}
	if buildErr := deployPreflight(ctx, opts.WorkDir); buildErr != nil {
		return "failed", "", fmt.Errorf("main still does not build after %d repair iterations; refusing to deploy a red main: %w", opts.RepairMaxIters, buildErr)
	}
	return "ok", "repaired: main builds again", nil
}

// shipWaitAutorun blocks until one autorun session leaves `running`.
func shipWaitAutorun(ctx context.Context, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		views, err := autorunSessions.status(id)
		if err != nil {
			return err
		}
		if len(views) > 0 && views[0].Status != "running" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("repair still running after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(autorunDrainPollInterval):
		}
	}
}

func shipPinHead(ctx context.Context, workDir string) (string, error) {
	out, err := shipGitExec(ctx, workDir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse HEAD: %w: %s", err, strings.TrimSpace(out))
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", fmt.Errorf("rev-parse HEAD returned nothing")
	}
	return sha, nil
}

func keeperFor(s *HTTPServer) *RunnerKeeper {
	if s == nil {
		return nil
	}
	return s.ensureRunnerKeeper()
}

// shipNotify is best-effort by design. A ship must never fail because the
// notification channel was not configured — but the user is outside, so the
// notification is the only thing that closes the loop for them.
func shipNotify(s *HTTPServer, message string) {
	if s == nil || s.notifyMgr == nil {
		return
	}
	s.notifyMgr.sendAll(message)
}
