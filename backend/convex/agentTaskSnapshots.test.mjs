import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const schemaSource = readFileSync(join(here, "schema.ts"), "utf8");
const moduleSource = readFileSync(join(here, "agentTaskSnapshots.ts"), "utf8");
const httpSource = readFileSync(join(here, "http.ts"), "utf8");

test("agent task snapshots expose identity and lifecycle only", () => {
  const table = schemaSource.match(/agentTaskSnapshots: defineTable\(\{[\s\S]*?\n  \}\)[\s\S]*?\.index\("by_device"/)?.[0] ?? "";
  for (const field of ["userId:", "deviceId:", "observedAt:", "taskId:", "yaverSessionId:", "hostKind:", "status:", "updatedAt:"]) {
    assert.ok(table.includes(field), `snapshot schema missing ${field}`);
  }
  for (const forbidden of ["title:", "prompt:", "description:", "output:", "source:", "path:", "project:", "model:"]) {
    assert.ok(!table.includes(forbidden), `snapshot schema must not hold ${forbidden}`);
    assert.ok(!moduleSource.includes(`${forbidden} v.`), `snapshot mutation must not accept ${forbidden}`);
  }
});

test("host kind is a closed, non-sensitive enum", () => {
  for (const kind of ["terminal_tmux", "desktop_gui", "runner_process"]) {
    assert.ok(moduleSource.includes(`v.literal("${kind}")`), `missing host kind ${kind}`);
  }
});

test("snapshot sync is one bounded row per owned device", () => {
  assert.match(moduleSource, /resolveUser\(ctx\)/);
  assert.match(moduleSource, /Device ownership mismatch/);
  assert.match(moduleSource, /args\.tasks\.filter\([\s\S]{0,100}\.slice\(0, 200\)/);
  assert.match(moduleSource, /withIndex\("by_device"/);
  assert.match(moduleSource, /observedAt: Date\.now\(\)/);

  const deviceLookup = moduleSource.indexOf('.query("devices")');
  const snapshotLookup = moduleSource.indexOf('.query("agentTaskSnapshots")');
  assert.ok(deviceLookup >= 0, "upsert must query the submitted deviceId");
  assert.ok(snapshotLookup > deviceLookup, "device ownership must be checked before snapshot upsert");
  assert.match(moduleSource, /if \(!device \|\| device\.userId !== userId\)/);
  assert.ok((moduleSource.match(/return upsertSnapshot\(ctx, userId, args\)/g) ?? []).length === 2,
    "native and HTTP-auth mutations must share the bounded upsert");
});

test("GET task-snapshots is bearer authenticated and wired", () => {
  assert.match(httpSource, /path: "\/task-snapshots"/);
  assert.match(httpSource, /authHeader\?\.startsWith\("Bearer "\)/);
  assert.match(httpSource, /api\.agentTaskSnapshots\.list/);
});

test("POST task-snapshots hashes Yaver bearer auth and enforces prompt-free metadata", () => {
  const start = httpSource.indexOf("/** POST /task-snapshots");
  const end = httpSource.indexOf("/** POST /tasks/placement/status", start);
  const route = httpSource.slice(start, end);
  assert.ok(start >= 0 && end > start, "POST /task-snapshots route must be present");
  assert.match(route, /path: "\/task-snapshots"/);
  assert.match(route, /method: "POST"/);
  assert.match(route, /authHeader\?\.startsWith\("Bearer "\)/);
  assert.match(route, /sha256Hex\(authHeader\.slice\(7\)\)/);
  assert.match(route, /promptFreeMetadataBodyDeniedReason\(body\)/);
  assert.match(route, /internal\.agentTaskSnapshots\.syncByToken/);
  assert.match(route, /syncByToken,\s*\{\s*tokenHash,/,
    "POST bridge must forward the shorthand tokenHash property");
  for (const field of ["deviceId", "observedAt", "tasks"]) {
    assert.match(route, new RegExp(`${field}:`), `POST bridge must pass ${field}`);
  }
  for (const forbidden of ["title", "prompt", "description", "output", "source", "path", "project", "model"]) {
    assert.ok(!route.includes(`body.${forbidden}`), `POST bridge must not forward ${forbidden}`);
  }
});

test("HTTP bridge validates the Yaver token hash before shared upsert", () => {
  const bridge = moduleSource.slice(moduleSource.indexOf("export const syncByToken"));
  assert.match(bridge, /internalMutation\(\{/);
  assert.match(bridge, /tokenHash: v\.string\(\)/);
  assert.match(bridge, /userFromToken\(ctx, args\.tokenHash\)/);
  assert.match(bridge, /return upsertSnapshot\(ctx, userId, args\)/);
});

test("POST task-snapshots rejects malformed and invalid top-level shapes before mutation", () => {
  const start = httpSource.indexOf("/** POST /task-snapshots");
  const end = httpSource.indexOf("/** POST /tasks/placement/status", start);
  const route = httpSource.slice(start, end);
  const parse = route.indexOf("await request.json()");
  const objectGuard = route.indexOf('errorResponse("Request body must be a JSON object", 400)');
  const mutation = route.indexOf("ctx.runMutation");

  assert.ok(parse >= 0 && objectGuard > parse, "malformed/non-object JSON must be rejected after parsing");
  assert.ok(mutation > objectGuard, "body shape must be validated before runMutation");
  assert.match(route, /catch \{\s*return errorResponse\("Malformed JSON body", 400\);\s*\}/);
  assert.match(route, /!parsedBody \|\| typeof parsedBody !== "object" \|\| Array\.isArray\(parsedBody\)/);
  assert.ok(route.indexOf("const body = parsedBody") > objectGuard,
    "body fields must not be exposed before the object guard");
  assert.match(route, /typeof body\.deviceId !== "string" \|\| body\.deviceId\.trim\(\) === ""/);
  assert.match(route, /typeof body\.observedAt !== "number" \|\| !Number\.isFinite\(body\.observedAt\)/);
  assert.match(route, /!Array\.isArray\(body\.tasks\)/);

  for (const message of [
    "deviceId must be a non-empty string",
    "observedAt must be a finite number",
    "tasks must be an array",
  ]) {
    assert.match(route, new RegExp(`errorResponse\\("${message}", 400\\)`));
  }
});
