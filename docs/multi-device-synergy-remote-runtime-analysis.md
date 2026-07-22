# Multi-device Synergy for Yaver Remote Runtime

Last updated: 2026-07-22

Status: analysis and product architecture. Code is still the source of truth.
Before implementing any route, payload, native bridge, or SDK behavior named
here, grep the repo and use the implementation on disk.

## 0. Core thesis

Yaver's first multi-device wedge should be development vibing: the user points
screens, recordings, screenshots, voice, logs, SDK telemetry, and live preview
state at a remote runtime; the runtime analyzes what is happening, changes code,
runs checks, reloads the app, and reports back through whatever device is
closest.

Yaver should not treat tvOS, car, watch, AR/VR, mobile, web, CLI, MCP, and SDK
embeds as separate products. They are different sensing, control, preview, and
feedback surfaces around the same development runtime:

```text
human intent + sensory evidence from whichever device is nearest
  -> surface-aware ingress: voice, screenshot, video, logs, SDK events, preview state
  -> runtime router chooses the right owned runtime/session/project
  -> runner analyzes, edits, tests, renders, reloads, or asks for approval
  -> surface-aware feedback returns as speech, haptic, glance, panel, stream, or handoff
```

The product sentence is:

> Yaver lets me vibe-develop against my own remote runtimes from any device:
> speak, show, record, test, reload, approve, and hear back.

The core development loop is:

```text
phone / SDK / TV / glasses / watch / car
  "this screen is broken" + screenshot/video/log/audio/preview state
    -> Yaver routes to the selected runtime and active project/session
    -> request enters the remote runner queue
    -> runner inspects evidence, edits code, runs tests, rebuilds/reloads
    -> app preview updates inside the Yaver mobile container connected to that runtime
    -> watch/car/TV/glasses speak or show: "Done. You can test it on your phone."
    -> phone/web/desktop can open the detailed diff, logs, pane, and replay
    -> Yaver asks: "Deploy to TestFlight / Google Play internal testing?"
```

This is not "watch app controls phone app." The phone is only one possible
bridge. The user's mental model should be "Yaver is my remote runtime fabric;
every device is a sensor, remote control, output channel, or preview screen."

Generic remote machines still matter. A runtime can be a Windows PC, Mac mini,
Linux workstation, cloud workspace, Android device farm, redroid box, iOS
simulator host, or embedded/robotics controller. But the architecture is not
"Windows remote control." It is "development runtime orchestration from all
surfaces."

### 0.1 Development vibing is the priority

The first-class use case is not generic personal assistant CRUD. It is:

```text
observe app/system behavior
  -> explain what is wrong
  -> produce a code or config change
  -> run the relevant check
  -> render the result on a real target
  -> collect human feedback
  -> iterate
```

Yaver's multi-device advantage is that every surface can contribute a different
piece of this loop:

| Surface | Development-vibing role |
|---|---|
| Phone | primary preview, camera/screen capture, private approval, detailed fix review |
| SDK in app | screen/audio/events/logs, hot reload, in-context bug report |
| Watch | always-available voice command, haptic progress, private approve/deny, TTS result |
| Car | hands-free "keep going / run tests / summarize result" while away from desk |
| TV | shared preview wall, task/session wallboard, visual QA from across the room |
| AR/VR | private spatial cockpit for previews, logs, command cards, multiple runtimes |
| Web/Desktop | full detail: diff, terminal, logs, project graph, review |
| MCP/CLI | automation and external agent entry |
| Remote runtime | edits/builds/tests/renders/analyzes evidence |
| Remote runner queue | backlog, autorun feed, active/done state, retry/cancel |

The sensor inputs should be generic:

- screenshot
- screen recording
- live video stream
- mic narration
- STT transcript
- touch/click/key event trace
- command output
- app logs
- browser console/network logs
- SDK breadcrumbs
- crash report
- current preview URL/bundle/version
- device model/screen dimensions/orientation
- runtime session pane tail

The output should be generic too:

- one spoken sentence
- haptic status
- glance card
- command card
- preview stream/frame
- hot reload status
- diff/log/detail handoff
- approve/deny prompt

## 1. What exists in the repo today

### 1.1 Surface identity already exists

`desktop/agent/surface.go` normalizes `X-Yaver-Surface` into:

- `mobile`
- `tablet`
- `tv`
- `car`
- `watch`
- `vision`
- `web`
- `cli`
- `desktop`

It correctly says surface identity is advisory, never authorization. That is the
right base. Authorization must stay with bearer tokens, Secure Enclave/device
keys, relay access graph, and owner checks.

Mobile also has a richer TypeScript viewport model in
`mobile/src/lib/runtimeSurfaceTypes.ts`:

- surfaces: `wearable-watch`, `car-audio`, `tv-apple`, `headset-visionos`,
  `web-spatial-vr`, `mcp`, `cli`, etc.
- interaction: `voice`, `dpad`, `touch`, `keyboard`, `approval`, `stream`
- visual budget: `none`, `glance`, `panel`, `full`
- risk policy: `driving`, `watch`, `shared-tv`, `spatial`, etc.

That is already very close to the abstraction Yaver needs. The missing part is
making this a first-class runtime routing contract, not just request hints.

### 1.2 Voice ingress exists, but it is task-centric

The agent voice WebSocket is in `desktop/agent/voice_http.go`:

- `GET /voice/status`
- `WS /voice/stream`
- start frame includes `surface`, `interaction`, `paneCount`, `ttsBudget`,
  `visualBudget`, `riskPolicy`, `sttProvider`, `ttsProvider`
- audio is PCM 16-bit LE, 16 kHz mono
- server emits provider info, partial/final transcript, task lifecycle, TTS
  frames, and errors

`desktop/agent/voice_dispatch.go` turns a final transcript into a task with
source `voice-input`. It sets a `TaskViewport` so the prompt wrapper can answer
appropriately for voice/readback.

This is useful, but for multi-device synergy there are two distinct command
classes:

1. **Task mode:** create new work and poll it. Good for "run tests", "fix this",
   "deploy after confirmation".
2. **Live session mode:** drive an existing Claude/Codex/OpenCode tmux runner.
   Better for "keep going in that session", "answer option two", "retry the
   build in that pane".

Yaver needs to route watch/car/tv voice to live session mode when the user names
an existing runtime/session.

### 1.3 Live runner turn exists and is the right primitive

`desktop/agent/runner_session_turn.go` implements:

```text
POST /runner/session/turn
  { session?, runner?, text?, choice?, waitMs? }
```

It returns:

- target session
- runner
- whether it sent a prompt or choice
- `awaitingChoice`
- `options[]`
- plain text pane tail

This endpoint is more important than `/watch/turn` for the "my watch controls
my active development runtime" product. `/watch/turn` starts a new task.
`/runner/session/turn` drives the live runner session already doing the work.

It also encodes the hard safety lessons:

- do not type prompts into menus
- do not send choices when no menu is visible
- send menu digits without Enter
- wait for pane redraw to settle before interpreting state
- never type into a tmux session unless a runner process is actually confirmed

Those rules should become the invariant for every constrained surface: watch,
car, TV, AR glasses, SDK voice overlay, and MCP wrappers.

### 1.4 Watch transport exists, with the right shape

The watch path has three layers:

- watchOS app under `watch/`
- Wear OS app under `wear/`
- phone-side bridge in `mobile/src/lib/watchEntry.ts` and
  `mobile/src/lib/watchBridge.ts`

