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
| `hermes/compile` | `queued` → `metro_bundling` → `hermesc_compiling` → `validating` → `ready` |
| `bundle/push` | `queued` → `uploading` → `validating` → `bridge_reload` → `ready` |

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

## Future (not in v1)

- v2: replace the JSON-over-SSE channel with a CBOR-framed
  multiplexed QUIC stream so we can move blackbox + bundle-push +
  feedback onto the same envelope.
- v3: bidirectional control plane — consumer can SUBSCRIBE/PAUSE
  per-topic without HTTP roundtrips.
- v4: cross-language client libraries (Swift, Kotlin, Rust) so any
  Yaver SDK can speak the protocol natively.

See `docs/yaver-protocol-v1-design.md` (TBD) for the full v2/3/4 plan.
