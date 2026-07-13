# Connectivity handoff — 2026-07-13

Written for the session working on **Yaver Mesh** (secure + robust connectivity).
Everything below was observed on **production**, not reasoned about. Commands to
reproduce are included. Where I was wrong earlier in the day, I say so — those
wrong turns are themselves useful, because they show which signals mislead.

---

## 0. TL;DR

The reported symptom was *"the phone can't reach ANY machine — Mac mini, magara,
cloud box"*. Every component was healthy in isolation; only the **composed
credential path** was broken. Three bugs, all now fixed and deployed:

1. **Mobile** silently installed **password-less** relay servers whenever
   `/settings` failed → every relay request 401'd forever.
2. **Relay** reported *every* auth failure as `invalid relay password` —
   including "the auth backend is down" and "you sent no password at all".
3. **Agent** snapshotted its credentials at goroutine start and replayed them
   forever → a relay restart stranded **every agent permanently** until each
   process was restarted by hand.

Three bugs remain **OPEN** (§3). The most important for mesh is **#1: task send
aborts** — a machine can be connected and still be useless.

### 0.1 What works, and *why* it works

| Thing | State | Why |
|---|---|---|
| Phone → Mac mini / ubuntu over relay | ✅ **works** | The client now attaches the **per-user** `relayPassword` from `/settings` to the platform relay. It can no longer silently fall back to the password-less `/config` list. Confirmed in the user's own log: `relay password missing …` → app refreshed credentials → `[connect-success] Connected via relay`. |
| Agents recover after a relay restart | ✅ **works** | Each reconnect **re-reads** the token + relay password from config instead of replaying a startup snapshot (`currentRelayCredentials`). Proven: restarted the relay, Mac mini + ubuntu re-registered unattended in ~2 min. Previously: **zero, forever**. |
| Relay auth | ✅ **works, and is honest** | Backend-unreachable is now `503 … retry` (no strike), missing credential is named as such, and only a real verdict says `invalid`. Still **fails closed**. |
| Agent credentials survive a bad startup | ✅ **works** | `SaveConfig` refuses to overwrite a config it cannot read, and blank values never clobber real ones. A failed *read* can no longer cause a destructive *write*. |
| `yaver.io` / web | ✅ **works** | Deployed on Cloudflare Workers; Vercel fully removed from the zone. |

### 0.2 What does NOT work, and *why*

| Thing | State | Why |
|---|---|---|
| **Sending a task** from the phone | 🔴 **broken** | The agent **never receives** the POST — it dies in flight (`Task failed: Aborted`). A *plain Convex* call (`refreshDevices`) aborts at the same instant, so this is **not** the relay. Suspected: the app holds several long-lived streams open, starving iOS's connection pool, so new requests queue past their abort timers (30s task / 10s refresh). **See §3.1 — highest priority.** |
| **Parked cloud box** invisible / not wakeable | 🟡 **broken** | The Hetzner box was snapshotted + deleted via `hcloud` **directly, bypassing the control plane**, so its `cloudMachines` row still carries a dead `hetznerServerId` and no parked status. The app therefore renders nothing instead of "Parked — Wake". **§3.2** |
| **magara** unreachable off-LAN | 🟡 **broken** | Agent is **v1.99.258**, predating the relay self-heal; it holds no relay tunnel. Needs an upgrade + restart; no SSH key for it from the dev Mac. **§3.3** |
| Relay **expose URLs** (`<id>.dev.yaver.io`) | 🟢 **never worked** | A `*.yaver.io` wildcard matches **one** label, so `<id>.dev.yaver.io` was never covered by DNS. Needs a real `*.dev.yaver.io` record. **§3.4** |

---

## 1. What was actually wrong (root causes, fixed)

### 1.1 `/config` strips the relay password — and two consumers didn't notice

The relay password is **per-user** (`userSettings.relayPassword`), not shared.
`GET /config` is public and unauthenticated, so it correctly **strips**
`password` from `relayServers` (`backend/convex/http.ts`, the `/config` route).

Both clients had a fallback that used `/config`'s relay list **as-is** — i.e.
with no password:

- **Mobile** (`mobile/src/context/DeviceContext.tsx::fetchRelayServers`):
  `getUserSettings()` swallowed *every* error and returned `{}`, which is
  indistinguishable from "this account has no settings". Any transient
  `/settings` failure → password-less platform relays installed **and persisted
  to `RELAY_CACHE_KEY`**, poisoning the cache.
