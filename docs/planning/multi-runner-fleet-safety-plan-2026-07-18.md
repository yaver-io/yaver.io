# Multi-Runner Fleet Safety — Implementation Plan

Date: 2026-07-18
Status: plan only. No code in this document has been written.

## 0. How to use this document

**Read this section before touching anything.**

This plan was produced by four independent read-only audits of the code on
2026-07-18. Every claim below carries a `file:line` anchor. Even so:

> **Verify before you trust.** During the audit that produced this plan, three
> separate claims in the project's own memory and doc comments were falsified by
> grep. `verifyDeployPin` was documented as shipped — it does not exist anywhere
> in the repo. The git-ref CAS fleet tier was recorded as "landed" — it has zero
> non-test callers. `autorun_placement.go` reads as production code — it is
> called only by its own tests.

> **CORRECTION (2026-07-18, later same day).** This document failed its own
> rule. Re-verified against `github/main` at `273913f03`, two of those three
> claims were themselves wrong — the audit read a tree that had moved:
>
> | Claim above | Verified on main |
> |---|---|
> | `verifyDeployPin` ABSENT | **WRONG — LIVE.** `deploy_all.go:180` calls it; added in `5dfc3e38a`, which IS an ancestor of main. |
> | Git-ref CAS fleet tier DEAD | **WRONG — LIVE.** Four non-test callers: `autorun.go:837`, `autorun_cmd.go:495`, `autorun_ops.go:171,215`. |
> | `autorun_placement.go` tests-only | **CORRECT at the time — now FIXED.** `runShip` Phase 6.5 calls `checkShipPlacement` as of `273913f03`. |
>
> The trap that produced the false half is worth more than the corrections: the
> greps were run from a branch **ten commits behind main**, so symbols that had
> landed looked absent. A file's mtime gave it away — `deploy_all.go` was dated
> two days BEFORE the commit that modified it.
>
> So the rule needs a second half: **verify, and verify against the right
> ref.** Before trusting a `grep` verdict in this document, run
> `git rev-parse --abbrev-ref HEAD` and `git rev-list --count HEAD..github/main`.
> A non-zero count means every ABSENT/DEAD verdict below is suspect.

So: **re-grep before editing.** If this document and the code disagree, the code
wins and this document is the bug — fix it in the same change (per `CLAUDE.md`).

Three phrases are used with precise meaning throughout:

- **LIVE** — has non-test callers, runs in production.
- **DEAD** — implemented and often tested, but has zero non-test callers.
- **ABSENT** — does not exist.

### Ground rules inherited from CLAUDE.md

These are not negotiable and several phases below depend on them:

- Work on a branch, rebase, merge. Never commit without explicit permission.
- **`git commit -- <paths>` ALWAYS.** Never `-a`, never `add -A`.
- Committed/pushed work is immutable. Never revert someone else's landed work.
- One deploy per converged change. Never deploy to "check" something.
- Never commit credentials, infra IPs, or hostnames.
- Convex privacy contract: no paths, prompts, file contents, or task output.

---

## 1. The problem

A single user runs several coding agents at once — Claude Code, Codex, opencode
(GLM) — plus autorun loops, plus their own hands on a keyboard. These may be on
one machine or several. All of them speak to the Yaver MCP server, and through
it can edit files, run builds, start dev servers, migrate databases, land git
commits, and deploy.

Today they can silently destroy each other's work.

The requirement, in the user's framing:

1. Any runner (claude / codex / opencode / autorun / human) must be safe to run
   alongside any other, for the **same user**, without corrupting shared state.
2. This must hold **across machines**, not just across processes on one box.
3. The system must be **git aware** — no automation mutates a tree it does not
   own.
4. It must be **resource aware** — CPU, RAM, SSD/disk — and must decide
   **sequential vs parallel** builds and **reclaim artifacts** rather than
   wedging the machine.
5. It must be **capability aware** — route build/deploy work only to machines
   that can actually do it.
6. **Exactly one deploy** happens, at the end, after work converges. N runners
   must not mean N deploys.

---

## 2. Verified state of the code

### 2.1 The single structural fact

**Exactly one cross-process synchronization primitive exists in the entire
desktop agent**: the vault's `flock(2)` (`vault_lock_unix.go:35`, taken at
`vault.go:726`).

Everything else is a Go `sync.Mutex` — which serializes goroutines inside **one
process** and is worth nothing across Claude Code, Codex and opencode as
separate processes, and nothing at all across machines — or a probe-then-act
check, which is TOCTOU.

### 2.2 Coordination primitives

| Primitive | File | Scope | Status |
|---|---|---|---|
| Admission control | `autorun_coordination.go:175` | one process | **LIVE** |
| Typed leases (path/build/seat/land) | `autorun_leases.go:46-60` | one process | **LIVE** |
| `land/<base>` lease | `autorun_leases.go:59` | — | re-verify; `autorun.go:837` takes a fleet lease on the land path |
| Git-ref CAS fleet tier | `autorun_leases_git.go` | fleet | **LIVE** (corrected) — reached via `autorunFleetLeases` |
| `fleetLeaseCoordinator` | `autorun_leases_fleet.go:53` | fleet | **LIVE** (corrected) — `autorun.go:837`, `autorun_cmd.go:495`, `autorun_ops.go:171,215` |
| `autorunLandMu` | `autorun.go:794` | one process | LIVE |
| `opsGitLandMu` | `ops_git_land.go:43` | one process | LIVE, **deliberately a separate lock** from `autorunLandMu` |
| Placement / eligibility | `autorun_placement.go:78-256` | — | was DEAD (correctly reported); **now LIVE** — `ship.go` Phase 6.5, `273913f03` |
| Vault flock | `vault_lock_unix.go:35` | **cross-process** | LIVE, but see §2.5 |

Verify the DEAD claims with:

```bash
grep -rn "fleetLeaseCoordinator\|newGitLeaseClient" desktop/agent/*.go | grep -v _test.go
grep -rn "placeAutorunTarget\|autorunPlacementPlan" --include=*.go . | grep -v _test.go
grep -rn "verifyDeployPin" .
```

