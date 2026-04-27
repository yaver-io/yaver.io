# Yaver Test Suite — Developer Guide

Everything in `.github/workflows/` runs on a public repo so CI minutes are unmetered. Tests are organised by what they verify, not by who wrote them.

## Critical-path headless tests (run every release)

These are the tests that prove the core user-visible flows work end-to-end against the live `yaver-test-ephemeral` Hetzner box.

| Workflow | What it verifies | Runs on |
|---|---|---|
| `Mobile Hermes Through Relay Smoke` | Bundle compile + Hermes bytecode validation through `public.yaver.io` | every release tag + manual |
| `Webview Through Relay Smoke` | `/dev/start` → Metro listen → `/dev/events` SSE flow through relay | every release tag + manual |
| `Restart sfmg dev + verify SSE` | Stop → start → 10 s SSE sample on both localhost and relay | manual |
| `SSE With Bearer Header` | `?token=` query auth and `Authorization` header path through relay | manual |
| `Verify Yaver Protocol v1` | Asserts `snapshot`, `phase`, `progress` events emitted by agent v1.99.67+ | manual |
| `Feedback SDK Relay Smoke` | `/feedback` upload + `/blackbox/command-stream` SSE handshake | every release tag + manual |

All run via `gh workflow run <name>.yml`. They use `secrets.HCLOUD_SSH_PRIVATE_KEY` + `secrets.HETZNER_TEST_SERVER_IP` to drive the test box.

## Local test entry point

```bash
./scripts/test-suite.sh                    # everything that runs on this Mac
./scripts/test-suite.sh --unit             # Go unit tests only (~30 s)
./scripts/test-suite.sh --lan              # localhost direct connect (~1 min)
./scripts/test-suite.sh --relay            # local relay + agent task flow (~2 min)
./scripts/test-suite.sh --relay-docker     # deploy + test + teardown on Hetzner
./scripts/test-suite.sh --tailscale        # cross-machine via Tailscale
./scripts/test-suite.sh --cloudflare       # Cloudflare tunnel
./scripts/test-suite.sh --sdk              # SDKs unit + integration
```

No credentials needed for `--unit`, `--lan`, `--relay`. Remote modes need `REMOTE_SERVER_IP` + SSH key; either set as env vars or store in `.env.test` (gitignored).

## Per-component tests

### Agent (Go)

```bash
cd desktop/agent
go test ./...                               # all unit tests
go test -run TestYaverProtocol ./...        # protocol parser tests
go vet ./...                                # static checks
```

Unit tests spin up real HTTP servers on random ports — no mocks, no external deps. Coverage:
- HTTP API (auth, tasks, agent status, ping/pong, shutdown)
- MCP protocol (initialize + tools/list JSON-RPC)
- SDK token security (scope restriction, IP allowlist, IP binding, TLS, cache isolation, new-device tracking, cross-user rejection — 25+ tests)
- Two-agent integration (token isolation, task separation)

### Web dashboard (Next.js)

```bash
cd web
npx tsc --noEmit                            # typecheck (~30 s)
npm run build                               # production build
```

The dashboard is rendered by Next.js + deployed via `@opennextjs/cloudflare` to Cloudflare Workers.

### Mobile app (React Native + Expo)

```bash
cd mobile
npx tsc --noEmit                            # typecheck
```

The mobile app is built **natively** via Xcode / Gradle — never via `expo run:ios` for distribution. Use Yaver's local-deploy scripts (`scripts/deploy-testflight.sh` / `scripts/deploy-playstore.sh`) for builds, never CI for iOS.

### Browser end-to-end (Playwright)

```bash
cd e2e
npm install
npx playwright install --with-deps chromium    # first run only
npm test                                       # boots Next dev server, runs in headless Chromium
```

Tests live in `e2e/tests/`. CI: `.github/workflows/e2e.yml`.

### Selenium (Yaver Protocol consumer)

`Selenium Web App sfmg` workflow drives a headless Chromium against `https://yaver.io/dashboard` and asserts:
- Webview tab clicks
- Web App toggle switches
- Bundling progress UI renders OR iframe swaps to Expo Web

This test injects a localStorage token but doesn't establish a Convex session — useful for catching regressions in dashboard rendering, less useful for proving the full auth+device flow. The localhost-on-box smoke tests are the source of truth for protocol correctness.

## Workflow invocation cheat sheet

```bash
# All headless against yaver-test-ephemeral
gh workflow run 'Mobile Hermes Through Relay Smoke'
gh workflow run 'Webview Through Relay Smoke'
gh workflow run 'Verify Yaver Protocol v1'
gh workflow run 'Feedback SDK Relay Smoke'
gh workflow run 'Restart sfmg dev + verify SSE'
gh workflow run 'SSE With Bearer Header'

# Diagnostic helpers (when something is broken)
gh workflow run 'Diagnose Web Preview Load'
gh workflow run 'Diagnose Expo Prebuild'
gh workflow run 'Diagnose Dev Events SSE'

# Force-update yaver-test-ephemeral past a stale auto-update repo
gh workflow run force-update-test-ephemeral.yml -f version=1.99.67
```

## Yaver Protocol v1 — testing the producer

`Verify Yaver Protocol v1` SSHes into the test box, restarts the sfmg dev server, samples `/dev/events` for 25 s, and asserts:
- `snapshot` events fire (≥ 1 in the window — every 5 s while running)
- `heartbeat` events fire (legacy)
- `phase` and/or `progress` events fire when bundling is active

It also pretty-prints the first `progress` event so you can visually confirm `topic` / `pct` / `done` / `total` / `currentFile` / `progressSrc` are populated.

## Yaver Protocol v1 — testing the consumer

The web dashboard's CONSOLE strip renders per-topic progress bars from `topicProgress` state (see `web/components/dashboard/PreviewPane.tsx::ConsoleStatusHeader`). To test by hand:

1. Hard-refresh `https://yaver.io/dashboard`
2. Open Webview tab on a connected device
3. Click `▶ START` on an Expo project
4. Watch the CONSOLE: real `dev/start` progress bar should appear with `metro bundling 67% — 1247/2390 modules — Route.js`
5. Channel chip should stay green (`channel: live`) throughout

If channel goes amber/red while compile is healthy, the agent isn't emitting snapshots correctly — re-run `Verify Yaver Protocol v1` to confirm.

The mobile app's DevPreview banner reads the same SSE stream via `mobile/src/components/DevPreview.tsx` and renders the same per-topic progress info inside the bundle banner.

## Adding a new test

1. New workflow under `.github/workflows/<name>.yml` with `on: { workflow_dispatch }` so it doesn't fire automatically.
2. Use `actions/checkout@v4` + the SSH-key prep block from any existing diag workflow as your starting point.
3. Render your bash + python script via `cat > /tmp/x.sh <<'BODY' ... BODY` (avoids YAML heredoc collision pitfalls — see `force-update-test-ephemeral.yml` for the canonical layout).
4. Validate locally first: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/<name>.yml'))"`.
5. Add it to this file's tables.

## CI minutes notes

The repo is public; GitHub Actions minutes are unmetered. Run tests as often as you want. The only real budget is wall-clock time:
- Unit tests: ~30 s
- Critical-path smokes: ~3-5 min each (most of the time is QUIC tunnel reconnect + dev server bring-up)
- Selenium: ~5-10 min
- E2E: ~5 min
