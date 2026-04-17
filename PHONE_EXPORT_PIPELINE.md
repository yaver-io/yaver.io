# Phone Backend Export Pipeline — Implementation Log

Status: **shipped** (April 17, 2026). Core export / receive / promote flow is in place. Follow-up work is mostly polish, monorepo bootstrap, and deploy ergonomics.

This is the dump of what actually got written. Pairs with
[`MOBILE_BACKEND_EXPORT.md`](MOBILE_BACKEND_EXPORT.md) (the design) and
[`yc.md`](yc.md) (the YC sprint it fits into).

## What this thread delivers

**Same endpoint, multiple targets.** The mobile app — or the `yaver phone push`
CLI — exports a phone-backend project and POSTs the tarball to
`/phone/projects/receive` on any reachable `yaver serve`. That agent can be:

1. The developer's own Mac / Mac mini / Raspberry Pi / Linux box / VPS
   (free, reachable via direct LAN or the existing relay fallback).
2. A Hetzner box running the bundled Docker compose stack (the paid
   Yaver-cloud tier in MVP).
3. The developer's own cloud box running `yaver serve` directly or via the
   containerized export scaffold.

This is the intended continuum for the phone-first backend:

`phone sandbox -> dev machine / own host -> Yaver Cloud`

Supabase, Convex, Neon, Turso, Firebase, and similar systems remain escape
hatches. They matter for trust, not because they replace the default Yaver path.

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

## The wire format

The tarball produced by `ExportPhoneProject()` is the promotion unit:

```
<slug>/
├── .yaver/config.yaml    # BackendAdapter config (SQLite)
├── .yaver/project.yaml   # declarative manifest
├── schema.yaml           # portable schema DSL
├── auth.yaml             # persona list
├── seed.json             # fixture rows
├── local.db              # optional live SQLite rows when include-data is on
├── schema.sql            # generated DDL (SQLite)
├── schema.postgres.sql   # generated DDL (Postgres)
├── Dockerfile            # optional containerized Yaver-lite runtime
├── docker-compose.yml    # optional compose scaffold
├── .env.example          # optional deploy scaffold
├── .dockerignore         # optional container hygiene
├── .gitignore            # exported-project git hygiene
└── README.md             # embedded how-to-promote
```

`ImportPhoneProject` reads `schema.yaml`, `auth.yaml`, `seed.json`, and
`.yaver/project.yaml` — everything else is advisory. By default the target
rebuilds from schema + seed. When `include-data` is requested, `local.db`
is bundled and restored verbatim so runtime rows survive promotion.

When `containerize` is requested, the bundle also includes a Docker / compose
scaffold so the same project can be started on the developer's own remote box
or fed into the Yaver Cloud runtime with less manual setup.

## Endpoints

| Method + path | Auth | Source of caller |
|---|---|---|
| `POST /phone/projects/receive` | owner bearer token | mobile `pushPhoneProject()` • `yaver phone push` CLI |
| `POST /phone/projects/export?slug=…[&includeData=true]` | owner bearer token | (existing) — producer side of the pipe |

Nothing else in `phone_backend_http.go` changed. The receive handler reuses
the same `s.auth(…)` middleware as every other phone route.

## CLI surface

```
yaver phone list                                # list local projects
yaver phone export [--out <path>] [--include-data] [--containerize] <slug>
yaver phone import <path> [--slug …]            # inverse of export
yaver phone push --to <base-url>                # the main event
       [--as-slug NAME] [--conflict reject|rename|overwrite] [--skip-seed] [--include-data] [--containerize] <slug>
```

`push` reads `~/.yaver/config.json` for the bearer token, POSTs multipart to
`<base-url>/phone/projects/receive`, prints the resulting slug + browse URL.
Target can be any reachable agent:

```bash
# Another laptop on the same LAN:
yaver phone push --to http://192.168.1.42:18080 my-todos

# Via relay (developer's Mac mini):
yaver phone push --to https://relay.yaver.io/d/dev_mac_mini my-todos

# Your own cloud box with Docker scaffold included:
yaver phone push --to https://my-box.example.com --include-data --containerize my-todos

# Yaver cloud (paid tier):
yaver phone push --to https://cloud.yaver.io my-todos
```

If you want the exported project on disk first instead of pushing directly:

```bash
yaver phone export --include-data --containerize --out my-todos.tgz my-todos
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

## Product position

This thread is part of the phone-first full-stack story:

- build from the phone
- keep the first backend tier on the phone
- promote to your own machine or your own cloud
- use Yaver Cloud when you want managed hosting
- keep third-party migrations available as escape routes

The still-missing piece for the ideal vibe-coding story is one-tap monorepo
bootstrap. The transport, runtime continuity, and containerized/exported backend
path already exist underneath it.

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

### Test B — Hetzner (***REMOVED***, aarch64, shared dev box)

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

## Dogfood log — 2026-04-17 (pm) — wedge-demo two-button deploy

Second dogfood against the same two targets as the morning run — this time driving the new `[Your Dev Machine]` / `[Yaver Cloud]` deploy surface in mobile + web (yc.md §Wedge Demo Apr 21). CLI still the emulator (`yaver phone push`) since iOS hot-iterate is a 20-minute loop.

### Flow shipped end-to-end

```
[1] Phone creates 'vibe-todos' (template: todos)
      created in 15 ms
[2] Phone adds 2 rows (simulating user CRUD)
      source now has 5 todos
[3] Tap [Your Dev Machine] → push to :28080
      1349-byte bundle, 17 ms total latency
      Mac target now has 3 todos (schema+seed; runtime rows NOT shipped per contract)
[4] Tap [Yaver Cloud] → push to Hetzner
      1349-byte bundle, 196 ms total latency
      Hetzner now has 3 todos
```

Both targets reachable and running the same project after a clean 2-tap flow. 17 ms to the Mac is fast enough to feel instant in a demo recording; 196 ms to Hetzner is the baseline RTT to eu-central + a few round-trips of the upload. Both are well under the 3-minute budget `yc.md §HN Launch Playbook` requires for the HN-launch demo.

### What the new UI looks like

Mobile (`mobile/app/phone-project/[slug].tsx`) and web (`web/components/dashboard/PhoneProjectsView.tsx`) now both show a "Deploy" section with exactly two primary affordances:

- **Your Dev Machine** — filled indigo button. Auto-targets the first online, owner-owned, non-mobile device (i.e. an agent running on a Mac / Pi / Linux box). Long-press (mobile) / dropdown (web) to pick a different sibling device. Goes through the active relay URL, which is how the phone's existing HTTP path already routes to the Mac. Cost: free.
- **Yaver Cloud** — outlined button to `https://cloud.yaver.io`. Uses the same `POST /phone/projects/receive` endpoint; the Hetzner box runs a stock `yaver serve`. Cost: $19/mo tier per yc.md §Business Model.

The 6-target switch-engine picker (SQLite, Turso, Postgres, Supabase, Neon, Convex) is hidden behind a collapsible "Advanced — promote to a switch-engine target" toggle so the wedge stays visually simple for the demo recording.

### Follow-ups from this run

- **Live-data carry-over is opt-in.** `vibe-1` + `vibe-2` rows added on the "phone" don't survive the promote. Matches the documented contract (`PHONE_EXPORT_PIPELINE.md §"The wire format"` — SQLite file not shipped). For the YC demo we'll want an **`--include-data` flag** on `phone push` / a checkbox in the UI so "I added a todo, now it's on my cloud too" is demonstrable. Small: bundle the `.db` file alongside seed.json in the tarball when requested; receiver copies it verbatim instead of rebuilding from seed.
- **Token hygiene for CLI pushes.** `yaver phone push` reads the caller's `~/.yaver/config.json` for the bearer, so pushing to a cloud agent owned by a *different account* requires swapping the config. Mobile / web go through the same relay with the user's own token, so this is only a concern for the CLI path.
- **macOS LaunchAgent / Linux systemd auto-registration** fires whenever the agent starts in a new HOME, which adds up over many CI / dogfood runs. Not hazardous (the units point at specific binary paths), but worth a `--no-autostart` flag for automation.

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
1. Mobile UI deploy sheet in `apps.tsx` (target picker + progress). **[Shipped 2026-04-17 — see Dogfood log above]**
2. Stand up one Hetzner box with DNS + TLS so `https://cloud.yaver.io` is
   real by Apr 23.
3. Record the 2-minute "phone → Mac → cloud" flow for the HN launch
   (Apr 29) and the YC application video (May 1).

## Handoff notes for Codex — 2026-04-17 EOD

Context: the mobile → Mac and mobile → Hetzner paths work end-to-end. Wire format is locked, endpoints are stable, tests cover both sides. This section lists the remaining pieces with enough specificity that Codex can pick them up without rediscovering the architecture. Each item is scoped to ship in ≤ 1 day.

### 1. Demo-blockers (ship before Apr 29 HN launch)

#### 1.1 `--include-data` flag on the export/push path — ✅ SHIPPED (with one 10-min polish left)

