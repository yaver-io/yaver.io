// models.test.mts — registry compatibility + picker logic.
// Run: npx tsx src/lib/localAgent/models.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { MODEL_REGISTRY, ENGINE, getModel, bundledModel, modelPicker } from "./models.ts";

test("every model shares the one engine (compatibility guarantee)", () => {
  for (const m of MODEL_REGISTRY) assert.equal(m.engine, ENGINE);
});

test("exactly one bundled model, and it's a router", () => {
  const bundled = MODEL_REGISTRY.filter((m) => m.bundled);
  assert.equal(bundled.length, 1);
  assert.equal(bundled[0].tier, "router");
  assert.equal(bundledModel().id, bundled[0].id);
});

test("downloadables declare a GitHub-Releases url on kivanccakmak/yaver-models", () => {
  for (const m of MODEL_REGISTRY.filter((x) => !x.bundled)) {
    assert.ok(m.downloadUrl, `${m.id} needs downloadUrl`);
    assert.match(m.downloadUrl!, /github\.com\/kivanccakmak\/yaver-models\/releases\/download/);
    assert.notEqual(m.sha256, undefined);
  }
});

test("getModel resolves known ids", () => {
  assert.ok(getModel("qwen2.5-coder-3b-q4"));
  assert.equal(getModel("nope"), undefined);
});

test("picker: iPhone 14 (6GB) — bundled installed, coder not runnable, router recommended", () => {
  const rows = modelPicker({ totalRamMb: 6144, downloadedIds: [] });
  const bundled = rows.find((r) => r.bundled)!;
  assert.equal(bundled.installed, true);
  assert.equal(bundled.runnable, true);
  // 3B coder needs 7500 → not runnable on 6GB
  const coder3b = rows.find((r) => r.id === "qwen2.5-coder-3b-q4")!;
  assert.equal(coder3b.runnable, false);
  // recommended is a runnable router (the 1.5B, larger of the runnable routers)
  const rec = rows.find((r) => r.recommended)!;
  assert.equal(rec.tier, "router");
  assert.equal(rec.id, "qwen2.5-1.5b-instruct-q4");
});

test("picker: 8GB device — coder runnable + recommended", () => {
  const rows = modelPicker({ totalRamMb: 8192 });
  const coder3b = rows.find((r) => r.id === "qwen2.5-coder-3b-q4")!;
  assert.equal(coder3b.runnable, true);
  assert.equal(rows.find((r) => r.recommended)!.id, "qwen2.5-coder-3b-q4");
});

test("picker: downloaded coder shows installed", () => {
  const rows = modelPicker({ totalRamMb: 8192, downloadedIds: ["qwen2.5-coder-3b-q4"] });
  assert.equal(rows.find((r) => r.id === "qwen2.5-coder-3b-q4")!.installed, true);
  // a non-downloaded one is not installed
  assert.equal(rows.find((r) => r.id === "qwen2.5-1.5b-instruct-q4")!.installed, false);
});

test("picker: unknown RAM (0) keeps everything runnable but recommends a router", () => {
  const rows = modelPicker({});
  assert.ok(rows.every((r) => r.runnable));
  assert.equal(rows.find((r) => r.recommended)!.tier, "router");
});
