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
