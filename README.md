# Yaver

[![Tests](https://github.com/kivanccakmak/yaver.io/actions/workflows/test-suite.yml/badge.svg)](https://github.com/kivanccakmak/yaver.io/actions/workflows/test-suite.yml)
[![License: AGPL%20v3](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0.en.html)

Docs: [CI setup](CI.md) · [Test SDK](docs/yaver-test-sdk.md)

**Build mobile apps from your phone. Keep the backend local until you decide otherwise.**

> **Using an AI agent?** If a coding agent (Claude Code, Codex, Cursor, Aider, …) is reading this
> because the human asked it to *"install yaver"* or *"set up yaver"*, stop here and
> fetch **[https://yaver.io/llms.txt](https://yaver.io/llms.txt)** — that file is the
> canonical, machine-readable install + configure guide. It covers headless / WSL / SSH,
> the resumable device-code flow, and exactly what to surface back to the human. Do not
> scrape the rest of this README as a substitute.


Yaver's short-term product target is phone-first app and backend creation, then promotion to your own dev machine, then optional promotion to Yaver Cloud. The core claim is narrow on purpose:

`phone sandbox -> your dev machine -> Yaver Cloud`

What is real in the repo today:

- Hermes reload from Linux, WSL, macOS, or a remote host into the Yaver mobile app
- local phone-project sandboxing with SQLite-backed data, schema, auth personas, seed data, and CRUD
- portable export/import/push of a phone project to another `yaver serve` agent
- MCP and CLI surfaces for phone project export, import, and push
- local-first runtime API for third-party apps using per-project tokens
- Linux install surfaces including `apt`, AppImage, tarballs, `.deb`, `.rpm`, and Homebrew CLI

What is still incomplete:

- the default one-tap monorepo scaffold for phone-created projects
- more polish on the AI prompt-to-project scaffold
- a fully proven end-to-end App Store / TestFlight release loop from this machine

## The Clear Story

Yaver's primary loop is:

1. Start on the phone.
2. Vibe-code the app and backend from the phone.
3. Run the first backend tier in the phone sandbox.
4. Grow the same project onto your dev machine, your own server, or Yaver Cloud.
5. Keep Supabase, Convex, Postgres, Turso, Firebase, and similar systems as escape hatches, not the default destination.

What is first-class today:

- **Hermes reload from Linux / WSL / remote host to iPhone or Android** through the Yaver mobile app
- **Mobile-first backend sandbox** with schema, auth personas, seed data, CRUD, and local persistence
- **Promotion to your own hardware** via `yaver serve` on a Mac, Linux box, Pi, VPS, or other reachable machine
- **Promotion to Yaver Cloud** via the same portable bundle and the same `yaver serve` binary
- **Containerized export** for running the promoted backend on your own cloud with Docker
- **Escape routes** to systems like Supabase and Convex as secondary trust signals

What is not fully finished yet:

- **Phone-first monorepo scaffolding** as the default one-tap `init` path
- **Prompt scaffold quality polish** for the phone-created full-stack starter

The headline path is Yaver-native:

`phone sandbox -> your dev machine / your cloud / Yaver Cloud`

Everything else is there so the user knows they can leave later.

For a WSL-based developer, that means:

1. Run `yaver serve` or the Go agent tooling on WSL/Linux.
2. Build the Hermes bundle on that host.
3. Open the app inside the Yaver mobile app on the phone.
4. Use native-install paths only when you actually need a full store/native build.

## The Four Pieces of Yaver

Yaver is built for solo developers and small teams who ship from anywhere. It has four distinct pieces — each exists for a specific reason:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                                                                             │
│  📱 MOBILE APP (yaver.io)                    🔧 DESKTOP AGENT (yaver)      │
│  App Store / Play Store                       brew install yaver            │
│                                                                             │
│  Your remote control for everything.          The brain on your dev machine.│
│  Send tasks to AI agents, test apps on        Runs AI agents (Claude Code,  │
│  real hardware, hot reload, visual QA.        Codex, Aider), serves P2P     │
│  Native container for RN apps (not WebView).  connections, manages builds,  │
│  Works from beach, coffee shop, anywhere.     MCP server with 473 tools.    │
│                                                                             │
│  ─────────────────────────────────────────────────────────────────────────  │
│                                                                             │
│  📦 NPM BOOTSTRAP (`yaver-cli`)              🐛 FEEDBACK SDK               │
│  npm install -g yaver-cli                     yaver feedback setup          │
│                                                                             │
│  One npm install, two surfaces:               Embed in YOUR app during dev. │
│  `yaver serve` bootstraps the Go agent;       Shake to report bugs with     │
│  `yaver push` handles third-party RN apps.    screenshots + voice. Black    │
│  Bundles JS, compiles Hermes bytecode,        box flight recorder streams   │
│  pushes over Wi-Fi. ~4 seconds.               all events to your AI agent.  │
│  No project modifications required.           React Native, Flutter, Web.   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Why four pieces?** The mobile app and desktop agent are the core — phone talks to your machine P2P. The npm package (`yaver-cli`) is the umbrella install point: it gives you the `yaver` command, bootstraps the agent binary, and covers the React Native push flow. The Feedback SDK still embeds inside *your* app, but the install path is now routed through the same `yaver` command (`yaver feedback setup` / `yaver sdk add feedback`) instead of sending developers straight to raw package-manager commands first.

**You might use:**
- Just the **mobile app + agent** — control AI agents from your phone, hot reload any framework
- Add the **npm bootstrap package** — `yaver serve` for the agent, `yaver push` for native RN testing
- Add the **Feedback SDK** — embed a debug console in your app, shake to report bugs to your AI agent
- All four together — the full loop: code on machine, push to device, test, report bugs, AI fixes, repeat

## Key Features

- **Push to Device** — Real-device testing in ~4 seconds. No TestFlight. 40+ native modules.
- **Visual QA Loop** — Shake to report. AI sees screenshot, writes fix, hot reloads.
- **Autonomous Testing** — Agent navigates screens, catches crashes, fixes, repeats.
- **P2P Encrypted** — Code flows directly between devices. No cloud.
- **Any AI Agent** — Claude Code, Codex, Aider, Ollama, Goose, Amp, or any CLI tool.
- **Hot Reload** — Expo, Flutter, Vite, Next.js over P2P.
- **473 MCP Tools** — Docker, K8s, git, CI/CD, databases.
- **Feedback SDKs** — Debug console for React Native, Flutter, Web.
- **Session Transfer** — Move AI sessions between machines.
- **Chained Tasks** — Queue a whole feature: "build landing page, add Stripe, deploy." Tasks execute sequentially, next starts when previous succeeds.
- **Auto-Retry** — Failed task? Agent retries with error context. Only pings you after 3 failures.
- **Ship It Button** — One tap to deploy. Agent detects your project (Cloudflare, Vercel, TestFlight, Play Store, Fly.io, etc.) and ships.
- **Morning Summary** — Daily digest at 9am: "3 tasks done, landing page live, 2 tests failing." Via Telegram, Discord, Slack, or email.
- **Live Terminal Stream** — Watch Claude Code work in real-time from your phone via SSE. Full terminal output, not just status updates.
- **Set-and-Forget Autodev** — `yaver autodev <project>` forks itself as a detached, session-leader child so the kick loop survives terminal close, ssh disconnect, or laptop lid. Kicks fire on a timer (5 min in lite, 30 s in burst), refill ideas when the checklist empties (`--auto-ideas`, default 999), optionally use a hardening preset (`--harden security|memory|perf|quality|all`), pin a roof theme (`--prompt`), work on a dedicated branch (`--auto-branch`), and ship to every shippable surface at the end (`--deploy auto` covers TestFlight, Play Store, Convex, Vercel). Re-attach the live tail any time with `yaver stream autodev:<loop>`.
- **Autoinit (cached project context)** — `yaver autoinit <project>` writes a project `init.md` (stack, layout, conventions, build/test/deploy commands, recent direction) that every later autodev / autoideas / autotest kick reads as cached context, so Claude doesn't re-grep the repo from scratch every kick. Each successful run auto-appends to the file's history block so the next session knows what was just shipped. Available as CLI, HTTP (`POST /autoinit/start`, `GET /autoinit/status`), and MCP (`autoinit_start`, `autoinit_status`).
- **Autoideas (overnight idea generator)** — `yaver autoideas <project>` runs a long-lived loop that asks the AI for fresh single-PR-sized improvement ideas every tick and appends them as `- [ ] <title>` lines to `ideas.md`. Mobile / web shows them as checkboxes; pick the ones you want and the daemon fires an `autodev` run with the curated subset as `--remained`. Generation continues in parallel with implementation. Same flag set as autodev (`--hours`, `--lite/--heavy`, `--prompt`, `--harden`, `--engine`, `--hybrid`).
- **Live Chat Stream (terminal + mobile + web)** — autodev publishes structured events (`yaver_say`, `runner_action`, `runner_text`, `runner_result`) to `/streams/autodev:<loop>` so any client renders the run as a chat: yaver's voice on one side, the AI's on the other, tool uses inline. Generic across runners (Claude / Codex / Aider / Ollama). Mobile Auto Dev tab has a Chat section; CLI uses `yaver stream <name>` for ANSI-colored bubbles.
- **Always Native, Never WebView** — React Native apps always load via Hermes bytecode into a native bridge with TurboModules + Fabric. WebView is never used for app loading.
- **Task Scheduling** — Cron-like scheduling.
- **Notifications** — Telegram, Discord, Slack, Teams, PagerDuty, Opsgenie, Linear, Jira, email.
- **CI/CD Webhooks** — GitHub Actions, GitLab CI triggers.
- **Git Providers** — Browse and clone repos from phone.
- **SDKs** — Go, Python, JS/TS, Flutter/Dart, C.

## Vibe Coding from Anywhere

Built for the solo developer who ships from a beach in Thailand. Dump tasks from your phone, let your machine do the work, check results over coffee.

```
Morning at the beach:
  1. Open Yaver → Queue 5 tasks: "add dark mode", "fix login bug",
     "write payment API", "add validation", "deploy to Cloudflare"
  2. Agent chains them — each starts when the previous succeeds
  3. If one fails, agent retries with the error context (up to 3x)
  4. You get a push: "4/5 done, payment API retry 2/3 in progress"

Lunchtime:
  5. Check the Summary — "4 tasks done ($0.32), 1 running"
  6. Open the live terminal stream — watch Claude Code typing in real-time
  7. Tap "Ship It" — one tap, agent deploys to Cloudflare Workers

Next morning:
  8. Morning summary notification: "5 tasks completed, site live at myapp.com"
```

### Chained Tasks (queue a whole feature)

```bash
# From the mobile app or API:
POST /chain
{
  "tasks": [
    { "title": "Build a landing page with pricing section" },
    { "title": "Add Stripe checkout for $10/mo plan" },
    { "title": "Write tests for the payment flow" },
    { "title": "Deploy to Cloudflare Workers" }
  ],
  "autoRetry": true
}
# → First task starts immediately. Each subsequent task starts when the previous completes.
# → If any task fails, it auto-retries with error context (up to 3x).
# → Chain stops if a task fails all retries.
```

### Ship It (one-tap deploy)

The agent auto-detects your project type and offers the right deploy target:

| Detected | Deploy Target | Command |
|----------|--------------|---------|
| `wrangler.toml` | Cloudflare Workers | `npm run deploy` |
| `vercel.json` | Vercel | `npx vercel --prod` |
| `netlify.toml` | Netlify | `npx netlify deploy --prod` |
| `ios/` + deploy script | TestFlight | `scripts/deploy-testflight.sh` |
| `android/` + deploy script | Google Play | `scripts/deploy-playstore.sh` |
| `convex/` | Convex | `npx convex deploy` |
| `firebase.json` | Firebase | `npx firebase deploy` |
| `fly.toml` | Fly.io | `fly deploy` |
| `docker-compose.yml` | Docker Compose | `docker compose up -d --build` |

### Morning Summary

Daily digest at 9am via all configured notification channels:

```
☀️ Morning Summary

📊 5 tasks: 4 completed, 1 failed ($0.47)

✅ Build landing page with pricing (34s)
✅ Add Stripe checkout (89s)
✅ Write payment tests (22s)
✅ Deploy to Cloudflare (15s)
❌ Add dark mode (failed after 3 retries)
```

## Always Native, Never WebView

React Native apps pushed to the Yaver phone app **always load natively** — never in a WebView. The pipeline:

1. **Bundle**: Metro bundles your JS into a single file
2. **Compile**: `hermesc` compiles JS → Hermes bytecode (HBC, version 96, from RN 0.81.5)
3. **Validate**: Both CLI and phone validate HBC magic (`0x1F1903C1`) and BC version match
4. **Load**: Phone creates a native bridge via `ExpoReactNativeFactory` with full New Architecture support
5. **Run**: Your app runs with TurboModules, Fabric, and JSI — same as if built with Xcode

The `safeReloadBridge` sequence invalidates the old bridge, waits for HadesGC cleanup (up to 3s weak-reference poll), then creates a fresh bridge. This prevents SIGABRT crashes from GC touching freed memory.

**Why this matters**: WebView-based "containers" (like some dev tools) can't access native modules, have different performance characteristics, and break any app using `TurboModuleRegistry.getEnforcing()`. Yaver's native bridge gives your app the same runtime as a production build.

## Full Pipeline from Anywhere

```
Developer at the beach? No problem.

1. Open Yaver on your phone
2. Switch to your repo: "switch to my-flutter-app"
3. Chat with your AI agent — it writes code on your home machine
4. Build: yaver build flutter apk
5. Test: yaver test unit
6. Deploy: artifact transfers P2P to your phone — tap to install

Or run the full pipeline:
  yaver pipeline --test --deploy p2p

Skip GitHub Actions. Skip TestFlight queues. Your build goes straight to your phone.
```

**Key capabilities:**

- **Repo switching** — `yaver repo switch my-app` auto-discovers git repos under `~/` and changes the agent's working directory. No manual path typing.
- **Auto-detect testing** — `yaver test unit` detects your framework (Flutter, Jest, pytest, Go test, Cargo, XCTest, Espresso, Playwright, Cypress, Maestro) and runs the right command. Pass/fail counts stream to your phone.
- **yaver-test-sdk** — Embedded E2E test runner that replaces Playwright + Percy + axe-core. Drop YAML specs under `yaver-tests/`, run `yaver test run`, and every test executes on your own hardware for $0/mo. See [`docs/yaver-test-sdk.md`](docs/yaver-test-sdk.md).
- **Full pipeline** — `yaver pipeline --test --deploy p2p` builds, tests, and deploys in one command. Stops on test failure by default.
- **Platform-aware builds** — When you request a build from your phone, the agent knows your platform (iOS or Android) and builds the right artifact (APK/AAB for Android, IPA for iOS).
- **Expo support** — `yaver build expo-android` and `yaver build expo-ios` for Expo-managed projects. Runs `eas build` or `expo prebuild` + native build depending on your setup.
- **Auto vault sync** — When your phone connects to the agent, keys and signing credentials from the P2P encrypted vault sync automatically. No manual key management on each connect.
- **Store uploads** — `yaver build push testflight` and `yaver build push playstore` upload directly to app stores. Credentials stay in the vault.

## Self-Host a Relay Server

Install a relay on any VPS with one command:

```bash
curl -fsSL https://yaver.io/install-relay.sh | sudo bash -s -- \
  --domain relay.example.com \
  --password your-secret
```

This installs Docker, deploys the relay, sets up nginx + Let's Encrypt SSL, and configures auto-updates. The relay is a pass-through proxy — it never stores your data. All connections are encrypted via QUIC (TLS 1.3).

## How It Works

```
┌─────────────┐     HTTP         ┌──────────────┐    QUIC tunnel    ┌──────────────┐
│  Mobile App │─────────────────►│ Relay Server │◄──────────────────│ Desktop Agent│
│ (React Native)  short-lived    │  (optional)  │  persistent       │  (Go CLI)    │
│  Wi-Fi/5G   │  HTTP requests   │  public IP   │  outbound conn    │  behind NAT  │
└──────┬──────┘                  └──────┬───────┘                   └──────┬───────┘
       │                                │                                  │
       │  Auth only                     │  Platform config                 │  Register device
       ▼                                ▼                                  ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        Convex Backend                                       │
│  Auth + Peer Discovery + Platform Config (relay server list)                │
│  Apple / Google / Microsoft Sign-In                                         │
└─────────────────────────────────────────────────────────────────────────────┘
```

No code, task data, or AI output ever touches our servers. The relay is a pass-through proxy. When you're on the same network, traffic goes direct.

## Quick Start

```bash
# Install (pick one)
brew install kivanccakmak/yaver/yaver          # macOS / Linux
scoop bucket add yaver https://github.com/kivanccakmak/scoop-yaver && scoop install yaver  # Windows
winget install Yaver.Yaver                      # Windows (Winget)
curl -fsSL https://yaver.io/install.sh | sh     # Quick install (macOS / Linux)
irm https://yaver.io/install.ps1 | iex          # Quick install (Windows PowerShell)

# Sign in — GUI machine opens a browser, headless (Pi / VPS / SSH-only / Docker) uses --headless
yaver auth                 # opens browser automatically
yaver auth --headless      # prints a URL + short code you approve from your phone
# `yaver auth` starts the agent automatically if needed
#
# Supported sign-in providers (all 6 linkable to the same account):
#   Google · Apple · Microsoft/O365 · GitHub · GitLab · email/password
# See https://yaver.io/download#headless-auth for the full flow.

# Adopt a project (one-time, ~3 min) — caches stack/layout/conventions into init.md
cd ~/code/my-project
yaver autoinit my-project

# Generate ideas overnight (long-lived, detached, survives terminal close)
yaver autoideas my-project --hours 8 --engine codex
# Pick ideas later via mobile / web (checkboxes) → triggers autodev on the picks

# Or go straight to autodev — picks the next remained.md item every kick
yaver autodev my-project --hours 8 --model sonnet      # cheap default
yaver autodev my-project --hours 1 --model opus --max-iterations 1  # high-stakes one-shot
yaver autodev my-project --planner claude:opus --implementer codex  # hybrid: premium plan, cheap impl

# Watch the live chat from any terminal (or the mobile Auto Dev tab)
yaver stream autodev:my-project-autodev
```

### Headless machines (Mac mini upstairs, Hetzner VPS, Linux box over SSH)

You don't need a browser on the headless machine. Pick one:

```bash
# Option A — fresh OAuth via QR (Apple / GitHub / GitLab / Google / Microsoft)
yaver auth --headless
# → prints a QR code pointing at yaver.io/auth/device
# → scan it with your phone camera, sign in with whatever provider,
#   the headless agent polls and receives the token.

# Option B — copy an existing signed-in token over the P2P relay
# (fastest path — no OAuth dance at all)
yaver auth pair
# → prints a 6-char pairing code + QR code.
# On a machine that's already signed in (your laptop), run:
yaver auth send <PAIR-CODE> <target-url-from-the-qr>
# Or scan the QR from the Yaver mobile app → More → Pair device.
```

Both paths work the same for Apple, GitHub, GitLab, Google, and
Microsoft — the web sign-in page at `yaver.io/auth/device` accepts all
five and hands
the resulting token back through the device-code flow.

Headless reachability defaults:

- Linux and macOS now block OS sleep while `yaver serve` is running
- WSL cannot block Windows host sleep from inside the distro
- real pre-login macOS boot requires one extra privileged step:
  `sudo yaver serve --install-launchd-daemon`
- WSL reboot recovery is best-effort: Yaver installs a shell helper and
  prefers a Windows Scheduled Task when available, but the Windows host
  still needs sleep disabled for unattended remote use

### All Installation Methods

| Method | Command |
|--------|---------|
| **Homebrew** | `brew install kivanccakmak/yaver/yaver` |
| **Scoop** | `scoop bucket add yaver https://github.com/kivanccakmak/scoop-yaver && scoop install yaver` |
| **Winget** | `winget install Yaver.Yaver` |
| **Chocolatey** | `choco install yaver` |
| **AUR** | `git clone https://github.com/kivanccakmak/aur-yaver.git && cd aur-yaver && makepkg -si` |
| **apt** (Debian/Ubuntu) | `echo "deb [arch=$(dpkg --print-architecture) trusted=yes] https://cdn.jsdelivr.net/gh/kivanccakmak/apt-yaver@main stable main" \| sudo tee /etc/apt/sources.list.d/yaver.list && sudo apt update && sudo apt install yaver` |
| **dnf/rpm** (Fedora/RHEL) | Download `yaver_<version>_x86_64.rpm` from [releases](https://github.com/kivanccakmak/yaver.io/releases) and `sudo rpm -i yaver_*.rpm` (or `sudo dnf install ./yaver_*.rpm`) |
| **AppImage** | Download from [download page](https://yaver.io/download), `chmod +x Yaver-*.AppImage && ./Yaver-*.AppImage` |
| **Tarball** | `curl -fsSL https://yaver.io/install.sh \| sh` — auto-detects arch, downloads the right tarball, installs to `~/.local/bin/yaver` |
| **npm bootstrap** | `npm install -g yaver-cli` — fastest start; installs a `yaver` command and covers both `yaver serve` and `yaver push` |
| **Nix** | `nix run github:kivanccakmak/yaver.io` |
| **Docker** (multi-arch: amd64, arm64) | `docker pull kivanccakmak/yaver-cli:latest` · also on `ghcr.io/kivanccakmak/yaver.io/cli:latest` |
| **Raspberry Pi / ARM64 SBC** | `curl -fsSL https://yaver.io/install.sh \| sh` — then `yaver install pi-dev-node && yaver auth && yaver serve --install-systemd`. Pi 4 (4+ GB) runs `yaver serve` 24/7; hermesc arm64 compiles RN bundles natively. See [download page](https://yaver.io/download#raspi). |
| **Raspberry Pi 5 Image** | Download `yaver-pi5-devnode-arm64.img.xz` from the [download page](https://yaver.io/download#raspi), flash it to a Pi 5, boot, pair from Yaver mobile, and finish first-boot provisioning there. |
| **curl** | `curl -fsSL https://yaver.io/install.sh \| sh` |
| **PowerShell** | `irm https://yaver.io/install.ps1 \| iex` |
| **Binary** | Download from [releases](https://github.com/kivanccakmak/yaver.io/releases) |

### Desktop App (GUI)

Download the desktop app with full GUI from the [download page](https://yaver.io/download) — available as DMG (macOS), installer (Windows), deb/AppImage (Linux).

The download page also exposes the new **Raspberry Pi 5 dev-node image** through the same Convex-backed public artifact pipeline as the CLI/download assets.
The image build itself is Linux-native; from macOS use `./scripts/build-pi-image.sh --docker` once Docker is running, or let the `pi-image/vX.Y.Z` GitHub workflow build and publish it.
The Pi image is intentionally a **hybrid appliance**: it bakes in the OS image, the `yaver` binary, first-boot provisioning, cloud-init, and systemd services, then uses first boot to install the heavier dev/backend stack (`ollama`, `aider`, `opencode`, TDD tools, `sqlite3`, `vercel`, `convex`, PostgreSQL, Redis, Supabase, MQTT). That keeps the downloadable image smaller and easier to update while still shipping a full dev-node product.

## Always-Up Mode (Boots Without Auth)

`yaver serve` is designed to start and stay reachable even on a brand-new install with no token. The HTTP server comes up in **bootstrap mode** the moment you run it for the first time, and on the primary always-on targets it also registers itself with the OS auto-start system.

Primary always-on targets:

- **macOS** via LaunchAgent by default, or LaunchDaemon for real headless pre-login boot
- **Linux** via systemd user service + linger
- **Windows** via Scheduled Task

WSL support is different:

- WSL is supported for the React Native / Hermes daily loop and headless auth
- WSL is **not** the same as native Linux for reboot persistence
- Yaver does **not** install a native systemd auto-start service inside WSL
- Yaver installs a WSL startup helper and prefers a Windows Scheduled Task when available
- WSL cannot stop the Windows host from sleeping; unattended remote use still requires Windows power settings and ideally Tailscale on Windows itself
- native Linux and macOS still have the stronger always-on path

```bash
# Brand-new install. No `yaver auth` needed yet.
yaver serve
# → Yaver agent started in bootstrap mode (PID …, port 18080).
# → Registered as macOS LaunchAgent (will auto-start after login).
#
# This machine has no auth token yet. The agent is up and waiting.
# Open the Yaver mobile app (already signed in) on the same Wi-Fi —
# the box will appear as 'needs auth', tap it to pair.
```

`yaver status` reflects the bootstrap state instead of bailing:

```
Yaver:    v1.85.0
Auth:     ● not signed in
Agent:    ● running (bootstrap mode, port 18080)
Host:     mac-mini.local
Mode:     bootstrap — waiting for a phone to pair
Auto-start: ● installed (will run on login/boot)
```

The bootstrap HTTP surface only mounts the four endpoints needed to receive a token — `/health`, `/info`, `/auth/pair/{info,submit}`, and `/auth/recover`. Everything else (tasks, vault, exec, dev server) is gated behind a successful pairing.

### Two Ways to Pair From the Mobile App

| Path | When | How |
|------|------|-----|
| **LAN beacon** | Box and phone on the same Wi-Fi | Bootstrap mode broadcasts a UDP beacon every 3s. The mobile app's beacon listener picks it up automatically and shows it in **More → Pair device** with a one-tap "adopt this machine" button. |
| **Remote re-auth (host-only)** | Box is on a remote network reachable via Tailscale, Cloudflare Tunnel, or the Yaver relay, AND your phone has previously paired with it before | The phone POSTs to `/auth/recover` with your Convex Bearer token. The agent calls `convex /devices/owner-by-hardware` with its hardware fingerprint. If Convex says you're the registered owner, the recovery flow proceeds. No pre-shared secret to remember — your Convex identity IS the host check. |

The mobile app automatically picks the right path based on whether the device is in your device list. Guests can never trigger the recovery flow on a host machine, even if they know the relay URL — the host check happens server-side in Convex against the original `userId` that registered the hardware fingerprint.

### Survives Reboots

The first `yaver serve` writes the OS-native auto-start descriptor:

| OS | What gets installed |
|----|---------------------|
| **macOS** | Default: `~/Library/LaunchAgents/io.yaver.agent.plist` (RunAtLoad + KeepAlive, starts after login). For a real headless Mac mini that must boot before login: `sudo yaver serve --install-launchd-daemon`, which installs `/Library/LaunchDaemons/io.yaver.agent.plist`. |
| **Linux** | `~/.config/systemd/user/yaver.service` + `loginctl enable-linger` so the unit runs without an interactive login. |
| **WSL** | No native systemd service. Yaver installs a WSL startup helper and prefers a Windows Scheduled Task when available; Windows host sleep still has to be handled in Windows settings. |
| **Windows** | A scheduled task that fires on user login. |

After the first install the agent comes back automatically on native Linux and Windows, and on macOS after login via the default LaunchAgent. For a truly headless macOS box that must come back before login, install the LaunchDaemon once. On WSL, keep the expectation narrower: the daily dev flow is supported and Yaver can install the helper + Windows-login path, but native Linux/macOS still provide the stronger always-on behavior.

## Visual Feedback Loop

Test your build on your real device, record bugs visually, and the AI agent fixes them.

```
You test the app → Record screen + voice → AI agent sees the recording → Fixes the bugs → Rebuilds → Repeat
```

**Three runtime modes** (user selects at runtime from within their app):

| Mode | What happens | Best for |
|------|-------------|----------|
| **Full Interactive** | Screen + voice stream live to agent. Agent's vision model detects bugs in real-time. Hot reload pushes fixes as you speak. Say "make this bigger" and it happens. | Active development, quick iterations |
| **Semi Interactive** | Screen + voice stream live. Agent comments on what it sees but doesn't auto-fix. Say "fix it now" or "keep in mind for later". | Code review, discussion, QA |
| **Post Mode** | Record everything offline. No streaming. Compress and submit when done. Agent processes the full session afterwards. | Slow connections, detailed QA, batch reports |

**Agent Commentary Levels** (0-10): Controls how proactive the agent is. Level 0 = silent. Level 5 = comments on obvious issues. Level 10 = comments on everything it sees (layout, performance, accessibility). Like pair programming where the AI watches over your shoulder.

### Feedback SDKs

Embed in your app during development. The SDK provides device discovery, connection UI, screen recording, voice annotation, and P2P upload — all in a single package. Disabled automatically in production builds.

**Preferred install flow:**

```bash
npm install -g yaver-cli

# Then, inside the project you want to wire up:
yaver feedback setup

# Or be explicit:
yaver sdk add feedback --platform web
yaver sdk add feedback --platform react-native
yaver sdk add feedback --platform flutter
```

**Manual fallback:**

```bash
# Web (any framework: React, Vue, Svelte, vanilla JS)
npm install yaver-feedback-web

# React Native
npm install yaver-feedback-react-native

# Flutter
flutter pub add yaver_feedback
```

**Quick start (Web):**
```typescript
import { YaverFeedback } from 'yaver-feedback-web';

if (process.env.NODE_ENV === 'development') {
  YaverFeedback.init({ trigger: 'floating-button' });
  // That's it. A "Y" button appears. Click to record bugs.
  // Auto-discovers your Yaver agent on the LAN.
}
```

**Quick start (React Native, 0.5+):**
```tsx
import { YaverFeedback, FeedbackModal } from 'yaver-feedback-react-native';

// 0.5 is zero-config: no agentUrl, no authToken needed.
// The SDK ships its own login screen (Apple / Google / GitHub / GitLab /
// Microsoft via device-code flow, plus email + password) and a remote-
// machine picker that lists the user's own dev boxes plus guest-shared
// machines. Works LAN-direct and over Convex/relay off-LAN so Hermes
// bundle reloads keep working on cell data.
if (__DEV__) {
  YaverFeedback.init({ trigger: 'shake' });
}

// In your root component:
<FeedbackModal />  // Embeds login + machine-picker modals automatically
```

**Quick start (Flutter):**
```dart
import 'package:yaver_feedback/yaver_feedback.dart';

void main() {
  if (kDebugMode) {
    YaverFeedback.init(FeedbackConfig(
      trigger: FeedbackTrigger.floatingButton,
      mode: FeedbackMode.narrated,
      agentCommentaryLevel: 5,
    ));
  }
  runApp(MyApp());
}

// Add the floating button:
Stack(children: [child, const YaverFeedbackButton()])
```

**What each SDK includes:**
- Device discovery — auto-finds your Yaver agent on the LAN
- Connection UI — URL input, connect button, status indicator
- Screen recording — ReplayKit (iOS), MediaProjection (Android), getDisplayMedia (Web)
- Voice annotation — microphone recording synced to timeline
- Screenshot capture — tap to annotate at any moment
- P2P upload — multipart POST to agent, works through relay
- Three runtime modes — user selects live/semi/post at runtime
- Agent commentary — chat-like view of agent's observations
- Voice commands — "fix this now", "push to TestFlight", "run the tests"
- Auto-disabled in production — only active in `__DEV__` / development mode

**CLI commands:**
```bash
yaver feedback list              # List bug reports from device testing
yaver feedback show <id>         # View timeline, transcript, screenshots
yaver feedback fix <id>          # AI agent creates a fix task from the report
yaver feedback delete <id>       # Delete a report
```

**Dogfooding:** Yaver's own mobile app embeds its own feedback SDK. We develop Yaver with Yaver.

## QA Testing Workflow

Combine push-to-device with the Feedback SDK for a complete QA loop:

```
1. Push to device:     yaver push
2. Test on real phone: tap around, find bugs
3. Report bug:         shake phone → screenshot + voice → sent to AI agent
4. AI fixes it:        agent sees screenshot, reads stack trace, writes fix
5. Re-push:            yaver push → fix on device in ~4s
6. Repeat
```

No TestFlight queues. No Play Store reviews. Real-device testing in seconds. Works with any AI agent (Claude Code, Codex, Aider, Ollama).

## MCP Integration

Yaver implements the Model Context Protocol (MCP) with 473 tools. Connect from Claude Desktop, Cursor, VS Code, Windsurf, Zed, or any MCP-compatible client.

### One-Command Setup

```bash
yaver mcp setup claude       # Claude Desktop
yaver mcp setup claude-code  # Claude Code user MCP config
yaver mcp setup cursor       # Cursor
yaver mcp setup vscode       # VS Code
yaver mcp setup windsurf     # Windsurf
yaver mcp setup zed          # Zed
yaver mcp setup show         # Show config JSON (copy/paste manually)
```

The repo also ships registry metadata in [`server.json`](server.json) and [`glama.json`](glama.json) so Yaver can be indexed by the official MCP Registry and Glama.

### Manual Setup — Claude Desktop

Add to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "yaver": {
      "command": "yaver",
      "args": ["mcp"]
    }
  }
}
```

For Claude Code, Yaver can register itself through the Claude CLI instead of editing files manually:

```bash
claude mcp add --scope user yaver -- /path/to/yaver mcp
```

`yaver auth` and `yaver serve` now try to do this automatically when `claude` is on `PATH`.

### Network MCP (HTTP) — Remote / Claude Web UI

```bash
yaver mcp --mode http --port 18090
```

Connect from any MCP client at `http://your-machine:18090/mcp`.

### GitHub Action

Trigger AI tasks from CI/CD:

```yaml
- uses: kivanccakmak/yaver.io@main
  with:
    agent-url: ${{ secrets.YAVER_AGENT_URL }}
    webhook-secret: ${{ secrets.YAVER_WEBHOOK_SECRET }}
    prompt: "Review this PR and suggest improvements"
    runner: claude
```

### Available MCP Tools

| Category | Tools | Count |
|----------|-------|-------|
| **Docker** | ps, logs, exec, build, push, pull, prune, stats, inspect, compose, networks, volumes | 23 |
| **Kubernetes** | pods, logs, describe, get, apply, exec, top, events, contexts, namespaces | 11 |
| **Terraform** | plan, apply, state, output, init, validate | 6 |
| **Helm** | list, status, values, search, repos, history | 6 |
| **Git** | info, stash, blame, reflog, branches, tags, log advanced, shortlog, stats | 13 |
| **Compilers** | gcc, clang, clang-tidy, clang-format, objdump, nm, binary size | 8 |
| **Rust/Cargo** | build, test, clippy, fmt, doc, bench, tree, update, audit, check, add/remove | 14 |
| **Go** | build, test (race/cover), vet, mod tidy/graph/why, generate, staticcheck, vulncheck | 11 |
| **Python** | pytest (coverage/markers), ruff (check/format/fix), mypy, black, pip-compile, uv | 6 |
| **Node/TS** | npm run, tsc, eslint (--fix), prettier, biome (check/format/lint) | 5 |
| **Make/CMake** | targets, run, clean, configure, build, test, install | 7 |
| **Static Analysis** | cppcheck, shellcheck, hadolint, semgrep, bandit, gosec, trivy | 10+ |
| **Profiling** | valgrind (memcheck/callgrind/massif), perf, strace, ltrace, go pprof | 10+ |
| **Testing** | run_tests (auto-detect), lint, format_code, type_check, benchmark | 4 |
| **Dependencies** | outdated, audit, list — npm/pip/cargo/go auto-detect | 3 |
| **Package Registries** | npm, PyPI, crates.io, Go modules, pub.dev, Homebrew, RubyGems, Maven, NuGet, Docker Hub | 24 |
| **GitHub + GitLab** | PRs, issues, CI, releases, stars, trending, MRs, pipelines | 10 |
| **Platforms** | Supabase, Convex, Cloudflare (Workers/Pages/R2/D1/KV), Netlify, Firebase, Fly.io, Railway | 33 |
| **Database** | query + schema (SQLite, Postgres, MySQL, Redis) | 2 |
| **Network** | tcpdump, tshark, nmap, netcat, port scan, arp, traceroute, mtr, curl timings | 18 |
| **Linux Sysadmin** | dmesg, lsmod, modprobe, systemctl, journalctl, ufw, iptables, df, du, lsblk, tree, top, ps, vmstat | 30 |
| **Smart Home** | Home Assistant, Philips Hue, Shelly, Sonos, Nanoleaf, Elgato, Tasmota, Govee | 20 |
| **Mobile Dev** | App Store, TestFlight, Play Store, Xcode, Gradle, Flutter, Expo, CocoaPods | 25 |
| **Daily Utils** | JWT decode, epoch, cron explain, subnet calc, fake data, domain check, color, QR | 13 |
| **Finance** | stocks, crypto, currency exchange | 3 |
| **Location** | EV charging, restaurants, hotels, geocode, directions, weather | 9 |
| **Productivity** | standup, changelog, gist, badges, gitignore, license, invite | 13 |
| **Desktop** | notify, volume, music, TTS, timer, calculator, clipboard | 26 |
| **Core** | tasks, sessions, tmux, scheduling, email, notifications, ACL, MCP peers | 67 |

See [MCP Integration Guide](https://yaver.io/docs/mcp) for full documentation.

## Web Search MCP Tool

Yaver ships a built-in `web_search` MCP tool so any connected AI agent (Claude Code, Codex, Aider, ...) can ground its output in current information — competitor research, library docs, error messages, news — without each agent needing its own search integration.

| Provider | Cost | Setup |
|----------|------|-------|
| **DuckDuckGo** (default) | Free, no key | Works out of the box |
| **Google** | Paid (free tier 100 q/day) | `export GOOGLE_CSE_KEY=… GOOGLE_CSE_CX=…` (Programmable Search Engine) |
| **Bing** | Paid | `export BING_API_KEY=…` (Azure Bing Search v7) |

Set `provider: "auto"` and Yaver picks the best available backend (Google → Bing → DuckDuckGo). Used directly by `yaver handoff autodev` for market-gap research during proactive-mode handoffs.

## Pass Session to Yaver (Handoff)

Hand off an in-progress AI session — Claude Code, Codex, Aider, anything — to Yaver, and let Yaver's autodev loop finish the remaining work autonomously. Works on the current machine, on a remote dev box, with hybrid (planner + cheap local implementer), or with a single arbitrary runner.

```bash
# Default: claude-code finishes the work locally
yaver handoff

# Cheap: planner + local implementer (Aider + Ollama/Qwen)
yaver handoff --engine hybrid

# Specific runner
yaver handoff --engine runner --runner aider
yaver handoff --engine runner --runner ollama:qwen2.5-coder:14b

# Hand a specific Yaver task or session file
yaver handoff --from <taskId>
yaver handoff --from ~/.claude/sessions/<uuid>.jsonl

# Hand off to a remote dev machine
yaver handoff --to my-mac-mini --engine hybrid

# Add focus, set caps
yaver handoff --message "finish the failing tests first" --max-kicks 50 --deadline 3600
```

**From inside an AI agent.** Any agent connected to Yaver's MCP server can call the `session_handoff` tool — say "pass session to yaver" and the agent invokes it. The tool returns `exitNow: true` plus a sentinel file path, signalling the agent to terminate cleanly. Yaver writes `~/.yaver/handoff/<loopName>.json` (and a stable `latest.json`) so external agents that can't be force-killed can poll for the takeover and exit on their own.

**What Yaver does on handoff:**

1. Exports the source session to a `TransferBundle` (conversation turns + agent-specific state files).
2. Imports it into the target machine's TaskManager.
3. Optionally stops the source Yaver task.
4. Builds a develop-mode autodev loop with the chosen engine/runner. The resume prompt is synthesised from the bundle context plus pending items in the todo list plus any `--message`.
5. Writes the sentinel and kicks the loop immediately.

**Engine choices:**

| Engine | Runner | When to use |
|--------|--------|-------------|
| `claude` (default) | `claude-code` | Highest quality, frontier model end-to-end |
| `hybrid` | planner=claude, implementer=aider+ollama | 80–95% cheaper on feature loops |
| `runner` | any (`aider`, `codex`, `ollama:<model>`, …) | You know exactly which runner you want |

**Surfaces:** CLI (`yaver handoff`), MCP tool (`session_handoff`), HTTP (`POST /session/handoff`). Same arguments across all three.

**Hard takeover (vs cooperative).** Yaver doesn't just write a sentinel — it actually terminates the calling AI process. Caller PID is resolved in this order: explicit `--caller-pid` / `caller_pid` arg → stdio MCP parent PID (auto-set when Claude Code/Desktop spawns `yaver mcp`) → HTTP MCP loopback peer port via `lsof`. After the response is sent, Yaver sends SIGTERM and SIGKILLs 5s later if the process is still alive. Non-loopback HTTP clients are never killed.

**Load + duration knobs (mirror `yaver autodev`).** `--load lite` (default) stretches kicks to one every 5min and respects the dev's Claude/Codex 5-hour windows. `--load burst` runs every 30s up to 200 iterations/day. `--hours 8` caps each individual kick at 8h so a runaway prompt can't burn the whole budget.

**Verify takeover:** `yaver handoff status` prints the most recent sentinel + the live loop's iteration count and last summary.

**Full autodev parity.** Everything `yaver autodev` exposes is also a handoff flag, so a session takeover can be configured exactly like a from-scratch autodev run:

| Flag | Effect |
|------|--------|
| `--prompt "..."` | Explicit focus prompt (replaces the auto-resume prompt) |
| `--target web\|ios-sim\|android-emu` | Loop target (default: auto-detect from workdir) |
| `--branch <name>` / `--auto-branch` | Ship to named branch / `autodev/<loop>-<YYYYMMDD>` |
| `--deploy` | Default = ship to **all** configured platforms. Disable with `--deploy false`/`no`/`0`/`none`. Restrict to one platform with `testflight`/`playstore`/`web`. |
| `--notify` | Mobile notification when the loop ends |
| `--no-autotest` | Skip interleaved regression test pass |
| `--auto-ideas N` | Cap on idea-refill batches when checklist runs dry |
| `--remained <path>` | Pull next item from a `remained.md` checklist |
| `--lite` / `--heavy` | Shortcuts for `--load lite/burst` |
| `--engine hybrid` / `--hybrid` | Cheap planner+local-implementer mode |

## Security Sandbox

The command sandbox is enabled by default and blocks dangerous operations:

- **Filesystem destruction**: `rm -rf /`, `rm -rf ~`, etc.
- **Encryption/ransomware**: bulk encryption of home/root
- **Privilege escalation**: `sudo`, `su`, `doas` (unless allowed)
- **Disk manipulation**: `mkfs`, `fdisk`, `dd` to block devices
- **Network exfiltration**: `curl|bash`, piping sensitive files
- **System compromise**: overwriting `/etc/passwd`, disabling services

### Configuration

```json
// ~/.yaver/config.json
{
  "sandbox": {
    "enabled": true,
    "allow_sudo": false,
    "blocked_commands": ["terraform destroy", "kubectl delete namespace"],
    "allowed_paths": ["/home/user/projects"],
    "max_output_size_mb": 100
  }
}
```

```bash
yaver config set sandbox.allow-sudo true    # Allow sudo
yaver config set sandbox.enabled false      # Disable sandbox (not recommended)
```

## Multi-User Support

Multiple users can share the same machine (e.g. shared GPU server with Ollama). Each user runs their own agent:

```bash
# User A
yaver auth && yaver serve --port 18080

# User B
yaver auth && yaver serve --port 18081
```

Each agent instance has:
- Separate auth token and user ID
- Isolated task store (`~/.yaver/tasks.json`)
- Own sandbox configuration
- Independent relay connections
- Auth-aware LAN beacon (only same-user devices discover each other)

## Guest Access

Share your machine with anyone — no team or subscription needed. Invite by email, they accept from the Yaver app. Guests can run tasks and use dev server but cannot access shell, vault, or sessions.

```bash
# Invite a guest
yaver guests invite cousin@gmail.com
# → Invite code: K7WP3N (share this if they sign up with a different email)

# Configure guest limits
yaver guests config cousin@gmail.com limit=3600 mode=scheduled
yaver guests config cousin@gmail.com runners=claude,aider

# View guest usage
yaver guests usage

# List all guests
yaver guests list

# Revoke access
yaver guests remove cousin@gmail.com
```

**Guest config options:**

| Setting | Values | Default | Description |
|---------|--------|---------|-------------|
| `limit` | seconds/day | unlimited | Daily task-seconds cap (e.g. `3600` = 1 hour/day) |
| `mode` | `always`, `idle-only`, `scheduled` | `always` | When the guest can use the machine |
| `runners` | comma-separated | all | Which AI runners the guest can use |

**How it works:**
1. Host invites via CLI, mobile app, or MCP (`guest_invite` tool)
2. Guest signs in to Yaver app with any OAuth (Apple, Google, Microsoft)
3. Guest accepts via email match or 6-character invite code
4. Host's devices appear in guest's device list
5. Max 5 guests per host, invitations expire in 2 days

Config (limits, runners, usage mode) syncs via Convex. Project access is managed P2P on each agent.

## Container Sandbox (Optional)

Run AI agent tasks inside Docker containers for full filesystem isolation. Optional and disabled by default — the default mode runs tasks directly on the host.

```bash
# Build the sandbox image (one-time, ~3 min)
yaver sandbox build

# Enable for guests only (security isolation)
yaver serve --containerize-guests

# Enable for all tasks (clean build environments)
yaver serve --containerize-host

# Check status
yaver sandbox status
```

**What's in the container:** Node.js, Python, Go, Rust, Java, Ruby, Claude Code, Aider, Expo CLI, Wrangler, and common build tools. Build caches (npm, Gradle, Cargo, Go modules) persist across tasks via Docker volumes.

**Project-specific containers:** Place a `Dockerfile.yaver` in your project root for custom toolchains. The agent auto-detects and builds it.

**Extra host mounts** (e.g. Android SDK): add to `~/.yaver/config.json`:
```json
{
  "container_mounts": ["/opt/android-sdk:/opt/android-sdk:ro"]
}
```

**Note:** Xcode/xcodebuild requires macOS and cannot run in Docker. iOS builds must use direct execution (non-containerized). Android builds via Gradle work fully inside containers.

## Hot Reload — Dev Server to Phone

Start a dev server on your machine and preview the app on your phone in real time — all through the P2P channel. Works on any network (Wi-Fi, 4G, behind NAT).

```bash
# From the Yaver mobile app: tap a project → Open App
# Or from CLI:
yaver dev start --framework expo     # Expo / React Native
yaver dev start --framework flutter  # Flutter
yaver dev start --framework vite     # Vite
yaver dev start --framework nextjs   # Next.js
```

### Linux / WSL / Remote iPhone Workflow

For React Native / Expo, this is a first-class path:

- Run `yaver` on Linux, WSL, or a remote box
- Pair your iPhone with the Yaver mobile app
- Start Metro on the host
- Tap `Open in Yaver` on the phone
- Yaver builds a Hermes bundle on the host and pushes it into the Yaver iPhone app

This means your daily iPhone dev loop does not need Xcode or a Mac. The iPhone behaves like a real device attached to your remote workflow, not like a simulator tied to a local Mac.

What still needs macOS:

- building a standalone native iOS binary
- code signing / provisioning
- App Store / TestFlight shipping

What does not need macOS:

- React Native / Expo JS iteration
- Metro-based hot reload on a real iPhone through Yaver
- relay / Tailscale / remote-box workflows

If you are on Linux or WSL, Yaver should use the Hermes bundle path for iPhone work rather than `xcodebuild`.

### Mobile-First Backend Continuum

Yaver is not just a phone-to-screen bridge. For the phone-project flow, the
phone can be the first backend tier.

The same portable project bundle can move across three targets:

| Tier | What runs it | Typical use |
|------|---------------|-------------|
| **Phone sandbox** | Yaver mobile app | first CRUD loop, offline prototyping, quick demos |
| **Your dev machine / your own host** | `yaver serve` on Mac, Linux, WSL-adjacent box, Pi, VPS, or remote machine | real-device testing, staging, privacy-sensitive self-hosting |
| **Yaver Cloud** | the same `yaver serve` behind the `cloud/` stack | managed deployment with zero-ops setup |

The promotion unit is the same portable manifest every time:

- schema
- auth personas
- seed data
- optional live SQLite rows
- generated SQL
- app spec and related metadata

This is the intended full-stack vibe-coding loop:

1. Create the project from the phone.
2. Prompt or edit the app and backend from the phone.
3. Run it locally in the phone sandbox.
4. Promote it to your own machine or cloud when it needs to grow.
5. Export or migrate only if you want an escape hatch later.

### Containerized Backend Export

If you want the phone-created backend to land on your own server with Docker,
use the phone export / push containerization path:

```bash
yaver phone export --containerize --include-data my-todos
yaver phone push --to https://your-box.example.com --containerize my-todos
```

The exported bundle can include:

- `Dockerfile`
- `docker-compose.yml`
- `.env.example`
- `.dockerignore`

That gives you a short path from phone sandbox to your own VM, Hetzner box, or
other Docker-capable host without changing runtimes.

### Monorepo Position

The product direction is phone-first full-stack development with a monorepo:

- mobile app
- backend
- shared schema/types
- deploy/export path from the same project root

That monorepo bootstrap story is a priority, but the one-tap repo scaffolder is
still in progress. Today the core backend continuum and promote/export path
exist first; monorepo automation sits on top of that.

### WSL To iPhone Quickstart

If your code lives in WSL and you want real iPhone reload, the daily loop is:

1. Install Yaver mobile on the phone.
2. Run `yaver serve` inside WSL.
3. Pair the phone with that agent.
4. Open the Expo / React Native project from Yaver.
5. Tap `Open in Yaver`.
6. Let Yaver build Metro + Hermes on the WSL host and load the bundle inside the phone app.

Important boundary:

- WSL is supported for development and phone testing
- WSL is not the primary always-on deployment target for Yaver itself
- if you want the machine to survive power loss and come back without touching a terminal, prefer native Linux or macOS
- if you stay on WSL, use Yaver's WSL startup helper and Windows Scheduled Task path
- also disable Windows sleep; WSL itself cannot keep the Windows host awake

The important rule is:

- **WSL iPhone reload = Hermes bundle into Yaver mobile**
- **WSL iPhone reload != `xcodebuild`**
- **WSL reboot persistence uses a helper path, not native Linux systemd**
- **WSL unattended remote use depends on Windows power settings**

Command-first version:

```bash
brew install kivanccakmak/yaver/yaver
yaver auth
yaver serve
```

Optional: force iPhone work to stay on bundle mode explicitly:

```bash
yaver mcp call set_ios_install_method '{"method":"bundle"}'
```

Then from the phone:

- select the paired machine
- select the project
- tap `Open in Yaver`

What should happen:

- Metro runs on the WSL host
- Hermes bundle builds on the WSL host
- the bundle is pushed to the phone
- the app runs inside Yaver on the iPhone

If your cousin only remembers one line, make it this:

> **WSL -> Hermes bundle -> Yaver mobile app**

This is the intended path for projects like `sfmg` when they are Expo / React Native apps.

Contributor workflow:

1. contributor clones `sfmg` and edits source with Claude Code
2. contributor runs `yaver serve`
3. contributor opens the project from the Yaver phone app
4. if the project is source-only, Yaver now shows `Compile Hermes`
5. contributor taps `Open in Yaver` to test on the phone inside the Yaver container
6. contributor commits and pushes
7. maintainer deploys the real TestFlight build later from the Mac/Xcode path

That means "never built before" is not a blocker for the daily contributor loop. Yaver can detect:

- source-only project that still needs its first Hermes compile
- previously compiled project that is ready to open
- last Hermes build failed and should be rebuilt after fixing the error

Troubleshooting shortcut:

- if the system tries to do a native iOS install on WSL, the mode is wrong
- on WSL the resolved iOS install method should be `bundle`
- missing native-module support is a container compatibility issue, not a WSL issue

Full guide: [docs/wsl-ios-hermes-quickstart.md](docs/wsl-ios-hermes-quickstart.md)

### Do I Need To Modify My Project?

Usually, no.

For the normal Yaver agent flow (`yaver` running on Linux, WSL, macOS, or a remote host):

- You do not need to inject the npm bootstrap package into the app
- You do not need to add the Feedback SDK just to open the app in Yaver
- Yaver starts Metro, builds the Hermes bundle, and loads it into the Yaver phone app

Use the npm package when:

- you want direct push-to-device workflows without the full agent
- you want compatibility analysis against Yaver's native module manifest
- you want watch-mode push from a terminal with `yaver push --watch`

Use the Feedback SDK when:

- you want shake-to-report bug capture inside your own app
- you want remote reload commands sent into your own app process
- you want black-box event streaming and AI fix context

In short:

- `yaver` agent + mobile app = enough for Hermes reload into Yaver on iPhone/Android
- `yaver-cli` = npm distribution name for the unified bootstrap package
- Feedback SDK = optional in-app debug/reload/reporting workflow
- the phone UI now exposes `Compile Hermes`, `Rebuild Hermes`, and `Open in Yaver` as separate steps for source-only third-party apps

What can still block success:

- unsupported native modules not present in the Yaver host container
- React Native / Hermes version mismatch for direct push workflows
- apps that depend on a custom native module outside Yaver's shipped manifest

### Open App — dynamic dispatch (iOS)

The Yaver mobile app's **Open App** button dispatches dynamically based on the connection mode — **never a WebView**. Third-party React Native apps always load natively:

| Connection | What runs | Outcome |
|------------|-----------|---------|
| **iOS + same Wi-Fi on macOS** (direct LAN) | `xcodebuild build` (auto-detected `.xcworkspace` / `.xcodeproj` + scheme) inside `./ios/` with `-allowProvisioningUpdates`, then `xcrun devicectl device install app` + `xcrun devicectl device process launch` | App is installed + launched on the real device the same way Xcode would do it manually — fastest full-native path when Xcode is available. |
| **iOS + cellular / relay** | `/dev/build-native` runs the framework's bundler (`expo export:embed` or `react-native bundle`), compiles with embedded `hermesc` (BC96 from RN 0.81.5), ships the validated HBC over the P2P channel, phone loads it into the Yaver super-host via `YaverBundleLoader` (New Arch guest bridge with TurboModules + Fabric) | App runs *inside* Yaver with its full JS. Works over 4G / relay / anything. |
| **iOS + Linux / WSL / remote host** | Same Hermes HBC push path as relay mode | The normal non-macOS workflow. Develop anywhere, hot reload on a real iPhone, no Xcode in the daily loop. |
| **Android** | Hermes HBC push into the Yaver super-host (same path as iOS relay) | Single path — Android doesn't need a separate native install branch. |

The dispatch lives in `mobile/app/(tabs)/apps.tsx`'s `handleOpen` + `handleTapProject`; the LAN native build uses the `PlatformXcodeDeviceInstall` build platform in `desktop/agent/builds.go`; and `desktop/agent/device_install.go` reads `CFBundleIdentifier` via `PlistBuddy` so the app auto-launches after install.

**Supported frameworks:**

| Framework | Dev Server | Hot Reload |
|-----------|-----------|------------|
| Expo / React Native | Metro (`npx expo start`) | Auto (Metro watches files) |
| Flutter | `flutter run -d web` | Auto (`r` keystroke) |
| Vite | `npx vite` | Auto (Vite HMR) |
| Next.js | `npx next dev` | Auto (Fast Refresh) |

**Expo modes:** LAN native install on macOS (same Wi-Fi + iOS), Hermes HBC push to the Yaver super-host (any network, including Linux/WSL/remote hosts), or raw dev client (custom native build with all native modules).

### Remote Reload — Trigger from Your Phone

When a third-party app has the Feedback SDK embedded and is connected to the same agent, you can trigger a reload from the Yaver mobile app — even while away from your desk. The agent broadcasts the reload command to all connected SDK devices via a persistent SSE command channel.

```
Yaver Mobile App ──tap "Reload"──► Agent ──SSE push──► Third-Party App (Feedback SDK)
                                    │                     ├─ onReload() callback
                                    └─ /dev/reload-app    └─ auto DevSettings.reload()
```

Two modes: `dev` (hot reload from dev server) and `bundle` (rebuild Hermes bytecode + push). Works over both direct LAN and relay connections.

## Push to Device — Test Existing RN Apps on Real Hardware

Yaver doubles as a native container app (like Expo Go, but for existing projects). Install the yaver.io app from the App Store / Play Store, then push your existing React Native project to it — no project modifications required.

```bash
# Install the npm bootstrap package
npm install -g yaver-cli

# Analyze your existing project
cd my-existing-rn-app
yaver push init

🔍 Analyzing your project...

  React Native:  0.81.5 ✅ (yaver supports 0.81.x)
  Hermes:        enabled ✅
  New Arch:      enabled ✅

  Native modules found in your project:
    react-native-screens@4.16.0       ✅ available in yaver
    react-native-reanimated@4.1.1     ✅ available in yaver
    react-native-gesture-handler@2.28 ✅ available in yaver
    react-native-ble-plx@3.2.0       ❌ NOT in yaver SDK

✅ Created yaver.json

# Push to your phone
yaver push

📡 Found: Kivanc's iPhone (192.168.1.42)
✅ Compatible
🔨 Bundling for ios...
⚡ Compiling Hermes bytecode...
📤 Pushing 847 KB...
🚀 Done in 4.1s — app loading on device
```

**What this is NOT:** Not a WebView. Every `<View>` renders as a real `UIView` / `android.view.View` with full New Architecture support (TurboModules, Fabric). Not Metro dev server — the phone runs a production App Store binary with 80+ pre-installed native modules.

### How It Works

1. **`yaver push init`** reads your `package.json`, compares against the SDK manifest (React Native version, Hermes bytecode version, native modules), and reports compatibility
2. **`yaver push`** bundles your JS with `react-native bundle`, compiles to Hermes bytecode with the npm package's embedded `hermesc`, validates the bytecode version matches the phone app, and pushes via HTTP to the phone's on-device server (port 8347)
3. The phone validates the Hermes bytecode, saves it, and safely reloads the React Native bridge — polling for old bridge deallocation (Hermes GC teardown), then creating a new bridge with full New Architecture support (TurboModules, Fabric, JSI)

### CLI Commands

```
yaver serve                             Start the Go agent from the npm bootstrap package
yaver push init                         Analyze project, show compatibility, create yaver.json
yaver push [--device <ip>]              Bundle + validate + push
yaver push --watch                      Watch mode — re-push on file save
yaver push --ignore-missing             Push even with missing native modules
yaver push doctor                       Deep compatibility report with fix suggestions
yaver push devices                      List discovered devices
yaver push modules                      List all SDK native modules (80+)
yaver push reset                        Clear pushed bundle on device
yaver push status                       Device + project status
yaver-push <same-subcommand>            Legacy alias for existing scripts
```

### Handling Missing Modules

If your project uses native modules not in the yaver SDK, you can still push — features using those modules will crash, but everything else works. Add graceful checks:

```javascript
import { NativeModules } from 'react-native';
const isYaver = !!NativeModules.YaverInfo;

// Skip unavailable features in yaver
if (!isYaver) {
  // use react-native-ble-plx normally
}
```

### SDK Manifest

The yaver.io app now ships with 80+ pre-installed native modules including: `react-native-screens`, `react-native-reanimated`, `react-native-gesture-handler`, `react-native-svg`, `react-native-webview`, `react-native-maps`, `@shopify/react-native-skia`, `@shopify/flash-list`, `@react-native-picker/picker`, `react-native-view-shot`, `expo-camera`, `expo-location`, `expo-notifications`, `expo-updates`, and more. Run `yaver push modules` for the full list.

That manifest is generated from the actual mobile host app dependencies and Expo plugin config, then copied into the CLI package and embedded iOS app bundle. Regenerate with `node scripts/generate-sdk-manifest.mjs` and verify drift in CI with `node scripts/generate-sdk-manifest.mjs --check`.

### Platform Support

React Native has first-class push-to-device support. Other frameworks have hot reload or build-only support.

| Platform | Push to Device | Hot Reload | Build & Upload | How |
|----------|:-:|:-:|:-:|-----|
| **React Native / Expo** | **Yes** | **Yes** | **Yes** | JS bundled + Hermes bytecode compiled + pushed to native container. Full New Arch. |
| **Flutter** | -- | **Yes** | **Yes** | `flutter run` to real device with hot reload via stdin. No container push. |
| **Vite** | -- | **Yes** | -- | Dev server proxied through P2P. Web preview on phone. |
| **Next.js** | -- | **Yes** | -- | Dev server proxied through P2P. Web preview on phone. |
| **Swift / Xcode** | -- | -- | **Yes** | `xcodebuild` + TestFlight upload. Full native build each time. |
| **Kotlin / Gradle** | -- | -- | **Yes** | Gradle APK/AAB build + Play Store upload. Full native build each time. |

**Why React Native is special:** React Native apps are JavaScript at their core. Yaver compiles your JS into Hermes bytecode and loads it into a pre-built native container on the phone -- same principle as Expo Go. Other frameworks compile to machine code (Swift, Kotlin) or use their own VM (Flutter's Dart VM), so there's no way to "inject" your app into a container without building the entire binary.

### How Push to Device Works (Under the Hood)

If you've never worked with React Native internals, here's what's happening when you run `yaver push`:

```
Your Code (JSX/TypeScript)
        |
        v
   Metro Bundler ---- combines all your files into one big JS file
        |
        v
   Hermes Compiler (hermesc) ---- converts JS into compact bytecode (like .class files in Java)
        |
        v
   Hermes Bytecode (.jsbundle) ---- ~60% smaller, loads 2x faster than raw JS
        |
        v
   HTTP push to phone (port 8347) ---- sent over Wi-Fi to Yaver app
        |
        v
   Yaver app validates + loads ---- checks bytecode version, MD5, then hot-swaps the bridge
```

**Key concepts:**

**Hermes** is a JavaScript engine built by Meta specifically for React Native. Instead of parsing JavaScript text at runtime (slow), Hermes pre-compiles it into bytecode (fast). Think of it like the difference between running Python source code vs. a compiled `.pyc` file, or Java source vs. `.class` bytecode.

**Hermes Bytecode (HBC)** is the compiled output. The file starts with a magic number (`0x1F1903C1`) and a version number (currently BC96 for RN 0.81). If the version in your compiled bundle doesn't match the version compiled into the phone app, it will crash -- like trying to run Java 21 bytecode on a Java 8 JVM.

**The Bridge** is how JavaScript talks to native code (UIKit on iOS, Android Views on Android). When you write `<View>`, the JS side sends a message across the bridge saying "create a native view." The native side creates a real `UIView` or `android.view.View`. This is NOT a WebView -- every component is a real native component.

**New Architecture (TurboModules + Fabric)** is React Native's modern runtime. Old RN used an async JSON bridge (slow). New Architecture uses JSI (JavaScript Interface) for synchronous, direct communication between JS and native -- like calling a C function from JS instead of sending a message. TurboModules are native modules that use this fast path. Fabric is the new rendering system. Yaver's container supports both.

**The Native Container** is Yaver's phone app with 80+ native modules pre-compiled in. When you push your JS bundle, it runs inside this container using all the pre-installed native modules (cameras, maps, sensors, storage, lists, pickers, etc.). If your app uses a native module that isn't pre-installed, that specific feature won't work, but everything else will. This is the same concept as Expo Go, but Yaver supports New Architecture and more modules.

**Safe Bridge Reload** -- when a new bundle arrives, Yaver can't just swap the JS file. It needs to: (1) shut down the old JavaScript runtime, (2) wait for background threads (Hermes garbage collector) to finish, (3) create a fresh runtime with the new bundle. If step 2 is skipped, the GC thread touches freed memory and the app crashes. Yaver polls for actual deallocation before proceeding.

## Git Providers — Clone Repos from Your Phone

Yaver auto-detects GitHub and GitLab credentials already on your dev machine — from `gh` CLI, `glab` CLI, macOS Keychain, git credential helpers, or environment variables. No tokens ever leave the machine.

```
Phone (Yaver app)                         Dev Machine
┌──────────────┐                      ┌──────────────┐
│ Browse repos │──GET /git/repos────►│ Agent queries │
│ from GitHub  │                      │ GitHub/GitLab │
│ or GitLab    │                      │ API with      │
│              │◄─repo list──────────│ local creds   │
│              │                      │               │
│ Tap "Clone"  │──POST /git/clone───►│ git clone     │
│              │                      │ on machine    │
└──────────────┘                      └──────────────┘
```

This is useful for headless dev machines (cloud VPS, Mac Mini) where you haven't cloned a repo yet. Browse your GitHub/GitLab repos from the app, tap clone, and the dev machine pulls it down using its own credentials. Then start coding from your phone immediately.

```bash
# Or from CLI:
yaver git providers        # List detected providers
yaver git repos            # Browse repos
yaver git clone <repo>     # Clone to dev machine
```

## Email Connectors

Connect Office 365 or Gmail for AI-assisted email workflows.

```bash
# Setup
yaver email setup     # Interactive — choose Office 365 or Gmail
yaver email test      # Send a test email
yaver email sync      # Sync emails to local SQLite database

# Available as MCP tools: email_list_inbox, email_get, email_send, email_sync, email_search
```

### Office 365
Requires Azure AD app registration with Microsoft Graph API permissions (`Mail.Read`, `Mail.Send`). Uses client credentials flow.

### Gmail
Requires Google Cloud OAuth2 credentials with Gmail API scope. Uses refresh token flow.

Synced emails are stored locally in `~/.yaver/emails.db` (SQLite) for fast search and retrieval.

## ACL — Agent Communication Layer

Connect Yaver to other MCP servers for agent-to-agent workflows:

```bash
# Connect to local Ollama
yaver acl add ollama http://localhost:11434/mcp

# Connect to a filesystem MCP server (stdio)
yaver acl add files --stdio "npx -y @modelcontextprotocol/server-filesystem /home"

# Connect to a remote database
yaver acl add mydb https://db.example.com/mcp --auth token123

# List / manage peers
yaver acl list
yaver acl tools ollama
yaver acl health
yaver acl remove ollama
```

ACL peers are also accessible via MCP tools (`acl_list_peers`, `acl_call_peer_tool`, etc.), enabling Claude to chain tools across multiple MCP servers.

## Components

| Piece | Directory | Install | What it does |
|-------|-----------|---------|-------------|
| **Mobile App** | `mobile/` | App Store / Play Store | Remote control for AI agents + native RN container + on-device HTTP server (port 8347) |
| **Desktop Agent** | `desktop/agent/` | `brew install yaver` or `apt install yaver` | Native `yaver` command for P2P server, AI agent runner, MCP, hot reload, builds, and session transfer. Also bridges `yaver push` through npm when Node is present. |
| **Unified NPM Bootstrap** | `cli/` | `npm i -g yaver-cli` | Umbrella install. Installs the `yaver` command, bootstraps the agent, and gives you one entry point for `yaver serve`, `yaver push`, `yaver feedback setup`, `yaver sdk add ...`, and `yaver install ...`. |
| **Feedback SDKs** | `sdk/feedback/` | `npm i -g yaver-cli` then `yaver feedback setup` | Debug console + black box recorder embedded in your app. React Native, Flutter, Web. |
| **Programmatic SDKs** | `sdk/` | `npm i -g yaver-cli` then `yaver sdk add core` | Automate Yaver from code — Go, Python, JS/TS, Flutter/Dart, C. |
| Desktop Installer | `desktop/installer/` | [Download](https://yaver.io/download) | GUI installer (DMG/EXE/DEB) — installs the Go agent binary |
| Relay Server | `relay/` | Docker / binary | QUIC relay for NAT traversal — self-hostable, pass-through only |
| Backend | `backend/` | Managed (Convex) | Auth + peer discovery + platform config. No user data. |
| Web | `web/` | Managed (Cloudflare Workers) | Landing page at yaver.io |

## Publish Runners

Yaver can now run package and store publishes directly on developer-owned
hardware. The source of truth is project-scoped config in `.yaver/publish.yaml`.
That same config is surfaced through CLI, web, mobile, and MCP.

- Primary path: local/self-hosted execution on the developer's own machine.
- Yaver-first upload/register: built artifacts are archived into Yaver's own blob
  storage first so the system dogfoods its own uploader before any external CI
  or store fallback path is used.
- Optional fallback: self-hosted GitHub Actions `workflow_dispatch`, only when
  the project allows it and the user explicitly requests it for that run.
- Secret sources: Yaver vault entries and/or environment variables already
  present on the machine or injected by GitHub secrets/vars.
- Supported target kinds: `npm`, `pypi`, `pub.dev`, `testflight`, `playstore`.

```bash
yaver publish init
yaver publish config
yaver publish run --target npm-cli
yaver publish run --target pypi-sdk-python
```

The repo includes `.github/workflows/yaver-publish.yml` for the GitHub fallback
path and exposes MCP tools `publish_config_get`, `publish_run`,
`publish_submit`, `publish_upload`, `publish_ci_dispatch`, `publish_list`, and
`publish_status`.

## CLI Commands

```
yaver auth          Sign in (opens browser — Apple, Google, or Microsoft)
yaver serve         Start the agent
yaver mcp           Start MCP server (--mode stdio|http)
yaver email         Email connector (setup, test, sync, status)
yaver acl           Agent Communication Layer (add, list, remove, tools, health)
yaver connect       Connect to a remote agent
yaver attach        Interactive terminal
yaver set-runner    Set default AI agent (claude/codex/aider/custom)
yaver relay         Manage relay servers (add/remove/test — hot-reload, no restart)
yaver tunnel        Manage Cloudflare Tunnels
yaver config        Get/set configuration
yaver publish       Project-scoped package/store publish runner
yaver status        Show auth, agent, relay, and connection status
yaver doctor        System health check (auth, runners, relay, network)
yaver devices       List registered devices
yaver exec          Execute a command on a remote device
yaver session       Transfer AI agent sessions between machines
yaver handoff       Pass the current AI session to Yaver (autodev takes over)
yaver build         Build apps (Flutter, Gradle, Xcode, React Native)
yaver test          Run tests (auto-detect framework)
yaver deploy        Deploy to phone, TestFlight, Play Store, or CI
yaver debug         Hot reload debug sessions
yaver repo          Switch between projects
yaver vault         P2P encrypted key management
yaver pipeline      Build → test → deploy in one command
yaver feedback      Visual bug reports (list/show/fix) — screen recording + voice from device
yaver stop          Stop the agent
yaver restart       Restart the agent
yaver logs          View agent logs
yaver completion    Generate shell completions (bash/zsh/fish)
yaver version       Print version
```

### Shell Completions

```bash
# Bash — add to ~/.bashrc
eval "$(yaver completion bash)"

# Zsh — add to ~/.zshrc
eval "$(yaver completion zsh)"

# Fish
yaver completion fish | source
```

## Voice Input & Text-to-Speech

Yaver supports voice input for both mobile and CLI — speak your tasks instead of typing.

### Providers

| Provider | Type | Cost | Quality |
|----------|------|------|---------|
| **On-device (Whisper)** | Free, offline | $0 | Good (English) |
| **OpenAI** | Cloud, API key | $0.003/min | Excellent |
| **Deepgram** | Cloud, API key | $0.004/min | Excellent |
| **AssemblyAI** | Cloud, API key | $0.002/min | Good |

### Mobile

Configure in Settings > Voice or during onboarding. Tap the mic button in the task creation modal (WhatsApp-style). Supports TTS — have responses read aloud.

### CLI

```bash
# Configure speech provider
yaver config set speech.provider whisper     # Free, local (requires whisper-cpp)
yaver config set speech.provider openai      # Cloud (bring your own key)
yaver config set speech.api_key sk-...       # Set API key

# Use voice in interactive mode
yaver connect
yaver> voice                                  # Records, transcribes, sends as task
```

For local/free STT, install whisper.cpp: `brew install whisper-cpp`

### Response Verbosity

Control how detailed AI responses are (0-10 scale):
- **0-2**: Minimal — "Done, no issues"
- **3-4**: Brief — 2-3 sentence summary
- **5-6**: Moderate — key changes + reasoning
- **7-8**: Detailed — full code changes
- **9-10**: Everything — diffs, alternatives, reasoning

Set via mobile app (Settings > Voice > Response detail) or passed per-task.

## SDK — Embed Yaver in Your App

Yaver provides SDKs for Go, Python, and JavaScript/TypeScript. Connect to agents, create tasks, stream output, and use speech-to-text from your own code.

### Go

```go
import yaver "github.com/kivanccakmak/yaver.io/sdk/go/yaver"

client := yaver.NewClient("http://localhost:18080", token)
task, _ := client.CreateTask("Fix the login bug", nil)
for chunk := range client.StreamOutput(task.ID, 0) {
    fmt.Print(chunk)
}
```

### Python

```python
from yaver import YaverClient

client = YaverClient("http://localhost:18080", token)
task = client.create_task("Fix the login bug")
for chunk in client.stream_output(task["id"]):
    print(chunk, end="")
```

### JavaScript / TypeScript

```typescript
import { YaverClient } from 'yaver-sdk';

const client = new YaverClient('http://localhost:18080', token);
const task = await client.createTask('Fix the login bug');
for await (const chunk of client.streamOutput(task.id)) {
  process.stdout.write(chunk);
}
```

### C/C++ (shared library)

Build the shared library, then link against it:

```bash
cd sdk/go/clib && go build -buildmode=c-shared -o libyaver.so .
```

```c
#include "libyaver.h"
int client = YaverNewClient("http://localhost:18080", token);
char* result = YaverCreateTask(client, "Fix the bug", NULL);
```

### Speech in SDK

All SDKs support speech-to-text:

```python
# Python — transcribe audio
result = client.transcribe("recording.wav", provider="openai", api_key="sk-...")
print(result["text"])
```

```go
// Go — record and transcribe
audioPath, _ := yaver.RecordAudio()
tr := yaver.NewTranscriber(&yaver.SpeechConfig{Provider: "openai", APIKey: "sk-..."})
result, _ := tr.Transcribe(audioPath)
fmt.Println(result.Text)
```

### Feedback SDKs — Visual Bug Reports from Inside Your App

Embed in your app during development. Screen recording + voice + screenshots → sent to AI agent via P2P. Auto-disabled in production.

| SDK | Install | Trigger Modes |
|-----|---------|---------------|
| **Web** | `npm install yaver-feedback-web` | Floating button, keyboard shortcut (Ctrl+Shift+F), manual |
| **React Native** | `npm install yaver-feedback-react-native` | Shake-to-report, floating button, manual |
| **Flutter** | `yaver_feedback: ^0.1.0` in pubspec.yaml | Shake, floating button, manual |

All SDKs include: auto device discovery, connection UI, screen recording, voice annotation, three runtime modes (Full Interactive / Semi Interactive / Post Mode), agent commentary, voice commands, and remote reload (trigger hot reload from the Yaver mobile app via the agent's command channel).

See [Feedback SDK Examples](sdk/examples/feedback/) for demos of each mode.

## System Health Check

```bash
$ yaver doctor
Yaver Doctor
  Version: 1.36.0

── Configuration ──
  Config file                    ✓ ~/.yaver/config.json

── Authentication ──
  Auth token                     ✓ Present
  Token validation               ✓ Valid
  Device ID                      ✓ f5e857d3...

── AI Runners ──
  Claude Code (claude)           ✓ /usr/local/bin/claude (2.1.80)
  OpenAI Codex (codex)           ! Not installed — npm install -g @openai/codex
  Aider (aider)                  ! Not installed — pip install aider-chat
  Ollama (ollama)                ✓ /usr/local/bin/ollama (0.18.2)

── Relay Servers ──
  Relay: My VPS                  ✓ OK (89ms, password set)

── Network ──
  Local IP                       ✓ 192.168.1.103
  Internet connectivity          ✓ OK

Doctor summary: 12 passed, 3 warnings, 0 failures
```

## Relay Server — Hot Reload

Relay servers can be added, removed, or updated while the agent is running — no restart needed.

```bash
yaver relay add https://relay.example.com --password secret --label "My VPS"
# → Agent notified — relay will connect within seconds.

yaver relay remove a4ef61ac
# → Agent notified — relay tunnel will be stopped.

yaver relay set-password newsecret
# → Agent notified — new password will be used.

yaver relay list
yaver relay test
```

The agent polls config every 30s as a safety net, and responds instantly to `SIGHUP` when relay commands run.

### Relay Health Monitoring

The agent pings each relay's `/health` endpoint every 60 seconds. Results are cached and shown in `yaver status`:

```
Relay:
  Servers:
    My VPS     https://relay.example.com     OK (89ms, 1 tunnel(s), v0.1.0) [password]
              Last check: 22s ago
```

## Token Refresh & Re-Auth

Sessions last 30 days and auto-refresh:
- **CLI**: Refreshes token on startup + weekly. Detects 401 in heartbeat → warns to re-auth.
- **Mobile**: Refreshes on launch + every foreground resume. Auto-logouts if expired.
- **Backend**: `POST /auth/refresh` extends session by 30 more days.

Settings (relay servers, tunnels, preferences) are preserved across sign-out/sign-in on both CLI and mobile.

## Networking

Three-layer stack — no Tailscale, no TUN/TAP, no VPN rights. Application-layer only.

```
1. LAN Beacon (direct)  ──  ~5ms   ── same WiFi, instant discovery
2. Convex IP (direct)   ──  ~5ms   ── known IP from device registry
3. QUIC Relay (proxied) ──  ~50ms  ── roaming, NAT traversal
```

See [CLAUDE.md](CLAUDE.md) for detailed networking architecture.

## Development

```bash
cd backend && npm install && npx convex dev    # Convex dev server
cd web && npm install && npm run dev           # Web (localhost:3000)
cd desktop/agent && go run . serve --debug     # Desktop agent
cd relay && go run . serve --password secret   # Relay server (local)
```

### Tests

```bash
# Unit tests (no external deps)
cd desktop/agent && go test -v ./...
cd relay && go test -v ./...

# Integration test suite
./scripts/test-suite.sh                # Run all tests
./scripts/test-suite.sh --unit         # Go unit tests only
./scripts/test-suite.sh --builds       # Build verification (all platforms)
./scripts/test-suite.sh --lan          # LAN direct connection (localhost)
./scripts/test-suite.sh --relay        # Local relay server test
./scripts/test-suite.sh --relay-docker # Deploy relay via Docker to remote server, test, teardown
./scripts/test-suite.sh --relay-binary # Deploy relay binary to remote server, test, teardown
./scripts/test-suite.sh --tailscale    # Tailscale cross-machine (local ↔ remote server)
./scripts/test-suite.sh --cloudflare   # Cloudflare tunnel test
./scripts/test-suite.sh --help         # Show all options
```

**No credentials needed:** `--unit`, `--lan`, and `--relay` work out of the box.

**Remote server tests:** `--relay-docker`, `--relay-binary`, and `--tailscale` SSH into a remote server (e.g., Hetzner VPS) to deploy relay/agent binaries, run them, test cross-network connectivity, then tear everything down.

**Credentials:** Set up via `.env.test` (gitignored) or `../talos/.env.test`:
```bash
cp .env.test.example .env.test   # fill in REMOTE_SERVER_IP, etc.
```
For CI, store as GitHub Actions secrets. See `.github/workflows/test-suite.yml`.

## Auth

- Apple Sign-In, Google Sign-In, Microsoft/Office 365
- `yaver auth` opens `https://yaver.io/auth?client=desktop` → OAuth → callback to `http://127.0.0.1:19836/callback?token=<token>`

## Self-Hosting

### Relay Server

The relay is a lightweight QUIC proxy for NAT traversal. It's pass-through only — no data is stored. Deploy on any VPS with a public IP.

#### Automated Setup (recommended)

The setup script handles everything: Docker, nginx, Let's Encrypt SSL, firewall, and relay deployment.

```bash
# Prerequisites: VPS with SSH access (root), DNS A record pointing to your VPS IP
./scripts/setup-relay.sh <server-ip> <domain> --password <relay-password>

# Example
./scripts/setup-relay.sh 1.2.3.4 relay.example.com --password mysecret

# Without a domain (testing / IP-only access)
./scripts/setup-relay.sh 1.2.3.4 --no-domain --password mysecret

# Custom ports
./scripts/setup-relay.sh 1.2.3.4 relay.example.com --password secret --quic-port 5433 --http-port 9443

# Show all options
./scripts/setup-relay.sh --help
```

The script will:
1. Install Docker on the VPS (if not present)
2. Install nginx + certbot, obtain Let's Encrypt SSL certificate
3. Configure nginx as HTTPS reverse proxy with SSE/streaming support
4. Sparse-clone the relay directory to `/opt/yaver-relay`
5. Build and start the relay Docker container
6. Configure firewall (UFW) — TCP 443, UDP 4433, TCP 80
7. Run a health check and print connection details

#### Manual Setup (Docker)

```bash
# On your VPS
git clone --depth 1 --filter=blob:none --sparse https://github.com/kivanccakmak/yaver.git /opt/yaver-relay
cd /opt/yaver-relay && git sparse-checkout set relay && cd relay

# Set password and start
echo "RELAY_PASSWORD=your-secret" > .env
docker compose up -d

# Verify
curl http://localhost:8443/health
# {"status":"ok"}
```

#### Manual Setup (native binary, no Docker)

```bash
# Build the relay binary (requires Go 1.22+)
cd relay
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o yaver-relay .

# Copy to server
scp yaver-relay root@<server-ip>:/usr/local/bin/yaver-relay

# On the server: run directly
RELAY_PASSWORD=your-secret yaver-relay serve --quic-port 4433 --http-port 8443

# Or install as systemd service
scp relay/deploy/yaver-relay.service root@<server-ip>:/etc/systemd/system/
ssh root@<server-ip> 'systemctl daemon-reload && systemctl enable --now yaver-relay'
```

#### HTTPS with nginx (for production)

If you set up manually (without the setup script), add nginx + Let's Encrypt for HTTPS:

```bash
# On your VPS — install nginx and certbot
apt install -y nginx certbot python3-certbot-nginx

# Get SSL certificate (point DNS A record to VPS IP first)
certbot certonly --standalone -d relay.example.com

# Copy nginx config template and edit domain
cp relay/deploy/nginx-relay.conf /etc/nginx/sites-available/yaver-relay
sed -i 's/DOMAIN/relay.example.com/g; s/HTTP_PORT/8443/g' /etc/nginx/sites-available/yaver-relay
ln -sf /etc/nginx/sites-available/yaver-relay /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx

# Open firewall
ufw allow 443/tcp    # HTTPS
ufw allow 4433/udp   # QUIC
ufw allow 80/tcp     # HTTP redirect
```

#### Connect clients to your relay

```bash
# CLI — add relay to config
yaver relay add my-relay \
  --quic-addr <server-ip>:4433 \
  --http-url https://relay.example.com \
  --password your-secret

# Or edit ~/.yaver/config.json directly
```

```json
{
  "relay_servers": [
    {
      "id": "my-relay",
      "quic_addr": "<server-ip>:4433",
      "http_url": "https://relay.example.com"
    }
  ],
  "relay_password": "your-secret"
}
```

Mobile app: Settings → Relay Servers → Add your relay URL and password.

#### Relay management

```bash
# Health check
curl https://relay.example.com/health

# View connected tunnels
curl https://relay.example.com/tunnels

# Logs
ssh root@<server-ip> 'cd /opt/yaver-relay/relay && docker compose logs -f'   # Docker
ssh root@<server-ip> 'journalctl -u yaver-relay -f'                          # systemd

# Stop / remove
./relay/deploy/down.sh <server-ip>           # Stop
./relay/deploy/down.sh <server-ip> --purge   # Stop and remove everything
```

#### VPS requirements

- **CPU/RAM**: 1 vCPU, 512 MB RAM minimum (relay is very lightweight)
- **Ports**: TCP 443 (HTTPS), UDP 4433 (QUIC), TCP 8443 (HTTP fallback), TCP 80 (Let's Encrypt)
- **OS**: Any Linux with Docker (Ubuntu 22.04+ recommended)
- **Providers**: Hetzner, DigitalOcean, Linode, AWS Lightsail, Vultr — any VPS works

### No Relay (Tailscale)

If both devices are on your Tailscale tailnet, no relay is needed:

```bash
yaver serve --no-relay  # Connect directly via Tailscale IP
```

Tailscale client is open source (BSD 3-Clause). For a fully self-hosted alternative to the Tailscale coordination server, use [Headscale](https://github.com/juanfont/headscale).

## Related Work

Projects and tools in the same problem space. Yaver is compatible with most of these and can be used alongside them. Items marked `[OSS]` are open-source software.

### AI Coding Agents
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) — Anthropic's agentic coding tool
- [OpenAI Codex CLI](https://github.com/openai/codex) `[OSS]` — OpenAI's terminal coding agent
- [Aider](https://aider.chat) `[OSS]` — AI pair programming in your terminal
- [Goose](https://github.com/block/goose) `[OSS]` — autonomous coding agent by Block
- [Amp](https://github.com/nichochar/amp) `[OSS]` — terminal-native AI coding agent
- [OpenCode](https://github.com/opencode-ai/opencode) `[OSS]` — AI coding in the terminal
- [Continue](https://continue.dev) `[OSS]` — AI code assistant for IDEs

### Local LLMs & Inference
- [Ollama](https://ollama.com) `[OSS]` — run LLMs locally with one command
- [Qwen](https://github.com/QwenLM/Qwen) — open-weight LLMs by Alibaba Cloud
- [GLM-4](https://github.com/THUDM/GLM-4) — open-weight multilingual LLM
- [llama.cpp](https://github.com/ggml-org/llama.cpp) `[OSS]` — LLM inference in C/C++
- [vLLM](https://github.com/vllm-project/vllm) `[OSS]` — high-throughput LLM serving engine

### Remote Development
- [code-server](https://github.com/coder/code-server) `[OSS]` — VS Code in the browser
- [Coder](https://github.com/coder/coder) `[OSS]` — self-hosted remote dev environments
- [tmate](https://github.com/tmate-io/tmate) `[OSS]` — instant terminal sharing
- [sshx](https://github.com/nichochar/sshx) `[OSS]` — collaborative terminal sharing over the web
- [ttyd](https://github.com/nicm/ttyd) `[OSS]` — share your terminal over the web

### Networking & NAT Traversal
- [Tailscale](https://tailscale.com) — mesh VPN built on WireGuard (client is open-source)
- [NetBird](https://github.com/netbirdio/netbird) `[OSS]` — network connectivity platform
- [frp](https://github.com/fatedier/frp) `[OSS]` — fast reverse proxy for NAT traversal
- [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) — expose local services securely
- [Headscale](https://github.com/juanfont/headscale) `[OSS]` — self-hosted Tailscale control server

### Infrastructure & Protocols
- [Convex](https://www.convex.dev) — reactive backend-as-a-service (runtime is open-source)
- [quic-go](https://github.com/quic-go/quic-go) `[OSS]` — QUIC protocol implementation in Go
- [tmux](https://github.com/tmux/tmux) `[OSS]` — terminal multiplexer
- [MCP](https://modelcontextprotocol.io) `[Open Spec]` — Model Context Protocol
- [QUIC (RFC 9000)](https://www.rfc-editor.org/rfc/rfc9000.html) `[Open Standard]` — UDP-based transport protocol
- [WireGuard](https://www.wireguard.com) `[OSS]` — modern VPN protocol

## Security

- **Reporting a vulnerability:** email `kivanc.cakmak@simkab.com`. 48-hour
  acknowledgement, 90-day disclosure window, good-faith safe harbour.
  Do **not** open a public GitHub issue for security bugs.
  Full policy: [`SECURITY.md`](./SECURITY.md).
- **Production is protected by a 5-layer defence:** required reviewer on
  the `Production` environment, branch + tag allowlist on that
  environment, `main` branch ruleset (no force-push, linear history,
  signed commits), release-tag ruleset (only admin can create/update),
  `CODEOWNERS` gating all CI / deploy / auth / vault code, and fork
  PRs are blocked from reading secrets. See [`SECURITY.md`](./SECURITY.md)
  §"How production is protected" for the full breakdown.
- **Contributors:** sign off every commit with `git commit -s` (DCO).
  Commits on `main` from the repo owner are GPG-signed — unsigned
  commits by the owner name are a signal to investigate.

## Legal

- [Privacy Policy](https://yaver.io/privacy)
- [Terms of Service](https://yaver.io/terms)

Developed by **[SIMKAB ELEKTRIK](https://simkab.com/)** — Istanbul, Turkey

Contact: kivanc.cakmak@simkab.com

## License

Yaver uses a **split-license model** — see [`LICENSING.md`](./LICENSING.md)
for the full mapping and [`CONTRIBUTING.md`](./CONTRIBUTING.md) for how
contributions are licensed.

- **Core** (agent, relay, backend, web UI, mobile app, desktop
  app/installer, pi-image) — [`FSL-1.1-Apache-2.0`](./LICENSE):
  Functional Source License. Free for any non-competing use; each
  release auto-transitions to Apache-2.0 two years after publication.
- **Client SDKs & CLIs** (`cli/`, `sdk/js`, `sdk/feedback/*`,
  `sdk/flutter`, `sdk/python`, `sdk/go/*`, `sdk/errors-js`) —
  Apache-2.0 from day one, embed in closed-source apps freely.

Rule of thumb: *does your app import / bundle / invoke this code?*
Yes → Apache-2.0. No, it's a network service → FSL-1.1-Apache-2.0.

A commercial license is available for organizations that need the
core without the Competing Use restriction — contact
[kivanc.cakmak@simkab.com](mailto:kivanc.cakmak@simkab.com).
