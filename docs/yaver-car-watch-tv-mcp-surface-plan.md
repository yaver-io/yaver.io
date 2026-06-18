# Yaver Car, Watch, TV Surface Plan - MCP + Remote Runtime

Last updated: 2026-06-18

This is the build-start document for Yaver's thin companion surfaces:

- car
- watch
- TV / Apple TV / Android TV
- MCP-driven utilization of those surfaces
- personal assistant and Hermes reload use cases across all of them

Per the repo rule, this document is strategy and implementation guidance, not
source of truth. Before implementing any route, function, plugin, or native
bridge named here, grep the code and use the implementation on disk.

## 1. Thesis

Yaver should treat car, watch, and TV as thin control surfaces over the same
remote runtime fabric:

```text
human intent on a constrained surface
  -> phone / web / MCP / CLI bridge
  -> Yaver agent on owned or rented runtime
  -> API, browser, redroid, Hermes, ffmpeg, pyatv, task runner
  -> terse result, stream, or approval request back to the surface
```

The runtime does the heavy work. The surface only handles input, status,
approval, and handoff.

This gives Yaver one product shape instead of three separate apps:

> Yaver is the personal remote runtime and approval fabric for your devices,
> apps, agents, and open-source engines. Phone, watch, car, TV, web, CLI, and
> MCP are just different front doors.

## 2. Existing Anchors In This Repo

The repo already has real anchors for most of this:

| Capability | Local anchor |
|---|---|
| Remote runtime fabric | `docs/yaver-anywhere/strategy-and-reality.md` |
| Personal app assistant | `docs/yaver-personal-agent-gateway.md` |
| Car voice coding | `docs/yaver-car-voice-coding.md`, `mobile/app/car-voice-coding.tsx` |
| Watch voice terminal | `docs/yaver-smartwatch-voice-terminal.md`, `mobile/native-watch/`, `mobile/native-wear/` |
| Apple TV control and capture | `docs/yaver-appletv-remote-control.md`, `desktop/agent/appletv*`, `desktop/agent/capture.go` |
| Hermes reload | `CLAUDE.md`, `mobile/app/(tabs)/hotreload.tsx`, `/dev/build-native` handlers |
| MCP coverage | `YAVER_MCP_COVERAGE.md`, `desktop/agent/mcp_*`, `desktop/agent/ops*` |
| Personal assistant gateway | `desktop/agent/gateway_*`, `docs/yaver-personal-agent-gateway.md` |
| Remote desktop / stream | `desktop/agent/remotedesktop*`, `remote_runtime_webrtc.go`, `ghost_stream.go` |
| redroid / Android clone | `desktop/agent/studio/redroid.go`, `desktop/agent/android_clone_provision.go`, `desktop/agent/ops_qa.go` |

Implementation rule: these surfaces should call the same agent functions,
ops verbs, and MCP tools as phone/web/CLI. Do not build parallel semantics.

## 3. Platform Reality

### 3.1 Watch

Watch is not a miniature phone. It should not run a coding agent, browser, or
React Native app. It should be:

- voice input
- one-line result
- haptic progress
- approve / deny
- handoff to phone

Use phone-paired mode first:

- watchOS: WatchConnectivity / WCSession to the iPhone app
- Wear OS: Wear Data Layer / MessageClient to the Android app
- phone app then uses existing Yaver mobile connection manager and relay

Standalone LAN / relay can come later, after phone-paired mode proves useful.

External platform references:

- Apple Watch Connectivity: https://developer.apple.com/documentation/watchconnectivity
- Wear OS Data Layer: https://developer.android.com/training/wearables/data/overview

### 3.2 Car

Car is not an arbitrary dashboard. It should be:

- voice-first
- async
- one-sentence status
- no code or diffs read aloud
- approval-only for high-risk actions

Tier 0 is already the correct first product:

- phone app records voice
- phone transcribes with local or configured STT
- phone dispatches to selected Yaver runtime
- remote runner completes the work
- phone reads one terse summary over car audio

Android Auto and CarPlay should be progressive enhancements:

- Android Auto messaging notification: coding assistant as a conversation
- Android Auto weather / EV / status as constrained categories later
- CarPlay communication / voice conversational apps after entitlement work

External platform references:

- Android for Cars categories: https://developer.android.com/training/cars
- Apple CarPlay developer categories: https://developer.apple.com/carplay/

### 3.3 TV

TV is a living-room or lab control surface. It should focus on:

- D-pad remote control
- media/control metadata
- capture-card viewing for user-owned, non-protected sources
- remote runtime wallboard
- approval prompts visible across a room

Apple TV control should use pyatv through a sidecar on a Pi or LAN machine.
Capture should use ffmpeg and existing Yaver stream/capture paths. Yaver must
not add DRM or HDCP circumvention.

External FOSS references:

- pyatv: https://pyatv.dev/
- ffmpeg: https://ffmpeg.org/

### 3.4 MCP

MCP is the universal automation front door. Every feature below should have
one of these shapes:

1. First-class MCP tool, when it returns a special content type or is a core
   agent feature.
2. Ops verb, when it needs remote machine routing, relay fallback, guests, or
   mobile/web reuse.
3. Thin MCP wrapper over an ops verb, when both are needed.

Default rule:

```text
remote target involved -> ops verb is source of truth
MCP clients involved   -> MCP tool wraps ops verb
mobile/web involved    -> call ops verb through existing quic/connection client
```

This matches the Apple TV decision already documented: mobile uses ops verbs,
MCP can expose thin first-class wrappers.

## 4. Core Use Cases

### 4.1 Hermes Reload From Any Surface

This is still the sharpest developer wedge.

Use case:

```text
agent edits RN/Expo app
  -> Yaver builds Hermes bytecode on selected machine
  -> bundle is signed / validated
  -> phone receives bundle
  -> Yaver mobile reloads guest app inside native container
  -> constrained surface receives "loaded" or "failed" status
```

Surface usage:

| Surface | Hermes reload role |
|---|---|
| Phone | Primary preview, load, reload, crash view |
| Watch | "Reload app", "did it load?", approve switching target |
| Car | "Reload the app on my phone", one-sentence result |
| TV | Big-room preview or status wall, D-pad for selecting target app |
| MCP | `phone_project_push` / `mobile_project_build` style tool call from any AI agent |

MCP / ops shape:

- `mobile_project_build`: compile bundle on the selected runtime
- `phone_project_push`: push to paired phone
- `dev_start`, `dev_reload`, `dev_stop`: dev server lifecycle
- `remote_runtime_status`: status and target selection
- new optional ops verb: `hermes_reload_target_status`

Optimization:

- Keep bundle building on the runtime that has the repo and dependencies.
- Keep phone as the preview sink.
- Surfaces only issue commands and receive short status.
- Watch/car never show logs by default.
- TV can show a status wall and stream preview, but phone remains the canonical device.

### 4.2 Personal Assistant CRUD Over User Apps

This is the broadest non-developer product case.

Universal operation:

```text
utterance
  -> connector + verb + resource + params + risk
  -> execute via api | playwright/chromedp | redroid | mcp
  -> verify
  -> summarize
  -> ask for approval when needed
```

Examples:

| Request | Operation | Engine |
|---|---|---|
| "Is the charger free?" | `GET ev.station_status` | API or redroid |
| "What is my bank balance?" | `GET bank.balance` | API or browser |
| "Book the 9am slot" | `ADD calendar.event` | API or browser |
| "Reorder groceries" | `ADD commerce.order` | browser or redroid |
| "What is EUR from my broker?" | `GET broker.fx_rate` | API or browser |
| "Pay this bill" | high-risk `ACT` | browser with dry-run and confirm |

Surface roles:

| Surface | Assistant role |
|---|---|
| Phone | Main chat, connector setup, auth binding, approval details |
| Watch | Quick question, yes/no approval, result glance |
| Car | Voice-only query and safe approval, no long content |
| TV | Household wallboard, remote status, shared approval prompt |
| MCP | Third-party AI agents call the same gateway operations |

