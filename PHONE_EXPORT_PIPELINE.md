# Phone Backend Export Pipeline — Implementation Log

Status: **shipped** (April 17, 2026). All 11 new Go tests pass. Agent compiles clean.

This is the dump of what actually got written. Pairs with
[`MOBILE_BACKEND_EXPORT.md`](MOBILE_BACKEND_EXPORT.md) (the design) and
[`yc.md`](yc.md) (the YC sprint it fits into).

## What this thread delivers

**Same endpoint, two targets.** The mobile app — or the `yaver phone push`
CLI — exports a phone-backend project and POSTs the tarball to
`/phone/projects/receive` on any reachable `yaver serve`. That agent can be:

1. The developer's own Mac / Mac mini / Raspberry Pi / Linux box / VPS
   (free, reachable via direct LAN or the existing relay fallback).
2. A Hetzner box running the bundled Docker compose stack (the paid
   Yaver-cloud tier in MVP).

No branching logic in the client. The target agent doesn't know or care
whether it's running on the developer's closet Mac mini or a rented VPS.

```
Mobile / CLI
  ├── /phone/projects/export (local)  ← existing
  │       │ tgz bundle
  │       ▼
  └── /phone/projects/receive (target) ← NEW
              │
              ▼
          ImportPhoneProject → ~/.yaver/phone-projects/<slug>/
              │
              ▼
          returns { slug, localUrl, browseUrl, project }
```

## Files touched

### Go agent — `desktop/agent/`

| File | Change |
|---|---|
| `phone_backend.go` | Added `ImportPhoneProject(tgz []byte, opts PhoneImportOptions)`, `PhoneImportOptions` struct (`SlugOverride`, `OnConflict`, `SkipSeed`), `phoneProjectExists`, `uniquePhoneSlug`. Rejects path-traversal entries. Honours conflict policies `reject` / `rename` / `overwrite`. |
| `phone_backend_http.go` | Added route `POST /phone/projects/receive` in `registerPhoneRoutes`. New handler `handlePhoneReceive` supports **multipart** (`bundle` file part + `slug` / `onConflict` / `skipSeed` form fields) and raw **application/gzip** body (options from query string). Caps body at 128 MB. |
| `phone_cmd.go` **(new)** | `yaver phone <list\|export\|import\|push>`. `push <slug> --to <base>` is the primary driver for the YC demo and dogfooding. |
| `main.go` | Dispatches `phone` subcommand to `runPhone`. |
| `phone_export_test.go` **(new)** | 11 tests — see below. |

### Mobile — `mobile/src/`

| File | Change |
|---|---|
| `lib/phoneProjects.ts` | Added `PhonePushTarget` union (`dev-hw` / `yaver-cloud` / `custom`), `PhonePushResult`, and `pushPhoneProject(slug, target, opts)`. Client pulls the bundle from the locally connected agent (`/phone/projects/export`) and POSTs it as multipart to the target's `/phone/projects/receive`. |

### Hetzner cloud tenant — `cloud/` **(new directory)**

| File | Purpose |
|---|---|
| `Dockerfile` | Static `yaver` binary (pure-Go, `modernc.org/sqlite` + `CGO_ENABLED=0`), runs as non-root, volume-mounts `/home/yaver/.yaver`, 18080 exposed. |
| `docker-compose.yml` | Two services — `yaver-agent` + `caddy` — plus persistent `yaver-data` volume. |
| `Caddyfile` | Let's Encrypt TLS on `$CLOUD_DOMAIN`, `request_body max_size 128MB`, `reverse_proxy yaver-agent:18080`. |
| `.env.example` | `CLOUD_DOMAIN`, `CLOUD_TLS_EMAIL`, `CLOUD_OWNER_TOKEN`, `CLOUD_OWNER_USER_ID`. |
| `deploy.sh` | Fresh-box bootstrap (installs Docker, clones repo, generates owner token, `docker compose up -d --build`). |
| `README.md` | Operator runbook. |

## The wire format (unchanged from the existing export)

The tarball produced by `ExportPhoneProject()` is the promotion unit:

