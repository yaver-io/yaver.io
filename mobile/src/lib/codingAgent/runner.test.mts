// runner.test.mts — the agentic coding loop, driven against a mock provider.
// Run: npx tsx src/lib/codingAgent/runner.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  runCodingAgent,
  defaultCodingAgentConfig,
  type CodingAgentConfig,
} from "./runner.ts";
import type { CodingSandbox, CodingSandboxEntry } from "./sandboxTools.ts";

function memSandbox(initial: Record<string, string> = {}): CodingSandbox & { dump: () => Record<string, string> } {
  const files = new Map<string, string>(Object.entries(initial));
  return {
    async readFile(path) {
      if (!files.has(path)) throw new Error(`not found: ${path}`);
      return files.get(path)!;
    },
    async listFiles(): Promise<CodingSandboxEntry[]> {
      return [...files.entries()]
        .map(([path, content]) => ({ path, isDirectory: false, size: Buffer.byteLength(content) }))
        .sort((a, b) => a.path.localeCompare(b.path));
    },
    async writeFile(path, content) {
      files.set(path, content);
    },
    async deleteFile(path) {
      files.delete(path);
    },
    dump: () => Object.fromEntries(files),
  };
}

// Build an OpenAI-style /chat/completions response with optional tool calls.
function oaiTurn(opts: { content?: string; toolCalls?: Array<{ id: string; name: string; args: unknown }> }) {
  return {
    ok: true,
    async json() {
      return {
        choices: [
          {
            finish_reason: opts.toolCalls?.length ? "tool_calls" : "stop",
            message: {
              role: "assistant",
              content: opts.content ?? null,
              tool_calls: opts.toolCalls?.map((c) => ({
                id: c.id,
                type: "function",
                function: { name: c.name, arguments: JSON.stringify(c.args) },
              })),
            },
          },
        ],
        usage: { prompt_tokens: 100, completion_tokens: 20 },
      };
    },
  } as any;
}

// A scripted fetch that returns canned turns in order and records each request.
function scriptedFetch(turns: any[]) {
  const requests: Array<{ url: string; body: any; auth: string | null }> = [];
  let i = 0;
  const fetchImpl = (async (url: any, init: any) => {
    requests.push({
      url: String(url),
      body: JSON.parse(init.body),
      auth: init.headers?.Authorization ?? null,
    });
    const turn = turns[Math.min(i, turns.length - 1)];
    i++;
    return turn;
  }) as unknown as typeof fetch;
  return { fetchImpl, requests, calls: () => i };
}

const GLM_CFG: CodingAgentConfig = defaultCodingAgentConfig("glm-key");

test("defaultCodingAgentConfig points at GLM (z.ai, glm-4.6)", () => {
  assert.equal(GLM_CFG.provider, "glm");
  assert.equal(GLM_CFG.model, "glm-4.6");
  assert.match(GLM_CFG.baseUrl!, /api\.z\.ai/);
});

test("rejects when no api key configured", async () => {
  await assert.rejects(
    () => runCodingAgent({ prompt: "x", sandbox: memSandbox(), config: { ...GLM_CFG, apiKey: "" } }),
    /not configured/,
  );
});

test("multi-step loop: read_file → edit_file → finish, feeds tool results back", async () => {
  const box = memSandbox({ "App.tsx": "const title = 'old'\n" });
  const { fetchImpl, requests } = scriptedFetch([
    oaiTurn({ toolCalls: [{ id: "t1", name: "read_file", args: { path: "App.tsx" } }] }),
    oaiTurn({
      toolCalls: [{ id: "t2", name: "edit_file", args: { path: "App.tsx", old: "'old'", new: "'new'" } }],
    }),
    oaiTurn({ content: "Renamed the title to 'new'." }),
  ]);

  const res = await runCodingAgent({ prompt: "rename title to new", sandbox: box, config: GLM_CFG, fetchImpl });

  // The edit actually landed in the tree.
  assert.equal(box.dump()["App.tsx"], "const title = 'new'\n");
  assert.equal(res.finalText, "Renamed the title to 'new'.");
  assert.equal(res.steps, 3);
  assert.equal(res.hitMaxSteps, false);
  assert.deepEqual(res.mutatedPaths, ["App.tsx"]);
  assert.equal(res.toolCalls.length, 2);
  assert.equal(res.toolCalls[0].name, "read_file");
  assert.equal(res.toolCalls[1].mutating, true);
  assert.equal(res.inputTokens, 300); // 3 turns × 100
  assert.equal(res.outputTokens, 60);

  // GLM transport: z.ai chat/completions + Bearer, and the SECOND request
  // carries a role:"tool" message with the read_file result fed back.
  assert.match(requests[0].url, /api\.z\.ai\/.*chat\/completions/);
  assert.equal(requests[0].auth, "Bearer glm-key");
  const toolMsgs = requests[1].body.messages.filter((m: any) => m.role === "tool");
  assert.equal(toolMsgs.length, 1);
  assert.match(toolMsgs[0].content, /const title = 'old'/); // the read result
});

