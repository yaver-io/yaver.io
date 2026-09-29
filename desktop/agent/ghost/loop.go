package ghost

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// loop.go — the multi-step operator loop.
//
// WHY THIS EXISTS
// Engine.Act (vision.go) is single-shot: capture → locate → execute, one
// iteration, no re-observation. That was deliberate — the multi-step
// plan→verify→retry loop was "owned by the caller (Talos)". The consequence is
// that nothing in the tree can actually carry out a goal that takes more than
// one action, so the "operate my computer" capability could be demonstrated but
// not used. RunLoop is that caller, in-tree and bounded.
//
// SHAPE
// Observe → decide → act → repeat, where the decision is the injected Locator
// (wired by the agent to the model the user already configured — Claude/Codex
// for vision clients, or the text-adapted path for opencode/deepseek). The loop
// stops on: the locator saying "none" (goal met), a step/time budget, or two
// identical consecutive actions (a stalled model — re-sending the same click
// because the screen did not change is not progress).
//
// The package stays free of any LLM dependency: it only knows the Locator
// interface, so it is fully unit-testable with fakes and a customer can ground
// entirely on-prem.

// LoopStep is one iteration of the loop, kept for the trace a surface renders.
type LoopStep struct {
	Index  int    `json:"index"`
	Action Action `json:"action"`
	Error  string `json:"error,omitempty"`
}

// UIStateFunc returns a textual signature of the current UI. The engine can
// supply the accessibility tree (Engine.TreeText); a caller may override it.
type UIStateFunc func(ctx context.Context) (string, error)

// VerifyFunc is asked, after an action, whether goal is satisfied given the
// current UI text. It is the GROUNDED successor to trusting the model's
// ActionNone: instead of "I think I'm done", the caller checks the tree.
type VerifyFunc func(ctx context.Context, goal, uiState string) (done bool, detail string, err error)

// LoopOptions bounds a RunLoop. Zero values take the documented defaults, so a
// caller that only sets MaxSteps still gets sane per-step and total timeouts.
type LoopOptions struct {
	// Display is the monitor index to capture (0 = primary).
	Display int
	// MaxSteps caps the number of observe→act iterations (default 8).
	MaxSteps int
	// PerStepTimeout bounds a single Locate call (default 60s).
	PerStepTimeout time.Duration
	// TotalTimeout bounds the whole loop (default 5m).
	TotalTimeout time.Duration
	// NoProgressStopsAfter ends the loop after this many identical consecutive
	// actions (default 3). Set < 0 to disable.
	NoProgressStopsAfter int
	// Observe, when set, returns a UI signature after each action. If a
	// mutating action (click/type/key) leaves it unchanged this many times in a
	// row, the loop stops with no_change — an action that provably changed
	// nothing is not progress.
	Observe UIStateFunc
	// NoChangeStopsAfter is the threshold for Observe (default 2). Set < 0 to
	// disable no-change detection.
	NoChangeStopsAfter int
	// Verify, when set, is asked whether goal is done after each action. A true
	// answer completes the loop with reason "verified" — the tree-backed
	// alternative to the locator's unverified "none".
	Verify VerifyFunc
}

func (o LoopOptions) withDefaults() LoopOptions {
	if o.MaxSteps <= 0 {
		o.MaxSteps = 8
	}
	if o.PerStepTimeout <= 0 {
		o.PerStepTimeout = 60 * time.Second
	}
	if o.TotalTimeout <= 0 {
		o.TotalTimeout = 5 * time.Minute
	}
	if o.NoProgressStopsAfter == 0 {
		o.NoProgressStopsAfter = 3
	}
	if o.NoChangeStopsAfter == 0 {
		o.NoChangeStopsAfter = 2
	}
	return o
}

// isMutatingKind reports whether an action is expected to change the UI. Hover
// and scroll are excluded: they routinely leave the accessibility tree
// identical, so counting them as "no change" would be a false positive.
func isMutatingKind(k ActionKind) bool {
	switch k {
	case ActionClick, ActionDoubleClick, ActionType, ActionKey:
		return true
	}
	return false
}

// LoopResult is the trace of a RunLoop. It is designed to be rendered as-is by
// a surface: what was tried, why, and where it stopped — never a silent hang.
type LoopResult struct {
	Goal      string     `json:"goal"`
	Steps     []LoopStep `json:"steps"`
	Completed bool       `json:"completed"`
	// Verified is true only when a Verify hook confirmed completion against the
	// live UI (the tree), not merely the model's opinion. Completed with
	// Verified=false is the locator's own "none" and should be treated as
	// unverified.
	Verified     bool   `json:"verified"`
	VerifyDetail string `json:"verifyDetail,omitempty"`
	// StoppedReason is one of: verified (grounded done), none (locator said
	// done, unverified), budget (MaxSteps), deadline, no_progress, no_change,
	// capture_error, locate_error, execute_error.
	StoppedReason string `json:"stoppedReason,omitempty"`
}

