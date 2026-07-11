# Yaver Managed / Arbitrage Resale — Security Threat Model & Pre‑Sale Hardening Gate

> **Status:** analysis + gate checklist (no product code changed by this doc).
> **Scope:** the *managed arbitrage* business model — the owner connects **their
> own** Hetzner account, provisions boxes on it, and **resells** compute+orchestration
> to paying customers with a markup. Customers do **not** connect their own Hetzner
> by default (BYO remains an opt‑in escape hatch on a separate code path).
> **Audience:** the owner, before charging the first external customer.
>
> **Read `CLAUDE.md` first.** Code is source of truth; every `file:line` below was
> grepped on the date noted and MUST be re‑verified before you act on it.

---

## 0. Why this model is the highest‑stakes posture in Yaver

Three properties combine into a threat surface unlike anything else in the product:

1. **The code is open source.** Every gate, endpoint, protocol, and secret‑handling
   path is on public GitHub. There is **no security‑through‑obscurity** available —
   assume the attacker has read all of it.
2. **Customers get root on a box that runs on *your* Hetzner account.** They "use it
   as they wish." The OS is theirs; the **account** is yours.
3. **It is your money.** A powered‑off Hetzner server still bills full price; a leaked
   account token can spin up unlimited servers or delete everything; one abusive box
   can get your **whole account suspended**.

Everything in this document derives from a single attacker model:

> **The attacker is a paying (or trial) customer who has: the full source code, root
> on the box they rented, and a financial incentive to (a) use more than they paid
> for, (b) reach your platform token, (c) abuse your IPs, or (d) pivot to your control
> plane or another tenant.**

---

## 1. Assets — what a hacker actually wants

| # | Asset | Where it lives | If compromised |
|---|-------|----------------|----------------|
| A1 | **Platform Hetzner token** (`HCLOUD_TOKEN`) | Convex env only (`backend/convex/cloudMachines.ts:1336,1930`) | Unlimited spend / delete every server in the project — *"makes me poor."* |
| A2 | **Cloudflare token** (`CF_API_TOKEN`) | Convex env only (same file) | DNS hijack of `yaver.io`, cert issuance. |
| A3 | **Your Hetzner *account*** (not just one box) | Hetzner control panel | Abuse report on one box → **account‑wide suspension** → every customer down. |
| A4 | **Relay** (rendezvous for all tenants) | `relay/` (public box `yaver-relay-free`) | Cross‑tenant tunnels; impersonation if per‑user scoping is weak. |
| A5 | **Other tenants' data / compute / vaults** | Each customer box | Lateral movement = trust collapse, disclosure, resource theft. |
| A6 | **Your control plane** (Convex, admin routes) | Convex prod `perceptive-minnow-557` | Billing bypass, log tampering, provisioning on your dime. |
| A7 | **Your own dev/Talos box + repos** | Separate box on the same account (today) | A customer reaching it = source/infra compromise. |
| A8 | **A customer's own scoped credentials** | That customer's box vault | Their loss, your liability + churn. |

---

## 2. Trust boundaries (the map that must not be crossed)

```
  UNTRUSTED                         SEMI‑TRUSTED                 TRUSTED (yours)
 ┌──────────────┐   relay (auth‑    ┌───────────────┐          ┌──────────────────┐
 │ customer box │   scoped, E2E     │  public relay │          │  Convex control  │
 │  (root, code │◀─ pass‑through ──▶│  (no secrets) │◀────────▶│  plane + env     │
 │   is public) │                   └───────────────┘          │  HCLOUD/CF token │
 └──────────────┘                                              └──────────────────┘
        │  MUST NOT reach: A1 A2 A6 A7, other tenants (A5)             ▲
        └── may reach ONLY: its own scope + relay endpoints scoped to its user
                                                                        │
                        provisioning / billing / idle‑reap decisions ───┘
                        happen HERE, never on the box
```

**The Golden Rule:** *the control plane is authoritative; the box is never trusted.*
Anything a rooted customer can read on their box is effectively public, and anything
the box "reports" can be a lie. Security decisions live in Convex, enforced with the
platform token the box never sees.

---

## 3. Token security — the "unreachable tokens" mandate

