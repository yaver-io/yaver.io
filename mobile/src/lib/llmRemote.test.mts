// llmRemote.test.mts — the "remote runner (GLM)" LlmProvider. Ships the sandbox
// to a box and maps the box's sandboxRunResponse back into an EditPlan.
// Run: npx tsx src/lib/llmRemote.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { createRemoteProvider, type RemoteSandboxRequest, type RemoteSandboxResponse } from "./llmRemote.ts";
import type { EditFilesRequest } from "./llmClient.ts";

function recorder(responder: (req: RemoteSandboxRequest) => RemoteSandboxResponse) {
  const calls: RemoteSandboxRequest[] = [];
  const dispatch = async (req: RemoteSandboxRequest) => {
    calls.push(req);
    return responder(req);
  };
  return { dispatch, calls };
}

const baseReq: EditFilesRequest = {
  prompt: "make the heading green",
  files: [{ path: "app/index.tsx", content: "color: red" }],
  framework: "react-native",
};

test("requires a dispatch function", () => {
  assert.throws(() => createRemoteProvider({ dispatch: undefined as any }), /dispatch is required/);
});

test("forces runner=glm and forwards prompt + files + framework + schema", async () => {
  const rec = recorder(() => ({ ok: true, edits: [], runner: "glm", model: "glm-4.7" }));
  const provider = createRemoteProvider({ dispatch: rec.dispatch });
  assert.equal(provider.id, "remote");
  await provider.editFiles({ ...baseReq, schema: { tables: [{ name: "todos" }] } as any });
  assert.equal(rec.calls.length, 1);
  const sent = rec.calls[0];
  assert.equal(sent.runner, "glm");
  assert.equal(sent.prompt, "make the heading green");
  assert.equal(sent.framework, "react-native");
  assert.deepEqual(sent.files, [{ path: "app/index.tsx", content: "color: red" }]);
  assert.deepEqual(sent.schema, { tables: [{ name: "todos" }] });
});

test("maps a successful response into an EditPlan", async () => {
  const rec = recorder(() => ({
    ok: true,
    rationale: "made it green",
    runner: "glm",
    model: "glm-4.7",
    edits: [
      { action: "update", path: "app/index.tsx", content: "color: green", reason: "the ask" },
      { action: "create", path: "app/new.tsx", content: "// new" },
      { action: "delete", path: "app/old.tsx" },
    ],
  }));
  const provider = createRemoteProvider({ dispatch: rec.dispatch });
  const plan = await provider.editFiles(baseReq);
  assert.equal(plan.rationale, "made it green");
  assert.equal(plan.edits.length, 3);
  assert.deepEqual(plan.edits[0], {
    action: "update",
    path: "app/index.tsx",
    content: "color: green",
    reason: "the ask",
  });
  assert.equal(plan.edits[2].action, "delete");
  assert.deepEqual(plan.debug, { runner: "glm", model: "glm-4.7" });
});

test("throws when the box reports failure with no edits", async () => {
  const rec = recorder(() => ({ ok: false, edits: [], error: "GLM is not configured on this box" }));
  const provider = createRemoteProvider({ dispatch: rec.dispatch });
  await assert.rejects(() => provider.editFiles(baseReq), /Remote runner failed.*not configured/);
});

test("folds a partial error into the rationale when edits exist", async () => {
  const rec = recorder(() => ({
    ok: true,
    rationale: "started",
    edits: [{ action: "update", path: "app/index.tsx", content: "color: green" }],
    error: "glm runner timed out",
  }));
  const provider = createRemoteProvider({ dispatch: rec.dispatch });
  const plan = await provider.editFiles(baseReq);
  assert.equal(plan.edits.length, 1);
  assert.match(plan.rationale, /started/);
  assert.match(plan.rationale, /partial: glm runner timed out/);
});

test("rejects oversized requests before dispatch (shared budget guard)", async () => {
  const rec = recorder(() => ({ ok: true, edits: [] }));
  const provider = createRemoteProvider({ dispatch: rec.dispatch });
  const big = "a".repeat(250_000);
  await assert.rejects(
    () => provider.editFiles({ prompt: "x", files: [{ path: "big.ts", content: big }] }),
    /exceeds 200000 bytes/,
  );
  assert.equal(rec.calls.length, 0); // never hit the network
});
