# Multiplayer AI + Cloud for Small Software — Deep Audit

**Date:** 2026-07-23
**Scope:** YC RFS Fall 2026 items **#4 Multiplayer AI** and **#3 A Cloud for
Small Software**, measured against what this repo actually does today.
**Method:** source read of `desktop/agent/`, `backend/convex/`, `relay/`,
`web/`, `mobile/`. Claims below carry `file:line`. Nothing here was verified by
running the product — where a claim is an inference from code shape rather than
an observed behavior, it says so.

**RFS text being measured against:**

> **#4 Multiplayer AI** — AI that's multiplayer by default, where anyone can
> drop into the same live agent session to watch it work, redirect it, and hand
> it off.
>
> **#3 A Cloud for Small Software** — a cloud built specifically for
> purpose-built tools that will only have one or a handful of users.

---

## 0. Verdict up front

**#4 Multiplayer AI — partially built, and built in the wrong place.**

Every individual primitive multiplayer needs already exists somewhere in this
repo: a controller/viewer lease, a multi-client terminal attach, six invite
systems, scoped access enforcement, a live event bus, and a second-human path
that triggers AI work. What does **not** exist is any of them pointed at the
*live coding-agent session*. A second human today can reach a raw terminal, a
feedback form, and a "vibe" button. They cannot watch, join, redirect, or take
over a running Claude/Codex session. The RFS sentence has three verbs —
**watch, redirect, hand off** — and the honest score is:

| Verb | Owner's own devices | A second human |
|---|---|---|
| **Watch** a live agent session | Partial (poll, or tmux mirror) | **No** |
| **Redirect** it mid-flight | Partial (tmux keystrokes; queued messages) | **No** |
| **Hand it off** | Partial (`session_transfer`, tmux adopt) | **No** |

The gap is not conceptual and it is not large. It is roughly: one session
object with a participant roster, the existing lease generalized, the existing
tmux attach opened to non-owners under scope, and a presence channel on the bus
that already advertises itself as the presence layer. Detail in §3–§5.

**#3 A Cloud for Small Software — the machinery is built; the product identity
forbids it.**

`phone_backend.go` (2,059 lines), the 19-target switch engine, `PhoneShare`
friend-preview codes, and the scale-to-zero cloud workspace together *are* a
cloud for one-to-a-handful-user software. But a standing product law says
Yaver is a **development tool, not a runtime host** — products built with Yaver
run on their own stack with zero Yaver runtime dependency. And the Convex
privacy contract forbids storing user/app data centrally. RFS #3 asks for
exactly the thing both rules currently prohibit. That is a founder decision, not
an engineering one, and it is stated plainly in §7.

**Recommendation:** lead with #4. Use #3 as the revenue paragraph, not a second
pitch. Rationale in §9.

---

## 1. The problem statement, decomposed

"Anyone can drop into the same live agent session" implies seven capabilities.
Naming them separately is what makes the gaps visible:

| # | Capability | Meaning |
|---|---|---|
| C1 | **Session identity** | A live agent session is a first-class, addressable object with a stable ID |
| C2 | **Participant roster** | The session has N humans attached, not one owner |
| C3 | **Join** | A second human can attach without the owner re-provisioning anything |
| C4 | **Watch** | Attached humans see the same output, live, without polling |
| C5 | **Redirect** | An attached human can inject input into the running agent |
| C6 | **Arbitration** | Exactly one human drives at a time; contention is resolved, not last-writer-wins |
| C7 | **Hand off** | Control transfers cleanly, and the receiving human keeps full context |

Against those seven, §2 inventories what exists and §3 states what's missing.

---

## 2. Inventory — what already exists

### 2.1 The headline finding: **seven parallel sharing systems**

This repo does not have one sharing model. It has seven, each with its own
invite flow, own TTL semantics, own storage, and own allow-list. This is the
single largest structural obstacle to a credible multiplayer story — not
because any one is bad, but because there is no common object for a UI, a
partner demo, or a new engineer to point at.

