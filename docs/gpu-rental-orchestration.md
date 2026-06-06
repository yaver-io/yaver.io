# GPU-rental orchestration in Yaver (Salad / DeepInfra / hourly GPU)

Deep analysis for: *manage rented GPU/inference compute from Yaver — wire my
Salad/DeepInfra account, change GPU type, and run a production call-center
dispatcher on a cheap always-on box that bursts to hourly/serverless GPU.*

> Source of truth is code. Every route/flag/field below is grepped from the
> agent + backend on 2026-06-06; if a name drifts, the code wins. No changes are
> proposed to the call-center repo (`e-back`) — only to Yaver.

Driving use case: `e-back/call-center` — a realtime voice agent
(VAD → ASR → LLM → TTS, OpenAI-compatible endpoints, `< 800 ms` TTFT budget).
Today it points at DeepInfra remote via two env vars. It is already detected and
runnable as a Yaver **companion** project. See `docs/serverless-companion-audit.md`.

---

## Status (2026-06-06) — SHIPPED end-to-end

P0–P5 implemented, unit-tested, committed + pushed to `main` (`67b98579`); Convex
`gpuRentals` table deployed to PROD; and the driving app (`e-back/call-center`)
now exposes the `/metrics` contract the dispatcher polls (committed on its
`call-center` branch). Build + `go vet` clean; focused tests green; `/metrics`
verified live (`{"concurrency":0,"p95TtftMs":0,"samples":0}`).

### End-to-end runbook (the call-center)

```bash
# 1. Connect the GPU/inference accounts (BYO keys, vault-encrypted)
yaver account connect deepinfra --fields '{"token":"<deepinfra-key>"}'
yaver account connect salad     --fields '{"token":"<salad-key>"}'

# 2. Bind the always-on serverless baseline into the app's vault project
#    (the companion service reads these as DEEPINFRA_BASE_URL/_API_KEY/LLM_MODEL)
yaver ops gpu_bind --provider deepinfra \
  --model nvidia/NVIDIA-Nemotron-3-Super-120B-A12B --project callcenter

# 3. Run the call-center backend as a companion service (reads vault: callcenter)
#    so it serves /health, /metrics, and the VoIP gateway.

# 4. Start the dispatcher: poll /metrics, burst Salad on sustained load, reap on idle
yaver ops gpu_autoscale_start \
  --organization <salad-org> --project <salad-project> \
  --metricsUrl http://localhost:8809/metrics \
  --gpuClass a100-80gb --burstAt 20 --reapAfterSec 300 --bindProject callcenter

yaver ops gpu_autoscale_status      # watch state: baseline → bursted → draining
yaver ops gpu_autoscale_stop --key callcenter
```

DeepInfra serverless is always the baseline (no cold-start dead air); Salad
bursts in only when sustained concurrency justifies it, drains gracefully (no
dropped calls), and is reaped to stop the hourly bill.

| Phase | Status | Where |
|---|---|---|
| P0 providers | ✅ | `accounts.go` (salad, deepinfra, runpod, vast) |
| P1 app binding | ✅ | `writeInferenceBinding` + `gpu_bind` verb (vault project the companion reads) |
| P2 catalog + DeepInfra | ✅ | `gpu_rental.go` (`gpuRentalCatalog`, `VoiceSafeModel`, `provisionDeepInfra`), `gpu_plans` verb |
| P3 Salad + scale-gpu | ✅ | `provisionSalad` + registry; `scale` verb GPU/rebind branch; `gpu_status`/`gpu_destroy` verbs |
| P4 autoscaler | ✅ | `gpu_autoscaler.go` (state machine + `liveGPUBurstBackend`), tested with fake backend/clock |
| P5 Convex + privacy | ✅ | `schema.ts` `gpuRentals` table + `gpuRentals.ts`; `gpu_rental_sync.go` payload builder pinned by privacy test |
| P6 managed metered | ⏸ deferred (post-YC) | reuses dormant `canProvisionManaged` gate |

