# Yaver AI App-Test Agent — deep design

**Status:** design / deep-analysis, 2026-06-08. Builds directly on the Studio
capture layer (`desktop/agent/studio/`) and the on-device-sandbox closed loop
both proven on magara this session. No new code yet; this is the build-ready
plan. grep the code before trusting any line.

> Read the code, not the doc. The capture layer, surfaces, account flow, job/
> status, metering, and streaming this design composes all exist and were
> exercised end-to-end on magara (x86 redroid) + an iOS simulator on macOS.

---

## 0. One paragraph

An **AI-driven, source- and use-case-aware automated test agent**: a developer
(using Yaver, building an iOS or Android app) asks, in plain language, "test my
app" — and an LLM, having read the app's **source**, generates and **executes**
full real-device scenarios on a capture surface (Android redroid / iOS
Simulator): **create a fresh account → exercise tons of flows → assert the UI is
correct → remove the account**. The whole session is **recorded** (video +
screenshots + step log), the AI **judges each screen** against intent ("is this
UI ok / what was expected?"), results are **uploaded to storage**, and the
developer **watches it live or after, from their phone**, streaming from a
**self-hosted** box or **Yaver-managed cloud**. It is **credit-metered**, with
two model modes: **BYOK** (the dev's own model key → no inference charge, only
compute/farm) and **Yaver inference** (routed through the gateway → arbitrage
markup). The test-authoring intelligence + saved scenario library live in
**Talos** (closed knowledge plane); **execution** is **Yaver** (open execution
plane) — the open-core split.

---

## 1. Why this is mostly assembled, not new

| Capability the test agent needs | Already built (this session / repo) |
|---|---|
| Drive a real app: tap / type / wait-for-text / key / back / home | `studio/redroid.go` + `ios.go` Driver verbs (TapText via uiautomator, coords fallback) |
| Capture surface, Android + iOS, managed-cloud + on-prem | `RedroidSurface` (x86 **with proot** + arm64) · `IOSSimSurface` (simctl) · `LocalRunner`/`SSHRunner` |
| Account lifecycle (signup → … → remove) | `AccountSpec` + `AccountSignInSteps`; **validated**: created an account on magara redroid (email+password, full onboarding) |
| Record a full session + timed captions | `RunFlowRecording` → mp4 + `Cue[]`; `compositor.go` |
| Async job + live status (phase/log/artifacts) | `studio_jobs.go` + `studio_job_start`/`studio_job_status` ops verbs |
| Live streaming to the phone | `vibe_preview` SSE + WebRTC H.264 (`remote_runtime_video_track.go`) |
| Credit metering, BYOK vs inference | `managedMeter.ts` (`inference` 1.5×, `studio` 1.6×, opt-in gate, dryRun) |
| Model routing / OpenRouter / gateway | runner-provider lane + gateway (`project_sdk_policy_acl_spine`, `inference` meter) |
| Storage upload | `storage_*` verbs / R2 |
| On-device Linux dev env (run tools on the surface) | proot x86_64 + Alpine x86_64 rootfs **proven on redroid** this session |

The genuinely **new** parts: (a) **source-aware scenario generation**, (b) the
**AI drive+assert loop** (goal-seek + VLM screen judgement), (c) **account
teardown/removal**, (d) the **test-result object** (recording + assertions +
verdict) and its **mobile viewer**, (e) the **BYOK/inference model-mode split**
wired into one flow, (f) Talos scenario library.

---

## 2. Architecture

```
 developer ── "test my app: signup, browse, checkout, delete account" ──┐
   (CLI / MCP / mobile / web; iOS or Android dev)                        │
                                                                          ▼
 Talos (closed): scenario authoring + library ──────────────────────────┐
   reads SOURCE → understands routes/features/auth → emits Scenario[]    │
   (saved, versioned, reusable — the moat)                               │
                                                                          ▼
 Yaver agent (open execution) — studio/test package ─────────────────────┼─────
   TestJob: provision surface → install app → for each Scenario:         │
     ┌───────────────────────────────────────────────────────────────┐  │
     │ AI DRIVE+ASSERT LOOP                                            │  │
     │  observe (screenshot + view tree)                              │  │
     │  → LLM decides next action toward the scenario goal            │  │
     │  → act (Driver verb) → observe                                 │  │
     │  → VLM ASSERT: "does this screen match expectation?" (verdict) │  │
     │  records video + per-step screenshot + reasoning               │  │
     └───────────────────────────────────────────────────────────────┘  │
   account: fresh signup at start → … → REMOVE account at teardown       │
   upload: session.mp4 + shots + report.json → storage                   │
   meter: BYOK (compute only) | inference (compute + token arbitrage)    │
                                                                          ▼
 viewer (mobile / web): live stream (SSE/WebRTC) + after: recording + report
   from self-hosted box OR Yaver-managed cloud
```

The drive+assert loop is the existing **Appium bug-hunter** pattern
(`vibe_preview_appium.go`) generalized: instead of "walk randomly and catch
red-boxes", it's "walk *toward a goal*, asserting expected UI at each step."

---

## 3. Source- and use-case-aware scenario generation (the intelligence)

1. **Read the source.** The runner has the repo (owner's machine, or a clone on
   the farm — same runner model). Static analysis (extends `shots_analyzer.go`):
   route map (expo-router / RN nav / Flutter routes / native), auth provider,
   forms + fields, feature surfaces, API calls. → a *capability map*.
2. **Generate scenarios.** LLM turns the capability map + the dev's intent into
   a `Scenario[]`: ordered steps with **goals** ("reach checkout") and
   **assertions** ("cart total = sum of items", "confirmation screen shows order
   id"). Each scenario is data — committable to `.yaver/tests/*.yaml`, and (the
   moat) savable to the **Talos scenario library** for reuse across runs/apps.
3. **Three authoring tiers** (degrade gracefully, mirrors flow authoring):
   committed scenarios → source-heuristic generation → live AI goal-seek when
   the map is thin.

Open-core split: the **generator + library** (domain knowledge, reusable test
suites, tuning) are **Talos**; the **executor** is **Yaver** (open). Same split
as [[project_yaver_talos_open_core_robotics]].

---

## 4. The drive + assert loop (per step)

```
observe  := { screenshot (PNG), viewTree (uiautomator/idb), currentRoute }
action   := LLM(scenario.goal, history, observe) → one Driver verb
            (Tap/TapText/Type/Key/Swipe/Wait)  — cheap/fast model
assert   := VLM(screenshot, scenario.expectation[step]) → {pass, reason, severity}
            — "is this UI ok / what was expected?"  — stronger model
record   := append (action, screenshot, assert.reason) to the session
```

- **Two-model split for cost** (the OpenRouter angle): a *cheap, fast* model
  drives navigation (most steps); a *stronger* multimodal model runs the **UI
  assertions** (the judgement that matters). Both routed through the same model
  lane (§6). This is the deterministic-servo / VQA-verify split from the Fairino
  cell ([[project_fairino_cobot_cell]]) applied to UI testing.
- **Self-healing selectors:** when `TapText` misses (RN/uiautomator gaps, as on
  the magara redroid), fall back to coordinate-from-screenshot via the VLM
  ("tap the blue Sign Up button") — exactly the manual recovery used in this
  session's closed loop, automated.
- **UI-optimization findings:** the assert step also flags *expected-vs-actual*
  drift, slow screens, overflow/clipping, contrast — a "UI review" byproduct the
  dev gets for free.

---

## 5. Account lifecycle — fresh → exercise → REMOVE (validated foundation)

- **Create:** the signup flow is already validated on magara (email+password, no
  OTP; `AccountSignInSteps`). For third-party apps the owner supplies a provider
  + test credential, or a disposable-mailbox `CodeSource` (the `AccountSpec`
  seam) when the app needs an email code, or a seed/deeplink.
- **Exercise:** the scenarios run.
- **Remove:** a `RemoveAccount` scenario (navigate to Settings → Delete account
  → confirm) tears the account down at the end, so each run is clean and leaves
  no residue. **Never use the owner's real account on a shared runner** — always
  a disposable one; teardown both the account and the surface.

This "fresh account → tons of actions → remove account, all recorded" is the
canonical TestJob template.

---

## 6. Model modes — BYOK vs inference (the arbitrage)

Two modes, one flow, selected per run:

| Mode | Inference routing | Inference charge | What's metered |
|---|---|---|---|
| **BYOK** | dev's own key (Anthropic/OpenAI/OpenRouter), passed to the runner, **never stored** | **none** (their key, their bill) | compute/farm minutes only (`studio`/compute meter) |
| **Yaver inference** | routed through the Yaver **gateway** (GLM/OpenRouter upstream) | per-token **arbitrage** markup (`inference` meter, 1.5×) | compute + inference tokens |

- BYOK is the honest free-exit (no inference credit down) and what pros use; the
  key flows to the runner as a scoped env, never to Convex (privacy contract).
- Inference mode is the arbitrage: GLM/OpenRouter tokens are far cheaper than the
  dev's mental anchor (Claude/GPT), so 1.5× still reads cheap while earning.
- **OpenRouter** is a first-class upstream for the gateway (model breadth + the
  two-model cost split: route nav→cheap, assert→strong by model id).
- Model selection is policy (`project_sdk_policy_acl_spine`): per-run model ids
  for {navigator, asserter}, overridable; defaults tuned for cost.

---

## 7. Recording, upload, and the test-result object

- **Record** the whole session (existing `RecordStart/Stop` → mp4 + cues) +
  per-step screenshots + a `report.json` (scenario, steps, actions, assertions,
  verdicts, model usage, timings).
- **Upload** to storage (R2 / managed bucket via `storage_*`), returning shareable
  artifacts. External upload = explicit/consented (privacy).
- **Verdict** = pass/fail per scenario + a summary; failing assertions carry the
  screenshot + the AI's reason ("expected order confirmation, saw an error
  toast").

---

## 8. Mobile + web viewing — live and after

- **Live:** stream the running session to the dev's phone via the existing
  `vibe_preview` SSE (frames) / WebRTC H.264 track, from a **self-hosted** box or
  **managed cloud** — the same transport the Studio record path uses. The user
  watches the AI drive the app in real time + a live step/assert log
  (the `studio_job_status` polling UI, extended with the assert stream).
- **After:** the recording + `report.json` render in `app/test.tsx` (mobile) and
  a web `TestRunView` — scrub the video, jump to failed assertions (deep-linked
  to the timestamp via the cues), see model usage + cost.
- Reuses the job-status UI already built (`studioClient` + `StudioPanel`),
  extended with the assertion timeline.

---

## 9. Surfaces & runner model (proven)

- **Drive from:** CLI (`yaver test`), MCP (`test_run`/`test_status` — agentic
  LLM, host Claude Code), mobile, web. iOS or Android target.
- **Run on:** owner's machine (free, BYO), Yaver-managed-cloud farm
  (`LocalRunner`, metered), on-prem box (`SSHRunner`) — all three proven this
  session. Android = redroid (x86 **with proot+rootfs** validated, or arm64); iOS
  = Simulator on a Mac runner (screenshot validated on macOS).

---

## 10. Privacy & security

- Disposable test accounts; **remove** at teardown; never the owner's real
  account on shared runners.
- BYOK keys → scoped runner env, **never Convex/logs**.
- Shared-runner isolation: ephemeral workdir + network jail + teardown wipe
  (operator-fleet gaps C/D — gate the metered shared path behind that).
- Recordings/repos are work-derived → P2P/storage with consent, never Convex
  (only the meter ledger: counters/labels/timestamps).

---

## 11. Phased plan

- **T0 — TestJob skeleton:** reuse `studio_jobs.go` shape → `test` job kind;
  Scenario = committed `.yaml` (goals + assertions); run the drive loop with a
  single model; record + verdict. Android first (redroid).
- **T1 — VLM assertions + two-model split** (navigator/asserter), self-healing
  selectors.
- **T2 — Account fresh→remove lifecycle** as a built-in scenario template.
- **T3 — Source-aware scenario generation** (capability map → Scenario[]);
  Talos library save/reuse.
- **T4 — BYOK vs inference modes + metering** (`test`/inference meter, cost in
  report).
- **T5 — Live mobile/web viewer** (stream + assert timeline) + storage upload.
- **T6 — iOS parity** (Mac runner; idb for taps) + multi-locale.
- **T7 — Shared-runner hardening** (isolation), then GA the managed path.

T0 reuses ~80% of the Studio capture layer; the new code is the loop + asserts +
scenario gen.

---

## 12. Risks

- **Flakiness** of AI UI navigation (redroid uiautomator gaps; soft-keyboard
  layout shifts — both hit this session). Mitigate: coordinate+VLM fallback,
  retries, committed scenarios for critical paths, seed-and-replay.
- **Assertion reliability** — VLM false pass/fail; mitigate with explicit
  expectations + multi-sample on disagreement (the adversarial-verify pattern).
- **Cost** — cap steps/run, cheap navigator model, budget guard; BYOK avoids it.
- **Account-removal availability** — not all apps expose delete-account; fall
  back to disposable accounts + flag.
- **Arch** — x86 emulator now runs the full sandbox (proot+rootfs proven); arm64
  for real-device fidelity.

---

## 13. One line

Everything to *drive, record, assert, meter, and stream* an app test already
exists in the Studio layer and was proven on magara; the AI test agent is the
loop + source-aware scenario generation + account-removal + the BYOK/inference
model split + the mobile result viewer — Yaver executes, Talos remembers.

---
---

# Part II — redroid-first, base image, local corpus, fix mode, UX

**Status:** design / deep-analysis addendum, 2026-06-08. Part I above is the
drive+assert loop. Part II makes redroid **first-class** (not just a Studio
capture surface), adds the **Yaver Base Image** keystone, reconciles the agentic
loop with the *existing* `testkit` runner so test cases live **synced in the
repo**, splits **catch-only vs fix** modes, and specs the **watch-live / read-
summary** UX (bugs caught / fixed). grep the code before trusting any line —
file refs are accurate as of 2026-06-08.

## 14. First-class redroid + the Yaver Base Image (`yaver-base`)

Today redroid boots cold per job: `redroid_capture.sh` loads `binder_linux`,
pulls the image, boots (~12s on x86 magara), *then* the Studio job installs the
APK. For **app testing** that cold path is wrong twice over:

1. **Latency/cost.** Every run re-pays boot + (for the Hermes path) a Yaver-
   container install + a test-account sign-in. On a metered farm that's wasted
   minutes on COGS that never change between runs.
2. **Two divergent Android stacks.** `studio/redroid.go` drives via `docker
   exec` + `am`/`input`/`uiautomator` (no adb daemon). `testkit/
   driver_androidemu.go` drives a *real AVD* via **adb**. Two code paths, two
   capability sets, one Android — a maintenance tax and a behavior fork.

**`yaver-base`** fixes both. It is a **golden, snapshot-restorable redroid
`/data` volume** that already contains everything invariant across runs:

- binder loaded + `sys.boot_completed` reached (no cold boot),
- the **Yaver mobile container** (`io.yaver.mobile`) installed with the
  `YaverBundleLoader` native module ready,
- signed into a **disposable test account** (vault creds — **never the owner's**;
  Part I §10),
- uiautomator2 server + the tap/dump helpers staged, fonts + locale seeded.

```
yaver base build           # boot (redroid_capture.sh recipe) → install Yaver APK
                           #   from the mobile build → sign in test user (vault) →
                           #   stage helpers → snapshot /data → tar + sha256
                           # → ~/.yaver/base/<arch>/<ver>.tar  (+ manifest)
yaver base up              # boot redroid with the snapshot volume → WARM in ~2-3s
yaver base ls / gc         # versions; prune old snapshots
```

This is the **same builder pattern already in the repo** for the on-device proot
rootfs (`scripts/build-android-rootfs-alpine-x86_64.sh`, the 38 MB Alpine
rootfs `2e6214ab` with a sha256 manifest) — just one layer up, at the Android-
image level instead of the chroot level. Versioned next to
`mobile/sdk-manifest.json` + `versions.json`; arch = **arm64** (the farm runs
redroid arm64, no KVM) + **x86_64** (proot rootfs proven this session).

**Two app-load paths sit on top of one warm base** — this is the unlock:

| Path | How the app-under-test gets in | When |
|---|---|---|
| **(b) Hermes-push** | RN/Expo bundle loaded **into** the warm Yaver container via `/dev/build-native` → `loadApp()` — **no APK rebuild** | RN/Expo dev loop; the fast synced-local path; **dogfoods Yaver's own loader** |
| **(a) APK install** | any `.apk` (Flutter / native / non-RN) `pm install`-ed on top of base, driven standalone | first-time / non-RN / release-artifact tests |

Path (b) is the headline: because the Yaver container is *already in the base*,
an agentic test of an RN app is **bundle-push → drive**, in seconds, with zero
native build. **Allocation + metering unit = one restored base instance**; farm-
minutes meter from `base up`; the owner's box is COGS 0 (Part I §6, reuse
`CIRunWhere` self-hosted/operator-fleet/yaver-cloud + `managedMeter`).