MCP / ops shape:

- `gateway_query`: read-only GET
- `gateway_plan`: NL to grounded plan
- `gateway_act_dry_run`: drive to final screen and stop
- `gateway_act_confirm`: execute the pending act
- `gateway_connector_list`
- `gateway_connector_author`
- `gateway_auth_bind`
- `gateway_audit`

Optimization:

- Official API first.
- Browser automation second.
- redroid for mobile-only apps.
- Never bypass blocks or automate captcha/2FA.
- Read-only first; ACT only with explicit confirm, dry-run preview, post-verify, audit.
- Store credentials and sessions in vault, never Convex.

External FOSS references:

- Playwright: https://playwright.dev/
- chromedp: https://github.com/chromedp/chromedp
- redroid: https://github.com/remote-android/redroid-doc

### 4.3 Remote Coding From Car

Use case:

```text
"Fix the failing auth test and run it"
  -> phone transcribes
  -> selected Yaver runtime creates coding task
  -> remote agent works
  -> car receives one-sentence result
```

Rules:

- No code readback.
- No diffs readback.
- No long logs.
- "Show me the diff" means handoff to phone.
- Push/deploy/delete/force actions require explicit confirmation.

MCP / ops shape:

- `create_task` with `Voice:true` / driving-safe viewport
- `get_task`
- `continue_task` for short follow-up
- `stop_task`
- `task_handoff_to_phone`

Optimization:

- Use client-side STT/TTS fallback so it works even if the remote box has no voice keys.
- Prefer agent-side voice WebSocket when configured for lower latency.
- Readback should be deterministic summarization, not another expensive model call.
- Car task state should be the same task ID visible on phone/web/MCP.

### 4.4 Watch Approval Layer

Use case:

```text
remote agent wants to deploy
  -> Yaver policy marks action confirm-required
  -> phone pushes approval to watch
  -> watch shows 1-line summary + approve/deny
  -> result returns to task runner
```

Approval types:

| Risk | Watch action |
|---|---|
| Low | one-tap approve |
| Medium | approve with short reason shown |
| High | watch can deny or handoff; approve requires phone second factor |
| Financial / irreversible | watch notification only, phone approval required |

MCP / ops shape:

- `approval_request_create`
- `approval_request_status`
- `approval_request_answer`
- `device_broadcast_command` to wake phone/watch
- `task_policy_gate` wrapping risky tool calls

Optimization:

- Watch holds no secret by default.
- Phone remains session holder.
- Queue replies via WCSession `transferUserInfo` / Wear MessageClient.
- Do not poll from the watch in the background.

### 4.5 TV Remote Runtime Wall

Use case:

```text
TV screen shows selected Yaver runtime:
  - active task
  - build status
  - stream/capture frame
  - approval prompt
  - Apple TV now-playing / remote controls
```

TV modes:

| Mode | Description |
|---|---|
| Runtime wall | current tasks, builds, deploys, remote session |
| D-pad remote | Apple TV / Android TV / browser D-pad control |
| Capture viewer | capture card / camera / non-protected source stream |
| Pairing station | QR or short-code pairing for a room device |
| Approval board | shared confirm/deny prompt for lab/household use |

MCP / ops shape:

- `tv_runtime_status`
- `tv_runtime_select`
- `tv_dpad_input`
- `appletv_remote_key`
- `appletv_now_playing`
- `capture_start`, `capture_stop`, `capture_frame`
- `stream_offer`

Optimization:

- D-pad abstraction should be shared by TV, car, and watch.
- Avoid pointer-only remote desktop for TV.
- Use MJPEG fallback where WebRTC is not practical.
- Keep capture content-agnostic and do not inspect or police media.
- Do not build or recommend HDCP circumvention.

## 5. FOSS Wrapping Plan

Yaver should wrap mature open-source engines instead of hand-rolling their
domains.

