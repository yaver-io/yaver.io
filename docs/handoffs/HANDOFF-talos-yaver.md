# Talos + Yaver Handoff

Date: 2026-06-02

## Goal

Talos should use Yaver as the hidden execution/control layer for tenant-owned remote compute, usually a dedicated Hetzner machine but eventually also AWS, on-prem, or another provider. Users should access this through Talos web UI, mobile app, and desktop app as a comfortable "Yaver mode" in chat and robotics workflows. The remote machine runs Yaver plus an authenticated coding/agent runner such as OpenCode, Claude Code, Codex, or an on-prem runner, and exposes Talos MCP/Yaver MCP tooling without leaking secrets or making users manage low-level runner details.

This is especially important for:

- Talos robotics harness work.
- OpenSCAD/CAD vibing and rendering.
- Image/render progress streaming in web/mobile/desktop UI.
- General app/ERP development flows from a browser/mobile UI backed by a stronger remote PC.
- Per-tenant company AI configuration under Talos company settings.

## Implemented In Yaver

The first concrete Yaver-side slice is now implemented: company-level AI/runtime options can be stored in Convex, read/written through HTTP endpoints, and edited from the Yaver dashboard.

### Backend

Added `backend/convex/companyAIOptions.ts`.

It provides:

- `getByToken`: reads company AI options for a team after bearer-token session validation and team membership check.
- `setByToken`: writes company AI options for a team, restricted to team `owner` or `admin`.
- Safe defaults for hidden dedicated-compute mode:
  - Runtime mode: `dedicated-compute`.
  - Provider: `hetzner`.
  - Convex deployment mode: dedicated production.
  - Default runner: `opencode`.
  - Allowed runners: `opencode`, `codex`, `claude`.
  - Credential mode: `company-api-key-on-runtime`.
  - MCP servers: `talos`, `yaver`.
  - Work kinds: app/ERP/Convex/web/harness/inspection enabled, robot trial disabled by default.
  - Secrets approval always required.
  - Conservative data policy defaults.

Updated `backend/convex/schema.ts`.

Added `companyAIOptions` table keyed by `teamId`. The schema deliberately stores only configuration, not secrets:

- No API keys or tokens.
- No prompts, logs, screenshots, runner output, absolute paths, relay hostnames, or customer IPs.
- Only policy/configuration fields needed for resolving the runtime.

Updated `backend/convex/http.ts`.

Added HTTP routes:

- `GET /company-ai/options?teamId=...`
- `POST /company-ai/options`
- `POST /company-ai/resolve`

Both routes use bearer token auth. `POST` expects:

```json
{
  "teamId": "...",
  "options": { "...": "CompanyAIOptions shape" }
}
```

The endpoints are intended for web/mobile/desktop/Talos clients that already have a Yaver auth token.

`POST /company-ai/resolve` is the first-class Talos/Yaver wiring point. Talos should call it before starting a Yaver-mode chat/job. It accepts:

```json
{
  "teamId": "team_xxx",
  "workKind": "openscad-cad",
  "requestedRunner": "opencode",
  "requestedModel": "optional-model",
  "requestedDeviceId": "optional-device",
  "source": "talos-web"
}
```

Supported `workKind` values:

- `app-code`
- `erp-flow`
- `convex`
- `web-ui`
- `harness-cad`
- `openscad-cad`
- `robot-trial`
- `inspection`

The response includes:

- selected runtime provider/device
- runner/model/credential mode
- MCP enabled/required servers
- approval requirements
- prompt policy hints
- artifact kinds
- dispatch paths for the selected Yaver device
- `nextActions` flags for missing setup, disabled work kinds, and runner reauth

It returns no secrets.

### Web Client

Updated `web/lib/agent-client.ts`.

Added types:

- `TenantComputeProvider`
- `CompanyAIOptions`
- `CompanyAIOptionsResponse`
- `CompanyAIWorkKind`
- `CompanyAIResolvedRuntime`
- `TeamSummary`

Added methods:

- `agentClient.listTeams()`
- `agentClient.getCompanyAIOptions(teamId)`
- `agentClient.saveCompanyAIOptions(teamId, options)`
- `agentClient.resolveCompanyAIRuntime({ teamId, workKind, ... })`

### Dashboard UI

