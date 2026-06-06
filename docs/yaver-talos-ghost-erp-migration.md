# The Ghost: risk-free ERP migration via screen-observed, computer-use sync

Deep analysis of the "install a ghost first" go-to-market and the technical
architecture behind it. The ghost is a **Yaver** capability (heavy execution);
**Talos** utilizes the knowledge it produces (the order + machine-knowledge
plane). This doc is the design for the highest-leverage thing the
screenlog/DVR work unlocks: **using a screen-observing AI agent to make
switching ERPs risk-free.**

Grounded in real code: Yaver's `screenlog_*` (observe), `activity_report.go`
(understand), the agent runner (`claude-code`/`codex`); Talos's Convex schema
(`screenRecordings`, `screenFrames`, `machineRecordingSessions`,
`machineSessions`, `manufacturingReferenceFamilies`, `yaverConfig`).

---

## 0. The pitch, in one breath

> "Don't rip out your ERP. We install a **ghost** that watches how your people
> actually work — your ERP, your machines, your spreadsheets. It learns your
> whole operation, migrates your data into Talos, and then **keeps your old ERP
> updated for you** by using it the same way your staff does. So you can try
> Talos with your old system still live and current underneath. If you hate us,
> you've lost nothing. If you love us, you switch when *you're* ready."

That last sentence is the entire wedge. **ERP migration's #1 blocker is fear**,
not features — you're betting the business on an unproven system, and the old
one goes stale the moment you stop double-entering. The ghost removes the fear
by **keeping the old ERP a live, synced fallback** for as long as the customer
wants. The objection "what if it doesn't work" disappears.

---

## 1. Why this is the wedge (and why it's defensible)

Selling a new ERP is the hardest sale in B2B software:

- **Switching cost is existential.** Wrong migration = lost orders, payroll,
  inventory truth. CFOs say no by default.
- **The old ERP goes stale instantly.** The day you start entering in the new
  system, the old one diverges. Now you can't fall back. So nobody dares cut
  over — they run parallel by *double-entering by hand*, which is so painful it
  kills most migrations.
- **"Just trust us" doesn't scale.** Every ERP vendor promises a smooth
  migration. Buyers have been burned.

The ghost flips all three:

| Blocker | Ghost's answer |
|---|---|
| Switching is existential | Old ERP stays **live + auto-synced** → switching is reversible |
| Old ERP goes stale | The ghost **drives the old ERP** (computer-use), so it never goes stale |
| "Just trust us" | The ghost **proves understanding first** — it shows you it learned your process before you commit a byte |

**Defensibility:** the moat isn't the migration script (every vendor has one).
It's that **the ghost learns each customer's actual, idiosyncratic process by
watching it** — the undocumented steps, the tribal knowledge, the "we always
override field X on Tuesdays." That learned process model is Talos's
machine-knowledge plane (§6). It compounds per customer and can't be copied
from a brochure.

---

## 2. The three acts

### Act I — Observe & Learn ("the ghost shadows you")
Yaver's screenlog runs on the operators' machines (and the back-office ERP
machines). It already captures, **locally and privately**:

- **frames** — `~/.yaver/screenlog/<id>/*.jpg`, dedup'd, active-app/window
  tagged (`screenlog.go`, `screenlog_window.go`).
- **input events** — `events.jsonl` in the **computer-use action schema**
  (`{t,type,x,y,button,key,text,dx,dy,screenW,screenH}`,
  `screenlog_input.go`) — keystrokes + clicks paired 1:1 with frames, the
  exact substrate a GUI agent replays.

On top of capture, Yaver **understands** (this is the heavy-work the user wants
the SDK to own, §5):
- deterministic `ActivityReport` (`activity_report.go`) → "what tools/screens
  did this person spend time in, when."
- **runner-driven semantic analysis** → "what *task* were they doing, in what
  *system*, with what *data*" — a `claude-code`/`codex` runner reads sampled
  keyframes + the event trace and emits a structured **process model** (§5).

