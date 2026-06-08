# Managed-Cloud CI Absorption — killing the GitHub/GitLab bill as a managed-cloud value prop

> **Status:** design-only, 2026-06-08. Strategy + architecture, no code written.
> **Frame:** the inverse customer to `roadmap_ci_solo_developer_lower_costs.md`.

## The gap this fills

Every CI document already in the repo optimizes for **one** persona: a solo
developer with a capable, always-on laptop who runs CI locally for **$0**.
`roadmap_ci_solo_developer_lower_costs.md` is explicit that "Hosted Yaver Cloud
SaaS" and "team CI" are **out of scope** — "the whole point is the dev's own
machine." The `yaver-test-sdk` (`yaver ci` / `yaver test`) is the embodiment of
that frame: a local-first, BYO-runner, vibe-code-your-tests loop.

This document is the **inverse customer**:

- They do **not** have always-on hardware (or don't want to babysit it).
- They **pay GitHub Actions / GitLab CI today** (metered minutes).
- You are selling them **Yaver Managed Cloud**.

For them, "$0 on your own laptop" is irrelevant — there is no laptop-runner. The
pitch is: **the managed box you're renting pays for itself by killing your
GitHub Actions bill.** CI absorption becomes a *demand driver* for managed
cloud, and the most legible piece of the resale arbitrage.

## Why CI is the best resale arbitrage Yaver has

Markup on what the managed-cloud spine already resells vs. what GitHub charges
for CI compute:

| Resold thing | Markup over raw cost |
|---|---|
| Inference (`managedMeter`) | ~1.5× |
| Compute / dev box | ~2× |
| **CI minutes (GitHub charges)** | **25–50× raw compute** |

- **GitHub Actions Linux** = $0.008/min = **$0.48/CPU-hr**.
- **Hetzner CAX21** (4 vCPU arm64) ≈ €6.49/mo ≈ **$0.009/CPU-hr**.
- GitHub is charging **~25–50× raw compute** for orchestrated minutes.

Even at a 3× managed markup, Yaver lands **~10× under GitHub** *and keeps fat
margin*. Inference resale (~1.5×) is a rounding error next to this.

**macOS is the extreme case.** GitHub macOS = $0.08/min = **$4.80/hr**, and it
is the single largest line in any mobile CI bill (the 10× multiplier). A Mac
mini (see `project_publish_macfarm`) amortizes to ~$0.15/hr → **~32× arbitrage**.
Hetzner has no Macs, so macOS is the one line that genuinely costs Yaver — and
it is exactly where the buyer's pain is largest.

### Worked example — small mobile team, 10 PRs/day

| Job | Per-run | Runs/mo | Rate | GitHub cost |
|---|---|---|---|---|
| iOS build (macOS) | 25 min | 220 | $0.08/min | **$440** |
| Android build (Linux) | 20 min | 220 | $0.008/min | $35 |
| Lint + unit (Linux) | 8 min | 220 | $0.008/min | $14 |
| **Total** | | | | **~$489/mo** |

Run the same workload on a Mac mini you own/colo + a Hetzner box: **~$1
electricity + the hardware.** A **99%+ kill** on the metered portion. The Mac
mini is the only real cost, and it is 30× cheaper than GitHub macOS minutes.

## Three delivery models (the decision)

There are exactly three architecturally distinct ways to absorb the bill. The
existing docs pursue only the hardest one.

### Model 1 — Self-hosted runner adapter  *(lowest effort, captures ~100% of the compute bill)*

Register the managed box (or the user's own box, or an operator-fleet box) as a
**GitHub Actions self-hosted runner** / **GitLab runner**. The user keeps their
`.github/workflows/*.yml` **verbatim** — they only flip
`runs-on: ubuntu-latest` → `runs-on: [self-hosted, yaver]`.

GitHub keeps everything it's good at: webhooks, matrix expansion, the checks
UI, Actions secrets, the marketplace of actions. And **GitHub bills $0 for
self-hosted-runner minutes.** This is GitHub's *own supported, documented path*,
not a hack.

- **Pro:** zero CI re-authoring. The compute *is* the bill (orchestration is
  free), so this captures essentially all the savings.
- **Pro:** keeps the entire GitHub/GitLab ecosystem the user already depends on.
- **Con:** you are *lowering the bill*, not *replacing GitHub* — if GitHub is
  down, CI is down. (On-brand: source-of-truth stays where the user wants it.)
- **Con — the security footgun:** GitHub **explicitly forbids** self-hosted
  runners on **public** repos, because fork PRs run arbitrary contributor code
  on your box. See the security section — this is the gating dependency.
- **Status:** **does not exist** in the repo (no `actions-runner` /
  `RUNNER_TOKEN`). Reserved as "Phase 4" in the solo-dev roadmap. For the
  *commercial* path it should be **Phase 1** — it is the keystone primitive.

### Model 2 — Yaver-native CI  (`yaver ci` / `yaver-test-sdk`)  *(already designed, M1–M7)*

Yaver's own runner + YAML test specs + the vibe-code-your-tests loop, local-first
and $0. A genuine differentiator (privacy, one vocabulary across web/iOS/Android,
AI-healing selectors over the same MCP). But **high re-authoring friction** for
someone who already has working Actions YAML. This is the *local-dev* story, not
the *kill-my-existing-bill* story. Keep it; don't lead managed-cloud with it.

### Model 3 — Full webhook-ingest CI control plane  *(highest effort)*

Yaver ingests push/PR webhooks directly, runs its own pipeline DSL, posts checks
back via the Checks API. This is "build a GitHub Actions competitor" — matrix,
artifacts, a logs UI, caching, the actions marketplace, the whole surface.

**Recommendation: don't.** You don't need to win the CI-product war; you need to
kill the compute bill. Model 1 does that with ~5% of Model 3's effort.

### Recommendation

**Lead managed cloud with Model 1.** Keep **Model 2** as the local/vibe-code
differentiator for devs who want it. **Skip Model 3.**

## The savings ledger — the killer UX (and it's on-brand)

`project_normie_concierge_fair_metering`'s doctrine is *reject opacity, honest
breakdown, BYO exit*. So do the honest-broker move no CI vendor can: **parse the
workflow, count minutes × GitHub's published rate (with the macOS 10× / Windows
2× multipliers), and show the delta on every invoice.**

> *This month Yaver ran 142 CI jobs. GitHub Actions would have billed **$487**.
> You paid **$34**. You saved **$453**.*

This turns the monthly invoice into a marketing asset, and it is *true*. No
competitor does this because no competitor is undercutting GitHub on its own
compute. Implementation: a small GitHub/GitLab pricing table + a workflow-minutes
parser + a `kind: "ci"` row in `managedMeter` carrying both `cost` and
`wouldHaveCostUpstream`.

## What already exists (~80%)

Confirmed by code inventory:

| Component | File(s) | Reuse |
|---|---|---|
| Runner abstraction | `desktop/agent/runner.go`, `runner_http.go` | 90% — add a `ci` job kind |
| Scheduler (cron/interval) | `desktop/agent/scheduler.go` | 100% |
| Durable job queue (retry/DLQ) | `desktop/agent/jobqueue.go` | 100% |
| Fleet dispatch (`select`/`queue`/`exec`) | `sdk/js/src/fleet.ts` | 100% |
| Secret injection | `desktop/agent/vault.go`, `vault_http.go` | 100% |
| Metering + prepaid wallet | `backend/convex/managedMeter.ts`, `cloudLifecycle.ts`, `prepaidCredits`/`creditUsage` | 95% — add `ci` kind |
| Managed runner OAuth | `runner_auth_*.go`, `docs/managed-cloud-runner-oauth-plan.md` | plan written |
| Container isolation | `desktop/agent/container_runner.go`, `docker/Dockerfile.sandbox` | 80% — orphaned from runner |
| Git provider / PR | `git_provider.go`, `git_pr.go`, `git_oauth_device.go` | 70% — no checks/status API |

## The managed-cloud-specific missing 20%

1. **GHA/GitLab self-hosted-runner adapter** (Model 1) — the keystone. Register
   box → runner; ephemeral per-job; deregister on teardown.
2. **Ephemeral per-job isolation + teardown** — `container_runner.go` exists
   (~80%, already has GitHub/GitLab-token-aware `sharedSecretEnvVars`) but is
   *orphaned* from the runner abstraction. Wire it: per-job container,
   proc-kill + cache-wipe + secret-scrub on job end.
3. **Savings ledger** — GitHub/GitLab pricing table + workflow-minutes parser +
   `ci` meter kind carrying `wouldHaveCostUpstream`.
4. **macOS CI capacity** — Mac colo / own-mini fleet (`project_publish_macfarm`).
   The one line that costs real money.
5. **Overflow scheduler** — own box busy/asleep → burst to Yaver Cloud. Fleet
   SDK `select(tags)` + `queue` already does most of this; tag boxes by
   always-on-ness (laptops sleep; Pis/Hetzner/managed don't).

Model 1 does **not** need checks/status writeback (GitHub handles it). Only
Model 3 would — so defer it.

## The blocker you cannot skip — security (operator-fleet gap C)

Managed-cloud CI means running the user's code — and on **public/fork PRs,
literally arbitrary attacker code** — on Yaver infra, frequently, in parallel,
on a box that holds deploy secrets. GitHub forbids self-hosted runners on public
repos for exactly this reason.

This is the **same threat model as the operator-fleet network jail**
(`project_public_compute_operator_fleet`), and that memory already flags
**gap C: ZERO teardown today** — no proc-kill/cache-wipe on revoke, bare host
PTY with `os.Environ`, shared caches. CI turns that latent gap into a critical
one.

> **Managed-cloud CI cannot ship until allocation teardown is closed:**
> ephemeral per-job container (wire the orphaned `container_runner`), proc-kill +
> cache-wipe + secret-scrub on job end, relay-only egress jail (block RFC1918),
> and **private repos only by default; public/fork PRs require approval + jail**.

The upside: closing operator-fleet gap C and shipping managed CI are the **same
work**. The isolation primitive is ~80% built.

## Packaging / metering

CI is **event-triggered ephemeral parallel compute**, distinct from the
persistent dev box. Bill it as a distinct `kind: "ci"` in `managedMeter`
(legible savings story) that debits the same one wallet. Tiers, honestly framed:

- **CI on your own box / operator free fleet → $0.** (Model 1 + owned/free
  hardware.) The wedge: a newcomer kills their GitHub Actions bill on day one.
- **CI burst to Yaver Cloud (Linux) → metered**, ~10× under GitHub, with the
  savings delta shown.
- **Managed macOS CI → the one premium line** (Mac colo costs real money), still
  ~10–30× under GitHub macOS minutes. Interim honest framing: *"bring your Mac
  mini, we orchestrate"* until the colo fleet exists.

This slots CI in as a **6th meter** alongside the existing five
(compute/inference/backend/web/publish) in `project_yaver_premium_zero_to_hero`,
and as another independent 1-tap capability on the `CapabilityShelf` in
`project_normie_concierge_fair_metering`.

## Strategic risks / honesty

- **You decrease the bill, you don't replace GitHub.** Say so. Source-of-truth,
  PR review, issues stay on GitHub. On-brand (BYO exit).
- **macOS in cloud is the hard, costly part.** Don't promise managed-cloud macOS
  you can't supply. "Bring your Mac mini, we orchestrate" is the honest interim.
- **Self-hosted runner security on public repos is a real footgun.** Private
  repos are the safe default; public/fork PRs need the jail + approval gate.
- **Idle-machine reliability.** Laptops sleep. Always-on Pis/Hetzner/managed
  boxes are the reliable runners. Tag by always-on-ness.
- **Don't over-build Model 3.** Resist becoming GitHub Actions.

## Phasing (commercial path)

1. **Model 1 adapter** (private repos only) + **ephemeral teardown** (= operator
   gap C) + **`ci` meter kind**. The minimum that lets a paying user point
   `runs-on: [self-hosted, yaver]` at a managed box and watch their Actions bill
   go to ~$0.
2. **Savings ledger** on the invoice. Turns it into the marketing asset.
3. **Overflow scheduler** (own box → Yaver Cloud burst) + **always-on tagging**.
4. **macOS CI capacity** (Mac colo / own-mini fleet).
5. **Public/fork-PR support** behind the full jail + approval gate.

## Implementation spec — grounded in code

Two confirmations from reading the actual source make this much smaller than it
looks:

1. **`runner.go` was *designed* for this.** Its header comment: *"Replaces the
   per-vertical SaaS runners (GitHub Actions self-hosted, EAS Build, e2b
   sandbox, Modal, Checkly, Cronitor, Devin, ...)."* It already reserves
   `RunnerJobWorkflow = "workflow" // Phase 4`, `RunnerScheduleWebhook`, and a
   `RunnerSchedule.WebhookSecret` field. The CI adapter is the **already-named
   `workflow` job kind**, not a new subsystem.
2. **`managedMeter.ts` adds a CI meter in one line.** `recordManagedUsage` is a
   generic `{kind, provider, unit, quantity, providerCostCents, ref, dryRun}`
   debit against the one `prepaidCredits` wallet; a new meter is *"a new `kind`
   string + a markup default; no new table, no new wallet."*

### A. Self-hosted runner adapter (Model 1) — `desktop/agent/ci_runner.go` (new)

**Lifecycle model: ephemeral supervisor.** GitHub's documented pattern for
untrusted/autoscaled compute is `--ephemeral` runners — a runner process
claims exactly **one** job, runs it, then deregisters. Yaver registers a
persistent *supervisor* per repo that loops: mint config → run one ephemeral
runner (in a container) → on exit, meter + re-register. One job = one ephemeral
runner = one isolated container = one `RunnerRun`.

```
CIRunnerRegistration (persisted ~/.yaver/runner/ci-registrations.json)
  Provider      "github" | "gitlab"
  Scope         "repo" | "org"
  Target        "owner/repo" | "org-name"
  Labels        ["self-hosted","yaver", ...LocalCapabilities()]  // os:linux, arch:arm64, host:*
  Isolation     "container" (default) | "host"      // host only for private+trusted
  MaxConcurrent int                                  // bounded by runnerLimiter
  Ephemeral     true                                 // never false for shared/operator boxes
```

**Reuse map (almost everything already exists):**

| Need | Reuse | Note |
|---|---|---|
| Runner package | download `actions/runner` tarball → `~/.yaver/runner/gha/<ver>/` once | mirrors how the CLI fetches the agent binary |
| Registration token | `git_provider.go` stored creds → `POST /repos/{o}/{r}/actions/runners/registration-token` | **repo scope already requested** in `git_oauth_device.go` (`["repo",...]`); only **org-level** runners need an `admin:org` scope upgrade |
| Run lifecycle + logs + UI | `RunnerStore.Start/Append/Finish` | ephemeral child's stdout → `Append`; child exit → `Finish`. Mobile/web "Runs" tab renders it free |
| Labels → `runs-on` | `LocalCapabilities()` | user writes `runs-on: [self-hosted, yaver]` |
| Concurrency cap | `runnerLimiter` (Owner 8 / Guest 2) | per-registration `MaxConcurrent` |
| Job kind | `RunnerJobWorkflow` (already reserved) | flip it from "Phase 4 reserved" to live |
| Webhook (optional, JIT) | `RunnerScheduleWebhook` + `WebhookSecret` (already reserved) | only if you later want 1-JIT-runner-per-queued-job instead of the long-poll loop |

**Secrets simplification:** in Model 1, **Yaver never touches CI secrets.**
GitHub injects repo/org Actions secrets into the job env over its own encrypted
channel at job-claim time. So `composeRunnerEnv` + vault are *not* on this path
— which is why isolation matters even more (those secrets land in the job's env
in plaintext; an un-jailed neighbor job could read them).

### B. Isolation + teardown (the gating dependency) — wire `container_runner.go`

`container_runner.go` is ~80% and already CI-aware — its `sharedSecretEnvVars`
list includes `GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_TOKEN`, and
`forbiddenMountSources` already blocks `/Users`, `/home`, `/root`, etc. It is
*orphaned* from `runner.go`. Wiring = each ephemeral runner runs inside a fresh
`SandboxVariantFat` container, and **teardown on `Finish`** does:

- kill the container + any child procs (no bare-host PTY with `os.Environ` — the
  operator-fleet gap C failure mode),
- wipe the per-job workspace + caches,
- scrub the injected-secret env.

`--ephemeral` + per-job container + teardown is the whole safety story.
**Default: private repos only; `Isolation: host` is opt-in for trusted private
work; public/fork PRs stay disabled until the jail + an approval gate ship.**

### C. Metering + the savings ledger — `managedMeter.ts`

**One-line meter add** (markup table; CI compute COGS is so far below GitHub's
anchor that 2× parity-with-compute still lands ~10–20× under GitHub):

```ts
const MARKUP_BY_KIND = { inference:1.5, backend:2, web:2, publish:1.3, compute:2,
  ci:2 };   // ← add
```

**One debit per run, on `Finish`** (the agent calls this via the existing
gateway → `recordManagedUsage` path):

```
recordManagedUsage({
  kind:"ci", provider:"yaver-cloud"|"self-hosted"|"operator-fleet",
  unit:"cpu-min"|"mac-min", quantity: durationMin,
  providerCostCents: cogs(where, durationMin),   // 0 on own/operator HW → free
  ref: runId, dryRun: <gateway flag>,
})
```

`cogs(where)` carries the Linux-vs-Mac difference so one `ci` kind suffices:
own box / operator fleet → **0** (free, but still logged for the ledger);
Yaver-Cloud Linux → Hetzner rate (~$0.00015/CPU-min); Yaver-Cloud macOS →
colo rate (~$0.0025/min). The per-user `userOptedIntoKind` gate +
`YAVER_MANAGED_METER_LIVE` global already fail-closed to `dryRun`, so CI is
simulated-only until the user opts the `ci` capability in on the CapabilityShelf.

**The savings ledger = one new non-secret field.** Add
`wouldHaveCostUpstreamCents` to the `managedUsage` insert, computed at the
call site as `durationMin × upstreamPerMin(runner_os)` using GitHub's published
per-minute prices (Linux $0.008, **macOS $0.08**, Windows $0.016 — the
multipliers are already baked into those per-min numbers). Then:

```
savings = Σ wouldHaveCostUpstreamCents − Σ chargedCents
```

rendered on the invoice. The field is a pure counter (non-secret), so the only
other change is adding `"wouldHaveCostUpstreamCents"` to the `managedUsage`
allowlist in `convex_privacy_test.go` (line ~650, alongside `chargedCents`,
`providerCostCents`) and the optional column in `schema.ts` `managedUsage`.

### D. Worked pricing — the mobile team from above, on the wallet

| Where CI runs | quantity | COGS (cents) | charged @2× | GitHub would charge | shown saving |
|---|---|---|---|---|---|
| Own Mac mini (macOS) | 5,500 min | 0 | **$0** | $440 | **$440** |
| Own/Hetzner (Linux) | 4,840 min | ~73¢ | **~$1.46** | $49 | ~$48 |
| **Total/mo** | | | **~$1.46** | **$489** | **~$487** |

The invoice line writes itself: *"Yaver ran your CI for **$1.46**. GitHub
Actions would have billed **$489**. You saved **$487**."* — and it's debited
honestly against the same `prepaidCredits` wallet as every other managed meter.

## Surfaces (agent + web + mobile), 2026-06-08

Wired across all three surfaces via the self-registering ops verbs (web/mobile
drive them over the relay/LAN, mirroring the `arm` feature — no central-router
edit):

- **Agent verbs:** `ci_runner_register` / `_list` / `_remove` / `_status`,
  `ci_workflow_scaffold`, `ci_workflow_targets` (`ops_ci.go`). Workflow
  scaffolder `ci_workflows.go` generates `runs-on: [self-hosted, yaver]`
  pipelines for **test / npm / testflight / play-internal** (TestFlight pins
  `os:darwin` — run the 10× macOS build on your own Mac for $0; Play pins
  `os:linux`), returning the YAML + the GitHub Actions secrets to set.
- **Web:** `web/components/dashboard/CIRunnerView.tsx` + route
  `web/app/dashboard/ci/page.tsx` (`/dashboard/ci`). Device picker → register
  form (provider/repo/scope/isolation/where) → registrations list+remove →
  **savings ledger** ("GitHub would have billed $X, saved $Y") → workflow
  scaffold (preview/write). `tsc --noEmit` = 0.
- **Mobile:** `mobile/src/lib/ciClient.ts` + screen `mobile/app/ci.tsx`
  (route `/ci`). Same flow, RN. `tsc` clean for these files.

Both UIs are standalone routes (same bar as the shipped `arm` feature — not yet
in a launcher menu).

## Build status (2026-06-08) — FULLY WIRED (agent + backend), uncommitted

The Model 1 adapter is end-to-end wired and unit-tested. The only parts not
exercisable in-session are the live forge job (needs a real repo + admin token +
a long-poll) and live billing (deliberately dormant — `YAVER_MANAGED_METER_LIVE`
off + per-user `ci` opt-in). `go build ./...` = 0; 14 new Go tests + the privacy
guard pass; `npx convex codegen` = 0.

**Agent (`desktop/agent/`):**
- `ci_selfhosted_runner.go` — both seams REAL now (no stubs):
  - **SEAM 1** `mintRunnerRegistrationToken` → `githubRegistrationTokenURL`
    (repo/org/GHES) + `fetchRegistrationToken` (real POST, token via
    `detectGitHubToken`/`detectGitLabToken`). GitLab needs a numeric project_id
    (honest error otherwise).
  - **SEAM 2** `runEphemeralRunner` — both providers wired:
    - **GitHub** → `githubRunnerDownloadURL` + `ensureGitHubRunnerExtracted`
      (download+untar once) + `ghRunnerConfigArgs` (`--ephemeral --unattended`) +
      host-mode (`runGitHubRunnerOnHost`) and container-mode
      (`runGitHubRunnerInContainer`, mounts the runner into the sandbox image).
    - **GitLab** (`ci_gitlab_runner.go`) → `gitlabRunnerDownloadURL` +
      `ensureGitLabRunner` (download the binary once) + `gitlabRunnerRunArgs`
      (`run-single --max-builds 1` — GitLab's one-shot ≈ GitHub `--ephemeral`);
      host→`shell` executor, container→`docker` (default `alpine:latest`).
    - per-run work dir wiped on return (teardown).
  - `CIManager` singleton (Register/Unregister/ResumeAll/Status) + per-reg
    `CISupervisor` ephemeral loop, sharing the HTTPServer `RunnerStore` so CI
    runs surface in the Runs tab.
  - `defaultCIMeter` → local savings ledger `~/.yaver/runner/ci-savings.jsonl`
    (`readCISavingsSummary` aggregates it) + best-effort
    `managedMeter:recordCIUsageFromAgent`.
- `ops_ci.go` — self-registering verbs `ci_runner_register` / `_list` /
  `_remove` / `_status` (web/mobile/CLI drive via callOps; no httpserver edit).
- `main.go` — one boot line: `go resumeCIRunnersOnBoot(ctx, httpServer.ensureRunnerStore())`.
- `ci_selfhosted_runner_test.go` — 14 tests (savings/COGS math, download URL,
  registration-token URL, label dedup, forge URL, store CRUD+defaults,
  `fetchRegistrationToken` via httptest, config args, numeric/shell-join).
- `convex_privacy_test.go` — `TestCIUsageAgentPayload_isCounterOnly` pins the
  meter payload to non-secret counters.

**Backend (`backend/convex/`):**
- `managedMeter.ts` — extracted `applyManagedUsage` shared core; `ci: 2` markup;
  new PUBLIC `recordCIUsageFromAgent` mutation (resolves user via `resolveUser`,
  pins `kind:"ci"`, debits the one wallet, dormant until live).
- `schema.ts` — `managedUsage.wouldHaveCostUpstreamCents` (optional).

**Gap-C teardown — per-run sandbox DONE** (`ci_jail.go` + `ci_pgroup`/exec
helpers): every CI run now (1) runs in its own process group and SIGKILLs the
whole group on exit so orphaned daemons can't survive on a shared box
(`TestCIProcessGroupReaping`); (2) in container mode runs with `--cap-drop ALL
--security-opt no-new-privileges --pids-limit --memory --rm`
(`ciDockerHardeningArgs`); (3) wipes the per-run work dir + scrubs the runner's
on-disk registration creds (`scrubGitHubRunnerCreds`). Also fixed a real hang:
`streamCmdToRun` now uses an `os.Pipe` so a job spawning a background daemon
doesn't block `cmd.Wait()` forever.

**Network jail — BUILT** (`ci_jail_setup.go` + `ci_jail_setup`/`_status`/
`_teardown` ops verbs): creates a dedicated docker bridge (`yaver-ci-jail`) and
installs `DOCKER-USER` iptables rules that DROP jail-subnet → RFC1918 /
link-local / CGNAT egress, so a jailed job can reach the public internet
(npm/github) but **not the operator's LAN**. Persisted via a marker so container
CI runs auto-join it. The docker-network lifecycle is verified live on macOS
(`TestCIJailNetworkLifecycleLive`); the iptables firewall is Linux-only and not
yet exercised on a real Linux box (the rule builders are unit-tested).

**Remaining to flip on for a paying user:** (1) flip `YAVER_MANAGED_METER_LIVE`
+ surface the `ci` capability on the CapabilityShelf (launch/billing call); (2)
verify the iptables firewall on a real Linux operator box; (3) a fork-PR approval
gate (needs webhook ingestion — Model 3). Both GitHub and GitLab private-repo
host/container paths are complete; the GitHub path is verified live on a real
registered box.

### Earlier slice notes

- **`desktop/agent/ci_selfhosted_runner.go`** (new) — Model 1 adapter:
  `CIRunnerRegistration` + persistent `CIRegistrationStore`
  (`~/.yaver/runner/ci-registrations.json`) + `CISupervisor` ephemeral loop +
  the savings-ledger math (`githubActionsUpstreamCents`, `ciCogsCentsPerMin`) +
  the `CIMeterFunc` debit seam. Reuses verified `runner.go` symbols
  (`RunnerStore.Start/Append/Finish`, `RunnerJobWorkflow`, `runnerLimiter`,
  `LocalCapabilities`) and `ci.go`'s `CIProvider`. The two OS seams
  (`mintRunnerRegistrationToken`, `runEphemeralRunnerContainer`) are explicit
  `errCINotWired` stubs so the supervisor idles cleanly instead of busy-looping.
  **`go build ./...` → exit 0.**
- **`backend/convex/managedMeter.ts`** — added `ci: 2` markup +
  `wouldHaveCostUpstreamCents` arg, conditionally inserted into `managedUsage`.
- **`backend/convex/schema.ts`** — `managedUsage.wouldHaveCostUpstreamCents`
  (optional). **`npx convex codegen` → exit 0.**
- **`desktop/agent/convex_privacy_test.go`** — whitelisted the new non-secret
  field. **`TestPrepaidWalletFields_AreNotConvexForbidden` → PASS.**

**Two pre-existing CI files found (don't duplicate):**

- **`ci_runner.go`** is **Yaver-native CI (Model 2), already built**:
  `.yaver/ci.yaml` (steps/image/onFail) → `docker run -v dir:/workspace` per
  step → persisted runs → `/ci/run`, `/ci/runs`, `/ci/config`. Confirms Model 2
  exists in basic form; note it uses a raw bind-mount (no `container_runner.go`
  hardening) + no metering/teardown — would need the same isolation work for
  managed-cloud use.
- **`ci.go`** has reusable forge plumbing for Model 1's SEAM 1:
  `triggerGitHubWorkflow`, `listGitHubWorkflowRuns`, `triggerGitLabPipeline`,
  `uploadGitHubRelease`, `getVaultToken`, `detectRepoFromGit`,
  `CIProvider`/`CIGitHub`/`CIGitLab`.

**Remaining to make it live:** the two seams — `mintRunnerRegistrationToken`
(wire to `ci.go` creds + the forge runner-registration API) and
`runEphemeralRunnerContainer` (wire to `container_runner.go` `SandboxVariantFat`
+ download/run `actions/runner --ephemeral`) — plus the supervisor's HTTP/ops
surface and start-on-serve wiring, and the agent→Convex `recordManagedUsage`
call behind `CIMeterFunc`. Teardown-on-Finish == operator-fleet gap C.

## Related

`project_public_compute_operator_fleet` (gap C is the shared blocker) ·
`project_yaver_premium_zero_to_hero` (CI = 6th meter) ·
`project_normie_concierge_fair_metering` (savings-ledger honesty, CapabilityShelf) ·
`project_yaver_cloud_credits` (prepaid wallet CI debits) ·
`project_publish_macfarm` (macOS capacity) ·
`docs/roadmap_ci_solo_developer_lower_costs.md` (the local-first inverse) ·
`docs/managed-cloud-runner-oauth-plan.md` (runner auth onto a managed box).
