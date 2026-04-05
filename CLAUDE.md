# Yaver.io — Claude Code Project Guide

## Important Rules
- **Never push or commit without explicit user permission.** Vercel auto-deploys from GitHub on push to `main`.
- **Vercel deploy size guard**: `web/` must stay under 10 MB (currently ~2.5 MB). The deploy script enforces this. Do not add large assets to `web/`. The biggest file is `web/public/demo.mp4` (~1.2 MB, compressed from 8 MB original). If adding videos, compress aggressively first: `ffmpeg -i input.mp4 -vcodec libx264 -crf 32 -preset veryslow -vf "scale=720:-2" -an output.mp4`. Prefer external hosting (YouTube embed, GitHub releases CDN) for anything over 1 MB.
- **NEVER use WebView to load third-party apps.** All app loading must be native (real UIView/android.view.View via ExpoReactNativeFactory with New Architecture). When "Open App" is tapped, use `/dev/build-native` to compile a Hermes bytecode bundle and load it into a native bridge with full TurboModule support — never a WebView. WebView is only acceptable for web content (landing pages, docs), never for React Native apps.
- **NEVER commit credentials, IPs, API keys, or secrets to the repo.** The repo is open-source on GitHub. All credentials must go in `.env.test` (gitignored), env vars, or GitHub Actions secrets. This includes Hetzner server IPs, Apple Developer keys, SSH key paths, relay passwords, Tailscale IPs. If you see a hardcoded credential, replace it with an env var or placeholder immediately.

## Repository & Deployment
- **Source of truth**: GitLab (`gitlab.com/kivanccakmak/yaver.io`) — development happens here
- **Public mirror**: GitHub (`github.com/kivanccakmak/yaver.io`) — open-source, single squashed initial commit, no git history
- **To update GitHub mirror**: Push to GitLab first, then sync to GitHub with a squashed commit (see below)
- **Vercel**: auto-deploys from GitHub on push to `main`. Manual deploy: `./scripts/deploy-vercel.sh`
- **Landing page links**: point to `https://github.com/kivanccakmak/yaver.io`

### Syncing GitLab → GitHub
```bash
# After pushing to GitLab, create a fresh GitHub mirror:
cd /tmp && rm -rf yaver-github-mirror && mkdir yaver-github-mirror && cd yaver-github-mirror
git init && git remote add origin git@github.com:kivanccakmak/yaver.io.git
rsync -a --exclude='.git' --exclude='node_modules' --exclude='.next' --exclude='.env.test' --exclude='.env.local' --exclude='backend/.env.local' --exclude='keys/' /path/to/yaver.io/ .
rm -rf node_modules .next keys/ .env.test backend/.env.local mobile/ios/Pods/ web/.next/ web/node_modules/ mobile/node_modules/ backend/node_modules/ desktop/installer/node_modules/ 2>/dev/null
# SCAN FOR CREDENTIALS before pushing:
grep -rn '37\.27\|5SJZ4KA39A\|77Z6B543D5\|7bd9329e\|NJ2VE6KEM55' . --include='*.go' --include='*.ts' --include='*.sh' --include='*.py' --include='*.md'
git add -A && git commit -m "Update open-source release" && git push --force origin main
```

## Dev Server Proxy (Hot Reload to Phone)
When a user asks to "run an app on my phone", "hot reload", "load the app", or "start the app":
1. **Do NOT tell the user to run commands manually.** The user only runs `yaver auth` and `yaver serve`. Everything else is automatic.
2. **Start the dev server using curl**:
```bash
curl -s -X POST http://localhost:18080/dev/start \
  -H "Authorization: Bearer $(cat ~/.config/yaver/config.json 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin).get(\"auth_token\",\"\"))' 2>/dev/null || cat ~/.yaver/config.json | python3 -c 'import sys,json;print(json.load(sys.stdin).get(\"auth_token\",\"\"))')" \
  -H "Content-Type: application/json" \
  -d '{"framework":"expo","workDir":"/absolute/path/to/app"}'
```
3. The Yaver mobile app **automatically detects** the dev server and shows a green "Open App" banner.
4. The user taps the banner → app loads in a full-screen WebView through the P2P/relay channel.
5. **After fixing code**, trigger hot reload:
```bash
curl -s -X POST http://localhost:18080/dev/reload \
  -H "Authorization: Bearer $TOKEN"
```
6. The WebView auto-refreshes with the updated code.
7. **Never output raw `exp://` URLs, QR codes, or tell the user to run terminal commands.** Everything flows through the Yaver P2P channel automatically.
8. When done: `curl -s -X POST http://localhost:18080/dev/stop -H "Authorization: Bearer $TOKEN"`

### Dev Server — Supported Frameworks

| Framework | Detection | Dev Server Command | Hot Reload | Bundle URL |
|-----------|-----------|-------------------|------------|------------|
| **Expo / React Native** | `expo` in package.json | `npx expo start --web --lan` | Auto (Metro watches files) + `/dev/reload` | `/dev/` (web version) |
| **Flutter** | `pubspec.yaml` | `flutter run -d web --web-port N` | `r` keystroke to stdin | `/dev/` |
| **Vite** | `vite.config.{ts,js}` | `npx vite --port N --host 0.0.0.0` | Auto (Vite HMR) | `/dev/` |
| **Next.js** | `next.config.{ts,js}` | `npx next dev --port N --hostname 0.0.0.0` | Auto (Fast Refresh) | `/dev/` |

### Dev Server — How It Works Through Relay

```
Phone (Yaver app)                    Relay                     Dev Machine
┌──────────────┐              ┌──────────────┐             ┌──────────────┐
│  WebView     │──GET /dev/──►│  QUIC relay  │──forward───►│  Agent :18080│
│  loads app   │              │  (pass-thru) │             │    │         │
│  through     │◄─HTML/JS────│              │◄─response───│    ▼         │
│  relay URL   │              └──────────────┘             │  /dev/* proxy│
└──────────────┘                                          │    │         │
                                                          │    ▼         │
                                                          │  Metro :8081 │
                                                          │  (or Vite,   │
                                                          │   Flutter)   │
                                                          └──────────────┘
```

The agent's `/dev/*` endpoint reverse-proxies to the local dev server. The relay forwards HTTP requests transparently. The phone loads the web version of the app in a WebView — works through captive portals, 4G, any network.

### Dev Server — Key Files

