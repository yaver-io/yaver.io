# Yaver Task Packages — Shareable, Portable, Cross-Device Task Execution

Date: 2026-06-15
Audience: Claude Code / Yaver implementation agent.
Status: DESIGN + AGENT CORE BUILT. Reuses BUILT primitives (companion, scheduler,
collection, egress, support-link, mobile agent); the package format, allocator,
cross-user share, and mobile runner are new.

> Build status (2026-06-15): the Go agent core is BUILT + tested (uncommitted).
> Files: `package_store.go` (yaver/v1 manifest types + local store + validator),
> `package_runtime.go` (run-once executor: fetch+JSON extraction, MCP-over-MCP via
> remote http server AND local ops verb, ACTING-tier confirm gate, vantage-tagged
> persistence into `collection_store`), `package_ops.go` (7 owner-only ops verbs:
> `package_publish/list/get/run/delete/allocate/status`, auto-registered → already
> MCP/CLI-reachable). Tests `package_test.go` (all green; real httptest MCP server,
> no mocks) prove a package calling a yaver-bet-style MCP and capturing the result.
> ALSO BUILT (2026-06-15, uncommitted): (1) Convex cross-user sharing — schema
> `taskPackages` + `packageAllocations`, `backend/convex/taskPackages.ts`
> (upsert / share / accept→scoped infraAccessGrant origin "task-package" / status /
> run counters), agent privacy seam `package_sync.go` (`buildTaskPackagePayload`,
> hostnames-only) wired into publish + pinned by a new convex_privacy_test.go case;
> `npx convex codegen` exits 0. (2) Web `PackagesView.tsx` + route
> `app/dashboard/packages/page.tsx` (list / publish / run / allocate via
> `agentClient.callOps`); tsc clean. (3) Mobile `packagesClient.ts` + screen
> `app/packages.tsx` + periodic `backgroundCollector.ts` (expo-background-fetch
> on-phone runner, ~15m Android / opportunistic iOS); tsc clean.
>
> SANITY CHECK (2026-06-15): `package_check` (package_check.go) — preflight that
> lints the manifest + dry-runs the package once, returning pass/warn/fail with
> reasons (geo/IP block from the owner's vantage = WARN, not FAIL; broken manifest
> or unreachable MCP binding = FAIL). `package_allocate` REFUSES to share a package
> that failed or was never checked, unless `force=true`. Wired into BOTH UIs:
> web "Preflight check" button + verdict + a gated Allocate (force checkbox), and
> mobile a per-package "Check" button + verdict. Tests green; both surfaces tsc clean.
>
> RUNNER FLOW + NAV (2026-06-15): Convex HTTP routes `POST /packages/{allocation,
> accept,shared}` (http.ts, CORS-preflighted) wrap allocationByCode / acceptAllocation /
> sharedWithMe; `npx convex codegen` exits 0. Mobile runner CONSENT screen
> `mobile/app/package-accept.tsx` + client `mobile/src/lib/packageShareClient.ts`
> (deep-link `?code=` or paste; shows domains/schedule/will-not/data-shared, wifi/
> charging toggles, Accept→scoped grant). NAV entries wired: web dashboard "Packages"
> tab (page.tsx) + mobile `more.tsx` "Task Packages" + "Accept a shared task" cells.
> All tsc clean.
>
> COMPILER + ON-PHONE COLLECTOR (2026-06-15): (1) `package_compose` (package_compose.go)
> — loose NL goal → a concrete TaskPackage (infers sources/URLs, kind collect-vs-operate,
> vantage geo, schedule, consent), published + preflighted; behind a `packageComposer` seam
> (heuristic now, LLM-backed later). Tests green. (2) On-phone WebView extractor
> `mobile/src/components/HiddenPackageWebView.tsx` (off-screen WebView, injects the source's
> selectors, exits the phone's own IP, stops on CAPTCHA) + store-and-forward
> `mobile/src/lib/onPhoneCollector.ts` (AsyncStorage queue → drains to the phone's local
> agent collection store) + a "Run here" button in `mobile/app/packages.tsx`. tsc clean.
>
> Remaining polish only: swap the heuristic composer for an LLM-backed one (autopilot/
> codingAgent) behind the existing seam, and broaden WebView extraction selectors.

## 0. One Sentence

A **Task Package** is a portable, declarative, consent-gated unit of work that an
owner builds once and **ships to another person from inside Yaver** — like
shipping a systemd unit / docker service / a small program — and that person runs
it on **their own device (phone or PC)**, lending their compute and network
vantage (IP/geo), with results flowing back to the owner.

This is the generic infrastructure. Data collection is one package `kind`; Yaver
Bet (Serbian live-odds) is one *use case* of that kind. Fintech rate monitoring,
ad verification, regional price checks, localized compliance, uptime-from-region,
and app-catalog checks are others (§10). **Nothing in this design is
betting-specific.**

A package is **not only static code**. Its body can be (§2b):

```text
- declarative collectors (sources + extraction),
- imperative code (a step that runs a program), OR a docker image,
- an AI RUNNER: a loosely-defined task that an agent loop carries out at runtime,
  calling tools / MCP servers (local OR remote) as it goes,
- a browser (Playwright/chromedp) or Android (redroid) automation flow.
```

And it can **save data OR perform operations** (do things, with guardrails), and
its runner and its MCP servers can be **local or remote** (a phone, the friend's
PC, or a remote worker box the owner controls/borrows). The owner can **define the
task loosely in natural language**; Yaver's authoring runner **generates** the
concrete package (code / MCP bindings / Playwright / redroid flow) and emits a
`yaver.package.yaml` (§3b, "The Compiler").

## 1. Why This Shape (and what it is NOT)

The recurring need: run owner-defined work **from a place or device the owner
doesn't have** — a residential connection in another country, a phone on a
specific carrier, a machine behind someone's LAN — using a **trusted person** the
owner already knows, with the **lowest possible friction** for that person
(create account, install the Yaver app or run the Yaver agent, accept a package).

This sits between three bodies of prior art and deliberately takes the ethical and
architectural high ground of each:

| Prior art | What it does | What Yaver Task Packages do differently |
|---|---|---|
| **BOINC / volunteer computing** ([Anderson](https://boinc.berkeley.edu/boinc_a_platform_for_volunteer_computing.pdf), [mobile SOTA](https://www.hajim.rochester.edu/ece/sites/tapparello/papers/Tapparello_BookChapter2015.pdf)) | Owner-defined work units run invisibly on volunteers' devices incl. Android, only on wifi/charging/idle | Same volunteer-runtime idea, but **cross-user *shared* per task**, consent-scoped, and oriented at **network vantage**, not just spare CPU |
| **Proxyware** (Honeygain, IPRoyal/Pawns, PacketStream — [overview](https://www.honeygain.com/blog/bandwidth-sharing-apps/), [risk](https://www.trendmicro.com/en_us/research/23/b/hijacking-your-bandwidth-how-proxyware-apps-open-you-up-to-risk.html)) | Users sell unused bandwidth into an **anonymous rotating IP pool** resold to unknown buyers; opaque traffic, abuse-report risk | The **inverse**: a **named person** runs a **named owner's task** they can **see in full** (domains, frequency, data). No pool, no resale, **no IP rotation**, no unknown traffic. Transparency is the product. |
| **Commercial geo proxies** (Oxylabs, Decodo, Scrapeless — [survey](https://www.scrapeless.com/en/blog/best-residential-proxies)) | 90–115M purchased residential IPs, 195+ countries, **rotate IPs per request** to evade detection | Yaver uses **your own trusted people** as vantages, **never rotates to defeat a block**, and treats a block as *data* (the geo-gating is often the finding) |

The hard line (enforced, not advisory — same Non-Goals as
`docs/user-directed-data-collection-runtimes.md`):

```text
A Task Package is consented work run by a real, named person on their own device.
It is NOT a rotating-IP proxy pool, NOT ban-evasion, NOT a way to present an
egress or run an action the runner is not entitled to. Account login, payments,
KYC, irreversible actions, credential storage, and CAPTCHA/login bypass are
forbidden in the generic runtime; a vertical that needs them must design a
separate, explicit, compliant product.
```

## 2. The Package Abstraction

```text
PACKAGE   the portable manifest (yaver.package.yaml): what runs, where it can
          run, the vantage it needs, the schedule, the consent, the I/O.
TARGET    a runtime that can execute the package on a device:
            agent   — host process, durable via systemd/launchd (companion/managed_units)
            docker  — containerized step on a host with Docker
            mobile  — WebView/fetch/JS step on a phone (WorkManager/BGTaskScheduler)
RUNNER    a person (the owner, or a shared-to friend) whose device executes it.
VANTAGE   the (runner-device, egress ip/geo) a run is observed from (egress.go).
ALLOCATION  a binding of a package to a runner+device+target, under consent.
RUN       one execution; results ship to the owner's sink, vantage-tagged.
```

A package is **target-agnostic**: the same manifest runs on a phone (mobile
target) or a PC/server (agent or docker target). The allocator picks a target per
device from the manifest's declared `runtimes` and the device's capabilities.
This is exactly how the existing yaver-bet `serbia-collector/` (a PC Playwright
script) and the new on-phone collector become **two targets of one package**.

## 2b. What a Package Can Contain — Kinds & Engines

A package declares a `kind` (what it's for) and the `engines` it needs (how it
runs). The allocator (§5) uses both to pick a runner+target that can satisfy them.

```text
KIND      collect   save data (read-only observation)            low risk
          probe     reachability / correctness check from a vantage  low risk
          monitor   collect + diff + alert on change              low risk
          operate   DO something (submit a form, click, file an action) — GATED
          agent     a loosely-defined task an AI runner carries out at runtime,
                    using tools/MCP; may collect or operate                GATED

ENGINE    fetch     plain HTTP/JSON (any target, incl. mobile)
          webview   rendered DOM on a phone (mobile target)
          playwright real browser automation (agent/docker target — chromedp today)
          redroid   Android app observation/automation (host that supports it)
          mcp       call MCP tool(s) at runtime — server local(stdio) or remote(http)
          runner    an AI agent loop (Yaver runner) with a scoped toolset
```

Two risk tiers, enforced differently (§4d, §15):

```text
READ-ONLY  collect | probe | monitor with fetch/webview/playwright/redroid-observe.
           Consent = "reads public pages". No action ever taken. The default.
ACTING     operate | agent, or any redroid tap / form submit / MCP write tool.
           Consent = "this RUNS CODE / TAKES ACTIONS on your device". Confirm-gated,
           sandboxed, never silently. A friend's phone/PC running owner-authored
           actions is a different trust contract than read-only collection.
```

`engines: [playwright]` or `[redroid]` make a package **ineligible for the mobile
WebView target** (a phone can't run Chromium/an emulator) — the allocator routes
it to an `agent`/`docker` target or a remote worker (§4c) instead, and says so.

## 3. The Syntax — `yaver.package.yaml`

Borrows the best of the formats the owner already knows: systemd's
restart/constraint semantics, docker-compose's portability + image, Nomad's
job→group→task shape ([HCL spec](https://developer.hashicorp.com/nomad/docs/job-specification)),
GitHub Actions' step list. One declarative file, signed and shipped.

```yaml
apiVersion: yaver/v1
kind: TaskPackage
metadata:
  name: regional-price-watch
  owner: <userId>                 # set by Yaver at publish; not user-typed
  version: 3
  description: "Watch public list price of N SKUs from a given region."
spec:
  # ---- what the work is -------------------------------------------------
  task:
    kind: collect                 # collect | probe | monitor | exec
    # declarative collectors (portable across mobile/agent/docker):
    sources:
      - id: sku_123
        url: https://shop.example.com/p/123
        render: auto              # auto | fetch(JSON) | webview(rendered DOM)
        extract:
          price:        { selector: "[data-price]", as: number }
          availability: { selector: ".stock",       as: text }
    # OR an imperative step (agent/docker targets only — needs a real runtime):
    steps:
      - run: "node collect.js"
        workdir: .
  # ---- where it can run (ordered preference) ----------------------------
  runtimes: [mobile, agent, docker]
  image: node:20-alpine           # used only by the docker target
  # ---- the vantage it needs (drives ALLOCATION, §5) ---------------------
  vantage:
    geo: ["RS"]                   # required region(s); [] = anywhere
    residential: true             # must be a non-datacenter egress
  # ---- scheduling (cross-platform; honest floors, §4) -------------------
  schedule:
    every: 10m                    # Android WorkManager floor is 15m; iOS opportunistic
    jitter: 2m
    wakeable: true                # owner may push-to-wake a cycle
  # ---- constraints (systemd / WorkManager semantics) --------------------
  constraints:
    wifiOnly: true
    chargingOnly: false
    restart: on-failure           # always | on-failure | never
    stopOn: [captcha, login_wall, geo_block, device_integrity]
  # ---- where results go -------------------------------------------------
  output:
    sink: owner_box               # owner_box | ingest:<relay-url> | dataset:<name>
    dataset: regional_prices
  # ---- consent + governance (travels WITH the package) ------------------
  consent:
    summary: "Fetch public product pages from shop.example.com ~6x/hour using your connection."
    willNot: [login, payment, account_creation, store_credentials, place_orders]
    dataShown: ["price", "availability"]
  governance:
    retention: 7d
    revocable: true
    expiresAt: null
```

Notes:
- **Two task styles.** `sources[]` is a declarative collector that the runtime
  implements natively (works on `mobile` because it's just WebView/fetch+extract).
  `steps[].run` is an imperative program that needs a real OS — **agent/docker
  targets only**, never `mobile`. A package that only has `steps.run` simply isn't
  `mobile`-eligible, and the allocator says so.
- **`vantage` is the allocation contract.** `geo`/`residential` are matched
  against each runner's advertised egress (`egress.go runtime_egress`). No match →
  `unsatisfied_geo`, surfaced ("invite someone in RS"), never faked.
- **Consent is part of the artifact**, so it cannot drift from what runs. The
  runner sees `consent.summary` + `willNot` + `dataShown` verbatim on accept.
- The **secret-bearing parts** (full output endpoint w/ token, vault refs) are
  resolved on-device, never serialized into the shareable manifest or Convex
  (privacy contract; mirror `companion_sync.go`).

### 3a. Acting / agentic / MCP variants

The same envelope carries an operation, an MCP-driven step, or a loosely-defined
agent task. `runner` and `mcp` may point at **local or remote** endpoints.

```yaml
spec:
  task:
    kind: agent                   # loosely-defined; an AI runner figures it out
    goal: |                       # natural-language intent (the owner's loose def)
      Every morning, open the RS bank's public rates page, read the 3-month
      deposit rate, and if it changed since yesterday save it and note the delta.
    engines: [webview, runner, mcp]
    tools: [collection_observe, http_request]   # scoped allowlist the runner may use
    mcp:
      - name: rates-helper
        transport: http
        url: remote://<owner-box>/mcp/rates     # remote MCP reached over the relay
  runner:
    where: device                 # device | owner_box | worker:<id> (remote worker, §4c)
    model: claude-haiku-4-5       # cheap on-device-class model for simple loops
  guard:
    tier: acting                  # read_only | acting  -> drives consent + sandbox
    confirm: per-action           # never | per-run | per-action
    sandbox: required             # proot/container; required for code/operate/agent
```

```yaml
spec:
  task:
    kind: operate
    engines: [playwright]         # or [redroid] for an Android operation
    steps:
      - run: "node do-the-thing.js"
  runtimes: [agent, docker]       # NOT mobile — needs a real browser
  guard: { tier: acting, confirm: per-run, sandbox: required }
```

## 3b. The Compiler — Loose Task → Package

The owner does not have to hand-write `yaml`. They **describe the task loosely**,
and Yaver's **authoring runner** (the existing coding-agent / autopilot stack:
`autopilot.go`, `agent_mode.go`, the `codingAgent` sandbox tools) **writes the
implementation and emits a package**:

```text
owner: "watch superbet's public live football odds from Serbia every ~10 min and
        save them; stop if it shows a captcha"
   |
   v  Yaver authoring runner
   - classifies: kind=collect, engine=webview (rendered odds), vantage=RS+residential
   - GENERATES the extraction selectors / a small collector (code OR declarative
     sources[]), the schedule, the stop conditions, the consent text
   - validates against the yaver/v1 JSON-schema; dry-runs once on the owner box
   - emits yaver.package.yaml  (a reviewable, versioned, signable artifact)
   |
   v  owner reviews + edits (form or raw yaml) -> publish
```

The compiler is itself "an AI runner that writes code/MCP/Playwright/redroid
flow" — exactly the loose-definition→artifact path. Output is **always a concrete,
inspectable package** (not an opaque prompt), so the runner-friend still sees real
domains/actions on the consent screen, and the owner can audit/version/diff it.
For `kind: agent` packages the generated artifact still pins the toolset, MCP
endpoints, guard tier, and consent — the runtime agency is bounded by the package.

## 4. Runtime Targets

### agent (PC / server / always-on box)
The Yaver agent runs the package as a **durable OS unit** via the existing
`companion.go` + `managed_units.go`: systemd `--user` on Linux (`Restart=always`,
`WantedBy=default.target`, `enable-linger`), launchd on macOS (`KeepAlive`,
`RunAtLoad`). This is the "ship a systemd unit" path — already BUILT. `steps.run`
and `image`-less collectors run here.

### docker (host with Docker)
For packages that declare `image:`, the agent runs the step in a container
(reuse `docker_*` ops verbs). This is the "ship a docker service" path. Good for
heavyweight collectors (a real Playwright/Chromium image) the owner doesn't want
to install natively on the friend's box.

### mobile (phone, no PC, no install beyond the app)
The Yaver mobile app runs **declarative `sources[]`** via WebView (rendered DOM)
or `fetch` (JSON), exiting the phone's own residential IP. Periodic execution uses
the **platform-standard** primitives, not a bespoke loop:

```text
Android  WorkManager — periodic min interval 15m, survives reboot, Doze/Standby
         aware, native wifi/charging/battery constraints. (Use the existing
         SandboxService FGS only for a long single run, not the periodic tick.)
iOS      BGTaskScheduler (BGAppRefreshTask short / BGProcessingTask longer) —
         OPPORTUNISTIC: the OS decides timing from battery/network/usage; a few
         minutes CPU per launch. Cannot promise a fixed interval.
```

Sources: [Android/iOS background ops runbook](https://www.sachith.co.uk/background-tasks-and-limits-on-ios-android-ops-runbook-practical-guide-may-4-2026/),
[WorkManager guide](https://medium.com/@chetanshingare2991/mastering-workmanager-in-android-the-ultimate-guide-to-reliable-background-tasks-9e62025cda69),
[Expo BackgroundTask](https://docs.expo.dev/versions/latest/sdk/background-task/).

**Honesty rule, baked into the UI:** Android = "about every 15 min, even closed";
iOS = "when your phone allows (often hourly), and whenever you open the app".
push-to-wake (`schedule.wakeable`) closes the gap when freshness matters. The
phone is loopback-only and is **never reached** — it pulls its package and pushes
results, all outbound over the relay.

### 4c. remote worker (a "worker node" / borrowed box)
A package's runner can live on a **remote box the owner controls or borrows** — a
managed-cloud box, a node from the public-compute fleet, or a friend's paired box —
rather than on the device the friend is holding. Reuse the existing remote-exec
plane: `machine` routing + `mcp_exec_remote.go` + `mcp_remote_proxy.go` dispatch a
package's step/agent loop to `worker:<id>` over the relay, headless. This is the
"yaver slave remote box" target: the friend may just provide the **vantage**
(their residential egress, via the phone) while the **heavy runtime** (Playwright,
redroid, a big model loop) runs on a worker — or the worker does both. The
allocator treats `device` and `worker:<id>` as interchangeable runner locations
subject to the package's `vantage` and `engines`.

### 4d. agentic + MCP runtime (local or remote)
For `kind: agent`/`engines:[runner]`, the target runs a **Yaver runner loop**
(`ops_runner.go`, `autopilot.go`) with the package's pinned toolset and `guard`.
For `engines:[mcp]`, the runtime calls MCP tools through the existing registry:

```text
local  stdio MCP server bundled/launched on the runner (mcp_external.go stdio)
remote http MCP server reached over the relay: remote://<box>/mcp/<name>
       (mcp_external.go http + mcp_remote_proxy.go) — "the MCP can be remote too"
```

So a phone-hosted `agent` package can call a **remote** MCP on the owner's box
(e.g. heavy analysis) while doing only light webview/fetch locally — runner and
MCP each independently local or remote.

### 4d-safety. Acting packages are gated
`operate`/`agent`, redroid taps, browser submits, and MCP **write** tools are the
ACTING tier (§2b). They run **only** under: explicit "this runs code / takes
actions on your device" consent; a sandbox (proot on Android, container on a
box — reuse `sandbox_proot.go`, `container_runner.go`); and `guard.confirm`
(per-run or per-action approval, surfaced to the runner). redroid/Playwright
"observe-only" stays read-only; the moment a flow taps/submits, it is ACTING. No
package silently performs irreversible actions on a friend's device. Owner-side,
`operate` defaults to draft/dry-run where the vertical supports it.

## 5. Allocation — "IP blocking allocations"

The owner accumulates a **roster of runners** (people who accepted packages), each
a vantage with a region. Allocation = matching a package's `vantage` requirement
to runner-vantages:

```text
package needs RS+residential  -> allocate to cousin's phone (RS) and/or a friend's PC (RS)
package needs US              -> allocate to a US runner
package needs anywhere        -> allocate to any accepted runner (or owner's own box)

no runner satisfies a required geo  ->  unsatisfied_geo: surface it, prompt
                                        "invite someone in <geo>"  (never fake it)
multiple runners in one geo         ->  multi-vantage coverage / redundancy;
                                        dedupe-merge in the owner's dataset
```

This is the cross-user generalization of the parent doc's vantage selection. It is
how an owner "handles IP-block allocations": route each region-locked package to a
person who is actually in that region, rather than buying a rotating proxy pool.

## 6. Sharing — owner → friend, from inside Yaver

Reuse the support-link handshake (`backend/convex/support_link.ts`):

```text
1. Owner publishes a package (library entry) and creates a share link / invite.
2. Friend opens it in Yaver (web OR mobile) under THEIR account.
3. Friend sees the consent screen (manifest's consent block, verbatim) and the
   target their device supports (phone -> mobile; PC with agent -> agent/docker).
4. Accept materializes:
     - an infraAccessGrant (origin: "task-package") scoped to THIS package,
     - a packageAllocation row (who/which device/which target),
     - the package cached on-device; secret-bearing config pulled from the owner
       box over the relay on first run.
5. Friend can pause, set wifiOnly/chargingOnly, and "Stop & wipe" anytime; revoke
   is immediate and tears down the grant + local caches + (agent target) the OS unit.
```

Auth boundary: today routines/egress/collection are owner-only
(`AllowGuest:false`, `/mcp` is `s.auth`-gated) and sharing is host→guest only. The
**reverse capability** (a friend's device executing the owner's package and
shipping data back) is the new grant direction this design adds — clamped to one
package, consent-recorded, revocable.

## 7. Data Model

Three planes; the split is the privacy contract (collected data + URLs-with-tokens
never touch Convex; Convex holds handshake + non-sensitive counters).

### 7.1 Convex — registry + handshake + status (privacy-safe)

```ts
// taskPackages — the shareable manifest METADATA (public-safe fields only).
// Full collector spec / output endpoint / secrets are NOT here.
taskPackages: {
  ownerUserId: Id<"users">,
  name: string, version: number, kind: string,   // collect|probe|monitor|exec
  description: string,
  domains: string[],                              // hostnames only, for consent
  runtimes: string[],                             // [mobile, agent, docker]
  vantageGeo: string[], vantageResidential: boolean,
  schedule: string,                               // "every 10m"
  consentSummary: string, willNot: string[], dataShown: string[],
  status: "draft" | "published" | "archived",
}

// packageAllocations — a package bound to a runner+device+target, under consent.
packageAllocations: {
  packageId: Id<"taskPackages">,
  ownerUserId: Id<"users">, runnerUserId: Id<"users">,
  runnerDeviceId: string, target: "mobile" | "agent" | "docker",
  status: "proposed" | "accepted" | "active" | "paused" | "revoked",
  consentAt: number | null,
  constraints: { wifiOnly: boolean, chargingOnly: boolean },
  expiresAt: number | null,
  // counters only — no data, no IPs:
  lastRunAt: number | null, runCount: number, blockCount: number,
  lastStatus: string, lastCountry: string,        // coarse "RS", never the IP
}

// packageRuns — per-cycle audit, COUNTS only.
packageRuns: {
  allocationId: Id<"packageAllocations">,
  at: number, status: string, rowsExtracted: number,
  sourcesOk: number, sourcesBlocked: number, country: string,
}
```

`buildPackagePayload` is the single agent→Convex seam; extend
`convex_privacy_test.go` so a URL/selector/IP/path leak trips at test time.

### 7.2 Owner box — the sink (LOCAL, mostly built)
Reuse `collection_store.go` (vantage-keyed observations/runs/health). Each runner
device is a vantage; `collection_vantage_compare` gives residential-vs-datacenter
diffs. A vertical with its own ingest (e.g. a single-writer `POST /ingest`) can be
the sink instead — the runtime just POSTs there, same outbound model.

### 7.3 Runner device — pull cache + push queue (LOCAL, new)
`package-cache` (last manifest + resolved config), `pending-uploads/`
(store-and-forward, mirrors `jobqueue.go`, survives restart, drained on ack),
`run-log` (the runner's own transparency log). "Stop & wipe" clears all three.

## 8. Data Flow

```text
OWNER (web)        CONVEX                RUNNER DEVICE (phone or PC)        OWNER BOX
 | publish pkg --->| taskPackages         |                                  |
 | share link -----+---- invite --------->| consent screen (manifest verbatim)|
 |                 |<-- accept -----------| materialize grant + allocation    |
 |                 |  status=active       |-- pull resolved config -------->   | (relay, authed)
 |                 |                      |<-- config (endpoint, selectors)-   |
 |                 |                      | [WorkManager/BGTask | systemd tick]|
 |                 |                      | run sources (own residential IP)   |
 |                 |                      | extract / detect block -> stop     |
 |                 |                      |-- push results --------------->    | collection_store / ingest
 |                 |<-- run counters -----|   (vantage = device, <geo>)        |
 | roster + dataset|  runCount,lastStatus |                                    |
 | + vantage diff <+-------------------------------------------------------- read local
```

## 9. UI

### 9.1 Owner — Web: "Packages"
```text
Packages (library)         create/edit a yaver.package.yaml (form OR raw editor),
                           validate, publish, version.
Runners (roster)           people who accepted; device, target, geo, freshness, state.
Allocations                grid: package x runner; allocate by region or 1-tap;
                           "unsatisfied_geo: no runner in <X> — invite someone".
Region coverage map        which vantages cover which packages; gaps highlighted.
Package detail             live dataset (per vantage), cross-vantage compare,
                           consent record, runs log (counts only), pause/revoke.
```

### 9.2 Runner (friend) — Mobile/Web: "Shared with you" / "Helping <owner>"
```text
Invite       consent-first: "<owner> wants your DEVICE to run: <name>.
             It will: <consent.summary>.  It will NOT: <willNot>.
             Data shared: <dataShown>.  Results go to <owner>.
             Run on: [x] Wi-Fi  [ ] charging      [Decline] [Accept & start]
Active       ● Running · last 2m ago · next ~15m (Android) / "when iOS allows"
             Today: 132 runs · 3,940 rows · 1 blocked (geo)
             [Pause]                              [Stop & wipe]
Activity log every run the device did (domain, time, status) — the runner's record.
```
On CAPTCHA/login: source blocked, run stops, shown in the log. Never solved.

### 9.3 MCP / CLI (owner)
```text
package_publish {manifest}      package_share {packageId, runnerInvite}
package_list / package_status   package_allocate {packageId, device, target}
package_pause / _resume / _revoke
```
Owner-only; the runner side is the consent UI, not an MCP surface. CLI:
`yaver package publish ./yaver.package.yaml`, `yaver package share <id> --to <email>`.

## 10. Use Cases (betting is one row)

| Vertical | Package does | Vantage need |
|---|---|---|
| **Betting** (yaver-bet, the original example) | collect public live odds from licensed books | residential, specific country |
| **Fintech / compliance** ([GeoComply](https://www.geocomply.com/), [fintech GEO](https://upgrowth.in/geo-regulated-industries-fintech-compliance-playbook-2026/)) | verify advertised rates/fees/returns render correctly per region; catch geo-gated disclosures | region-specific |
| **E-commerce price/availability** ([regional availability](https://www.godatafeed.com/blog/google-regional-availability)) | localized price, stock, delivery windows, store assortment | city/country |
| **Ad verification** ([ProxyScrape](https://proxyscrape.com/usecases/ad-verification)) | confirm creatives/placements render from real consumer IPs per market | residential, many geos |
| **Localized SEO / compliance** ([intl SEO](https://searchengineland.com/guide/international-seo-best-practices)) | hreflang, GDPR notices, SERP differences, regional landing pages | per-country |
| **Uptime / availability from region** | is my service reachable + correct from country X | any, regional |
| **App-store / catalog checks** | which app/catalog/content is offered in market X | mobile, regional |
| **Operations (acting)** | file a recurring report, run a checkout-availability flow, drive an Android app step (redroid), trigger an MCP write tool | any target; ACTING-gated |
| **Agentic** (loose def) | "watch X and do Y when Z" — runner figures out the steps, calls tools/MCP | runner local/remote |

All consume the **same** package/allocation/vantage/dataset machinery. The vertical
owns only interpretation; Yaver owns shipping, scheduling, consent, vantage, audit.

## 11. Reuse vs New

| Capability | Source | Status |
|---|---|---|
| Durable OS unit on a PC (systemd/launchd) | `companion.go`, `managed_units.go` | BUILT |
| Containerized step | `docker_*` ops verbs | BUILT |
| Periodic firing (owner side) | `scheduler.go` / routines | BUILT |
| Vantage / egress identity, geo match | `egress.go` | BUILT |
| Vantage-keyed dataset + block model + compare | `collection_store.go`, `collection_ops.go` | BUILT |
| Android FGS + wake lock (long runs) | `SandboxService.kt` | BUILT (T5 HW-unverified) |
| Outbound relay pull/push, push-to-wake | mobile relay, `/blackbox/command-stream` | BUILT |
| Store-and-forward retry model to mirror | `jobqueue.go` | BUILT (reference) |
| Cross-user handshake to reuse | `support_link.ts`, `infraAccessGrants` | BUILT |
| Authoring/agent runner (loose task → code) | `autopilot.go`, `agent_mode.go`, `codingAgent` | BUILT |
| Remote-exec plane (worker box / remote runner) | `mcp_exec_remote.go`, `mcp_remote_proxy.go`, `machine` routing | BUILT |
| External/remote MCP registry (local stdio + remote http) | `mcp_external.go`, `mcp_remote_proxy.go` | BUILT |
| redroid Android observe/operate | `studio/redroid.go` | BUILT |
| Sandbox for ACTING tier (proot / container) | `sandbox_proot.go`, `container_runner.go` | BUILT |
| Worker fleet to borrow runners from | public-compute fleet / managed cloud | BUILT (fleet design-only) |
| **`yaver.package.yaml` format + validator + signing** | new | **NEW** |
| **Compiler: loose NL task → concrete package artifact** | new (wraps authoring runner) | **NEW** |
| **Kind/engine model + ACTING-tier guard + consent tiers** | new | **NEW** |
| **Agent/MCP runtime binding (pinned toolset, local/remote MCP)** | new (wraps runner + mcp_external) | **NEW** |
| **Allocator (vantage↔package matching, unsatisfied_geo)** | new | **NEW** |
| **Reverse-capability grant (friend runs owner's package)** | extend `infraAccessGrants` | **NEW** |
| **Mobile runner: WorkManager/BGTask + WebView/fetch collector + store-forward** | mobile | **NEW** |
| **Owner web Packages/Runners/Allocations + runner consent UI** | web + mobile | **NEW** |
| **Convex taskPackages/packageAllocations/packageRuns + privacy seam/test** | backend | **NEW** |

## 12. Phased Plan
```text
P1  Package format + validator (yaver/v1), Convex registry + privacy seam/test.
    Start with READ-ONLY kinds (collect/probe/monitor) only.
P2  Owner web: author/publish a package; share link (reuse support-link redeem).
P2b Compiler: loose NL task -> generated collect-kind package (wrap authoring
    runner; validate + dry-run on owner box; owner reviews the artifact).
P3  Reverse-capability grant + accept/consent flow (web + mobile).
P4  agent target: run a published package as a companion durable unit (+docker).
P5  mobile target: WorkManager/BGTask runner + WebView/fetch collector +
    store-and-forward; push results to owner box / ingest.
P5b remote worker target (worker:<id> via mcp_exec_remote) + remote MCP binding;
    phone-provides-vantage / worker-does-heavy-runtime split.
P6  Allocator UI: roster, allocations, region coverage, unsatisfied_geo.
P7  Dataset + cross-vantage compare; first vertical adapter (yaver-bet) consumes it.
P7b ACTING tier: operate/agent kinds + playwright/redroid engines, guard +
    sandbox + per-action consent; draft/dry-run defaults. Gated behind P1-P6 proving
    the read-only path first.
P8  T5 on-device durability campaign; publish honest periodicity per platform.
```

## 13. Acceptance Criteria
```text
1.  Owner authors a yaver.package.yaml, validates, and publishes it.
2.  Owner shares it to a friend; friend (separate account) accepts an
    informed-consent screen rendered FROM the manifest.
3.  Friend's device is matched to a target it supports (phone->mobile, PC->agent/docker).
4.  The package runs on the friend's device with ITS OWN egress and ships
    vantage-tagged results; data never transits Convex.
5.  A CAPTCHA/login/geo-block pauses that source per vantage; no bypass, no IP rotation.
6.  Allocator reports unsatisfied_geo when no runner covers a required region.
7.  Friend can pause, constrain (wifi/charging), and "Stop & wipe"; revoke is immediate.
8.  Same package runs on BOTH a phone (mobile) and a PC (agent), proving portability.
9.  No login/payment/KYC/irreversible action/credential storage on any path.
10. convex_privacy_test.go proves no URL/selector/IP/path leaks to Convex.
11. UI states periodicity honestly per platform (Android ~15m; iOS opportunistic).
12. ≥2 distinct verticals (e.g. betting + price-watch) run on the SAME machinery.
```

## 14. Open Questions
- Manifest language: YAML now; do we want an HCL-style typed variant later for
  imperative `steps`? (Lean: YAML + JSON-schema validation; defer HCL.)
- Signing/trust: packages should be signed by the owner so a runner verifies
  origin; where does the key live? (Lean: owner device key, like provisioning.)
- Imperative `steps.run` on a friend's PC is arbitrary code execution on their
  machine — gate hard behind explicit "this package runs code on your computer"
  consent + sandbox (reuse proot/container), separate from the safe declarative
  `sources[]` path which needs no such warning.
- iOS periodicity floor vs push-to-wake — acceptable for v1, or agent/Android-only
  for latency-sensitive packages?
- Marketplace later? (Public package registry — out of scope; trust + abuse model
  must be designed first, given the proxyware cautionary tale.)

## 15. Compliance & Ethics (the proxyware lesson)
Proxyware's failure mode is **opacity**: users don't know whose traffic flows
through them or what it triggers ([Trend Micro](https://www.trendmicro.com/en_us/research/23/b/hijacking-your-bandwidth-how-proxyware-apps-open-you-up-to-risk.html)).
Yaver's design inverts every one of those:

```text
- Named owner + named task, not an anonymous resold pool.
- The runner sees the exact domains, frequency, data, and "will NOT" list, from
  the manifest, before accepting and in a live activity log after.
- No IP rotation, no third-party buyers, no traffic the runner can't enumerate.
- Public / authorized data only; stop on challenge; no accounts/payments/KYC.
- One-tap revoke + wipe; consent travels with the artifact and can't drift.
```

That transparency is not just compliance theater — it is the wedge that makes
"run my friend's task on my phone" trustworthy where proxyware is not.

## 16. Related
```text
docs/user-directed-data-collection-runtimes.md   collect-kind runtime/vantage/egress model
desktop/agent/companion.go, managed_units.go      agent/docker durable-unit targets (BUILT)
desktop/agent/collection_store.go, egress.go      sink + vantage identity (BUILT)
backend/convex/support_link.ts                    cross-user handshake to reuse (BUILT)
mobile/android/.../SandboxService.kt              on-phone supervisor (BUILT, T5 TBD)
```
Vertical applications (betting, etc.) live in their own (often private) repos and
reference this doc. Do not name a vertical or a specific source in this file.
```
