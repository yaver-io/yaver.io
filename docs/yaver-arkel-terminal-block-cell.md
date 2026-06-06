# Yaver — Arkel terminal-block insertion + screw-driving cell

**Status:** design, 2026-06-06, `feat/yaver-robot-cell`. Design-only.
**Scope:** Arkel op **4 (klemens vidalama)** + the insertion that precedes it —
land a **ferruled NYAF 0.75 mm² wire** into a **Megaradar 5.08 mm pluggable
terminal block** and **drive the M2.5 screw to torque**, verified. The most
tractable Role-B operation and the first robotic labor removed on the Arkel line.
**Grounded in:** Talos PROD recipe **1004930** (Arcube Asenkron EN81-20) + the
Simkab machine/op data. **Reuses:** `robot/screw.go` (torque-gated drive),
`robot/control.go` (`ForceMove`/execute), `firmware/yaver-companion` (INA219/HX711
torque), `robot/verify.go` (vision), the 6×6 grid + `block_clamp.scad`.

---

## 1. Ground truth (from Talos)

- **1004930 BOM** (real, PROD): NYAF 0.75 mm² single-core wire **metered per
  color** (Sarı/Yeşil 3.31 m, Kırmızı 3.45 m, Siyah 2.09 m, Açık Mavi 3.72 m,
  Kahve 6.31 m, …) + Phoenix **`AI 0,75-8`** insulated ferrules ×100 + **Klemsan/
  Megaradar 5.08 pluggable terminal blocks** (codes 11.202 ×6, 11.254 ×3, 11.257,
  11.252 ×2, 11.203, 11.206, 11.680, 12.202, 10.602) + varistors + CC36-K9.
- **Connector = Megaradar 5.08 mm female pluggable plug**, rising-cage ("elevator")
  clamp, **M2.5 screw**, ≤2.5 mm²/16 A, 2/3/5/8-pin. Wire enters the **front**
  (horizontal); **screw on top**.
- **Torque: not recorded** in Talos (only a yes/no `TORQ` QC flag). Use the
  Klemsan/M2.5 figure **≈0.4–0.5 N·m** (confirm against Megaradar datasheet).
- **Labor today:** org rate **$13/hr**; dizgi (insertion) **~12 s/pin**, RKES path
  6 s/wire when klemens+makaron. Driver = JCWelec CST18D. → op 4 is ~12 s/pin of
  pure manual labor; that's what this cell removes.
- **Wiring style:** **color-grouped cut-lists, NO from-to** (§3 — the key
  constraint).

---

## 2. The mechanical crux: insert ⊥ screw

Wire enters the plug **horizontally** (front cage mouth); the M2.5 screw is driven
**vertically** (top). Two orthogonal axes. Three ways to handle it:

| Option | How | Verdict |
|---|---|---|
| **A. Tilt the plug ~35–45° in the nest** | nest presents the plug canted so one Fairino wrist orientation reaches both the cage mouth and the screw head with a small re-orient | **preferred** — one tool family, one approach zone, gravity helps the ferrule seat |
| B. Role-split | a gripper inserts+holds the ferrule (Cartesian or 2nd tool); Fairino only drives the screw | simplest controls, but needs a holder/2nd actuator |
| C. Dual-head end-effector | coaxial wire-pusher + screwdriver bit on one flange | most compact cycle, most tooling to build |

Recommend **A + tool-change**: plug tilted in the nest; Fairino picks the ferruled
wire (gripper tool), inserts to the cage backstop, swaps to the **screwdriver**
(you already drive one via tool-DO) and drives the M2.5 to torque. The tilt makes
both the cage and the screw reachable in one work cone.

---

## 3. The from-to gap — and why the jig solves it

**Arkel ships no point-to-point routing** — only "N wires of color C, length L."
The Talos from-to fields (`kesimRows.connectionDetail`, `endAInsertPos`) are
**empty for Arkel** (populated only for Harting/Eaton). So we cannot compile an
Arkel robot program from a from-to that doesn't exist.

**Solution — capture the position map once, then repeat** (reuse Yaver
teach-and-repeat, already built):
1. Hold the plug in the nest on the 6×6 grid (pose per cage known by construction).
2. **Teach pass:** build one 1004930 the normal way with **pick-to-light** guiding
   the operator; the top-down camera + the operator's confirm records **which
   color lands in which plug:position** → write it into the recipe as a
   `positionMap` (augments the BOM that Talos already has).
3. **Repeat:** the cell now runs the captured map autonomously — insert + screw +
   verify per position — forever, for that SKU.

