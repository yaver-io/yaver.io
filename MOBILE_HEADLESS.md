# `mobile-headless` — mobile app surrogate for tests + MCP

A Node/Bun harness that runs the **exact code** from `mobile/src/lib/*`
against a real Yaver agent, with the React UI layer lopped off and a
handful of native modules shimmed. Lets CI run end-to-end smoke tests
without simulators, lets an AI vibe-coding agent drive the mobile app
through MCP, and gives a human a CLI for ad-hoc "pretend I'm tapping"
sessions.

## Why this instead of QEMU / Appium / Maestro

QEMU + an actual emulator proves full-stack behavior but is
~10-100× heavier than we need for smoke. Most mobile-app regressions
at this phase of the project sit in the data/protocol layer
(auth, QUIC relay, install flows, wizard answers, guest invites,
dev-server push). That layer is plain TypeScript running on React
Native's JS runtime — it has no reason to depend on a UI.

`mobile-headless` covers that 90% in seconds per run on GitHub's
free public-repo runners. UI-only regressions (a button offscreen,
a modal that won't dismiss) still need Maestro/Detox/a device;
that's a future addendum, not this doc.

## Goals / non-goals

**Goals**
- Share the real mobile TS lib code verbatim — no forks, no stubs.
- One MobileClient facade, three surfaces: programmatic (tests), CLI
  (ad-hoc), MCP (AI-driven).
- GitHub-hosted CI runs the full smoke suite on every PR.
- Feels "part of the CLI" — `yaver mobile-test …` works for a user.

**Non-goals**
- Render any React component.
- Cover UI-layer regressions (layout, keyboard avoidance, gestures).
- Replace Playwright/Detox for iOS/Android native QA.

## File layout

```
mobile-headless/
  package.json              # deps: typescript, zod, minimist, @modelcontextprotocol/sdk
                            # bin: { "yaver-mobile-headless": "./dist/bin/cli.js" }
  tsconfig.json             # extends mobile/tsconfig.json, adds `paths` → shims
  README.md

  src/
    shims/
      react-native.ts            # Platform, Alert, AppState, NetInfo (minimal)
      react-native-udp.ts        # dgram-backed — beacon actually works
      expo-secure-store.ts       # file-backed, chmod 600
      expo-device.ts             # constants, overridable via env
      expo-constants.ts
      async-storage.ts           # JSON file
      platform-detect.ts         # returns "ios" | "android" from env or default

    mobile-client.ts        # MobileClient class — one stable facade
    journeys/
      auth.ts               # signIn, signOut, link, merge, providers
      devices.ts            # listDevices, connect, waitForBeacon, infraSummary
      wizard.ts             # new-project Q&A, presets, generate
      install.ts            # listInstallables, installTool, respondSudo,
                            #   SSE stream → async iterable of events
      guests.ts             # invite, accept, list, revoke, configs
      phone-project.ts      # phone-first backend: create, browse, promote
      dev-server.ts         # start, stop, reload, status
      tasks.ts              # createTask, pollTask — thin wrapper

    mock-agent.ts           # in-process HTTP server used by hermetic tests

    bin/
      cli.ts                # argv parser → MobileClient
      mcp.ts                # stdio MCP server, exposes mobile_tap_* + mobile_api_*

  test/
    hermetic.test.ts        # uses mock-agent.ts, no external deps
    smoke-local.test.ts     # spins up `yaver serve` in background, drives it
    smoke-wizard-preset.test.ts
    smoke-install.test.ts   # ollama install on ubuntu-latest, w/ sudo respond
    smoke-guest-flow.test.ts
    wizard-snapshot.test.ts # catches new wizard questions w/o default
    fixtures/
      scratch-convex.ts     # ensures dev Convex has a throwaway test user
```

## Shim strategy

Resolved via **tsconfig `paths`** so the mobile source never changes.
Total shim code: ≤ 300 lines. Each shim is 10–40 lines.

| Module | Shim behavior |
|---|---|
| `react-native` core | `{ Platform: { OS: "ios" \| "android" }, Alert: { alert: (t)=>log(t) }, AppState: { addEventListener: ()=>({remove(){}}) } }`. |
| `react-native-udp` | Wraps Node `dgram` with the same surface: `createSocket({type})`, `bind(port, cb)`, `send()`, `on("message", cb)`. Makes `beacon.ts` work verbatim. |
| `@react-native-async-storage/async-storage` | Single JSON file at `$YAVER_MOBILE_HEADLESS_DIR/storage.json`. `getItem/setItem/removeItem/clear`. |
| `expo-secure-store` | `$YAVER_MOBILE_HEADLESS_DIR/secure.json`, perms 600. |
| `expo-device` | Constants: `osName`, `modelName`, `deviceName`, `brand`, `manufacturer`. Overridable via env (`YMH_PLATFORM`, `YMH_DEVICE_NAME`). |
| `expo-constants` | Same pattern — static bag, env-overridable. |

Not shimmed because the `lib/` layer doesn't import them:
`expo-router`, `expo-haptics`, `expo-blur`, `react-native-gesture-handler`,
`react-native-reanimated`, `@shopify/flash-list`.

## Runtime choice

**Bun** as the default runtime.
Reasons: native TS + JSX, zero build step, fast install in CI
(one curl line), same binary runs CLI + MCP + jest-compatible tests.
Fallback `tsx`/`node --import=tsx` behind a flag for platforms where
bun isn't easy.

Packaged output: `bun build ./src/bin/cli.ts --compile` produces a
single-file executable per OS/arch for distribution, same way
`yaver-cli` is shipped.

## State isolation (the one code change in `mobile/`)

`mobile/src/lib/quic.ts` exports a module-level `quicClient` singleton.
For harness tests we need a fresh instance per `MobileClient`.

Add a factory export (one-line change) next to the existing singleton:

```ts
// mobile/src/lib/quic.ts (existing)
export const quicClient = new QuicClient();

// + new export, unused by the mobile app itself, used by headless:
export function createQuicClient() { return new QuicClient(); }
```

No behavior change for the real app. Same pattern for any other
singletons we find (probably `beacon` state and maybe `DeviceContext`
module-level cache — audit during M1).

## The `MobileClient` facade

One stable shape, consumed by CLI + MCP + tests:

```ts
class MobileClient {
  constructor(opts?: {
    dataDir?: string;             // default: $YAVER_MOBILE_HEADLESS_DIR or tmp
    convexUrl?: string;           // default: prod Convex
    authToken?: string;           // optional — can also sign in via signIn()
    platform?: "ios" | "android"; // default: "ios"
    deviceName?: string;          // default: "mobile-headless"
  });

  // Auth
  signIn(p: { token?: string; email?: string; password?: string }): Promise<void>;
  signOut(): Promise<void>;
  listIdentities(): Promise<Identity[]>;

  // Devices
  listDevices(): Promise<Device[]>;
  connect(deviceId?: string): Promise<void>;  // auto if only one
  waitForBeacon(opts?: { timeoutMs: number }): Promise<Beacon>;
  infraSummary(target?: string): Promise<InfraSummary>;

  // Install catalogue
  listInstallables(target?: string): Promise<InstallEntry[]>;
  installTool(tool: string, opts?: { target?: string }): AsyncIterable<InstallEvent>;
  respondSudo(tool: string, password: string, opts?: { target?: string }): Promise<void>;
  cancelSudo(tool: string, opts?: { target?: string }): Promise<void>;

  // Wizard
  wizard: WizardJourney;         // .start() .answer() .applyPreset() .generate()

  // Guests
  guests: GuestsJourney;

  // Phone projects
  phoneProject: PhoneProjectJourney;

  // Dev server
  devServer: DevServerJourney;

  // Raw escape hatch (also what powers mobile_api_* MCP tools)
  raw: { get(path: string): Promise<any>; post(path: string, body?: any): Promise<any> };
}
```

## CLI surface

Two entry points, same binary:

```bash
# Direct binary (for people who ran `npm i -g yaver-mobile-headless`)
yaver-mobile-headless sign-in --token=...
yaver-mobile-headless devices
yaver-mobile-headless wizard --preset=indie-maker --generate-into=/tmp/proj
yaver-mobile-headless install ollama --target=<deviceId>
yaver-mobile-headless guests invite foo@bar.com
yaver-mobile-headless mcp          # stdio MCP server mode

# Subcommand surfaced from Go CLI (shells out to the npm binary)
yaver mobile-test sign-in --token=...
yaver mobile-test wizard --preset=indie-maker
# If the npm binary is missing:
#   "yaver mobile-test requires yaver-mobile-headless. Install with:
#      npm i -g yaver-mobile-headless"
```

## MCP surface (both styles, one server)

Two namespaces on the same stdio MCP server:

**`mobile_tap_*`** — screen-level actions, reads like manual QA:
- `mobile_sign_in` `{ token?, email?, password? }`
- `mobile_tap_devices` `{}` → returns list
- `mobile_tap_select_device` `{ deviceId }`
- `mobile_tap_new_project` `{}` → starts wizard, returns first question
- `mobile_wizard_answer` `{ questionId, answer }`
- `mobile_wizard_apply_preset` `{ preset: "indie-maker" | ... }`
- `mobile_wizard_generate` `{ parentDir? }`
- `mobile_tap_install_tool` `{ tool, target? }` → streams events
- `mobile_respond_sudo` `{ tool, password }`
- `mobile_tap_invite_guest` `{ email }`

**`mobile_api_*`** — raw endpoint pass-throughs, one per mobile HTTP
call. Cheap to autogenerate from `mobile/src/lib/quic.ts`:
- `mobile_api_install_list` `{ target? }`
- `mobile_api_infra_summary` `{ target? }`
- `mobile_api_wizard_start` `{}`
- `mobile_api_wizard_answer` `{ sessionId, questionId, answer }`
- `mobile_api_raw` `{ method, path, body? }` — escape hatch

The pairing is deliberate: screen-level for UX flows, API-level for
fine-grained regression probes.

## CI wiring

Three layers, all on GitHub-hosted runners (public repo, free minutes):

### 1. Hermetic tests — every PR, < 30s
```yaml
# .github/workflows/mobile-headless.yml
- runs: bun install
- runs: bun test mobile-headless/test/hermetic.test.ts
```
`mock-agent.ts` spins up a fake HTTP server in-process. No `yaver serve`,
no Convex, no network. Catches regressions in the mobile lib's own logic
and the shims.

### 2. Smoke against local agent — every PR, ~2 min
```yaml
- runs: go build ./desktop/agent
- runs: ./agent serve --port=18080 &
- runs: wait-for-health http://localhost:18080/health
- runs: bun test mobile-headless/test/smoke-local.test.ts
```
Proves the real agent HTTP contract works. Tests: wizard round-trip,
infra summary, install-list, dev server start/stop.

### 3. Install matrix — nightly + on-demand, ~5 min
```yaml
strategy:
  matrix:
    os: [ubuntu-latest, macos-latest]
    tool: [fd, ollama, ripgrep]
```
Runs real `mobile_tap_install_tool` against each OS, exercising the
Convex registry + PTY + sudo response path (passwordless sudo on GitHub
runners, so `respondSudo` is exercised with a stub credential).

## Wizard snapshot test

Tiny safety net for "you added a wizard question without wiring a
default, and now the bento e2e test hangs":

```ts
// test/wizard-snapshot.test.ts
import { wizardQuestionIds } from "../src/journeys/wizard";
it("has stable question ids + defaults", async () => {
  const snapshot = await mobile.raw.get("/project/wizard/questions");
  expect(snapshot.map((q) => [q.id, q.default ?? "(none)"])).toMatchSnapshot();
});
```

Runs in hermetic suite. When someone adds a question, the snapshot
update is the review signal. Zero runtime cost.

## Convex strategy

Point CI at the existing dev Convex (`perceptive-minnow-557`). Create
a throwaway test user in `global-setup` + tear down in `global-teardown`
— same pattern `e2e/global-setup.ts` already uses for Playwright.
No new Convex project needed.

## Milestones

| # | Scope | ETA |
|---|---|---|
| **M1** | Scaffold + shims + `MobileClient` skeleton with auth · devices · wizard · install. One hermetic test + one local-agent smoke test passing. Factory export in `mobile/src/lib/quic.ts`. | ~½ day |
| **M2** | Full journey coverage (guests · phone-project · dev-server · tasks). CLI entry point with ~15 subcommands. | ~½ day |
| **M3** | MCP server with `mobile_tap_*` + `mobile_api_*` tools. Wizard snapshot test. In-process mock agent fleshed out. | ~½ day |
| **M4** | `.github/workflows/mobile-headless.yml` running the three layers on ubuntu-latest + macos-latest. `yaver mobile-test` subcommand added to Go CLI (shells out). npm publish pipeline for `yaver-mobile-headless`. | ~½ day |

~2 days of real work if nothing exotic surfaces.

## Locked decisions

- Path: `/Users/kivanccakmak/Workspace/yaver.io/mobile-headless/`
- npm package name: `yaver-mobile-headless`
- Go CLI subcommand: `yaver mobile-test …` (shells out to npm binary)
- Runtime: **Bun** (fallback tsx behind a flag)
- MCP: **both** `mobile_tap_*` + `mobile_api_*` namespaces on one server
- CI: **GitHub-hosted** runners only (public repo)
- Convex: **dev project + throwaway user**, same as Playwright e2e
- Wizard snapshot: **yes**, in hermetic suite

## Open questions for later

- Does the MCP server need RemoteTrigger / cron-style hooks (so a
  scheduled GH action can run `mobile_tap_install_tool` weekly)?
- Do we extend `mobile-headless` to also drive the **web** dashboard
  (`web/lib/agent-client.ts` is almost identical to
  `mobile/src/lib/quic.ts`) as `web-headless`, or keep them separate
  and share a `headless-core` package? Probably defer until M4 ships.
- Should `mobile-headless` emit structured events (JSONL to stdout)
  for long-running flows so an MCP caller gets progress updates
  instead of waiting for a terminal result? Expect yes; will add in
  M3.
