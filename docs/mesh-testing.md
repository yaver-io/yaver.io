# Yaver Mesh & Support-Link — testing

Tests are split into categories you can run independently. Two tiers:

1. **Unit** (no privilege, no network, no cloud) — pure logic: keys, packet
   parsing, ACL matching, DERP framing, STUN parsing, DNS, support scope.
2. **Data-plane e2e** (real Linux kernel, privileged, **local + $0**) — real
   TUN + wireguard-go handshake + `ip` netconfig + ICMP over the 100.96.x
   overlay, run in two network namespaces inside one privileged container.

## Run them

```bash
# --- Unit (fast, runs anywhere incl. macOS) ---
cd desktop/agent
go test ./mesh/                       # the mesh data-plane package (40+ tests)
go test -run TestGuestSupportScope .  # support-link least-privilege scope lock
go test -run TestMesh .               # mesh privacy + agent-side wiring
go test -run 'TestConvexSync' .       # Convex privacy guard (no secrets leak)

# --- Data-plane end-to-end (needs Docker running; no cloud, no OAuth) ---
./scripts/test-mesh-e2e.sh            # builds cmd/meshtest, runs the netns test
./scripts/test-suite.sh --mesh-e2e    # same, via the categorized suite runner
```

## Why a container instead of a Hetzner box

The mesh data plane needs a real Linux kernel (`/dev/net/tun`, network
namespaces, `ip`). On macOS, Docker Desktop's LinuxKit VM provides exactly that,
so two mesh nodes can be stood up in two netns and pinged across the overlay —
proving the real code path **without** spinning a cloud box, **without** Convex,
and **without** the OAuth step (which can't be automated). Cost: $0, ~30s.

It also runs natively on Linux/CI (no Docker-in-Docker needed when the runner is
already Linux + privileged).

## What the e2e proves

`ci/mesh/e2e-in-container.sh` drives `cmd/meshtest` (a thin harness over the
`mesh` package):

- `mesh.NewDevice` creates a real `yaver-wg0` TUN + wireguard-go device,
- `ConfigureNetwork` assigns the overlay IP + route (`netconfig_linux.go`),
- `SetPeers` configures the WireGuard peer,
- ICMP flows both directions over `100.96.x` → **PASS**.

The first packet shows ~1s RTT (the WireGuard handshake), then sub-millisecond —
the signature of a working tunnel.

## Categories in `scripts/test-suite.sh`

`--unit`, `--lan`, `--relay`, `--relay-docker`, `--tailscale`, `--cloudflare`,
**`--mesh-e2e`** (new), `--auth`, `--sdk`, … — combine freely, e.g.
`./scripts/test-suite.sh --unit --mesh-e2e`. The `--mesh-e2e` category skips
gracefully (not fail) when Docker isn't running.

## Roadmap — what else we can test locally (no cloud, no OAuth)

The mesh e2e proved the pattern: real Linux kernel in a container, no Hetzner,
no Convex, no OAuth. Here's the prioritized catalog of what else can move to
local-container / mock-backed e2e, grouped by effort→confidence.

### A. The enabler: a reusable mock-Convex + mock-OAuth fixture
Bootstrap/auth UNIT coverage is already strong (auth_recover_test.go ×14,
bootstrap_integration_test.go ×8, auth_owner_claim_test.go ×5,
devicecode_test.go ×7). What's missing is the stitched END-TO-END loop. Build
once, unlocks everything in B:
- Reuse `newMockConvex` (auth_convex_path_test.go) → a `ci/mock-convex` that
  serves `/auth/validate`, `/devices/bootstrap{,-pending}`, `/devices/list`,
  `/devices/owner-by-hardware`, `/auth/device-code` (+poll).
- Reuse `ci/oauth-mock` (already serves Google/MS/Apple/GitHub/GitLab).

### B. Bootstrap e2e (needs A) — highest product value
1. **Fresh install → pair → owner-claim → serve** — `yaver serve` (no token) →
   bootstrap mode → POST `/auth/pair/owner-claim` (simulated mobile) → token
   splice → re-exec to serve. Catches passkey/relay/re-exec bugs.
2. **Headless device-code full loop** — `yaver auth --headless` → mock Convex
   issues code → mock OAuth authorizes → poll returns token → persisted.
3. **Auth recovery direct + pair against mock Convex** — the real
   `/devices/owner-by-hardware` validation path (today stubbed in units).

### C. Multi-node networking e2e (containers, ranked by ROI)
1. **Relay-as-DERP fallback** ⭐ best ROI — relay + 2 agents in netns; iptables
   DROP the direct path → prove WG still flows via the relay mesh_relay stream
   (`relay/mesh.go` + `mesh_derp_transport.go`). The one piece the mesh e2e
   couldn't cover. ~`test-mesh-relay-e2e.sh`.
2. **Relay HTTP-over-QUIC tunnel** — relay + 1 agent; external client →
   relay `/d/<id>/health` → agent. Reuses `relay/server_collision_test.go`
   harness (`startTestRelayQUIC`).
3. **LAN beacon discovery** — 2 agents on one docker network; verify UDP 19837
   discovery + token-fingerprint match + `classifyRemoteBaseKind`=="same-lan".
4. **Manual-peer mesh** — like mesh e2e but bypassing Convex with a static peer
   list (already effectively done by cmd/meshtest).

### D. Kill the Hetzner test box — convert cloud tests to local containers
These currently need a paid box (~€6/mo) and only health-check anyway:
- `--relay-docker`, `--relay-binary` → local privileged container + health.
- `--features-remote` → drop in favor of local `--features` + a CI arch matrix.
- `ci/remote/verify-guest-docker-isolation.sh` (security!) → local
  `--guest-isolation` category (docker-in-docker / privileged).
- `ci/remote/verify-host-share-lifecycle.sh` → local `--host-share` category.

### E. Untested subsystems worth unit/e2e coverage
relay `tunnel.go` + `mesh.go`; agent `SendHeartbeat`; MCP tool-invocation
(only initialize+tools/list covered today); voice STT streaming.

**Recommended build order:** A (the fixture) → C1 (relay-DERP, the one real
data-plane gap) → B1 (fresh-install→serve) → D (retire the Hetzner box).

## Files

- `desktop/agent/cmd/meshtest/main.go` — keygen + run harness (linux).
- `desktop/agent/mesh/*_test.go` — unit tests incl. `extreme_test.go`
  (malformed packets, port boundaries, IPv6 reject, truncated frames,
  fuzz round-trips, clamp invariants).
- `scripts/test-mesh-e2e.sh` — host orchestrator (cross-compile + docker run).
- `ci/mesh/e2e-in-container.sh` — the in-container netns test.
