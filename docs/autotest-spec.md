# Yaver Autotest — Self-Growing Regression Test Suite

## Overview

Yaver Autotest is a self-growing, autonomous app testing system for solo developers. It uses the Feedback SDK (already embedded in the target app) as a test driver that navigates the app in an emulator or physical device, captures errors via BlackBox streaming, delegates bug fixes to AI coding agents (Claude Code, Codex, Aider, etc.), and codifies every finding into a permanent, version-controlled test suite that syncs to GitHub Actions CI.

**Core promise:** Drop the Feedback SDK in your app, run one command (or tap one button on your phone), and get a test suite that grows automatically — forever. No test files to write. No config. No QA team needed.

---

## Architecture

### System Diagram

```
┌────────────────────────────────────────────────────────────────────────────┐
│                                                                            │
│   TRIGGER                    EXECUTE                    VIEW               │
│                                                                            │
│   ┌──────────┐              ┌──────────────┐           ┌──────────┐       │
│   │  Yaver   │──────────────│              │──────────►│  Yaver   │       │
│   │  Mobile  │   P2P/relay  │  Yaver Agent │  push     │  Mobile  │       │
│   │  App     │              │  (Go CLI)    │  results  │  App     │       │
│   └──────────┘              │              │           └──────────┘       │
│                             │  ┌────────┐  │                              │
│   ┌──────────┐              │  │AI Agent│  │           ┌──────────┐       │
│   │ Feedback │──────────────│  │Claude/ │  │──────────►│ Feedback │       │
│   │ SDK (in  │   localhost  │  │Codex   │  │  push     │ SDK (in  │       │
│   │ app)     │              │  └────────┘  │  results  │ app)     │       │
│   └──────────┘              │              │           └──────────┘       │
│                             │  ┌────────┐  │                              │
│   ┌──────────┐              │  │Emulator│  │           ┌──────────┐       │
│   │  CLI     │──────────────│  │or      │  │──────────►│  CLI     │       │
│   │ terminal │   local      │  │Device  │  │  stdout   │ terminal │       │
│   └──────────┘              │  └────────┘  │           └──────────┘       │
│                             │              │                              │
│                             │  ┌────────┐  │           ┌──────────┐       │
│                             │  │GitHub  │◄─┼──────────►│  GitHub  │       │
│                             │  │Actions │  │  sync-ci  │  CI      │       │
│                             │  └────────┘  │           └──────────┘       │
│                             └──────────────┘                              │
│                                                                            │
└────────────────────────────────────────────────────────────────────────────┘
```

### Autotest Lifecycle

```
┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
│ DISCOVER │───►│  TEST    │───►│   FIX    │───►│ CODIFY   │───►│ CI SYNC  │
│          │    │          │    │          │    │          │    │          │
│ AI reads │    │ SDK runs │    │ AI agent │    │ AI turns │    │ Promote  │
│ code,git,│    │ tests in │    │ patches  │    │ findings │    │ tests to │
│ Jira,old │    │ emulator │    │ code,    │    │ into     │    │ GitHub   │
│ results  │    │ captures │    │ hot-     │    │ permanent│    │ Actions  │
│ → plan   │    │ errors   │    │ reloads  │    │ test     │    │ workflow │
│          │    │          │    │          │    │ cases    │    │          │
└──────────┘    └──────────┘    └──────────┘    └──────────┘    └──────────┘
     │                                               │               │
     │              LOOP (iterations)                 │               │
     └───────────────────────────────────────────────►│               │
                                                      │               │
                                  each run grows ─────┘    user       │
                                  the suite              approves ────┘
```

### Growth Flywheel

```
  New feature merged
        │
        ▼
  Autotest runs (triggered from phone, SDK, CLI, or cron)
        │
        ▼
  AI discovers untested paths ──────────────────────┐
        │                                            │
        ▼                                            │
  SDK navigates app in emulator or physical device   │
        │                                            │
        ├── No bugs found → new test cases codified  │
        │                    into .yaver/tests/       │
        │                                            │
        └── Bugs found → AI fixes → re-test          │
                │                                    │
                ▼                                    │
        Fixed bugs become regression tests ──────────┤
                                                     │
                                                     ▼
                                          .yaver/tests/ grows
                                                     │
                                                     ▼
                                          yaver autotest sync-ci
                                                     │
                                                     ▼
                                          GitHub Actions runs
                                          growing suite on every PR
```

