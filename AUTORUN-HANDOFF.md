# Handoff — mobile follow-up chat bug + remote autorun visibility

Written 2026-07-19. Target: run this on the Mac mini (`pokayoke@100.89.155.25`).
Everything below was verified against the code/box on 2026-07-19 — the "Verified
facts" section is what makes the rest of the plan the shape it is. Read it first.

---

## 0. Verified facts (do not re-litigate these)

1. **The follow-up chat bug is ALREADY FIXED.** Branch `followup-ux`, commit
   `98e984b15` *"fix(mobile): your follow-up message was invisible, and the chat
   became a new task"*. It is **now pushed** to `github` (was laptop-only until
   today). 10 commits ahead of `main`, including `mobile 1.18.148` and a
   TestFlight build-443 commit, plus `mobile/src/lib/followUpPlan.ts` and
   `mobile/maestro/followup-visible.yaml`.
   **⇒ The job is to VERIFY and LAND it, not to re-implement it.**

2. **`--goal` is claude/glm ONLY** — `desktop/agent/autorun_cmd.go:53`.
   Codex and opencode run as autorun runners but get **no `/goal` loop**.

3. **`--machine` SILENTLY DROPS `--goal` and `--tmux`.**
   `autorun_cmd.go:70` calls
   `dispatchRemoteAutorun(m, *task, *runner, *master, *gate, *interval, *maxIters, *push, scopes)`
   — `goal`, `tmux` and `workDir` are not in that argument list.
   **⇒ Never use `yaver autorun --machine …` for a goal-driven run. SSH into the
   mini and run `yaver autorun` there.**

4. **RN-web / Chrome / the Ubuntu-4GB box CANNOT test this.**
   `mobile/node_modules/expo-secure-store/build/ExpoSecureStore.web.js` is
   literally `export default {};` — signed-in screens are unreachable on web, so
   the chat composer cannot be driven from a browser. **Simulator only.**

5. **Mac mini state:** Xcode 26.4, iPhone + iPad simulators ✓, tmux ✓,
   claude / codex / opencode all installed ✓.

6. **Two hard blockers on the mini — see §1.**

7. **Do not touch tmux session `1`.** Codex (gpt-5.5) is doing live work there on
   a *different* bug: `yaver primary projects --json` times out across all
   candidates while `mobiles --json` returns `scanning:true` with an empty list.
   It works in the shared `~/Workspace/yaver.io`. Sessions `yaver` and `clauth`
   are dummies/stale.

8. **Auth-check traps on this box** (each produced a false reading today):
   - `ls ~/.claude/.credentials.json` → "No such file" is **normal** on macOS.
   - `~/.claude.json` `oauthAccount.emailAddress` is a **leftover profile field**,
     not proof of a live session.
   - `security … -w | head -c 40 && echo FOUND` prints FOUND on an **empty**
     result — `&&` binds to `head`, not `security`. Never pipe the probe.
   - Over SSH the login keychain is locked ("User interaction is not allowed");
     `launchctl asuser 501` fails ("Could not switch to audit session") and
     `sudo -n` needs a password.
   - **The only reliable check:** start claude in tmux and
     `tmux capture-pane`. A fresh session works fine.

---

## 1. Blockers to clear FIRST (a human must do these)

### 1a. Claude is on API billing, not the subscription  ← MUST FIX
The mini's claude header reads `Opus 4.8 (1M context) · API Usage Billing`.
No `ANTHROPIC_API_KEY` is set anywhere and `~/.claude.json` has
`hasApiKey: false` — so the OAuth login went through the **Anthropic Console
(API-billed)** path. Standing project rule is **subscription-only**; an autorun
loop here bills per token.

```bash
ssh -t pokayoke@100.89.155.25 'tmux attach -t authtest'
# then inside claude:  /login  → choose "Claude account with subscription"
# confirm the header no longer says "API Usage Billing"
```

### 1b. Disk: 4.6 GB free, 98 % full  ← MUST FIX
Simulator work needs `node_modules` (~2–3 GB) plus build output. Roughly 7 GB
sits in ten idle autorun clones under `~/Workspace/` (no tmux session is live
for any of them).

**Do not just delete them** — every one carries
`yaver-autorun-failed-iteration-*` stashes (`yaver-webrtc-autorun` has 14), and
their `origin` still points at the pre-transfer `kivanccakmak/yaver.io` URL
(which is also why `yaver` prints `release lookup failed (403)`).