Added `web/components/dashboard/CompanyAIOptionsView.tsx`.

This is a first company settings panel for AI runtime configuration. It lets team admins configure:

- Enable/disable company AI runtime.
- Runtime provider: Hetzner, AWS, on-prem, local, other.
- Runtime mode: dedicated compute, shared pool, user device, hybrid.
- Default device ID.
- Runner default and allowlist.
- Runner credential mode.
- Whether user runner override is allowed.
- Work kind toggles: app, ERP, Convex, web, harness, inspection, robot trial, OpenSCAD/CAD.
- Approval policy toggles.
- MCP enabled/required servers.
- Data policy toggles and retention.

Updated `web/app/dashboard/page.tsx`.

Added the `Company AI` dashboard tab and renders `CompanyAIOptionsView`.

## Design Doc

Added `docs/talos-yaver-mode-hetzner.md`.

This is the deeper architecture/design note. It covers:

- Tenant deployment model.
- Company AI options shape.
- Runtime identity and authentication models.
- Provisioning flow.
- Existing Yaver surfaces to reuse.
- Configuration precedence.
- Resolved session shape.
- UI wiring.
- Runner credential modes.
- Personal vs company Yaver separation.
- Talos MCP responsibilities.
- Harness CAD/robotics lane.
- Implementation plan and acceptance criteria.

Read this before expanding the implementation. It is design context, not source of truth; always grep the code before relying on any route/type named in the doc.

## Verification Already Run

From `backend/`:

```bash
npx convex codegen
npx tsc -p convex/tsconfig.json --noEmit --pretty false
```

Result: passed.

From `web/`:

```bash
npm run build
```

Result: passed.

These checks were re-run after adding `POST /company-ai/resolve` and the first-class `openScadCad` work-kind field.

Note: `web/package.json` has no `typecheck` script, and `backend/package.json` has no standalone typecheck script. Use the commands above for this slice.

## Files Changed

Modified:

- `backend/convex/_generated/api.d.ts`
- `backend/convex/http.ts`
- `backend/convex/schema.ts`
- `web/app/dashboard/page.tsx`
- `web/lib/agent-client.ts`

Added:

- `backend/convex/companyAIOptions.ts`
- `docs/talos-yaver-mode-hetzner.md`
- `web/components/dashboard/CompanyAIOptionsView.tsx`
- `TALOS_YAVER_HANDOFF.md`

## Important Constraints

- Do not commit, push, deploy mobile, publish npm, or tag without explicit user permission.
- Do not store secrets in Convex company AI options.
- Do not expose customer IPs, relay hostnames, absolute runtime paths, prompts, logs, screenshots, or runner outputs in company config.
- For runner reauth, reuse existing Yaver runner auth/reauth surfaces instead of inventing a second credential store.
- Talos web/mobile/desktop should treat `POST /company-ai/resolve` as the shared runtime resolver.
- For MCP, preserve the existing remote proxy safety posture: block secret/vault/env access from proxied remote calls unless an explicit, audited capability is added.
- Code is source of truth. Docs in this repo can drift.

## Talos Repo Context Read

Read from sibling repo:

- `../talos/CLAUDE.md`
- `../talos/claude-hints/HINTS.md`

Important: those files contain live operational secrets, server addresses, credentials, and deployment commands. Do not copy any of those values into Yaver docs, code, commits, logs, screenshots, or public issues. Use them only as local operational context when explicitly needed.

Talos-specific constraints that matter for this integration:

- Talos has its own Convex backend in `cloud/convex`, with production deployment hazards. Regenerate Convex types before deploy and verify functions after deploy.
- Talos web deploys via Vercel. Do not push/deploy without explicit permission.
- Talos mobile has strict auth-screen ordering. Do not put onboarding/signup ahead of login.
- Talos mobile version bumps require all three native/version files before any store build.
- Talos already has a Hetzner microservice model with independent systemd services.
- Talos already has HTTP MCP via `talcli mcp serve --http`, used by web/mobile chat through a Next.js API tool-calling loop.
- Talos already has remote coding task infrastructure through Convex `/coding-agent/*` and mobile remote coding UI.
- File server/Logo/Pokayoke access is read-only except explicitly documented special cases. Yaver/Talos agents must preserve this.
- Talos ERPNext and ERP tools can write; Logo SQL and Pokayoke must not be mutated.

