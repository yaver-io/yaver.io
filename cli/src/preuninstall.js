#!/usr/bin/env node
// npm preuninstall hook — best-effort call to `yaver mcp unregister` so the
// CLI cleans up the MCP entries it added to Claude Desktop / Claude Code /
// Codex / Cursor / VS Code / Windsurf / Zed before the binary is removed.
//
// Must NEVER fail the uninstall. If yaver is not on PATH, silently succeed.

const { spawnSync } = require("node:child_process");

try {
  const res = spawnSync("yaver", ["mcp", "unregister"], {
    stdio: "inherit",
    timeout: 15_000,
  });
  if (res.error && res.error.code === "ENOENT") {
    // yaver not on PATH — nothing to clean up.
    process.exit(0);
  }
  // Ignore exit code; we always succeed so npm uninstall can finish.
  process.exit(0);
} catch {
  process.exit(0);
}
