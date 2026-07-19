# Remote-Box-First App Development Handoff

Date: 2026-07-19
Repo: `/Users/kivanccakmak/Workspace/yaver.io`

## Objective

Move Yaver app development away from phone/local/browser sandbox onboarding and toward one simple model:

- A user starts from Tasks or a voice/vibe surface.
- New app development always runs on a remote box: self-hosted Yaver Mesh or Yaver Managed Cloud.
- Greenfield apps default to Yaver Git, Yaver Serverless, and a Yaver monorepo unless the user explicitly chooses another stack.
- Phone, web, watch, car, TV, and AR/VR are control, voice, approval, and preview surfaces.
- Hermes/WebRTC preview and the Feedback SDK are the core iteration loop.
- Keep old sandbox implementation code for possible future re-enable, but do not show it as a product path.

## Implemented

### Go agent and MCP

- `desktop/agent/sandbox.go`
  - `DefaultSandboxConfig().Enabled` is now `false`.
  - Comment explains that remote-box-first is the default and legacy sandbox can be explicitly re-enabled later.

- `desktop/agent/sandbox_test.go`
  - Added `TestDefaultSandboxConfigDisabledWhileRemoteBoxFirst`.
  - Existing sandbox guard tests now opt into enabled sandbox config.

- `desktop/agent/mcp_core_profile.go`
  - Added hidden sandbox MCP tool filter for:
    - `sandbox_run`
    - `sandbox_status`
    - `sandbox_config`
    - `sandbox_quickstart`

- `desktop/agent/mcp_tools.go`
  - `create_task` schema now accepts:
    - `device_id`
    - `work_dir`
    - `placement_kind`
  - Tool description now says new app development should pick a self-hosted remote box or Yaver Managed Cloud.
  - Sandbox tools are filtered from MCP `tools/list`.

- `desktop/agent/httpserver.go`
  - MCP `create_task` can proxy directly to a selected owned remote device via `device_id`.
  - Proxied task body includes runner/model/mode/workDir/video/askFreely/placementKind and `allowLocalFallback: true`.
  - Local MCP `create_task` passes `PlacementKind` and `WorkDir` into placement metadata.
  - `sandbox_run`, `sandbox_status`, `sandbox_config`, and `sandbox_quickstart` now return MCP errors while hidden.

- `desktop/agent/task_context.go`
  - No-question task preamble now encodes:
    - Yaver Git/Yaver monorepo/Yaver Serverless defaults.
    - Remote box requirement.
    - Hermes/WebRTC/Feedback SDK loop.
    - Cross-surface voice/vibe operation.
    - No phone/local/browser sandbox for new app development.

### Backend placement vocabulary

- `backend/convex/taskPlacement.ts`
  - Comments now describe placement across relay source runners, owned remote machines, and managed cloud.
  - `phone_sandbox` enum remains only for legacy stored row compatibility.

- `backend/convex/schema.ts`
  - Same comment update.
  - `phone_sandbox` remains in schema for old rows.

### Mobile app

- `mobile/src/lib/startCoding.ts`
  - New app development requires a remote box.
  - Backend/data app no longer routes to phone backend.
  - No-box cases return `needs-setup` on `tasks`.
  - Greenfield Hermes-remote also opens `tasks`; existing slug-specific routes can still open old project code routes.
  - Copy points users to self-hosted Yaver box or Yaver Managed Cloud.

- `mobile/src/lib/startCoding.test.mts`
  - Updated expectations to assert no sandbox/phone-backend route for greenfield app development.

- `mobile/src/lib/taskRequestBody.ts`
  - Added optional `placementKind` serialization for mobile-originated task requests.

- `mobile/src/lib/taskRequestBody.test.mts`
  - Tests now assert placement hints serialize for initial sends and Cloud Workspace final handoff.

- `mobile/src/lib/quic.ts`
  - `sendTask(...)` accepts optional trailing `placementKind`.
  - Request body passes `placementKind` to `/tasks`.

- `mobile/app/car-voice-coding.tsx`
  - Car voice coding dispatch tags tasks with `placementKind: "vibe"`.

- `mobile/src/components/WatchBridgeHost.tsx`
  - Watch voice dispatch tags tasks with `placementKind: "vibe"`.

- `mobile/app/(tabs)/settings.tsx`
  - Visible sandbox / phone-as-box settings are hidden behind `KEEP_SANDBOX_SURFACE = false`.

- `mobile/app/(tabs)/infra.tsx`
  - Container sandbox section is hidden behind `SHOW_SANDBOX_UI = false`.
  - Docker copy changed to optional infra wording.

- `mobile/app/(tabs)/more.tsx`
  - Primary app-development entry now opens Tasks.
  - Empty device state points to pairing/Yaver Managed Cloud.

- `mobile/app/(tabs)/tasks.tsx`
  - Removed the zero-device “build on this phone” escape link.