```
<slug>/
├── .yaver/config.yaml    # BackendAdapter config (SQLite)
├── .yaver/project.yaml   # declarative manifest
├── schema.yaml           # portable schema DSL
├── auth.yaml             # persona list
├── seed.json             # fixture rows
├── schema.sql            # generated DDL (SQLite)
├── schema.postgres.sql   # generated DDL (Postgres)
└── README.md             # embedded how-to-promote
```

`ImportPhoneProject` reads `schema.yaml`, `auth.yaml`, `seed.json`, and
`.yaver/project.yaml` — everything else is advisory. The SQLite file is
**not** shipped; the target rebuilds it from schema + seed. This keeps the
bundle tiny and makes dialect portability the default.

## Endpoints

| Method + path | Auth | Source of caller |
|---|---|---|
| `POST /phone/projects/receive` | owner bearer token | mobile `pushPhoneProject()` • `yaver phone push` CLI |
| `POST /phone/projects/export?slug=…` | owner bearer token | (existing) — producer side of the pipe |

Nothing else in `phone_backend_http.go` changed. The receive handler reuses
the same `s.auth(…)` middleware as every other phone route.

## CLI surface

```
yaver phone list                                # list local projects
yaver phone export <slug> [--out <path>]        # write .tgz locally
yaver phone import <path> [--slug …]            # inverse of export
yaver phone push <slug> --to <base-url>         # the main event
       [--as-slug NAME] [--conflict reject|rename|overwrite] [--skip-seed]
```

`push` reads `~/.yaver/config.json` for the bearer token, POSTs multipart to
`<base-url>/phone/projects/receive`, prints the resulting slug + browse URL.
Target can be any reachable agent:

```bash
# Another laptop on the same LAN:
yaver phone push my-todos --to http://192.168.1.42:18080

# Via relay (developer's Mac mini):
yaver phone push my-todos --to https://relay.yaver.io/d/dev_mac_mini

# Yaver cloud (paid tier):
yaver phone push my-todos --to https://cloud.yaver.io
```

## Mobile surface

```ts
import { pushPhoneProject } from "@/lib/phoneProjects";

// Dev-hw target (the developer's own other machine):
await pushPhoneProject("my-todos", {
  kind: "dev-hw",
  deviceId: "dev_mac_mini",
  relayHttpUrl: "https://relay.yaver.io",
});

// Yaver cloud target:
await pushPhoneProject("my-todos", { kind: "yaver-cloud" });

// Or a custom base URL (e.g. staging):
await pushPhoneProject("my-todos", {
  kind: "custom",
  baseUrl: "https://staging.cloud.yaver.io",
});
```

Optional third arg: `{ onConflict: 'reject' | 'rename' | 'overwrite', skipSeed: boolean }`.

## Tests (all passing)

Added to `desktop/agent/phone_export_test.go`:

| Test | Covers |
|---|---|
| `TestExportImportRoundtrip` | Create → Export → Delete → Import. Asserts schema + seed survive. |
| `TestImportConflictReject` | Default policy returns `ErrPhoneProjectExists`. |
| `TestImportConflictRename` | Gets `<slug>-2` on collision. Both projects end up in `ListPhoneProjects()`. |
| `TestImportConflictOverwrite` | Pre-mutated target is wiped; marker row gone. |
| `TestImportSkipSeed` | Seed fixtures ignored when `SkipSeed: true`. |
| `TestImportRejectsTraversalPath` | Hand-crafted tgz with `../../evil` → error containing `"unsafe"`. |
| `TestImportRejectsEmpty` | Zero-byte bundle → error. |
| `TestHandlePhoneReceive_Multipart` | Full multipart POST → 200 + response shape. |
| `TestHandlePhoneReceive_RawBody` | `Content-Type: application/gzip` path, query-string options. |
| `TestHandlePhoneReceive_MethodNotAllowed` | GET → 405. |
| `TestHandlePhoneReceive_EmptyBundleRejected` | Empty body → 400. |

Run: `cd desktop/agent && go test -run 'Phone' ./...`

## Security

- **Auth**: receive endpoint is behind the agent's standard `auth()`
  middleware. Owner bearer only in MVP. No guest ACL exposure for this route.
