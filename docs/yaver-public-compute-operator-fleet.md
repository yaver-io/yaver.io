# Yaver Public Compute — Operator Fleet (free, on-prem, open-source)

> Status: **design / deep analysis** (2026-06-08). Grounded in a 5-agent
> code audit; file:line citations are point-in-time. First implementation
> slice (removable allocations) shipped on `main` in commit c2a3954e.
>
> Vision: Yaver (the operator) runs a small fleet of its own hardware —
> old PCs reflashed to Linux, NUCs, Raspberry Pis — and offers it as a
> **free, best-effort, multi-tenant compute tier** so newcomers can
> *experience* Yaver end-to-end (clone a repo, run a coding agent, deploy)
> **without paying for Hetzner and without bringing their own API key.**
> The inventor provides this to society. It stays fully open source.
>
> Two hard promises:
> 1. **A tenant's own data/keys never leak** — slices are isolated and
>    fully wiped on release ("removable allocation").
> 2. **Our shared GLM/z.ai key never leaks** — tenants use it through a
>    metered gateway; the key never touches a machine they can shell into.

---

## 0. TL;DR

- A **distinct run-mode** of the *same* open-source Go agent (`yaver serve
  --operator ...`), not a fork. Additive, flag-gated; personal single-owner
  behavior is untouched.
- An **operator/service identity** (not a person's account) owns the fleet,
  so a leaked box token can't drain a human's account.
- Tenants get **ephemeral, containerized, network-jailed slices** that are
  **hard-killed and wiped on release**.
- Inference is the owner's GLM key, fronted by the **Yaver Gateway** (a
  Cloudflare Worker that already exists, ~80% built, dormant). Tenants
  authenticate with a **scoped inference-only token**, funded by a small
  **free credit grant** the gateway meters and caps.

The single non-trivial prerequisite is the operator identity. Everything
else is wiring + hardening of code that exists.

---

## 1. Why this is feasible (what already exists)

One binary, flag/config-driven modes (`main.go:2096-2132`). Relevant shipped:

| Capability | Status | Where |
|---|---|---|
| Single binary, flag/config modes | EXISTS | `main.go:2096-2132` |
| `--multi-user` serving + per-user workspaces | EXISTS (caveat §2) | `main.go:3198`, `multiuser.go:41-190` |
| Container task isolation (cpu/mem caps, mount guard) | EXISTS | `container_runner.go` |
| Force-containerize fails **closed** without Docker | EXISTS (one path) | `feedback_http.go:381-389` |
| Many boxes under one account | EXISTS | `devices.ts:482` |
| Share a box (scoped, TTL, revoke) | EXISTS | `infraAccessGrants` `schema.ts:1378`; `hostShare.ts`; `httpserver.go:1751,1849` |
| Per-grant limits (cpu%, ramMb, isolation, priority) | EXISTS (fields) | `infraAccessGrants` `schema.ts:1378` |
| Relay-out (no home IP/LAN exposure) + `--no-quic` | EXISTS | `main.go:2565,2101` |
| Optional (non-mandatory) vault | EXISTS | `main.go:2257`; `vault.go:184` |
| Headless unattended bring-up | EXISTS | `buildManagedCloudInit` `cloudMachines.ts:408` |
| arm64 / Raspberry Pi first-class | EXISTS | `release-cli.yml`; `cli/src/agent-runtime.js:93` |
| Metered inference gateway (key server-side only) | BUILT, DORMANT | `gateway/src/index.ts`; `http.ts:6530,6557` |
| Prepaid wallet + per-kind meter + free-grant top-up | EXISTS | `prepaidCredits`, `managedMeter.ts`, `creditTopups.source` |
| No hardcoded agent secrets (OSS-safe) | CLEAN | audit scan negative |

Not a new build — the existing binary in a new mode plus gap-closing.

---

## 2. The four load-bearing gaps

