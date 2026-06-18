# AR-glasses dev — two first-class paths

Yaver supports a fully portable dev rig: AR glasses + a wireless foldable keyboard, with the Yaver mobile app as your "PC". Two equally-supported paths, both shipped via the same Yaver iOS and Android app — only the compute location differs.

```
                    ┌──────────────────────────────────────────┐
                    │            Yaver mobile app              │
                    │   (iOS and Android — feature parity)     │
                    └─────────┬───────────────────────┬────────┘
                              │                       │
                  ╔═══════════▼═══════════╗   ╔═══════▼════════════╗
                  ║  Path A — SSH/tmux    ║   ║  Path B — local    ║
                  ║  terminal mode        ║   ║  mobile-headless   ║
                  ║                       ║   ║                    ║
                  ║  Connects to a remote ║   ║  Spawns runners on ║
                  ║  Linux box (yours or  ║   ║  the host device   ║
                  ║  Yaver managed cloud) ║   ║  itself (Termux on ║
                  ║  and runs Claude Code ║   ║  Android / iSH on  ║
                  ║  + Codex + tmux there ║   ║  iOS / native      ║
                  ║                       ║   ║  runner pool)      ║
                  ╚═══════════════════════╝   ╚════════════════════╝
```

Both paths run **the same** coding agents (claude-code, codex, opencode — per `feedback_runners_always_dangerous`), use **the same** subscription-token-mirror auth (per `feedback_yaver_single_user_wrapper`), and render the same way in the Xreal glasses (because both are just the Yaver app on screen).

---

## Hardware kit (shared between both paths)

| Item | Approx | Notes |
|---|---|---|
| **Xreal One glasses** | $480 | 1080p × 50° FoV, plug-and-play DP Alt Mode, X1 chip anchors the virtual screen so head movement isn't fatiguing |
| **Foldable wireless keyboard** | $60–$100 | Logitech Keys-To-Go 2 / iClever BK06 / similar. Bluetooth, fits in a jacket pocket |
| **PD power bank** (~25 000 mAh) | $70 | DP-Alt-Mode drains host device ~2× normal; full-day operation needs top-up |
| Host device | — | Either iPhone 15+ (already own) OR Xreal Beam Pro (~$200) |

The same glasses + keyboard work with both host devices. You can buy one host now and add the other later.

---

## Path A — Remote dev via Yaver app's terminal mode

> "I have a remote Linux box (mine or Yaver managed) and want to use my iPhone / Beam Pro as the terminal."

### Topology

```
iPhone 15+ OR Beam Pro (USB-C → Xreal glasses, BT keyboard)
  └─ Yaver mobile app — terminal mode (SSH + mosh + tmux pass-through)
        │
        │  mosh over LTE/Wi-Fi (Yaver relay if dev box is NAT'd)
        ▼
Your remote dev host
  ├─ Option 1: self-hosted VM (any cloud — OVH/Vultr/Linode/DO/etc.)
  ├─ Option 2: Yaver managed cloud (`yaver_managed_cloud_onboarding`)
  └─ Option 3: home NUC + Yaver relay (`add_relay_server`)
        │
        Common stack:
        ├─ tmux session "main" (systemd-managed — always alive at boot)
        ├─ Claude Code CLI
        ├─ OpenAI Codex CLI
        └─ Yaver Go agent (ACL peer + relay endpoint)
```

### What's in the Yaver app for Path A

- **Built-in SSH + mosh client** — no third-party app like Blink Shell needed. Reads keys from the device keychain (FaceID/Touch ID on iOS, Android KeyStore on Android)
- **tmux integration** — auto-attaches to `main` session, surfaces tmux's window/pane state in the app chrome so you can swap windows by tap as well as `Ctrl-b 0/1/2/3`
- **Claude Code TUI rendering** — handles ink-based terminal UIs (per `feedback_runners_always_dangerous`); cursor/key handling tuned for AR-glasses-readable text
- **Push notifications** — long-running agents on the remote ping the same app when done, even if you're not looking at the glasses
- **Connection helper** — when the remote box is behind NAT (home NUC), Yaver relay tunnels in transparently. User sees the same SSH UX

