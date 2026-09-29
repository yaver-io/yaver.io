# Handoff — Remoteless AI lane (P0) + plan for P1–P4

Date: 2026-08-21. Session ended on instruction: "dump what's done / what's left — another session continues; no more building."

Canonical plan + design: **`docs/architecture/REMOTELESS_AI.md`** (read first). This file is the delta state since that doc.

Code is the source of truth. Re-grep every symbol/route before acting (AGENTS.md).

## Goal (recap)

Make any owned remote box usable/fixable from every surface (electron, mobile, tvOS, visionOS/AR, watchOS, Wear OS, Android TV, car, web) with **no coding-runner CLI and no stored key on the box where avoidable**, via three lanes:

- **Lane A — Remoteless AI runner** (hosted model, DeepSeek default): tasks/chat, deep-audit, fix-with-AI, connectivity/OAuth diagnosis, vibing.
- **Lane B — Fix-with-AI** wired to connectivity, OAuth, deep-audit, vibing failures.
- **Lane C — Client-local SSH** (per-surface secure storage; agent wraps real ssh).
- **Lane D — Yaver "HTTPS-SSH"**: JSON-RPC 2.0 repair channel over relay HTTP/WS, `repair`-scope token, pair-proven unauthenticated path.

**Surface-agnostic principle (load-bearing):** the agent HTTP surface is the single contract; clients are thin UI + local secure storage only.

## Committed + pushed (mine)

- `ecfc52c62` — **P0**: interim `remoteless` runner + aiFix default. **Pushed to origin/main.**
- `9ec167d3a` — doc update (REMOTELESS_AI.md status + tvOS handoff constraints). **Push was still in-flight at session end** — `git log origin/main -1` showed `ecfc52c62`; verify/`git push` again.

## What P0 did (all in `desktop/agent/`, all verified)

- `tasks.go`
  - `builtinRunners["remoteless"]` → `opencode run --dangerously-skip-permissions {prompt}` + `Model: "deepseek/deepseek-flash"`. The id is the **stable lane contract** (backend later swaps to an in-process Go loop without touching callers).
  - `supportedRunnerIDs` += `"remoteless"` (last — a working subscription binary still wins the default fallback).
  - `runnerModelCompatible` `case "opencode","remoteless"` (provider/model split).
  - `startProcess` model splice: `case "opencode","remoteless"` → `insertRunnerFlagAfter(args,"run","--model",...)`.
- `runner_auth.go`
  - `DetectRunnerRuntimeStatus` case `remoteless` → `detectRemotelessStatus(workDir)`.
  - `detectRemotelessStatus` = opencode binary present AND `remotelessCredentialSource(workDir) != ""`.
  - `remotelessCredentialSource` (pure): `DEEPSEEK_API_KEY` env/vault OR `opencode.json` `provider.deepseek` with key/baseURL.
  - `runnerCapabilityName` += `"remoteless"` → "Remoteless AI".
- `runner_preflight.go`: `runnerHasAuthModel("remoteless")=true`; `runnerReauthCommand("remoteless")` = "configure a DeepSeek API key…" (no CLI login exists — honest CTA, not a wall).
- `capability_gap.go`: `compileFailureGap` now iterates `preferRemotelessFirst(installedRunners)` — Fix-with-AI prefers the cheap hosted lane; `remotelessAIAvailable()`; pure `preferRemotelessFirstList(usable, installed)`.
- `yaver_agent_tools.go`: `runnerAuditOrder` += remoteless; `runnerLabel`.
- `yaver_agent_tools_test.go`: audit test now expects 4 runners.
- `remoteless_test.go` (new): credential source (env/vault/opencode.json/none), model compat, `preferRemotelessFirstList`, `GetRunnerConfig("remoteless")`, `DetectRunnerRuntimeStatus` no-key ⇒ Ready=false.