Landed in a parallel thread: `ExportPhoneProjectWithOptions` + `PhoneExportOptions{IncludeData: true}` in `phone_backend.go`, `TestExportIncludeDataBundlesSQLiteFile` + `TestImportWithIncludedDataPreservesRuntimeRows` in `phone_export_test.go`, and the `includeData` query-string branch in `handlePhoneExport`. Runtime rows now survive the promote hop when opt-in.

**Remaining:** `mobile/src/lib/phoneProjects.ts :: pushPhoneProject` does not yet forward `includeData` to `/phone/projects/export`. Add an `opts.includeData?: boolean` param, append `&includeData=true` when set, and expose a checkbox in the mobile Deploy UI. Same for `web/lib/agent-client.ts :: pushPhoneProject`.

#### 1.2 OAuth providers per phone-project — ✅ SHIPPED

User asked for Apple / Google / Microsoft OAuth setup guidance from the phone. Shipped as:
- `desktop/agent/phone_oauth.go` — `PhoneOAuthConfig` (apple / google / microsoft sub-structs), per-project `oauth-providers.yaml` at 0600 perms, validated `GET`+`POST /phone/projects/oauth`. Partial patches (nil = leave alone, empty struct = clear). Per-provider validators (Apple Team/Key IDs are 10 upper-alnum, Services ID is reverse-DNS, private key is PEM; Google Client ID ends with `.apps.googleusercontent.com`; Microsoft tenant is GUID or common/organizations/consumers).
- `phone_backend.go :: ExportPhoneProject` — bundles `oauth-providers.yaml` into the tgz so the config travels with the push.
- `mobile/app/phone-project/oauth.tsx` — three collapsible provider cards. Each shows the console URL (deep link to developer.apple.com / console.cloud.google.com / portal.azure.com), numbered step-by-step, paste-back inputs with format hints + `secureTextEntry` on secrets, per-provider Save. Green dot when configured.
- `mobile/src/lib/phoneProjects.ts` — `getPhoneOAuth` / `setPhoneOAuth` helpers + type mirrors.
- `phone_oauth_test.go` — 11 tests covering all of the above.

