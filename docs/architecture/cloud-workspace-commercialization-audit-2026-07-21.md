# Cloud Workspace Commercialization Audit — Compute, Transport, Inference, Money

Date: 2026-07-21
Author: Claude Code deep audit (four parallel read-only tracks + independent verification)
Scope: can Yaver sell managed compute and inference, and give a free/trial tier first?

Status: **audit only. No code was changed.**

Read with, and treat as superseding where they disagree:

- `docs/architecture/cloud-relay-compute-inference-audit-2026-07-21.md` (the Codex pass)
- `docs/architecture/cloud-provider-implementation-handoff-2026-07-21.md` (the handoff)

Those two documents are accurate about *intent* and substantially optimistic about
*state*. This document is the code-first counter-reading. Per `CLAUDE.md`: where a
doc and the code disagree, the doc is the bug.

---

## 0. Executive verdict

**You cannot sell either product today, and the reason is not the provider matrix.**

Three findings dominate everything else:

1. **The multi-provider architecture is a test-only island.** ~1,900 lines across
   `cloudProviders/registry.ts`, `cloudProviderPlacement.ts`, `cloudPoolPlacement.ts`,
   `inferenceBackends.ts`, `inferencePlacement.ts`, `providerCatalog.ts` and
   `runtimeSlices.ts` have **zero production callers**. Their tests pass and prove
   nothing about the shipped product. The real path is hardwired Hetzner and partly
   bypasses even its own facade.
2. **The money plane cannot bill, and its safety caps are structurally dead.** Metered
   usage accumulates in `managedUsage`/`creditUsage` and is never converted into a
   charge by any code path. The documented "$3/day hard COGS backstop" can never fire
   because of a single unreachable mutation. Two of three gateway backstops are
   unbound bindings that fail *open*.
3. **A freshly provisioned box is not SSH-reachable, ever, without manual steps** —
   because `/info` has never emitted `ownerUserId`, and the auto-bootstrap
   unconditionally short-circuits on its absence. This is a one-field fix blocking
   the entire "zero install" premise.

What *is* genuinely good: the ownership/isolation invariants, the fail-closed
entitlement ordering before spend, the forced-command cage, the wake-phase
cross-surface parity, and the product rule that end users never pick a provider.
Those are real and should not be disturbed.

**Recommendation in one line:** ship a Hetzner-only, hard-capped, short-TTL trial on
your own money; fix the reaper and the metering truth before it; sell compute; never
sell inference standalone; treat provider credits and multi-provider as phase 2.

---

## 1. How to verify any claim in this document

Every claim below carries a `file:line`. The structural claims were established by
caller-grep, not by reading declarations. To re-derive the central one:

```bash
grep -rn "createManagedCloudProviderRegistry\|selectPlacementCandidate\|\
selectInferenceCandidate\|buildWorkspaceRuntimePlan\|createInferenceBackendRegistry" \
  backend web mobile desktop cli gateway | grep -v "_generated\|\.test\.mts"
```

Result today: **nothing outside the defining files**. That single command is the
audit's thesis.

---

## 2. Plane-by-plane state

### 2.1 Compute — DONE (Hetzner only), FACADE (everything else)

| Stack | Status | Reality |
|---|---|---|
| `cloudMachines.ts` + `cloudLifecycle.ts` | **DONE, Hetzner-only** | Every real VM. Raw `fetch` to `api.hetzner.cloud` or `createHetznerProvider`. |
| `cloudProviders/*` + placement modules | **FACADE, 0 callers** | Compiles, has real HTTP code, never instantiated. |
| `desktop/agent/launch_*.go` + `cloud_*.go` | **DONE but parallel** | User's own creds; never touches Convex quota/entitlement/metering. |

**Provider choice is hardcoded.** `cloudMachines.ts:2080` `createHetznerProvider(HCLOUD_TOKEN)`.
`createCloudMachine` (`:1056-1073`) never writes the `provider` field at all, so every
managed row has `provider === undefined` and every reader defaults it
(`cloudPlacementCapacity.ts:83`, `cloudLifecycle.ts:1119`). Region is hardcoded `"eu"`
at every call site (`http.ts:4514`, `:4553`, `cloudMachines.ts:2795`).

**The facade is self-blocking by construction.** Only Hetzner declares
`delete-stops-compute-spend` (`hetzner.ts:64`); AWS/GCP/Azure correctly omit it
(`aws.ts:64-76`, `gcp.ts:67-79`, `azure.ts:75-85`) because EBS/PD/managed disks
persist. But `requiredCapabilitiesForProfile` (`cloudProviderPlacement.ts:54`) requires
it — so no non-Hetzner provider can ever pass placement even if the engine were wired.

**Non-Hetzner adapters are not merely unwired; they are incorrect:**

- **GCP treats an Operation as an Instance.** `gcp.ts:196-201` reads `selfLink`/`status`/
  `natIP` off the `instances.insert` response, which is an Operation. `cloudResourceId`
  becomes an operation URL, `serverIp` is always `undefined`, and a later `deleteMachine`
  (`:297-303`) targets a nonexistent path. Same bug in `createVolume:139`. **Latent orphan
  generator.**
