# Mobile Backend Export Pipeline

Companion doc to [`MOBILE_WORKER.md`](MOBILE_WORKER.md) (Parallel Product Track: Mini Backend Inside Yaver) and [`yc.md`](yc.md) (17-day YC application sprint). This file owns the **export/deploy pipeline** — how a project created inside the mobile app's mini-backend is promoted to (a) the developer's own dev hardware, or (b) Yaver's managed cloud, or (c) chained (phone → dev hw → cloud).

The parallel thread owns the **runtime** (SQLite + schema + CRUD inside the mobile app). This thread owns everything that happens after the user taps **Deploy**.

## Business Model Recap (why this pipeline exists)

| Target | Paid? | Infrastructure cost to Yaver |
|---|---|---|
| Phone-only (local) | Free | Zero — runs in the mobile app's sandbox |
| Developer's own Mac / Mac mini / Pi / Linux / VPS | Free | Zero — runs inside the developer's existing `yaver serve` agent |
| Developer's hw via Yaver **managed relay** | $10/mo | Shared relay server cost (already offered) |
| **Yaver cloud** (Hetzner tenant) | $19/mo single project, $49/mo unlimited | One Hetzner box per ~50 projects |

The developer always has a free path. Paid tiers are pure convenience — no AWS bill until someone actually pays Yaver.

## What Already Exists (do not rebuild)

Confirmed present in the repo (April 2026 codebase — verify before depending on specific functions):

- `desktop/agent/phone_backend_http.go` — routes `/phone/projects/{list,create,get,schema,export,promote,…}`. `handlePhoneExport()` produces a `.tgz`. `handlePhonePromote()` wraps the SwitchEngine migration planner.
- `desktop/agent/phone_backend.go` — SQLite + schema + CRUD core of the mini-backend on the agent side.
- `desktop/agent/cloud_deploy.go` — `CloudDeployManager` provisions Hetzner VPS, templates Docker+Caddy, supports Postgres/Redis side services; currently deploys from a `workDir`, not from a tarball.
- `relay/server.go` — `/d/{deviceId}/...` proxy is live and exercised; any new agent endpoint is reachable from mobile for free.
- `backend/convex/schema.ts::userProjects` — already stores `userId`, `deviceId`, `slug`, `status`.
- `mobile/src/lib/phoneProjects.ts` — TypeScript types mirror the Go phone-backend domain.

## What's Missing (this thread builds)

| # | Gap | Lives in |
|---|---|---|
| 1 | Target agent accepts a `.tgz` uploaded from another device, extracts, starts its runtime | `desktop/agent/projects_receive.go` (new) |
| 2 | `CloudDeployManager.DeployFromTarball()` — same thing for Yaver cloud | extend `desktop/agent/cloud_deploy.go` |
| 3 | Phone→dev-hw deployment flow with progress streaming | `mobile/src/lib/deploy.ts` (new) |
| 4 | Phone→cloud deployment flow | shared code path with (3) |
| 5 | Chained promotion (dev-hw → cloud) from the mobile UI | same |
| 6 | `projectDeployments` table + mutations for multi-target tracking | `backend/convex/schema.ts`, `backend/convex/deployments.ts` |
| 7 | Mobile UI: target picker + one-tap deploy + status | `mobile/app/(tabs)/apps.tsx` deploy sheet |
| 8 | Cloud tenant mode: multi-project isolation on one Hetzner box | `desktop/agent/cloud_tenant.go` (new, post-MVP) |

## Wire Format (the `.tgz` package)

`handlePhoneExport()` already produces a tarball. We standardize what's inside so every target (dev hw, cloud, future Supabase/Convex promote) reads the same contract.

```
project.tgz
├── yaver.project.json   # manifest (required)
├── schema.json          # collections, fields, indices (required)
├── rules.json           # auth personas + access rules (optional, MVP punts)
├── fixtures.json        # seed data (optional)
├── data.sqlite          # current snapshot for live migrations (optional)
└── functions/           # custom queries/mutations (v2 — NOT in MVP)
```

`yaver.project.json` shape (MVP):

```json
{
  "schemaVersion": 1,
  "projectId": "prj_abc123",
  "slug": "my-todo-app",
  "runtimeMinVersion": "1.0.0",
  "createdOnDeviceId": "phone_xyz",
  "origin": "phone",
  "targets": [
    { "kind": "dev-hw", "deviceId": "dev_mac_mini", "assignedAt": "..." },
    { "kind": "yaver-cloud", "cloudMachineId": "cm_hetzner_01", "url": "..." }
  ]
}
```

Target list is informational — the actual source of truth is the `projectDeployments` Convex table. The manifest travels with the package so a target can announce itself back to the user's other devices.

## Endpoints

### New on every `yaver serve` agent (dev-hw targets)

