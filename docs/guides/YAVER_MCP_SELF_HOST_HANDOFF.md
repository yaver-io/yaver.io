# Yaver MCP Self-Hosted First-Capture Handoff

Date: 2026-05-20
Branch: `fix/yaver-cloud-per-tenant-isolation`

## Purpose

The product goal is to reduce Yaver adoption friction for normal users and developers:

- A user opens Claude Code, Codex, or another coding agent from a terminal.
- The agent discovers/installs Yaver MCP through `npx -y yaver-cli yaver-mcp`.
- The agent runs `yaver_lazy_setup` so the human signs in from their phone.
- After sign-in, the agent captures the user with a self-hosted starter monorepo first.
- The starter stack is `Convex + Cloudflare + Expo React Native + web UI + shared package`.
- The user can test the mobile app through Yaver mobile via Hermes reload.
- Managed Cloud is introduced later, only after the repo exists and the user wants an always-on hourly machine.

The main funnel is:

```text
agent terminal -> install Yaver MCP -> yaver_lazy_setup -> project_self_host_create
  -> mobile_hermes_doctor -> mobile_project_prepare -> mobile_project_build
  -> user tests on Yaver mobile -> optional managed cloud upsell
```

## What Was Done

### MCP discoverability

Pushed commits:

- `78ac2b47 Improve MCP discoverability`
- `2a388c36 Shorten MCP registry description`
- `ff3cce4b Polish MCP first-capture discovery`

Public discovery now exists at:

- `https://yaver.io/llms.txt`
- `https://yaver.io/.well-known/mcp.json`
- `https://yaver.io/.well-known/mcp.llmfeed.json`
- `https://yaver.io/.well-known/mcp/server.json`
- `https://yaver.io/.mcp.json`

Official MCP Registry:

- `io.github.yaver-io/yaver@1.99.219` is active.
- `scripts/publish-mcp-registries.sh --official` was made idempotent for duplicate active versions.

Glama:

- Listing exists: `https://glama.ai/mcp/servers/kivanccakmak/yaver.io`
- Glama API still looked stale during the last check: old env/tools metadata.

Smithery:

- Yaver was still not visible in Smithery search during the last check.

### Self-hosted monorepo creation

Pushed commit:

- `fe45160b Add self-hosted MCP project creation`

Added MCP tool:

- `project_self_host_create`

It creates a full self-hosted-first monorepo:

- `apps/web` Next.js web UI with Cloudflare Worker config.
- `apps/landing` static landing site.
- `apps/mobile` Expo React Native app for iOS and Android.
- `backend/convex` local Convex backend, deployable later.
- `packages/shared` shared TypeScript utilities/types.
- `.yaver/config.yaml` and `.yaver/services.yaml`.
- Starter legal, privacy, app-review, App Store, and Play Store files.

Also fixed the one-shot MCP wizard path so it finishes with sane defaults instead of waiting on unanswered wizard fields.

### Hermes/RN mobile reload doctor

Pushed commit:

- `4c9be58f Add MCP Hermes reload doctor`

Added MCP tool:

- `mobile_hermes_doctor`

Purpose:

- Make the most common mobile use case agent-friendly: “Can this Expo/React Native app reload into my Yaver phone app?”

What it checks:

- Resolves `apps/mobile`, `mobile/`, `app/`, or nested monorepo app paths.
- Confirms detected framework is Expo or React Native.
- Checks local tools and package manager readiness.
- Checks dependency install state.
- Checks Hermes compiler readiness.
- Checks prior Hermes bundle build state.
- Runs native-module compatibility via existing Yaver compatibility code.
- Returns `nextActions`, usually `mobile_project_prepare` then `mobile_project_build`.

Files added/changed:

- `desktop/agent/mcp_mobile_hermes_doctor.go`
- `desktop/agent/mcp_mobile_hermes_doctor_test.go`
- `desktop/agent/httpserver.go`
- `desktop/agent/mcp_tools.go`
- `desktop/agent/agent_test.go`
- `web/public/llms.txt`
- `web/public/.well-known/mcp.json`
- `web/public/.well-known/mcp.llmfeed.json`
- `web/public/.well-known/mcp/server-card.json`
- `web/app/docs/mcp/page.tsx`
- `scripts/sync-mcp-discovery-files.sh`

### Web deploy

Deployed Cloudflare web after the Hermes doctor docs/discovery change.

Current deployed version:

- `e61d5e09-32ef-4d1b-a606-e3dbecb2db34`

Live verification passed:

```bash
curl -fsSL https://yaver.io/llms.txt | rg -n "mobile_hermes_doctor|project_self_host_create"
curl -fsSL https://yaver.io/.well-known/mcp.json | jq -r '.prompts[]' | rg "mobile_hermes_doctor|project_self_host_create"
curl -fsSL https://yaver.io/.well-known/mcp.llmfeed.json | jq -r '.first_capture_tool, .phone_reload_doctor_tool'
```

Expected live output includes:

```text
project_self_host_create
mobile_hermes_doctor
```

### npm metadata release preparation

Pushed commit:

- `37084ed4 Prepare yaver CLI npm metadata release`

Prepared:

- `cli/package.json` -> `1.99.220`
- `cli/package-lock.json` -> `1.99.220`
- `versions.json` index version -> `cli: 1.99.220`
- `server.json` -> MCP package version `1.99.220`
- `web/public/.well-known/mcp/server.json` -> MCP package version `1.99.220`

Dry-run passed:

```bash
cd cli && npm publish --dry-run
```

Dry-run package:

- `yaver-cli@1.99.220`
- package size about `3.4 MB`
- unpacked size about `12.8 MB`

## Validation Already Run

