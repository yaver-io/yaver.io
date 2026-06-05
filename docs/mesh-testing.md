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

## Files

- `desktop/agent/cmd/meshtest/main.go` — keygen + run harness (linux).
- `desktop/agent/mesh/*_test.go` — unit tests incl. `extreme_test.go`
  (malformed packets, port boundaries, IPv6 reject, truncated frames,
  fuzz round-trips, clamp invariants).
- `scripts/test-mesh-e2e.sh` — host orchestrator (cross-compile + docker run).
- `ci/mesh/e2e-in-container.sh` — the in-container netns test.
