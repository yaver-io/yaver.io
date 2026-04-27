# Yaver Protocol v1

The Yaver Protocol is the producer/consumer messaging contract between
the **Go agent** (producer) and the **web dashboard / mobile app**
(consumers). v1 lives on the existing SSE channel at `/dev/events`;
the wire format is JSON-encoded `DevServerEvent` objects.

The whole point: **the consumer never feels disconnected**. The agent
guarantees a `snapshot` event every 5 seconds while a dev server is
running, plus structured `progress` events with real percentages
extracted from Metro / Expo / Hermesc stdout. The consumer renders
from the latest snapshot — if a delta event drops, the next snapshot
5 s later restores correct state.

## Event types

| `type` | When | Producer guarantee |
|---|---|---|
| `snapshot` | every 5 s while a dev server is running | full picture of every active topic + last known progress + recent log tail |
| `phase` | discrete phase transition | one event per transition; consumer renders phase label + advances progress bar |
| `progress` | percent update during a phase | throttled at 200 ms / 5 % delta; carries `pct`, `done`, `total`, `currentFile`, `etaMs` |
| `heartbeat` | every 5 s while running | proves transport is alive even when compile is silent |
| `log` | every stdout/stderr line | raw line for the CONSOLE pane |
| `starting` / `ready` / `reload` / `error` / `stopped` | lifecycle | legacy event types, kept for backwards-compat |

## Topics

Each `progress` / `phase` / `snapshot` carries a `topic` string. The
agent owns the topic taxonomy:

| Topic | Phases (in order) |
|---|---|
| `dev/start` | `queued` → `installing_deps` → `starting` → `metro_bundling` → `listening` → `idle` ⇄ `metro_bundling` (loops on each device fetch) |
| `webview/build` | `queued` → `preparing` → `web_bundling` → `listening` → `ready` |
| `webview/transport` | `compiled` → `ready_to_serve` → `serving` → `streaming` → `delivered` (or `error`) — per-bundle delivery lifecycle from compile-complete to iframe-loaded |
| `hermes/compile` | `queued` → `metro_bundling` → `hermesc_compiling` → `validating` → `ready` |
| `bundle/push` | `queued` → `uploading` → `validating` → `bridge_reload` → `ready` |

### Caller × Target × Endpoint matrix