### 2.3 There is no actor identity

MCP calls are anonymous. Nothing distinguishes a Claude Code session from Codex
from opencode from a human clicking in the web UI. The only `clientId` in the
system is a self-declared string on `runtime_take_control`
(`mcp_tools.go:3547-3564`) for remote-runtime video sessions.

`autorun_leases` **is exposed over MCP but is read-only** — its handler calls
`autorunLeases.Snapshot()` (`autorun_leases.go:316`) and returns. There is no
acquire, renew, or release verb. **A third-party runner cannot take a lease as
itself.** Its only coordination primitive today is the coarse
`autorun_pause_all`.

This is the root blocker: a claim cannot be attributed, renewed, or reaped
without a stable identity to attach it to. Phase P0 exists for this reason.

### 2.4 Direct "runners break each other" mechanisms

These are not theoretical. Each is a specific code path.

| # | Mechanism | Anchor |
|---|---|---|
| A | **`TaskManager.workDir` is process-global.** One actor's `set_work_dir` silently redirects every other actor's workDir-defaulting verb to the wrong repo. Written under mutex at `httpserver.go:6440`, **read unlocked** at `httpserver.go:5676, 6108, 6250, 7250, 11128, 13473` and `tasks.go:1018, 1221, 1922`. `tasks.go:1221` sets `cmd.Dir` for the spawned runner from it. | `tasks.go:1132` |
| B | **`DevServerManager` is a singleton that kills the incumbent.** Any start unconditionally `Stop()`s the running dev server and cancels its context, then drops SSE replay history. | `devserver.go:452-473` |
| C | **`runner_pty.go` default tmux session name is the runner ID alone**, then `new-session -A`. Two clients on `?runner=claude` land in the **same TUI**. With `?fresh=1`, the second **kills the first mid-turn** (`runner_pty.go:119-120`). The autorun path already learned this and includes the runner ID (`autorun_tmux.go:92-98`); the lesson never propagated. | `runner_pty.go:105-108, 134` |
| D | **Nine `git add -A` sites** bypass the `ops_git_verbs.go` pathspec contract: `git_commit_push.go:101`, `git_http.go:397`, `repos_http.go:302`, `vibing_actions_http.go:72`, `managed_git.go:209`, `managed_git.go:618`, `switch_steps.go:42`, `deploy_all_cmd.go:414`, `devserver_pull_fast.go:140`, `template.go:237`. | — |
| E | **`git pull --rebase --autostash`** stashes another runner's uncommitted edits, then restores them across a rebase that may conflict. | `vibing_actions_http.go:80` |
| F | **`tasks.json` uses bare `os.WriteFile`** — no tmp+rename, no fsync, no lock. `Load` returns an **empty map** on parse failure, so a torn write silently discards every task record. Compare `saveConfigUnchecked` (`config.go:585`) which does all three correctly, same directory. | `store.go:77, 99-102` |
| G | **Unsynchronized lazy init** of `s.dbLifecycleMgr` on the shared `HTTPServer` at `mcp_workspace_handlers.go:513, 527, 538, 548, 559, 570, 583, 594`. A genuine Go data race; the loser gets a *different manager instance*, so `db_lifecycle.go:53`'s mutex protects nothing. | — |
| H | **Vault lost update.** `flock` is held only around write+rename, not around read-modify-write. `entries` is an in-memory map loaded at unlock time; `Set` mutates that stale map and re-serializes the whole thing. Disk is never re-read under the lock. flock failure is **fail-soft** (`vault.go:727-732`). Temp path is fixed `vs.path + ".tmp"` (`vault.go:800`). | `vault.go:468-503, 725-771` |
| I | **Config lost update.** Atomic write, but no lock and no version check. The blank-field salvage (`config.go:556-580`) only protects *blank* values; a stale non-blank `AuthToken` overwrites a freshly rotated one. | `config.go:542-640` |
| J | **Convex `upsertProject` is mis-keyed.** Index is `(userId, slug)` — `deviceId` is **not** in the key though it is in the patch. Every machine with a `yaver.io` checkout writes one shared row and stamps its own `deviceId` over the last, so P2P routing dials the wrong machine. | `backend/convex/agentSync.ts:29-40` |
| K | **Dev server ports are hardcoded with no scan**: RN 8081 (`devserver.go:2091`), Vite 5173 (`:2433`), Next 3000 (`:2478`), Flutter 9100 (`:2160`). Expo scans +1..+20 but with a TOCTOU probe (`:1687-1700`, `:2039`). | — |
| L | **`convexSyncer.callMutation` has no concurrency control** — no ETag, version, or CAS; failures are silently swallowed. | `convex_state_sync.go:412-425` |

### 2.5 Resource awareness

| Signal | Measured | Consumed by | Binding? |
|---|---|---|---|
| Free disk (workdir) | `autorun_resources.go:40` | `autorun_cmd.go:346-357` | **Binding** — 3 GB floor |
| **TOTAL** RAM | `autorun_resources.go:43-45` | `autorun_cmd.go:340-343` | Binding but **useless** |
| Load avg / core | `autorun_resources.go:57-66` | `autorun_cmd.go:362-374` | Advisory — park one interval |
| Disk % background | `diskhealth.go:85-88, 384` | statuspage, phone push | Advisory — no scheduler reads it |
| `Hardware.RAM` / `.DiskFree` | `pipeline.go:52+` | `agent_mesh.go:355-362` (+35/+20) | Advisory, 24h-cached snapshot |
| `MaxTaskSlots` | `console_machines.go:266-282` | `agent_mesh.go:146, 401` | **Binding** — the only hardware→admission link, and it is a static CPU-shaped constant |
| Thermal | `mcp_sysadmin.go:377` | MCP dispatch only | Human-facing |
| `df`/`free`/`load`/`top`/`vmstat`/`iostat` | `mcp_sysadmin.go` | **MCP dispatch only** | Human-facing; return **raw unparsed strings** |
| Human presence | **ABSENT** | — | — |