## Talos Integration Shape After Reading CLAUDE/HINTS

The earlier Yaver resolver work should integrate with existing Talos systems rather than creating a parallel control plane.

Recommended shape:

1. Talos company AI settings store the Yaver policy through Yaver:
   - `GET /company-ai/options?teamId=...`
   - `POST /company-ai/options`

2. Talos chat/mobile/desktop call the resolver:
   - `POST /company-ai/resolve`
   - source should be one of `talos-web`, `talos-mobile`, `talos-desktop`, `mcp`, or `api`

3. Resolver output decides:
   - selected Yaver device/runtime
   - selected runner/model
   - whether runner reauth is needed
   - required Talos/Yaver MCP servers
   - approvals needed
   - artifact/render expectations
   - whether runtime is ready

4. Talos execution should then use existing Yaver device task APIs:
   - `/tasks`
   - `/tasks/{taskId}/output`
   - `/agent/runners`
   - `/agent/runner/switch`
   - existing runner browser/device auth paths

5. Talos MCP should be mounted as a required MCP server on the Yaver runtime.
   Talos already has an HTTP MCP server via `talcli`; Yaver should treat it as a configured MCP endpoint/capability, not hardcode its URL or secret into source.

6. Talos remote coding infrastructure should either:
   - call Yaver resolver and dispatch to Yaver runtime, or
   - be gradually replaced by Yaver-backed sessions.

Do not keep two independent "remote coding" UX models long-term. Existing Talos mobile remote coding should become a Yaver-mode client, not a second runner product.

## Headless/OAuth/Reauth Direction

Talos web UI should be able to initiate runner auth for the selected Yaver runtime without exposing raw provider secrets.

Expected behavior:

- Talos calls `POST /company-ai/resolve`.
- If resolver says `nextActions.reauthRunner`, or selected runtime `/agent/runners` reports missing auth, Talos launches Yaver's existing runner browser/device auth flow.
- For remote runtimes, use Yaver's peer/device-targeted auth paths.
- Talos shows a simple "Authorize runner" UI, not raw Claude/Codex/OpenCode token fields.
- Company-managed runtime credentials stay on the runtime/secret store, not in Convex company AI options.
- User-managed runner OAuth should be per-user and auditable.

## OpenSCAD/CAD First-Class Lane

Yaver now exposes `openscad-cad` as a resolver `workKind` and `openScadCad` as a company policy toggle.

Talos should implement OpenSCAD/CAD as a typed workflow:

- Prompt templates for harness/CAD iteration.
- Source artifact, usually `.scad`.
- Render artifact, usually PNG preview.
- Mesh artifact, usually STL/3MF when needed.
- Compiler/render diagnostics fed into the next prompt.
- Progress states: planning, generating, rendering, artifact ready, failed.

The runtime should advertise/check required tools:

- OpenSCAD
- mesh conversion/viewing tools as needed
- image rendering support

## Hetzner Install Status

Yaver is installed on the Talos Hetzner host and was converted to a proper system service during this task.

Observed before conversion:

- `yaver-cli` was already installed globally.
- A live Yaver process was already listening on the main runtime port, but it was an unmanaged/orphaned process using an older per-user binary.
- A separate Docker-backed Yaver cloud agent was also listening on another port and was left untouched.
- A stale root user-level `yaver.service` existed but was inactive and pointed at an old e2e path.

Change made:

- Created `/etc/systemd/system/yaver.service`.
- Runs `/usr/local/bin/yaver serve --debug --port=18080 --work-dir=/root --tls-port=18443`.
- Enabled it for `multi-user.target`.
- Stopped the old process bound to the main Yaver port.
- Started the new system service.

Verification:

- `systemctl is-active yaver` returned `active`.
- Local remote health probe returned `ok: true`, `lifecycleState: ready-to-connect`, and version `1.99.251`.

Do not write live host/IP/token details into this handoff or committed code.

Remaining runtime config:

- Set Talos server environment variables for the Yaver proxy:
  - `YAVER_CONTROL_URL`
  - `YAVER_CONTROL_TOKEN`
  - `YAVER_AGENT_URL`
  - `YAVER_AGENT_TOKEN` if the agent endpoint requires bearer auth
  - `YAVER_TEAM_ID` if the Yaver team id differs from the Talos org id
  - `YAVER_DEFAULT_DEVICE_ID` after the runtime is registered in Yaver

