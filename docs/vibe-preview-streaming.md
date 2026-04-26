# Vibe Preview — Live Streaming of Web UI from Phone

> **Status: design doc, not yet implemented.** This document describes a feature
> being added to Yaver, not the current state. Treat any file paths cited as
> *targets for new code*; verify against actual files before writing or
> referencing them. The general rule from `CLAUDE.md` applies: when this doc
> and the code disagree, the code wins.

## 1. Problem

The user is vibe-coding a monorepo from their phone. The remote agent runs the
codebase, drives the AI runner, and hosts the dev servers. For native targets
(iOS/Android), the existing pipeline already works — the phone is the canvas,
Hermes bytecode is pushed, the developer sees the app running on their own
device. For backend changes the developer doesn't *need* a visual; logs and
test output suffice.

The gap is **web UI changes**. When the agent edits a Next.js / Vite / Astro
project, the rendered output lives on the dev machine — behind the relay,
typically on `127.0.0.1:3000`. The developer can't see it. They can ask the
agent to describe what it built, but that's not the same as watching the page
re-render after a Tailwind tweak. Web frameworks are visual; vibing without
visuals is debugging blind.

The mobile app already has a `DevPreview` modal that opens a WebView against
`/dev/*` proxy. That works *while the developer is actively interacting* — but
during long autodev runs, the developer wants to glance, see what changed, and
get back to whatever else they're doing. WebView is the wrong UX for that:
it's heavyweight, it captures gestures, and it's only useful when the framework
fully renders against the phone's user agent.

**This feature ships three complementary modes:**

| Mode | What | When |
|------|------|------|
| **Live frame stream** | Continuous JPEG/PNG screenshots of the rendered dev server, at adaptive FPS. Like a low-bitrate VNC. | Developer wants to watch a web tweak land in real time. |
| **Summary stream** | After each autodev kick or `/dev/reload`, capture before+after frames; let Claude describe the visual delta in one sentence. SSE delivers the summary + thumbnail to the phone. | Developer is multitasking. Glances every few minutes to see the running narrative. |
| **Demo clip** | After an autodev kick that touched UI, agent records a short MP4 (default 8–15 s) of the running feature in action — from a simulator/emulator the agent already drives, or from the dev's phone via the Yaver super-host. Streams back to mobile as a watchable success video. | Mobile work, where a still frame doesn't show the interaction. Also "show me what you just built" requests on demand. |

