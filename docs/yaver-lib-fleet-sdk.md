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

Every shipped item has network-free unit tests (`sdk/js/src/test.ts`) and/or Go
tests (`file_transfer_http_test.go`). The merge, the verified-action paths, the
work-stealing distribute, the gate/audit, and the service-command builder are
all pinned.

## Remaining (agent-side, larger)

These need deeper work than a lib composition and are intentionally not stubbed:

### P5 — Remote interactive PTY
A live `ssh -t`-style PTY over QUIC so `machine.shell()` streams stdin/stdout/stderr
and an agent can drive a remote shell (answer sudo prompts, REPLs). Needs an agent
WebSocket PTY endpoint (the mobile Terminal already has a WS path to reuse) framed
over the resolved transport. Until then, `exec` covers non-interactive commands.

### P12 — Local-first AI routing
Route agent/inference work to a machine's **local model first** (the `models_*`
Ollama lane), spilling to cloud only on capability/uncertainty — same request
shape either way. Needs an agent-side router that picks local vs cloud per the
machine's reported `edgeProfile`/installed models; the lib would expose
`agent(prompt, { preferLocal: true })`. Strong on-prem/edge story (data stays on
the box). Builds on the existing runner-provider injection + `edgeProfile`.

### P13 — Store-and-forward command queue
Make a command for an **offline** machine durable: queue locally, replay +
re-verify on reconnect, transparent to the caller. Needs the agent's job queue to
be device-targeted and offline-durable (local ring buffer + reconnect replay).
Turns "machine was offline" from an error into a deferred, eventually-consistent
action — essential for flaky edge links. Composes naturally with P9's verified
loop (replay is safe because actions are idempotency-keyed).

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