| # | System | Storage | Invite | Enforcement | Reaches a terminal? | Reaches an agent session? |
|---|---|---|---|---|---|---|
| 1 | **guestAccess** (`backend/convex/guests.ts`) | Convex, long-lived, email-tied | `yaver guests invite` | `guest_scope.go` path allow-lists | **No** | **No** |
| 2 | **infraAccessGrants** (`backend/convex/access.ts:14`) | Convex, expiring | via guests/projectShares | `getActiveInfraGrant` | — | — |
| 3 | **hostShare** (`host_share_cmd.go`, `backend/convex/hostShare.ts`) | Convex invite + session rows | 6-char code → `/host-share/join` | `httpserver.go:1719` prefixes + `HostSharePolicy` | **Yes** (`/ws/terminal`, gated on `Policy.AllowTerminal`, `httpserver.go:1839`) | **No** |
| 4 | **support sessions** (`support.go:29`) | **In-memory only**, dies on restart | 6-char code + bearer | `supportAllowedPrefixes` (read-only by default) | Only with `--shell` opt-in | **No** |
| 5 | **projectShares** (`backend/convex/projectShares.ts:13`) | Convex, roles `owner\|dev\|normie\|viewer` | code / email → materializes #1 + #2 | `scopeForRole()` collapses to guest scope | **No** | **No** |
| 6 | **PhoneShare** (`phone_share.go:15`) | Local only, `~/.yaver/phone-projects/_shares/` | join code | read-only `pp_` data token | — | — |
| 7 | **SDK capability tokens** (`httpserver.go:1740` `scopePathPrefixes`) | Convex `sdkTokens` (hashes) | minted | per-scope prefix + per-verb gate in `ops.go` | **No** | **No** |

Read the last two columns. **Not one of the seven reaches a live coding-agent
session.** Three of them can reach a raw shell under explicit opt-in. That is
the whole finding in one table.

### 2.2 The primitives that *are* right — and are in the wrong subsystem

Three pieces of existing work are genuinely the correct multiplayer design.
None of them is applied to agent sessions.

**(a) The controller/viewer lease — `remote_runtime_lease.go`**

```
// The lease: at any moment ≤1 client holds *controller* role for a
// session; the rest are *viewers*. Any client can `take` the lease
// (either it's free, held by someone else past a soft timeout, or
// the caller passes `force=true`); the holder can `release` it.
//   — remote_runtime_lease.go:11-13
```

`ControlLease` (`:37`) implements `TakeControl` / `ReleaseControl` /
`CheckAndRefresh`, broadcasts every mutation to peers, and produces a
`LeaseSnapshot` (`:46`) with `holderId` / `holderLabel` / `held` so a UI can
render "TV is driving — take over?" (`:62`). It even has an idle-steal timeout
(`defaultControlLeaseIdle = 60s`, `:54`).

This is C6 (arbitration), fully built and unit-shaped. It is scoped to
**remote-runtime streaming sessions** — screen/device mirroring — and its
docstring is explicit that it arbitrates *"among the clients this agent is
serving"* (`:23`), i.e. among **one owner's own devices**. It has never been
pointed at a coding-agent session, and it has no concept of a second human.

**(b) Multi-client terminal attach — `runner_pty_attach.go`**

The strongest existing co-presence primitive. Attaching to a tmux session uses
`new-session -t <target>` (`:83`) — a *grouped* session, not a plain attach — so
each client gets an independent current-window over shared windows, plus
`-f ignore-size` so a phone attaching doesn't resize the desktop's terminal
(`:30-37`). The file's header comment is a postmortem of a real false green:
the phone's "Open terminal" button, its deep link, and its handler all existed
and the operation *had never once worked* (`:15-21`).

That reasoning — two clients, shared state, independent viewports, no
interference — is precisely multiplayer thinking. It stops at auth: the route
is `mux.HandleFunc("/ws/runner", s.auth(...))` (`httpserver.go:1275`), and
`/ws/runner` appears in **zero** guest allow-lists.

**(c) A second human triggering AI work — the vibe path**