- **AWS and Azure never return `serverIp`** (`aws.ts:175-179`, `azure.ts:222-226`), and
  `cloudMachines.ts:2327` hard-throws `"returned no public IPv4 address"`. Every
  AWS/Azure create would abort *after* the instance exists. **Guaranteed orphan.**
- **GCP/Azure auth is a ~1h OAuth token in a Convex env var** with no refresh
  (`gcp.ts:57`, `azure.ts:63`). Broken-by-design in production.
- **No provider implements snapshot or wake-from-snapshot** except Hetzner
  (`aws.ts:190-197`, `gcp.ts:212-219`, `azure.ts:237-244` all throw `not_wired`).
- **Azure NSG priority resets to 1200 per call** (`azure.ts:272`) → rule collision.
- **AWS `getMachineStatus` parses `<name>` from EC2 XML with a naive first-match regex**
  (`aws.ts:369-372`) — DescribeInstances XML has many `<name>` tags.

**False capability declaration.** `hetzner.ts:58-70` claims `tagged-cleanup` while
`:205-210` returns `[]`. This is exactly the inventory-vs-operation false green
`CLAUDE.md` warns about, on the one capability the placement gate checks.

**Quota and entitlement are correctly ordered and fail-closed** (`cloudMachines.ts:2062`):
credentials (`:2071`) → entitlement (`:2093-2119`) → quota (`:2126-2141`) → *then* spend.
Client cannot inject a provider: `create`/`ensureForSubscription` args carry no
`provider` field, both are `internalMutation`, and HTTP routes enforce per-row ownership
(`http.ts:6731-6733`, `:6790-6792`). **No auth-bypass found.** Quota is enforced only in
the action, not the mutation (`:2046` admits this), so `status:"error"` rows consume
slots (`:2037`) with no reclamation — self-DoS at the default limit of 1 (`:2018-2027`).

### 2.2 Cost containment — the leaks

**R1 — Orphaned billing VM on partial provision failure.** `cloudMachines.ts:2311`
creates the server; the row is not written until `setProvisioned` at `:2382`; the catch
at `:2457` deletes **only the volume** (`:2462`). Any throw between — including the
deliberate `!serverIp` throw at `:2328`, a Convex transient on `recordBinding:2374`, an
OCC conflict, or an action execution-limit — leaves a running, billing, unreferenced
server. Narrower same-shape window in `cloudLifecycle.resumeMachine` (create `:1761` →
persist `:1781`, catch `:1819+` never deletes). `abandonWake` (`:1580`) handles this
correctly but fires only for wakes, never provisions.

**R2 — Orphaned volume on every customer decommission.** `cloudMachines.destroy:3015`
deletes server + Cloudflare DNS and stops. It **never touches `machine.volumeId`**. The
only code that deletes volumes, `cloudLifecycle.purgeMachineResources:1527`, is reachable
solely from the **owner-only** dev route `http.ts:6130`. Every volume-backed box a
customer removes leaves a permanently billing Hetzner Volume.

**R3 — No reconciliation exists at all.** `crons.ts` is empty (`:26-38`). `cleanup.ts`
prunes logs only. All four `listYaverTaggedResources` return `[]` (`hetzner.ts:209`,
`aws.ts:237`, `gcp.ts:261`, `azure.ts:297`). `reconcileSubscriptions` (`:2737`) goes
subscriptions→rows, one direction. **Nothing can answer "does the provider hold
resources Convex doesn't know about?"** Combined with R1/R2: leaks are permanent and
undetectable.

**R4 — Scheduling depends on an external box.** All crons were moved to systemd timers
on a self-hosted Hetzner box POSTing `/crons/run` (`crons.ts:3-24`, `http.ts:9602`). If
that box is deleted — which the Hetzner metered rule actively pushes you toward —
**both the meter and the idle-park brake stop silently.** `idleSweepCron`
(`cloudLifecycle.ts:1311`) documents itself as "registered in crons.ts"; crons.ts is
empty. Dead function.

**R5 — Statuses that bill but aren't metered.** `listMeterableMachines:1023` counts only
`active`/`paused`. Boxes in `grace` (7-day hosted window, `cloudMachines.ts:2937`),
`error` (failed destroy — server still up per `:3100`), `resuming`, or `suspended` are
running and billing with zero `creditUsage` rows. Direct margin leak.

**R6 — Park is hardcoded Hetzner while reading `machine.provider`.**
`cloudLifecycle.ts:1145` calls `hetznerDelete` unconditionally; `:1128`/`:1150` read
`machine.provider ?? "hetzner"` for telemetry only. A row with `provider:"aws"` would be
marked `paused` while the instance runs forever. Latent today (nothing writes non-Hetzner
rows), but the field is schema-writable (`schema.ts:1446`) — this is the exact shape of a
future silent leak.

**R7 — CLI launch is entirely unaccounted.** `launch_cmd.go:75` → `launch_auto.go:17`
creates real VMs with **no Convex row of any kind** — not `cloudMachines`, not
`byoMachines` (grep: `byoMachines` never appears in any `launch_*.go`). No quota, no
entitlement, no meter, no park, no cleanup. The only teardown is a printed
`hcloud server delete …` hint inside an error string (`launch_hetzner.go:115`). Every
`yaver launch cloud` VM is an orphan by construction.