**Remaining:** a `NoSecrets` flag on `ExportPhoneProjectWithOptions` so the switch-engine promote path (to Supabase, Convex, Firebase, etc. — third parties we don't want leaking secrets into) can strip `oauth-providers.yaml` before the tgz goes out. Add to the existing `PhoneExportOptions` struct; gate the `os.ReadFile(phoneOAuthPath(p.Dir))` branch in `ExportPhoneProject` on `!opts.NoSecrets`.

#### 1.3 Voice/text prompt → project scaffold

**Why:** yc.md Apr 20 core deliverable — "Voice/text prompt on phone produces a running RN project on the dev Mac." Today the user manually picks a template; we need the AI half of the loop.

**Scope that fits in a day:**

- Add `POST /phone/projects/scaffold` to `phone_backend_http.go`. Body: `{ name, prompt, mode: "text"|"voice-transcript", template?: string }`. Calls a new `desktop/agent/phone_scaffold.go` that wraps the existing `autodev`/Claude Code runner but with a fixed system prompt: "produce a `PhoneSchema` JSON + an optional `PhoneSeed` for this app idea". Parse the runner's response → `ApplyPhoneSchema` + `ApplyPhoneSeed`.
- Mobile UI: in the existing create form (`phone-projects.tsx`), add a new top option above the Template rows: a `TextInput` + microphone button. Filled prompt → call `scaffold` instead of `create`. Leave the manual template flow as fallback.
- Voice input already exists (`desktop/agent/voice.go` with PersonaPlex / Whisper). The mobile side should hit `/voice/transcribe` first, then feed the transcript into the scaffold call.
- The agent's auto-ideas loop already knows how to run Claude Code (`desktop/agent/autodev_ideas.go::autodevRefillIdeas` is the closest pattern). Mirror it.

**Acceptance:** `curl -X POST /phone/projects/scaffold -d '{"name":"Habit Tracker","prompt":"habit tracker with streak count and daily reminders"}'` returns a PhoneProject whose schema has at least a `habits` table with sensible columns. Keep it bounded: one prompt, one scaffold pass, no follow-up turns. Failures return a clear error; user falls back to manual template.

#### 1.4 `cloud.yaver.io` DNS + TLS

**Why:** yc.md Apr 24. Today the mobile app and CLI point at `https://cloud.yaver.io` but nothing listens there. For the demo video we need a real URL.

**Runbook (fits in 30 minutes if the Hetzner box is already up):**

1. Provision or reuse a Hetzner VPS (recommended: CX22 or similar, €4–€8/mo).
2. `scp cloud/deploy.sh root@<IP>:/tmp/ && ssh root@<IP> bash /tmp/deploy.sh` — see `cloud/README.md`.
3. Set `cloud.yaver.io` A record to the VPS IP (Cloudflare → yaver.io zone → new A record, proxy OFF for Let's Encrypt).
4. Set `cloud/.env` on the box — `CLOUD_DOMAIN=cloud.yaver.io`, `CLOUD_TLS_EMAIL=admin@yaver.io`, `CLOUD_OWNER_TOKEN=$(openssl rand -hex 32)` (stash in team vault).
5. `docker compose up -d` on the box — Caddy will fetch the cert in ~30s.
6. Smoke test: `curl -sf https://cloud.yaver.io/health` → 200, then `yaver phone push --to https://cloud.yaver.io <slug>` with the owner token.

**Don't commit the token anywhere.** The mobile app will need it too — simplest is to surface it through the user's vault (`yaver vault set yaver-cloud-token <token>` then pull in the mobile client). Long-term: swap for per-user tokens via the existing `sdkTokens` table.

### 2. User-requested, lower demo-criticality

#### 2.1 GitHub / GitLab monorepo scaffolding

**Why:** user wants the phone-first flow to result in a real git repo on their account, so they can vibe-code against it from any surface. Monorepo = mobile + backend + shared schema in one repo.

**Scope:**

- New `desktop/agent/phone_git.go`: functions `CreateRepo(provider, name)`, `PushManifest(projectSlug, remoteURL)`. Uses existing OAuth infra (`desktop/agent/github_oauth.go` if it exists — check `accounts.go` and the Convex `accounts` table).
- New CLI: `yaver phone git-init <slug> --provider github --name my-repo`. Creates the repo, seeds it with: `/backend/` (symlink or copy of the phone-project tarball contents), `/mobile/` (Expo scaffold), `/package.json` (monorepo), `/.github/workflows/deploy.yml` (runs `yaver phone push --to $YAVER_CLOUD_URL` on main).
- Mobile UI: in the project detail screen, add an "Open in GitHub" section after Deploy — link existing repo, or one-tap "Create new repo from this project".

**Defer until 1.1 + 1.2 are shipped.** The phone-only demo works without this; HN audience doesn't need to see a GitHub repo creation in the 2-minute video.

#### 2.2 OpenAI key onboarding helper

**Why:** user wants "easier way to get the key — OAuth via OpenAI SDK". Pragmatic reality: **OpenAI does not expose one-click OAuth for API key issuance.** Their OAuth-for-ChatGPT-Apps grants scopes for ChatGPT (not the API). Third-party integrations paste the key.

**Ship this instead (1 hour):**

- New mobile screen: `mobile/app/ai-keys.tsx`. Listen to paste, auto-detect key format (`sk-...`), validate live via `fetch("https://api.openai.com/v1/models", {Authorization: Bearer ...})`. Green check if 200, red cross with error body on 401.
- A big "Open platform.openai.com/api-keys" button that opens in-app WebView / system browser and pre-focuses on the API keys page.
- Save into the existing vault via `POST /vault/set` with category `api-key` and name `openai-default`.
- Same for Anthropic (`sk-ant-...`, validate via `/v1/messages` with a 1-token test). Same for Google Gemini.

**Optional polish:** "Sign in with ChatGPT" button that authenticates against OpenAI's OAuth to confirm the user has a ChatGPT Plus subscription, then suggest "now paste your API key from platform.openai.com" — but this is theater, not real OAuth-to-key.

#### 2.3 True on-device SQLite runtime (`expo-sqlite`)

**Why:** MOBILE_WORKER.md §"iPhone and Android Expectations" — "iPhone is viable if the runtime stays inside the app sandbox". Today "Phone only" mode in the create form actually stores the project on the currently-connected agent (usually the user's Mac). For the pitch to be literally true, we need on-device.

**Scope (2–3 days — don't start until demo-blockers are in):**

- New `mobile/src/lib/phoneLocal.ts` backed by `expo-sqlite` — same API surface as the HTTP wrappers in `phoneProjects.ts` (`listPhoneProjects`, `browsePhoneTable`, etc.), but against a local DB at `FileSystem.documentDirectory + 'phone-projects/<slug>/local.db'`.
- `exportLocalBundle(slug)`: builds the same tgz shape as agent-side `ExportPhoneProject`. Use `expo-tar` or a JS-only gzip + tar writer (the tarball is tiny so perf doesn't matter).
- In the create form's "This device" path, branch: if the user has pulled a new `useLocalMiniBackend()` feature flag, use `phoneLocal` instead of `createPhoneProject`. Otherwise fall through to the current agent-hosted path.
- Deploy flow: when pushing from the on-device runtime to Mac/Cloud, upload the local bundle directly instead of going through `/phone/projects/export` on an agent.

**Acceptance:** enable the feature flag, disconnect the phone from the agent, create a project offline, add rows, reconnect to a Mac agent, deploy → rows appear on Mac. Proves the data lived on the phone.

#### 2.4 Cloudflare DNS helpers — ✅ SHIPPED

User asked: "make DNS helpers for Cloudflare etc. so user may deploy yaver lite backend to Cloudflare as well with DNS settings."

DNS half is **shipped** as of 2026-04-17:
- `desktop/agent/cloudflare_dns.go` — typed `CloudflareClient` (VerifyToken, ListZones, ListRecords, CreateRecord, DeleteRecord). 14 tests cover envelope parsing, error surfacing, pagination, validation.
- `desktop/agent/httpserver.go` — `registerDNSRoutes` with `/dns/cloudflare/{verify,zones,records}`. Owner-auth only. Token via `X-CF-Token` header (never persisted by the agent).
- `mobile/src/lib/phoneProjects.ts` — `verifyCloudflareToken`, `listCloudflareZones`, `listCloudflareRecords`, `createCloudflareRecord`, `deleteCloudflareRecord` helpers.
- `mobile/app/phone-project/dns.tsx` — "Custom Domain" screen. Paste token → verify → list zones → pick zone → CNAME/A/TXT form with proxy toggle → one-tap create. Long-press a record to delete. Token cached in AsyncStorage on-device (keyed `yaver.cloudflare.token`).

Uses:
- Tap `[Yaver Cloud]`, then head into **Custom domain (Cloudflare DNS)** to CNAME `myapp.example.com → cloud.yaver.io`. Caddy's on-demand TLS on the Hetzner box picks up the new hostname automatically — no redeploy needed.
- Works for bring-your-own-infra too: CNAME to your dev-hw agent's tunnel URL, or A-record straight to a Hetzner IP.

**Remaining for full "DNS etc." parity with the user's note:**
- **Route 53 / DigitalOcean / Google Cloud DNS adapters** — same interface shape, swap the `CloudflareClient`. The mobile screen would become a single "DNS provider" picker with per-provider paste-back. ~1 day each.
- **Auto-inject yaver.cloud apex records** so paying users who own a domain can point it at Yaver Cloud with one tap (Yaver asks for the Cloudflare token once, creates both the apex A record + the wildcard CNAME, saves a "domain linked" flag in the user's settings). ~2 hours once Route 53 is done.

#### 2.4d Runtime data API + per-project API keys + TS SDK — ✅ SHIPPED

User: "make sure that all inbounds and outbounds of yaver backend works perfect for mobile we dont have export since its just react native and same for web (for third party apps that developers will develop with yaver)."

The phone-projects surface until today was owner-managed (`/phone/projects/*` behind the agent's owner bearer). A third-party RN / web app the developer ships to end users needed scoped tokens + a clean runtime API + an SDK. All three shipped:

- **Agent: per-project tokens.** `desktop/agent/phone_tokens.go` — `pp_<slug>_<64hex>` format, stored as SHA-256 in `<project-dir>/tokens.yaml` (0600), raw returned once on mint. `POST /phone/projects/tokens` mint, `GET /phone/projects/tokens?slug=X` list (summaries only, no hash), `DELETE ?slug=X&tokenId=Y` revoke. `ValidatePhoneProjectToken(raw)` returns the summary + bound slug or `ErrInvalidPhoneProjectToken`. **Scope is per-project, hard-enforced** — a forged cross-project token (same raw hex, swapped slug prefix) fails the hash check. 8 tests.

- **Agent: public data routes.** `desktop/agent/phone_data_http.go` — `GET /data/{slug}/{table}[/{id}]` list + get-one, `POST /data/{slug}/{table}` insert, `PATCH /data/{slug}/{table}/{id}` update, `DELETE /data/{slug}/{table}/{id}` remove. CORS preflight with permissive defaults (origin echoed, credentials off — tokens flow explicitly through `Authorization`/`X-API-Key`/`?api_key=`). Request body capped at 1 MB per row. Cross-project token access returns 403 even when the slug guess is valid. 8 tests.

- **SDK: `createYaverBackendClient({ baseUrl, slug, apiKey }).collection(name)`** — new `sdk/js/src/backend.ts` in the existing `yaver-sdk` npm package. Zero dependencies (uses global `fetch`). Works in RN + browser + Node ≥18. Typed per-row `YaverCollection<Row>`: `list`, `get` (returns null on 404, not throw), `insert`, `update`, `remove`. `YaverBackendError` with `.status` for non-2xx. `raw fetch` escape hatch for unsupported endpoints without bypassing the token. Re-exported from `sdk/js/src/index.ts`.

- **Mobile UI: API Keys screen.** `mobile/app/phone-project/api-keys.tsx` — mint (label-required), shows raw plaintext ONCE with big copy-to-clipboard + dismiss buttons, lists active keys with `createdAt` + `lastUsed`, instant-effect revoke. Entry point from the project detail screen next to OAuth + DNS. Bottom of the screen has a copy-pasteable `yaver-sdk` snippet pre-filled with the current slug so a developer can ship a working integration in 30 seconds.

Use cases this fills:
- **RN app** the developer ships: `import { createYaverBackendClient } from "yaver-sdk"`, store key in `react-native-config`, `yaver.collection("todos").list()` works on the user's device.
- **Web app** (Next.js / Vite / plain `<script>`): same import, server-side routes read the key from env, browser SPAs also work thanks to CORS.
- **Node script / serverless function**: same import, same contract, no extra runtime config.

**Codex follow-ups flagged in §3 below:**
- Per-project CORS origin allowlist (mobile UI: text input for comma-separated origins, agent-side gate before the permissive default).
- Rate limit per-token so a buggy app or leaked key can't drain an agent.
- Typed schema codegen — run through `schema.yaml` to emit `type Todo = { id: string; title: string; done: boolean }` for the SDK so `collection<Todo>("todos")` inference becomes free. Ship as `yaver codegen` CLI + a Next.js plugin.

#### 2.4c Cost guardrails — ✅ SHIPPED

User concern: "make sure to not make me poor etc. with cloudflare convex vercel deploys of tons of bytes etc."

Two guardrails shipped today:

1. **Hard byte-cap on export + receive.** `desktop/agent/phone_cost.go` defines `PhoneDeployBudgetBytes` (default 50 MB) and `EnforcePhoneDeployBudget`. `ExportPhoneProjectWithOptions` refuses to return a tgz that exceeds the cap (with a descriptive error that mentions the `--include-data` remedy). `handlePhoneReceive` enforces again server-side (413 Payload Too Large with the same descriptive body), so a malicious client can't bypass by building the tgz themselves. `PhoneExportOptions` gains `MaxBundleBytes` for per-call overrides. 5 new tests.

2. **Pre-flight cost hint.** `GET /phone/projects/cost-hint` returns advisory strings per target kind (this-device / dev-hw / yaver-cloud / cloudflare-workers / custom). Mobile `runPush` now HEADs the export URL to get bundle size, then shows a confirm alert: `"Uploading ~X.Y MB (cap: 50 MB). Plan: <free line>. <advice>."` User hits Deploy or Cancel with full transparency.

**What this rules out:**
- Accidentally shipping a 2 GB SQLite to Yaver Cloud on a cellular data plan.
- A Cloudflare Workers accidental bill from pushing a 500 MB D1 database.
- Repeated deploys that silently balloon — each one is gated behind an explicit confirm.

**What this doesn't solve yet (Codex follow-ups):**
- **Cumulative byte accounting.** No "this month: 120 MB pushed to Yaver Cloud" yet. Needs a per-target ledger in `~/.yaver/deploy-ledger.json` + a `/phone/projects/usage` endpoint.
- **Deploy rate limiting.** A double-tap still sends two bundles; should debounce server-side with a 30-second min interval per (slug, target).
- **Convex / Supabase per-call metering.** When using the escape paths we have no insight into the destination's actual cost — just into our own upload. Would need to pull quota data from the destination's admin API after deploy.
- **Hard-budget kill switch.** Configurable `cost_budget_usd_per_month` that refuses deploys after N dollars. Requires the ledger above + a rough byte→$ conversion table per target.

#### 2.4b Curated escape routes — ✅ SHIPPED (behind the existing Advanced collapsible)

**Positioning clarification (user-stated 2026-04-17 pm):** the primary use case for Yaver is a **vibe coder working from a phone on a monorepo** — chatting, prompting, deploying to their own dev hw or Yaver Cloud. **Escape routes are a trust signal, not a headline feature.** They reassure the user "no lock-in" so they commit to the Yaver-native continuum; they're never fronted in the main UI.

Shipped as:
- `desktop/agent/phone_escape.go` — `EscapeRoute` struct + `escapeRouteCatalog` (curated list: inbound "X → Yaver Cloud" with `highlight=true`, outbound "Yaver → X" for no-lock-in reassurance, and third-party-to-third-party routes that use Yaver-as-transit). Handlers: `GET /escape/routes?from=&to=` + `POST /escape/plan`. The plan endpoint is a thin wrapper over `SwitchEngine.Plan` — we don't add migration code, we just map friendly (from,to) pairs to switch-target IDs.
- `phone_escape_test.go` — 11 tests, including a catalog-drift guard that asserts every curated route points at a real SwitchEngine target id.
- `mobile/src/lib/phoneProjects.ts` — `listEscapeRoutes({from, to})` + `planEscapeRoute(routeId, projectDir, opts)`.
- `mobile/app/phone-project/[slug].tsx` — the existing Advanced collapsible now fetches the curated list on open and renders friendly rows ("Yaver → Neon", "Supabase → Yaver Cloud · PITCH · hard"). No new screen, no top-level entry point. Reuses the existing `doPromote` handler so the switch engine runs the migration the same way a power-user would via `/switch/plan`.

When Codex extends this:
- Adding a route = append to `escapeRouteCatalog`. Ensure `toTargetID` exists in `SwitchTargets()` (test covers this).
- For sources Yaver doesn't know yet (Firebase, PlanetScale, DynamoDB): add a new `BackendKind` + inferer in `backend_adapter.go :: inferBackend`, then the catalog row lights up automatically.
- Connection-string-only source ("I don't have the code on disk, here's my Supabase URL + service key") is the next logical step — not shipped today. Add a `POST /escape/inspect` that takes a URL + creds, uses the existing `backend_sql.go` / `backend_convex.go` adapters to pull the schema, and returns a synthetic projectDir the plan endpoint can then use.

#### 2.5 Yaver Lite backend on Cloudflare Workers — ⏳ NOT YET STARTED

User also asked: "if possible integrate so user may deploy yaver lite backend to Cloudflare as well." Workers is a real tier to add to the continuum — free for low traffic, global edge, D1 as the SQLite substrate. Not a few-hours feature though; this is a ~2-3 day port.

**Target shape:** `{ kind: "cloudflare-workers", accountId: string, scriptName: string, domain?: string }` as a new `PhonePushTarget` variant. `pushPhoneProject` unchanged on the client; `resolvePhonePushTargetBase` maps the target to the Worker's public URL and uploads the tgz to a receive endpoint that the Worker implements.

**Scope (fits in 2-3 days if done cold):**

1. **`cloud/workers/` directory** — new wrangler-based project. `wrangler.toml` declares:
   - `name = "yaver-lite-<user-slug>"`
   - `main = "src/worker.ts"`
   - `compatibility_date = "2026-03-01"`
   - `[[d1_databases]]` binding `DB` (one per deployed phone-project — deploy script creates it)
   - `[[r2_buckets]]` binding `STORAGE` (optional, for the `storage/` blob bucket)
   - Route: `${YAVER_PUBLIC_DOMAIN}/phone/*`

2. **`cloud/workers/src/worker.ts`** — TypeScript port of the `/phone/projects/*` HTTP surface backed by D1. The Worker implements:
   - `GET /phone/projects/list` / `get` / `tables` / `browse` — all read-only against D1.
   - `POST /phone/projects/{create,insert,update,delete-row,schema,auth,seed}` — write-throughs.
   - `POST /phone/projects/receive` — multipart, unpack tgz, ingest `schema.yaml` → D1 DDL, `seed.json` → INSERT OR REPLACE, `local.db` (if included) → streamed into D1 via `batch()`.
   
   The pure-Go `phone_backend.go` is the spec. Port verbatim.

3. **`desktop/agent/cloudflare_workers.go`** — agent-side deploy helper. Uses the Cloudflare API:
   - `POST /accounts/{accountId}/workers/scripts/{scriptName}` (multipart, `metadata` + `index.js` + modules) to publish the Worker.
   - `POST /accounts/{accountId}/d1/database` + migrations.
   - `POST /zones/{zoneId}/dns_records` for the custom domain (reuse `CloudflareClient.CreateRecord`).
   
   Trigger from `POST /phone/projects/deploy-workers` with `{ slug, accountId, zoneId?, subdomain? }`.

4. **Mobile UI:** in the Deploy section of `[slug].tsx`, add a third primary button `[Cloudflare Workers]` alongside `[Your Dev Machine]` + `[Yaver Cloud]`. Once a phone-project has a Cloudflare API token configured (reuse the token from the DNS screen), the button lights up.

5. **Caveats to flag in the UI:**
   - **Size ceiling.** Workers free tier caps the script at 1 MB; paid at 10 MB. Tgz + runtime is tiny, but D1-free caps at 500 MB / 5M rows per database. Adequate for the phone-first cohort; surface the limit in the success Alert.
   - **SQLite-compat, not identical.** D1 supports most SQLite features but some pragmas are unavailable. The PhoneSchema DSL is conservative enough that existing templates (blank/crud/todos/notes) all work unchanged — confirm with a dogfood run.
   - **No persistent sockets / long-running tasks.** Fine for phone-projects (all CRUD, no background jobs in MVP).
   - **Worker subdomain shape.** By default the Worker is at `<scriptName>.<accountId>.workers.dev`. Custom domain requires a Cloudflare zone — which the DNS screen above already handles via CNAME.

6. **Acceptance:** create a phone project on iPhone → tap `[Cloudflare Workers]` → paste Account ID + pick zone → 30-60s later, `myapp.example.com` serves the project's `/phone/projects/browse` over Workers + D1. Runtime rows survive promotion when `--include-data` is on. Tear-down from the mobile UI removes the Worker + D1.

**Why defer past HN:** Workers port is a green-field TypeScript codebase that has to stay in lockstep with `phone_backend.go`'s wire shape. Worth it for the three-icon Deploy row (`[dev hw] / [yaver cloud] / [workers]`) but not worth blocking HN launch on — `[Yaver Cloud]` + the DNS helper already deliver the "deploy from phone" story.

### 3. Housekeeping follow-ups (do whenever)

- **`--no-autostart` flag on `yaver serve`** so automation / CI doesn't register a LaunchAgent / systemd unit per HOME. See § "Observations" in the morning dogfood log.
- **Flag convention** on `yaver phone push`: Go's `flag` package requires flags before positional args, which is unfriendly. Either switch this specific subcommand to a hand-rolled parser or document the order explicitly in the help text.
- **`cloud/.env.example`** has `CLOUD_OWNER_TOKEN=` blank — the `deploy.sh` currently fails if you forget to fill it in. Add a "generate a random one for you?" prompt.
- **Privacy regression coverage** for the new `/phone/projects/create` and `/phone/projects/receive` payloads — extend `desktop/agent/convex_privacy_test.go` with a payload fixture that asserts none of the forbidden keys leak into Convex sync calls.

### Where to find things

| Concern | File |
|---|---|
| Runtime (Go) | `desktop/agent/phone_backend.go`, `phone_backend_http.go`, `mcp_phone.go` |
| CLI | `desktop/agent/phone_cmd.go` |
| Tests | `desktop/agent/phone_backend_test.go`, `phone_export_test.go` |
| Mobile deploy UI | `mobile/app/phone-project/[slug].tsx`, `mobile/app/phone-projects.tsx` |
| Mobile client lib | `mobile/src/lib/phoneProjects.ts` |
| Web deploy UI | `web/components/dashboard/PhoneProjectsView.tsx` |
| Web client lib | `web/lib/agent-client.ts` (look for `pushPhoneProject`, `PhonePushTarget`) |
| Hetzner cloud stack | `cloud/` — Dockerfile, docker-compose.yml, Caddyfile, deploy.sh, README.md |
| Spec | `MOBILE_WORKER.md §"Mini Backend"` |
| Design notes | `MOBILE_BACKEND_EXPORT.md` |
| Sprint plan + current status | `yc.md` |

### Commit trail (end of 2026-04-17)

```
4960c31a yc.md §Apr 20: 3-mode picker at phone-project creation
8e7a8c69 yc.md §Wedge Demo: two-button Deploy + Hetzner cloud stack + dogfood log
b6d06165 Phone project push: cross-device deploy + relay-aware target resolution
dbf75d61 Dogfood phone push pipeline + fix missing-slug 500 → 404
39e40740 Ship phone-first mini backend runtime
47040150 Ship phone-first mini backend + export pipeline
```

Run `git log --oneline -10 -- desktop/agent/phone_backend.go mobile/src/lib/phoneProjects.ts web/components/dashboard/PhoneProjectsView.tsx` for the full context any time.
