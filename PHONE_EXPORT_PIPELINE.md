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
