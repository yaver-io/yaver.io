# Yaver relay — production operations

Status: living doc (2026-07-12). How the relay actually runs, deploys, and stays
up in production, and the security rollout it carries. Grounded in the real
topology (verify against source before acting — CLAUDE.md).

## 1. Topology — the relay is a FALLBACK, not the spine

Connection strategy is **direct-first, relay-fallback**:
`same-lan → tailscale → direct → cloudflare-tunnel → relay`
(agents priority-sort transports, relay last — `agent_mesh_remote.go`). The relay
is used *sometimes* — cellular, CGNAT, no direct route.

**Two independent auth gates on any request:**
1. **Transport gate** — may this client USE this path. Relay: password → device
   signature. Direct: you reached the agent's socket.
2. **Agent gate** — may this client COMMAND this agent. The **Bearer token**,
   validated end-to-end by the agent's `auth()` middleware on EVERY transport.

The agent is the real authority. A fully-compromised or down relay cannot command
an agent without a Bearer token the agent itself checks. Security never depends on
the relay being trustworthy or up.

**Physical:** `client → (Cloudflare DNS-only, gray) → nginx (reverse proxy) →
Docker → yaver-relay :8080` on a Hetzner box (public.yaver.io). QUIC 4433 for
agent tunnels. The relay's immediate peer is **nginx (localhost) / the Docker
gateway (private IP)** — never a Cloudflare IP. (This is why the trusted-proxy
default must include loopback + RFC1918, not just Cloudflare — a prior default
would have keyed all traffic on one bucket. Fixed in relay 0.1.18+.)

## 2. Deploy pipeline (relay 0.1.18+ / workflow hardened)

`relay/v*` tag → `release-relay.yml` (builds the binary + GH release) →
`relay-deploy-binary.yml` (workflow_dispatch: scp to box, backup-swap, restart).

Hardening (relay-deploy-binary.yml):
- **Preflight** — asserts `RELAY_SSH_HOST` + `HCLOUD_SSH_PRIVATE_KEY` are set and
  the box resolves + is reachable on :22 BEFORE any mutation. Fails fast + clear.
- **--version sanity** — the new binary must execute before restart.
- **Health-gated auto-rollback** — after restart, probe `/health`; if unhealthy,
  auto-restore `.previous` binary+unit and restart. A bad deploy self-corrects.
- **Public probe** — hits unauthenticated `/health` (no secret in the workflow).

## 3. Runtime resilience

- **Watchdog self-heal** (`public-relay-watchdog.sh`, systemd timer ON the box):
  on failed `/health`, it **restarts yaver-relay and re-checks** before paging —
  recovers a HUNG relay (process alive, not serving), which systemd's
  `Restart=on-failure` can't catch. Escalates (email) only if the restart fails.
- **Fallback-only blast radius** — a relay outage cuts off cellular/NAT'd clients
  but not direct/LAN/Tailscale users. Recovery target: self-heal in seconds, or
  reprovision (`provision-relay.sh`) in minutes — never silent-until-email.
- **In-relay hardening** (audit Bucket B, live in 0.1.18+): trusted-proxy IP
  gating, brute-force throttle, constant-time compares, per-user stream caps,
  Referrer-Policy.

## 4. Security rollout — staged, fail-open, no flag-day

Each layer deploys independently; old + new interoperate.
1. **Backend** (`npx convex deploy`) — `signPublicKey` field, `resolveDeviceSig`,
   `/relay/resolve-sig`. Live, inert until used. ✅
2. **Relay 0.1.19** — verifies device signatures when present, password still
   accepted. Emits `/authmix` telemetry.
3. **Agent release** (`cli/v*`) — agents auto-update, register `signPublicKey`,
   sign relay-routed requests. Old agents keep using the password.
4. **Cutover** — watch `/authmix` (`sigPercent`); flip password auth off ONLY
   when signatures are ~100% of proxy auths. Data-driven, not a guess.
5. **Attestation** — App Attest / Play Integrity gate mobile token issuance
   (`docs/yaver-app-attestation.md`), additive, same pattern.

## 5. Required config (config-as-code — stops drift)

The deploy pipeline needs these GitHub secrets. A missing one is now caught by
preflight (a prior deploy failed silently on an unset `RELAY_SSH_HOST`).

| Secret | Purpose |
|---|---|
| `RELAY_SSH_HOST` | The relay box IP/hostname (same box as `RELAY_HTTP_URL`). **SSH deploy target.** |
| `HCLOUD_SSH_PRIVATE_KEY` | SSH key to reach the box |
| `RELAY_HTTP_URL` | Public relay base URL (clients / probes) |
| `RELAY_PASSWORD` | Shared relay password (legacy transport gate; being retired via device-sig) |

Verify with `gh secret list -R kivanccakmak/yaver.io`.

## 6. Open production actions

1. **🔴 Rotate `RELAY_PASSWORD`** — it was hardcoded in a public-repo workflow
   (removed in 0.1.19's commit, but present in git history). Rotate it; scrub
   history (`git filter-repo`) when convenient. The device-sig migration removes
   the need for a shared password entirely.
2. **Set `RELAY_SSH_HOST`** — unblocks the relay deploy (preflight will pass).
3. **Deploy relay 0.1.19**, verify `/health` + `/authmix`, then cut the agent
   release for the device-sig rollout. Cutover on `sigPercent ~100`.
4. **Wire watchdog → auto-reprovision** for the case where restart doesn't help
   (box-level failure) — currently escalates to email.
</content>
