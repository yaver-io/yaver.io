# Yaver Cloud Box — Lifecycle + First-Class Initialization (Deep Analysis)

> Analysis date: 2026-06-08. Grounded in code, not prior docs. Where this
> doc and the code disagree, the code wins — re-grep before acting.

This is a deep analysis of three interlocking surfaces that today live in
separate corners of the app, and the case for fusing them into one
first-class **"Box"** concept on mobile:

1. **Server lifecycle** — init / stop / remove / pause / resume / snapshot
   (both Yaver-managed prepaid boxes and BYO-Hetzner).
2. **Initialization as a first-class setting** — git, codex, claude code,
   yaver-oauth, runner-auth, toolchain — currently scattered across 5+
   screens.
3. **Cloud image utilization** — golden snapshots, the build pipeline, and
   why the current managed path under-uses them.

---

## 0. TL;DR

- The **lifecycle backend is essentially complete and fail-closed**: provision
  → pause(snapshot+delete) → resume(create-from-image) → destroy, with a
  prepaid wallet, hourly metering cron, and a `dryRun`/`HCLOUD_TOKEN` gate that
  makes "live spend" an explicit owner opt-in. See §1.
- The **mobile UI exposes most of it** (ManagedCloudCard + CloudProvidersSection
  + cloud-onboarding), but with notable gaps: **GPU spin-up is stubbed**, no
  scaling, no managed-box snapshot/bake UI, BYO has no usage ledger. See §2.
- **Initialization is the real weakness**: it's *functionally there* but
  **scattered** — pairing, cloud-onboarding, runner-auth, machine-onboarding
  (git), toolchain-sync, and runner-mirror all live in different screens with
  different mental models. There is no single "is this box ready to code?"
  answer. See §3 — this is the highest-leverage fix.
- **Image utilization is bifurcated**: the *managed* provision path uses a thin
  inline bootstrap (slow, installs everything at boot), while a full
  golden-image build pipeline (`build-cloud-image.sh` → `cloud-images.json` →
  `launch_hetzner.go`) exists but the managed lane doesn't consistently read
  from it. See §4.
- On-device coding (proot/Alpine on Android, Hermes loop on iOS, Claude
  *subscription* OAuth instead of metered API) is freshly scaffolded and
  tested but not device-run. It changes what "a box" even means — the phone
  itself becomes a box. See §5.

---

## 1. Server lifecycle — what's actually built

### 1.1 State machine (backend)

`backend/convex/schema.ts:1119` (`cloudMachines`) + `cloudLifecycle.ts` +
`cloudMachines.ts` implement a full lifecycle:

```
provisioning → active → (pause) → stopping → stopped/paused
                          (resume) → resuming → active
                          (suspend on low balance) → suspended
                          (subscription end) → grace → destroy
```

Key files / behaviors:

- **Provision**: `cloudMachines.ts:create()` mints `machineId` + `machineToken`,
  builds cloud-init via `buildManagedCloudInit()` (`cloudMachines.ts:408`),
  calls Hetzner `POST /servers`. Progress phases (`creating → booting →
  installing-docker → pulling-image → starting-agent → registering →
  authorizing-runners → ready`) are POSTed back from the box and drive the
  mobile progress bar.
- **Pause / Resume**: `cloudLifecycle.ts:509 pauseMachine()` →
  snapshot-then-delete; `:561 resumeMachine()` → create-from-image + Cloudflare
  A-record rebind (`<id>.cloud.yaver.io` gets a new IP). **Fail-closed**:
  snapshot must succeed before delete (`cloudMachines.ts:686`).