Four findings that drive Phase P4:

1. **The RAM floor reads `TotalRAMGB`, not free.** `autorun_resources.go:43-45`
   feeds `getSystemMemoryMB()` into a 2.0 GB floor. This is a static
   machine-class check that either always passes or always fails on a given box.
   **A 64 GB Mac with 200 MB free passes.** It can never fire in response to
   actual memory pressure.
2. **`go clean -cache` is user-global.** `reclaimAutorunDisk` (`autorun.go:137-148`)
   wipes `GOCACHE` unconditionally once disk < 3 GB. Every concurrent run on the
   box and every human `go build` in any other repo pays a cold rebuild. There
   is no lease or coordination around it.
3. **Two disk-reclaim implementations with different safety models.**
   `ops_diskguard.go` is careful — 85% threshold (`:49`), 60-minute `minAge` to
   avoid live files (`:55`), protected-names deny list (`:77+`), dry-run default
   (`:645-650`). Autorun's reclaim uses **none** of it.
4. **`build_preflight.go` has no disk or memory check at all** — `preflightResult`
   (`:53-60`) has no such field; it gates only host OS and toolchain. A direct
   `build_ios`, `xcode_build`, `gradle_build`, `docker_build`, or `deploy_run`
   performs **no free-space preflight whatsoever**. The documented 20 GB mobile
   floor lives only in `~/.local/bin/mobile-cache-cleanup.sh` — outside the repo,
   invoked by no Go code.

### 2.6 Capability awareness

**The capability booleans probe toolchains but are named and reported as
credentials** (`console_machines.go:232-235`):

```go
caps.SupportsTestFlight = runtime.GOOS == "darwin" && toolLooksInstalled("xcrun")
caps.SupportsIOS        = caps.SupportsTestFlight || runtime.GOOS == "darwin"
caps.SupportsPlayStore  = toolLooksInstalled("java") || toolLooksInstalled("javac") || toolLooksInstalled("gradle")
caps.SupportsAndroid    = caps.SupportsPlayStore || toolLooksInstalled("adb")
```

`toolLooksInstalled` is `exec.LookPath` (`:313-316`). So `capTestFlight`
(`autorun_placement.go:176-184`) rejects with `"no App Store Connect
credentials"` on a signal that is literally `darwin && which xcrun`. A Mac with
Xcode and zero App Store credentials is declared eligible to deploy to
TestFlight. Same shape for Play Store: `java` on PATH ⇒ "can publish".

**Machine profile tags override capability with no probe at all**
(`console_machines.go:249-261`): tags containing `testflight`/`xcode`/`ios` set
`SupportsIOS` and `SupportsTestFlight` true unconditionally.

**Scoring has no floor.** `chooseNodePlacement` (`agent_mesh.go:182-192`) sorts
descending and takes `placements[0]` **unconditionally**. A machine that is
offline (−5000), lacks the toolchain (−180) and blocks API keys (−1500) still
gets the work if it is the only candidate. And at `:154-156`, if the allow-list
filters everything out, the filter is **discarded** and all machines become
candidates again.

**Vocabulary mismatch.** `ship_targets.go` (LIVE — decides *what* to deploy from
a git diff) uses `web-cloudflare`, `testflight-ios`, `playstore-android`,
`cli-npm`, `convex`. `autorun_placement.go` (DEAD — decides *where*) uses `web`,
`ios`, `android`, `agent`, `convex`. The two cannot be joined without a
translation table that does not exist.

**A correction the implementer must carry:** `autorun_placement.go:114-130`
hardcodes web and agent/npm as `CIOnly: true`. **This is wrong as a static
fact.** `CLAUDE.md` documents local-first deploys for both
(`./scripts/deploy-web.sh`, `cd cli && npm publish`), and the project's own notes
contain a contradictory claim that no local Cloudflare token exists after a
vault migration. Both are true at different times — which is precisely why
capability must be **probed at plan time, never declared in a static table**.
TestFlight remains Mac-only for a genuinely non-credential reason: CI runner
keychains lack the registered iPhone UDIDs, so `release-mobile.yml` is
`if: false`.

### 2.7 The deploy barrier

`runShip` (`ship.go:105`) is a careful 7-phase pipeline: toparla → freeze →
drain → pin → repair → detect → deploy-once → mark, with `context.WithoutCancel`
thaw on every exit path (`ship.go:150-152`). Within its stated scope it is good
work. Its guarantee is much narrower than it appears.

| Gap | Anchor |
|---|---|
| **Freeze fans out; drain does not.** `autorunDrain()` reads only the local in-process session map. `shipFreezeAll` never passes `waitFor` and `shipCallVerb` discards the response body — even though the remote `autorun_pause_all` handler **does** compute and return a drain payload and **does** accept `waitFor`. | `autorun_gate.go:289-320`; `ship_fanout.go:34, 57-64, 112`; `autorun_ops.go:498-513` |
| **Freeze ≠ drain, by up to 30 min.** A loop already inside `autorunKick` keeps running — and can still commit and push — until it reaches the gate. `paused` is not "safe to deploy"; `parked()` is. | `autorun_gate.go:23-28` |
| **Drain timeout is not an error.** Ship warns and deploys anyway. | `ship.go:167-171` |
| **`verifyDeployPin` is ABSENT.** `shipPinHead` is `git rev-parse HEAD` (`ship.go:335`), used only to compute the diff and move the `ship/last` tag. It is **never passed to `RunDeployAll`** (`ship.go:240` passes only `Only: plan.Targets`), and every deploy step builds from the working tree at execution time. A loop landing a commit during a ~45 min TestFlight archive ships in that build. | — |
| **Freeze targets are a user-supplied list.** `shipFreezeTargets` seeds `["local"]` and appends `opts.FreezeMachines` verbatim. Omit a machine and it is silently never frozen. | `ship.go:46`; `ship_fanout.go:93` |
| **Raw-tmux loops are invisible.** `discoverAutorunTmuxSessions` exists precisely because such loops evade `autorun_stop_all` — and **ship never calls it**. | `autorun_wrapup.go:86` |
| **No `leaseDeploy` class.** A deploy holds no claim, and is not excluded against a `build` lease on the same toolchain. | `autorun_leases.go:46-60` |
| **Quota PARK is designed and never wired.** `placeAutorunTarget(..., quotaExhausted)` has zero production callers. No counter exists for TestFlight, CI minutes, Cloudflare, or npm. The 15-20/day cap lives only in comments. | `autorun_placement.go:198-220` |