---

## Three Trigger Points

### 1. Yaver Mobile App

The primary trigger for solo heroes. After vibe-coding a feature from their phone, they tap "Autotest" to test it.

```
Yaver App → Devices tab → MacBook → "Autotest" button

┌─────────────────────────────┐
│  MacBook-Air  ●  online     │
│─────────────────────────────│
│  [Tasks]  [Terminal]  [Test]│
│─────────────────────────────│
│                             │
│  Run Autotest               │
│                             │
│  Target:                    │
│  ┌─────────────────────┐   │
│  │ ○ Emulator (iOS Sim)│   │
│  │ ○ Emulator (Android)│   │
│  │ ● Physical (iPhone) │   │
│  │ ○ Both              │   │
│  └─────────────────────┘   │
│                             │
│  Scope:                     │
│  ┌─────────────────────┐   │
│  │ ● Full suite        │   │
│  │ ○ Changed files only│   │
│  │ ○ Specific screen   │   │
│  └─────────────────────┘   │
│                             │
│  ☑ Auto-fix bugs            │
│  ☑ Add new test cases       │
│  ☐ Sync to CI after         │
│                             │
│  [ ▶ Start Autotest ]       │
│                             │
└─────────────────────────────┘
```

Live results stream in:

```
┌─────────────────────────────┐
│  Autotest Running ◉ live    │
│─────────────────────────────│
│  Iteration 2 of 3           │
│  ████████████░░░ 73%        │
│                             │
│  ✓ HomeScreen        0.4s   │
│  ✓ ProductList       1.2s   │
│  ✗ CartScreen        0.8s   │
│    → crash on empty cart    │
│    → fixing...              │
│  ◌ CheckoutFlow      —     │
│  ◌ ProfileScreen     —     │
│                             │
│  Found: 2 bugs              │
│  Fixed: 1                   │
│  New tests: +8              │
│                             │
│  [Stop]  [Skip to results]  │
└─────────────────────────────┘
```

Results tab:

```
┌─────────────────────────────────┐
│  Autotest Results               │
│─────────────────────────────────│
│                                 │
│  ▼ Mar 25, 14:30 (22 min)      │
│    3 iterations • 1 bug fixed   │
│    +16 new tests • 47 total     │
│    Branch: autotest/run-0325    │
│                                 │
│    [Approve Fixes] [View Diff]  │
│    [Sync to CI]    [Re-run]     │
│                                 │
│  ▶ Mar 22, 09:15 (18 min)      │
│    2 iterations • 2 bugs fixed  │
│                                 │
│  ▶ Mar 20, 11:00 (25 min)      │
│    4 iterations • 4 bugs fixed  │
│                                 │
│─────────────────────────────────│
│  Suite: 47 tests │ CI: 38 │ 81%│
│  [Growth Chart]                 │
└─────────────────────────────────┘
```

### 2. Feedback SDK (Inside the Target App)

Dev is testing manually, wants to run full autotest from right there:

```
┌─────────────────────────────┐
│  Feedback SDK Panel         │
│─────────────────────────────│
│  [Bug Report] [Screenshot]  │
│  [BlackBox]   [Autotest ▶]  │
│─────────────────────────────│
│                             │
│  "Run autotest on this app?"│
│                             │
│  Target:                    │
│  ● This device (physical)   │
│  ○ Emulator on dev machine  │
│                             │
│  [ Start ]  [ Cancel ]      │
└─────────────────────────────┘
```

When running on the same physical device, the SDK takes over navigation. When targeting emulator, the SDK tells the agent to spin up an emulator and run there — the physical device just shows results.

Results overlay inside the app:

```
┌─────────────────────────────────┐
│  Autotest Results  ✓ Complete   │
│─────────────────────────────────│
│                                 │
│  ✓ HomeScreen           pass    │
│  ✓ ProductList          pass    │
│  ✗ CartScreen      fixed ✓     │
│  ✓ CheckoutFlow         pass    │
│  ✓ ProfileScreen        pass    │
│                                 │
│  1 bug fixed • +16 tests        │
│                                 │
│  [Approve]  [Details in App]    │
└─────────────────────────────────┘
```