**R8 — Three-to-four conflicting SKU/region maps.** `cloud_capacity.go:49,68` (arm cax*,
EU-only) vs `cloudLifecycle.ts:670,690,702` (cx/cpx/gex, nbg1/ash) vs `hetzner.ts:88,212`
(cx32/gex44, fsn1/ash) vs `providerCatalog.ts:78`. Resize/resume across engines can pick
an incompatible type — the exact class of failure `serverType` (`schema.ts:1329`) was
added to prevent.

### 2.3 Transport / SSH — the zero-install blocker

**`/info` has never emitted `ownerUserId`.** `sshBootstrapDevice` (`ssh_bootstrap.go:83-90`)
reads it and returns `SkipReason` when absent. `handleInfo`
(`httpserver.go:2980-3010`) publishes `osUser`, `homeDir`, `hostname`, `version` — and no
owner field. `git log -S'"ownerUserId"' -- desktop/agent/httpserver.go` is **empty**: the
producer was never written. Three consumers dead-end identically
(`ssh_bootstrap.go:84`, `mcp_primary_tools.go:183`, `ping_cmd.go:297`).

> **Consequence: auto-bootstrap fails 100% of the time. `yaver ssh <box>` prints
> `bootstrap skipped: remote agent did not report ownerUserId` and exits 255
> (`main.go:8069-8076`). No box — cloud or otherwise — can self-bootstrap SSH.**
> This is the #1 blocker for a zero-install trial, and it is a one-field fix.

**Cloud-init injects no SSH key at all.** `buildCloudInitUserData` (`launch_cmd.go:307`)
and `buildCloudInitUserDataWithInstall` (`:342`) contain no `ssh_authorized_keys`, no
`# yaver-managed` block, no device key. The only provider hook is `AWS_SSH_KEY_NAME` →
`--key-name` (`launch_aws.go:87-89`), an operator's pre-existing keypair, AWS only. The
VM boots with an empty `authorized_keys`.

**Two disjoint key systems; the one the docs describe is the dead one.**

- **System A — `# yaver-managed` caged keys** (`ssh_managed_keys.go`). Complete, pure,
  well-tested. **`applyManagedKey:128` and `revokeManagedKeyOnDisk:153` have zero
  non-test callers.** In production no box has ever had a `# yaver-managed` line.
- **System B — plain bootstrap keys** (`ssh_bootstrap.go` + `auth_ssh_http.go`). What
  `yaver ssh` actually uses, and it installs an **unrestricted full-shell key**:
  `auth_ssh_http.go:113` explicitly *rejects* lines carrying `command=`/`no-`/`restrict`.

They are mutually exclusive by construction. **Decide which one is the product.**

**Reverse SSH does not exist.** `superviseReverseTunnel` (`ssh_reverse_tunnel.go:83`) is a
supervisor with no dialer — `reverseDialFunc` (`:72`) has zero implementations outside
tests. `chooseSSHTransport` (`:33`) is never called. Nobody initiates, nothing terminates,
no keepalive, no recovery. The real transport is the QUIC relay tunnel (`relay/tunnel.go`,
`relay/server.go:1063-1087`), which is genuinely solid.

**The relay→local-SSH bridge is clientless.** Relay side `relay/server.go:1939,1948`,
agent side `main.go:11753-11771`. `grep -rn "_yaver_ssh_control"` across mobile, web,
desktop, Swift, Kotlin returns **only the two constant definitions**. The Phase-A
primitives from `c8c2adb61` (`relayStreamTagSSH`=0x02, `bridgeToLocalSSH`) are
**dead-on-arrival** — the shipped bridge is hand-inlined and does not use the tag byte the
impl plan specifies. The mac-mini "VALIDATED" run went over plain Tailscale SSH, not the
relay (`SSH_NATIVE_CLIENT_IMPL_PLAN.md:47`).

**The forced-command cage itself is sound.** Shared parser so entries cannot drift
(`ssh_session_cmd.go:128`, used by `runSSHSession:112` and `runExec:128`). Closed-switch
whitelist defaulting to deny (`:46-73`); fixed method+path per verb; `{id}` substituted
only after `isSafeTaskID:89`. Channel-level `direct-tcpip` rejection
(`ssh_control_server.go:81`); request-level `shell`/`pty-req`/`x11-req`/`subsystem`/`env`
denial plus `default:` deny (`:111-120`); `DiscardRequests` kills `tcpip-forward` (`:77`);
`PublicKeyCallback` only (`:46-53`). **No escape found.**

Three caveats: `authorizedManagedKeysChecker` (`:183-208`) **accepts any key in
`authorized_keys`**, not just marked ones — its own comment claims otherwise and it has no
test coverage (`ssh_control_server_test.go:41` injects a fake). No handshake deadline, no
`MaxAuthTries`, no idle/lifetime cap. The host key (`ssh_keygen.go:106`) is **never
published to Convex**, so the client-side pinning the docs promise has no fingerprint
source.

