# Self-Improving MCP for Driving Mobile Apps (redroid)

> Status: architecture, design (2026-06-17). Specializes the Personal Agent Gateway
> (`docs/yaver-personal-agent-gateway.md` §18) to the **mobile-app / redroid** engine.
> Key reuse insight: **Yaver's AI app-*test* agent IS this machinery** — testing an app and
> *driving* an app are the same loop (observe → act → verify), pointed at different goals.

## 1. Thesis

Your **mobile apps become a self-improving MCP tool surface.** A redroid instance (logged into
your apps) is driven by an AI loop that learns each app's screens, compiles reliable flows,
heals them when the app changes, and exposes `get/add/update` per app as MCP tools. It runs
**remote-only** (managed cloud you never touch) and **improves from every use**.

EV is one connector ("is the Eşarj free?"); the architecture is general (Misli, bank, broker, …).

## 2. Why mobile ≠ web (the constraints that shape everything)

| | Web (Playwright) | Mobile (redroid) |
|---|---|---|
| Structure | DOM + CSS selectors | **accessibility tree** (uiautomator nodes) + **pixels** |
| Read a screen | `extract_text`/`get_dom` | `droid_ui_texts` (a11y) + `droid_frame` (vision) |
| Locate element | CSS/role | resource-id / content-desc / text / bounds / **vision** |
| Act | click/type | tap / type / swipe / back at node or coordinate |
| Persistence | `storageState` cookie | **golden snapshot** of the logged-in device |
| Drift source | layout / A-B test | **app version bumps** (UI changes wholesale) |

Implications: there is no stable selector language → **resilience must be multi-strategy +
vision-backed**; persistence is a device snapshot not a cookie; and app updates are a
first-class drift event the system must detect and adapt to.

## 3. The Screen abstraction (the core data model)

Unify the accessibility tree and vision into one model so the agent reasons about "where am I"
robustly:
```
Screen {
  nodes:      [{ role, text, contentDesc, resourceId, bounds, clickable }]   // from droid_ui_texts/uiautomator
  pixels:     frameRef                                                       // from droid_frame
  signature:  ScreenSignature   // robust fingerprint: salient resource-ids + text shape + layout hash + vision embedding
  appPkg, appVersion
}
```
- **ScreenSignature** is the key idea: a fuzzy fingerprint that recognizes "this is the
  station-detail screen" across minor changes. Flows are keyed to signatures, not pixel-exact
  layouts, so small UI shifts don't break replay — and a *big* shift (signature miss) is the
  trigger to self-heal.

## 4. Action + verify model
```
Action = tap(target) | type(target, text) | swipe(dir) | back | wait(cond)
target = byResourceId | byText | byContentDesc | byBounds | byVision(prompt)   // tried in resilience order
```
- **Every action is followed by a re-observe + verify**: did the screen advance to the expected
  next signature / did the expected state change appear? Trust the *observed* result, never the
  tap. Idempotency where the app allows.

## 5. Capabilities = MCP tools (per-user, dynamic)
Each app advertises `get/add/update` capabilities, each compiled to a **Flow** + an
**answerSchema** (what structured data to extract). These register as MCP tools on the user's box
(`gw_<app>_<capability>`) — the host AI (in-car voice, Claude Code) sees your apps as tools.
```
Flow { capabilityId, appPkg, steps:[{ expectSignature, action, fallbacks[] }], answerSchema, version }
```

## 6. The self-improvement loop (the heart)

```
        ┌──────────────────────────── per interaction ───────────────────────────┐
OBSERVE → ACT → VERIFY → LEARN → (GENERALIZE / SCORE) → next
   │        │       │        │
 Screen   Action  re-obs.  on success: record concrete trace
                            on failure: vision-LLM re-locate → heal → update Flow
```

What "learns" concretely:
1. **Trace → Flow compiler.** First successful run of a capability is recorded as a concrete
   trace (signatures + actions + which inputs varied). The curator parameterizes it into a
   reusable Flow with selector fallbacks. (Reuse the **vibe recorder** + record/codegen.)
