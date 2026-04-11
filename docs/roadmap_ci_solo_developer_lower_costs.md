# Roadmap: CI for the Solo Developer at $0 / month

> **Persona:** one developer building a React Native (or React + RN) app on
> their own laptop. They ship to TestFlight / Play / a Vercel-or-similar web
> target. They cannot afford a multi-seat SaaS plan, do not want a fleet of
> GitHub-hosted runners, and do not want to maintain Kubernetes for tests.

## Intention

This document is the north star for one slice of Yaver: **make a solo
full-stack developer's CI bill $0/month without giving up real test
coverage, by turning the dev's own machine — the one already sitting
idle most of the day — into the test runner.**

### Why this exists

CI for a solo dev today is a stack of metered SaaS bills (GitHub
Actions minutes, BrowserStack, Sauce Labs, Percy, Vercel preview
builds, etc.) layered on top of a workflow that round-trips through
the cloud even when the dev is sitting two feet from a laptop that
could run the same tests in less time. Most of those services exist
because there was no clean way to drive a browser, an iOS Simulator
and an Android Emulator from a phone or terminal. Yaver already
solves the "drive my dev machine from my phone" half of the problem;
this roadmap is about closing the loop on the other half — driving
the actual test tools (Selenium / Playwright / Appium / xcrun simctl
/ adb / emulator) from the same Yaver agent the developer already
trusts to run their AI coding sessions.

### Constraints we accept

- The dev's laptop is the runner. No "Yaver Cloud," no hosted
  multi-tenant SaaS. The agent is a Go binary the dev installed.
- Everything keeps working when offline. Cloud is optional, never
  required.
- Real tests, not smoke screens. If a feature replaces Percy it has
  to actually diff screenshots. If it replaces BrowserStack it has
  to actually drive a real iOS Simulator and a real Android
  Emulator.
- Open-source friendly. The agent, SDK, mobile app and runner specs
  all live in this repo and stay MIT.
- Free does not mean fragile. Hardware-aware scheduling, retry on
  flake, and clear failure artifacts are part of the deliverable —
  not "the dev figures it out."

### Non-goals

- Replacing release CI (Apple notarization, Play Store upload, code
  signing). Those run rarely enough that GitHub Actions on tag-push
  stays free, and the trust model for distribution belongs in the
  cloud.
- Building a hosted competitor to BrowserStack / Sauce. The point is
  the dev's *own* devices, not someone else's.
- Multi-tenant team CI. Solo + pair is the max for now; team CI is a
  later, separate roadmap.

### What "done" looks like

A solo developer can:

1. Install Yaver, plug their iPhone into their MacBook.
2. Run `yaver ci run mobile-smoke` (or tap a button in the mobile app).
3. Watch the iOS Simulator boot, the app install, and an Appium
   suite drive it — live, mirrored to their phone.
4. See pass / fail / artifacts in the mobile app within seconds.
5. Disable the same job on GitHub Actions for their private repo and
   stop paying for runner minutes that mostly sat in a queue.
6. Continue to use cloud CI for tag-triggered release builds.

If after a month their GitHub Actions bill drops to $0 and their
test coverage went up rather than down, the roadmap shipped.

## Why local-first

The standard pipeline for this persona looks like this:

```
push → GitHub Actions runner → install → unit tests → build → e2e on
BrowserStack/Sauce Labs → screenshot diff on Percy → deploy preview on
Vercel/Netlify → notify in Slack
```

Every box on that line is metered. For a solo dev shipping a few times a
day the bill stacks up:

| Service                   | Free tier                | Where it bites                                   |
| ------------------------- | ------------------------ | ------------------------------------------------ |
| GitHub Actions            | unlimited (public repos) | 2,000 min/mo (private), then $0.008/min Linux    |
| BrowserStack App Live     | none after trial         | $39/mo solo, $159/mo team                        |
| Sauce Labs Real Devices   | none                     | from ~$149/mo                                    |
| Percy visual snapshots    | 5k snapshots/mo          | $149/mo for 25k                                  |
| Vercel preview deploys    | 100 GB-h/mo              | each preview build burns minutes + bandwidth     |
| Cloudflare Workers builds | 500 builds/mo            | each push is one build                           |

The dev's own laptop is **idle 80% of the day** and can run all of the
above for $0. Yaver already has the bones to make that happen — a Go
agent that lives on the laptop, a mobile remote, and a feedback SDK that
streams events out of the running app. The piece missing is "use the
agent as a local CI runner that can drive Chromium and an iOS/Android
emulator the same way GitHub Actions would."

## What Yaver has today

A quick honest inventory of what is actually shipping in this repo as of
2026-04-11. Things in **bold** are usable, things in *italics* are
partial / stubbed.

### Yaver agent (`desktop/agent/`)
- **Spawns AI agent processes** (Claude Code, Codex, Aider, Ollama,
  custom commands) inside tmux and streams stdout back over QUIC/HTTP.
- **Runs arbitrary tasks** with auto-restart on crash and a vault for
  per-task secrets.
- **MCP server** so other tools can talk to it as a model context
  provider.
- **HTTP API** with auth + CORS so the mobile app and other clients can
  drive it from anywhere.
- *Todo list manager* for queueing prompts.
- **Beacon-based LAN discovery** + relay fallback for NAT traversal.
- **Vault sync** between desktop and mobile.

### Mobile app (`mobile/`)
- **Direct + relay + Cloudflare-tunnel transport** with reconnect
  counter and Stop button.
- **Speech in / TTS out** for hands-free task entry.
- **Multi-device + guest invitations** so a second person can hop on.
- *Mobile web build* (`npm run web`) for browser dev.

### Feedback SDK (`sdk/feedback/react-native/`)
- **Shake-to-report**, screen recording, voice annotations.
- **BlackBox event streaming** to the agent.
- **Auto-discovery** of a local agent on the LAN.
- 75 unit tests covering token, P2P client, discovery, types.

### Web (`web/`, `e2e/`)
- **Cloudflare Workers** deploy via `@opennextjs/cloudflare`.
- **Playwright E2E** suite (this directory) hitting the landing + auth
  flow with a throwaway dummy user.

### CI today (`.github/workflows/`)
- `ci.yml` — per-PR Go tests, mobile typecheck, web build, SDK tests.
- `e2e.yml` — Playwright on PR + push when web/ or e2e/ changes.
- `test-suite.yml` — long-running integration suite (relay, tailscale,
  cloudflared variants).
- `release-*.yml` — manual releases for CLI / mobile / relay / SDK.

That's a real foundation. The gap is everything that lives between
"Playwright on a Linux runner" and "Appium on the dev's own iPhone
plugged in over USB" — i.e. the **device side** of the story.

## What's missing for the local-CI vision

Ranked by what unlocks the most cost-saving for the persona:

1. **Yaver-as-CI-runner mode.** A `yaver ci run <suite>` subcommand that
   reads a YAML/JSON spec, sets up the right ports, kicks off the
   browser/device/emulator under test, runs the suite, and emits a
   GitHub-Actions-compatible JSON summary the mobile app can render.
   Today the agent spawns AI processes; it does not "spawn a test
   suite" as a first-class concept.

2. **Browser driver bridge (Selenium / Playwright).** The agent should
   know how to bring up a headless or visible browser on the host and
   forward CDP/WebDriver to it, the same way it forwards an AI runner.
   Playwright already runs anywhere Node runs — the missing piece is a
   stable session protocol so the mobile app can stream live frames or
   trace-on-failure back over the existing P2P channel.

3. **Appium / iOS Simulator / Android Emulator driver bridge.** Same
   shape as #2 but for native device automation. `xcrun simctl boot` +
   Appium for iOS, `emulator -avd` + Appium for Android. The agent has
   to manage simulator lifecycle (boot / install build / record video
   / shutdown).