**One door in, ≥10 around.** No deploy path anywhere consults `autorunFreeze` —
zero hits in any deploy file:

| Door | Barrier? |
|---|---|
| `ship` (`ship_ops.go:15`) | **Yes** — the only one |
| `deploy_all` (`deploy_all.go:329`) | No — same fan-out ship uses, reachable directly |
| `deploy_run` (`deploy_pipeline.go:425`) | No |
| `publish_run` / `submit` / `upload` / `ci_dispatch` (`publish.go:413`) | No |
| `convex_deploy` (`mcp_platforms.go:61`) | No |
| `cf_deploy` (`mcp_platforms.go:102`) | No |
| `mobile_platform_deploy` (`deploy_platform_cmd.go:153`) | No |
| `site_deploy` (`mcp_workspace_handlers.go:1387`) | No |
| `github_workflow_run` (`mcp_platforms.go:558`) | No — off-machine by construction |
| all 15 `scripts/deploy-*.sh` | No — `grep -l "autorun\|freeze\|ship/last"` returns empty |

`ship_session.go:46-50` guards only ship-vs-ship.

---

## 3. Design decisions

These are the load-bearing choices. Understand them before implementing; they
explain why the phases are shaped as they are.

### D1 — Identity first, because a claim needs a holder

`gitLeaseRecord.Holder` is typed as "autorun session ID"
(`autorun_leases_git.go`). The whole lease namespace assumes an autorun run.
Widening `Holder` to a general **actor** is the smallest change that lets an
interactive Claude Code session be a first-class lease holder. Nothing else in
this plan can work without it.

### D2 — Quiescence, not termination

`ship` can drain autoruns because an autorun has a terminal state — `converged`,
`no_edits`, `done` (`autorun.go:52-72`). **A human-driven Claude Code or Codex
session has no terminal state.** The human simply stops typing. No event ever
says "I am done."

So "wait for everyone to finish" is undecidable and must not be attempted.
Instead: **every actor holds a TTL lease that it renews on activity.** The lease
expiring *is* the quiescence signal. "Has everyone been quiet for N seconds?" is
decidable; "is everyone done?" is not.

This is why P0's heartbeat is not a nicety — it is the mechanism that makes the
deploy barrier possible for actors that can only be observed, not commanded.

### D3 — Reuse the lease layer; do not build a second one

The typed local tier and the git-ref CAS tier are **already written and already
tested**. The CAS tier is genuinely good design: `git update-ref <ref> <new>
<old>` is a real atomic compare-and-swap, the remote arbitrates so there is no
leader to elect, the namespace is already replicated and authenticated, and a
human can break a stuck claim with plain git. It keeps work-derived data out of
Convex, which the privacy contract requires.

**The work is wiring it, not writing it.** Resist the urge to design a new lock
service.

Honest limits, which the code already documents (`autorun_leases_git.go:21-29`)
and the implementer must respect:

- CAS is atomic per-remote, not instantaneous. Two machines can both believe
  they hold a lease **until one pushes**; the push is the serialization point.
  Good enough for "do not compile the same subsystem"; wrong for anything
  needing sub-second exclusion.
- A lease is only as fresh as the last fetch — so the local tier stays the fast
  path and git is the fleet tier.
- Clock skew makes TTL approximate. **Reap generously, never aggressively.**

### D4 — Capability is probed, never declared

Per §2.6. Every capability answer is `(verdict, evidence, checkedAt)` where
verdict is `yes` / `no` / `no-but-CI-can`, evidence names *what was actually
checked*, and the answer is recomputed at plan time. A vault-locked machine must
degrade to an honest "cannot verify" rather than lying in either direction.

### D5 — Resource admission is a reservation, not a counter

Builds are not uniform. An Xcode archive is disk- and RAM-heavy for 15-45
minutes; a `go build` is CPU-bursty and short. A single "max N builds" number
cannot express that.

So each build target declares an expected **footprint** (RAM GB, disk GB, CPU
weight), and admission asks "does this fit alongside what is already reserved,
against **free** resources?" That is what lets "build tvOS" and "develop web"
overlap while refusing two concurrent Xcode archives.

**Sequential vs parallel is an output of this, not a setting.**

### D6 — Reclamation is ownership-aware

`go clean -cache` turns a disk problem into N cold rebuilds for every sibling and
every human on the box (§2.5). Reclamation must:

1. Prefer garbage **owned by the reclaimer** — dead worktrees, stale `$TMPDIR`
   prompt files, old archives, orphaned artifacts.
2. Then unclaimed shared garbage, using `ops_diskguard.go`'s existing safety
   model (age gate, deny list, dry-run) rather than a second implementation.
3. Touch a shared cache **only** as a last resort, **only** when no live lease
   covers a build that depends on it, and **always** announcing itself.

### D7 — One chokepoint, not ten polite callers

Ten doors that each remember to check a flag will drift; one of them already
does not exist yet. Every deploy must funnel through a single function that
acquires the deploy lease and verifies quiescence. Doors become thin wrappers.
A door that cannot be routed through the chokepoint (e.g. `github_workflow_run`
firing off-machine) must **record its intent** so the barrier can see it.

### D8 — Fail toward safety, except where the code already chose otherwise

The existing freeze lease fails **open** on purpose (`autorun_gate.go:46-48`): a
dead coordinator must not park the fleet forever. Keep that. But for the new
mutating paths, an unavailable lease tier means **refuse**, not proceed.
Note that today's build-lease contention does the opposite — it logs
`"build target contended — gating anyway."` (`autorun_cmd.go:498`) and proceeds.
That must change.

