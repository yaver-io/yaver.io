# Mobile Sandbox → Remote Runner (GLM)

> Status 2026-06-20. Working tree only — **not committed**. Builds + unit tests
> green (Go native + linux/arm64, mobile tsx + tsc). Live Hetzner e2e **not run**
> (box flapping/unreachable, local CLI unauthed this session).

## What this adds

The phone-only **Mobile Sandbox** edits a project whose source lives entirely on
the phone (`<docDir>/phone-projects/<slug>/src/`, see `phoneSandboxSource.ts`).
Until now it could only be coded by an **on-device** model or a **BYO-key cloud**
call made *from the phone* (`codingBackend.ts`: local / anthropic / openai / glm).

This adds a new coding backend — **`remote` (Remote runner, GLM)** — that runs
the coding **agent on a connected box** instead of on the phone:

1. The phone ships the sandbox's source files + the user's prompt to the box.
2. The box materializes them into a throwaway workdir, runs the **`glm` runner**
   (the `claude` binary pointed at z.ai — `tasks.go` + `provider_keys.go`)
   agentically over them, then **diffs** the workdir against the input.
3. The box returns an EditPlan-shaped diff; the phone previews + applies it to
   its local sandbox exactly like every other backend.

Distinct from the create-wizard's `codingMode="runner"` (which edits a repo *on*
the box). Here the engine runs remotely but the edited tree stays the phone's.

**GLM-only by design for now.** The runner is fixed to `glm`; any other runner id
is rejected. The z.ai credential lives **only on the box** (runner-provider vault
/ `ZAI_API_KEY`) — the phone never holds it. That's the headline benefit over the
existing on-device `glm` backend (which needs a key on the phone): agentic,
multi-step editing on a box that owns the credential.

## Why diff-the-workdir (not parse structured output)

The runner edits files on disk with its own tools; we capture the result by
snapshotting before/after and diffing. This is model-agnostic (no fragile
output-format parsing), and it's exactly what "the box ran the agent" means.

## Files

**Agent (`desktop/agent/`):**
- `sandbox_remote.go` — NEW. Types (`sandboxRunRequest/Response`, `sandboxEdit`),
  pure helpers (`sandboxSafeRelPath`, `writeSandboxFiles`, `snapshotSandboxDir`,
  `diffSandboxSnapshots`, `buildSandboxRemotePrompt`), the testable core
  `processSandboxRun(ctx, req, runFn)`, the default runner `runGLMSandbox`
  (reuses `GetRunnerConfig("glm")` + `runnerProviderEnv("glm")`), and the
  `POST /sandbox/run` handler. The GLM exec is isolated behind `sandboxRunnerFn`
  so the write/snapshot/diff logic is fully unit-tested without a binary/network.
- `httpserver.go` — registers `mux.HandleFunc("/sandbox/run", s.auth(...))`.
- `sandbox_remote_test.go` — NEW. 9 tests: path safety, ignore filter, write↔
  snapshot round-trip, diff, fake-runner end-to-end (create/update/delete),
  no-op, partial-error, unsafe-input rejection, prompt assembly.

**Mobile (`mobile/`):**
- `src/lib/llmRemote.ts` — NEW. Pure `createRemoteProvider({ dispatch, model })`
  implementing `LlmProvider`; maps `EditFilesRequest` → `/sandbox/run` body and
  the response → `EditPlan`. Forces `runner:"glm"`; folds a partial box error
  into the rationale; throws on a hard failure.
- `src/lib/quic.ts` — adds `QuicClient.sandboxRun(body)` (POST `/sandbox/run`
  over the live transport, timeout = budget + 30s).
- `src/lib/codingBackend.ts` — `"remote"` added to `CodingBackendId`, metadata,
  `backendUsable` (gated on new `remoteRunner` availability), and a note that
  remote is **never auto-picked** (explicit choice only).
- `src/lib/codingBackendStore.ts` — `VALID_PREFS`, `loadCodingAvailability`
  (`remoteRunner: quicClient.isConnected`), and `makeProvider` case `remote`
  → `createRemoteProvider({ dispatch: quicClient.sandboxRun })`.
- `src/lib/codingSession.ts` — label for the new backend.
- `app/sandbox-ai.tsx` — no change needed; the chooser maps `CODING_BACKENDS`,
  so the "Remote runner (GLM)" row appears, selectable only when a box is
  connected.
- `src/lib/llmRemote.test.mts` — NEW (6 tests). `codingBackend.test.mts` — 2 new
  tests for the remote backend. Availability fixtures updated in
  `coding{Backend,Session}.test.mts` + `startCoding.test.mts`.

## Verification

- `go build ./...` ✅ · `GOOS=linux GOARCH=arm64 go build` ✅
- `go test` (sandbox_remote) ✅ 9/9 · `gofmt` clean
- `npx tsx llmRemote.test.mts` ✅ 6/6 · `codingBackend` ✅ 9/9 ·
  `codingSession` ✅ 17/17 · `startCoding` ✅ 11/11
- `npx tsc --noEmit` (mobile) ✅ 0 errors

## Security / limits

- Incoming paths validated by `sandboxSafeRelPath` (no abs, no `..`, no `\`) —
  the phone can't write outside the throwaway workdir. Workdir is `RemoveAll`'d.
- Diff ignores dot-dirs (`.git`, `.claude`, `.expo`), `node_modules`, `dist`,
  `build`, `vendor`, and files > 512 KB — so agent scratch/config doesn't leak in
  as spurious edits.
- Request caps: ≤ 400 files, ≤ 2 MB total; timeout default 180s, max 600s.
- Auth: standard same-user bearer (`s.auth`) — runs as the signed-in user.

## Open / next

- **Live Hetzner e2e** — set `ZAI_API_KEY` (or `API_KEY__glm`) in the box's
  runner-provider vault, then from the phone pick "Remote runner (GLM)" and edit
  a sandbox project. Deferred this session (env unstable).
- **Subscription-precedence caveat** — on a box that *also* has a `claude`
  subscription login, whether the `claude` binary honors the z.ai
  `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` override is the open question from
  the GLM-hybrid work (`docs/yaver-glm-hybrid-STATUS.md`). For a GLM-only box
  this doesn't bite; revisit if mixing on one box.
- Future: surface the box's GLM-config readiness in availability (today it's a
  run-time error with a clear "configure ZAI_API_KEY on the box" message).
