# Phone-First Dev Stack — Remained

Tracker for the phone-first dev stack work driven by
`docs/phone-first-dev-stack.md`. Updated when a slice lands so
future sessions can pick the work up without re-tracing the
conversation.

> **Vision recap.** Step 1: developer codes entirely on their phone
> (editor + on-device backend + AI assist + live preview). Step 2:
> export to a hosted PC and deploy through the four canonical
> targets (Convex, Cloudflare, TestFlight, Play Store). The full
> framing lives in the design doc above.

## Snapshot — 2026-04-28

Today HEAD is `1de2b87c` ("mobile: phone-side BYOK LLM client
(Slice 3 of phone-first stack)") plus the `a4fa58b6` runner
whitelist tip on origin. The phone now has the storage primitive,
the editor surface, and the LLM provider in place. Live preview and
the deploy plumbing are next.

## Done so far

### ✅ Slice 0 — unified ProjectStore foundation

Pre-requisite for the phone-first work. ProjectStore interface +
agent-tier and repo-tier implementations + cross-tier round-trip
test + TS twin (`mobile/src/lib/projectStore.ts`,
`projectStoreSandbox.ts`).

- Agent: `cc983d36`, `72331e1b`, `f936ce59`, `643097cd`, `652f817b`
- TS: `5c705af1`
- Reference: `docs/yaver-code-deploy-integration.md` §"Unified
  Export/Import Layer"

### ✅ Slice 1 — phone-side source-tree storage

Each phone project now gets an on-device `src/` tree under
`<doc>/phone-projects/<slug>/src/`. Editor + future export pipeline
both read and write through the same source store.

- Commit: `c3a03b39`
- Files:
  - `mobile/src/lib/phoneSandboxFs.ts` — adapter interface
  - `mobile/src/lib/phoneSandboxFsExpo.ts` — production adapter
    (only place that imports `expo-file-system`)
  - `mobile/src/lib/phoneSandboxSource.ts` — `createSourceStore`
    factory + read/write/delete/list/hasSource (pure logic)
  - `mobile/src/lib/phoneSandboxSourceDefault.ts` — production
    binding to `expoFsAdapter`
  - `mobile-headless/src/shims/expo-file-system.ts` — Node-fs
    shim for hermetic tests
  - `mobile-headless/test/phone-sandbox-source.test.ts` — 27 tests
- Hard rules pinned: posix-relative paths only, no `..`, no abs,
  no backslash, no NUL, no double-slash; UTF-8 only; atomic writes
  via `.tmp` + rename; strict slug validation at every entrypoint.

### ✅ Slice 2 — phone code editor screen

`mobile/app/phone-project/code/[slug].tsx`. File tree + multiline
editor, dirty-state guard, save / new / delete, starter `App.tsx`
on first visit. iOS uses `Alert.prompt`; Android falls through to
an inline TextInput panel.

- Commit: `f1b85f9b`
- Reachable from `[slug].tsx` via the new "Code ›" link next to
  "Workspace ›".
- UX: long-press deletes (with confirmation), Save greys out when
  nothing is dirty, dirty-state guard refuses to switch files or
  back-navigate without explicit "Discard".

### ✅ Slice 3 — phone-side BYOK LLM client

Phone can now ask Claude to edit the project's `src/` tree without
involving an agent. Tool-use forced via `apply_edits` for reliable
JSON; partial-success contract on apply.

- Commit: `1de2b87c`
- Files:
  - `mobile/src/lib/llmClient.ts` — `LlmProvider` interface,
    `EditPlan`, `FileEdit`, `ApplyTarget`, `applyEditPlan`,
    `assertRequestSize`, `formatEditPlan`. Pure, zero native deps.
  - `mobile/src/lib/llmAnthropic.ts` — `createAnthropicProvider`
    with `anthropic-dangerous-direct-browser-access` header, model
    + maxTokens + baseUrl + fetchImpl overrides. Default model
    `claude-opus-4-7`.
  - `mobile-headless/test/llm-client.test.ts` — 17 tests pinning
    request shape, response parsing, abort/timeout, partial-success
    apply, end-to-end flow.

**Hermetic test totals: 64/64 across 5 files.**

## Remained — ordered by leverage

### ⏳ Slice 3.5 — UI + secure-store glue for the LLM client

The provider exists but the editor screen doesn't call it yet, and
the API key has nowhere persistent to live. Small slice, ships
real value the day it lands.

- **Persist the API key.** New `mobile/src/lib/llmKeys.ts` wrapping
  `expo-secure-store` (already shimmed in mobile-headless). Keys
  scoped per provider id (`anthropic` / `openai` / future).
- **Settings entry to enter the key.** Small screen
  `mobile/app/phone-project/llm-settings.tsx` with a TextInput,
  save, clear. No trickery — paste the `sk-ant-...` key in.
- **"Ask AI" button on the editor screen.** Read all source files,
  build `EditFilesRequest`, call `createAnthropicProvider().editFiles(...)`,
  show `formatEditPlan` output in a modal with Apply / Cancel
  buttons. On Apply, run `applyEditPlan` against
  `phoneSandboxSourceDefault`.
- **Tests.** Headless test for `llmKeys` (round-trip via shim,
  clear works). UI is pure plumbing, no headless coverage path.

Effort: 1 day.

### ⏳ Slice 4 — source-mode dev preview

The hardest slice. Two paths from the design doc:

- **WebView path** for web/Vite-class projects (ships first). Phone
  bundles the source via a Metro-lite concat + import resolver,
  serves it via a tiny on-device HTTP server, loads it into the
  existing WebView component (which gets JS-with-JIT inside the
  system WebView). Realistic floor for what runs.
- **Hermes-source path** for RN. Skip `hermesc`. Drop source files
  into the existing Hermes runtime via `loadApp()` after a Metro-
  lite pre-pass. Slow but works without JIT.

Probably ships the WebView path behind a feature flag first, then
revisits Hermes-source mode once the rest of the loop is proven.

Open questions to resolve before starting:
- Where does the on-device HTTP server live? `mobile/ios/Yaver/`
  has a `YaverHTTPServer.swift` for the existing push-to-device
  flow on port 8347. Reuse or stand up a new one?
- How does the live frontend hit `/data/<slug>/<table>` against the
  on-device SQLite? The proxy lives where?
- Hermes interpreter mode: does Yaver's RCT bridge already accept
  uncompiled source, or do we need a per-bundle `hermes -O`
  fallback that runs fine on-device?

Effort: 3-5 days, mostly driven by the open questions above.

### ⏳ Slice 5 — Step 2 deploy plumbing

Hand off the project from the phone to a hosted PC and run one of
the four canonical deploy scripts. Reuses every existing piece —
just wires them together.

- **Extend the export tarball with `src/` + `package.json`.**
  `desktop/agent/phone_backend.go::ExportPhoneProjectWithOptions`
  walks `<phone-project>/src/` today only when the agent has it
  on disk; the phone-first flow needs the *phone's* `src/` tree
  to travel. Add a `sourceTree` field to the upload payload
  (multipart already supports it via the bundle field).
- **Add `ProjectKind` detection on import.** `phone-backend |
  expo-app | web-app`. Switch the import handler to choose the
  right next step.
- **`POST /phone/projects/deploy` on the agent.** Body:
  `{slug, target: convex|cloudflare|testflight|playstore, hostBaseUrl?}`.
  Runs the matching script in-process, streams stdout via the
  existing `/dev/events` SSE pipe.
- **Mobile target picker.** Replace the two-button section in
  `mobile/app/phone-projects.tsx` with a 4-target picker (Convex
  / Cloudflare / TestFlight / Play Store) and a host sub-picker.
- **Capability flags from `/info`.** Hosts probe for
  `xcodebuild` / Java 17 / `wrangler` / `convex` on agent startup
  and surface the flags so the picker can grey out unreachable
  targets.

Effort: 2-3 days.

### Pre-existing blockers (touched only when forced)

- **TestFlight `react-native-udp` API mismatch.** `Yaver/UdpSockets.m:122`
  + `:131` call `joinMulticastGroup:address error:` while the
  framework's header declares `joinMulticastGroup:(NSString *)group
  error:`. Fix is small (cast or refactor), but blocks every
  TestFlight push. Caught today as exit-65 archive failure during
  the Slice 0 push attempt.
- **Cloudflare Workers target for phone-projects.** Spec exists
  in `PHONE_EXPORT_PIPELINE.md` §Handoff 2.5; implementation
  estimated at 2-3 days. Not strictly required for Slice 5 (the
  other three targets cover the common path), but unlocks the
  "hosted PC = a Cloudflare Worker" story.

### Future polish (non-blocking)

- **Other LLM providers.** OpenAI / xAI / Codex Cloud all plug into
  `LlmProvider` the same way Anthropic does. ~half-day each once
  the contract is exercised in production.
- **Schema awareness in the LLM prompt.** `editFiles` already
  accepts `schema: PhoneSchema` — we don't yet send it from the
  editor screen. Wire it through so the model writes correct CRUD.
- **Direct OAuth on the phone (no-agent runner sign-in).** Today
  `RunnerAuthModal.tsx` mediates through an agent's
  `/runner-auth/browser/*` endpoints. The vision needs a phone-side
  twin so a user can sign into Claude Code from the device with
  zero PC involvement. New `mobile/src/lib/runnerAuthDirect.ts`
  that mirrors the agent flow on-device.

## Verification — how to check what's true now

```bash
# Hermetic phone-stack tests (Slices 1–3)
cd mobile-headless
bun test test/phone-sandbox-source.test.ts \
         test/llm-client.test.ts \
         test/project-store.test.ts \
         test/build-native.test.ts \
         test/hermetic.test.ts
# Expected: 64/64 pass.

# Type-check the headless project (Slices 1, 3 are headless-friendly).
./node_modules/.bin/tsc --noEmit
# Pre-existing errors in unrelated mobile lib files (builds.ts,
# phoneSandboxLocal.ts, quic.ts) are NOT from this work.

# Go agent tests (ProjectStore foundation from Slice 0)
cd ../desktop/agent
go test -run "TestNewProjectNotFound|TestProjectNotFound|TestConflictReject|TestProjectMetaTier|TestAgentStore|TestRepoStore|TestCrossTier" -count=1 .
# Requires Go 1.26 — runs cleanly on the Mac dev box.
```

## Files to know

- Design doc: `docs/phone-first-dev-stack.md`
- Adjacent design: `docs/yaver-code-deploy-integration.md` (Slice 0
  ProjectStore foundation)
- Source store: `mobile/src/lib/phoneSandboxSource*.ts`
- Editor screen: `mobile/app/phone-project/code/[slug].tsx`
- LLM client: `mobile/src/lib/llmClient.ts`,
  `mobile/src/lib/llmAnthropic.ts`
- Hermetic tests: `mobile-headless/test/phone-sandbox-source.test.ts`,
  `test/llm-client.test.ts`, `test/project-store.test.ts`
- Agent ProjectStore: `desktop/agent/projectstore*.go`,
  `desktop/agent/code_phone*.go`

## Known quirks

- **Mac Tailscale flap.** During the Slice 0 push the WSL
  workstation lost SSH to the dev Mac mid-push and has not
  reconnected since. Pushes now go through the GitHub PAT
  configured in `~/.config/gh/hosts.yml` on this WSL box. Don't
  block on the Mac for routine commits.
- **Bun must be on `$PATH`** for `mcp.test.ts` and
  `mcp-drift.test.ts` to run — those tests spawn `bun` as a
  subprocess. The five hermetic tests this work cares about don't
  need that.
- **`mobile/node_modules/`** is not installed on this WSL box, so
  `tsc --noEmit` from inside `mobile/` can't run here. The
  headless project's tsc covers Slices 1 + 3; the editor screen
  (Slice 2) is type-checked on the Mac / in CI.
