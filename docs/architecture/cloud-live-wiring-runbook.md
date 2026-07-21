# Cloud Live-Wiring Runbook

Date: 2026-07-21
Scope: how to take the Cloud Workspace / Relay Pro stack from *compiles* to
*verified against real provider APIs*, per provider, in cost order.

Companions:

- `yaver-cloud-workspace-product-model.md` — what is sold/given/forbidden.
- `cloud-workspace-commercialization-audit-2026-07-21.md` — state + risks.
- **`cloud-secrets-and-env.md`** — every credential, where it must live, and the
  setup commands. Read this before wiring any account.
- `cloud-multiprovider-placement-architecture.md` — capacity/failover design.

---

## 0. The rule this document exists to enforce

**Inventory is not operation.** A credential that is present, a CLI that is on
PATH, a capability that is declared, a type that compiles — none of these prove
the operation works. Everything below is ordered so the *cheapest probe that can
falsify an assumption* runs first.

Two things found in a single afternoon by actually calling Hetzner, that no
amount of typechecking would ever have surfaced:

1. **`cx32` / `cx42` / `cx52` / `gex44` do not exist.** They were the default
   SKUs for `standard` / `heavy` / `build` / `gpu`. Every paid provision would
   have failed at server-create — *after* creating and paying for the volume.
   Found by `hcloud server-type list`. Fixed to `cx33` / `cx43` / `cpx51`.
2. **The orphan sweep's label selector matched nothing.** It filtered on
   `yaver=managed`; provisioning actually writes `managed=true`. The sweep would
   have reported zero orphans forever and looked healthy while being blind.
   Found by `hcloud server list -l managed=true`. Fixed.

Both are the same failure mode, and both were *in code written to prevent that
failure mode*. Assume the next one is too.

---

## 1. Cost ladder — always probe in this order

| Tier | Cost | Examples |
|---|---|---|
| **A. Read-only** | **€0** | list servers/volumes/images/IPs, `server-type list`, pricing, describe |
| **B. Reserve-and-release** | cents | reserve a primary IP / EIP, release it in the same command |
| **C. Create-and-destroy** | cents/hour | smallest SKU, delete in the same command |
| **D. Full lifecycle** | ~1 box-hour | provision → park → wake → decommission |

**Never leave tier C or D running.** Hetzner bills stopped servers too — only
**delete** stops the meter. Every write probe below is written as a single
command that cleans up after itself.

---

## 2. Hetzner — WIRED, production provider

Credentials live in Convex prod env (`HCLOUD_TOKEN`) and in the local `hcloud`
CLI context `yaver-io`.

### 2.1 Tier A — run these first, they cost nothing

```bash
hcloud context use yaver-io

# Catalog truth. THIS is what caught the cx32 bug.
hcloud server-type list -o columns=name,cores,memory,disk,architecture
hcloud location list

# Does our label convention actually select our resources?
hcloud server list -l managed=true
hcloud volume list -l managed=true
hcloud image list --type snapshot

# Reserved addresses (the new paid resource — must never accumulate)
hcloud primary-ip list -o columns=id,name,assignee_id,auto_delete
```

**Expected today:** one server `yaver-relay-free` (the public relay, legitimately
always-on), zero volumes, zero snapshots, two primary IPs both `auto_delete:yes`
and attached to that relay. **Zero orphans** — the leak mechanisms were real but
had not fired, because paid placement has barely run in owner-only preview.

**Recurring check.** Any *unassigned* primary IP, any volume with no server, or
any snapshot not referenced by a `cloudMachines` / `managedRelays` row is money
burning. That is exactly what `cloudLifecycle.reconcileProviderResources` reports.

### 2.2 Tier B — egress IP reserve/release (~€0.002)

```bash
# Reserve, confirm it exists unassigned, release. One command, no residue.
ID=$(hcloud primary-ip create --type ipv4 --datacenter fsn1-dc14 \
      --name yaver-probe-egress --label managed=true --label service=yaver-egress-ip \
      -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["primary_ip"]["id"])') \
  && hcloud primary-ip describe "$ID" -o columns=id,name,assignee_id,auto_delete \
  && hcloud primary-ip delete "$ID"
```

Verifies: `auto_delete:false` is honoured, the label convention selects it, and
release works on an unassigned address.

### 2.3 Tier C — server create with a pinned primary IP (~€0.01)

