// llmOpenAI.test.mts — OpenAI/GLM provider: request shape + response parsing.
// Run: npx tsx src/lib/llmOpenAI.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { createOpenAiProvider, parseChatCompletion, extractJsonObject } from "./llmOpenAI.ts";
import type { EditFilesRequest } from "./llmClient.ts";

const REQ: EditFilesRequest = {
  prompt: "add a header",
  files: [{ path: "App.tsx", content: "export default function App(){return null}" }],
  framework: "react-native",
};

test("requires an api key", () => {
  assert.throws(() => createOpenAiProvider({ flavor: "openai", apiKey: "" }));
});

test("forces the apply_edits tool and hits the flavor base URL", async () => {
  let captured: { url: string; body: any; auth: string | null } | null = null;
  const fetchImpl = (async (url: any, init: any) => {
    captured = {
      url: String(url),
      body: JSON.parse(init.body),
      auth: init.headers.Authorization ?? null,
    };
    return {
      ok: true,
      json: async () => ({
        choices: [
          {
            message: {
              tool_calls: [
                {
                  function: {
                    name: "apply_edits",
                    arguments: JSON.stringify({
                      rationale: "added header",
                      edits: [{ action: "update", path: "App.tsx", content: "// new" }],
                    }),
                  },
                },
              ],
            },
          },
        ],
        usage: { prompt_tokens: 10, completion_tokens: 5 },
      }),
    } as any;
  }) as unknown as typeof fetch;

  const glm = createOpenAiProvider({ flavor: "glm", apiKey: "k", fetchImpl });
  const plan = await glm.editFiles(REQ);

  assert.ok(captured);
  assert.match(captured!.url, /api\.z\.ai/); // GLM base URL
  assert.equal(captured!.auth, "Bearer k");
  assert.equal(captured!.body.tool_choice.function.name, "apply_edits");
  assert.equal(plan.edits.length, 1);
  assert.equal(plan.edits[0].path, "App.tsx");
  assert.equal(plan.rationale, "added header");
  assert.equal(plan.inputTokens, 10);
  assert.equal(plan.outputTokens, 5);
});

test("throws on non-ok HTTP with provider label", async () => {
  const fetchImpl = (async () =>
    ({ ok: false, status: 401, text: async () => "bad key" }) as any) as unknown as typeof fetch;
  const p = createOpenAiProvider({ flavor: "openai", apiKey: "k", fetchImpl });
  await assert.rejects(() => p.editFiles(REQ), /OpenAI 401/);
});

test("parseChatCompletion drops malformed edits, keeps valid ones", () => {
  const plan = parseChatCompletion({
    choices: [
      {
        message: {
          tool_calls: [
            {
              function: {
                name: "apply_edits",
                arguments: JSON.stringify({
                  rationale: "x",
                  edits: [
                    { action: "create", path: "a.ts", content: "1" },
                    { action: "bogus", path: "b.ts" }, // dropped
                    { action: "delete" }, // dropped (no path)
                  ],
                }),
              },
            },
          ],
        },
      },
    ],
  });
  assert.equal(plan.edits.length, 1);
  assert.equal(plan.edits[0].path, "a.ts");
});

test("parseChatCompletion falls back to JSON embedded in content", () => {
  const plan = parseChatCompletion({
    choices: [
      {
        message: {
          content:
            'Sure! ```json\n{"rationale":"hi","edits":[{"action":"create","path":"x.ts","content":"y"}]}\n```',
        },
      },
    ],
  });
  assert.equal(plan.edits.length, 1);
  assert.equal(plan.rationale, "hi");
});

test("extractJsonObject pulls a balanced object from noisy text", () => {
  assert.equal(extractJsonObject('prefix {"a":{"b":1}} suffix'), '{"a":{"b":1}}');
  assert.equal(extractJsonObject('a string with } brace then {"ok":true}'), '{"ok":true}');
  assert.equal(extractJsonObject("no object here"), "{}");
});
