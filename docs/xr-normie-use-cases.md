# Yaver in AR/VR — use cases for people who don't code

**Status: ideas only. No implementation.** Companion to `docs/xr-spatial-design.md`
(also design-only). Every "already ships" note below was grepped against the code
on 2026-07-17 — re-verify before building, per CLAUDE.md.

## The premise

A normie will not put on a headset to read a diff. Everything here obeys four rules:

1. **No keyboard.** Voice, gaze, pinch, point. If it needs typing it belongs on the desktop.
2. **No reading.** If the payoff is text, it's a worse phone app.
3. **Short.** Headset comfort is ~15 minutes. Design for 90 seconds.
4. **The payoff must be visual.** XR earns its cost only when the thing being
   understood is *spatial* or *temporal* — a layout, a device wall, a timeline,
   a machine in front of you. Otherwise ship it flat.

The wedge isn't "code in VR." It's that Yaver's agent works for hours while
nobody watches, and the output of that work is **visual and reviewable** — an app
screen, a diff of a UI, a build that passed or didn't. That's exactly the payload
XR is good at and a terminal is bad at.

---

## 1. Watching the agent's work (the strongest normie category)

Autorun runs overnight. A non-coder cannot review 40 commits. They can watch a movie.

### 1.1 The Recap — an auto-cut summary video of last night's build
The single highest-value idea in this document.

Sit in a virtual room, one big screen, 60–90 seconds: what the agent set out to
do, the app UI morphing before→after, what broke, what shipped, what it wants
permission for. TTS narration. Ends with one question and two big buttons: *ship it*
/ *no*.

Seams that already exist: `screenlog_frames` + `screenlog_export` (frame capture),
`vibe_preview_clip_record` / `clip_start` / `clip_stop` (clip recording),
`morning_latest` / `morning_show` (the overnight digest already exists as text),
`say` / `voice_speak` (TTS). The Recap is largely a **cut-and-narrate pass over
data Yaver already records.** It does not need XR to exist — but XR is its best
theater, and `cast_start` means the same reel plays on the existing tvOS surface.

### 1.2 The Failure Reel
Only the 90 seconds where it got stuck. Everything else is noise. This is the
version a busy person actually watches.

### 1.3 Ask-the-movie
Gaze at the screen, say "wait, why did it do that?" — the reel pauses and the
agent narrates that specific moment from the run log. Turns a passive video into
the review meeting.

### 1.4 Netflix row of recaps
One row per project. "Continue watching." A normie's mental model of their own
company as a show they're behind on.

### 1.5 Watch it live, ambient
The agent working, on a wall, while you do something else. `DataPane3D` +
`fleetStats.ts` already billboard fleet state; this is the same idea pointed at a
single run. The value is peripheral awareness, not attention.

---

## 2. Vibe peer coding (not pair programming)

Pair programming is two people and one keyboard. This is two people and **no**
keyboard — both talking at the same runner, pointing at the same floating app.

### 2.1 Point-and-say
Gaze/pinch a button on the live app plane → that becomes a spatial anchor → your
spoken comment attaches to it → it becomes a feedback item → the runner picks it up.
"This should be blue and bouncier." Nobody described a file path.

Seams: `AppScreenPlane3D` already renders the live guest app from
`/vibing/preview/*`; the feedback SDK (`sdk/feedback/`, `feedback_create`,
`feedback_fix`) already turns a complaint into agent work. Point-and-say is
**the feedback SDK with a 3D cursor.**

### 2.2 The designer chair
Your designer friend in a Quest drags the button 20px left. That's a real edit —
the mini-Figma direct-manipulation work is the mechanism, the headset is just the
hands.

### 2.3 The non-technical co-founder seat
They join `feedback-only` — a guest scope that **already exists** (`full` /
`feedback-only` / `deploy` in `backend/convex/guests.ts`). They can see everything
and touch nothing. This is the single cheapest multiplayer story here: the
permission model is already built, only the room is missing.

### 2.4 The agent has a chair
`VoiceOrb3D` already gives the agent a *position* and positional-audio TTS that
comes *from* that position. Push it one step: the agent is a participant with a
seat at the table, not a service you invoke. This is a very small change with a
disproportionate effect on how non-coders relate to it.

### 2.5 Async spatial stickies
Leave a voice note pinned to a spot on the app. Your teammate walks in at 9am,
sees the orb, taps it, hears you. Async peer review with zero writing.

### 2.6 Watch-party deploy
Everyone in the room, one person pulls the lever, everyone watches it go green.
Ceremony is underrated — and it matches the CLAUDE.md "one deploy per converged
change" rule better than a CI button does, because a deploy that *feels*
consequential doesn't get spammed.

### 2.7 Teach by spectating
A senior in your room, seeing your panes, never touching your keyboard. Also:
interviews, where the candidate vibe-builds and you watch how they think.

---