The one assumption typechecking cannot test: **a Primary IP pins the
*datacenter*, so the create must send `datacenter`, not `location`.** Sending
both, or sending `location` with an IP from another datacenter, fails.

```bash
ID=$(hcloud primary-ip create --type ipv4 --datacenter fsn1-dc14 --name yaver-probe2 \
      --label managed=true -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["primary_ip"]["id"])')
hcloud server create --name yaver-probe-box --type cx23 --image ubuntu-24.04 \
  --datacenter fsn1-dc14 --primary-ipv4 "$ID" --label managed=true
# ⇒ confirm the server's public IPv4 EQUALS the reserved address, then:
hcloud server delete yaver-probe-box
hcloud primary-ip describe "$ID"   # MUST still exist (auto_delete:false)
hcloud primary-ip delete "$ID"
```

**Pass criteria:** server IP == reserved IP; the IP survives the server delete;
release succeeds afterwards. That chain is the entire stable-egress feature.

### 2.4 Tier D — full lifecycle through Yaver

Only after A–C pass. Provision a workspace, park it, wake it, decommission it,
then re-run the Tier A listing. **The listing must come back to its starting
state.** Anything left behind is the leak this work exists to prevent.

Specifically verify:
- Wake reuses the **same** egress address (this is the whole point).
- Decommission removes server **and volume** (the old bug) **and** the reserved
  IP **and** the stale snapshot.
- A deliberately failed provision (e.g. bad SKU) leaves **no** server behind —
  that is the R1 fix.

### 2.5 Known Hetzner gaps

- `openFirewall` throws `unsupported` — Hetzner firewall management is not wired.
- `readBudgetStatus` reports credential presence only. **Hetzner has no spend
  API**, so month-to-date spend cannot be read; it can only be *estimated* from
  running resources × `/pricing`. Do not present an estimate as an invoice.
- GEX (GPU) is **not enabled on this account** — a `gpu` placement fails at
  create. That is deliberate: a loud provider error beats silently giving a
  customer a CPU box they did not buy.

---

## 3. AWS — implemented, NOT production-eligible

