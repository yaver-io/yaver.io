# Yaver ChatGPT / OpenAI Apps Readiness

Date: 2026-07-05

## 2026-07-05 Submission Status

Yaver was submitted for OpenAI app review on 2026-07-05.

Submitted hosted MCP URL:

```text
https://yaver.io/api/mcp
```

Authentication:

```text
No Auth
```

Public directory subtitle:

```text
Set up local dev MCP
```

Hosted tools submitted:

- `yaver_codex_setup`
- `yaver_mcp_package_info`
- `yaver_project_bootstrap_guide`
- `yaver_privacy_summary`

Domain verification:

```text
https://yaver.io/.well-known/openai-apps-challenge
```

The hosted ChatGPT app is intentionally narrow and read-only. It provides setup guidance and public package/privacy information. Full local machine control remains in the user's local Yaver MCP server and Codex plugin.

## Current State

Yaver is ready for local Codex / Claude Code / OpenCode MCP use through stdio:

```bash
codex mcp add yaver -- npx -y yaver-cli yaver-mcp
claude mcp add --scope user yaver -- npx -y yaver-cli yaver-mcp
```

The repo now also contains a Codex plugin bundle:

- `plugins/yaver/.codex-plugin/plugin.json`
- `plugins/yaver/.mcp.json`
- `plugins/yaver/skills/yaver/SKILL.md`
- `.agents/plugins/marketplace.json`

That plugin declares the same `npx -y yaver-cli@latest yaver-mcp` server and adds Codex guidance to start with `yaver_lazy_setup`.

## OpenAI App Scope

The first hosted OpenAI app submission is a setup connector, not a remote machine-control connector.

OpenAI Apps submission requires a concrete working MCP Server URL for review, not a placeholder. Official submission docs: https://developers.openai.com/apps-sdk/deploy/submission

Production now exposes `https://yaver.io/api/mcp` as a no-auth, public, read-only MCP endpoint. It deliberately does not expose the full local stdio MCP surface.

## Recommended Hosted App Shape

For a future higher-value ChatGPT app, build a separate authenticated hosted MCP endpoint:

```text
https://yaver.io/api/mcp
```

That future authenticated app must not expose Yaver's full local agent surface. Keep authenticated v2 read-only and low-risk:

- `yaver_machines_list`
- `yaver_machine_status`
- `yaver_projects_list`
- `yaver_project_status`
- `yaver_runner_status`
- `yaver_sessions_list`
- `yaver_session_status`
- `yaver_dev_server_status`

Do not expose in v1:

- shell execution
- file reads/writes
- vault or credential access
- deploy/publish/tag operations
- destructive cleanup
- tunnel/proxy raw access
- arbitrary local agent forwarding

## Required Implementation Before Submission

1. Add hosted MCP route at `web/app/api/mcp/route.ts`.
2. Add OAuth protected-resource metadata at `web/app/.well-known/oauth-protected-resource/route.ts`.
3. Add OAuth authorization-server metadata at `web/app/.well-known/oauth-authorization-server/route.ts`.
4. Reuse or implement authorization-code + PKCE endpoints for ChatGPT connector login.
5. Validate every bearer token on every MCP request.
6. Add output schemas for every tool.
7. Add OpenAI annotation justifications for read-only / destructive / open-world flags.
8. Create a reviewer account with seeded non-sensitive demo devices/projects/sessions.
9. Add a privacy note explaining that Yaver does not send source code through the hosted connector.

## Product Positioning

Submit the hosted app as `Yaver`, not as a generic MCP proxy.

Description:

> Yaver connects ChatGPT to a developer's own Yaver workspace so verified users can inspect paired machines, project status, runner state, and mobile development loop health. Source code stays on the user's own machine; the hosted connector exposes only scoped operational status tools.

## Separate Track: Codex Desktop Plugin

Codex Desktop users do not need the hosted ChatGPT app. The better first-class path is the plugin:

```text
plugins/yaver
```

That plugin is the correct package for users who downloaded Codex Desktop and want to use Yaver MCP locally.
