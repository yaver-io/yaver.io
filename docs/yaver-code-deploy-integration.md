# yaver code ↔ Yaver Deploy: Integration Plan

> Goal: make `yaver code` (the terminal coding surface) the single
> tool a developer uses to take a phone-sandbox project from
> "scaffolded on phone" through "running on my dev box / Yaver
> Cloud / Cloudflare Workers" — without ever leaving the terminal
> and without juggling a separate `yaver phone` CLI tree.
>
> Status: planning input. Today (2026-04-28) `yaver code` and
> `yaver phone` are sibling command trees that don't know about
> each other. This doc maps the integration in concrete commands
> and identifies the implementation slices.

## The Two Worlds Today

### `yaver code`
- entry: `yaver code` (interactive) or `yaver code "<prompt>"` (one-shot)
- canonical control plane: `code_control.go` (verbs: `attach`, `auth`,
  `set agent|model|repo`, `sessions`, `continue`, `fork`, `dev reload`,
  `deploy mobile|backend|frontend|all`)
- session ownership: every interactive session maps to a parent
  `Task`; child runners (Claude / Codex / OpenCode) execute, Yaver
  owns the conversation
- Phase 1–3 active per `YAVER_CODE_TODO.md`; Phase 4–5 dropped

### `yaver phone`
- entry: `yaver phone {list|export|import|push}` — a **separate** CLI
  tree dispatched from `main.go`, not from `code_control.go`
- runtime: `desktop/agent/phone_backend.go`, `phone_backend_http.go`,
  `phone_data_http.go`, `phone_tokens.go`, `phone_cost.go`,
  `phone_oauth.go`, `phone_escape.go`
- HTTP surface: `/phone/projects/*` (CRUD on schema/auth/seed/data,
  export, receive, tokens, cost-hint, oauth)
- public data routes: `/data/{slug}/{table}[/{id}]` with
  `pp_<slug>_<hex>` Bearer/X-API-Key auth
- mobile UI: `mobile/app/phone-projects.tsx`,
  `mobile/app/phone-project/[slug].tsx`, `…/api-keys.tsx`,
  `…/oauth.tsx`, `…/dns.tsx`
- targets (today): `dev-hw` ✅, `yaver-cloud` ✅,
  `cloudflare-workers` ❌ (specced in `PHONE_EXPORT_PIPELINE.md`
  §Handoff 2.5, not yet implemented)

A developer working on a phone project today can:

1. tap `[Your Dev Machine]` on the phone → project lands on the agent
2. open a terminal → `yaver code` to edit `~/.yaver/phone-projects/<slug>/`
3. but `yaver code` does **not** know it's a phone project — there is
   no awareness of the slug, the schema, the bound deploy target, the
   API tokens, or where to push when changes are ready

That last gap is what this plan closes.

## Target UX

```
$ cd ~/.yaver/phone-projects/todo-app
$ yaver code

todo-app · phone-project · ~/.yaver/phone-projects/todo-app
bound: dev-hw (this machine) · cloudflare-workers (drafted)
runner: claude · model: default

⟩ /phone status
schema:    3 tables (Todo, Tag, User)        last edit  2 min ago
auth:      apple+google                       last edit  1 day ago
seed:      12 rows (3 Todo + 5 Tag + 4 User)
tokens:    2 active (web-prod, mobile-debug)  last used  17 min ago
deploy:
  dev-hw                ready   1.2 MB  bound to this terminal
  yaver-cloud           ready   1.2 MB  cloud.yaver.io
  cloudflare-workers    drafted        deploy-workers --confirm

⟩ /phone schema add Tag.color string
+ schema.yaml updated
+ Tag table now has columns: id, name, color
+ migration plan: ALTER TABLE Tag ADD COLUMN color TEXT
+ run /phone push to apply on dev-hw + yaver-cloud

⟩ make me a UI to filter todos by tag color
[claude] editing app/(tabs)/index.tsx ...
[claude] added <ColorFilter /> bound to /data/todo-app/Tag
[claude] done — open the app to see it

⟩ /phone push
+ dev-hw         migrated + reseeded   18 ms
+ yaver-cloud    migrated + reseeded   320 ms
+ cloudflare     drafted (run /phone deploy workers to push)
+ smoke test     ✓ /data/todo-app/Tag list  3 rows
+ smoke test     ✓ /data/todo-app/Todo list 3 rows

⟩ /phone deploy workers --confirm
+ wrangler.toml synthesized at .yaver/workers/wrangler.toml
+ D1 migration applied
+ worker deployed: https://todo-app-yaver.kivanc.workers.dev
+ smoke test ✓
+ token rotated; new pp_todo-app_*** stored in vault
```