- `POST /projects/receive` — multipart upload of `.tgz`. Validates manifest + schema-version compatibility. Extracts to `~/.yaver/projects/{projectId}/`. Starts the phone-backend runtime against that directory. Returns `{ projectId, status, localUrl, relayUrl }`.
- `GET  /projects/{projectId}/status` — `{ status, pid, lastHealthCheck, url }`.
- `POST /projects/{projectId}/stop` — stop runtime, leave files.
- `POST /projects/{projectId}/start` — start runtime from stored files (resume after reboot).
- `DELETE /projects/{projectId}` — stop + archive for 7 days, then purge.
- `GET  /projects/{projectId}/export` — re-export the current running state as a `.tgz` (so a user can chain: dev-hw → cloud later, with any data the running app accumulated).

All authed by the existing agent auth middleware — owner-only for write, guests can be granted via existing guest ACL.

### New on Yaver cloud (Hetzner tenant)

- `POST /cloud/projects/receive` — same multipart upload, but also takes a `cloudMachineId` and a user's cloud-tier subscription check. Internally calls `CloudDeployManager.DeployFromTarball()`.
- `GET  /cloud/projects/{projectId}/{status,logs,stop,start}` — parallel to dev-hw endpoints, behind Yaver's cloud auth.
- Custom URL: `{slug}-{projectId:8}.yaver.cloud` (one-time DNS wildcard on our Hetzner box, handled by Caddy).

### Mobile client API (`mobile/src/lib/deploy.ts`)

```ts
type DeployTarget =
  | { kind: 'dev-hw'; deviceId: string }
  | { kind: 'yaver-cloud' }
  | { kind: 'chain'; via: string /* deviceId */ };

deployProject(projectSlug: string, target: DeployTarget): Observable<DeployEvent>
```

Streams `{ phase: 'packaging' | 'uploading' | 'starting' | 'ready' | 'error', progress, url? }` via SSE.

## Deployment Flows

### Flow A — Phone to the developer's own hardware (free, MVP target)

```
Mobile app                  Agent on dev hw (Pi/Mac/Linux/VPS)
┌────────────┐  POST /d/{deviceId}/projects/receive
│ tap Deploy │──────────────► multipart upload (.tgz)
│  → Dev HW  │              │
└────────────┘              ▼
                          [extract to ~/.yaver/projects/{id}]
                          [start mini-backend runtime]
                          [open port via existing agent HTTP]
┌────────────┐              │
│ poll status│◄─ SSE events─┤ { phase: "ready", url: "..." }
└────────────┘              │
                          [url = http://lan-ip:PORT or
                                 https://relay.yaver.io/d/{deviceId}/p/{projectId}]
```

Reachability reuses the existing two-layer strategy: direct LAN when the phone can see the dev machine, relay otherwise. No new transport.

### Flow B — Phone directly to Yaver cloud ($19/mo tier)

```
Mobile app                    Yaver cloud (Hetzner)
┌────────────┐  POST https://cloud.yaver.io/projects/receive
│ tap Deploy │────────────────► (auth = user's Yaver token + cloud sub check)
│  → Cloud   │                │
└────────────┘                ▼
                            [CloudDeployManager.DeployFromTarball()]
                            [provisions or reuses Hetzner tenant slot]
                            [writes Caddy route, spins up runtime]
┌────────────┐                │
│ poll status│◄─ SSE events───┤ { phase: "ready", url: "my-todo-abc123.yaver.cloud" }
└────────────┘                │
                            [records projectDeployments row in Convex]
```

### Flow C — Chain: phone → dev hw → cloud

For developers who want to iterate free, then ship paid. This is the promote story from `MOBILE_WORKER.md` §Portability Contract.

```
Phone ── Deploy → Dev HW (free, dogfood for days)
                        │
                        │  later, user taps "Promote to Yaver Cloud"
                        ▼
Mobile ── GET /d/{deviceId}/projects/{projectId}/export (fresh .tgz w/ live data)
       ── POST https://cloud.yaver.io/projects/receive
                        ▼
                      Yaver cloud runs the project
                        │
                        │  mobile records both targets in projectDeployments
                        │  user's UI now shows: "Running on Mac mini + Cloud"
```

The **dev-hw export endpoint** is what makes this chain work — and it's also how a user backs up or migrates between their own machines.

## Convex Schema Addition

```ts
// backend/convex/schema.ts
projectDeployments: defineTable({
  userId: v.id('users'),
  projectId: v.string(),           // stable across deployments
  slug: v.string(),
  targetKind: v.union(
    v.literal('dev-hw'),
    v.literal('yaver-cloud'),
  ),
  deviceId: v.optional(v.id('devices')),        // set when dev-hw
  cloudMachineId: v.optional(v.id('cloudMachines')), // set when cloud
  status: v.union(
    v.literal('deploying'),
    v.literal('ready'),
    v.literal('stopped'),
    v.literal('error'),
    v.literal('archived'),
  ),
  url: v.string(),
  schemaVersion: v.number(),
  deployedAt: v.number(),
  lastHealthCheck: v.optional(v.number()),
  error: v.optional(v.string()),
})
  .index('by_user', ['userId'])
  .index('by_project', ['userId', 'projectId'])
```

