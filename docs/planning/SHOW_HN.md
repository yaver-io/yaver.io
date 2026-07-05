# Show HN draft

## Title options (pick one; HN title ~80 char limit)

1. **Show HN: Yaver – MCP server that builds a full-stack app, then hot-reloads it on your phone**
2. Show HN: Yaver – Open-source MCP for Claude Code/Codex, with phone hot-reload
3. Show HN: Drive Claude Code from your phone and hot-reload the app from inside it

Recommended: **#1** (concrete, names the loop, no jargon-first).

---

## Body

Hi HN — I'm Kıvanç. Yaver is an open-source, self-hostable MCP server. You
register it in Claude Code, Codex, or OpenCode; the agent then scaffolds and
builds a full-stack app **on your own machine**, and you hot-reload that app on
your phone — so you keep iterating from inside the running app instead of
alt-tabbing between a terminal and a simulator.

The loop I kept wanting:

1. `claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp`
2. In the agent chat: `call yaver_lazy_setup` → tap the sign-in link, pair your
   phone in-chat.
3. Ask the agent to build something. It runs on your box (your subscription,
   your files, your git).
4. The app hot-reloads on your paired phone. Shake to send feedback/context
   back to the agent. Repeat.

**How it works (the interesting parts):**

- **MCP-first.** No global install needed to try it — `npx` pulls the server on
  first run. The agent drives your machine through MCP tools (run, build,
  deploy, git, tunnels, hot-reload, sessions).
- **Real hot-reload, not a WebView.** For React Native / Expo, the CLI compiles
  your JS to **Hermes bytecode** and loads it through a real native bridge
  (`ExpoReactNativeFactory` + `RCTAppDependencyProvider`), so TurboModules,
  Fabric, and JSI behave exactly like a production build. Works on iOS and
  Android.
- **Your code stays local.** Traffic is P2P over QUIC+TLS; on the same WiFi the
  phone finds your machine by LAN broadcast. For remote access there's a relay
  you can **self-host with one Docker command** — it's a dumb pipe that forwards
  bytes and can't read them.
- **Bring your own agent + subscription.** It shells out to whatever CLI you
  have installed (Claude Code, Codex, OpenCode, and anything OpenCode wraps —
  Aider, Goose, local Ollama, etc.). It uses your existing login; no Yaver API
  key, no double-billing.

**Honest about the edges (please hold me to these):**

- **Self-host scope.** The CLI, agent, relay, and backend (Convex, via Docker)
  are genuinely self-hostable. The **mobile app currently connects through a
  thin hosted coordination plane** (identity + device discovery only — no code,
  files, prompts, or output ever touch it; that's enforced by tests in the
  repo). Pointing the mobile app at your *own* backend still needs an app
  rebuild — full mobile self-host is the next thing I'm building.
- **iOS direct CLI-push** (phone with no agent running) is stubbed; the
  agent-driven path is the supported one today.
- **Managed cloud / hosted relay** will come later for people who don't want to
  run their own box. Everything above works without them, for free.

**License:** core is FSL-1.1-Apache-2.0 (free for any non-competing use, auto-
converts to Apache-2.0 two years after each release); client SDKs are Apache-2.0
from day one.

Repo: https://github.com/kivanccakmak/yaver.io
Try it: `npm install -g yaver-cli && yaver auth`

I built this because "AI writes the code in seconds, but the loop around it —
run it, see it on a device, feed back the bug — still takes hours." I'd love
feedback on the loop itself, the MCP surface, and where the self-host story
should go next.

---

## First-comment (post immediately after, HN convention)

Some technical detail that didn't fit above:

- **Why a phone at all?** The device is where mobile bugs actually reproduce
  (gestures, sensors, real network). The shake-to-capture sends screenshot +
  logs + repro context straight to the agent, so the fix round-trips without you
  narrating the bug.
- **What Convex stores** (the coordination plane): sign-in identity, peer
  discovery rows, and audit summaries. A test suite (`convex_privacy_test.go`)
  fails the build if a payload ever contains file contents, prompts, stdout,
  vault values, or absolute paths.
- **Runners auth via your subscription**, not API keys — it copies/mirrors your
  existing agent login to the remote box when you drive one, so usage stays on
  your plan.
- Stack: Go agent/CLI, React Native app, Next.js dashboard, Go QUIC relay,
  Convex for the coordination metadata. Happy to go deeper on any of it.
