# Wake / sleep / connect — what is left, and what is already taken

Written 2026-07-18. Read the top section before starting anything: a large part
of this area is **actively being worked by an autorun on the Mac mini**, and the
fastest way to waste a thread is to re-do it or collide with it.

---

## 1. IN FLIGHT — do not touch these

An autorun is running on the Mac mini (`pokayoke@100.89.155.25`):

| | |
|---|---|
| clone | `~/Workspace/yaver-wake-autorun` (dedicated; not the shared checkout) |
| branch | `wake-ux-autorun`, pushed with upstream tracking |
| runner | `codex` (`claude` is not reliably authed on that box) |
| brief | `tasks/wake-sleep-all-surfaces.md` |
| gate | `~/wake-gate.sh` — web+mobile+backend `tsc` and both unit suites |
| tmux | session `wake-autorun`; log at `~/wake-autorun.log` |

It owns these four areas. **Leave them alone until the branch merges:**

1. Unifying mobile's three parallel wake ladders (`wakeMachineCore` vs
   `parkedMachines.ts::deriveWakeView` vs the inline bar in `ManagedCloudCard`).
2. Mobile parity: provider line, `ParkedSummary` in the picker and Infra tab.
3. tvOS / watchOS / Wear enrichment (elapsed clock, stall hints, creep).
4. Closed-loop specs: `yaver-tests/web-wake-states.test.yaml`,
   `e2e/tests/managed-cloud-wake.spec.ts`, a redroid wake/park spec.

If the run dies, the brief is complete enough to hand to a fresh runner as-is.
Check liveness with `pgrep -f "codex --model"` on the mini — the autorun log only
writes at iteration boundaries, so an empty log is **not** evidence of a hang.

**Merging it is an explicit follow-up:** rebase `wake-ux-autorun` onto latest
`main`, merge, push. Nobody has done this yet.

---

## 2. FREE TO TAKE — not in the autorun's scope

### 2.1 The wake→bill→re-park loop is still visible-only

We made the failure *legible*; we did not change the policy. A box whose Yaver
session has expired still: wakes → runs ~10 min → `abandonWake` deletes it →
parks. Repeat on every wake attempt, each one billing real Hetzner minutes.

`backend/convex/cloudMachines.ts::resumeHealthCheck` retries a signed-out box to
`attempt < 40` before giving up. Worth deciding: should a box with
`lastWakeOutcome === "needs-auth"` refuse to wake at all until it is signed in,
rather than burning another ten minutes to reach the same wall? The data to do
this now exists — that is what `lastWakeOutcome` is for.

**Related and immediately actionable:** the owner's own box
(`mn777j15vc4wnt1gv4ceyad5858afzs4`) is in exactly this state. Until someone runs
`yaver auth` on it, every wake will fail the same way.

### 2.2 `ManagedCloudPanel` hardcodes `deviceReachable={false}`

`web/components/dashboard/ManagedCloudPanel.tsx:1010` and `:1048` pass
`deviceReachable={false}` because that component has no device list to consult.
Invariant 5 says reachability outranks every phase claim, so this panel can show
a wake still climbing after the box is genuinely up — `DevicesView` gets it
right. Give the panel a real reachability signal.

### 2.3 The optimistic-rung rule differs between web and mobile

Mobile (`wakeMachine.ts:262`, applied `:287-290`) yields the optimistic rung as
soon as the server's phase overtakes it **by percent**. Web
(`web/lib/wakeProgress.ts::computeWakeView`) uses a **45s time grace**.

Mobile's rule is better. Web's is only needed for the one case percent
comparison cannot catch: a request the server *never* accepts. Port mobile's
rule to web and keep the timer purely as that expiry. (An earlier draft of the
autorun brief had this backwards and would have had a runner downgrade mobile;
that is corrected in the brief, but the underlying divergence is still real.)

### 2.4 `useManagedMachines` stops polling once nothing is pending

`web/components/dashboard/DevicesView.tsx` — the poll chain ends when no machine
is in a transitional state. If a wake is started from **another surface** (phone,
watch, TV), the open dashboard never notices until a manual refresh. The
`refresh()` added for the local button does not cover the remote-initiated case.

### 2.5 The Convex lifecycle code has no tests at all

There are no `backend/convex/*.test.ts`. Everything landed in `setLifecycleTiming`,
`resumeMachine`'s phase ladder, `resumeHealthCheck`'s classification and
`abandonWake`'s outcome recording is verified only by typecheck and by manual
observation against one live box. The subtle bits deserve real tests — in
particular: `machine` is read at the top of `pauseMachine`, so
`machine.parkStartedAt` there is the **previous** park's stamp (this was a real
bug, fixed by holding the value in a local; nothing stops it regressing).

### 2.6 `provisionProgress` is written but ignored

The control plane writes a 0-100 `provisionProgress` on every `setPhase`. No
client uses it — web and mobile both drive the bar from `PHASE_META` percent plus
creep. Either delete it or make it authoritative; right now it is a number that
looks meaningful and is not.

### 2.7 Native percent scales diverge, deliberately but unresolved

`PHASE_META` is 40/65/86 (booting/registering/online); tvOS, watchOS and Wear use
52/80/94. The headers previously *claimed* they matched exactly; they never did.
The comments now tell the truth, but the divergence itself is unresolved. Fixing
it means changing all three native surfaces together — don't do it piecemeal.

---

## 3. Infrastructure gaps that will bite

- **Wear OS does not build here.** No Gradle wrapper, no CI; its own
  `build.gradle.kts` calls itself a source-only scaffold. That is how a real bug
  (`probeHealth` treating a signed-out box as READY, then re-sending the pending
  turn) survived. Any Wear change is *reviewed, not compiled* — say so plainly.
  Adding a wrapper + a compile job is the fix.
- **Mac mini disk is tight** — ~11 GiB free after three `node_modules` installs.
  The redroid test lane may not fit. Better to report that lane unavailable than
  to ship a spec that cannot run.
- **`gh` is not installed on the mini.** Plain `git push` over SSH works; anything
  needing the GitHub API does not.
- **Concurrency is unmanaged.** Three distinct incidents in one day on the shared
  checkout: work swept into a sibling's commit; work reverted off disk *after it
  had been deployed to Convex prod* (prod briefly held a schema the tree no longer
  contained); and a sibling switching the checked-out branch mid-session, which
  silently turned a `git push github main` into a no-op. CLAUDE.md now mandates
  branch-per-work and `git commit -- <paths>`, but nothing enforces it. Per-session
  git worktrees would.

---

## 4. How to verify anything here

```bash
# the gate the autorun uses
cd web     && npx tsc --noEmit && npx tsx lib/wakeProgress.test.ts
cd mobile  && npx tsc --noEmit && npx tsx --test src/lib/wakeMachine.test.mts
cd backend && npx tsc --noEmit -p convex
```

Never `go test ./...` in `desktop/agent` — `TestAuthLogout` hits the real
`~/.yaver` and signs the machine out.

Inspect a live box instead of guessing:

```bash
yaver ops cloud_status                    # status, phase, telemetry, errorMessage
curl -s http://<ip>:18080/health          # authExpired / lifecycle.usable / state
hcloud server list                        # is a server actually running + billing?
```

`{"ok":true,"authExpired":true,"lifecycle":{"usable":false}}` means the box is UP
and only needs signing in. **`ok:true` never means usable** — the agent answers
while signed out because it still serves the pairing routes. Every probe in the
codebase must demand `lifecycle.usable && !authExpired && !needsAuth`.
