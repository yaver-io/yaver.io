import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const source = readFileSync(join(import.meta.dirname, "userSettings.ts"), "utf8");

test("settings normalizer upgrades obsolete Codex model rows", () => {
  assert.match(source, /OBSOLETE_CODEX_MODEL_IDS[\s\S]*gpt-5\.3-codex/);
  assert.match(source, /CURRENT_CODEX_MODEL_ID = "gpt-5\.6-sol"/);
  assert.match(source, /String\(row\.runnerId \|\| ""\)\.trim\(\)\.toLowerCase\(\) === "codex"/);
  assert.match(source, /return \{ \.\.\.row, model: CURRENT_CODEX_MODEL_ID \}/);
});

test("settings reads and writes pass through primary runner normalization", () => {
  assert.match(source, /return normalizeSettingsForClient\(settings\)/);
  assert.match(source, /const normalizedPrimaryRunnerRows = normalizePrimaryRunnerRowsForClient/);
  assert.match(source, /patch\.primaryRunnerByDevice = normalizedPrimaryRunnerRows/);
});

test("the per-device favorite stores Codex reasoning atomically", () => {
  assert.match(source, /type PrimaryRunnerRow = \{[\s\S]*reasoningEffort\?: string/);
  assert.match(source, /CODEX_REASONING_EFFORTS = new Set\(\["none", "low", "medium", "high", "xhigh", "max", "ultra"\]\)/);
  assert.equal((source.match(/reasoningEffort: v\.optional\(v\.union\(v\.string\(\), v\.null\(\)\)\)/g) || []).length, 2);
  assert.equal((source.match(/if \(reasoningEffort\) row\.reasoningEffort = reasoningEffort/g) || []).length, 2);
});

test("legacy verbosity rows remain schema-readable until production migration", () => {
  const schema = readFileSync(join(import.meta.dirname, "schema.ts"), "utf8");
  assert.match(schema, /verbosity: v\.optional\(v\.number\(\)\)/);
});

test("fleet roles are ownership checked and one device may be both master and worker", () => {
  const schema = readFileSync(join(import.meta.dirname, "schema.ts"), "utf8");
  const http = readFileSync(join(import.meta.dirname, "http.ts"), "utf8");
  assert.match(schema, /workerDeviceIds: v\.optional\(v\.array\(v\.string\(\)\)\)/);
  assert.match(schema, /showWorkerDevices: v\.optional\(v\.boolean\(\)\)/);
  assert.match(schema, /opportunisticFleet: v\.optional\(v\.boolean\(\)\)/);
  assert.match(source, /normalizeOwnedDeviceIds/);
  assert.match(source, /deviceIds\.length > 64/);
  assert.match(source, /deviceId\.length > 128/);
  assert.match(source, /device\.userId !== userId \|\| device\.removed/);
  assert.doesNotMatch(source, /filter\(\(id\) => id !== effectivePrimaryDeviceId\)/);
  assert.match(source, /patch\.workerDeviceIds = normalizedWorkerDeviceIds/);
  assert.match(http, /workerDeviceIds: body\.workerDeviceIds/);
  assert.match(http, /showWorkerDevices: body\.showWorkerDevices/);
  assert.match(http, /opportunisticFleet: body\.opportunisticFleet/);
});