The user never invokes `yaver phone` directly. `yaver code` knows
it's in a phone-project workdir and exposes the verbs inline.

## Command Surface

All under `/phone *` inside `yaver code`, plus `yaver code phone *`
non-interactive equivalents.

| Verb | What | Wraps |
|------|------|-------|
| `/phone status` | one-screen view of schema/auth/seed/tokens/deploys | `GET /phone/projects/get?slug=…` + bound-target probe |
| `/phone schema show` | render `schema.yaml` | local read |
| `/phone schema add <Table.column> <type>` | edit + save + migration plan | local edit, runner-driven where useful |
| `/phone schema codegen` | emit `types.ts` for SDK consumers | new CLI subcommand + `sdk/js` plugin |
| `/phone push [--to dev-hw|yaver-cloud|cloudflare-workers]` | push the current project to one or all bound targets | `POST /phone/projects/receive` per target |
| `/phone deploy workers [--confirm]` | first-time provision Workers + D1 (Cloudflare API) | new `phone_backend_workers.go` + `POST /phone/projects/deploy-workers` |
| `/phone token mint <label> [--cors <origin>] [--rate-limit <rps>]` | mint an API token | `POST /phone/projects/tokens` |
| `/phone token list` / `revoke <id>` | manage tokens | `GET / DELETE /phone/projects/tokens` |
| `/phone test [--target <t>]` | smoke-test all `/data/<slug>/<table>` routes against the SDK contract | new test harness; reuses `sdk/js/src/backend.ts` |
| `/phone bind <target>` | rebind active phone CRUD to a target after push | `POST /phone/projects/bind` (already exists) |
| `/phone export [--no-secrets]` | tar.gz a portable bundle | `GET /phone/projects/export` |
| `/phone migrate <provider>` | guided escape-route migration | `phone_escape.go` + runner-driven wiring |

## Where the Code Goes

| New file | Lives | Purpose |
|----------|-------|---------|
| `desktop/agent/code_phone.go` | agent | dispatcher for `/phone *` slash commands inside `code` |
| `desktop/agent/code_phone_status.go` | agent | aggregates `/phone status` (no new HTTP — composes existing endpoints) |
| `desktop/agent/code_phone_test.go` | agent | unit tests for verb parsing + status aggregation |
| `desktop/agent/phone_backend_workers.go` | agent | Cloudflare API client: `POST /phone/projects/deploy-workers` |
| `desktop/agent/phone_codegen.go` | agent | `schema.yaml` → `types.ts` (and `types.go` later) |
| `cloud/workers/` | cloud | TS source for the Workers runtime that mirrors `phone_data_http.go` over D1 |
| `cli/src/commands/code-phone.ts` | cli | `yaver-cli` mirror so it works without the Go binary present (optional) |

Existing files this plan will touch (adds, no rewrites):

- `desktop/agent/code_cmd.go` — register `/phone` palette entries
- `desktop/agent/code_control.go` — `code phone <verb>` non-interactive
- `desktop/agent/main.go` — keep `yaver phone` as alias; route both
  to the same handlers so muscle memory keeps working
- `mobile/src/lib/phoneProjects.ts` — extend `PhonePushTarget` with
  `{kind: 'cloudflare-workers'}`
- `mobile/app/phone-project/[slug].tsx` — show "drafted / deployed"
  state for the Workers target

## Convex Integration

Convex stays minimal. Phone-project data, schema, tokens, and
runtime CRUD remain P2P. What Convex picks up:

- **deploy ledger** (`projectDeployments` table — new, optional)
  — append-only `{userId, slug, target, ts, bytes, status}` so the
  mobile Deploy screen can show "this month: 120 MB pushed". Today
  this lives nowhere. Cap at 1000 rows per user; rotate.
- **target registry** (`projectTargets` table — new, optional) —
  stores which targets a project is currently bound to (for
  cross-device sync of "my todo-app is on dev-hw + cloudflare").
  Falls back to local agent state if Convex is unreachable.
- **public DNS hint** (already exists — `cfDnsRecords` queries) —
  reused for the Workers target's hostname mapping.

What Convex does **not** get:
- the schema, the auth config, the seed data, the API tokens, the
  CRUD payloads, or anything else covered by the existing P2P privacy
  test (`convex_privacy_test.go`). The deploy ledger row should fail
  the privacy test if it ever leaks any of those.

## Cloudflare Workers Target — Detail

Spec source: `PHONE_EXPORT_PIPELINE.md` §Handoff 2.5 ("Yaver Lite on
Cloudflare Workers"). Estimate stands at 2-3 days.

Slice 1 — agent endpoint:
- `phone_backend_workers.go::handleDeployWorkers(w, r)`
- input: `{slug, cfApiToken, cfAccountId, [workerName], [d1Name]}`
- steps:
  1. read `~/.yaver/phone-projects/<slug>/{schema,auth,seed,data}`
  2. synthesize `wrangler.toml` + `worker.ts` (template + slug-specific
     bindings)
  3. provision a D1 database via Cloudflare API (`POST /accounts/.../d1/database`)
  4. apply schema as D1 migration (single SQL file generated from
     `schema.yaml`)
  5. seed if requested (capped at 1 MB per insert batch — same cap as
     `/phone/projects/receive`)
  6. publish the worker (`PUT /accounts/.../workers/scripts/<name>`)
  7. return `{workerUrl, d1Id, dnsAdvice}`

Slice 2 — Workers runtime:
- `cloud/workers/src/index.ts` — minimal router that mirrors
  `/data/{slug}/{table}[/{id}]` semantics on D1
- shares the `pp_<slug>_<hex>` token format; tokens stored as Worker
  secrets (one secret per project)
- per-token rate limit via Workers' `Cloudflare-RateLimit` headers +
  D1-backed counter (shared with `phone_data_http.go` later)
- CORS allowlist read from a D1 row (`__yaver_meta`) — same shape as
  the per-project allowlist gap called out in `remained.md`

Slice 3 — `yaver code` integration:
- `/phone deploy workers` palette entry (interactive)
- `yaver code phone deploy workers --slug <slug> --confirm` (CI/script)
- Cloudflare API token comes from the existing `dns.tsx` flow (cached
  per-device in AsyncStorage, never persisted on agent)

## Bottlenecks That Block "Perfect Usage" Today

These are the concrete things a user would hit if they tried the
end-to-end flow this week. Listed by leverage, not by effort.

1. **No `yaver code phone *` surface** — every fix in this doc.
2. **No Workers target** — Slice 1+2 above. Without it, the third
   deploy tier is a paper UI button.
3. **No `npx create-yaver-app`** — even if push works, the user has
   to hand-write `createYaverBackendClient()` calls. SDK wedge story
   stalls. Ship a one-screen template that wires the SDK + a Todo
   screen against the project's first table.
4. **No per-token rate limit** — leaked key has no throttle. Add a
   token bucket in `phone_tokens.go` keyed on token hash, default
   600 rpm, configurable per token.
5. **No per-project CORS allowlist** — wildcard echo today. Add a
   text input on the API Keys screen + an allowlist gate in
   `phone_data_http.go::writePhoneDataCORS`.
6. **No deploy ledger** — Convex `projectDeployments` table + agent
   `~/.yaver/deploy-ledger.json` mirror. Surfaces in the
   `/phone status` view.
7. **No deploy rate limit** — 30s min interval per (slug, target),
   429 with `Retry-After`. Server-side, not client.
8. **No schema codegen** — `phone_codegen.go` + `yaver-sdk`
   companion. Without this, every SDK consumer types things by hand.
9. **No `/phone test` smoke runner** — first-class, runs on push,
   exits non-zero on contract drift. Ties `yaver code` to
   `yaver test` (the existing test SDK).
10. **`ExportPhoneProjectWithOptions.NoSecrets` is partial** —
    `oauth-providers.yaml` doesn't auto-strip on third-party promote.
    Gate the `migrate <provider>` flow on `NoSecrets=true` by default.

## Sequencing

This is a four-slice plan. Each slice ships independently and each
unblocks the next.

### Slice A — `/phone status` + token CLI (1-2 days)

Smallest possible cut that makes `yaver code` phone-aware. No new
HTTP — wraps existing endpoints. Lands `code_phone.go`,
`code_phone_status.go`, `code_phone_test.go`.

Done when: opening `yaver code` inside a phone-project workdir
shows the status header automatically and `/phone token mint web-prod`
works.

### Slice B — `/phone push` + `/phone test` (2-3 days)

Wires the existing dev-hw / yaver-cloud push under `/phone push` and
adds a smoke-test runner that exercises the SDK contract end-to-end.
Lands `phone_codegen.go` here too — a pre-test type-generation step.

Done when: a schema edit followed by `/phone push` round-trips
through both targets and `/phone test` exits 0.

### Slice C — Cloudflare Workers target (2-3 days)

Slices 1 + 2 above. New file `phone_backend_workers.go`, new
`cloud/workers/` runtime, mobile UI surface for the third tier.

Done when: `yaver code phone deploy workers --confirm` returns a
working `*.workers.dev` URL and the SDK's smoke test passes against
it.

### Slice D — guardrails (1-2 days)

Per-token rate limit, per-project CORS allowlist, deploy ledger,
deploy rate limit, `NoSecrets=true` default for migrate. These are
small and independent — pick them off as the SDK starts seeing real
traffic.

Done when: a leaked token is throttle-bounded, a misconfigured CORS
returns a real 403, and the mobile Deploy screen shows monthly
volume.

## Out of Scope (kept here so we don't re-litigate)

- Real-time subscriptions (`/data/{slug}/{table}/subscribe`) —
  deferred per `PHONE_EXPORT_PIPELINE.md`. Polling-first.
- Multi-tenant phone projects (one slug, multiple owners) — single
  owner per slug, aligned with the rest of the P2P model.
- Replacing `yaver phone` with a hard rename — both trees stay; the
  `code phone` surface is additive, not destructive. Muscle memory
  for `yaver phone push` keeps working.
- Hosted Yaver Cloud auto-provisioning — manual DNS/TLS today, fine
  for the user persona this targets.

## Definition of Done

`yaver code` is "perfect for usage with yaver-based deploy" when
all of this is true:

- a user inside a phone-project workdir gets a status header for free
- `/phone schema add` followed by `/phone push` round-trips a real
  migration on both dev-hw and yaver-cloud
- `/phone deploy workers --confirm` provisions a working Workers + D1
  target with a valid `*.workers.dev` URL and a rotated token
- `/phone test` exercises the SDK contract against any chosen target
  and exits non-zero on drift
- the mobile Deploy screen surfaces the same project state the
  terminal sees (deploy ledger + bound targets via Convex)
- `yaver phone` (the legacy tree) and `yaver code phone` (the new
  surface) produce identical effects on identical inputs

When these hold, the developer takes a phone-sandbox project from
"made it on the phone" to "live on Cloudflare with a typed SDK" in
one terminal.

---

## Unified Export/Import Layer (the "dual mode" extension)

> A user follow-up reframed Slice A: before adding `yaver code`
> verbs, define the **unified export/import layer** that treats all
> three runtime tiers (mobile sandbox, self-hosted, cloud) as
> interchangeable, AND makes the project's source code a co-equal
> editing surface inside the same git repo. Self-hosted and cloud
> are the same tier — both run the same agent binary — so the layer
> only has to distinguish two tiers: **sandbox-on-phone** and
> **agent-on-machine**.

### The mental model

A phone project has three coexisting representations:

1. **Live runtime** — schema, auth, seed, and CRUD data executing
   in one of the runtime tiers. Source of truth for live data.
2. **Source export** — a git-friendly directory tree of YAML/JSON
   files that fully describes the project. Source of truth for
   schema, auth, and seed when the runtime is offline.
3. **Consuming app code** — the React Native / Web / Node app that
   reads through `yaver-sdk` against the runtime. Lives next to the
   source export in the same repo.

Today (1) is real, (2) is a tarball that can't be diffed, (3) is
created by hand. The unified layer makes (2) a first-class
git-checkable directory and lets the developer move freely between
all three.

### The repo layout (dual mode)

A project in the repo looks like:

```
my-todo-app/
├── .yaver/
│   ├── project.yaml          # slug, name, current target bindings
│   ├── schema.yaml           # tables + columns (canonical)
│   ├── auth.yaml             # apple/google/microsoft providers
│   ├── seed.yaml             # initial rows for empty tables
│   ├── tokens.lock.yaml      # token labels + scopes (NO secrets)
│   └── snapshots/            # optional: live-data dumps for offline work
│       └── 2026-04-28T12-00.jsonl
├── src/                      # consuming app (RN, web, Node)
│   ├── App.tsx
│   └── ...
├── package.json
├── yaver.json                # SDK config (slug, baseUrl resolver)
└── README.md
```

Hard rules:
- `.yaver/` files are **plain text** (YAML/JSON), git-diffable, no
  binary blobs. Snapshots are JSONL, line-per-row.
- **No secrets** in `.yaver/`. API tokens live in
  `~/.yaver/phone-projects/<slug>/tokens.yaml` (per-machine, never
  committed). `tokens.lock.yaml` has labels + scopes only.
- Schema migrations are derived diffs against the runtime, not
  hand-written SQL. The agent computes the migration plan when
  syncing.
- `seed.yaml` is the initial-state contract. `snapshots/` is an
  optional offline-work artifact a developer can opt into and clean
  up before committing.

This shape is what `yaver code phone export <slug>` writes, and
what `yaver code phone import <dir>` reads. The same shape works
for both tiers.

### The two-tier interface

Go side (proposed `desktop/agent/projectstore/`):

```go
type ProjectStore interface {
    // List projects this store knows about.
    List(ctx context.Context) ([]ProjectMeta, error)

    // Read the canonical project files into memory.
    Read(ctx context.Context, slug string) (Project, error)

    // Write a project bundle into the store. If slug exists,
    // ConflictPolicy decides reject / rename / overwrite.
    Write(ctx context.Context, p Project, opts WriteOptions) (ProjectMeta, error)

    // Snapshot the live data (optional — sandbox tier may skip).
    Snapshot(ctx context.Context, slug string, opts SnapshotOptions) (Snapshot, error)

    // Apply a snapshot back onto the live runtime.
    ApplySnapshot(ctx context.Context, slug string, snap Snapshot) error
}

type Project struct {
    Slug        string
    Schema      Schema       // from schema.yaml
    Auth        AuthConfig   // from auth.yaml
    Seed        SeedRows     // from seed.yaml
    Targets     []TargetBind // current bindings (dev-hw, cloud, workers)
    TokenLabels []TokenLabel // labels + scopes only, no secrets
}
```

Implementations:

- `agentProjectStore` — backed by `~/.yaver/phone-projects/<slug>/`
  on a self-hosted or cloud agent box. Same binary runs both tiers.
- `repoProjectStore` — backed by `.yaver/` inside a git repo on
  any developer machine. Pure-text I/O; no SQLite dependency.
- (Phone is special — see "Phone-side mirror" below.)

The runtime tier (agent) keeps SQLite as its source of truth and
projects `.yaver/` files on every write. The repo tier keeps
`.yaver/` as its source of truth and replays into SQLite via
`Write` when imported.

TS side (proposed `mobile/src/lib/projectStore.ts` +
`sdk/js/src/projectStore.ts`):

```ts
export interface ProjectStore {
  list(): Promise<ProjectMeta[]>
  read(slug: string): Promise<Project>
  write(p: Project, opts?: WriteOptions): Promise<ProjectMeta>
  snapshot(slug: string, opts?: SnapshotOptions): Promise<Snapshot>
  applySnapshot(slug: string, snap: Snapshot): Promise<void>
}

export const phoneSandboxStore: ProjectStore   // local SQLite on phone
export const repoStore: (root: string) => ProjectStore  // .yaver/ in a repo
export const agentStore: (baseUrl: string, headers: Record<string,string>) => ProjectStore
```

### Phone-side mirror

The phone has a third implementation: `phoneSandboxStore`, backed
by `expo-sqlite` (same DB the offline phone-project sandbox already
uses). The phone gains:

- `phoneSandboxStore.write(project)` — accept an imported bundle
  from a repo / agent, materialise it into the on-device sandbox
- `phoneSandboxStore.read(slug)` — produce the same `Project`
  shape an agent or repo would
- pull from agent: `phoneSandboxStore.write(await agentStore(baseUrl,headers).read(slug))`
- push to agent: `agentStore(baseUrl,headers).write(await phoneSandboxStore.read(slug))`

Today the phone only has push. The pull direction (agent → phone
sandbox) doesn't exist; this layer adds it.

### Movement matrix (verbs across tiers)

| From → To | Verb | What runs |
|-----------|------|-----------|
| repo → agent (self-hosted or cloud) | `code phone push <slug> --to <target>` | `repoStore.read` → `agentStore.write` |
| agent → repo | `code phone pull <slug> [--repo .]` | `agentStore.read` → `repoStore.write` |
| repo → phone sandbox | mobile UI "Receive into sandbox" | `repoStore.read` → `phoneSandboxStore.write` |
| phone sandbox → repo | mobile UI "Export to repo" + agent receive | `phoneSandboxStore.read` → `repoStore.write` (via agent relay) |
| phone sandbox → agent | mobile UI [Your Dev Machine] / [Yaver Cloud] | `phoneSandboxStore.read` → `agentStore.write` |
| agent → phone sandbox | mobile UI "Pull from agent" (NEW) | `agentStore.read` → `phoneSandboxStore.write` |
| repo ↔ Cloudflare Workers | `code phone deploy workers <slug>` | `repoStore.read` → workers-specific provisioner |

All seven flows reduce to two primitives: `read(slug)` and
`write(project)`. The CLI / UI verbs are sugar on top of those
primitives plus an HTTP transport when the tiers don't live in the
same process.

### Dual-mode editing

A repo with `.yaver/` plus app source under `src/` lets the
developer edit either side and reconcile:

1. **Source-mode edit**: `vim .yaver/schema.yaml` adds a column.
   Run `yaver code phone push` — the agent reads the schema diff,
   computes the migration, applies it on the bound runtime, reseeds
   if requested. The live runtime catches up to the repo.
2. **Runtime-mode edit**: developer edits the schema from the phone
   or via the agent's own UI. A subsequent `yaver code phone pull`
   reads the runtime's current schema and updates `.yaver/`. Then
   `git diff` shows exactly what the runtime change was. Commit.
3. **Both at once** (the dual mode the user asked for): the
   developer can keep iterating on the consuming app code in `src/`
   AND on the schema in `.yaver/` in the same `yaver code` session.
   The session knows it's a dual-mode workdir (presence of
   `.yaver/project.yaml` AND a `package.json` next to it) and the
   `/phone status` header reflects both sides:

```
todo-app · dual-mode (repo + dev-hw)
schema:    3 tables · 1 unpushed change to Tag
app:       react-native · 12 files dirty
runtime:   dev-hw bound · last sync 4 min ago · in sync ✓
```

`/phone push` then becomes a coordinated action: stage schema
changes, run TypeScript codegen against the new schema, and push
the runtime — all before the user makes the next edit.

### File contracts

Each `.yaver/` file has a stable schema so the import side never
has to guess.

- `project.yaml`:
  ```yaml
  slug: todo-app
  name: Todo App
  createdAt: 2026-04-17T...
  defaultRunner: claude
  targets:
    dev-hw:
      bound: true
      lastSync: 2026-04-28T...
    yaver-cloud:
      bound: false
    cloudflare-workers:
      bound: false
  ```
- `schema.yaml` (exists today; add a `version:` field):
  ```yaml
  version: 1
  tables:
    Todo: { id: string!, title: string!, done: bool, tagId: ref<Tag> }
    Tag:  { id: string!, name: string!, color: string }
  ```
- `auth.yaml`:
  ```yaml
  providers: [apple, google]
  apple:    { clientId: ${APPLE_CLIENT_ID} }
  google:   { clientId: ${GOOGLE_CLIENT_ID} }
  ```
- `seed.yaml`:
  ```yaml
  Tag:  [{ id: t1, name: home }, { id: t2, name: work }]
  Todo: [{ id: 1, title: "buy milk", tagId: t1 }]
  ```
