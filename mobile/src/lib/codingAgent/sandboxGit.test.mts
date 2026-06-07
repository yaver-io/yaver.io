// sandboxGit.test.mts — the sandbox VCS core against Node's fs in a temp dir
// (isomorphic-git is fs-agnostic, so this exercises the real git logic headless).
// Run: npx tsx src/lib/codingAgent/sandboxGit.test.mts

import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import {
  ensureRepo,
  isRepo,
  changedFiles,
  commitAll,
  headOid,
  log,
  revertTo,
  checkpointBefore,
  checkpointAfter,
} from "./sandboxGit.ts";

function tmpProject(seed: Record<string, string> = {}): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "sandboxgit-"));
  for (const [rel, content] of Object.entries(seed)) {
    const full = path.join(dir, rel);
    fs.mkdirSync(path.dirname(full), { recursive: true });
    fs.writeFileSync(full, content);
  }
  return dir;
}
const opt = (dir: string) => ({ fs, dir });

test("ensureRepo is idempotent and isRepo reflects it", async () => {
  const dir = tmpProject();
  assert.equal(await isRepo(opt(dir)), false);
  assert.equal(await ensureRepo(opt(dir)), true); // created
  assert.equal(await ensureRepo(opt(dir)), false); // already a repo
  assert.equal(await isRepo(opt(dir)), true);
});

test("commitAll stages adds, returns oid, then null when clean", async () => {
  const dir = tmpProject({ "src/App.tsx": "v1", "src/lib/x.ts": "export const x=1" });
  await ensureRepo(opt(dir));
  const oid = await commitAll(opt(dir), "initial");
  assert.ok(oid && oid.length >= 7);
  assert.equal(await commitAll(opt(dir), "nothing changed"), null); // clean → no empty commit
  const entries = await log(opt(dir));
  assert.equal(entries.length, 1);
  assert.equal(entries[0].message, "initial");
  assert.equal(entries[0].oid, oid);
});

test("changedFiles classifies add / modify / delete vs HEAD", async () => {
  const dir = tmpProject({ "a.ts": "1", "b.ts": "2" });
  await ensureRepo(opt(dir));
  await commitAll(opt(dir), "base");
  fs.writeFileSync(path.join(dir, "a.ts"), "1-modified"); // modify
  fs.writeFileSync(path.join(dir, "c.ts"), "3"); // add
  fs.rmSync(path.join(dir, "b.ts")); // delete

  const changes = await changedFiles(opt(dir));
  const byPath = Object.fromEntries(changes.map((c) => [c.path, c.status]));
  assert.equal(byPath["a.ts"], "modified");
  assert.equal(byPath["c.ts"], "added");
  assert.equal(byPath["b.ts"], "deleted");
});

test("commitAll captures deletions (git rm), not just writes", async () => {
  const dir = tmpProject({ "keep.ts": "k", "drop.ts": "d" });
  await ensureRepo(opt(dir));
  await commitAll(opt(dir), "base");
  fs.rmSync(path.join(dir, "drop.ts"));
  const oid = await commitAll(opt(dir), "remove drop");
  assert.ok(oid);
  // After committing the deletion, the tree is clean again.
  assert.deepEqual(await changedFiles(opt(dir)), []);
});

test("checkpoint before/after wraps a run and revertTo restores the tree", async () => {
  const dir = tmpProject({ "App.tsx": "const title = 'old'\n" });

  // before-checkpoint snapshots the starting state.
  const before = await checkpointBefore(opt(dir), "rename title");
  assert.ok(before, "before checkpoint should commit the initial file");

  // simulate the agent editing the tree
  fs.writeFileSync(path.join(dir, "App.tsx"), "const title = 'new'\n");
  fs.writeFileSync(path.join(dir, "extra.ts"), "junk the agent added\n");
  const after = await checkpointAfter(opt(dir), "rename title");
  assert.ok(after, "after checkpoint should commit the agent's edits");
  assert.notEqual(before, after);

  // history has both checkpoints, newest first
  const entries = await log(opt(dir));
  assert.equal(entries[0].message, "agent: rename title");
  assert.equal(entries[1].message, "checkpoint: before rename title");

  // Revert the run → working tree matches the before state exactly.
  await revertTo(opt(dir), before!);
  assert.equal(fs.readFileSync(path.join(dir, "App.tsx"), "utf8"), "const title = 'old'\n");
  assert.equal(fs.existsSync(path.join(dir, "extra.ts")), false, "agent-added file is gone after revert");
});

test("headOid is null before any commit, set after", async () => {
  const dir = tmpProject({ "a.ts": "1" });
  await ensureRepo(opt(dir));
  assert.equal(await headOid(opt(dir)), null);
  const oid = await commitAll(opt(dir), "first");
  assert.equal(await headOid(opt(dir)), oid);
});