| Engine | Use in Yaver | Surface consumers |
|---|---|---|
| Playwright | browser connectors, task automation, visual flows | assistant, MCP, phone, car/watch via summaries |
| chromedp | Go-native CDP control, lower-level browser tools | MCP, gateway, remote runtime |
| redroid | Android clone, mobile-only app automation, QA | assistant, MCP, phone, TV wall |
| scrcpy | Android screen/control/record reference path | TV wall, QA, remote device view |
| pyatv | Apple TV control and metadata | phone, TV, MCP |
| ffmpeg | capture, transcode, MJPEG/WebRTC source | TV, phone, web, MCP |
| whisper / STT providers | speech input | car, watch, phone |
| system TTS / cloud TTS | terse readback | car, watch, phone |

External FOSS references:

- scrcpy: https://github.com/Genymobile/scrcpy
- pyatv: https://pyatv.dev/
- Playwright: https://playwright.dev/
- chromedp: https://github.com/chromedp/chromedp
- redroid: https://github.com/remote-android/redroid-doc

## 6. Shared Abstractions To Build

### 6.1 Surface Viewport

Every task or tool call should carry surface context:

```json
{
  "surface": "phone|watch|car|tv|web|mcp|cli",
  "interaction": "voice|dpad|touch|keyboard|approval|stream",
  "ttsBudgetChars": 200,
  "visualBudget": "none|glance|panel|full",
  "riskPolicy": "normal|driving|watch|shared-tv"
}
```

This lets the same task return different result shapes:

- MCP: structured JSON plus stream ID
- phone: full result
- watch: one line
- car: one sentence, spoken
- TV: large status and optional stream

### 6.2 D-Pad Command Model

Needed for TV, Apple TV, watch quick controls, and car-safe controls.

```json
{
  "target": "appletv|androidtv|browser|remote_runtime|phone_preview",
  "key": "up|down|left|right|select|back|home|play_pause|next|previous",
  "repeat": 1
}
```

Ops verbs:

- `dpad_input`
- `dpad_targets`
- `dpad_focus_state`

Adapters:

- Apple TV: pyatv
- Android TV: ADB / media key events
- Browser: DOM focus / keyboard events
- Remote desktop: normalized keyboard input

### 6.3 Approval Protocol

Approval must be surface-independent:

```json
{
  "id": "approval_x",
  "taskId": "task_x",
  "risk": "low|medium|high|financial|destructive",
  "title": "Deploy to production",
  "summary": "About to deploy web from main to production.",
  "detailsRef": "local-only-or-phone-ref",
  "allowedAnswers": ["approve", "deny", "handoff"],
  "expiresAt": 1234567890
}
```

Surfaces render the same approval:

- watch: title + summary + approve/deny
- car: read summary, accept explicit spoken phrase where allowed
- TV: large prompt, D-pad approve/deny only for low/medium
- phone: full details and high-risk approval
- MCP: ask user / tool result requiring human confirmation

### 6.4 Gateway Connector Descriptor

Personal assistant connector capabilities should be tool-schema shaped so the
router can use them like MCP tools:

```json
{
  "connectorId": "broker_x",
  "capabilityId": "fx_rate",
  "verb": "get",
  "title": "Read FX rate",
  "paramsSchema": {},
  "answerSchema": {},
  "engine": "api|playwright|redroid|mcp",
  "risk": "read|low-act|high-act",
  "authRef": "vault://gateway/broker_x"
}
```

### 6.5 Runtime Target Descriptor

All surfaces should choose the same target shape:

```json
{
  "deviceId": "dev_x",
  "alias": "primary",
  "families": ["desktop", "linux", "redroid", "phone-preview", "browser"],
  "reachability": ["lan", "relay", "tailscale"],
  "capabilities": ["tasks", "hermes", "browser", "redroid", "capture", "voice"]
}
```

This prevents car/watch/TV from each inventing separate device pickers.

## 7. MCP Utilization Model

### 7.1 MCP Tool Packs

Create or group tools into packs that match real workflows.

