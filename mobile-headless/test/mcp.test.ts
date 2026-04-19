// Integration test for the stdio MCP server. Spawns bun running
// src/bin/mcp.ts, speaks JSONRPC over stdin/stdout, and asserts the
// tool inventory + a couple of end-to-end mobile_tap_* + mobile_api_*
// calls work. Uses the mock agent so nothing external is needed.

import { describe, it, expect, beforeAll, afterAll } from "bun:test";
import { spawn, type Subprocess } from "bun";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { startMockAgent, type MockAgentHandle } from "../src/mock-agent";

let mockAgent: MockAgentHandle;
let mcp: Subprocess;
let dataDir: string;

// Simple JSONRPC client over the MCP subprocess's stdio.
let nextId = 1;
const inflight = new Map<number, (v: any) => void>();
let stdoutBuf = "";

function call(method: string, params: any = {}): Promise<any> {
  const id = nextId++;
  const msg = JSON.stringify({ jsonrpc: "2.0", id, method, params });
  return new Promise((resolve) => {
    inflight.set(id, resolve);
    (mcp.stdin as any).write(msg + "\n");
  });
}

beforeAll(async () => {
  dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "ymh-mcp-"));
  mockAgent = await startMockAgent({ token: "mock-token" });

  mcp = spawn({
    cmd: ["bun", "run", path.resolve(__dirname, "../src/bin/mcp.ts")],
    cwd: path.resolve(__dirname, ".."),
    stdin: "pipe",
    stdout: "pipe",
    stderr: "pipe",
    env: {
      ...process.env,
      YMH_DATA_DIR: dataDir,
      YMH_AUTH_TOKEN: "mock-token",
      YMH_CONVEX_URL: mockAgent.baseUrl,
      YMH_AGENT_URL: mockAgent.baseUrl,
    },
  });

  // Drain stdout as newline-delimited JSONRPC.
  (async () => {
    if (!mcp.stdout) return;
    const reader = (mcp.stdout as any).getReader();
    const decoder = new TextDecoder();
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      stdoutBuf += decoder.decode(value, { stream: true });
      let idx: number;
      while ((idx = stdoutBuf.indexOf("\n")) >= 0) {
        const line = stdoutBuf.slice(0, idx).trim();
        stdoutBuf = stdoutBuf.slice(idx + 1);
        if (!line) continue;
        try {
          const msg = JSON.parse(line);
          if (typeof msg.id === "number") {
            const resolver = inflight.get(msg.id);
            if (resolver) { resolver(msg); inflight.delete(msg.id); }
          }
        } catch { /* non-json line, ignore */ }
      }
    }
  })();
});

afterAll(async () => {
  mcp.kill();
  await mcp.exited;
  await mockAgent.close();
});

describe("MCP server", () => {
  it("lists 19 tools split across mobile_tap_* and mobile_api_*", async () => {
    const res = await call("tools/list");
    const names: string[] = res.result.tools.map((t: any) => t.name);
    expect(names.length).toBeGreaterThanOrEqual(18);
    expect(names.filter((n) => n.startsWith("mobile_tap_") || n === "mobile_sign_in" || n === "mobile_respond_sudo" || n === "mobile_wizard_answer" || n === "mobile_wizard_generate").length).toBeGreaterThanOrEqual(7);
    expect(names.filter((n) => n.startsWith("mobile_api_")).length).toBeGreaterThanOrEqual(7);
  });

  it("mobile_tap_install_list returns the mock catalogue", async () => {
    const res = await call("tools/call", {
      name: "mobile_tap_install_list",
      arguments: {},
    });
    const payload = JSON.parse(res.result.content[0].text);
    expect(Array.isArray(payload)).toBe(true);
    expect(payload.map((x: any) => x.name)).toContain("ollama");
  });

  it("mobile_tap_new_project → answer → generate happy path", async () => {
    const start = await call("tools/call", {
      name: "mobile_tap_new_project",
      arguments: {},
    });
    const startBody = JSON.parse(start.result.content[0].text);
    const sessionId = startBody.session?.id;
    expect(sessionId).toBe("mock-session");

    const ans = await call("tools/call", {
      name: "mobile_wizard_answer",
      arguments: { sessionId, questionId: "app_template", answer: "saas-dashboard" },
    });
    const ansBody = JSON.parse(ans.result.content[0].text);
    expect(ansBody.session?.done).toBe(true);

    const gen = await call("tools/call", {
      name: "mobile_wizard_generate",
      arguments: { sessionId },
    });
    const genBody = JSON.parse(gen.result.content[0].text);
    expect(genBody.ok).toBe(true);
  });

  it("mobile_api_get /info returns something from the convex URL", async () => {
    const res = await call("tools/call", {
      name: "mobile_api_get",
      arguments: { path: "/info" },
    });
    const payload = JSON.parse(res.result.content[0].text);
    // Mock agent's /info doesn't require auth and returns a known shape.
    expect(payload.status).toBe(200);
    expect(payload.body?.hostname).toBe("mock");
  });

  it("returns a structured error for unknown tools", async () => {
    const res = await call("tools/call", {
      name: "does_not_exist",
      arguments: {},
    });
    const payload = JSON.parse(res.result.content[0].text);
    expect(payload.ok).toBe(false);
    expect(String(payload.error)).toMatch(/unknown tool/i);
  });
});
