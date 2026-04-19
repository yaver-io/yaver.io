#!/usr/bin/env node
// npm preuninstall hook — best-effort cleanup before the binary is removed.
//
// Two cleanup steps, both fire-and-forget. Order matters: notify Convex
// FIRST while the agent process still has its token + deviceId in memory,
// THEN unregister MCP integrations.
//
//   1. `yaver wipe --including-auth --yes` — stops the running agent and
//      deletes the device record from Convex so mobile / web stop showing
//      it as live the moment npm uninstall completes (instead of waiting
//      90 s for the heartbeat-staleness gate). Skipped via env
//      YAVER_SKIP_PREUNINSTALL_WIPE=1 if a user wants `npm uninstall +
//      reinstall` without losing their device row.
//
//   2. `yaver mcp unregister` — removes Claude Desktop / Claude Code /
//      Codex / Cursor / VS Code / Windsurf / Zed entries.
//
// Must NEVER fail the uninstall. If yaver is not on PATH, silently succeed.

const { spawnSync } = require("node:child_process");

function envEnabled(name) {
  const raw = String(process.env[name] || "").trim().toLowerCase();
  return raw === "1" || raw === "true" || raw === "yes";
}

function safeRun(cmd, args, timeoutMs) {
  try {
    const res = spawnSync(cmd, args, {
      // Send output to stderr so npm's progress bar isn't disturbed.
      stdio: ["ignore", "ignore", "inherit"],
      timeout: timeoutMs,
    });
    if (res.error && res.error.code === "ENOENT") return false;
    return true;
  } catch {
    return false;
  }
}

if (!envEnabled("YAVER_SKIP_PREUNINSTALL_WIPE")) {
  // 8 s budget: stops agent (~500 ms), tells Convex (~5 s shutdown
  // timeout), wipes ~/.yaver. Plenty of slack but bounded so a slow
  // network never strands `npm uninstall`.
  safeRun("yaver", ["wipe", "--including-auth", "--yes"], 8_000);
}

safeRun("yaver", ["mcp", "unregister"], 15_000);

process.exit(0);
