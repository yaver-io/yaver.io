# Activation: The Zero-Friction Trial — Deep Analysis

Date: 2026-07-21
Status: design. **Reverses an earlier decision** — see §3.
Trigger: an HN visitor registered for the app and then did nothing.

Companions: `yaver-four-tier-deep-analysis.md` (tiers, economics),
`cloud-unit-economics.md` (measured prices),
`yaver-cloud-workspace-product-model.md` (what is sold).

---

## 1. The actual problem: signup ≠ activation

Someone found Yaver, believed it enough to create an account, and then stopped.
That is not a marketing failure — they were already convinced. It is an
**activation** failure: between "I want this" and "I see it working" there is a
wall.

Count the wall from a cold visitor's side:

| # | Step | Why they stop |
|---|---|---|
| 1 | `npm install -g yaver-cli` | leaving the browser for a terminal |
| 2 | `yaver auth` | second auth after already registering |
| 3 | `yaver serve` | now a process must stay running |
| 4 | **have a project** | nothing to point it at |
| 5 | **have a Claude/Codex subscription** | a paid dependency to evaluate a free tool |
| 6 | keep a machine on | the problem Yaver solves, required before it can solve it |

Six steps, each shedding users, **none of which shows the product**. The person
who registered and vanished did not reject Yaver — they never saw it.

The wall is worst for exactly the audience that would benefit most: someone
without a spare always-on machine and without a model subscription.

---

## 2. What "seeing it work" actually requires

The demo is not "a terminal on a box". It is the loop that makes Yaver
different:

> *Code on a real machine, from anywhere, with an agent doing the work — and
> watch the app change.*

That needs four things present at once, and all four are what the trial must
supply:

1. **A machine** — running, reachable, not theirs to maintain.
2. **A project** — real, buildable, immediately recognisable.
3. **An agent** — something to give instructions to.
4. **A visible result** — the app rendering, changing as the agent edits it.

Give three and it is unconvincing. Give a box with no project, and the user is
back at step 4 of the wall.

---

## 3. Reversing the earlier decision — and what changed

`yaver-four-tier-deep-analysis.md` §4.3 says trials get **no VM**, for three
stated reasons: abuse blast radius, orphan cost, egress churn. That reasoning
was sound for an *open-ended* trial box. It does not survive contact with the
constrained design below, and I would rather correct it than defend it.

| Original objection | Why it no longer holds |
|---|---|
| **Abuse → provider account suspension** | The scenario was a long-lived box with public ingress. A 60-minute box with **no inbound ports**, egress policy, and one-per-verified-identity has a bounded blast radius. See §6 for the honest residual. |
| **Orphan cost** | The trial is **ephemeral: no volume, no reserved IP, no snapshot.** There are no satellites to leak. The one leak class left — the server itself — is exactly what the R1 fix and the orphan sweep now cover. |
| **Egress churn** | Applies only to a box carrying the user's mirrored Claude credentials across park/wake. A trial box has none, never parks, and is deleted. |

Two things also changed since that decision: the **reclamation path and orphan
sweep now exist**, and the **serverless isolation floor** (`cap-drop ALL`, user
namespaces, egress policy, pids/memory caps) is available to constrain the
sandbox.

**The earlier decision was right for the design it was judging. This is a
different design.**

---

## 4. The trial specification

| | |
|---|---|
| Machine | **2c/4GB**, cheapest available — same default class as the paid tier |
| Duration | **60 minutes of wall-clock**, hard stop. Not "60 minutes of use" |
| Storage | **none** — ephemeral rootfs, no volume, no snapshot |
| Network | **no inbound ports.** Reachability is outbound-registered via the relay |
| Egress | RFC1918 + link-local blocked; SMTP blocked; rate-limited |
| Project | **pre-seeded RN todo app** (§5) |
| Preview | **Chrome + WebRTC** — the default path, no emulator, no device needed |
| Agent | opencode-class runner on **trial inference credits**, hard-capped |
| Feedback | **`yaver-feedback-react-native` pre-installed and wired** |
| Identity | **one per verified account**, enforced server-side |
| Conversion | "Keep this workspace" → provisions a real one, same class |

### Why each choice

**60 minutes wall-clock, not idle-based.** An idle timer can be defeated by a
keepalive and turns a bounded cost into an unbounded one. Wall-clock is a
promise we can keep and a cost we can compute in advance.

**No volume, no reserved IP.** Every satellite is a resource that outlives its
server and has to be reclaimed. A trial with no satellites has exactly one
thing to delete, which is the one thing the existing reclamation already
handles well.

**No inbound ports.** This is the single biggest abuse reduction and it costs
nothing, because Yaver's transport is already outbound-registered: the agent
dials the relay. Nothing about the demo needs a listening port on the public
internet.

**Same machine class as paid.** The trial must be an honest sample. Giving a
trial user a faster box than they would buy is a bait-and-switch discovered on
day two.

---

## 5. The sample project — and whether to ask

**Only one: an RN todo app.** Not a picker. A menu at step zero is another
decision to make before seeing anything, and the entire point is to remove
decisions. RN because it is Yaver's flagship path — Hermes push, feedback SDK,
hot reload — and a todo app because it is recognisable in one glance, so
attention goes to *Yaver* rather than to understanding the sample.

The fixture already exists as `yaver-todo-rn`, deliberately outside this repo so
it stays an honest test of what a user's own project hits.

### Ask or auto-seed?

Both, depending on context — and the distinction is who is in the room:

| Context | Behaviour | Why |
|---|---|---|
| **Web/mobile trial** (no terminal) | **auto-seed, no question** | They came from a browser to see a demo. A prompt is friction with no upside; there is nothing else for the box to do. |
| **`yaver install` / CLI** | **ask, default yes** | They already have a machine and probably a project. Cloning a sample into someone's environment uninvited is presumptuous, and the terminal is where users expect to be asked. |

```
No project detected here.
Start with a sample React Native todo app? [Y/n]
```

One keystroke, and `n` is a real answer that leaves their machine untouched.
Never write into a directory that already contains a project.

---

## 6. Abuse — the honest analysis

The earlier objection deserves a real answer rather than a dismissal.

| Vector | Viability against this design |
|---|---|
| **Crypto mining** | 2 cores × 1 hour mines a fraction of a cent. Economically pointless. |
| **Scraping / spam from our IP** | **The real risk.** A datacenter IP abusing a third party threatens the whole provider account. Mitigated by egress rate limits, SMTP block, short TTL, and one-per-identity — not eliminated. |
| **Data exfiltration / pivot** | RFC1918 + link-local blocked, so no reach into the host, the bridge, co-tenants, or cloud metadata. |
| **Resource exhaustion** | cgroup memory/CPU/pids caps; the box is dedicated per trial. |
| **Account farming** | One per verified identity. **Audit `mergeUserInto` for a loophole before launch.** |

**Residual risk, stated plainly:** a determined abuser gets one hour of 2 cores
per verified identity to make outbound requests. That is a real but small
window, and it is monitorable. The correct posture is a kill switch and an
egress-anomaly alert, not the belief that the design is abuse-proof.

`CLAUDE.md`'s rule — a datacenter IP hammering a third party gets the whole
account suspended — is the reason this needs monitoring from day one rather than
after the first incident.

---

## 7. Economics

Measured: `cpx22` at **€0.0368/hour**.

| | Cost |
|---|---|
| One 60-minute trial | **€0.037** |
| 1,000 trials/month | **€36.80** |
| 5,000 trials/month | €184 |

Against $29/mo (≈€26.7) conversions:

| Trials/mo | Cost | 2% conv | 5% conv | 10% conv |
|---:|---:|---|---|---|
| 1,000 | €36.80 | 20 → €534 MRR, CAC €1.84 | 50 → €1,335, CAC €0.74 | 100 → €2,670, CAC €0.37 |
| 5,000 | €184 | 100 → €2,670, CAC €1.84 | 250 → €6,675, CAC €0.74 | 500 → €13,350, CAC €0.37 |

**CAC between €0.37 and €1.84 against €26.7/month recurring.** Even at 2%
conversion the first month repays acquisition roughly fifteen times over.

Compare the alternative: the current activation path costs €0 and converts the
HN visitor at 0%. **A free tier that nobody activates is not cheaper — it is
worth less.**

Add trial inference (opencode-class, hard-capped) at a few cents per session and
the picture does not change materially.

---

## 8. What the trial user actually sees

1. Sign in on the web. No install.
2. "Your workspace is starting" — ~60–90 s, real progress phases (the
   `provisionPhase` ladder already exists).
3. An RN todo app renders in the browser over WebRTC.
4. A prompt box: *"Try: make the completed items strike through and turn grey."*
5. The agent edits; the preview updates live.
6. They shake/click feedback; the SDK overlay fires inside the streamed app.
7. Timer visible throughout. At the end: **"Keep this workspace — $29/mo"**, or
   *"Install Yaver on your own machine, free forever"*.

Step 7 matters as much as the rest. The conversion offer must include the
**free self-hosted path**, or the trial reads as a paywall rather than a demo.

---

## 9. Implementation plan

**P0 — the trial itself**
1. `trial` machine profile: ephemeral, no volume, no egress IP, 60-min hard TTL.
2. Entitlement: one per verified identity, server-side; fail closed.
3. Seed `yaver-todo-rn` into the box at provision (baked into the image, not
   cloned at boot — a git clone is a dependency on a third party during the most
   fragile 90 seconds of the funnel).
4. Auto-start: dev server → Chrome → WebRTC stream.
5. Pre-install and wire `yaver-feedback-react-native`.

**P1 — safety**
6. Egress policy + rate limit + SMTP block (reuse `serverless_isolation.go`).
7. Kill switch: disable all trials with one env flag.
8. Egress-anomaly alert.
9. Hard-capped trial inference credits.

**P2 — conversion**
10. Visible countdown + "keep this workspace" flow.
11. Free self-hosted path shown alongside the paid one.
12. Funnel instrumentation: started / rendered / prompted / converted. **Without
    this we cannot tell a failed trial from an uninterested visitor**, which is
    the exact blindness that produced this document.

**CLI**
13. `yaver install` asks, default yes, never overwrites an existing project.

---

## 10. What would make this wrong

Stated up front so it can be checked rather than rationalised later:

- **Conversion below ~1%.** CAC stays cheap, but it would mean the demo is not
  persuasive and the money belongs elsewhere.
- **Abuse becomes routine.** One suspended provider account costs more than
  every trial combined. Kill switch first, apologies never.
- **The 90-second provision is not reliably 90 seconds.** A trial that opens on
  a spinner is worse than no trial — it demonstrates the opposite of the claim.
  Measure P95 before launch, not after.
- **Users convert to the free self-hosted tier instead of paying.** That is a
  *good* outcome and must not be prevented by hiding it.
