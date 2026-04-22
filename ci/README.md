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
```

Everything under `ci/hcloud` assumes `HCLOUD_TOKEN` and `HCLOUD_SSH_PRIVATE_KEY_PATH` env vars are set. Scripts write provisioning artifacts (`server-id`, `server-ip`, `server-name`, `server.json`, `snapshot.json`) to `ci/.artifacts/` so later steps — and cleanup — don't have to re-resolve anything. `ci/.artifacts/` is gitignored.

## GitHub secrets

Set once with `gh secret set <NAME> --repo kivanccakmak/yaver.io`.

| Secret | Used by | Rotation |
|---|---|---|
| `HCLOUD_TOKEN` | create/delete/snapshot server | Rotate in Hetzner Cloud Console → Security → API Tokens |
| `HCLOUD_SSH_PRIVATE_KEY` | every remote step | Regenerate keypair, delete + re-register `yaver-ci` SSH key |
| `HETZNER_TEST_SERVER_ID` | status / label queries | Replace when the persistent box is recreated |
| `HETZNER_TEST_SERVER_IP` | persistent-mode workflow | Replace when the persistent box is recreated |
| `HETZNER_TEST_SNAPSHOT_ID` | snapshot restore | Replace after each `snapshot-server.sh` run |

If the box is ever compromised, **rotate all five**, delete the `yaver-ci` key in Hetzner, and recreate with `ssh-keygen -t ed25519 -f ~/.ssh/hetzner_ci_ed25519`.

## Running

### Locally from your laptop (quickest for iteration)

```bash
export HCLOUD_TOKEN=$(grep '^  token' ~/.config/hcloud/cli.toml | sed -E 's/.*"(.*)".*/\1/')
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

### From GitHub Actions

`.github/workflows/remote-verify.yml` — manual (`workflow_dispatch`), two modes:

- `target=persistent` — reuses the long-lived box (fast; default)
- `target=ephemeral` — creates a new box from the snapshot, runs verify, **always deletes on cleanup**

```bash
gh workflow run remote-verify.yml --ref main -f target=persistent
gh workflow run remote-verify.yml --ref main -f target=ephemeral   # clean slate
```

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

- A relay. Use `relay.yaver.io` or the proper relay setup in `relay/`.
- A managed customer machine. Use the `cloudMachines` provisioning flow.
- An always-on CI runner. GitHub-hosted runners are cheaper and simpler for unit/lint/typecheck work — this box is strictly for tests that need a real remote Linux host (integration, guest-sharing, Cloudflare/Tailscale/relay connectivity, hybrid-mode with Ollama, OAuth callbacks against mocks).
- A secret store. No prod tokens, no customer data, no real user-PII test fixtures.

If a test can run on a GH-hosted runner for free, it should. Reach for this box only when the test fundamentally needs remote-machine semantics.
