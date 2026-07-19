import test from "node:test";
import assert from "node:assert/strict";

import {
  canAttachArtifactToFeedback,
  canRouteFeedbackWorkStatus,
  canReuseFeedbackRelaySourceIntent,
  feedbackPendingLimit,
  feedbackPendingLimitExceeded,
  feedbackRelayIntentMetadata,
  feedbackRelayLocalTaskId,
  feedbackRelayReason,
  feedbackRelaySourceLocalTaskCollision,
  isTerminalFeedbackWorkStatus,
  normalizeAttachmentUrl,
  normalizeAttachmentUrls,
  normalizeFeedbackKind,
  normalizeFeedbackPriority,
  normalizeFeedbackTarget,
  normalizeProjectSlug,
  uniqueFeedbackArtifactIds,
} from "./feedbackWorkItems.js";

test("feedback labels normalize to bounded public enums", () => {
  assert.equal(normalizeFeedbackKind("Bug"), "bug");
  assert.equal(normalizeFeedbackKind("new feature"), "other");
  assert.equal(normalizeFeedbackPriority("HIGH"), "high");
  assert.equal(normalizeFeedbackPriority("urgent"), "normal");
  assert.equal(normalizeFeedbackTarget("issue"), "issue");
  assert.equal(normalizeFeedbackTarget("deploy"), "triage");
});

test("feedback project slugs are labels, not paths", () => {
  assert.equal(normalizeProjectSlug("demo-app"), "demo-app");
  assert.equal(normalizeProjectSlug(" nested/app "), undefined);
  assert.equal(normalizeProjectSlug("../app"), undefined);
});

test("feedback attachment urls are https only and strip credentials", () => {
  assert.equal(
    normalizeAttachmentUrl("https://user:secret@example.com/shot.png"),
    "https://example.com/shot.png",
  );
  assert.throws(() => normalizeAttachmentUrl("http://example.com/shot.png"), /https/);
  assert.throws(() => normalizeAttachmentUrl("/tmp/shot.png"), /absolute https/);
});

test("feedback attachment url list deduplicates and caps", () => {
  const urls = normalizeAttachmentUrls([
    "https://example.com/a.png",
    "https://example.com/a.png",
    "https://example.com/b.png",
  ]);
  assert.deepEqual(urls, ["https://example.com/a.png", "https://example.com/b.png"]);
});

