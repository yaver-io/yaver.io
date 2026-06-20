# GLM + Hybrid Duo/Trio Orchestration — Session Status

> Snapshot 2026-06-20. Working tree only — **nothing committed**.

## TL;DR

| Area | Status |
|---|---|
| Agent code (hybrid routing) | ✅ done, builds (native + linux/arm64) |
| Unit tests | ✅ 6/6 pass (`agent_mesh_hybrid_test.go`) |
| Web UI selector | ✅ wired, tsc-clean |
| Mobile UI selector | ✅ wired, tsc-clean |
| Live Hetzner e2e | ⛔ blocked (env unstable, box unreachable) |
| Open runtime question | ⚠️ glm-runner vs claude subscription precedence — unresolved |
| Vault cleanup | ⚠️ leftover claude-override entries, CLI auth flapping |

## The design (confirmed)

Yaver's existing model: **wrap Claude Code + Codex on subscription LOGIN (flat plan,
the cheap path); GLM + OpenCode via APIKEY (metered).** The hybrid spends the free
subscription lanes first and spills parallel overflow to the cheap GLM apikey lane.

- `HybridDegree`: **0 = single-model (default, your plan)**, 2 = duo (claude-code+glm),
  3 = trio (claude-code+codex+glm). Optional — single-model unchanged.

## Done — files changed (uncommitted)

**Agent (`desktop/agent/`):**
- `console_machines.go` — added `glm` to the runner capability-detection loop.
- `agent_mesh.go` — `inferPreferredRunnerCandidates` offers glm for bulk/overflow;
  `choosePlacementModel` → `glm-5.2`; `runnerNeedsHostedAPIKey` includes glm;
  new `runnerCostTier`, `hybridLaneSet`, `filterRunnerLanes`; `chooseCandidateRunner`
  takes `req` + applies the lane filter; scorer emits cost-tier reasons.
- `agent_mode.go` — `AgentGraphCreateRequest.HybridDegree`.
- `httpserver.go` — `agent_graph_start` MCP arg `hybrid_degree` (POST `/agent/graphs`
  already decodes the struct directly, so the web/mobile JSON field flows through).
- `agent_mesh_hybrid_test.go` — new unit tests (pass).

**Web (`web/`):**
- `lib/agent-client.ts` — `createAgentGraph` accepts/sends `hybridDegree`.
- `components/dashboard/VibeCodingView.tsx` — `hybridDegree` state + Single/Duo/Trio
  segmented control + passed to graph creation.

**Mobile (`mobile/`):**
- `src/lib/quic.ts` — `createAgentGraph` accepts/sends `hybridDegree`.
- `app/(tabs)/agent.tsx` — "Cost Mode" segment (Single/Duo/Trio) + passed through.

**Docs:** `docs/yaver-multi-model-duo-trio-orchestration.md` (design + honest efficiency
analysis + implementation status + open question).

## Verification run

- `go build ./...` ✅  ·  `GOOS=linux GOARCH=arm64 go build` ✅ → `/tmp/yaver-glm-arm64`
- `go test -run 'TestHybrid…' .` ✅ all pass (scoped, no keychain)
- web tsc / mobile tsc ✅ no errors in changed files
- z.ai direct probe ✅ — coding-plan key + model `glm-5.2` returns `PONG` (Bearer + x-api-key both work)

## Blocked / open

1. **OPEN runtime question (gates the payoff):** does the `claude` binary honor the
   z.ai `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` override when a `--claudeai`
   subscription login exists? Earlier claude+glm-5.2 task hit Anthropic and failed
   ("model glm-5.2 may not exist") with the override present → suspect the binary
   prefers its stored subscription. If confirmed, the glm lane only works on boxes
   **without** a claude login; fallback = route glm via opencode's anthropic provider
   instead of the claude binary (~10-line change).
2. **Live Hetzner e2e not run** — CLI auth/vault flapping (`not authenticated` /
   `wrong passphrase`, token-rotation re-lock) + box exec-unreachable.
3. **Vault hazard** — `BASE_URL__claude` + `API_KEY__claude` still in `runner-provider`
   (earlier delete failed under lock). Remove when auth settles:
   `yaver vault delete BASE_URL__claude --project runner-provider` (+ `API_KEY__claude`).

## Environment changes made this session (be aware)

- **Vault was reset** (`yaver vault reset`) — old vault archived at
  `~/.yaver/vault.enc.reset-bak.20260620-014803` (encrypted under a rotated token →
  likely unrecoverable). App Store/Play keys have fallbacks: GH secrets +
  `~/.appstoreconnect/yaver.env`. Fresh vault holds `ZAI_API_KEY` (+ the 2 leftover
  claude-override entries to delete).
- **`yaver code` config** restored to `runner: claude`, plan-default model, local
  (the bogus `glm-5.2`/`zai-coding-plan` override was cleared).
- Local agent restarted once (PID changed) to reload the vault.

## Next actions (pick)

- **(a)** Retry Hetzner deploy + e2e once the box is authed/reachable (binary ready).
- **(b)** Preemptively add the opencode-anthropic-provider fallback so GLM works
  regardless of the subscription-precedence question.
- **(c)** Resolve the open question cheaply: one `glm`-runner task on the Hetzner box
  (no claude login there) — if GLM responds, the lane is live.
