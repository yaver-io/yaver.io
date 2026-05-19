# Sandbox → Hosted-Tier Handoff (for the yaver-cloud Docker-image session)

_Session B (hosted-tier + sandbox export) → Session A (yaver-cloud Docker image)._
_2026-05-19 · prod Convex `perceptive-minnow-557` · all my work is on `main`, committed + released._

This is the **contract your Docker image / container cloud-init must
satisfy** so the hosted-tier + mobile-sandbox flow keeps working. We
edited adjacent areas — please read §"Hard requirements on the image".

---

## What I'm building

The "normie" loop: develop an app from the phone → it runs on a
**self-hosted OR yaver-managed-cloud box** whose **own self-hosted
Convex** is the backend (no BYOK, no Convex Cloud) → **friends preview
it via Hermes** inside the Yaver container → export the sandbox as a
bundle a coding agent (or a yaver-cloud box) can pick up.

## Done ✅ (all on `main`, shipped)

| Phase | What | Commit / cli |
|---|---|---|
| 0+1 | `cloudMachines.tier` (`byok`\|`hosted`, default byok) + `hostedConvexUrl`; `buildManagedCloudInit` runs `ghcr.io/get-convex/convex-backend` on the box when `tier=hosted`, nginx `/_convex-api`+`/_convex-http`, admin key → `/etc/yaver/convex-selfhosted.json` (0600) | `1fdf4eac`, Convex prod-deployed |
| 2 | Zero-BYOK deploy: `convex:selfhosted` template + `convex-selfhosted` doctor/token entries; reads creds from the on-box file at runtime | `bd72576d`, cli `1.99.213` |
| 4 | Hosted teardown grace (7-day, status `grace`, `deprovisionAt`, `scheduledDestroyId`) + **mandatory** pre-delete snapshot (failed snapshot ABORTS delete) + resubscribe cancels grace | `a8d15e99`, Convex prod-deployed |
| 3 keystone | Hosted box auto-bakes its Convex URL as `EXPO_PUBLIC_CONVEX_URL` into the Hermes/dev bundle (dev + friends' copies, zero config) | `df9c1156`, cli `1.99.214` |
| 5 | Privacy tests: admin key never leaves the box (not in bundle, not in deploy script) | `daa9f319` |
| export | Phone-sandbox export now ships `AGENTS.md` (coding-agent handoff) + a `.zip` twin (`?format=zip`); single source of truth so tgz/zip can't drift | `f1ed5a6e`, cli `1.99.215` |

Everything is **gated on `tier=hosted`**; the `byok` path is proven
byte-identical (tests assert it). Nothing in current prod behavior
changed.

## ⚠️ Hard requirements on the yaver-cloud Docker image / container cloud-init

My Phase-1 self-hosted-Convex bootstrap was added to the **legacy
in-VM** `buildManagedCloudInit`. You moved provisioning to a **thin
container cloud-init** (`buildManagedCloudInitContainer`, `docker run
ghcr.io/kivanccakmak/yaver-cloud:latest`). **These two do not yet
converge.** For `tier=hosted` to work in the container model, the image
/ container cloud-init MUST provide:

1. **`/etc/yaver/convex-selfhosted.json`** — `{"url":"https://<host>/_convex-api","adminKey":"…"}`, mode 0600, **inside the path the agent reads**. The agent runs in your container, so this must live on the **persisted state volume** (`/srv/yaver/state:/root` per your status doc) — e.g. resolve to `/root/.yaver/...` or keep `/etc/yaver/...` but mount it persistently. If it's container-ephemeral, deploy + bundle-env break after the first restart. (Override env `CONVEX_SELFHOSTED_FILE` is honored everywhere I read it — use it if the path differs.)
2. **A running self-hosted Convex** reachable at `127.0.0.1:3210` (API) and `:3211` (HTTP actions) from the agent's network namespace. In the container model decide: sibling container the host starts, docker-in-docker, or supervised inside the image. My cloud-init currently `docker run`s `ghcr.io/get-convex/convex-backend:latest` with volume `yaver-convex-data` — please carry that (or equivalent) into the container path and generate the admin key into the file in §1.
3. **nginx `/_convex-api` (WebSocket) + `/_convex-http`** → loopback 3210/3211. Your host nginx proxies `:443 → container:18080`; add these two locations (WS upgrade headers on `/_convex-api`) or the hosted backend is unreachable from clients.
4. **Image must carry**: `jq` (deploy template runs `jq -r .adminKey`), `node`+`npm` (`npx convex deploy`), Docker access to run/pull the convex-backend image, plus the existing agent/runner toolchain. (You already list most — `jq` is the easy miss.)
5. **`tier` plumbing**: a hosted purchase must set `cloudMachines.tier="hosted"` (schema field exists). `ensureForSubscription` currently does NOT pass `tier` (so subs are byok today). Whoever wires the hosted SKU must thread `tier:"hosted"` through `ensureForSubscription` → `createCloudMachine` (arg already supported).

If you keep the **container** cloud-init as the only path, the cleanest
convergence is: port the `hostedSnippet` (self-hosted Convex container +
admin-key file + nginx locations) from `buildManagedCloudInit` into
`buildManagedCloudInitContainer`, keyed off the same `tier==="hosted"`.
I left `buildManagedCloudInit`'s version intact and **did not touch
your uncommitted `cloudMachines.ts` / `Dockerfile.yaver-cloud`** to
avoid clobbering your WIP — this convergence is the one shared edit;
let's agree who does it.

## Missing / next ⏳

1. **Container ↔ hosted-tier convergence** (above) — the real blocker for hosted on managed cloud.
2. **Receive-side zip + run + event streaming** (requested, not started): `/phone/projects/receive` currently accepts **tgz only** (`gzip.NewReader`). Need: accept `.zip` too (sniff magic / content-type), unzip + materialize + (for hosted) deploy to the box's self-hosted Convex, **streaming status events to the mobile UI** (there's `emitBuildProgress`/`upsertDevOperation` infra to reuse). Must work for self-hosted AND managed-cloud targets.
3. **Phase 3 follow-up**: phone-side monorepo scaffold UI + share/join-by-code (mobile-heavy).
4. **Live `cx42`/`cpx42` hosted e2e**: never run (real spend). Provision one hosted box, prove convex-backend healthy + `yaver deploy --target=selfhosted` works + a friend Hermes-loads pointed at it.

## Coordination notes

- `main` is authoritative (`f1ed5a6e`). Your work is uncommitted on
  branch `fix/yaver-cloud-per-tenant-isolation` (cloudMachines.ts WIP,
  Dockerfile.yaver-cloud, ManagedCloudPanel.tsx). Rebase that branch on
  `main` before continuing — main moved a lot (Phases 0–5 + export).
- I work only via isolated `main` worktrees, never your branch — no
  disk collisions, but our `cloudMachines.ts` diverges: **main has my
  tier/grace/snapshot/hostedSnippet**, your branch has your
  cpx42/container changes. The merge needs care on that file.
- Privacy invariant to preserve: the self-hosted **admin key never
  leaves the box** (not to central Convex, not in the bundle, not in
  the deploy script). Tests pin this — keep them green.