### Hardware kits for Path A

| If you have... | Buy | Total add-on |
|---|---|---|
| iPhone 15+ | Glasses + keyboard + power bank | ~$640 |
| Nothing yet | Beam Pro + glasses + keyboard + power bank | ~$840 |

### Remote box choice

| Option | Pros | Cons |
|---|---|---|
| **Yaver managed cloud** | One CLI command; identity + billing already linked; zero-touch auth | Single SKU; bound to Yaver platform |
| **Self-hosted on any cloud** (OVH, Vultr, Linode, DO, Scaleway, Azure, GCP, AWS, Oracle Free Tier) | Total control; pick region for lowest LTE latency; choose spec | You operate billing + maintenance |
| **Home NUC + Yaver relay** | Zero monthly cost beyond electricity | Needs Yaver relay for reachability; home-internet SPOF |

**Minimum spec**: 2 vCPU / 4 GB / 40 GB. Recommended for parallel Claude Code + Codex + builds: 4 vCPU / 8 GB / 80 GB. ARM and x86 both work — `scripts/setup-remote-dev.sh` auto-detects.

### One-time setup

```bash
# On the remote box (any Linux)
scp scripts/setup-remote-dev.sh user@your-dev-host:/tmp/
ssh user@your-dev-host bash /tmp/setup-remote-dev.sh

# First-run auth (inside the persistent tmux session)
ssh user@your-dev-host
tmux a -t main
claude login           # device-code URL → open on phone Safari/Chrome
codex login            # same
yaver auth link start  # link this box as a Yaver agent identity
```

On the phone: open Yaver app → terminal mode → add SSH host → connect. From now on the glasses show your dev environment.

### Beach reality check for Path A

| Possible failure | Recovery |
|---|---|
| Lose cellular for 15 min | Mosh reconnects, tmux state preserved |
| Phone dies | Power bank → 15 min back on |
| Cloud box reboots | tmux-main.service auto-restarts at boot |
| Auth token expires | Re-run `claude login` in another tmux pane |
| Yaver platform outage | Plain-SSH falls back automatically; only the relay path breaks |

---

## Path B — Standalone host with Yaver mobile-headless

> "Just the Beam Pro (or iPhone) and glasses. No remote box. Yaver mobile app drives everything locally."

### Topology

```
Xreal Beam Pro (Android 14, $200) OR iPhone 15+ (USB-C → Xreal glasses, BT keyboard)
  └─ Yaver mobile app — mobile-headless mode
        ├─ Spawns coding-agent runners locally
        │   (claude-code · codex · opencode — same set as Path A)
        ├─ Subscription-OAuth-mirrored auth — Yaver copies your existing
        │   desktop tokens onto this device; no re-OAuth per device
        │   (per feedback_yaver_single_user_wrapper)
        ├─ Underlying runtime:
        │     Android → Termux (F-Droid build) — runners installed once
        │                via npm, Yaver spawns them in tmux there
        │     iOS    → iSH/embedded runner pool — Yaver provides the
        │                terminal environment in-app
        ├─ Built-in file browser, git verbs, agent-graph view
        └─ Same glasses-rendering as Path A — output is the Yaver app
```

### What's in the Yaver app for Path B

- **Local-runner spawn** — Yaver app starts Claude Code / Codex in the embedded terminal runtime. No SSH involved
- **Mobile-headless mode** — per `MOBILE_HEADLESS.md`, Yaver auto-detects tmux + attaches without `YAVER_TMUX_RUNNER` gating (per `feedback_tmux_opportunistic`)
- **Same auth model as Path A** — subscription token mirror from your existing desktop, never per-device OAuth, never API keys
- **Offline-capable editing + git** — works without network; only Claude/Codex API calls need internet
- **In-app file tree + commit/push/pull** — common git verbs handled in-app, Termux/iSH for advanced ops

