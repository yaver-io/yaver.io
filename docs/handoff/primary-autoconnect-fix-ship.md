# Handoff — ship the primary auto-connect fix

**Written** 2026-07-18 · **Branch** `primary-autoconnect-fix` · **Commit** `270eb4ebb`

You are picking up a landed-but-unpushed bug fix and shipping it. Read the
"Correct the premise" section first — it removes about half the work the task
was originally scoped for.

---

## Correct the premise before you start

The request that produced this doc was *"commit, push, deploy the Go agent, then
TestFlight, and make sure the remote Mac mini has the latest Yaver Go agent."*
Two of those are already done or unnecessary. Verified 2026-07-18:

| Assumption | Reality |
|---|---|
| "deploy the Go agent" | **The fix contains ZERO Go changes.** `git show --stat 270eb4ebb` is 11 files: 10 under `mobile/src`, 1 under `tvos/`. No agent release is needed for this fix. |
| "make sure the mini has latest greatest" | **Already true.** `yaver primary status` reports the mini on agent **1.99.311**, and `npm view yaver-cli version` is **1.99.311**. It is already on the newest published agent. |

So the actual remaining work is: resolve two collided files, unbreak the tree,
push, and build **mobile only**.

Do **not** cut a `cli/v*` release for this. If you decide one is needed for an
unrelated reason, note that tag creation has been broken since the org transfer
(the ruleset's `bypass_actors` was emptied) — use
`gh workflow run release-cli.yml`, not `git tag`.

---

## What the fix is

The phone flapped between "Connecting · Primary" and "No machine selected" for
23 seconds, then connected in **1.0 s** the moment the user picked the same box
by hand. The connection log settles it — there was never a connectivity problem:

```
14:28:04  settings ready — primaryDeviceId=229aeb03, forceRelay=false
14:28:05  Found 8 devices
          ... 23 seconds, ZERO connect attempts ...
14:28:28  [connect-start]   Mobiles-Mac-mini.local     <- the MANUAL pick
14:28:29  [connect-success] via relay
```

Root cause: the auto-connect effect in `DeviceContext` burned its retry nonce on
**entry**, and listed `devices` / `connectedDeviceIds` / `activeDevice?.id` as
effect deps. Those re-identify constantly (fresh array every 30 s poll; the
pool-warming effect mutates the connected set). React runs the previous cleanup
before re-running an effect, so each churn cancelled the in-flight probe — and
the re-entry saw `attemptedNonce === nonce` and returned. A cancelled sweep was
indistinguishable from a completed one, so nothing ever tried again.

Full reasoning is in the commit message (`git show 270eb4ebb`). Summary of the
change set:

- nonce burns on **completion**; an interrupted sweep re-arms *and* bumps the
  trigger. A **user** cancel still burns it. Decision extracted to
  `autoConnectStatus.resolveSweepOutcome`, unit-tested.
- churny values read via refs; deps hold only semantic triggers.
- one shared probe→repair ladder (`lib/probeWithRepair.ts`) for the automatic
  and manual paths — auto-connect previously had **no** relay-credential repair
  rung, so a drifted per-user relay password failed every automatic connect
  while a manual tap silently repaired it. Budget unified at 4000 ms.
- last-resort connect when the probe finds no route but Convex says online (the
  probe knows only relay+host+lanIps; `quic.ts` also tries beacon/tunnel/cache).
- `.local` dropped from probe legs — the connector never dials it, and on iOS
  mDNS *hangs* on Local Network permission rather than failing fast.
- beacon bind failure now names an iOS Local Network denial.
- tvOS had the identical wedge (`YaverStore.autoConnectStarted` was never
  cleared by `cancelAutoConnect`) — fixed in the same commit.

---

## Blockers to clear, in order

### 1. The tree does not typecheck right now — and it is NOT this fix

```
src/lib/deviceStatus.ts(233,16): error TS2304: Cannot find name 'isCredentialSafeBase'.
```

Another session is mid-edit on `deviceStatus.ts`. They added `isPrivateHost` and
`isCredentialSafeBase` to `mobile/src/lib/probeTargets.ts` and started calling
the latter from `deviceStatus.ts` **without adding the import**. Their work is a
genuine security fix (the probe was attaching `Authorization: Bearer <session
token>` over plaintext `http://` to public Hetzner IPs on every 8 s probe).

Verify it is still theirs and still missing before touching it:

```bash
git show HEAD:mobile/src/lib/deviceStatus.ts | grep -c isCredentialSafeBase   # expect 0 (my commit is clean)
grep -n isCredentialSafeBase mobile/src/lib/deviceStatus.ts                    # worktree usage
grep -n "^import" mobile/src/lib/deviceStatus.ts                               # is the import there yet?
```

**Preferred:** ask whoever owns that change to finish it, or wait for it. It is
a one-line import (`isCredentialSafeBase` from `./probeTargets`). If you add it
yourself, say so — do not silently rewrite their in-flight change, and do not
revert it to make the build green.

`mobile/` has **no** `npm run build`; `npx tsc --noEmit` plus the tsx suites are
the gate here.

### 2. Two files hold my changes mixed with other sessions'

Neither was committed, deliberately — per CLAUDE.md's concurrency rule I would
have swept a sibling's work into a commit titled "fix auto-connect".

| File | Mine | Theirs |
|---|---|---|
| `mobile/src/components/RemoteBoxPickerModal.tsx` | `handleContinue` now calls the shared `probeDeviceWithRepair` instead of its own inline probe+repair | `SleepingMachineRow` wake-clock anchoring (`wakeStartedAt` / `lastWokeAt`) |
| `mobile/src/lib/quic.ts` | reconnect ladder logs via `appLog` instead of `console.log` (~lines 6586-6604) | a **staged** `runner: "glm"` → `"opencode"` change at ~line 9380 |

`quic.ts` shows `MM` — someone has staged work there. A pathspec commit on it
commits the working-tree version, which includes their staged hunk.

**The picker change is required** — without it the "one shared ladder" claim is
false and the two paths silently diverge again. Get it in.

The `quic.ts` logging change is independently valuable: the banner counts
"reconnect 1/5" while the Connection Logs panel the UI points users at shows
nothing at all. But it can ship separately if the collision is awkward.

```bash
git diff -- mobile/src/components/RemoteBoxPickerModal.tsx    # confirm both changes still present
git diff --cached -- mobile/src/lib/quic.ts                   # see their staged hunk
```

### 3. Other sessions' unrelated WIP is in this tree

`backend/convex/{cloudMachines,http}.ts`, `desktop/agent/autorun_cmd.go`,
`desktop/agent/autorun_wrapup.go` (untracked), `mobile/src/lib/{llmRemote,
subscription}.ts`, and a mobile version bump to **1.18.146** across all five
files + `versions.json`.

**Use `git commit -- <paths>` with explicit paths. Never `-a`, never `add -A`.**
The version bump to 1.18.146 is already done by someone else — do not re-bump.

---

## Ship sequence

### A. Land it

```bash
cd ~/Workspace/yaver.io
git rev-parse --abbrev-ref HEAD          # expect primary-autoconnect-fix
git log --oneline -1                     # expect 270eb4ebb

# after resolving blockers 1 and 2:
cd mobile && npx tsc --noEmit && cd ..
for t in autoConnectStatus probeTargets probeWithRepair agentStatus agentSlots; do
  (cd mobile && npx tsx src/lib/$t.test.ts >/dev/null 2>&1) \
    && echo "PASS $t" || echo "FAIL $t"
done

git commit -- mobile/src/components/RemoteBoxPickerModal.tsx   # + quic.ts if you took it
git fetch github && git rebase github/main
git checkout main && git merge primary-autoconnect-fix
git push
```

Resolve conflicts, never bulldoze them. Committed work is immutable.

### B. Mobile build — **this is the only deploy this fix needs**

Preflight (the threshold is 20 GB; this Mac had **27 GB** free on 2026-07-18 —
tight, check again):

```bash
mobile-cache-cleanup.sh preflight
```

Fastest loop, no daily cap — **prefer this** to validate before spending a
TestFlight slot:

```bash
cd ~/Workspace/yaver.io        # repo ROOT — wire/wireless walks into ./mobile itself
yaver wireless push            # WiFi-paired iPhone
# or: yaver wire push          # USB
```

Confirm the output ends with `bundleID: io.yaver.mobile`. If it names another
bundle you were in the wrong directory and just installed someone else's app.

TestFlight (local only — CI is `if: false` on purpose, GH runner keychains lack
your device UDIDs). **~15-20 uploads/app/day, no rollback.** Do not spend one
until `wireless push` shows the fix working:

```bash
$(yaver vault env --project mobile) || source ~/.appstoreconnect/yaver.env
./scripts/deploy-testflight.sh
mobile-cache-cleanup.sh mark-deployed yaver
```

Version is already at **1.18.146** everywhere. `CFBundleVersion` auto-increments.

If this Mac is low on disk or the archive is slow, the mini is the designated
box for heavy builds.

### C. tvOS

The tvOS wedge fix is in the commit but **not shipped by any of the above**.
`tvos/*.xcodeproj` is gitignored and generated, so it goes stale — regenerate
before building, and do not pass `-sdk` with an embedded watch target. If you
are not shipping tvOS this round, say so explicitly rather than implying it went
out with the phone.

### D. The Mac mini

Nothing to do for this fix — it is already on 1.99.311, the newest published
agent, and the fix has no Go component. Confirm rather than assume:

```bash
yaver primary status | head -6      # expect agent version 1.99.311
npm view yaver-cli version          # expect the same
```

If they ever diverge, the mini self-updates (tri-state default-on, jittered
6-12 h), or push desired state from any surface via Convex `desiredAgentVersion`
— do not SSH in and hand-install.

---

## Verify the fix actually works

**Nobody has run this on a device.** It is verified by typecheck, 23 unit
assertions, and code reading only. The behavioural claim is unconfirmed. This is
the most valuable thing you can add.

On the phone after installing, sign out and back in (or force-quit and relaunch)
so the launch sweep runs, and watch the Tasks banner:

- **Pass:** it goes `Connecting · Primary` → connected, without ever passing
  through "No machine selected", and the sub-line narrates per-rung stages
  ("Pinging …", and on a stale credential "Repaired — re-checking …").
- **Fail, but now diagnosable:** More → Connection Logs will contain
  `[auto-connect] sweep interrupted mid-flight — re-running`. That line is new
  and means cleanup is still cancelling the sweep — some dep is still churning,
  or a fourth trigger exists that was not in the original dep array.
- Also new and worth checking: `[reconnect] …` lines now appear in that panel
  (only if you took the `quic.ts` change). Previously the banner counted
  "reconnect 1/5" while the log showed nothing.

Unrelated but visible in the same screenshots: the banner reads **"Claude Code
needs sign-in"**, and `yaver primary status` confirms `claude: ✗ not configured`
on the mini. Fix with `yaver primary auth claude`. Not part of this change.

---

## Known-open, deliberately not fixed here

- **web/ has no relay-credential repair rung on either path.** `connectToDevice`
  (`web/app/dashboard/page.tsx:1510-1649`) ladders
  `ownerClaimDevice` → `reauthAgent` → error, and never calls `repairRelay` —
  even though the helper exists at `page.tsx:809-835` and is wired into
  ProjectsView / PreviewPane / WebReloadView. A stale per-user relay password
  breaks web connect on both automatic and manual paths equally. Not an
  auto-vs-manual asymmetry like mobile's was, so it was out of scope — but it is
  a real gap and the same class of bug.
- **web/ burns its auto-connect token before running** (`page.tsx:1684`), the
  same shape as the mobile bug. It cannot wedge today because web has no
  cancellation path, but it is one `return () => {}` away from reproducing it.
- **iOS Local Network permission is never requested deliberately.** It is
  triggered implicitly by the beacon's UDP bind (`lib/beacon.ts:91-105`), which
  is why the permission dialog appeared *during* a connect attempt in the
  original report. Denial is now named in the log rather than swallowed, but a
  proper pre-flight request at onboarding needs a small native module.
- `watch/` and `wear/` have no auto-connect routine at all — nothing to port.
