# yaver lib — the Fleet SDK

Yaver had a world-class *substrate* (ssh adoption, runners, mesh/relay/QUIC,
devices, vault, observability) and a 1000-verb MCP surface — but no **composable
programmable spine**: you could do anything to *one* box, one verb at a time,
but there was no way to address a *fleet* from code. The Fleet SDK is that spine.

It is a thin composition of pieces that already existed:
- **selection** → `POST /devices/select` (device tags, auto-seeded at registration)
- **per-machine transport** → the `connect.ts` ladder (direct-LAN → tunnel →
  relay, health-raced); relay's `X-Relay-Password` rides along automatically
- **actions** → the agent's existing `/exec`, `/tasks`, `/cron/*`, `/fleet/file`
  endpoints, called over whatever transport won

```ts
import { Fleet, fileAuditSink } from 'yaver-sdk';

const fleet = await Fleet.connect({
  token,
  relay,                                   // optional, for NAT'd machines
  approve: (ev) => ev.risk !== 'high' || ui.confirm(ev),   // P10 HITL gate
  onAudit: fileAuditSink('/var/log/yaver-fleet.jsonl'),    // P11 audit trail
});

const gpu = await fleet.select({ tags: ['gpu'], online: true });   // P2 selector

for await (const { machine, stream, text } of gpu.exec('nvidia-smi -L')) { … }   // P1 fan exec
for await (const { text } of (await fleet.machine('build-box')).agent('fix the flaky test', { runner: 'claude-code' })) { … }  // P3
await gpu.upload('./model.gguf', '/opt/models/model.gguf');         // P4 file transfer
await fleet.select({ tags: ['edge'] }).apply({                      // P9 verified action
  key: 'rate-v3', precheck: 'cfg get rate', expect: '5',
  do: 'cfg set rate 5', verify: 'cfg get rate', rollback: 'cfg set rate 0', onFail: 'rollback',
});
const out = await gpu.distribute(jobs, (m, job) => m.run(job.cmd), { concurrency: 2 });  // P7 commander/worker
await fleet.select({ tags: ['edge'] }).schedule({ name: 'backup', schedule: '0 3 * * *', command: 'yaver-edge backup' });  // P8 fleet cron
await fleet.all().then((s) => s.serviceRestart('yaver'));           // P6 lifecycle
```

## Shipped (tested + committed)

| # | Capability | Where |
|---|---|---|
| P2 | Device tags + selector query (auto-seeded gpu/arm64/x64/docker/…) | `backend/convex/devices.ts`, `http.ts` |
| P1 | `Fleet.connect` / `select` / `Machine` / `Selection`, fan `exec` merged + tagged by machine | `sdk/js/src/fleet.ts` |
| P3 | `agent()` — run claude-code/codex/opencode on box N, stream session (no SSH) | `fleet.ts` |
| P4 | `upload`/`download`/`uploadDir` over the resolved transport (mesh/relay/direct) | `desktop/agent/file_transfer_http.go`, `fleet.ts` |
| P9 | `apply()` — verified, idempotent, reversible mutation (precheck→do→verify→commit\|rollback) | `fleet.ts` |
| P7 | `distribute()` work-stealing commander/worker + `mapReduce()` | `fleet.ts` |
| P10 | `approve` human-in-the-loop gate (high-risk exec / agent / apply / upload) | `fleet.ts` |
| P11 | `onAudit` trail + `fileAuditSink` JSONL record plane | `fleet.ts` |
| P6 | `serviceRestart`/`service`/`reboot` (platform-native, gated) | `fleet.ts` |
| P8 | fleet-wide cron via `schedule`/`unschedule` fan-out | `fleet.ts` |
| P5 | `Machine.shell()` interactive PTY over the existing `/ws/terminal` | `fleet.ts` |
| P12 | local-first routing — `agent(prompt, { preferLocal })` picks the local runner on `local-inference` machines | `fleet.ts` |
| P13 | `Fleet.queue(path)` durable store-and-forward `CommandQueue` (disk-persisted) | `fleet.ts` |

Every shipped item has network-free unit tests (`sdk/js/src/test.ts`) and/or Go
tests (`file_transfer_http_test.go`). The merge, the verified-action paths, the
work-stealing distribute, the gate/audit, the service-command builder, the PTY
URL builder, the local-first runner pick, and the durable queue's persistence
are all pinned.

## All 13 proposals shipped

The last three landed as **pure lib additions** (no agent changes — they reuse
endpoints that already existed):

- **P5 remote PTY** → `Machine.shell()` opens the agent's existing `/ws/terminal`
  (creack/pty + gorilla/websocket) over the resolved transport. stdin is sent as
  binary frames (never parsed as control), resize/close as text control frames.
  Direct/tunnel/mesh auth via `?token`; relay needs a custom `WebSocketImpl` that
  forwards the password header.
- **P12 local-first routing** → `agent(prompt, { preferLocal, localRunner })`.
  When the machine carries the auto-seeded `local-inference` tag (P2), dispatch
  routes to the local runner (default `ollama`) so data stays on the box; else it
  falls back to the cloud runner. Pure `pickAgentRunner()` is unit-tested.
- **P13 store-and-forward** → `Fleet.queue(path)` → `CommandQueue`: `enqueue()`
  parks a command (persisted to disk immediately), `flush()` runs the ones whose
  target is now reachable and drops them, keeping the rest for next time.
  Survives restarts; pair with P9 `apply` for exactly-once (raw exec replay is
  at-least-once).

## Design notes

- **Isomorphic**: file methods dynamically `import('node:fs')` so the browser/RN
  bundle stays clean; everything else is fetch-only.
- **Transport-correct**: all actions go through `transportFetch(transport, …)`,
  so relay password headers and tunnel/direct/mesh all work uniformly.
- **Fan-out never aborts on one failure**: `Selection.apply`/`upload`/`schedule`
  capture per-machine outcomes; the streamed `exec`/`agent` merge re-arms each
  machine independently so a slow/dead box never blocks the rest.
- **Safety is opt-in but first-class**: with no `approve`/`onAudit` the lib is a
  plain fan-out; wire them and every mutation is gated + recorded.