test("feedback artifact id list deduplicates and caps", () => {
  const ids = uniqueFeedbackArtifactIds([
    "a",
    "a",
    "b",
    "c",
    "d",
    "e",
    "f",
    "g",
    "h",
    "i",
    "j",
    "k",
    "l",
    "m",
  ]);
  assert.deepEqual(ids, ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"]);
});

test("feedback artifact attachment stays inside project and private owner/uploader boundary", () => {
  const base = {
    shareId: "share-a",
    ownerUserId: "owner",
    userId: "guest-a",
    status: "active",
    visibility: "project",
    expiresAt: 2_000,
  };
  const ctx = {
    shareId: "share-a",
    ownerUserId: "owner",
    requesterUserId: "guest-a",
    now: 1_000,
  };

  assert.equal(canAttachArtifactToFeedback(base, ctx), true);
  assert.equal(canAttachArtifactToFeedback({ ...base, shareId: "share-b" }, ctx), false);
  assert.equal(canAttachArtifactToFeedback({ ...base, ownerUserId: "other-owner" }, ctx), false);
  assert.equal(canAttachArtifactToFeedback({ ...base, status: "hidden" }, ctx), false);
  assert.equal(canAttachArtifactToFeedback({ ...base, expiresAt: 900 }, ctx), false);
  assert.equal(canAttachArtifactToFeedback({ ...base, visibility: "private" }, ctx), true);
  assert.equal(canAttachArtifactToFeedback({ ...base, visibility: "private", userId: "guest-b" }, ctx), false);
  assert.equal(
    canAttachArtifactToFeedback(
      { ...base, visibility: "private", userId: "guest-b" },
      { ...ctx, requesterUserId: "owner" },
    ),
    true,
  );
});

test("feedback relay metadata is bounded and body-free", () => {
  assert.equal(feedbackRelayLocalTaskId("abc123"), "feedback:abc123");
  const reason = feedbackRelayReason("bug", "Login button fails");
  assert.equal(reason, "feedback:bug:Login button fails");
  assert.ok(!reason.includes("stack trace"));
  assert.equal(feedbackRelayReason("unknown", ""), "feedback:other:");
});

test("feedback relay intent metadata includes safe provider target", () => {
  assert.deepEqual(
    feedbackRelayIntentMetadata({
      itemId: "item-1",
      projectSlug: "demo",
      title: "Fix signup",
      kind: "bug",
      repoUrl: "https://github.com/acme/app.git",
      defaultBranch: "main",
    }),
    {
      localTaskId: "feedback:item-1",
      baseBranch: "main",
      branch: "yaver/source/demo-fix-signup",
      reason: "feedback:bug:Fix signup",
      providerKind: "github",
      providerHost: "github.com",
      providerRepo: "acme/app",
      providerBranch: "yaver/source/demo-fix-signup",
      providerBranchUrl: "https://github.com/acme/app/tree/yaver/source/demo-fix-signup",
      providerAuthMode: "none",
      providerAuthStatus: "required",
    },
  );

  assert.deepEqual(
    feedbackRelayIntentMetadata({
      itemId: "item-1",
      projectSlug: "demo",
      title: "Fix signup",
      kind: "bug",
      repoUrl: "https://token@github.com/acme/app.git",
    }).providerKind,
    undefined,
  );
});

test("feedback relay-source reuse requires matching live intent", () => {
  const base = {
    ownerUserId: "owner",
    shareId: "share-a",
    localTaskId: "feedback:item-1",
    status: "queued",
  };
  const ctx = {
    ownerUserId: "owner",
    shareId: "share-a",
    localTaskId: "feedback:item-1",
  };

  assert.equal(canReuseFeedbackRelaySourceIntent(base, ctx), true);
  assert.equal(canReuseFeedbackRelaySourceIntent({ ...base, status: "handoff_ready" }, ctx), true);
  assert.equal(canReuseFeedbackRelaySourceIntent({ ...base, status: "failed" }, ctx), false);
  assert.equal(canReuseFeedbackRelaySourceIntent({ ...base, status: "cancelled" }, ctx), false);
  assert.equal(canReuseFeedbackRelaySourceIntent({ ...base, ownerUserId: "other" }, ctx), false);
  assert.equal(canReuseFeedbackRelaySourceIntent({ ...base, shareId: "share-b" }, ctx), false);
  assert.equal(canReuseFeedbackRelaySourceIntent({ ...base, localTaskId: "feedback:item-2" }, ctx), false);

  assert.equal(feedbackRelaySourceLocalTaskCollision(base, ctx), false);
  assert.equal(feedbackRelaySourceLocalTaskCollision({ ...base, ownerUserId: "other" }, ctx), true);
  assert.equal(feedbackRelaySourceLocalTaskCollision({ ...base, shareId: "share-b" }, ctx), true);
});

test("feedback terminal statuses distinguish recoverable blocked rows", () => {
  assert.equal(isTerminalFeedbackWorkStatus("queued"), false);
  assert.equal(isTerminalFeedbackWorkStatus("claimed"), false);
  assert.equal(isTerminalFeedbackWorkStatus("blocked"), true);
  assert.equal(isTerminalFeedbackWorkStatus("issue_draft_created"), true);
  assert.equal(isTerminalFeedbackWorkStatus("branch_created"), true);

  assert.equal(canRouteFeedbackWorkStatus("queued"), true);
  assert.equal(canRouteFeedbackWorkStatus("claimed"), true);
  assert.equal(canRouteFeedbackWorkStatus("blocked"), true);
  assert.equal(canRouteFeedbackWorkStatus("task_created"), false);
  assert.equal(canRouteFeedbackWorkStatus("issue_draft_created"), false);
  assert.equal(canRouteFeedbackWorkStatus("issue_created"), false);
  assert.equal(canRouteFeedbackWorkStatus("branch_created"), false);
  assert.equal(canRouteFeedbackWorkStatus("cancelled"), false);
  assert.equal(canRouteFeedbackWorkStatus("rejected"), false);
  assert.equal(canRouteFeedbackWorkStatus("expired"), false);
});

test("feedback pending queue limits are fail-open only when explicitly zero", () => {
  assert.equal(feedbackPendingLimit("25", 100), 25);
  assert.equal(feedbackPendingLimit("0", 100), 0);
  assert.equal(feedbackPendingLimit("-1", 100), 100);
  assert.equal(feedbackPendingLimit("nope", 100), 100);

  assert.equal(feedbackPendingLimitExceeded({ pendingCount: 79, limit: 80 }), false);
  assert.equal(feedbackPendingLimitExceeded({ pendingCount: 80, limit: 80 }), true);
  assert.equal(feedbackPendingLimitExceeded({ pendingCount: 500, limit: 0 }), false);
});
