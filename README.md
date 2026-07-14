# Yaver

[![Tests](https://github.com/kivanccakmak/yaver.io/actions/workflows/test-suite.yml/badge.svg)](https://github.com/kivanccakmak/yaver.io/actions/workflows/test-suite.yml)
[![License: FSL-1.1-Apache-2.0](https://img.shields.io/badge/License-FSL--1.1--Apache--2.0-blue.svg)](docs/planning/LICENSING.md)

**Yaver is an open-source, self-hostable MCP server: your coding agent — Claude Code, Codex, or OpenCode — builds a full-stack app on your own machine, then you hot-reload it across iOS, Android, watch, TV, car, and AR/VR surfaces and keep iterating from inside the running app.**

Your code stays on your machine. The `yaver` agent runs on your Mac, Linux box, WSL machine, Pi, or VPS; the client surfaces are the remote controls and preview targets. iOS and Android are the deepest path today, and the repo also carries watch, TV, car, and AR/VR work. The CLI, agent, relay, and backend are all self-hostable — client apps connect through a thin hosted coordination plane today (identity + device discovery only; your code stays P2P), and full client self-host is on the way.

<p align="center">
  <a href="https://github.com/kivanccakmak/yaver.io/releases/download/yaver-hosting-demo-v1/yaver-hosting-demo.mp4">
    <img src="demo-videos/yaver-hosting-demo.gif" alt="Yaver phone-to-agent demo animation" width="720">
  </a>
</p>

<p align="center">
  <a href="https://github.com/kivanccakmak/yaver.io/releases/download/yaver-hosting-demo-v1/yaver-hosting-demo.mp4">Watch the landing demo</a>
</p>

## What Works Today

- Run Claude Code, Codex, OpenCode, Aider, Goose, or another terminal agent from the Yaver agent.
- Push React Native / Expo bundles to a paired phone through the native Hermes path.
- Use Yaver surfaces for iOS, Android, watch, TV, car, and AR/VR workflows.
- Capture dev-build feedback with screenshots, logs, and replay context.
- Stream task, build, and reload progress back to mobile or the web dashboard.
- Keep peer discovery, relay, and vault flows local-first and self-hostable.
- Use SDKs and examples for React Native, Flutter, web, Unity, Go, Python, and JS/TS.

## Quick Start

```bash
npm install -g yaver-cli
yaver auth
yaver serve
```

For headless machines:

```bash
yaver auth --headless
yaver serve
```

If an AI coding agent is setting Yaver up for you, read the canonical machine guide first:

```bash
curl -s https://yaver.io/llms.txt
```

## Use from Claude Code, Codex, or opencode (MCP)

Yaver ships an MCP server, so a coding agent can drive your machine directly. You do **not** need a global install first — `npx` pulls the server on first run. Register it once, then ask the agent to call `yaver_lazy_setup`; it surfaces the sign-in link for you to tap and pairs your device from inside the chat.

```bash
# Claude Code
claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp

# Codex
codex mcp add yaver -- npx -y yaver-cli yaver-mcp

# opencode
npm install -g yaver-cli && yaver mcp setup opencode
```

Codex Desktop can also use the repo-local plugin in `plugins/yaver`, with its marketplace entry in `.agents/plugins/marketplace.json`. The plugin bundles the same `npx -y yaver-cli@latest yaver-mcp` server and a small setup skill.

Already installed Yaver globally? `yaver mcp setup claude-code` (or `codex` / `opencode`) writes the same entry, and `yaver auth` auto-registers every installed runner on first sign-in. Yaver is published to the official MCP registry as `io.github.kivanccakmak/yaver`. Full tool list and HTTP/remote setup: [MCP guide](https://yaver.io/docs/mcp).

## Core Loop

1. Start `yaver serve` on your own machine.
2. Pair the mobile app, web dashboard, or another Yaver surface with that agent.
3. Send a task to your coding agent from the nearest surface.
4. Watch terminal/build/reload progress live.
5. Push the fix to a real device or deploy from your own machine when ready.

## Repository Map

| Path | Purpose |
|---|---|
| `desktop/agent/` | Go agent, CLI surfaces, local API, relay/P2P/runtime integrations |
| `mobile/` | React Native mobile app and native preview container |
| `watch/`, `wear/`, `tvos/` | Apple Watch, Wear OS, and Apple TV client surfaces |
| `web/` | Next.js marketing site and dashboard |
| `backend/convex/` | Hosted identity, session, and device-discovery metadata |
| `relay/` | QUIC relay service |
| `sdk/` | Public SDKs and feedback clients |
| `demo/` | Small fixture apps used to test SDK and push flows |
| `demo-videos/` | Source notes for the landing/demo clips |
| `docs/` | Architecture notes, setup guides, audits, handoffs, and planning material |

## Documentation

- [Docs index](docs/README.md)
- [Setup](docs/setup/SETUP.md)
- [Contributing](docs/setup/CONTRIBUTING.md)
- [Runtime architecture](docs/architecture/AI_ARCH.md)
- [Protocol](docs/yaver-protocol.md)
- [Feedback SDK](docs/mobile/FEEDBACK_SDK.md)
- [Security](docs/security/SECURITY.md)
- [License](docs/planning/LICENSING.md)

Markdown in this repo is context, not source of truth. If a doc and the code disagree, trust the code and fix the doc in the same change.

## Development

```bash
# Web dashboard / landing
cd web
npm install
npm run dev

# Go agent tests
cd desktop/agent
go test ./...
```

Run the narrower package tests for the area you change; the full repo spans Go, Node, React Native, Swift, Kotlin, Flutter, Unity, and embedded C work.

## License

Core Yaver code is under FSL-1.1-Apache-2.0. SDK packages are Apache-2.0 where marked. See [docs/planning/LICENSING.md](docs/planning/LICENSING.md).
