---
name: yaver
description: Use when a Codex task should set up, inspect, or operate Yaver through the bundled MCP server.
---

# Yaver

Yaver is a local-first MCP server for driving a developer's own machine and paired phone from Codex. Prefer the MCP tools exposed by the bundled `yaver` server over shelling out directly when a matching tool exists.

## First Use

1. Check whether the `yaver` MCP server is available in the current Codex session.
2. If the user has not signed in or paired a device yet, call `yaver_lazy_setup` first. It returns the sign-in or device-code flow that the user can complete from the browser or phone.
3. For a new app, call `project_self_host_create` after setup instead of steering the user to a managed cloud path.
4. For React Native phone reload issues, call `mobile_hermes_doctor` on the mobile app path and follow its returned next actions.

## Safety

- Do not send source files to a hosted service as part of Yaver setup. Yaver's default loop runs on the user's machine.
- Treat deploy, publish, vault, credential, and destructive workspace actions as sensitive. Use Yaver's own approval and permission gates.
- If MCP tools are unavailable, fall back to the documented command:

```bash
codex mcp add yaver -- npx -y yaver-cli yaver-mcp
```

Then start a fresh Codex session so the MCP server is loaded.