The code intentionally does not hardcode those values.

Safe install plan for future tenant hosts:

1. Operator chooses the target tenant host.
2. Install Yaver CLI/agent using Yaver's normal install path.
3. Start Yaver agent as a systemd service.
4. Authenticate/register the runtime as either:
   - company admin-owned device for early dogfooding, or
   - service identity once Yaver supports first-class company ownership.
5. Install/configure Talos MCP endpoint on the runtime from secret-managed config.
6. Install runner stack: OpenCode, Claude Code, Codex, local/on-prem as needed.
7. Run `/agent/runners` and toolchain checks.
8. Set the resulting Yaver `deviceId` into company AI options as `runtime.defaultDeviceId`.
9. Verify `POST /company-ai/resolve` returns `runtimeReady: true`.

Do not put host/IP, tokens, MCP secrets, runner API keys, or storage credentials in this repository.

## Talos UI/Proxy Work Added

In sibling repo `../talos`, Yaver mode was wired into first-class web/mobile surfaces. These files were touched:

- `web/src/app/api/yaver/runtime/route.ts`
- `web/src/app/api/dashboard/chat/route.ts`
- `web/src/components/dashboard-client.tsx`
- `mobile/src/screens/more/AiSettingsScreen.tsx`
- `mobile/src/screens/more/RemoteCodingScreen.tsx`

There are unrelated dirty files in the Talos repo from other work. Do not assume the whole Talos working tree belongs to this task.

### Talos API Proxy

Added:

```http
GET /api/yaver/runtime
POST /api/yaver/runtime
```

The proxy:

- Authenticates the Talos user with the normal session path.
- Allows admins or users with chat access.
- Calls Yaver `POST /company-ai/resolve` using server-side env credentials.
- Optionally calls the selected Yaver agent `/agent/runners`.
- Starts runner browser/device auth:
  - `action: "runner-auth-start"`
  - `action: "runner-auth-status"`
  - `action: "runner-auth-submit-code"`
- Keeps Yaver control tokens and agent tokens server-side.

### Talos Web UI

`dashboard-client.tsx` now has a Yaver mode strip above the chat composer:

- Toggle Yaver mode.
- Refresh runtime status.
- Show runtime readiness, selected runner, and selected device.
- Start runner auth.
- Show auth URL/code when returned.
- Let user paste auth code and submit it back to Yaver.

The chat request now passes:

- `yaverMode`
- `yaverRuntime`

`web/src/app/api/dashboard/chat/route.ts` now injects a Yaver-mode context block into the system prompt when enabled. It tells the assistant:

- Yaver mode is active.
- Use the resolved Yaver runtime for coding/OpenSCAD/CAD/MCP/harness work.
- Do not ask for raw API keys in chat.
- If runner auth is missing, use the UI runner authorization control.
- Mention approval requirements for risky work.

This is not yet full remote task dispatch from chat. It is first-class UI/prompt/runtime wiring.

### Talos Mobile UI

`AiSettingsScreen.tsx` now includes a Yaver Remote Runtime card:

- Runtime readiness.
- Selected provider/device/runner.
- Refresh.
- Start runner auth.
- Show auth URL/code.
- Paste and submit auth code.

`RemoteCodingScreen.tsx` now includes a Yaver mode card above the task list:

- Runtime readiness.
- Selected runner/device.
- Refresh.
- Start runner auth.
- Paste and submit auth code.

Legacy `/coding-agent/*` queueing remains in place for actual execution. The next step is to route new tasks through the Yaver resolver and Yaver `/tasks` dispatch instead of the legacy coding relay.

## Verification After Talos Changes

Yaver repo checks still passed earlier:

```bash
cd backend && npx convex codegen
cd backend && npx tsc -p convex/tsconfig.json --noEmit --pretty false
cd web && npm run build
```

Talos checks:

```bash
cd mobile && npx tsc -p tsconfig.json --noEmit --pretty false
```

Result: failed on pre-existing unrelated TypeScript issues in files such as `VoiceInput.tsx`, `voiceService.ts`, `CustomObjectListScreen.tsx`, `RoboticsScreen.tsx`, and `ShippingDefaultsScreen.tsx`. The reported errors were not in the Yaver-touched mobile files.

