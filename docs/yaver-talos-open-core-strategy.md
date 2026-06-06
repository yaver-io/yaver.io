# Yaver (open) + Talos (proprietary) — does open-source "mess it up"? (strategy)

> Your case: **Yaver is open source** (the engine + SDK), and you build **Talos**
> (proprietary, your wire-harness company) on top of it — the robot cell *and*
> machine-design troubleshooting *and* operator-onboarding speedup, all via Yaver's
> SDK. The worry: does open-sourcing the engine give away the business?
>
> **No — if the moat is the domain layer, not the engine.** Open Yaver is your
> *adoption wedge*; Talos's *domain IP + data* is the moat. The only way it goes
> wrong is blurring the line.

---

## 1. Why open Yaver is a strength, not a leak

The engine being open is *the point* ("OpenClaw for developers", free, no lock-in):
it drives adoption, trust, and an ecosystem — shops run Yaver "for whatever they
want," then buy Talos for the part they can't build themselves. You also build
Talos **faster on your own open SDK** (same as building a proprietary app on React
— the framework being open doesn't reduce your app's value).

A competitor *can* fork Yaver. They **cannot** fork:
- your **squeezing recipes** (the tuned torque/depth/dwell per terminal),
- your **production data** (runs, QC history, accumulated recipes),
- your **onboarding content** + **troubleshooting playbooks**,
- your **brand, support, integrations, customer relationships**.

The control engine was never the moat. Anyone can move a stepper. Knowing the
*right squeeze for AI-0.75-8 at 0.4 N·m* — and having years of it — is the moat.

## 2. The line you must never blur

| **OPEN — Yaver (engine + SDK + primitives)** | **CLOSED — Talos (domain config + content + data)** |
|---|---|
| Drive any machine: move/jog/rotate/GPIO/camera/verify/teach | The **right** parameters for *your* parts |
| Machine-hijack, mesh, AI runner, voice, camera vision | The **diagnostic playbooks** for *your* machines |
| Generic teach (record jogs), guided-flow engine | The **onboarding content** for *your* operators/procedures |
| `ConfigProvider` **interface** | The `TalosProvider` **implementation** (recipes, tuning, backup) |
| "How to drive a machine" | "What's correct for THIS machine/terminal/operator" |

Rule of thumb: **open = capability ("how"), closed = the encoded domain knowledge
("what's right")** — recipes, tuned configs, content, data, and the *tools that
author them*. If a thing took your company years of floor experience to learn, it
goes in Talos, never the Yaver repo.

## 3. The three Talos apps — same pattern, one moat

| Talos app | Open (Yaver SDK) | Proprietary (Talos) |
|---|---|---|
| **Robot cell** | control + teach + camera + verify | squeezing recipes + config tool + tuning |
| **Machine-design troubleshooting** | camera + AI + machine-hijack + mesh | your machines' quirks KB + diagnostic playbooks |
| **Operator onboarding speedup** | voice + AI + teach + guided-flow engine | your procedures + tuned onboarding flows + content |

Every one is *open engine + proprietary domain layer + your data*. Reuse the same
SDK; the value is always the closed content/config/data.

## 4. The real "fuck-ups" to avoid (the discipline)

1. **License choice — decide deliberately.** MIT/Apache = max adoption, but a
   well-funded competitor could fork Yaver and run a *competing* proprietary layer.
   AGPL or a source-available/BSL license keeps Yaver "open" while blocking
   SaaS-competitor forks. For a wire-harness shop the fork risk is low (your moat is
   recipes/data they can't fork), so MIT-for-adoption is defensible — but choose it
   on purpose, not by default.
2. **Never leak domain IP into the open repo.** Recipes, tuning algorithms,
   onboarding content, troubleshooting KBs live in the **closed Talos repo**. The
   `ConfigProvider` interface is open; the provider *with the IP* is closed. This
   discipline is the entire game — one accidental commit of the recipe library and
   the moat is public.
3. **Don't open-source the config *tools*.** The squeezing-recipe authoring/tuning
   tool *encodes* domain knowledge → keep it in Talos. Yaver only *consumes/runs*
   an opaque recipe; it can't *author* one.
4. **Make data gravity work for you.** Yaver is local-first (good, open); Talos is
   the **system-of-record + backup + analytics** for runs/QC/recipes → switching
   cost + a compounding data moat. Don't let the production record stay only on the
   edge where a competitor's tool could read it.
5. **Yaver platform Convex stays clean.** Only the open mesh (discovery + capability
   flags). No recipes/programs/QC there — they're the user's, in *their* Talos.

## 5b. Open *code* ≠ public *R&D* (the resolution)

The worry "I do robotics R&D I don't want public, so can Yaver be open?" dissolves
once you separate **code** from **data**:

- **Yaver open-sources the *engine*** — teach-and-repeat, jog/move/rotate, camera,
  verify, the SDK. The *machinery*. This is fine to be public; it's "all good."
- **Your robotics R&D is *data*** — the programs you teach, the squeeze recipes,
  the tuning experiments, the taught sequences, the QC results. None of this is in
  the Yaver repo. It's runtime data that lives on **your** infrastructure.

Open-sourcing teach-and-repeat does **not** publish a single program you teach with
it — the same way open-sourcing git doesn't publish your private repos, or Postgres
being open doesn't publish your tables. **The engine is public; the work product is
yours.**

So your private robotics R&D can be kept private **either place**, your call:

| Keep R&D in… | What it is | When |
|---|---|---|
| **Yaver, privately** | local-first edge files (`~/.yaver/robot-programs/`), your own self-hosted relay/Convex, your devices — never the public repo, never Yaver's platform Convex | solo / on-prem / air-gapped R&D; you just don't publish it |
| **Talos** | your proprietary backend: recipe library, tuning tools, production data model + backup | when you want the company tier — sync, multi-site, the polished config tools, the data moat |

Both are private. The difference isn't "open vs. closed" — it's "local-first edge
storage" vs. "your proprietary cloud product." Teach-and-repeat in open Yaver is
the *bridge*: you R&D privately with it today (data on your box), and graduate the
valuable, repeatable recipes into Talos when you want the product tier.

## 6. Bottom line

Open Yaver = the wedge (adoption, trust, your own fast SDK). Talos = the moat
(domain recipes + content + data + the tools that make them, all closed). It only
"messes up" if you put the moat in the open repo or open-source the config tools.
Keep the line — **open how, closed what's-right** — and open-source is pure
upside for you.