No payloads, no schemas, no data land in Convex — only deployment metadata. Respects the privacy contract in `CLAUDE.md`.

## MVP Scope (fits the 17-day sprint)

Ship in this order. Each item is a day or less once the parallel thread has the runtime working.

1. **Apr 19** — Lock wire format. Update `handlePhoneExport()` to emit `yaver.project.json` per the contract above.
2. **Apr 20** — `POST /projects/receive` on the agent. Multipart upload → extract → start runtime → return URL. Owner-auth only.
3. **Apr 20** — Agent-side status/stop/start/delete endpoints. All call through to a `ProjectRunner` struct that wraps the phone-backend runtime.
4. **Apr 21** — Mobile `deployProject()` client + target picker sheet in `apps.tsx`. Dev-hw target only in v1 (cloud button shows "Coming soon" until Apr 22).
5. **Apr 22** — Yaver cloud target: `POST /cloud/projects/receive` on one Hetzner box. Reuses `CloudDeployManager`. Custom subdomain via Caddy wildcard.
6. **Apr 23** — Chain flow: mobile pulls `.tgz` from dev-hw, posts to cloud. Dogfood end-to-end — build a real app on phone, run on Mac mini, promote to cloud.
7. **Apr 24** — `projectDeployments` Convex table + health-check cron. Mobile UI shows target list per project with status dots.
8. **Apr 25** — Beta user dry-run. Fix whatever breaks.

## Explicit Non-Goals for MVP

- **Custom functions / server code** — schema + CRUD + rules only. v2.
- **Postgres migration** — SQLite everywhere. v2.
- **Multi-tenant cloud hardening** — single Hetzner box, manual billing for beta, 50-project cap. Automate post-launch.
- **Rollback / versioning** — re-deploy is destructive in MVP. 7-day archive is enough for undo.
- **Zero-downtime migrations** — stop old runtime, swap files, start new. Single-digit-second blip is fine for phone-originated apps.
- **Schema evolution on live data** — MVP rejects deploys that would break the running schema. User must redeploy with `--force` and accept data loss (or export first).

## Security

- Tarballs are signed by the originating agent's device key. Target agent verifies the signature against the user's Convex-registered device list before extracting.
- Each deployed project gets a scoped API token, minted on deploy, returned to the mobile client, never re-surfaced. Mobile stores it alongside the project row.
- Cloud tenant isolation: each project runs as its own OS user on the Hetzner box, with its own SQLite file; project-scoped API tokens prevent cross-project access. (Multi-tenant containerization is post-MVP — single Hetzner box + Linux user isolation is enough for beta.)
- Archives and deleted projects are purged from disk after 7 days; Convex row moves to `status: archived` immediately.

## Integration Contract With the Parallel Thread

The other thread is building the runtime. This thread agrees not to depend on:

- Any runtime internal structure beyond the `yaver.project.json` + `schema.json` + SQLite file.
- Any specific runtime startup command — we'll call a `ProjectRunner.Start(dir)` interface that the other thread implements.

The other thread agrees not to depend on:

- Any transport assumption. The runtime must accept a directory and an optional bound port, and expose an HTTP handler — wrapping in HTTP/P2P is this thread's problem.

Exact Go interface (proposed — belongs in `desktop/agent/project_runtime.go`):

```go
type ProjectRuntime interface {
    Start(ctx context.Context, projectDir string) (addr string, err error)
    Stop(ctx context.Context) error
    Health(ctx context.Context) (Health, error)
    ExportState(ctx context.Context, outPath string) error
}
```

If the parallel thread hits this interface, the export pipeline plugs in with no further coordination.

## Open Questions (resolve before Apr 20)

1. **Schema version bumps** — when the runtime changes, how does the target know whether to accept an old `.tgz`? Proposal: target rejects if `schemaVersion > target.runtimeMaxSchema`, else proceeds. Runtime is responsible for backward compat within a major.
2. **Port allocation** — do we bind each project to a random high port and proxy through the agent, or run them all on Unix sockets? Proposal: Unix socket behind the agent, cleaner on macOS firewall prompts.
3. **Cloud sub verification** — where does the mobile client check the cloud tier before attempting `POST /cloud/projects/receive`? Proposal: Convex query `isCloudTierActive`, agent validates again server-side as defense-in-depth.
4. **Billing metering** — does MVP meter usage at all? Proposal: no. Flat $19/mo, 50 project cap per box. Meter in v2 when someone actually asks.

## Success Criteria

By the end of Apr 23 the following should all work on video in under 5 minutes:

- [ ] Create a project in the mobile app on the phone.
- [ ] Tap **Deploy → Your Mac mini**. Watch progress. Project URL opens, data loads.
- [ ] Add rows via the phone. Confirm they appear when querying the Mac mini's URL directly.
- [ ] Tap **Promote to Yaver Cloud**. Watch progress. New URL opens, same data, still working.
- [ ] Mobile UI shows both targets with green dots.

That video is what goes on HN (Apr 29) and into the YC demo video (May 1). Everything else in this document is in service of making that video undeniable.
