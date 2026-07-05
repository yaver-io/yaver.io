# Show HN draft

## Title (pick one; HN caps ~80 chars)

1. **Show HN: Yaver – MCP server that builds a full-stack app, then hot-reloads it on your phone**
2. Show HN: Yaver – Open-source MCP for Claude Code/Codex, with phone hot-reload
3. Show HN: Drive Claude Code from your phone and hot-reload the app from inside it

Recommended: **#1** — names the loop, concrete, no jargon-first.

---

## Body

Hi HN — I'm Kıvanç. Yaver is an open-source MCP server. You register it in Claude
Code, Codex, or OpenCode; the agent then scaffolds and builds a full-stack app
**on your own machine**, and you hot-reload that app on your phone — so you keep
iterating from inside the running app instead of alt-tabbing between a terminal
and a simulator.

The loop I kept wanting:

1. `claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp`
2. In the agent chat: `call yaver_lazy_setup` → tap the sign-in link, pair your
   phone in-chat.
3. Ask the agent to build something. It runs on your box — your subscription,
   your files, your git.
4. The app hot-reloads on your paired phone. Shake to send a screenshot + logs +
   repro context back to the agent. Repeat.

**How it works (the interesting parts):**

- **MCP-first.** Nothing to install to try it — `npx` pulls the server on first
  run. The agent drives your machine through MCP tools: run, build, deploy, git,
  hot-reload, tunnels, sessions.
- **Real hot-reload, not a WebView.** For React Native / Expo, the CLI compiles
  your JS to **Hermes bytecode** and loads it through a real native bridge
  (`ExpoReactNativeFactory` + `RCTAppDependencyProvider`), so TurboModules,
  Fabric, and JSI behave like a production build. Works on iOS and Android.
- **Your code stays on your machine.** Traffic is P2P over QUIC+TLS; on the same
  WiFi the phone finds your machine by LAN broadcast. For remote access there's
  a relay you can **self-host with one Docker command** — a dumb pipe that
  forwards bytes and can't read them.
- **Bring your own agent + subscription.** It shells out to whatever CLI you
  have (Claude Code, Codex, OpenCode, and anything OpenCode wraps — Aider, Goose,
  local Ollama…). It uses your existing login; no Yaver API key, no double-bill.

**It's free and open source, and I want to be precise about "self-hosted":**

The CLI, agent, relay, and backend (Convex, via Docker) are genuinely self-
hostable — you can run the whole desktop stack yourself. What's honest to say:
the **mobile app connects through a thin hosted coordination plane** for sign-in
and device discovery — Tailscale-style. No code, files, prompts, or output ever
touch it; that's enforced by a test in the repo (`convex_privacy_test.go` fails
the build if a payload ever contains file contents, prompts, stdout, secrets, or
absolute paths). Pointing the *phone* at your own backend still needs an app
rebuild today — full mobile self-host is what I'm building next.

**License:** core is FSL-1.1-Apache-2.0 (free for any non-competing use, auto-
converts to Apache-2.0 two years after each release); client SDKs are Apache-2.0
from day one.

Repo: https://github.com/kivanccakmak/yaver.io
Install: `npm install -g yaver-cli && yaver auth`
Android APK / QR: https://download.yaver.io · iOS: on the App Store

I built this because "AI writes the code in seconds, but the loop around it —
run it, see it on a device, feed the bug back — still takes hours." Would love
feedback on the loop itself, the MCP surface, and where the mobile self-host
story should go.

---

## First comment (post right after — HN convention)

A few things that didn't fit above:

- **Why a phone at all?** It's where mobile bugs actually reproduce — gestures,
  sensors, real network. Shake-to-capture round-trips the bug to the agent
  without you narrating it.
- **What the coordination plane stores:** sign-in identity, peer-discovery rows,
  audit summaries. Nothing work-derived — the privacy test is the guardrail.
- **Runners auth via your subscription, not API keys** — it mirrors your existing
  agent login to a remote box when you drive one, so usage stays on your plan.
- **On the MCP surface:** a fresh install exposes a focused set of dev/build/
  deploy/hot-reload tools (not a 900-tool firehose); `YAVER_MCP_PROFILE=full`
  unlocks everything.
- **Stack:** Go agent/CLI, React Native app, Next.js dashboard, Go QUIC relay,
  Convex for the coordination metadata. Happy to go deep on any of it.

---

## Posting notes

- Post Tue–Thu, ~8–10am ET (HN morning). Have the repo README + a short demo
  clip (build → phone hot-reload) ready above the fold.
- First comment goes up immediately.
- Be around for the first 2 hours to answer — that window decides the front page.
- Don't mention pricing/paid tiers; it's genuinely free right now. If asked about
  the business model: "managed hosting will come later for people who don't want
  to run their own; everything works without it today."