| File | Purpose |
|------|---------|
| `desktop/agent/devserver.go` | DevServer interface, manager, 4 framework implementations |
| `desktop/agent/devserver_http.go` | HTTP handlers: /dev/start, /dev/stop, /dev/status, /dev/events (SSE), /dev/* proxy |
| `desktop/agent/dev_cmd.go` | CLI: `yaver dev start\|stop\|status\|reload` |
| `mobile/src/components/DevPreview.tsx` | Banner + WebView + SSE auto-reload |
| `mobile/src/lib/quic.ts` | `getDevServerStatus()`, `startDevServer()`, `reloadDevServer()` |
| `relay/tunnel.go` | SSE detection for /dev/events, 200MB body limit for /dev/ paths |

### Hot Reload for Native Apps (Swift, Kotlin)

Native apps compile to machine code — no runtime hot swap. For Swift/Kotlin, Yaver provides:
1. **Feedback capture**: SDK captures screenshots, crash logs, stack traces
2. **Build-deploy-restart**: Agent fixes code → rebuilds → pushes binary (ADB for Android, TestFlight for iOS)
3. **Iteration speed**: ~30-60s build-deploy vs instant JS hot reload, but fully automated

## Three-Part Architecture

Yaver has three distinct components for developers:

```
┌──────────────────────────────────────────────────────────────────────────┐
│                        Yaver Platform                                    │
│                                                                          │
│  1. Mobile App (yaver.io)       ── App Store / Play Store               │
│     • Native container for testing third-party RN apps                   │
│     • AI agent control from phone (tasks, feedback, hot reload)          │
│     • HTTP server on port 8347 for receiving pushed bundles              │
│                                                                          │
│  2. Push-to-Device CLI (yaver-cli)  ── npm install -g yaver-cli         │
│     • For third-party developers to push THEIR existing RN projects      │
│     • Analyzes compatibility, bundles JS, compiles Hermes, pushes        │
│     • Talks directly to phone's HTTP server (no agent needed)            │
│                                                                          │
│  3. Desktop Agent (yaver)       ── brew install yaver                   │
│     • Go binary for AI agent connectivity (P2P, relay, MCP)             │
│     • Hot reload dev servers (Expo, Flutter, Vite, Next.js)             │
│     • Session transfer, tasks, builds, deploys                           │
│     • Not needed for push-to-device — that's CLI→phone direct           │
└──────────────────────────────────────────────────────────────────────────┘
```

**Key distinction:** `yaver-cli` (npm) and `yaver` (Go binary) are completely separate tools for different use cases. `yaver-cli` is for third-party RN developers who want to test their apps on real devices. `yaver` is for running AI agents from your phone. A developer might use both.

## Push to Device (yaver-cli)

Yaver doubles as a native container app (like Expo Go) for existing React Native projects. Developers push their existing RN projects to the yaver.io phone app via `yaver-cli` for real-device testing.

### Architecture
```
Developer's Machine                          Phone (yaver.io app)
┌─────────────────────┐                     ┌─────────────────────┐
│  yaver-cli         │     HTTP POST       │  HTTP Server :8347  │
│  ├── analyzer.js    │────/bundle─────────►│  ├── /health        │
│  ├── bundler.js     │                     │  ├── /bundle        │
│  │   └── hermesc    │◄───/health──────────│  ├���─ /reset         │
│  ├── discovery.js   │     GET             │  └── /assets        │
│  └── transport.js   │                     │                     │
│                     │                     │  sdk-manifest.json  │
│  sdk-manifest.json  │  must match ◄──────►│  (embedded in app)  │
└─────────────────────┘                     └─────────────────────┘
```

### Key Files
| File | Purpose |
|------|---------|
| `mobile/sdk-manifest.json` | Source of truth — RN version, Hermes BC, native modules |
| `mobile/ios/Yaver/YaverHTTPServer.swift` | iOS HTTP server (GCDWebServer on port 8347) |
| `mobile/ios/Yaver/YaverInfo.swift` + `.m` | YaverInfo native module (isYaver detection) |
| `mobile/android/.../YaverHTTPServer.kt` | Android HTTP server (NanoHTTPD on port 8347) |
| `mobile/android/.../YaverInfoModule.kt` | Android YaverInfo native module |
| `cli/` | `yaver-cli` npm package root |
| `cli/src/analyzer.js` | Project analysis — RN version, native module compatibility |
| `cli/src/bundler.js` | JS bundling + hermesc compilation |
| `cli/src/discovery.js` | Device discovery (UDP beacon, LAN scan, manual IP) |
| `cli/src/transport.js` | HTTP push to device |
| `cli/src/commands/` | init, push, doctor, devices, modules, reset, status |

### SDK Manifest Contract
The `sdk-manifest.json` must be kept in sync across:
1. `mobile/sdk-manifest.json` (source of truth)
2. `mobile/android/app/src/main/assets/sdk-manifest.json` (Android copy)
3. iOS bundle (Xcode → Copy Bundle Resources → sdk-manifest.json)
4. `cli/sdk-manifest.json` (CLI copy)

When updating native modules in `mobile/package.json`, update the manifest and copy to all locations.

### Hermes Bytecode Validation
Both CLI and device validate Hermes bytecode version matches. The CLI ships its own `hermesc` (from RN 0.81.5, located at `cli/hermesc/`) to guarantee BC version match. HBC header format: magic `0x1F1903C1` at offset 4, BC version at offset 8 (uint32 LE, currently 96). Validation is done by `ValidateHBC()` in `desktop/agent/bundlecheck.go` (Go side) and `YaverBundleValidator.swift` (iOS side).

### Safe Bridge Reload
When a bundle is pushed, `safeReloadBridge` invalidates the old bridge and polls for deallocation (weak-reference check, up to 3s timeout) before creating a new one. The wait lets Hermes HadesGC thread finish — without it, GC touches freed memory → SIGABRT on TestFlight. The new guest bridge is created via `ExpoReactNativeFactory` + `RCTAppDependencyProvider` (same pattern as the primary app), which provides full New Architecture support including TurboModules, Fabric, and JSI. This is required for RN 0.81+ apps that use `TurboModuleRegistry.getEnforcing()` for core modules like `PlatformConstants`.

### Platform Support for Push to Device

React Native / Expo is the **only** framework with full push-to-device container support. Other frameworks have hot reload (dev server proxy) or build-only support.

| Platform | Push to Device | Hot Reload | Build & Upload | Implementation |
|----------|:-:|:-:|:-:|-----|
| **React Native / Expo** | Yes | Yes | Yes | `cli/src/bundler.js` → hermesc → HTTP POST to phone. Guest bridge via `ExpoReactNativeFactory`. |
| **Flutter** | -- | Yes | Yes | `devserver.go` `FlutterDevServer`: `flutter run -d <device>`, hot reload via stdin `r`. |
| **Vite** | -- | Yes | -- | `devserver.go` `ViteDevServer`: dev server on port 5173, proxied via P2P. WebView on phone. |
| **Next.js** | -- | Yes | -- | `devserver.go` `NextDevServer`: dev server on port 3000, proxied via P2P. WebView on phone. |
| **Swift / Xcode** | -- | -- | Yes | `build_cmd.go`: `xcodebuild` → TestFlight via `testflight.go`. Full binary each time. |
| **Kotlin / Gradle** | -- | -- | Yes | `build_cmd.go`: Gradle APK/AAB → Play Store via `testflight.go`. Full binary each time. |

**Why only React Native?** RN apps are JavaScript — you can compile JS into Hermes bytecode and load it into a pre-built native container (like Expo Go). Flutter uses Dart VM, Swift/Kotlin compile to machine code. These can't be injected into a container app.

### Technical Glossary (React Native Internals)

Reference for understanding the push-to-device pipeline and bridge architecture.

- **Hermes** — Meta's JavaScript engine for React Native. Pre-compiles JS into bytecode for fast startup. Ships as part of the RN binary.
- **Hermes Bytecode (HBC)** — Compiled JS output. Header: magic `0x1F1903C1` at offset 4, BC version (e.g. 96) at offset 8. Both CLI and phone validate these match. Mismatch = crash (like running Java 21 bytecode on Java 8 JVM).
- **hermesc** — The Hermes compiler binary. Takes JS input, outputs `.jsbundle` HBC file. Yaver embeds its own hermesc (from RN 0.81.5) to guarantee BC version match. Located at `cli/hermesc/`.
- **Metro** — React Native's JavaScript bundler (like webpack). Combines all source files + node_modules into a single JS bundle. Yaver's CLI calls `npx react-native bundle` which uses Metro under the hood.
- **RCTBridge** — The old-architecture bridge between JS and native. Passes JSON messages asynchronously. **Do not use for guest apps** — lacks TurboModule support, crashes on RN 0.81+.
- **ExpoReactNativeFactory** — Expo's factory that creates a bridge with full New Architecture support. Used for both Yaver's own app and guest apps. Configured with `RCTAppDependencyProvider` which registers all TurboModules.
- **RCTAppDependencyProvider** — Registers TurboModules (PlatformConstants, DeviceInfo, etc.) into the bridge's JSI runtime. Without it, `TurboModuleRegistry.getEnforcing()` throws → crash.
- **TurboModules** — New Architecture native modules that use JSI for synchronous, direct JS↔native calls (vs old bridge's async JSON). RN 0.81+ uses TurboModules by default for all core modules.
- **Fabric** — New Architecture rendering system. Uses JSI for direct communication between JS and native UI. Replaces the old async "shadow thread" approach.
- **JSI (JavaScript Interface)** — C++ API that lets native code interact directly with the JS runtime. Foundation for TurboModules and Fabric. Much faster than the old JSON bridge.
- **New Architecture** — Umbrella term for TurboModules + Fabric + JSI. Enabled by default in RN 0.76+. Yaver's `sdk-manifest.json` has `newArch: true, fabric: true`.
- **Native Container** — Yaver's phone app with 40+ native modules pre-compiled in. Guest apps run inside it using the pre-installed modules. Same concept as Expo Go.
- **sdk-manifest.json** — Declares what's available in the container: RN version, Hermes BC version, architecture flags, and all pre-installed native modules with versions. Must match between CLI and phone.
- **Safe Bridge Reload** — The sequence: invalidate old bridge → poll for deallocation (Hermes GC cleanup) → create new bridge via factory. Skipping the wait → SIGABRT from GC touching freed memory.
- **HadesGC** — Hermes's concurrent garbage collector. Runs on a background thread. After bridge invalidation, the GC thread may still be running — must wait for it to finish before creating a new bridge.

## What is Yaver?
Yaver is an open-source P2P tool that lets developers use any AI coding agent (Claude Code, Codex, Aider, Ollama, etc.) from their mobile device or any terminal, connecting directly to their development machines. Task data flows peer-to-peer between your devices — servers only handle auth and peer discovery.

## Architecture Overview
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
       ▲
       │
┌──────┴──────┐
│  Web (Vercel)│
│  yaver.io    │
│  Landing +   │
│  Sign Up     │
└─────────────┘
```

### Connection strategy (direct-first, relay-fallback)
1. Mobile tries **direct connection** to desktop agent (3s timeout) — lowest latency
2. If direct fails, tries **each relay server** in priority order (5s timeout each)
3. Desktop agent connects outbound to **all** relay servers via QUIC tunnels on startup
4. If a relay goes down, traffic automatically routes through remaining relays

## Directory Structure
- `desktop/` — Electron installer (DMG/EXE/DEB) + Go CLI agent
  - `desktop/installer/` — Electron app for installation GUI
  - `desktop/agent/` — Go binary (QUIC server, agent runner, tmux manager)
- `mobile/` — React Native mobile app (iOS + Android) + on-device HTTP server for push-to-device
- `cli/` — `yaver-cli` npm package (push existing RN projects to device)
- `backend/` — Convex backend (auth + peer discovery + platform config)
- `relay/` — QUIC relay server for NAT traversal (Go, self-hostable)
  - `relay/deploy/` — Deployment scripts (up.sh, down.sh, systemd unit)
- `web/` — Next.js landing page, deployed on Vercel at yaver.io
- `keys/` — Private keys, signing scripts (gitignored, not in repo — see `keys/CLAUDE.md` for details)

## Tech Stack
- **Networking**: Application-layer QUIC relay (direct-first, relay-fallback). No TUN/TAP, no VPN rights — won't conflict with user's VPN.
- **Relay Server**: Go with `quic-go`, self-hostable via Docker. Password-protected. Agents connect outbound via QUIC tunnels; mobile makes short-lived HTTP requests.
- **Auth**: Convex + Google Sign-In + Apple Sign-In + Microsoft/Office 365
- **Desktop Agent**: Go with quic-go, runs any AI agent CLI in tmux
- **Desktop Installer**: Electron (electron-builder for DMG/EXE/DEB)
- **Mobile**: React Native (native builds via xcodebuild/Gradle)
- **Web**: Next.js, deployed on Vercel (yaver.io)
- **Backend**: Convex (auth tables + device registry + platform config for relay servers)

## Key Design Decisions
1. **P2P only** — Convex is ONLY for auth, peer discovery, and platform config. Task data, logs, and output flow directly between mobile and desktop agent (via relay if needed, but relay doesn't store anything).
2. **Desktop = installer + CLI** — The Electron app is only for installation. The actual agent is a Go CLI binary.
3. **Privacy-first** — No code, task data, or AI output ever touches our servers. The relay is a pass-through proxy.
4. **Self-hostable relay** — Users can deploy their own relay server with Docker + password auth, or use Tailscale instead.
5. **Multi-relay redundancy** — Multiple relay servers can be configured. If one goes down, traffic routes through others. Clients try all relays in priority order.
6. **Application-layer only** — No TUN/TAP, no VPN rights. Won't conflict with user's existing VPN.
7. **LLM-agnostic** — Works with any terminal AI agent: Claude Code, Codex, Aider, Ollama, Qwen, etc.
9. **Voice-first mobile** — Voice input is always available in the mobile app and Feedback SDK. Audio is recorded on-device and sent to the agent for transcription. S2S providers (PersonaPlex, OpenAI Realtime) are optional for real-time voice conversations.
10. **Provider-agnostic voice** — Like LLM-agnosticism for coding agents, voice is provider-agnostic: PersonaPlex (free, on-prem), OpenAI Realtime (paid, cloud), or any future provider via the VoiceProvider interface.
8. **Session Transfer** — Transfer AI agent sessions (Claude Code, Aider, Codex, Goose, Amp, OpenCode) between machines via `yaver session transfer`. Includes conversation history, agent-specific state files, and optional workspace (via git or tar). Also available as MCP tools for use directly from within AI agents.

## Voice AI Architecture

Yaver supports provider-agnostic real-time voice AI. Voice input is always available (mobile/SDK can always record and send audio). Speech-to-speech providers are optional enhancements.

### Providers

| Provider | Type | Cost | GPU | Setup |
|----------|------|------|-----|-------|
| NVIDIA PersonaPlex 7B | On-prem S2S | Free | NVIDIA A100/H100 or Apple Silicon | `yaver voice setup --provider personaplex` |
| OpenAI Realtime API | Cloud S2S | Paid per token | None (cloud) | `yaver voice setup --provider openai --api-key <key>` |
| Whisper (local) | STT only | Free | Optional | `yaver config set speech.provider whisper` |
| OpenAI Whisper API | STT only | $0.003/min | None | `yaver config set speech.provider openai` |
| Deepgram Nova-2 | STT only | $0.004/min | None | `yaver config set speech.provider deepgram` |
| AssemblyAI | STT only | $0.002/min | None | `yaver config set speech.provider assemblyai` |

### How Voice Flows

```
Phone (mic) → Yaver mobile/SDK → agent HTTP /voice/transcribe → Provider (S2S or STT)
                                                                      ↓
Phone (text result) ← agent HTTP response ← transcribed text ←───────┘
```

- **Always-on voice input**: Mobile app and Feedback SDK always show a mic button. Audio is recorded on-device.
- **Auto-transcription**: If STT or S2S is configured on the agent, audio is transcribed automatically.
- **Fallback**: If no provider is configured, raw audio is saved and attached to the task/feedback for the AI agent.
- **Capability discovery**: Mobile checks `/voice/status` and `/info` (includes `voiceInputEnabled`, `voiceProvider`, `sttProvider`). Beacon includes `vc` flag.

### Key Files

| File | Purpose |
|------|---------|
| `desktop/agent/voice.go` | VoiceProvider interface, registry, GPU detection |
| `desktop/agent/voice_personaplex.go` | PersonaPlex 7B provider (download, serve, stream) |
| `desktop/agent/voice_openai.go` | OpenAI Realtime API provider |
| `desktop/agent/voice_cmd.go` | CLI: `yaver voice setup/serve/status/test/providers` |
| `desktop/agent/voice_http.go` | HTTP: `/voice/status`, `/voice/transcribe`, `/voice/providers`, `/voice/config` |
| `desktop/agent/voice_test.go` | Unit tests for voice subsystem |
| `sdk/feedback/react-native/src/types.ts` | `VoiceCapability` type, `voiceEnabled` in FeedbackConfig |
| `sdk/feedback/react-native/src/P2PClient.ts` | `voiceStatus()`, `transcribeVoice()` methods |

### Cloud Dev Machine (GPU tier)

Two tiers (all dedicated, no sharing):
- **CPU Machine** ($49/mo) — 8 vCPU / 16 GB RAM / 160 GB NVMe (Hetzner CX42). Pre-installed: Node.js, Python, Go, Rust, Docker, Expo CLI, EAS CLI, Yaver server.
- **GPU Machine** ($449/mo) — Dedicated NVIDIA RTX 4000, 20 GB VRAM (Hetzner GEX44). Includes Ollama + Qwen 2.5 Coder 32B, PersonaPlex 7B (voice AI), Whisper (STT). Full local AI stack.
- **Managed Relay** ($10/mo) — shared relay infra, no dedicated server.

**Multi-user / Team mode**: CPU and GPU machines support `--multi-user` mode for team sharing. Each user gets isolated workspace, tasks, and sessions. GPU resources are shared across team members. See "Multi-User Mode" section below.

**Important**: Never mention Hetzner, server costs, or infrastructure provider in customer-facing content (landing page, CLI output, emails). Customers buy convenience and reliability, not a reseller relationship.

## Feedback SDK (Error Capture + Black Box Streaming)

The Feedback SDK captures visual bug reports from device testing and sends them to the AI agent. Available for React Native, Flutter, and Web.

### Error Capture (observe-only, no conflicts)
The SDK **never hijacks global error handlers** — no conflicts with Sentry, Crashlytics, Bugsnag, or any other tool. Two explicit patterns:
- `wrapErrorHandler(existing)` — pass-through wrapper for the error handler chain
- `attachError(err, metadata)` — manual capture in catch blocks
- `wrapConsole()` — opt-in console.log/warn/error interception (BlackBox only)

### Black Box Streaming (flight recorder)
Continuous streaming of all app events to the agent via `/blackbox/events`:
- Event types: `log`, `error`, `navigation`, `lifecycle`, `network`, `state`, `render`
- Ring buffer on agent (last 1000 events per device)
- Injected into fix prompts via `GenerateBlackBoxContext()`
- Fatal crashes auto-create fix tasks
- `/blackbox/subscribe` SSE for live log watching

### Key Files
| File | Purpose |
|------|---------|
| `sdk/feedback/react-native/src/BlackBox.ts` | RN black box streaming client |
| `sdk/feedback/react-native/src/YaverFeedback.ts` | SDK entry, error buffer, wrapErrorHandler |
| `sdk/feedback/react-native/src/FeedbackModal.tsx` | Modal with hot reload button + streaming indicator |
| `sdk/feedback/flutter/lib/src/blackbox.dart` | Flutter black box + NavigatorObserver |
| `sdk/feedback/flutter/lib/src/feedback.dart` | Flutter SDK entry, wrapFlutterErrorHandler |
| `desktop/agent/blackbox.go` | BlackBoxManager, session management, prompt generation |
| `desktop/agent/blackbox_http.go` | HTTP: /blackbox/stream, /events, /logs, /subscribe, /context |

## Multi-User Mode (Shared Machines)

Shared CPU/GPU machines support multiple users with isolated workspaces. Each user authenticates with their own OAuth account (Apple/Google/Microsoft) — no shared passwords, no SSH keys.

### How it works
1. Machine starts with `yaver serve --multi-user --team <teamId>`
2. Team member connects from Yaver app → token validated against Convex → team membership checked
3. `MultiUserManager` creates isolated `UserSession` at `/var/yaver/users/yaver-{userId[:8]}/`
4. Each user gets: own workspace, task queue, feedback reports, AI agent sessions, black box streams
5. GPU resources (Ollama, PersonaPlex) shared across all users

### Team Management
- Teams managed via Convex: `teams` + `teamMembers` tables
- Admin creates team, invites members by email
- Endpoints: `POST /teams`, `POST /teams/members`, `GET /teams/validate`
- Agent validates team membership on every request via `GET /auth/validate` (returns `teams[]`)

### Key Files
| File | Purpose |
|------|---------|
| `desktop/agent/multiuser.go` | MultiUserManager, UserSession, workspace isolation |
| `desktop/agent/multiuser_http.go` | HTTP handlers + multiUserAuth middleware |
| `backend/convex/teams.ts` | Team CRUD mutations/queries |
| `backend/convex/cloudMachines.ts` | Machine provisioning mutations/queries |
| `backend/convex/schema.ts` | teams, teamMembers, cloudMachines tables |
| `scripts/provision-machine.sh` | Hetzner machine provisioning (dev tools + GPU + Yaver) |

### CLI Flags
```bash
yaver serve --multi-user              # Enable multi-user mode
yaver serve --multi-user --team team_abc  # Restrict to team members
yaver serve --multi-user --max-users 10   # Limit concurrent users
```

## SDK Token Security

The Feedback SDK uses a dedicated token system with defense-in-depth security:

### Token Types
- **CLI session token**: Used by `yaver serve`, short-lived, full agent access
- **SDK token**: Long-lived (configurable), scoped to feedback endpoints only, independent from CLI session
- CLI reauth does NOT invalidate SDK tokens (they are separate sessions in Convex)

### 6 Security Layers

| Layer | What | How |
|-------|------|-----|
| **Scope restriction** | SDK tokens limited to feedback/blackbox/voice/builds | `authSDK()` middleware checks path against token scopes |
| **IP binding** | Token restricted to specific networks | `allowedCIDRs` field on sdkTokens, checked per-request |
| **Agent IP allowlist** | Block all external IPs | `--allow-ips` flag, outer middleware before auth |
| **Token rotation** | Rotate without downtime | `POST /sdk/token/rotate`, 5-min grace period |
| **New device alerts** | Detect token use from new IPs | `seenIPs` tracking, events sent to Convex |
| **HTTPS on LAN** | Encrypt LAN traffic | Self-signed TLS cert, port 18443, fingerprint in beacon |

### Auth Middleware Architecture
```
Request → ipAllowlist → CORS → auth()/authSDK()
                                     │
                  ┌──────────────────┼──────────────────┐
                  │                  │                   │
              auth()            authSDK()           /health
          (full access)      (SDK-accessible)      (public)
              │                  │
          Accepts:           Accepts:
          - Agent token      - Agent token (full)
          - CLI session      - CLI session (full)
          - Rejects SDK      - SDK token (scoped)
              │                  │
          Endpoints:         Endpoints:
          /tasks             /feedback
          /exec              /blackbox/*
          /vault             /voice/*
          /agent/*           /builds
          /session/*
          /tmux/*
```

### Key Files
| File | Purpose |
|------|---------|
| `backend/convex/schema.ts` | `sdkTokens` table (scopes, allowedCIDRs, replacedBy/At) |
| `backend/convex/auth.ts` | createSdkToken, validateSdkToken, rotateSdkToken, reportSecurityEvent |
| `backend/convex/http.ts` | POST /sdk/token, GET /sdk/token/validate, POST /sdk/token/rotate |
| `desktop/agent/httpserver.go` | auth(), authSDK(), ipAllowlist(), trackNewIP() middlewares |
| `desktop/agent/auth.go` | ValidateSdkTokenFull(), CreateSdkToken(), ReportSecurityEvent() |
| `desktop/agent/sdk_token.go` | CLI: `yaver sdk-token create` with --scopes, --allowed-ips, --expires |
| `desktop/agent/tls.go` | Self-signed TLS cert generation with IP SANs |
| `desktop/agent/sdk_token_test.go` | 25+ tests: scopes, IP allowlist, IP binding, TLS, cache isolation |

### CLI Commands
```bash
# Create SDK token (default scopes, 1 year)
yaver sdk-token create --label "AcmeStore dev"

# Narrow scopes + IP binding + short expiry
yaver sdk-token create --scopes feedback,blackbox --allowed-ips 192.168.1.0/24 --expires 7d

# Agent IP allowlist
yaver serve --allow-ips 192.168.1.0/24

# Disable HTTPS
yaver serve --no-tls
```

## Networking Stack

Yaver's networking has three layers that work together for instant, reliable connections:

```
┌─────────────────────────────────────────────────────────────────────┐
│                    CONNECTION PRIORITY                               │
│                                                                     │
│  1. LAN Beacon (direct)  ──  ~5ms   ── same WiFi, instant discovery│
│  2. Convex IP (direct)   ──  ~5ms   ── known IP from device registry│
│  3. QUIC Relay (proxied) ──  ~50ms  ── roaming, NAT traversal      │
│                                                                     │
│  Silent roaming: transitions between layers are invisible to user   │
└─────────────────────────────────────────────────────────────────────┘
```

### Layer 1: LAN Beacon Discovery (same network)

UDP broadcast protocol for instant same-network device discovery.

- **CLI** broadcasts a beacon every 3s on UDP port `19837` (`255.255.255.255`)
- **Mobile** listens on port `19837` via `react-native-udp`
- **Auth-aware**: beacon includes a token fingerprint (`th` = first 8 hex chars of SHA256(userId)) — only same-user devices match
- **Beacon payload** (~100 bytes):
  ```json
  {"v":1,"id":"dcbfdc50","p":18080,"n":"MacBook-Air","th":"a1b2c3d4"}
  ```
- Mobile matches beacon `id` against its Convex device list and `th` against its userId fingerprint
- Discovered devices get a `local: true` flag and their IP is used for direct HTTP connection
- If no beacon received for 10s → device marked as not local, falls back to relay
- **Graceful degradation**: if UDP socket fails (OS restriction, permission denied), everything works via Convex + relay

### Layer 2: Convex Device Registry (cross-network)

Central presence hub for auth, pairing, and cross-network visibility.

- **CLI** registers on `yaver serve` start: sends `{deviceId, hostname, platform, localIP, httpPort}` to Convex
- **CLI** heartbeat every 2 minutes includes current local IP (handles DHCP changes, VPN toggles)
- **Mobile** polls device list every 3 seconds — sees devices come online within seconds
- Device is "online" if `isOnline=true` AND `lastHeartbeat` within 5 minutes
- On `yaver serve` stop, CLI marks device offline immediately

### Layer 3: QUIC Relay (NAT traversal / roaming)

Application-layer QUIC relay for when direct connection isn't possible.

- **Desktop agent** connects outbound to all relay servers via QUIC tunnels on startup (solves NAT — no inbound ports needed)
- **Mobile app** makes short-lived HTTP requests to relay (IP changes from Wi-Fi/5G roaming don't matter)
- **Relay is pass-through** — no task data, logs, or AI output is stored on relay servers
- **Password-protected** — relay server requires a shared secret for agent registration and HTTP proxy
- **Reconnection** uses exponential backoff (1s → 2s → 4s → 8s → max 30s)

### Connection Flow

```
Mobile connects to a device:
  │
  ├─ On WiFi?
  │   ├─ LAN beacon found? → direct HTTP to beacon IP:port (2s timeout)
  │   │   └─ Success → mode = "direct" ✓
  │   │
  │   ├─ Convex IP is private? → direct HTTP to Convex IP:port (2s timeout)
  │   │   └─ Success → mode = "direct" ✓
  │   │
  │   └─ Direct failed → try relay servers
  │       └─ Success → mode = "relay" ✓
  │
  ├─ On Cellular? → skip direct, try relay servers immediately
  │   └─ Success → mode = "relay" ✓
  │
  └─ All failed → error, reconnect with exponential backoff (max 15 attempts)

Network changes (WiFi ↔ cellular):
  → Full reconnect with new strategy
  → WiFi→Cellular: relay (direct skipped)
  → Cellular→WiFi: direct first (beacon rediscovered), relay fallback
  → All transitions are silent — no UI disruption
```

### Key Files

| File | Purpose |
|------|---------|
| `desktop/agent/beacon.go` | UDP broadcast beacon (send every 3s) |
| `desktop/agent/httpserver.go` | HTTP server on `0.0.0.0:18080` |
| `desktop/agent/quic.go` | QUIC server on `0.0.0.0:4433` |
| `mobile/src/lib/beacon.ts` | UDP beacon listener + auth matching |
| `mobile/src/lib/quic.ts` | Connection strategy (direct-first, relay-fallback) |
| `mobile/src/context/DeviceContext.tsx` | Device list, beacon integration, auto-connect |
| `relay/` | QUIC relay server (Go, self-hostable via Docker) |

## Relay Server

Yaver uses application-layer QUIC relay servers for NAT traversal and roaming. Self-hostable via Docker with password auth.

### Relay server config
Relay servers can be configured locally in the CLI config (`~/.yaver/config.json`) or in the mobile app settings. Optionally, they can also be served from Convex `platformConfig`.

### Self-hosting a relay
See `relay/README.md` and `scripts/setup-relay.sh` for automated VPS setup with Docker + nginx + Let's Encrypt.

```bash
# Quick start with Docker
cd relay && RELAY_PASSWORD=your-secret docker compose up -d

# Or use the automated setup script
./scripts/setup-relay.sh <server-ip> <domain> --password <relay-password>
```

### Alternative: Tailscale
If you use Tailscale, you don't need a relay server at all. Just use `yaver serve --no-relay` and connect directly via Tailscale IP.

## Conventions
- Go code: standard Go project layout, `gofmt`
- TypeScript/React: functional components, hooks, no class components
- Convex: mutations for writes, queries for reads, HTTP actions for OAuth callbacks
- Mobile: always native builds (xcodebuild for iOS, Gradle for Android), never Expo CLI

## Tests

### Unit Tests
```bash
cd desktop/agent && go test -v ./...    # Run all agent tests (HTTP API, auth, MCP, ping, shutdown)
cd relay && go test -v ./...            # Run relay tests
```

Tests spin up real HTTP servers on random ports — no mocks, no external dependencies. Covers:
- Health, auth, CORS, task CRUD, agent status, ping/pong, shutdown
- **Server-client integration**: two agents on the same machine, verifies token isolation and task separation
- **MCP protocol**: initialize + tools/list JSON-RPC
- **SDK token security** (`sdk_token_test.go`): scope restriction, IP allowlist, IP binding, TLS cert generation, token cache isolation, new device tracking, cross-user rejection (25+ tests)

### Integration Test Suite
Full end-to-end test suite covering CLI-to-CLI connections via all transport modes, builds, and MCP.

```bash
# Run everything
./scripts/test-suite.sh

# Run specific test sections
./scripts/test-suite.sh --unit           # Go unit tests only
./scripts/test-suite.sh --builds         # Build verification (all platforms)
./scripts/test-suite.sh --lan            # LAN direct connection (localhost)
./scripts/test-suite.sh --relay          # Local relay server test
./scripts/test-suite.sh --relay-docker   # Deploy relay to Hetzner via Docker, test, teardown
./scripts/test-suite.sh --relay-binary   # Deploy relay to Hetzner as native binary, test, teardown
./scripts/test-suite.sh --tailscale      # Tailscale cross-machine (local ↔ Hetzner)
./scripts/test-suite.sh --cloudflare     # Cloudflare tunnel test

# Combine flags
./scripts/test-suite.sh --unit --lan --relay
```

**What it tests:**
| Section | What | Infra needed |
|---------|------|-------------|
| `--unit` | Go agent + relay unit tests | None |
| `--builds` | CLI (current + linux/amd64), relay, web, backend typecheck, mobile typecheck, iOS, Android | Node.js, Go, Xcode (macOS), Java 17 (Android) |
| `--lan` | Auth rejection, task CRUD via direct HTTP, MCP protocol | Convex backend (for test account) |
| `--relay` | Local relay + agent registration, task flow via relay proxy, password rejection | Convex backend |
| `--relay-docker` | Deploy relay to Hetzner via Docker, register agent, test proxy, teardown | `REMOTE_SERVER_IP` + SSH key |
| `--relay-binary` | Deploy relay binary to Hetzner, register agent, test proxy, teardown | `REMOTE_SERVER_IP` + SSH key |
| `--tailscale` | Deploy agent to Hetzner, connect via Tailscale IPs, task flow | `REMOTE_SERVER_IP` + Tailscale on both machines |
| `--cloudflare` | Quick tunnel + named tunnel with CF Access service token | `cloudflared` + CF credentials |

**Credentials:** Loaded from (in priority order):
1. Environment variables (for CI — use GitHub Actions secrets)
2. `.env.test` in repo root (gitignored)
3. `../talos/.env.test` (for keeping creds outside this repo)

See `.env.test.example` for all available variables.

**No credentials needed:** `--unit`, `--lan`, `--relay` work out of the box.
**Need remote server:** `--relay-docker`, `--relay-binary`, `--tailscale` require `REMOTE_SERVER_IP` and SSH key.
**Need Cloudflare:** `--cloudflare` requires `cloudflared` installed (`brew install cloudflared`).

**CI:** Runs via `.github/workflows/test-suite.yml` on pushes to `main` and via manual `workflow_dispatch`.

### Running remote tests (private — credentials in .env.test)
The `.env.test` file (gitignored) contains credentials for the shared Hetzner server used by Talos/Yaver. It's loaded automatically by the test suite. To run remote tests:
```bash
# Remote relay + Tailscale + Cloudflare tests
./scripts/test-suite.sh --relay-docker --relay-binary --tailscale --cloudflare

# Full suite (all transports)
./scripts/test-suite.sh --unit --lan --relay --relay-docker --relay-binary --tailscale --cloudflare
```
The test suite auto-detects the remote server's CPU architecture (aarch64 on the Hetzner server) and cross-compiles accordingly. Each remote test deploys, tests, and tears down — nothing is left running on the server after the test suite finishes. Credentials are in `.env.test` or `../talos/.env.test` — **never commit these to the repo**.

## Local Development
- `cd backend && npx convex dev` — Start Convex dev server
- `cd web && npm run dev` — Start web dev server
- `cd mobile/ios && xcodebuild ...` or open in Xcode — Build and run on device/simulator
- `cd desktop/agent && go run . serve` — Run desktop agent
- `cd desktop/installer && npm run dist` — Build desktop installers (Electron GUI)
- `cd relay && go run . serve --password your-secret` — Run relay server locally

### CLI Development (`desktop/agent/`)
The `yaver` CLI is a Go binary in `desktop/agent/`. Run from source during development:
```bash
cd desktop/agent
go run . auth       # Sign in via browser (Apple/Google/Microsoft)
go run . serve      # Start agent server
go run . connect    # Connect to a remote agent
go run . status     # Show auth status
go run . devices    # List registered devices
go run . relay      # Manage relay server config
go run . help       # Show all commands
```

Build a local binary: `cd desktop/agent && go build -o yaver .`

### CLI Release Process
1. Cross-compile: `GOOS=darwin GOARCH=arm64 go build -o yaver-darwin-arm64 .` (repeat for darwin/amd64, linux/arm64, linux/amd64, windows/amd64)
2. Sign Windows .exe: `./keys/sign-windows.sh yaver-windows-amd64.exe` (requires SimplySign Desktop logged in)
3. Create GitHub release with all binaries
4. Update SHA256 hashes in homebrew Formula and Scoop manifest
5. Users install via:
   - macOS/Linux: `brew tap kivanccakmak/yaver && brew install yaver`
   - Windows: `scoop bucket add yaver https://github.com/kivanccakmak/scoop-yaver && scoop install yaver`

### CLI Auth Flow
`yaver auth` opens `https://yaver.io/auth?client=desktop` in the browser. The web app handles OAuth (Apple/Google/Microsoft) and redirects back to `http://127.0.0.1:19836/callback?token=<token>`. The CLI's local HTTP server receives the token and saves it to `~/.yaver/config.json`. The token is long-lived and persists across reboots — no re-auth needed.

### Systemd Service (Linux — run on boot, auto-update)
For headless machines (Mac Mini, cloud VPS, dev servers), install Yaver as a systemd user service:
```bash
# One-time setup:
yaver auth                    # Sign in (requires browser — do this once)
yaver serve --install-systemd # Creates + enables + starts the service

# That's it. Yaver now:
# - Starts automatically on login/boot
# - Auto-updates from GitHub releases (checks every 6h, restarts with new binary)
# - Runs from $HOME, discovers all projects automatically
# - Survives reboots
```

**Management commands:**
```bash
systemctl --user status yaver   # Check status
journalctl --user -u yaver -f   # Live logs
systemctl --user restart yaver  # Restart
systemctl --user stop yaver     # Stop
systemctl --user disable yaver  # Disable auto-start
```

**How auth survives reboot:** `yaver auth` saves the token to `~/.yaver/config.json`. The systemd service reads it on startup. OAuth sign-in is only needed once — the token is long-lived.

**Auto-update:** The agent checks GitHub releases every 6 hours. When a new version is found, it downloads the binary, replaces itself, and exits. Systemd automatically restarts with the new version (via `Restart=on-failure`).

**macOS (launchd):** macOS doesn't use systemd. On macOS, `yaver serve` forks to background automatically and writes a PID file. Use `yaver stop` / `yaver logs` to manage. For login-item auto-start, use the Yaver desktop installer (`desktop/installer/`).

## Deployments

### Convex Backend
```bash
cd backend
npx convex dev --once    # Push to dev
npx convex deploy --yes  # Push to prod
```

### Web (Vercel)
> **Auto-deploy is disabled** in `vercel.json`. Deploy manually:
> ```bash
> ./scripts/deploy-vercel.sh
> ```

Required Vercel env vars — see `web/.env.example`

### Relay Server
See `relay/README.md` for full deployment guide.

```bash
# Deploy via Docker
cd relay && ./deploy/up.sh <server-ip> --docker

# Stop relay
cd relay && ./deploy/down.sh <server-ip>

# Health check
curl https://<your-relay-domain>/health
```

### iOS — TestFlight (Local, No EAS, No Fastlane)

#### First-time setup
```bash
cd mobile
npx expo prebuild --platform ios
cd ios && pod install
```
> **Warning**: `npx expo prebuild --clean` resets CFBundleVersion to 1. Restore manually.

#### Deploy to TestFlight
```bash
export APP_STORE_KEY_PATH="$HOME/Workspace/talos/mobile/ios/AuthKey_77Z6B543D5.p8"
export APP_STORE_KEY_ID="77Z6B543D5"
export APP_STORE_KEY_ISSUER="7bd9329e-49b0-440a-97ed-873c74244c12"
export APPLE_TEAM_ID="5SJZ4KA39A"
./scripts/deploy-testflight.sh
```
> The script auto-bumps CFBundleVersion, archives, and uploads to TestFlight in one step.
> **Always deploy iOS and Android together** when making mobile changes:
> ```bash
> # iOS
> export APP_STORE_KEY_PATH="$HOME/Workspace/talos/mobile/ios/AuthKey_77Z6B543D5.p8"
> export APP_STORE_KEY_ID="77Z6B543D5"
> export APP_STORE_KEY_ISSUER="7bd9329e-49b0-440a-97ed-873c74244c12"
> export APPLE_TEAM_ID="5SJZ4KA39A"
> ./scripts/deploy-testflight.sh
>
> # Android
> JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh && \
>   PLAY_STORE_KEY_FILE=keys/google-play-service-account.json python3 scripts/upload-playstore.py
> ```

**Manual steps** (if script fails or for more control):
```bash
# 1. Bump build number
cd mobile/ios
/usr/libexec/PlistBuddy -c "Set CFBundleVersion $(( $(/usr/libexec/PlistBuddy -c 'Print CFBundleVersion' Yaver/Info.plist) + 1 ))" Yaver/Info.plist

# 2. Archive
xcodebuild -workspace Yaver.xcworkspace -scheme Yaver -configuration Release \
  -archivePath /tmp/Yaver.xcarchive archive \
  DEVELOPMENT_TEAM="$APPLE_TEAM_ID" CODE_SIGN_STYLE=Automatic \
  ENABLE_USER_SCRIPT_SANDBOXING=NO -allowProvisioningUpdates \
  -authenticationKeyPath "$APP_STORE_KEY_PATH" \
  -authenticationKeyID "$APP_STORE_KEY_ID" \
  -authenticationKeyIssuerID "$APP_STORE_KEY_ISSUER" \
  -derivedDataPath /tmp/YaverBuild

# 3. Export & upload
cat > /tmp/ExportOptions.plist <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>method</key><string>app-store-connect</string>
  <key>teamID</key><string>$APPLE_TEAM_ID</string>
  <key>signingStyle</key><string>automatic</string>
  <key>destination</key><string>upload</string>
</dict></plist>
PLIST
xcodebuild -exportArchive -archivePath /tmp/Yaver.xcarchive \
  -exportOptionsPlist /tmp/ExportOptions.plist \
  -exportPath /tmp/YaverExport -allowProvisioningUpdates \
  -authenticationKeyPath "$APP_STORE_KEY_PATH" \
  -authenticationKeyID "$APP_STORE_KEY_ID" \
  -authenticationKeyIssuerID "$APP_STORE_KEY_ISSUER"
```

### Android — Google Play (Local)

#### First-time setup
```bash
cd mobile
npx expo prebuild --platform android
```

#### Deploy to Google Play
```bash
# Full deploy: bump versionCode + build AAB + upload to internal testing
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh && \
  PLAY_STORE_KEY_FILE=keys/google-play-service-account.json python3 scripts/upload-playstore.py

# Or step by step:
# 1. Build (bumps versionCode, builds AAB):
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh
# 2. Upload to internal testing track:
PLAY_STORE_KEY_FILE=keys/google-play-service-account.json python3 scripts/upload-playstore.py
```
> **The service account key is approved and working.** Upload goes directly to internal testing track.
> Always run both commands together — build then upload. The upload script auto-finds the AAB.
**Keystore**: `keys/yaver-upload.keystore` (alias: `yaver-upload`, pw: `yaver2024release`)
**Service account**: `keys/google-play-service-account.json`
**Config**: `mobile/android/keystore.properties` (gitignored, references keystore)

#### Build release AAB only (no upload)
Requires Java 17:
```bash
cd mobile/android
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./gradlew bundleRelease
```

#### Known issue: expo-modules-core `components.release` error
If Android build fails with `Could not get unknown property 'release' for SoftwareComponent`, patch `node_modules/expo-modules-core/android/ExpoModulesCorePlugin.gradle` line ~91: wrap `from components.release` in `if (components.findByName('release') != null)`. This is an Expo SDK 52 + AGP compatibility issue.

#### Known issue: `expo-share-intent` version
Must use `expo-share-intent@3.2.3` with Expo SDK 52. Version 4+ requires Expo 53+, version 6+ requires Expo 55+.

### SDK Publishing

#### npm (`yaver-cli`) — Push-to-Device CLI
Requires npm org `@yaver` and a granular access token with publish permission.
Token stored as `NPM_TOKEN` GitHub Actions secret. **Never commit tokens to repo.**
```bash
# Local publish (token in .npmrc, gitignored)
echo "//registry.npmjs.org/:_authToken=YOUR_TOKEN" > cli/.npmrc
cd cli && npm install && npm publish --access public
```
Before publishing, ensure:
1. `cli/sdk-manifest.json` matches `mobile/sdk-manifest.json` (copy if updated)
2. `cli/hermesc/` contains hermesc binaries for all platforms (from yaver.io's exact Hermes build)
3. Version in `cli/package.json` is bumped

#### npm (`@yaver/sdk`) — Programmatic SDK
Same npm org and token as above.
```bash
echo "//registry.npmjs.org/:_authToken=YOUR_TOKEN" > sdk/js/.npmrc
cd sdk/js && npm publish --access public
```

#### PyPI (`yaver`)
Requires a PyPI API token. Token stored as `PYPI_TOKEN` GitHub Actions secret.
```bash
cd sdk/python
python3 -m build
python3 -m twine upload dist/* --username __token__ --password YOUR_PYPI_TOKEN
```

#### Flutter/Dart (`yaver` on pub.dev)
Requires a pub.dev account linked to a verified publisher.
```bash
cd sdk/flutter && dart pub publish
```

#### Go (`github.com/kivanccakmak/yaver.io/sdk/go/yaver`)
No publishing needed — Go modules import directly from GitHub via `go get`.

#### C shared library
Built locally per-platform. Not published to a registry.
```bash
cd sdk/go/clib
go build -buildmode=c-shared -o libyaver.so .   # Linux
go build -buildmode=c-shared -o libyaver.dylib . # macOS
```

### SDK Testing
```bash
./scripts/test-suite.sh --sdk   # Unit + integration (starts agent, tests all SDKs)
```

### Version Bumping (before releases)
Update version in **four** places — all must match:
1. `mobile/app.json` → `expo.version` (e.g. "1.0.1")
2. `mobile/ios/Yaver/Info.plist` → `CFBundleShortVersionString` (e.g. "1.0.1")
3. `mobile/ios/Yaver.xcodeproj/project.pbxproj` → `MARKETING_VERSION` (e.g. 1.0.1) — appears twice (Debug + Release)
4. `mobile/android/app/build.gradle` → `versionName` (e.g. "1.0.1")
5. `desktop/agent/main.go` → `const version` (e.g. "1.40.0")
6. `web/app/page.tsx` → version badges (grep for old version number)

Build numbers (CFBundleVersion / versionCode) are auto-incremented by deploy scripts.
