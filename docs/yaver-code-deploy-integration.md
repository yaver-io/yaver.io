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