Tests: `gpu_rental_test.go`, `gpu_autoscaler_test.go` (real httptest servers for
Salad/DeepInfra; `$HOME`-isolated vault; privacy walker on the sync payload).
Remaining wiring: run the autoscaler loop on a real dispatcher box (scrape the
gateway `/metrics`, call `Tick`), and deploy the Convex schema (`npx convex
deploy`) when bookkeeping is wanted. Web/mobile render the new providers
automatically via the generic accounts list.

## 0. TL;DR

- **The substrate is ~70% there.** Vault, accounts manager, companion compute
  (durable services + crons, env-from-vault injection), cloud provisioner
  registry, the `scale` verb (already models a `gpu` field), and a privacy-clean
  Convex bookkeeping layer all exist.
- **The real gaps are three, and only three matter:**
  1. **GPU/inference provider accounts** — Salad, DeepInfra, RunPod, Vast are
     not in `accounts.go`. "Salad" exists today only as a *string* in the
     coding-runner lane, not as a provisionable provider.
  2. **A GPU provisioner + GPU-type change** — the provisioner registry is
     Hetzner-only; the `scale` verb's `gpu` field has no implementation.
  3. **The dispatcher/autoscaler** — a cheap always-on box that owns the app and
     *bursts* hourly/serverless GPU on demand, injecting the resulting endpoint
     into the app's runtime env. This is genuinely new.
- **One conceptual correction drives the whole design (Section 2):** Yaver's
  existing `runner-provider` "Salad lane" serves *coding agents*. The call-center
  needs *application-runtime inference*. They share a vault namespace pattern but
  are different planes. Don't bolt the call-center onto the coding-runner lane.

---

## 1. What exists today (grounded inventory)

| Capability | Where | Reuse verdict |
|---|---|---|
| Encrypted secret storage (NaCl + Argon2id, keychain-derived, never synced to Convex) | `desktop/agent/vault.go`, `vault_http.go` | **Reuse as-is** for Salad/DeepInfra keys + app inference config |
| Provider account manager (AES-GCM, `account_connect/list/status/disconnect`) | `desktop/agent/accounts.go:23-65`, `mcp_tools.go` | **Extend** — add GPU/inference providers |
| Cloud provisioner registry (provider-agnostic facade) | `desktop/agent/cloud_provisioners.go:39-46`, `cloud_deploy.go` | **Extend** — add a Salad provisioner |
| Cloud lifecycle verbs: `provision` / `scale` / `destroy` (`scale` already has a `gpu` string field + `gpu-4000`-style plan ids) | `desktop/agent/ops_cloud.go:38-81,449-462` | **Implement** the GPU path behind `scale` |
| Managed-cloud machine specs incl. a GPU SKU (`gex44`, RTX 4000, 20 GB VRAM) | `backend/convex/cloudMachines.ts:9-38` | **Extend** with marketplace SKUs |
| Convex cloud rows: `cloudMachines` (`provider`, `cloudResourceId`, `machineType`, snapshot meta) | `backend/convex/schema.ts`, `cloudMachines.ts` | **Extend** with `gpu`/usage fields |
| Managed vs BYO split, fail-closed LemonSqueezy gate (dormant pre-YC) | `backend/convex/subscriptions.ts::canProvisionManaged`; `ops_cloud.go` | **Reuse** — BYO first; metered billing later |
| **Companion compute**: durable services (OS units, reboot-safe) + crons (`companion_http` verb via scheduler Verb-mode), **env-from vault + dotenv injection** | `desktop/agent/companion.go:151-159,530-555`; `companion_http` handler; `backend/convex/companion.ts` | **Reuse — this is the call-center's home** |
| Coding-runner provider lane (base-URL + key from vault `runner-provider`, protocol-correct env injection, `/runner-provider/preflight`) | `desktop/agent/provider_keys.go`, `runner_provider_http.go` | **Reference pattern, not the vehicle** (see §2) |
| On-prem / air-gap / Salad-hosted-model design | `docs/yaver-onprem-airgap.md` | Prior art; this doc extends it from *coding* to *app inference + GPU lifecycle* |

What does **not** exist anywhere (grepped): no Salad/DeepInfra/RunPod/Vast
provisioner, no GPU machine catalog beyond one Hetzner GPU SKU, no per-hour GPU
burst/autoscale, no application-runtime inference-endpoint binding, no
inference-cost accounting.