The first two modes share the same screenshot pipeline. The third (demo
clip) reuses the wire-protocol shell but a different capture path: it
records straight to MP4 from the platform-native screen-capture tool
(`xcrun simctl io booted recordVideo`, `adb shell screenrecord`, or
ReplayKit/MediaProjection on the developer's phone) and serves the file
back over Range-aware HTTP. All three honor the same privacy boundary —
nothing reaches Convex.

## 2. Constraints

These are hard rules pulled from `CLAUDE.md` and the codebase audit. The
design must honor every one.

- **P2P only.** Frames and summaries flow agent → mobile/web directly (relay
  is a transparent forward; nothing buffered, nothing logged). They never
  reach Convex. Add `previewFrame`, `frameData`, `previewBlob` to the
  forbidden-keys list in `desktop/agent/convex_privacy_test.go`.
- **Relay-friendly.** Each frame must fit under the relay's per-response cap
  (200 MB for `/dev/*`, 10 MB default — see `relay/tunnel.go:243-246`). Target
  ≤200 KB per frame so a single SSE event is well below any limit. The relay
  detects SSE by path suffix (`tunnel.go:222-231`); the new endpoint must end
  with a recognised suffix or extend the list.
- **Direct-first, relay-fallback.** No transport-specific code; everything
  goes through `quicClient.baseUrl`, exactly like `/dev/events` does today.
- **Auth scope.** Capture / start / stop are owner-only mutations.
  Read-only viewing piggy-backs on the existing `guest-vibing` scope so a
  collaborator who already has vibing access can watch. End-user feedback
  guests do **not** get preview access by default — they don't need to see
  someone else's repo.
- **Cellular-aware.** The mobile client tells the agent its current network
  mode (`direct` / `relay-wifi` / `relay-cellular`). The agent picks an FPS
  + resolution profile so we don't melt a 4G tether.
- **Don't reinvent.** Reuse `BrowserManager` (`desktop/agent/browser.go`) for
  capture — it already wraps `chromedp` with idle-timeout, screenshot
  primitive, and an SSE event channel. The new code orchestrates it; it does
  not embed another headless browser.
- **Don't collide.** `desktop/agent/preview.go` is **already taken** by the
  branch-preview manager (git-worktree builds on a chosen port). The new
  symbols use the prefix `VibePreview*` and the file `vibe_preview.go`.
  HTTP routes live under `/vibing/preview/*`, slotting under the existing
  `guest-vibing` scope (`httpserver.go:1139`).

## 3. Topology

The connection topology is the same three-layer sandwich the rest of Yaver
uses; the new feature is an additional surface on top of it.

```
Phone (Yaver app)                Relay                       Dev machine (agent)
┌──────────────────┐                                       ┌──────────────────────┐
│ VibePreview      │                                       │ VibePreviewManager   │
│ modal in tabs    │  GET /vibing/preview/events           │   ├ orchestrates     │
│ ┌──────────────┐ │  (SSE: frame metadata + summaries)    │   │  BrowserSession  │
│ │ <Image/>     │ │ ──────────────────────────────────►   │   │  via captureState│
│ │ frame loop   │ │ ◄───────── SSE frames ────────────    │   ├ ringbuffer       │
│ └──────────────┘ │                                       │   ├ summarizer (LLM) │
│ ┌──────────────┐ │  GET /vibing/preview/frames/:seq      │   └ subscribes to    │
│ │ summary      │ │  (binary JPEG, content-addressed)     │      autodev hooks   │
│ │ scrubber     │ │ ◄───── binary frame ───────────────   │                      │
│ └──────────────┘ │                                       │ chromedp ──► dev     │
│                  │                                       │ headless    server   │
│ NetInfo hint     │  POST /vibing/preview/start           │ Chrome      127.0.0.1│
│ → mode profile   │  POST /vibing/preview/stop            │   1280x720 viewport  │
└──────────────────┘                                       └──────────────────────┘
```

Frame metadata (sequence number, byte size, content hash, dims) is small and
flows through SSE. Frame *bytes* are fetched separately via a content-addressed
GET so the browser/HTTP cache can dedupe consecutive identical frames and the
relay's SSE forwarder doesn't have to carry hundreds of KB per event.

## 4. Subsystem layout

### 4.1 `vibe_preview.go` — the manager

Owns the capture loop, the ringbuffer, the SSE fan-out, and the summary
queue. One manager per agent (singleton on the http server). Sessions are
keyed by `(deviceId, projectSlug)` since a developer might preview multiple
projects in parallel.

```go
type VibePreviewSession struct {
    ID         string         // "<projectSlug>:<random>"
    Project    string         // resolved from yaver.workspace.yaml
    TargetURL  string         // e.g. http://127.0.0.1:3000 — the dev server
    StartedAt  time.Time
    LastFrame  time.Time
    Profile    PreviewProfile // FPS, dims, JPEG quality
    Mode       string         // "live" | "summary-only" | "change-only"
    BrowserID  string         // BrowserManager session id (1:1 with this preview)
    FrameCount int
    SummaryCount int
}

type PreviewProfile struct {
    FPS        float64 // 0 = capture only on trigger
    Width      int     // viewport width fed to chromedp
    Height     int
    Quality    int     // 1-100, JPEG re-encode (lower for cellular)
    MaxFrameKB int     // hard cap, throttle FPS down if exceeded
}
```

The manager owns a content-addressed ringbuffer:

```go
type frameRecord struct {
    Seq       uint64
    Hash      string    // sha256 of jpeg bytes (first 12 hex chars used as id)
    SizeBytes int
    Width     int
    Height    int
    CapturedAt time.Time
    Path      string    // ~/.yaver/vibe-preview/<sessionId>/<hash>.jpg
}
```

Capacity is bounded both by count (default 1000 frames) and total disk
(default 500 MB) — whichever fills first triggers eviction. The bound is
configurable via the existing `Config` struct (`vibe_preview_max_frames`,
`vibe_preview_max_disk_mb`).

### 4.2 `vibe_preview_capture.go` — the capture loop

Drives `BrowserManager.captureState(session)` at the profile's FPS, JPEG-
re-encodes the PNG that chromedp returns (chromedp returns PNG; we want JPEG
for size), hashes the bytes, writes to disk, and emits an SSE event with
metadata only.

```go
for {
    select {
    case <-ctx.Done():
        return
    case <-ticker.C:
        png, err := bm.captureState(browserSess)
        if err != nil { ... }
        jpg := transcodePNGtoJPEG(png, profile.Quality, profile.Width, profile.Height)
        // Skip frame if it hashes identically to the last one — Tailwind
        // tweaks rarely produce pixel-identical output, so this collapses
        // genuinely-static periods into one stored frame + many "stable"
        // events.
        h := sha256hex(jpg)[:12]
        if h != lastHash {
            persistFrame(sessionID, h, jpg)
            lastHash = h
        }
        s.emit(VibePreviewEvent{
            Type: "frame",
            Seq: nextSeq(), Hash: h, SizeBytes: len(jpg),
            Width: profile.Width, Height: profile.Height,
            CapturedAt: time.Now().UTC(),
        })
    }
}
```

**Profiles** (chosen from the client's connection-mode hint):

| Profile | FPS | Resolution | JPEG Q | Frame size target |
|---------|----:|-----------:|-------:|------------------:|
| `live-direct` (LAN, direct) | 8 | 1280×720 | 75 | <300 KB |
| `live-relay-wifi` | 4 | 1280×720 | 60 | <200 KB |
| `live-relay-cell` | 2 | 854×480  | 50 | <80 KB  |
| `change-only` | 0 (event-driven) | 1280×720 | 70 | <250 KB |
| `summary-only` | 0 (event-driven) | 854×480  | 55 | <100 KB |

Profile selection is automatic from the `mode` query parameter on
`/vibing/preview/start`, but the developer can override (`--profile=cell` from
CLI, or a pull-down on mobile).

### 4.3 `vibe_preview_summary.go` — the change summarizer

Hooks fire from two existing seams:

1. **Autodev post-kick.** `loop_exec.go` already has a clean exit point per
   kick (after the runner returns). Add an opt-in callback hook on
   `LoopRunner` that fires `manager.OnKickComplete(sessionID, kickResult)`.
2. **Dev-server reload.** `devserver_http.go:1135` (`/dev/reload`) already
   broadcasts to subscribers; add the manager as one. When the dev server
   reloads, that's a strong "something visible changed" signal.

Each fire schedules:

1. Capture a "before" frame (use the most recent frame in the ringbuffer if
   it's <2 s old; otherwise force a capture).
2. Wait for the dev server to settle (poll `/dev/status` until `serving=true`
   *and* the most recent SSE `ready` event is older than 500 ms).
3. Capture an "after" frame.
4. If `before.hash == after.hash`, emit `{type:"summary", text:"no visible
   change", ...}` and skip the LLM call — saves tokens on backend-only edits.
5. Otherwise enqueue a Claude job: send both JPEGs (vision-mode, base64) plus
   the kick prompt and the kick's diff, ask for one sentence describing the
   visual delta. Use the existing `claudeSession` if present; fall back to
   the configured runner for diff-summarisation only when Claude is
   unavailable.
6. Emit `{type:"summary", text:..., before:hash, after:hash, kick:...}`.

The summary itself never touches Convex. It's persisted to
`~/.yaver/vibe-preview/<sessionId>/summaries.jsonl` (one line per summary) so
the scrubber can fetch a window of history on subscribe.

## 5. Wire protocol

All endpoints are mounted under `/vibing/preview/` so the existing
`guest-vibing` allow-list covers reads. Mutating endpoints (`/start`, `/stop`,
`/profile`) require owner auth; reading (`/events`, `/frames/:hash`,
`/summaries`, `/status`) is owner + vibing-scope-guest.

| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/vibing/preview/start` | POST | owner | `{project, targetUrl?, mode, profile?}` → `{sessionId, profile}`. Boots browser session, starts capture loop, registers autodev hook. |
| `/vibing/preview/stop` | POST | owner | `{sessionId}` → tears down browser session, stops loop, keeps ringbuffer until idle. |
| `/vibing/preview/status` | GET | owner+vibing | List active sessions (no frame data). |
| `/vibing/preview/events` | GET (SSE) | owner+vibing | Live event stream — frames + summaries + lifecycle. Replays last N events on subscribe (like `DevServer.Subscribe`). |
| `/vibing/preview/frames/:hash` | GET | owner+vibing | Binary JPEG. Cache-Control: `public, max-age=86400, immutable` (content-addressed). |
| `/vibing/preview/summaries` | GET | owner+vibing | `?since=<ts>&limit=N` → JSON array of recent summaries. |
| `/vibing/preview/profile` | POST | owner | `{sessionId, profile}` → live-update FPS/dims/quality. |
| `/vibing/preview/snapshot` | POST | owner+vibing | Force a one-shot capture, returns frame metadata. |

### 5.1 SSE event envelope

```json
// Frame ready (most common; live mode)
{"type":"frame","seq":42,"hash":"a1b2c3d4e5f6","size":118000,"w":1280,"h":720,"ts":"2026-04-26T14:23:12.480Z"}

// Frame deduped (no visible change since last frame)
{"type":"stable","seq":43,"hash":"a1b2c3d4e5f6","ts":"2026-04-26T14:23:12.730Z"}

// Summary ready
{"type":"summary","seq":44,"text":"Nav background changed from white to blue; logo size unchanged.","before":"a1b2c3d4e5f6","after":"7890abcdef12","kickId":"k_173","ts":"2026-04-26T14:23:13.110Z"}

// Profile auto-throttled because frames are over budget
{"type":"throttle","reason":"frame_size_exceeded","oldFPS":8,"newFPS":4,"ts":"..."}

// Browser session died
{"type":"capture_error","message":"chromedp: target closed","ts":"..."}

// Lifecycle
{"type":"started","sessionId":"...","profile":{...},"ts":"..."}
{"type":"stopped","sessionId":"...","ts":"..."}
```

Keepalive: 15 s comment line (`: keepalive\n\n`), matching `/dev/events`.
Replay-on-subscribe: last 50 events from the in-memory tail.

### 5.2 Why metadata-over-SSE plus binary GET

We could base64-inline frames into SSE events. Pros: one round-trip. Cons: 4/3
size inflation (cellular cost matters), every consumer pays even if they're
muted/in-background, no HTTP cache reuse. Splitting metadata from bytes lets
us:

- Mobile in background: still consume tiny SSE metadata, skip frame fetches.
- Web dashboard: leverage browser HTTP cache on `:hash` GETs, so identical
  frames cost zero bytes after the first.
- Future codec swap (WebP, AVIF): only the GET changes, SSE protocol stays.

### 5.3 Connection-mode hint

The mobile client sends an `X-Yaver-NetMode` header on `/vibing/preview/start`
and on every reconnect of `/vibing/preview/events`. Values: `direct`,
`relay-wifi`, `relay-cell`. The manager picks the matching profile when no
explicit `profile` is set, and applies a one-step downgrade when the
connection mode shifts mid-session (e.g., walking out of WiFi). The agent
emits a `throttle` event so the client UI can surface that the FPS dropped.

This header is defined now and not a future addition — the existing
`quicClient.connectionMode` tracker on mobile (`mobile/src/lib/quic.ts`)
already knows the value, so plumbing it as a header is one line.

## 6. Mobile UI

`mobile/src/components/DevPreview.tsx` already shows a banner above the tab
bar when the dev server is running. The new flow:

1. **Banner additions.** When `framework` is web (Next/Vite/Astro/etc.) and
   the developer is on a project where they've enabled vibe-preview, the
   banner gets a second button: a film-strip icon next to the existing "Open
   in Yaver" button. Tap → opens the new modal.
2. **VibePreviewModal.tsx (new).** Three regions:
   - **Top:** `<Image source={{uri: latestFrameUrl}} />` updated whenever a
     `frame` SSE event arrives. The URL is
     `${baseUrl}/vibing/preview/frames/${hash}` with auth headers. RN's
     `Image` cache deduplicates identical hashes for free.
   - **Middle:** small status row — current FPS, frame count, bytes/min,
     mode pill (Live / Summary / Change-only).
   - **Bottom:** scrubbable horizontal list of summaries (most recent first),
     each showing the `after` thumbnail + the one-sentence text. Tap a
     summary to pin its `after` frame as the displayed image.
3. **Background behavior.** On AppState=`background`, drop the SSE frame
   subscription but keep the summary subscription. On `active`, reconnect
   with the network-mode hint freshly evaluated.
4. **Mode switcher.** Pull-down at top of modal: Live / Summary-only /
   Change-only. Defaults to whatever the agent reported on `started`.
5. **Stop button.** "Stop preview" calls `/vibing/preview/stop`. The frame
   ringbuffer survives — the developer can still scrub yesterday's summaries
   even after the capture stopped.

The image source needs auth headers, which `<Image>` supports via the
`headers` prop on the source. We do **not** route frames through the QUIC
client's fetch wrapper for image rendering — RN's native image loader is
roughly 10× faster.

The connection-mode tracker (`quicClient.getConnectionMode()`) is already
exposed to the component layer; the modal subscribes and re-emits as the
`X-Yaver-NetMode` header on reconnect.

## 7. Web dashboard

`web/components/dashboard/DevServerView.tsx` (existing) gets a sibling tab
`VibePreviewView.tsx`. The implementation is simpler than mobile:

- `<img src="/vibing/preview/frames/<hash>" />` — browser handles cache.
- Native `EventSource` for SSE.
- Larger summary timeline (the dashboard has the screen real estate the
  phone doesn't).
- Same auth: existing `agent-client.ts` path adds `X-Yaver-Auth`.

No new abstractions on the web side — just a thin view component. Web does
not pass a `NetMode` header (browser ≈ always direct-from-relay; the agent's
default profile suits it).

## 8. CLI

`yaver vibe preview` subcommand, mirroring the HTTP surface.

```bash
yaver vibe preview start --project web --mode live --profile direct
yaver vibe preview status
yaver vibe preview stop --session <id>
yaver vibe preview snapshot --session <id>     # prints metadata; with --out file.jpg writes bytes
yaver vibe preview summaries --since 10m       # tail recent summaries
```

`vibe_preview_cmd.go` follows the existing pattern (look at
`devserver_cmd.go` for shape). All commands talk to `localhost:18080`; remote
machines use `--device <id>` like everywhere else, dispatched via the `ops`
verb (next section).

## 9. MCP surface

A new `ops` verb `preview` plus standalone tools for AI agents that prefer
direct access. Both are already-supported patterns.

```jsonc
// ops verb form
{
  "verb": "preview",
  "machine": "primary",
  "payload": {
    "op": "start",          // start|stop|status|snapshot|summaries|profile
    "project": "web",
    "mode": "summary-only",
    "profile": "relay-wifi"
  }
}
```

Standalone tools (registered next to existing `dev_*` MCP tools):

| Tool | Purpose | Allowed for guests? |
|------|---------|---------------------|
| `vibe_preview_start` | Start a session | No (owner only) |
| `vibe_preview_stop` | Stop a session | No |
| `vibe_preview_status` | List sessions | Vibing-scope guests |
| `vibe_preview_snapshot` | Single capture | Vibing-scope guests |
| `vibe_preview_summarize` | Recent summaries | Vibing-scope guests |

`vibe_preview_summarize` is the most useful tool to expose to *the AI runner
itself*. After Claude finishes a kick that touches the web frontend, it can
call this tool to read its own visual diff summary and decide whether the
change matched intent. That closes a loop today's autodev cannot:
"I edited the nav. Let me check what it looks like." Today the runner is
text-blind to its own UI output; this fixes that.

## 10. Convex privacy

Three changes:

1. `backend/convex/schema.ts` — `vibePreviewSessions` table:

   ```ts
   vibePreviewSessions: defineTable({
     userId: v.id("users"),
     deviceId: v.string(),
     project: v.string(),
     mode: v.string(),
     startedAt: v.number(),
     endedAt: v.optional(v.number()),
     frameCount: v.number(),
     summaryCount: v.number(),
   }).index("by_user", ["userId"]).index("by_device", ["deviceId"]),
   ```

   No frame contents, no summary text. Just a per-session counter so the
   dashboard can show "you ran 14 preview sessions today".

2. `desktop/agent/convex_privacy_test.go` — append to forbidden keys:

   ```go
   "previewFrame", "frameData", "previewBlob", "summaryText",
   "previewSummary", "frameJpeg", "screenshotB64",
   ```

   Plus a new test fixture: a `recordPreviewSession` call whose payload
   strictly contains only the fields above, asserts no leak.

3. The existing `convexSyncer.callMutation` path — add `recordPreviewSession`
   as a counted-only mutation (like the existing activity audit summaries).
   Frame data and summary text are *never* passed to it.

## 11. Failure modes and handling

- **Chrome not installed.** `BrowserManager.OpenSession` already returns
  `"launch chrome: ... (install Chrome/Chromium)"`. Surface this clearly:
  the manager emits a `capture_error` event with a one-shot installation
  hint (`yaver install chromium`). Mobile UI shows a fix-it banner with a
  copy-the-command button, not a generic error.
- **Dev server not running.** `start` returns 412 if the resolved
  `targetUrl` doesn't accept TCP. Hint: "Run /dev/start first."
- **Dev server crashes mid-session.** Capture loop catches navigation
  failures, emits `capture_error`, keeps the session alive (the dev server
  will likely come back). Auto-reconnect after 5 s; back off to 30 s after
  three failures.
- **Frame cap exceeded.** If 5 consecutive frames are over `MaxFrameKB`,
  drop FPS by 2× and resolution by 75%. Emit `throttle`. Don't drop quality
  below `quality=30` — at that point switch to `summary-only` and tell the
  user.
- **Disk pressure.** Ringbuffer eviction is by frame count *or* total bytes,
  whichever fills first. Eviction never deletes the most recent 10 frames
  (mobile background→foreground transitions need a frame to display
  immediately).
- **Idle.** No SSE subscribers for 60 s? Pause the capture loop; resume on
  next subscribe. Saves CPU when the developer's phone is asleep.
- **Long-running session.** `BrowserSession` already has a 30-min idle
  timeout (`browser.go:64`). The vibe-preview manager touches the session
  on every capture so an active loop keeps it alive. When `stop` is called,
  the browser session is closed via `bm.CloseSession`.

## 12. Testing plan

- **Unit (`vibe_preview_test.go`).** Manager lifecycle, ringbuffer eviction,
  profile selection from net-mode hint, frame deduplication on identical
  hash, summary skip when before==after.
- **Integration (`vibe_preview_integration_test.go`).** Spin up a tiny HTTP
  server that serves a static page, point the manager at it, drive captures,
  assert SSE event order and binary GET round-trip. Skip on CI if Chrome
  isn't available; gate on `BROWSER_AVAILABLE` env var same as
  `browser_test.go` already does.
- **Privacy (`convex_privacy_test.go`).** Extend the existing fixture: the
  test home directory has a marker file, the test asserts the marker never
  appears in any value passed to `callMutation` for `recordPreviewSession`.
- **Mobile (Detox or RN testing-library).** Mock the SSE endpoint, render
  the modal, assert frames update, assert summary list scrubs.
- **End-to-end smoke (manual).** `yaver vibe preview start --project web`
  on a Mac, watch the modal on a phone over cellular, confirm <80 KB
  frames at 2 FPS arrive without dropouts for 5 minutes.

## 13. Phased implementation

Each phase compiles, tests pass, ships independently. Don't wait until phase
9 to merge — that's exactly the half-finished refactor `CLAUDE.md` warns
against.

**Status: Phase 1 done as of 2026-04-26.** `vibe_preview.go` + HTTP
handlers + CLI + 10 passing unit tests under `-race`. Frame capture
verified end-to-end against a fake browser; chromedp path inherits from
the existing `BrowserManager` so the smoke test on real Chrome is in
Phase 2 alongside SSE.

1. **Phase 1 — capture skeleton.** *Done.* Manager, session, ringbuffer,
   start/stop/status/snapshot HTTP, CLI, unit tests.

2. **Phase 2 — wire protocol.** `/events` SSE, `/frames/:hash` GET. Disk
   persistence, eviction. Profile selection. Adaptive throttling on frame
   size. Estimated 1.5 days.

3. **Phase 2.5 — simulator + emulator MP4 capture.** New
   `vibe_preview_clip.go` wrapping `xcrun simctl io booted recordVideo`
   and `adb shell screenrecord`. `/vibing/preview/clip/{start,stop}` and
   `/clip/:id` (Range-aware GET). Storage + ringbuffer for clips
   separate from frames. Estimated 2 days.

4. **Phase 3 — mobile modal.** New `VibePreviewModal.tsx`, banner button,
   frame loop, NetMode header, mode pull-down, stop button, plus the
   inline `<Video>` element for clip playback. Estimated 2 days.

5. **Phase 4 — summary pipeline (text + clip).** Hook into `/dev/reload`
   and autodev post-kick. Claude vision call for one-sentence summary.
   Auto-clip on mobile-stack kicks (gated by manifest opt-in). Summary
   persistence + GET endpoint. Mobile summary scrubber + success
   carousel. Estimated 2.5 days.

6. **Phase 5 — phone-side capture (ReplayKit / MediaProjection).** Native
   modules on the Yaver mobile app for screen capture; chunked upload to
   `/vibing/preview/clip/upload`; permission UX. Only path that doesn't
   need any new agent code, just new mobile code. Estimated 2 days.

7. **Phase 6 — web dashboard view + MCP tools + ops verb.** Web is small
   because the protocol is ready; MCP follows the existing tool-registration
   pattern. Estimated 1 day.

8. **Phase 7 — exercise scripts (Maestro / Claude one-shot).** Auto-write
   short E2E flows so demo clips have motion instead of a static idle.
   Saved exercises become a seed library for real E2E later. Estimated
   1.5 days.

9. **Phase 8 — privacy boundary tests + Convex schema (sessions + clips).**
   Last because it's strictly defensive — the runtime never sent anything
   sensitive in earlier phases, but the test makes that property
   load-bearing. Estimated 0.5 day.

Total remaining: ~13 days of focused work (12 days new — the Phase 1
day already shipped).

## 14. File-by-file change list

New files:

- `desktop/agent/vibe_preview.go` — manager, session, ringbuffer **(done)**
- `desktop/agent/vibe_preview_http.go` — HTTP handlers **(done — start/stop/status/snapshot)**
- `desktop/agent/vibe_preview_cmd.go` — CLI **(done)**
- `desktop/agent/vibe_preview_test.go` — unit tests **(done)**
- `desktop/agent/vibe_preview_capture.go` — capture loop, JPEG transcode (Phase 2)
- `desktop/agent/vibe_preview_disk.go` — disk persistence + ringbuffer eviction (Phase 2)
- `desktop/agent/vibe_preview_sse.go` — `/vibing/preview/events` SSE fan-out (Phase 2)
- `desktop/agent/vibe_preview_clip.go` — simulator + emulator MP4 recording (Phase 2.5)
- `desktop/agent/vibe_preview_clip_http.go` — `/vibing/preview/clip/*` handlers, Range-aware (Phase 2.5)
- `desktop/agent/vibe_preview_clip_upload.go` — chunked upload from phone (Phase 5)
- `desktop/agent/vibe_preview_exercise.go` — Maestro / Claude one-shot driver (Phase 7)
- `desktop/agent/vibe_preview_summary.go` — summary queue, hooks (Phase 4)
- `desktop/agent/vibe_preview_integration_test.go` — chromedp + simctl tests (gated)
- `desktop/agent/ops_vibe_preview.go` — ops verb dispatch (Phase 6)
- `desktop/agent/mcp_vibe_preview.go` — MCP tools (Phase 6)
- `mobile/src/components/VibePreviewModal.tsx` — modal (Phase 3)
- `mobile/src/components/VibePreviewSuccessCarousel.tsx` — auto-clip pinned card (Phase 4)
- `mobile/src/lib/vibePreview.ts` — client (Phase 3)
- `mobile/src/lib/screenRecorder.ts` — RN bridge to native ReplayKit / MediaProjection (Phase 5)
- `mobile/ios/Yaver/YaverScreenRecorder.swift` *(extend existing)* — ReplayKit `startCapture` path for vibe-preview clips
- `mobile/android/.../YaverScreenRecorderModule.kt` (new) — MediaProjection + MediaRecorder
- `web/components/dashboard/VibePreviewView.tsx` — web view (Phase 6)
- `web/lib/vibe-preview-client.ts` — web client (or extend agent-client.ts)

Modified files:

- `desktop/agent/httpserver.go` — register `/vibing/preview/*` routes,
  instantiate `vibePreviewMgr` in `NewHTTPServer`, add scope-prefix entries
  for the new paths under `guest-vibing`.
- `desktop/agent/devserver_http.go` — `/dev/reload` handler invokes
  `vibePreviewMgr.OnDevReload(workDir)` if a session is active.
- `desktop/agent/loop_exec.go` — post-kick hook calls
  `vibePreviewMgr.OnKickComplete(...)` when a session is registered for
  the project.
- `desktop/agent/config.go` — `VibePreviewMaxFrames`, `VibePreviewMaxDiskMB`,
  `VibePreviewDefaultMode` config keys.
- `desktop/agent/convex_privacy_test.go` — forbidden keys + new fixture.
- `desktop/agent/convex_state_sync.go` — `recordPreviewSession` counter
  mutation.
- `backend/convex/schema.ts` — `vibePreviewSessions` table.
- `backend/convex/agentSync.ts` — `recordPreviewSession` mutation.
- `mobile/src/components/DevPreview.tsx` — film-strip button, modal mount.
- `mobile/src/lib/quic.ts` — `getConnectionMode()` already exists; expose
  `vibePreview*` methods that map to `/vibing/preview/*`.
- `web/components/dashboard/DevServerView.tsx` — add tab/button to open
  the new view.
- `relay/tunnel.go` — extend the SSE path-suffix list to include
  `/vibing/preview/events`. The 200 MB cap is already enough for binary
  frame GETs since each is sub-MB.
- `CLAUDE.md` — add a short section pointing here. (Index, not duplicated
  content.)
- `docs/vibe-preview-streaming.md` — this file.

## 15. Demo clips (video) — mobile + web

§§ 1-14 cover the screenshot pipeline. This section covers the third mode:
short MP4 clips that show the running feature, not just a frame of it.
The trigger paths and storage shell are shared; only the capture and the
binary serving differ.

### 15.1 Capture sources

| Project type | Where the app runs | Capture tool | Triggered by |
|--------------|--------------------|--------------|--------------|
| Web (Next/Vite/Astro) | Headless Chrome on agent (already wired) | chromedp screencast → MP4 mux *or* a stream of frames re-encoded to MP4 | autodev post-kick / explicit |
| iOS native or RN/Expo | iOS Simulator on agent's Mac | `xcrun simctl io booted recordVideo --codec=h264 <out>.mp4` | autodev post-kick / explicit |
| Android native or RN | Android emulator on agent | `adb shell screenrecord --time-limit=15 /sdcard/<id>.mp4` then `adb pull` | autodev post-kick / explicit |
| Mobile via Yaver super-host (Hermes guest bundle on the dev's phone) | Developer's own phone | ReplayKit (iOS) / MediaProjection (Android) inside the Yaver mobile app, uploaded over P2P | explicit "record from my phone" only — auto-trigger would surprise the user |

Source detection order on the agent: explicit `source` param → workspace
manifest stack (`react-native-expo` or `flutter` → simulator/emulator) →
running dev server (web). The user can override with `--source=phone` from
the CLI or mobile UI to opt the developer's own phone in.

### 15.2 Demo "exercise" — what the camera films

A 12-second video of an idle simulator is useless. We need motion. Three
strategies, picked in order:

1. **Maestro flow on disk.** If the project has `e2e/<feature>.flow.yaml`
   matching the kicked feature name, the agent runs it (`maestro test`)
   while recording. This is the cleanest path and the one to nudge users
   toward.
2. **Claude one-shot interaction.** Agent asks Claude vision: "given a
   screenshot of the home screen and the diff that just landed, write a
   short Maestro YAML that exercises this feature in 8 seconds." Save to
   `~/.yaver/vibe-preview/exercises/<projectSlug>/<kickId>.yaml`, run it,
   keep on success. Failure → fall through.
3. **Idle pan.** Just record. Default for unknown projects. Better than
   nothing.

The exercise script + the clip are persisted side-by-side so a future kick
can replay or inspect what Claude wrote. The `exercises/` directory grows
over time into a seed library — actually useful for E2E tests, on top of
the demo-clip use case.

### 15.3 Wire protocol additions

| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/vibing/preview/clip/start` | POST | owner | `{project, source?, durationMaxSec?, exerciseHint?}` → `{clipId, recording: true}`. Spawns the recorder + (if needed) the exercise. |
| `/vibing/preview/clip/stop` | POST | owner | `{clipId}` → ends the recording early. |
| `/vibing/preview/clips` | GET | owner+vibing | List clips for a session. |
| `/vibing/preview/clip/:id` | GET | owner+vibing | Binary MP4. **Honors `Range:` requests** so mobile players can seek without re-downloading. `Cache-Control: private, max-age=86400`. |
| `/vibing/preview/clip/:id/poster` | GET | owner+vibing | First-frame JPEG (cheap thumbnail for the timeline). |
| `/vibing/preview/clip/:id/summary` | GET | owner+vibing | Optional one-paragraph description (Claude reads ~5 sampled frames + the kick diff). Generated lazily on first GET, cached on disk. |

SSE events on the existing `/vibing/preview/events` channel get two new
types so the mobile UI can update the timeline live:

```json
{"type":"clip_started","clipId":"c_173","source":"sim-ios","durationMaxSec":15,"ts":"..."}
{"type":"clip_ready","clipId":"c_173","sizeMB":1.8,"durationSec":11.4,"posterHash":"...","ts":"..."}
```

The event arrives the moment the file is closed and `mp4 -movflags
+faststart` has been applied (so the mobile player can stream-decode
without downloading the full file first).

Relay impact: clip files are larger than frames (single-digit MB) but
well under the 200 MB `/dev/*` cap. `relay/tunnel.go` already buffers
responses in memory; a 5 MB MP4 is fine. For very long clips (rare —
default cap 30 s), the agent re-encodes at 24 fps + 720p H.264 to keep
size proportional.

### 15.4 Auto-trigger on kick success

Same hook point as the text summary: the autodev post-kick callback in
`loop_exec.go`. When the manifest stack is `react-native-expo` *or* the
modified files in the kick include `mobile/`, `ios/`, `android/`, or
`*.tsx`/`*.swift`/`*.kt`, the manager schedules a clip in addition to
the text summary. The clip ID is returned in the same SSE summary event so
the timeline shows them together:

```json
{"type":"summary","seq":44,"text":"Added Pricing card with three tiers; checkout button slides up.","clipId":"c_173","ts":"..."}
```

Auto-clip is **opt-in per project** because recording adds 10–15 s to
every successful kick. Toggle: `yaver.workspace.yaml`'s app block has
`vibe_preview: { auto_clip: true }`. Default false — explicit opt-in
keeps surprise spend on simulator I/O bounded.

### 15.5 Phone-side capture (Yaver mobile super-host)

When the developer is testing a guest bundle inside the Yaver mobile app
(the "push to device" flow), the *phone is the canvas*, not a simulator.
For these cases the recorder lives on the phone:

- iOS: `RPScreenRecorder.shared().startCapture(handler:)` writes CMSampleBuffers
  into an `AVAssetWriter` configured for H.264 in a temp `.mp4`.
- Android: `MediaProjection` + `MediaRecorder` writing to a temp file.

When the agent fires `clip_started` with `source: "phone"`, the
super-host's command-channel listener (which already exists for hot
reload — see `mobile/src/lib/quic.ts` `streamBlackBoxCommands`) receives
the request, prompts the user once for permission (iOS shows the system
recording UI), and streams the MP4 back to the agent on `/vibing/preview/clip/upload`
in 256 KB chunks. The agent assembles the chunks, writes
`<clipId>.mp4`, and emits `clip_ready`.

Permission on iOS is interactive — there is no way to start ReplayKit
silently. The mobile UI shows a clear "Yaver wants to record this app"
dialog the first time per session, and remembers the answer for the rest
of that session.

### 15.6 Mobile UI

`VibePreviewModal.tsx` adds:

- A play-button overlay on summaries that have a `clipId`. Tapping
  expands an inline `<Video>` element (`expo-av` already in
  `mobile/package.json`).
- "Record from agent" button next to the existing snapshot button. Posts
  to `/vibing/preview/clip/start` with the active project; the source is
  picked automatically.
- "Record from this phone" button (visible only when a guest Hermes
  bundle is currently loaded). Triggers the ReplayKit / MediaProjection
  flow described above.
- A **summary success carousel** at the top of the modal — when an
  autodev burst finishes with `auto_clip:true`, the latest clip plus its
  one-sentence summary are pinned for 60 seconds so the developer sees
  "what just shipped" without scrolling. Auto-dismisses or can be
  swiped away.

### 15.7 Web dashboard

`<video>` element with `<source src="/vibing/preview/clip/{id}" type="video/mp4">`.
Browsers handle Range requests natively. Same auth header path as the
frame fetch. The dashboard view also gets a "Download MP4" button per
clip — useful for building a public changelog out of vibe-coded
features.

### 15.8 CLI

```bash
yaver vibe preview clip --project mobile                 # record now (auto source)
yaver vibe preview clip --project mobile --source sim-ios --duration 12
yaver vibe preview clip --project mobile --source phone  # uses developer's phone
yaver vibe preview clips --project mobile                # list recent
yaver vibe preview clip get <id> --out ./demo.mp4        # download
yaver vibe preview clip summary <id>                     # print Claude summary
```

### 15.9 MCP tools

Following the existing `vibe_preview_*` family:

| Tool | Purpose |
|------|---------|
| `vibe_preview_clip_record` | Start a recording. Owner-only. Returns `clipId`. |
| `vibe_preview_clip_status` | Poll a recording. |
| `vibe_preview_clip_list` | Recent clips for a project. Vibing-scope guests OK. |
| `vibe_preview_clip_summarize` | Ask Claude to describe the clip from sampled frames. Vibing-scope guests OK. |

The summarize tool is the most useful for AI-driven flows: after Claude
finishes a feature, it can call `vibe_preview_clip_record` then
`vibe_preview_clip_summarize` and read its own demo, exactly like the
Phase 4 still-image summary closes the visual-blindness loop.

### 15.10 Privacy

Same boundary. Convex stores **only**:

```ts
vibePreviewClips: defineTable({
  userId: v.id("users"),
  deviceId: v.string(),
  project: v.string(),
  clipId: v.string(),
  durationSec: v.number(),
  sizeBytes: v.number(),
  source: v.string(),          // "browser" | "sim-ios" | "sim-android" | "phone"
  createdAt: v.number(),
  // NO video data, NO summary text, NO file path
})
```

Forbidden-keys list adds: `clipMp4`, `videoBlob`, `clipBytes`, `clipPath`,
`exerciseScript`. The summary text — even though Claude wrote it from
visual data — is structurally indistinguishable from a feedback
description and stays in the same forbidden bucket as the still-image
summaries.

### 15.11 Storage

- `~/.yaver/vibe-preview/clips/<sessionId>/<clipId>.mp4`
- `~/.yaver/vibe-preview/clips/<sessionId>/<clipId>.poster.jpg`
- `~/.yaver/vibe-preview/clips/<sessionId>/<clipId>.summary.txt` (lazy)
- `~/.yaver/vibe-preview/exercises/<projectSlug>/<kickId>.yaml`

Disk cap default: 2 GB across all clips, separate from the frame ring
budget. Eviction by mtime. Pin-protect the most recent 3 per project so
the success carousel always has something to show.

## 16. Open questions

1. **Headful Chrome on Linux servers.** `chromedp` defaults to headless and
   `BrowserManager.OpenSession(headful=false)` is the path we'd use. But on
   a Hetzner box without an X server, even headless Chrome needs `xvfb` or
   the `--use-gl=swiftshader` flag for some GPU-dependent web pages
   (Three.js demos, etc.). Out of scope for v1; document as a known
   limitation. Most web frontends render fine in true-headless.
2. **Audio.** Web frontends with audio (rare in vibe-coding) are silent in
   the preview. No plan to add audio capture; the cost (WebRTC pipeline)
   isn't worth the rare benefit.
3. **Interactive preview.** A future phase could let the developer *click*
   on the streamed frame to drive the headless browser (so the UI is
   reactive even from the phone). This is interesting but not in v1 — the
   loop currently solves "watch what the agent built", not "drive what the
   agent built".
4. **Multi-page apps.** v1 captures whatever URL the dev server serves at
   `/`. A future flag `--paths=/,/pricing,/dashboard` could rotate
   captures across multiple routes so the developer sees the whole site
   change, not just the home page.
5. **Vision-LLM cost.** Each summary call sends two frames + the kick diff
   to Claude with vision. At 100 KB/frame that's ~200 KB in, plus output
   tokens. On a 5-minute autodev burst with 3 kicks, that's 3 summary
   calls; well under $0.05. But on a multi-hour autodev run with reloads
   every 30 s, costs add up. Add a `min_summary_interval` (default 60 s)
   to coalesce nearby reloads into a single before/after pair.
6. **Guest visibility default.** Vibing-scope guests get read access by
   default. Should they? Counter-argument: a host might let a guest run
   tasks but not want the guest to see incidental work-in-progress UI.
   Mitigation: scope can be tightened with the existing `guests config`
   surface (`allowed_endpoints` filter) — a host who cares can revoke
   `/vibing/preview/*` per guest.

---

*When this doc and the code disagree, the code wins. Update this file as
part of the same change that modifies the behavior it describes.*
