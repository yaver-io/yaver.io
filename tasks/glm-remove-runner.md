---
doer: codex
---

<!-- Single seat, deliberately. claude is not authed on the mini, and seats in
     front matter are binding — naming an unauthed master fails the run at
     iteration 1 rather than falling back. codex runs this solo. -->


# Retire GLM as a runner — it must never spawn the `claude` binary

## The decision (settled — do not relitigate)

`builtinRunners["glm"]` sets `Command: "claude"`. That takes the
**subscription-OAuth** CLI and drives it with a **z.ai API key**. The runner-auth
split says the opposite in `mcp_tools.go:1931`: *"Claude Code and Codex use
subscription OAuth… Provider credentials here are for OpenCode/GLM only."*
The `glm` runner crosses that boundary with a single `Command:` field.

It also fails in a way that lies. `claude -p` on a box whose TUI is signed in
reports `"OAuth session expired and could not be refreshed"`. A real run died
that way today on the mini (`toolchain-and-remote-git:opencode`, master `glm`) —
an Anthropic auth error it never actually had.

**GLM stays available. It moves to the runner built for API keys.** opencode
already ships `zai-coding-plan` as a built-in provider serving
`zai-coding-plan/glm-4.7` (`opencode_config.go:528`). That path exists and works
today — this task deletes the non-compliant duplicate, it does not remove GLM.

After this task: `supportedRunnerIDs = {claude, codex, opencode}`.

## The one distinction that governs every edit

GLM appears on **four** axes. Only the first is being removed:

1. **GLM as a RUNNER** (`Command: "claude"`) → **DELETE**
2. **GLM as a MODEL** (`glm-4.7`, `zai-coding-plan/glm-4.7`) → **KEEP** (opencode serves it)
3. **GLM as an opencode PROVIDER** (`zai-coding-plan`, `zai`, Zhipu/bigmodel) → **KEEP**
4. **GLM as the yaver-agent control-plane PROVIDER** (`yaver_agent_config.go:47`
   `yaverAgentProviders = ["glm","anthropic","openai","openrouter"]`) → **KEEP** —
   different axis entirely, not a coding runner.

If you cannot tell which axis a reference is on, it is axis 2/3/4. Leave it.

## HAZARD — read before touching tasks.go

`GetRunnerConfig` (`tasks.go:236`) falls back to `defaultRunner` for anything
unknown. So deleting `builtinRunners["glm"]` **does not** make `glm` requests
fail — it makes them **silently run claude**. Two callers depend on this and
would break invisibly, re-creating claude-against-z.ai with the evidence gone:

- **`sandbox_remote.go`** (468 lines) — "GLM-only by design"; `:307`
  `rc := GetRunnerConfig("glm")`, `:341` `runnerProviderEnv("glm")` (sets
  `ANTHROPIC_BASE_URL` to z.ai), `:443` rejects any non-glm runner. This is the
  mobile sandbox → remote GLM feature. It MUST be migrated to opencode +
  `zai-coding-plan/glm-4.7`, or fail loudly. Do not let it fall through.
- **`glm_loop.go`** (265 lines) — `:262` returns `"glm"`.

**Therefore: add the loud failure BEFORE deleting the builtin.** A retired id
must produce an error naming the replacement — house law is *visible failure over
silent retry* (`feedback_visible_failure_over_silent_retry`). Suggested shape
(already drafted and reverted once; re-derive it properly):

```go
var retiredRunners = map[string]string{
    "glm": "the `glm` runner ran the `claude` binary against z.ai, which mixes " +
        "subscription-OAuth tooling with an API key. Use runner `opencode` with " +
        "model `zai-coding-plan/glm-4.7` instead (opencode is the API-key runner).",
}
func retiredRunnerReason(id string) (string, bool) // normalizeRunnerID(id) first
```
Callers resolving a user-supplied runner check this **before** `GetRunnerConfig`.

## Go sites (desktop/agent) — verified, but re-grep; do not trust this list

- `tasks.go` — `builtinRunners["glm"]` (~:219), `supportedRunnerIDs` (:267),
  `exitCommands["glm"]` (:132), `runnerModelCompatible` `case "glm"` (:311),
  `case "claude","glm"` (:502), `:2464`, `:3034`, `:3773`.
  Note `runnerModelCompatible`'s `opencode` case already returns `true` for any
  provider-prefixed model, so `zai-coding-plan/glm-4.7` passes with no change;
  and `claude` correctly *rejects* `glm-*` once glm is gone. Both are desirable.
