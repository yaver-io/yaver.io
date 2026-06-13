# Remote-machine redroid + AI testing — deep analysis

Scope: how Yaver can run a **remote Linux box** as an Android test/QA appliance using
**redroid** (Android-in-Docker), covering deterministic unit/UI specs, AI-driven
exploratory flows, autonomous bug-finding + fixing, third-party-app testing, and the
MCP surface that lets an agent orchestrate all of it remotely. Grounded in the current
code (file paths inline); ends with the concrete gaps to close.

Date: 2026-06-13.

---

## 0. TL;DR

- The pieces exist and are wired: **redroid surface** (`studio/redroid.go`), an **AI test
  brain** (`qa_brain.go`, two-model BYOK), a deterministic **oracle bank** (`studio/oracle.go`),
  a **catch→patch→reload→re-verify** fix loop (`qa_flow.go` + `qa_fix.go`), and a
  **testkit** with app-agnostic UIAutomator selectors (`testkit/`).
- redroid needs **no GPU and no KVM** — only Docker + the `binder_linux` kernel module
  (`CONFIG_ANDROID_BINDERFS=m`). Verified today on the Hetzner aarch64 agent box
  (`6.8.0-101-generic`, module loads, arm64 image runs arm64 APKs natively).
- **Third-party apps work for catch-only** today: the selector engine + oracle bank are
  app-agnostic (UIAutomator XML + logcat). `yaver-tests/flows/02-settings-navigation.flow.yaml`
  already drives `com.android.settings`. **Auto-fix needs source**, so third-party = report-only.
- The **biggest gaps** are MCP-exposure and an **autonomous explorer**: redroid boot, APK
  install, the catch/fix loop, and "map the app + generate a test corpus" are reachable only
  via `create_task`/shell, not as first-class MCP tools an agent can call directly.

---

## 1. Why a remote machine + redroid

A remote Linux box running redroid is a **headless Android device farm in a container**:

- **No Mac, no physical device, no emulator/KVM.** redroid is real AOSP on the host kernel;
  it boots in ~12 s cold (or ~2–3 s from a warm "base image" snapshot) and is driven via
  `docker exec` (no host adb, no published 5555 port → many instances per host).
- **Cheap to fan out.** A 4-core / 8 GB box runs at least one redroid comfortably (verified
  spec in `docs/yaver-store-asset-studio.md` §recipe: Ubuntu 20.04, 3.7 GB RAM). Multiple
  boxes register as **MCP peers** (`acl_add_peer`) → parallel suites.
- **arch matters.** arm64 host runs Yaver's arm64-only `.so` natively. x86_64 hosts need an
  x86 split/universal APK (Yaver ships arm64-only) — so prefer **arm64 cloud boxes** (Hetzner
  CAX, AWS Graviton, etc.).

### Boot recipe (no host sudo) — `studio/redroid.go` `Provision()`, `studio/redroid_capture.sh`
```bash
# 1. load binder via a throwaway privileged helper (no host sudo needed; docker daemon is root)
docker run --rm --privileged -v /lib/modules:/lib/modules debian:bullseye-slim \
  bash -c 'apt-get install -y -qq kmod && modprobe binder_linux devices=binder,hwbinder,vndbinder'
# 2. boot redroid (privileged for binderfs)
docker run -itd --privileged --name yaver-studio-redroid -p 5555:5555 \
  redroid/redroid:13.0.0-latest \
  androidboot.redroid_width=1080 androidboot.redroid_height=2340 androidboot.redroid_dpi=440
# 3. wait sys.boot_completed=1 ; adb connect 127.0.0.1:5555 ; adb install -r -g app.apk
```
Kernel requirement: `CONFIG_ANDROID_BINDERFS=m` + `linux-modules-extra-$(uname -r)`.

---

## 2. The two test tiers

Yaver unifies deterministic and AI testing on one loop. The design identity (per
`docs/yaver-ai-app-test-agent.md`) is **Spec ⊂ Flow**: a Spec is a Flow driven by a
`ScriptedBrain`; a Flow is the same loop driven by the LLM `TestBrain` (`studio/test.go`).