### Gap A — No operator/service identity (the prerequisite)
Every box authenticates **as a personal `users` row** (`serve` requires
`cfg.AuthToken` `main.go:8673`; auth compares to `s.ownerUserID`
`httpserver.go:224,1971`; cloud-init bakes the provisioning user's token
`cloudMachines.ts:546`). Wrong for a public fleet: a leaked box token would
be a *person's* full token, and "owner-equivalent" is what tenants must
never be. **Build:** a dedicated **operator principal** — scoped (serve
tenants; not read a human's projects/vault/billing), rotatable, baked into
boxes instead of a personal token. Then `s.ownerUserID` = operator.

### Gap B — Non-owner can become owner-equivalent
`--multi-user` is largely a façade (`multiUserAuth`/`X-Yaver-UserID` routing
has **zero call sites** `multiuser_http.go:173`); paired tokens pass through
**as owner** (`httpserver.go:1928`). **Build:** kill paired=owner on operator
boxes; route every foreign token through the scoped containerized tenant
path (preferred over wiring per-user singletons).

### Gap C — Allocations were not removable (zero teardown) — **DONE (c2a3954e)**
Host-share had create + fast revoke-of-new-requests but **no wipe**:
no workspace delete, revoke didn't kill running processes, default terminal
was a bare host PTY with full `os.Environ()`, shared cross-tenant cache
volumes, and the cleanup cron never pruned `hostShareSessions`.
**Shipped:** `DeleteWorkspace`/`ReapExcept`/`SanitizeSessionID`
(`host_share_workspace.go`); a reaper (`host_share_reaper.go`) that fetches
active sessions for this host device, hard-kills revoked terminals
(`close()`→`process.Kill()`) and wipes stale workspaces, hooked into the 10s
`refreshGuestList` loop (no-op on normal boxes), fail-safe on fetch error;
`operatorMode` field. **Still to do for C:** per-tenant container with no
host volume / no shared caches; server-side `hostShareSessions` reaper cron.

### Gap D — Safe key path (gateway) is dormant
The owner-key-without-leak machine exists: `gateway/src/index.ts` is an
OpenAI-compatible Worker whose upstream key is a **Worker secret** (never
sent to clients); the client sends only a token. Gated by `cloudAccessAllowed`
+ fail-closed `GATEWAY_SHARED_SECRET`, ships dry-run. **Trap:** the *other*
path injects the key as an env var into the runner / `opencode.json` — coding
agents run yolo shell → `echo $GLM_API_KEY` extracts it. **Never** carry the
owner key as env for tenants. **Build:** `wrangler deploy` + `ZAI_API_KEY`
Worker secret + verify `pricing.ts` rates + ceilings; tenant runner env
`OPENAI_BASE_URL=<gateway>` + scoped inference-only token; extend
`sdk_token.go` for that scoped token; free credit grant via
`creditTopups.source="free-grant"`.

---

## 3. Recommended architecture

```
newcomer → "try a box" → ALLOCATOR picks idle owned box → scoped, TTL'd,
auto-wiping host-share grant (open-share) → box appears in their account
(cloudMachines.listForUser merges granted machines) → they connect over relay:
   • tenant task FORCE-containerized (bridge net, NO LAN route, cgroup caps,
     clean env, no host volume, no shared cache)
   • inference → Yaver Gateway with a scoped inference-only token
   • free credit grant funds the gateway quota
   • on idle/expiry/abuse → hard-kill + wipe (DONE), instant revoke
Gateway (CF Worker): owner GLM key = secret only; client sends token;
  pre-flight balance(402) + hourly cap(429); meters → managedMeter → wallet.
```

**Operator-node command (target):** `yaver serve --operator --multi-user
--max-users <n> --relay-only --containerize-tenants`, authed as the operator
principal, pointed at an operator relay, devices flagged **open-share**.

New flags (additive, default-off): `--operator`, `--relay-only` (bind direct
listeners to 127.0.0.1 — today hardcode `0.0.0.0` `httpserver.go:1316,1370`),
`--containerize-tenants` (fail-closed). `operatorMode` field already landed.

---

## 4. Security & threat model (public + on the operator's own LAN)

The boxes sit on a **home/office LAN**, so network jailing matters more than
the datacenter case. Non-negotiable before any stranger touches a box.

| Threat | Control |
|---|---|
| Read operator files / `$HOME` / git creds | Container-only tenants, clean env, no host `$HOME` mount; operator box holds no personal vault/keys (operator identity). |
| Extract the GLM key | Gateway-only, Worker secret; tenant gets a scoped token. |
| LAN pivot / box as attack relay | `--relay-only` + container `--network bridge` + **egress firewall blocking RFC1918** except the gateway; no inbound public ports. |
| Cross-tenant residue | Per-tenant container, no shared caches, no host volume; **hard-kill + wipe on release (DONE)**; reaper cron. |
| Revoked tenant keeps running | Reaper hard-kills processes/containers on revoke (DONE). |
| Resource exhaustion / mining | cgroup caps + TTL + idle timeout + `--max-users`; gateway hourly cap; allocator assigns idle only. |
| Token farming / replay | Scoped inference-only tokens, per-user free-grant budget, fast revoke. |
| Open-source ⇒ attacker reads code | Token/HMAC/cap-based; no security-through-obscurity (audit confirmed). Only on-disk secret is the operator token — scoped + rotatable (Gap A). |

**Bidirectional key promise:** tenants don't bring keys (inference provided);
a future BYO key goes into the gateway as a Worker secret or stays
client-side — never an operator box's vault/env. Our GLM key is gateway-only.

---

## 5. Free-tier economics & abuse limits

Each newcomer gets a small free credit grant (~$1–2 inference) via
`creditTopups.source="free-grant"`; the gateway enforces it (balance 402 +
hourly 429 + per-token metering). Compute is free but bounded: per-session
TTL, idle timeout, cgroup caps, `--max-users`, allocator picks idle. Spent /
expired → allocation auto-wiped; upsell = BYO Hetzner or managed credits
([[project_yaver_cloud_credits]]). The fleet is the *try-it* on-ramp.

---

## 6. Open-source posture

Everything ships public (operator mode, allocator, gateway, reaper).
Intentional and safe: no hardcoded agent secrets (audit-confirmed); security
rests on tokens, HMAC, relay passwords, cgroup/network caps. Deployment
secrets (operator token, relay password, `ZAI_API_KEY`,
`GATEWAY_SHARED_SECRET`) live only in env / Worker secrets / the operator
box's `config.json`. Anyone can run their own operator fleet — that's the
point.

---

## 7. Phased launch plan

**Phase 0 — one box safe for one tester:** operator principal (A) + bake into
a box; `--operator` forces tenant path, kills paired=owner (B-1);
`--containerize-tenants` (fail-closed) + `--relay-only` + egress firewall;
launch gateway (D) + scoped token; manual single grant to one tester. Verify
tester can clone/code/use GLM via gateway but **cannot** read host
files/keys, reach the LAN, or extract the key.

**Phase 1 — removable allocations + self-serve:** teardown (C — wipe DONE;
add per-tenant container no-shared-cache + `hostShareSessions` reaper cron);
allocator ("idle box → scoped TTL grant") + free grant + "Try a free box"
button. Cross-tenant residue test.

**Phase 2 — harden if abuse appears:** rootless Docker / bubblewrap
(`cloudMachines.ts:491-498`) → Kata/Firecracker; per-tenant OS users;
reputation / waitlist gating.

---

## 8. Non-negotiables

1. Operator identity, not a person's account. 2. Container-only tenants,
fail-closed without Docker. 3. Network jail: `--relay-only` + bridge +
egress block of RFC1918 except gateway. 4. Gateway-only inference with a
scoped token. 5. Removable allocation: hard-kill + wipe on release (DONE).

---

## Related
- [[project_public_compute_operator_fleet]] (memory)
- [[project_yaver_cloud_credits]] — prepaid wallet + meter the free grant rides on
- [[project_yaver_premium_zero_to_hero]] — resale stack this is the free on-ramp to
- [[project_normie_concierge_fair_metering]] — gateway + managedMeter + capability shelf
- `docs/yaver-gateway-spec.md` — the inference gateway already specced + skeletoned
