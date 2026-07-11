# Provisioning & remote-machine robustness — hardening distilled from operations

> This doc encodes lessons learned while actually provisioning + operating the
> single-owner/managed remote-machine flow (2026-07-11), so the *product* is
> robust for every user — not just the one case that surfaced each gap. Each item
> says what broke, the principle, and status (✅ built / ⏳ partial / ❌ backlog).

## 1. Capacity resilience — a provider being "out of stock" is normal, not fatal
**What broke:** Hetzner Ampere/arm (`cax*`) returned `resource_unavailable` across
**every** size (cax11/21/31) and **every** EU location (fsn1/nbg1/hel1) at once. The
create path tried a *single* location and died with a raw error — no fallback, no
retry, no actionable signal.
**Principle:** provisioning must try alternatives and, when everything is genuinely
out, return a *typed, retry-able* signal the UI/MCP/loop can act on.
- ✅ **Multi-location fallback + typed `errHetznerCapacity`** — `cloud_capacity.go`
  (`hetznerCreateResilient`, `hetznerLocationsFor`, `isHetznerCapacityErr`). Both
  create *and* resume (`hetznerCreateServerFromImage`) use it. Arch-aware: arm is
  EU-only, so a "us" arm request cycles EU instead of dying on `ash` (no cax there).
- ⏳ **amd64 fallback** — when arm is globally out, offer `cpx*` (amd64 usually has
  stock) with an amd64 golden image + amd64 bootstrap. Needs an amd64 bootstrap
  variant (current `ci/remote/bootstrap.sh` is arm64-only).
- ❌ **Provider-agnostic facade** — when *Hetzner* is out, fall back to another
  provider/region. The single-provider dependency is the real product risk.
- ❌ **Capacity-aware onboarding UX** — `machine_onboard` should surface
  `{status:"capacity_unavailable", tried:[...], retryIn}` (customer-friendly) and
  auto-retry, never a raw 500.

## 2. Preflight before you spend
**What broke:** provisioning failed *late* on preconditions — wrong SSH-key name
(`yaver-ci` didn't exist; the real key was `yaver-ci-relay-bootstrap`),
`HCLOUD_TOKEN` not in env, capacity out — each discovered only mid-create.
**Principle:** validate every precondition (token valid, SSH key exists, quota
available, capacity present) **before** creating a billable resource, and report
*which* precondition failed.
- ❌ **A `machine_preflight` step** inside `machine_create`/`onboard` that checks all
  of the above and returns a structured pass/fail with the exact remedy.

## 3. Validate server types against live availability
**What broke:** the plan→type map had drifted to **deprecated** `cx21/cx31/cx41`
(x86, old-gen) while the bootstrap is arm-only; and the managed plane uses `cpx51`
(amd64) while BYO/dev uses `cax` (arm) — an arch split that means one golden image
can't serve both.
**Principle:** server types are not static constants — verify them against the
provider's live `/server_types`, and keep image arch == server arch.
- ✅ **cx→cax fix + single source of truth** (`hetznerServerTypeForPlan`).
- ❌ **Reconcile the managed (`cpx51` amd64) vs BYO/dev (`cax` arm) arch split** —
  either an amd64 golden image for the managed plane, or move managed to arm.

## 4. Cost-safety must be self-contained and fail loud
**What broke:** the idle auto-off (`idleSweep`) was fully built but fired only from
a now-**deleted** external cron box, and was default-off — so "never bill me for
idle" silently wouldn't run. A cost guarantee that quietly doesn't execute is worse
than none.
**Principle:** a "don't bill the user for idle" guarantee must not depend on any
external component staying up, and its absence must be detectable.
- ✅ **Convex-native idle cron** (`crons.ts` → `idleSweepCron`), decoupled from the
  prepaid meter, enabled on prod (`YAVER_CLOUD_IDLE_ENABLE`, `_MINUTES=120`).
- ✅ **Fail-closed** — `pauseMachine` snapshots then deletes, aborts the delete if
  the snapshot fails (box never lost), no-op without `HCLOUD_TOKEN`.
- ❌ **Auto-off health check** — a check/alert that the sweep actually ran recently,
  so a broken cron surfaces instead of silently billing.

## 5. Secret hygiene under an open-source, hostile-tenant model
**What broke / verified:** `npx convex env list` prints secret **values** (footgun);
public `authLogs` let anyone read/wipe logs; the arbitrage model puts hostile
tenants on the owner's account.
**Principle:** money-tokens live only in the control plane, never on a box or in an
MCP result/log; assume the code is public (no security-by-obscurity).
- ✅ `authLogs` closed (`internal*`); ✅ `rotate-money-token.sh` (values never touch a
  transcript); ✅ machine **quota** (control-plane, owner-exempt, `provision()`-enforced);
  ✅ threat model `docs/security/arbitrage-resale-threat-model.md`.
- ❌ Relay cross-tenant scoping audit; retire the agent-side `HCLOUD_TOKEN` plane;
  per-box Hetzner firewall on egress. (P0/P1 in the threat-model gate.)

## 6. Control-plane authoritative, box never trusted
**Principle (arbitrage):** billing, quota, idle-reaping, and metering are decided in
Convex with the platform token — never trusted from the box or the MCP verb (a
rooted customer / arbitrary caller can lie).
- ✅ quota + reaper are control-plane; ⏳ meter-from-provider (not box self-report).

## 7. Onboarding friction — broker existing OAuth, don't re-login
**Principle:** the user already completed OAuth (Claude Code locally + the Yaver
mobile app). Init a box by *brokering* those sessions, not by fresh logins on the box.
- ⏳ **OAuth-sync broker** (designed in `docs/yaver-mcp-machine-onboarding.md`): mint a
  single-use bootstrap token → box self-registers (zero-touch, no OAuth on box); runner
  auth via local cred import (PC) or a one-tap mobile-brokered device-code (phone has
  the browser + Anthropic session). Creds transit box↔broker over relay/QUIC E2E, never
  Convex/MCP/logs. Builds on the shipped phone-MCP connector.

## 8. Deploy-from-local footguns
- `backend/.env.local` `CONVEX_DEPLOYMENT` is a `dev:` value; `npx convex deploy` needs
  it sourced (`set -a; source .env.local; set +a`) and then targets the project's
  **prod** deployment. env commands use `--prod`. (Documented so nobody re-learns it.)

---

**Net:** the flagship hardening from this session — capacity-resilient create/resume
(§1) + self-contained auto-off (§4) + control-plane quota (§5/§6) — is shipped. The
backlog (§1 amd64/provider fallback, §2 preflight, §3 arch reconcile, §4 health check,
§7 OAuth-sync broker) is the roadmap to a genuinely production-robust remote-machine
product.