This turns the missing digital routing into a **one-time teach**, and the jig +
recipe become the routing record Arkel never gave us. (Long-term: ask Arkel for
the drawing → `recipeCatalogEntries.externalDocuments`, or get Harting/Eaton-style
EPLAN for the compiler path; Arkel just needs the teach.)

---

## 4. The cell

```
ferruled NYAF wire (from F3/RKES, kitted by color+length)
        │  Fairino gripper picks the ferrule (rigid tip)
        ▼
  [Megaradar plug in tilted nest on 6×6 grid]   pose per cage from 5.08 pitch
        │  1) INSERT  ferrule -> cage mouth, ForceMove to backstop (F/T or hard-stop)
        │  2) tool-change -> screwdriver
        │  3) DRIVE   M2.5 screw to ~0.4-0.5 N·m  (screw.go DriveScrew + companion torque)
        │  4) VERIFY  vision: correct color in position P, ferrule fully in, screw seated
        │  5) PULL    tug to retention spec (F/T) — catch unseated/over-stripped
        ▼
  next position -> next plug -> plug populated -> to formalama/test
```

### Control mapping (all existing)
- **Insert:** `ForceMove` along the cage axis with a low Wrench cap; stop on
  backstop contact (ferrule `AI 0,75-8` = ~8 mm pin, so insert depth ≈ 8 mm). The
  rigid ferrule + a funnel on the nest cage mouth forgive ±0.5 mm.
- **Screw-drive:** `robot/screw.go::DriveScrew` torque-gated loop, target
  0.4–0.5 N·m, torque from the companion (INA219 current×Kт, or HX711 on a
  reaction arm). This is *exactly* what `screw.go` already does for the Ender
  screwdriver — Fairino fires the same driver via tool-DO.
- **Verify (op 9 folded in):** `VerifyMotion` — "is a <color> wire fully seated in
  position P, screw flush, no stray strand?" Qwen3-VL/Gemini. Cross-checked
  against the recipe `positionMap`.
- **Pull-test:** retract with capped Wrench; below spec → reject (over-strip / not
  seated / bad ferrule). Records a real retention number where Arkel had only a
  yes/no flag — an inline QC upgrade.
- **Gate/recover:** strip-out, cross-thread, wrong color → e-stop latch / BT
  supervisor (loop 3).

---

## 5. The nest — `hardware/yaver-harness-jig/block_clamp.scad`

Parametric for any N-pin Megaradar 5.08 plug:
- captures the plug body (anti-rotation), **tilts it** (`tilt_deg`) so cage mouths
  + screw heads share one work cone,
- **funnel per cage** (lead-in for the ferrule), **back-stop** so insert force
  reacts into the fixture,
- **screw-head windows** open to the top for the driver,
- flat **6×6 grid baseplate** (M5 @ 25 mm) → plug pose known by construction,
- self-fiducial dot for vision position-indexing.

Params: `pins, pitch=5.08, screw_pitch=5.08, body_w/d/h, cage_d, ferrule_d,
funnel, tilt_deg, foot_cells`.

---

## 6. Build order (smallest provable steps)

1. **Print `block_clamp.scad`** for the 1004930 plugs (start with the most common,
   e.g. the 2-pin 11.202 ×6) on the 6×6 base.
2. **Screw-drive first** (lowest risk, reuses `screw.go`): hold a pre-inserted
   wire, prove M2.5 drive-to-torque + torque record + vision "screw seated." This
   alone removes the `klemens vidalama` labor.
3. **Insert next:** ferrule → cage to backstop + pull-test.
4. **Teach the 1004930 positionMap** (§3) → run one full plug autonomously.
5. **Combine** insert+screw per position; scale to all plugs in 1004930; then the
   other 41 Arkel SKUs (same families, same nest generator).

---

## 7. Open questions (to finish the first build)

1. **Megaradar plug part ↔ Klemsan code map**: which physical Megaradar SKU is
   11.202 / 11.254 / etc., and pin counts per 1004930 (the BOM has the counts; I
   need the pin-count per code). Drives the `block_clamp.scad` instances.
2. **Screw drive type** — slotted vs Pozidriv on the Megaradar M2.5 (sets the bit).
3. **Confirm torque** from the Megaradar datasheet (≈0.4–0.5 N·m assumed).
4. **Ferrule OD** of `AI 0,75-8` (crimped) → the nest `ferrule_d`/funnel.
5. **Teach UI**: confirm we capture the positionMap via pick-to-light + camera on
   the first build (reuse teach-and-repeat program store).

---

*Code is the source of truth — re-grep `robot/screw.go`, `robot/control.go`,
`firmware/yaver-companion` before building (parallel sessions). Megaradar/torque
figures are vendor estimates pending datasheet; 1004930 BOM is from Talos PROD.*