The closest thing shipped to "someone else redirects the agent". A guest at
scope `sdk-project` with `canVibe` can reach `/vibing`
(`guest_scope.go:293`), and the resulting task is always force-isolated and
routed to a GLM/BYO runner — never the owner's Claude/Codex plan
(`guest_scope.go:168-173`). So a second human *can* cause AI work to happen on
the owner's box, safely, today.

It is fire-and-forget: submit a vibe, get a task. There is no watching, no
joining, no redirecting, no shared view.

### 2.3 The event bus that advertises presence and doesn't have it

`bus.go:20-21`:

```
// ... Convex's device registry remains the source of truth for "does
// this user own that device". The bus is the live-presence layer on top.
```

The bus is real (topic registry, dedup, retain, QoS 1 ack/retry) and exposes
`/bus/events` SSE (`httpserver.go:371`). But a grep for `presence` /
`participant` / `roster` / `viewers` across `desktop/agent/*.go` returns
**nothing that models who is currently attached to anything** — the only
`viewers` hits are remote-runtime streaming and spatial-surface comments. C2
does not exist.

### 2.4 What the collaboration UI already ships

Not nothing: `web/components/dashboard/CollabView.tsx` (575 lines) is a real
People + Shared Projects surface with connections, invites, role setting, and
revocation; `mobile/app/connections.tsx` (433) + `mobile/src/lib/projectShares.ts`
(120) mirror it. `docs/yaver-social-invite-to-code.md` is a full design doc for
the social graph.

So the *social* layer — who are my people, which project did I invite them to —
is built on both surfaces. What is missing sits one layer down: those people
have no live session to enter.

---

## 3. Gaps — ranked, with evidence

### GAP 1 — No second human can reach a live agent session *(P0, blocks the whole RFS claim)*

**Evidence.** Agent sessions are served at `/runner/agent/sessions[/…]`
(`httpserver.go:1509-1510`), both behind `s.auth`. The full-teammate guest
allow-list (`guest_scope.go:90`) contains `/runner/jobs`, `/runner/runs`,
`/runner/pools` — and the matcher is segment-aware (`guest_scope.go:328`), so
those entries cannot bleed into `/runner/agent/sessions`. The comment three
lines up says it outright:

```
// Agent sessions and sandboxes stay owner-only — too broad to scope
// safely in Phase 2.
//   — guest_scope.go:126-128
```

`/ws/runner` (`httpserver.go:1275`) is in no guest list at all. `hostShare` gets
`/ws/terminal` but not `/ws/runner` (`httpserver.go:1719-1737`). Support
sessions get `/ws/terminal` only under `--shell` (`support.go:50-56`).

**Consequence.** The literal RFS sentence — *"anyone can drop into the same live
agent session"* — is currently false for every sharing system in the repo. A
guest can get a raw shell (host-share) but not the agent's session; they'd have
to `tmux attach` by hand and know the session name.

### GAP 2 — The session object has one owner and no participants *(P0)*

`AgentSession` (`runner_agent_session.go:68`) carries `OwnerUserID string`
(`:76`) and nothing else identity-shaped. No participants, no roles, no
join/leave, no last-seen. `Messages []AgentSessionMessage` (`:80`) has a
`Direction` of `"user" | "agent"` (`:58`) — **which** user is not recorded, so
even after adding participants, history could not attribute a turn to a person.

Sequencing is enforced by refusing a follow-up while the previous task runs
(`:10-12`). For a single owner that's correct. For N humans it is the wrong
primitive: it turns contention into an error instead of a queue or a lease.

### GAP 3 — The lease is built but never generalized *(P0, cheapest high-value fix)*

`ControlLease` is ~150 lines, dependency-free, and already models exactly C6.
It is instantiated only for remote-runtime sessions. Nothing in
`runner_agent_session.go`, `runner_pty*.go`, or `tmux*.go` references it.

Two things it would need for multiplayer: (a) identity keyed on
`(userID, deviceID)` rather than an opaque `clientID`, so "who is driving" is a
*person*, not a browser tab; (b) an audit trail — today `TakeControl(force=true)`
silently steals with no record, which is acceptable among one owner's devices
and unacceptable across two humans.

