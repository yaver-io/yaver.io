import assert from "node:assert/strict";
import test from "node:test";
import worker from "./index.ts";

test("retired gateway never reads legacy inference plaintext or credentials", async () => {
  const marker = "SECRET_MARKER_YAVER_91827";
  const request = new Request("https://gateway.example/v1/chat/completions", {
    method: "POST",
    headers: { Authorization: `Bearer ${marker}`, "content-type": "application/json" },
    body: JSON.stringify({ messages: [{ role: "user", content: marker }] }),
  });
  const response = await worker.fetch(request);
  assert.equal(response.status, 410);
  assert.equal(request.bodyUsed, false, "Cloudflare code consumed plaintext request body");
  assert.doesNotMatch(await response.text(), new RegExp(marker));
});

test("health advertises that plaintext inference is disabled", async () => {
  const response = await worker.fetch(new Request("https://gateway.example/healthz"));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { ok: true, mode: "zero-knowledge", inferenceProxy: false });
});
