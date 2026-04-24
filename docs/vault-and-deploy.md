# Yaver Vault + Deploy Script Generator

This is the developer reference for Yaver's on-device secret store and
the deploy-script generator built on top of it. If you're a user just
trying to ship a TestFlight build, the short form is:

```bash
yaver vault add APP_STORE_KEY_PATH --project mobile --value ~/keys/AuthKey.p8
yaver vault add APP_STORE_KEY_ID   --project mobile --value 77Z6B543D5
yaver deploy generate --app mobile --target testflight --out scripts/deploy-mobile-ios.sh
bash scripts/deploy-mobile-ios.sh
```

Everything below explains the pieces: how the vault is stored, how
secrets sync between your own machines, how the doctor preflight
works, how the script generator picks templates, and how to extend
any of these.

---

## 1. Goals

| Goal | How we get there |
|---|---|
| **GitHub/GitLab-secrets parity on your own machine** | Project-scoped, encrypted-at-rest vault at `~/.yaver/vault.enc`. |
| **Nothing sensitive on our servers, ever** | Convex is forbidden from seeing any vault value — enforced by `convex_privacy_test.go`. |
| **Secrets follow you between your devices** | P2P `/vault/sync` between your own agents, last-writer-wins by `UpdatedAt`, tombstones for deletes, no Convex round-trip. |
| **"User's machine = CI runner" — no cloud CI required** | `yaver deploy generate` emits a bash script that reads from the vault and runs the real build+upload commands on whichever machine it's invoked on. |
| **AI agents can drive the whole thing** | MCP verb `secrets` (list/get/set/delete/env/projects), plus HTTP endpoints for the doctor and generator. |

---

## 2. Vault storage

```
~/.yaver/vault.enc
 = [salt(16B)] [nonce(24B)] [nacl_secretbox(JSON)]

plaintext (v2): [
  {
    "name": "APP_STORE_KEY_PATH",
    "project": "mobile",
    "category": "signing-key",
    "value": "/Users/alice/keys/AuthKey_ABC.p8",
    "notes": "",
    "created_at": 1714000000000,
    "updated_at": 1714000000000,
    "device_id": "macmini-alice"
  },
  ...
]
```

Plaintext v1 (`map[string]VaultEntry`, no `project`, no `device_id`,
no `deleted`) is auto-migrated on load — every v1 entry becomes a
`project=""` global. The next write re-serialises as v2.

The key is derived from a passphrase with Argon2id:

- Default passphrase: SHA-256 of the user's Yaver auth token
- Override: `YAVER_VAULT_PASSPHRASE=<value> yaver vault ...`

Rotating the auth token therefore invalidates the derivation. If you
re-auth without first setting `YAVER_VAULT_PASSPHRASE` to the old
passphrase, the agent can't decrypt the old file. Back up with
`yaver vault export` before rotating if you care.

---

## 3. VaultEntry fields

| Field | Purpose |
|---|---|
| `name` | Env-var-like identifier. Charset `A-Za-z0-9_-.`, max 128 chars. |
| `project` | Grouping unit. `""` = global. Same charset as `name`. |
| `category` | Free-form tag: `api-key` / `signing-key` / `ssh-key` / `git-credential` / `custom`. |
| `value` | The secret (plaintext in memory; absent from list/summary responses). |
| `notes` | Free-form description. |
| `created_at` / `updated_at` | Unix milliseconds. `UpdatedAt` is last-writer-wins for sync. |
| `device_id` | Stamped on every write — the device that authored this revision. |
| `deleted` | Tombstone bit. Soft-deletes propagate via sync; GC'd after 30 days. |

### Global vs. project entries

The same `name` is allowed under multiple projects. A common pattern:

```
(global)   APPLE_TEAM_ID     = 5SJZ4KA39A      # shared across all iOS projects
mobile     APP_STORE_KEY_ID  = 77Z6B543D5      # mobile-specific
sfmg       APP_STORE_KEY_ID  = ABC0123XYZ      # different app, different key
```

`yaver vault env --project mobile` produces both the global and
scoped entries, with project entries winning on name collision.

---

## 4. CLI

```
yaver vault add <name> [--project <p>] [--category <cat>] [--value <val>] [--notes <text>]
yaver vault list [--project <p>|*]       # default "*" = every project
yaver vault get <name> [--project <p>]
yaver vault delete <name> [--project <p>]
yaver vault export                       # plaintext JSON dump
yaver vault import <file.json>
yaver vault projects                     # distinct project names
yaver vault env --project <p> [--no-globals]
yaver vault exec --project <p> -- <cmd...>
yaver vault sync [--from <deviceId>]
```

### Worked examples

Adding credentials for a new app:

```bash
yaver vault add CLOUDFLARE_API_TOKEN  --project web --category api-key
yaver vault add CLOUDFLARE_ACCOUNT_ID --project web --value abc123
yaver vault list --project web
```

Running an existing shell command with vault-injected env:

```bash
yaver vault exec --project web -- npm run deploy
```

Syncing secrets to another machine of yours (assume you've paired a
Mac mini to the same Yaver account):

```bash
yaver vault sync                         # pull + push with every paired device
yaver vault sync --from macmini-alice    # just one peer
```

---

## 5. HTTP endpoints

All are owner-auth and rate-limited. Nothing is guest-accessible —
feedback-only and full-scope guests both get 403 on every `/vault/*`
path.

| Endpoint | Method | Purpose |
|---|---|---|
| `/vault/list?project=<p>` | GET | Summaries (no values). Response: `{entries, projects}`. `project=*` = all, `project=""` = globals only. |
| `/vault/get?name=X[&project=Y]` | GET | Full `VaultEntry` (includes `value`). |
| `/vault/set` | POST | Body: `{name, project?, value, category?, notes?}`. |
| `/vault/delete?name=X[&project=Y]` | DELETE | Soft delete (tombstone). |
| `/vault/env?project=X[&globals=0]` | GET | Plain-text `export KEY='...'` script. |
| `/vault/digest` | GET | Sync handshake: `{entries: [{name, project, updated_at, deleted}, ...]}`. |
| `/vault/sync` | POST | Body: `{digest: [...]}`. Response: `{entries: [VaultEntry, ...]}` (newer than peer's digest). |
| `/vault/push` | POST | Body: `{entries: [VaultEntry, ...]}`. Response: `{accepted, rejected, errors?}`. |

---

## 6. P2P sync protocol

The sync loop between two agents owned by the same user:

```
Agent A                                       Agent B
──────                                        ──────
1.  local_digest = vs.Digest()
    POST /vault/sync              ───────►
    body: {digest: local_digest}
                                              2.  remote_entries = EntriesNewerThan(
                                                    local_digest)
                                  ◄───────    3.  response: {entries: remote_entries}
4.  for e in remote_entries: vs.Upsert(e)
5.  GET /vault/digest             ───────►
                                              6.  response: {entries: B's digest}
                                  ◄───────
7.  our_newer = EntriesNewerThan(
      B's digest)
    POST /vault/push              ───────►
    body: {entries: our_newer}
                                              8.  for e in entries: vs.Upsert(e)
                                  ◄───────    9.  response: {accepted, rejected}
```

### Merge rule (`Upsert`)

```
if inbound.updated_at <= local.updated_at:
    reject  (we're equal or ahead)
else:
    accept  (inbound wins; carries its own device_id + timestamps)
```

Tombstones are ordinary entries with `deleted=true` and `value=""`.
Upsert accepts them by the same rule — a later delete on machine A
propagates to machine B on the next sync, even if B last saw a
non-deleted revision.

### Conflict example

1. At t=0, A has `CLOUDFLARE_API_TOKEN=old` (`updated_at=t0`).
2. A syncs to B. Both have `old` at `t0`.
3. A edits to `new_on_A` at t=10 (not synced yet).
4. B edits to `new_on_B` at t=11 (not synced yet).
5. A pushes to B first → B rejects (B is newer by 1ms).
6. B pushes to A → A accepts (`t11 > t10`).
7. Both converge on `new_on_B`. `new_on_A` is silently lost.

That's by design — same trade-off as last-writer-wins in any
distributed K/V. If you need audit trails, the CLI `yaver vault list`
shows `UPDATED` + `DEVICE` columns so you can see which machine
authored the current revision.

---

## 7. Privacy boundary (enforced)

`desktop/agent/convex_privacy_test.go` keeps a forbidden-keys list:
`token`, `rawToken`, `secret`, `password`, `vaultValue`, `privateKey`,
`path`, `absPath`, `workDir`, `sourcePath`, `filePath`, `stdout`,
`stderr`, `output`, `logs`, `logOutput`, `taskOutput`, `fileContent`,
`fileBytes`, `body`. The test asserts no Convex mutation payload ever
contains any of them.

This is load-bearing for the vault: the sync path bypasses Convex
entirely and uses the existing peer-to-peer proxy (direct-LAN or
relay-forwarded) between your own agents, not Convex as an
intermediate. Your secret values never transit through our servers.

---

## 8. Toolchain doctor (`yaver doctor build`)

Given a target like `testflight`, the doctor answers "does this
machine have everything it needs to ship, including the secrets?"

```
yaver doctor build --target=testflight --project=mobile
```

Output (human form):

```
[FAIL] testflight  (react-native-expo)  project=mobile
-----------------------------------------------------
  [  OK] xcodebuild     /usr/bin/xcodebuild Xcode 16.0
  [  OK] pod            /opt/homebrew/bin/pod 1.15.2
  [  OK] node           /opt/homebrew/bin/node v22.10.0
  [  OK] npm            /opt/homebrew/bin/npm 10.9.0
  [  OK] APP_STORE_KEY_PATH             vault:project (mobile)
  [MISS] APP_STORE_KEY_ID               not set in vault or env
  [MISS] APP_STORE_KEY_ISSUER           not set in vault or env
  [  OK] APPLE_TEAM_ID                  vault:global
  * APP_STORE_KEY_ID not found in vault or env — add with: yaver vault add APP_STORE_KEY_ID --project mobile
```

JSON form (`--json`) emits `BuildDoctorReport` — used by the HTTP
endpoint (`GET /doctor/build`) and the generated deploy scripts
(which run the doctor as a preflight gate).

### Targets catalogue

Defined in `desktop/agent/doctor_build.go::buildTargets`:

| Target | Stack | Required tools | Secrets |
|---|---|---|---|
| `testflight` | react-native-expo | xcodebuild (darwin), pod (darwin), node, npm | APP_STORE_KEY_PATH, APP_STORE_KEY_ID, APP_STORE_KEY_ISSUER, APPLE_TEAM_ID |
| `playstore` | react-native-expo | java, python3 | ANDROID_KEYSTORE_PASSWORD, ANDROID_KEY_ALIAS, ANDROID_KEY_PASSWORD, PLAY_STORE_KEY_FILE |
| `cloudflare` | nextjs | node, npm, wrangler | CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID |
| `convex` | convex | node, npm | CONVEX_DEPLOY_KEY, CONVEX_URL |
| `npm-publish` | node | node, npm | NPM_TOKEN |
| `pypi-publish` | python | python3, twine (optional) | PYPI_TOKEN |

Adding a new target is one entry in the map.

---

## 9. Deploy-script generator

`yaver deploy generate --app=<name> --target=<target>` emits a bash
script that:

1. Sources vault env (project + globals) — vault-present wins over
   parent env.
2. Runs `yaver doctor build` as a preflight gate — refuses to
   proceed if the toolchain is incomplete.
3. Runs the target-specific build+upload commands.

The generator resolves `stack` + `path` from `yaver.workspace.yaml`
(if present) or accepts `--stack` / `--path` flags explicitly.

```bash
yaver deploy templates                           # list supported combos
yaver deploy generate --app web --target cloudflare --out scripts/deploy-web.sh
```

Templates live in `desktop/agent/deploy_script_gen.go::deployTemplates`,
keyed by `<stack>:<target>`. Bodies are Go `text/template` strings
with access to `{{.App}}`, `{{.Path}}`, `{{.Stack}}`, `{{.Target}}`.

### Composite deploys

Not yet a single command. Today, generate both and run in parallel:

```bash
yaver deploy generate --app mobile --target testflight --out /tmp/tf.sh
yaver deploy generate --app mobile --target playstore  --out /tmp/ps.sh
bash /tmp/tf.sh & bash /tmp/ps.sh & wait
```

---

## 10. Integration with existing `scripts/deploy-*.sh`

`scripts/deploy-testflight.sh`, `scripts/deploy-playstore.sh`, and
`scripts/deploy-web.sh` now source the vault at the top (silent no-op
if `yaver` isn't installed). Precedence:

- Vault value present → used.
- Vault value missing → parent env passes through unchanged (CI mode).

This means GitHub Actions keep working without any change — CI runs
have no vault, so env vars set by `secrets.ANDROID_KEYSTORE_PASSWORD`
etc. pass through. Local runs can now omit `export` gymnastics by
putting the values in the vault once.

---

## 11. MCP surface

One verb, wrapping everything the vault CLI does:

```
ops secrets { op: list,   scope: vault, project: "*" }
ops secrets { op: get,    scope: vault, project: "mobile", name: "APP_STORE_KEY_ID" }
ops secrets { op: set,    scope: vault, project: "mobile", name: "X", value: "..." }
ops secrets { op: delete, scope: vault, project: "mobile", name: "X" }
ops secrets { op: env,    scope: vault, project: "mobile", include_globals: true }
ops secrets { op: projects, scope: vault }
```

The `scope: op` path defers to the existing `op_get` / `op_list` MCP
tools for 1Password — one surface across both stores.

**Remote proxy rule (`mcp_remote_proxy.go`):** `vault_*` MCP tools
with a non-empty `device_id` are refused (Layer-4 rule). Sync uses
its own explicit protocol rather than per-tool proxying — that's
what `/vault/sync` exists for.

---

## 12. Guest-triggered deploys (`POST /deploy/ship`)

Shared-machine flow: you share your Mac mini with a friend, and
they run a TestFlight deploy from their laptop against your
machine. Secrets stay invisible; only build stdout streams back.

```bash
# Host side — one-time invite with the new "deploy" scope + the
# project the guest is allowed to ship.
yaver guests invite friend@example.com --scope=deploy --projects=mobile

# Guest side — once accepted:
yaver deploy ship --app mobile --target testflight --machine <host-device-id>
```

### Surface

| Endpoint | Method | Purpose |
|---|---|---|
| `/deploy/ship` | POST | Generate script server-side, preflight, spawn bash with vault env injected, stream stdout/stderr/exit as SSE. |

Request body:

```json
{
  "app": "mobile",
  "target": "testflight",
  "stack": "react-native-expo",   // owner-only override
  "path": "/abs/path",            // owner-only override
  "timeout_sec": 1800             // default 1200, max 3600
}
```

SSE event stream:

```
event: meta
data: {"app":"mobile","target":"testflight","stack":"react-native-expo","path":"...","started_at":...,"timeout_s":1200}

event: line
data: {"stream":"stdout","text":"..."}
event: line
data: {"stream":"stderr","text":"..."}

event: exit
data: {"code":0,"duration_ms":1234567,"ok":true}
```

### Security envelope

1. **Script source is server-side.** The guest POSTs `{app, target}`
   — the script body is rendered from a vetted template. Guests
   cannot inject shell.
2. **Vault values never appear in responses.** They're injected
   into the subprocess env (not the script source). Stdout only
   contains whatever the build commands echo. Templates reference
   secrets via `$VAR`, not their plaintext value, so the value
   doesn't end up in a log unless a user explicitly echoes it
   (which the built-in templates don't).
3. **`allowedProjects` gates every call.** A guest with
   `allowedProjects=["web"]` trying to deploy `mobile` gets 403.
4. **Guests cannot override `stack` or `path`.** Those come from
   `yaver.workspace.yaml` only. Owner calls may override.
5. **Guest subprocess env is a whitelist** — `PATH`, `HOME`, `USER`,
   `LOGNAME`, `SHELL`, `LANG`, `LC_*`, `TMPDIR`, `PWD`, `TERM`,
   plus vault values for the project. Stray host env vars (e.g.
   an open shell's `GITHUB_TOKEN`) never cross into the guest's
   deploy subprocess. Owner deploys still inherit the full parent
   env so ad-hoc local env vars keep working.
6. **Time-bounded.** Default 20 min, max 60 min. After that the
   subprocess is SIGKILL-ed and the stream closes.

### Guest scope tiers

| Scope | `/feedback` | `/blackbox` | `/voice` | `/tasks` | `/vibing` | `/dev/*` | `/deploy/ship` | `/deploy/templates` |
|---|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| `feedback-only` (default) | ✓ | ✓ | ✓ | — | — | — | — | — |
| `deploy` (new) | — | — | — | — | — | — | ✓ | ✓ |
| `full` (teammate) | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |

`deploy` is the minimum tier a "ship-but-don't-touch-code" friend
needs. `/info` returns the usual full-scope payload (no redaction)
for `deploy` guests — they need `voiceInputEnabled` etc. to show a
sensible build UI — but the host/projects/task-stats fields that
`feedback-only` hides stay visible.

### CLI

```bash
yaver deploy ship --app mobile --target testflight
yaver deploy ship --app mobile --target testflight --machine mac-mini-alice
yaver deploy ship --app web   --target cloudflare --timeout 900
```

Owner runs target the local daemon by default; `--machine <id>`
routes the call through the existing peer-proxy to another device
the same user owns.

### Composite targets (parallel multi-target deploy)

```bash
yaver deploy ship --app mobile --targets testflight,playstore
```

The CLI fans out per-target SSE streams in parallel against
`/deploy/ship`. Output is line-prefixed with `[target]` so you can
tell the two streams apart on a shared terminal. The final
`── composite summary ──` block shows per-target success/failure,
and the overall exit code is the max of per-target codes (i.e.
the call fails iff any target fails).

There's no server-side "bulk" endpoint yet — the fan-out is purely
client-side. That keeps the existing per-run concurrency limiter,
history, and guest project gating working unchanged. A composite
run just shows up as N rows in `/deploy/runs`.

### Per-caller concurrency cap

`/deploy/ship` now bounces callers at a per-identity cap before
doing any other work:

| Identity | Cap | Behaviour when at cap |
|---|---|---|
| Owner | 8 concurrent deploys | `429 Too Many Requests` |
| Guest | 2 concurrent deploys per guest userID | `429 Too Many Requests` |

Response body on 429: `{"error": "deploy concurrency cap reached …", "cap": N}`. xcodebuild and gradle would serialize at the filesystem
layer anyway, but this stops a misbehaving guest from CPU-burning
the host with dozens of parallel spawns.

Tune the numbers in `desktop/agent/deploy_run_support.go::deployShipLimits`.

### Run history (`/deploy/runs`)

```
GET /deploy/runs[?limit=N]        → { runs: [ DeployRun, ... ], count }
GET /deploy/runs/{id}             → DeployRun (includes last ~8 KB of output)
GET /deploy/runs/{id}/output      → text/plain full log (streamed, chunked)
```

- Ring buffer of the last 100 runs in memory, plus a per-run
  on-disk log at `~/.yaver/deploys/<id>/output.log`. The in-memory
  list is fast; the on-disk log is durable across noisy runs and
  carries the full stdout/stderr beyond the 8 KB tail.
- Each `DeployRun` carries `id`, `app`, `target`, `stack`, `path`,
  `requested_by`, `is_guest`, `started_at`, `finished_at`,
  `duration_ms`, `exit_code`, `ok`, `in_progress`, `error_class`,
  `timed_out`, `log_bytes`, and (detail endpoint only)
  `output_tail`.
- `log_path` is intentionally stripped from API responses — it
  contains `$HOME` which is the operator's identity; callers get
  `log_bytes` instead so a UI can offer "download full log" without
  learning where the host's home lives.
- List responses elide `output_tail` so a list of 100 runs doesn't
  push a megabyte across the wire.
- Every `/deploy/ship` event stream carries `id` in both `meta` and
  `exit` payloads, so clients can correlate a live stream with its
  future history entry.
- The `exit` event also carries `error_class` and `timed_out`.
- Guest filter: guests only see runs they themselves initiated.
  Others' runs return 404 — indistinguishable from "unknown",
  deliberately.
- GC: on-disk logs drop oldest first once total under
  `~/.yaver/deploys/` exceeds `deployDiskQuotaBytes` (500 MB
  default). Ring-buffer eviction also wipes the matching on-disk
  directory so in-memory + on-disk stay in sync.

### Error classification (`error_class`)

Every finished run is classified with a narrow regex pass over the
captured output tail. Classes are hints, never authoritative: raw
`exit_code` + full log stay available for the skeptic.

| Class | When | Rewrite `ok` to true? |
|---|---|---|
| `already_uploaded` | Apple's "Redundant Binary Upload" / version-bump errors | **yes** (not a failure — Apple's way of saying "you already shipped this") |
| `vault_locked` | "wrong passphrase or corrupted vault", missing vault entry | no |
| `preflight_failed` | The generated script's embedded doctor gate exited non-zero | no |
| `signing_error` | Code Sign, keystore tampering, missing signing cert | no |
| `auth_error` | 401 / 403, E401, "authentication failed", "invalid token" | no |
| `toolchain_missing` | "command not found", "No such file or directory" on a .sh/.gradle/.plist | no |
| `network_error` | "could not resolve host", TCP connection refused, "i/o timeout" | no |
| `disk_full` | "No space left on device" | no |
| `timeout` | Our own context deadline fired (wall clock exceeded) | no |
| `build_failed` | Non-zero exit with nothing specific matched | no |

Rules are ordered specific → generic; first match wins. Add a new
class = add a `classifyRule` entry in
`desktop/agent/deploy_classify.go`.

### `yaver deploy diagnose`

Composite preflight; answers "is this machine set up to ship this
app to this target?" without actually spawning the build. Bundles:

- Workspace resolution (app found in `yaver.workspace.yaml`; path
  exists; stack known).
- `yaver doctor build --target=X --project=Y` (tool versions +
  secret presence in vault + env).

```bash
yaver deploy diagnose --app mobile --target testflight
yaver deploy diagnose --app mobile --target testflight --json  # MCP-friendly
```

HTTP: `GET /deploy/diagnose?app=X&target=Y` — same payload
(`DiagnoseReport`). Guests are allowed subject to
`allowedProjects` (they can pre-flight their own scoped deploy).

### `yaver deploy logs` / `yaver deploy runs`

```bash
yaver deploy runs [--limit 20] [--machine <deviceId>]    # listing
yaver deploy logs <run-id>  [--machine <deviceId>]       # full log
```

`runs` hits `GET /deploy/runs` and renders a compact table
(`ID APP TARGET STATUS DURATION CLASS BY STARTED`). `logs` streams
`GET /deploy/runs/{id}/output` and falls back to the in-memory tail
when disk persistence wasn't active for that run.

### Idempotent Play Store resume

The Android template now has the same kept-on-failure behavior as
TestFlight. A sidecar fingerprint file at
`/tmp/yaver-deploy-<app>-playstore.fp` records
`vc=<versionCode> git=<HEAD sha>` when the AAB is built. On a
rerun, if the AAB exists + the fingerprint matches the current
versionCode AND current git HEAD + the AAB is less than 6 h old,
the template prints `⏭ Resuming: existing AAB is M min old and
fingerprint matches ...` and skips `gradle bundleRelease` + the
versionCode bump entirely. A failed upload therefore costs ~30
seconds on retry instead of another Gradle pass + a wasted
versionCode bump.

Upload success removes the fingerprint so the next invocation
builds fresh. Upload failure (or a rerun at the same commit)
preserves it.

Gradle's own incremental build already makes a rebuild cheap
relative to xcodebuild, but preserving the versionCode + skipping
the JNI/codegen setup still saves meaningful wall time, and —
crucially — avoids wasted versionCode increments when the Play
upload flakes.

### Idempotent TestFlight resume

The iOS template is resumable across retries. If a previous run
archived successfully but the upload failed (Apple transient error,
network hiccup, TestFlight rate limit), the archive is **kept on
disk**. The next invocation of `yaver deploy ship --app X --target
testflight` checks for an existing archive at
`/tmp/yaver-deploy-<app>-testflight.xcarchive` whose embedded
`ApplicationProperties:CFBundleVersion` matches the project's
current `CFBundleVersion` and whose mtime is less than 6 h old.
If both hold, it prints `⏭ Resuming: existing archive for build N
is M min old — skipping xcodebuild archive.` and jumps straight to
the 30-second export + upload phase.

The template also scopes archive + export + derived-data + export-
options-plist paths per `(app, target)` so parallel deploys of
different projects no longer race on `/tmp/yaver-deploy.xcarchive`.

On successful upload, all four temp dirs/files are removed. On
failure, they stay — that's the whole point of the resume.

Cost of this change: ~15 lines of bash in the template + one extra
test assertion. No new Go code. Works with the existing
`ClassifyDeployOutput` error-classifier (a resumed run that
succeeds on the second try still lands as `exit_code=0,
error_class=""`; a repeated Apple-side failure classifies the same
way it did the first time).

### Completion webhook

`Config.DeployWebhookURL` (in `~/.yaver/config.json`) is POST'd a
JSON summary after every finished `/deploy/ship` run. Intended
targets: Slack / Discord / Zapier inbound URLs, or a small private
dashboard. Fire-and-forget: runs in its own goroutine, one retry
after 2 seconds on non-2xx, then gives up. A failing webhook
**cannot** block or slow a deploy.

```json
{
  "id": "abc12345",
  "app": "mobile",
  "target": "testflight",
  "stack": "react-native-expo",
  "requested_by": "friend-user-id",
  "is_guest": true,
  "started_at": 1714065600000,
  "duration_ms": 187000,
  "exit_code": 0,
  "ok": true,
  "error_class": "",
  "timed_out": false,
  "host": "mac-mini"
}
```

Config:

| Key | Value | Notes |
|---|---|---|
| `deploy_webhook_url` | HTTPS URL | empty = disabled |
| `deploy_webhook_on`  | `all` (default), `success`, `failure` | filter |
| `deploy_webhook_secret` | arbitrary string | enables HMAC signing; empty = unsigned |

```bash
# Set via direct edit of ~/.yaver/config.json, or:
yaver config set deploy-webhook-url "https://hooks.slack.com/..."
yaver config set deploy-webhook-on failure   # only fire for fails
yaver config set deploy-webhook-secret "$(openssl rand -hex 32)"
```

Rationale for host-local (not Convex) storage: the URL is a
host-machine behavior, not a user-identity setting. It lives in
`~/.yaver/config.json` next to the other fire-and-forget hooks
(`webhook_secret`, `analytics_webhook_url`). The Privacy Contract
for Convex is unchanged.

#### HMAC signing (X-Yaver-Signature)

When `deploy_webhook_secret` is non-empty, every POST carries two
extra headers:

| Header | Value |
|---|---|
| `X-Yaver-Timestamp` | unix seconds at send time |
| `X-Yaver-Signature` | `sha256=<hex>` where hex = HMAC-SHA256(secret, `{timestamp}.{body}`) |

The timestamp is included in the signed data so an attacker can't
replay a captured signed body indefinitely. A receiver should:

1. Reject if headers are missing.
2. Reject if `|now - timestamp|` exceeds an acceptable skew (Slack
   uses 5 minutes; your call).
3. Recompute `HMAC-SHA256(secret, "<timestamp>.<raw body bytes>")`
   and constant-time compare.

Both retry attempts reuse the same (timestamp, signature) so a
receiver that accepts once and rejects the retry on skew grounds
is doing the right thing — the request is already delivered.

Verification reference (Go):

```go
// import "desktop/agent" for the constants + helper
err := VerifyDeployWebhookSignature(
    secret,
    r.Header.Get(WebhookTimestampHeader),
    r.Header.Get(WebhookSignatureHeader),
    rawBodyBytes,
    5 * time.Minute,
)
if err != nil {
    http.Error(w, "signature: "+err.Error(), http.StatusUnauthorized)
    return
}
```

Other languages — same rule:

```python
import hmac, hashlib, time
mac = hmac.new(secret.encode(), f"{ts}.".encode() + body, hashlib.sha256)
expected = "sha256=" + mac.hexdigest()
if not hmac.compare_digest(expected, header_sig):
    reject()
```

```bash
# Verify from a shell hook:
ts="$1"
sig="$2"
body_file="$3"
expected="sha256=$( { printf '%s.' "$ts"; cat "$body_file"; } \
  | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $NF}' )"
[ "$expected" = "$sig" ] || { echo "bad sig"; exit 1; }
```

### Server-side composite (`targets: [...]`)

`POST /deploy/ship` accepts either `target: "..."` (single) or
`targets: ["testflight", "playstore"]` (multiple). When multiple
targets are supplied the server:

1. Runs preflight (`RunBuildDoctor`) for **every** target upfront.
   If any fails, returns `409 Conflict` with
   `{error, target, doctor}` before opening the SSE stream —
   nothing runs partially.
2. Acquires the per-caller concurrency slot once per target
   (composite of 3 = 3 slots). Rolls back cleanly on 429.
3. Generates a scoped script per target, writes each to its own
   temp file.
4. Opens the SSE stream, spawns one goroutine per target, and
   multiplexes per-target events into that stream. Every `meta`,
   `line`, and `exit` event carries a `target` field so clients
   can demux by target (also by `id`, which is per-target).
5. After every target finishes, emits a `composite` event:

```
event: composite
data: {
  "all_ok": false,
  "any_failure": true,
  "summary": [
    {"target":"testflight","id":"...","ok":true,"code":0,"error_class":"","duration_ms":180000},
    {"target":"playstore", "id":"...","ok":false,"code":1,"error_class":"signing_error","duration_ms":42000}
  ]
}
```

The CLI (`yaver deploy ship --app X --targets t1,t2`) sends one
request with the plural field, demuxes events by `target`, prefixes
printed lines with `[target]`, and renders the composite summary
as a footer. Overall exit code is the worst per-target exit.

Each target still gets its own row in `/deploy/runs`, obeys the
concurrency limiter individually, and fires its own completion
webhook — composite is purely a transport-layer fan-out, not a new
execution model.

SSE writer is mutex-protected so overlapping writes from the N
goroutines never corrupt SSE framing.

### Per-target webhook filters

`Config.DeployWebhookOnByTarget` maps target → filter. Lookup
precedence per finished run:

1. `DeployWebhookOnByTarget[run.Target]`  (most specific)
2. `DeployWebhookOn`                       (global fallback)
3. `"all"`                                 (default)

```json
{
  "deploy_webhook_url": "https://hooks.slack.com/...",
  "deploy_webhook_on": "all",
  "deploy_webhook_on_by_target": {
    "testflight": "failure",
    "cloudflare": "all"
  }
}
```

With the config above, TestFlight failures page; TestFlight
successes are silent; every Cloudflare run (regardless of outcome)
fires. Any target absent from the map (npm-publish, convex, etc.)
falls back to the global `deploy_webhook_on`.

### Remaining follow-ups

- **Idempotent resume for Play Store**: today only TestFlight has
  the archive-resume heuristic. Android AABs could get the same
  "skip rebuild if versionCode + source hash unchanged" guard,
  but gradle's own incremental build does most of the heavy lift
  already.
- **Webhook receiver kit**: a tiny standalone Go binary (plus JS
  npm package) with a single `Verify()` call would save downstream
  users from hand-rolling the timestamp + HMAC check.
- **Bundle fingerprint in resume**: archive matching currently
  keys only on `(CFBundleVersion, mtime)`. A commit-SHA check
  would refuse to resume an archive built from different source.

---

## 12b. Vault sync conflicts + durability

### Structured sync report

`yaver vault sync` and the underlying `vaultSyncWithPeer` now return
a structured per-peer report:

```go
type VaultSyncReport struct {
    Peer            string
    Pulled          int   // peer's entries we accepted
    SupersededLocal int   // within Pulled, entries that overrode
                          // an older local value (your value was
                          // silently replaced)
    Pushed          int   // our entries the peer accepted
    Rejected        int   // our entries the peer rejected (they
                          // already had something as-new-or-newer)
    DurationMs      int64
}
```

CLI rendering:

```
  mac-mini-alice: pulled 3 (superseded-local 1), pushed 0 (rejected 2), 142ms
  laptop:         pulled 0 (superseded-local 0), pushed 5 (rejected 0), 87ms
Sync complete: pulled 3, superseded-local 1, pushed 5, rejected 2 across 2 peers.
```

A non-zero `superseded_local` or `rejected` means two devices wrote
the same `(project, name)` at around the same time and the loser
got silently dropped by last-writer-wins. The CLI prints a footer
hint in that case; UIs should surface the counts prominently.

### Cross-process lock

`~/.yaver/vault.enc.lock` is taken with `flock(LOCK_EX)` on POSIX
and `LockFileEx(LOCKFILE_EXCLUSIVE_LOCK)` on Windows during every
`persist()`. Without this, two `yaver vault add` invocations in
different terminals each loaded the current file, made their edit,
and saved — last save silently wins and drops the other's entry.

The in-process mutex in `VaultStore` is still the primary path; the
file lock is belt-and-braces for the cross-process case.

Flock failures log once and fall through (the write still proceeds
with in-process protection only). This is deliberately fail-soft —
losing file locking is a weaker guarantee, not an unrecoverable
state.

### Stale tempfile detection

If `~/.yaver/vault.enc.tmp` exists at open time (because a previous
save was interrupted after `WriteFile` but before `Rename`), the
loader logs a loud warning with size + mtime. It does **not**
auto-delete — a forensic operator may want to decrypt the `.tmp`
manually. Safe to `rm` once you've confirmed the live vault is OK.

## 13. Relevant files

| File | Role |
|---|---|
| `desktop/agent/vault.go` | `VaultStore` + encryption + merge semantics |
| `desktop/agent/vault_cmd.go` | `yaver vault` CLI |
| `desktop/agent/vault_http.go` | `/vault/*` HTTP handlers |
| `desktop/agent/vault_test.go` | Unit tests |
| `desktop/agent/ops_secrets.go` | MCP `ops secrets` verb |
| `desktop/agent/doctor_build.go` | Toolchain preflight catalogue + runner |
| `desktop/agent/deploy_script_gen.go` | Script generator + template catalogue |
| `desktop/agent/deploy_script_http.go` | `/doctor/build`, `/deploy/templates`, `/deploy/generate` |
| `desktop/agent/deploy_script_gen_test.go` | Generator + doctor tests |
| `desktop/agent/deploy_run.go` | `/deploy/ship` handler — generates + spawns + streams |
| `desktop/agent/deploy_run_support.go` | Concurrency limiter + `/deploy/runs` + `/deploy/runs/{id}/output` handlers |
| `desktop/agent/deploy_history.go` | `DeployHistory` ring buffer + 8 KB tail + per-run on-disk logs + GC |
| `desktop/agent/deploy_classify.go` | Error classification regexes + `ClassifyDeployOutput` |
| `desktop/agent/deploy_diagnose.go` | `yaver deploy diagnose` + `/deploy/diagnose` handler |
| `desktop/agent/deploy_logs_cmd.go` | `yaver deploy logs` + `yaver deploy runs` CLI |
| `desktop/agent/deploy_history_test.go` | History + limiter + endpoint tests |
| `desktop/agent/deploy_history_persist_test.go` | On-disk persistence + GC + `/output` endpoint tests |
| `desktop/agent/deploy_classify_test.go` | Classifier coverage (12 scenarios) |
| `desktop/agent/deploy_run_test.go` | `/deploy/ship` end-to-end + scope + env-whitelist tests |
| `desktop/agent/deploy_ship_cmd.go` | `yaver deploy ship` CLI (single + composite fan-out) |
| `desktop/agent/guest_scope.go` | `GuestScopeDeploy` tier + allow-lists |
| `desktop/agent/vault_lock_unix.go` / `_windows.go` | Cross-process file lock for vault writes |
| `desktop/agent/vault_lock_test.go` | File-lock + stale-tmp detection tests |
| `desktop/agent/deploy_webhook.go` | Fire-and-forget completion webhook (one-retry-then-give-up) |
| `desktop/agent/deploy_webhook_test.go` | Webhook payload + filter + retry tests (httptest.Server) |
| `scripts/deploy-testflight.sh` | TestFlight deploy, vault-aware |
| `scripts/deploy-playstore.sh` | Play Store deploy, vault-aware |
| `scripts/deploy-web.sh` | Cloudflare deploy, vault-aware |
| `yaver.workspace.yaml` | Monorepo manifest consumed by the generator |