| Pack | Tools |
|---|---|
| `yaver.mobile_runtime` | Hermes reload, phone push, dev server, crash |
| `yaver.personal_assistant` | gateway query, plan, dry-run, confirm |
| `yaver.car_voice` | driving task create, readback, handoff |
| `yaver.watch_approval` | approval request/status/answer |
| `yaver.tv_control` | dpad, stream, appletv, capture |
| `yaver.android_clone` | redroid boot, install, drive, screenshot |
| `yaver.browser_runtime` | browser sessions, click/type/extract/screenshot |

### 7.2 Ops vs MCP Rule

Use ops when:

- target machine may be remote
- mobile/web needs the same command
- guest access matters
- relay fallback matters
- stream IDs are needed

Use MCP when:

- an external agent is the caller
- return value needs MCP content blocks
- the tool is local to the attached agent

Use both when:

- a remote feature should also be agent-callable
- example: `appletv_now_playing`, `capture_frame`, `robot_camera`

### 7.3 Tool Result Shapes

For constrained surfaces, tool results should include a terse summary:

```json
{
  "ok": true,
  "summary": "Done. Tests pass on primary.",
  "spoken": "Done. Tests pass on primary.",
  "handoff": {
    "surface": "phone",
    "route": "/tasks/task_x"
  },
  "streamId": "stream_x",
  "taskId": "task_x"
}
```

MCP agents can use the structured fields. Watch/car can use `spoken`. TV can
use `summary` and `streamId`.

## 8. Implementation Sequence

### Phase 0 - Contract Unification

Goal: make car/watch/TV call the same runtime primitives.

Tasks:

1. Add shared `SurfaceViewport` type for task/tool calls.
2. Add shared approval request shape.
3. Add shared D-pad command shape.
4. Add shared runtime target descriptor.
5. Make mobile connection manager expose target capabilities in one shape.

Acceptance:

- A task created from phone, watch bridge, car screen, or MCP can carry surface
  metadata.
- Existing phone task flow still works.
- No platform-specific business logic in native watch/car/TV bridge code.

### Phase 1 - Hermes Reload Surface Pack

Goal: make Hermes reload controllable from MCP, watch, car, and TV without
duplicating the build path.

Tasks:

1. Audit existing `/dev/build-native`, mobile hot reload, and MCP mobile tools.
2. Add a small status tool/ops verb for active Hermes target readiness.
3. Add watch/car commands that only call phone-side JS.
4. Add TV status panel or route that shows current phone preview target.
5. Add MCP pack docs for Hermes reload.

Acceptance:

- MCP agent can push a RN/Expo app to phone.
- Watch can request reload and get a pass/fail summary.
- Car can request reload by voice and hear one sentence.
- TV can show reload status without being the canonical preview sink.

### Phase 2 - Watch Approval V1

Goal: watch approves or denies low/medium-risk Yaver actions.

Tasks:

1. Wire JS watch bridge to receive `approval_request`.
2. Render native watch approve/deny UI.
3. Send answer back through phone bridge.
4. Integrate with task risk gate for deploy/push/delete/prod keywords.
5. Add MCP wrapper for creating and waiting on approval.

Acceptance:

- Remote task can pause on approval.
- Watch shows short summary.
- Watch answer resumes or cancels task.
- High-risk actions can force phone handoff.

### Phase 3 - Car Voice Assistant V1

Goal: driving-safe personal assistant and remote coding from car.

Tasks:

1. Use existing `carVoiceCoding` loop as the base.
2. Route both coding tasks and `gateway_query` through the same voice UI.
3. Add driving-safe readback policy to gateway results.
4. Add Android Auto MessagingStyle activation behind explicit native rebuild.
5. Keep CarPlay as entitlement-stage only.

Acceptance:

- "Fix failing tests" creates a remote coding task.
- "Is the charger free?" calls gateway read-only connector.
- Result is one sentence.
- Requests for code/diff/log hand off to phone.
- Risky ACT requests stop for confirmation.

### Phase 4 - TV Runtime Wall + D-Pad

Goal: TV becomes a glanceable runtime/control wall.

Tasks:

1. Build shared D-pad ops verb.
2. Add adapters for Apple TV pyatv and browser/remote runtime.
3. Add TV route/screen for runtime status.
4. Add capture frame/stream panel using existing capture endpoints.
5. Add MCP wrappers for D-pad and capture.

Acceptance:

- Phone/web/MCP can send the same D-pad command.
- TV can show selected runtime/task status.
- Apple TV control works through ops path.
- Capture remains content-agnostic and does not attempt DRM circumvention.

### Phase 5 - Personal Assistant Gateway First Slice

Goal: read-only personal assistant across phone/car/watch/MCP.

Tasks:

1. Implement connector registry if not already complete.
2. Implement read-only intent router.
3. Add one web connector using Playwright/chromedp.
4. Add one redroid connector for mobile-only app read.
5. Add surface-specific result summaries.
6. Add car/watch entry points.

Acceptance:

- User can ask a GET question from phone, car, watch, or MCP.
- Same gateway execution path runs.
- Credentials remain vault-local.
- A blocked service returns a block finding and does not retry with evasion.

### Phase 6 - ACT With Consent

Goal: enable add/update/delete safely.

Tasks:

1. Add dry-run preview protocol.
2. Add pending action token.
3. Add approval routing across phone/watch/car/MCP.
4. Add post-verify.
5. Add audit summary.
6. Add per-connector spend/velocity/policy caps.

Acceptance:

- ACT never fires from ambiguous voice alone.
- Financial/destructive actions require phone approval.
- Watch/car can deny or hand off.
- Post-verify confirms actual result.

## 9. Product UX Rules

### Watch

- Show one thing at a time.
- Prefer haptic plus one line.
- No logs, no diffs, no setup.
- Every detail screen should have "open on phone."

### Car

- Never require reading.
- Never speak code, diffs, secrets, or long logs.
- One command in, one status out.
- Confirm only with explicit phrases.
- If unsure, decline or hand off to phone.

### TV

- Use D-pad, not tiny pointer UI.
- Use large text and stable focus.
- Treat TV as shared space: hide secrets by default.
- Use streams and status walls, not dense forms.

### MCP

- Return structured data plus human summary.
- Expose stream IDs for long-running work.
- Do not hide approval gates from agents.
- Use same semantics as CLI/mobile/web.

## 10. Security And Policy Rules

These rules are load-bearing:

- Yaver is content-agnostic for capture and streaming.
- No DRM / HDCP circumvention.
- No WebView for third-party React Native apps; use Hermes push path.
- No captcha-solving, bot-detection evasion, UA spoofing, WAF bypass, or IP rotation.
- Official API first for personal assistant connectors.
- User credentials and sessions stay in vault or local storage, never Convex.
- Convex stores identity, discovery, session bookkeeping, and audit summaries only.
- ACT actions need consent, audit, post-verify, and policy caps.
- Same-user peer egress only; never open relay.
- Third-party reads must respect blocks and rate limits.

## 11. Naming Proposal

Use names that explain the runtime model:

- Yaver Runtime Surfaces
- Yaver Surface Pack: Car
- Yaver Surface Pack: Watch
- Yaver Surface Pack: TV
- Yaver Personal Runtime
- Yaver Assistant Gateway
- Yaver Hermes Runtime

Avoid positioning this as three unrelated apps. The product is one runtime
fabric with multiple surfaces.

## 12. First Tickets

1. `SurfaceViewport` audit: find task/tool structs that need surface metadata.
2. Hermes MCP pack audit: list current MCP tools and missing wrappers.
3. Watch bridge smoke: verify phone-side native bridge can emit JS event and send reply.
4. Car voice gateway route: allow car voice screen to call `gateway_query` as well as coding task.
5. D-pad ops skeleton: add target-neutral `dpad_input` with no adapters first.
6. TV runtime status route: show selected device, task, stream availability.
7. Gateway read-only connector: implement one Playwright connector and one redroid connector.
8. Approval protocol: implement low/medium approval with phone and watch routing.
9. ACT dry-run design: define pending action schema and storage.
10. Docs sync: update older car/watch/TV docs after each implementation lands.