- `mobile/app/local-box.tsx`
  - Old local-box route remains in code, but default screen is a remote-box message with an Open Tasks CTA.
  - Legacy implementation moved behind `SHOW_LOCAL_BOX_SURFACE = false`.
  - Old visible titles changed to “Legacy local box”.

- `mobile/app/phone-projects.tsx`
  - Old phone-project route remains in code, but default screen is a remote app Tasks message.
  - Legacy implementation moved behind `SHOW_PHONE_PROJECTS_SURFACE = false`.
  - Old visible alert titles changed to “Legacy project”.

- `mobile/app/(tabs)/guests.tsx`
  - Guest vibe copy now says work runs on the selected remote box with scoped guest access.

### Web app

- `web/lib/agent-client.ts`
  - `CreateTaskParams` now includes `placementKind`.
  - `buildCreateTaskBody` serializes `placementKind`, defaulting to `"unknown"`.

- `web/lib/agent-client.test.ts`
  - Tests assert `placementKind` is serialized.

- `web/lib/pending-cloud-dispatch.ts`
  - Pending Cloud Workspace task params now carry `placementKind`.

- `web/app/dashboard/page.tsx`
  - Dashboard task creation now sends inferred `placementKind`.
  - Pending Cloud Workspace dispatches preserve `placementKind`.
  - Lean dashboard tab list hides non-core surfaces.
  - Phone Backend tab/render branch removed from normal dashboard routing.

- `web/components/dashboard/VibeCodingView.tsx`
  - Vibe tasks send inferred `placementKind`.
  - Pending Cloud Workspace dispatches preserve `placementKind`.
  - Deploy task actions send `placementKind: "deploy"`.

- `web/components/dashboard/WebReloadView.tsx`
  - Prompt tasks send `placementKind: "vibe"`.
  - Merge-conflict handoff tasks send `placementKind: "source"`.

- `web/lib/task-placement.ts`
  - New type no longer offers `phone_sandbox`.
  - Legacy label fallback changed from “Phone sandbox” to “Legacy local lane”.

- `mobile/src/lib/taskPlacement.ts`
  - Mobile placement lane type no longer offers `phone_sandbox` for new client code.

- `web/lib/yaver-apps.ts`
  - App catalog lifecycle changed from mobile sandbox to:
    - `remote-box`
    - `yaver-git`
    - `yaver-serverless`
  - Deployment targets now include `yaver-managed-cloud` and no longer include `phone-sandbox`.

- `web/components/dashboard/InfraView.tsx`
  - Sandbox section hidden with `SHOW_SANDBOX_UI = false`.

- `web/components/dashboard/ToolsView.tsx`
  - Docker copy changed to optional infra workloads.

- `web/components/dashboard/PhoneProjectsView.tsx`
  - Hidden legacy component heading changed from “Phone Backend” to “Legacy Projects”.

- `web/app/apps/page.tsx`
  - Developer revenue posture copy now starts from Tasks on a self-hosted Yaver box or Yaver Managed Cloud.

- `web/app/games/page.tsx`
  - Developer path now begins with remote box and Yaver Git/private repo.

- `web/app/docs/mcp/page.tsx`
  - MCP security guidance changed to owned remote boxes and optional legacy guard disabled by default.

- `web/app/page.tsx`
  - Homepage/FAQ/new project/cost copy updated for remote-box-first, Mesh/Relay Pro/Cloud Workspace, Yaver Git, Yaver Serverless, monorepo.

### tvOS

- `tvos/YaverTV/Views/DashboardView.swift`
  - Removed non-core tiles from the first dashboard surface:
    - Apple TV remote
    - Capture
    - Android
    - Update agent
    - Shared with
  - Kept core tiles:
    - Session
    - Tasks
    - Projects
    - Runtime
    - Feedback
    - Box switch
    - Sign out

### Docs

- `docs/architecture/REMOTE_WORKER.md`
  - Rewritten around remote-box-first development.
  - Documents Yaver stack defaults, Mesh/Relay/Tailscale-compatible transport, cross-surface STT/TTS, Feedback SDK loop, and MCP `create_task(device_id=...)`.

- `README.md`
  - Added greenfield app development paragraph.

- `CLAUDE.md`
  - Feature pointers updated to say legacy sandbox hidden and greenfield app development is remote-box-first.

- `docs/releases/YAVER_GAMES_SFMG_RELEASE.md`
  - Third-party developer lifecycle baseline changed to remote-box first.

## Validation Passed

Commands run successfully:

```bash
cd web && npx tsc --noEmit --pretty false
cd mobile && npx tsc --noEmit --pretty false
cd mobile && npx tsx src/lib/startCoding.test.mts && npx tsx src/lib/taskRequestBody.test.mts
cd web && npx tsx lib/agent-client.test.ts && npx tsx lib/task-placement-request.test.ts
cd backend && npx tsx convex/taskPlacementClassifier.test.mts
cd desktop/agent && go test . -run 'Test(DefaultSandboxConfigDisabledWhileRemoteBoxFirst|Sandbox|MCPRemoteDevelopmentToolSchemas|YaverWrapperCapabilityContext|YaverDevServerContext)'
git diff --check
```