| | **Spec** (`yaver-tests/specs/*.test.yaml`) | **Flow** (`yaver-tests/flows/*.flow.yaml`) |
|---|---|---|
| Driver | `ScriptedBrain` — no model | `llmBrain` (`qa_brain.go`) |
| Input | known `steps:` + `assert.*` | natural-language `goal:` + `expectations:` |
| Determinism | exact, CI gate | exploratory, finds edge cases |
| Cost | $0 inference | text nav + vision asserts (BYOK) |
| Use | regression / smoke | discovery / "does this feature work" |

Spec example (`specs/01-launch.test.yaml`): `goto: /` → `assert.visible: testID=home-tab`.
Flow example (`flows/01-launch-and-home.flow.yaml`): goal "reach home with bottom tabs",
expectation "no red box / crash", `max_steps: 10`.

### The AI brain — `qa_brain.go` + `qa_model.go`
- **Two-model split** (cost): `Decide()` = cheap **text** navigation (Haiku / gpt-4o-mini /
  Mistral / Ollama) reading the **UIAutomator XML** tree; `Judge()` = stronger **vision**
  assertion on the final screenshot. **BYOK** — keys from env/`~/.yaver`, never in Convex.
- **Vision fallback:** when the UIAutomator dump is < 40 chars (redroid sometimes returns an
  empty tree), the brain switches to screenshot + coordinate taps.
- Output is strict JSON: `{"verb":"taptext|tap|type|key|back|wait","args":{…},"done":bool}`.
- Selectors are **app-agnostic** (`testkit/android_uiautomator.go`): match by text / testID
  (RN testID → content-desc) / resource-id / class, partial-match tolerant.

### The oracle bank — `studio/oracle.go` (deterministic, no model, every frame)
- **RedBox** (RN `Unhandled JS Exception`, `ReactNativeJS: Error`), **Crash/ANR**
  (`FATAL EXCEPTION`, `ANR in`), **BlankScreen** (no hierarchy post-launch), plus the
  **VLM expectation** check on the final screen. Deduped per (oracle+title). These fire even
  while the LLM navigates, so a crash mid-flow is caught regardless of the goal.

---

## 3. Find **and fix** — the closed loop

`qa_flow.go` (`runQAFlows`) drives each flow once (`driveFlowOnce`): capture
screenshot+tree+logcat → oracle bank → brain action → repeat → VLM-judge expectations.
In **`mode: fix`**, `qa_fix.go` `fixFlow` runs the autonomous repair loop:

1. **Dispatch** each caught bug (title, severity, detail, repro flow) to a **coding-agent job**
   on the **developer's machine** (not the shared farm). Prompt: "fix the ROOT CAUSE in source,
   minimal change, **do NOT commit or push**, reply one-line summary."
2. **Reload** the patched app — `mobileReloader.Reload()` → `/dev/build-native` rebuilds the
   Hermes bundle, then broadcasts `reload_bundle` to the running app (seconds on a warm base;
   full APK rebuild for native changes). See `mobile_hermes_reload` + `REMOTE_MCP_HERMES_RELOAD_PLAN.md`.
3. **Re-verify** — re-drive the same flow with **fresh oracle state**; reconcile each bug to
   `fixed` / `attempted-unresolved`, and flag any **new regressions** the patch introduced.
4. Bounded by `MaxFixes` (default 3). The patch stays in the working tree for human review.

This is the "AI finds bugs and fixes them" capability — real, but it **requires the app's
source** (the coding agent edits files + Hermes-reloads).

### Tests of the tester (unit/integration) — `desktop/agent/qa_*_test.go`
`qa_brain_test.go` (JSON parse, vision fallback, verdicts), `qa_flow_test.go` (orchestrator
catches an injected `FATAL EXCEPTION` with a fake driver+brain), `qa_fix_test.go` (catch→fix→
verify with mocks), `qa_testaccount_test.go`. CI (`.github/workflows/test-suite.yml`):
`go test ./...` + jest/pytest/flutter SDK tests + iOS/Android smoke via `yaver test run`.