Go focused tests:

```bash
cd desktop/agent
go test -run 'TestMobileHermesDoctor|TestMCPInitializeAndToolsList|TestMCPSelfHostedProjectCreateGeneratesFullMonorepo'
go test -run '^$'
```

Web type check:

```bash
cd web && npx tsc --noEmit
```

JSON/discovery sync:

```bash
./scripts/sync-mcp-discovery-files.sh
jq empty cli/package.json cli/package-lock.json versions.json server.json \
  web/public/.well-known/mcp/server.json \
  web/public/.well-known/mcp.json \
  web/public/.well-known/mcp.llmfeed.json \
  web/public/.mcp.json
```

## What Is Not Done Yet

### Live npm publish is blocked

This machine is not logged into npm.

Observed:

```bash
npm whoami
# npm error code E401
# npm error 401 Unauthorized - GET https://registry.npmjs.org/-/whoami
```

Also:

```bash
echo "$NPM_TOKEN"
# empty
```

So `npm publish` was not run live. Only `npm publish --dry-run` was run.

### MCP Registry version `1.99.220` not published yet

`scripts/publish-mcp-registries.sh --dry-run --version 1.99.220` prepared the correct `server.json`, but failed npm propagation because `yaver-cli@1.99.220` is not live yet:

```text
Checking npm propagation...
curl: (56) The requested URL returned error: 404
```

This is expected until npm publish succeeds.

### Web discovery for `server.json` version `1.99.220` not deployed after metadata bump

The web deploy was completed for commit `4c9be58f`.

Then commit `37084ed4` prepared `1.99.220` npm/MCP metadata, but because npm publish is blocked, the final web deploy for `server.json` version `1.99.220` should wait until npm is live.

Do not deploy `1.99.220` MCP server metadata publicly before npm has `yaver-cli@1.99.220`, otherwise registry crawlers may see a package version that 404s.

## Immediate Next Steps

### 1. Authenticate npm

Either log in interactively:

```bash
npm login
npm whoami
```

Or provide token in env:

```bash
export NPM_TOKEN=...
npm whoami
```

Expected result:

```text
<npm username>
```

### 2. Publish npm package

Only after npm auth works:

```bash
cd cli
npm publish
```

Then verify:

```bash
npm view yaver-cli version keywords mcpName --json
```

Expected:

```json
{
  "version": "1.99.220",
  "mcpName": "io.github.yaver-io/yaver"
}
```

Also check that keywords include MCP/discovery terms from `cli/package.json`.

### 3. Publish official MCP Registry metadata

After npm propagation:

```bash
MCP_VERSION=1.99.220 ./scripts/publish-mcp-registries.sh --official
```

Then verify registry status:

```bash
mcp-publisher status --status active io.github.yaver-io/yaver 1.99.220
```

### 4. Deploy web discovery for `1.99.220`

Use a clean worktree to avoid unrelated dirty files:

```bash
DEPLOY_DIR=/tmp/yaver-web-deploy.dkuRVd
git -C "$DEPLOY_DIR" fetch github fix/yaver-cloud-per-tenant-isolation
git -C "$DEPLOY_DIR" checkout --detach 37084ed4
./scripts/deploy-web.sh
```

Run from the deploy worktree, not the dirty main worktree:

```bash
cd "$DEPLOY_DIR"
./scripts/deploy-web.sh
```

Then verify:

```bash
curl -fsSL https://yaver.io/.well-known/mcp/server.json | jq -r '.version, .packages[0].version'
curl -fsSL https://yaver.io/.well-known/mcp.llmfeed.json | jq -r '.first_capture_tool, .phone_reload_doctor_tool'
```

Expected:

```text
1.99.220
1.99.220
project_self_host_create
mobile_hermes_doctor
```

## Dirty Worktree Warning

The repo has many unrelated dirty files from managed-cloud/mobile work. Do not revert them.

Known unrelated dirty examples include:

- `.github/workflows/build-yaver-cloud-image.yml`
- `backend/convex/*`
- `desktop/agent/cloud_stopstart.go`
- `desktop/agent/command_events.go`
- `desktop/agent/glm_loop.go`
- `mobile/*`
- `web/components/dashboard/*`
- `YAVER_CLOUD_HANDOFF.md`
- managed-cloud docs/scripts

Important:

- Only stage files relevant to the task.
- Do not run `git reset --hard`.
- Do not run destructive cleanup.
- `versions.json` still has an unstaged user/other-work change:

```diff
"mobile": "1.18.121" -> "1.18.122"
```

The committed npm metadata release only staged the `cli` version bump, preserving the existing mobile dirty change unstaged.

## Current Commits To Know

Latest branch history:

```text
37084ed4 Prepare yaver CLI npm metadata release
4c9be58f Add MCP Hermes reload doctor
ff3cce4b Polish MCP first-capture discovery
fe45160b Add self-hosted MCP project creation
```

## Product Notes

Yaver is now differentiated in the MCP flow by:

- Agent-first install path with `npx -y yaver-cli yaver-mcp`.
- Phone-first sign-in through `yaver_lazy_setup`.
- Self-hosted-first app creation via `project_self_host_create`.
- Mobile Hermes reload readiness via `mobile_hermes_doctor`.
- Optional paid managed cloud later, not forced before the user has a repo.

The strongest next product improvement is to make Claude Code/Codex prompts even more explicit:

```text
Install Yaver MCP from https://yaver.io/llms.txt, call yaver_lazy_setup,
then create a self-hosted app with project_self_host_create. After that,
run mobile_hermes_doctor on apps/mobile and follow its nextActions.
Do not ask me to run npm install -g yaver-cli or yaver auth manually.
```