"Details in App" deep-links to full results in the Yaver mobile app.

### 3. Yaver CLI (Terminal)

```bash
yaver autotest start                          # AI generates plan + runs (emulator default)
yaver autotest start --target device          # Target connected physical device
yaver autotest start --target "iPhone 15 Pro" # Target specific device by name
yaver autotest start --target emulator:android # Android emulator
yaver autotest start --target all             # All available targets
yaver autotest start --stories ./my-tests/    # User-provided test stories
yaver autotest start --context jira,git       # Pull context from integrations
yaver autotest start --no-fix                 # Report only, don't fix
yaver autotest start --iterations 5           # Max fix-retest iterations
```

### Target Selection Logic

```
                    POST /autotest/start
                    { target: "emulator:ios" | "device" | "emulator:android" | "all" }
                            │
                            ▼
                    ┌───────────────┐
                    │ Yaver Agent   │
                    │ (orchestrator)│
                    └───────┬───────┘
                            │
              ┌─────────────┼─────────────┐
              ▼             ▼             ▼
     ┌──────────────┐ ┌──────────┐ ┌──────────────┐
     │ iOS Simulator│ │ Physical │ │ Android Emu  │
     │              │ │ Device   │ │              │
     │ Agent starts │ │ App has  │ │ Agent starts │
     │ simulator,   │ │ SDK,     │ │ emulator,    │
     │ builds app,  │ │ agent    │ │ builds app,  │
     │ installs,    │ │ sends    │ │ installs,    │
     │ SDK takes    │ │ commands │ │ SDK takes    │
     │ over nav     │ │ directly │ │ over nav     │
     └──────────────┘ └──────────┘ └──────────────┘
```

---

## Test Case Sources

Test cases can come from multiple sources. The system gets smarter over time as more sources feed in.

| Source | How | When |
|--------|-----|------|
| **User-written stories** | Markdown files in `.yaver/tests/stories/user/` | User writes test scenarios manually |
| **AI-generated from code** | AI reads components, screens, navigation graph | Every run — AI finds untested paths |
| **AI-generated from git diff** | AI reads recent changes, generates tests for new code | When new features are merged |
| **AI-generated from Jira/Linear** | AI reads ticket descriptions, acceptance criteria | When connected to issue tracker |
| **Discovered from bugs** | Every bug found in a run becomes a regression test | Automatic — every run |
| **Crash replays** | Real crashes (from Feedback SDK) converted to test cases | When users hit crashes |
| **Production learning** | Anonymized error patterns from opt-in production SDK | When app has real users |
| **Previous run gaps** | Coverage map shows untested screen × state combos | AI prioritizes gaps each run |

---

## Test Suite Structure

In-repo, version-controlled, grows automatically:

```
.yaver/
  tests/
    manifest.json              # master index of all test cases
    config.json                # emulator config, timeouts, retry policy
    stories/
      user/                    # user-written test stories
        checkout-flow.md
        cart-edge-cases.md
      generated/               # AI-generated from code analysis
        product-detail-nav.md
        empty-states.md
      discovered/              # auto-discovered from test runs
        cart-crash-empty.md    # was a bug, now a regression test
        image-404-fallback.md
    snapshots/                 # expected UI state snapshots (visual regression)
      cart-screen-empty.png
      product-detail-loaded.png
    ci/
      autotest.yml             # generated GitHub Actions workflow
      runner.sh                # CI-compatible test runner script
  results/                     # gitignored — local only
    runs/
      2026-03-25T14-30-00/
        results.json
        results.md
        screenshots/
        fixes/
```

### manifest.json

```json
{
  "version": 1,
  "lastRun": "2026-03-25T14:52:00Z",
  "totalCases": 47,
  "sources": {
    "user": 5,
    "generated": 28,
    "discovered": 14
  },
  "cases": [
    {
      "id": "tc-001",
      "name": "Empty cart shows placeholder",
      "source": "discovered",
      "discoveredAt": "2026-03-20T10:00:00Z",
      "discoveredFrom": "run-2026-03-20T10-00",
      "originalBug": "CartScreen crash on empty cart array",
      "fixCommit": "abc1234",
      "screens": ["CartScreen"],
      "steps": [
        {"action": "navigate", "target": "CartScreen"},
        {"action": "assert", "condition": "visible", "element": "empty-cart-placeholder"}
      ],
      "severity": "critical",
      "ciEnabled": true
    }
  ]
}
```

