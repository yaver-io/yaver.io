import test from "node:test";
import assert from "node:assert/strict";

import {
  normalizeRelayBranch,
  parseRelaySourceProviderTarget,
  relaySourceSlug,
} from "./relaySourceIntents.js";

test("relaySourceSlug normalizes branch-safe labels", () => {
  assert.equal(relaySourceSlug(" Fix login flow!! "), "fix-login-flow");
  assert.equal(relaySourceSlug("../main"), "main");
  assert.equal(relaySourceSlug("", "task"), "task");
});

test("normalizeRelayBranch keeps relay work under yaver namespace", () => {
  assert.equal(normalizeRelayBranch("feature/login", "task-1"), "yaver/source/task-1");
  assert.equal(normalizeRelayBranch("yaver/alice", "task-1"), "yaver/alice");
  assert.equal(normalizeRelayBranch("main", "task-1"), "yaver/source/task-1");
  assert.equal(normalizeRelayBranch("yaver/main", "task-1"), "yaver/source/task-1");
});

test("normalizeRelayBranch removes dangerous git ref fragments", () => {
  assert.equal(normalizeRelayBranch("yaver/../prod.lock", "task-2"), "yaver/prod");
  assert.equal(normalizeRelayBranch("", "Project Task 2"), "yaver/source/project-task-2");
});

test("parseRelaySourceProviderTarget stores only non-secret branch metadata", () => {
  assert.deepEqual(
    parseRelaySourceProviderTarget("https://github.com/acme/app.git", "yaver/source/fix-1"),
    {
      providerKind: "github",
      providerHost: "github.com",
      providerRepo: "acme/app",
      providerBranch: "yaver/source/fix-1",
      providerBranchUrl: "https://github.com/acme/app/tree/yaver/source/fix-1",
      providerAuthMode: "none",
      providerAuthStatus: "required",
    },
  );
  assert.deepEqual(
    parseRelaySourceProviderTarget("git@gitlab.com:group/app.git", "main"),
    {
      providerKind: "gitlab",
      providerHost: "gitlab.com",
      providerRepo: "group/app",
      providerBranch: "yaver/source/group/app",
      providerBranchUrl: "https://gitlab.com/group/app/-/tree/yaver/source/group/app",
      providerAuthMode: "none",
      providerAuthStatus: "required",
    },
  );
  assert.deepEqual(parseRelaySourceProviderTarget("https://token@github.com/acme/app.git", "yaver/source/x"), {});
});