---

## 4. Third-party app testing — what works, what doesn't

**Works today (catch-only):** because the selector engine and oracle bank read only the
UIAutomator tree + logcat + screenshots, they are app-agnostic. `flows/02-settings-navigation`
drives stock `com.android.settings` with zero Yaver instrumentation. So the pipeline
"install arbitrary APK → launch → AI explores toward a goal → oracles catch crashes/ANRs/
red-boxes → VLM judges screens → bug report + video" runs on any app.

**Blockers for arbitrary apps:**
- **No auto-fix** — fix mode edits *source*; a third-party APK is a closed binary. Unless the
  owner supplies the repo, third-party = **report-only**.
- **No account teardown** when the app lacks a delete-account UI (the disposable-account
  contract assumes one) → falls back to "flag and move on."
- **No source-aware scenario generation** — for Yaver's own app, source can seed a capability
  map; for third-party, it's pure goal-seeking from whatever the explorer discovers.
- **Red-box oracle is RN-specific** (still catches native crashes/ANRs on any app, but the
  "JS error overlay" signal only applies to RN apps).

**Missing for "AI discovers the app and makes ALL the tests":** there is **no autonomous
crawler** yet. The brain is *goal-driven per flow* — give it a goal, it pursues it. To
"discover an app and generate a full test corpus" you need an **exploration mode** that maps
the screen graph (BFS over tappable elements, dedupe by view-signature), records reachable
states, and **emits flow/spec YAML** from what it found. That generator is the main net-new
piece (see §6).

---

## 5. MCP surface for remote + droid + testing

855+ MCP tools total (`mcp_tools.go` defs, `httpserver.go` dispatch); ~83 relevant here.

**Testing** — `testkit_run`, `testkit_list_specs`, `testkit_last_failure`,
`testkit_flake_report`, `testkit_self_heal_selector`, `run_tests` (auto-detects jest/pytest/
cargo/go/make). History in `~/.yaver/testkit/<root>/history.jsonl` (local).

**Mobile/Hermes** — `mobile_project_status/prepare/build`, `mobile_hermes_reload`,
`mobile_hermes_doctor`. **Wire/wireless** — `wire_push`, `wireless_push`, etc.
**Android/ADB** — `adb_command`, `adb_devices`, `adb_screenshot`. **iOS** — `simulators`,
`simulator_boot/_screenshot`. **Build** — `build_android/_ios`, `native_build`, `eas_*`.

**Capture** — `vibe_preview_start/stop/snapshot/clip_record/clips` (live device stream + MP4,
relay-aware FPS/res profiles), `record_start/stop`, `clip_*`, `screenshot`, `cast_*` (terminal),
`screenlog_*` (18 — local frame+input black box).

**Remote machine transport** — this is the key enabler:
- `acl_add_peer` / `acl_call_peer_tool` / `acl_list_peer_tools` / `acl_health` (`acl.go`):
  register a remote box (HTTP or stdio/`ssh box yaver serve`) and call **any** of its MCP
  tools as JSON-RPC `tools/call`. This is how one agent drives a remote redroid host.
- Relay/QUIC (`bus_relay.go`, `quic.go`) for WAN/cellular peers; `create_task` to delegate a
  whole job to a runner (Claude Code/Codex/OpenCode) on the remote box; mobile reaches a
  runner's `/mcp` via `yaverMcpDirect`.

### MCP gaps (capability exists, but not callable as a tool)
| Capability | Today | Gap |
|---|---|---|
| Boot redroid | `studio/redroid.go` / `redroid_capture.sh` | not an MCP tool — only via `create_task`/shell |
| Install arbitrary APK | `adb install` | only through thin `adb_command`; no `install_app{apk}` |
| AI explore an app | `qa_brain` goal-flows | no `explore_app` that maps screens + emits flows |
| Catch/fix QA run | `qa_flow.go`/`qa_fix.go` (CLI/job) | no `qa_run{mode,apk,flows}` MCP verb |
| Studio capture (screenshots/video) | `redroid_capture.sh` | not MCP (`adb_screenshot`/`adb_devices` were stubbed-out 2026-04-28) |

