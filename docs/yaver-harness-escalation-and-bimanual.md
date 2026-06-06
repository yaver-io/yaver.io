# Yaver — the escalation doctrine + the bimanual deformable endgame

**Status:** strategic deep analysis, 2026-06-06, `feat/yaver-robot-cell`.
**Companion to:** [thesis](yaver-harness-automation-thesis.md),
[jig/formboard](yaver-wire-harness-jig-formboard-design.md),
[compiler](yaver-harness-compiler-design.md),
[Arkel cell](yaver-arkel-terminal-block-cell.md).
**Grounded in:** Simkab/Talos ($13/hr labor; dizgi 12 s/pin, formalama 20 s,
RKES 6 s/wire, test 30 s; cut/crimp already auto).

---

## 0. TL;DR — wage it like a war, not a moonshot

The incumbent spends **$45M on a Morocco plant** in one bet. We do the opposite:
a **self-funding capability ladder** — each rung is **cheap**, removes **one**
labor slice, **pays for itself in weeks–months**, and **funds + de-risks the next
rung**. You can **stop at any rung and still be profitable** — no rung depends on a
future rung paying off. That property *is* the asymmetric weapon: we never need a
big balance sheet, only the last rung's savings.

The hardest rung — **bimanual (two-arm) handling of the deformable wire bundle,
the formalama (#1 labor sink)** — is reached **last**, paid for by rungs 0–5, and
even then only if volume justifies it. Until then formalama stays
**human-with-pick-to-light**, which already banks most of its value at rung 0 for
~$1k.

---

## 1. The doctrine: a self-funding ladder

Three rules, applied case by case:

1. **Cheapest ROI-positive rung first.** Never buy capability you can't pay back
   from the labor it removes within months.
2. **Bank, then climb.** The savings from rung *n* fund rung *n+1*. Cash compounds
   up the ladder; no external capital, no plant.
3. **Every rung stands alone.** Each is independently profitable and shippable.
   The ladder is an *option chain*, not a dependency chain — we can halt at any
   step and keep the gains.

This is the inverse of hard automation (huge upfront line, payback over years, all
risk front-loaded). Here risk is **diced into rungs**, each small and self-funding.

---

## 2. The ladder (grounded costs & payback)

Costs assume the existing machinery is already owned (the retrofit-over-existing
premise); "arm" = a cheap used 6-DOF or the Ender/Fairino already in the cell.
Labor $ = $13/hr = **$0.0036/sec** (Talos org rate).

| Rung | Capability (op#) | ~Spend | Labor removed | Payback | Unlocks | Stand-alone ROI |
|---|---|---|---|---|---|---|
| **R0** | **Digital formboard + pick-to-light** (no robot) | $0.8–1.5k (printer+cam+light; sw free) | speeds **formalama (op7)** 20–40% + kills rework/mis-wire | weeks | 6×6 grid std, recipe **positionMap** (teach), the data spine | ✅ error↓ + speed↑ |
| **R1** | **Inline vision QC + existing-tester integration** (op8,9) | ~$0.3k | göz-kontrol (op9) + field escapes | weeks | closed-loop verify the robots need | ✅ scrap/return↓ |
| **R2** | **Screw-driving cell** (op4) — 1 arm + screwdriver + `block_clamp` nest | arm + ~$0.5k tooling | klemens vidalama; part of dizgi 12 s/pin | months @ Arkel vol | robot stack proven on the easiest dexterous task | ✅ at volume |
| **R3** | **Insertion** (op2) — + gripper (term-block, then housing) | ~$0.5–1k | dizgi 12 s/pin in full | months | autonomous plug population | ✅ at volume |
| **R4** | **Machine tending Role A** (F3 first) — arm + magazine + presentation fixture | arm + ~$1–2k/mach | **a whole operator per semi-auto** | fast (full FTE) | lights-out front-end | ✅✅ highest $/effort |
| **R5** | **Single-arm + active-infra deformable assist** — servo posts/combs + 1 arm | $50–200 steppers + board | part of formalama (op7) | medium | the deformable approach, incrementally | ◐ partial |
| **R6** | **Bimanual deformable** (§4) — 2nd arm + active board + GPU vision | $5–15k+ (biggest) | the **rest of formalama** (#1 sink) | only if vol justifies | full lights-out assembly | ⚠ research-risk, last |
| **R7** | **Line integration** (AGV/conveyor) + **pano-way** service | incremental | inter-station handling | scale | the Xometry-for-panels business | scale play |

**Reading the ladder:** R0–R1 are near-free and pay back on quality alone — do
them now, no robot. R2–R3 prove the dexterous stack on bounded, low-risk tasks
(reuse `screw.go`). **R4 is the best dollar-for-dollar move** — tending a semi-auto
removes an entire operator, not seconds-per-pin — so once the stack is proven, R4
funds the rest fastest. R5–R6 are the **deformable frontier**, escalated to only
after R0–R4 have banked the cash and the domain knowledge.

---

## 3. The economics engine (why it compounds)

One operator ≈ $13/hr × ~2,000 productive hr/yr ≈ **~$26k/yr** of removable cost.
A rung that removes even **0.3–0.5 operator** pays back a **few-$k** investment in
**3–6 months**, then throws off cash for the next rung.

Per-unit pool, concretely: a 1004930-class harness has tens of terminal positions.
At 12 s/pin dizgi + 20 s formalama + 30 s test, a harness with ~30 positions is
~**410 s ≈ $1.48 of removable assembly labor**. Order **SA141228 = 5,600 pcs** →
~**$8.3k** of labor on a *single order* — more than the entire R2+R3 spend. **One
big Arkel order funds the climb from screw-driving to full insertion.** That's the
self-funding loop made literal.

> The incumbent can't run this loop: their cost floor is a fixed labor base they
> can't shrink rung-by-rung. We remove labor in $0.3k–$2k increments, each paid by
> the last. They remove it in $45M increments, paid by nobody until years out.

---

## 4. The bimanual deformable endgame (the hard rung, analyzed)

Formalama — routing/dressing/tying the floppy bundle — is the **#1 labor sink** and
the **one robotics-hard step** ("deformable-object manipulation is an emerging
research problem," per the market lit). The user's instinct is right: the human
does it **bimanually** (one hand anchors a node, the other strokes the wire into
place), at **desk scale**. So the endgame is **two tabletop arms** — human-analog,
human-workspace-sized.

### 4.1 Don't build human hands — build a cell that needs less than human hands
The trap is trying to match human dexterity. Instead, **collapse the required
dexterity** by offloading two of the three hard sub-problems to cheap infra:

| Sub-problem | Human uses | We offload to |
|---|---|---|
| **Holding** the routed topology | fingers as temporary clamps | **passive combs/forks + ACTIVE servo posts** (the "underlying infra" — the extra hands). Arm routes → post clamps → arm lets go. |
| **Knowing** the deformable state (where the wire is) | eyes + proprioception | **GPU vision** — wire-centerline tracking + verify vs the taught path (rented-GPU physical-AI). |
| **The dynamic move** (anchor + dress) | two hands | **two arms** — the *only* residual that genuinely needs the 2nd arm. |

So: **active infra absorbs "holding," GPU absorbs "knowing," and the second arm is
added only for the residual "moving."** That is what makes a research-hard problem
into a buildable cell — and it's exactly the active-board (§10.2-④) + GPU-brain
(thesis §3c) you already have on the roadmap.

### 4.2 The bimanual primitive
```
arm A: grasp + ANCHOR a node (or the trunk)         ← static, low precision
arm B: STROKE the wire from the anchor into the next comb slot   ← dynamic
active post: CLOSE around the seated wire             ← infra holds it
arm B: release; GPU vision verifies the segment vs taught path
repeat to the next breakout; tie head cinches at stations
```
The arms never have to hold the whole bundle in free space — the board does. They
do one anchored stroke at a time, gated by vision. This is tractable with today's
arms; it is not VLA-grade general manipulation.

### 4.3 Cost, risk, and why it's last
- **Highest spend** (a 2nd arm + integration) and **highest research risk**
  (deformable perception, stroke-path planning). → R6, funded by R0–R5.
- **Gated on volume:** if formalama volume doesn't justify a 2nd arm, **stop at
  R0/R5** — pick-to-light already captured most of formalama's value for ~$1k. The
  doctrine explicitly permits stopping short here.
- **Hardware:** "two arms on a desk" is now affordable — two small 6-DOF arms
  (~$3–6k each) or **Fairino + Cartesian acting as the two hands** (already in the
  cell). Desk-scale, human-workspace — fits the low-spend doctrine.

### 4.4 Incremental path *into* bimanual (so even R6 is diced)
1. **R5a:** one arm + active posts dress the *simple straight runs*; human does
   breakouts. (Removes the easy 60% of formalama.)
2. **R5b:** add GPU centerline tracking + verify on those runs.
3. **R6a:** add the 2nd arm for **anchor-and-stroke on one branch type**; expand
   branch types one at a time (case by case).
4. **R6b:** robot tie at stations.
Each sub-step is independently shippable — the war, fought meter by meter.

---

## 5. Decision rules (when to climb, when to stop)

- **Climb when:** banked savings ≥ next rung's spend **and** next rung's payback
  < ~6 months **and** the rung's labor pool (per SKU × volume) is real.
- **Sequence by $/effort, not by tech novelty:** R0→R1 (quality, near-free) →
  R2→R3 (prove the stack, low risk) → **R4 (whole-operator wins, best $/effort)** →
  R5→R6 (deformable, only once funded).
- **Stop when:** a rung's marginal labor pool < its cost. Every prior rung is
  already profitable; there is no penalty for stopping.
- **Always:** new SKUs ride the same 6×6 grid + compiler-generated printed jigs →
  changeover cost stays ~$0, so the ladder's economics hold across the high-mix
  catalog (the whole point vs hard tooling).

---

## 6. Risk register

| Risk | Mitigation |
|---|---|
| Deformable perception (R5–R6) hard | offload holding to active infra; GPU only tracks/verifies; keep human fallback (pick-to-light) |
| Arm capex | reuse existing arms; tabletop/used 6-DOF; tend-an-arc layout (1 arm, many machines) |
| High-mix changeover | printed jigs + compiler → ~$0/SKU; teach positionMap once per SKU |
| Safety (crimp/screw pinch, arm motion) | light curtains + existing e-stop latch; "human out" is the point |
| Teach-data quality (Arkel no from-to) | capture positionMap on first build via pick-to-light + camera; reuse teach-and-repeat |
| Over-investing | the stop rule — never spend ahead of banked savings |

---

*Code is the source of truth; re-grep `desktop/agent/robot/`,
`firmware/yaver-companion` before building. Costs are 2026 estimates against the
Simkab/Talos labor + op data; treat as planning figures, not quotes.*