The phone-paired mode is correct:

```text
Watch -> WatchConnectivity / Wear Data Layer -> Phone
Phone -> selected Yaver remote box -> result
Phone -> Watch reply
```

`watchBridge.ts` already has:

- transcript, confirm, intent, and wake messages
- risky-action confirmation
- read-code handoff
- intermediate `ack` / `working`
- final `summary`
- optional `wakeBox` for parked managed boxes

The gap is that this bridge currently reuses the car task loop. The next
architecture should let it choose:

- idea capture into the active development context
- `/goal` creation or goal update
- start-coding routing for a fresh implementation
- autorun enqueue/start for longer async work
- existing live runner session turn
- new task dispatch
- ops verb
- SDK feedback action
- wake/resume flow
- handoff to phone/TV/spatial

There is already an important product hint in `mobile/src/lib/watchPrompt.ts`:
watch prompts classify into `idea-capture`, `browser-automation`,
`remote-runtime-question`, and `implementation`. That should become the first
watch experience, not an edge case. A user often does not know whether they are
"creating a task", "starting coding", "adding to autorun", or "setting a goal".
They just got an idea and talk to their watch. Yaver should preserve the idea
and route it.

### 1.5 Car voice loop exists, with good constraints

`mobile/src/lib/carVoiceCoding.ts` is the Tier 0 car loop:

```text
record -> STT -> dispatch to remote box -> poll task -> summarize -> TTS
```

It refuses read-code requests and clamps spoken output. That is exactly right
for driving.

`mobile/src/lib/carSessionTurn.ts` already models the live-session path for car:

- maps spoken "one/two/yes/no" into choice digits
- calls `/runner/session/turn`
- summarizes pane text
- refuses code-like lines for TTS

This should be promoted from car-specific code into a shared constrained-surface
session-turn module used by watch, car, TV, AR glasses, and SDK voice overlays.

### 1.6 tvOS exists as a lean-back control surface

`tvos/` is a real SwiftUI app. Important constraints from
`docs/yaver-tvos-surface.md`:

- tvOS cannot record audio directly
- tvOS can use system dictation through text fields
- TTS works with `AVSpeechSynthesizer`
- no WebKit, so no WebView terminal
- plain text pane rendering works because `tmux capture-pane -p` strips ANSI

`tvos/YaverTV/AgentClient.swift` calls `/ops` and sets `X-Yaver-Surface`.
It also comments that the `machine` field can select a remote target after a
reachable agent is found. That is a key design point for generic remote runtime
routing across local machines and cloud workspaces.

tvOS should be the shared-room wallboard and approval/status display, not a
full IDE. It can show the pane tail, task list, runtime dashboard, preview
frames, and "needs choice" prompts.

### 1.7 AR/VR exists as a spatial presentation plan

`docs/xr-spatial-design.md` frames the phone as brain/network and glasses as
display/head-pose. That is consistent with Yaver:

- phone owns mesh, auth, and data connectivity
- glasses show spatial panels
- voice is primary
- gaze/phone acts as selection and approval

There is also an actual Mentra miniapp in `mentra-miniapp/src/index.ts`:

- receives transcription from glasses
- dispatches to `/voice/dispatch`
- uses Mentra TTS when available
- subscribes to `/blackbox/command-stream`
- displays command/status overlays on HUD

This is a strong early proof of the "tiny display, voice in, voice out, command
stream as feedback" pattern.

### 1.8 Feedback SDK already has the right product loop

The SDK examples describe:

- full interactive: screen + mic streaming, agent fixes, hot reload
- semi interactive: stream + conversation
- post mode: offline capture, later fix

This should be folded into the multi-device model:

```text
App with Yaver SDK
  -> sends screen/audio/events
  -> remote runner fixes
  -> Hermes/hot reload returns to app
  -> watch/car/tv/spatial receive progress, approval, and summary
```

The SDK is not just a bug-report widget. It is another surface in the same
runtime loop.

### 1.9 Autorun and runner ops already point toward the queue

There are two important existing docs that should be treated as prior art, not
ignored:

- `docs/architecture/AUTORUN_TOPIC_QUEUE.md`
- `docs/design/yaver-autorun-closed-loop-kicker.md`

The topic-queue doc says the missing product is "feed a running loop by intent,
from any surface." It also records a key implementation warning: do not build a
second queue beside an inert one. If an existing runner/keeper queue accepts
prompts but nothing drains it, fix that false-ready path first or make it fail
loudly.

The closed-loop-kicker doc defines autorun as:

```text
task/plan MD
  -> remote persistent runner loop
  -> edit/test/gate/commit
  -> progress MD
  -> optional highlight reel
  -> phone watches progress and output
```

That is directly aligned with this doc. The only framing change here is product
surface: the user-facing concept should be a remote runner queue, while autorun
is one execution engine beneath it.

The agent already exposes `runner_turn` and `runner_sessions` as ops verbs in
`desktop/agent/ops_runner_turn.go`. This matters because ops verbs remote
generically through the machine envelope. A watch/car/TV/MCP surface should not
need a bespoke raw HTTP path to reach the selected runtime. It should call the
same source-of-truth operation through the transport it already has.

## 2. The product model Yaver should expose

### 2.1 Device roles, not device apps

Every user device should advertise a role set:

| Role | Meaning | Examples |
|---|---|---|
| `ingress.voice` | can capture or provide transcript | watch, phone, car, glasses, tv dictation |
| `ingress.text` | can type prompt | phone, TV text field, web, CLI |
| `feedback.tts` | can speak short replies | watch, phone, car, TV, glasses |
| `feedback.haptic` | can buzz status | watch, phone |
| `feedback.glance` | can show one-line/prompt | watch, car notification, HUD |
| `feedback.panel` | can show cards/pane summaries | phone, TV, AR/VR |
| `feedback.full` | can show logs/diffs/terminal | phone, tablet, web, desktop |
| `preview.video` | can show app/runtime stream | phone, TV, web, spatial |
| `approval.private` | safe for secrets/risky actions | phone, watch |
| `approval.shared` | visible to others, restrict detail | TV |
| `runtime.runner` | can execute tasks/sessions | Mac, Windows, Linux, cloud workspace |
| `runtime.preview` | can run app target | iPhone, Android phone, emulator, TV, XR emulator |
| `bridge` | can relay another surface | phone for watch/glasses/car |

Then each flow becomes role-based:

- watch is excellent `ingress.voice`, `feedback.tts`, `feedback.haptic`,
  `approval.private`
- car is `ingress.voice`, `feedback.tts`, high-risk gated, no logs
- TV is `feedback.panel`, `preview.video`, `approval.shared`
- AR/VR is `feedback.panel`, `preview.video`, `ingress.voice`, gaze selection
- phone is the universal bridge/control/private-detail surface
- Mac/Windows/Linux/cloud machines are runtimes

### 2.2 The surface should choose output budget, not execution semantics

The runner should do the same work regardless of whether the command came from
watch, car, TV, or web. The surface should only change:

- how much text comes back
- whether TTS is used
- whether code/logs are suppressed
- how risky actions are confirmed
- where details are handed off

Bad:

```text
watch task engine
car task engine
tv task engine
spatial task engine
```

Good:

```text
one runtime command router
  with surface viewport constraints
  and output adapters
```

### 2.3 Idea capture should be the watch default

The user should be able to say:

- "Idea: the checkout button should explain why it is disabled."
- "Add to the current app: show failed validations inline."
- "Yaver, start coding the onboarding fix I mentioned."
- "Add this to autorun for the mobile app."
- "Make this the current goal and keep going."
- "Run the tests on the box."
- "Answer option two in that session."
- "Read me the result on my watch."

This requires explicit routing state:

- target device id
- target device nickname
- OS/platform
- reachable route: LAN, mesh, relay, cloud wake
- runner sessions on that device
- default runner for that device
- current project/session affinity
- last surface that asked for the work
- preferred feedback surface
- current goal
- active autorun slots
- active project/app preview
- latest SDK feedback bundle or screen recording

The code already has many pieces. The missing piece is one user-visible command
router that binds them.

The default classification should be conservative:

| Utterance shape | Default action |
|---|---|
| vague thought, "idea", "maybe", "what if" | capture as idea note against current project/session |
| "goal", "make this the goal", "focus on" | create/update `/goal` context |
| "start coding", "build", "implement", "fix" | route through `startCoding.ts` / runtime turn |
| "add this to autorun", "keep working on this async" | enqueue/start autorun or remote runner queue item |
| "continue", "answer one/two", "keep going" | live session turn |
| "why did this fail", "what happened" | analyze logs/session/recording and summarize |
| destructive/deploy/payment/secrets | private confirmation first |

The watch should not force the user to structure the thought. It should respond:

```text
"Captured. I'll attach it to the current app."
"Starting that on your box."
"Added to the remote runner queue."
"Done. You can test it in the Yaver mobile app."
"It passes locally. Do you want me to deploy to TestFlight or Google Play internal?"
"I need you to choose a target on your phone."
```

### 2.4 The remote runner queue is the development spine

The user should not need to know whether a request becomes a task, live session
turn, autorun item, or full `/goal`. The product should expose a simple queue:

```text
idea / bug / command / recording
  -> queued
  -> running on selected runtime
  -> needs input / needs approval
  -> ready to test in Yaver mobile
  -> ready to deploy
  -> done
```

The queue should be addressable from every surface:

- watch: add item, hear status, approve/deny, mark tested
- phone: inspect item, open preview, test in container, view diff/logs
- TV: wallboard of queued/running/done items
- AR/VR: spatial board of queue + previews
- car: add hands-free item, hear completion
- SDK: attach recording/screenshot/logs to queue item
- MCP/CLI: enqueue, status, cancel, retry, deploy gate

An item should carry:

```jsonc
{
  "itemId": "rq_...",
  "origin": {
    "surface": "watch",
    "input": "voice",
    "transcript": "idea: make onboarding explain permissions"
  },
  "target": {
    "deviceId": "selected-runtime",
    "project": "current-app",
    "session": "auto"
  },
  "evidence": [
    { "kind": "screenshot", "ref": "latest-mobile-frame" },
    { "kind": "recording", "ref": "feedback-clip" },
    { "kind": "logs", "ref": "preview-logs" }
  ],
  "execution": {
    "mode": "goal|autorun|task|session-turn|analysis",
    "runner": "auto",
    "status": "queued|running|needs_input|ready_to_test|ready_to_deploy|done|failed"
  },
  "testTarget": {
    "kind": "yaver-mobile-container",
    "connectedDeviceId": "phone-or-tablet",
    "reload": "hermes-bundle|dev-server|native-build"
  }
}
```

When the runner finishes the code/test loop, the next user-facing state should
not be only "done". For development vibing it should be:

```text
"Done. I loaded it in Yaver mobile; you can test it now."
```

If local checks pass and the project has deploy capability, the next prompt is:

```text
"It passes locally. Do you want me to deploy to TestFlight, Google Play internal testing, or leave it here?"
```

This must stay explicitly confirmed. Per project rules, Yaver must never deploy
mobile, publish npm, push a tag, or release externally without user confirmation.

### 2.5 Queue, `/goal`, autorun, and live sessions are different layers

The user should see one simple mental model, but internally the layers are
different:

| Layer | Purpose | Good for |
|---|---|---|
| idea note | preserve vague thought | "maybe the empty state needs a better CTA" |
| `/goal` | focus the current thread/run | "focus on Yaver mobile dogfood queue" |
| remote runner queue | durable backlog of development items | watch/car/SDK inputs, ready-to-test state |
| autorun | autonomous repeated implementation/check loop | larger async work, multi-runner convergence |
| live session turn | immediate control of existing runner pane | "continue", "answer two", "retry" |
| task dispatch | one bounded unit of work | "run tests", "fix this bug", "analyze recording" |

The watch/car/TV user should not have to choose these. The router should:

```text
rough idea       -> idea note + optional queue item
"make it goal"   -> /goal
"work on this"   -> queue item -> task/live session
"autorun this"   -> queue item -> autorun enqueue/start
"keep going"     -> live session turn
"answer two"     -> live session choice
```

The queue is the user-visible spine because it can show state across all of
those execution modes:

```text
captured -> queued -> running -> needs_input -> ready_to_test -> ready_to_deploy -> done
```

## 3. Proposed runtime command router

### 3.1 New concept: `runtime_turn`

Yaver needs one surface-neutral operation. It can start as an ops verb and later
gain HTTP/MCP wrappers.

```jsonc
{
  "utterance": "keep fixing checkout and run tests",
  "target": {
    "deviceId": "selected-runtime",
    "deviceAlias": "primary box",
    "session": "yaver-codex",
    "runner": "codex",
    "project": "current-app"
  },
  "development": {
    "goal": "make onboarding permissions understandable",
    "evidence": [
      { "kind": "screenshot", "ref": "latest-phone-frame" },
      { "kind": "recording", "ref": "feedback-session-123" },
      { "kind": "logs", "ref": "latest-preview-logs" }
    ],
    "intentClass": "idea-capture|goal|start-coding|queue|autorun|session-turn|analysis",
    "queue": {
      "mode": "enqueue-or-run",
      "priority": "normal",
      "afterFinish": ["load-mobile-container", "ask-deploy"]
    }
  },
  "surface": {
    "id": "watch-apple",
    "class": "watch",
    "interaction": "voice",
    "visualBudget": "glance",
    "ttsBudget": 160,
    "riskPolicy": "watch",
    "replyTo": "watch"
  },
  "mode": "auto"
}
```

`mode: auto` should decide:

1. If the utterance is a vague idea, capture it against the current
   project/session and return a short acknowledgement.
2. If the utterance names goal/focus, create or update the current `/goal`.
3. If the utterance asks to start coding, route through the unified start-coding
   policy and open/create the right coding surface.
4. If the utterance asks for async development work, add a remote runner queue
   item; resolve/start autorun when the work is long-running or explicitly asks
   for autorun.
5. If a target live runner session exists and the utterance sounds like
   continuation, call `/runner/session/turn`.
6. If the utterance is a menu answer and the last target is awaiting choice,
   call `/runner/session/turn` with `choice`.
7. If no live session exists, create a task on the selected runtime.
8. If the utterance maps to an ops verb, use ops.
9. If the target machine is asleep/parked, run wake/resume and return progress.
10. If the request requires a better display, return handoff with a deep link.

### 3.2 Response shape

