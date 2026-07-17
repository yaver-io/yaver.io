package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Prompts are the ship barrier's COOPERATIVE lever. The gate (autorun_gate.go)
// is the coercive one. They are different in kind and the design needs both:
//
//   - A prompt asks. The runner may ignore it, misread it, wedge on a trust
//     prompt, or cheerfully claim done and keep editing. It guarantees nothing.
//   - The gate tells. Nothing talks its way past a flag checked at the iteration
//     boundary. It guarantees everything, and costs up to autorunKickTimeout
//     (30m) to take effect on a loop already mid-kick.
//
// Cooperation alone is not a barrier. Coercion alone means waiting half an hour.
// Together, toparla collapses the common case to minutes and the gate is what
// makes it true rather than merely likely.
//
// The prompt is an optimization. The flag is the oracle. Nothing in this file
// may ever be treated as a guarantee.

// shipPromptToparla is the wrap-up prompt.
//
// The wording is load-bearing, and the target is BASELINE-OK, NOT DONE.
//
// A wrap-up prompt that reads as "finish up" invites precisely the behavior that
// must never reach a deploy: a runner racing to make a half-built feature LOOK
// green under time pressure. So this prompt does the opposite of urging
// completion — it explicitly forbids trying to finish, explicitly licenses
// reverting work that is not ready, and promises the continuation (devam) so
// stopping costs the runner nothing.
//
// Not ready is fine. Build-breaking is not.
const shipPromptToparla = `toparla — the machine is about to deploy.

Stop starting new work. Do NOT try to finish your task.

Get to the nearest state where the build is OK and the basics pass:
  1. Make it compile. Run the gate. Fix only what is breaking the build.
  2. Commit and push what passes.
  3. If part of your work is not yet baseline-OK, revert or stash that part
     rather than rushing it green. Half-done is fine. Build-breaking is not.

Leave nothing uncommitted and nothing half-edited. Do not begin anything new.

You are NOT being asked to deliver. You are being asked to reach a ledge, not a
summit. You will be told to continue (devam) as soon as the deploy lands, and
your task is unchanged — so stopping here costs you nothing.`

// shipPromptDevam is the resume prompt — the mirror of toparla.
//
// Thawing the gate is necessary but not sufficient. A resumed runner wakes with
// no idea that time passed, that a deploy happened, or that main moved under it;
// its next iteration would read a repo it does not recognize. The gate makes it
// able to work again; this tells it what happened while it was held.
const shipPromptDevam = `devam — the deploy is done. Continue.

Main has moved: pull before you touch anything.

Your task is unchanged. Pick up where toparla interrupted you, including
anything you reverted or stashed to reach a safe build — that work was parked,
not cancelled. Recover it and carry on.`

// shipPromptLibrary is the attachable prompt set. Named prompts so a user can
// reach for one by name from any surface — including by voice — without a
// rebuild, and can attach their own ad-hoc text instead.
var shipPromptLibrary = map[string]string{
	"toparla": shipPromptToparla,
	"devam":   shipPromptDevam,
}

func shipPromptNames() []string {
	names := make([]string, 0, len(shipPromptLibrary))
	for k := range shipPromptLibrary {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// resolveShipPrompt maps a name to its text, falling back to treating the input
// as the prompt itself. That fallback is the `--prompt "son bir test"` path: an
// unknown name is an ad-hoc prompt, not an error, because the surface this is
// driven from is often a voice utterance.
func resolveShipPrompt(nameOrText string) string {
	s := strings.TrimSpace(nameOrText)
	if s == "" {
		return ""
	}
	if text, ok := shipPromptLibrary[strings.ToLower(s)]; ok {
		return text
	}
	return s
}

// shipPromptDelivery records what happened to one runner's prompt.
//
// Delivered means bytes were written to a pane. It does NOT mean the runner read
// it, understood it, or acted on it — see shipPromptResult.
type shipPromptDelivery struct {
	Session   string `json:"session"`
	Runner    string `json:"runner,omitempty"`
	Delivered bool   `json:"delivered"`
	Detail    string `json:"detail,omitempty"`
}

// shipPromptResult is the fleet-wide outcome of one prompt broadcast.
type shipPromptResult struct {
	Prompt     string               `json:"prompt"`
	Deliveries []shipPromptDelivery `json:"deliveries"`
	// Delivered/Failed count PANE WRITES, not acknowledgements. A ship that
	// treated Delivered == len(Deliveries) as "the fleet wrapped up" would be
	// wrong; the drain is the only thing that knows. Reported so a human can see
	// which runners never got the message and why the drain took the long path.
	Delivered int `json:"delivered"`
	Failed    int `json:"failed"`
}

// broadcastShipPrompt writes one prompt to every live runner pane on this
// machine.
//
// Delivery is not acknowledgement, and this function is careful not to imply
// otherwise. tmux send-keys reports that bytes reached a pane — nothing more. A
// pane sitting on claude's folder-trust prompt (which --dangerously-skip-
// permissions does not skip) will swallow the text into the prompt rather than
// the runner, and report success. Callers must wait on the drain, never on this
// return value.
func broadcastShipPrompt(ctx context.Context, k *RunnerKeeper, prompt string, source string) shipPromptResult {
	res := shipPromptResult{Prompt: prompt, Deliveries: []shipPromptDelivery{}}
	if k == nil || strings.TrimSpace(prompt) == "" {
		return res
	}
	for _, s := range listRunnerPTYSessions() {
		if ctx.Err() != nil {
			break
		}
		d := shipPromptDelivery{Session: s.Name, Runner: s.Runner}
		// Enqueue rather than send-keys directly: the keeper already owns the
		// idle-pane debounce that keeps a prompt from landing in the middle of a
		// runner's turn and being eaten by its composer. Reusing it means the
		// prompt arrives when the runner can actually read it.
		if _, err := k.EnqueuePrompt(s.Name, prompt, source); err != nil {
			d.Detail = err.Error()
			res.Failed++
		} else {
			d.Delivered = true
			res.Delivered++
		}
		res.Deliveries = append(res.Deliveries, d)
	}
	return res
}

func (r shipPromptResult) summary() string {
	if len(r.Deliveries) == 0 {
		return "no live runner panes"
	}
	return fmt.Sprintf("%d/%d panes queued", r.Delivered, len(r.Deliveries))
}