// RunLoop drives the engine toward goal, one grounded action per iteration.
//
// It returns the trace ALWAYS, plus a non-nil error only for a hard failure
// (capture/grounding/actuation). A trace with err != nil is still meaningful:
// the caller can show how far it got and name the cause.
func (e *Engine) RunLoop(ctx context.Context, loc Locator, goal string, opts LoopOptions) (LoopResult, error) {
	res := LoopResult{Goal: goal}
	if e == nil || e.Screen == nil || e.Input == nil {
		return res, ErrUnsupported
	}
	if loc == nil {
		return res, fmt.Errorf("ghost: no vision locator configured (set a vision provider: GHOST_VISION_* / OPENAI_*, or pass provider/baseUrl/apiKey/model)")
	}
	o := opts.withDefaults()

	loopCtx := ctx
	if o.TotalTimeout > 0 {
		var cancel context.CancelFunc
		loopCtx, cancel = context.WithTimeout(ctx, o.TotalTimeout)
		defer cancel()
	}

	consecutive := 0
	var prev Action
	var havePrev bool
	noChange := 0
	prevState := ""
	haveState := false

	for i := 1; i <= o.MaxSteps; i++ {
		if loopCtx.Err() != nil {
			res.StoppedReason = "deadline"
			return res, nil
		}

		pngBytes, _, err := e.CapturePNG(o.Display)
		if err != nil {
			res.Steps = append(res.Steps, LoopStep{Index: i, Error: "capture: " + err.Error()})
			res.StoppedReason = "capture_error"
			return res, fmt.Errorf("ghost: capture failed at step %d: %w", i, err)
		}

		stepCtx, cancel := context.WithTimeout(loopCtx, o.PerStepTimeout)
		act, err := loc.Locate(stepCtx, pngBytes, goal)
		cancel()
		if err != nil {
			res.Steps = append(res.Steps, LoopStep{Index: i, Error: "locate: " + err.Error()})
			res.StoppedReason = "locate_error"
			return res, fmt.Errorf("ghost: vision grounding failed at step %d: %w", i, err)
		}

		res.Steps = append(res.Steps, LoopStep{Index: i, Action: act})
		if act.Kind == "" || act.Kind == ActionNone {
			res.Completed = true
			res.StoppedReason = "none"
			return res, nil
		}

		if err := e.Execute(act); err != nil {
			res.Steps[len(res.Steps)-1].Error = "execute: " + err.Error()
			res.StoppedReason = "execute_error"
			return res, fmt.Errorf("ghost: action failed at step %d: %w", i, err)
		}

		if havePrev && sameAction(prev, act) {
			consecutive++
		} else {
			consecutive = 1
		}
		prev, havePrev = act, true
		if o.NoProgressStopsAfter > 0 && consecutive >= o.NoProgressStopsAfter {
			res.StoppedReason = "no_progress"
			return res, nil
		}

		// Grounded observation + completion, when the caller supplied them.
		uiState := ""
		if o.Observe != nil {
			if s, oerr := o.Observe(loopCtx); oerr == nil {
				uiState = s
			}
		}
		if o.Observe != nil && uiState != "" && isMutatingKind(act.Kind) {
			if haveState && uiState == prevState {
				noChange++
			} else {
				noChange = 1
			}
			prevState, haveState = uiState, true
			if o.NoChangeStopsAfter > 0 && noChange >= o.NoChangeStopsAfter {
				res.StoppedReason = "no_change"
				return res, nil
			}
		}
		if o.Verify != nil && uiState != "" {
			done, detail, verr := o.Verify(loopCtx, goal, uiState)
			if verr == nil && done {
				res.Completed = true
				res.Verified = true
				res.VerifyDetail = detail
				res.StoppedReason = "verified"
				return res, nil
			}
		}
	}

	res.StoppedReason = "budget"
	return res, nil
}

// TreeText returns a compact, bounded textual signature of the accessibility
// tree for the focused application. It is the engine's default UI-state source
// for Observe/Verify, so completion can be judged on semantics (element names
// and roles) rather than pixels. Returns ErrUnsupported when the platform has
// no tree backend.
func (e *Engine) TreeText(ctx context.Context) (string, error) {
	if e == nil || e.Tree == nil {
		return "", ErrUnsupported
	}
	root, err := e.Tree.ElementTree("")
	if err != nil {
		return "", err
	}
	const maxBytes = 16 * 1024
	var b strings.Builder
	var walk func(n Node, d int)
	walk = func(n Node, d int) {
		if d > 8 || b.Len() > maxBytes {
			return
		}
		label := strings.TrimSpace(n.Role)
		switch {
		case n.Name != "":
			label += ":" + n.Name
		case n.Value != "":
			label += ":" + n.Value
		}
		if label != "" {
			b.WriteString(label)
			b.WriteByte('\n')
		}
		for _, c := range n.Children {
			walk(c, d+1)
		}
	}
	walk(root, 0)
	return b.String(), nil
}

// sameAction reports whether two actions are the same observable event. Used
// for stall detection: a model that re-issues an identical action is not making
// progress, whatever its stated reason.
func sameAction(a, b Action) bool {
	if a.Kind != b.Kind || a.X != b.X || a.Y != b.Y || a.Text != b.Text ||
		a.DX != b.DX || a.DY != b.DY || a.ToX != b.ToX || a.ToY != b.ToY ||
		a.Button != b.Button {
		return false
	}
	return strings.Join(a.Keys, "+") == strings.Join(b.Keys, "+")
}
