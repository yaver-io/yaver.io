# Yaver Web UI vs Mobile UI — parity audit

Status: draft
Date: 2026-04-24

## Context

Web dashboard (`https://yaver.io/dashboard`) feels much thinner than the mobile app. User-visible complaint: device list shows `make ★` labels but no richness — no card fields, no heartbeat, no access-scope chip, no runners list, no git badge. Mobile has all of that, plus whole feature surfaces (git, vibing, Hermes push, phone preview) that are either missing or orphaned on web.

This doc inventories the delta, organized by the user-named priorities, and calls out the cross-cutting visual primitives to mirror.

## 1 · Git (GitHub + GitLab) — **TOTAL GAP in web**

| Mobile (exists) | Web (missing) |
|---|---|
| `mobile/app/(tabs)/gitproviders.tsx` — connect/disconnect GitHub + GitLab, auto-detect from `gh`/`glab` tokens, manual token fallback | No OAuth connect UI anywhere under `web/app/` or `web/components/dashboard/` |
| `GitProviderSection` in `more.tsx` — branch name, clean/dirty with ✓/◯, ↑/↓ ahead/behind, modified/staged/untracked counts, last 10 commits | No repo browser, no branch picker, no commit list, no PR viewer |
| Inline actions: pull / push / stash / commit | Zero actions |
| Per-device badge "github" / "gitlab" inferred from the device's projects | Not surfaced on device rows |

`web/components/dashboard/OpsView.tsx` has a "Clone" sub-tab in the tab bar but no content — the nearest existing mount point. A `GitView` component + device-card badge is the right shape.

## 2 · Access control / Guest ↔ host — **mostly there, UX polish needed**

| Mobile | Web |
|---|---|
| `guests.tsx` two-tab layout (My guests / Join as guest) | `GuestsStatusView.tsx` — full |
| Invite flow with email/user-id toggle + lookup preview + **device-picker checkboxes** + project/runner scoping | Web has invite + scope but no device checkbox grid in one place |
| Access-scope chip on device cards: `OWNER` / `SHARED-SCOPED` / `SHARED-LEGACY` / `FEEDBACK-ONLY` | Chips exist on the guests screen but **not on the main device cards in the sidebar** |
| Large 6-char invite code display + per-recipient memo + share sheet | Account/merge page only — plain paste flow |
| Support/TeamViewer (`/support`) | `web/app/support/page.tsx` — solid |

Gap = move the access-scope chip onto every device card in the sidebar + center workspace, and put the device-picker-with-checkboxes into the invite flow.

## 3 · Vibing — **orphaned, needs to be the primary dashboard tab**

| Mobile | Web |
|---|---|
| `agent.tsx` Agent Mode: runner pills (Auto / claude-code / codex / aider / ollama), machine pills (multi-select), template pills (full/ship), max-parallel, goal textarea, live DAG with node status + placement reason | `web/app/hybrid/page.tsx` — **exists but unlinked from main dashboard**; basic planner/implementer pickers, work-dir, prompt, step list |
| `hybrid.tsx` planner + implementer + model text inputs, two-step Plan → Plan & Run, SSE live progress (step n/m, step title, retry count, per-step duration, final cost) | Hybrid page has step output but no cost, no DAG |
| Runner pill + machine pill pattern consistent across screens | Missing; single device dropdown only |
| Session list on composer (resume/stop/retry) | Minimal task sidebar; no session replay |

Key moves: (a) mount hybrid/agent as a real dashboard tab, not a standalone route; (b) replace model/runner text inputs with pill selectors (see `agent.tsx:150-172`, `hybrid.tsx:129-131`); (c) render the DAG (`agent.tsx` has nodes with kind/status/placement/error banners).

## 4 · Dev server + reload (IP-link flow) — **partial**

| Mobile | Web |
|---|---|
| `hotreload.tsx` + `DevPreview.tsx` — framework-icon card (📱⚛🦆▲⚡), port, hot-reload on/off, Hermes vs native install mode, target-worker picker | `PreviewPane.tsx` — iframe + auto-SSE reload |
| Live Metro/Expo/Flutter stdout tail (last ~6 lines, monospace) during startup + failure banner with stderr | No log tail, no failure banner |
| Explicit buttons: Open in Yaver · Reload · Shot · Stop · Retry | Reload is auto-only (SSE); **no manual reload button**, no stop, no screenshot |
| Mobile-worker target chip row | Target device picker exists |
| LAN-IP link shown prominently + copyable | Not surfaced as a chip |

Mirror the log-tail card + reload/stop buttons + an "Open URL / Copy link" chip next to the device pill.

## 5 · Deploy — web — **covered**

`OpsView.tsx` + `BuildsView.tsx` have framework detection, branch/healthcheck/auto-deploy toggle, webhook secret, rollback by deploy-id, uptime monitors, deploy preview. Parity with mobile is fine. Minor polish: result-toast summary ("Deployed to 2 targets · 3 pushes") matching mobile's `phone-project/[slug].tsx:deployToBoth`.

## 6 · Deploy — mobile (iOS / Android) — **partial**

| Mobile | Web |
|---|---|
| `apps.tsx` — Hermes-ready state chip (green/red/amber + timestamp), compile button, "Ship to TestFlight" / "Ship to Play Store" / "Flush to App" (Flutter LAN) / "Run on this phone" grouped buttons, macOS-only warning for iOS archives | `BuildsView.tsx` has TestFlight/PlayStore buttons but no Hermes-ready state chip, no macOS-only warning |
| `builds.tsx` — artifact list with status spinner, platform badge, MB/MB download progress, one-click install (iOS OTA manifest + Android APK direct) | Artifact list exists; no install action |

