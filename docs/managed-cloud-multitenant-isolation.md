# Managed Cloud — Multi-Tenant Isolation (Phase 2 design)

_Status: design. Single-user ships first; this is the plan for putting
more than one paying tenant's code on shared infra without leaks._

## Threat model

A tenant gets a **full shell + SSH + the yaver agent** in their box.
Assume a tenant is **fully hostile and root inside their environment**.
The boundary must stop tenant A from reading tenant B's:

- source code / working tree
- vault values, OAuth tokens (claude/codex/opencode/yaver), GLM api-key
- env, process memory, network traffic, build caches

"No code leak" therefore means the boundary must contain a hostile
root user. A plain shared-kernel Docker container does **not** meet
this bar (one Linux LPE / container escape = full cross-tenant read).

## The hard infra constraint

Per-tenant **microVM** (Firecracker / Cloud-Hypervisor / Kata-with-VM)
is the industry answer (Fly.io, AWS Fargate/Lambda). It needs
`/dev/kvm` (nested virtualization).

**Hetzner Cloud `cpx`/`cx`/`ccx` do NOT expose `/dev/kvm`.** No nested
virt anywhere on Hetzner Cloud. Only **Hetzner Robot dedicated
bare-metal** has real `/dev/kvm`. So microVM density is **impossible
on the cpx42 we provision today** — it would require the separate
Robot provisioning path (a much larger project; see
`docs/managed-cloud-buy-to-deploy.md`).

What runs on Hetzner Cloud cpx42 (no KVM):
- plain Docker — weak boundary (shared kernel)
- **gVisor (`runsc`)** — userspace kernel, intercepts syscalls; strong
  isolation, **no KVM needed**, runs fine on cpx42. Real overhead on
  syscall/IO-heavy work (npm install, bundlers) but acceptable.

## Decision: three tiers, ship the safe one, earn the dense one

### Tier A — VM-per-tenant (DEFAULT, already built & deployed) ✅
"Multi-tenant" = many **single-tenant** dedicated cpx42 VMs. Hypervisor
isolation, trivial to reason about, zero new tech. This is **already
the production path** (`cloudMachines.provision` makes one dedicated
box per subscription). It IS multi-tenant safe today.
- Cost: 1 × €29.99/mo per tenant. No density.
- **Recommendation: this is the multi-tenant launch.** Don't gate
  launch on density tech that doesn't exist yet.

### Tier B — gVisor containers on a shared cpx42 (DENSITY, Hetzner-Cloud-compatible)
Many tenants per VM, each in a `runsc` (gVisor) container with
defense-in-depth. The only hardened option that works without KVM.
- Isolation: gVisor sentry (userspace kernel) + per-tenant user
  namespace + seccomp + dropped caps + `--no-new-privileges` +
  per-tenant network namespace (no shared bridge) + per-tenant
  dedicated volume (no shared mounts) + disk quota.
- Weaker than a VM (gVisor sentry bugs exist) but **far** stronger
  than plain Docker; acceptable for a paid preview with density.
- Cost: ~5–10 tenants per cpx42 → ~€3–6/mo per tenant. Profitable.
- Overhead: ~10–30% on syscall/IO-bound steps.

### Tier C — Robot bare-metal + Firecracker (BEST density+isolation, biggest lift)
Hetzner Robot server (has `/dev/kvm`) running Firecracker/Kata microVMs
per tenant. VM-grade isolation at container density. Requires the
Robot provisioning path + microVM orchestration. Largest project;
revisit when Tier B density economics or isolation prove insufficient.

## Implementation plan (Tier B, when density is needed)

Touch-points (all additive; Tier A stays the default/fallback):

1. **Image runtime**: install gVisor on the host in
   `buildManagedCloudInitContainer` (the runsc binary + containerd
   shim or `docker run --runtime=runsc`).
2. **Per-tenant container**: replace the single `docker run yaver`
   with one container **per tenant**:
   `docker run --runtime=runsc --userns=host-mapped
   --security-opt=no-new-privileges --cap-drop=ALL
   --network=tenant-<id>-netns --read-only
   -v /srv/yaver/tenants/<id>:/root <image>`.
3. **Orchestration**: a host-side supervisor (new
   `cloudMachines` fields: `tenantSlots`, per-tenant deviceId/token)
   that places N tenants on a box, tracks capacity, evicts on
   unsubscribe. New Convex table `managedTenants` (machineId →
   userId, slot, status) — privacy-test must forbid path/token leak
   into it (only ids + status).
4. **Network**: per-tenant netns + nft rules; no tenant can reach
   another tenant's `:18080` or the host metadata.
5. **Storage**: per-tenant subvol/quota under `/srv/yaver/tenants/`;
   never a shared mount. Vault/OAuth dirs are per-tenant volumes.
6. **Billing gate**: each tenant slot still behind
   `canProvisionManaged` (active sub or owner allowlist).
7. **Privacy test**: extend `convex_privacy_test.go`
   `fieldsWeForbidInAnyConvexPayload` for any new tenant-sync fields.

## Recommendation

- **Now / launch:** Tier A (VM-per-tenant). Already secure, already
  deployed. Multi-tenant = many single-tenant VMs.
- **When cost/density forces it:** build Tier B (gVisor) — it's the
  only hardened option that runs on the Hetzner Cloud boxes we use.
- **Only if Tier B is insufficient:** Tier C (Robot + Firecracker).
- **Never:** plain Docker multi-tenant labeled "secure".
