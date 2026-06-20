# Task-Runner → Browser → Video (self-host + managed-cloud)

Status: design / not yet implemented. Author seed: 2026-06-20.
Verify every file:line against code before acting (repo rule: docs drift).

## 0. Goal (the thing we proved manually, now make it first-class)

A user gives a task ("re-quote X on talos.works", "test this checkout flow"),
a **runner agent executes it driving a real browser**, and Yaver **returns a
video of the run** — identically whether the agent runs **self-hosted** (user's
machine) or in **yaver-managed cloud** (provisioned box).

This was just done by hand against talos.works: Playwright drove the live UI on a
Hetzner box, recorded a `.webm`, and returned it. This doc turns that into a
built-in Yaver capability that records the agent's *own* browser session.

## 1. What already exists (reuse — do NOT rebuild)

| Capability | Where | Notes |
|---|---|---|
| Browser automation (open/navigate/click/type/screenshot/eval/interactive) | `desktop/agent/browser.go`, `browser_interactive*.go`; tools `mcp_tools.go:3970-4196`; dispatch `httpserver.go:14486-14866` | **chromedp v0.15.1** (CDP). Runs **locally in the agent process**. Sessions persist in `BrowserManager.sessions` + profiles `~/.yaver/browser-profiles/`. Screenshots returned base64. **No video.** |
| Task runner | `desktop/agent/tasks.go`, create `httpserver.go:3747-4044`, store `store.go` | Runners = Claude/Codex/OpenCode/GLM subprocesses. The agent subprocess calls Yaver MCP tools (incl. `browser_*`) over stdio → `httpserver.go` MCP dispatcher. SSE output. |
| Task already has video fields | `tasks.go:862-863, 984-987` | `VideoEnabled / VideoSource / VideoClipID / VideoStatus(queued\|recording\|ready\|failed)`. Comment at `:973-984` says web tasks currently get a **"frame burst"**, not a clip. |
| Auto-record on task completion | `tasks_video_summary.go` (`MaybeRecordTaskSummary`, `autoDetectVideoSource`) | Fires when `VideoEnabled`; records **sim-ios (`xcrun simctl recordVideo`)** / **sim-android (`adb screenrecord`)**. |
| Clip store + serving | `vibe_preview_clip.go`, `vibe_preview_clip_http.go:104-182` | MP4 at `~/.yaver/vibe-preview/clips/<project>/<clipID>.mp4` + poster; **HTTP Range** serve at `GET /vibing/preview/clip/<id>` (+ `/poster`). Reachable through relay `{relayURL}/d/{deviceId}/...`. |
| Playwright `recordVideo` precedent | `scripts/generate-demo-videos/generate.mjs:48-83` | One-off: headless Chromium → webm → `ffmpeg -c:v libx264 -crf 26` → mp4. Not in the live agent. |
| Managed-cloud provisioning | `backend/convex/cloudMachines.ts:856-950`, `cloud-image/`, `Dockerfile.yaver-cloud`, `cloud-firstboot.sh`, relay `relay/` | Hetzner VM → Docker `yaver-cloud` → cloud-init → `yaver serve --multi-user`. Same agent binary/API as self-host; only *where it runs* differs. |
| Shared storage backends | `desktop/agent/shared_storage.go` | local/smb/webdav/storagebox/s3 — exists, but **no auto-upload of clips**. |

## 2. The gap (precise, 4 items)

- **A. No browser-source clip recorder.** `vibe_preview_clip.go:332` hard-returns
  `"browser source uses the frame stream — record via /vibing/preview/start mode=live"`.
  `VibeClipSourceBrowser="browser"` (`:35`) is a declared-but-dead enum.
- **B. Recording isn't tied to the agent's session.** `MaybeRecordTaskSummary`
  is a *post-completion summary* of a sim/emulator — it does not record the
  actual `browser_*` actions the agent performed during the task.
- **C. Managed-cloud image can't run it.** `Dockerfile.yaver-cloud` has **no
  Chromium and no ffmpeg** (grep: none). Headless Chrome renders offscreen so
  **no Xvfb is needed**, but the binaries must be present.
- **D. No durable/shareable artifact beyond the box.** Clips are local, served
  via relay (works while the box lives). Managed-cloud retention + a shareable
  link need an opt-in uploader.

## 3. Design decision — record via CDP screencast, in-process

Record the agent's **own** chromedp session (not a separate replay) using the
CDP connection chromedp already holds:

- `Page.startScreencast` (JPEG frames) **or** a `captureScreenshot` loop →
  pipe frames to `ffmpeg -f image2pipe` → H.264 MP4.