---

## 2. The load-bearing distinction: two inference planes

The exploration (and the on-prem doc) conflate two things that must stay
separate:

### Plane A — Coding-runner inference (already solved)
*"Run Claude Code / Codex / OpenCode against a self-hosted or Salad-hosted
model instead of the dev's subscription."*

- Vehicle: vault project `runner-provider` (`BASE_URL`, `API_KEY`,
  `BASE_URL__<runner>`), injected at **runner spawn** as `ANTHROPIC_BASE_URL` /
  `OPENAI_BASE_URL` (`provider_keys.go::runnerProviderEnv`).
- Audience: the *agent that writes code*. Latency-insensitive (seconds fine).
- Status: built + tested (`docs/yaver-onprem-airgap.md`, memory
  `project_onprem_gaps_built`).

### Plane B — Application-runtime inference (the call-center need; NOT solved)
*"My deployed app calls LLM + ASR + TTS on every user turn, under a sub-second
budget, and I want Yaver to supply and manage those endpoints."*

- Audience: the *running product* (`e-back/call-center`), not an agent.
- Latency: hard realtime. TTFT `< 800 ms`, `> 25-40 tok/s`, barge-in. Reasoning
  models and giant context are disqualified
  (`e-back/call-center/deepinfra-model-analysis.md`).
- Separable workloads: LLM, ASR (Voxtral), TTS — each can scale/route
  independently and be priced differently (per-token vs per-minute-audio).
- Binding mechanism: the app reads `DEEPINFRA_BASE_URL`, `DEEPINFRA_API_KEY`,
  `LLM_MODEL`, `ASR_BASE_URL`, `TTS_URL` from **its own env**
  (`e-back/call-center/src/config.ts`, `.env.example`).

**Design consequence:** Plane B does *not* go through the coding-runner lane. It
goes through **companion compute**: the app runs as a companion service, and
Yaver injects the inference endpoints into the service env via
`CompanionEnvSource{Vault: ...}` (`companion.go:130-159,539-555`). The GPU
provisioner produces the endpoint URL+key; the dispatcher writes them into the
vault project the companion reads. That is the seam that ties everything
together.

---

## 3. Target architecture

```
                        ┌─────────────────────────────────────────────┐
                        │  Yaver control plane (your laptop / phone)   │
                        │  account_connect salad|deepinfra (vault)     │
                        │  cloud_plans / cloud_provision / scale (gpu) │
                        └───────────────┬─────────────────────────────┘
                                        │ relay / direct
                                        ▼
   ┌──────────────────────────────  Dispatcher box (Hetzner CPU, always-on, cheap)  ──────────────────────────────┐
   │                                                                                                                │
   │  Yaver agent (companion engine)                                                                                │
   │   ├─ companion service: call-center backend (durable OS unit)   reads vault → DEEPINFRA_BASE_URL / LLM_MODEL   │
   │   │      VoIP gateway :8810  (VAD → ASR → LLM → TTS, barge-in)                                                  │
   │   ├─ companion cron: scale-to-load / idle-reaper (companion_http verb)                                          │
   │   └─ GPU autoscaler (NEW): watches concurrency/latency → provisions/destroys GPU → rewrites vault endpoint     │
   │                                                                                                                │
   └───────────────┬───────────────────────────────────────────────┬────────────────────────────────────────────┘
                   │ burst (hourly)                                  │ serverless (per-token)
                   ▼                                                 ▼
        ┌─────────────────────────┐                      ┌──────────────────────────┐
        │  Salad container group   │                      │  DeepInfra serverless    │
        │  RTX 4090 / A100 / H100  │                      │  OpenAI-compatible API   │
        │  vLLM + Voxtral + TTS    │                      │  (no machine to manage)  │
        │  /v1 OpenAI-compatible    │                     │  baseline + spillover    │
        └─────────────────────────┘                      └──────────────────────────┘
```

**Why a dispatcher box instead of pointing the app straight at Salad?**
- Salad/serverless GPU is the *expensive* tier; an always-on Hetzner CPU box
  (~€/mo) hosts the cheap, stateful, latency-tolerant parts (VoIP gateway, VAD,
  session state, CRM tool calls, the autoscaler) and only rents GPU when call
  volume needs it.