### GAP 4 — Watching is polling; there is no session event stream *(P1)*

There is no `/tasks/{id}/events`. SSE exists for `/bus/events`,
`/dev/events`, `/blackbox/events`, `/browser/events`, `/analytics/events`
(`httpserver.go:371,487,918,956,999`) — **not** for tasks or agent sessions.
`runner_stream.go` publishes structured chat events (`yaver_say`,
`runner_action`, `runner_text`, `runner_result`, `:122-159`) into named log
streams, which is the right event vocabulary — but it posts to
`http://127.0.0.1:18080/streams/…` (`:78`), i.e. loopback-only publication into
a stream surface that, per GAP 5, teammates cannot read.

Consequence: a second viewer's "watch it work" is `GET /tasks/{id}` on a timer.
Fine for a status chip; not a shared live session.

### GAP 5 — Stream access is inverted between tiers *(P1, likely a bug)*

`/streams` + `/streams/` are in the **support** (read-only) allow-list
(`guest_scope.go:226-227`) and **absent** from the **full teammate** list
(`guest_scope.go:90-139`).

A least-privilege support guest can watch live run streams. A full-scope
teammate — who can trigger tasks, deploy, and read the project list — cannot.
Since `runner_stream.go` publishes autorun/autoideas output there, this is
precisely backwards for the multiplayer case.

I could not find a rationale in comments or tests for the asymmetry. Treat it
as a bug pending confirmation.

### GAP 6 — `projectShares` roles are not enforced agent-side *(P1, false green)*

`backend/convex/projectShares.ts:6-11` states:

```
// ... the role is a preset that fills in scope/allowedProjects;
// branch-pin / PR-only / deploy-gate for "normie" are enforced
// agent-side off membership.role.
```

The agent never receives a role. `GuestConfig` (`auth.go:787-812`) has
`Scope`, `AllowedProjects`, `CanVibe`, `DailyTokenLimit`, `AllowedRunners`,
`UsageMode`, device/machine lists, `ResourcePreset` — **no `Role`**. A grep for
`membershipRole` / `projectRole` / `ShareRole` across `desktop/agent/*.go`
returns nothing. `scopeForRole()` (`projectShares.ts:15`) collapses `dev` and
`normie` to the *same* `"full"` scope before the agent ever sees it.

So `normie` and `dev` are indistinguishable at the enforcement layer. The
branch-pin / PR-only / deploy-gate behavior the comment promises does not exist
where it says it does. (`git_pr.go` implements a normie PR *flow*; it is not a
role-derived restriction.) This is the doc-vs-code failure mode CLAUDE.md warns
about, and per house rules the fix belongs in the same change as the discovery.

### GAP 7 — Relay signature auth is same-user-only; the access graph isn't wired in *(P1, architectural)*

`backend/convex/devices.ts:2584-2585`:

```
// The signer's owner must own the target too (same-user mesh).
if (String(signer.userId) !== String(target.userId)) return deny;
```

`relay/server.go:751` `authorizeProxyViaSig` → `resolveSigViaConvex` (`:681`)
→ that check. A guest is by definition a *different* `userId`, so the modern
asymmetric path denies them and they fall back to the shared relay-password
path (`:562` `validateAndResolveViaConvexE`).

Meanwhile `infraAccessGrants` — the actual cross-user access graph
(`backend/convex/access.ts:14`) — is **not consulted** by `resolveDeviceSig`.

**Consequence, and this is the important one:** cross-account multiplayer today
rides entirely on the legacy password path. `sigFailReason`'s own comment
(`relay/server.go:707-718`) describes the plan to flip the password off once
migration metrics look clean. Doing that with the graph unwired would silently
kill every guest/host-share/support session over relay — and the failure would
present as "the relay is broken", not "guests lost auth". Per the standing
false-green rule, the probe for this must *attempt a cross-account reach*, not
check that a grant row exists.

### GAP 8 — Seven sharing systems, no unified session *(P1, product-level)*