### Why pick Path B over Path A

| Reason | Detail |
|---|---|
| No monthly cloud cost | $0/month after hardware purchase |
| Offline-friendly | Editing + git work without network; only API calls need internet |
| Single device | No SSH, no remote box to maintain, no port forwarding to debug |
| Cleaner separation | "phone for calls" stays separate from "computer for work" (if you use Beam Pro) |
| Stronger privacy | Code never leaves your device — only Claude/Codex API requests go out |

### Hardware kits for Path B

| If you have... | Buy | Total add-on |
|---|---|---|
| iPhone 15+ | Glasses + keyboard + power bank | ~$640 |
| Nothing yet | **Beam Pro** + glasses + keyboard + power bank | ~$840 |

The Beam Pro is the recommended host for Path B because:
- Dedicated battery (~5h) leaves your real phone charged for calls
- Hardware camera shutter, 3.5mm headphone jack, microSD slot
- More RAM/storage to spare for local builds than a phone
- Designed by Xreal specifically as the "computing companion" for the glasses

### One-time setup (Path B, Beam Pro)

1. Install **Yaver Android app** (Play Store or sideload)
2. On a desktop with Claude Code / Codex already authenticated, run `yaver auth link start` from the Beam Pro and approve on desktop — your subscription tokens mirror over
3. Install **Termux** from F-Droid (not Play Store — outdated). Yaver's mobile-headless mode auto-attaches to a tmux session here
4. In Termux, install the runners once: `pkg install nodejs git && npm i -g @anthropic-ai/claude-code @openai/codex`
5. Pair the foldable Bluetooth keyboard
6. Plug glasses into Beam Pro's USB-C → auto-mirrors the Yaver app
7. Open Yaver → start a task → it spawns Claude Code locally and surfaces the I/O in the glasses

### One-time setup (Path B, iPhone)

Same as Beam Pro setup but the underlying runtime is the Yaver app's embedded terminal pool (iOS doesn't have Termux). Yaver app provides the in-app terminal environment; runners are installed via the app's package manager UI, not a separate Termux install.

### Reality check for Path B

| Scenario | Result |
|---|---|
| Lose hotspot signal | Local editing continues. Claude API calls queue / fail until back online |
| Heavy compile (50K-file webpack) | Slow on 6 GB Beam Pro RAM; iPhone fares similarly. Acceptable for 30-min stints |
| Push code | Tiny SSH payload, ~1 min of LTE is enough |
| Lose Beam Pro / phone | Worse than losing a remote box — local repos go with it. **Mitigation: push to git often; Yaver's warm-mirror to managed cloud as backup** |
| Want voice prompts | Plug AirPods into Beam Pro's 3.5mm jack — far-field mic in the glasses is muffled |

---

## Path C — Vibe-coding hybrid (remote dev + on-device app under test)

When the thing you're **testing** is the Yaver mobile app itself (or another React Native / Expo app on the same phone), Path A and Path B blend. The development PC is at the **far end** of the relay, but the JS bundle being reloaded is **on the phone in your hand**.

### Topology

```
  Xreal glasses (USB-C DP Alt Mode)
        │ video out
  ┌─────┴───────────────────────────────────┐
  │  iPhone 15+ / Beam Pro                  │
  │  ┌────────────────────────────┐         │
  │  │ Yaver mobile app           │         │
  │  │  • glass-terminal: shell   │◀────────┼───── remote dev box
  │  │    mode → tmux+claude+codex│  relay  │      (claude code / codex /
  │  │  • glass-terminal: vibe    │  WS     │       tmux session stays
  │  │    bar → reload/push/dr    │         │       alive through reloads)
  │  └────────────┬───────────────┘         │
  │               │ Hermes/Metro fast-refresh         │
  │  ┌────────────▼───────────────┐         │
  │  │ Your app under test        │         │
  │  │  (also React Native)       │         │
  │  └────────────────────────────┘         │
  └─────────────────────────────────────────┘
       BT foldable keyboard ──────────┘
```