- The app's mid-call sessions are stateful (`CallSession` per call); a dispatcher
  lets GPU swap underneath without dropping calls (graceful endpoint cutover for
  *new* turns; in-flight turns finish on the old endpoint).
- This is exactly the "primary box + companion + burst" shape Yaver already
  models — the autoscaler is the only new piece.

---

## 4. The three gaps, designed

### Gap 1 — GPU / inference provider accounts

Add to `desktop/agent/accounts.go` (`AccountProvider` enum + `AccountProviders()`):

```go
ProviderSalad     AccountProvider = "salad"      // GPU container marketplace
ProviderDeepInfra AccountProvider = "deepinfra"  // serverless inference
ProviderRunPod    AccountProvider = "runpod"     // optional: hourly pods
ProviderVast      AccountProvider = "vast"       // optional: spot market
```

All four are `AuthType:"token"`, `Fields:["token"]` — same shape as Hetzner/Neon.
Metadata: SignupURL + TokenURL. This lights up `account_connect salad`,
`account_list`, `account_status`, `account_disconnect` for free (MCP + CLI +
web/mobile account UI), and stores the key AES-GCM at `~/.yaver/secrets`.

Read at request time only via the existing `accountField(ProviderSalad,"token")`
helper — never persisted into a payload, never to Convex (privacy contract).

> Distinction from the coding-runner lane: `runner-provider` vault entries point
> *agents* at a model. These provider **accounts** are for Yaver to *provision
> and bill* GPU on the user's behalf, and to mint the **app** inference config.
> Both can coexist; they store different things.

### Gap 2 — GPU provisioner + GPU-type change

**a) Provisioner registry** (`cloud_provisioners.go:39-46`): add
`HostSalad: provisionSalad`. Salad's primitive is a *container group*, not a VM:

```go
func provisionSalad(name string, opts map[string]string) (*ProvisionResult, error) {
    token := accountField(ProviderSalad, "token")
    // opts: gpuClass ("rtx4090"|"a100"|"h100"), image (vLLM/Voxtral/TTS),
    //       replicas, region, modelId, port
    // POST Salad container-group create → returns group id + access domain
    // ProvisionResult{Provider:"salad", Resource:"container-group",
    //   ID:<groupId>, ConnectionString:"https://<domain>/v1",
    //   Details:{gpu, model, replicas, hourlyEstimate}}
}
```

DeepInfra is serverless — there is no machine to create. Model it as a
*binding* provisioner: validate token + model via `GET /v1/models`, return
`ConnectionString:"https://api.deepinfra.com/v1/openai"` + the model id. It
never appears in `cloudMachines` as a running box; it's an endpoint record.

**b) Catalog** (`cloud_plans`): make the plan enumerator provider-aware. Today
`availablePlans` (`cloud_deploy.go:107-120`) is a hardcoded CPU list. Add a
`HostSalad` branch that returns the GPU class catalog (live from Salad API, or a
pinned table) with hourly price, VRAM, and a **`voiceSafe`** flag derived from
the model-selection rules in `e-back/.../deepinfra-model-analysis.md` (MoE /
small-dense = voice-safe; reasoning / >70B dense = batch-only).

**c) "Change GPU/PC type"** = the existing `scale` verb (`ops_cloud.go:65-81`),
whose payload **already has** `cpu`, `ramGb`, and `gpu string` (line 42). Wire
`opsScaleHandler` (currently a hint, line 449-462) to a provider switch:

```go
switch host {
case HostSalad:     // resize container group: change gpuClass / replicas
case HostDeepInfra: // pure model swap — zero machine, just rewrite the binding
case HostHetzner:   // existing snapshot → new size → cutover → destroy old
}
```

For Salad, "change GPU type" = recreate the container group at the new class and
flip the endpoint (Salad has no in-place GPU resize). For DeepInfra it's a no-op
on infra — just change `modelId`. Either way the handler ends by **rewriting the
vault endpoint the companion reads** (the §4-Gap-3 seam), so the app picks up the
new backend on its next turn.