**Multi-tenant isolation holds, and it is real.** Signature path
`relay/server.go:1706` → `authorizeProxyViaSig:751` → `resolveSigViaConvex:681` →
`backend/convex/devices.ts:2584`: `if (String(signer.userId) !== String(target.userId))
return deny`. Password path → `userSettings.ts:757-765` `device_mismatch`. Registration
collision refused for a different owner (`relay/server.go:1063,1088`). The SSH sentinel is
checked *after* this block, so it inherits it. **A tenant's stream cannot reach another
tenant's box.**

Relay-side defects: `HasSuffix` (`server.go:1939`) vs `==` (`main.go:11753`) **desync** →
a crafted path hijacks the relay connection forever while the agent 404s (goroutine +
socket leak per request, any authenticated tenant). The splice lane is **completely
unmetered** — `proxyWebSocket` never calls `RecordBytes` → free uncapped egress on the
shared relay, contradicting the docs' own byte-cap requirement. Hijack requires HTTP/1.1.
`repair-relay` is whitelisted (`ssh_session_cmd.go:64`) but points at a **Convex** route
via the agent's loopback mux — **the primary self-heal verb 404s exactly when needed.**

### 2.4 Inference — cataloged, not invokable

There are **two disjoint inference planes** and only one works.

| Plane | Status |
|---|---|
| **Gateway (Cloudflare Worker)** — `gateway/src/*`, `openrouterKeys.ts`, `managedMeter.ts` | **DONE / live-capable** |
| **`cloudProviders/*Inference` + `inferenceBackends` + `inferencePlacement` + `providerCatalog`** | **MISSING — descriptor-only, 0 callers, `invoke()` throws** |

Both real `invoke()` methods hard-throw `not_wired` (`bedrockInference.ts:54-58`,
`openaiCompatibleInference.ts:67-71`). Vertex, Azure AI and DashScope have **catalog rows
but no class exists at all** (`providerCatalog.ts:214-255`).

**The two catalogs do not overlap.** `providerCatalog.ts` lists AWS/GCP/Azure/Alibaba/BYO;
`gateway/src/pricing.ts` lists DeepInfra/Together/z.ai/OpenRouter. Neither references the
other.

**Advertised models resolve to something else.** `aiModels.ts:71-84` sells
`bedrock/deepseek.r1-v1:0` and `bedrock/deepseek.v3-1-v1:0`; `resolveRoute`
(`pricing.ts:123-132`) falls back to `ROUTES.auto` — the user silently gets DeepInfra
DeepSeek-V3 under a Bedrock label. `web/lib/sandbox/gateway.ts:11` sends `model:"glm-5.2"`,
which matches no upstream and falls back the same way. **This is mislabeling, not a gap.**

**`claude-code` structurally cannot use the gateway.** It speaks Anthropic wire protocol;
the gateway is OpenAI-only (`provider_keys.go:143-146`). Managed inference is unavailable
for the flagship runner — which is *fine*, and is why the trial design below uses an
opencode-class runner.

**No provider credit sync exists.** All four `readBudgetStatus()` return env-var presence
only (`hetzner.ts:107`, `gcp.ts:113`, `aws.ts:113`, `azure.ts:122`). `creditUsdRemaining`
(`types.ts:85-86`) is consumed by `inferencePlacement.ts:74-80` and
`cloudProviderPlacement.ts:104-107` and **populated by nothing**. The `credit-first` cost
policy on four catalog entries is fiction. The real credit system — per-user OpenRouter
keys with a hard monthly limit (`openrouterKeys.ts:173-242`) — is genuine, but
**`syncUsageForUser:276` has no callers**, so its state is stale immediately, and no UI
reads it.

### 2.5 Money — cannot bill, caps are dead

**No usage→invoice pipeline exists.** `managedUsage` and `creditUsage` accumulate; the
only live reader is a daily-cap sum that is itself dead; `/managed/burn` and
`/managed/cockpit` return 410 (`http.ts:6492-6518`); credit-pack checkout returns 410
(`:5859-5887`). Overage past the wallet is clamped to zero and absorbed
(`managedMeter.ts:184`, `cloudLifecycle.ts:541`). **Revenue is capped at the flat
subscription regardless of consumption.**

**The daily cap is structurally dead.** `managedMeter.ts:150-154`:

```ts
const optedIn = await userOptedIntoKind(ctx, p.userId, p.kind);
const sim = p.dryRun !== false || !optedIn;
```

`userOptedIntoKind` reads `userSettings.managedServices`, whose only writer —
`managedServices.setServiceForUser` (`managedServices.ts:115`) — has **zero callers**, and
whose HTTP route returns 410. So every row is written `dryRun:true` forever;
`gatewayPolicy.ts:41` sums only `!dryRun` rows; `spentTodayCents` is permanently 0; the
documented "$3/day hard COGS backstop" (`plans.ts:97`) **never fires**. This silently
defeats `YAVER_MANAGED_METER_LIVE` as well.

**Two of three gateway backstops are unbound and fail open.** `USER_METER` (Durable
Object, hourly cap) and `OR_USER_KEYS` (KV, per-user OpenRouter limit) are **commented out**
in `gateway/wrangler.toml:36-51`; `limiter.ts:107` returns `{allow:true}` when the binding
is absent; `/admin/orkey` returns `501 kv_unbound` (`index.ts:222`).