test("loop continues on tool_calls even when finish_reason isn't 'tool_calls' (GLM quirk)", async () => {
  const box = memSandbox();
  const writeTurn = {
    ok: true,
    async json() {
      return {
        choices: [
          {
            finish_reason: "stop", // GLM lies here, but tool_calls are present
            message: {
              role: "assistant",
              content: null,
              tool_calls: [
                {
                  id: "w1",
                  type: "function",
                  function: { name: "write_file", arguments: JSON.stringify({ path: "a.ts", content: "1" }) },
                },
              ],
            },
          },
        ],
        usage: {},
      };
    },
  } as any;
  const { fetchImpl } = scriptedFetch([writeTurn, oaiTurn({ content: "done" })]);
  const res = await runCodingAgent({ prompt: "make a.ts", sandbox: box, config: GLM_CFG, fetchImpl });
  assert.equal(box.dump()["a.ts"], "1");
  assert.deepEqual(res.mutatedPaths, ["a.ts"]);
});

test("confirmMutation=false blocks the write and the tree is untouched", async () => {
  const box = memSandbox({ "App.tsx": "keep me\n" });
  const { fetchImpl } = scriptedFetch([
    oaiTurn({ toolCalls: [{ id: "t1", name: "write_file", args: { path: "App.tsx", content: "OVERWRITE" } }] }),
    oaiTurn({ content: "I was not allowed to write." }),
  ]);
  const res = await runCodingAgent({
    prompt: "overwrite App",
    sandbox: box,
    config: GLM_CFG,
    fetchImpl,
    confirmMutation: () => false,
  });
  assert.equal(box.dump()["App.tsx"], "keep me\n"); // untouched
  assert.equal(res.mutatedPaths.length, 0);
  assert.equal(res.toolCalls[0].denied, true);
});

test("read-only tools are never gated by confirmMutation", async () => {
  const box = memSandbox({ "App.tsx": "x" });
  let asked = 0;
  const { fetchImpl } = scriptedFetch([
    oaiTurn({ toolCalls: [{ id: "t1", name: "read_file", args: { path: "App.tsx" } }] }),
    oaiTurn({ content: "read it" }),
  ]);
  await runCodingAgent({
    prompt: "read App",
    sandbox: box,
    config: GLM_CFG,
    fetchImpl,
    confirmMutation: () => {
      asked++;
      return true;
    },
  });
  assert.equal(asked, 0); // read_file is not mutating → no confirm
});

test("hitMaxSteps when the model never stops calling tools", async () => {
  const box = memSandbox({ "App.tsx": "x" });
  // Always return a read_file tool call → loop never terminates on its own.
  const loopTurn = oaiTurn({ toolCalls: [{ id: "t", name: "read_file", args: { path: "App.tsx" } }] });
  const { fetchImpl } = scriptedFetch([loopTurn]);
  const res = await runCodingAgent({
    prompt: "spin",
    sandbox: box,
    config: GLM_CFG,
    fetchImpl,
    maxSteps: 3,
  });
  assert.equal(res.hitMaxSteps, true);
  assert.equal(res.steps, 3);
});

test("HTTP error surfaces with provider label", async () => {
  const errFetch = (async () => ({ ok: false, status: 401, text: async () => "bad key" }) as any) as typeof fetch;
  await assert.rejects(
    () => runCodingAgent({ prompt: "x", sandbox: memSandbox(), config: GLM_CFG, fetchImpl: errFetch }),
    /glm 401/,
  );
});

test("anthropic transport drives the same tool loop via tool_use blocks", async () => {
  const box = memSandbox({ "App.tsx": "const x = 1\n" });
  const antToolTurn = {
    ok: true,
    async json() {
      return {
        stop_reason: "tool_use",
        content: [
          { type: "text", text: "Editing now." },
          { type: "tool_use", id: "tu1", name: "write_file", input: { path: "App.tsx", content: "const x = 2\n" } },
        ],
        usage: { input_tokens: 50, output_tokens: 10 },
      };
    },
  } as any;
  const antDoneTurn = {
    ok: true,
    async json() {
      return {
        stop_reason: "end_turn",
        content: [{ type: "text", text: "Done — x is now 2." }],
        usage: { input_tokens: 30, output_tokens: 8 },
      };
    },
  } as any;
  const { fetchImpl, requests } = scriptedFetch([antToolTurn, antDoneTurn]);
  const res = await runCodingAgent({
    prompt: "set x to 2",
    sandbox: box,
    config: { provider: "anthropic", model: "claude-opus-4-7", apiKey: "ant-key" },
    fetchImpl,
  });
  assert.equal(box.dump()["App.tsx"], "const x = 2\n");
  assert.equal(res.finalText, "Done — x is now 2.");
  assert.deepEqual(res.mutatedPaths, ["App.tsx"]);
  assert.match(requests[0].url, /api\.anthropic\.com\/.*messages/);
  // Second request feeds a tool_result block back inside a user message.
  const userMsgs = requests[1].body.messages.filter((m: any) => m.role === "user");
  const lastUser = userMsgs[userMsgs.length - 1];
  assert.equal(lastUser.content[0].type, "tool_result");
  assert.equal(lastUser.content[0].tool_use_id, "tu1");
});