4. **Build cache reuse with the dev's local toolchain.** Today the dev
   builds the app once on their laptop for development, then GitHub
   Actions builds it again from scratch in CI. The agent already has
   all dependencies installed — give it a `yaver ci build` command that
   uses the existing local cache (Pods, Gradle, Metro) and reports
   "cache hit / cache miss" so the dev can see why a build was slow.

5. **Test-result inbox in the mobile app.** Already half-built via the
   SDK's BlackBox streaming and the in-app log viewer; needs a
   first-class "Runs" tab with status, duration, failure clusters, and
   "rerun this one job" / "open trace" buttons.

6. **GitHub-CI-compatible failure dump.** When a local run fails, it
   should emit the same shape of artifact (`junit.xml`, screenshots,
   trace.zip, video.mp4) so the dev can drag-and-drop into a PR comment
   or upload to GitHub Actions if they ever want a second run.

7. **Visual regression / screenshot diffing.** Solid drop-in for what
   Percy / Chromatic charge for. Per-screen baselines stored in
   `e2e/__snapshots__/` with a CLI flag to update.

8. **Sync-to-cloud-CI bridge.** Optional: when the dev does push, a
   webhook lets GitHub Actions ask "did the last local run on this SHA
   already pass?" and skip redundant work. This is the autotest spec
   already in `docs/autotest-spec.md` — keep building it.

9. **Cron-style nightly runs from the agent.** A real solo dev does
   not babysit CI. The agent should be able to run "every night at
   2am, do a full Playwright + Appium pass on the latest main and
   notify me only if it broke."

10. **Hardware-budget aware scheduling.** If the dev is on battery or
    GPU is busy, defer the run. If they're plugged in and idle, kick
    it off.

11. **Autonomous test → fix → deploy loops.** The endgame: the dev
    defines a spec, a persona and a budget, and the agent runs the
    whole test → AI patch → commit → re-deploy cycle on its own while
    the dev is away from the machine. This is where Yaver stops being
    "a CI runner" and starts being "a second developer the dev hired
    for the price of electricity." See the Autonomous loops section
    below for M8.

## Cost comparison (rough, single dev)

For a dev shipping ~10 PRs/week, ~50 commits/week to a private repo,
running ~30 minutes of tests per push:

| Stack                                                | Monthly cost (USD) | Notes                                                       |
| ---------------------------------------------------- | ------------------ | ----------------------------------------------------------- |
| GitHub Actions private + BrowserStack + Percy        | ~$200              | 200 min over free tier + BS + Percy + Vercel                |
| GitHub Actions public repo only                      | $0                 | Unlimited minutes, but the code is public                   |
| **Yaver local agent + own browser/sim**              | **$0**             | Laptop power. No SaaS bills. Optional Hetzner relay $5/mo   |
| Yaver local + cloud relay shared with team           | ~$5                | One $5 Hetzner box for 2-3 devs                             |

Even setting BrowserStack/Percy aside, replacing the GitHub Actions
30-minute job with a local 30-minute job during dev time is a real
win on its own — feedback latency drops from `push → wait for queue →
wait for runner → download artifacts` (~5–10 min round trip) to
`save → run → see results in mobile app` (~30 sec to first signal).

## Roadmap

Three milestones, ordered by user-visible value, not by code volume.

### M1 — "Run your existing Playwright suite via Yaver" (≈2 weeks of focused work)
- [ ] `yaver ci` subcommand skeleton (cobra subcommand under `desktop/agent/`).
- [ ] YAML test-spec format with `commands:`, `cwd:`, `env:`, `artifacts:`.
- [ ] Spawn `npx playwright test` as a managed task with stdout streaming + exit code propagation.
- [ ] Upload `playwright-report/` and `test-results/` to the agent's artifact store (already exists for tasks).
- [ ] Mobile "Runs" tab: list, status badge, tap to view stdout + artifact links.
- [ ] Re-use the existing E2E suite at `e2e/` as the first thing this can run.

**Acceptance:** `yaver ci run e2e` from a laptop terminal produces the
same pass/fail result as `cd e2e && npm test`, and the mobile app shows
the run with artifacts within 2 seconds of completion.

### M2 — "Drive an iOS Simulator from the agent" (≈3 weeks)
- [ ] `simctl` lifecycle wrapper: boot named device, install `.app`, launch, screenrecord, shutdown.
- [ ] Bridge Appium WebDriver session through the agent so existing Appium scripts work unchanged.
- [ ] Live screen mirroring of the simulator into the mobile app via `simctl io recordVideo` chunks.
- [ ] Same shape for Android Emulator (`emulator -avd`, `adb shell screenrecord`).
- [ ] Test-spec extension: `device:` block with `platform`, `version`, `udid`.

**Acceptance:** A 5-test Appium suite that opens the dev's own RN app
on an iOS Simulator runs end-to-end via `yaver ci run mobile-smoke`,
the dev sees the simulator screen on their phone in real time, and a
failing test surfaces a screenshot + video + Appium log in the Runs
tab.

### M3 — "Replace GitHub Actions for the inner loop" (≈4 weeks)
- [ ] Cron scheduler in the agent: `yaver ci schedule "nightly" "@daily" mobile-smoke web-smoke`.
- [ ] Hardware-aware queue (skip when on battery, when GPU busy, when other Yaver task running).
- [ ] Visual regression: per-screen baseline, `--update-snapshots` flag, diff viewer in mobile app.
- [ ] JUnit XML emitter so the same runs can be uploaded to GitHub Actions if needed.
- [ ] `yaver ci sync` — push the latest passing-on-this-SHA marker to a Convex backend so cloud CI can short-circuit.
- [ ] Mobile "Notify on failure only" toggle.

**Acceptance:** The dev can disable `ci.yml` on private repos for the
preview-PR loop, run everything locally via Yaver, and only re-enable
cloud CI for tag-triggered release builds. Their monthly bill goes
from "GitHub Actions over-quota + BrowserStack + Percy" to "$0 + a $5
relay if they want one."

## yaver-test-sdk: tests embedded in the Go agent itself

M1–M3 above assume the dev brings their own test runner (Playwright,
Appium, etc.) and Yaver just orchestrates it. The next step is to
fold the runner into the agent so the dev does not have to install
*anything* beyond the `yaver` binary itself. We call this slice
**yaver-test-sdk**: a built-in, batteries-included test definition
format + executor that lives inside the Go agent and ships with every
Yaver release.

### Vision

```
my-app/
├── src/
├── yaver-tests/                  ← lives in the user's repo, version-controlled
│   ├── login.test.yaml
│   ├── checkout.test.yaml
│   ├── visual/baseline-home.png
│   └── fixtures/test-user.json
└── yaver.config.yaml             ← optional, project-wide defaults
```

```bash
$ yaver test                       # runs every yaver-tests/**/*.test.yaml
$ yaver test login                 # runs login.test.yaml
$ yaver test --watch               # vibe-coding loop: re-runs on file save
$ yaver test --update-snapshots    # accept current pixels as new baseline
$ yaver test --record              # generate a yaml from a recorded session
```

The agent boots the right runner (Chromium via embedded CDP, an iOS
simulator via `simctl`, an Android emulator via `adb`, a real device
via WebDriverAgent / UIAutomator2) and executes the spec, **without
the user installing Playwright, Appium, ChromeDriver, geckodriver,
or any Node/Python dependency.** Everything the agent needs is either
statically linked into the Go binary, downloaded once into
`~/.yaver/runtimes/` on first use, or driven through Apple/Google's
own simulator tools that already exist on a dev's machine.

### Vibe-coding loop

The whole point of "in-repo + embedded" is to make the loop tight
enough that a dev can write tests by talking to an AI inside their
editor:

```
[edit yaver-tests/login.test.yaml in Cursor / Claude Code]
        │
        ▼
[file watcher in agent re-runs that single spec — <2s feedback]
        │
        ▼
[mobile app shows pass/fail + screenshot + diff]
        │
        ▼
[AI suggests next assertion via the same MCP that's already running]
```

The Yaver agent already exposes an MCP server for AI tools (it lives
under `mcp/`). yaver-test-sdk plugs into the same MCP so any AI agent
on the dev's machine can list specs, read failures, propose patches,
and re-run — without leaving the editor and without going through a
cloud CI round trip.

### Standardized syntax (`*.test.yaml`)

A draft of what a spec looks like. Deliberately Playwright-shaped
because that's the thing most devs already know, but encoded as data
so the agent can read it without spawning Node:

```yaml
# yaver-tests/login.test.yaml
name: login flow
target: web                       # web | ios-sim | android-emu | device
url: http://localhost:3000        # for web
app: ./build/MyApp.app            # for ios-sim/android-emu
viewport: { width: 1280, height: 800 }
artifacts:
  on: failure                     # always | failure | never
  trace: true
  video: true
  screenshot: true

setup:
  - http.post:
      url: "{{env.CONVEX_URL}}/auth/signup"
      body:
        email: "e2e-{{run.id}}@yaver.test"
        password: "{{secrets.test_password}}"
      capture: { token: "$.token" }

steps:
  - goto: /auth
  - fill: { selector: 'input[type=email]', text: "e2e-{{run.id}}@yaver.test" }
  - fill: { selector: 'input[type=password]', text: "{{secrets.test_password}}" }
  - click: 'button:has-text("Sign In")'
  - wait_for_url: /dashboard/
  - assert.visible: 'h1:has-text("Dashboard")'
  - assert.local_storage: { key: yaver_auth_token, exists: true }
  - snapshot: dashboard-loaded   # diffs against visual/dashboard-loaded.png

teardown:
  - http.post:
      url: "{{env.CONVEX_URL}}/auth/delete-account"
      headers: { Authorization: "Bearer {{captured.token}}" }
```

Key properties of the syntax:

- **Pure data, no code.** A spec is just YAML. AIs read and write it
  without running an interpreter. Diffs in PRs make sense.
- **One vocabulary across web + mobile + device.** `goto`, `click`,
  `fill`, `wait_for`, `snapshot`, `assert.*` mean the same thing
  whether the target is a web page, an iOS app or an Android app.
  Behind the scenes the agent picks the right driver.
- **Templating in `{{ }}`** with three scopes: `env.*` (process env),
  `secrets.*` (vault-backed, never logged), and `captured.*` /
  `run.*` (set during the run).
- **Explicit `artifacts:` block** so failures always come back with
  enough breadcrumbs (trace.zip, video.mp4, screenshot.png).
- **`setup:` / `teardown:` are first-class** so a dummy user pattern
  like the one already in `e2e/global-setup.ts` is a one-line
  `http.post` instead of a TypeScript file.

### What ships in the Go binary

The agent already has a process-spawning, port-managing, artifact-
storing runtime. yaver-test-sdk adds:

| Capability                 | How it ships in `yaver`                                                                           |
| -------------------------- | ------------------------------------------------------------------------------------------------- |
| Headless Chromium driving  | Embedded CDP client over WebSocket. No ChromeDriver/Selenium server. Uses the user's Chrome.      |
| Selenium-style WebDriver   | Optional fallback for Firefox / Safari via the standard W3C WebDriver protocol over HTTP.        |
| iOS Simulator              | `simctl` (already on every Mac with Xcode). Lifecycle: boot, install, launch, screenrecord, shutdown. |
| Android Emulator           | `emulator` + `adb` from the Android SDK already in the dev's PATH.                               |
| Real iOS device            | WebDriverAgent + `idevice*` tools, started by the agent on demand.                                |
| Real Android device        | UIAutomator2 + `adb`, same pattern.                                                              |
| Image diffing              | Pure-Go pixel diff (e.g. `imaging` + delta-E). No ImageMagick dependency.                         |
| Trace + video assembly     | Native Go: collect frames + events into a zip the existing mobile app already knows how to render. |
| Snapshot baselines         | Plain PNGs in `yaver-tests/visual/`. Versioned in git.                                            |
| Reporters                  | JUnit XML (for upload to GH Actions), HTML report, JSON for the mobile app, MCP messages for AI.  |
| Spec parser + scheduler    | Pure Go. Fan-out across cores, retry-on-flake, hardware-aware throttling.                         |

The non-negotiable rule: **after `brew install yaver` (or scoop /
apt) the dev should be able to run `yaver test` against any of the
target types above without installing a single extra binary that
ships from npm or pip.** The two exceptions we accept are Apple's
Xcode (which the dev already needs to build the app) and Google's
Android SDK (same).

### Recording mode (`yaver test --record`)

A bootstrap path so a dev never has to write the first spec by hand:

```
$ yaver test --record login
[agent] launching chromium against http://localhost:3000 ...
[agent] open the page in the browser, do the login flow
[agent] press Ctrl+S to save the recorded spec
... user clicks around ...
[agent] saved yaver-tests/login.test.yaml (12 steps, 3 assertions)
```

The agent watches the CDP / Appium event stream, distills clicks
and inputs into the canonical step vocabulary, and writes the YAML.
The dev then opens the YAML in their editor and edits or asks an AI
to refine it. This is what "vibe coding tests" looks like in
practice — the dev thinks in terms of "the thing I just did" and
the agent does the boilerplate.

### Why this beats "just install Playwright"

- **One install.** No `npm install -D playwright @playwright/test`
  with its own version skew per project.
- **No JS toolchain in CI.** A pure Go binary runs the tests, so the
  dev doesn't need Node 20 or pnpm or yarn or corepack on whatever
  machine is running them.
- **Same agent runs AI tasks and tests.** Failure → AI fix → re-run
  is one process talking to itself, not three (editor, AI, test
  runner) coordinating via the filesystem.
- **Mobile-first failure inspection.** The agent already knows how
  to stream artifacts to the phone. A failing test surfaces in the
  Yaver mobile app the same way a failing AI task does.
- **AI-friendly by design.** YAML + MCP + a fixed vocabulary means
  any AI agent can read a failing run, propose a YAML patch, and
  re-run it without any custom integration per AI tool.

### Roadmap addendum — yaver-test-sdk milestones

Slot these in after M3:

#### M4 — "Spec parser + Chromium driver" (≈3 weeks)
- [ ] `desktop/agent/test/` package: spec types, YAML loader, validator.
- [ ] Embedded CDP client (or vendor `chromedp`) so the agent can drive Chrome with no external Selenium server.
- [ ] Step vocabulary: `goto`, `click`, `fill`, `wait_for`, `assert.visible`, `assert.text`, `screenshot`.
- [ ] Templating engine for `{{env.*}}`, `{{secrets.*}}`, `{{captured.*}}`.
- [ ] `yaver test` command, default discovery from `yaver-tests/`.
- [ ] JUnit + JSON reporters; failure artifacts into the existing per-task store.

**Acceptance:** the current `e2e/tests/auth-flow.spec.ts` Playwright
test is rewritten as a 30-line `yaver-tests/auth-flow.test.yaml`,
runs via `yaver test`, and produces the same pass/fail + screenshot
on failure.

#### M5 — "Mobile drivers + recording mode" (≈4 weeks)
- [ ] iOS Simulator driver (`simctl` lifecycle, Appium-style WebDriver
      bridge so existing assertions work).
