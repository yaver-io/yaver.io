# Yaver Multi-Model Duo / Trio Orchestration — Deep Design & Honest Efficiency Analysis

> Status: **design analysis, 2026-06-20.** Grounded in the current code
> (`desktop/agent/agent_mesh.go`, `agent_mode.go`, `autopilot.go`,
> `tasks.go`, `provider_keys.go`). Reads the code as the source of truth per
> `CLAUDE.md`. Verdict section is deliberately skeptical — the ask was "be
> honest whether this would be efficient or not."

## 1. The vision (restated)

Run coding through **2–3 different model/runner backends at once** to cut cost:

1. A strong "planner" agent is prompt-engineered to **write a breakdown `.md`
   that decomposes the task into independent parts first**.
2. **Auto mode assigns those parts to 2–3 backends** — cheap work to
   **z.ai / GLM-5.2**, some to **Codex (GPT-5.5)**, the hard/coherence parts to
   **Claude Code (Opus)** — to **spend tokens where they're worth it**.
3. Runs **autonomously on a remote box** (`yaver auto`), and the **same routing
   is usable from local `yaver code`** to reduce a solo dev's agent bill.

Two strategies hide inside this, and they have **opposite** efficiency
profiles. Naming them is the whole game:

- **Strategy A — Split-and-fan-out.** Cut *one task* into parts, hand parts to
  different models *in parallel*, stitch the results.
- **Strategy B — Tiered routing / escalation.** Don't split. Send *each whole
  task* (or slice) to the *cheapest model that can do it*, and **escalate to a
  pricier model only when the cheap one fails or the work is hard.**

The vision as written is mostly Strategy A. **The money is almost entirely in
Strategy B.** Section 5 is why.

## 2. What Yaver actually has today (code-grounded)

Yaver already ships a real multi-agent DAG orchestrator — it is *not*
hypothetical:

- **Agent graph = DAG of slices.** `AgentGraphNodeSpec`
  (`agent_mode.go:51`) with `DependsOn`, per-node `Runner`, `Model`,
  `AllowedRunners`, `ResourceModes`, and scoring hints
  `DesignPoints`/`BuildPoints`/`VerifyPoints`. The default template is exactly
  **plan → implement → verify** (`buildAgentGraphTemplate`,
  `agent_mode.go:309`). You saw it live earlier: *"Plan Slice → Implement →
  Verify And Synthesize"* split across this Mac and the Hetzner box.
- **Capability-based placement scorer.** `scoreNodePlacement`
  (`agent_mesh.go:241`) picks a machine **and** a runner per node from rich
  signals: hard pins (+1000), offline (−5000), runner-ready (+220), iOS↔macOS
  (+300), Android-capable (+280), local-LLM intent (+260), high-RAM (+35),
  shared-infra deboost (−80…−400), per-machine/per-runner load balancing.
- **Per-task-type runner preference.** `inferPreferredRunnerCandidates`
  (`agent_mesh.go:455`): plan → `[claude-code, codex, opencode]`; build →
  `[codex, opencode, claude-code]`; verify → `[codex, claude-code, opencode]`.
- **Per-node model — but only for Claude.** `choosePlacementModel`
  (`agent_mesh.go:488`): claude-code planning chat → `claude-opus-4-6`, build/
  verify → `claude-sonnet-4-6`. **Every other runner returns `""`** (no model
  chosen).
- **Autonomous remote execution is real.** `AutopilotManager`
  (`autopilot.go:16`) runs a todo batch unattended, persists to
  `~/.yaver/autopilot.json`, tracks `TotalCost`/`TurnCount`. Remote nodes run
  via `executeRemoteChatNode` (`agent_mode.go:971`) over the mesh
  (LAN→tailscale→direct→relay fallback). This is the "yaver auto at remote box."
- **Git worktree isolation per node** (`graph_slice.go:93`) so parallel local
  nodes don't clobber each other — the real enabler for safe fan-out.

## 3. The gap between the vision and the code

The exact thing you described is **not wired**, and the gaps are specific:

| Vision element | Reality in code |
|---|---|
| Route cheap work to **z.ai / GLM** | **`glm` is not in the graph runner rotation.** `inferPreferredRunnerCandidates` only emits claude-code/codex/opencode (`agent_mesh.go:455`). The `glm` runner exists for single tasks (`tasks.go:219`) but the mesh planner never selects it. |
| **Cost-aware** assignment | **Not implemented.** `MachineInfo.Cost` (`console_machines.go`) is a display label, **never read by the scorer**. No token budget, no $/token table, no "cheapest capable model" logic anywhere. The planner is capability- and latency-aware, **cost-blind**. |
| **"Auto mode" assigns to 2–3** | The `orchestration_mode` auto/manual toggle was **dropped 2026-04-28** and is an **inert config field** (`config.go:190`). Mesh placement still runs, but there's no user-facing "auto-split across N cheap models" switch. |
| Pick model **per runner** | Only claude-code gets opus/sonnet selection; codex/opencode/glm get `""`. |
| **Cross-model verify** (model B checks model A) | No enforcement. Verify is a *separate node* but can land on the *same* runner as implement. No ensemble, no vote, no "different model must review." |

So: the **skeleton** (DAG, placement, remote autopilot, worktree isolation) is
solid and shipping. The **cost-routing brain** the vision needs is the missing
~200 lines, not a missing platform.

## 4. What it would take to wire the vision (small, concrete)

1. **Add `glm` to the runner rotation** — extend
   `inferPreferredRunnerCandidates` so bulk/mechanical slices prefer
   `[glm, opencode, codex]`, and give `choosePlacementModel` a `glm → glm-5.2`
   default (`agent_mesh.go:455,488`).
2. **Add a cost signal to the scorer** — a static `$/1M` table per runner
   (glm-5.2 ≈ $1.4/$4.4 · opus ≈ 5–10× · gpt-5.5 ≈ similar) and a bonus of
   `+k·(1/price)` for slices flagged "bulk." Bounded, deterministic, ~30 lines
   in `scoreNodePlacement`.
3. **Tag slices cheap-eligible at decomposition** — the planner MD already
   emits `DesignPoints/BuildPoints/VerifyPoints`; add a boolean
   `coherenceCritical`. Critical → Opus/Codex; non-critical bulk → GLM.
4. **(Optional) cross-model verify** — force the verify node's runner ≠
   implement node's runner. Cheap insurance; one line in placement.

None of this needs a new subsystem. It rides the existing graph.

## 5. Honest efficiency evaluation — does this actually save money?

This is the part you asked for bluntly. The honest answer is **"it depends, and
the popular version of the idea is usually a net loss."**

### 5a. Why Strategy A (split one task across 2–3 models) usually loses

1. **Coding tasks are not embarrassingly parallel.** Real diffs share types,
   interfaces, and call sites. "Independent parts" is *rare* — and the parts
   that *are* independent are usually the easy ones. The single biggest value
   of a frontier long-context model is holding the **whole** task coherently;
   splitting throws that away to save on the cheap parts.
2. **Context gets paid for N times.** Each of 3 agents must load the relevant
   files/types to not break the interface. You pay input tokens **3×** instead
   of 1×. For many tasks this *erases or inverts* the per-token discount of the
   cheap model. Cost reduction can become cost **increase**.
3. **Integration is the new bottleneck, and it's expensive.** Someone — usually
   a *frontier* model — must reconcile 3 different code styles, fix interface
   mismatches, and resolve merge conflicts. That reconciliation often re-reads
   everything and partially re-does the work. The barrier at "stitch" eats the
   parallelism win in both **$** and **wall-clock**.
4. **Decomposition has up-front cost.** Having "the best agent" (Opus) write the
   breakdown MD is itself frontier tokens spent *before* any code. For small/
   medium tasks that overhead alone exceeds the savings.
5. **Cross-model handoff loses fidelity.** GLM's, Codex's, and Claude's
   assumptions differ. Stitched code is less consistent and harder to verify —
   more verify tokens, more bugs that slip through.