**d) Destroy** reuses the snapshot-before-delete safety in
`mcpCloudDestroy` (`cloud_provisioners.go:90-134`) — for Salad there's nothing to
snapshot (stateless inference), so pass through the existing `skipSnapshot` path;
for hourly pods with state, snapshot first.

### Gap 3 — The dispatcher / GPU autoscaler (the new component)

This is the only genuinely new subsystem. It lives on the dispatcher box as a
companion-managed loop (or a small always-on service). Responsibilities:

1. **Observe load.** Concurrency + latency from the VoIP gateway (calls in
   flight, rolling TTFT). The gateway already tracks per-turn timing; expose a
   tiny `/metrics` the autoscaler scrapes locally.
2. **Decide.** Policy: keep DeepInfra serverless as the always-available
   baseline (no cold-start risk, per-token); when concurrency crosses a
   threshold *and* sustained, provision a Salad GPU group (cheaper per-call at
   volume) and shift new sessions to it. Below threshold for N minutes →
   destroy the Salad group (idle reaper).
3. **Act, idempotently.** `cloud_provision`/`destroy` are minutes-long and
   already streamable; drive them through the **scheduler in Verb-mode** (the
   same path companion crons use — `companion_http`/ops verbs), so actions are
   reboot-durable and recorded in `ScheduleRun` history with `OpsCode`.
4. **Rebind.** On every provision/destroy/scale, write the winning endpoint into
   the vault project the call-center companion reads:
   `DEEPINFRA_BASE_URL`, `DEEPINFRA_API_KEY`, `LLM_MODEL` (and `ASR_BASE_URL` /
   `TTS_URL` if those move to the GPU box). The companion service reads vault on
   (re)start; for hot rebind without a restart, the call-center already supports
   per-turn config — endpoint changes apply to the *next* session, in-flight
   calls finish on the old one. No dropped calls.

**Why this maps cleanly onto existing primitives:** provisioning = existing
verbs; durability = scheduler Verb-mode + companion OS units; config delivery =
`CompanionEnvSource{Vault}`; cost guardrails = idle reaper cron. The autoscaler
is glue + policy, not new infra.

---

## 5. Convex + billing

Extend, don't redesign (`backend/convex/schema.ts`, `cloudMachines.ts`):