**No input-token ceiling.** `index.ts:290` computes
`worst = costCents(primary, max_tokens, max_tokens)` — using the *output* clamp as an
input proxy. Nothing bounds prompt size. A huge prompt passes the affordability check
(which needs only `min(worst, 50c)`), and the overdraft is silently absorbed.

**All COGS rates are placeholders** (`pricing.ts:9-11, 64-65, 74-116`) and are the sole
input to the wallet debit. No reconciliation against actual provider invoices exists.

**Metering failures are silently dropped.** `index.ts:118-130` only `console.error`s a
failed `/gateway/meter` POST despite its own comment saying it MUST be alarmed.

**Absent `gatewayPolicy` row defaults to *enabled*** (`gatewayPolicy.ts:47`) with all caps
at 0 = unlimited. The safe default for a money-spending gate is deny.

**`past_due` keeps everything running.** `http.ts:5725-5744` sets the status and returns;
the gateway is untouched, the box is not paused, and `/billing/status:6237` counts
`past_due` as subscribed.

**The only compute SKU sold turns the gateway off.** Checkout hardcodes `tier:"byok"`, and
`byok` has `gateway.enabled:false` (`plans.ts:74-105`). **Managed inference is currently
unpurchasable through the public funnel.**

**There is no free tier as a product.** "Free" means a $0 wallet and a 402
(`http.ts:9273-9276` → `index.ts:246-248`). `freeGrantCents` is explicitly informational
(`schema.ts:2590`) and never enters an allow/deny decision. The BYO plane *is* genuinely
free and costs nothing (`http.ts:6520-6533`) — that is the only real free tier today.

### 2.6 Surfaces — the one area that is largely right

**The "no provider picker" rule is honored.** `ManagedCloudPanel.tsx:753-816` exposes plan
+ `eu`/`us` region only; `hetznerServerId` is read-only telemetry (`:22-30`). Mobile
likewise (`ManagedCloudCard.tsx`, `cloud-onboarding.tsx`). Provider pickers and token
entry exist only on the explicitly-separate BYO surfaces (`CloudProvidersSection.tsx`,
`ByoCloudPanel.tsx`) and the B2B `CompanyAIOptionsView.tsx`. One item to confirm as
intentional: `mobile/app/sandbox-ai.tsx` lets a normal user pick Claude/OpenAI/GLM and
paste a model key.

**Wake-phase parity is genuinely good** — vocabulary pinned across four languages to
`wakeMachineCore.ts` PHASE_META (`tvos/.../BoxLifecycle.swift:26,87`,
`watch/.../BoxLifecycle.swift:27,55,100`, `ManagedCloudCard.tsx:23-42`).

Parity gaps: **the CLI has zero cloud-workspace awareness** (no `cloud`/`workspace`/`wake`
command in `cli/src/commands/`); tvOS/watch are wake-only; `gpuRentals` is **write-only**
(`gpu_rental_sync.go:98` writes, no surface reads). `HIDE_PAID_UI` is **duplicated** rather
than imported (`ManagedCloudPanel.tsx:49` vs `web/lib/launchFlags.ts:4`) — flipping the
shared flag will not hide the panel's own buy block.

Access is **owner-only by default**: `cloudAccessAllowed` requires the owner allowlist or
`YAVER_CLOUD_PUBLIC=true` (`http.ts:157-160`), and checkout 503s unless
`LEMONSQUEEZY_<VARIANT>` is set (`:5781-5787`).

---

## 3. Product recommendation

### 3.1 Sell compute. Do not sell inference standalone.

**Compute margin is defensible.** The 2–3x markup (`cloudLifecycle.ts:40`) is not a CPU
markup — it buys scale-to-zero, park/wake, relay reachability and orchestration. Spend is
bounded, observable, and already has a working brake (idle sweep runs `dryRun:false`,
`http.ts:9583`; the wallet-reserve gate is fail-closed).

**Inference arbitrage is the wrong first business.** The gateway routes through
OpenRouter — itself a thin-margin router — so a 1.5x markup is a price war with no moat,
against an unknown cost basis (placeholder rates), with the margin protections currently
disabled. Per-request cost is unbounded and unobservable in advance; a VM's hourly rate is
not.

**The runner plane is BYO by law, not by choice.** Claude Code / Codex / opencode run under
the user's own subscription, CLI-only. That is a compliance boundary, not a pricing
decision — multi-tenant resale of a subscription CLI violates vendor terms. So "sell
inference" can only ever mean the gateway plane, a different product from the runner.

> **Documentation defect:** this rule has no canonical text. `docs/autotest-spec.md:204-205,449`
> and `docs/autorun-remote-handoff.md:149` cite `feedback_no_api_keys_subscription_only.md`
> and `feedback_no_headless_p_mode.md`, and **neither file exists in the repo**. The rule is
> also absent from `CLAUDE.md`. For a constraint this load-bearing, write it down.

### 3.2 DECIDED: the trial lends inference, not a machine

Canonical statement: `yaver-cloud-workspace-product-model.md`. Summary here so this
document is self-consistent.