Restating §2.1 as a gap because it is the one a YC partner will feel
immediately. "Anyone can drop into the session" requires a noun — *the
session* — that a user, a UI, and a demo can all point at. Today the answer to
"how do I share this?" is a decision tree across seven mechanisms with
different lifetimes (Convex-persistent vs in-memory-dies-on-restart), different
invite UX (email vs 6-char code vs join URL vs minted token), and different
enforcement layers.

### GAP 9 — Support sessions die on agent restart *(P2)*

`support.go:12-15` is explicit: "Nothing is persisted… dies on `stop` / TTL /
process restart." Correct for a TeamViewer-shaped grant. But the agent
auto-updates, and per memory the auto-update path was recently made default-on
with a 6–12h jitter — meaning an in-memory support grant can vanish mid-session
for reasons the two humans involved cannot see. Any multiplayer session built on
this pattern needs either persistence or an explicit, surfaced "your session
ended because the agent updated".

### GAP 10 — Handoff carries the machine, not the conversation *(P2)*

`transfer.go` (997 lines) and `session_*.go` implement session transfer, and
tmux adoption (`tmux.go`, `runner_tmux.go`) can take over a live session. But
`AgentSession` persistence deliberately excludes per-message output
(`runner_agent_session.go:15-20`) — output is re-fetched from the TaskManager on
read. A handoff to a *different human on a different account* would therefore
need the receiving side to be authorized against the host's TaskManager. There's
no path for that today (GAP 1). Handoff between the owner's own devices works;
handoff to another person does not.

---

## 4. What "compliant" would concretely mean

Minimum bar for the RFS sentence to be literally true, in the order I'd build it.

### Phase 1 — Make the session multiplayer-shaped *(the foundation)*

1. **`SessionParticipant`** on `AgentSession`: `{userID, deviceID, label, role:
   owner|driver|viewer, joinedAt, lastSeenAt}`. Roster in memory; persisted
   alongside the existing store.
2. **Attribute turns**: add `authorUserID` + `authorLabel` to
   `AgentSessionMessage` (`runner_agent_session.go:57`). Without this, shared
   history is unreadable the moment there are two humans.
3. **Generalize `ControlLease`** to key on `(userID, deviceID)`, attach one per
   agent session, and log every `force` steal to `session_audit.go`.
4. **Replace the "refuse while running" rule** (`:10-12`) with lease-gated
   input: the driver may inject; viewers get a clear "X is driving — request
   control?" This is the single change that converts the session from
   single-player to multiplayer.

### Phase 2 — Let a second human in *(the RFS verb "drop into")*

5. **New scope tier `GuestScopeSession`** in `guest_scope.go`, granting exactly:
   `/runner/agent/sessions` (read), `/runner/agent/sessions/{id}` (read),
   `/runner/agent/sessions/{id}/message` (write, lease-gated), `/streams/`
   (read), `/ws/runner?name=` (attach, policy-gated). Nothing else — no
   `/exec`, no `/files/raw`, no `/vault`, no `/repos`.
6. **Open `/ws/runner` to that scope**, reusing the grouped-session attach that
   already exists (`runner_pty_attach.go:83`). The tmux work is done; this is an
   auth change plus a per-session `allowAttach` policy flag mirroring
   `HostSharePolicy.AllowTerminal`.
7. **Fix GAP 5** — add `/streams/` to the full-teammate list, or state in a
   comment why a teammate must not read streams.

### Phase 3 — Make watching live *(the RFS verb "watch")*

8. **`GET /runner/agent/sessions/{id}/events` (SSE)**, emitting the vocabulary
   `runner_stream.go:122-159` already defines (`yaver_say`, `runner_action`,
   `runner_text`, `runner_result`) plus `participant_joined|left`,
   `lease_taken|released`. One stream, N subscribers.
9. **Presence on the bus** — a `session/{id}/presence` topic. `bus.go` calls
   itself the presence layer; give it a presence topic.

### Phase 4 — Cross-account transport *(unblocks everything above off-LAN)*

