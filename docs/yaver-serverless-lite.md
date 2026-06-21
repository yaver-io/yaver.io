# Yaver Serverless Lite

Status: design + first implementation target.

Yaver Serverless Lite is the portable backend runtime for projects that start
in the mobile sandbox and later run on a self-hosted machine or Yaver managed
cloud. It is not a Convex wrapper and sandbox projects do not export/import to
Convex. Convex may remain Yaver's own account/control-plane backend, but user
app data moves through a Yaver-native bundle.

## Product rule

```
mobile sandbox <-> self-hosted yaver serve <-> Yaver managed cloud
```

Every arrow is import/export of the same Yaver Serverless bundle. A project can
start on the phone, land on this Mac, land on a Hetzner-backed Yaver managed
cell, and come back to the phone without changing product identity.

## Normie flow

1. User describes an app in the mobile sandbox.
2. Yaver creates schema, seed data, auth personas, and a simple app plan.
3. User edits data/screens locally on the phone.
4. User taps "Deploy".
5. Yaver exports `my-app.yaver.tgz`.
6. Target receives the bundle:
   - this Mac or another owned box: `yaver serve` + `/phone/projects/receive`
   - Yaver managed cloud: same endpoint on a managed cell
7. Target returns the hosted project URL and data API URL.
8. The same mobile project can be exported back from the target and imported
   into the phone.

The user-facing story is "I made an app on my phone and deployed it", not "I
migrated a sandbox to a vendor backend."

## Bundle contract

Canonical archive extension: `.yaver.tgz`.

Current archive readers also accept `.zip` for OS and coding-agent handoff.

```
<slug>/
  yaver.serverless.yaml
  .yaver/config.yaml
  .yaver/project.yaml
  schema.yaml
  auth.yaml
  seed.json
  app.yaml
  data/app.sqlite
  local.db                  # legacy compatibility while old clients exist
  oauth-providers.yaml      # secret-grade; only included if present
  schema.sql
  README.md
  AGENTS.md
```

`yaver.serverless.yaml` is the placement-neutral source of truth for runtime
metadata. It describes the project, database file, supported placements, and
public routes. It must not contain tokens, hostnames, customer paths, or managed
cloud internals.

`data/app.sqlite` is the canonical SQLite payload for zero-loss moves. `local.db`
is retained for compatibility with existing importers and tests.

## Manifest shape

```yaml
version: 1
runtime: yaver-serverless-lite
name: Todo App
slug: todo-app
database:
  engine: sqlite
  file: data/app.sqlite
  schema: schema.yaml
auth:
  mode: local
  config: auth.yaml
api:
  basePath: /p/todo-app
  dataPath: /p/todo-app/data
  routes:
    - method: GET
      path: /data/todos
    - method: POST
      path: /data/todos
placements:
  - mobile-sandbox
  - self-hosted
  - yaver-managed-cloud
export:
  includesData: true
  secrets: excluded-by-default
```

## Runtime surfaces

The existing implementation already exposes most of the owner and public data
surface. The Lite product should make them project-scoped and documented:

```
GET    /phone/projects/export?slug=<slug>&includeData=true
POST   /phone/projects/receive
GET    /data/<slug>/<table>
POST   /data/<slug>/<table>
GET    /data/<slug>/<table>/<id>
PATCH  /data/<slug>/<table>/<id>
DELETE /data/<slug>/<table>/<id>
```

Future project-scoped surfaces:

```
/p/<slug>/auth/*
/p/<slug>/rpc/<function>
/p/<slug>/http/*
/p/<slug>/jobs/*
/p/<slug>/storage/*
```

## Storage and secrets

Included by default:

- schema
- seed/dev rows
- live SQLite data when `includeData=true`
- app plan
- generated SQL
- non-secret project metadata

Excluded by default:

- Yaver owner session
- managed cloud deploy token
- relay hostnames
- SMTP/API keys
- provider OAuth client secrets unless explicitly doing an encrypted backup
- live end-user sessions
- absolute local paths

`oauth-providers.yaml` is currently included if present for local handoff
compatibility, but this should become an explicit encrypted-backup option before
public managed cloud.

## Self-hosted target

Self-hosted is just another placement of the same bundle:

```bash
yaver serve
yaver phone push my-app --to http://127.0.0.1:18080 --include-data
```

The target writes the project under its own `~/.yaver/phone-projects/<slug>/`
and serves the public data API from its own agent.

For this Mac, the validation loop is:

1. create a phone project
2. export with `includeData=true`
3. receive into an isolated local agent
4. call `/data/<slug>/<table>`
5. export from that target
6. import back into an isolated mobile/source home

## Yaver managed cloud target

For the MVP, Yaver managed cloud can run on the operator's general-purpose
Hetzner server:

```
Caddy/TLS -> yaver serve -> ~/.yaver/phone-projects/<slug>/data/app.sqlite
```

The managed box runs the same binary and the same `/phone/projects/receive`
endpoint as self-hosted. The difference is ownership, TLS, quotas, billing, and
tenant isolation.

Public rollout must replace the current shared-token model with:

- per-user deploy authorization
- per-project receive token
- project quota checks before import
- byte cap on bundle receive
- SQLite backup before overwrite
- audit row in Yaver control plane
- no user app data copied into the control plane

For private dogfood on the operator's Hetzner server, a shared cloud token can
remain temporarily if the target is not exposed as a public multi-tenant
service. Treat it as a beta shortcut, not product architecture.

## Tests that prove the thesis

Minimum green tests:

- bundle contains `yaver.serverless.yaml`
- bundle contains `data/app.sqlite` when `includeData=true`
- import accepts `data/app.sqlite`
- export/import preserves runtime rows
- export from agent A receives into agent B
- public `/data/<slug>/<table>` works on agent B
- export from B imports back into A-compatible storage

Browser/mobile validation:

- run mobile web preview or a Playwright-driven web/mobile surface
- create a sandbox app from prompt
- use GLM only for draft generation, not for hidden state
- deploy to local `yaver serve`
- verify the hosted data URL shows rows
- deploy to managed cloud when target credentials are present

## Non-goals for Lite

- Convex sandbox export/import
- realtime subscriptions
- distributed database
- arbitrary unbounded npm execution in managed cloud
- cloud dashboard clone
- vendor-branded database migrations as the default path

External platforms can still exist as an advanced "switch/export to external
target" feature. They are not the default sandbox lifecycle.
