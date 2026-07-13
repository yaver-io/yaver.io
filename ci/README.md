# Hetzner CI / Remote Verification

This directory holds everything needed to run Yaver integration tests against a real remote Linux box — hybrid mode with Ollama, guest sharing, remote-worker, `ops` verb routing, OAuth — on dedicated Hetzner infrastructure rather than GitHub-hosted runners.

**This server is for testing only. Not a production service. Delete it whenever.**

---

## The box

| Attribute | Value |
|---|---|
| Name | `yaver-test-ephemeral` |
| Hetzner ID | `127759097` (stored as `HETZNER_TEST_SERVER_ID` secret) |
| IP | stored as `HETZNER_TEST_SERVER_IP` secret (never committed) |
| Type | `cax21` — 4 vCPU ARM64, 8 GB RAM, 80 GB NVMe |
| Location | `hel1` (Helsinki) |
| Image | `ubuntu-24.04` |
| List price | ~€6.49 / month running, ~€0.009 / hour |
| Snapshot | id `379185792` (stored as `HETZNER_TEST_SNAPSHOT_ID` secret) |

Stopping does **not** pause billing — only deleting does. If the box will be idle for more than a few days, either run `ci/hcloud/snapshot-server.sh` then `hcloud server delete yaver-test-ephemeral` (snapshot stays, ~€0.10/mo) and recreate from snapshot via `ci/hcloud/create-server.sh` when needed.

## What's installed

Provisioned by `ci/remote/bootstrap.sh` (idempotent — safe to rerun).

| Tool | Version (verified 2026-04-22) | Purpose |
|---|---|---|
| Docker CE | 29.4.1 | Container sandbox tests, docker-based relay deploy |
| Docker Compose | v5.1.3 | Stack-up smoke tests |
| Node.js | 22.22.2 | Web / mobile TypeScript builds, SDK packages |
| Go | 1.22.8 (arm64) | Agent build + `go test ./...` |
| Python | 3.12.3 + pipx 1.4.3 | Convex CLI, aider, general scripting |
| Ollama | 0.21.1 | Local model server |
| `qwen2.5-coder:1.5b` | 986 MB | Smallest practical coder model. Produces poor code; perfect for verifying Yaver's Ollama wrapping, not for actual coding. |
| aider | 0.86.2 | Hybrid-mode implementer |
| opencode | 1.14.20 | Alternate coding agent for planner/implementer tests |
| Yaver CLI | 1.99.15 | Agent under test |

SSH access: private key is `~/.ssh/hetzner_ci_ed25519` on Kıvanç's machine, also stored as the `HCLOUD_SSH_PRIVATE_KEY` GitHub secret for CI. Matching public key registered in Hetzner as `yaver-ci` (id 111205416).

## Directory layout

```
ci/
├── README.md                  ← you are here
├── hcloud/                    ← runs on GitHub runner / your laptop
│   ├── common.sh              ← shared env + logging helpers
│   ├── create-server.sh       ← provision ephemeral box (uses snapshot if set)
│   ├── wait-for-ssh.sh        ← poll port 22 until sshd accepts
│   ├── sync-repo.sh           ← rsync repo → /opt/yaver on box
│   ├── snapshot-server.sh     ← take a Hetzner snapshot (cheap pause)
│   ├── gather-logs.sh         ← scp remote logs back for artifacts
│   └── delete-server.sh       ← always-run cleanup
│
└── remote/                    ← runs on the Hetzner box
    ├── bootstrap.sh           ← first-time install of everything above
    └── verify.sh              ← smoke test: toolchain + docker + ollama + go test
    └── verify_qwen_codegen.sh ← focused qwen hello-world codegen smoke
    └── verify_ops_ollama.sh   ← focused localhost /ops → ollama smoke
    └── verify_hybrid_loop.sh  ← focused hybrid planner→implementer file-change smoke
    └── verify-host-share.sh   ← focused borrowed-session / host-share tests
    └── verify-guest-docker-isolation.sh ← focused guest Docker-isolation tests
```

Everything under `ci/hcloud` assumes `HCLOUD_TOKEN` and `HCLOUD_SSH_PRIVATE_KEY_PATH` env vars are set. Scripts write provisioning artifacts (`server-id`, `server-ip`, `server-name`, `server.json`, `snapshot.json`) to `ci/.artifacts/` so later steps — and cleanup — don't have to re-resolve anything. `ci/.artifacts/` is gitignored.

`ci/hcloud/preflight.sh` is the first gate the GitHub-hosted remote workflows run now. It validates:

- `HCLOUD_TOKEN` can actually talk to Hetzner
- persistent mode: `HETZNER_TEST_SERVER_ID` exists and `HETZNER_TEST_SERVER_IP` still matches the live server
- ephemeral mode: `HETZNER_TEST_SNAPSHOT_ID` is accessible before create/delete starts