- `autorun.go:474-480` — `autorunRunsClaudeBinary()` returns true for claude AND
  glm. Once glm is gone this is claude-only. **Also fix `autorun.go:334`**, which
  hand-rolls `normalizeRunnerID(runner.RunnerID) == "claude"` instead of calling
  this helper — that omission is why glm never got the tmux force and hit `-p`.
- `runner_auth.go` — `detectGLMStatus()` (:594-602), `:147`, `:313`, `:333`,
  `:880`, `:1034`. **`normalizeRunnerID` (:384) maps `zai|z.ai|z-ai|glm-4.6|
  glm-4.7 → "glm"`** — decide deliberately: those aliases should now resolve to
  the retired-runner error, NOT to a live runner.
- `agent_mesh.go` — runner preference lists (:474-497, :527-547, :577, :617).
- `runner_pty.go` (:92, :117, :199), `runner_pty_cmd.go` (:491, :876, :889, :947),
  `runner_auth_setup.go` (:128, :195, :209, :237), `runner_auth_cmd.go` (:136,
  :229 `{ID:"glm", Cmd:"claude"}`, :453-455), `runner_preflight.go` (:63, :76),
  `runner_signature.go:39`, `provider_keys.go` (:125, :143),
  `agent_runner_resume.go:37`, `env_profile.go:282`, `code_control.go:2167-2169`,
  `console_machines.go:212`, `main.go` (:484, :8914), `httpserver.go` (:3488-3495,
  :3540, :3610), `ops_runner_auth.go:41`, `mcp_tools.go` (:1931-1932 enums).

## web/ + mobile/ — DONE ONCE, THEN LOST. Redo from this exact list.

Both surfaces were completed and typechecked clean (`npx tsc --noEmit` exit 0 on
each; `boxInit.test.mts` 8/8), then a parallel session wiped the uncommitted
working tree before they could be committed. The edits below are verified — they
compiled and passed. Re-apply them; do not re-derive.

### web/ (typechecked exit 0)
- `web/components/dashboard/DevicesView.tsx`
  - `~2216-2218` drop `glm: "glm-4.7"` from `DEFAULT_MODEL_BY_RUNNER` + its comment.
  - `~2285` `RUNNER_WHITELIST = ["claude","codex","opencode"]`. **This is the
    chokepoint** — `app/dashboard/page.tsx:945,952` filter live runners through
    `RUNNER_WHITELIST_SET`, so a `glm` reported by an older agent drops out of
    every web picker automatically.
  - `~5172` remove the `runner === "glm" ? "GLM (z.ai)"` branch in `runnerLabel`.
- `web/lib/yaver-apps.ts:129,264` — union + array literal → `claude|codex|opencode|custom-tmux`.
- `backend/convex/schema.ts:1414` — comment only (`id` is `v.string()`, no validator).
- **Convex needs nothing else.** `aiRunners.ts` `PREDEFINED_RUNNERS` never
  contained glm. Every other Convex hit is GLM-as-model/provider — keep
  (`cloudMachines.ts:125-126` `zai-coding-plan/glm-4.7` + its tests,
  `schema.ts:896` provider hint, `managedMeter.ts`, `plans.ts:145`,
  `openrouterKeys.ts`).
- **Leave alone (axis 3/4):** `agent-client.ts:666` `YaverAgentProviderId`,
  `YaverAgentSettings.tsx:23,30,44`, `ToolsView.tsx:1241`, `DevicesView.tsx:2356`
  (opencode.json provider keys for Zhipu/bigmodel), `RunnerAuthSetParams`
  `glmApiKey`/`zaiApiKey`. Marketing prose positioning GLM via OpenCode is the
  surviving story — it reads correctly as-is.

### mobile/ (typechecked exit 0)
- `src/lib/boxInit.ts` — L24 `RunnerKind` → `claude|codex|opencode`; L75 drop
  `"setup_glm"` from `BoxActionId`; L79 drop `"glm"` from `CheckKey`; L114-117
  delete `isGLM()`; L172-184 delete `glmCheck()`; L238 remove from `checks[]`;
  L248 `canCode` stops counting glm; L11/L230 comments.
- `src/lib/boxInit.test.mts` — L40-42, L63-65, L103-104, L131-132.
- `src/lib/boxInitStore.ts` — L150-159 delete `case "setup_glm"`. **Keep the
  `glmApiKey` param** — `setup_opencode` uses it; that is the sanctioned path.