### D9 — Non-goals

Explicitly out of scope; do not let these expand the work:

- Cross-**user** isolation. Everything here is same-user.
- Sub-second mutual exclusion. The git tier cannot provide it (D3).
- A scheduler daemon, master election, or central broker. Work-stealing and
  CAS, per the existing design.
- Replacing `agent_mesh.go` scoring. Add a floor; do not rewrite the model.
- Anything work-derived in Convex. Privacy contract (`convex_privacy_test.go`).

---

## 4. Phases

Ordered by (evidence of harm × independence). **Each phase is independently
landable and independently valuable.** Do not batch them into one branch.

Per `CLAUDE.md`: branch per phase, rebase onto latest `main`, merge, push. Commit
with explicit pathspecs only.

---

### P0 — Actor identity at the MCP boundary

**Why first:** nothing else can attribute, renew, or reap a claim without it
(D1). This is pure addition — it changes no existing behavior.

**Build:**

1. `desktop/agent/actor.go` — new.

   ```go
   type ActorID struct {
       UserID   string // from the authenticated session
       DeviceID string // localDeviceID()
       Kind     string // "claude-code" | "codex" | "opencode" | "autorun" | "human-web" | "unknown"
       Session  string // stable per MCP connection / per autorun run
   }
   ```

   `String()` must be **ref-safe verbatim** so it can be embedded in
   `refs/yaver/lease/...` without escaping — mirror the constraint
   `autorun_leases.go:148-156` already honors for lease keys.

2. `actorRegistry` — TTL map, `mu sync.Mutex`, entries
   `{ActorID, FirstSeen, LastSeen, WorkDir, Labels}`. Default TTL **90s**,
   renewed on **any** MCP call from that actor. Expired entries are reaped
   lazily on read *and* by a slow janitor, so an actor that dies without
   releasing does not hold claims forever (D2).

3. **Ingress.** Accept `X-Yaver-Actor-Kind` and `X-Yaver-Actor-Session` headers
   in the MCP HTTP handler. When absent, synthesize a stable session from the
   connection and set `Kind: "unknown"`. **Never reject a call for missing
   identity** — an unknown actor is still tracked; it just gets conservative
   treatment later. Backward compatibility is mandatory here.

4. **Verbs:** `actor_whoami`, `actor_list`, `actor_heartbeat`, `actor_release`.
   `actor_list` is the observability surface for every later phase.

**Acceptance:**
- Two concurrent MCP clients appear as two actors in `actor_list`.
- An actor that stops calling disappears within TTL + janitor interval.
- A client sending no headers still works, and appears as `unknown`.

**Do not:** gate any existing verb on identity in this phase.

---

### P1 — Stop the bleeding

**Why second:** these are the direct destruction mechanisms of §2.4. Each is
small, local, and independently landable. **None of them require P0** — land
them in parallel if convenient.

Ordered within the phase by harm:

1. **Per-actor workDir (mechanism A).** Make `TaskManager.workDir` a per-actor
   value keyed by `ActorID`, with the current global as the fallback default for
   `unknown` actors. Fix the unlocked reads at `httpserver.go:5676, 6108, 6250,
   7250, 11128, 13473` and `tasks.go:1018, 1221, 1922`. `tasks.go:1221`'s
   `cmd.Dir` is the sharp end — a wrong value there runs a build in the wrong
   repo.
   *This one needs P0.* If landing before P0, instead make every workDir-
   defaulting verb **require** an explicit `directory` and error clearly when
   absent — less friendly, but honest.

2. **Dev server: refuse, don't kill (mechanism B).** `devserver.go:452-473`.
   Key sessions by `(workDir, framework)`. Starting a *different* project must
   return a structured error naming the incumbent — never `Stop()` it. Add an
   explicit `takeover: true` for the deliberate case. Preserve SSE history per
   session rather than dropping it globally (`:470-473`).

3. **tmux session naming (mechanism C).** `runner_pty.go:105-108` — include the
   actor session in the default name, exactly as `autorun_tmux.go:98` already
   does with the runner ID. Gate the `?fresh=1` kill path
   (`runner_pty.go:119-120`) on the caller **owning** that session.

4. **Atomic task store (mechanism F).** `store.go:77` → tmp+rename+fsync, copying
   `saveConfigUnchecked` (`config.go:585-640`) verbatim in shape. And
   `store.go:99-102` must **not** return an empty map on parse failure — return
   the error, and keep a `.bak` of the last good file.

5. **`git add -A` sites (mechanism D).** Nine sites. Convert each to explicit
   pathspecs. Where "ship what I'm looking at" is genuinely the intent
   (`git_commit_push.go:97-99`), keep the behavior but require an explicit
   `sweep: true` **and** a dirty-baseline snapshot taken before the operation,
   refusing if unexpected paths appeared. `ops_git_verbs.go:526` is the model:
   reject an empty `paths` list rather than broadening.

6. **`--autostash` (mechanism E).** `vibing_actions_http.go:80` — drop
   `--autostash`, or scope it to a tree the caller owns.

7. **Lazy-init data race (mechanism G).** `mcp_workspace_handlers.go:513-594` —
   `sync.Once` or construct at server init. Eight sites, mechanical.

8. **Vault read-modify-write (mechanism H).** `vault.go:725-771` — re-read the
   file **under** the flock before merging, and make flock failure **fail
   closed** (`vault.go:727-732`), reversing the current fail-soft. Randomize the
   temp path (`vault.go:800`).

9. **Config CAS (mechanism I).** `config.go:542-640` — carry an mtime/version,
   re-read under a lock, and refuse a write whose base version is stale. The
   auth-token rotation path (`vault_rekey.go:47`) is the one that matters.

10. **Dev server ports (mechanism K).** Replace hardcoded 8081/5173/3000/9100
    with the `DevPortAllocator` path (`devport_allocator.go`), and close the
    TOCTOU by holding the listener until the child process inherits it, or by
    retrying on bind failure.

