# Beta redroid multi-tenancy — isolation design

**Status:** Model B chosen + isolation primitive built 2026-06-22
(`desktop/agent/redroid_tenant.go` + test). Wiring into the beta data-plane is
the remaining work.

## Goal

The shared beta box (`yaver-beta-cloud`) is usable by all beta users. They should
be able to use **redroid** (Android-in-Docker) too — **with per-user isolation so
one tenant's data never leaks to another.**

## The hard constraint

redroid must run `--privileged` to mount `binderfs`. **A privileged container can
escape to the host.** Therefore no single shared redroid, and no privileged
container on a shared host, is a *hard* sandbox against a malicious tenant. Two
models follow:

- **Model A — per-user ephemeral box (isolation by physics).** Each user's
  redroid runs on its own scale-to-zero Hetzner box (snapshot + delete when
  idle). Strongest; the only safe choice for *untrusted* users. (Matches the
  CLAUDE.md metered/scale-to-zero rule.)
- **Model B — hardened per-user container on the shared box (CHOSEN).** One
  redroid container *per tenant* with strong tenant-to-tenant **data** isolation
  + resource fairness + defense-in-depth. Cheaper. Acceptable **only because beta
  is invite-only / owner-vetted** — it does not remove the `--privileged`
  host-escape risk. Document this honestly; never sell it as a hard sandbox.

## Model B isolation contract (enforced by `RedroidTenantSpec`)

| Control | How | Why |
|---|---|---|
| Per-tenant `/data` | `-v BaseDir/<tenant>:/data` (0700), name namespaced by sanitized id | one user's apps/files/accounts invisible to another |
| Dedicated network | per-tenant bridge, `enable_icc=false` | tenants can't reach each other over docker |
| Resource fairness | `--cpus`, `--memory`, `--pids-limit` | one tenant can't starve the box |
| Defense-in-depth | `--security-opt no-new-privileges`, `managed-by`/`yaver-tenant` labels | least-privilege + sweepable |
| No host-env leak | argv built explicitly, no `os.Environ` passthrough | secrets/host config don't reach the tenant |
| Teardown-wipe | `rm -f` container + `network rm` + `rm -rf BaseDir/<tenant>` (case-glob guarded to BaseDir) | zero cross-tenant residue (operator-fleet gap C) |
| LAN/RFC1918 egress block | host-level operator-fleet jail (`access_policy.go` / `egress_proxy.go`) | tenant app can't reach the host LAN |

The honest limit (privileged host-escape) and the gating (beta-only) are stated
in the package doc comment so no caller mistakes this for a hard sandbox.

## Remaining work to wire it in

1. A per-tenant lifecycle manager (allocate on first use, idle-timeout teardown,
   cap N concurrent tenants to the box's RAM) gated behind the beta + isolation
   flags (`betaAccess.ts` `requireIsolation`).
2. A scoped per-tenant token (operator-fleet gap D) so the tenant's agent uses a
   job-scoped credential, never the owner's.
3. Reuse the studio `RedroidSurface` driver for control, pointed at the
   per-tenant container/network/volume from `RedroidTenantSpec`.
4. Optional: promote untrusted/over-capacity tenants to **Model A** automatically.

See `[[project_beta_invisible_infra_share]]` and the operator-fleet doc for the
shared-runner gaps this closes (C: teardown-wipe ✓ here; D: scoped token, TODO).