**Free tier = relay + BYO compute (the user's own machine) + capped, time-boxed trial
inference credits.** Trials do **not** include a VM.

This is the single highest-leverage decision in the whole plan, because lending compute is
what carries every expensive failure mode, and *not* lending it removes them all at once:

- **Abuse** — a free VM with a public IP is the classic vector. `CLAUDE.md` already names
  the consequence: a datacenter IP hammering third parties gets the **entire provider
  account** suspended, paying customers included. No trial VMs ⇒ no such blast radius.
- **Orphan cost** — trials are a high-volume create/destroy engine, and §2.2 shows that
  path leaked on both ends with nothing able to detect it.
- **Egress churn** — §3.4 does not apply to trials at all: a trial user has no mirrored
  subscription credentials to protect.

**Trial inference is still mandatory,** because a trial user by definition has no
Claude/ChatGPT subscription — that is the barrier being removed. It runs an opencode-class
runner on gateway inference. **This is the real justification for the inference plane:
fuel, not margin.**

**Accepted cost of this decision:** the trial does *not* deliver "use the whole product
with no installation". The user still installs the agent on their own machine. The trial
removes the *"I need a model subscription"* barrier, not the *"I must install and
authenticate something"* barrier.

**Know which barrier your buyer has.** Solo devs — the stated audience — mostly already
pay for Claude or ChatGPT; their blockers are install friction and lacking an always-on
machine, so trial inference gives them something they do not need. Normie/phone-first users
will not buy a subscription to evaluate an unknown tool, so for them it is the entire
unlock. **This is an open product decision** (product-model §8, item 1).

A zero-install demo box stays a **separate, later, tightly-capped experiment**, built only
if conversion data shows install friction is the real killer — and only after Phase 0 and
Phase 2 below are green.

### 3.3 Trial preconditions

1. **Prepaid and fail-closed.** A trial user cannot be billed after the fact, so an
   open-failing cap is unrecoverable loss. Everything in §2.5 applies double here.
2. **Hard ceilings per request *and* per account,** including an **input**-token ceiling —
   `index.ts:290` currently has none and estimates worst case from the output clamp.
3. **Time-boxed**, or it is a free tier by another name.
4. **One per verified identity.** Passkeys/OAuth exist; audit `mergeUserInto` for farming
   loopholes.
5. **Framed as a loan,** so conversion does not read as a takeaway — see product-model
   §4.4.

### 3.4 Vendor IP / ToS risk — the scale-to-zero interaction

Post-auth CLI traffic from datacenter IPs is not blanket-blocked (Claude Code and Codex
run in GitHub Actions on Azure-hosted runners). The real risks are narrower:

- **Interactive browser login from a cloud VM** is the fragile leg. Your design already
  sidesteps it: device-code headless auth plus credential *mirroring* from the user's own
  machine (`mobile/app/cloud-onboarding.tsx`). Keep it that way; never have the box log in
  directly.
- **IP churn is self-inflicted.** Park is delete-not-stop (correct for Hetzner metering),
  so **every wake yields a fresh public IP**. A user parking/waking twice daily presents
  one subscription from ~60 datacenter IPs a month — close to the canonical signature of
  credential sharing. **Hetzner floating-IP management does not exist** (`hetzner.ts:163`
  reads the IP off the create response only). A floating IP (~€1/mo, far below the server)
  is the highest-leverage mitigation and closes a real adapter gap.
- **The operator-fleet seam.** `gateway_runner_env.go:181-197` injects a *gateway* key into
  a tenant's runner — that path is clean. The shape to never build is the inverse: one
  Yaver-held subscription serving many users' runners. That is termination-grade, not
  rate-limit-grade.
- **Verify empirically.** Mirror creds to `yaver-test-ephemeral`, run a real session, park
  and wake repeatedly, watch for challenges. Vendor anti-abuse changes without
  announcement; this beats any reasoning from documentation.
- **Build the probe.** Per the incident rule, "runner auth was rejected by the vendor" must
  be a *named* diagnosis. `providerTroubleshooting.ts` has no such plane today.

---

## 4. Sequenced plan with gates

Each phase has an exit gate that must be **probed, not assumed**.

### Phase 0 — Stop the bleeding (before any trial or sale)

1. Delete the created server in the `provision` catch when the id was obtained but not
   persisted (`cloudMachines.ts:2457`), or persist immediately after create. Same for
   `resumeMachine` (`cloudLifecycle.ts:1819`).
2. Make `destroy` delete the volume (`cloudMachines.ts:3015` — reuse
   `purgeMachineResources` rather than duplicating it).
3. Implement `listYaverTaggedResources` for Hetzner **for real** and build a reconciliation
   sweep: provider → Convex, flagging unknown servers/volumes/images. Until then, remove
   the false `tagged-cleanup` capability claim (`hetzner.ts:64`).
4. Meter `grace`/`error`/`resuming`/`suspended` (`cloudLifecycle.ts:1023`).
5. Make cron delivery not depend on a deletable box, or alarm loudly when ticks stop.

**Gate:** create a box, kill provisioning mid-flight, decommission another, then run the
sweep — it must find both leaks. A green sweep on a clean fleet proves nothing.

### Phase 1 — Zero-install reachability

6. **Emit `ownerUserId` from `/info`** (`httpserver.go:2981`, owner-tier gated). One field;
   unblocks all SSH auto-bootstrap.
7. Inject the key at provision (`launch_cmd.go:307,342`).
8. Decide System A vs System B and give the winner a caller. If A, `auth_ssh_http.go:113`
   must stop rejecting caged lines.
9. Fix `authorizedManagedKeysChecker` to require the marker before `YAVER_SSH_CONTROL` is
   enabled anywhere; add a real test.
10. Fix the `HasSuffix`/`==` desync; add `RecordBytes` and an idle deadline to the splice.
11. Rewire `repair-relay` to an agent-local route.

**Gate:** provision a fresh box from a clean account and reach it over SSH with **zero**
manual steps, twice — once direct, once relay-only.

### Phase 2 — Metering truth

12. Resolve `userOptedIntoKind` (`managedMeter.ts:150-154`) — restore a writer or remove it
    from the predicate. It currently defeats both the live flag and the daily cap.
13. Bind `USER_METER` and `OR_USER_KEYS`; flip `limiter.ts:107` to fail **closed**.
14. Add an input-token ceiling; charge affordability against the true worst case.
15. Verify every rate in `pricing.ts` against live provider pricing.
16. `gatewayPolicy` absence → **deny** (`gatewayPolicy.ts:47`).
17. Treat `past_due` like `cancelled` for gateway policy.
18. Retry/dead-letter failed meter POSTs (`index.ts:118`).

**Gate:** drive synthetic load past the daily cap and confirm it *stops*. Reconcile a
week of `managedUsage` against a real provider invoice.

### Phase 3 — Trial inference (no VMs)

19. Trial entitlement: prepaid, fail-closed, time-boxed, one per verified identity.
20. Trial runner = opencode-class on gateway inference; conversion = the user connects
    their own machine and their own Claude/Codex subscription.
21. Loan framing in product copy (product-model §4.4) so conversion is not a takeaway.

**Gate:** a trial account cannot exceed its ceiling by any route — including one oversized
prompt — and the cap is demonstrated *stopping* real traffic, not merely present.

### Phase 4 — Sell compute

22. Flip `YAVER_CLOUD_PUBLIC`, set LemonSqueezy variants, de-duplicate `HIDE_PAID_UI`.
23. Usage→invoice: restore credit-pack checkout or add metered overage.
24. Give the CLI cloud-workspace commands.
25. **Stable egress IP for paid workspaces** (product-model §5). Paid only — trials have no
    compute, BYO is not our resource. **Depends on Phase 0:** a reserved IP is another
    detachable paid resource that outlives its server, so shipping it before the
    reclamation path and the sweep exist would double the leak rather than fix anything.

### Phase 5 — Credits and multi-provider (only after a terms read)

26. Fix GCP Operation-vs-Instance, AWS/Azure `serverIp`, GCP/Azure credential refresh.
27. Implement snapshot/wake and tagged cleanup per provider.
28. Real budget telemetry; then and only then wire `registry.ts` and
    `selectPlacementCandidate` to a caller.
29. Reconcile the SKU/region maps to one source.

### Phase 6 — Inference as a product (optional, last)

Only if credits or volume make the margin real. Fix the Bedrock mislabeling
(`aiModels.ts:71-84`) **before** anything else here — shipping a label that resolves to a
different model is worse than shipping no label.

---

## 5. Dead-code inventory — decide: wire it or delete it

> **Partially addressed 2026-07-21** (branch `cloud-egress-stability`, uncommitted,
> typecheck-clean, **never run against a real provider account**). Landed: a single
> reclamation path (`reclaimAuxResources`) closing the volume/IP/snapshot leak on
> decommission; provider→Convex orphan reconciliation
> (`reconcileProviderResources`, report-only); real `listYaverTaggedResources` on
> all four adapters; stable egress IP (Hetzner Primary IP, AWS EIP, GCP static
> address, Azure Standard PIP) with a long-park release sweep; server-side provider
> selection (`cloudProviders/selection.ts`) replacing the hardcoded Hetzner import;
> and correctness fixes to the GCP Operation-vs-Instance bug, AWS/Azure missing
> `serverIp`, the AWS instance-state regex, and the Azure NSG priority collision.
>
> **Unverified by design** — no provider accounts were available, so nothing has been
> exercised against a live API. Treat every adapter path as *compiles and reads
> correctly*, not *works*. The audit's own rule applies: probe the real capability
> before believing it.

Green tests on these provide **zero** production assurance. Leaving them in place makes
the system look ~5x more capable than it is, which is how the two prior audit documents
came to overstate readiness.

| Symbol | File | Callers |
|---|---|---|
| `createManagedCloudProviderRegistry` | `cloudProviders/registry.ts:12` | ✅ **WIRED 2026-07-21** — `cloudProviders/selection.ts` (provision) + `cloudLifecycle.reconcileProviderResources` |
| `selectPlacementCandidate`, `providerSupportsProfile` | `cloudProviderPlacement.ts:116,75` | tests only |
| `selectPoolEntry`, `leaseWouldExceedBudget` | `cloudPoolPlacement.ts:52,95` | 0 |
| `createInferenceBackendRegistry` | `inferenceBackends.ts:9` | 0 |
| `selectInferenceCandidate` | `inferencePlacement.ts:39` | tests only |
| `buildWorkspaceRuntimePlan` | `runtimeSlices.ts:57` | tests only |
| `classifyRuntimeTroubleshooting` | `providerTroubleshooting.ts:30` | 0 |
| `COMPUTE_PROVIDER_CATALOG` policy objects | `providerCatalog.ts` | seed only; no reader |
| `applyManagedKey`, `revokeManagedKeyOnDisk` | `ssh_managed_keys.go:128,153` | 0 |
| `superviseReverseTunnel`, `chooseSSHTransport` | `ssh_reverse_tunnel.go:83,33` | 0 |
| `relayStreamTagSSH`, `bridgeToLocalSSH`, `spliceBidirectional` | `ssh_relay_bridge.go` | 0 |
| `ensureLocalDeviceSSHKey`, `rotateLocalDeviceSSHKey` | `ssh_keygen.go` | tests only |
| `idleSweepCron`, `ensureVolume` | `cloudLifecycle.ts:1311,1472` | 0 |
| `listYaverTaggedResources` (all 4) | `cloudProviders/*.ts` | ✅ **IMPLEMENTED 2026-07-21** — real listings, consumed by the orphan sweep |
| `syncUsageForUser` | `openrouterKeys.ts:276` | 0 |
| `setServiceForUser` | `managedServices.ts:115` | 0 — **and its absence breaks the daily cap** |

---

## 6. Doc-vs-code divergences to fix in the same change

Per `CLAUDE.md`, the doc is the bug.

| Doc claim | Reality |
|---|---|
| ROBUST §4d.1 — control server accepts only `# yaver-managed` keys | `ssh_control_server.go:194-206` accepts **any** key in `authorized_keys` |
| ROBUST §4e — managed key "injected at provision… zero user action" | `launch_cmd.go:307,342` inject no key; `applyManagedKey` has 0 callers |
| ROBUST §4e — wake re-establishes via `superviseReverseTunnel` | no dialer, no reverse tunnel exists |
| ROBUST §4b — `permitopen="127.0.0.1:18080"` | `ssh_managed_keys.go:28` emits `no-port-forwarding` and no `permitopen` (code is right, doc contradicts itself) |
| ROBUST §4b — idle timeout, max lifetime, byte caps | none of the three exist |
| ROBUST §4d.2 — bridges within the same **access-graph** | same-**owner** only; guests/host-share have no relay-proxy path |
| SECURE:854-863 — agent exposes a `reverseSsh` status object | `grep reverseSsh` → zero hits |
| SECURE:1077-1082 — `yaver doctor ssh`, `ssh enable --forced-command` | do not exist |
| SECURE:123 — `MachineTransport` incl. `"reverse-ssh"` | zero hits in `mobile/src`, `web/lib` |
| `SSH_NATIVE_CLIENT_IMPL_PLAN.md` Phase A — `POST /d/<id>/_ssh` + `0x02` tag | shipped as `/_yaver_ssh_control` sentinel + hijack; tag unused |
| Codex audit — "AWS/GCP/Azure facades concrete, not production-eligible" | also **incorrect**: GCP Operation bug, AWS/Azure no `serverIp`, 1h tokens |
| Codex audit — Hetzner "tagged cleanup" | returns `[]` |
| `phone_cost.go:62-63` — "$49/mo CPU, $449/mo GPU, flat, no metering" | actual catalog is $9 / $29 metered; **shipped to mobile UI** via `/phone/projects/cost-hint` |

---

## 7. What is genuinely good — do not disturb

- Fail-closed ordering before spend: credentials → entitlement → quota → create
  (`cloudMachines.ts:2062-2141`).
- No client-supplied provider anywhere; per-row ownership enforced on every HTTP route.
- Park's snapshot path aborts the delete on snapshot failure (`cloudLifecycle.ts:1198-1209`)
  — never loses the box.
- `destroy` surfaces failure as `status:"error"` rather than lying `stopped`
  (`cloudMachines.ts:3092-3110`), and refuses to lie when `HCLOUD_TOKEN` is absent
  (`:3027-3037`).
- Idle sweep is deliberately `dryRun:false` while the wallet meter is dry-run
  (`http.ts:9583` vs `:9561`) — correct asymmetry: preview must never keep a real server
  running.
- Relay same-user tunnel eviction (`relay/server.go:1063-1087`) and Convex-side owner
  scoping (`devices.ts:2584`).
- The forced-command cage, with a shared parser so the two entry points cannot drift.
- Device-code bootstrap: cloud-provider identity never changes the Yaver device auth model.
- The "no provider picker" product rule, already honored across web and mobile.
- Wake-phase vocabulary pinned across four languages.

---

## 8. Open questions for the owner

1. **System A or System B** for SSH keys? They are mutually exclusive today.
2. Is `mobile/app/sandbox-ai.tsx`'s provider+API-key picker intentionally outside the
   managed product?
3. Do you want `yaver launch cloud` to register a `byoMachines` row, or to remain
   deliberately unaccounted as an operator tool? Today it is the latter by accident.
4. Trial length, concurrency cap, and monthly ceiling — these are business inputs the
   engineering cannot pick.
5. Is the `hosted` tier still a product? It carries grace-period and self-hosted-Convex
   machinery that nothing in the public funnel can reach.