The owner's directive: **tokens must be unreachable to an attacker, period.** Translated
into enforceable rules, per token:

### 3.1 Money tokens — Hetzner (A1) + Cloudflare (A2)

**Current state (verified 2026‑07‑11): CORRECT.**
- Read **only** in Convex actions (`cloudMachines.ts:1336,1930`, `cloudLifecycle.ts:576`);
  no literals in the tree (secret audit 2026‑07‑07 found zero); **no provision path
  injects a token into `user_data`/cloud‑init** (grep empty across `cloud_deploy.go`,
  `cloud_stopstart.go`, `cloud_byo_provision.go`).
- Confirmed present in **Convex prod env** (`convex env list --prod`): `HCLOUD_TOKEN`,
  `CF_API_TOKEN`, `CF_ZONE_ID`, `CLOUD_PREVIEW_OWNER_EMAIL/_USER_IDS`. GH Actions carries
  the same via `${{ secrets.HCLOUD_TOKEN }}` / `${{ secrets.CLOUDFLARE_API_TOKEN }}`.
  **Neither store ever ships the value to a box or a client.**
- **Full‑history scan (2026‑07‑11):** the Hetzner / Cloudflare / npm tokens and any
  private key were **NEVER committed** — not in HEAD, not in history. The only
  history secret is the Android keystore password (`yaver2024release`, commit
  `b8361ffc6`), which is **not a spend vector** and is handled by upload‑key rotation,
  not a public‑repo history rewrite (see §8).

**Rules to keep it that way (make them CI‑ and review‑enforced):**
- **R1 — Money tokens live ONLY in Convex env, are read ONLY in Convex actions, and
  are NEVER** serialized into a box, a payload, a log line, an error string, or
  `user_data`. Hetzner metadata (`169.254.169.254`) is readable by a rooted box — so
  **nothing secret goes into metadata/user_data, ever.**
- **R2 — Retire the agent‑side `HCLOUD_TOKEN` plane** (`desktop/agent/launch_hetzner.go:22`,
  `launch_cmd.go:418`, `yaver launch`). It reads a Hetzner token from the *box's* env.
  It must be **impossible** for that plane to run on a managed customer box. (Already
  slated as "Phase 2 retire the RemoteManager/`yaver launch` env‑token plane"; for
  arbitrage it is a **P0 security requirement**, not cleanup.)
- **R3 — Blast‑radius via separate Hetzner *projects*.** Hetzner API tokens are
  **project‑scoped but server‑unlimited** — one token controls every server in its
  project and cannot be scoped narrower. The *only* way to contain a leak is
  **multiple projects**: `owner‑dev` (your Talos/Yaver box) ≠ `managed‑pool` ≠
  optional per‑risk‑tier pools. A leaked managed‑pool token then cannot touch your
  dev box (A7) or vice versa.
- **R4 — Hetzner budget/spend alarms** on the pool project so a mining abuser or a
  provisioning bug cannot silently run to €thousands.
- **R5 — Rotation runbook.** Rotate on any anomaly; keep the CI secret‑literal check
  green; audit `cloudMachines.ts:1431` "operator‑only injection points" — anything
  injected into a box is **readable by a rooted customer**, so treat that surface as
  public and confirm no secret rides it.

### 3.2 Relay password (A4) — the shared‑secret problem

The relay is shared by all tenants and password‑protected. **Because the code is
public, the *mechanism* is known to every customer**, and the relay password reaches
each box. Therefore the password is **not** a tenant boundary — the **per‑user
token‑hash scoping is.**
- Verified scoping exists: refuse‑on‑collision (`relay/server.go:562`, audit C‑1) and
  refuse‑without‑password (`:964`).
- **P0 audit:** prove a customer **cannot** use the relay to reach *another* tenant's
  box — i.e. tunnels are bound to the authenticated user, not merely to "knows the
  relay password." This is the multi‑tenant chokepoint; validate it adversarially,
  not by reading the happy path.
- Do **not** hand managed customers any relay *admin* capability; scope them to their
  own device set.

### 3.3 User auth tokens + vaults (A8) — already strong

