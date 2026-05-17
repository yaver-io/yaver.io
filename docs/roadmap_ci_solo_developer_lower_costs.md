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
   already pass?" and skip redundant work.

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

The non-negotiable rule: **after `npm install -g yaver-cli` the dev should be able to run `yaver test` against any of the
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