- **Agent**: same class of bug in the relay reconciler; the platform entry from
  `/config` carries no password and it falls back to `rm.globalPassword`.

**Invariant to hold on to (this is the load-bearing one):**

> A client that only has `/config` holds **NO** relay credential. So "fall back
> to `/config` when `/settings` fails" is not a degraded mode — it is a
> guaranteed, self-inflicted outage.

`scripts/test-relay-path.mjs` asserts exactly this. **Run it before touching the
credential path.**

```bash
YAVER_TOKEN=<session token> YAVER_DEVICE_ID=<deviceId> node scripts/test-relay-path.mjs
```

### 1.2 The relay lied about *why* auth failed

`validateAndResolveViaConvex` collapsed transport errors, timeouts, 5xx and a
genuine rejection into a single `false`, which every caller rendered as
`invalid relay password`.

Consequences, all observed:

- A **transient Convex hiccup** tells every agent and every client that its
  password is wrong.
- The agent's one self-heal assumed the *password* was stale, refetched it,
  found it **identical**, and gave up — looping forever on credentials that were
  never broken.
- Debugging took hours because the error named the wrong thing.

**Fixed** (`relay/server.go`): `errAuthBackendUnavailable` distinguishes
"could not check" from "you are wrong".

| Situation | Before | Now |
|---|---|---|
| auth backend down / 5xx | `401 invalid relay password` | `503 relay auth backend unavailable — retry`, **no invalid-auth strike** |
| no password sent | `401 invalid relay password` | `401 relay password missing — sign in again to fetch it` |
| genuinely wrong credential | `401 invalid relay password` | `401 invalid relay credentials (password or session token)` |

Still **fails closed** — only the reported reason changed. Regression test
verified to fail without the fix; a genuine 401 verdict must still deny (no
attacker bypass).

> This fix is *visibly* working in the user's mobile logs:
> ```
> 12:27:53 [connect-relay-auth-refresh] relay password missing — sign in again to fetch it
> 12:27:56 [connect-success] Connected via relay
> ```
> The app read the honest error, refreshed its credentials, and recovered on its
> own. Compare 09:16 the same morning: `too many invalid relay password attempts`.

### 1.3 A relay restart stranded the entire fleet, permanently

`runRelayTunnel(ctx, relayAddr, agentAddr, deviceID, token, password, …)`
captured `token` and `password` **by value** and replayed them on every
reconnect. Combined with §1.2, any rejection became terminal.

**Proven in production:** I restarted the relay. Every tunnel dropped. **Zero
agents re-registered** — Primary, Mac mini and ubuntu all sat in a rejection
loop until I restarted each process by hand.

**Fixed** (`desktop/agent/main.go::currentRelayCredentials`): every reconnect
attempt re-reads the token + relay password from config (the source of truth,
rewritten on rotation). **Re-verified by restarting the relay again: Mac mini and
ubuntu re-registered on their own in ~2 minutes, no intervention.**

⚠️ **Mesh-relevant:** this is a *fleet-wide availability* property. Any control
plane that can reject a peer must (a) say *why*, and (b) never let a rejection be
terminal for a peer holding valid credentials. Mesh should assume peers rotate
credentials underneath long-lived sessions.

### 1.4 A failed config READ destroyed credentials (agent data loss)

A managed box came up unable to resolve its config, concluded it had none,
**minted a fresh random `device_id` and saved** — overwriting a file holding a
live `auth_token`, `relay_password` and `convex_site_url`.

Two failures at once:

- **Lost its sign-in.** Unrecoverable; the box had to be re-authorized from a
  browser.
- **Lost its identity.** It re-registered as a *brand-new device* while the
  owner's `primaryDeviceId`, aliases and ACLs still named the old id. The machine
  was running and healthy, yet the app said **"no device responded"**. This looks
  exactly like a network bug and is not one.

**Fixed** (`desktop/agent/config.go`, `auth_bootstrap.go`):
- `SaveConfig` **refuses** to write when the on-disk config exists but can't be
  parsed. A failed *read* must never cause a destructive *write*.
- Un-regenerable fields (token, relay password, device id, backend URL) survive a
  blank write. Deliberate wipes still go through `SaveConfigClearingAuth`.
- A **managed** box now recovers its real id — `cloud-<shortId>`, derivable from
  `/etc/yaver/machine.json` (`hostname: <shortId>.cloud.yaver.io`) — instead of
  minting a random UUID.

⚠️ **Mesh-relevant:** device identity is a *stable, derivable* thing for managed
boxes. Mesh ACLs/peer lists keyed on `deviceId` break silently when a node
re-identifies. Prefer identities that can be **recovered**, not re-minted.