That turns the common “stale secret” failures into immediate, concrete CI errors instead of 3-minute SSH timeouts.

## GitHub secrets

Set once with `gh secret set <NAME> --repo kivanccakmak/yaver.io`.

| Secret | Used by | Rotation |
|---|---|---|
| `HCLOUD_TOKEN` | create/delete/snapshot server | Rotate in Hetzner Cloud Console → Security → API Tokens |
| `HCLOUD_SSH_PRIVATE_KEY` | every remote step | Regenerate keypair, delete + re-register `yaver-ci` SSH key |
| `HETZNER_TEST_SERVER_ID` | status / label queries | Replace when the persistent box is recreated |
| `HETZNER_TEST_SERVER_IP` | persistent-mode workflow | Replace when the persistent box is recreated |
| `HETZNER_TEST_SNAPSHOT_ID` | snapshot restore | Replace after each `snapshot-server.sh` run |
| `GLM_API_KEY` | GitHub-hosted `runner-integrations.yml` OpenCode GLM lane | Rotate in GLM / Z.AI console; mirrored into `ZAI_API_KEY` at runtime for CLI compatibility |

If the box is ever compromised, **rotate all Hetzner box secrets**, delete the `yaver-ci` key in Hetzner, and recreate with `ssh-keygen -t ed25519 -f ~/.ssh/hetzner_ci_ed25519`.

`GLM_API_KEY` is intentionally **not** used on the Hetzner box. It stays on GitHub-hosted runners because OpenCode/GLM integration does not require remote-machine semantics, and `ci/` should keep expensive or internet-backed checks off the Hetzner machine unless that surface is what we are actually validating.

The OpenCode GLM lane uses runtime-only `OPENCODE_CONFIG_CONTENT` with Z.AI's OpenAI-compatible coding endpoint instead of checking any provider config into the repo. That keeps CI hermetic and avoids coupling project config to one vendor.

## Running

### Locally from your laptop (quickest for iteration)

```bash
export HCLOUD_TOKEN=$(python3 - <<'PY'
import pathlib, tomllib
cfg = tomllib.loads((pathlib.Path.home() / ".config/hcloud/cli.toml").read_text())
active = cfg.get("active_context")
for ctx in cfg.get("contexts", []):
    if ctx.get("name") == active:
        print(ctx["token"])
        break
else:
    raise SystemExit("active hcloud token not found")
PY
)
export HCLOUD_SSH_PRIVATE_KEY_PATH=~/.ssh/hetzner_ci_ed25519

# Point server-ip artifact at the existing persistent box
mkdir -p ci/.artifacts
hcloud server describe yaver-test-ephemeral -o json \
  | jq -r '.public_net.ipv4.ip' > ci/.artifacts/server-ip

./ci/hcloud/sync-repo.sh
ssh -i "$HCLOUD_SSH_PRIVATE_KEY_PATH" \
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    "root@$(cat ci/.artifacts/server-ip)" 'bash /opt/yaver/ci/remote/verify.sh'
```

`sync-repo.sh` intentionally skips bulky local artifacts that are not needed for
remote verification (`dist/`, videos, demo payloads, installer output, mobile
build caches, local agent binaries). If you add another large generated tree,
exclude it there before using the persistent box for iteration.

### Locally from your laptop: focused host-share verification

Use this when you are changing borrowed-session / guest-owned repo flows and do
not want the full `go test ./...` sweep on the remote box:

```bash
export HCLOUD_TOKEN=$(python3 - <<'PY'
import pathlib, tomllib
cfg = tomllib.loads((pathlib.Path.home() / ".config/hcloud/cli.toml").read_text())
active = cfg.get("active_context")
for ctx in cfg.get("contexts", []):
    if ctx.get("name") == active:
        print(ctx["token"])
        break
else:
    raise SystemExit("active hcloud token not found")
PY
)
export HCLOUD_SSH_PRIVATE_KEY_PATH=~/.ssh/hetzner_ci_ed25519

mkdir -p ci/.artifacts
hcloud server describe yaver-test-ephemeral -o json \
  | jq -r '.public_net.ipv4.ip' > ci/.artifacts/server-ip

./ci/hcloud/sync-repo.sh
ssh -i "$HCLOUD_SSH_PRIVATE_KEY_PATH" \
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    "root@$(cat ci/.artifacts/server-ip)" \
    'bash /opt/yaver/ci/remote/verify-host-share.sh'
```

This focused path is the preferred check for:
- `host-share attach-repo`
- `host-share sync-repo --to-host`
- `host-share sync-repo --from-host`
- `host-share end`
- guest-scoped file sync / workspace bootstrap behavior

### From GitHub Actions

`.github/workflows/remote-verify.yml` — manual (`workflow_dispatch`), two modes:

- `target=persistent` — reuses the long-lived box (fast; default)
- `target=ephemeral` — creates a new box from the snapshot, runs verify, **always deletes on cleanup**

```bash
gh workflow run remote-verify.yml --ref main -f target=persistent
gh workflow run remote-verify.yml --ref main -f target=ephemeral   # clean slate
```

`.github/workflows/remote-host-share-verify.yml` — manual focused borrowed-session check:

- `target=persistent` — reuse the always-on test box
- `target=ephemeral` — restore a fresh temporary box from snapshot

```bash
gh workflow run remote-host-share-verify.yml --ref main -f target=persistent
gh workflow run remote-host-share-verify.yml --ref main -f target=ephemeral
```

Use this workflow when changing:
- `host-share attach-repo`
- `host-share sync-repo`
- `host-share end`
- guest-owned repo mirror behavior
- guest-scoped workspace/file sync

`.github/workflows/remote-host-share-agentless.yml` — manual policy-boundary check:

- guest runs on the GitHub runner
- host stays on the dedicated Hetzner machine
- verifies guest can join and use the allowed host-share surface
- verifies guest cannot self-escalate through `/guests/config` or `/exec`
- supports `target=persistent|ephemeral`; use `ephemeral` when you want the host side fully reset after the run

Required GitHub secrets for this workflow:
- `HCLOUD_TOKEN`
- `HCLOUD_SSH_PRIVATE_KEY`
- `HETZNER_TEST_SERVER_ID`
- `HETZNER_TEST_SERVER_IP`
- `CONVEX_SITE_URL`
- `HOST_SHARE_HOST_EMAIL`
- `HOST_SHARE_HOST_PASSWORD`

This workflow now resolves the shared host target from the canonical Hetzner box in CI:

- runs `ci/hcloud/preflight.sh persistent`
- waits for SSH reachability on that box
- discovers `HOST_SHARE_HOST_BASE_URL` as `http://<hetzner-ip>:18080`
- discovers the live `HOST_SHARE_HOST_DEVICE_ID` from `/info`

So the GitHub runner is always the guest user, and the Hetzner box is always the host side of the host-share session.
The guest account is created as a random throwaway user during the run and deleted with `/auth/delete-account` in cleanup, so the workflow is stateless on the guest-account side.

`.github/workflows/remote-guest-docker-verify.yml` — manual focused Docker security check:

- runs on the Hetzner test box
- proves a guest-isolated task actually executes in Docker
- proves mounted workspace visibility and host secret/env/path non-leakage

```bash
gh workflow run remote-host-share-agentless.yml --ref main -f target=persistent
gh workflow run remote-host-share-agentless.yml --ref main -f target=ephemeral
gh workflow run remote-guest-docker-verify.yml --ref main -f target=persistent
gh workflow run remote-guest-docker-verify.yml --ref main -f target=ephemeral
```

`.github/workflows/remote-host-share-lifecycle.yml` — manual session-stop enforcement check:

- guest runs on the GitHub runner
- host stays on the dedicated Hetzner machine
- creates a throwaway guest account, joins a host-share invite, verifies access
- host ends the session, then CI proves the guest loses access
- guest account is deleted during cleanup
- supports `target=persistent|ephemeral`; `ephemeral` gives you a clean host box plus guest-account cleanup

```bash
gh workflow run remote-host-share-lifecycle.yml --ref main -f target=persistent
gh workflow run remote-host-share-lifecycle.yml --ref main -f target=ephemeral
```

`.github/workflows/ci.yml` also carries the basic account lifecycle smoke now:

- create account via `/auth/signup`
- log in
- delete account
- verify deleted token stops validating
- verify login is rejected after deletion

Role model for the Hetzner-backed remote workflows:

- `remote-verify.yml`, `remote-host-share-verify.yml`, `remote-guest-docker-verify.yml`
  GitHub runner acts as the host owner / remote operator. It reaches the Hetzner box over SSH after `ci/hcloud/preflight.sh`.
- `remote-host-share-agentless.yml`
  GitHub runner acts as the host's shared guest user. It resolves the Hetzner host via the same preflight, discovers the live `deviceId` from `/info`, then exercises only the guest-allowed host-share surface.

`.github/workflows/remote-integration.yml` — Hetzner-backed network/integration matrix:

- uses the same canonical Hetzner host or snapshot restore path as the other remote workflows
- walks the connectivity cases one by one: `lan`, local `relay`, `relay-docker`, `relay-binary`, `tailscale`, `cloudflare`
- treats the GitHub runner as `host-owner` for the cross-machine sections: `relay-docker`, `relay-binary`, `tailscale`
- SSHes into the Hetzner box for sections that should run on the host itself: `lan`, `relay`, `auth`, `cloudflare`, `ollama`, `ollama-ci`, `hybrid-local`
- uploads `ci/.artifacts/test-suite-*.log` plus remote `/var/log/yaver-ci/test-suite-*.log` and summary logs as artifacts