```jsonc
{
  "ok": true,
  "turnId": "turn_...",
  "target": {
    "deviceId": "selected-runtime",
    "session": "yaver-codex",
    "runner": "codex"
  },
  "state": "queued|working|ready_to_test|ready_to_deploy|done",
  "spoken": "On it.",
  "haptic": "start",
  "glance": {
    "title": "Current app",
    "line": "Running tests on the selected runtime..."
  },
  "queue": {
    "itemId": "rq_...",
    "position": 1
  },
  "testTarget": {
    "kind": "yaver-mobile-container",
    "state": "loading|ready|failed",
    "deviceId": "phone"
  },
  "panel": {
    "kind": "pane",
    "text": "..."
  },
  "awaitingChoice": false,
  "options": [],
  "handoff": null
}
```

When a menu appears:

```jsonc
{
  "state": "awaiting_choice",
  "spoken": "Choose: one, trust this folder. Two, exit.",
  "haptic": "attention",
  "awaitingChoice": true,
  "options": [
    { "key": "1", "label": "Yes, trust this folder" },
    { "key": "2", "label": "No, exit" }
  ]
}
```

When detail is unsafe or unsuitable for the current surface:

```jsonc
{
  "state": "handoff",
  "spoken": "I sent the diff to your phone.",
  "handoff": {
    "targetSurface": "phone",
    "reason": "code_detail",
    "url": "yaver://session/yaver-codex"
  }
}
```

### 3.3 Where it should live first

Start in the agent as a transport-agnostic core:

```text
desktop/agent/runtime_turn.go
desktop/agent/ops_runtime_turn.go
desktop/agent/runtime_turn_test.go
```

Then wire:

- watch phone bridge: call runtime turn instead of direct car task loop when a
  target/session is known
- car: replace car-specific live-session implementation with shared runtime turn
- tvOS: session screen and dictation input call runtime turn
- Mentra/glasses: call runtime turn instead of one-off `/voice/dispatch`
- MCP: expose a thin `runtime_turn` tool/ops wrapper
- web/mobile: use it in runtime dashboards and device pickers

### 3.4 Queue resolution ladder

When an item enters the queue, Yaver should resolve execution like this:

1. **Explicit target wins.** If the user named a device, project, runner,
   session, app, or autorun slot, use that as a hard filter.
2. **Current development context wins next.** If the user is looking at a Yaver
   mobile container, SDK feedback modal, TV preview, or spatial panel, attach
   the queue item to that app/project/runtime.
3. **Awaiting choice wins for short answers.** If any recent turn is waiting on
   a menu and the utterance is "one", "two", "yes", "cancel", etc., route to
   `runner_turn` with `choice`.
4. **Live session continuation.** If a single active runner session owns the
   current project and the utterance says "continue", "retry", "keep going",
   route to `runner_turn` with `text`.
5. **Autorun enqueue.** If the utterance says "autorun", "async", "overnight",
   "keep working", or the item is broad enough to require multiple iterations,
   resolve an existing autorun slot or create one.
6. **Task dispatch.** If the work is a bounded fix/check/analysis, dispatch a
   normal task on the selected runtime.
7. **Idea note only.** If confidence is low and no current project is known,
   preserve the thought and ask the phone for target selection.

The resolver should return what it picked and why:

```jsonc
{
  "resolved": true,
  "executionMode": "autorun",
  "target": {
    "deviceId": "primary-box",
    "project": "yaver",
    "slot": "mobile-dogfood:codex"
  },
  "reason": "current phone preview is Yaver mobile, utterance asked to keep working async, one matching autorun is active"
}
```

This is how "never block" and "do not silently feed the wrong runner" coexist:
auto-pick must be visible, reversible, and cancelable before it is spliced into a
live loop.

### 3.5 Queue state machine

The queue should have product states, not runner-internal states:

| State | Meaning | Surface behavior |
|---|---|---|
| `captured` | thought/evidence saved, no execution target yet | watch haptic + "Captured" |
| `queued` | target resolved, waiting for runner capacity | TV/phone queue row |
| `waking` | runtime is parked/asleep | watch/car "Waking your box" |
| `running` | runner/autorun/task is active | command cards, haptics, TV status |
| `needs_input` | menu/credential/ambiguous target/risky approval | private surface prompt |
| `ready_to_test` | code/check loop finished and preview loaded | watch/car TTS, phone opens app |
| `needs_retest` | user rejected result or SDK feedback says still broken | enqueue follow-up |
| `ready_to_deploy` | tests/build passed and deploy capability exists | ask TestFlight/Play prompt |
| `deploying` | explicit deploy confirmed | phone/web logs, TV summary |
| `done` | accepted or intentionally parked | final summary |
| `failed` | runner/build/reload failed | short spoken error + detail handoff |
| `cancelled` | user cancelled before execution/splice | all surfaces remove/grey row |

`ready_to_test` is a first-class state. A remote runner saying "done" is not
enough for vibing. The product promise is that the user can test the result in
the Yaver mobile container or target preview.

### 3.6 Evidence ingestion contract

Every queue item should be able to carry evidence from multiple surfaces:

```jsonc
{
  "evidence": [
    {
      "kind": "screenshot",
      "sourceSurface": "mobile",
      "uri": "p2p-or-local-ref",
      "capturedAt": "2026-07-22T12:00:00Z",
      "screen": "mobile/app/(tabs)/devices"
    },
    {
      "kind": "recording",
      "sourceSurface": "sdk",
      "uri": "feedback-video-ref",
      "durationMs": 18000,
      "timelineRef": "feedback-timeline-ref"
    },
    {
      "kind": "voice",
      "sourceSurface": "watch",
      "transcript": "the unreachable box card should say the exact failed probe"
    },
    {
      "kind": "command_events",
      "sourceSurface": "agent",
      "stream": "/streams/task_..."
    }
  ]
}
```

Important privacy boundary: evidence that contains code, paths, stdout/stderr,
screen pixels, customer data, or secrets should stay P2P/local unless an
existing Yaver privacy rule explicitly permits syncing metadata. Convex can hold
queue metadata and pointers; it should not become a dumping ground for private
screens or logs.

### 3.7 Deploy gate contract

Deploy is a downstream state, not part of "done":

```text
ready_to_test
  -> user tests in Yaver mobile / SDK target / TV preview
  -> local checks are green
  -> deploy capability report is green
  -> private confirmation
  -> TestFlight / Google Play internal testing
```

Surface copy should be explicit:

```text
"It passes locally and is ready to deploy. Do you want TestFlight, Google Play
internal testing, both, or leave it here?"
```

Rules:

- never deploy automatically
- never infer approval from a car utterance
- prefer watch/phone for confirmation
- TV can show "ready to deploy" but should hand off private confirmation
- failed deploys return one spoken sentence plus full logs on phone/web
- quota/cost limits must surface before starting, not after a failed attempt

## 4. Surface deep dives

### 4.1 Watch: thought capture, approval, and completion readback

The watch is the lowest-friction input surface. The user gets an idea while
walking, in bed, in a meeting room, or away from the desk. They should raise
their wrist and speak naturally:

```text
"Idea: in Yaver mobile, when the box is unreachable, show the exact last probe."
"Add this to the current autorun."
"Make that the goal."
"Keep working on the mobile reload bug."
"Did it finish?"
```

The watch should do four jobs:

1. **Capture without structure.** Convert rough thought into a durable queue
   item or goal note. Do not require project, branch, runner, device, or command
   syntax when current context can infer them.
2. **Start useful work.** If the utterance clearly asks to implement, enqueue or
   start the remote runner. If confidence is low, capture and ask the phone to
   disambiguate.
