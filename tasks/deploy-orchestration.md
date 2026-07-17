---
master: claude
doer: codex
---

# Autorun-level orchestration for deploys

## Why this exists

Autorun already models the hard thing: declarative intent, a **required** gate,
bounded blast radius, named self-heal kinds, a stable address, and iterate-until-
verified. The deploy layer was written earlier and has the same needs with none of
the answers. Almost all of this is pointing the second system at the first
system's spine — read `desktop/agent/autorun.go`, `autorun_ops.go` and
`autorun_cmd.go` first; their header comments are the contract you are copying.

**The law: a deploy is metered, irreversible-ish, and externally visible.** It is
not a build step. Every rule below follows from that.

## Ground rules

- Do the priorities **in order**. Each is one increment the gate can verify.
- Do not weaken a gate to make it pass. A gate that can be bypassed is not a gate
  — that is the bug you are fixing.
- Never make a real deploy from inside this loop. You are changing the machinery,
  not exercising it. Do not run `wrangler deploy`, `convex deploy`, `npm publish`,
  or any `scripts/deploy-*.sh`.
- New status strings must come from ONE vocabulary (P4). Do not invent a sixth.

## P0 — autorun owns its worktree, and always lands it back on main

Two loops in one checkout destroy each other. `rollbackAutorunChanges`
(`autorun.go:417`) runs `git stash push --include-untracked` on the **whole**
workDir, so any loop's failed iteration stashes every *other* loop's live edits —
and the clean-tree precondition means a second loop can't even start while the
first has edits. The mini currently carries a **9-stash graveyard** that is
exactly this.

Autorun has no worktree concept today: `worktree` appears in `autorun*.go` only
in comments, never as `git worktree add`. Build it, in the Go agent and exposed
over MCP/ops — **not as a shell recipe a human runs first.** Setting this up by
hand is error-prone: a symlinked `node_modules` reads as untracked because
`.gitignore` says `node_modules/` (trailing slash = directories only, not
symlinks), which trips the clean-tree check. The agent should own the whole
lifecycle so no operator has to know that.

- The slot key **is** the address: `~/.yaver/worktrees/<slot>`, branch
  `autorun/<slot>`. `autorunSlotKey` (`autorun.go:284`) deliberately excludes the
  machine and worktrees are per-machine, so this needs no new identity.
- Autorun creates the worktree, prepares deps, and removes it when the slot is
  released. A restart lands back in the same tree — that is the point of a stable
  slot.