**Acceptance:** for each, a test with two concurrent actors that fails before
and passes after. Real processes on random ports, no mocks — the house pattern
(`desktop/agent/*_test.go`).

---

### P2 — Generalize leases to actors, and wire the fleet tier

**Depends on:** P0.

**Why:** the machinery exists (D3). This makes it usable by anything, and makes
it work across machines for the first time.

**Build:**

1. **Widen the holder.** `gitLeaseRecord.Holder` and the local `leaseManager`
   take `ActorID` instead of an autorun session string. Keep a compatibility
   shim so existing autorun call sites need no edit in this phase.

2. **Add `leaseDeploy`** to the class enum (`autorun_leases.go:46-60`) with a
   `deployLease(target)` constructor alongside the existing four
   (`:158-166`). Exclusivity: one holder per deploy target, fleet-wide.

3. **Wire `fleetLeaseCoordinator`** (`autorun_leases_fleet.go:53`) into the
   actual acquire path. This is the single highest-value line-count-to-benefit
   change in the plan — the code is written and tested and simply has no caller.
   Note the two traps already recorded in the design notes:
   - The CAS tier needs a **repo**, not a remote. Gating the tier on a
     configured remote means two machines sharing a repo get zero exclusion.
     Remote is only for fetch/publish.
   - **Losing the remote CAS must RELEASE the local claim**, or the loser holds
     a phantom lease blocking its own siblings.

4. **Acquire the `land` lease.** It is defined (`autorun_leases.go:59`) and never
   taken. Both landing paths must take it — `autorunLandMu` (`autorun.go:794`)
   and `opsGitLandMu` (`ops_git_land.go:43`). They currently do not exclude each
   other; the shared lease is what fixes that.

5. **MCP verbs — the point of the whole plan for third-party runners:**
   `lease_acquire`, `lease_renew`, `lease_release`, `lease_status`,
   `lease_break` (with a loud audit record).
   - Acquire is **all-or-nothing** across the requested set. That is the
     deadlock guard; no cycle detection is needed anywhere. Preserve this.
   - Leases auto-release when the holding actor's registration expires (D2).
   - `lease_status` must show holder, class, key, TTL remaining, and tier
     (`local` / `fleet` / `degraded`).

6. **Make build contention binding.** `autorun_cmd.go:498` currently logs
   `"build target contended — gating anyway."` and proceeds. Refuse instead, per
   D8.

**Acceptance:**
- Two actors on **one** machine cannot both hold `build/ios`.
- Two actors on **two** machines sharing a remote cannot both hold `build/ios`;
  the loser observes rejection and its local claim is released.
- An actor killed with `SIGKILL` has its leases reaped within TTL.
- `lease_acquire` with a partially-conflicting set grants **nothing**.

---

### P3 — Git awareness

**Depends on:** P0 (ownership needs an owner). Benefits from P2.

**The invariant**, stated plainly and enforced in code:

> **An automation must never mutate a working tree it does not own.** No
> `checkout`, no `reset`, no `clean`, no `stash` on any tree shared with a human
> or another actor. Its own worktree is the only tree it may write.

This is the highest-severity finding of the prior audit
(`docs/architecture/AUTORUN_COLLECTIVE_SYNC_AUDIT.md` §3.8): on 2026-07-18 a
shared checkout produced three distinct forms of data loss in one day, one of
which reverted work **off disk after it had been deployed to prod** — leaving
production running a schema the source tree no longer contained, one deploy away
from dropping live fields.

**Build:**

1. **Per-actor worktrees off one bare repo.** Autorun already proves the model
   (`autorun.go:552-585`) — its per-run worktrees are the one part of the system
   that has never lost anyone's data. Extend it to every actor. A `checkout` or
   `clean` can then only ever destroy that actor's own work.
2. **Ownership check before tree mutation.** A helper every git-mutating path
   calls. Deny by default when the tree is not the caller's. Audit
   `autorun.go:854-859` (`git checkout main` on the shared source checkout, with
   its dirty-guard checked **outside** `autorunLandMu`), `deploy_pipeline.go:105`,
   `transfer.go:337`, `platform.go:1241`, `preview.go:277-294`.
3. **`--force-with-lease` on every push.** It is literally a lease and it refuses
   to overwrite work that appeared after your last fetch — precisely the revert
   class above. Add `--atomic` for multi-ref lands.
4. **Detect concurrent git.** Check `.git/index.lock` before mutating; treat its
   presence as contention, not as a stale file to delete.
5. **Durable record.** `git notes` under `refs/notes/yaver` for actor, lease keys
   and outcome per commit — metadata only, never paths or contents. `reflog`
   already records every checkout/reset/rebase; expose it via an `autorun
   autopsy`-style verb so "who did this?" stops being unanswerable.

**Acceptance:** an automation attempting `checkout` on a tree it does not own is
refused with a clear error naming the owner. A push that would clobber an
unfetched commit fails rather than succeeding.

---

### P4 — Resource awareness: sequential vs parallel, and safe reclamation

**Depends on:** P2 (reservations are leases).

**Build:**

1. **Fix the RAM probe.** `autorun_resources.go:43-45` must read **available**
   memory, not total. macOS: `vm_stat` free+inactive+purgeable, or
   `vm.page_free_count`; Linux: `MemAvailable` from `/proc/meminfo`. Keep
   `TotalRAMGB` as a separate field for machine-class decisions, and rename so
   the two can never be confused again. **The current 2 GB floor cannot fire
   under real pressure** — this fix is what makes every downstream decision
   meaningful.

2. **Structured resource snapshot.** One `MachineResources` type with parsed
   numeric fields: `AvailRAMGB`, `FreeDiskGB` (per relevant mount), `LoadPerCPU`,
   `CPUs`, optional `ThermalPressure`. The existing MCP verbs return **raw
   unparsed strings** (`mcp_sysadmin.go:175-178`) which makes machine
   consumption impractical by construction — parse once, here, and let the
   human-facing verbs keep their strings.

