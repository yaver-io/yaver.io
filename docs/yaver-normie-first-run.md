# Yaver normie first-run: Claude Code + Yaver MCP + mobile app

Status: analysis + first two fixes shipped (2026-06-27). Source is truth —
this doc points at code; if they disagree, fix the code reference here.

## The scenario

A non-developer ("normie") has:

1. **Claude Code** (or Codex / opencode) running in a terminal.
2. Registered the Yaver MCP server with one line — no global install, `npx`
   pulls it on first run:
   ```
   claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp
   codex  mcp add yaver -- npx -y yaver-cli yaver-mcp
   npx -y -p yaver-cli yaver mcp setup opencode
   ```
3. Installed the **Yaver mobile app** and signed in with OAuth.

He has NOT run `yaver serve` or `yaver auth` in a separate terminal, and he
shouldn't have to — everything bootstraps from inside the agent chat.

## The one idea that makes or breaks the trial: one agent, many surfaces

Yaver ships no model of its own. When a task runs, Yaver literally
`exec`s the same binary the user already has — `claude` / `codex` /
`opencode` (`tasks.go` `builtinRunners`, spawn at `tasks.go:2560`+). So there
is **one agent driven from three surfaces**: the terminal he types into, the
phone (Yaver spawns a runner), and the web dashboard.

**Auth is shared for free** (`tasks.go` `taskEnv` → `provider_keys.go`
`runnerProviderEnv`): if Claude Code works in his terminal, every
Yaver-spawned task inherits the same subscription/key automatically — no
second login. This is the biggest hidden win and must be said out loud in
onboarding, or the normie hunts for "Yaver AI settings" that don't exist.

## First-run choreography (grounded)

**Terminal half** (inside Claude Code, via MCP):
- `yaver_lazy_setup` → returns a device-code sign-in URL + a `next_action`
  the agent speaks (`mcp_lazy_setup.go`).
- Human taps the URL, signs in (Apple/Google/GitHub/Microsoft). The tool
  polls; on success `authFinalizeToken` saves the token, **best-effort forks
  `yaver serve`**, and auto-registers MCP in the other runners
  (`mcp_auth_tools.go`).

**Mobile half** (auto, zero taps beyond sign-in):
- Phone signs in same account → listens on UDP **19837** for the desktop's
  LAN beacon (token-hash fingerprint, only same-user devices match) →
  **auto-pairs** (X25519/passkey), no QR, no IP typed
  (`mobile/src/lib/beacon.ts`, `mobile/src/context/DeviceContext.tsx`).
- Desktop shows **CONNECTED**; Projects tab lists his repos; Tasks tab shows
  "Tap + to create your first task."

The handshake is two invisible conditions: **same OAuth account** AND **the
daemon actually serving**.

## The two coding modes he'll use

- **Mode A — at his desk (default).** He types into Claude Code; Yaver MCP
  gives it superpowers (preview on phone, deploy, pull feedback). The phone is
  a second screen/remote. No Yaver runner is spawned — Claude Code itself is
  the agent calling yaver tools.
- **Mode B — away.** From the phone: tap **+ → device + runner + model → "fix
  the button" → send** (`mobile/app/(tabs)/tasks.tsx`). Yaver spawns the
  runner on the desk (`tasks.go` runner selection: explicit → per-device
  default → global default), streams output back by **polling `/tasks`**.

Honesty: a plain `claude` in his terminal is **not** mirrored to the phone —
tmux adoption is strictly manual (`tmux.go`; no auto-detect on serve). Only
Yaver-spawned tasks (and explicitly adopted tmux sessions) appear on the
phone.

## Deploy his app to the phone, from Claude Code (Hermes)

The conversion moment. Fully wired over MCP. The normie says "put my app on my
phone"; the agent calls the chain (`mcp_tools.go`, dispatch in `httpserver.go`):

```
mobile_project_status   → RN/Expo? deps installed? Hermes available?
mobile_project_prepare  → auto-install deps if missing
mobile_hermes_doctor    → blockers + native-module compatibility
mobile_project_build    → compile the Hermes bundle Yaver loads (/dev/build-native)
mobile_hermes_reload    → bundle loads INTO the Yaver container (ExpoReactNativeFactory)
```

His app appears running **inside the Yaver app** — no TestFlight, no Xcode, no
App Store. Bundle validated (HBC magic `0x1F1903C1`).

### Fix shipped: `mobile_deploy_to_phone` (one verb)

`mcp_mobile_deploy.go` adds a single doctor-driven verb that chains all five
steps, stops at the first blocker, and returns one `next_action` sentence plus
a per-step trace. `plan_only=true` runs only the fast checks
(status/prepare/doctor) and hands off the slow build/reload as explicit next
calls — for agents with short tool timeouts. `device_id` routes to an owned
remote box (proxies the per-step endpoints); empty = this machine (the normie
case). Prefer this verb over calling the five tools by hand.

## Where a normie gets stuck (honest gaps)

1. **Silent daemon-fork failure** (`mcp_auth_tools.go` best-effort start). He
   sees "Signed in!" but the phone forever shows "Set Up Your Desktop." **#1
   first-run killer.**
2. **Cellular with no relay.** Auto-discovery is LAN-beacon-first; off-LAN
   needs a relay tunnel. A normie won't know which.
3. **Two empty states** — Tasks "Pair your computer" vs Devices "Set Up Your
   Desktop" — same problem, two screens.
4. **No first-timer wizard on mobile** — lands on the Reload tab.
5. **Polled, truncated output** (~last 10k chars) can feel laggy.
6. **Runner-not-installed for Mode B** fails opaquely.
7. **First Hermes build is slow** with little progress feedback.

### Fix shipped: daemon-health gate in `yaver_lazy_setup`

`mcp_lazy_setup.go` now verifies the daemon is actually answering on
`127.0.0.1:18080` before reporting "all set". If it isn't, it makes one
`safeStartDaemon()` attempt, waits up to 6s for the port to bind, and — if
still down — returns `daemon_serving:false` with a `next_action` that tells the
human to run `yaver serve` manually instead of a false success. Probe accepts
any HTTP response (proves the process is listening, which is what mobile
discovery needs), so a token mismatch doesn't masquerade as "not serving".

## Remaining recommendations (not yet built)

- Chain the handoff in copy: terminal `next_action` → "open the phone app, same
  account"; mobile → "Found 'your Mac' — pairing…".
- Auto-enable relay on first pair so cellular works out of the box (gap #2).
- Decide on terminal mirroring: auto-adopt the calling session, or stop
  implying it streams to the phone (gap, Mode A vs B).
- One first-run wizard on mobile that detects the desktop and offers the first
  task / first deploy.

## Code pointers

| Concern | Where |
|---|---|
| One-shot auth + daemon-health gate | `desktop/agent/mcp_lazy_setup.go` (`yaverLazySetup`, `applySignedIn`, `daemonServing`) |
| Device-code OAuth + finalize | `desktop/agent/mcp_auth_tools.go` |
| One-verb phone deploy | `desktop/agent/mcp_mobile_deploy.go` (`mobileDeployToPhone`) |
| Hermes sub-steps | `desktop/agent/mobile_project_http.go`, `mcp_mobile_hermes_doctor.go`, `mcp_mobile_hermes_reload.go` |
| Mobile discovery / pairing | `mobile/src/lib/beacon.ts`, `mobile/src/context/DeviceContext.tsx` |
| Runner spawn + auth inheritance | `desktop/agent/tasks.go`, `provider_keys.go` |