10. **Wire `infraAccessGrants` into `resolveDeviceSig`** (`devices.ts:2565`):
    allow when signer's owner owns the target **OR** an active, unexpired grant
    links them. Keep the deny-by-default shape; this widens the graph by exactly
    one documented edge.
11. **A doctor probe that attempts a real cross-account reach** and fails loudly
    if the only thing holding it up is the password fallback.

### Phase 5 — Collapse the seven *(the product-level fix)*

12. One `SharedSession` object; the seven existing systems become *presets* over
    it (support = read-only + short TTL; host-share = terminal + lease;
    projectShares = repo-bound + role). One invite verb, one revoke verb, one
    "who's here" view, on every surface.

**Cross-surface parity is not optional here.** Per house rules: RN surfaces
(mobile/tablet/car/glass) share `DeviceContext`/`AuthContext` and inherit for
free; **web, tvOS, watchOS, Wear OS each need an explicit port**. A multiplayer
session only the phone can join is not multiplayer.

---

## 5. What to demo (and what a partner will poke)

**The demo that matches the RFS sentence, end to end:**

1. Owner starts an agent session on their Mac from their phone.
2. Owner sends a link. Second person opens it **on a different account, on
   cellular** — that's the part nobody else can do, because it's P2P/relay to a
   personal machine, not a SaaS session.
3. Second person sees the live session — same output, same moment.
4. Second person taps **Take control**, types a redirect; the owner's phone
   shows "Ayşe is driving".
5. Owner takes it back. Audit trail shows both turns, attributed.

Steps 3–5 do not work today. Steps 1–2 substantially do.

**What a partner will attack:**

- *"Isn't this a Claude Code wrapper?"* — Answer with the transport and trust
  model, not the feature list: forced-command SSH cages
  (`ssh_control_server.go`, `ssh_session_cmd.go`), device-key-only auth against
  a `# yaver-managed` set, a pass-through relay that authorizes nothing and
  holds no device keys. A fully compromised relay still cannot enter a session.
  That is a real system and it is what a wrapper cannot clone.
- *"Who is the second player?"* — Today, mostly the owner's other devices. Before
  applying, get real two-human sessions on the board. That number matters more
  for this slot than signups.
- *"What stops my teammate reading my whole disk?"* — This is Yaver's strongest
  answer and it should be led with, not defended: six enforced scope tiers, a
  segment-aware matcher with an explicit prefix-collision fix
  (`guest_scope.go:80-89`, `:304-321`), and dedicated pentest tests
  (`guest_security_test.go`, `files_browser_hostshare_pentest_test.go`,
  `guest_header_strip_test.go`). Most "multiplayer AI" entrants will have a
  shared web session and no answer at all.

---

## 6. RFS #3 — A Cloud for Small Software

### 6.1 What exists

**The mini-backend is real and portable.** `phone_backend.go` (2,059 lines) +
`phone_backend_http.go` (675): each project is `~/.yaver/phone-projects/<slug>/`
over SQLite, with a portable schema subset — tables, columns, indexes,
relations, queries, mutations, auth rules, storage rules, seed, env
(`phone_backend.go:22-28`). Explicitly designed so a project is
*representable*, not trapped.

**Promotion is built.** The switch engine (`switch_*.go`, 19 targets) promotes a
project from phone → dev machine → Convex/Supabase/Postgres/etc., with
snapshots and 7-day rollback. "Start tiny, grow out, never get locked in" has
running code behind it.

**Distribution to a handful of users is built.** `PhoneShare`
(`phone_share.go:15-45`): a join code carrying `Runtime`, `DataURL`, a
**read-only scoped `pp_` data token**, plus `Schema` + `App` so a friend's
Hermes-loaded copy renders and reads live data **without the owner's session and
without write access**. That is a working answer to "my five users need to
actually use the thing".

**The economics are the RFS thesis.** Per the cloud unit-economics work: shared
scale-to-zero carries ~98% margin vs ~16% dedicated; Hetzner is metered and
every box must be volume-backed or snapshot+deleted when idle. Software with one
user is idle ~99% of the time — that is *exactly* the workload a
scale-to-zero-native cloud wins and a per-seat SaaS cloud loses. If #3 is the
pitch, this is the paragraph.