- **Destroy**: `cloudMachines.ts:1651` — hosted tier `snapshotIsMandatory()`
  (user's only data copy); byok tier disposable. Cloudflare DNS cleanup +
  orphan-snapshot warning surfaced (never silently orphan a paid image).
- **Agent-side Hetzner calls**: `desktop/agent/cloud_deploy.go:849`
  (`hetznerCreateServer` cx21/cx31/cx41 × nbg1/ash), `:941`
  (`hetznerSnapshotServer`), `:962` (`hetznerDeleteServer`), plus
  `cloud_provisioners.go` registry mapping hosts → provisioners.

### 1.2 Billing / metering (prepaid wallet)

`schema.ts:1684` (`prepaidCredits`, `creditUsage`, `creditTopups`) +
`cloudLifecycle.ts:25-72` (math):

- Markup: **CPU 2×, GPU 3×** (env-overridable). Raw COGS from Hetzner monthly
  ÷ 730; live vs stopped rates tracked separately.
- `minimumReserveCents(type)` = 1-month stopped storage + 1-hour buffer → the
  system **auto-stops before hitting zero** (no crash-on-depletion).
- `meterTick` hourly cron (`crons.ts:18` → `cloudLifecycle.ts:474`) charges
  per-machine, auto-suspends live boxes below floor.
- **Safety posture**: `dryRun:true` is the cron default; absence of
  `HCLOUD_TOKEN` forces every state transition to dry-run. Going live is a
  two-flag owner decision (set token + flip dryRun). This matches the
  "free at launch, billing dormant" business stance.

### 1.3 GPU rental (separate but adjacent)

`desktop/agent/gpu_rental.go` + `gpu_autoscaler.go` + `gpu_rental_sync.go` +
`backend/convex/gpuRentals.ts`: load-based burst (Salad hourly) over a
DeepInfra serverless baseline, `voiceSafe` model tagging, privacy-safe Convex
sync (endpoint+model only, never keys). This is **wired and deployed**
(per project memory, `gpuRentals` table is live) but is a *different plane*
from the prepaid box lifecycle — worth unifying conceptually under "compute"
in the UI eventually, not now.

---

## 2. Mobile UI — what's exposed vs missing

| Capability | Managed (prepaid) | BYO Hetzner | State |
|---|---|---|---|
| Provision CPU | ✅ `ManagedCloudCard` spin-up | ✅ plan/region/repo | done |
| Provision GPU | ❌ stub (`spinUp("gpu")`, no button) | — | **gap** |
| Pause / Resume | ✅ ⏸/▶ per machine | ✅ snapshot→start | done |
| Delete / Decommission | ✅ destructive btn | ✅ permanent | done |
| Snapshot list / restore | ❌ not exposed | ✅ | **gap (managed)** |
| Bake golden image | ❌ not exposed | ✅ "Bake" | **gap (managed)** |
| Wallet / topup | ✅ packs + web checkout | n/a | done |
| Usage ledger | ✅ | ❌ | **gap (BYO)** |
| Scale (count / type) | ❌ | ❌ | **gap (both)** |
| Reboot / SSH-key rotate | ❌ | ❌ | **gap (both)** |

Primary files:
- `mobile/src/components/ManagedCloudCard.tsx` — prepaid card, wallet (8s
  poll), provision progress, pause/resume/decommission.
- `mobile/src/components/CloudProvidersSection.tsx` — BYO connect/list/stop/
  start/bake/delete/snapshots/reconcile.
- `mobile/app/cloud-onboarding.tsx` + `src/lib/managedCloudFlow.ts` — 4-step
  post-purchase setup (find_box → wait_for_box → wait_for_agent →
  mirror_runner).
- Transport: Convex `/billing/yaver-cloud/*` + `/billing/credits/*` for
  managed; agent `/ops` `cloud_*` verbs for BYO (vault token stays on agent).

**Top UI gaps, ranked by leverage:**
1. **GPU spin-up button** — the backend supports it, only the button is
   missing. Cheapest win.
2. **Managed snapshot/bake parity** — the BYO side already has the pattern;
   port it.
3. **BYO usage/cost visibility** — even a rough "this box has run N hours"
   readout.

---

## 3. Initialization as a first-class setting — the core gap

### 3.1 What exists (and where it hides)

Everything needed to make a box "ready to code" already exists as separate
flows:

| Step | Where it lives now | API |
|---|---|---|
| Yaver OAuth (account) | `AuthContext.tsx`, `auth.ts:getOAuthUrl` | `yaver://oauth-callback` |
| Device pairing/bootstrap | `app/onboarding-pair.tsx` | LAN beacon + `adoptBootstrapDevice` |
| Cloud box setup | `app/cloud-onboarding.tsx` | `runManagedCloudFlow` |
| Runner auth (claude/codex) | `settings.tsx:4700`, `quic.runnerAuthSetup` | `/runner-auth/*` |
| Runner credential mirror | `managedCloudFlow.ts` | ops `runner_auth_mirror` |
| Git creds (GitHub/GitLab) | `settings.tsx:4844`, `quic.machineOnboardingApply` | `/machine/onboarding/apply` |
| Toolchain copy | `settings.tsx:2947`, `quic.applyToolchainSync` | `/toolchain/*` |
| Vault key sync | `settings.tsx:2400` | `vaultSet` |

### 3.2 Why scattered is the problem

There is **no single answer to "is box X ready to run a coding task?"** A user
who provisions a box has to:
- go to cloud-onboarding to mirror the runner,
- go to settings → machine onboarding to apply git creds,
- go to settings → runner auth to confirm claude code is installed+authed,
- maybe toolchain-sync from another box.

Each has its own status shape (`RunnerAuthStatusRow`,
`MachineOnboardingProviderStatus`, `EnvironmentProfile`), its own screen, its
own mental model. This is exactly the "scattered" failure mode.

### 3.3 Proposal — a unified `Box` readiness model

Introduce one normalized readiness object, computed agent-side, surfaced once:

```ts
// proposed: mobile/src/lib/boxInit.ts (pure) + boxInitStore.ts (I/O)
type BoxReadiness = {
  deviceId: string;
  agent:   { online: boolean; version: string };           // existing health
  oauth:   { signedIn: boolean };                           // yaver account on box
  git:     { github: ProviderState; gitlab: ProviderState };// machineOnboardingStatus
  runners: { claudeCode: RunnerState; codex: RunnerState; opencode: RunnerState };
  keys:    { anthropic: boolean; openai: boolean; glm: boolean };
  overall: "not-ready" | "partial" | "ready";
};
```

UI: **Settings → "Boxes"** (or a per-device "Initialize" sheet) that renders
this as a checklist with inline fix buttons, each calling the *existing* API:

```
☁ mac-mini · ready
   ✓ Yaver account signed in
   ✓ Claude Code installed + authed (subscription)
   ✓ Codex installed + authed
   ✓ Git: GitHub ✓  GitLab —          [Configure]
   ✓ Keys: anthropic ✓ openai ✓

☁ hetzner-box · partial
   ✓ Agent online (v1.99.x)
   ⚠ Claude Code: installed, not authed  [Mirror from Mac] [Sign in on box]
   ✗ Git not configured                   [Configure]
```

This is **orchestration over existing primitives**, not new backend work:
- `installRunner` / `runnerAuthSetup` already exist (`quic.ts:3269`).
- `machineOnboardingStatus/apply` already exist (`quic.ts:3335`).
- `runner_auth_mirror` already exists (`managedCloudFlow.ts`).
- A single `initializeBox(deviceId, plan)` flow (mirror of
  `runManagedCloudFlow`) sequences them and reports one progress stream.

The win is **one screen, one status shape, one "make ready" button** — the
first-class setting the request is asking for.

---

## 4. Cloud image utilization — bifurcated, under-used

Two paths exist and they don't fully meet:

**Path A — golden image (fast, exists, under-wired for managed):**
`scripts/build-cloud-image.sh` provisions a VM → installs everything via
`ci/remote/bootstrap.sh` → snapshots → writes
`dist/cloud-image/<provider>-<version>-<arch>.json`. `launch_hetzner.go`
(`readHetznerSnapshot`) reads that manifest and boots **from the snapshot**
(~30s) with a fallback to vanilla ubuntu (+~3min npm install).

**Path B — inline bootstrap (slow, what managed provision uses):**
`cloudMachines.ts:buildManagedCloudInit()` generates a cloud-config that
installs Docker/Node/Go/Python (and NVIDIA/Ollama for GPU) **at first boot
every time**. This is the path the prepaid managed flow actually runs.

### Consequence

Every managed box pays the full install cost on **both** initial provision and
**every resume** (resume = create-from-snapshot, so resume is fine — but the
*first* provision and any vanilla-fallback are slow). The golden-image
pipeline that would cut cold-provision from minutes to ~30s isn't consistently
consumed by the managed lane.

### Recommendation

1. Make `cloudMachines.create()` prefer a golden snapshot ID (per region/arch,
   per `cloud-images.json`) and fall back to inline cloud-init only when no
   fresh image exists.
2. Add the golden image's version stamp to provisioning telemetry so stale
   images are visible.
3. Surface "image: golden v1.99.x / vanilla-fallback" in the provision
   progress UI — today the user can't tell which path they got.
4. Keep the pause→snapshot→resume loop (it's correct); the only fix is the
   *cold* provision source.

---

## 5. What changes the definition of "a box" — on-device coding

Freshly scaffolded (uncommitted), tested, **not yet device-run**:

- **Backend selection** (`codingBackend.ts` + `codingBackendStore.ts`): 5
  backends with auto-priority `local → subscription → anthropic → openai →
  glm`. Crucially, **Claude *subscription* OAuth outranks metered API key** —
  same model, no per-token bill.
- **Subscription transport** (`claudeSubscription.ts` + `subscriptionStore.ts`
  + `llmClaudeSubscription.ts`): reuses the desktop `claude` CLI's mirrored
  `~/.claude/.credentials.json` (`Authorization: Bearer sk-ant-oat-…` +
  `anthropic-beta: oauth-2025-04-20`), with a hard guard
  (`assertSubscriptionRequest`) that throws if an `x-api-key` ever leaks in —
  so the subscription path can never accidentally burn metered credits.
- **Android proot sandbox** (`sandbox_proot.go` + `build-android-sandbox.sh`):
  static Go agent in jniLibs wraps the runner subtree in a userspace
  proot/Alpine chroot (node/git/claude-code/codex), **env-gated** so macOS/
  Linux/CI builds are untouched (`console_terminal.go` 4-line hook).
- **iOS**: no exec → Hermes-native loop or remote PTY.

**Why it matters for this analysis**: the phone becomes a "box" too. The
`BoxReadiness` model in §3.3 should treat the local device as a first-class
box (backend = subscription/local, no git mirror needed, runner = native
loop). The initialization screen then unifies *every* compute target — phone,
LAN box, managed cloud, BYO Hetzner — under one readiness checklist. That is
the actual end-state the request ("initialization first class … all things")
is pointing at.

---

## 6. Concrete next steps (ranked)

1. **Unify initialization** (§3.3): `boxInit.ts` readiness model +
   `initializeBox()` orchestrator + one Settings → Boxes screen. Pure
   orchestration over existing APIs. *Highest leverage, lowest backend risk.*
2. **GPU spin-up button** (§2): expose the existing `spinUp("gpu")` path.
3. **Golden-image-first managed provision** (§4): prefer snapshot, fall back to
   inline; show which path ran.
4. **Managed snapshot/bake + BYO usage parity** (§2): port existing patterns
   across the two cloud surfaces.
5. **Land on-device coding** (§5): device-run the proot sandbox, wire local
   device into `BoxReadiness` as a box.

All of 1–4 reuse code that already exists; none require new Convex tables. The
work is consolidation and exposure, not invention.