The shell websocket and tmux session never close during a reload — only the JS bundle of the app-under-test gets swapped.

### Voice in/out (works for all three paths)

`glass-terminal` has built-in STT + TTS aimed at the AR-glasses + paired-headset workflow. The architecture rule:

> **Voice happens at the edge. MCP carries text only.**

Why: the mic is next to the user's face (phone in pocket, headset paired). The MCP server may be a Yaver managed-cloud box thousands of kilometres away. Round-tripping audio through MCP would be slow, fragile, and waste bandwidth. The mobile app captures + transcribes locally, sends text through the agent loop, and speaks the text result locally. The same code path is used for Path A (text → remote dev box agent), Path B (text → on-phone runner), and Path C (text → either + Hermes reload fires from chip).

| Direction | Surface | Backend | Provider |
|---|---|---|---|
| STT (voice in) | 🎤 mic chip on the input row — tap once to start, tap again to send | `startRealtimeTranscribe()` in `mobile/src/lib/speech.ts` (streaming whisper.rn → partial-result subscriber updates the input field) | On-device whisper.cpp tiny model (~75 MB, one-time download) OR cloud (OpenAI gpt-4o-mini-transcribe, OpenRouter, Deepgram Nova-2, AssemblyAI) — user picks in Settings |
| TTS (voice out) | 🔊 toggle in the header — when on, every `model_text` event speaks itself | `speakText()` in `mobile/src/lib/speech.ts` | Device default (iOS/Android system TTS, zero-config) OR cloud (OpenAI gpt-4o-mini-tts, OpenRouter, configurable voice) |

Permissions baked into `mobile/app.json`: `NSMicrophoneUsageDescription`, `NSSpeechRecognitionUsageDescription`, Android `RECORD_AUDIO`. No additional grants needed at runtime beyond the first prompt.

What this looks like in glass-terminal:
- Glasses on, hands free, BT headset paired
- Tap mic → "reload the app and check the bundler status"
- Whisper streams the partial into the input as you speak
- Tap mic again → command flies into the agent loop (local or remote MCP)
- `🔊` is on → the response is read aloud through the headset
- tmux on the remote box stays untouched throughout

### What's in the Yaver app for Path C

The `glass-terminal` screen has a **vibe action bar** sitting above the prompt:

| Chip | What it does | Path |
|---|---|---|
| `🎯 target` | Pick which device the `⟳` chip reloads. Sticky select, persisted across app relaunches. Long-press to clear back to broadcast. | UI only (Phase 7) |
| `⟳ reload` | Trigger a Hermes/Metro fast-refresh on the app under test. No rebuild. | **Direct MCP** — POSTs `tools/call mobile_hermes_reload` to the selected agent's `/mcp` endpoint over the existing bearer-auth transport. ~500 ms. Falls back to the LLM-narrated path if the agent is older than 1.99.234 or the call fails. |
| `📦 push` | Push the latest code from the connected remote dev box to the app under test. | LLM-narrated — picks `wire_push` / `wireless_push` |
| `📊 status` | Summarise bundler / Hermes / dev-client reachability for the selected device. | LLM-narrated — `mobile_project_status` |
| `🩺 doctor` | Run `mobile_hermes_doctor` and surface action items. | LLM-narrated |

These chips do NOT share the agent-mode `busy` state and do NOT close the shell websocket — so a tmux session running `claude code` on the remote dev box stays untouched while you hit `⟳ reload` between edits.

### Cross-device targeting (Path-C extension)

Beam Pro in pocket + glasses + BT keyboard drives the dev loop. **iPhone on the desk** runs the Expo dev-client of the app under test. Tap `🎯 target` → pick `@iphone-on-desk` → tap `⟳ reload` → only the iPhone reloads. The Beam Pro's own Yaver app stays put.

