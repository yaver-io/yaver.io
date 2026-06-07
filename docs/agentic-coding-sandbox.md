# Agentic coding in the Mobile Sandbox — opencode behavior, Hermes-native, GLM-first

> Design + first slice. Code is the source of truth; when this drifts, fix it in
> the same change (CLAUDE.md). Sibling to `docs/coding-agent-on-device.md` (which
> covers running the *real* claude/codex/opencode binary on-device). This doc
> covers the **Hermes-native agentic loop** — the thing you run when you can't or
> won't ship a binary, defaulting to a cheap BYO GLM key.
>
> Status 2026-06-08: first slice landed (uncommitted) — the coding tool registry
> + the provider-agnostic agentic loop + 19 tsx tests. Not yet wired into the
> sandbox UI. See "What's built" / "What remains".

## The ask, restated

"Port opencode / agentic stuff into Yaver in TS to use a GLM API key for Mobile
Sandbox coding."

The naive read is "vendor the opencode npm package." That doesn't work and isn't
what you want. opencode is TypeScript on **Bun**, client-server, with
**Drizzle/SQLite** persistence, **`bash`/child-process** tools, and **LSP**
subprocesses ([DeepWiki: sst/opencode](https://deepwiki.com/sst/opencode),
[Agent System](https://deepwiki.com/sst/opencode/3.2-agent-system)). None of
Bun, child_process, or SQLite-via-Bun exists inside **Hermes** (the RN JS
engine), and iOS can't exec a downloaded binary or JIT at all.

What's actually valuable in opencode is its **behavior**: the iterative
`list → grep → read → edit → (run) → observe → repeat` loop, anchored edits, and
a permission gate. That behavior is a system prompt + ~6 small tool functions +
a tool-use loop — not a dependency. We already had **both halves** of it in the
repo; this work fuses them.

## The two loops we already had

| | Sandbox coding path | Control-plane path |
|---|---|---|
| Files | `codingBackend.ts`, `llm{OpenAI,Anthropic,ClaudeSubscription,Local}.ts`, `codingBackendStore.ts` | `yaverAgentRunner.ts`, `yaverAgentTools.ts` |
| Shape | **single-shot** `editFiles(req) → EditPlan` | **multi-step** tool-use loop (`maxSteps`, feeds `tool_result` back) |
| Providers | Anthropic key / Claude subscription / OpenAI / **GLM** / on-device | Anthropic / **GLM** / OpenAI / OpenRouter |
| Tools | one forced `apply_edits` | `device.list/resolve/audit/next_step` |
| Context strategy | **inlines the whole `src/` tree** (200 KB cap) every call | model pulls only what it asks for |
| Self-verifies | ❌ | n/a |
| Coding | yes (but dumb) | **explicitly forbidden** in the system prompt |

The single-shot path is the weak link: it crams the entire tree into the prompt
(burns GLM input tokens, dies past a few files), can't read selectively, can't
run `tsc`/tests, and can't recover from its own mistakes. The control-plane loop
already has the *machinery* we want but refuses to code.

**The port = take the loop, swap the control-plane tools for coding tools bound
to the phone sandbox, default it to GLM.** That's it. ~70% recombination.

## Architecture

```
sandbox-ai.tsx  (UI: "Agent" mode alongside the existing "Quick edit")
      │  prompt + slug + confirmMutation hook (diff preview / yolo)
      ▼
runCodingAgent(opts)                         mobile/src/lib/codingAgent/runner.ts
  ├─ provider transport (GLM/OpenAI/OpenRouter = OpenAI-compatible; Anthropic native)
  ├─ tool-use loop: model → tool_calls → execTool → results fed back → repeat
  └─ execTool: gate mutations (confirmMutation) → dispatchCodingTool → record path
      ▼
CODING_TOOLS dispatcher                      mobile/src/lib/codingAgent/sandboxTools.ts
  list_files · read_file · grep · write_file · edit_file · delete_file
      ▼
CodingSandbox (slug-scoped capability)       mobile/src/lib/codingAgent/sandboxBinding.ts
      ▼
phoneSandboxSource  (path safety + atomic writes + traversal rejection — unchanged)
      ▼
<doc>/phone-projects/<slug>/src/             the phone-local tree
```

Everything below the UI is **pure / injectable** so it's tsx-tested end-to-end
without expo or a network: the loop takes a `fetchImpl`, the tools take an
in-memory `CodingSandbox`.

## The tools (`sandboxTools.ts`)

Provider-agnostic `CodingTool` (same shape as `yaverAgentTools.YaverAgentTool`:
`name` + `description` + JSON-Schema `parameters` + `invoke`), plus one extra
field — `mutating: boolean` — so the runner knows which calls to gate.

| Tool | Mutating | Why it matters vs single-shot |
|---|---|---|
| `list_files(glob?)` | no | tree + sizes, **no contents** — cheap discovery; replaces "inline everything" |
| `read_file(path)` | no | read **only what's needed**; truncates >60 KB and flags it |
| `grep(pattern, glob?, flags?)` | no | JS-regex content search (no ripgrep binary needed); find before edit |
| `write_file(path, content)` | yes | create / full overwrite |
| `edit_file(path, old, new, replaceAll?)` | yes | **anchored replace** — refuses if `old` is missing or non-unique; kills the "model truncated my file" failure mode |
| `delete_file(path)` | yes | idempotent; reports prior existence |

Safety: paths never bypass `phoneSandboxSource` — traversal/absolute/`..`
rejection and atomic writes stay there. The `CodingSandbox` binding closes over
the slug, so a tool arg can **never** name another project.

Caps that protect the context window: `READ_MAX_BYTES=60_000`,
`GREP_MAX_MATCHES=80`, `LIST_MAX_FILES=400`, `GREP_MAX_FILE_BYTES=400_000`.
Every cap is flagged in the result (`truncated: true`) so the model can narrow.

## The loop (`runner.ts`)

A fork of `yaverAgentRunner.ts`'s tool-use loop, generic over a tool registry:

- **Two transports**, identical behavior: OpenAI-compatible (`/chat/completions`,
  Bearer) covers **GLM / OpenAI / OpenRouter**; Anthropic native (`/messages`,
  `x-api-key`, `tool_use`/`tool_result`) covers Claude.
- **GLM robustness**: the loop continues whenever `tool_calls` are present,
  **ignoring `finish_reason`** (GLM sometimes returns tool calls with
  `finish_reason: "stop"`). It ends only when a turn has no tool calls.
- **Mutation gate** (`execTool`): before any `mutating` tool runs, call
  `confirmMutation({name, args})`. Return `false` and the model is told "user
  rejected this change" and adapts. Omit the hook for **yolo** mode (matches the
  always-dangerous runner preference). `mutatedPaths` only records calls that
  actually applied (`result.ok`), so a rejected anchor doesn't count.
- **Result accounting**: `{ finalText, toolCalls, mutatedPaths, steps,
  inputTokens, outputTokens, hitMaxSteps }`. `maxSteps` defaults to 16 (coding
  needs more rounds than the 6-step control plane); `timeoutMs` 90 s per turn.

### Default config — GLM first

```ts
defaultCodingAgentConfig(key) // → { provider:"glm", model:"glm-4.6",
                              //     baseUrl:"https://api.z.ai/api/paas/v4", apiKey:key }
```

`sandboxBinding.loadGlmCodingConfig()` reads the **same** `LOCAL_KEYS.glmApiKey`
slot the single-shot GLM backend uses — one key powers both paths.

## Why GLM + agentic beats single-shot (the "optimization")

- **Cost** — single-shot inlines the whole tree every call; agentic
  `read_file`/`grep` pull only what's needed → typically **5–20× fewer input
  tokens** on multi-file projects. On GLM-4.6 (already ~7–10× cheaper than Claude
  per our own BYO-cheap moat), that compounds into the genuinely-cheap standalone
  story.
- **Correctness** — `edit_file` anchored replace instead of full-file
  `apply_edits` content blobs eliminates truncation/rewrite damage.
- **Scale** — single-shot dies at the 200 KB request cap; agentic never holds the
  whole tree, so it scales to real projects.
- **Self-repair** — the loop can (once `run` lands) `tsc`/test on a paired box
  and fix its own errors; single-shot can't.

## Per-surface strategy

| Surface | Agentic coding | Rationale |
|---|---|---|
| **iOS standalone** | this Hermes loop, **GLM default** | only option — no exec, no JIT, no Node. This is the upgrade to Tier 2 in `coding-agent-on-device.md`. |
| **Android standalone** | this Hermes loop **or** the real `opencode` binary under proot (`sandbox_proot.go`) | Hermes loop ships now, zero rootfs download, cross-platform; proot is the full-fidelity (bash/LSP) option for power users. |
| **Any phone + paired box** | real `claude`/`codex`/`opencode` over `/ws/terminal` (`agentLaunch.ts`) | best fidelity when a machine is reachable. |

The selector is `localAgent/brain.ts`'s remote-first policy: box reachable →
remote runner; else → this Hermes loop on GLM. The future `run` tool routes
compile/test to a box when one appears, else returns "needs a machine" — same
brain, one policy function, rest of the app oblivious.

## Privacy

The loop is **phone ↔ provider only**. Sandbox file contents, paths, and prompts
**never** touch Convex (enforced by `desktop/agent/convex_privacy_test.go`, which
forbids `output`/`path`/file contents and scans for `/Users/` path leaks). The
GLM key lives in `expo-secure-store`, same as every other BYO key.

## Architecture rule that makes it portable (learned the hard way)

The **pure agent core has zero native imports** — `runner.ts` and `sandboxTools.ts`
import nothing from `react-native`/`expo-*`. Each *surface* supplies its own
`CodingSandbox` (file capability) and its own config source:

| Surface | CodingSandbox binding | Config |
|---|---|---|
| RN app | `sandboxBinding.ts` → `phoneSandboxSourceDefault` (expo-file-system) | `loadGlmCodingConfig` (SecureStore) |
| **yaver mobile headless** | node-fs adapter in the `sandbox-agent` CLI verb, pointed at the shim path | `--glm-key` / `$GLM_API_KEY` |

Why the split is mandatory, not cosmetic: the headless harness shares
`mobile/src/lib/*` via tsconfig `paths`, but **those aliases do NOT reach bare
specifiers imported from inside `../mobile/src/lib/*`** (a Bun/tsx resolution
limit — the harness's `preload.ts` documents giving up on this). So the moment a
reused module transitively imports `expo-file-system` or `react-native`, Bun
loads the *real* package and dies on React Native's Flow syntax
(`import typeof …`). `sandboxBinding.ts` is therefore RN-only and intentionally
NOT bun-importable; the headless surface binds its own node-fs `CodingSandbox`.
The agent core stays shared and pure.

## What's built (this slice, uncommitted)

| File | Purpose | Tests |
|---|---|---|
| `mobile/src/lib/codingAgent/sandboxTools.ts` | 6-tool coding registry + dispatcher + glob | `sandboxTools.test.mts` (10) |
| `mobile/src/lib/codingAgent/runner.ts` | provider-agnostic agentic loop (GLM/OpenAI/OpenRouter + Anthropic), mutation gate, **GLM Coding-Plan default endpoint** | `runner.test.mts` (9) |
| `mobile/src/lib/codingAgent/sandboxBinding.ts` | RN wiring: `sandboxForSlug` + `loadGlmCodingConfig` (reuses the single-shot GLM key slot). RN-only. | — (expo-coupled) |
| `mobile/src/lib/codingAgent/sandboxGit.ts` | sandbox VCS on isomorphic-git: init/commit/status/log + before/after checkpoints + **blob-based revert** (works around isomorphic-git's force-checkout-doesn't-overwrite-modified-files bug) | `sandboxGit.test.mts` (6) |
| `mobile/src/lib/codingAgent/sandboxGitOps.ts` | FULL git: branches, merge + conflict markers/listing/resolution, file diffs, remotes, clone/fetch/push/pull with per-host PAT auth | `sandboxGitOps.test.mts` (8) |
| `mobile/src/lib/codingAgent/gitFsExpo.ts` | on-device fs adapter: isomorphic-git over expo-file-system (base64⇄bytes, ENOENT/EEXIST codes); `gitForSlug` binding | `gitFsExpo.test.mts` (3, real isomorphic-git round-trip) |
| `mobile/src/lib/codingAgent/gitTools.ts` | git as agentic tools (status/diff/log/commit/branch/merge/conflict; push when creds wired) → plug into `runCodingAgent({tools})` | `gitTools.test.mts` (4) |
| `mobile/src/components/SandboxAiPanel.tsx` | **Quick edit / Agent** mode toggle; Agent runs the loop with file+git tools, auto-checkpoints each run, live tool trace, **Revert this run** | (RN; project tsc clean) |
| `mobile-headless/src/bin/cli.ts` | `sandbox-agent` verb — runs the loop headless against a phone-project tree | proven live (below) |

Note: `sandboxGit*` / `gitFsExpo` / `gitTools` were built in a parallel session;
`sandboxGitOps.ts` superseded an earlier `sandboxGitRemote.ts` (removed). Full
`mobile` project `tsc --noEmit` is clean.

Run unit suites: `cd mobile && for t in sandboxTools runner sandboxGit sandboxGitRemote; do npx tsx src/lib/codingAgent/$t.test.mts; done` (30 pass).

## Headless validation (proven live, 2026-06-08)

All three legs verified end-to-end with a real GLM Coding-Plan key:

1. **Agentic loop on live GLM** — `live.smoke.mts` (skips without `GLM_LIVE_KEY`):
   `list_files → read ×2 → edit ×2`, 4 steps, ~10.5 s, 5.8 k in / 359 out tokens,
   both files correctly edited. Caught the endpoint bug: a Coding-Plan key 429s on
   `paas/v4`; default is now `…/api/coding/paas/v4`.
2. **Remote git on live GitHub** — `sandboxGitRemote.live.smoke.mts` shallow-clones
   `octocat/Hello-World` in ~0.8 s via isomorphic-git's Node http client (RN uses
   `/http/web`; identical protocol/auth code).
3. **Full headless flow** — `mobile-headless`:
   ```bash
   GLM_API_KEY=<key> bun run src/bin/cli.ts --data-dir=$D sandbox-agent \
     --slug=notes-app --prompt="rename title to 'My Notes' and add a subtitle …"
   ```
   drove GLM through the real `runner`+`tools`, edited the on-disk tree, and emitted
   the run JSON. tsc clean.

## What remains

1. ~~Wire into the editor panel~~ — **done** (`SandboxAiPanel.tsx` Agent mode + auto-checkpoint + revert).
2. ~~On-device git~~ — **done** (`gitFsExpo.ts` + `gitForSlug`).
3. **`run` tool → remote box** — when `brain.ts` says a box is reachable, expose
   `run(cmd)` that proxies `tsc`/test to it over the existing runner transport;
   else omit the tool. Unlocks self-repair.
4. **Push creds in the UI** — `makeGitTools` enables `git_push` only when a
   `net` (http + onAuth) is passed; wire a PAT entry so the agent can push to
   GitHub/GitLab from the phone (auth shapes already in `sandboxGitOps`).
5. **Persistence + streaming** — append-only message log so a backgrounded loop
   resumes; SSE so it feels responsive.
6. **Android proot fidelity** — flip the same UI to the real `opencode` binary
   per `coding-agent-on-device.md` when the rootfs is installed.
7. **(Optional) `glm-subscription`** — GLM's Coding Plan also exposes an
   Anthropic-compatible endpoint; mirror `claudeSubscription.ts` for a
   zero-marginal-cost GLM lane.

## Risks / gotchas

| Risk | Mitigation |
|---|---|
| GLM returns tool JSON in `content`, or `finish_reason≠tool_calls` | loop decides on tool-call presence, not finish_reason; single-shot path already handles content-embedded JSON (`extractJsonObject`) |
| Tool output blows the context window | per-tool caps, all flagged `truncated` |
| Runaway loop | `maxSteps` (16) + `hitMaxSteps` surfaced to UI |
| iOS background suspension on long runs | persist after every tool result (item 3) |
| A mutating tool slips past the gate | single `execTool` chokepoint; both transports funnel through it; tested |
| Path traversal from a malicious model | never bypasses `phoneSandboxSource`; slug closed over in the binding |

Sources: [opencode docs](https://opencode.ai/docs/),
[sst/opencode DeepWiki](https://deepwiki.com/sst/opencode),
[Agent System](https://deepwiki.com/sst/opencode/3.2-agent-system),
[github.com/sst/opencode](https://github.com/sst/opencode).