**Current state (verified 2026‑07‑11): STRONG.**
- Vault master key is a **per‑machine random key**, keychain‑backed, **NOT
  auth‑token‑derived** (`masterKeyFilename`/`masterKeyLen`; see
  `project_vault_keychain_redesign`). Consequences that help arbitrage directly:
  - Stealing box A's on‑disk auth token **cannot decrypt box B's vault** (different
    random master keys).
  - Parking/snapshot/restore does **not** lock the vault (key isn't token‑derived).
- Keep it this way. A customer reading their *own* box's vault is fine — it's theirs.
  Cross‑tenant reads are blocked by (a) different master keys and (b) network isolation
  (§4.3), never by trusting the OS boundary alone.

### 3.4 Guest Hermes‑bundle inherited token — a real exfil vector **if** you sell "load your app"

`mobile/ios/Yaver/YaverInfo.swift` **hands loaded guest code the host's auth token and
relay password** (`setInheritedAuth` `:113`; read as `Authorization: Bearer` at `:232`;
relay password pushed independently near `:120`). This is the documented **"code you
load in Yaver == your Yaver account"** property (2026‑07‑07 audit, HIGH by‑design).
- For **your own dogfooding** (sfmg on your phone) this is fine.
- For a **sold** "load your app into Yaver" feature it is a **token‑theft vector**: a
  malicious guest bundle exfiltrates the host's full account token.
- **Requirement before selling that surface:** either (a) **sandbox** guest bundles
  from `YaverInfo`, or (b) restrict loading to **first‑party/trusted** bundles, or (c)
  hand guest bundles an **ephemeral, capability‑scoped token** instead of the full
  account token. Until one is done, do **not** market third‑party app loading to
  untrusted users.

---

### 3.6 In‑transit / anti‑sniffing — why the money token is unsniffable

The strongest property here is not "we encrypt the token" — it is **the money token
never crosses a wire an attacker can reach:**
- **Hetzner/CF tokens are used only server‑to‑server:** Convex action → Hetzner/CF API
  (TLS, inside Convex) and GitHub runner → Hetzner API (HTTPS, on GitHub infra, masked
  in logs). Both are *your control plane → provider*. A customer on their rented box
  **never sees the token on the wire at all.** Not "encrypted so they can't read it" —
  **never present on their segment.**
- **Transport is TLS end‑to‑end elsewhere too:** agent↔Convex = HTTPS; relay = QUIC/TLS
  1.3 (`quic.go`); LAN SDK = self‑signed TLS on `:18443` (`tls.go`, `ListenAndServeTLS`
  `httpserver.go:1491`); LAN beacon `:19837` carries only a token‑*hash* fingerprint,
  never the raw token.
- **The one plaintext‑on‑wire item is the *user* auth token, not the money token:** the
  agent's `:18080` is plain HTTP (`s.server.ListenAndServe()` `httpserver.go:1518`) and
  binds `0.0.0.0`. On loopback that's fine; on a LAN/public box the `Authorization:
  Bearer <user‑token>` could be sniffed. **Mitigation (P0 for managed):** firewall
  `:18080` shut on any public‑IP box — force off‑box traffic onto `:18443`/relay — so no
  bearer ever crosses an untrusted segment in the clear. (Same requirement as §4.2/§4.4.)

**R6 — operational: `npx convex env list` prints VALUES, not just names.** Never run it
in a shared terminal, a screen‑share, a CI log, or anything recorded. If a money‑token
value is ever displayed (env list, clipboard, log), **rotate it** — regenerate in the
Hetzner/Cloudflare console and `convex env set … --prod` + `gh secret set …`. Storage is
correct (Convex env / GH secrets); the hazard is *displaying* it. Use
`scripts/rotate-money-token.sh` so a new value never transits a transcript.

---

## 4. Threat catalog → mitigations

### 4.1 Idle‑squat & billing bypass — *box lies about usage / never stops*

- **Threat:** a rooted customer patches out the on‑box idle reaper to squat on your
  compute, and/or under‑reports usage.
- **Correction to the earlier design:** the **agent‑side self‑reaper (Phase 4) is
  BYO‑ONLY.** For managed, **the reaper moves to the control plane** — Convex observes
  idle from *outside* the box and calls snapshot+delete with the platform token. This
  is the seed already present as managed "auto‑stop‑before‑zero" in `cloudMachines.ts`.
- **Meter from Hetzner's billing API / control‑plane observation, never box
  self‑report.** A patched agent can lie about what it did; it cannot lie about what
  Hetzner charges you.
- **Prepaid, fail‑closed:** never provision without cleared payment (`subscriptions.isActive`
  gate + prepaid wallet). Wallet burn‑down must be **atomic** — no double‑spend, no
  negative‑balance squat; auto‑stop fires with the stop‑transition cost reserved.

### 4.2 Egress abuse → **account‑wide suspension** (business‑ending)

- **Threat:** one customer mines / DoSes / scrapes / spams from your datacenter IP →
  Hetzner flags the **account** → every customer down + you banned. (CLAUDE.md "do no
  harm to third parties," here existential.)
- **Hard truth:** a rooted box **bypasses Yaver's Policy Guard** (`access_policy.go`)
  trivially by running raw tools outside the agent. The Yaver tool‑gate is **not** the
  boundary for a rooted box.
- **Real boundary = network layer you own from outside:**
  - **Hetzner Cloud Firewall per managed box** — egress allow/deny set by the control
    plane; the customer cannot remove it (account‑side, not OS‑side).
  - **Egress anomaly detection** → auto snapshot+delete a box that trips abuse
    heuristics (traffic spikes, port‑scan fan‑out, known‑abuse destinations).
  - **Prepaid + ToS + kill‑switch** so an abuser has skin in the game and a contract
    to terminate.
- `egress_proxy.go` is about **lending your** egress (opt‑in, default‑off,
  RFC1918‑blocked) — a *different* concern; do not confuse it with constraining a
  customer's egress.

### 4.3 Lateral movement — box → control plane / other tenant / your dev box

- **Network segmentation:** each managed box in its own network; **deny inter‑tenant
  traffic, deny to Convex admin routes, deny to relay privileged paths, deny to the
  `owner‑dev` project (A7).**
- **Relay scoping** (§3.2) is the cross‑tenant chokepoint — audit it adversarially.
- **Metadata is safe** (no account token exposed) **only as long as R1 holds** — keep
  secrets out of `user_data`.

### 4.4 Control‑plane tampering — *public/unauth endpoints*

- **`backend/convex/authLogs.ts` is PUBLIC** (`writeLog`/`recentLogs`/`clearAll` are
  unauth `mutation`/`query`, `:4,20,32`). Anyone with the deploy URL can **read, flood,
  or wipe** auth logs. **P0 one‑line fix:** `internalMutation` / owner check. Matters
  more now — attackers read the code and know this route.
- **Local agent `/webhooks/stripe|lemonsqueezy` unauth+unsigned** (`httpserver.go:694`,
  `invoices.go:511`) — a LAN attacker flips invoices to "paid." No money moves, but wire
  the existing LS verifier (`lemonsqueezy.go:808`).
- **Agent binds `0.0.0.0` by default** (`httpserver.go:1517`) — fine behind NAT, but a
  **public‑IP managed box needs a firewall** in front of `:18080`/`:4433` (ties to §4.2).

### 4.5 Trial / sybil abuse — *free compute farming*

- **Prepaid‑before‑provision is the antidote** — no free compute without money down.
- Low trial caps, card/phone verification, `ownerAllowlist` (`CLOUD_PREVIEW_OWNER_EMAIL`
  Convex env) **for the owner only** — never a hardcoded literal (public repo +
  `feedback_yaver_is_for_everyone`).

### 4.6 Audit / forensics blind spots

- Failed logins never durably logged; `exec_command` / vault reads / guest denials
  unaudited; `agent.log` wipeable by any token holder via `yaver_clear_logs`; no
  rate‑limit / IP‑attribution on agent `:18080` auth. **Fix before selling** — you need
  attributable, tamper‑resistant logs when a customer disputes usage or triggers abuse.

---

## 5. Pre‑sale hardening gate — checklist

**Do not charge an external customer until every P0 is checked.**

### P0 — blocks the first sale
- [ ] **Retire agent‑side `HCLOUD_TOKEN` plane** so it can never run on a customer box (`launch_hetzner.go`, `yaver launch`). *(R2)*
- [ ] **Managed reaper is control‑plane‑only**; on‑box reaper disabled/absent for managed. Box‑untrusted idle stop verified. *(§4.1)*
- [ ] **Usage metered from Hetzner billing API**, not box self‑report. *(§4.1)*
- [ ] **Prepaid fail‑closed** — provision refused without cleared payment; wallet burn‑down atomic; auto‑stop reserves transition cost. *(§4.1, §4.5)*
- [ ] **Separate Hetzner project** for the managed pool (isolated from `owner‑dev`) + **budget alarm**. *(R3, R4)*
- [ ] **Per‑box Hetzner Cloud Firewall** egress boundary; public‑IP box not exposing `:18080`. *(§4.2, §4.4)*
- [ ] **Relay cross‑tenant scoping audit** — prove box A cannot reach box B via the relay. *(§3.2)*
- [ ] **Fix public `authLogs`** → `internalMutation`/owner. *(`authLogs.ts:4,20,32`)*
- [ ] **git‑history secret scrub** + confirm leaked keystore password (`yaver2024release`) was rotated. Public repo. *(2026‑07‑07 audit)*
- [ ] **Guest‑bundle token inheritance gated/scoped** *if* selling "load your app" (`YaverInfo.swift:113`). Otherwise explicitly out of scope for untrusted users. *(§3.4)*

### P1 — before scaling past a handful of customers
- [ ] Durable, non‑wipeable audit log (failed logins, exec, vault reads, guest denials). *(§4.6)*
- [ ] Rate‑limit + IP attribution on agent `:18080` auth. *(§4.6)*
- [ ] Wire signature verification on agent stripe/LS webhooks. *(`httpserver.go:694`, `lemonsqueezy.go:808`)*
- [ ] Egress anomaly detection → auto snapshot+delete abuser. *(§4.2)*
- [ ] Fix vault prompt echo (`vault_cmd.go:397`). *(`project_vault_prompt_echo_bug`)*
- [ ] ToS + abuse kill‑switch documented and wired.

### P2 — posture / later
- [ ] **Do NOT ship shared‑kernel multi‑tenant** (`container_runner.go`/`Dockerfile.sandbox`) for hostile customers; stay **one VM per payer**. Revisit only with gVisor/Kata/Firecracker.
- [ ] cli MCP SDK dep vulns (ReDoS/DNS‑rebind).
- [ ] Phone `:8347` push contract — add auth before implementing (`cli/src/transport.js`).

---

## 6. Already correct — do NOT re‑fix (waste of time / regression risk)

- All **9 criticals** from the 2026‑05‑02 audit are **verified fixed** in current code
  (relay hijack, support scope, deploy webhook, dev‑bundle disclosure, X‑Relay‑Password
  constant‑time, ops guest pivot, feedback traversal ×2, relay open‑mode).
- **Card data is fully PCI‑offloaded** to Stripe/LemonSqueezy hosted checkout — never
  touches Yaver; prod LS webhook is signature‑verified + fail‑closed.
- **Vault crypto is solid** — NaCl secretbox + per‑machine random master key (not
  auth‑token‑derived).
- **Platform token is Convex‑env‑only**, no literals, **not injected into `user_data`**.
- Constant‑time compares everywhere (`secret_compare.go`); Convex privacy test real
  (~120 forbidden keys + path canary); no hardcoded secrets in tracked HEAD.

---

## 7. The two‑tier boundary (keep them on separate code paths *and* separate projects)

| | **BYO** ("use your own machines") | **Managed arbitrage** (primary GTM) |
|---|---|---|
| Whose money | customer's Hetzner | **owner's** |
| Token location | customer vault | Convex env, **own isolated project** |
| Who reaps idle | **on‑box** self‑reaper (Phase 4) | **control plane** (box untrusted) |
| Usage metering | n/a | **Hetzner API**, not box self‑report |
| Egress boundary | customer's problem | **owner's Hetzner Cloud Firewall** |
| Blast radius | 1 customer's account | **isolate via separate project + budget cap** |
| Multi‑tenant | n/a | **one VM per payer** (no shared kernel) |

---

*Grounded on 2026‑07‑11 against current `main`. Re‑verify every `file:line` before
acting — other threads move constants in parallel (CLAUDE.md).*