How it works:
1. `⟳ reload` calls `mobile_hermes_reload` with `{ target_device_id: "<iphone-id>" }`.
2. Agent's `POST /dev/reload` receives the body, hits `BlackBoxManager.SendCommandToDevice(<iphone-id>, ...)` instead of `BroadcastCommand`.
3. Only the iPhone's BlackBox SSE listener receives the `reload` command → `DevSettings.reload()` → Hermes swaps the JS bundle.
4. Metro's bundler-level reload still fires unscoped (it's the bundler, not the SDK channel) — but that only affects clients connected to Metro as dev-clients, which the Beam Pro's Yaver app isn't.

### Workflow

1. Glasses on. BT keyboard out. iPhone (or Beam Pro) in your pocket.
2. Open Yaver app → `glass-terminal` route → `shell` mode → long-press the title → pick your remote dev box.
3. `tmux a -t main` to reattach; `claude code` is already running.
4. Edit `mobile/app/foo.tsx` via Claude Code on the remote.
5. Save → `⟳ reload` chip on the vibe bar → Hermes hot-reloads the app under test on the phone.
6. The shell pane on glasses still shows your tmux scrollback. Nothing was lost.

This is "yaver wireless push" but with the dev PC at the far end of a relay instead of on the same LAN, and the phone playing both *driver* and *device under test* simultaneously.

---

## How the three paths combine

You don't have to pick one. The Yaver mobile app supports **all three modes side-by-side** — same app, switchable inside `glass-terminal`:

- **Today** you might use Path A from your iPhone to a Yaver managed-cloud box for a heavy refactor
- **Tomorrow** you might use Path B on the Beam Pro during a flight for offline work
- **The day after** you might use Path A from the Beam Pro (not just the iPhone) to that same Yaver managed-cloud box
- **While building a feature in the mobile app itself** you slip into Path C — same shell session, vibe bar reloads the app under test without dropping tmux

The glasses, keyboard, power bank don't care which path you're on.

| Activity | Suggested path |
|---|---|
| Heavy stack work (Convex+Next+Mobile builds) | Path A — remote box has the cores |
| Offline editing on a plane | Path B — local Termux/iSH runner pool |
| "I want to be at the beach with the lightest kit" | Path B on Beam Pro — single device |
| "I want to be at the beach but already own a Yaver managed-cloud box" | Path A from iPhone — minimal new hardware |
| Quick voice-prompted asks while in glasses | Either — Yaver app handles voice the same way in both modes |
| Push code at midnight from your couch | Either — both pull a fresh git pull just fine |

---

## Yaver's role across both paths

| Capability | Path A | Path B |
|---|---|---|
| Mobile app | **Built-in terminal mode** — owns SSH + mosh + tmux pass-through, no third-party SSH client needed | **Mobile-headless driver** — spawns local runners, in-app file tree, git verbs |
| Subscription-OAuth token mirror | Per remote box, mirrored from desktop | Per Beam Pro/iPhone, mirrored from desktop |
| Push notifications | Long-running remote agents ping the same app | Long-running local agents ping the same app |
| Relay (NAT traversal) | Used when home-NUC remote needs it | Not needed |
| Coding agents | claude-code / codex / opencode in tmux on remote | Same runners, spawned in Termux (Android) or in-app pool (iOS) |
| Glasses rendering | Same Yaver UI mirrored via USB-C DP Alt Mode | Same — only the compute location differs |

This is the **same app, two modes**. iOS and Android builds keep feature parity (per `feedback_always_deploy_yaver` and `feedback_mobile_only_wire_push`).

---

## What ships in this repo