Needs: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`, plus
`AWS_SUBNET_ID` and `AWS_SECURITY_GROUP_ID` until network bootstrap exists.

**Probe order:**

1. **Tier A:** `DescribeInstances` / `DescribeVolumes` with our tag filter.
   Confirms SigV4 signing works at all — it is hand-rolled and has never been
   executed against the real endpoint. *Assume it is wrong until this passes.*
2. **Tier B:** `AllocateAddress` → `DescribeAddresses` → `ReleaseAddress`.
3. **Tier C:** smallest instance, confirm `serverIp` is actually returned (the
   bounded DescribeInstances poll), then terminate.

**Before eligibility:**

- [ ] VPC / subnet / security-group **bootstrap** (today they must pre-exist).
- [x] `snapshotMachine` (CreateImage → AMI) + `createMachineFromSnapshot`
      **implemented 2026-07-21**, unverified. ⚠️ Deregistering an AMI does NOT
      delete its backing EBS snapshots — reclamation must handle both.
- [ ] Verify the `<instanceState>`-scoped status parse against real XML.
- [ ] EBS attach/mount semantics for the durable-volume contract.
- [ ] Budget telemetry (Cost Explorer or a Yaver-side ledger).
- [ ] Confirm `delete-stops-compute-spend`: terminating an instance does **not**
      release the EIP or delete EBS volumes. Until reclamation is proven, AWS
      cannot declare that capability, and `selectComputeProvider` will keep
      refusing it. **Do not relax that check to "test" the adapter** — use
      `YAVER_FORCE_COMPUTE_PROVIDER=aws`, which is loud and auditable.

---

## 4. GCP — implemented, NOT production-eligible

Needs `GCP_PROJECT_ID`, `GCP_ZONE`, and an access token.

> ✅ **Credentials FIXED 2026-07-21.** A service-account JWT (RS256, `jose`) is
> now exchanged for an access token per request, with a short cache — see
> `cloudProviders/credentials.ts`. Set `GCP_SERVICE_ACCOUNT_JSON`.
> `GCP_ACCESS_TOKEN` still works but is **manual probing only**: it expires in
> ~1h and cannot refresh, so it must never be how production runs.

**Probe order:** Tier A listing (instances/disks/snapshots/addresses) → Tier B
address reserve/release → Tier C instance create.

**The specific thing to verify at Tier C:** `instances.insert` returns an
**Operation**, not an Instance. The adapter now ignores the insert response and
polls `instances.get` for the real `natIP`. Confirm `cloudResourceId` is a real
instance path and that `deleteMachine` on it actually deletes. The old code put
an *operation* URL there, so delete silently targeted nothing.

**Before eligibility:** ~~credential refresh~~ (done); network/firewall
bootstrap; ~~snapshot + wake~~ (done — captures the boot disk as a custom image
with `forceCreate`, so the wake is an ordinary create); persistent-disk
attach/mount; budget telemetry; proof that delete stops spend (disks and
reserved addresses survive instance deletion).

---

## 5. Azure — implemented, NOT production-eligible

Needs `AZURE_SUBSCRIPTION_ID`, `AZURE_RESOURCE_GROUP`, `AZURE_LOCATION`, a
bearer token, and today a pre-existing `AZURE_NETWORK_INTERFACE_ID`.

> ✅ **Credentials FIXED 2026-07-21.** Client-credentials flow against Entra ID,
> per request with a short cache. Set `AZURE_TENANT_ID` / `AZURE_CLIENT_ID` /
> `AZURE_CLIENT_SECRET`. `AZURE_BEARER_TOKEN` remains manual-probing only.

**Also unverified:** `COMPUTE_API = "2026-03-01"` is a **future-dated API version**
that has never been called. Validate it against the real ARM endpoint before
anything else — a wrong api-version fails every request.

**Probe order:** Tier A resource-group listing with the tag filter → Tier B
public-IP create/delete → Tier C VM create, confirming the NIC →
ipConfiguration → publicIPAddress walk actually returns an address.

**Before eligibility:** ~~credential flow~~ (done); VNet/subnet/NIC/public-IP
bootstrap; ~~snapshot + wake~~ (done — snapshot → managed disk → VM with the
disk ATTACHED, since Azure cannot boot from a snapshot directly; the
intermediate disk is reclaimed if VM creation fails); disk attach/detach;
budget telemetry; confirm the NSG priority change (now derived from the port)
does not collide with existing rules.

---

## 6. Inference — deliberately not wired

Per the product model: **inference is never sold**, only lent as a capped trial.
`bedrockInference` and `openaiCompatibleInference` both throw `not_wired`, and
that stays true until the trial ceilings in the audit's Phase 2/3 are real and
demonstrated *stopping* traffic. Wiring an invoke path before the caps is the
one ordering that can lose real money with no recovery.

---

## 7. Go/no-go checklist per provider

A provider may be marked `productionEligible: true` only when **all** hold:

- [ ] Tier A–D probes pass against the real API.
- [ ] `listYaverTaggedResources` returns real resources, verified by creating one
      and seeing it appear. *(A `[]` from an unimplemented method is
      indistinguishable from "nothing is leaking" — the most dangerous possible
      false green.)*
- [ ] The label selector used by the sweep matches what provisioning writes.
      **Verify by creating a resource and selecting it back.**
- [ ] Decommission provably stops spend for server **and** every satellite
      (volume, reserved IP, snapshot).
- [ ] A deliberately failed provision leaves nothing behind.
- [ ] Snapshot + wake-from-snapshot work.
- [ ] Credentials refresh without human action.
- [ ] Budget telemetry is real, or the provider is explicitly capped elsewhere.
- [ ] SKU names verified against the live catalog. **Not assumed.**

Until then the provider stays non-eligible and `selectComputeProvider` refuses
it. That refusal is a safety property, not an inconvenience.

---

## 8. Standing operational checks

**Weekly, tier A, free:**

```bash
hcloud server list -l managed=true
hcloud volume list          # any volume with no server is money burning
hcloud primary-ip list      # any unassigned IP is money burning
hcloud image list --type snapshot
```

**Cross-check** against `cloudLifecycle.reconcileProviderResources` (report-only
by design — this token can also see boxes that are **not** Yaver workspaces, so
deletion stays a human decision).

**Single point of failure to watch.** All metering and the idle-park brake run
from **systemd timers on an external box** POSTing `/crons/run` — there are no
Convex crons. If that box is deleted (and the Hetzner metered rule pushes toward
deleting boxes), billing *and* the cost brake stop **silently**. The free relay
appears to be the only always-on server in the account, which makes it the
likely host. Verify, and alarm on missing ticks.