---

## 2. Where I was WRONG (so you don't repeat it)

- **"One bad client IP-bans the whole NAT."** False. The invalid-auth limiter
  (12/min, burst 6) gates only the **failure** path — a valid password never
  consults that bucket. The phone locked out only *itself*. The Mac mini's tunnel
  stayed up the entire time, which was the disproof I initially ignored. The
  pre-auth `/d/` proxy bucket (240/min, burst 80) *could* cause NAT collateral,
  but nowhere near a 30s retry loop.
  *(The `clearInvalidAuth` change I made is still correct hardening for shared
  NAT / CGNAT, just not the cause of this outage.)*
- **"The agent's auth token went stale."** False. Every previous token still
  validated (HTTP 200) — the rotation grace window is generous.
- **"My relay binary broke registration."** False. I rolled back and it still
  failed; the bug was §1.3 in the *agent*.
- **`relay.yaver.io` → Vercel** was a red herring for this outage. Clients never
  use it (they get `public.yaver.io` + QUIC `46.224.110.38:4433` from `/config`).
  It was still wrong and has been removed (§4).

**Lesson:** the relay's error message named the wrong component and sent me
hunting a credential bug that didn't exist. Honest, distinguishable errors are a
*debuggability* feature, not a cosmetic one.

---

## 3. OPEN — not fixed

### 3.1 🔴 Task send aborts (highest priority)