Closing these turns "an engineer runs scripts" into "any agent (or the chat) orchestrates a
remote redroid QA run end-to-end."

---

## 6. Proposed end-to-end: "ingest an APK → full AI QA → report (+fix if owned)"

A thin orchestration layer over what already exists:

```
APK ──▶ [remote redroid host, registered as MCP peer]
        1. boot_redroid                     (new MCP wrapper over studio/redroid.go)
        2. install_app(apk)                  (new MCP wrapper over adb install -r -g)
        3. explore_app(budget)               (NEW: brain BFS — crawl tappable elements,
                                              dedupe by view-signature, record screen graph,
                                              emit flows/*.flow.yaml + candidate specs)
        4. qa_run(mode=catch, flows)         (MCP wrapper over runQAFlows; oracle bank on)
        5. if source available → mode=fix    (qa_fix loop: patch→hermes-reload→re-verify)
        6. capture(vibe_preview_clip)        (MP4 per flow + final report.json with bugs)
```

- **Fleet:** N redroid containers across M arm64 boxes, each an `acl` peer; the orchestrator
  shards flows across peers (`acl_call_peer_tool` fan-out). Warm "base image" snapshots
  (pre-signed-in) cut per-run cost.
- **Owned vs third-party:** step 5 only runs when a source repo is attached; otherwise the run
  stops at a report (bugs + repro video + VLM verdicts).
- **Determinism ladder:** promote stable discovered flows into `specs/*.test.yaml` so the next
  run gates them deterministically in CI (cheap, no inference), reserving the LLM for new surface.

What to build (smallest set): the 3 MCP wrappers (`boot_redroid`, `install_app`, `qa_run`) +
the **`explore_app` crawler** (the only genuinely new logic) + a `report` artifact. Everything
downstream (brain, oracles, fix loop, capture, peer transport) already exists.

---

## 7. Concrete use cases for a remote redroid box

1. **Headless CI gate** — run `specs/*.test.yaml` on every push from a Linux runner; no Mac,
   no device lab. (`testkit_run` + redroid driver.)
2. **Nightly AI exploration** — `flows/*` against the latest build; oracle bank + VLM produce a
   morning bug report with repro videos.
3. **PR auto-fix** — on the owned app, `mode=fix`: catch → patch (draft, never pushed) →
   Hermes-reload → re-verify; surface the working-tree diff for review.
4. **Store-asset / declaration capture** — drive a feature and screen-record it (exactly the
   Play "special use" FGS declaration video being produced now: redroid → launch → drive to
   *Settings → This phone as a box → Start* → record the foreground-service notification).
5. **Third-party QA-as-a-service** — smoke/regress a vendor or competitor APK (catch-only):
   crash/ANR/blank detection + screen-by-screen VLM verdicts.
6. **Fleet parallelism** — shard a large flow corpus across many redroid peers via `acl`.
7. **Bug-report reproduction** — feed a user's reported flow as a goal; let the brain reproduce
   it headlessly and attach the video.

---

## 8. Risks / honesty

- **redroid UIAutomator can return an empty tree** (observed); the vision fallback covers it but
  vision turns cost more — keep flows short and lean on specs once stable.
- **No GPU on the current boxes** → AI uses **hosted** model APIs (BYOK) or a local Ollama; the
  box itself only runs Android + the agent. Fine for testing; not for on-box model inference.
- **x86_64 hosts** can't run Yaver's arm64-only libs — use arm64 boxes or build an x86 split.
- **Third-party auto-fix is out of scope** without source; don't promise it.
- **Cost control** matters: bound `max_steps`, prefer text nav, gate vision to final asserts,
  reuse warm base images, and promote stable flows to deterministic specs.