Gaps: Hermes build-state chip on project cards; install-to-phone action (obviously no-op on web, but needs the "Download IPA/APK" visible).

## 7 · Deploy — Hermes bundle push — **missing trigger UI in web**

Mobile has the full flow end-to-end: `apps.tsx` → build HBC → validate MD5 + BC96 → download → inject into native super-host. On web the user can't *trigger* a bundle push to their phone even though the agent endpoint exists (`/dev/build-native` + `cli/` npm package). Expected surface: a "Push to [phone name]" button next to each mobile project on the web Builds tab, with the same size-advisory + MD5-chip the mobile screen shows.

## 8 · Phone emulator in web — **iframe only, no device skin** (first step landed)

`web/components/dashboard/PreviewPane.tsx` was a plain `<iframe>`. The first pass of this initiative — committed as `web: phone-style device chrome on the preview pane` — adds:

- Device skin picker (iPhone 15 / iPhone SE / Pixel 8 / Pixel 8 Pro / Tablet / Desktop).
- Portrait / landscape toggle.
- Fit-scale inside the pane (transform: scale).
- Notch + home indicator for iOS skins, punch-hole for Pixel.
- LocalStorage persistence of the skin + orientation choice.
- Empty-phone idle state replacing the bare placeholder.

Still missing:

- MJPEG / video-frame stream from a remote phone (no agent endpoint for it yet).
- Touch gesture simulation / device-pixel-ratio accuracy.
- Screenshot gallery — the `Shot` button already fires the BlackBox `capture_screenshot` command, but the captured image is not surfaced back in the web pane.

---

## Cross-cutting patterns to mirror (not copy)

These are the mobile visual primitives that make the device/vibing cards feel alive and that the web workspace page doesn't have:

| Pattern | Mobile file | Web mount point |
|---|---|---|
| Status dot + heartbeat age (`online 2h ago`) | `devices.tsx:82-95` | Sidebar + hero list |
| Access-scope chip (color bg + label) | `devices.tsx:97-109` | Device cards |
| Runner/project scope badges (`ALL AGENTS` / `SOME AGENTS · claude, aider`) | `guests.tsx` throughout | Device cards (when shared) |
| Framework tag pill | `hotreload.tsx:629-636` | Project cards |
| Pill / segment buttons (runner, machine, template) | `agent.tsx:150-172` | Replaces every dropdown |
| Live SSE log tail (6 lines, monospace) | `hotreload.tsx:160-179` | Dev server card + autodev |
| Pending-action gate | `guests.tsx:580-615` | Guest invites page |
| Cost advisory line (`About to upload 12.5 MB`) | `phone-project/[slug].tsx` | Deploy buttons |

## Features entirely absent from web (not in any priority area)

- Relay server status / latency panel — `web/components/dashboard/RelayServerView.tsx` exists but is **never mounted**.
- Preferences tab — `PreferencesView.tsx` exists but is **never mounted** (speech provider, verbosity, key storage).
- Beacon / LAN autodiscovery hint on the "no devices" empty state.
- Mobile project scanner result (`/projects/mobile`) — mobile's `hotreload.tsx` scans home dir for RN/Expo/Flutter/Next/Vite projects; web never calls it.
- Two-factor challenge UI (web handles TOTP via a different page, but not the phone-initiated recovery flow).

## Connection-failure UX (out of band — user raised mid-audit)

When the web client can't reach the agent, the error card today is a single flat string: `Could not reach agent (direct or via relay)`. The agent's `/health` endpoint is unauthenticated and already returns `authExpired: true` when the agent's own Convex token is stale. The web client throws away both the per-relay status codes and the `authExpired` flag, so the user can't tell "box is down" from "box is up but needs `yaver auth`".

Minimum fix:

1. Capture per-attempt diagnostics in `agent-client.ts` (path, status, authExpired, error string).
2. Render them on the error card: per-relay row with status + badge (`auth expired`, `relay up / agent offline`, `network`).
3. Show a copy-able `yaver auth` command when `authExpired` is detected, plus an explanatory sentence.
4. (Follow-up) Expose a `/reauth/start` POST endpoint on the agent that accepts the user's current Convex session token and updates the local config without requiring shell access to the box. That closes the loop — re-auth directly from the web UI.

## Recommended implementation order

1. **Device-card rewrite** (priority 1 blocker for the rest): status dot + heartbeat age + access-scope chip + runner list + LAN-IP chip. This alone closes the visual gap in the workspace screenshot.
2. **Git tab** — mount `GitView` in the dashboard tab bar (OAuth connect, repo list, branch picker, commit list). Wire the Clone sub-tab in `OpsView` to it.
3. **Vibing tab** — promote `/hybrid` into the dashboard as a first-class tab, rebuild the composer with runner pills + machine pills + live DAG.
4. **Dev-server card upgrade** — log tail, reload/stop buttons, copyable LAN-URL chip.
5. **Hermes bundle push trigger** on web Builds tab.
6. **Emulator panel** — CSS device chrome + rotate/resize + screenshot + MJPEG stream. **First pass landed; MJPEG pending agent work.**
7. **Connection diagnostics + web-side re-auth** (raised mid-audit, blocks daily use).