Every event also carries a `caller` field (the originating surface,
e.g. `mobile-app/1.18.15` or `web-dashboard/1.1.83`) so the dashboard
CONSOLE can attribute phases to the surface that triggered them. The
agent reads `X-Yaver-Caller` request header (or `?caller=` query param
for SSE-style requests where headers can't be set).

| Endpoint | Allowed callers | Build target | Output served at |
|---|---|---|---|
| `POST /dev/build-native target=mobile-hermes` | mobile-app/* | iOS / Android Hermes bytecode | `/dev/native-bundle` |
| `POST /dev/build-native target=web-js-bundle` | web-dashboard/*, mobile-app/* | static web bundle (`expo export -p web`) | `/dev/web-bundle/` |
| `POST /dev/build-native target=web-hermes-wasm` | (experimental) | HBC bundle + hermes.wasm runner | `/dev/web-bundle/` + `/dev/hermes-wasm-runtime` |
| `POST /dev/web-preview/start` | web-dashboard/* | live HMR Expo Web sibling | `/dev-web/` (proxied) |
| `POST /dev/web-bundle/ack` | dashboard iframe | — | iframe → agent: load complete |
| `POST /dev/web-bundle/error` | dashboard iframe | — | iframe → agent: JS init error |

## Progress payload

```ts
type Progress = {
  type: "progress";
  topic: string;          // see Topics
  phase: string;          // current phase
  pct: number;            // 0..100, REAL number from compiler stdout
  done?: number;          // e.g. 1247 modules
  total?: number;         // e.g. 2390 modules
  unit?: string;          // "modules" | "bytes" | "files" | "tasks"
  currentFile?: string;   // e.g. "node_modules/expo-router/build/Route.js"
  progressSrc:            // critically: tells UI whether pct is REAL
    | "exact"             //   parsed from "Bundling 67% (1247/2390)"
    | "heuristic"         //   estimated from rate
    | "unknown";          //   no idea — UI shows indeterminate spinner, NOT a fake bar
  etaMs?: number;         // est remaining millis (only when src=="exact")
};
```

The consumer **must** branch on `progressSrc`:
- `exact` → render a real progress bar, show "X / Y modules · ~Zs left"
- `heuristic` → render a progress bar with caveat label
- `unknown` → render an indeterminate spinner; never render a fake percentage

## Snapshot payload

```ts
type Snapshot = {
  type: "snapshot";
  snapshot: {
    generatedAt: string;
    running: boolean;
    framework: string;
    port: number;
    webPort: number;
    workDir: string;
    uptimeSec: number;
    idleSec: number;
    pid: number;
    pidAlive: boolean;
    phases: { [topic: string]: string };  // topic → current phase
    progress?: Progress;       // most recent active progress (dev/start)
    webProgress?: Progress;    // most recent active progress (webview/build)
    recentLogs: string[];      // last 8 stdout/stderr lines
    beatNumber: number;
  };
};
```

A reconnecting consumer reads ONE snapshot and is fully caught up.
No replay storm.

## Liveness contract

Consumers MUST decouple connection-health from compile state:

| Time since last byte (any kind) | UI label | Animation |
|---|---|---|
| < 6 s | `channel: live` (green dot) | normal |
| 6–15 s | `channel: syncing…` (amber, pulsing) | ping animation |
| 15–60 s | `channel: reconnecting…` (orange, spinner) | auto-reconnect attempts |
| > 60 s | `channel: lost — Reconnect & Fix` (red) | manual button |

The agent emits SOMETHING (snapshot or heartbeat) every 5 s, so 6 s
is a generous threshold. A user staring at a slow 90-second compile
sees the channel chip stay green — never "lost" — and a real progress
bar moving in the topic strip.

## Producer-side details (agent)

Implementation lives in:

- `desktop/agent/devserver_progress.go` — regex-parse Metro / Expo /
  hermesc stdout into `progressTracker.FeedLine(line)` calls; emits
  structured `progress` and `phase` events with real `pct`, `done`,
  `total`, `currentFile`.
- `desktop/agent/devserver.go` — `DevServerManager.heartbeatLoop`
  ticks every 5 s and emits BOTH `heartbeat` (legacy) AND `snapshot`
  (v1) events. `recordRecentLog` keeps the snapshot's `recentLogs`
  ring buffer fresh.
- `desktop/agent/devserver.go::DevServerEvent` — the JSON struct on
  the wire. New v1 fields are all `omitempty` for backwards-compat
  with v1.99.66 and earlier consumers.

The progress trackers are wired into the spawn pipeline so every
stdout line passes through `progressTracker.FeedLine` BEFORE being
emitted as a `log` event. The same line can produce up to 3 events
(phase, progress, log) — consumer dedupes by topic + ts.

## Consumer-side details

### Web dashboard (`web/components/dashboard/PreviewPane.tsx`)

- `topicProgress` state: `Record<topic, ProgressState>` updated by
  `progress` and `phase` events.
- `latestSnapshot` state: source-of-truth picture, reconciled from
  the `snapshot` event every 5 s.
- `lastByteAt` state: UNIX millis of the most recent SSE message of
  any type. Drives `connectionHealth`.
- `ConsoleStatusHeader` renders a per-topic progress bar with real
  `pct`, `currentFile`, `etaMs` — and the connection-health chip
  separately so compile state and transport state don't get
  conflated.

### Mobile app (`mobile/src/components/DevPreview.tsx`)

- `progressState`: same shape as web, single active topic at a time.
- `lastByteAt`: same liveness signal.
- DevPreview banner shows `{phase}: {pct}% — {currentFile}` when
  progress is exact; `{phase}…` when src is `unknown`.

## Versioning

- v1.99.67 (cli) + 1.18.15 (mobile) + 1.1.77 (web) ship the
  protocol producer and the consumer support.
- The agent's `DevServerEvent` is forward-compatible: older
  consumers ignore the new fields silently because they're all
  `omitempty`.
- The `type: "snapshot"` and `type: "progress"` and `type: "phase"`
  values are NEW — older consumers ignore them. They keep getting
  `heartbeat` + `log` like before.

## Web UI rendering paths — two ways to put a third-party app in the dashboard iframe

The dashboard's **Web App** sub-tab supports two distinct rendering
pipelines for showing a third-party RN/Expo project inline. Both run
through the agent — neither requires the user's phone to be online —
but they have different runtime characteristics, and a third-party
project supports them by meeting different sets of requirements.

### Path 1 — Web JS bundle (the default, "raw binary")

```
target=web-js-bundle
  ↓ agent runs `expo export -p web`
  ↓ bundle written to .yaver-build-web/ (HTML + _expo/static/js/* + assets/*)
  ↓ scanBundleManifest indexes every file by relative path + size
  ↓ webTransport tracker spun up → emits topic=webview/transport phases
  ↓ /dev/web-bundle/ serves files; index.html injected with <base href>
  ↓ iframe loads and runs the bundle in V8/JSC
```

Runtime in the browser: V8/JSC + DOM + react-native-web's primitive →
DOM mapping. Plain web app at the end of the day. **Recommended path
— stable, fast, no external runtime dependencies.**

**Third-party app requirements:**

| Requirement | Why |
|---|---|
| `react-native-web` in dependencies | `react-native` imports get aliased to it during the web build. Without RN-Web there's no DOM renderer for `<View>` / `<Text>` / etc. |
| `expo` in dependencies (for now) | The agent currently only supports projects that go through Expo's `expo export -p web`. Bare RN web bundling without Expo is a future path. |
| Source code free of native-only branches | Everything that needs to run in the web bundle must either work in a browser or be guarded by `Platform.OS === "web"` no-ops. Camera, BLE, Skia native shaders, `react-native-record-screen` — these have no browser implementations. |
| No iOS/Android-only assets in the entry path | `.ttf` / `.png` resolve via Metro, but anything pulled from `ios/`-only resource bundles will break. |

This is the path the dashboard auto-builds on Webview tab open
(`web 1.1.86+`). It's the path that just rendered sfmg's onboarding
in the iframe end-to-end during the v1.99.74 verification pass.

### Path 2 — Hermes WASM (best-effort, experimental)

```
target=web-hermes-wasm
  ↓ agent runs `expo export:embed --platform web` (Metro web preset)
  ↓ pipes the JS through hermesc → Hermes bytecode (HBC)
  ↓ writes runner HTML alongside the .jsbundle
  ↓ /dev/web-bundle/ serves the runner HTML
  ↓ runner loads /dev/hermes-wasm-runtime (hermes.wasm) + the HBC
  ↓ Hermes WASM evaluates the HBC in the browser
  ↓ react-native-web still does the actual DOM rendering
```

Runtime in the browser: **hermes.wasm** (~3 MB, no JIT) +
`react-native-web` for the renderer. Same engine as mobile, so the
same bytecode runs in both places — useful for determinism / sandbox
testing. Bytecode is identical to a desktop / CI compile, so semantic
parity is enforceable across mobile and web.

**Third-party app requirements (additional, on top of Path 1):**

| Requirement | Why |
|---|---|
| All Path 1 requirements | RN-Web is the renderer regardless of engine. |
| No JIT-required JS | Hermes' WASM build doesn't include the JIT, so heavy compute paths run interpreted (3-5× slower than V8). Avoid hot-path math libs that benchmark assuming a JIT. |
| Tolerant of stub native modules | Hermes WASM has no native module bridge. The agent injects globals (`process`, `HermesInternal`) but `TurboModuleRegistry.getEnforcing(...)` will throw on anything sfmg-style imports of `react-native-record-screen` etc. Same intersection problem as the mobile container. |
| HBC version match between hermesc and the runner | `hermes.wasm` shipped with the agent must match the BC version produced by the project's local hermesc. v96 today (RN 0.81 line). |

**Status: experimental.** The protocol is wired (build-native target,
serve endpoint, runner HTML, ack/error endpoints, transport tracker).
The runner HTML's JS that wires `hermes.wasm` to the iframe DOM is a
stub today — it surfaces a status banner indicating the engine compiled
but full execution is pending an upstream Hermes WASM runner. The
recommended path remains Path 1; Path 2 exists so the protocol half
of the work doesn't need to happen twice.

To enable Path 2 on a host: `yaver install hermes-wasm` (TODO) drops
hermes.wasm at `~/.yaver/runtimes/hermes-wasm/hermes.wasm`. Without
that file `/dev/hermes-wasm-runtime` returns 501 and the runner
reports "Hermes WASM runtime not installed".

### Decision matrix for third-party developers

| Goal | Path | Why |
|---|---|---|
| "I want my Expo app to render in the dashboard for design review / vibe coding without the phone running" | Path 1 (web-js-bundle) | Just works, fast, browser-native. |
| "I want byte-identical execution between mobile and web for parity testing" | Path 2 (web-hermes-wasm) | Same HBC bytecode runs in both runtimes. |
| "I'm running an RN-only app with no react-native-web" | Neither | The dashboard iframe can't render it — only the phone can via the existing Hermes super-host path. |
| "I have a heavy native dep (Camera, Skia native, BLE)" | Path 1 with `Platform.OS === "web"` guards | Branch around the missing native module on the web target. |

## Future (not in v1)

- v2: replace the JSON-over-SSE channel with a CBOR-framed
  multiplexed QUIC stream so we can move blackbox + bundle-push +
  feedback onto the same envelope.
- v3: bidirectional control plane — consumer can SUBSCRIBE/PAUSE
  per-topic without HTTP roundtrips.
- v4: cross-language client libraries (Swift, Kotlin, Rust) so any
  Yaver SDK can speak the protocol natively.

See `docs/yaver-protocol-v1-design.md` (TBD) for the full v2/3/4 plan.