3. **Approve privately.** High-risk operations should route to watch/phone, not
   shared TV/car. Watch can approve deploy intent, destructive edits, account
   auth, or runner menu choices.
4. **Speak completion.** The watch should say: "Done. It is ready to test in
   Yaver mobile." It should not read code, logs, or diffs.

Important design detail: the watch should not block on long HTTP requests. It
should receive state transitions:

```text
captured -> queued -> running -> needs_choice -> ready_to_test -> ready_to_deploy -> done
```

TTS and haptics should mirror this state. The user does not need to stare at the
screen.

### 4.2 Car: hands-free queueing and safe status

The car surface is not a coding UI. It is a safe voice lane into the same remote
runner queue:

```text
"Add a task: fix the new project screen on Yaver mobile."
"Keep autorun going on the reload bug."
"Run the tests when I get home."
"What happened with the build?"
```

Car must optimize for:

- no code/diff/log readback
- no long summaries
- no visually-required choices
- no surprise deploys
- no payment/account/destructive actions without private confirmation

The best car flow:

```text
1. Driver speaks a development intent.
2. Phone/car voice loop transcribes.
3. Runtime router queues the item.
4. Car says "Added. I'll tell you when it is ready."
5. If the runner needs a menu choice, car says a short choice prompt only if it
   can be answered safely by voice; otherwise it hands off to watch/phone.
6. When done, car says "Done. It is ready to test on your phone."
```

Car is especially valuable for "away from keyboard continuation." The user may
not be able to review details, but they can keep the remote runner queue fed.

### 4.3 tvOS: make it a real development wallboard

tvOS can be much better than a thin status app if it leans into what TV is good
at:

- large shared preview
- runtime wallboard
- queue board
- selected session pane
- app/video/screenshot review
- D-pad approval for non-private actions
- TTS for short result summaries
- QR/deep-link handoff to phone for private detail

tvOS cannot record audio directly and cannot host WebKit, so the right design is
not "terminal on TV." It is:

```text
TV shows:
  - current queue
  - active runner sessions
  - app preview/video/screenshot
  - test status
  - command cards
  - awaiting-choice prompts

TV speaks:
  - short status only

TV hands off:
  - diffs
  - secrets
  - account login
  - private customer data
  - deploy approval if shared-room policy says private confirmation required
```

High-value tvOS improvements:

- **Queue board:** columns for queued/running/needs input/ready to test/done.
- **Preview lane:** show latest Yaver mobile container frame, SDK recording, or
  app preview stream.
- **Session lane:** show `runner_sessions` and selected pane tail from
  `/runner/session/turn`.
- **Choice lane:** D-pad selectable options when the runner is awaiting choice.
- **Test lane:** latest checks, failing test names, build status.
- **Deploy lane:** "ready to deploy" state with QR/private handoff.
- **Dogfood lane:** when the project is Yaver itself, show the current Yaver app
  build/reload/test status.

tvOS can be the room-scale "mission control" for vibing without pretending to be
a full IDE.

### 4.4 AR/VR and glasses: private spatial cockpit

Spatial surfaces should show multiple development objects at once:

- remote runtimes
- queue items
- app preview surfaces
- command cards
- logs summarized into panels
- screenshots/recordings pinned beside current build
- test/deploy readiness

The natural interaction is:

```text
voice intent + gaze selection + phone/watch approval
```

Example:

```text
User looks at the Yaver mobile preview panel:
"make the disconnected state clearer"
  -> runtime queue item attaches current panel screenshot
  -> runner fixes Yaver mobile
  -> panel hot-reloads
  -> glasses say "Ready to test"
```

AR/VR is the best place to compare before/after screenshots and inspect multiple
remote runtimes, but it should still use the same queue and runtime-turn model.

### 4.5 Mobile app container: the canonical test target

For development vibing, the mobile app container is not just a viewer. It is the
canonical "test it now" surface.

The desired completion state is:

```text
runner finished code changes
  -> built/reloaded target into Yaver mobile container
  -> verified the app loaded
  -> user gets watch/car/TV TTS: "Done. You can test it in Yaver mobile."
  -> phone opens the exact screen/app/build
```

This is where Yaver beats plain remote coding. The runtime can be anywhere, but
the user tests on the real phone/tablet container connected to that runtime.

The user-facing flow should be:

```text
1. User connects Yaver mobile to the selected runtime.
2. Watch/car/SDK/phone/TV creates a queue item.
3. Runtime runner edits and builds.
4. Runtime exposes preview/reload artifact.
5. Yaver mobile receives the update in its container.
6. Watch/car/TV says "Ready to test."
7. User tests in the phone container.
8. User says "looks good", "still broken", or "deploy internal".
```

The phone should understand three test-result inputs:

- **Accept:** mark queue item done; optionally ask deploy.
- **Reject:** attach fresh screenshot/video/logs and enqueue a follow-up.
- **Deploy:** run explicit deploy gate for TestFlight/Google Play internal.

The phrase "connecting this PC" should be generic in the UI: the PC is a
selected runtime. It might be this Mac, a Windows tower, a Linux box, a cloud
workspace, or a remote mobile farm. The test surface is still the Yaver mobile
container.

### 4.6 SDK surfaces: evidence capture and hot reload in third-party apps

SDK feedback should be treated as a first-class evidence source for the queue:

- screenshot/video/audio from inside the app
- UI event trace
- device info
- app/bundle version
- crash/log context
- user narration
- current screen route if available

The queue item should preserve that evidence and feed it to the runner. When the
runner finishes, SDK-hosted apps should receive:

- hot reload or bundle reload if available
- "fix running" status
- "ready to retest" status
- optional command cards

The same watch/car/TV/spatial surfaces can narrate or display SDK progress.

### 4.7 Dogfooding Yaver from Yaver

Yaver should dogfood this loop against itself. The user should be able to use
Yaver mobile, Yaver TV, Yaver watch, and Yaver SDK feedback to develop Yaver.

The recursive product loop:

```text
Yaver mobile has a bug / UX gap
  -> user records screen or speaks to watch
  -> evidence attaches to a remote runner queue item
  -> runner edits yaver.io
  -> runner runs focused checks
  -> runner rebuilds/reloads Yaver mobile container
  -> user tests the fixed Yaver inside Yaver mobile
  -> Yaver asks whether to deploy TestFlight / Google Play internal
```

This matters because Yaver's own product is exactly the hard case:

- many devices
- native mobile surfaces
- live reload
- remote runners
- auth state
- relay/direct connectivity
- SDK feedback
- TestFlight/Play deploy gates
- concurrent autoruns
- dirty working trees

If the multi-device queue works for Yaver itself, it is much more likely to work
for customer apps.

Dogfood-specific requirements:

- Every Yaver surface should identify itself in feedback: phone/watch/car/TV/AR.
- A Yaver bug report should include the connected runtime/device id, active
  project, selected runner, relay/direct route, app version, and reload state.
- The runner should be able to open the exact Yaver mobile screen or reproduce
  from a recording.
- The mobile container should show "current dogfood build" versus "released app"
  to avoid confusing the user.
- Deploy prompts must be explicit: "Do you want me to deploy this Yaver build to
  TestFlight / Google Play internal testing?"
- Yaver should never silently deploy itself just because local dogfood passed.

Dogfood scenarios to force the design to be real:

| Scenario | Input | Runtime work | Test output |
|---|---|---|---|
| Watch idea | watch transcript | queue item -> edit Yaver mobile -> checks | watch TTS + Yaver mobile ready |
| SDK bug report | screen recording + narration | analyze recording -> fix -> reload | SDK overlay says ready to retest |
| TV QA | TV displays preview, user marks issue by phone/watch | attach screenshot -> runner fix | TV preview updates |
| Car continuation | car voice "keep going on reload bug" | queue/autorun continues | car says ready; phone opens detail |
| AR compare | gaze at before/after panel and reject | attach selected panel evidence -> follow-up | spatial panel updates |
| Deploy prompt | user accepts dogfood result | deploy gate -> TestFlight/Play internal | phone/web logs, watch summary |

Dogfooding should produce artifacts a runner can use:

- queue item id
- current Yaver app version/build number
- branch/worktree/runtime id
- connected phone/tablet id
- screenshot/video/log bundle
- exact screen route if available
- reload method and artifact id
- focused check command/results
- user acceptance/rejection note

The rule for Yaver-on-Yaver is simple: if a user can report a Yaver mobile issue
by talking to the watch, go away, and later hear "done, test it in Yaver", then
the multi-device loop is real.

## 5. STT/TTS architecture

### 5.1 STT should happen at the nearest competent device

The repo currently supports multiple patterns:

- watchOS system dictation: transcript only
- Wear OS `RecognizerIntent`: transcript only
- phone app STT: local/on-device or configured provider
- agent `/voice/stream`: raw PCM to OpenAI/Deepgram/AssemblyAI/local whisper
- Mentra: platform transcription
- tvOS: system dictation only through focused text field

Yaver should not force all surfaces through one STT provider. It should accept
both:

1. **Transcript-first turn**

   ```jsonc
   { "input": { "kind": "transcript", "text": "run the tests" } }
   ```

2. **Audio stream turn**

   ```jsonc
   { "input": { "kind": "audio", "format": "pcm16le/16khz/mono" } }
   ```

Decision rule:

- watch system dictation is best for short commands
- Wear recognizer is best for short commands
- phone local STT is best when privacy/offline matters
- Deepgram/OpenAI streaming is best for continuous voice
- agent local whisper is best when the surface is weak but the runtime is strong
- tvOS uses dictation text fields, not mic APIs

### 5.2 TTS should happen at the device that will speak

TTS is output, not a runner capability. The device closest to the user's ears
should speak when possible:

- Apple Watch: `AVSpeechSynthesizer`
- Wear OS: Android `TextToSpeech` should be added
- phone/car: platform audio route, Bluetooth/car audio
- tvOS: `AVSpeechSynthesizer`
- Mentra/glasses: platform audio manager when speaker exists
- agent: fallback local TTS only for desktop/CLI

The agent can still synthesize TTS frames on `/voice/stream`, but for watch,
car, TV, and glasses the better default is:

```text
agent returns short `spoken` text
surface performs native TTS
```

That avoids shipping audio across relay and lets devices honor current audio
route, volume, accessibility settings, and platform voice.

### 5.3 TTS budgets must be hard contracts

Current car readback cap is 200 chars. Watch should be even tighter, around
120-160 chars. TV and spatial can speak one sentence while showing a larger
panel.

Recommended budgets:

| Surface | Spoken budget | Visual budget |
|---|---:|---|
| watch | 120-160 chars | one line plus confirm |
| car | 160-200 chars | none / notification |
| glasses HUD | 160-280 chars | 1-3 lines |
| TV | 200-300 chars | pane/card |
| phone | 300+ chars | detail screen |
| web/desktop | no TTS default | full |

Prompt wrappers and summarizers should treat the budget as a hard ceiling,
not a hint.

## 6. Rendering and UI feedback model

### 6.1 Output should be structured once, adapted many ways

A runner/task/session should produce a small output envelope:

```jsonc
{
  "spoken": "Done. Tests pass.",
  "glance": {
    "status": "completed",
    "title": "Checkout fix",
    "line": "Tests pass on the selected runtime."
  },
  "panel": {
    "kind": "session_pane",
    "text": "plain pane tail..."
  },
  "stream": {
    "taskId": "task_...",
    "sse": "/streams/task_..."
  },
  "actions": [
    { "id": "open_phone", "label": "Open on phone", "risk": "low" },
    { "id": "approve_deploy", "label": "Approve deploy", "risk": "high" }
  ]
}
```

Surface adapters decide:

- watch: speak `spoken`, show `glance.line`, haptic by status
- car: speak `spoken`, maybe notification action for confirm
- TV: show `panel`, speak `spoken`, D-pad actions
- AR/VR: place `panel` in spatial card, speak `spoken`, gaze actions
- phone: open detail screen, show stream/logs/diff
- SDK overlay: show command card and hot reload state

### 6.2 Command events should feed all panels

`desktop/agent/command_events.go` already defines structured command events:

- `command_start`
- `command_output`
- `command_end`

Mobile and web mirror this. This should be the event source for TV, spatial,
and SDK command cards too.

The privacy rule in that file is critical: command/cwd/stdout/stderr must stay
P2P over task SSE and must not go to Convex.

### 6.3 Haptics are a real feedback channel

On watch, haptic output is not decorative. It is the equivalent of a status
LED:

- light tap: accepted
- repeated gentle taps: working/wake progress
- attention tap: confirmation needed
- success: completed
- failure: error

Watch TTS plus haptics means the user can issue a remote development task while
walking and know whether it started, needs a choice, is ready to test, or
finished.

### 6.4 TV and spatial are shared/contextual displays

TV is often visible to other people. Spatial glasses are private. They should
use different risk policies:

- `shared-tv`: hide secrets, avoid customer data by default, no private tokens,
  explicit confirmation on writes
- `spatial`: private display, can show richer detail, still gate destructive
  operations
- `watch`: private but tiny, approve/deny only
- `driving`: no code/logs/diffs, voice-only high-level summaries

### 6.5 Surface contract table

Each surface should implement the same state contract with different rendering:

| State | Watch | Car | tvOS | AR/VR | Phone/mobile container | SDK overlay |
|---|---|---|---|---|---|---|
| `captured` | haptic + "Captured" | "Added" | new queue row | new spatial card | queue row | report id |
| `queued` | optional glance | optional TTS | queue column | queue card | queue/detail | pending badge |
| `waking` | progress haptic | short TTS | runtime card | runtime card | wake progress | none |
| `running` | haptic only | no chatter unless asked | command cards | command cards | logs/diff/preview | fix running |
| `needs_input` | private prompt | hand off unless voice-safe | D-pad if non-private | gaze/phone approve | full prompt | modal/status |
| `ready_to_test` | TTS + success haptic | TTS | preview highlighted | preview highlighted | open container | ready to retest |
| `ready_to_deploy` | private approve | hand off | QR/private handoff | phone/watch approve | deploy sheet | no auto-deploy |
| `done` | final TTS | final TTS | done row | done card | accepted state | resolved |
| `failed` | short error | short error | error row | error card | full detail | error detail |

This table should drive implementation tests. A queue state that only appears on
one surface is not a real product state yet.

## 7. Diagnostics and observability

The multi-device loop will fail in confusing ways unless diagnostics are built
into the product from the start. The required probes should answer:

- Can this surface submit a turn?
- Did STT happen locally, on phone, in platform dictation, or on the agent?
- Which runtime did the router choose, and why?
- Did the item enter the queue?
- Is the queue drained by task dispatch, live session turn, or autorun?
- If autorun, which slot/tmux session/workdir owns it?
- Is the runner confirmed alive, not just a stale tmux pane?
- Is the runtime reachable by LAN, mesh, relay, or wake path?
- Did evidence upload/attach succeed?
- Did the mobile container receive the reload artifact?
- Did the app actually load after reload?
- Is deploy capability green, and which credential/check is blocking it?
- Which surface will receive final TTS/haptic/notification?

Product surfaces should show the same concise diagnosis:

```text
Queued on primary box because it owns the current Yaver mobile preview.
Running via autorun slot mobile-dogfood:codex.
Ready to test: Hermes bundle loaded on Kivan's iPhone.
Deploy blocked: TestFlight signing keychain is locked.
```

This follows the repo incident rule: if a failure costs a debugging session, the
next build needs a probe where the agent/mobile/TV/watch already looks. Likely
implementation points:

- agent doctor probe for queue drain and runner-session liveness
- ops verb for `runtime_turn_status`
- mobile diagnostics panel for current queue item and reload artifact
- tvOS wallboard reason strings
- watch/car spoken blocker strings with phone handoff
- deploy preflight reused before any TestFlight/Play prompt

## 8. Multi-device choreography

### 8.1 "I get an idea on watch, test it on phone, monitor on TV"

```text
1. Watch: "Idea: make the disconnected Yaver screen say what probe failed."
2. Phone bridge attaches current Yaver project/app context and latest mobile
   screen evidence.
3. Runtime router creates a remote runner queue item.
4. Watch says "Added to the queue."
5. Runner starts, edits Yaver, and runs focused checks.
6. TV wallboard shows queued -> running -> ready to test.
7. Runner hits a menu: "Trust this folder?"
8. Watch gets confirm-needed and speaks options.
9. User says "one."
10. Router sends choice=1 with no Enter.
11. Runner reloads Yaver mobile container.
12. Watch says "Done. You can test it in Yaver mobile."
13. Phone opens the updated screen, with diff/log/detail one tap away.
```

### 8.2 "Car adds work, watch approves deploy, phone shows detail"

```text
1. Car: "Add a task: fix the app startup flicker and run checks."
2. Router adds a remote runner queue item under driving-safe policy.
3. Car TTS: "Added to the queue."
4. Runner finishes and loads the app into Yaver mobile.
5. Car hears: "Done. It is ready to test on your phone."
6. If checks pass, Yaver asks on watch/phone: "Deploy to internal testing?"
7. User approves or cancels privately.
8. Phone receives detailed deploy log notification.
```

### 8.3 "AR glasses as private runtime cockpit"

```text
1. User says: "Show my Yaver floor."
2. Phone renders spatial panels for devices, sessions, tasks, SDK previews.
3. Selected runtime panel shows live runner status.
4. User gazes at the session card and says "continue with option two."
5. Runtime turn sends choice=2 to that session.
6. Glasses speak short result and update panel.
```

## 9. Required data model

### 9.1 Device/session affinity

Yaver needs to remember:

```jsonc
{
  "userId": "...",
  "deviceId": "selected-runtime",
  "aliases": ["primary box", "mac mini", "office desktop", "cloud workspace"],
  "platform": "darwin|windows|linux|cloud",
  "runners": [
    {
      "runner": "codex",
      "session": "yaver-codex",
      "project": "current-app",
      "state": "idle|working|awaiting_choice|auth_required|offline",
      "lastPaneSummary": "..."
    }
  ],
  "routes": {
    "lan": true,
    "mesh": true,
    "relay": true,
    "wake": false
  },
  "defaultReplySurface": "watch",
  "lastIngressSurface": "watch-apple"
}
```

Private details should stay local/P2P where required by Yaver's privacy
contract. Hosted coordination can carry identity/discovery/status, not code,
task data, stdout, secrets, or customer IPs.

### 9.2 Turn memory

The router needs short-lived memory:

- last target device
- last target session
- whether it is awaiting choice
- option labels
- originating surface
- reply surface
- handoff deep link

This lets "answer two" from a watch work after the previous turn came from a
car or TV.

## 10. Security and privacy invariants

1. Surface identity is never auth.
2. Relay does not authorize cross-tenant access.
3. Device/session routing must stay owner/access-graph scoped.
4. A compromised relay cannot inject turns into a user's box.
5. Watch/car/TV cannot gain broader permissions than the phone/user token grants.
6. Shared TV should not render private data without explicit user action.
7. Command events and stdout/stderr remain P2P, not Convex.
8. Risk policy must travel with the turn.
9. Destructive operations require explicit confirmation on a private surface
   unless the user has configured a narrower trusted automation.
10. Menus/options must be handled via `choice`, never prompt text.

## 11. Implementation roadmap

### Phase 1: Document and unify contracts

- Add this analysis.
- Audit current watch/car/tv/glasses/session contracts.
- Define shared `RuntimeTurnRequest` / `RuntimeTurnResponse` in Go and TS.
- Keep docs updated when code disagrees.

### Phase 2: Agent core

- Implement `executeRuntimeTurn` in Go.
- Wrap existing `executeRunnerSessionTurn`.
- Add task-dispatch fallback.
- Add ops-verb dispatch path for known constrained-surface intents.
- Add target device/session resolution.
- Add hard output budgets.
- Add tests for:
  - watch idea -> queue item for selected runtime/current project
  - menu conflict -> awaiting choice
  - choice with no menu rejected
  - driving policy suppresses code
  - shared TV redacts private detail
  - offline/wake path returns progress instead of failure

### Phase 3: Mobile bridge and mobile-container testing

- Update `watchBridge.ts` to call runtime turn.
- Reuse the same bridge for Wear OS.
- Promote `carSessionTurn.ts` logic into shared runtime-turn client code.
- Preserve native watch transport as a thin adapter.
- Add Wear OS TTS.
- On runner completion, load/reload the result into the Yaver mobile container
  and return `ready_to_test`.

### Phase 4: tvOS and AR/glasses

- Update tvOS session/dictation flows to call runtime turn.
- Use TV as panel/status output for turns started elsewhere.
- Update Mentra miniapp to call runtime turn where possible.
- For spatial, render runtime panels from the structured output envelope.

### Phase 5: SDK feedback synergy and Yaver dogfood

- Let SDK full-interactive mode emit a surface identity and preferred reply
  surface.
- Send agent command events to watch/TV/spatial status surfaces.
- Add "fix is running on <device>" and "reload delivered" feedback that can be
  spoken on watch/car and displayed in SDK overlay.
- Make Yaver's own mobile/TV/watch surfaces emit dogfood evidence into the same
  queue.

## 12. Near-term high-value slices

### Slice A: Watch idea to remote runner queue to mobile test

User story:

> From my watch, I can tell Yaver an app idea, Yaver queues it on the selected
> runtime, loads the result into Yaver mobile, and my watch says it is ready to
> test.

Work:

- phone resolves watch utterance to current project/runtime
- create remote runner queue item
- run or enqueue implementation depending on runner availability
- load/reload Yaver mobile container when done
- return `ready_to_test` and `spoken` to watch
- add native TTS on watchOS if not already wired into the reply path
- add Wear OS TTS
- tests for `awaitingChoice`

Concrete first implementation path:

| Area | Files to touch first | Purpose |
|---|---|---|
| shared TS contract | `mobile/src/lib/runtimeSurfaceTypes.ts` | add queue/runtime-turn request and response types |
| watch routing | `mobile/src/lib/watchBridge.ts`, `mobile/src/lib/watchPrompt.ts` | classify idea/goal/queue/session-turn and call queue/runtime client |
| runtime client | `mobile/src/lib/runtimeSurfaceClient.ts` | add `runtimeTurn` / queue-status ops calls |
| agent core | `desktop/agent/runtime_turn.go` | implement transport-agnostic router core |
| ops wrapper | `desktop/agent/ops_runtime_turn.go` | expose remote-capable ops verb |
| runner bridge | `desktop/agent/ops_runner_turn.go`, `desktop/agent/runner_session_turn.go` | reuse existing session-turn safety, do not fork it |
| queue store | new `desktop/agent/runtime_queue.go` or autorun-topic equivalent | durable queue metadata and state transitions |
| mobile test handoff | existing mobile reload/build client paths | mark `ready_to_test` only after reload/load succeeds |
| tests | Go + TS targeted tests | prove routing, queue state, TTS budget, awaiting-choice |

The smallest useful version does not need full autorun topic splicing. It can:

```text
watch transcript
  -> runtime_turn
  -> create queue item
  -> if one live runner session owns the current project, use runner_turn
  -> else create a normal task
  -> when terminal, return ready_to_test if reload/load succeeded, else done/failed
```

Then a second slice can connect queue items to real autorun/topic enqueue.

### Slice B: Cross-surface awaiting-choice

User story:

> If a runner asks a menu question, any private surface can answer it.

Work:

- persist short-lived turn memory
- watch/phone can answer car-started turn
- phone/TV can show full option labels
- never assume option 1 means yes

### Slice C: TV runtime wallboard

User story:

> My TV shows queued/running/ready-to-test development work across runtimes while
> my watch and phone handle private approvals.

Work:

- subscribe/poll runtime turn state
- render task/session cards
- render pane tail for selected session
- speak only summary
- no secrets on shared TV

### Slice D: SDK feedback to all surfaces

User story:

> While testing an app with the Yaver SDK, I can report a bug by voice, the
> remote runner fixes it, the app hot reloads, and my watch tells me it is done.

Work:

- SDK includes surface/reply preference
- agent emits structured progress
- watch/TV/spatial receive progress
- phone remains detail and approval surface

### Slice E: Internal testing deploy prompt

User story:

> After a queue item passes checks and I test it in Yaver mobile, Yaver asks
> whether to deploy to TestFlight or Google Play internal testing.

Work:

- detect deploy capability for the target project/platform
- show deploy readiness only after local checks/build/reload are green
- ask explicit confirmation on watch/phone
- never deploy automatically
- report deploy logs to phone/web/TV, with only summary on watch/car

### Slice F: Yaver dogfood queue

User story:

> While using Yaver mobile, I can report a Yaver issue by watch or screen
> recording, the remote runner fixes Yaver, Yaver reloads itself into the mobile
> container, and I can approve internal deploy from phone/watch.

Work:

- tag queue items with `project=yaver` and source surface
- attach Yaver mobile route/version/reload state
- run focused Yaver checks before `ready_to_test`
- make the mobile app show "dogfood build" versus "released app"
- make TV show dogfood queue and current build status
- add deploy prompt only after explicit user acceptance

This is the best first dogfood because it exercises the whole stack: watch voice,
mobile evidence, remote runner, tests, reload, TTS, TV wallboard, and deploy gate.

## 13. Open questions

1. Where should target-device aliases live: local agent config, Convex device
   profile, or both?
2. Should watch store any target preference, or should phone always resolve it?
   Default answer: phone resolves; standalone watch only stores minimal LAN
   target/token by explicit opt-in.
3. How should physical machine wake work across Mac/Windows/Linux? Wake-on-LAN,
   mesh heartbeat, cloud fallback, or user-configured script?
4. What should be the default when a user says "my box" and multiple runtimes
   exist? Default answer: ask a one-time private-surface disambiguation and
   remember.
5. How much session state can Convex store without violating the privacy
   contract? Likely only metadata, not pane text or command output.
6. Should runtime turn be HTTP first or ops first? Default answer: Go core,
   ops wrapper first, HTTP wrapper where native clients need it.

## 14. Strategic conclusion

The deepest opportunity is not "Yaver has apps on many devices." It is:

```text
Yaver turns the user's personal device cloud into one cooperative runtime.
```

The watch is the private voice button. The car is hands-free intent and
one-sentence readback. The TV is the room-scale wallboard. AR/VR is the private
spatial cockpit. The phone is the bridge, approval device, and detailed control
surface. Mac/Windows/Linux/cloud boxes are the execution layer. The remote
runner queue is the development spine. The SDK is the in-app observation and
hot-reload loop. Yaver mobile is the canonical "test it now" container.

The codebase already contains most primitives. The next step is to stop wiring
each surface directly to a different task path and introduce one runtime turn
router that understands target device, live session, surface budget, risk
policy, and reply channel.

## 15. Implemented first slice in this branch

This branch implements the first vertical slice of that router. It is not the
full roadmap yet; it is the shared contract that other surfaces can now build
against.

Implemented:

- Agent ops verbs:
  - `runtime_turn`
  - `runtime_turn_status`
  - `runtime_turns`
- Simple usage contract:
  - minimal request: `{ "text": "fix this", "run": true }`
  - `text` and `prompt` are accepted aliases for `utterance`
  - `run: true` maps to queue mode `run`
  - `queue: true` maps to queue mode `enqueue-or-run`
  - status accepts either `itemId` or `turnId`
- Agent in-memory runtime-turn queue:
  - `captured`
  - `queued`
  - `running`
  - `needs_input`
  - `ready_to_test`
  - `ready_to_deploy`
  - `done`
  - `failed`
  - `cancelled`
- Agent routing:
  - idea capture stays captured unless the request asks to enqueue/run
  - live runner session turns route through the existing session-turn path
  - development work creates a normal task with surface-aware viewport metadata
  - completed task-backed turns map to `ready_to_test`
  - deploy is never automatic; the returned state can ask for confirmation
- Shared mobile runtime surface client:
  - typed `RuntimeTurnRequest` / `RuntimeTurnResponse`
  - `runtimeTurn`
  - `turn`
  - `runtimeTurnStatus`
  - `runtimeTurns`
  - `waitForRuntimeTurnDone`
- Watch:
  - watch utterances use `runtime_turn` when the host injects it
  - idea utterances default to queue/capture semantics, not blind edits
  - `ready_to_test` / `needs_input` / `failed` map to watch-safe messages
- Car:
  - the phone car voice surface sends normal development turns to `runtime_turn`
  - Android Auto MessagingStyle replies use `runtime_turn`
  - existing risky-command confirmation still gates deploy/push/delete actions
  - car readback remains one sentence and never reads queue/task detail aloud
  - older agents fall back to the previous task/session paths
- tvOS:
  - `SessionClient` tries `runtime_turn` first for prompt and choice turns
  - it falls back to the existing direct `/runner/session/turn` endpoint
  - panel text still renders on TV and spoken output stays summarized

Still future work:

- durable queue storage across agent restarts
- multi-item runtime queue listing and filtering
- background push/TTS notification when a watched turn finishes
- actual mobile-container reload verification before claiming test success
- explicit deploy-preflight and TestFlight / Google Play internal prompt wiring
- AR/VR specific panels beyond the shared surface metadata
- SDK-origin evidence bundles wired directly into `runtime_turn`