Output of Act I: a per-role, per-station **process model** — the screens, the
fields, the sequences, the decision rules, the exception handling — for both
the **ERP workflows** and **how the machinery/tools are operated**.

### Act II — Migrate ("we move your data into Talos")
Two migration channels, ghost-assisted:
1. **Bulk** — the obvious path: read the old ERP's DB/export (Logo SQL, ERPNext,
   CSV) and load Talos. Talos already does ERP ingestion (Logo/ERPNext sync
   into Convex). This handles the *structured* data.
2. **Tribal** — the part bulk misses: the ghost's process model captures the
   *undocumented* rules (which fields are really required, what the operator
   types that isn't in any schema, the manual reconciliations). These become
   Talos work-instructions / `manufacturingReferenceFamilies` and validation
   rules. **This is what makes the migration *correct*, not just *complete*.**

The ghost also **validates** the migration by replay: it can re-derive the old
ERP's current state from its own observations and diff against what landed in
Talos — catching mapping errors before cutover.

### Act III — Keep-Synced ("the catch")
After migration, the customer works in **Talos**. Every change in Talos that
should reflect in the old ERP is handed to the ghost, which **drives the old
ERP's UI exactly like a trained operator** — opens the right screen, fills the
right fields, in the right sequence, with the same overrides it learned in
Act I. The old ERP stays current with **zero human double-entry**.

```
   Talos (new, source of truth) ──change event──▶  Ghost  ──computer-use──▶  Old ERP UI
                                                     │
                                            (learned process model
                                             from Act I drives the keystrokes)
```

That's the catch: **the same observation that taught the ghost the old ERP now
lets it operate the old ERP.** Observe → Understand → *Replicate* — Yaver's
North-star loop (already named in the screenlog design) applied to ERP sync.

Why it's safe to offer:
- The old ERP is never *worse* than before (it's at least as current as manual
  entry, usually more).
- Cutover is a **dial, not a switch** — run both for a week, a month, a quarter.
- Rollback is "stop using Talos" — the old ERP is right there, current.

---

## 3. Division of labor — Yaver does the heavy work, Talos utilizes

This is the explicit architectural rule ("yaver will do heavy work, talos will
utilize it"):

| Concern | Owner | Why |
|---|---|---|
| Screen + input capture (DVR) | **Yaver** | already built (`screenlog_*`), local-first, cross-platform incl. WSL |
| Tool/system-usage understanding (process model) | **Yaver SDK** | the runner + analysis spine live here; heavy compute |
| Ghost replication (drive the old ERP) | **Yaver** | computer-use over the same capture substrate |
| Sync orchestration (Talos→old-ERP loop) | **Yaver** | execution plane; relay/agent already there |
| Knowledge plane (process models, SOPs, machine knowledge) | **Talos** | the moat; `manufacturingReferenceFamilies` et al. |
| Order/data system of record | **Talos** | Convex tables, ERP ingestion already built |
| Customer relationship, billing, UI | **Talos** | the product the customer buys |

Yaver is the **hands and eyes**; Talos is the **brain and the books**. Yaver
ships the structured knowledge over; Talos stores, reasons, and sells it. The
boundary is a single ingestion contract (§7).

---

## 4. The ghost lifecycle as a state machine

```
 INSTALLED ──▶ OBSERVING ──▶ MODELED ──▶ MIGRATING ──▶ SHADOW-SYNC ──▶ STEADY-SYNC
   (agent       (screenlog    (process    (bulk +       (ghost drives    (Talos is SoT,
    on boxes)    capturing)    model        tribal)       old ERP in       ghost keeps
                              built +                     read-only         old ERP synced
                              validated)                  validate)         indefinitely)
                                                              │
                                                       cutover dial 0→100%
```

- **OBSERVING → MODELED** gate: the ghost must *prove* it understands before
  touching anything — it replays a few real tasks in a sandbox / read-only and
  shows the operator "this is what I think you do." Human confirms. (Trust is
  earned, not assumed — same principle as screenlog's consent gate.)
- **SHADOW-SYNC**: the ghost performs the old-ERP writes against a **staging
  copy** first (or dry-run/diff), so the first real writes are verified.
- **STEADY-SYNC**: the catch in steady state. Bounded, idempotent, audited.

---

## 5. Yaver's heavy work, in detail (the SDK capability the user asked for)

> "yaver sdk should have analysis understanding of tool usage by screen record
> processing with context, deeply — talos will utilize it."

The new Yaver capability is a **tool-usage understanding pipeline**. It sits on
top of the existing observe/understand spine and emits a **ProcessModel**.

### 5.1 Pipeline
```
frames + events.jsonl
   │  (1) segment        → sessionize into TASK episodes (gap + app-change boundaries;
   │                        reuse ActivitySample interval logic from activity_report.go)
   │  (2) sample         → pick keyframes per episode (screenlog_frames {sample:N}, dedup-aware)
   │  (3) contextualize  → attach the input trace + active app/window/title to each episode
   │  (4) runner analyze → a claude-code/codex runner reads keyframes+trace and emits, per episode:
   │                        { system, screen, intent, fields_touched[], values, sequence[],
   │                          decision_rules[], exceptions[], tool/machine_used, confidence }
   │  (5) reconcile      → merge episodes into a per-role ProcessModel; dedup variants;
   │                        promote repeated sequences to canonical SOPs
   ▼
 ProcessModel  (structured, Talos-ingestable §7)
```

Steps 1–3 are **deterministic** (pure Go, local, free — extends
`screenlog_analyze.go`). Step 4 is the **runner** doing the semantic lift
(vision + reasoning over keyframes + the literal keystrokes). Step 5 is
deterministic aggregation. Critically: **the runner is the MCP client's runner**
(claude-code/codex on the user's plan), consistent with the existing screenlog
rule "no headless P-mode runner — the MCP client narrates." Heavy, but on the
customer's own agent subscription.

### 5.2 ProcessModel — the artifact
```jsonc
{
  "role": "order-entry-clerk",
  "system": "Logo Tiger ERP",              // inferred from screens/titles
  "episodes": [{
    "intent": "enter sales order",
    "screen": "Sales > Orders > New",
    "sequence": [
      {"step":"open Orders","ui":"menu Sales→Orders"},
      {"step":"set customer","field":"Cari Kod","source":"from email","example":"120.01.0042"},
      {"step":"add line","field":"Stok Kodu","note":"operator always tabs past Depo, leaves default"},
      {"step":"override price when customer=ACME","rule":"manual 5% discount"}  // tribal knowledge
    ],
    "fields_touched": ["Cari Kod","Stok Kodu","Miktar","Birim Fiyat"],
    "decision_rules": ["if customer ACME → 5% discount", "if urgent → set Teslim=today"],
    "exceptions": ["if stock<order → split into backorder line"],
    "machine_or_tool": null,
    "confidence": 0.88,
    "evidence_frames": ["slog-x/000123_d0_...jpg", "..."]
  }],
  "machinery": [{                            // "learn how machinery is used"
    "machine":"CST18D crimp line",
    "observed_use":"operator loads applicator RKES-27, runs 48 cycles, inspects every 12th",
    "params":{"applicator":"RKES-27","cycle_target":48,"qc_interval":12},
    "evidence_frames":[...]
  }]
}
```

This is **both** the migration spec (Act II) **and** the replay script (Act
III) **and** Talos's knowledge (§6). One artifact, three uses.

### 5.3 Ghost replication (Act III execution)
The replay engine turns a Talos change-event into old-ERP keystrokes using the
ProcessModel + live screen verification:
- **drive**: navigate to `screen`, fill `fields_touched` from the change-event
  payload, apply `decision_rules`, in `sequence` order.
- **verify-each-step**: after each action, capture a frame and confirm the UI
  reached the expected state before the next action (closed-loop, like the
  robot-cell vision-gating). Never fire blind.
- **idempotency key**: every synced write carries a Talos record id → the ghost
  checks "does this order already exist in the old ERP?" before creating
  (search-then-write), so retries don't double-post.
- **safe-stop**: any unexpected screen → halt + flag for human, never guess on
  a mutation.

Reuses the screenlog input substrate in reverse: instead of *recording*
keystrokes, it *emits* them (CGEventTap/SetWindowsHookEx inject, or the
existing platform input layer).

---

## 6. How Talos utilizes it (real tables, real ingestion)

Talos is Convex-backed; the receiving structures largely exist:

- **`screenRecordings` / `screenFrames`** — raw DVR chunks + frames (machineId,
  phash, activeApp, activeWindowTitle, summary). Yaver already produces exactly
  this shape locally; the ghost uploads chunk metadata + summaries (never raw
  frames unless the org opts in — privacy contract holds).
- **`machineRecordingSessions` / `machineSessions` / `tickBatches`** — operator
  session + per-minute action telemetry. The ghost's episodes map onto these.
- **`manufacturingReferenceFamilies`** — **the moat.** The ProcessModel's
  canonical sequences + decision_rules + machinery params become reference
  families / SOPs (purpose, pre-conditions, steps, quality checkpoints,
  per-variant instructions, photos/videos). This is "teach-by-demonstration at
  scale," now fed automatically by the ghost instead of hand-authored.
- **`employeeCompetencies` / `competencies`** — inferred skill: who operates
  what, how well (from success/error rates in episodes).
- **`yaverConfig`** — the existing per-org link (convexUrl, accountToken,
  deviceId, relayUrl, agentUrl, agentToken, defaultRunner). The control channel.

**Missing piece to add in Talos** (the explore confirmed it doesn't exist yet):
a `processModels` / `operatorKnowledgeInsights` table + an ingestion mutation
`ingestProcessModel` (or HTTP route `/yaver/process-model`, mirroring the
existing `/logo/sync` pattern in `cloud/convex/http.ts`). That's the single
contract Yaver POSTs to. ~1 table + 1 mutation on the Talos side.

---

## 7. The ingestion contract (the Yaver↔Talos boundary)

One direction for knowledge, one for sync commands:

**Yaver → Talos (knowledge, Act I/II):**
`POST /yaver/process-model` → upsert `processModels` keyed by (orgId, role,
system). Payload = the ProcessModel (§5.2). Talos derives SOPs, competencies,
and migration validation rules from it.

**Talos → Yaver (sync commands, Act III):**
Talos emits change-events (new/updated order, stock move, etc.) to the org's
Yaver agent (`agentUrl`/relay from `yaverConfig`) as a **sync job**:
`{recordType, recordId, payload, targetSystem:"logo", idempotencyKey}`. The
ghost executes via §5.3 and reports back `{status, evidenceFrame, oldErpRef}`.

Privacy: frames + keystrokes stay **local to the customer's Yaver agent**
(Yaver's Convex-forbidden-fields contract already enforces this). Only the
**structured ProcessModel + sync results** cross to Talos — never raw screen
content unless explicitly opted in. The ghost can run **fully on-prem /
air-gapped** (Yaver already supports local-model runners + air-gap policy).

---

## 8. The hard parts (honest risk analysis)

| Risk | Severity | Mitigation |
|---|---|---|
| **Computer-use reliability** — old ERP UIs are finicky; a missed field corrupts data | HIGH | verify-each-step (closed-loop), search-then-write idempotency, safe-stop on unexpected screen, shadow-sync against staging first |
| **Process model is wrong/incomplete** — tribal knowledge missed | HIGH | OBSERVING→MODELED human-confirmation gate; replay-and-diff validation before any write; confidence thresholds per episode |
| **Old ERP is a moving target** — UI changes, popups, latency | MED | anchor on text/labels not pixel coords; per-step screen verification; the model re-learns on drift (continuous observation never stops) |
| **Auth/session into old ERP** | MED | ghost uses the operator's own creds via Yaver vault (local, token-derived encryption); never stores plaintext |
| **Idempotent sync / conflicts** (edited in both systems) | MED | Talos is declared SoT during migration; conflicts flagged not auto-merged; every write keyed by Talos recordId |
| **Scale of observation data** | LOW | dedup + ephemeral-frame mode + local ring buffer already bound it (`screenlog` QoS) |
| **Trust / "is it spying on staff"** | MED–HIGH | the screenlog consent gate, audit trail, kill-switch (`yaver screenlog kill`), and visible-recording recommendations (see security audit) apply verbatim — this is the same surveillance-grade capability, so the same controls are mandatory |

The two HIGH risks both reduce to one principle: **never let the ghost write
blind.** Observe → prove understanding → dry-run → verified write. The robot
cell already proves this pattern (vision-gated motion); ERP sync is the same
loop with keystrokes instead of steppers.

---

## 9. Build-on-existing map

| Capability | Exists? | Where |
|---|---|---|
| DVR capture (frames+input) | ✅ built | `screenlog_*.go`, `events.jsonl` computer-use schema |
| Deterministic activity report | ✅ built | `activity_report.go`, `screenlog_analyze.go` |
| Keyframe sampling for vision runner | ✅ built | `screenlog_frames {sample:N}` |
| Agent runner (claude-code/codex) | ✅ built | runner spawn, ops verbs |
| Reboot-durable, on-prem, air-gap | ✅ built | autostart + local-model lane + air-gap policy |
| **ProcessModel pipeline (§5)** | ❌ new | Yaver SDK — the "tool-usage understanding" capability |
| **Ghost replication engine (§5.3)** | ❌ new | Yaver — input injection + verify-each-step loop |
| **Talos ingestion (`processModels` + mutation)** | ❌ new | Talos `cloud/convex/` — 1 table + 1 mutation + 1 HTTP route |
| **Sync orchestrator (Talos→ghost jobs)** | ❌ new | Yaver agent job loop + Talos change-events |

Roughly: capture + understand-spine is done; the net-new is **(a) the semantic
ProcessModel pipeline, (b) the verified replay engine, (c) the thin Talos
ingestion contract.**

---

## 10. Implementation roadmap (phased, each shippable)

1. **P1 — ProcessModel pipeline (Yaver SDK).** Extend `screenlog_analyze.go`:
   episode segmentation → keyframe sample → runner prompt → structured
   ProcessModel. New MCP verb `screenlog_process_model {id}`. *Pure analysis,
   no writes — safe, demoable ("look what the ghost learned").*
2. **P2 — Talos ingestion.** `processModels` table + `ingestProcessModel`
   mutation + `/yaver/process-model` route. Yaver `talos_process_model_push`.
3. **P3 — Read-only validation replay.** Ghost re-derives old-ERP state from
   observation, diffs vs Talos. Proves correctness with zero write risk.
4. **P4 — Verified write replay (shadow).** Ghost drives the old ERP against
   **staging**, verify-each-step, idempotent. The catch, in a sandbox.
5. **P5 — Steady sync.** Talos change-events → ghost jobs → old ERP, audited,
   dial-able cutover. The catch, in production.
6. **P6 — Machinery knowledge.** Extend the ProcessModel's `machinery[]` into
   Talos `manufacturingReferenceFamilies` (auto-authored SOPs from observed
   machine use). Closes "learn how machinery is used."

P1 is the keystone and the demo: it makes the pitch real ("here's your process,
learned in a day"). P3–P5 are the moat and the catch.

---

## 11. The sentence that sells it

**"Keep your ERP. We'll teach a ghost to run it for you, move your data to
something better, and keep the old one current so you never have to choose under
pressure."** Yaver is the ghost; Talos is where the business actually moves.
The DVR work we just built is Act I of this — the eyes are already open.
