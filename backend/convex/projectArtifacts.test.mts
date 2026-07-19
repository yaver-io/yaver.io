import test from "node:test";
import assert from "node:assert/strict";

import {
  includedArtifactStorageBytes,
  isPublicArtifactVisible,
  normalizeArtifactSizeBytes,
  normalizeArtifactKind,
  normalizeArtifactProvider,
  normalizeArtifactUrl,
  normalizeObjectKey,
  summarizeArtifactUsage,
} from "./projectArtifacts.js";

test("artifact kind/provider normalize to supported public labels", () => {
  assert.equal(normalizeArtifactKind("APK"), "apk");
  assert.equal(normalizeArtifactKind("Hermes Bundle"), "other");
  assert.equal(normalizeArtifactKind("web-preview"), "web-preview");
  assert.equal(normalizeArtifactProvider("R2"), "r2");
  assert.equal(normalizeArtifactProvider("unknown-host"), "external");
});

test("artifact urls must be absolute https and strip credentials", () => {
  assert.equal(
    normalizeArtifactUrl("https://user:secret@example.com/app.apk"),
    "https://example.com/app.apk",
  );
  assert.throws(() => normalizeArtifactUrl("http://example.com/app.apk"), /https/);
  assert.throws(() => normalizeArtifactUrl("/local/path/app.apk"), /absolute https/);
});

test("object keys reject local-path shaped values", () => {
  assert.equal(normalizeObjectKey("artifacts/demo/app.apk"), "artifacts/demo/app.apk");
  assert.equal(normalizeObjectKey("kg29x8storageid"), "kg29x8storageid");
  assert.throws(() => normalizeObjectKey("/Users/me/app.apk"), /unsafe/);
  assert.throws(() => normalizeObjectKey("../secret.apk"), /unsafe/);
});

test("artifact included storage quota parses env with a 1 GiB fallback", () => {
  assert.equal(includedArtifactStorageBytes("2048"), 2048);
  assert.equal(includedArtifactStorageBytes("0"), 0);
  assert.equal(includedArtifactStorageBytes("nope"), 1024 * 1024 * 1024);
  assert.equal(includedArtifactStorageBytes("-1"), 1024 * 1024 * 1024);
});

test("artifact size normalizes to finite non-negative whole bytes", () => {
  assert.equal(normalizeArtifactSizeBytes(12.9), 12);
  assert.equal(normalizeArtifactSizeBytes("42"), 42);
  assert.equal(normalizeArtifactSizeBytes(-5), 0);
  assert.equal(normalizeArtifactSizeBytes(undefined), undefined);
  assert.equal(normalizeArtifactSizeBytes(Number.POSITIVE_INFINITY), undefined);
  assert.equal(normalizeArtifactSizeBytes("nope"), undefined);
});

test("artifact usage counts active metered storage without charging external links", () => {
  const now = 1_000_000;
  const usage = summarizeArtifactUsage([
    {
      kind: "apk",
      provider: "convex",
      sizeBytes: 500,
      visibility: "public-link",
      status: "active",
      createdAt: 100,
    },
    {
      kind: "web-preview",
      provider: "external",
      sizeBytes: 9000,
      visibility: "project",
      status: "active",
      createdAt: 200,
    },
    {
      kind: "bundle",
      provider: "r2",
      sizeBytes: 800,
      visibility: "private",
      status: "active",
      expiresAt: now - 1,
      createdAt: 50,
    },
    {
      kind: "log",
      provider: "convex",
      sizeBytes: 300,
      visibility: "project",
      status: "hidden",
      createdAt: 300,
    },
  ], now, 1000, 250);
  assert.equal(usage.activeCount, 2);
  assert.equal(usage.storageBytes, 500);
  assert.equal(usage.reservedUploadBytes, 250);
  assert.equal(usage.totalMeteredBytes, 750);
  assert.equal(usage.remainingBytes, 250);
  assert.equal(usage.quotaPercent, 0.75);
  assert.equal(usage.publicLinkCount, 1);
  assert.deepEqual(usage.byKind, { apk: 1, "web-preview": 1 });
  assert.equal(usage.oldestCreatedAt, 100);
  assert.equal(usage.newestCreatedAt, 200);
});

test("public artifact visibility requires active public link and unexpired clocks", () => {
  const now = 1_000_000;
  assert.equal(isPublicArtifactVisible({
    status: "active",
    visibility: "public-link",
    shareUrlExpiresAt: now + 1,
    expiresAt: now + 1,
  }, now), true);

  assert.equal(isPublicArtifactVisible({
    status: "hidden",
    visibility: "public-link",
    shareUrlExpiresAt: now + 1,
  }, now), false);

  assert.equal(isPublicArtifactVisible({
    status: "active",
    visibility: "project",
    shareUrlExpiresAt: now + 1,
  }, now), false);

  assert.equal(isPublicArtifactVisible({
    status: "active",
    visibility: "public-link",
    shareUrlExpiresAt: now,
  }, now), false);

  assert.equal(isPublicArtifactVisible({
    status: "active",
    visibility: "public-link",
    expiresAt: now,
  }, now), false);
});
