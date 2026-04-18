# Remained

## Thread Index

### Guest Sharing Presets

- Status: in progress
- Scope: guest/resource sharing policy, presets, future remote desktop gating

### Mac Escape Dev

- Status: in progress
- Scope: Hermes reload from Linux / WSL / macOS / remote host to Yaver mobile on iPhone/Android, plus compatibility surfacing and guidance about `yaver-cli` / Feedback SDK

### Phone Backend for Third-Party Apps

- Status: in progress — runtime data API, per-project tokens, CORS, cost guardrails, escape routes, OAuth providers, DNS helpers, deploy-state rebinding, and AI-prompt scaffold all shipped on 2026-04-17. Next slice is follow-ups listed below.
- Scope: every inbound/outbound a developer touches when they build a React Native / web / Node app **against** a Yaver-hosted project. Not the export/import pipeline (that's the peer thread); this is the runtime API surface their end users' devices actually talk to.

## Guest Sharing Presets

### Just Landed

- Guest/resource sharing now has explicit host-approved presets:
  - `machine-only`
  - `machine-with-host-keys`
  - `desktop-control`
  - `desktop-control-with-host-keys`
- Infra grants now store future remote-control capability flags:
  - `allowDesktopControl`
  - `allowBrowserControl`
  - `allowTunnelForward`
- Agent-side guest policy now infers those presets safely and injects them into the guest security prompt.
- MCP `guest_config`, CLI `yaver guests config`, mobile guest config UI, Convex guest config APIs, and docs all understand the new sharing model.

### Next Work

- Build the real remote desktop transport on top of the new policy layer.
  - Start with host-approved tunnel-backed RFB/noVNC or equivalent browser-safe stream path.
  - Require explicit host session approval per desktop session, not just per guest grant.
  - Keep desktop control, browser automation, and raw tunnel access separately revocable.
- Add backend tests for `resourcePreset` validation conflicts and default inference in Convex.
- Add end-to-end guest sharing tests across two devices proving:
  - `machine-only` never exposes host API keys
  - `desktop-control` does not implicitly enable tunnel forwarding
  - device-scoped grants stay device-scoped
- Add web dashboard UI for guest resource sharing.
  - The client types are updated, but there is not yet a dedicated dashboard editor for these new preset fields.
- Wire future remote desktop session creation through the same guest policy checks before exposing any VNC/RFB/WebRTC endpoint.

### Still Dirty Locally

- Uncommitted local files remain outside this commit scope:
  - `desktop/agent/agent_mesh_remote.go`
  - `desktop/agent/agent_mode.go`
  - `desktop/agent/completion.go`
  - `desktop/agent/exec_cmd.go`
  - `desktop/agent/httpserver.go`
  - `desktop/agent/mcp_workspace.go`
  - `desktop/agent/remote_yaver.go`
  - `desktop/agent/session_cmd.go`
  - `desktop/agent/stream_cmd.go`
  - `desktop/agent/tasks.go`
  - `desktop/agent/template.go`
  - `mobile/src/lib/quic.ts`
  - `scripts/test-yaver-to-yaver-local.sh`
  - `web/next-env.d.ts`
- Untracked local files remain outside this commit scope:
  - `desktop/agent/agent_mesh_remote_test.go`
  - `desktop/agent/agent_mode_template_test.go`
  - `desktop/agent/code_cmd.go`
  - `desktop/agent/graph_slice.go`
  - `desktop/agent/graph_slice_test.go`
  - `desktop/agent/template_test.go`

### Verification Run For This Slice

- `go test -run 'TestGuestResourcePresetInference|TestGuestPromptPrefixIncludesResourcePolicies|TestCollectAPIKeysForGuestBlocksHostKeysByDefault|TestTaskEnvStripsSharedSecretEnvForGuests|TestTaskEnvKeepsHostKeysWhenExplicitlyAllowed' ./...` in `desktop/agent`
- `npx tsc --noEmit` in `mobile`
- `npx tsc --noEmit` in `web`

## Mac Escape Dev

### Just Landed

- Added [MAC_ESCAPE_DEV.md](/Users/kivanccakmak/Workspace/yaver.io/MAC_ESCAPE_DEV.md:1) as the strategy dump for the Linux / WSL / remote-host to iPhone workflow.
- README now explicitly documents:
  - Linux / WSL / remote iPhone workflow
  - what still requires macOS
  - that `yaver` agent + mobile app is enough for Hermes reload into Yaver
  - that `yaver-cli` and Feedback SDK are optional, not required, for the default agent flow
- Mobile UI wording now favors the actual path:
  - `Open in Yaver` instead of `Open App`
  - visible mode label for `Hermes bundle in Yaver` vs `native install`
  - explicit reason text from the agent for why `bundle` or `native` was chosen
- Agent iOS install resolution is now reasoned rather than opaque:
  - `auto` resolves to `native` on macOS + Xcode
  - `auto` resolves to `bundle` on Linux / WSL / non-Xcode hosts
  - MCP/status/log output includes the reason string
- `/dev/status` now exposes:
  - `iosInstallMethod`
  - `iosInstallReason`
- Project action sheet now runs compatibility surfacing for Expo / React Native projects before open:
  - checks selected project against the phone app’s available native modules
  - shows guidance when Hermes reload should work
  - warns when native modules are missing from the Yaver host container
- `/dev/compatibility` is now manifest-driven instead of heuristic-only:
  - reads `sdk-manifest.json`
  - compares project `react-native` against Yaver RN version
  - checks New Architecture compatibility
  - warns on major native-module version mismatches
  - returns guidance about when `yaver-cli` may still be helpful
- The contributor workflow is now explicit in agent + mobile UI:
  - remembers Hermes build state (`needs_build`, `ready`, `build_failed`)
  - surfaces `Compile Hermes`, `Rebuild Hermes`, and `Open in Yaver` separately
  - shows package manager, dependency-install state, missing local tools, and Hermes compiler readiness before first build
- Fresh-clone preparation is now smarter on the agent:
  - detects `npm` / `yarn` / `pnpm` / `bun` from lockfiles or `packageManager`
  - auto-installs dependencies on first build when the machine can do it
  - blocks early with explicit guidance when `node` / `npm` / `npx` / other required tools are missing
- Claude Code MCP setup is now integrated into Yaver startup flow:
  - `yaver auth` and `yaver serve` auto-try to register Yaver into Claude Code MCP config when `claude` is on `PATH`
  - `yaver mcp setup claude-code` exists as the explicit recovery path
- MCP now exposes mobile-project readiness to remote agents:
  - `mobile_project_status`
  - `mobile_project_prepare`
  - this lets Claude Code on WSL inspect and prepare a freshly cloned Expo / React Native project before the phone tries to build it

### Current Product Position

- Supported default path:
  - developer runs `yaver` on Linux, WSL, macOS, or a remote host
  - phone runs Yaver mobile app
  - `Open in Yaver` builds Metro + Hermes on the host and loads the app into the Yaver phone app
- Not required for this path:
  - `yaver-cli`
  - Feedback SDK
- Optional extras:
  - `yaver-cli` for direct CLI push/watch and standalone compatibility checks
  - Feedback SDK for in-app bug reporting, black-box streaming, and reload inside the app’s own process
- Main remaining runtime risk:
  - project depends on native modules not present in the Yaver phone app manifest
- Supported contributor split now:
  - contributor clones source, runs `yaver auth` / `yaver serve`, tests on iPhone inside Yaver, commits and pushes
  - maintainer does the real TestFlight deploy later from the Mac/Xcode path

### Next Work

- Add MCP tools that trigger the actual Hermes build and open flow, not just status/prepare.
  - `mobile_project_build`
  - optional `mobile_project_open`
  - this would let Claude Code fully drive the “fresh clone -> install deps -> compile Hermes -> ready for phone” loop
- Make machine capability detection smarter than binary presence.
  - distinguish WSL, native Linux, macOS, remote VM
  - detect whether install guidance should prefer `apt`, `brew`, `mise`, `nvm`, or distro-native node packages
  - expose that advice directly in the compatibility payload and phone UI
- Add project-prep tests for the fresh-clone case.
  - no `node_modules`
  - missing `node`
  - `pnpm` / `yarn` / `bun` lockfile detection
  - embedded hermesc available vs project hermesc vs no hermesc path
- Add Claude Code MCP auto-setup tests / dry-run path.
  - avoid silently regressing CLI integration on WSL
  - verify we do not spam duplicate `claude mcp add` calls once already configured
- Surface the prep state outside the action sheet too.
  - project card badge for `needs deps`
  - project card badge for `needs build`
  - project card badge for `build failed`
- Add a one-shot “prepare this project for iPhone testing” button in mobile.
  - should install deps when allowed
  - should compile Hermes if possible
  - should leave `Open in Yaver` as the final step
- Make compatibility even closer to `yaver-cli` output.
  - Reuse or mirror the analyzer logic more directly so pure-JS exclusions and false-positive native package handling stay aligned in one place.
- Surface compatibility outside the action sheet too.
  - Show it in the project card, hot reload card, or a dedicated detail screen.
- Add explicit Hermes / RN mismatch UI states.
  - Red for hard incompatibility
  - Yellow for “may work” minor mismatch
- Add tests for `/dev/compatibility`.
  - RN major mismatch
  - RN minor mismatch
  - missing native modules
  - manifest missing / malformed
  - pure-JS package false positives
- Consider exposing SDK manifest details directly to mobile.
  - RN version
  - Hermes BC version
  - supported native module map
- Audit remaining UI copy for Mac-first assumptions.
  - especially any place that still implies Xcode/native install is the normal phone path

### Still Dirty Locally

- This thread intentionally did not resolve unrelated local changes already present in the worktree.
- Notable overlap-sensitive file already dirty before/alongside this work:
  - `mobile/src/lib/quic.ts`

### Verification Run For This Slice

- `go test -run 'TestResolveIOSInstallMethodWithReason'` in `desktop/agent`
- `go build ./...` in `desktop/agent`
- `npx tsc --noEmit` in `mobile`

## Phone Backend for Third-Party Apps

### Just Landed

- **Phone project runtime** — schema DSL, auth personas, seed, CRUD, portable tgz export + receive, 12+ tests. Same binary on phone (via local SQLite sandbox `phoneSandboxLocal.ts`), dev-hw (via `yaver serve`), and Yaver Cloud (via `cloud/` Docker stack).
- **Deploy pipeline** — `yaver phone push`, mobile `pushPhoneProject()`, `/phone/projects/receive` with conflict policies (reject / rename / overwrite) and optional `includeData`. Cap 50 MB (`PhoneDeployBudgetBytes`, configurable) enforced producer + consumer side with a descriptive 413.
- **Three-mode create** — `[This device]` / `[Your Dev Machine]` / `[Yaver Cloud]` picker in `mobile/app/phone-projects.tsx` so the user picks the tier at project birth rather than create-then-promote.
- **Deploy-state rebinding** — `PhoneProjectAccess` + `bindPhoneProjectToTarget` + `clearPhoneProjectBinding`. After a push, subsequent CRUD from the phone routes to the bound target via `getPhoneProjectAccess(slug)` so the UI stays coherent across the continuum.
- **Local on-device sandbox** — `mobile/src/lib/phoneSandboxLocal.ts` + `phoneProjects.ts` helpers (`ensureLocalPhoneProject`, `dumpLocalPhoneProjectRows`, `syncLocalPhoneProjectToConnectedAgent`). Offline create / CRUD on the phone, then sync to the connected agent when reachable.
- **Voice/text prompt → scaffold** — `runPhonePromptGenerator` + `generatePhoneProjectFromPrompt`. A prompt like "todo app with login" generates a `PhoneSchema` + `PhoneAuth` + `PhoneSeed` + portable `PhoneAppSpec` (screens + actions) via the AI runner.
- **Per-project API tokens** — `desktop/agent/phone_tokens.go`: `pp_<slug>_<hex>` format, SHA-256 stored in `<project-dir>/tokens.yaml` (0600), plaintext returned exactly once on mint, hard per-project scope (cross-project access 403'd before the adapter runs). `POST/GET/DELETE /phone/projects/tokens`. 8 tests.
- **Public data routes** — `desktop/agent/phone_data_http.go`: `GET /data/{slug}/{table}[/{id}]` list + get-one, `POST` insert, `PATCH` update, `DELETE` remove. Auth via `Bearer` / `X-API-Key` / `?api_key=` (all `pp_`-prefixed). CORS preflight with origin echo. 1 MB request body cap per row. 8 tests.
- **`yaver-sdk` backend client** — `sdk/js/src/backend.ts`: `createYaverBackendClient({ baseUrl, slug, apiKey }).collection<Row>(name)` with typed `list / get / insert / update / remove`, `YaverBackendError` carrying `.status`. Works in RN + browser + Node 18+ with zero deps (global fetch).
- **Mobile API Keys screen** — `mobile/app/phone-project/api-keys.tsx`: mint with label, show raw ONCE with copy + dismiss, list with createdAt/lastUsed, revoke with instant-effect. Pre-filled `yaver-sdk` snippet at the bottom.
- **OAuth providers** — `phone_oauth.go` + `mobile/app/phone-project/oauth.tsx`: paste-back guidance for Apple / Google / Microsoft. Values travel with the push when the project promotes to another of the user's own boxes; stripping for third-party promote is a follow-up.
- **Cloudflare DNS helpers** — `phone_data_*` + `mobile/app/phone-project/dns.tsx`: paste scoped CF token → verify → list zones → one-tap CNAME subdomain to `cloud.yaver.io` (or any target). Token cached per-device in AsyncStorage; agent never persists.
- **Cost guardrails** — `phone_cost.go` + cost-hint endpoint + mobile pre-flight confirm showing "Uploading ~X.Y MB (cap: N MB). Plan: …. Advice: …." before any bytes hit the wire.
- **Curated escape routes** — `phone_escape.go` with inbound (Convex / Supabase / Firebase-equivalents → Yaver Cloud, `highlight=PITCH`), outbound (Yaver → Neon / Turso / D1 / Convex / Supabase / Hetzner self-host), and cross-family third-party routes. Surfaced only inside the existing Advanced collapsible — trust signal, not headline.

### Current Product Position

- **Default developer loop**: create project on phone → tap `[Your Dev Machine]` or `[Yaver Cloud]` → mint an API key → drop it into their RN / web app via `yaver-sdk`. End users of that app hit `/data/<slug>/<table>/…` over CORS.
- **Deploy gating**: every promote goes through the cost-hint pre-flight and is capped at 50 MB by default.
- **Lock-in story**: escape routes exist + are one tap + use the existing switch engine with 7-day rollback. Kept non-prominent per the user's positioning call.

### Next Work (Codex-targetable, in leverage order)

- **Per-project CORS origin allowlist.** Permissive default ships today; add a text input in `mobile/app/phone-project/api-keys.tsx` for comma-separated origins, plus an allowlist gate in `phone_data_http.go :: writePhoneDataCORS` before the echo. Preserve wildcard when the project chooses it, 403 preflight otherwise.
- **Per-token rate limiting.** Buggy app / leaked key currently has no throttle. Implement a per-token token-bucket in memory (keyed on token hash) with a default of e.g. 600 req/min / 10 req/s, configurable per-token in `tokens.yaml`. Surface the limit in the mobile API Keys screen so the developer sees it per key.
- **Typed schema codegen.** `yaver codegen` CLI that reads `<project>/schema.yaml` and emits `types.ts` with `type Todo = { id: string; title: string; done: boolean; ... }` so `collection<Todo>('todos')` inference is automatic. Ship as a CLI subcommand + a Next.js / Metro plugin that re-runs on schema change.
- **SDK smoke tests.** Add `sdk/js/test.mjs` that spawns the agent binary, mints a token, exercises every SDK verb end-to-end. One file, node --test, no framework. Pins the HTTP contract between `phone_data_http.go` and `backend.ts`.
- **Cumulative byte ledger** (`~/.yaver/deploy-ledger.json`) + `/phone/projects/usage` endpoint so the mobile Deploy screen can show "this month: 120 MB pushed to Yaver Cloud" before the user taps.
- **Deploy rate limiter.** 30-second min interval per (slug, target) server-side to prevent double-tap storms. Return 429 with a Retry-After header.
- **Hard dollar budget cap.** Config flag `cost_budget_usd_per_month`; agent refuses deploys after the threshold with a clear error pointing at the ledger.
- **`ExportPhoneProjectWithOptions.NoSecrets`.** Strip `oauth-providers.yaml` (and any future secret files) before the tgz goes out. Gate the third-party escape routes (Convex/Supabase/etc.) to `NoSecrets=true` automatically so secrets can't leak when the user migrates to a third party.
- **Privacy regression coverage.** Extend `desktop/agent/convex_privacy_test.go` with a payload fixture that asserts the new `/data/*` and `/phone/projects/tokens` routes never push any of the forbidden keys through `convexSyncer.callMutation`. Follow the same pattern the existing test uses for projects / services / activity.
- **Yaver Lite on Cloudflare Workers.** Spec is in `PHONE_EXPORT_PIPELINE.md §Handoff 2.5`. 2-3 days of work — port `phone_backend_http.go` to TS on Workers backed by D1, plus a `POST /phone/projects/deploy-workers` agent helper using the Cloudflare API. Add `{kind: 'cloudflare-workers'}` to `PhonePushTarget`.
- **RN / web starter templates.** `npx create-yaver-app` that scaffolds an Expo + web app already wired to `createYaverBackendClient()`, prompts for the slug + API key, and drops a working Todo screen. Distribution of the wedge narrative.
- **Real-time subscriptions.** SSE or WebSocket over `/data/{slug}/{table}/subscribe` so RN/web apps don't have to poll. Phone-project sandbox can fan out SQLite triggers or poll locally. Deferred until after the HN launch — not demo-critical.

### Still Dirty Locally

- Parallel-thread work that landed while this slice was in flight (already committed on `main`, listed for context):
  - `mobile/src/lib/phoneProjects.ts` — `PhoneProjectAccess` + `PhoneAppSpec` types + sandbox-local bridge (`syncLocalPhoneProjectToConnectedAgent`, `preparePhoneProjectExport`)
  - `desktop/agent/phone_backend.go` — `PhoneAppSpec` + prompt-driven generation (`runPhonePromptGenerator`, `generatePhoneProjectFromPrompt`)
  - `desktop/agent/phone_backend_http.go` — `cost-hint` route registered alongside the runtime routes
  - `mobile/app/phone-project/[slug].tsx` — prompt scaffold UI threaded through the detail screen
- No uncommitted files left from this slice.

### Verification Run For This Slice

- `go test -run 'PhoneData|PhoneProjectToken|HandlePhoneTokens|ValidatePhoneProjectToken|MintPhone|ExtractPhoneProjectToken|PhoneDeploy|PhoneCostHint|Escape|PhoneOAuth|ExportIncludesOAuth|HandlePhoneOAuth|Cloudflare|HandleCFVerify|HandleCFZones|HandleCFRecords' -timeout 60s` in `desktop/agent`
- `npx tsc --noEmit` in `mobile`
- `npx tsc --noEmit` in `web`
- `npx tsc --noEmit` in `sdk/js`
