# Design — `capabilityLadder`: the deterministic "what can I / should I do now?" walk (2026-06-01)

Owner thread: yaver.io audits/mobile. Extends
`SPIKE-local-voice-helper-and-nicknames-2026-06-01.md`. Branch suggestion:
`feat/local-voice-helper`.

> **One-line:** "what should I do now?" is **not** an LLM question. It's a
> deterministic walk over a precondition graph. The ladder *computes* the
> answer (state + next action + menu); the on-device/remote model only
> *narrates* it. Same pattern already proven in `localAgent/connectivity.ts`
> ("diagnosis is deterministic so it's correct and auditable").

This doc is the design we implement `mobile/src/lib/localAgent/capabilityLadder.ts`
against. **No code yet** — this is the spec. It folds in all four refinements
from the design thread:

1. **Hermes fork** — live RN preview on the phone is its own branch.
2. **Runner auth** — coding runners (claude/codex/opencode) are an auth plane.
3. **Git + projects** — git is the *third* auth plane and bookends the coding fork.
4. **Lazy, goal-pulled introduction** — "nothing set up yet" is a resting
   state, not a 7-step wizard; most planes may never be introduced.

---

## 1. Where it sits — four pure layers

All four are PURE + RN-free (unit-tested under `tsx`), already the house style
for `localAgent/*`:

| Layer | File | Job |
|---|---|---|
| **Ladder** | `capabilityLadder.ts` *(new)* | state → `{reached, nextStep, available}` — the walk |
| **Brain** | `brain.ts` (exists) | picks the *narrator* (remote LLM vs local 1B vs scripted) |
| **Catalog** | `catalog.ts` (exists, **needs git/project entries**) | enforces safety tier (auto / confirm / blocked) |
| **Resolver** | `resolver.ts` (exists) | spoken ref → device; a sibling project-resolver is new |

Separation of concerns: **Ladder = state + next action. Brain = who speaks.
Catalog = is it safe. Resolver = which device/project.** The model is a
narrator over deterministic state, never the source of truth.

---

## 2. The dependency graph — spine, forks, planes, on-device branch

```
                    ┌─ 0  online?              (phone network)
            SPINE   ├─ 1  device exists?        (paired)
        (gates all  ├─ 2  device reachable?     (heartbeat / LAN|relay path)
         remote)    ├─ 3  agent authorized?     (yaver session; not bootstrap)   ← PLANE 1
                    └─ 4  connected?            (live transport from phone)
                            │
          ┌─────────────────┼───────────────────────────┐
   CODING FORK         HERMES FORK                  DEPLOY FORK
   ├ 5C runner installed     ├ 5H hermes stack           (= coding fork)
   ├ 6C runner authed ←PLANE2│     provisioned           ├ + deploy target
   ├ 7C.0 git authed ←PLANE3 ├ 6H dev project /          │   configured
   │      (clone-priv+push)  │     framework detected
   ├ 7C.1 project present    └ pushes to THIS phone
   ├ 7C.2 project selected
   └ 7C.3 branch hygiene

   ON-DEVICE BRANCH (bypasses the spine entirely):
   └ Coder tier (8GB+, tiers.ts) → sandbox vibe-coding, no machine
```

Load-bearing structural facts:

- **The spine is linear and monotonic; the forks are independent.** A missing
  git plane must not block edit/build/run/test. A missing Hermes stack must not
  block a coding task. A missing runner must not block live preview.
- **`reached = connected` is a complete, valid resting state.** A blank,
  freshly-paired box satisfies the whole spine with every fork at rung zero.
  That is *not* "6 things broken" — it's "connected and idle, waiting for a goal."
- **The on-device branch has no spine dependency.** An 8GB phone with no paired
  machine still has a real capability (sandbox codegen). Never a dead end.

---

## 3. The three authorization planes (the "authorized" rung, exploded)

A connected box does not have one auth bit. It has **three independent OAuth
planes**, each gating different capabilities, each with a distinct fix. Verbs
below are verified against `desktop/agent/ops_*.go`.

