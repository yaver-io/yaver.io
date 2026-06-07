// llmLocal.test.mts — on-device provider: prompt grounding + fenced parsing.
// Run: npx tsx src/lib/llmLocal.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { createLocalProvider, parseFencedEdits } from "./llmLocal.ts";
import type { EditFilesRequest } from "./llmClient.ts";

test("path in fence info string", () => {
  const { edits } = parseFencedEdits("```tsx src/Button.tsx\nexport const B = 1\n```");
  assert.equal(edits.length, 1);
  assert.equal(edits[0].path, "src/Button.tsx");
  assert.equal(edits[0].content, "export const B = 1");
});

test("path on its own line before the fence", () => {
  const text = "Here you go:\n\nApp.tsx\n```tsx\nexport default function App(){}\n```";
  const { edits, rationale } = parseFencedEdits(text);
  assert.equal(edits.length, 1);
  assert.equal(edits[0].path, "App.tsx");
  assert.match(rationale, /Here you go/);
});

test("path as a leading comment inside the block is stripped from body", () => {
  const { edits } = parseFencedEdits("```\n// screens/Home.tsx\nconst x = 1\n```");
  assert.equal(edits[0].path, "screens/Home.tsx");
  assert.equal(edits[0].content, "const x = 1"); // comment line removed
});

test("known paths become update, unknown become create", () => {
  const text = "App.tsx\n```\nA\n```\nNew.tsx\n```\nB\n```";
  const { edits } = parseFencedEdits(text, ["App.tsx"]);
  const byPath = Object.fromEntries(edits.map((e) => [e.path, e.action]));
  assert.equal(byPath["App.tsx"], "update");
  assert.equal(byPath["New.tsx"], "create");
});

test("blocks without a resolvable path are skipped", () => {
  const { edits } = parseFencedEdits("```\nsome shell output\nno path here\n```");
  assert.equal(edits.length, 0);
});

test("no fences → whole text is the rationale", () => {
  const { edits, rationale } = parseFencedEdits("I can't do that safely.");
  assert.equal(edits.length, 0);
  assert.match(rationale, /can't do that/);
});

test("provider builds a grounded prompt and parses the completion", async () => {
  let seenPrompt = "";
  const provider = createLocalProvider({
    modelId: "qwen2.5-coder-3b-q4",
    openPath: "App.tsx",
    stack: ["typescript", "react-native"],
    complete: async (prompt) => {
      seenPrompt = prompt;
      return "Done.\n\nApp.tsx\n```tsx\nexport default function App(){return null}\n```";
    },
  });
  const req: EditFilesRequest = {
    prompt: "add a title",
    files: [{ path: "App.tsx", content: "old" }],
  };
  const plan = await provider.editFiles(req);

  // Prompt was grounded via sandboxPrompt (header + open file + request).
  assert.match(seenPrompt, /on-device coding assistant/);
  assert.match(seenPrompt, /Open file — App\.tsx/);
  assert.match(seenPrompt, /Request: add a title/);
  // Completion parsed into an update (App.tsx is a known file).
  assert.equal(plan.edits.length, 1);
  assert.equal(plan.edits[0].action, "update");
  assert.equal(plan.model, undefined); // model lives on the provider, not the plan
});