- **Branches must never become the new graveyard.** A branch that outlives its
  loop is stranded work, the same disease as a stash. So the loop's terminal step
  is: merge back to `main`, **resolve conflicts itself** (a coding runner is
  precisely the tool for that — feed it the conflict, don't fail the run), push,
  and delete the branch + worktree. Landing is part of converging. A run that
  ends with its work still on a branch is **not** DONE.
- Only after the work lands on `main` may the sink deploy (P6). Deploy from
  `main`, never from a slot branch.

**Builds and deploys must be autorun-aware, or they compile a torn tree.** A
loop is *by definition* mid-edit for most of its life. Any build that reads the
same checkout while a doer is halfway through writing it compiles a half-written
tree and ships it — a broken build with no bad commit to blame, which is the
worst kind to debug. `scripts/deploy-web.sh` builds `web/` straight from the
working tree today, so this is live.

- **Build from a commit, never from a live directory.** A deploy resolves the SHA
  it intends to ship, checks that SHA out into its own detached worktree, and
  builds *there*. No live tree, no race, and the artefact is reproducible.
- This is also a prerequisite for P2: you cannot assert "SHA X is serving" unless
  you built SHA X rather than "whatever was in the folder at 12:04".
- A checkout that has a live autorun slot is **not** a build input. If something
  must read one, it needs an advisory lock — `deployMu` (`deploy_pipeline.go:72`)
  serialises deploys against each other but knows nothing about autorun, so it
  does not help here.

**The lock is scoped, never global — this is a monorepo.** A repo-wide build lock
would mean a loop compiling `mobile/**` blocks a loop working on
`backend/convex/**`, which share nothing. That is not safety, it is queueing for
no reason, and on a box running p0…p9 it would serialise the whole fleet.

- **`--scope` already declares the lock domain.** It is mandatory for remote runs
  and already enforced (`autorun.go` stages by explicit path, never `commit -a`).
  It is now doing triple duty: what a loop may touch, which deploy targets its
  diff implies (P5/P7), and which builds it excludes. Do not invent a fourth
  declaration — derive.
- Disjoint scopes ⇒ **fully parallel**. `mobile/**` and `backend/convex/**` never
  wait on each other, and a mobile build proceeds while a backend loop iterates.
- Overlapping scopes ⇒ **signal, don't corrupt**. The second party waits and says
  why (`blocked` — P6 — naming the slot it is waiting on), rather than reading a
  torn tree or failing with a mystery compile error. A visible wait beats a silent
  race; see the "visible failure over silent retry" law.
- Yaver's MCP/ops layer owns this. `runner_autorun` has no `machine` param today
  and several `machine` params are documented "Reserved for future remote-attach
  routing" — a scope-aware lock is meaningless if the layer holding it cannot
  address the box the slots live on. Fix the addressing as part of this.

## P1 — `ops deploy` lies, and four of its targets do nothing

`desktop/agent/ops_deploy.go`. `opsDeployHandler` maps a target to a CLI string,
calls `StartExec` (`:195`), and returns `OK: true` **as soon as the process
spawns** (`:199-210`) — it never waits, never reads the exit code, never asks
whether anything happened. Worse, `cloud` (`:146`), `platform` (`:177`),
`testflight` (`:182`) and `playstore` (`:187`) are **stubs that deploy nothing
and return `OK: true`** with a hint string.

- Make the verb await completion and report the real exit code.
- A stub must not return `OK: true`. Return an explicit unimplemented result, the
  way `opsDeployRollbackHandler` already honestly refuses with `no_rollback`
  (`:257-261`). Copy that shape.
- `deploy_pipeline.go` already has the real staged path with a health check
  (`:177`) and rollback (`:193`). `mcpDeployRun` (`deploy_pipeline.go:425`)
  reaches it; ops `deploy` routes around it. Converge them: one path, gated.

## P2 — a deploy gate asks the world, not the process

"Deployed" currently means "uploaded" everywhere. Nothing confirms the new
version is serving. Add a required post-deploy gate per target, and treat its
absence as fatal rather than as success.

- `deploy_pipeline.go:288` passes on `StatusCode < 500` — **a 404 counts as
  healthy.** Fix: assert the deployed build identifier, not merely a response.
- Uploaded-but-unconfirmed is **not** success. It is `unknown` — the same
  contract autorun already has, where an empty `finalCommit` means the run did not
  finish however quiet it looks (`agentStatus.ts:189-207` documents the reasoning).
  A confirmed live version is a deploy's `finalCommit`.
- Gates are a question about the world:
  - web → fetch the deployed URL, assert the shipped SHA/version
  - convex → query the deployment for its version
  - npm → `npm view yaver-cli@<v> version`
  - testflight → App Store Connect build state == PROCESSED
    (`desktop/agent/appstoreconnect.go` already speaks this API — reuse it)

## P3 — deploy identity is random; make it a slot

`deploy_history.go`. `NewRunID()` (`:113`) is 8 random bytes, and `:317` sorts by
`StartedAt` descending — so "the current deploy for web" is a guess at the top of
a recency-sorted list. This is **exactly** the shape `autorunSlotKey`
(`autorun.go:284`) was introduced to kill; read that comment, it makes the whole
argument.

- Give a deploy a stable address: `deploy:<target>` (e.g. `deploy:web`).
- Sort by slot, not recency — mirror `sortAutorunViewsBySlot`
  (`autorun_ops.go:162`) and its comment.
- History is in-memory and lost on agent restart (`deploy_history.go:72-87`,
  logs persist but rows don't). A slot that forgets itself on restart is not an
  address. Persist the rows.

## P4 — one status vocabulary (there are five)

- `autorun_ops.go` → `running · completed · failed · stopped · stopping`
- `deploy_pipeline.go:33` → `running · success · failed · rolled-back`
- `deploy_all.go:56` → `deployed · skipped · blocked · failed` (+ gate
  `green/red/forced`)
- `deploy_history.go:58` → **no status at all**, just `OK bool` + `InProgress bool`
- `publish.go:55` → `running · completed · failed · dispatched`

Deploy says `success` where two others say `completed`. Both UIs already paper
over the hole by **synthesising** the missing field — `BuildsView.tsx:395` and
`builds.tsx:595` both do `run.status || (run.ok ? "completed" : "failed")`. When
two surfaces independently invent the same field, it belongs in the model.

Converge on autorun's wire vocabulary, add `unknown` (P2), keep `rolled-back`
(it is a real deploy state autorun has no analogue for). Delete the synthesis in
both UIs once the field is real.

## P5 — the DAG: publish is an edge, deploy is a sink

A push is internal and composable; a deploy is external, metered and effectively
irreversible (`convex`, `testflight`, `playstore` all return `no_rollback` —
`ops_deploy.go:257-261`). So **N loops must not mean N deploys.**

- Task front matter grows two keys, parsed exactly like the seats are today
  (`autorunSeatsFromTask`, `autorun.go:238` — read it; front matter only, never
  prose, and the operator's explicit flag always wins):
  - `needs: [<slot key>, …]` — this loop's dependencies
  - `deploy: auto | none | [targets]`
- A loop **with dependents publishes only** (commit + push). The **terminal** loop
  deploys, once, after the whole queue (p0…p9) converges.
- `needs` takes **slot keys**, not session IDs — a session ID is new on every
  restart, which is the entire reason `task:seat` exists.

## P6 — quota is `blocked`, not `failed`

Nothing anywhere detects a usage limit: grep `autorun*.go` and
`runner_session*.go` for quota / rate-limit / 429 and you get zero hits.

This is the **same bug** as commit `7a5c652d7`. A TUI turn is a pane capture, so a
runner that has hit its limit exits 0 and returns its limit screen; non-empty text
sails past the `instruction == ""` guard in `autorunPlan` and the doer is kicked
with a billing notice as "YOUR INSTRUCTION FOR THIS ITERATION".
`autorunTurnIsSignInChrome` matches a **sign-in phrase**, so a quota screen walks
straight through it.

- Extend the chrome detector to usage-limit screens, with the same both-signals
  rule it already uses (chrome AND a limit phrase — either alone is legitimate,
  since an instruction may legitimately discuss quotas).
- Surface it as `blocked` — the one state that is a request. Not `failed` (nothing
  is broken, retrying won't help) and not `healing` (the loop cannot fix it).
- TestFlight is **quota-aware, not quota-blind**: ~15–20 uploads/app/day, and no
  rollback. Read the remaining budget before spending it and park as `blocked`
  when it's gone. Burning the day's quota on loop iterations is the failure this
  prevents.

## P7 — cost awareness is a product requirement

See the new deploy-cost rule in `CLAUDE.md`. Vercel billed per build harshly,
which is why web runs on Cloudflare Workers — and **Cloudflare is cheaper, not
cheap**. Every deploy is metered somewhere.

- A deploy result should report what it cost or consumed (builds, uploads,
  minutes). `remote_cost` and `switch_cost` are the existing seams — follow their
  shape rather than inventing one.
- Coalesce: never deploy per-iteration, only per converged change (P5).

## P8 — auto-deploy on by default (do this LAST)

Only meaningful once P1–P3 make the gate real. Default-on over a layer that
returns `OK` on spawn automates the *claim* that you deployed, not the deploy.

- `deploy: auto` is the default when the key is absent.
- Per-target defaults in `backend/convex/userSettings.ts` (which already has
  `get`/`set`/`setByToken`/`seedDefaults` — this is a field, not a subsystem):
  `web: auto`, `convex: auto`, `npm: ask`, `testflight: ask`, `play: ask`.
  Rationale is reversibility + cost + quota, not taste.
- `ask` is not a modal on someone's desk — it is `blocked` (P5), pushed, with one
  confirm.
- Precedence mirrors the seats rule: task front matter > userSettings > defaults.
- Surface one three-state row per target in the web dashboard settings and the
  mobile settings screen. One Convex row, both surfaces reading it — do not
  create a second definition (that is the P4 mistake).

## Out of scope

This loop is **Go + Convex only**. Scope is `desktop/agent/**` + `backend/convex/**`
and the gate is `go build ./...` — deliberately, so the loop needs no
`node_modules` and cannot be blocked by JS dep setup. P8's web/mobile settings
rows are a FOLLOW-UP loop; land the Convex field and the precedence rule here,
leave the UI to it.

Do not touch `tasks/ambient-slots.md`'s files (another loop owns `web/**` and
`mobile/**` UI work — coordinate via git, not by editing its files):
`mobile/src/lib/agentSlots.ts`, `agentStatus.ts`, `web/lib/agentStatus.ts`,
`web/app/spatial/**`. Do not add dependencies. Do not perform a real deploy.

## Definition of done

Say DONE, alone, only when P0–P8 are complete and verified in the git log, the
gate passes, and `ops deploy` can no longer report success for a deploy that did
not happen.