Visible stale-label grep passed with no hits:

```bash
rg -n "Start in Mobile Sandbox|This phone as a box|Phone Backend|Mobile sandbox ->|developer can start in the mobile sandbox|Runs sandboxed|Phone sandbox" \
  mobile/app mobile/src web/app web/components web/lib tvos watch wear visionos README.md CLAUDE.md docs/architecture docs/releases \
  -g '!node_modules' -g '!dist' -g '!build'
```

## Autorun Check

The `yaver` wrapper on PATH tried to install `1.99.319` and hung at:

```text
Installing Yaver agent 1.99.319 for darwin-arm64...
```

I interrupted those wrapper processes and used the already-running local binary:

```bash
/Users/kivanccakmak/.yaver/bin/1.99.309/darwin-arm64/yaver --version
/Users/kivanccakmak/.yaver/bin/1.99.309/darwin-arm64/yaver autorun --help
/Users/kivanccakmak/.yaver/bin/1.99.309/darwin-arm64/yaver ops autorun_status --payload='{}'
```

Results:

- Running binary version: `yaver 1.99.309`
- `autorun` supports `-goal`
- `autorun_status` returned no active sessions

No autorun loop was started because the direct implementation pass completed and validated.

## Intentionally Still Present

These are kept by design:

- Low-level sandbox implementation files and tests remain in the repo.
- Backend/schema support for stored `phone_sandbox` placement rows remains for compatibility.
- Legacy mobile routes/components remain behind constants:
  - `SHOW_LOCAL_BOX_SURFACE = false`
  - `SHOW_PHONE_PROJECTS_SURFACE = false`
  - `KEEP_SANDBOX_SURFACE = false`
  - `SHOW_SANDBOX_UI = false`
- Browser iframe `sandbox` attributes and `web/lib/sandbox/*` internals remain because they are technical implementation terms, not product onboarding.
- Historical docs outside the edited architecture/release docs may still mention sandbox as past design/context.

Do not delete the legacy code unless the next task explicitly asks for removal. The request was to stop showing it and make the active product path remote-box-first.

## What Is Still Missing / Follow-Ups

1. Full app-wide visual QA
   - I did not run the mobile app, web app, tvOS app, watch app, Wear app, or visionOS app visually.
   - Claude Code should smoke the actual screens if this is going to ship immediately:
     - mobile Tasks/More/Settings/Infra
     - web dashboard Devices/Vibe/Feedback/Webview/Vibe Preview/Cloud/Settings
     - tvOS dashboard
     - watch/wear voice dispatch paths
     - visionOS dashboard if build tooling is available

2. Complete hidden-route cleanup
   - Old routes still exist:
     - `mobile/app/local-box.tsx`
     - `mobile/app/phone-projects.tsx`
     - `mobile/app/sandbox-ai.tsx`
     - `web/components/dashboard/PhoneProjectsView.tsx`
   - They are not normal entry points now, but deep links or direct imports can still reach compatibility screens.

3. MCP docs may need a generated tool-list refresh
   - Code now hides sandbox MCP tools and expands `create_task`.
   - If Yaver has generated MCP docs or screenshots elsewhere, regenerate them.

4. Autorun wrapper install hang
   - PATH `yaver` wrapper hung trying to install `1.99.319`.
   - The running agent binary `1.99.309` works.
   - This should be diagnosed separately if autorun is expected to run from the wrapper on this MacBook.

5. Cloud Workspace / Yaver Managed Cloud UX
   - The flow now points users toward self-hosted boxes or Yaver Managed Cloud.
   - If paid UI is hidden by `HIDE_PAID_UI`, confirm whether Managed Cloud should still be visible as “request/join cloud workspace” in launch builds.

6. Backend placement model still has legacy lane
   - `phone_sandbox` remains in Convex validators for backward compatibility.
   - New code should not create it. Add an explicit classifier/placement test if there is any function that might still choose it.

7. Full test suite not run
   - Only focused tests plus web/mobile TypeScript compile checks were run.
   - Full Go test suite, full Next build, Expo build, Xcode builds, and store deploy scripts were not run.

## Suggested Next Claude Code Prompt

Use this exact follow-up if needed:

```text
Continue from docs/handoff/remote-box-first-app-dev-2026-07-19.md. Do not revert the existing remote-box-first changes. Visually and mechanically audit every user-facing surface for the new Yaver model: Tasks + remote box + Yaver Git + Yaver Serverless + Hermes/WebRTC preview + Feedback SDK, with STT/TTS from mobile/watch/car/TV/AR-VR as control surfaces. Keep legacy sandbox code but hide it. Fix any remaining visible old sandbox/phone-backend routes, run the strongest practical validation, and update docs/tests where code and docs disagree.
```