2. **Screen-signature self-heal.** On replay, match current Screen to the expected signature; on
   a miss (app changed), the **vision-LLM re-locates** the target by intent, the Flow is updated,
   and the fix is recorded. (Reuse `testkit_self_heal_selector`.)
3. **Outcome reinforcement.** Success/fail + post-verify update a **reliability score** per
   capability and bias the resilience order toward the selector strategy that's been working.
4. **Correction learning.** When you correct it ("no, the *other* button"), record it; never
   repeat the mistake.
5. **App-version awareness.** Read the package version; keep per-version Flow variants; on a
   version bump, **proactively re-validate** READ flows before they're needed.
6. **Cross-app transfer.** Patterns learned once (login screens, OTP entry, list extraction,
   pull-to-refresh) bootstrap *new* apps faster — the system gets better at wrapping apps it has
   never seen.

## 7. Exploration agent (capability discovery, READ-ONLY)
A bounded agent that **safely explores** an app's read-only surface (lists, detail screens,
settings *views*) to propose new capabilities ("this app also shows session history — add as
`get`?"). **Never triggers an ACT during exploration** (no buttons that spend/submit/delete). It
expands the tool surface without risk; proposals go to the curator → you.

## 8. The curator (bounded self-improvement)
A periodic agent (the completeness-critic pattern) that reviews usage and:
- **auto-applies (low blast radius):** selector heals, READ-flow refinements, reliability
  re-scoring, preference learning, screen-signature updates.
- **requires your confirm:** any **new ACT/financial capability**, **auth changes**, **policy/
  spend-cap changes**. A self-rewriting system must never silently grant itself "transfer money".
- All changes **versioned, reversible, audited**.

## 9. The big reuse: the AI app-test agent *is* this
Yaver already built (per `docs/yaver-ai-app-test-agent.md`): redroid + the test brain
(T1 + oracle bank), `droid_*` UI driving, `testkit` + self-heal selectors, the `yaver-base`
warm golden snapshot, the catch→fix→reload→re-verify loop, `ops_qa`. **"Catch a regression on a
screen" and "reliably accomplish a task on a screen" are the same observe→act→verify machinery.**
So this architecture is largely a *re-aiming* of existing organs:

| Need here | Existing organ |
|---|---|
| Drive UI | `droid_frame` / `droid_input` / `droid_ui_texts` (`droid_interactive.go`) |
| Logged-in persistence | `yaver-base` golden snapshot |
| Self-heal selectors | `testkit_self_heal_selector` |
| Record → flow | vibe recorder / codegen |
| Observe + oracle | TestBrain T1 + oracle bank |
| Cheap/vision inference | `models_*` + multimodal (`robot_camera` image-tool pattern) |
| Remote-only operation | `rd/stream`+`/rd/input`, `device_broadcast_command` |

## 10. Remote-only, safety, privacy, open-core
- **Remote-only:** all interaction via `droid_frame`/`droid_input`; human steps (2FA, captcha,
  first login) surface via remote-view on your *own* phone — never a physical tap on the
  managed-cloud device (parent §19).
- **Policy Guard:** your apps/accounts only; human cadence; back off + record on
  anti-automation/captcha-storm; **no evasion, no auto-captcha-solve** (the human solves it live).
- **Privacy:** Screens, signatures, flows, traces, reliability stats, sessions — your most
  personal data → **local-first / vault, never Convex.** The personalized MCP runs on your box.
- **Open-core:** the *engine + learning framework* is generic/open Yaver; your *specific* app
  flows, credentials, and learned preferences are private.

## 11. Build path
1. **Screen model + signature** over `droid_ui_texts`+`droid_frame` (the perception layer).
2. **Flow store + replay** with multi-strategy locate + post-verify.
3. **Trace→Flow recorder** (one READ capability on one app, e.g. an EV "is-it-free").
4. **Self-heal on signature miss** (vision re-locate) — the durability proof.
5. **MCP registration** of compiled capabilities (per-user dynamic tools).
6. **Exploration agent** (READ-only discovery) + **curator** (gated improvement).
7. ACT capabilities (parent §16 consent model) — last, confirm-gated.

Each step reuses an existing organ; the net-new is the **Screen-signature self-heal loop** and
the **Flow store/curator** that turn one-off app-test runs into durable, improving tools.