### Test Case Lifecycle

```
                    ┌─────────────────────────┐
                    │     NEW TEST CASE        │
                    │                          │
                    │  Sources:                │
                    │  • User writes story     │
                    │  • AI analyzes new code  │
                    │  • Bug found in run      │
                    │  • Git diff → new screen │
                    │  • Jira ticket context   │
                    │  • Crash replay          │
                    │  • Production patterns   │
                    └───────────┬──────────────┘
                                │
                                ▼
                    ┌─────────────────────────┐
                    │   DRAFT (local only)     │
                    │   .yaver/tests/stories/  │
                    └───────────┬──────────────┘
                                │
                         autotest run validates
                                │
                                ▼
                    ┌─────────────────────────┐
                    │   VALIDATED              │
                    │   Added to manifest.json │
                    │   ciEnabled: false       │
                    └───────────┬──────────────┘
                                │
                     yaver autotest sync-ci
                     (user approves)
                                │
                                ▼
                    ┌─────────────────────────┐
                    │   IN CI                  │
                    │   ciEnabled: true        │
                    │   Runs on every PR       │
                    └──────────────────────────┘
```

---

## Codify Phase

After each run, before reporting results, the AI agent runs a codify step:

1. **Diff the test suite** — compare what was tested vs. what's in `manifest.json`
2. **Extract new cases** from:
   - Bugs found → regression test (so it never happens again)
   - Screens navigated that had no existing tests → coverage gap test
   - Edge cases discovered (empty states, error responses, slow loads)
   - New code since last run (`git diff` → new components/screens → new tests)
3. **Write test story** as markdown in `discovered/` or update `generated/`
4. **Add to manifest** with `ciEnabled: false` (not in CI until user approves sync)
5. **Report to user**: "Added N new test cases from this run"

---

## CI Sync

`yaver autotest sync-ci` promotes the local test suite into GitHub Actions:

```bash
yaver autotest sync-ci          # AI reviews suite, generates/updates CI workflow
yaver autotest sync-ci --dry    # Show what would change, don't write
yaver autotest sync-ci --all    # Promote ALL validated tests to CI
yaver autotest sync-ci --pick   # Interactive: choose which tests to promote
```

What it does:

1. Reads manifest — finds all `ciEnabled: false` validated tests
2. Asks AI agent to review them:
   - "Are these tests CI-appropriate?" (some need emulator, some can be unit tests)
   - "Can any be converted to simpler unit/integration tests?" (faster in CI)
   - "Are there redundant tests to merge?"
3. Generates/updates `.github/workflows/yaver-autotest.yml`
4. Marks tests as `ciEnabled: true` in manifest
5. Stages changes — user reviews and approves the PR

### Generated CI Workflow

```yaml
# .github/workflows/yaver-autotest.yml
# AUTO-GENERATED by yaver autotest sync-ci
# Edit .yaver/tests/ to modify — this file is regenerated on sync
name: Yaver Autotest Suite
on:
  pull_request:
    branches: [main]
  schedule:
    - cron: '0 2 * * *'  # nightly full run

jobs:
  autotest-unit:
    # Tests converted to unit tests by AI (no emulator needed)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: npm install
      - run: npx jest --config .yaver/tests/ci/jest.config.js

  autotest-ios:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
      - uses: futureware-tech/simulator-action@v3
        with: { model: 'iPhone 16' }
      - run: npm install && npx expo prebuild --platform ios
      - run: .yaver/tests/ci/runner.sh --ci --platform ios

  autotest-android:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: reactivecircus/android-emulator-runner@v2
        with:
          api-level: 34
          script: .yaver/tests/ci/runner.sh --ci --platform android
```

---

## Agent HTTP Endpoints

New endpoints on the Yaver Agent HTTP server:

```
POST   /autotest/start           # Start a run
  body: { target, scope, autoFix, addTests, syncCi }

GET    /autotest/status           # Current run status (SSE stream)
  → { phase, iteration, progress, currentScreen, bugsFound, bugsFixed, newTests }

POST   /autotest/stop             # Stop current run

GET    /autotest/results          # List all runs
GET    /autotest/results/:runId   # Specific run details
GET    /autotest/results/latest   # Latest run

POST   /autotest/approve          # Approve fixes + tests
  body: { runId, fixes: [ids], tests: [ids] }

POST   /autotest/sync-ci          # Promote tests to CI
  body: { runId, testIds: [] | "all" }

GET    /autotest/suite            # Current test suite stats
GET    /autotest/suite/coverage   # Coverage matrix (screen × state)
GET    /autotest/suite/growth     # Growth over time
```

---

## CLI Commands

```bash
# === Run Tests ===
yaver autotest start                        # AI generates plan + runs in emulator
yaver autotest start --target device        # Run on connected physical device
yaver autotest start --target emulator:android
yaver autotest start --target all           # All available targets
yaver autotest start --stories ./my-tests/  # User-provided stories
yaver autotest start --context jira,git     # Pull context from integrations
yaver autotest start --no-fix               # Report only, don't fix
yaver autotest start --iterations 5         # Max fix-retest iterations

# === Results ===
yaver autotest results                      # Last run summary
yaver autotest results --detail             # Full details with screenshots
yaver autotest results --all                # All runs for current repo
yaver autotest results --json               # Machine-readable

# === Test Suite Management ===
yaver autotest suite                        # Show current test suite stats
yaver autotest suite --coverage             # Screen × state coverage matrix
yaver autotest suite --gaps                 # What's NOT covered yet

# === CI Sync ===
yaver autotest sync-ci                      # Promote validated tests to CI
yaver autotest sync-ci --dry                # Preview changes
yaver autotest sync-ci --all                # Promote everything
yaver autotest sync-ci --pick               # Choose which tests

# === Approve Fixes ===
yaver autotest approve                      # Approve all pending fixes + new tests
yaver autotest approve --fixes-only         # Approve fixes, skip new tests
yaver autotest approve --tests-only         # Approve new tests, skip fixes
yaver autotest approve --cherry-pick 1,3    # Specific items

# === Growth Analysis ===
yaver autotest growth                       # Show how suite has grown over time
```

---

## Notification Format

Sent to user on completion (via mobile push + in-app):

```markdown
## Autotest Run Complete — AcmeStore

**Run**: 2026-03-25 14:30 → 14:52 (22 min, 3 iterations)

### Bugs Found & Fixed
- [x] `CartScreen` — crash on empty cart (fixed, iteration 1)
- [x] `ProfileScreen` — unhandled null user.avatar (fixed, iteration 2)
- [ ] `PaymentFlow` — Stripe timeout (needs manual review)

### New Test Cases Added (16)
| # | Test Case | Source | Screen | CI Ready |
|---|-----------|--------|--------|----------|
| 1 | Empty cart placeholder | discovered (was bug) | CartScreen | yes |
| 2 | Null avatar fallback | discovered (was bug) | ProfileScreen | yes |
| 3 | Product search empty query | generated | SearchScreen | yes |
| 4 | Deep link to deleted product | generated | ProductDetail | yes |
| ... | +12 more | | | |

### Suite Stats
- **Before**: 31 cases (20 in CI)
- **After**: 47 cases (38 in CI after sync)
- **Coverage**: 14/16 screens covered

### Next Steps
- `yaver autotest approve` — approve fixes + new tests
- `yaver autotest sync-ci` — push 9 new tests to GitHub Actions
- Branch: `autotest/run-20260325-1430` (2 fix commits, 1 test update commit)
```

---

## Additional Capabilities (by phase)

### Phase 1: Core Loop
- **SDK Test Driver Mode**: `YaverFeedback.enableAutoTest()` — SDK navigates app autonomously, receives commands from agent, captures everything via BlackBox
- **Autotest Orchestrator**: Go subsystem in agent, manages the discover → test → fix → codify loop
- **Bug Fix Loop**: AI agent patches code, hot-reloads, re-tests, iterates
- **Test Codification**: Every finding → permanent test case in `.yaver/tests/`
- **Results & Notifications**: Structured results, stored locally, pushed to user's phone