- [ ] Android Emulator driver (`emulator` + `adb` + UIAutomator2).
- [ ] `target: ios-sim` / `target: android-emu` in spec parser.
- [ ] Live screen mirroring of the device under test into the mobile
      app (reuse the existing video pipeline).
- [ ] `yaver test --record` mode: capture CDP / driver events and
      emit a YAML.

**Acceptance:** `yaver test --record login` against an RN app on
the iOS Simulator produces a YAML the dev can commit, and re-running
that YAML reproduces the flow on a clean simulator boot.

#### M6 — "Visual regression + AI integration" (≈3 weeks)
- [ ] Pure-Go image diff (alpha, delta-E, perceptual).
- [ ] `snapshot:` step + `--update-snapshots` flag.
- [ ] Diff viewer in the mobile app: side-by-side, swipe to compare.
- [ ] MCP tool surface: `yaver.test.list`, `yaver.test.run`,
      `yaver.test.read_failure`, `yaver.test.propose_patch`.
- [ ] `--watch` mode that re-runs the affected spec on file save.

**Acceptance:** an AI agent inside the dev's editor can read a
failing snapshot test via MCP, propose a YAML edit + a baseline
update, and apply both without leaving the editor.

#### M7 — "yaver-test-sdk 1.0" (≈2 weeks)
- [ ] Stable spec schema, semver-bumped on changes.
- [ ] Hardware-aware scheduler for parallel runs across CPU cores
      and multiple devices.
- [ ] Documentation site under `docs/test-sdk/` with the full
      vocabulary, examples for every target, migration guide from
      Playwright + Appium.
- [ ] Marked stable; bundled with the next CLI release.

## Autonomous loops: the agent as a second developer

M1–M7 assume a human is in the tick: the dev edits code, presses a
button, the suite runs, the dev reads the result. M8 removes the
human from the inside of the tick. The dev still owns the outside
(they write the spec, they review the branch in the morning), but
the **test → AI patch → commit → re-deploy → re-test** cycle runs
on its own on the dev's own Mac mini while they sleep.

This is the first milestone where Yaver stops being a CI runner and
starts being an always-on pairing partner that happens to run on the
dev's own hardware. Every primitive needed for it already exists in
the agent — M8 is the harness that wires them together, plus the
safety rails that keep an unattended loop from bricking the repo.

### Why this is worth its own section

The paid AI-QA landscape (Octomind, Waldo, Reflect, QA Wolf, Maestro
Cloud) charges $50–$500/mo for a worse version of this loop that
round-trips through someone else's cloud. Yaver's agent already:

- **Spawns AI agent processes** (Claude Code, Codex, Aider, Ollama,
  custom commands) in tmux — the patching step is already a first-
  class capability, not a new integration.
- Runs arbitrary tasks with crash-restart and per-task vaults.
- Exposes an MCP server so the AI can read artifacts and propose
  patches over a standard surface.
- Has an artifact store that already holds screenshots, traces and
  logs per run.
- Has a scheduler (`desktop/agent/scheduler.go`) that fires cron
  and interval-based tasks.
- Has a mobile app with live status, stop buttons and per-task
  event streams already wired.

The missing piece is a thin loop controller that stitches those
primitives into a single "autonomous dev loop" concept and enforces
the safety rails below. Everything else is reuse.

### M8 — "Autonomous test → fix → deploy loop" (≈3 weeks)

**Loop shape (one iteration):**

```
 ┌─ boot target (expo web / ios-sim / android-emu) ──────────────┐
 │                                                               │
 │  1. agent spawns the app under test                           │
 │  2. embedded CDP driver (M4) + persona fuzzer plays it        │
 │     for N minutes                                             │
 │  3. heuristic detectors flag friction: blank screen, JS       │
 │     console error, stuck-on-same-route, slow transition,      │
 │     low-contrast text, hard crash                             │
 │  4. agent writes a per-iteration report (screenshots,         │
 │     trace, friction log, persona voice-over)                  │
 │                                                               │
 │  5. agent spawns Claude Code / Codex / Ollama in tmux —       │
 │     the same capability that already runs AI dev tasks —      │
 │     feeds it the report, the persona framing, and a           │
 │     "fix one friction point, keep the diff small,             │
 │     do not touch tests" system prompt                         │
 │                                                               │
 │  6. typecheck + unit tests run as a GREEN GATE. If they       │
 │     fail, rollback the worktree, mark iteration "stuck",      │
 │     move on. A patch can never commit over a red build.      │
 │                                                               │
 │  7. commit lands on the branch named in `ship.branch` —      │
 │     defaults to `main` (solo-dev default; the dev owns        │
 │     the repo). Riskier prompts can opt into a loop branch.    │
 │  8. deploy step re-bundles and pushes the new build to        │
 │     the dev's own device via `yaver-push` over the            │
 │     existing transport                                        │
 │                                                               │
 │  9. sleep until next scheduled tick, or loop immediately      │
 │     if the budget allows                                      │
 └───────────────────────────────────────────────────────────────┘
```

**The $0 trick — why step 5 does not send a dollar to anyone.**
The "AI patching" step reuses the agent's existing
`Spawns AI agent processes (Claude Code, Codex, Aider, Ollama,
custom commands)` capability. That means the patching runs under
the dev's own Claude Code subscription (or a local Ollama model,
for fully offline mode). yaver-loop itself never holds an
`ANTHROPIC_API_KEY`, never makes metered HTTP calls, and never
proxies tokens through any cloud Yaver controls. This is the hard
constraint that makes M8 actually free, not "free-tier-until-you-
scale." If a feature in M8 ever requires a metered API key to
function, the feature does not ship.

**Spec format — `.loop.yaml`** (extends the `*.test.yaml` from M4):

```yaml
name: sfmg-genz-playtest
target: web
url: http://localhost:8081
persona: gen-z-turkish

schedule:
  every: 15m                   # or cron: "*/15 * * * *"
  max_iterations: unlimited
  only_when: [plugged_in, idle, not_on_battery]

playtest:
  duration: 3m
  fuzzer: heuristic            # later: persona-llm
  heuristics:
    - blank_screen
    - console_error
    - stuck_on_same_route
    - slow_transition
    - low_contrast
    - crash
  artifacts: { trace: true, video: true, screenshots: per_step }

think:
  runner: claude-code          # or: codex | aider | ollama:llama3
  prompt: .yaver/prompts/genz-fixer.md
  max_edits: 1
  require_green: [typecheck]
  worktree: .yaver/loops/sfmg-genz/worktree

ship:
  branch: main                 # default — solo dev, they own the repo
  commit_prefix: "yaver-loop:"
  deploy: yaver-push push --device mac-mini
  # for riskier prompts, opt into a protected loop branch:
  # branch: yaver-loop/sfmg-genz

budget:
  max_patches_per_day: 20
  max_commits_per_day: 20
  stop_after_consecutive_stuck: 5
```

**What ships in the agent:**

- [ ] `yaver loop run <name>` / `yaver loop stop <name>` /
      `yaver loop list` / `yaver loop pause <name>` CLI, backed by
      the existing `scheduler.go`.