### 6.2 The blocker is a rule, not a missing feature

Two standing constraints sit directly across RFS #3:

1. **"Yaver is a development tool, not a runtime host."** Products built with
   Yaver run on their own standalone stack with zero Yaver runtime dependency.
   Yaver is the workbench, not the house. RFS #3 asks for the house.
2. **The Convex privacy contract.** `userProjects` may hold slug + deviceId +
   flags + branch and explicitly **no absolute paths**; vault values, task
   input, stdout, and file contents are forbidden and enforced by
   `desktop/agent/convex_privacy_test.go`. A hosted small-software cloud means
   storing *somebody's application data* — which needs either a documented
   carve-out (app data in the user's own workspace volume, never in Convex —
   consistent with today's `PhoneShare`, which is deliberately local-only and
   P2P) or a rewrite of the contract.

Constraint 2 has a clean answer already implied by `PhoneShare`'s design:
**tenant data lives on the tenant's workspace volume; Convex holds only the
pointer.** Constraint 1 is a genuine identity decision and only the founder can
make it.

### 6.3 Gaps if #3 becomes a real pitch

- **No multi-tenant runtime.** Phone projects are single-owner and single-box.
  "One or a handful of users" needs per-app tenancy, per-app quotas, and a wake
  path a *non-owner's* request can trigger. Today waking is an owner action.
- **Wake-on-request doesn't exist for end users.** Scale-to-zero for small
  software means an end user's HTTP request wakes the box. Current wake is
  operator-initiated; and per memory, all Yaver SKUs were sold out in EU Hetzner
  at last check — wake needs availability-substitution before it can be a
  product promise, not just an ops nicety.
- **No per-app billing seam.** `remote_cost` / `switch_cost` exist for the
  owner's opex. Nothing meters *an app's* consumption for a cloud SKU.
- **No custom domain / TLS path for a phone project.** `domain_*` and
  `ssl_provision` verbs exist for the owner's machine, not per-tenant app.

### 6.4 Honest score

The **build/promote/share** half of a cloud for small software is done and is
genuinely differentiated (portable schema + friend-preview + 19-target
promotion). The **host/serve/meter** half is not built, and is currently
forbidden by product law. #3 is a strong *supporting* claim — "here's why the
economics work and how apps escape" — and a weak *primary* claim, because the
primary claim would require reversing a decided identity in the same quarter you
pitch it.

---

## 7. The one decision that resolves both

Both RFS items point at the same unbuilt thing: **a shared, addressable,
multi-participant session on someone else's machine.**

- For #4, the session contains a coding agent and the participants are humans
  collaborating on code.
- For #3, the session contains a running app and the participants are its
  handful of users.

Same object, same lease, same presence channel, same access graph, same
scale-to-zero substrate. Building §4 Phase 1–4 moves both. That is the
strongest possible answer to "why are you the team for this" — not "we fit two
RFS items", but "we found the primitive underneath them".

---

## 8. Incident-hardening this audit must leave behind

Per the standing rule that every incident makes the product harder, the findings
above should become probes, not just prose:

| Finding | Probe to build | Must attempt the operation |
|---|---|---|
| GAP 1 | `doctor_multiplayer.go`: attempt a real second-identity join of a live session | Yes — a route-registration check is exactly the false green `runner_pty_attach.go` documents |
| GAP 6 | Test asserting every `projectShares` role reaches the agent, or deleting the claim from `projectShares.ts:6-11` | Yes — assert a `normie` grant is actually restricted |
| GAP 7 | Cross-account relay reach probe that reports **which** auth path carried it | Yes — must fail loudly if only the password path works |
| GAP 5 | Test pinning each scope's stream access with a stated rationale | Yes |
| GAP 9 | Surfaced "session ended: agent updated" instead of a silent drop | Yes |

The `runner_pty_attach.go` header is the model to copy: state the incident, state
the false green it produced, then the fix.

---

## 9. Recommendation

