# Phone-as-dev-environment — deep audit (2026-06-08)

> Honest state-of-the-world for "use the phone as a development environment":
> Mobile Sandbox, git, tasks, Hermes projects, and the XReal + keyboard + tmux
> workstation UX. Code is the source of truth; this is a map, not a promise.
> Verdict up top, evidence below.

## Verdict

| Capability | State | One-line truth |
|---|---|---|
| Sandbox single-shot edit (ask AI → preview → apply) | ✅ shipped | `SandboxAiPanel` → `resolveActiveBackend` → `provider.editFiles` → `applyEditPlan`. All backends incl. **GLM** work. |
| Sandbox agentic loop (read→grep→edit, cheaper) | ⚠️ built, unwired | `codingAgent/{runner,sandboxTools}.ts` pure + tested, but **no UI calls `runCodingAgent()`**. |
| Git on the phone | ✅ **unblocked 2026-06-08** | `gitFsExpo.ts` written + proven: real isomorphic-git `init→commit→log→revert` round-trips over it (`gitFsExpo.test.mts`). Still needs UI wiring + a binding that passes `gitDirForSlug(slug)` + `createExpoGitFs()`. |
| Full git lib (branch/merge/conflict/diff) | ✅ **built 2026-06-08** | `sandboxGitOps.ts` — branches, merge w/ conflict markers, conflict list/resolve/complete (2-parent merge commit), line diff, remotes, push/pull/clone (injected http+onAuth). Tested through real isomorphic-git. |
| Git as agentic tools | ✅ **built 2026-06-08** | `gitTools.ts` — `makeGitTools(git, net)` → CodingTool[] (git_status/diff/log/commit/branch/merge/list_conflicts/resolve_conflict/complete_merge/remote/push). The coding loop can do git itself. |
| Phone → GitHub commit/push | ✅ **wired 2026-06-08** | `githubAuth.ts`/`githubAuthStore.ts` (token storage + onAuth, tested 6) + the git panel's push row (token input → `addRemote(normalizeRepoUrl)` → `push` over `isomorphic-git/http/web`). End-to-end from the editor. |
| Git panel in the editor | ✅ **built + mounted 2026-06-08** | `SandboxGitPanel.tsx` (status/commit, branches, merge + keep-mine/keep-theirs conflict resolver, history, push) mounted in `phone-project/code/[slug].tsx`. View-model `gitPanelModel.ts` tested (4). |
| `codingSession`/`codingExecution` (engine×target) | ⚠️ built, unwired | The new policy spine exists + tested but **the sandbox screen doesn't call it yet**. |
| Run/preview the phone-local app | ❌ edit-only | "Run" screen is a SQLite data browser. No Hermes eval / web preview. Build needs a machine (Android proot or a box). |
| Remote tasks (Claude/Codex/opencode on a box) | ✅ shipped | `tasks.tsx` → `quicClient.sendTask` → `/tasks`, streaming. No auto-commit. |
| Hermes / phone-backend projects (SQLite app) | ✅ shipped | `apps.tsx` + `phone_backend*.go`, 5-step wizard, promotable. |
| Unified coding entry point | ⚠️ **brain built 2026-06-08** | `startCoding.ts` (`routeCoding`) collapses the 3 surfaces into one decision via `codingSession`; tested (10). Remaining: a thin "Start coding" entry component + navigation. |
| Agentic loop + git in the sandbox | ✅ **wired 2026-06-08** | `codingAgentRun.ts` runs the loop with the git tools available AND brackets it in before/after git checkpoints (`runWithCheckpoints`) for one-tap revert. |
| Hardware keyboard capture | ✅ shipped | iOS `GCKeyboard` + Android `dispatchKeyEvent` + `keyboardRouter.ts` mux. ~95%. |
| tmux on mobile | ❌ unwired | Agent exposes `/tmux/{sessions,adopt,input}`; **mobile never calls them**. `glass-workspace` multi-pane is cosmetic — the shell pane is read-only. |
| External display / XReal | ❌ mobile-zero | Web `/spatial` detects `android-trio`; the **mobile app has no Presentation/DisplayPort code**. Glasses = screen mirror only. |
| Multi-session terminal | ❌ one PTY | `shell.tsx` is a single PTY. No tabs, no pane input, font hardcoded 13pt. |

Net: **the phone-as-workstation story is ~50% real.** The Mobile Sandbox edit path
and remote tasks are solid; the agentic loop, git, the unifying session policy,
and the entire keyboard/tmux/glasses workstation layer are built-in-pieces but
not wired into a working whole.

---

## 1. Mobile Sandbox (code a phone-local project)

**Works:** `mobile/src/components/SandboxAiPanel.tsx` drives the single-shot path:
gather `src/` as `FileSnapshot[]` → `provider.editFiles(req)` → `EditPlan`
(rationale + edits) → preview → `applyEditPlan(slug, plan, {writeSourceFile,
deleteSourceFile})`. Path safety in `phoneSandboxSource.ts` (no `..`/abs/NUL,
atomic `.tmp`→rename, UTF-8 only). Backends in `codingBackend.ts`:
`local` (GGUF/llama.rn), `subscription` (mirrored plan OAuth), `anthropic`,
`openai`, **`glm`** — GLM is an OpenAI-flavor (`https://api.z.ai/.../v4`,
`glm-4.6`); the agentic runner uses the cheaper coding endpoint. **GLM is fully
wired and is the intended cheap default.**