### Phase 2: CI Promotion
- **CI Sync**: `yaver autotest sync-ci` generates GitHub Actions workflow from test suite
- **PR-Aware Targeted Runs**: AI reads PR diff → picks only relevant tests (saves CI minutes for solo devs paying for runners)
- **Coverage Map**: Screen × state matrix showing what's tested and what's not, AI prioritizes gaps

### Phase 3: Regression Safety
- **Visual Regression**: Golden screenshots stored from passing runs. Next run diffs against them. AI reviews: intentional UI change or regression? If regression → fix. If intentional → update golden.
- **Performance Regression**: SDK measures render times, navigation durations, API response times. If screen X went from 200ms to 800ms → new test case with perf budget assertion.
- **API Contract Testing**: BlackBox captures all network requests. AI builds implicit contracts from observed responses. If response shape changes → flag + generate contract test. Lightweight enough for CI without emulator.
- **Crash Replay**: Real crash from testing/production → AI converts BlackBox event stream into reproducible test case. The crash literally becomes a permanent regression test.

### Phase 4: Self-Maintenance
- **Flaky Test Self-Healing**: Test passes 9/10 runs → AI rewrites it to be deterministic (longer waits, retry logic, etc.). Suite heals itself.
- **Test Pruning**: Two tests cover same code path → AI suggests merge. Tested code deleted → test auto-deprecated. Keeps suite lean.
- **Dependency-Triggered Runs**: `package.json`/`Podfile`/`build.gradle` changed → full suite auto-runs to catch breakage from dependency bumps.
- **Dead Code Detection**: Coverage map reveals screens the test driver can never reach. Either dead code (flag for removal) or broken navigation (bug).

### Phase 5: Growth Path
- **Multi-Device Matrix**: Same suite across iPhone SE / iPhone 16 Pro Max / Android old / Android new / dark mode / RTL. Device-specific bugs get tagged.
- **Production Learning Loop**: Feedback SDK in production (opt-in) feeds anonymized error patterns back into test suite. Real user crashes → test cases → fixes → CI. Users unknowingly contribute to QA.

---

## Coverage Map

```
$ yaver autotest suite --coverage

Screen Coverage Matrix — AcmeStore
═══════════════════════════════════════════════════════
  Screen            Empty  Loaded  Error  Offline  Auth
───────────────────────────────────────────────────────
  HomeScreen          ✓      ✓      ✓      ✓       —
  ProductList         ✓      ✓      ✗      ✗       —
  ProductDetail       ✓      ✓      ✗      ✗       —
  CartScreen          ✓      ✓      ✗      ✗       —
  CheckoutFlow        ✗      ✓      ✗      ✗       ✓
  ProfileScreen       ✗      ✓      ✗      ✗       ✓
  SettingsScreen      ✗      ✗      ✗      ✗       ✗
═══════════════════════════════════════════════════════
  Coverage: 14/35 states (40%)
  Next run will prioritize: SettingsScreen, offline states
```

---

## Growth Tracking

```
$ yaver autotest growth

Autotest Suite Growth — AcmeStore
═══════════════════════════════════════════════════
  Run Date       Cases   New   Bugs   Fixed   CI
───────────────────────────────────────────────────
  Mar 15, 2026      8    +8      3      3      0
  Mar 18, 2026     14    +6      1      1      5
  Mar 20, 2026     23    +9      4      4     12
  Mar 22, 2026     31    +8      2      2     20
  Mar 25, 2026     47   +16      1      1     38
═══════════════════════════════════════════════════
  Total growth: 8 → 47 cases in 10 days
  CI coverage: 38/47 tests (81%) promoted
  Bugs caught & fixed: 11
  Zero-effort test cases: 42 (89% AI-generated)
```

---

## Solo Dev Timeline