Safe order:
```bash
for d in ~/Workspace/yaver-*autorun* ~/Workspace/yaver-routine-agents; do
  git -C "$d" remote set-url origin git@github.com:yaver-io/yaver.io.git
  git -C "$d" push --all origin 2>&1 | tail -2      # preserve, then delete
done
# only after the pushes succeed, remove the clean ones (dirty=0):
#   yaver-autorun-digest glm stack toolchain, yaver-ci-bus-autorun,
#   yaver-ci-review-autorun, yaver-forge-autorun, yaver-mail-autorun (2.6G),
#   yaver-webrtc-autorun, yaver-routine-agents
# KEEP (dirty): yaver-channel-autorun, yaver-feedback-runner-autorun, yaver-wake-autorun
```
Also kill the hung opencode from the dead GLM probe — stuck **46 h** in
`~/Workspace/yaver-autorun-toolchain` (PIDs ~67450/67455/67521).

---

## 2. Task A — verify + land the follow-up fix

**Dedicated clone. Never the shared `~/Workspace/yaver.io`** (codex is live in it,
and autorun does `git stash push --include-untracked`, so two loops in one
checkout stash each other).

```bash
ssh pokayoke@100.89.155.25
git clone git@github.com:yaver-io/yaver.io.git ~/Workspace/yaver-followup-autorun
cd ~/Workspace/yaver-followup-autorun
git checkout main && git merge --no-ff origin/followup-ux
```

Then run the loop **on the box** (not `--machine`):

```bash
cd ~/Workspace/yaver-followup-autorun
yaver autorun \
  --task ~/Workspace/yaver-followup-autorun/AUTORUN-HANDOFF.md \
  --runner claude \
  --goal "maestro follow-up flow passes on the iPhone simulator and the merge is pushed" \
  --gate "cd mobile && npx tsc --noEmit" \
  --scope 'mobile/**' \
  --push
```
Autorun forces tmux for claude — note the session name it prints.

### What "verified" means here
Boot the iPhone simulator, install the app, and run
`mobile/maestro/followup-visible.yaml`. The flow drives `followup-input` /
`followup-send` (testIDs added by the fix — `tasks.tsx` had **zero** testIDs
before, which is why no UI automation could target the composer).

⚠️ **The assertion window is load-bearing.** The flow asserts the message appears
within ~2 s. A generous `waitFor` **passes against the broken build too**, because
`fetchTasks()` eventually fills the turn in — *that delay is the bug*. Do not
relax the timeout to make it green.

### Gate honesty
`npx tsc --noEmit` in `mobile/` is a **weak** gate — it sees untracked files and
has produced false greens before. Treat a green tsc as necessary, not sufficient;
the maestro run on the simulator is the real oracle.

---

## 3. Task B — remote autorun visibility in mobile (NEW WORK)

Currently `mobile/app/autoruns.tsx` (285 lines) is scoped to the **connected
device only** — it renders *"Connect to a device to read its autorun seats"* and
shows no tmux session label anywhere.

Wanted:
1. An autorun running on **any** remote device (mini, etc.) shows up in the
   mobile app as an autorun, once connected to that device.
2. Show its **tmux session name** as a label, in the autoruns list **and** on the
   related task in the Tasks screen.
3. Show which runner(s) it is using (claude / codex / opencode) and master/doer
   seats.

Backend seam already exists: `desktop/agent/autorun_channel.go` publishes
`autorunStateEvent` on topic `autorun/<deviceID>/<slot>` and there is an
`autorun_runs` verb — the data is there, the mobile surface is not consuming it
for remote devices. `autorun_tmux.go` owns session naming.

**Cross-surface parity applies**: mobile / tablet / car / glass share
`DeviceContext`, so an RN fix reaches them together — but **web, tvOS, watchOS and
Wear OS have their own code and must be ported explicitly**. Note the parity
table in the commit.

---

## 4. Deploy — with care

- **npm (`yaver-cli`) and the Go agent** can ship from the mini.
  ⚠️ `cli/v*` tags **cannot be created** since the org transfer emptied
  `bypass_actors` → use `gh workflow run release-cli.yml` instead of tagging.
- **TestFlight from the mini is BLOCKED twice**, do not attempt until both are
  fixed:
  1. `security find-identity -v -p codesigning` returns **0** identities — no
     Apple Distribution cert on that box. (The ASC API key *is* there:
     `~/.appstoreconnect/yaver.env` + `AuthKey_77Z6B543D5.p8`.)
  2. Disk is far under the 20 GB mobile-deploy preflight floor.
  ⇒ TestFlight still ships from the MacBook (`./scripts/deploy-testflight.sh`).
- **One deploy per converged change**, never one per iteration. TestFlight is
  capped at ~15–20 uploads/app/day with no rollback.

---

## 5. Rules the loop must not break

- `git commit -- <paths>` **always**. Never `-a`, never `add -A` — the index is
  shared between concurrent sessions.
- Never revert / force-push / stash someone else's work. If a committed change
  looks wrong, propose a **new** commit.
- Work on a branch, rebase onto latest `main`, merge, push.
- Never two autoruns in one checkout.
- Subscription runners only — no API keys.
