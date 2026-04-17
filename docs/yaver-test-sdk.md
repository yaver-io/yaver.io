# yaver-test-sdk

End-to-end testing for solo developers. **$0/month CI** — every test
runs on your own hardware. No Playwright, no Selenium, no browser
driver to install. The runner is a single Go binary you already have
(`yaver`).

This page is the canonical reference. For the why behind the
design see [`docs/autotest-spec.md`](./autotest-spec.md); for the
roadmap see [`docs/roadmap_ci_solo_developer_lower_costs.md`](./roadmap_ci_solo_developer_lower_costs.md).

---

## Install

`yaver-test-sdk` ships inside the `yaver` binary. If you already
have Yaver installed, you have the SDK.

```bash
# macOS / Linux
brew tap kivanccakmak/yaver && brew install yaver

# Windows
scoop bucket add yaver https://github.com/kivanccakmak/scoop-yaver
scoop install yaver
```

Verify:

```bash
yaver test --help
```

---

## First spec in 60 seconds

```bash
# 1. Drop example specs into the current repo.
yaver test init

# 2. Run everything.
yaver test run
```

`yaver test init` creates:

```
yaver-tests/
├── README.md                 # per-repo docs
├── .gitignore                # ignores .yaver-test-results/
├── smoke.test.yaml           # example web spec
└── macros/                   # shared macros (empty at init)
```

`yaver test run` walks every `*.test.yaml` (or `.test.yml`) under
`yaver-tests/` in lexical order and runs them. Failing specs drop
screenshots, HAR, axe reports, and console logs under
`.yaver-test-results/<spec-name>/`.

---

## Spec vocabulary

A spec is YAML. Top-level fields:

| Field        | Type             | Purpose                                                           |
|--------------|------------------|-------------------------------------------------------------------|
| `name`       | string           | Human label. Defaults to the filename.                            |
| `target`     | enum             | `web` (default) • `ios-sim` • `android-emu` • `device`            |
| `url`        | string           | Start page (web targets).                                         |
| `app`        | string           | Path to built `.app` / `.apk` (mobile targets).                   |
| `viewport`   | `{width, height}`| Chromium viewport. Optional.                                      |
| `headful`    | bool             | Show the browser. Default headless.                               |
| `timeout_ms` | int              | Default per-step timeout. Default `7000`.                         |
| `setup`      | `Step[]`         | Runs once before `steps`. Failure aborts the spec.                |
| `steps`      | `Step[]`         | The test body.                                                    |
| `teardown`   | `Step[]`         | Runs once after `steps`, always.                                  |
| `artifacts`  | `ArtifactsConfig`| Screenshot / trace / video config.                                |
| `capture`    | `CaptureConfig`  | Enable console / network / perf / a11y collection.                |
| `include`    | `string[]`       | File-level macros — see [Macros](#macros).                        |

### Step actions

Each step sets exactly one action field. The runner dispatches on
whichever is non-empty. The YAML reads like a scripting language:

```yaml
steps:
  - goto: /auth
  - click: 'button:has-text("Sign In")'
  - fill:
      selector: 'input[type=email]'
      text: 'dev@example.test'
  - assert.visible: 'text=Welcome back'
  - screenshot: true
```

| Action              | Argument                          | What it does                                                  |
|---------------------|-----------------------------------|---------------------------------------------------------------|
| `goto`              | URL or path                       | Navigate. Paths are joined with `Spec.URL`.                   |
| `click`             | CSS selector                      | Click the first match.                                        |
| `fill`              | `{selector, text}`                | Type into an input.                                           |
| `wait_for`          | CSS selector                      | Wait until visible.                                           |
| `wait_for_url`      | substring                         | Wait until the URL contains it.                               |
| `sleep_ms`          | int                               | Pause. Use sparingly.                                         |
| `assert.visible`    | CSS selector                      | Fail if the element isn't visible.                            |
| `assert.text`       | substring                         | Fail if the page doesn't contain the text.                    |
| `assert.title`      | substring                         | Fail on title mismatch.                                       |
| `assert.url`        | substring                         | Fail on URL mismatch.                                         |
| `screenshot`        | `true`                            | Save a PNG under the spec's artifact dir.                     |
| `snapshot`          | name                              | Visual snapshot — baseline on first run, diff on subsequent.  |
| `inspect`           | question (optional)               | Run an LLM visual inspection on the current screenshot.       |
| `a11y`              | `{min_impact, tags}`              | Run an axe-core audit on the current page.                    |
| `save_har`          | filename                          | Dump accumulated network capture as HAR 1.2.                  |
| `eval`              | JS string                         | Run raw JavaScript in the page context.                       |
| `include`           | path                              | Inline a macro at this position — see [Macros](#macros).      |

### Artifacts config

```yaml
artifacts:
  on: failure       # always | failure (default) | never
  screenshot: true  # default true
  trace: false      # CDP trace zip (M6)
  video: false      # mp4 (M6)
```

### Capture config (instrumentation)

```yaml
capture:
  console_errors: true   # JS console feed
  network: true          # URL, method, status, timing, size
  performance: true      # DOMContentLoaded, load, LCP, CLS
```

Collected data is written to `.yaver-test-results/<spec>/` as
`console.json`, `network.har`, and `performance.json`. The dev's
own LLM (`inspect`) and the auto-fix log both consume these files.

### a11y audit

```yaml
steps:
  - goto: /
  - a11y:
      min_impact: serious    # minor | moderate | serious | critical
      tags: [wcag2aa]        # axe rule tags; empty = axe defaults
```

Any violation at or above `min_impact` fails the step. The full
violation list is saved as `axe-<step>.json` for the mobile Runs
tab to scroll.

---

## Macros

Reusable step sequences. Extract once, reuse across every spec.

### File-level include (prepends to setup)

```yaml
# yaver-tests/checkout.test.yaml
name: checkout
target: web
url: https://dev.example.test

include:
  - macros/login.test.yaml

steps:
  - click: 'button.buy'
```

`yaver-tests/macros/login.test.yaml` is loaded and its
`setup + steps` are prepended to `checkout`'s setup, so "log in as
testuser" fires before the first real step.

### Positional include (mid-flow)

Use the `- include:` step marker when the macro has to fire in the
middle of a flow — "log in as admin before the delete":

```yaml
# yaver-tests/delete-user.test.yaml
steps:
  - goto: /
  - include: macros/admin-login.test.yaml
  - click: 'button.delete'
```

The macro expands in place. Depth-guarded (max 8) and cycle-safe.

---

## Targets

| Target          | Status | Driver                                   | Notes                                                  |
|-----------------|:------:|------------------------------------------|--------------------------------------------------------|
| `web`           | Yes    | chromedp / CDP                           | Ships with the binary. Headless by default.            |
| `ios-sim`       | Yes    | `xcrun simctl` + WebDriverAgent          | Screenshot + tap by coordinate. Selector taps via WDA. |
| `android-emu`   | Yes    | `emulator` + UIAutomator2                | Screenshot + tap by coordinate + selector.             |
| `device`        | Yes    | `idevice_*` + `adb` + yaver-test-sdk SDK | Physical iPhone or Android over USB.                   |
| Safari (macOS)  | Local  | `safaridriver` (W3C)                     | Opt-in: `sudo safaridriver --enable` once.             |
| Firefox         | Yes    | `geckodriver` (W3C)                      | Ships built-in.                                        |

For mobile smoke specs in CI, keep the spec committed and pass the
build output in via env vars:

```yaml
# yaver-tests/mobile-ios-smoke.test.yaml
name: ${YAVER_TEST_IOS_SIM_DEVICE} local-first smoke
target: ios-sim
url: ${YAVER_TEST_IOS_SIM_DEVICE}
app: ${YAVER_TEST_IOS_APP}
steps:
  - goto: ${YAVER_TEST_IOS_BUNDLE_ID}
  - sleep_ms: 3000
  - screenshot: true
```

```bash
export YAVER_TEST_IOS_SIM_DEVICE="iPhone 17 Pro"
export YAVER_TEST_IOS_APP="/tmp/Derived/Build/Products/Debug-iphonesimulator/Yaver.app"
export YAVER_TEST_IOS_BUNDLE_ID="io.yaver.mobile"
yaver test run yaver-tests/mobile-ios-smoke.test.yaml
```

Same shape for Android:

```bash
export YAVER_TEST_ANDROID_AVD="Pixel_6_API_34"
export YAVER_TEST_ANDROID_APK="$PWD/mobile/android/app/build/outputs/apk/debug/app-debug.apk"
export YAVER_TEST_ANDROID_PACKAGE="io.yaver.mobile"
yaver test run yaver-tests/mobile-android-smoke.test.yaml
```

---

## CLI surface

```bash
yaver test init [--dir <repo>]           # scaffold yaver-tests/
yaver test run [path] [--watch]          # run one spec or all specs
yaver test record --url <url>            # open browser, record a spec
yaver test history [path]                # show recent runs
yaver test flake [path]                  # per-spec failure ratios
yaver test sync                          # print local pass markers
yaver test schedule <cron> [root]        # register a cron entry
yaver test debug --capture-packets ...   # tcpdump escape hatch
yaver test debug --install-axe           # bootstrap axe-core
```

Run history is written to `.yaver-test-results/.history.jsonl`
(one line per run). The `flake` command reads it and prints
per-spec failure ratios.

### `--watch` mode

`yaver test run --watch` re-runs the relevant specs whenever a
file under `yaver-tests/` or the app source changes. This is the
vibe-coding inner loop — the dev hacks code, the tests re-run
automatically, failure artifacts land on disk.

---

## Auto-fix log

When a spec fails, the self-heal pipeline can (optionally) ask the
dev's LLM to propose a patch. Every proposed patch is written to
`.yaver-test-results/autofix-log/<spec>-<ts>.json` with the full
diff, the prompt, and the rationale. Applied patches show up as
commits — the log is the "undo" ledger.

Config lives in `~/.yaver/config.json`:

```jsonc
{
  "testkit": {
    "self_heal": true,
    "llm_provider": "mistral"  // mistral | anthropic | openai | ollama
  }
}
```

The dev's API key never leaves the agent. LLM calls go directly
to the provider from the local machine.

---

## GH Actions integration

`yaver test run` writes pass markers into
`.yaver-test-results/.pass-markers/<spec>.sha256`. The marker is a
SHA256 of the spec + its dependencies. If the current commit's
markers match, `yaver test sync` exits 0 immediately — CI on GH
Actions short-circuits and doesn't re-run the local-green suite.

In `.github/workflows/ci.yml`:

```yaml
- run: yaver test sync
```

This is how yaver-test-sdk stays free: GH Actions only runs the
code that changed and its dependents, never the full suite.

---

## Remote dev machine (Hetzner)

If your laptop is too small for long-running mobile emulator runs,
deploy a dedicated agent to a Hetzner box (or any Linux VPS):

```bash
./scripts/deploy-yaver-agent-hetzner.sh <server-ip>
```

The script installs Yaver, brings up the agent as a systemd unit,
and sets up auto-update. Point your mobile app at the remote
machine; `yaver test run` runs there instead of on your laptop.
Cost: whatever your Hetzner box costs (~$5/mo is typical).

---

## Mobile "Local CI" tab

Open the Yaver mobile app → **Runs** tab. Seven sub-tabs:

| Tab         | What it shows                                           |
|-------------|--------------------------------------------------------|
| Specs       | Every spec the agent has discovered under `yaver-tests/`|
| Runs        | Recent run history with pass/fail + duration          |
| Alerts      | Notification feed (fails, flakes, self-heal proposals)|
| Auto-fixes  | The autofix-log ledger with one-tap undo              |
| Devices     | Connected iOS/Android/emulator devices                |
| Flake       | Per-spec failure ratio from history                   |
| Setup       | Spec scaffolding + dependency installer               |

Everything is driven over the existing P2P channel — no Convex
calls, no cloud.

---

## Why is this free?

- The runner is Go code compiled into `yaver`. No external browser
  driver, no language runtime to install.
- Every spec runs on the dev's own hardware. No per-minute cloud
  billing, no test-cloud quota.
- GH Actions only runs the diff (via `yaver test sync`), so the
  public-repo free tier is enough for any real project.
- The self-heal LLM calls go from the dev's machine straight to
  their provider — the dev pays only for the tokens they consume.

Nothing touches a central Yaver server. There is no central
Yaver server.

---

## Files of interest

- `desktop/agent/testkit/spec.go` — spec struct, loader, step vocabulary
- `desktop/agent/testkit/runner.go` — the chromedp / CDP driver
- `desktop/agent/testkit/instrumentation.go` — console/network/perf capture
- `desktop/agent/testkit/a11y.go` — axe-core integration
- `desktop/agent/testkit/snapshot.go` — visual diffing
- `desktop/agent/testkit/autofix_log.go` — auto-fix ledger
- `desktop/agent/testkit/driver_*.go` — per-platform drivers
- `desktop/agent/test_cmd.go` — CLI entry points
- `mobile/app/(tabs)/runs.tsx` — mobile "Local CI" screen