### Verification
- `go build -o /dev/null .` in `desktop/agent/` — green at the time (before the other session's WIP landed).
- Full P0 test set run in a **clean worktree at HEAD** (`/tmp/yaver-remoteless-verify`, since removed): `ok github.com/yaver-io/agent` — all remoteless + audit tests pass.

## Repo state at session end (IMPORTANT — concurrent-session WIP)

Another session was editing the repo concurrently (tvOS QR auth + remote-runtime viewer registry + vibe-studio). It was closed by the user, but its work is **uncommitted and currently does NOT compile**. Do not assume the tree is green.

**UPDATE (2026-08-21, second session): RESOLVED.** The follow-up session finished the viewer registry (deadlock fixed, 10 tests green, guard broken+restored), wired creator attribution through the POST handler, added tvOS rejoin-by-roster, fixed the stale docs, and landed the tablet Vibe Studio. The tree now builds: `go build ./...` green in `desktop/agent/`, viewer/creator/remoteless tests green, tvOS simulator build green, mobile `tsc` clean for the new files. All of it is committed in one change.

> Known pre-existing flake (NOT this change): `go test .` full main-package
> times out on `TestCustodianAbandonsAHangingWarden` in the full-suite context
> (passes in isolation; reproduced on clean HEAD via worktree).

`git log origin/main -1` at end = `f9ccfb262` (handoff commit).

## Related handoff you must read

`docs/handoffs/tvos-qr-auth-audit-2026-08-21.md` — TV-scoped companion tokens are denied `POST /tasks` (`auth.session.scope_denied`) on deployed agents because allowlist fix `6a70b7e3f` (`tvTaskMutationAllowed`) is unreleased. Rules folded into REMOTELESS_AI.md:
- A `auth.session.scope_denied` verdict must render "Update agent", NEVER Retry/Fix-with-AI.
- Remoteless `POST /tasks` under a TV-scoped token hits the same gate; Lane D's `repair`-scope token must be the TV repair path and independent of released allowlist state.
- Companion-scope parity tests must validate **method + path**.

## What's left (from REMOTELESS_AI.md, unstarted)

- **P1** — Fix-with-AI → connectivity/OAuth/deep-audit/vibing wiring:
  - Extend `AIFix` beyond compile-failure (`capability_gap.go`) to `reload.dev_server_unavailable`, vibe-capture-stuck, `browser_window.chrome_missing`, `capability.insufficient_disk`, `connectivity.relay.pin_stale`, runner re-login.
  - New `POST /repair/ai-fix` / `/ops` verb: structured diagnosis (machine_doctor findings, remote_repair plan, reason codes, `/dev/events` replay, `/streams/<name>` tail) → remoteless task → named route-to-fix or bounded repair. Auto for allowlisted deterministic repairs; human confirm for auth/OAuth/push.
  - Remoteless-driven device-code OAuth orchestration (codex/git/opencode); claude stays honest route.
  - Vibing render-failure path: inspect logs → deterministic restart or remoteless code-fix task; honour render/reload contract (render only on completed/review, coalesce, keep-last-good).
- **P2** — Client-local SSH lane: per-surface secure stores (`yaver.secure.ssh_<boxId>`; tvOS Keychain store; web localStorage w/warning); `/ops` repair verbs accept in-memory `sshOverride {host,user,port,identityKey}`; new `ssh` ops verb (thin ssh wrapper); managed-key install via `/auth/ssh/authorized-keys`.
- **P3** — JSON-RPC 2.0 repair channel: `POST /repair/rpc` + `WS /ws/repair` over relay `/d/<device>`; method allowlist (seed `ssh_session_cmd.go:46-74` + `remoteRepairCommand` + vibing verbs); `repair`-scope token (SDK-token mint shape, owner-only, agent-side scope enforcement via `scopePathPrefixes`); pair-proven unauthenticated path; audit ledger `GET /repair/audit`.
- **P4** — In-process Go loop (generalize `glm_loop.go` `RunGLMLoop`; `RunnerConfig.Kind`; `startProcess` in-process branch emitting same SSE lanes); caller-executed lane (key never on box); surface parity + tests + docs.

Key seams to reuse (already mapped): `glm_loop.go:115`; `opencode_config.go` provider upsert; `provider_keys.go` runner-provider lane; `remote_box_repair_plan.go` + `ops_remote_repair.go:106-137` allowlist; `auth_pair.go:61-89`; `auth.go:402-439` SDK-token mint; `httpserver.go:1775-1795` scope→path prefixes; `ssh_session_cmd.go:46-74`; `ssh_targets.go:171-194`.

## Guidance for the next session

- Fix or set aside the concurrent WIP first so the package builds; a clean **worktree at HEAD** (`git worktree add <dir> HEAD`) is the safe way to verify without touching uncommitted files.
- Run: `cd desktop/agent && go test -count=1 -run 'Remoteless|PreferRemotelessFirst|HandleYaverAgentDeviceAudit' .` (build is slow on this 8 GB box — allow minutes; run in background).
- Do not weaken: relay pass-through + same-owner, no keys in Convex, agent-side scope enforcement, pair-proof-only for unauthenticated boxes, sandbox for arbitrary input + allowlist for curated repairs, audit every mutation.