**Net:** for a *typical coupled feature/bugfix*, split-and-fan-out across 2–3
models is **slower and not cheaper**, and lower quality. Don't ship it as the
default.

### 5b. Where Strategy A genuinely wins

It works when the work is **actually independent and mechanical**, so there's no
shared context to duplicate and no integration step:

- "Write unit tests for these 40 pure functions."
- "Migrate these 30 files by the same mechanical rule."
- "Translate/port these isolated modules."
- Fan-out **read-only research/review** (no merge problem at all).

Here, cheap models (GLM-5.2, GLM-4.7, DeepSeek-Flash) do the job fine and trio
mode is a real win. This is the minority of coding work but it's exactly what
Yaver's worktree-isolated graph + autopilot are good at. Note GLM's known weak
spot — **test quality and long-horizon failure recovery** — so verify the
generated tests.

### 5c. Where the real savings are — Strategy B (tiered routing)

The honest, boring, high-ROI answer: **don't split — route, and escalate.**

- **Make GLM-5.2 the default model everywhere.** It benchmarks at ~94% of Opus
  on SWE-bench Verified at ~⅓ the price. For the bulk of edits, the quality gap
  is invisible. That single change cuts a solo dev's bill ~3× with ~6% quality
  hit — **no decomposition, no merge risk, no coordination overhead.**
- **Escalate, don't parallelize.** Run the slice on GLM first; if it fails tests
  / the verify node rejects / it stalls, *retry the same slice on Opus or
  Codex.* You pay frontier price only on the slices that need it. This is
  strictly better than pre-assigning a model blind, because the router learns
  per-slice difficulty from the *result*, not a guess.
- **Use a second cheap model as the verifier**, not the implementer. Cross-model
  verify (GLM implements → Codex/Claude sanity-checks the diff) catches a real
  fraction of errors for little money. This is the *one* multi-model pattern
  that pays for itself broadly.

Tiered routing captures ~80% of the achievable savings with ~5% of the
complexity and risk of split-and-fan-out. It's also **already half-built**:
Yaver's plan→implement→verify graph + per-node runner selection is the right
shape; it just needs GLM in the rotation and a "retry-on-fail-with-stronger-
model" edge (today a failed node fails — `agent_mode.go` has **no escalation
retry**).

### 5d. The local `yaver code` cost play (your last point)

For a solo dev, the cheapest correct setup is **not** trio mode — it's:

1. Default `yaver code` to **GLM-5.2 via the fast `claude`-runner base-URL swap**
   (Claude Code harness, z.ai endpoint — *not* OpenCode, which is the slowness
   you hit). ~3× cheaper than Opus, same harness quality.
2. Keep an explicit **escape hatch**: `yaver code --runner claude --model opus`
   for the rare gnarly task.
3. Reserve fan-out (autopilot/graph on the remote box) for the genuinely
   parallel, mechanical batches from §5b — overnight test-writing, mechanical
   migrations — where cheap models shine and there's no integration tax.

That's the efficient frontier. Trio-splitting a single interactive coding task
on your laptop would make it **slower and more error-prone**, not cheaper.

## 6. Bottom line

- **The platform is real** (DAG, placement, remote autopilot, worktree
  isolation). The **cost-routing brain is missing** (~200 lines: GLM in
  rotation, a price signal, escalation-on-fail).
- **Split-one-task-across-3-models is usually a net loss** for coupled coding
  work — context is paid N×, integration is the bottleneck, quality drops.
- **Tiered routing wins:** default to GLM-5.2, escalate to Opus/Codex only on
  failure, use a second cheap model to verify. ~3× cheaper, ~6% quality hit,
  near-zero coordination risk.
- **Fan-out only the embarrassingly-parallel, mechanical batches** — that's the
  honest home for "trio mode," and Yaver's autopilot already fits it.
- **Quick win, blocked:** make GLM-5.2 the local `yaver code` default via the
  claude-runner→z.ai swap. Currently gated by a **locked vault**
  (post-token-rotation). Unlock with `YAVER_VAULT_PASSPHRASE`, or decide on
  `yaver vault reset`, and this lands in minutes.