3. **Build footprints.** A table mapping build target → expected footprint,
   sibling to `autorunBuildTargetRules` (`autorun_leases.go:99`), not a reuse of
   it — that table answers "what toolchain is busy?", this one answers "how much
   does it cost?". Start conservative and measured, e.g. Xcode archive ~8 GB RAM
   / ~25 GB disk; Gradle bundleRelease ~6 GB / ~15 GB; `go build` ~2 GB / ~2 GB.
   **Record actuals and refine** — do not treat the first numbers as truth.

4. **Reservation-based admission (D5).** Before a build acquires its build
   lease, reserve its footprint against **free** resources minus outstanding
   reservations. Fits → parallel. Does not fit → queue behind the holder, i.e.
   sequential. The answer is derived, never configured.

5. **Ownership-aware reclamation (D6).** Replace `reclaimAutorunDisk`
   (`autorun.go:137-148`):
   - Tier 1: the reclaimer's own garbage — dead worktrees (`git worktree list`
     is an on-disk registry that already exists and survives restarts), stale
     `$TMPDIR` prompt files (`autorun_tmux.go:181`), old archives.
   - Tier 2: unclaimed shared garbage via `ops_diskguard.go`'s existing classes,
     honoring its `minAge` (`:55`), deny list (`:77+`) and dry-run
     (`:645-650`).
   - Tier 3: shared caches (`go clean -cache`) **only** when no live lease covers
     a build depending on them, and always emitting a visible record.
   Delete the second reclaim implementation; do not maintain two safety models.

6. **Disk preflight for builds.** `build_preflight.go` has none (§2.5). Add a
   free-space field to `preflightResult` (`:53-60`) and gate every build entry
   point — `build_ios`, `build_android`, `xcode_build`, `gradle_build`,
   `mobile_project_build`, `docker_build`, `deploy_run`. Port the 20 GB mobile
   floor from `~/.local/bin/mobile-cache-cleanup.sh` **into Go** so it stops
   being a manual step the agent cannot enforce.

7. **Human presence (currently ABSENT).** A small probe: parsed idle time from
   `w`/`who -u` (`mcpWho` at `mcp_sysadmin.go:440-443` already shells `w` and
   throws the idle column away), `tmux list-clients`, and on macOS
   `ioreg`-derived `HIDIdleTime`. Feed it in as an input, not a veto: defer
   Tier-3 reclamation, defer exclusive build leases, and prefer another machine
   while a human is actively working on this one.

**Acceptance:**
- Two Xcode archives are serialized on an 16 GB box and may overlap on a 64 GB
  box — with no configuration change, purely from measured free RAM.
- Disk pressure reclaims dead worktrees **before** touching `GOCACHE`, and never
  touches it while a sibling holds a Go build lease.
- A mobile build on a box with 10 GB free is refused with a clear reason rather
  than failing 20 minutes in.

---

### P5 — Capability awareness, probed not declared

**Depends on:** nothing structurally; do after P4 so placement can read real
resources.

**Build:**

1. **Credential probes.** Replace the `LookPath` booleans
   (`console_machines.go:232-235`) with real checks that answer what their names
   claim:
   - TestFlight: an ASC key readable **now** (vault, or
     `~/.appstoreconnect/yaver.env`), plus `xcrun`, plus macOS.
   - Play: a service-account JSON readable now, plus the JDK/SDK.
   - Cloudflare / npm: is a usable token present **on this machine** right now?
   Each returns `(verdict, evidence, checkedAt)` per D4.

2. **Separate toolchain from credential.** `SupportsIOS` (can build) and
   `CanDeployTestFlight` (can ship) are different questions with different
   answers. Today the second is derived from the first
   (`console_machines.go:233`).

3. **Profile tags stop overriding probes.** `console_machines.go:249-261` sets
   capability true from a self-declared tag with no verification. Demote tags to
   a *hint* that biases scoring; never let them assert capability.

4. **Add a floor to placement.** `chooseNodePlacement` (`agent_mesh.go:182-192`)
   takes `placements[0]` unconditionally. Introduce a minimum viable score and
   return "no capable machine" instead of picking an offline one. Fix
   `:154-156`, where an over-restrictive allow-list is silently **discarded**.

5. **Wire `autorun_placement.go` (currently DEAD).** Give it a caller and an MCP
   verb (`placement_preview`) so the routing decision is inspectable. Reconcile
   the vocabulary mismatch with `ship_targets.go` (§2.6) — one target vocabulary,
   or an explicit documented translation table.

6. **Correct the CI-only hardcodes.** `autorun_placement.go:114-130` marks web
   and agent/npm `CIOnly: true`. Replace with the probe from (1): the answer is
   per-machine and time-varying. Keep TestFlight's Mac-only rule — its reason
   (registered UDIDs absent from CI keychains, `:93-94`) is real and not
   credential-shaped. No code anywhere currently reads or verifies a UDID list;
   if that becomes load-bearing, it needs its own probe.

**Acceptance:** a Mac with Xcode but no ASC credentials reports `no` for
TestFlight deploy with evidence naming the missing key — not `yes`. A
vault-locked machine reports "cannot verify", not a false negative.

---

### P6 — One deploy, at the end

**Depends on:** P0 (quiescence), P2 (deploy lease).

**Build:**

1. **The chokepoint (D7).** One function — `AcquireDeployBarrier(ctx, targets)`
   → holds `leaseDeploy` per target fleet-wide, verifies quiescence, and returns
   a token. Every door in §2.7's table becomes a thin wrapper that calls it.
   `github_workflow_run` cannot be routed through it (it fires off-machine), so
   it must **record its intent** in the barrier's view instead.

2. **Quiescence check, replacing termination detection (D2).** Deploy proceeds
   when, for every actor whose work overlaps the deploy targets' source areas:
   no live `path` or `build` lease overlaps, and the actor has been idle beyond
   a quiet period. Autoruns additionally must be `parked()`, **not merely
   `paused`** (`autorun_gate.go:23-28`).