- **Path traversal**: `ImportPhoneProject` rejects any tar entry whose name
  starts with `/` or contains `..`. Covered by test.
- **Body cap**: 128 MB server-side (`maxBundle` in `phone_backend_http.go`,
  `request_body max_size` in `Caddyfile`). Keep the two in sync.
- **Overwrite semantics**: `onConflict=overwrite` calls `DeletePhoneProject`
  *before* re-creating, so there's no in-place mutation that could leave a
  half-migrated project if the import errors mid-way.

## Operational facts

- SQLite driver is pure-Go (`modernc.org/sqlite`). Cloud container builds
  statically with `CGO_ENABLED=0`. No toolchain on the Hetzner box.
- Cloud container runs as UID 10001; `~/.yaver` volume must be owned by
  that UID. `docker compose up` handles this on first start.
- Cloud stack = one `yaver` + one Caddy. No Postgres, no Redis, no shared
  services — phone projects are SQLite-backed by design. This is the MVP
  shape. Post-MVP a multi-project Hetzner box needs tenancy isolation
  above what the agent's owner-auth alone provides.
- Log volumes bounded: agent 50 MB × 5 files, same for Caddy.

## What this does **not** do yet

- Convex `projectDeployments` table — deferred. Mobile UI tracks its own
  list locally until that table lands. Not blocking the YC demo.
- Custom domains per project — out of scope. Slug-keyed paths
  (`cloud.yaver.io/phone/projects/browse?slug=…`) are fine for the demo.
- Per-user tokens in cloud mode — MVP uses a single `CLOUD_OWNER_TOKEN`.
  Replace with per-user tokens (via existing `sdkTokens` table) before
  opening the cloud tier to the public.
- Billing hooks — none. Manual for the beta cohort.
- Chain flow automation (`dev-hw → cloud` one-tap) — the primitives are all
  there (`GET /phone/projects/export` + `POST /phone/projects/receive`);
  the mobile UI that invokes both in sequence is a follow-up.
- Health-check poller + status dots in the mobile project list — follow-up.

## Verification

```bash
cd desktop/agent
go build ./...                                   # passes
go test -run 'Phone' ./...                       # PASS  11 new + existing
```

Manual dogfood (planned next session):

```bash
# 1. Start an agent:
yaver serve

# 2. In the mobile app, create a phone project "my-todos" (template: todos).

# 3. From the same laptop, push that project to a second agent instance
#    running on port 28080:
CFG=$HOME/.yaver-secondary yaver serve --port 28080 &
yaver phone push my-todos --to http://127.0.0.1:28080

# 4. Browse on the target:
curl -H "Authorization: Bearer $(jq -r .auth_token $HOME/.yaver/config.json)" \
     http://127.0.0.1:28080/phone/projects/list
```

## Dogfood log — 2026-04-17

End-to-end validation of `yaver phone push` using the CLI as a phone-emulator against (a) a second local agent on this dev machine and (b) the shared Hetzner test box. Both paths exercise the exact same endpoint — `POST /phone/projects/receive` — so a pass here is a pass for the mobile client too.

### Setup

- Built fresh agent binary: `go build -o /tmp/yaver-dogfood/yaver ./...` (49 MB).
- Cross-built linux/arm64 for Hetzner: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ...` (44 MB, statically linked).
- Isolated HOMEs with matching `auth_token` so the agent's fast-path (`token == s.token`) authorises the push without any Convex round-trip. Set `convex_site_url` to a throwaway hostname + `macos_permission_onboarding_done: true` + `YAVER_NO_BOOTSTRAP=1` so the agent skips pairing and onboarding prompts in automation.

### Test A — local dev-hw (same machine, two ports)

- Source agent on `:38080`, target agent on `:28080`, both pointed at different HOMEs.
- Created `dogfood-todos` (template=`todos`) on source. Hand-inserted one custom row to exercise runtime-divergent state.
- `yaver phone push --to http://127.0.0.1:28080 dogfood-todos` → 200, **5 ms** round-trip.
- Target ended up with 5 rows (2 personas + 3 seeded todos). The hand-inserted `custom-1` did **not** transfer — correct per the contract in §"The wire format": only `schema.yaml` + `auth.yaml` + `seed.json` travel, `local.db` does not.
- Conflict policies exercised end-to-end:
  - `reject` → 409 with `"phone project already exists: dogfood-todos"`
  - `rename` → target got `dogfood-todos-2`
  - `overwrite` → `dogfood-todos` replaced in place