- `scripts/setup-remote-dev.sh` — idempotent bootstrap for the remote box in **Path A**
  - Installs tmux + mosh + Node 20 + Claude Code + Codex + base build tools
  - Writes a glasses-friendly `~/.tmux.conf`
  - Creates a `tmux-main.service` systemd unit so the "main" session is always alive at boot
  - Opens UFW UDP 60000-60010 for mosh
  - Auto-detects ARM64 vs x86_64
  - Sets a friendly MOTD describing the first-run auth ritual
  - **Host-agnostic** — works on Yaver managed cloud OR any self-hosted Linux VM

**Path B needs no shell script** — Yaver mobile app + Termux (Android) or in-app runtime (iOS) handles everything via the app's UI.

## Recovery scenarios (both paths)

| Scenario | Recovery |
|---|---|
| Glasses break / left behind | Phone/Beam Pro screen still works as a smaller window |
| Beam Pro or phone broken | Buy another, sign back into Yaver, re-mirror tokens, restore Termux home from git |
| Cloud provider outage (Path A only) | Yaver relay can fail over to a backup managed-cloud box. Plain-SSH-only setups need manual standby |
| Auth tokens stolen | `yaver_auth_factory_reset` + revoke Claude / Codex / GitHub PATs + rotate SSH keys |
| Both phone AND glasses lost | Offline yubikey recovery → cloud provider's web console → rotate keys + spin up emergency access |

## Hardware smoke checklist — first run on a Beam Pro

When the Beam Pro arrives, run this checklist once. Anything that breaks here is a real bug, not a config issue.

- [ ] **USB-C DP Alt Mode** — plug Xreal One into Beam Pro USB-C, glasses show the Android UI mirrored
- [ ] **BT pairing** — foldable BT keyboard pairs in Android settings, typing into a TextInput works
- [ ] **Mic permission** — first tap of the 🎤 chip in glass-terminal prompts for `RECORD_AUDIO`, accept, partials appear in the input
- [ ] **TTS** — toggle `🔊` in the header on; submit a prompt; output is spoken through paired BT headset
- [ ] **Shell mode** — long-press header title, pick the remote dev box, `tmux a -t main` works
- [ ] **Direct-MCP reload (single device)** — vibe bar `⟳ reload` fires; output buffer shows `mobile_hermes_reload → js_only` within 1 s
- [ ] **Cross-device reload** — pair an iPhone with the Yaver app + an Expo dev-client connected to Metro; pick the iPhone via `🎯 target`; tap `⟳`; only the iPhone reloads, Beam Pro Yaver app stays put
- [ ] **Auto-reconnect** — pull the BT headset away to break audio; shell websocket reconnects within 16 s without manual intervention
- [ ] **SSE survives mirroring** — confirm BlackBox SSE stream doesn't drop when the screen mirrors over USB-C DP (Android sometimes throttles background-ish HTTP). Drop tolerance for the reload chip is < 5 s.
- [ ] **Power** — 25 000 mAh PD bank drives glasses + phone for 4+ hours of typical use

If `🎯 target` shows no devices, the iPhone hasn't paired with the Yaver agent yet — use the Devices tab first.

## See also

- `REMOTE_MCP_HERMES_RELOAD_PLAN.md` — full audit + phase tracker for the remote-MCP reload feature (Phases 2/3/7 shipped, Phase 4 is the smoke above)
- `REMOTE_WORKER.md` — Yaver remote worker architecture (Path A backbone)
- `MOBILE_HEADLESS.md` — Yaver headless mobile mode (Path B backbone)
- `SETUP.md` — general Yaver setup
- `feedback_yaver_single_user_wrapper` — Yaver mirrors subscription tokens, never re-OAuths per device
- `feedback_no_api_keys_subscription_only` — never propose `ANTHROPIC_API_KEY` / `OPENAI_API_KEY`
- `feedback_runners_always_dangerous` — supported runners = claude-code / codex / opencode only
- `feedback_tmux_opportunistic` — Yaver auto-attaches to existing tmux, no env-var gating
- Yaver MCP tools (`mcp__yaver__*`) — full inventory