| Plane | Authorizes | Verb family (real) | Creds live | Voice-readable |
|---|---|---|---|---|
| 1. Agent | Yaver session on box (spine rung 3) | `device.recoverAuth` / `yaver auth` | token hash → Convex | n/a |
| 2. Runner | claude/codex/opencode (rung 6C) | `runner_auth` (browser_start) | on box, P2P | yes (browser approve) |
| 3. Git | clone-private + push + PR | `git_connect` → `git_connect_status` → `git_push` | `git-credentials.json` on box, **never Convex** | **yes** — device flow returns `{user_code, verification_uri}` |

- All three are **independent**: agent-authed + runner-authed but git-unauthed =
  can edit/build/run, **can't push**. Git-authed but runner-unauthed = can
  clone, can't code. The walk surfaces whichever plane is the actual gap.
- **Subscription OAuth only** for runners (Max Pro / ChatGPT Plus), never API
  keys (`feedback_no_api_keys_subscription_only`).
- **Git creds are P2P, never Convex** (privacy contract). `git_connect`'s
  RFC-8628 device flow is tailor-made for TTS ("open github.com/login/device,
  enter ABCD-1234") — same shape as `yaver auth --headless`.

---

## 4. Git bookends the coding fork (clone-left, push-right)

Git isn't one rung; it gates two different things at opposite ends:

- **Left edge** — precondition for *acquiring* a private repo (clone). Skipped
  if the project is created fresh on the box, or the repo is public.
- **Right edge** — precondition for *shipping* (push / PR), which itself gates
  the deploy fork when deploy pulls from git.
- **Middle** — edit / build / run / test need **none** of it. Fork-independence:
  a missing git plane never blocks the local loop.

Detection + fix, grounded in real verbs:

| Rung | Detect via | First-gap action | Tier |
|---|---|---|---|
| 7C.0 git authed | `git_connect_status` / git-credentials.json on box | `git.connect` (read URL+code aloud) | SAFE_WRITE (to *start*) |
| 7C.1 project present | `prepare` (project state), `list_projects`/`userProjects` for box | clone (→needs 7C.0 if private) or `project.new` | SAFE_WRITE |
| 7C.2 project selected | active workDir; `workspace` op=list (monorepo pkgs); `prepare` plan | `project.select` | SAFE_WRITE |
| 7C.3 branch hygiene | git status (branch/dirty) — **not in `status` verb yet; gap** | checkout feature branch | SAFE_WRITE |
| push code | — | `run git push` / dedicated verb | **CONFIRM** (publishes) |
| copy creds to box | owned box lacks creds | `git.pushCreds` (`git_push`) | **CONFIRM** (moves a credential) |

Two hard safety calls, both from existing rules:

- **Pushing code is CONFIRM, never auto** — a push publishes to an external
  service. Speak it back.
- **`git_push` (copying a credential across machines) is CONFIRM** even though
  it's owned-only / self-excluded / never-Convex. *Starting* an OAuth
  (`git.connect`) is SAFE_WRITE — the browser approval is its own consent gate.

---

## 5. Lazy, goal-pulled introduction (the activation-funnel fix)

The walk must **not** default to "climb to full coding" — that turns a connected
blank box into a 7-step setup wizard, exactly the friction that kills activation
(SPIKE strategic frame). Instead:

- **Pull, not push.** Introduce a rung **only when a stated goal requires it**,
  just-in-time. Some planes/forks legitimately **never get introduced** in a
  session (a user who only ever runs `test` never sees a git device-flow). Never
  nag about a plane the goal doesn't touch.
- **Two modes:**
  - **No goal** → `nextStep = null`. "What should I do now?" answers *"what do
    you want to do?"*, not "step 1 of 7." `available` is a short invitation menu.
  - **Goal stated** → `nextStep` = first gap **on that goal's path only**.
- **Surface ≠ provision.** *Surfacing* a capability (one sentence: "I can connect
  GitHub when you want to push") is free and always fine. *Provisioning* (OAuth /
  clone / install) carries all the friction and runs **only on demand**, when the
  goal's path reaches that rung. The invitation menu surfaces; it never provisions.
- **Idempotent re-entry.** Introducing more later = re-running the walk with a new
  goal. The box accretes capabilities lazily across sessions; the ladder never
  assumes one-shot setup.
- **Anti-nag invariant:** a fork/plane the current goal doesn't require is
  **never** in `nextStep`, and **at most one line** in `available`.

### Minimum-viable path per goal (most rungs stay absent)

| Goal | Must introduce | Stays absent (don't touch) |
|---|---|---|
| `ask` (troubleshoot/question) | nothing | everything |
| `connect` | spine 1–4 | all forks |
| `code` (existing repo) | runner + project | **git, Hermes, deploy** |
| `code` (fresh) | runner + project(fresh) | git (until push), Hermes, deploy |
| `push` / PR | + git plane | Hermes, deploy |
| `preview` (RN app live on phone) | Hermes stack + dev project | **runner, git, deploy** |
| `deploy` | runner + project + git + target | Hermes |
| `sandbox` (on-device) | Coder tier locally | the whole remote spine |

---

## 6. Data model (concrete TS shapes to implement)

```ts
export type RunnerId = "claude" | "codex" | "opencode";
export type Plane = "agent" | "runner" | "git";

// Highest satisfied SPINE rung.
export type SpineRung =
  | "offline" | "no-device" | "unreachable" | "agent-unauthed" | "connected";

// What the user wants. Drives lazy introduction. `undefined` = no goal yet.
export type Goal =
  | { kind: "ask" }
  | { kind: "connect"; deviceRef?: string }
  | { kind: "code"; deviceRef?: string; projectRef?: string; fresh?: boolean }
  | { kind: "push"; deviceRef?: string; projectRef?: string }
  | { kind: "preview"; deviceRef?: string }
  | { kind: "deploy"; deviceRef?: string; projectRef?: string }
  | { kind: "sandbox" };

// Per-device facts the caller assembles from DeviceContext + device.audit.
export interface DeviceFacts {
  deviceId: string;
  lifecycle:
    | "connected" | "ready-to-connect" | "bootstrap"
    | "yaver-auth-expired" | "offline";
  connected: boolean;
  manualAuthRequired?: boolean;
  runners: Partial<Record<RunnerId, { installed: boolean; authed: boolean }>>;
  gitAuthed?: boolean;              // plane 3 (simplify multi-provider → bool for v1)
  hermesReady?: boolean;           // mobile dev stack provisioned on box
  projects: { slug: string; branch?: string }[];   // userProjects on this box
  activeProjectSlug?: string;      // selected workDir
  activeProjectClean?: boolean;    // branch hygiene (optional; gap in `status`)
  deployTargetConfigured?: boolean;
}

export interface LadderState {
  online: boolean;
  hasAnyDevice: boolean;
  reachableDeviceIds?: string[];
  device?: DeviceFacts;            // the resolved target (caller ran resolver first)
  localTier: "router" | "coder" | "none";   // from tiers.ts → sandbox availability
  lastError?: string | null;
}

export interface NextStep {
  rung: string;                    // which rung is the gap
  action?: string;                 // catalog id (must be auto/confirm-dispatchable)
  say: string;                     // TTS line
  shellHint?: string;              // command the user runs on their computer/box
  provisions?: Plane | "project" | "hermes" | "runner";  // what this introduces
}

export interface Capability {
  id: string;        // "ask"|"connect"|"edit"|"build"|"test"|"push"|"preview"|"deploy"|"sandbox"|"install-runner"|"connect-git"|"start-project"
  label: string;     // menu / spoken
  ready: boolean;    // unlocked NOW (true) vs an invitation to introduce (false)
}

export interface LadderResult {
  reached: SpineRung;
  nextStep: NextStep | null;       // first gap on goal's path; null if no goal / satisfied
  available: Capability[];         // affirmative menu (ready + invitation entries)
  blocked?: { reason: string };    // e.g. ambiguous device, filtered BLOCKED action
}
```

---

## 7. The walk (pseudocode)

```
function capabilityLadder(state: LadderState, goal?: Goal): LadderResult {
  // 1. SPINE — most-blocking first. Reuse diagnoseConnectivity's mapping.
  const reached = spineReached(state);            // offline→…→connected

  // 2. AVAILABLE — affirmative menu from `reached` (+ on-device sandbox from
  //    localTier). ALWAYS computed. Framed as invitations, never prerequisites.
  const available = surfaceMenu(reached, state);  // ready:true items + ≤1-line invites

  // 3. NEXT STEP — only when a goal exists. Walk that goal's required-rung list
  //    in order; return the FIRST unmet rung as the single next action.
  let nextStep = null;
  if (goal) nextStep = firstGapForGoal(goal, state, reached);

  // 4. GUARDS (belt-and-suspenders):
  //    - nextStep.action must be dispatchable: dispositionFor(action) ∈ {auto, confirm}
  //      (never BLOCKED).  Reuse actionIsDispatchable().
  //    - a via:"ops" action is only emittable when reached === "connected"
  //      (reachability gate — can't POST /ops to an unconnected box).
  //    - if the device is ambiguous/unresolved when the goal needs one →
  //      blocked:{reason:"ambiguous-device"}, ask the user (resolver).
  return { reached, nextStep, available, blocked };
}
```

`firstGapForGoal` = walk `requiredRungs(goal)` in order, map the first unmet rung
to `{rung, action, say, shellHint}` (reusing `diagnoseConnectivity` /
`diagnoseRunnerAuth` for the rungs they already cover; new logic for project / git
/ Hermes rungs). `requiredRungs` is literally the minimum-viable-path table (§5).

`surfaceMenu(reached)` (monotonic):

| reached | `available` (ready=true unless noted) |
|---|---|
| offline | `ask` (scripted/local) · *(8GB:* `sandbox`*)* |
| no-device / unreachable | `connect` (invite) · `ask` · *(sandbox)* |
| connected, no runner | `install-runner` (invite) · `start-project` (invite) · `preview` *(if hermesReady)* · `ask` |
| runner authed, project set | `edit`·`build`·`run`·`test` (ready) · `push` (ready if gitAuthed else invite) · `deploy` (invite) |
| (any) + coder tier | `sandbox` (ready) |

---

## 8. Catalog additions (new entries for `catalog.ts`)

`catalog.ts` today is device/runner control only — **zero git/project actions.**
Add (all `via:"ops"` → real verbs, all reachability-gated):

| id | verb | tier | note |
|---|---|---|---|
| `git.connect` | `git_connect` | SAFE_WRITE | starts device-flow OAuth (browser approval = consent) |
| `git.pushCreds` | `git_push` | **CONFIRM** | copies a credential to an owned box |
| `git.push` | `run`/dedicated | **CONFIRM** | publishes code |
| `project.list` | `list_projects`/`workspace`(op=list) | READ_ONLY | |
| `project.select` | `prepare`/set workDir | SAFE_WRITE | |
| `project.prepare` | `prepare` | SAFE_WRITE | discover serve-able plan |
| `project.new` | `init_project`/scaffold | SAFE_WRITE | fresh project |

(`reload` SAFE_WRITE already exists; keep — but the Hermes-fork reachability +
`hermesReady` gate from the spike applies so we never propose a reload the box
can't serve.)

---

## 9. Correctness invariants (carried from the thread)

1. **Monotonic spine, then fork.** Never offer a fork action before
   `reached === connected` — you can't `via:ops` to an unconnected box.
2. **Fork independence.** Missing git ⇏ no local loop; missing Hermes ⇏ no
   coding; missing runner ⇏ no preview.
3. **Reachability gate.** Every `via:ops` action requires `reached === connected`.
4. **BLOCKED/CONFIRM never auto.** Final gate via `actionIsDispatchable`.
5. **Publish = CONFIRM.** Any code push / outward action speaks back first.
6. **Lazy / goal-pulled.** No goal → `nextStep = null`. Introduce only the goal's
   rungs. Anti-nag: off-path planes ≤1 line, never in `nextStep`.
7. **Surface ≠ provision.** Menu surfaces; provisioning is on-demand only.
8. **Tie-safe resolution.** Ambiguous device/project → ask, never guess (reuse
   `resolver.ts`; add a sibling project resolver over `userProjects` slugs).
9. **Idempotent.** Re-running with a new goal accretes capabilities; no one-shot
   assumption.

---

## 10. `brain.ts` interaction (narrator only)

The ladder is brain-agnostic. `selectBrain` only decides *who speaks*:

- `reached < connected` → remote is by definition unreachable → **local 1B model
  narrates** the spine's `via:context` connect/auth/pair gaps. This is the
  onboarding mandate: climb the spine to "connected," then hand off.
- `reached === connected`, **no goal** → local model's job is to **elicit the
  goal** ("what do you want to do?"), not recite a checklist.
- `reached === connected` + runner-ready + goal → **remote brain narrates**, with
  real project context (it can read the repo), pulling in only the goal's rungs.

The hand-off point is exactly `selectBrain → remote` ⇔ `reached === connected &&
runnerReady`. The ladder makes it explicit.

---

## 11. Build list (what exists vs gaps)

**Exists, reuse:** spine rungs 0–4 (`diagnoseConnectivity`), runner rungs 5C/6C
(`diagnoseRunnerAuth`), `selectBrain`, `dispositionFor`/`actionIsDispatchable`,
`resolveDevice`, `selectModelTier`. Real verbs: `prepare`, `workspace`,
`git_connect`/`git_connect_status`/`git_push`, `runner_auth`, `reload`.

**Gaps to build:**
1. `capabilityLadder.ts` — the walk (`spineReached`, `surfaceMenu`,
   `firstGapForGoal`, `requiredRungs`) + the types in §6.
2. Stop `diagnoseConnectivity` returning `ok` at rung 4 (it's rung 4 of 7+,
   not "done") — or have `capabilityLadder` own the top of the walk and demote
   `diagnoseConnectivity` to the spine-only helper it already is.
3. Project sub-ladder rungs 7C.1–7C.3 + project resolver over `userProjects`.
4. Hermes rung 5H detection (`mobile_hermes_doctor`) — fold the spike's
   reachability/`hermesReady` gate.
5. Git plane rung 7C.0 + catalog git/project entries (§8).
6. `status` verb gap: surface git branch/dirty so 7C.3 is detectable
   (otherwise 7C.3 is best-effort / skipped).

---

## 12. Testing plan (matches house style: node:test via tsx)

`capabilityLadder.test.mts`, pure, no RN/native:

- Spine: offline / no-device / unreachable / auth-expired / bootstrap /
  connected → correct `reached` + (no-goal) `available` menu.
- Goal-pulled: each goal in §5 → `nextStep` is the first unmet rung **only**,
  off-path planes absent from `nextStep` and ≤1 line in `available`.
- Lazy/no-goal: `nextStep === null`; menu is invitations, not prerequisites.
- Fork independence: git-unauthed but runner-authed → `edit/build/test` ready,
  `push` is an invite (not a blocker); Hermes-unready ⇏ coding blocked.
- Guards: BLOCKED id never surfaces as `nextStep.action`; `via:ops` action never
  emitted when `reached !== connected`; ambiguous device → `blocked`.
- On-device branch: `localTier==="coder"` + no device → `sandbox` ready.
- Idempotency: re-run with a later goal accretes capabilities.

---

## 13. Open questions (resolve before/while implementing)

1. **Git multi-provider:** v1 collapses plane 3 to a single `gitAuthed` bool.
   Real state is per-provider (github/gitlab). Promote to
   `gitAuthed: Partial<Record<"github"|"gitlab", boolean>>` if a goal ever needs
   provider-specificity (e.g. "push to GitLab").
2. **Branch-hygiene detection (7C.3):** `status` verb doesn't currently return
   git branch/dirty. Either extend `status`, or mark 7C.3 best-effort and skip
   when unknown (don't block on an undetectable rung).
3. **Project resolver scope:** mirror `resolveDevice` over `userProjects` slugs
   (+ monorepo packages from `workspace` op=list). Tie → ask.
4. **Goal extraction:** who builds the `Goal` from the transcript — the
   grammar-constrained model, or a keyword pre-pass? Likely both (keyword
   fast-path for "connect"/"reload"/"deploy"; model for free-form "build me…").
   The ladder is agnostic; it just consumes a `Goal`.
```
