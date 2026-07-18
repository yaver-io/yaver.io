# Yaver MCP submission readiness

Last checked: 2026-07-05.

## Current official submission path

Yaver is ready for MCP ecosystem discovery as a local stdio MCP server:

- Official MCP Registry package file: `server.json`
- Registry name: `io.github.yaver-io/yaver`
- NPM package: `yaver-cli`
- Transport: `stdio`
- Install command: `npx -y yaver-cli yaver-mcp`
- Public docs: `https://yaver.io/docs/mcp`
- Public well-known metadata:
  - `https://yaver.io/.well-known/mcp/server.json`
  - `https://yaver.io/.well-known/mcp/server-card.json`
  - `https://yaver.io/.well-known/mcp.json`
  - `https://yaver.io/.mcp.json`

The repository already contains `.github/workflows/publish-mcp-registry.yml`, which publishes official MCP Registry metadata with GitHub OIDC and audits public discoverability.

## OAuth status

Yaver is OAuth aware for the local/server package:

- `YAVER_HEADLESS=1` forces device-code OAuth for headless or sandboxed environments.
- The MCP package metadata documents resumable headless OAuth.
- The app contains OAuth provider support for account sign-in and runner/auth flows.
- The desktop agent exposes authenticated local HTTP MCP under `/mcp` for paired devices.

## Directory connector status

Yaver is not ready for direct ChatGPT or Claude remote connector directory submission as of this check.

Reason: the codebase currently publishes a stdio MCP package and a paired-device local HTTP MCP surface. It does not expose a production hosted HTTPS MCP connector with:

- OAuth authorization-server metadata for MCP clients.
- OAuth protected-resource metadata for the MCP resource.
- Dynamic client registration for ChatGPT/Claude.
- A remote directory-safe tool allowlist.
- A consent screen describing remote AI-host access to a user's Yaver machines.
- Per-host revocation of remote MCP access.

Submitting the current Yaver stdio package as a remote ChatGPT/Claude connector would be inaccurate. The correct official route today is MCP Registry/local package discovery, plus Claude desktop-extension packaging if desired.

## Remote connector MVP required before directory submission

Build a separate hosted connector, not a thin public wrapper over the full local `/mcp` surface.

Recommended v1 tool allowlist:

- `list_machines`
- `machine_status`
- `list_projects`
- `runner_status`
- `start_dev_server`
- `stop_dev_server`
- `reload_app`
- `git_status`
- `create_session`
- `session_status`

Do not expose shell execution, vault/secrets, raw filesystem editing, deployment, TestFlight/Play Store publishing, token export, or arbitrary tunnel creation in the directory connector.

Required OAuth pieces:

- `/.well-known/oauth-authorization-server`
- `/.well-known/oauth-protected-resource`
- `POST /api/oauth/register`
- `GET /api/oauth/authorize`
- `POST /api/oauth/token`
- `POST /api/oauth/revoke`
- Consent UI that names the host and target machine permissions.
- Connected-app revocation UI.
- Per-token scopes and server-side enforcement.

## Pre-submit audit for current local package

Run:

```bash
scripts/mcp-discoverability-audit.sh
```

Expected successful checks:

- Local well-known files are synced.
- NPM `mcpName` is `io.github.yaver-io/yaver`.
- Official MCP Registry lists the current package version.
- Public metadata URLs are reachable.

## Submission position

Use this language if asked by a directory reviewer today:

```text
Yaver is currently submitted through the official MCP Registry as a local stdio MCP server. We are not requesting ChatGPT/Claude remote connector directory listing until the hosted OAuth connector and remote-safe tool allowlist are implemented.
```