```
Week 1:  npm install @yaver/feedback-sdk
         Add <YaverFeedback /> to app root
         yaver autotest start (or tap Autotest on phone)
         → 15 tests generated, 3 bugs found and fixed
         → "holy shit, it just tested my whole app"

Week 2:  yaver autotest start (again, or set up nightly cron)
         → 28 tests now, 2 new bugs from last week's feature
         yaver autotest sync-ci
         → GitHub Actions running 28 tests on every PR

Week 4:  Suite at 50+ tests, running on every PR
         Visual regression catches a CSS break
         Perf regression catches a slow list render
         → Solo dev has better QA than most funded startups

Week 8:  Suite at 100+ tests, self-maintaining
         Flaky tests auto-healed
         Dead tests auto-pruned
         → Zero test maintenance overhead

Month 6: Production learning loop active
         Real user crashes → test cases → fixes → CI
         → Users are unknowingly contributing to QA
```

---

## What Already Exists vs. What's New

| Component | Status | Notes |
|-----------|--------|-------|
| Feedback SDK error capture | **Exists** | BlackBox, wrapErrorHandler, attachError |
| BlackBox streaming | **Exists** | Ring buffer, `/blackbox/events`, SSE subscribe |
| Screenshot capture | **Exists** | In FeedbackModal |
| Hot-reload trigger | **Exists** | FeedbackModal has reload button |
| SDK ↔ Agent communication | **Exists** | P2PClient, HTTP endpoints |
| Mobile app device management | **Exists** | Device list, connect, task management |
| **SDK test driver mode** | **New** | Navigation commands, view hierarchy, auto-screenshot |
| **Agent autotest orchestrator** | **New** | Go subsystem for test loop management |
| **AI agent delegation for tests** | **New** | Yaver agent → Claude Code for test/fix generation |
| **Test suite storage (.yaver/tests/)** | **New** | In-repo, version-controlled, manifest.json |
| **Local results storage** | **New** | Per-repo autotest results in ~/.config/yaver/ |
| **CI sync** | **New** | Generate GitHub Actions from test suite |
| **Mobile app autotest UI** | **New** | Trigger, live status, results, approve from phone |
| **SDK autotest button + results overlay** | **New** | Trigger + view inside target app |
| **Visual regression** | **New** | Golden screenshots, pixel diffing |
| **Perf regression** | **New** | Render time tracking, perf budget assertions |
| **API contract testing** | **New** | Implicit contracts from observed network traffic |
| **Crash replay** | **New** | Real crashes → reproducible test cases |
| **Flaky self-healing** | **New** | AI rewrites flaky tests |
| **Test pruning** | **New** | AI merges/removes redundant tests |
| **Multi-device matrix** | **New** | Same suite across device configs |
| **Production learning loop** | **New** | Real user patterns → test cases |

---

## Sources of Test Suite Growth

```
                         ┌──────────────────────────┐
                         │     SOURCES OF GROWTH     │
                         ├──────────────────────────┤
                         │ • New features (git diff) │
                         │ • Bugs found in runs      │
                         │ • User-written stories     │
                         │ • Production crashes       │
                         │ • API contract changes     │
                         │ • Dependency bumps         │
                         │ • Coverage gap analysis    │
                         │ • Perf regression catches  │
                         │ • Visual diff catches      │
                         │ • Crash replays            │
                         │ • Multi-device findings    │
                         │ • Jira/Linear tickets      │
                         └────────────┬─────────────┘
                                      │
                                      ▼
                         ┌──────────────────────────┐
                    ┌───►│   .yaver/tests/manifest   │◄───┐
                    │    │   (growing test suite)     │    │
                    │    └────────────┬─────────────┘    │
                    │                 │                    │
                    │    sync-ci      │     autotest run   │
                    │                 ▼                    │
                    │    ┌──────────────────────────┐    │
                    │    │   GitHub Actions CI       │    │
                    │    │   (runs on every PR)      │    │
                    │    │                          │    │
                    │    │   • Unit tests (fast)     │    │
                    │    │   • Emulator tests (full) │    │
                    │    │   • Visual regression     │    │
                    │    │   • Perf budgets          │    │
                    │    │   • API contracts         │    │
                    │    └──────────────────────────┘    │
                    │                                      │
                    │         SELF-MAINTENANCE              │
                    │    • Prune redundant tests            │
                    │    • Heal flaky tests                 │
                    │    • Deprecate dead-code tests        │
                    │    • Merge overlapping tests          │
                    └──────────────────────────────────────┘
```
