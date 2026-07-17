# Ship barrier — freeze the fleet, converge, deploy once, thaw

Status: **built**. `yaver ship` + the `ship` / `ship_status` / `ship_prompts` ops
verbs, over the `autorun_pause_all` / `autorun_resume_all` gate primitives.

| Piece | Where |
|---|---|
| Gate: freeze, lease, exemption | `desktop/agent/autorun_gate.go` |
| Park point in the loop | `desktop/agent/autorun_cmd.go` (top of the iteration) |
| Coordinator: the 9 phases | `desktop/agent/ship.go` |
| Fan-out: freeze/thaw/renew | `desktop/agent/ship_fanout.go` |
| `toparla` / `devam` | `desktop/agent/ship_prompts.go` |
| Target detection | `desktop/agent/ship_targets.go` |
| Detached ship sessions | `desktop/agent/ship_session.go` |
| Verbs / CLI | `desktop/agent/ship_ops.go`, `ship_cmd.go` |

Known gap: the `web-cloudflare` step shells `./scripts/deploy-web.sh`, which needs
a local `CLOUDFLARE_API_TOKEN` that no longer exists (vault v2). Web currently has
to ship via `gh workflow run release-web.yml`. Ship will report that step as
failed rather than pretend.

The utterance this exists to serve:

> "stop all autoruns on the mini, make it compilable, commit push deploy to all
> platforms, then keep autoruns working after all deploys"

One sentence, said from the couch, that ends with the user outside downloading
the build. Everything below is in service of making that sentence safe.

## Why this cannot be a script

Three facts about the fleet make the naive version wrong:

1. **N loops would cause N deploys.** Each autorun commits and pushes every
   iteration (`autorun_cmd.go:413-425`). `CLAUDE.md` already says "one deploy per
   converged change — never one per iteration", but says it as *discipline*. A
   barrier makes it *structural*: loops that are parked cannot trigger a deploy.
2. **Main is regularly red.** Recent history is full of `fix(agent): main did not
   compile` and `fix(deploy): main did not build`. "Deploy all platforms" against
   an unverified main ships a brick — and TestFlight has no rollback, only
   supersede, at ~15-20 uploads/day.
3. **The freeze and the deploy happen on different machines.** Autoruns run on
   the mini and the Hetzner box; TestFlight can only be uploaded from the user's
   Mac (CI runner keychains lack the registered UDIDs). The coordinator is
   therefore always remote from at least one gate it is holding.

Fact 3 is what turns this from a script into a distributed protocol.

## Topology

```
   MacBook (coordinator + deploy host — pinned here by TestFlight)
       │
       │  ops fan-out via machine:<alias>  (ops.go:281)
       ├──────────────► mini      : autorun_pause_all  → loops park
       └──────────────► hetzner   : autorun_pause_all  → loops park
                                      │
                                      ▼
                            loops push their last iteration
                                      │
       ┌──────────────────────────────┘
       ▼
   MacBook: pull main → repair until it builds → detect targets → deploy once
       │
       └──────────────► broadcast autorun_resume_all → fleet continues
```

The freeze fans out; the deploy does not. These are different machine sets and
the design must never conflate them:

- **freezeTargets** — every machine running autoruns. Named explicitly ("the
  mini") or discovered from the device list.
- **deployHost** — always this Mac, because mobile is in scope.

## Phases

| # | Phase | Fails how |
|---|---|---|
| 1 | **`toparla`** — prompt every live runner to reach baseline-OK | best-effort; non-ack is reported, not fatal (D2a) |
| 2 | **Freeze** — `autorun_pause_all` to every freezeTarget | unreachable machine ⇒ abort (D3) |
| 3 | **Drain** — wait ≤10m for loops to reach the gate | expiry ⇒ proceed on the pinned SHA (D2a-i) |
| 4 | **Pin** — resolve main to a SHA on the deployHost | conflict ⇒ abort + thaw |
| 5 | **Repair** — make the pinned SHA compile (D4) | still red after N ⇒ abort + thaw |
| 6 | **Detect** — which targets the new commits touch (D5) | — |
| 7 | **Deploy** — one deploy per target, coalesced | partial ⇒ record + thaw (D8) |
| 8 | **Thaw** — `autorun_resume_all` everywhere | always runs (D8) |
| 9 | **`devam`** — tell the woken fleet what happened | best-effort (D2b-i) |

Note phases 1→2 and 8→9 are deliberately mirrored: prompt-then-freeze on the way
in, thaw-then-prompt on the way out. Both orderings exist because a parked loop
cannot read its pane.

## Decisions

### D1 — The freeze is machine-wide, not per-run

Keyed on neither run ID nor Slot. The point of a freeze is that nothing is
mid-flight during a deploy, so a loop that *starts* during the window has to park
too — a per-run flag cannot hold a run that did not exist when the freeze was
called. Implemented as `autorunGate` (`autorun_gate.go`).

### D2 — Freezing is instant; draining is not. Never conflate them.

`paused` stops the *next* iteration immediately. A loop already inside
`autorunKick` has up to `autorunKickTimeout` (**30 minutes**) before it reaches
the gate, and it can still commit and push until it does.

So the gate reports two different things, and `drained` — not `paused` — is the
"safe to deploy" signal:

```
drain: { paused: true, parked: [...], draining: [...], drained: false }
```

A caller that deploys while `draining` is non-empty is not *wrong*, but it is
choosing something specific: it ships everything through the last completed
iteration, and the in-flight one lands on the next ship. That is coherent because
every iteration is independently gated and committed. It must be a choice, not an
accident.

### D2a — "toparla": ask the runner to wrap up, don't just wait for it

D2's 30-minute drain is only unavoidable if the barrier is purely *coercive* —
if the only lever is a flag the loop checks between iterations. But the runner is
sitting in a tmux pane, and `runner_keeper` already knows how to put a prompt in
front of it (`runner_keeper.go`, queue at `~/.yaver/runner/queue.json`, delivered
by `tmux send-keys` on an idle-pane hash debounce).

So the barrier gets a second, softer lever: **send every live runner a wrap-up
prompt.** `toparla` — gather it up, pull it together.

> **toparla** — Stop starting new work. Do **not** try to finish your task.
> Get to the nearest state where the build is OK and the basics pass: make it
> compile, run the gate, commit and push what passes. If part of your work is not
> yet baseline-OK, revert or stash that part rather than rushing it green. Leave
> nothing uncommitted and nothing half-edited. The machine is about to deploy;
> you will be told to continue right after.

The target is **baseline-OK, not done** — and that distinction is load-bearing.
A wrap-up prompt that reads as "finish up" invites the exact behavior that must
never reach a deploy: a runner racing to make a half-built feature *look* green
under time pressure. `toparla` asks for the nearest safe commit, explicitly
licenses reverting work that is not there yet, and promises the continuation. The
runner is being asked to reach a ledge, not a summit.

This is also why `toparla` is safe to send mid-task at all. Because every autorun
iteration is already independently gated and committed, "the nearest baseline-OK
state" is usually a few minutes away, not a feature away.

The two levers are different in kind, and the distinction is the whole design:

| | mechanism | guarantee | latency |
|---|---|---|---|
| **toparla** | prompt into the tmux pane | **none** — the runner may ignore it, misread it, or already be wedged | fast: the runner converges on its own |
| **gate** | flag checked at the iteration boundary | **absolute** — no new iteration can start | slow: up to 30m for an in-flight kick |

Cooperative alone cannot guarantee anything. Coercive alone makes you wait half an
hour. Together: `toparla` collapses the common case from ~30 minutes to however
long a wrap-up takes, and the gate is what makes it *true* rather than merely
likely.

Order matters. Send `toparla` **first**, then freeze:

1. `toparla` → every live runner pane. The runners start wrapping up.
2. `autorun_pause_all` → loops cannot begin a new iteration.
3. Drain. Runners land their commits and hit the gate. `drained: true` arrives in
   minutes, not tens of minutes.

Freezing first would be worse than useless: a frozen loop that finishes its kick
parks immediately, so it never reaches the point where it would read the queued
prompt. The prompt would sit in the queue until after the deploy — arriving as a
"wrap up, we're deploying" message about a deploy that already happened.

### D2a-i — `toparla` has a hard timeout (~10m), and expiry is not a failure

Cooperation gets a deadline. Default **10 minutes**, fleet-wide — one clock for
all autoruns on all machines, not per-runner, because the user asked for one
utterance and one wait.

The number is chosen against `autorunKickTimeout` (30m), not out of a hat: 10m is
long enough for a runner to reach a ledge, and short enough that it is not just
the 30m wait wearing a different hat. If cooperation were given the full 30m,
`toparla` would buy nothing over the gate alone.

**At expiry, ship proceeds. It does not wait, and it does not stop the run.**
This is safe for one specific reason worth stating plainly:

> **The deploy pins a SHA.** Ship resolves main to a commit and deploys *that*.

So a runner that lands its work at minute 15, mid-deploy, is not a race and not a
corruption — its commit simply is not in this build, and ships on the next one.
Every iteration is independently gated, so the tail of the fleet is never
half-applied. The cost of a `toparla` timeout is *latency of one feature by one
ship*, not breakage.

What ship must **not** do at expiry is kill the runner to force the issue. That
throws away a live in-progress iteration and, per the loop's rollback path, parks
its work in a diagnostic stash — trading a one-ship delay for a human cleanup.
Let it finish; the gate catches it on the other side.

Expiry is reported, never swallowed: *"deployed a1b2c3d; 2 of 5 runners were
still in flight and will land on the next ship."*

### D2b-i — `devam`: the fleet is told to continue, not merely unblocked

Thawing the gate is necessary but not sufficient. A resumed runner wakes with no
idea that thirty minutes passed, that a deploy happened, or that main moved under
it — its next iteration reads a repo it does not recognize.

So the return trip is symmetric. After the deploy lands, ship sends **`devam`**:

> **devam** — Continue. The deploy is done and main has moved; pull before you
> touch anything. Your task is unchanged — pick up where `toparla` interrupted
> you, including anything you reverted or stashed to reach a safe build.

Ordering is the mirror of the freeze, and matters for the same reason:

1. `autorun_resume_all` → the gate lifts, loops become live again.
2. `devam` → the panes are told what happened.

Resume first, then prompt — the reverse of `toparla`. A parked loop is not reading
its pane; prompting a frozen fleet would queue a message about a deploy that gets
read only after the loop wakes anyway. Wake it, then tell it.

Two things this must not pretend:

- **`toparla` is best-effort and must be reported as such.** Ship reports which
  runners acknowledged and which did not; a non-acknowledging runner is not an
  error, it just means the gate does the work and the drain takes longer.
- **It never replaces the gate.** A runner that says "done!" and keeps editing is
  a normal failure mode of language models. The flag is the oracle; the prompt is
  an optimization.

### D2b — Prompts are attachable, not hardcoded

`toparla` is the first of a family, not a special case. The barrier takes a named
prompt from a small library so the user can attach their own without a rebuild:

```
yaver ship --prompt toparla          # default: wrap up, compile, commit
yaver ship --prompt "son bir test"   # ad-hoc
yaver ship --no-prompt               # gate only; pure coercive drain
```

Stored alongside the runner queue, delivered through the same `send-keys` path.
The prompt is per-ship, not per-machine — one utterance goes to the whole fleet.

### D2c — tmux hazards this inherits

Driving panes means inheriting the tmux failure modes already learned the hard
way:

- **A long-lived tmux server whose cwd is a deleted worktree kills every runner
  TUI in it**, and the errors lie about why (claude reports "Bun ENOENT", codex
  "os error 2"; only opencode says it plainly). Ship creates and deletes
  worktrees around repairs — it must never leave the server's cwd inside one.
- **Never target `:0`.** Pane targets are resolved explicitly, never positionally.
- **A trust prompt blocks delivery.** `claude` in an unseen directory sits on a
  folder-trust prompt that `--dangerously-skip-permissions` does not skip; a
  `send-keys` into that pane goes into the prompt, not the runner. Ship must
  detect a pane that is not at a runner prompt and report it as un-acknowledged
  rather than assume delivery.

Delivery is not acknowledgement. `send-keys` reports that bytes were written to a
pane — nothing more. Acknowledgement is the pane hash changing, or better, the
loop actually reaching the gate. Ship must wait on the drain, never on the write.

### D3 — An unreachable freezeTarget aborts the ship

If the mini is offline, its loops are not frozen; they will push into the middle
of the deploy. Freezing three of four machines is not a partial success, it is a
false sense of one. Abort, thaw whatever was frozen, and say which machine could
not be reached.

### D4 — "Make it compilable" is a bounded repair loop, and it must be exempt from its own freeze

This is the subtle one, and it is a deadlock waiting to happen.

The natural implementation of "make it compilable" is an autorun: kick a runner
until a gate passes is *precisely* what `autorunLoop` already does. So repair is
`autorun_start --gate 'go build ./...' --max-iters 3`.

But ship has just frozen every autorun on the machine. A repair loop started
under that freeze **parks itself instantly and forever**. Ship then waits for a
repair that is waiting for ship.

The gate therefore needs an **exemption**: the repair loop's ID is registered as
exempt before it starts, and `autorunGate.await` returns immediately for it. The
exemption is narrow — one run ID, cleared when the repair ends — because a broad
"exempt repairs" rule would let any loop claim to be one.

Bounds, because an unbounded repair at 3am is how you wake up to 200 commits:
- `maxIters` small (3), strict scope, gate = the real build.
- Still red after N ⇒ abort, thaw, notify. Do **not** deploy a red main.
- Repair commits are ordinary gated autorun commits, so they land like any other.

### D5 — Target detection is a diff against the last ship, not a guess

A durable marker is required, because "what changed since last time" cannot come
from memory. Use a git tag (`ship/last`) moved on every successful ship.

```
git diff --name-only ship/last..HEAD
```

| Path prefix | Target | Notes |
|---|---|---|
| `cli/**`, `desktop/agent/**` | npm | via `cli/v*` tag |
| `web/**` | Cloudflare | **must use CI** — no local `CLOUDFLARE_API_TOKEN` since vault v2 died |
| `backend/convex/**` | Convex | `npx convex deploy --yes` |
| `mobile/**` | TestFlight + Play | Mac-only; rate-limited |

Only touched targets deploy. This is what keeps "deploy to all platforms" from
meaning "burn a TestFlight upload because a Go comment changed".

### D6 — Deploy order: Convex → web → npm → mobile

Backend schema first (web and mobile may depend on it), mobile last because it is
slowest and the most rate-limited — a failure there should not have already
burned the cheaper targets' quota... but note the inverse risk: mobile last means
a mobile failure leaves the others already shipped. That is the correct trade
(D8 records it), because the alternative is shipping mobile against a backend
that never landed.

### D7 — Quota is read before it is spent

TestFlight is ~15-20 uploads/app/day with no rollback. Ship reads remaining quota
*before* starting a 20-minute archive and parks the target rather than burning
the run. Out-of-quota is a state that needs a human, not a retry.

### D8 — Failure auto-thaws and notifies

The user is outside. A fleet frozen forever is worse than a failed deploy, so any
abort path resumes the autoruns and pushes a notification saying what broke.
Partial success is recorded per-target so a retry does not redeploy Convex just
because mobile failed.

### D9 — The freeze needs a lease, because the coordinator is on another machine

The one genuinely dangerous asymmetry.

The in-memory freeze needs no persistence *locally*: if an agent restarts, its
loops and its freeze die together, so the flag can never outlive the loops it was
holding. That reasoning holds only on one machine.

Across machines it breaks. If the MacBook coordinator dies mid-ship — crash,
closed lid, dropped tunnel — the **mini's agent is still up and still frozen**,
with no one left to thaw it. The loops are held indefinitely by a coordinator
that no longer exists.

So `autorun_pause_all` must take a **lease TTL**, and the gate must auto-thaw when
the lease expires unless the coordinator renews it. Ship renews on a heartbeat
for as long as it is alive. A dead coordinator therefore degrades to "the fleet
resumes on its own in N minutes" instead of "the fleet is dead until someone
notices".

This is a dead-man switch, and it is the same shape as D8: **when in doubt, the
fleet runs.** A spurious resume costs one racing push. A permanent freeze costs
the whole point of having autoruns.

Implemented as `autorunGate.expiry` + `renew`, with `shipLeaseTTL` = 20m and a 5m
heartbeat from `shipRenewLease`. `leaseExpired` is surfaced in the gate state, so
"the fleet resumed on its own" is never silent — it means a ship died holding the
freeze.

## Surface

```
yaver ship                        # toparla → freeze → repair → deploy → thaw → devam
yaver ship --freeze mini          # freeze only the mini
yaver ship --toparla-timeout 10m  # how long to let runners reach baseline-OK (default 10m)
yaver ship --no-prompt            # gate only; pure coercive drain, no toparla/devam
yaver ship --prompt "son bir test"  # ad-hoc wrap-up prompt instead of toparla
yaver ship --no-repair            # abort instead of fixing a red main
yaver ship --targets convex,web   # override detection
```

Ops verb `ship_run` mirrors it, so the phone and voice surfaces get it for free —
which is the actual point. The utterance at the top of this file is the product;
the CLI is just its typed form.