- `tokens.lock.yaml` (labels only — secrets in
  `~/.yaver/phone-projects/<slug>/tokens.yaml`, never in repo):
  ```yaml
  tokens:
    - id: tok_a1b2c3
      label: web-prod
      scopes: [read, write]
      cors: ["https://my-app.com"]
    - id: tok_d4e5f6
      label: mobile-debug
      scopes: [read]
  ```

### Where this lives

| New file | Tier | Purpose |
|----------|------|---------|
| `desktop/agent/projectstore/store.go` | agent | `ProjectStore` interface + types |
| `desktop/agent/projectstore/agent.go` | agent | `agentProjectStore` impl (today's `phone_backend.go` refactor) |
| `desktop/agent/projectstore/repo.go` | agent | `repoProjectStore` impl (read/write `.yaver/` directories) |
| `desktop/agent/projectstore/store_test.go` | agent | round-trip tests: agent → repo → agent must yield identical bytes |
| `mobile/src/lib/projectStore.ts` | mobile | `ProjectStore` TS interface + `phoneSandboxStore` |
| `mobile/src/lib/projectStoreAgent.ts` | mobile | `agentStore(baseUrl, headers)` HTTP impl |
| `sdk/js/src/projectStore.ts` | sdk | `repoStore(root)` for use in CI / dev tooling |

Existing files refactored, not rewritten:

- `desktop/agent/phone_backend.go` — `ImportPhoneProject` becomes a
  thin wrapper over `agentProjectStore.Write`. `ExportPhoneProject`
  becomes `agentProjectStore.Read` + tarball serializer.
- `mobile/src/lib/phoneProjects.ts` — `pushPhoneProject` becomes a
  thin wrapper that wires `phoneSandboxStore.read` into
  `agentStore(target).write`.
- `mobile/src/lib/phoneSandboxLocal.ts` — exposes its CRUD as the
  `phoneSandboxStore` ProjectStore implementation; today the API is
  function-shaped, not interface-shaped.

### Sequencing (revised Slice A → D)

The earlier four-slice plan still holds. The unified layer adds two
upstream slices:

- **Slice 0 — define the layer** (1-2 days): write the Go interface,
  add `repoProjectStore`, refactor today's agent code into
  `agentProjectStore` behind it. Round-trip test:
  `agentStore.Read → repoStore.Write → repoStore.Read → agentStore.Write`
  must equal the original on every field.
- **Slice 0.5 — phone-side mirror** (1 day): wrap
  `phoneSandboxLocal.ts` as `phoneSandboxStore`, add the missing
  pull-from-agent direction, expose "Receive into sandbox" in the
  mobile UI.
- Slices A-D (status / token / push / workers / guardrails) then
  layer cleanly on top because they all speak `ProjectStore`.

### Why this matters

- Today the developer cannot edit a phone project offline in their
  editor. The schema is locked inside SQLite on the agent.
- Today the phone is push-only. If a teammate pushes a schema
  change to the agent, the phone has no way to pull it.
- Today consuming app code lives in a separate repo, divorced from
  the schema it depends on. Refactoring across both is manual.
- After Slice 0/0.5: a single git repo holds the schema, the seed,
  the auth config, the token labels, AND the consuming app source.
  `git log` shows real schema diffs. CI can run schema tests.
  Onboarding a teammate is `git clone` + `yaver code phone push
  --to dev-hw`.

This is the foundation that makes `yaver code` "perfect for usage
with yaver-based deploy" — not just a deploy tool, but an editing
surface that respects how developers already work with code.