- [ ] Persona registry — YAML files under `yaver-tests/personas/`
      that bias the fuzzer's action selection ("impatient: bounce
      off any screen that takes >2s to respond") and the framing
      passed to the AI patcher ("you are writing for a Gen-Z
      Turkish user whose attention span is ~3 seconds"). Ships
      with `gen-z`, `new-user`, `power-user`, `grumpy-qa`.
- [ ] Heuristic issue detector — blank screen, JS console error,
      stuck-route, slow transition, low-contrast, crash. Zero
      LLM calls. Runs inside the embedded CDP driver from M4.
- [ ] **Fresh-worktree execution.** Every iteration runs in
      `.yaver/loops/<name>/worktree/`, a git worktree checked out
      from the loop branch. The dev's main working tree is never
      touched. A loop gone wrong cannot eat uncommitted work.
- [ ] **Green-gate enforcement.** Typecheck + existing test suite
      must pass before the patch commits. A failing iteration
      rolls back the worktree, records "stuck", and moves on.
- [ ] **Configurable branch policy.** `ship.branch` defaults to
      `main` — for a solo dev, the loop's commits have the same
      trust level as every other commit the dev makes by hand, and
      treating `main` as special adds review friction the persona
      explicitly does not want. Riskier prompts can opt into a
      protected `yaver-loop/<name>` branch per-spec. Regardless of
      branch name, force-push is disabled on any branch the agent
      is managing, and the typecheck green-gate is a hard pre-commit
      check either way.
- [ ] **Stop from anywhere — critical.** The loop must be stoppable
      from three independent surfaces and any of them wins
      immediately:
      1. **Mobile app "Loops" tab** — tap "Stop" on the running
         loop row. The mobile app sends `loop.stop` over the
         existing QUIC/HTTP transport. The agent tears down the
         in-flight iteration cleanly (SIGTERM the fuzzer, kill the
         AI subprocess, roll back the worktree if mid-patch, mark
         the last iteration `aborted_by_user`). No zombie processes,
         no half-committed patches, no orphan simulator boots.
      2. **CLI** — `yaver loop stop <name>` on the Mac mini itself.
      3. **Physical kill file** — `touch ~/.yaver/loops/<name>/STOP`
         is polled every iteration; for the case where the agent
         is wedged and neither the mobile app nor the CLI
         responds.
      Stop is a first-class state, not "the process happened to
      die." The mobile app shows a clear `Stopped by you at 03:14`
      badge on the last row, and the loop stays stopped across
      agent restarts until the dev explicitly resumes it.
- [ ] **Mobile "Loops" tab.** One row per iteration: persona,
      friction summary, patch diff, green/red build, deploy
      status, elapsed time. A persistent header on the tab shows
      the loop's overall state (running / paused / stopped /
      stuck) with a single big **Stop** button next to it. Tapping
      Stop pops a confirmation, then the state flips within one
      round-trip. Also: push notification on `stuck_limit_hit` so
      the dev knows when a loop paused itself.
- [ ] Budget ledger — `.yaver/loops/<name>/budget.json` the dev
      can read in plain text: every commit, every patch, every
      dollar-equivalent (tokens spent if Ollama, minutes of
      laptop power if local) accounted for.

**Safety rails — the non-negotiables.** A loop that bricks the repo
is worse than no loop at all. M8 enforces, at the agent level:

- No `--no-verify`. No force-push. No `rm -rf`. No destructive git
  ops. The same hard rules Claude Code already follows are
  enforced on the AI subprocess via its sandbox config.
- **Stop always leaves the build green.** When the dev stops a
  loop — from the mobile app, from the CLI, or via the physical
  `STOP` kill-file — the agent tears the in-flight iteration down
  in a very specific order: (1) SIGTERM the AI subprocess, (2)
  wait up to 10s, (3) SIGKILL if still alive, (4) roll the
  worktree back to the last committed SHA, (5) re-run the green
  gate on that SHA to confirm it still passes, (6) only then
  mark the loop `stopped`. The invariant the dev relies on is
  *"the tip of `ship.branch` is always a commit that compiles,
  typechecks, and can be built into a deployable artifact, no
  matter when I hit Stop."* A loop that violates this invariant
  once loses the user forever, so it is a CI-blocking test in
  yaver's own suite, not a best-effort behavior.
- Typecheck is a hard gate. A loop that tries to disable the
  typecheck config as a "fix" is rolled back and the iteration is
  flagged as `tampered`.
- After `stop_after_consecutive_stuck` iterations the loop
  pauses itself and pings the mobile app.
- The dev's stop signal, from any of the three surfaces above,
  always wins the race against the next scheduled tick.

**Three modes, one loop harness.** Everything above describes "fix
mode", but the same harness also runs in Auto Develop mode and
Auto-fix (hardening) mode. The dev picks one per loop; a project
can have several loops running different modes concurrently on
different schedules.

- **Fix mode.** Input = friction report from the persona fuzzer.
  Output = one small patch per iteration. Terminates when the dev
  stops it — an infinite loop of small improvements against a
  working app. The SFMG case study below uses fix mode.
- **Auto-fix (hardening) mode.** Always-on, zero creativity
  allowed. The fuzzer's heuristic detectors run over the build
  continuously, looking for the kind of issues that don't need a
  judgment call to fix: overlapping elements, clipped text,
  contrast failures, typos and missing Turkish diacritics
  (`Kiralik` → `Kiralık`), inconsistent capitalization, misaligned
  spacing, broken image links, missing translations, obviously
  dead buttons, `undefined` showing up in UI strings. Each finding
  becomes a tiny, near-trivial patch with a commit message like
  `"Fix: Kiralik diacritic in contract modal"` or
  `"Fix: tab bar overlaps CTA on iPad viewport"`. The AI patcher
  is pinned to radicalness 0 and explicitly told *"do not refactor,
  do not restructure, do not touch logic — just fix the exact thing
  this report points at."* Intended use case: leave this loop
  running forever in the background. Dozens of micro-commits per
  week, reviewed lazily via `git log --oneline`. This is the mode
  most solo devs will want on by default, even while they sleep,
  even while they are actively coding on the same repo (the
  worktree isolation makes the two safe to run in parallel).