**Built but unwired:** `codingAgent/runner.ts` is the iterative loop
(`list_files/read_file/grep/write_file/edit_file/delete_file`, mutation gated by
a `confirmMutation` hook). Pure + tsx-tested. **Nothing calls `runCodingAgent()`
from the UI.** `codingSession.ts`/`codingExecution.ts` (the engine×target policy
just built) are likewise not yet consulted by `sandbox-ai`.

**The git blocker (concrete):** `codingAgent/sandboxGit.ts` implements
`ensureRepo/changedFiles/commitAll/log/revertTo/checkpointBefore/After` on
isomorphic-git, taking an injected `GitFs`. The production adapter
`gitFsExpo.ts` (expo-file-system → isomorphic-git fs) **does not exist**, so git
cannot run on-device. isomorphic-git is in `package.json` (`^1.38.4`). This is
the single highest-leverage unblock: writing the adapter lights up checkpoints,
history, and revert — the safety net that makes yolo agentic edits acceptable.

**Run/preview gap:** `phone-project/run/[slug].tsx` is a SQLite row browser, not
an app preview. The phone does **not** run the React app. `execForSession`
(new) routes shell to a box or the Android proot agent; iOS phone-local has no
shell → "needs a machine."

## 2. Tasks / projects / Hermes projects

Three separate systems, no unified door:
- **tasks.tsx** → `sendTask` (`/tasks`, `runner`+`model` picker:
  claude/codex/opencode), streaming output capped 8000 lines. **No auto-commit**
  — the `/vibing/*` pipeline owns commits, not raw tasks.
- **apps.tsx / phone-projects** → 5-step wizard with `startMode`
  (`this-phone`/`current-agent`/`dev-hw`/`yaver-cloud`) collapsing to
  `codingMode` (`phone`|`runner`); `phone_backend*.go` SQLite app, promotable to
  Convex/Supabase. Git step exists but doesn't auto-wire a provider.
- Weak **task↔project binding** (tasks carry a `workDir`, not a durable project
  link). No phone-side task→commit→PR loop.

The decision tree the user must infer today: connected machine → remote task;
DB app offline → Hermes project; quick code → sandbox. This maps cleanly onto
the new `codingSession` `intent` + engine×target — which is exactly why wiring
`codingSession` in is the unification lever.

## 3. XReal + keyboard + tmux workstation

- **Keyboard: shipped.** `YaverKeyboardRouter` (iOS `GCKeyboard`, Android
  `dispatchKeyEvent`) + `keyboardRouter.ts` multiplexer routing to terminal PTY
  / browser session / voice. ~95%. Gaps: no layout remap, single active sink.
- **tmux: agent-ready, mobile-blind.** `desktop/agent/runner_tmux.go` +
  `/tmux/{sessions,adopt,input}` exist; **mobile calls none of them.**
  `shell.tsx` always spawns a fresh PTY. `glass-workspace.tsx` renders i3-style
  tiles (1x1…2x3) but the **shell pane is read-only** and there's no
  session/window management — cosmetic multitasking.
- **External display: mobile-zero.** No `DisplayManager`/`Presentation`/
  DisplayPort code anywhere in `mobile/`. XReal works only as a **mirror** of the
  phone's portrait screen. The real 3-pane "android-trio" layout lives in the
  **web** `/spatial` route (`surfaceDetect.ts`), not the native app. Font is
  hardcoded 13pt — tiny on a 1080p virtual glass.
- **Multi-session: none.** One PTY per `shell.tsx`; no tabs/splits with input.

To make phone+XReal+keyboard+tmux a real workstation: (a) mobile tmux client
(call `/tmux/*`, render adopted sessions, route keyboard into the focused pane),
(b) Android `Presentation` external-display surface with a landscape grid +
runtime font size, (c) multi-pane keyboard input in `glass-workspace`.

---

## Prioritized roadmap (mobile-sandbox-first, per kivanc)

**Slice A — Sandbox, fully wired (now):**
1. ✅ `gitFsExpo.ts` — the isomorphic-git ⇄ expo-file-system adapter. DONE +
   tested end-to-end. Contract: ig runs on POSIX virtual paths under a baseUri
   (the document dir); pass `sandboxGit` a `dir` from `gitDirForSlug(slug)` and
   `fs` from `createExpoGitFs()` — NOT a `file://` URI (ig collapses the scheme).
2. Wire the agentic loop (`runCodingAgent`) into `SandboxAiPanel` with a
   preview/confirm gate + checkpoint-before/after via `sandboxGit`.
3. Route `sandbox-ai` through `codingSession`/`codingExecution` so the engine
   (on-device CLI / in-app Hermes / box) and apply-target are policy-chosen.
4. Add project git metadata (remote, branch, last commit) to the project model.

**Slice B — Close the loop:** phone → commit → GitHub push (provider auth +
`isomorphic-git.push`), then task↔project binding so a remote task's result is
committable from the phone.

**Slice C — Workstation UX:** mobile tmux client (`/tmux/*` + keyboard-into-pane),
Android `Presentation` external-display landscape grid + runtime font size.

Slice A is the "optimize mobile sandbox first" work and starts below.