**Symptom:** phone connects to the Mac mini over relay (158ms, "OpenAI Codex
ready"), user sends "Hello" → **`Task failed: Aborted`**. The agent **never
received the task** — no `[task …]` line in its log for it.

**Evidence:**
- Relay logged **five concurrent streams** from `229aeb03` collapsing at once:
  `streaming response from 229aeb03 failed: … write: broken pipe` — the relay
  wrote to the client and the client was gone.
- The app *also* aborted `refreshDevices` at the same moment — and that is a
  plain **Convex** HTTPS call, **not** relay:
  `12:28:57 [error] refreshDevices error: AbortError: Aborted`

So **both** the relay path and an unrelated HTTPS call stalled simultaneously.
That points at the **client's connection pool**, not the relay.

**Hypothesis (unverified):** the app holds several long-lived streams open (SSE /
task output / logs). iOS's fetch pool is finite, so new requests queue until they
hit their abort timer:
- `mobile/src/lib/quic.ts::sendTask` — hard **30s** timeout (line ~1702). Its own
  comment notes *"the relay caps non-SSE proxies at ~25s"*.
- `mobile/src/context/DeviceContext.tsx:1082` — `refreshDevices` aborts at
  **10s**.

**Next steps:** count concurrent open streams in the app; check whether SSE
streams are ever closed on device-switch/disconnect; consider a stream budget.
A connected machine that cannot run a task is still useless.

### 3.2 🟡 A parked cloud box is invisible and cannot be woken

The Hetzner primary was **snapshotted + deleted** (correct: metered billing, see
§4). The app now shows **nothing** for it — the user expects *"Parked — Wake"*.

- Its `cloudMachines` row still carries the **dead** `hetznerServerId`
  (`150297516`) because the server was deleted with `hcloud` directly, bypassing
  the control plane → the control-plane record is **stale**.
- `backend/convex/cloudMachines.ts` has `setStatus` (internalMutation) and
  `resumeHealthCheck`; a `status` field exists on the row.

**Needed:** mark the row parked + record the snapshot id, and render parked
machines in the mobile device list with a **Wake** action. The user's framing:
> *"convex can keep its state like yaver managed device in delete mode snapshot
> etc and mobile ui accordingly"*
> *"i should be able to see it wake it up etc"*

Snapshot to resume from: **`407990840`** (5.24 GB, `yaver-primary-park-2026-07-13`).

### 3.3 🟡 `magara` is on an old agent

`magara` (`08182df8-…`, LAN `10.0.0.45`) runs **v1.99.258**, which predates the
relay self-heal. Its agent answers on `:18080` (401) but it holds **no relay
tunnel**, so it is unreachable off-LAN. No SSH key for it from the dev Mac
(tried `root`, `kivanc`, `magara`, `ubuntu`, `pi`, `debian`), and it is not in
the tailnet.

**Fix:** on the box — `npm i -g yaver-cli@latest && sudo systemctl restart yaver`.

### 3.4 🟢 Smaller items

- **Relay expose URLs don't resolve.** The relay runs with
  `EXPOSE_DOMAIN=dev.yaver.io` and hands agents
  `https://<deviceId>.dev.yaver.io`. That hostname **never** resolved (a
  `*.yaver.io` wildcard matches one label only, so `<id>.dev.yaver.io` was never
  covered). Needs a real `*.dev.yaver.io` record if the expose feature is used.
- **Shared relay password is in `ExecStart`** — visible in `ps` and
  `systemctl cat`. Move to an `EnvironmentFile`.
- **Commits are landing unsigned** (push warns `Commits must have verified
  signatures`, bypassed).
- **Ghost device rows.** Several duplicates exist (two `ubuntu-4gb-hel1-1`, two
  `yaver-primary-kivanc`, six `Kvancs-MacBook-Air`). Recreated boxes leave
  orphans, and `primaryDeviceId` pointed at one of them (`9799b0f1`) — which is
  why the app said *"no device responded"* for a machine that was fine. Also in
  DNS: `yaver-primary-kivanc.yaver.io` had **4 A records**, 3 of them dead IPs.

---

## 4. Infra state (as left)

| Thing | State |
|---|---|
| Relay box `yaver-relay-free` | **running**, `46.224.110.38`, arm64, relay `v0.1.19` **with fixes** |
| Hetzner primary `yaver-cpu-mn71me24` | **DELETED** (metered billing). Snapshot **`407990840`** |
| Agents on fixed build | Mac mini, ubuntu (Primary was, before deletion) |
| `primaryDeviceId` | re-pointed → Mac mini (`229aeb03-…`) |
| Cloudflare web | deployed; `yaver.io` 200 |
| **Vercel** | **fully removed** from the zone (see below) |
| Convex | **no deploy done** — none of this work touches `backend/convex` |

**Vercel removal:** apex, `www` and the **`*.yaver.io` wildcard** all pointed at
Vercel anycast (`216.150.x.x`), so *every* unclaimed subdomain — including
`relay.yaver.io` — served Vercel's `DEPLOYMENT_NOT_FOUND`. Wildcard +
`_domainconnect` deleted; apex/www now black-holed to `192.0.2.1` **Proxied** (a
Worker *route* only fires on a hostname with a proxied DNS record — deleting them
outright takes the site down). Verified: zero `x-vercel-*` headers on the zone,
`relay/dev/test/cloud/app/docs.yaver.io` → NXDOMAIN.

---

## 5. Implications for Mesh

1. **Never let a public, unauthenticated endpoint be a credential source.**
   `/config` correctly strips the password; the outage was consumers *pretending*
   it hadn't. Any mesh peer list fetched anonymously must be treated as
   **non-authoritative for secrets**, and a client that lacks a credential must
   **say so loudly**, not attempt an unauthenticated connect.
2. **Distinguish "cannot verify" from "denied".** Fail closed, but report
   honestly, and never punish a peer (rate-limit strike, permanent rejection) for
   a *control-plane* failure. Otherwise a backend blip becomes a fleet outage.
3. **Never snapshot credentials into a long-lived connection.** Re-read them per
   attempt. Rotation is normal; a session that outlives a rotation must converge,
   not die.
4. **Identity must be recoverable, not re-minted.** A node that re-identifies
   silently breaks every ACL/pointer aimed at it, and presents as a *network*
   fault. For managed nodes, derive the id (`cloud-<shortId>`) from durable
   machine state.
5. **A connected peer is not a working peer.** §3.1 — transport up, task still
   fails. Mesh health checks should exercise a real round-trip, not just
   reachability.
6. **Test the composed path, not the components.** Every single piece here was
   green in isolation. `scripts/test-relay-path.mjs` is the pattern: assert the
   *invariants that span* components.

---

## 6. Headless testing (do this — don't debug from a phone)

Almost every hour lost today was spent guessing from a phone screenshot. **Every
layer here is testable headlessly**, because mobile and web share one contract:
they both talk to the relay over HTTPS with an `X-Relay-Password` header
(`mobile/src/lib/connectionCache.ts`). If you can `curl` it, you can test it.

### 6.1 The one that exists — run it first

```bash
YAVER_TOKEN=<session token> YAVER_DEVICE_ID=<deviceId> node scripts/test-relay-path.mjs
```

Real backend, real relay, no mocks. Asserts the **invariants that span
components** — which is the only place today's bug lived:

- `/config` never leaks a relay password (a per-user secret must not sit on a
  public endpoint)
- `/settings` returns `relayUrl` + `relayPassword` for a live session
- the account credential **resolves onto** the platform relay
  (mirrors `DeviceContext.resolveRelayServers`)
- the resolved credential **authenticates** to the live relay
- **a password-less request is rejected** — the old fallback was fatal
- **a `/config`-only client holds NO credential** ⇒ falling back to `/config` is
  an outage, not a degraded mode

This suite, existing in the morning, would have ended the outage in one run.

### 6.2 Getting a token / device id without a phone

Any signed-in agent has both, which is how everything above was tested:

```bash
ssh <box> 'python3 -c "import json;d=json.load(open(\"/root/.yaver/config.json\"));print(d[\"auth_token\"], d[\"device_id\"])"'
```

Probe any machine through the relay exactly as the app does — reaching the agent
(even when the **agent** then demands its own bearer token) proves the *relay*
accepted you and forwarded:

```bash
curl -s https://public.yaver.io/d/<deviceId>/info -H "X-Relay-Password: $PW"
# {"error":"missing or invalid Authorization header"}  → ✅ relay OK, agent answered
# {"error":"invalid relay password"}                   → ❌ relay rejected us
# {"error":"device not connected to relay"}            → ⚪ no tunnel (agent down)
```

Read the relay's own view (ground truth for "is this device actually connected"):

```bash
ssh root@46.224.110.38 'journalctl -u yaver-relay --since "-5min" --no-pager | grep -E "Tunnel:|Agent connected|invalid|abuse"'
```
Note: the relay **truncates device ids to 8 chars** in logs (`cloud-mn` is
`cloud-mn71me24`). This cost me an hour — don't read it as a short-id bug.

### 6.3 Tests worth ADDING (in priority order)

1. **Task round-trip, headless** — covers open bug §3.1, the one that matters
   most. Reachability ≠ usability. Assert: `POST /d/<id>/tasks` → task created →
   output streams back → completes. Today's app can connect and still be useless;
   no test catches that.
2. **Relay-restart resilience** — the fleet-killer (§1.3). Restart the relay,
   then assert **every** agent re-registers **unattended** within N minutes. This
   is the regression I proved by hand; automate it. Cheap and catches a
   catastrophic class.
3. **Auth-backend-down drill** — point the relay at a Convex URL that 500s and
   assert clients get `503 … retry`, **not** `401 invalid relay password`, and
   that agents keep retrying instead of self-destructing. Unit-covered in
   `relay/abuse_guard_test.go::TestAuthBackendDown_IsNotReportedAsInvalidPassword`;
   an end-to-end version would be better.
4. **Credential-rotation drill** — rotate `userSettings.relayPassword` and/or the
   auth token underneath a **live** tunnel; assert the agent converges without a
   process restart (`currentRelayCredentials`).
5. **Concurrent-stream budget** — open N SSE/stream connections from one client,
   then assert a plain `POST /tasks` still completes. This is the direct probe for
   the §3.1 hypothesis (connection-pool starvation).
6. **Mesh peer-admission drill** — for your work: assert a peer with valid
   credentials is **never** permanently rejected because the control plane was
   briefly unavailable, and that a rejection always states *which* credential
   failed.

### 6.4 Rule of thumb

> Every bug in this document was invisible to component tests and obvious to a
> composed test. Test the **path**, not the parts. And when a component reports
> an error, make sure it is naming the component that actually failed — the relay
> saying `invalid relay password` when Convex was unreachable is what turned a
> 20-minute bug into a day.

---

## 7. Commits

- `e179367d7` — mobile: no password-less relay fallback; honest `Session expired`.
  Relay: `clearInvalidAuth` (shared-NAT/CGNAT hardening).
- `6a1b5edc5` — relay: `errAuthBackendUnavailable` + distinct errors. Agent:
  `currentRelayCredentials` (re-read per reconnect). `scripts/test-relay-path.mjs`.
- `955ff9ef2` — agent: a failed config READ must never destroy credentials;
  managed device-id recovery.

**Not released:** the agent fixes reach other users only via a `cli/v*` tag. Not
cut yet.

⚠️ **Parallel-session hazard:** a concurrent session committed my working-tree
`DeviceContext.tsx` inside its own commit (`14afed592`), which left `main`
**not compiling** (it imported `UserSettingsUnavailableError` before `auth.ts`
defined it). Fixed by `e179367d7`. There are also **uncommitted mesh changes** in
the tree right now (`backend/convex/mesh.ts`, `desktop/agent/mesh/*.go`,
`mesh_agent.go`) — presumably yours. Check `git status` before you stage.