- `src/context/DeviceContext.tsx` — L196-199 `DEFAULT_MODEL_BY_RUNNER` glm entry;
  L202 `runnerIds` Set.
- `src/components/RunnerAuthModal.tsx:56` — drop the `"GLM (z.ai)"` label.
- `src/lib/deviceStatus.ts:163` — `codingReady()` id list.
- `src/lib/remoteCodingSelection.ts` — delete `HETZNER_GLM_MODEL` (dead once the
  runner is gone); **keep `HETZNER_OPENCODE_MODEL = "zai-coding-plan/glm-4.7"`**;
  L64 label, L96 `preferredDefaultRunnerForDevice`, L110/L116
  `preferredDefaultModelForRunner`.
- `src/lib/quic.ts:881,937` — `RunnerAuthSetupParams`/`RunnerAuthSetParams`
  unions. Keep `glmApiKey`/`zaiApiKey` (opencode provider credentials).
- `src/lib/managedCloudFlow.ts:73` — runner union.
- `app/(tabs)/tasks.tsx:1865` — `RUNNER_WL` Set (the runner-picker seed); L816-818 comment.
- `src/components/DeviceDetailsModal.tsx` — L1520 `nid === "opencode" || nid ===
  "glm"` → opencode only; L1532 "Set up →" condition; L1024 comment.
- `src/components/RemoteBoxPickerModal.tsx:1012` — comment only.
- **Leave alone (axis 2/3):** all phone-side BYO z.ai code (`codingBackend.ts`
  `"glm"` backend, `llmOpenAI.ts`, `phoneProjects.ts`, `sandbox-ai.tsx`,
  `settings.tsx`, `YaverAgentSettings.tsx`, `codingAgent/runner.ts`) — these hit
  `api.z.ai` directly with an API key and never touch the claude binary;
  `OpenCodeConfigModal.tsx` (`zai-coding-plan/glm-4.7` preset);
  `YaverAgentTasksHint.tsx:46`; `agentSlots.test.ts` `"gamma:glm"` (arbitrary
  `<name>:<runner>` slot-key fixtures, cosmetic only).
- `app/remote-runtime.tsx` — verified ZERO glm refs; do not touch.

### The mobile↔Go coupling that must be resolved together
`src/lib/llmRemote.ts:83` and `src/lib/quic.ts:9380` send `runner: "glm"` to
`POST /sandbox/run`. The Go handler is glm-only by design
(`sandbox_remote.go:443` rejects any other id). Retargeting mobile to `opencode`
alone would 400 against today's agent; deleting it alone would remove the whole
"Remote runner (GLM)" sandbox backend (`codingBackend.ts`, `codingSession.ts`,
`startCoding.ts` + 4 test files). **Decide the Go `/sandbox/run` contract first,
then make mobile follow in the same change.**

## Known follow-up this task must resolve

`web/lib/agent-client.ts:2538` and `VibeCodingView.tsx:267-269,1890` describe
`hybridDegree` duo/trio routing as *"Claude Code + GLM"* / *"Claude Code + Codex
+ GLM"*, including a user-visible tooltip. The routing is implemented in Go. Once
the glm runner is gone that copy is a lie. Fix the Go lane first, then make the
copy match what actually runs. Do not rewrite the label alone.

## Gate

```
cd desktop/agent && go build ./... && go test -count=1 -run 'TestRunner|TestLoadRunners|TestAutorun|TestTasks|TestGLM|TestSandbox' .
```

**NEVER run a bare `go test ./...` in `desktop/agent`** — `TestAuthLogout` hits
the real `~/.yaver` and signs the box out. Always scope with `-run`.

## Done means

- No code path can spawn the `claude` binary for a GLM request. Grep proof:
  no `Command: "claude"` reachable from any glm/zai id.
- `supportedRunnerIDs = {claude, codex, opencode}`.
- Asking for `glm`/`zai` yields the retirement error naming
  `opencode` + `zai-coding-plan/glm-4.7` — never a silent claude.
- `sandbox_remote.go` either runs GLM via opencode or fails loudly. It does NOT
  fall through to `defaultRunner`.
- GLM still reachable end-to-end via opencode + `zai-coding-plan/glm-4.7`.
- Axes 2, 3, 4 above are untouched.