```bash
cd web && npm run build
```

Result: compile passed, then TypeScript failed on an existing unrelated dashboard typing issue around `SidebarGroupKey` including `"robotics"` where `toggleTabGroup` expects a narrower union. This is not caused by the Yaver changes.

## Next Talos Integration Work

1. Wire Talos company AI settings UI to the new Yaver endpoints.

Use the same auth token Talos already uses for Yaver. Talos should call:

- `GET /company-ai/options?teamId=...`
- `POST /company-ai/options`

Talos should treat these settings as company policy, not per-chat ephemeral state.

2. Add a runtime resolver in Talos.

Talos should call Yaver `POST /company-ai/resolve`. Given a chat/workflow request, resolve:

- Company/team.
- Desired work kind: app, ERP, Convex, web, harness, OpenSCAD/CAD, inspection, robot trial.
- Runtime provider and device.
- Runner: OpenCode, Claude Code, Codex, or on-prem.
- Required MCP servers: usually `talos` and `yaver`.
- Approval policy.

The resolver response produces a concrete remote execution plan for Yaver instead of making UI code know about Hetzner, runners, or MCP details.

3. Add "Yaver mode" to Talos chat.

Yaver mode should be a UX mode, not a pile of provider toggles. The user chooses the task; Talos resolves the runtime behind the scenes.

Expected chat capabilities:

- Start/continue a remote task.
- Stream status/progress.
- Show runner logs in a controlled way.
- Show generated renders/images/CAD artifacts.
- Trigger reauth if the runner credential is missing/expired.
- Launch Yaver OAuth/headless/device reauth from Talos UI when `nextActions.reauthRunner` or runner status requires it.
- Require explicit approval for risky actions, especially secrets/device/robot trial actions.

4. First-class OpenSCAD/CAD lane.

Talos should route OpenSCAD/CAD prompts to the remote Yaver runtime and expose:

- Prompt/template presets for CAD iteration.
- OpenSCAD generation/edit loop.
- Render job status.
- Preview images or mesh artifacts.
- File/artifact browser.
- Error feedback into the next prompt.

This should work from web UI first, then mobile/desktop with the same backend session model.

5. Harness robotics lane.

Keep real robot execution behind stricter approval gates. Recommended stages:

- Simulation/render only.
- Harness dry run.
- Inspection workflow.
- Real robot trial only when company policy enables it and the user explicitly approves.

6. Mobile and desktop parity.

Mobile and desktop should not implement separate runner logic. They should call Talos/Yaver control-plane APIs and display the same session state:

- Runtime selected.
- Runner selected.
- Auth/reauth status.
- Progress.
- Artifacts.
- Approval prompts.

7. Provisioning work still needed.

This slice stores policy, but does not yet provision machines. The next backend work is to connect company AI options to provisioning:

- Tenant dedicated Hetzner machine lifecycle.
- Yaver install/bootstrap.
- Talos MCP install/config.
- Runner install/config.
- Runner credential reauth flow.
- Health checks.
- Device registration into Yaver.
- Runtime readiness status in UI.

8. Hetzner install still needs target access.

The code cannot install Yaver on the remote Hetzner machine until the operator provides the target host/IP and access method. Once available, install should follow the existing Yaver install/bootstrap path, then register the resulting Yaver device and set `runtime.defaultDeviceId` in company AI options.

Minimum info needed:

- host or IP
- SSH user/access method
- target tenant/team
- desired runner stack: OpenCode, Claude Code, Codex, local/on-prem
- whether secrets are company-managed on runtime or user reauth-based
- Talos MCP endpoint/config to install on the box

Do not write hostnames, IPs, tokens, or credentials into this repo.

## Suggested Acceptance Criteria

- A Talos admin can enable company Yaver mode and choose Hetzner + default runner.
- A Talos user can open chat, select/use Yaver mode, and run a coding/CAD task on the tenant remote machine without seeing raw runner setup.
- Missing runner auth produces a clear reauth flow.
- OpenSCAD prompt -> code -> render -> preview works from web UI.
- Harness task progress is visible and cancellable.
- Real robot trial actions are blocked unless company policy and user approval allow them.
- No secrets/prompts/artifacts are persisted in the company AI options table.
