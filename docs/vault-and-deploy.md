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

### Remaining follow-ups

- **Composite targets**: single command to deploy mobile to both
  TestFlight and Play Store with parallel execution + merged status.
  Today: generate two scripts, `bash a.sh & bash b.sh & wait`.
- **Per-project concurrency cap**: currently a guest can kick off
  multiple parallel `/deploy/ship` calls. The host's build tooling
  will serialize at the filesystem layer (xcodebuild, gradle) but
  a queue with fair scheduling would be friendlier.
- **Run history + idempotency**: replay last N runs, resumable
  artifact uploads. For now a run is a one-shot stream.

---

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
| `scripts/deploy-testflight.sh` | TestFlight deploy, vault-aware |
| `scripts/deploy-playstore.sh` | Play Store deploy, vault-aware |
| `scripts/deploy-web.sh` | Cloudflare deploy, vault-aware |
| `yaver.workspace.yaml` | Monorepo manifest consumed by the generator |