3. **Fix fleet drain.** `shipFreezeAll` must pass `waitFor` and **read the
   response** — the remote handler already computes and returns a drain payload
   (`autorun_ops.go:498-513`) and `shipCallVerb` (`ship_fanout.go:34, 57-64`)
   throws it away. Aggregate remote drain state into the deploy decision.

4. **Discover freeze targets; stop trusting a hand-typed list.** Replace
   `opts.FreezeMachines` (`ship.go:46`, `ship_fanout.go:93`) with discovery from
   the actor registry and lease holders. Keep the explicit list as an override.
   Call `discoverAutorunTmuxSessions` (`autorun_wrapup.go:86`) so raw-tmux loops
   stop being invisible.

5. **Make drain timeout an error by default.** `ship.go:167-171` currently warns
   and deploys anyway. Refuse, with an explicit `--force` for the human who
   knowingly accepts it.

6. **Actually enforce the pin.** Implement `verifyDeployPin` — the function the
   docs claim exists. Deploy must build from the pinned SHA, or verify the tree
   still matches it and is clean, and refuse otherwise. Pass the SHA into
   `RunDeployAll` (`ship.go:240` passes only `Only: plan.Targets`). Without
   this, the whole barrier is decorative: a commit landing during a 45-minute
   archive ships regardless.

**Acceptance:**
- Two actors calling `deploy_all` concurrently → one deploys, one is refused
  with the holder named.
- A deploy is refused while a human's Claude Code session holds a `path` lease
  overlapping the deploy target.
- A commit landing mid-archive does **not** appear in the shipped artifact.

---

### P7 — Quota counters

**Depends on:** P6.

The PARK behavior is written (`autorun_placement.go:218-220`) and never called;
`quotaExhausted` is a parameter every test passes as a literal. No counter
exists anywhere.

**Build:** a durable per-target counter — TestFlight uploads/day first, since it
is the only hard external limit with **no rollback** (a bad build can only be
superseded, and a retry burns tomorrow's slot). Then CI minutes, Cloudflare
deploys, npm publishes. Wire it into `placeAutorunTarget`. **Exhaustion PARKS**;
it must never retry. Surface remaining budget in `ship --dry-run`.

---

### P8 — Convex correctness

**Depends on:** nothing. Small and independent.

1. **Fix the mis-keyed upsert.** `backend/convex/agentSync.ts:29-40` — add
   `deviceId` to the index so each machine owns its own row. Today every machine
   with a `yaver.io` checkout clobbers one shared row and P2P routing dials the
   wrong machine.
2. **`taskPlacements` is not a claim table.** No unique index, no conditional
   write (`backend/convex/taskPlacement.ts`). Either add a uniqueness constraint
   and conditional write, or document it clearly as advisory routing so nobody
   builds exclusion on it. The genuine claim pattern already exists in this
   codebase — `agentRescueCommands` uses `queued → claimed` status transitions
   with TTL (`backend/convex/schema.ts:614-707`).
3. **Deploy Convex changes explicitly** — `cd backend && npx convex deploy --yes`.
   Not wired to CI. And per `CLAUDE.md`, after any deploy from a shared checkout,
   grep the source for a symbol you added to confirm it survived.

---

### P9 — Observability and tests

**Depends on:** all of the above; land incrementally alongside them.

1. `fleet_status` — one verb showing every actor, every lease, every reservation,
   every quota, across machines. This is what makes the system debuggable and is
   worth more than any individual fix.
2. **Honest terminal states.** The prior audit's finding stands: over one night,
   19 runs produced 4 runs' worth of code and the logs said almost every one
   converged. `no_edits ≠ converged` — a run that committed nothing never
   started. Until this is fixed, every layer above optimizes against a lying
   oracle.
3. **Concurrency test harness.** N real agent processes against one repo,
   asserting no lost writes. Real HTTP servers on random ports, no mocks — the
   house pattern.
4. Publish sanitized lease state to the bus for fleet visibility: holder alias,
   slot, repo hash, key, TTL. **Never** prompts, stdout, contents, or absolute
   paths — Convex privacy contract, enforced by
   `desktop/agent/convex_privacy_test.go`.

---

## 5. Suggested order

P0 and P1 are independent and can proceed in parallel. Everything else chains.

```
P0 (identity) ──┬── P2 (leases) ──┬── P3 (git)
                │                 ├── P4 (resources)
P1 (bleeding) ──┘                 └── P6 (deploy) ── P7 (quota)
                    P5 (capability) ──┘
P8 (convex)  — independent, land anytime
P9 (observability) — incremental throughout
```

If only one phase is ever done: **P1**. It is pure harm reduction and needs
nothing else.

If only two: **P1 + P2**, because P2 is mostly wiring code that already exists
and already passes its own tests.

---

## 6. Risks

| Risk | Mitigation |
|---|---|
| Leases deadlock the fleet | Acquire is all-or-nothing (already the design); TTL reaping; `lease_break` with audit. Never add cycle detection — all-or-nothing is the guard. |
| TTL reaping kills a live holder | Reap generously; renew on **every** MCP call, not just explicit heartbeats. Clock skew makes TTL approximate (D3). |
| Backward incompatibility for existing MCP clients | P0 never rejects a call for missing identity. Unknown actors work, with conservative treatment. |
| Footprint numbers are wrong | Start conservative, measure actuals, refine. Wrong-but-conservative costs latency; wrong-but-permissive costs a wedged machine. |
| Refusing where we used to proceed frustrates the user | Every refusal names the holder and the contended resource, and offers `force`. A refusal that cannot be acted on is a bug. |
| This plan is stale by the time it is implemented | §0. Re-grep. The code wins. |

---

## 7. What this does not fix

- Runner TUI dialect differences — autorun reads one runner's screen shape for
  all of them (prior audit §3.2). Unrelated and separately valuable.
- The false-`converged` terminal state, beyond P9.2 flagging it.
- Cross-user or multi-tenant isolation.
- Anything requiring sub-second mutual exclusion (D3).