Required secrets for `.github/workflows/remote-integration.yml`:

- Always:
  - `HCLOUD_TOKEN`
  - `HCLOUD_SSH_PRIVATE_KEY`
  - `CONVEX_SITE_URL`
- Persistent target:
  - `HETZNER_TEST_SERVER_ID`
  - `HETZNER_TEST_SERVER_IP`
- Ephemeral target:
  - `HETZNER_TEST_SNAPSHOT_ID`
- Tailscale section:
  - `TAILSCALE_AUTHKEY`
- Cloudflare section:
  - `CF_TUNNEL_URL`
  - `CF_ACCESS_CLIENT_ID`
  - `CF_ACCESS_CLIENT_SECRET`
- `remote-host-share-lifecycle.yml`
  GitHub runner acts as the host's shared guest user, but the focus is session revocation: access must work before `/host-share/end` and fail immediately after.

## Re-provisioning from scratch

If the box is gone (or snapshot is stale):

```bash
# Create fresh box (Ubuntu 24.04 if no snapshot id set)
HCLOUD_SSH_KEY_NAME=yaver-ci CI_SERVER_NAME=yaver-test-ephemeral \
  ./ci/hcloud/create-server.sh

# Wait for sshd
./ci/hcloud/wait-for-ssh.sh

# Install everything
ip=$(cat ci/.artifacts/server-ip)
scp -i ~/.ssh/hetzner_ci_ed25519 ci/remote/bootstrap.sh root@$ip:/root/
ssh -i ~/.ssh/hetzner_ci_ed25519 root@$ip 'bash /root/bootstrap.sh'

# Snapshot the finished state
./ci/hcloud/snapshot-server.sh yaver-test-ephemeral

# Update GH secrets with new ids
hcloud server describe yaver-test-ephemeral -o json \
  | jq -r '.id'                      | gh secret set HETZNER_TEST_SERVER_ID
hcloud server describe yaver-test-ephemeral -o json \
  | jq -r '.public_net.ipv4.ip'      | gh secret set HETZNER_TEST_SERVER_IP
jq -r '.image.id' ci/.artifacts/snapshot.json | gh secret set HETZNER_TEST_SNAPSHOT_ID
```

Total cost of a full rebuild: ~€0.02 in compute time while provisioning (~5 min), plus ~€0.10/mo ongoing for the snapshot.

## Stopping billing

```bash
# 1. Take a fresh snapshot of current state
./ci/hcloud/snapshot-server.sh yaver-test-ephemeral

# 2. Update HETZNER_TEST_SNAPSHOT_ID secret
jq -r '.image.id' ci/.artifacts/snapshot.json \
  | gh secret set HETZNER_TEST_SNAPSHOT_ID --repo kivanccakmak/yaver.io

# 3. Delete the server
hcloud server delete yaver-test-ephemeral

# Cost drops from €6.49/mo to ~€0.10/mo. Restore any time via create-server.sh.
```

## What this box **must not** become

- A relay. Use `public.yaver.io` (the relay the platform config actually
  serves) or the proper relay setup in `relay/`.
- A managed customer machine. Use the `cloudMachines` provisioning flow.
- An always-on CI runner. GitHub-hosted runners are cheaper and simpler for unit/lint/typecheck work — this box is strictly for tests that need a real remote Linux host (integration, guest-sharing, Cloudflare/Tailscale/relay connectivity, hybrid-mode with Ollama, OAuth callbacks against mocks).
- A secret store. No prod tokens, no customer data, no real user-PII test fixtures.

If a test can run on a GH-hosted runner for free, it should. Reach for this box only when the test fundamentally needs remote-machine semantics.

## Secret handling rules for the Hetzner box

- Never commit the box IP, SSH private key, `known_hosts`, or Hetzner API token into any tracked file.
- Use GitHub secret names only in tracked docs and workflows. Real values stay in GitHub secrets or local gitignored config.
- Local commands may reference `~/.ssh/hetzner_ci_ed25519` and `~/.config/hcloud/cli.toml`, but never copy their contents into logs, docs, fixtures, or markdown examples.
- If you need to mention the persistent server in code or docs, use the logical name `yaver-test-ephemeral`, not a raw IP.
- For GitHub-runner guest tests, keep the host base URL, host device id, host account password, and guest password in secrets only. Do not print invite codes, session ids, or host base URLs into logs unless they are explicitly masked.

For a persistent shared-owner setup on that box, including vault-backed Codex auth and multi-user/guest guidance, see [docs/hetzner-shared-owner-runbook.md](/Users/kivanccakmak/Workspace/yaver.io/docs/hetzner-shared-owner-runbook.md:1).