- **Headless-friendly** (no display/Xvfb) → identical in self-host and cloud.
- **In-process** with chromedp → no Playwright runtime dependency; records the
  real session the agent drives, so every `browser_*` action is captured.
- ffmpeg is already a soft dep (poster extraction `vibe_preview_clip.go:350`).

Rejected: Playwright `recordVideo` (separate browser, extra runtime dep, and it
wouldn't be the agent's session). Keep Playwright only for the demo-video script.

## 4. Change list (files)

1. **NEW `desktop/agent/browser_video.go`** — `BrowserVideoRecorder`:
   start/stop CDP screencast on a `*BrowserSession`, frames→ffmpeg→mp4, write a
   `VibeClipRecord` (reuse its status/poster/serving). Cap duration; killable.
2. **EDIT `browser.go`** — `BrowserSession` gains recorder fields; `OpenSession*`
   starts the recorder when requested; `CloseSession` finalizes (status=ready,
   poster). Idle-timeout path must finalize, not orphan.
3. **EDIT `mcp_tools.go` / `httpserver.go`** — `browser_open` gains
   `record bool` (+ optional `clip_id`/`task_id` link). Recording follows the
   **session**, so the agent need not manage it per action.
4. **EDIT `vibe_preview_clip.go:~332`** — route `VibeClipSourceBrowser` to
   `BrowserVideoRecorder` instead of erroring; reuse `VibeClipRecord`.
5. **EDIT `tasks_video_summary.go` / `tasks.go`** — when `VideoEnabled` and the
   task opens a browser session, pass the task's `VideoClipID` into the session
   so the agent's run is the recording; finalize on task done; surface
   `videoClipId` (already in the task JSON).
6. **EDIT `Dockerfile.yaver-cloud`** — add `chromium` + `ffmpeg` (+ fonts).
   Bump `cloud-image` + `versions.json`. No Xvfb (headless). Note ~300–500 MB
   RAM/headless-chrome budget against the box size.
7. **OPTIONAL NEW** — on finalize, push clip to a `shared_storage` backend
   (s3/storagebox) + presign; mint a `short_*` link. Convex stores only the
   URL/metadata (privacy contract), never bytes.
8. **NEW e2e** in `yaver-tests/` — create a task "open <url>, click X", assert a
   `ready` clip with non-zero size (mirror the talos Playwright proof). Local
   first (repo rule), CI second.

## 5. Self-host vs managed-cloud parity

Same recorder code; only the environment differs.

| | Self-host | Managed-cloud |
|---|---|---|
| Chrome | `findChromePath()` (Playwright cache / system) `browser_interactive.go:27-80` | add `chromium` to `Dockerfile.yaver-cloud` |
| ffmpeg | assumed on PATH | add to image |
| Display | none (headless) | none (headless — no Xvfb) |
| Serve clip | agent HTTP `/vibing/preview/clip/<id>` | same, via relay `{relayURL}/d/{deviceId}/...` |
| Gate | always on | behind `managed.go` subsystem toggle + box memory budget |

## 6. Safety (repo "do no harm" rules apply to the automation, not the recorder)

Recording is neutral (the OBS stance in `CLAUDE.md`). The **browser automation
it captures** must obey Policy Guard (`desktop/agent/access_policy.go`) and
anti-pivot egress (`egress_proxy.go`): no UA/Origin spoofing to defeat bot
detection, **stop on 403/429/451**, and for third-party reads prefer the user's
own vantage (`runtime: mobile_user_present`) over a 24/7 cloud-box scrape loop.
A managed box must not become a scraping engine.

## 7. Phasing

- **P1 (self-host, 80%)**: `browser_video.go` + `browser_open record` flag +
  reuse clip serving. e2e mirroring the talos proof.
- **P2 (task integration)**: auto-record web tasks; agent capability context
  ("web browser sessions are recorded; the user gets a video"); `videoClipId`
  surfaced to mobile/web "▶ Watch demo".
- **P3 (managed-cloud)**: chromium+ffmpeg in image; toggle; verify relay serving
  + memory budget.
- **P4 (sharing/retention)**: shared_storage upload + presign + short-link.

## 8. The single crux line

Flip `vibe_preview_clip.go:332` from "browser → frame stream, no clip" to
"browser → `BrowserVideoRecorder` (CDP screencast → ffmpeg)". Everything else
(task video fields, clip store, range serving, relay reach, managed provisioning)
already exists. That one recorder + the cloud-image binaries close the loop.