- **Auto Develop mode.** Input = a feature prompt the dev authored
  in the mobile app (e.g. "add a weekly market-crash event that
  devalues 3 random clients by 10–30%, show a news story, and
  award a resilience buff to clients who recover"). Output = a
  completed feature branch. The agent kicks Claude Code / Codex
  repeatedly until the AI declares the prompt `done: true`, OR
  the budget runs out, OR the dev stops it from the phone. Each
  kick is a full think → green-gate → commit → deploy → re-test
  cycle. If the AI gets stuck mid-feature, the agent reruns with
  a polite nudge prompt ("you said you'd finish X, the diff does
  not yet include X, continue") instead of giving up — but it
  never *hides* the stuck state from the dev.

Auto Develop mode is what makes Yaver a "second developer" in the
real sense. The dev writes a sentence from their phone during
lunch; the Mac mini spends the afternoon turning it into a working,
typecheck-passing, persona-playtested feature, and pings the phone
when it's done or when it needs a human decision.

**Ideas mode — let the agent propose what to build next.** A
sub-mode of Auto Develop for the dev who wants "second developer"
energy but doesn't have a specific feature in mind. Instead of the
dev authoring prompts by hand, the agent periodically generates a
ranked list of candidate features by reading:

- the current repo state (recent commits, open TODOs, `TODO.md`)
- the latest persona fuzzer reports — friction points the fix-mode
  loop could not solve with a small patch, which usually means
  "this area needs a new thing, not a fix to the old thing"
- the dev's stated product direction, kept in a short
  `.yaver/product.md` the dev either maintains themselves or lets
  the agent rewrite from conversation history in the mobile app
- the current radicalness setting — bolder settings bias the
  generator toward mechanic-level ideas, conservative settings
  toward quality-of-life additions

The output is a ranked list of 5–15 candidate features per
generation run. Each idea carries:

- a short title
- a one-paragraph description
- a **radicalness estimate (0–10)** so the dev can see at a glance
  whether it's a tweak or a rewrite
- a rough effort band (small / medium / large)
- a one-line "why this helps the target persona" note
- a one-line "why this is not worth doing" counter-argument so
  the dev sees the trade-off without having to ask

The list lands in the **Auto Dev tab** as a multi-select UI. The
dev ticks the ideas they want, taps **Kick**, and the agent queues
each selected idea as its own Auto Develop prompt and works through
them one at a time (or in parallel, up to the budget). **No idea is
ever executed without an explicit tap.** The agent regenerates the
list on a configurable cadence (default: daily) or on demand from
the mobile app, and the dev can dismiss ideas to stop them from
reappearing.

**Auto Develop dashboard (mobile app).** Auto Develop is driven
from a new **Auto Dev** tab in the mobile app. Every knob lives
there; the agent reads them over the existing QUIC/HTTP transport
and the dev never has to edit a YAML on disk unless they want to.

| Knob                               | Type / default                                                              | Why it exists                                                                                                                        |
| ---------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| **Prompt library**                 | CRUD list; each entry has name, body, mode (fix / develop / hybrid)        | The dev writes and edits prompts from their phone, not an editor. One entry per feature idea.                                        |
| **Active prompt**                  | one library entry, or "fix mode (no prompt)"                                | Switches what the loop is currently driving at. One-tap handoff between ideas.                                                       |
| **Daily token budget**             | int per AI provider, default 500k tokens                                    | Hard cap so a runaway loop can't drain a subscription's soft quota or spike an Ollama GPU night.                                     |
| **Daily commit budget**            | int, default 30                                                             | Prevents 400-commit nights when a prompt is mis-scoped.                                                                              |
| **Daily TestFlight deploy budget** | int, default 1 (see release train below)                                    | Apple throttles uploads and humans can't review 12 TestFlight builds a day. The most important safety valve in the whole tab.       |
| **Daily Play Store deploy budget** | int, default 1                                                              | Same shape for Android.                                                                                                              |
| **Daily web deploy budget**        | int, default unlimited                                                      | Web deploys are cheap and reversible — no need to throttle by default.                                                              |
| **Active hours**                   | time window, default 22:00–08:00                                            | "Only run while I'm asleep." Outside the window the scheduler refuses to kick a new iteration.                                      |
| **Stop conditions**                | `stuck_limit`, `token_budget_hit`, `commit_budget_hit`, `prompt_done`       | First one that trips halts the loop and pushes a notification to the phone.                                                         |
| **AI provider order**              | ordered list, default `[claude-code, codex, ollama]`                        | Fallback chain in the 9router style — drop to the cheaper/free provider when the primary hits a rate limit.                         |
| **Deploy target per iteration**    | `mac-mini`, `ios-sim`, `android-emu`, `testflight`, `play-store`, `web`   | Where the rebundled app lands each iteration. A loop can deploy to Mac mini every iteration but only to TestFlight once a day.      |
| **Pause / Resume / Stop**          | three buttons, always reachable from the tab's sticky header                | Same kill surface as the M8 "stop from anywhere" rule.                                                                              |
| **Radicalness (UI)**               | int 0–10, default 2                                                         | 0 = pixel-nudge fixes only, 5 = redesign a screen, 10 = rethink the information architecture. Caps how bold the UI patcher is allowed to be per commit. Auto-fix mode is pinned to 0. |
| **Radicalness (features)**         | int 0–10, default 2                                                         | 0 = add a settings toggle, 5 = new screen with backing state, 10 = new core mechanic that changes how the game plays. Ideas generator biases toward this value when proposing. |
| **Tone**                           | `conservative` / `neutral` / `casual` / `playful` / `irreverent`, default `casual` | Bias for copy the AI writes — button labels, news headlines, notification text, error strings. Playful matches a Gen-Z persona; conservative is safer for finance or legal screens. Applied at the system-prompt level before every kick. |
| **Notification rules**             | on: `prompt_done`, `stuck_limit_hit`, `budget_hit`, `deploy_success`, `error` | Push notifications via the existing mobile app channel. No email, no SMS, no third-party services.                                 |

All knobs are per-prompt overridable. A prompt can override any
global default, and the override only applies while that prompt is
the active one. The dev can keep a "conservative overnight fix"
preset and an "aggressive build-me-this-feature" preset and toggle
between them in one tap.

**Playtest is optional.** `playtest.enabled` defaults to `true` —
most loops should run the persona fuzzer between every commit, so
the AI has real feedback about whether its patch actually improved
anything. But some loops should skip playtest entirely: a
develop-mode loop working from a very specific prompt does not
need a fuzzer to tell it what to build next, and running one per
kick just burns time and session budget. Set `playtest.enabled:
false` to shortcut the loop to **think → green-gate → commit →
(optional) deploy** with no testing step in between. Auto-fix mode
ignores the flag (it's a heuristic scan by definition); ideas mode
never runs code in the first place.

**Dependency warnings at `loop add` time.** When the dev registers
a loop spec, the agent does a one-pass check of the tools the spec
actually needs to run — Chrome / Chromium for web playtests, the
AI runner CLI (`claude`, `codex`, `aider`, `ollama`), `xcrun` for
iOS Simulator targets, `adb` + `emulator` for Android, `node` for
the typecheck gate — and prints non-fatal warnings for anything
missing. The spec is still registered either way; the warning is a
heads-up ("install Chrome before you expect `yaver loop run` to
actually boot the web target"), not a hard refusal. The rationale
is that a dev on a new laptop should be able to commit a
`.loop.yaml` to the repo without having every backing tool
installed yet — the loop just won't execute cleanly until the dev
fixes the warnings, and `yaver loop status` highlights unresolved
missing-tool warnings on the affected loops.

**Timeouts — optional wall-clock caps per iteration.** The
`schedule.timeout` field accepts any Go duration string (`"5h"`,
`"30m"`, `"2h30m"`) and is optional. When set, the agent starts a
wall-clock timer at the beginning of each iteration and aborts the
in-flight kick if it runs past the cap — cleanly, with the same
stop-leaves-the-build-green guarantee above. When unset, there is
no wall-clock limit and the iteration runs until the AI declares
done/stuck/needs_human, the green gate fails, or the dev stops it.
Recommended default for develop-mode loops is `timeout: 5h` so a
runaway prompt can't burn an entire Claude Code session on one
kick; auto-fix loops typically leave it unset because each kick is
short by construction.

**Respect for provider session limits.** The big provider subscriptions
the loop rides on (Claude Code's 5-hour window, Codex's per-hour cap,
Cursor's monthly include, etc.) are the same subscriptions the dev
uses to code interactively. A loop that burns 90% of the Claude Code
window at 03:00 is fine; a loop that does the same at 14:00 and
locks the dev out of their own editor for the rest of the afternoon
is a disaster. M8 treats this as a first-class constraint, not a
nice-to-have:

- Every well-known runner has baked-in default limits in the agent
  (`claude-code`: 5h window, shared-with-interactive; `codex`: 1h
  window, shared; `aider`: 1h window, dedicated; `ollama:*`: no
  provider-imposed limit, only hardware gating).
- Each loop's spec has a `think.respect_session_limits` flag that
  **defaults to `true`**. When true, the loop:
  1. tracks per-provider usage across iterations (tokens in/out,
     session-window elapsed, rate-limit responses seen)
  2. refuses to kick a new iteration if the shared window is
     already past its **soft cap** (default 80% — leave headroom
     for the dev's own work)
  3. falls back to the next runner in `think.fallback` (e.g. from
     `claude-code` → `codex` → `ollama:llama3`) before giving up
  4. yields the provider entirely during `schedule.active_hours`
     when the provider is flagged `shared_with_interactive` — the
     "don't touch my Claude Code during work hours" rule
- A loop that exhausts every provider in its fallback chain stops
  itself and pushes a `budget_hit` notification to the mobile app;
  it does not poll and does not queue.
- The dev can raise the soft cap per loop (e.g. set it to 100 for
  a truly autonomous weekend run where they will not use the
  editor themselves) but cannot disable the tracking entirely.

The practical effect: the dev can leave the auto-fix loop running
24/7 on their main work machine without ever noticing it stole a
Claude Code slot they needed for an interactive session. This is
the difference between a loop that is "usable" and a loop that
gets turned off after two days.

**"Kick until done" — how the agent knows a prompt is finished.**
The AI patcher is given a rigid output contract the agent parses
after each iteration:

```json
{
  "status": "in_progress" | "done" | "stuck" | "needs_human",
  "summary": "short line for the commit message",
  "next_step": "what the next kick should focus on, if in_progress",
  "blockers": ["list of human-only decisions, if needs_human"]
}
```

- `in_progress` → schedule the next kick immediately, pass
  `next_step` as an extra system-prompt nudge on top of the
  original prompt.
- `done` → stop the loop, push a "prompt complete" notification,
  open the loop branch for human review.
- `stuck` → increment the stuck counter; halt if over the limit.
- `needs_human` → stop immediately, render the `blockers` list in
  the mobile app so the dev can answer them and resume.

The agent does not trust the AI to self-terminate. The `status`
field is one of several termination inputs alongside the budgets
and the dev's stop signal — whichever fires first wins.

**Deploy is optional per loop.** `ship.deploy` is a plain shell
command and is allowed to be empty. When empty, the loop commits
past the green gate and stops — no deploy step runs. The dev picks
the commits up on their next natural pull / build cycle. This is
the right default for:

- the always-on **auto-fix** loop, which produces dozens of tiny
  commits a week that do not individually justify burning a
  TestFlight slot
- any **ideas** loop (never writes code in the first place)
- any loop the dev wants to keep strictly text-level: "land the
  fix, skip the rebuild, skip the upload"

A loop with an empty `ship.deploy` can still be reached from the
mobile app's **Run now** button for a manual deploy later, or the
dev can promote a specific commit into a release-train upload
from the Auto Dev tab without kicking a new iteration. The point
is that *iterating* and *deploying* are separable steps, and the
dev gets to spend their TestFlight quota on builds that actually
matter, not on every time the auto-fix loop fixes a diacritic.

**Release train — TestFlight / Play Store as a gated sub-step.**
Deploying to the dev's own Mac mini on every iteration is fine: it
is the dev's own machine, reversible, and has no outside observer.
Deploying to **TestFlight** or **Play Store** on every iteration is
not fine: Apple and Google throttle uploads, external testers
cannot absorb 12 builds a day, and a bad auto-commit that ships to
TF is a real incident that is hard to undo.

M8 models TestFlight / Play Store as a **release train**, not a
per-iteration deploy:

- The loop deploys to the dev's Mac mini / simulator every
  iteration — cheap, reversible, local, zero blast radius.
- A TestFlight / Play Store deploy only fires when **all** of the
  following are true:
  1. The loop has completed ≥N green iterations since the last
     store deploy (default N = 3).
  2. The daily store deploy budget is not exhausted.
  3. The dev has not flipped the **Release train: paused** switch
     on the mobile app's Auto Dev tab.
  4. Auto Develop mode is on `status: done`, or fix mode has a
     dev-marked "release candidate" flag on the last iteration.
- The actual upload reuses the project's existing release script
  (for SFMG: `scripts/deploy-testflight.sh`, with its `agvtool`
  bump + `xcodebuild archive` + `altool --upload-app` flow) as a
  managed task. The agent is a scheduler around it, not a
  replacement for it. Build number collisions are avoided by
  reading `scripts/last-build.txt` and incrementing.
- Every store deploy sends a push notification with the build
  number and a deep link to App Store Connect / Play Console so
  the dev can promote or reject in one tap.

The **daily TestFlight budget exists precisely because the store-
side review cost (humans looking at builds) is the bottleneck**,
not the compute. Default is 1 upload/day. The dev can raise it
per-prompt, but a hard ceiling of 10 uploads/day is baked into the
agent code — not a config — because exceeding that starts to look
like abuse to Apple. The ceiling cannot be overridden from the
mobile app.

**Case study — SFMG (`github.com/kivanccakmak/sfmg`).** The first
dogfood target. SFMG is a Turkish football-manager RN game with a
working Expo web build (`scripts/play-web.sh`, serves on
`http://localhost:8081`), `yaver-cli` already integrated, and a
Mac mini already paired to the dev's yaver agent. The persona is
`gen-z-turkish` — impatient, Turkish UI, values vibe and meme
energy over depth. Today a non-technical cousin "vibe-tests" the
game from his phone via Claude remote + Tailscale; M8 automates
the half of that loop that doesn't need a human.

SFMG will run three concurrent loops against the same repo:

1. `sfmg-autofix` in **Auto-fix mode**, always on, radicalness 0,
   committing tiny hardening patches to `main` whenever the
   fuzzer's heuristics find a typo, diacritic, overlap, or dead
   button. The dev's "kaydet" workflow already lands on `main` —
   these commits land the same way and are reviewed lazily via
   `git log --oneline`.
2. `sfmg-genz` in **Fix mode**, nightly 22:00–08:00, radicalness 2,
   tone `playful`, driven by the `gen-z-turkish` persona. Commits
   to `main`. Each night yields a handful of real friction fixes
   the cousin would otherwise have to complain about from his
   phone the next morning.
3. `sfmg-ideas` in **Ideas mode**, triggered daily at noon,
   generates a multi-select list of feature candidates that
   surface in the Auto Dev tab on the dev's phone during lunch.
   The dev ticks any that look good; the agent then queues them
   as Auto Develop prompts that run during active hours.

First acceptance run: drop `sfmg-autofix.loop.yaml`,
`sfmg-genz.loop.yaml`, and `sfmg-ideas.loop.yaml` in the repo root,
run `yaver loop run --all` on the Mac mini, leave it running for
72 hours. At the end of the three days the dev expects:

- A stream of `main`-branch commits from the auto-fix loop, each
  tiny and each with a passing typecheck — at least 20 over the
  three days for a project SFMG's size
- ≥10 `main`-branch commits from the Gen-Z nightly fix loop, each
  a real friction fix the cousin would have reported by hand
- A generated list of feature ideas on the phone, at least one of
  which the dev actually picked and saw built by the Auto Develop
  loop before the 72 hours ran out
- 0 commits that broke the typecheck (all rolled back cleanly)
- At most 1 TestFlight upload across the three days (release-train
  default: 1/day + green-iteration gate typically produces less)
- End-to-end test of the stop button from the phone: mid-iteration
  stop halts the loop within one round-trip, no half-committed
  patches remain on disk, the row shows `Stopped by you`

Total cost for those three days: the Mac mini's idle power draw.
Nothing else. The persona prompts, heuristic rules, radicalness
defaults, and budget floors that emerge from the SFMG dogfood run
become the template every other yaver-loop user starts from.

**Acceptance:** A solo dev with zero metered SaaS bills can leave
their Mac mini running `yaver loop` on a real RN/web project
overnight and wake up to a `main` branch with a stream of small,
typecheck-passing, human-reviewable fixes proposed by an AI driven
entirely by the dev's own subscription, plus an optional list of
new feature ideas waiting on the phone for a morning triage tap.
They can stop any loop from their phone in one tap at any time and
trust that no half-applied patch, zombie simulator, or orphan git
state remains. TestFlight uploads respect the daily budget and the
green-iteration gate. If the loop ever commits over a red
typecheck, ever exceeds the hard-coded 10-uploads/day TestFlight
ceiling, or ever ignores a stop signal from the mobile app, the
milestone did not ship.

## Competitor scan — paid SaaS we're undercutting

Snapshot of the paid landscape as of early 2026. The point isn't to
clone any of these, it's to see exactly which features customers are
already paying real money for so we know what's worth building into
the agent.

| Tool                       | What you actually pay for                                                                  | Solo dev entry price          |
| -------------------------- | ------------------------------------------------------------------------------------------ | ----------------------------- |
| BrowserStack App Automate  | 3000+ real device cloud, parallel runs, video + logs                                       | ~$129/mo (1 parallel)         |
| BrowserStack App Live      | Manual interactive sessions on real devices                                                | ~$29/mo                       |
| BrowserStack Percy         | Visual diff baselines + PR review workflow, long-term history                              | $199/mo (25k snapshots)       |
| Sauce Labs RDC             | Enterprise device cloud, Sauce Visual AI, retention                                        | ~$39/mo entry, scales fast    |
| LambdaTest / HyperExecute  | "Cheap BrowserStack" — parallel sharding at lower per-minute cost                          | ~$15/mo Live, $19/mo Automate |
| Chromatic                  | Storybook-native visual diff, multi-browser snapshots, baseline mgmt                       | $179/mo (35k snapshots)       |
| Applitools Eyes            | "Visual AI" diff that ignores rendering noise; self-healing selectors; Ultrafast Grid      | Custom (≈$0–$500+/mo)         |
| Waldo.com                  | Record-on-real-device → replay-anywhere, no-code mobile E2E                                | Custom quote                  |
| Maestro Cloud (mobile.dev) | Cloud execution of OSS Maestro YAML; parallel sharding; history                            | $250/device/mo, $125/browser  |
| QA Wolf                    | Fully managed: humans + AI write *and* maintain your tests; 100% coverage SLA              | ~$50k+/yr                     |
| Reflect.run                | No-code web recorder, multi-browser, SMS/email tests                                       | $225/mo                       |
| Octomind                   | AI auto-generates Playwright tests from your app + auto-heals when DOM changes             | Free → custom                 |

The headline observation: **the real money is in things a pure SaaS
has to build a fleet for** (real device cloud, multi-browser
snapshot grid, managed QA humans). The other half — visual diff,
flake detection, parallel sharding, AI-healing selectors, recorder
mode — is plain code that runs on the user's machine just as well
as on a vendor's. Those are exactly the things yaver-test-sdk
should ship.

### What's worth pulling in vs leaving alone

| Capability                                  | Build into yaver-test-sdk?                | Notes                                                                                          |
| ------------------------------------------- | ----------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Parallel test sharding across cores         | **Yes — M4**                              | Pure Go scheduler over the spec list. No infra dependency.                                     |
| Flake detection (re-run failed N×, tag)    | **Yes — M6**                              | Already half-built: agent has retry logic for AI tasks.                                        |
| Visual diff (perceptual + delta-E)          | **Yes — M6**                              | Pure Go image lib. Baselines as PNGs in `yaver-tests/visual/`.                                 |
| Multi-browser snapshot grid                 | **Yes — M5/M6**                           | Spawn locally installed Chrome / Firefox / WebKit via CDP / playwright-go.                     |
| AI self-healing selectors                   | **Yes — M6**                              | When a selector fails, send DOM snapshot to the same MCP that drives Claude Code, ask for a re-derived selector. Already have the MCP server. |
| Test history / build analytics dashboard    | **Yes — M7**                              | Persist run JSON to local SQLite, surface in mobile app.                                       |
| Recorder mode → YAML emitter                | **Yes — M5**                              | Watch CDP / driver events, distill into the canonical step vocabulary.                         |
| Long-term baseline storage                  | **Yes — local-first, optional sync**      | `yaver-tests/visual/` versioned in git; optional `yaver test sync` to user-owned S3/GCS.       |
| Real device cloud (3000+ phones)            | **No — out of scope**                     | Requires owning hardware. Not a software problem. Refer users to BrowserStack if they need it. |
| Managed-QA humans (QA Wolf model)           | **No**                                    | This is a services business, not a tool. Not what Yaver is.                                    |
| Crowd-testing marketplace                   | **No**                                    | TestProject and Rainforest both died trying. Pattern: don't.                                   |

### Lessons from products that died

- **TestProject** (RIP March 2023) — free cloud execution backed by
  Tricentis upsell, killed because the free tier was unsustainable
  and the enterprise pivot alienated the indie community. Lesson:
  do not build a free cloud-execution tier funded by an enterprise
  upsell. Yaver runs on the user's own machine, so we don't have
  this temptation.
- **Rainforest QA** — pivoted entirely away from QA testing into
  embedded-payments fintech. Crowd-tester model didn't scale.
- **Functionize**, **Testim** — both acquired by Tricentis and
  subsumed into the enterprise suite, losing their developer
  identity. Lesson: developer-friendly tooling and enterprise
  consolidators don't mix; staying open-source is the moat.

### Cross-pollination: ideas to borrow from `decolua/9router`

[decolua/9router](https://github.com/decolua/9router) is a
locally-run OpenAI-compatible proxy that routes between 40+ AI
providers with a 3-tier auto-fallback (Subscription → Cheap API →
Free), per-provider quota tracking with reset countdowns, OAuth
token refresh, and multi-account round-robin. Different problem
space from CI testing, but several of its design patterns drop
straight into the Yaver agent:

1. **3-tier model fallback chain** — instead of a fixed
   `ANTHROPIC_API_KEY`, the agent should accept an ordered list
   `providers: [claude-sub, claude-api, ollama-local]` and walk it
   on rate-limit or 5xx. This is *exactly* the right shape for
   yaver-test-sdk's "AI self-healing selector" feature: try the
   primary model, fall back to a cheaper one, then to a local
   Ollama if both fail.
2. **Per-provider quota tracking with live reset countdown** —
   surface the agent's current quota usage per provider in the
   mobile app's status bar. Yaver shows nothing today; users find
   out they've hit a wall mid-task. The data structure 9router
   uses (token count, window size, reset_at) is the right shape.
3. **OAuth auto-refresh loop** — Yaver's agent today requires
   `yaver auth` to be run manually. Borrow 9router's background
   refresh pattern: poll the provider's token endpoint, write the
   refreshed token back to `~/.yaver/config.json`, never interrupt
   the user.
4. **Multi-account round-robin per provider** — for power users
   with several Claude / Codex subscriptions, accept a list and
   round-robin requests across them at the HTTP server layer. Low
   effort, large quality-of-life win.

**Not worth borrowing:** 9router ships as a Next.js app with a
dashboard. Yaver's agent is a single Go binary on purpose; the
mobile app is the UI. Don't add a Node runtime alongside the agent.
Also skip 9router's "cloud sync of config" feature — Yaver
deliberately never sends task data or credentials through its
servers, and replicating OAuth tokens via Convex would break the
privacy model.

## Things explicitly not in scope

- Hosted "Yaver Cloud" SaaS. The whole point is the dev's own machine.
- Multi-tenant SaaS UI for teams. Solo / pair max for now.
- Replacing release CI (Apple notarization, Play Store upload). Those
  are still cloud-only and that's fine — they run rarely enough that
  GitHub Actions stays free.
- Replacing dependency mirroring or container registries. Use what
  you already use.

## Open questions

- **Where do test artifacts live long-term?** Local-only is simplest
  but the dev wants to compare today's run to last week's. Per-device
  SQLite or a hosted-but-cheap object store?
- **Auth for Selenium grid mode** if a small team wants to share one
  beefy laptop as a runner. Probably reuse the existing relay password
  + device-list ACL.
- **iOS device farm story.** A real iPhone plugged in via USB versus a
  simulator. Both should work, but the lifecycle rules differ
  (provisioning, dev profile, Xcode signing).
- **Snapshot stability across OS versions.** If the dev upgrades macOS,
  the simulator emoji rendering shifts and visual tests start failing.
  Need a sane "baseline by OS bucket" story.
