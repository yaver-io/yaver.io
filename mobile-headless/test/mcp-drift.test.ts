// mcp-drift.test.ts — the reverse of drift.test.ts.
//
// drift.test.ts catches changes to mobile/src/lib/* that break the
// headless surrogate. This file catches a different drift: a new
// `yaver <verb>` CLI command that ships without an MCP equivalent,
// leaving MCP callers unable to invoke it. Since the primary tool
// and the MCP surface must stay in lockstep (see YAVER_MCP_COVERAGE.md
// "Design principles" §4), this guard pays for itself the moment
// someone forgets to wire up the MCP case for a new CLI command.
//
// Methodology:
//   1. Read desktop/agent/main.go and collect every top-level
//      `case "<cmd>":` line in the CLI router.
//   2. Read desktop/agent/mcp_tools.go and collect every registered
//      MCP tool name.
//   3. A CLI command passes iff:
//        - an MCP tool has an exact matching name (e.g. "handoff"
//          -> "session_handoff" is NOT exact — that's fine because
//          it's in the matching-by-related rule below), OR
//        - an MCP tool is prefixed with "<cmd>_" or contains "<cmd>_"
//          (family match: "cloud" -> "cloud_deploy", etc.), OR
//        - the command is on an explicit allowlist of commands that
//          intentionally have no MCP surface (destructive / GUI /
//          interactive).

import { describe, it, expect } from "bun:test";
import * as fs from "node:fs";
import * as path from "node:path";

const AGENT_DIR = path.resolve(__dirname, "../../desktop/agent");

/** CLI commands deliberately NOT exposed over MCP. Each entry has a
 *  one-line reason so a future contributor can verify the exclusion
 *  is still warranted. */
const MCP_OPT_OUT: Record<string, string> = {
  // Destructive — intentionally require the human at the physical machine.
  signout:         "dropping the auth token must be an explicit human action",
  uninstall:       "uninstalling yaver binary is a local-only operation",
  wipe:            "full factory reset must be performed at the keyboard",
  purge:           "destructive task/session purge is owner-keyboard-only",
  permissions:     "macOS accessibility prompts are intrinsically GUI",
  "factory-reset": "covered by yaver_auth_factory_reset; plain alias ignored",
  // Interactive / terminal-only primitives.
  completion:      "shell completion script generation, no remote use",
  ui:              "opens a browser tab; not a remote op",
  help:            "help text",
  version:         "version string",
  // Reserved for a follow-up that needs its own design PR.
  "sdk-token":     "token lifecycle covered by sdk create/rotate/list; top-level alias tracked in gap list",
};

/** Commands we already shipped but map to MCP via a prefix other than
 *  the command name itself. Listing them here documents the mapping
 *  and stops the heuristic from tripping on them. */
const MCP_ALIASED: Record<string, string> = {
  handoff:    "session_handoff",
  pair:       "yaver_auth_link_start",
  send:       "yaver_auth_start",
  primary:    "device_primary_get",
  stream:     "streams via raw /streams/<name> SSE endpoint",
  flags:      "flag_evaluate / flag_list / flag_set",
  vault:      "vault via ops('local','secrets',...) + existing vault HTTP",
};

function loadMainGoCliCases(): string[] {
  const src = fs.readFileSync(path.join(AGENT_DIR, "main.go"), "utf8");
  // We want only the top-level CLI router (the big switch on os.Args[1]).
  // Heuristic: the first switch statement in the file. Grab its cases.
  const switchStart = src.indexOf("switch os.Args[1]");
  if (switchStart < 0) return [];
  // Walk forward to the matching default: or closing brace of the switch.
  const rest = src.slice(switchStart);
  const defaultIdx = rest.indexOf("\n\tdefault:");
  const routerBody = defaultIdx > 0 ? rest.slice(0, defaultIdx) : rest;
  const out = new Set<string>();
  const re = /case\s+"([a-z][a-z0-9-]*)"(?:\s*,\s*"([a-z][a-z0-9-]*)")*\s*:/g;
  for (const m of routerBody.matchAll(re)) {
    for (let i = 1; i < m.length; i++) if (m[i]) out.add(m[i]);
  }
  return [...out].sort();
}

function loadMcpToolNames(): Set<string> {
  const src = fs.readFileSync(path.join(AGENT_DIR, "mcp_tools.go"), "utf8");
  const out = new Set<string>();
  const re = /"name":\s*"([a-z][a-z_0-9]*)"/g;
  for (const m of src.matchAll(re)) out.add(m[1]);
  return out;
}

function hasMcpCoverage(cmd: string, tools: Set<string>): boolean {
  const norm = cmd.replace(/-/g, "_");
  if (tools.has(norm)) return true;
  // Family match: any tool starting with "<cmd>_" or containing "_<cmd>".
  for (const t of tools) {
    if (t === norm) return true;
    if (t.startsWith(norm + "_")) return true;
    if (t.endsWith("_" + norm)) return true;
  }
  return false;
}

describe("mcp drift", () => {
  it("every CLI command is reachable over MCP (or explicitly opted out)", () => {
    const cliCases = loadMainGoCliCases();
    const mcpTools = loadMcpToolNames();

    const missing: string[] = [];
    for (const cmd of cliCases) {
      if (cmd in MCP_OPT_OUT) continue;
      if (cmd in MCP_ALIASED) continue;
      if (hasMcpCoverage(cmd, mcpTools)) continue;
      missing.push(cmd);
    }
    if (missing.length) {
      console.error(
        "\nCLI commands shipping without MCP coverage:\n" +
          missing.map((m) => "  - yaver " + m).join("\n") +
          "\n\nWire each up in desktop/agent/mcp_tools.go + httpserver.go or " +
          "add it to MCP_OPT_OUT / MCP_ALIASED in test/mcp-drift.test.ts with a reason.",
      );
    }
    expect(missing).toEqual([]);
  });

  it("every registered MCP tool has a dispatch case (no orphans)", () => {
    const mcp_tools_src = fs.readFileSync(path.join(AGENT_DIR, "mcp_tools.go"), "utf8");
    const http_src = fs.readFileSync(path.join(AGENT_DIR, "httpserver.go"), "utf8");
    const registered = new Set<string>();
    for (const m of mcp_tools_src.matchAll(/"name":\s*"([a-z][a-z_0-9]*)"/g)) registered.add(m[1]);
    const dispatched = new Set<string>();
    for (const m of http_src.matchAll(/^\s*case\s+"([a-z][a-z_0-9]*)":/gm)) dispatched.add(m[1]);
    const orphans = [...registered].filter((t) => !dispatched.has(t)).sort();
    if (orphans.length) {
      console.error(
        "\nMCP tools registered but missing a dispatch case:\n" +
          orphans.map((o) => "  - " + o).join("\n"),
      );
    }
    expect(orphans).toEqual([]);
  });

  it("ops + ops_verbs are present (grand-MCP smoke)", () => {
    const tools = loadMcpToolNames();
    expect(tools.has("ops")).toBe(true);
    expect(tools.has("ops_verbs")).toBe(true);
  });
});