## 15. One redroid surface, one local test corpus (consolidate with `testkit`)

**Decision A — kill the two-stack fork.** Make redroid a **first-class testkit
Target** (`target: android-redroid`) whose driver **is** the Studio
`RedroidSurface` — it already implements `Tap/Type/Key/TapText/WaitText/
Screenshot/RecordStart/RecordStop`, exactly the `Driver` surface testkit's
Android target needs. Keep `driver_androidemu.go`'s adb path only for a *real
local AVD*; redroid no longer goes through adb. One surface, two consumers
(deterministic testkit specs **and** the agentic `studio/test.go` loop).

**Decision B — one corpus, git-versioned, $0, no telemetry** (testkit's privacy
contract: tests live in the user's repo, run on the user's box, nothing crosses
Convex):

```
yaver-tests/
  specs/*.test.yaml     # deterministic testkit Specs  (regression gate)   ← exists
  flows/*.flow.yaml     # agentic Scenarios: goal + expectations + guardrails ← studio/test.go
  fixtures/             # seed data, golden screenshots, test-account profile
  yaver-qa.yaml         # suite: base image ref, targets, default mode, model lane
```

**Spec ⊂ Flow.** A deterministic Spec is a Flow driven by a `ScriptedBrain`;
an agentic Flow is the same loop driven by the LLM `TestBrain` — `studio/test.go`
*already* models exactly this (`NewScriptedBrain` vs the T1 LLM brain, same
`RunTest` loop). So "all test cases **and** agentic test cases synced local" is
one corpus with two brains, not two systems:

- **specs/** — stable regression: known steps + assertions, fast, deterministic.
  The gate that must stay green.
- **flows/** — exploration: a goal + guardrails + oracles; the agent visits
  states scripts never reach and surfaces bugs the regression suite is blind to.

"Synced local" = the corpus is in the repo and runs on the user's redroid (owner
box free, farm metered) — the testkit posture, extended to the phone UI.

## 16. Two modes — **catch-only** vs **fix** (the closed loop)

| | **catch-only** (default) | **fix** (closed loop) |
|---|---|---|
| Source | **read-only** — never touched | patched on the dev box, **draft only** |
| Loop | drive toward goals + undirected monkey/fuzz; oracle bank fires; emit `Bug[]` | catch → patch → reload → **re-verify**; repeat (bounded) |
| Output | a **Bug Report** (streamed + summarized) | the report **plus** a working-tree diff / draft PR per fix |
| Use | CI gate, "what's broken?", nightly hunt | "fix what you find," dev-loop autopilot |

**Oracle bank** (what counts as a bug, fired every observation): RN red-box / JS
exception (generalize `vibe_preview_appium.go`'s `RCTRedBox` hunter), native
crash / ANR (logcat tail), `console.error`, network 5xx, a11y violations
(axe, already a testkit step), **visual regression** vs golden screenshot, the
**VLM verdict** ("is this screen broken / does it match the goal?", Part I §4),
dead-end navigation, layout overflow / clipping / low contrast, untranslated
string, slow-frame / jank. Each fired oracle → a `Bug{title, severity, repro
trace, clip, oracle, rationale}`.

**Fix mode = generalized self-heal.** `testkit/selfheal.go` already repairs a
*selector* by handing the DOM + failure to the user's model and retrying. Fix
mode lifts that from "selector repair" to "app-bug repair":

```
bug caught
  → dispatch a runner "agent" job (mobile codingAgent / runner.go Kind:"agent")
      on the DEV box, payload = { repro action-trace, screenshots, oracle output,
      source pointer (the app repo lives there) }
  → agent proposes + applies a patch
  → /dev/build-native rebuild  →  loadApp() reload into the SAME warm base instance
  → re-run ONLY the failing flow  →  oracle/VLM confirms fixed?
        fixed     → record Bug as `fixed` + attach diff
        unfixed   → revert, record `attempted-unresolved`   (bounded retries)
```

**Guardrails (CLAUDE.md hard rules apply).** Fix mode **never** auto-commits or
pushes — it leaves a working-tree diff or a **draft PR** for human review.
Disposable test account only (never the owner's). Network jail + teardown-wipe on
shared/operator-fleet runners (gaps C/D). Destructive flows require `confirm`.

## 17. UX — watch it live, or read the summary (bugs caught / fixed)

A `Bug` is `{id, title, severity, outcome: caught|fixed|attempted-unresolved,
repro trace, clip (deep-linked to its recording cue), oracle, VLM rationale,
diff?(fix)}`. Two ways to consume a run — the user picks:

- **Watch live** ("see the AI test it"): the redroid screen via MJPEG / snapshot-
  poll (reuse Remote-Desktop `/rd/stream` + `vibe_preview` SSE/WebRTC), beside a
  **streaming action+oracle rail**: `tapped Login → typed email → ⚠ red-box on
  Profile → 🔧 dispatched fix → ✓ fixed`. iOS-safe = snapshot-poll (the Remote-
  Desktop mobile rule).
- **Read the summary** ("report card"): `N flows · M passed · K caught · J auto-
  fixed · $cost`; each bug expands to repro + clip + (fix) diff; severity badges;
  **trend vs last run** (reuse testkit `history.go` / `FlakeReport`).
- **Toggle**: watch vs **run-in-background-and-notify** (`notify` /
  `PushNotification` on done: "3 caught, 2 fixed").

**Surfaces** (all reuse the Studio job-status UI — `studioClient` + `StudioPanel`
— extended with the bug/assert timeline):

- **mobile**: `app/(tabs)/qa.tsx`, hidden under **More** (mirrors `studio.tsx` /
  `runs.tsx`), polls `qa_status` every ~3s, streams the rail via BlackBox events.
- **web**: `TestRunsView` / `QAPanel` next to `StudioPanel` on the dashboard;
  add to `CONNECTION_REQUIRED_TABS`.
- **CLI**: `yaver test [--fix] [--watch] [--base <ver>] [--target owner|farm]`.
- **MCP**: `qa_run` / `qa_status` so host Claude Code drives it agentically.

## 18. Wiring (ops verbs, reuse, meter)

`ops_qa.go` (the `registerOpsVerb` idiom, `Streaming:true`, `AllowGuest:false`):
`qa_run` (payload: `{app, base, target, mode:catch|fix, modelLane, flows[]}`),
`qa_status`, `qa_cancel`, `qa_results`, `qa_base_build`, `qa_base_up`. Reuse the
`studioJobManager` async-job + live-log shape verbatim. Meter via
`CIRunWhere`+`managedMeter` — QA is a meter dimension (or folds into the existing
`studio` meter); owner runner free, farm metered, BYOK = compute-only (Part I §6).

## 19. What exists vs the delta (honest)

| Capability | State |
|---|---|
| redroid surface (drive/record on Android-in-Docker) | ✅ `studio/redroid.go`, verified magara 2026-06-08 |
| deterministic spec runner (web/ios-sim/android-emu, a11y, visual, self-heal, flake) | ✅ `testkit/` |
| agentic loop scaffold (`TestBrain`, `RunTest`, ScriptedBrain) | ✅ `studio/test.go` (T0) |
| red-box / crash hunter | ✅ `vibe_preview_appium.go` (one oracle) |
| coding-agent dispatch + Hermes rebuild/reload | ✅ `runner.go` (Kind:"agent") + `/dev/build-native` + `loadApp` |
| credit metering, BYOK vs inference | ✅ `managedMeter.ts`, `CIRunWhere` |
| live stream + job-status UI | ✅ `vibe_preview`/`remotedesktop` + `studioClient`/`StudioPanel` |
| **`yaver-base` build/restore (warm golden snapshot)** | ❌ **new** (§14) — biggest leverage |
| **redroid ⇄ testkit consolidation** (one surface) | ❌ **new** (§15, Decision A) — two stacks today |
| **LLM `TestBrain` T1** (goal-seek + VLM assert) | ❌ **new** — only `ScriptedBrain` exists |
| **oracle bank** (beyond red-box) | ❌ **new** (§16) |
| **fix-mode closed loop** (catch→patch→reload→re-verify, draft-PR) | ❌ **new** (§16) |
| **`yaver-tests/` corpus + `yaver-qa.yaml`** | ❌ **new** (§15, Decision B) |
| **`qa_*` ops verbs + `qa.tsx` tab + `TestRunsView`** | ❌ **new** (§17–18) |

**Phased (extends Part I §11):** **P0** `yaver-base` build/restore + redroid⇄
testkit consolidation → **P1** deterministic specs on redroid end-to-end → **P2**
LLM `TestBrain` T1 + oracle bank + **catch-only** report → **P3** watch-live /
summary UX (mobile `qa.tsx` + web `TestRunsView`) → **P4** **fix mode** closed
loop (draft-PR only) → **P5** meter + farm allocation + operator-fleet teardown.

**Dogfood:** Yaver's own mobile app is an RN app — `yaver-base` + agentic flows
hunt bugs in Yaver's UI (ties to the dogfood-screenshot-loop). Yaver tests Yaver.

## 20. One line (Part II)

Bake the warm Android once (`yaver-base`), drive **one** redroid surface from
**one** in-repo corpus (deterministic specs + agentic flows), run it **catch-
only** or **fix** (catch→patch→reload→re-verify, draft-PR only), and let the dev
**watch it live or read "K caught / J fixed."**