## 3. Testing — the thing normies genuinely cannot do today

### 3.1 The device wall
Six phones side by side — iPhone SE, Pro Max, tablet, small Android — all live,
all the same build. A non-coder sees "it's broken on the small one" in one second.
They would never have thought to open a simulator matrix. **This is the clearest
case where the spatial version teaches something the flat version doesn't**, because
the insight is literally peripheral vision.

### 3.2 Grandma's phone
Same plane, but: 200% font, colorblind filter, one-handed reach zone drawn as a
heat overlay. Accessibility testing that requires no knowledge of accessibility.

### 3.3 Poke it with your finger
The app at arm's length, hand-tracked. Not a simulator — a thing.

### 3.4 On your actual kitchen table
Passthrough AR: place the app on the real table, use it like a real app, shake to
file feedback (the shake→feedback path already ships on mobile). **Blocked:** needs
`immersive-ar` + camera; neither exists in the codebase today.

### 3.5 Time-travel
Scrub the app back three commits, side by side with now. The UI as a timeline you
can drag. Normies understand "it used to look like this" instantly.

### 3.6 Watch a real user struggle
Replay a recorded session (screenlog / blackbox) as a floating plane; the agent
annotates where they hesitated. The most valuable 40 seconds in any product.

### 3.7 Slow-network twin
Two planes, one throttled to 3G. Nobody tests this. Everybody should.

### 3.8 The bug has a location
A crash gets a physical marker in your room. It stays there until it's fixed. You
walk past it. Guilt as a project management tool.

---

## 4. Ambient ops for people who don't read dashboards

- **The wall.** Revenue, users, errors, what the agent is doing right now.
  `DataPane3D` + `fleetStats.ts` already do a version of this.
- **Morning standup, seated.** `morning_latest` / `standup` verbs exist; the room
  is the missing part. Coffee, wall, overnight.
- **The cost gauge.** Hetzner hours, Cloudflare, TestFlight uploads left today, as
  a physical dial you can see from across the room. CLAUDE.md says cost-awareness
  is a product requirement, not a house rule — a gauge is its normie form, and
  "TestFlight uploads remaining today" is the one number that has no rollback.
- **The deploy lever.** Physical, deliberate, one pull.
- **The build furnace.** A fire that burns while builds run. You know the state of
  your company by whether the room is warm.

---

## 5. Real-world AR (the Talos-adjacent half)

All of these need the camera Yaver doesn't have yet. Listed because the *backends*
already exist and this is where AR stops being a demo.

- **Point glasses at a machine → its live Modbus state floats next to it.** The
  edge Pi RS485 work and `machine_modbus_testing` already produce the data.
- **Wire-harness formboard overlay.** Which wire goes where, drawn on the real
  board. The jig/formboard work exists; this is its natural output device, and the
  user is a factory-floor normie who will never open a terminal.
- **Point at your Mac mini → agent status.** Point at the router → the LAN.
  Physical device inventory as looking at things.
- **Breadboard overlay** from the circuit cell (`circuit_plot`, `desktop/agent/circuit/`).
- **Robot arm teleop by hand tracking**, over the existing single arm layer.

---

## 6. Onboarding — the zero-to-app path

- **Voice-only first project.** "Make me an app that tracks my plants," spoken, in
  a headset. `phone_project_create` + templates already exist; the plane pops up
  with the app on it. No install: Add-to-Home already ships for Quest/AVP.
- **Learn by watching it build.** The agent narrates while it works. Normies learn
  by spectating, not by tutorials.

---

## What to build first, and why

Ranked by (normie value) ÷ (distance from shipping code):

1. **The Recap / Failure Reel (§1.1, §1.2).** Highest value, and mostly a
   cut-and-narrate pass over frames Yaver already records. Ships flat first (phone,
   tvOS via `cast_start`); XR is the theater, not the prerequisite.
2. **Point-and-say (§2.1).** The feedback SDK plus a 3D cursor. Both halves exist.
3. **The `feedback-only` guest room (§2.3).** The permission model is already built.
4. **The device wall (§3.1).** `AppScreenPlane3D` already renders one plane. This
   is N planes.
5. **The agent gets a chair (§2.4).** Tiny change, large effect on the relationship.

**Two blockers gate the rest:**

- **Voice transcription in the browser is broken.** `web/app/spatial/useAgentBridge.ts:359`
  — MediaRecorder emits webm/opus, the backend's Deepgram config expects linear16
  16kHz, and the comment admits the WS "gracefully drops audio frames the backend
  can't parse." So `/spatial` gets TTS and result loops but **you cannot actually
  talk to it.** Every voice-first idea above is dead until the opus→PCM bridge lands.
  Nothing in this document matters more than this one fix.
- **There is no camera.** No `getUserMedia({video})`, no passthrough, no
  `immersive-ar` anywhere in the tree. All of §5 and §3.4 are behind it.