1. **Apply under #4, single slot.** It is the only RFS item where the demo can
   be real within weeks rather than quarters, and the security model is a
   genuine differentiator most entrants won't have.
2. **Build §4 Phase 1–3 before applying.** Roughly: participant roster + turn
   attribution + generalized lease + one new scope tier + one SSE endpoint. The
   tmux, lease, scope-enforcement, and social-graph work is already done — this
   is wiring, not invention.
3. **Land Phase 4 (relay access graph) regardless.** It is a latent P1 outage
   waiting for the password cutover, independent of any YC consideration.
4. **Use #3 as the business paragraph.** Scale-to-zero unit economics + portable
   schema + friend-preview. Do not pitch it as a second product without first
   deciding, explicitly, whether Yaver hosts other people's software.
5. **Fix GAP 6 now** — a comment claiming enforcement that doesn't exist is the
   exact failure class this repo's rules are written against, and it's a
   ten-line change either way.

---

## Appendix A — Evidence index

| Claim | Location |
|---|---|
| Guest scope tiers + allow-lists | `desktop/agent/guest_scope.go:36-63, 90-230` |
| Agent sessions owner-only, stated | `desktop/agent/guest_scope.go:126-128` |
| Segment-aware matcher + collision fix | `desktop/agent/guest_scope.go:80-89, 304-339` |
| `AgentSession` single owner | `desktop/agent/runner_agent_session.go:68-83` |
| Message has no author identity | `desktop/agent/runner_agent_session.go:57-63` |
| Serialized-turn rule | `desktop/agent/runner_agent_session.go:10-12` |
| Agent-session routes | `desktop/agent/httpserver.go:1509-1510` |
| Controller/viewer lease | `desktop/agent/remote_runtime_lease.go:11-13, 37-76` |
| Lease is in-process, owner's clients only | `desktop/agent/remote_runtime_lease.go:23-25` |
| tmux grouped-session attach | `desktop/agent/runner_pty_attach.go:26-37, 83` |
| The attach false-green postmortem | `desktop/agent/runner_pty_attach.go:15-21` |
| `/ws/runner` + `/ws/terminal` routes | `desktop/agent/httpserver.go:1272, 1275` |
| host-share allow-list + terminal gate | `desktop/agent/httpserver.go:1719-1737, 1839` |
| Support session model | `desktop/agent/support.go:12-15, 29-56` |
| Structured run events | `desktop/agent/runner_stream.go:122-159` |
| Stream publish is loopback-only | `desktop/agent/runner_stream.go:78` |
| SSE endpoints (no task/session SSE) | `desktop/agent/httpserver.go:371, 455, 487, 918, 956, 999` |
| `GuestConfig` has no role | `desktop/agent/auth.go:787-812` |
| Bus claims the presence layer | `desktop/agent/bus.go:20-21` |
| projectShares roles + collapse | `backend/convex/projectShares.ts:6-20` |
| Access graph helpers | `backend/convex/access.ts:14-27` |
| Relay sig auth is same-user | `backend/convex/devices.ts:2556-2593` |
| Relay proxy sig path | `relay/server.go:677-705, 751-787` |
| Password-cutover risk note | `relay/server.go:707-718` |
| Phone mini-backend + portability | `desktop/agent/phone_backend.go:22-28` |
| PhoneShare read-only data token | `desktop/agent/phone_share.go:15-45` |
| Collab UI (web / mobile) | `web/components/dashboard/CollabView.tsx`, `mobile/app/connections.tsx` |

## Appendix B — Method and limits

Source read only. No tests were run, no agent was started, no session was
attempted. Specifically **not** verified by execution: that a `full`-scope guest
is actually refused at `/runner/agent/sessions` (inferred from the allow-list
and the segment-aware matcher); that the relay password path is what carries
guest traffic today (inferred from the sig path's same-user deny); and every
claim about what a second human *experiences*, all of which is inferred from
route and scope definitions.

Per CLAUDE.md's first rule, this document is itself a `.md` that will drift.
Where it disagrees with the code, the code is right and this file is the bug.
