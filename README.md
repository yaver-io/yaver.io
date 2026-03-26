# Yaver

[![Tests](https://github.com/kivanccakmak/yaver.io/actions/workflows/test-suite.yml/badge.svg)](https://github.com/kivanccakmak/yaver.io/actions/workflows/test-suite.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)

**Your code never leaves your machine.** Yaver is an open-source P2P tool that lets developers use any AI coding agent (Claude Code, Codex, Aider, Ollama, etc.) from their mobile device, desktop app, or any terminal — connecting directly to their development machines with encrypted P2P connections. Free and open-source. Self-host everything. No vendor lock-in.

## Key Features

- **P2P Encrypted** — Your code, tasks, and AI output flow directly between your devices. Servers only handle auth.
- **Any AI Agent** — Claude Code, Codex, Aider, Ollama, Goose, Amp, OpenCode, or any custom CLI tool.
- **Remote Exec** — Run shell commands on any device (like SSH but through Yaver's transport).
- **Session Transfer** — Move AI sessions between machines. Start on your laptop, continue on your server.
- **Task Scheduling** — Cron-like scheduling for AI tasks. Run code reviews every morning.
- **Notifications** — Telegram, Discord, Slack alerts when tasks complete.
- **MCP Tools** — 473 tools: file search, git ops, exec, screenshots, session transfer — usable from Claude Desktop, Cursor, VS Code, Windsurf, Zed.
- **CI/CD Webhooks** — Trigger AI tasks from GitHub Actions, GitLab CI, or any webhook.
- **Hot Reload** — Expo, Flutter, Vite, Next.js — start dev servers and hot reload from your phone over P2P. Native app preview in a WebView, works through any network.
- **Git Providers** — Auto-detects GitHub and GitLab credentials on your dev machine. Browse repos from the app and clone to a headless server — no SSH, no manual git setup.
- **Free Relay** — Every user gets a free relay server (public.yaver.io). Self-host your own anytime.
- **SDKs** — Go, Python, JS/TS, Flutter/Dart, C — embed Yaver in your own apps.

## Code from the Beach

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

### Visual Feedback Loop

The killer feature: test your build on your real device, record bugs visually, and the AI agent fixes them.

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

**Install:**

```bash
# Web (any framework: React, Vue, Svelte, vanilla JS)
npm install @yaver/feedback-web

# React Native
npm install @yaver/feedback-react-native

# Flutter
# Add to pubspec.yaml: yaver_feedback: ^0.1.0
```

**Quick start (Web):**
```typescript
import { YaverFeedback } from '@yaver/feedback-web';

if (process.env.NODE_ENV === 'development') {
  YaverFeedback.init({ trigger: 'floating-button' });
  // That's it. A "Y" button appears. Click to record bugs.
  // Auto-discovers your Yaver agent on the LAN.
}
```

**Quick start (React Native):**
```tsx
import { YaverFeedback, YaverConnectionScreen } from '@yaver/feedback-react-native';

if (__DEV__) {
  YaverFeedback.init({ trigger: 'shake' }); // Shake phone to report bug
}

// In your dev settings:
<YaverConnectionScreen />  // Shows discovery + feedback controls
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

**Key capabilities:**

- **Repo switching** — `yaver repo switch my-app` auto-discovers git repos under `~/` and changes the agent's working directory. No manual path typing.
- **Auto-detect testing** — `yaver test unit` detects your framework (Flutter, Jest, pytest, Go test, Cargo, XCTest, Espresso, Playwright, Cypress, Maestro) and runs the right command. Pass/fail counts stream to your phone.
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

# Sign in & start agent
yaver auth
```

### All Installation Methods

| Method | Command |
|--------|---------|
| **Homebrew** | `brew install kivanccakmak/yaver/yaver` |
| **Scoop** | `scoop bucket add yaver https://github.com/kivanccakmak/scoop-yaver && scoop install yaver` |
| **Winget** | `winget install Yaver.Yaver` |
| **Chocolatey** | `choco install yaver` |
| **AUR** | `git clone https://github.com/kivanccakmak/aur-yaver.git && cd aur-yaver && makepkg -si` |
| **apt** | See [download page](https://yaver.io/download) for repo setup |
| **RPM** | `sudo rpm -i https://github.com/kivanccakmak/yaver.io/releases/latest/download/yaver_latest_x86_64.rpm` |
| **Nix** | `nix run github:kivanccakmak/yaver.io` |
| **Docker** | `docker run --rm kivanccakmak/yaver-cli version` |
| **curl** | `curl -fsSL https://yaver.io/install.sh \| sh` |
| **PowerShell** | `irm https://yaver.io/install.ps1 \| iex` |
| **Binary** | Download from [releases](https://github.com/kivanccakmak/yaver.io/releases) |

### Desktop App (GUI)

Download the desktop app with full GUI from the [download page](https://yaver.io/download) — available as DMG (macOS), installer (Windows), deb/AppImage (Linux).

## MCP Integration

Yaver implements the Model Context Protocol (MCP) with 473 tools. Connect from Claude Desktop, Cursor, VS Code, Windsurf, Zed, or any MCP-compatible client.

### One-Command Setup

```bash
yaver mcp setup claude       # Claude Desktop
yaver mcp setup cursor       # Cursor
yaver mcp setup vscode       # VS Code
yaver mcp setup windsurf     # Windsurf
yaver mcp setup zed          # Zed
yaver mcp setup show         # Show config JSON (copy/paste manually)
```

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
| **Platforms** | Supabase, Convex, Cloudflare (Workers/Pages/R2/D1/KV), Vercel, Netlify, Firebase, Fly.io, Railway | 33 |
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

## Hot Reload — Dev Server to Phone

Start a dev server on your machine and preview the app on your phone in real time — all through the P2P channel. Works on any network (Wi-Fi, 4G, behind NAT).

```bash
# From the Yaver mobile app: tap a project → Hot Reload
# Or from CLI:
yaver dev start --framework expo     # Expo / React Native
yaver dev start --framework flutter  # Flutter
yaver dev start --framework vite     # Vite
yaver dev start --framework nextjs   # Next.js
```

The agent starts the framework's dev server locally, then proxies it through the P2P channel. Your phone loads the web version in a full-screen WebView. Save a file → the app auto-reloads on your phone.

**Supported frameworks:**

| Framework | Dev Server | Hot Reload |
|-----------|-----------|------------|
| Expo / React Native | Metro (`npx expo start`) | Auto (Metro watches files) |
| Flutter | `flutter run -d web` | Auto (`r` keystroke) |
| Vite | `npx vite` | Auto (Vite HMR) |
| Next.js | `npx next dev` | Auto (Fast Refresh) |

**Expo modes:** Web preview (default), Expo Go deep link (`exp://` for full native modules), or dev client (custom native build with all native modules).

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

| Directory | What | Tech |
|-----------|------|------|
| `desktop/agent/` | CLI agent (QUIC server, MCP, runner, sandbox) | Go |
| `desktop/installer/` | Installation GUI (DMG/EXE/DEB) | Electron |
| `mobile/` | iOS & Android app | React Native |
| `backend/` | Auth, peer discovery, platform config | Convex |
| `relay/` | QUIC relay server for NAT traversal | Go (quic-go) |
| `web/` | Landing page & docs | Next.js 15 on Vercel |

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
yaver status        Show auth, agent, relay, and connection status
yaver doctor        System health check (auth, runners, relay, network)
yaver devices       List registered devices
yaver exec          Execute a command on a remote device
yaver session       Transfer AI agent sessions between machines
yaver build         Build apps (Flutter, Gradle, Xcode, React Native)
yaver test          Run tests (auto-detect framework)
yaver deploy        Deploy to phone, TestFlight, Play Store, or CI
yaver debug         Hot reload debug sessions
yaver repo          Switch between projects
yaver vault         P2P encrypted key management
yaver pipeline      Build → test → deploy in one command
yaver feedback      Visual bug reports (list/show/fix) — screen recording + voice from device
yaver cloud         Cloud dev machines (coming soon)
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
| **Web** | `npm install @yaver/feedback-web` | Floating button, keyboard shortcut (Ctrl+Shift+F), manual |
| **React Native** | `npm install @yaver/feedback-react-native` | Shake-to-report, floating button, manual |
| **Flutter** | `yaver_feedback: ^0.1.0` in pubspec.yaml | Shake, floating button, manual |

All SDKs include: auto device discovery, connection UI, screen recording, voice annotation, three runtime modes (Full Interactive / Semi Interactive / Post Mode), agent commentary, voice commands.

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

## Legal

- [Privacy Policy](https://yaver.io/privacy)
- [Terms of Service](https://yaver.io/terms)

Developed by **SIMKAB ELEKTRIK** — Istanbul, Turkey

Contact: kivanc.cakmak@simkab.com

## License

MIT — Free and open source.
