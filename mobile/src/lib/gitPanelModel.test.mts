// gitPanelModel.test.mts — pure git-panel view-model. Run: npx tsx src/lib/gitPanelModel.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { groupChanges, statusSummary, suggestCommitMessage, pushability } from "./gitPanelModel.ts";
import type { FileDiff } from "./codingAgent/sandboxGitOps.ts";

const CH: FileDiff[] = [
  { path: "src/b.ts", status: "modified" },
  { path: "src/a.ts", status: "added" },
  { path: "old.ts", status: "deleted" },
];

test("groupChanges buckets + sorts", () => {
  const g = groupChanges(CH);
  assert.deepEqual(g.added, ["src/a.ts"]);
  assert.deepEqual(g.modified, ["src/b.ts"]);
  assert.deepEqual(g.deleted, ["old.ts"]);
  assert.equal(g.total, 3);
});

test("statusSummary: clean vs counts", () => {
  assert.equal(statusSummary("main", []), "main · clean");
  assert.equal(statusSummary("main", CH), "main · 3 changes (1 +, 1 ~, 1 −)");
  assert.equal(statusSummary(null, []), "(detached) · clean");
});

test("suggestCommitMessage: single vs multi", () => {
  assert.equal(suggestCommitMessage([]), "");
  assert.equal(suggestCommitMessage([{ path: "src/App.tsx", status: "modified" }]), "Update App.tsx");
  assert.equal(suggestCommitMessage([{ path: "x/New.tsx", status: "added" }]), "Add New.tsx");
  assert.equal(suggestCommitMessage(CH), "Add 1 file, update 1, delete 1");
});

test("pushability gates on token/remote/busy", () => {
  assert.deepEqual(pushability({ hasToken: false, hasRemote: false, busy: false }), {
    enabled: false,
    hint: "Add a GitHub token to push",
  });
  assert.deepEqual(pushability({ hasToken: true, hasRemote: false, busy: false }), {
    enabled: false,
    hint: "Set a remote (owner/repo) to push",
  });
  assert.equal(pushability({ hasToken: true, hasRemote: true, busy: false }).enabled, true);
  assert.equal(pushability({ hasToken: true, hasRemote: true, busy: true }).enabled, false);
});