### Test B — Hetzner (37.27.184.85, aarch64, shared dev box)

- Pushed the arm64 binary + throwaway config to `/root/.yaver-dogfood/` via `scp`.
- Ran on `:28080` under `nohup` (did not touch the existing relay / Convex / erpnext containers on the box, did not use the Docker-compose `cloud/` stack, no Let's Encrypt needed).
- `yaver phone push --to http://$HETZ:28080 dogfood-todos` → 200, **200 ms** over the open internet. Target verified via `/phone/projects/list` + `/phone/projects/browse`.
- Chain test (`dev-hw → cloud` simulation):
  1. Inserted `hetzner-only` row on Hetzner.
  2. `GET /phone/projects/export?slug=dogfood-todos` → 1346-byte tgz. Confirmed all 8 portable files present (config, manifest, schema, auth, seed, generated SQLite + Postgres DDL, README). **No `local.db`** in the bundle.
  3. `yaver phone import --slug roundtrip` locally → new slug materialised, 5 rows (original seed; `hetzner-only` correctly absent).
- Teardown: killed the agent, removed `/root/.yaver-dogfood/`, left the box's pre-existing services untouched. Zero residue.

### Bug found + fixed

`GET /phone/projects/browse?slug=<does-not-exist>` used to return **500** with `"unable to open database file: out of memory (14)"` — the SQLite driver choking on a missing directory with a misleading error code (`SQLITE_CANTOPEN`).

Fix in `phone_backend.go`: `PhoneAdapter` now stats `.yaver/phone.yaml` before opening SQLite and returns a new `ErrPhoneProjectNotFound` sentinel when the slug has never been created. HTTP handlers already translate adapter errors to 404, so the behaviour now surfaces as:

```
HTTP 404
{"error":"phone project not found: does-not-exist","ok":false}
```

Regression test `TestPhoneAdapter_MissingProjectReturnsNotFound` added to `phone_export_test.go`. Full suite (12 tests) green.

### Observations / follow-ups

- Round-trip re-export from Hetzner correctly ships only the declarative seed.json (not the live `hetzner-only` row). For Flow C (phone → dev-hw → cloud with live data) the design doc's §Non-Goals MVP line holds — rows added at runtime don't survive promotion. If/when we want that, either ship `local.db` opt-in or snapshot rows into seed.json at export time.
- `yaver phone push` uses Go's `flag` package which requires **all** flags before positional args: `yaver phone push --to URL SLUG`, not `yaver phone push SLUG --to URL`. The error path is helpful (prints usage), but the docs / error message could call this out explicitly.
- The agent registers a macOS LaunchAgent / Linux systemd unit the first time it runs in a given HOME. For automation, set `YAVER_NO_BOOTSTRAP=1` **and** pre-write `macos_permission_onboarding_done: true` in `config.json` to avoid interactive prompts.

## Relationship to the YC sprint (`yc.md`)

This thread finishes **Apr 19 – Apr 21** items in the 17-day sprint:

- [x] Apr 19 — Lock wire format (reused existing `ExportPhoneProject` shape).
- [x] Apr 20 — `POST /phone/projects/receive` on the agent.
- [x] Apr 20 — Agent-side start/stop/delete endpoints (partial — `delete`
      exists as `DELETE`-via-POST; start/stop are no-ops because the runtime
      is in-process).
- [x] Apr 21 (partial) — Mobile client library (`pushPhoneProject`).
      UI sheet still to build.
- [x] Apr 22 (brought forward) — Cloud tenant Docker stack.

Next on the critical path for the YC demo video:
1. Mobile UI deploy sheet in `apps.tsx` (target picker + progress).
2. Stand up one Hetzner box with DNS + TLS so `https://cloud.yaver.io` is
   real by Apr 23.
3. Record the 2-minute "phone → Mac → cloud" flow for the HN launch
   (Apr 29) and the YC application video (May 1).