- `cloudMachines`: already has `provider` + `cloudResourceId`. Add optional
  `gpuClass`, `endpoint` (domain, **not** a secret), `kind:"gpu-group"|"serverless-binding"`.
  Keep the privacy contract: **no keys, no model prompts, no per-call data, no
  absolute paths** — only the opaque resource id + a public endpoint domain +
  status. (`desktop/agent/convex_privacy_test.go` enforces this; add the new
  fields to `fieldsWeForbidInAnyConvexPayload`'s allowlist test.)
- Usage/cost: a `cloudUsage` rollup (provider, gpuClass, hoursUsed | tokensUsed,
  costCents, deviceId) — **summary only**, no call contents. Powers a
  `remote_cost`-style "GPU spend this month" view.
- Billing: **BYO first** (user's own Salad/DeepInfra key, user pays the provider
  directly — no gate). Managed/metered (Yaver fronts the GPU and bills via
  LemonSqueezy) reuses the dormant `canProvisionManaged` fail-closed gate
  (`subscriptions.ts:117-137`) — keep it dormant pre-YC per
  `project_business_model`. Do not conflate the two tokens
  (`project_managed_vs_byo_hetzner`).

---

## 6. Surfaces (CLI / MCP / web / mobile)

Mostly free once the provider + provisioner + verbs exist:

- **CLI/MCP:** `account_connect salad`, `cloud_plans --host salad`,
  `cloud_provision --host salad --opts '{"gpu":"a100","model":"...","image":"vllm"}'`,
  `scale --gpu h100`, `cloud_destroy`, `remote_cost`. All already routed in
  `httpserver.go` / `ops_cloud.go`; new code is the provider implementations.
- **Web/mobile:** the accounts UI and devices/cloud views already render
  provider rows generically (`project_cloud_provider_agnostic_facade`). A
  "GPU & inference" section = a new account card + a plan picker fed by
  `cloud_plans`. No icon library (`feedback_no_lucide_use_inline_svg`).

---

## 7. Phased plan (BYO-first, ship-thin)

| Phase | Deliverable | Touches | Verifiable outcome |
|---|---|---|---|
| **P0 — Account wiring** | Salad + DeepInfra providers in `accounts.go` | `accounts.go`, account MCP tools | `yaver account connect salad` stores key; `account_status` green |
| **P1 — App binding via companion** | call-center companion reads inference config from vault; document `env_from: [{vault: callcenter}]` | doc + a vault project (no e-back code change) | `companion up` runs the gateway with `DEEPINFRA_*` from vault |
| **P2 — Catalog + serverless binding** | `cloud_plans --host {salad,deepinfra}` + DeepInfra binding provisioner + `voiceSafe` flag | `cloud_provisioners.go`, `cloud_deploy.go` | list GPU classes/models with price + voice-safe |
| **P3 — Salad provisioner + GPU-type change** | `provisionSalad` (container group) + `opsScaleHandler` Salad/DeepInfra branches | `cloud_provisioners.go`, `ops_cloud.go` | provision an A100 group, `scale --gpu h100`, destroy |
| **P4 — Dispatcher/autoscaler** | load watch → burst → idle-reap → vault rebind, via scheduler Verb-mode | new `gpu_autoscaler.go` + companion cron | volume spike provisions Salad; idle reaps it; no dropped calls |
| **P5 — Cost + Convex** | `cloudUsage` rollup, `remote_cost` GPU view, privacy-test fields | `schema.ts`, `cloudMachines.ts`, `convex_privacy_test.go` | monthly GPU spend visible; privacy test passes |
| **P6 (post-YC) — Managed/metered** | Yaver-fronted GPU + LemonSqueezy metered billing | `subscriptions.ts`, webhooks | gated managed provision |

P0–P1 deliver value immediately (manage your Salad/DeepInfra account + run the
call-center on a Yaver box reading rented endpoints) with almost no new infra.
P3–P4 deliver the "dispatcher that bursts hourly GPU" vision.

---

## 8. Risks / sharp edges

- **Realtime ≠ batch.** The catalog must encode the voice-safety rules or users
  will pick a reasoning model and get dead air. Make `voiceSafe` a first-class,
  visible field, defaulted on for the call-center template.
- **Cold start.** Salad/serverless GPU cold-starts can blow the `< 800 ms`
  budget. Hence DeepInfra-as-baseline + Salad-as-burst, never Salad-only for the
  first call. Pre-warm before cutover.
- **Stateful sessions.** Never tear down a GPU with calls in flight. The reaper
  must drain (route new sessions away, wait for in-flight to end) before
  `cloud_destroy`. The dispatcher owns this; the app stays stateless about it.
- **Privacy.** Endpoint domains and resource ids may go to Convex; keys, models'
  prompts, and call audio/transcripts must not. Extend
  `convex_privacy_test.go` alongside any new sync field — this is enforced, not
  optional.
- **Two-token confusion.** BYO Salad key (user's money, vault) vs a future
  managed Yaver-fronted key (Yaver's money, env-only, gated). Keep them as
  distinct as Hetzner BYO vs Managed already are.
- **Don't overload the coding-runner lane.** Resist wiring the call-center
  through `runner-provider`. It's the wrong plane (Section 2) and would couple
  app inference to agent-spawn semantics.

---

## 9. One-paragraph build order

Add Salad/DeepInfra to `accounts.go` (P0). Document the call-center as a
companion that reads `DEEPINFRA_*` from a vault project, so it runs on a cheap
Yaver box today (P1). Make `cloud_plans` provider-aware and add a DeepInfra
serverless *binding* provisioner with a `voiceSafe` flag (P2). Add the Salad
container-group provisioner and implement the `gpu` branch of the existing
`scale` verb (P3). Build the dispatcher autoscaler that watches gateway load,
bursts Salad via scheduler Verb-mode, reaps on idle, and rebinds the vault
endpoint the companion reads (P4). Add a `cloudUsage` rollup + `remote_cost` GPU
view and extend the privacy test (P5). Defer Yaver-fronted metered billing to
post-YC behind the existing fail-closed gate (P6).
