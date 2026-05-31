// downloadState.test.mts — background model-download state machine.
// Run: npx tsx src/lib/localAgent/downloadState.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  promptDownload, acceptDownload, declineDownload, startDownloading,
  setProgress, startVerifying, markReady, markFailed, cancelDownload,
  retryDownload, isActive, readyIds, statusLabel, type DownloadMap,
} from "./downloadState.ts";

test("happy path: prompt → accept → download → verify → ready", () => {
  let m: DownloadMap = {};
  m = promptDownload(m, "router");
  assert.equal(m.router.phase, "prompted");
  m = acceptDownload(m, "router");
  assert.equal(m.router.phase, "queued");
  m = startDownloading(m, "router");
  assert.equal(m.router.phase, "downloading");
  assert.equal(m.router.attempts, 1);
  m = setProgress(m, "router", 400_000_000, 800_000_000);
  assert.equal(m.router.progress, 0.5);
  m = startVerifying(m, "router");
  assert.equal(m.router.phase, "verifying");
  m = markReady(m, "router");
  assert.equal(m.router.phase, "ready");
  assert.deepEqual(readyIds(m), ["router"]);
});

test("decline keeps it idle (no download)", () => {
  let m: DownloadMap = {};
  m = promptDownload(m, "coder");
  m = declineDownload(m, "coder");
  assert.equal(m.coder.phase, "idle");
  assert.equal(isActive(m.coder), false);
});

test("UI never blocked: downloading is non-terminal + reports progress", () => {
  let m: DownloadMap = startDownloading(acceptDownload(promptDownload({}, "r"), "r"), "r");
  m = setProgress(m, "r", 100, 1000);
  assert.equal(isActive(m.r), true); // still active, user can keep using app
  assert.equal(m.r.progress, 0.1);
});

test("progress ignored unless downloading", () => {
  let m: DownloadMap = promptDownload({}, "r"); // phase prompted
  m = setProgress(m, "r", 50, 100);
  assert.equal(m.r.progress, 0); // unchanged
});

test("fail then retry → queued, attempts increment on next start", () => {
  let m: DownloadMap = startDownloading(acceptDownload(promptDownload({}, "r"), "r"), "r");
  m = markFailed(m, "r", "network");
  assert.equal(m.r.phase, "failed");
  assert.equal(m.r.error, "network");
  m = retryDownload(m, "r");
  assert.equal(m.r.phase, "queued");
  m = startDownloading(m, "r");
  assert.equal(m.r.attempts, 2);
});

test("cancel mid-download, then retry", () => {
  let m: DownloadMap = startDownloading(acceptDownload(promptDownload({}, "r"), "r"), "r");
  m = cancelDownload(m, "r");
  assert.equal(m.r.phase, "cancelled");
  m = retryDownload(m, "r");
  assert.equal(m.r.phase, "queued");
});

test("don't re-prompt something already in flight or ready", () => {
  let m: DownloadMap = markReady(startVerifying(startDownloading(acceptDownload(promptDownload({}, "r"), "r"), "r"), "r"), "r");
  const before = m.r.phase;
  m = promptDownload(m, "r");
  assert.equal(m.r.phase, before); // still ready, not re-prompted
});

test("statusLabel renders a background-pill string", () => {
  let m: DownloadMap = startDownloading(acceptDownload(promptDownload({}, "r"), "r"), "r");
  m = setProgress(m, "r", 120_000_000, 800_000_000);
  assert.equal(statusLabel(m.r, "Voice helper"), "Voice helper · 120/800 MB");
  assert.equal(statusLabel(markReady(m, "r").r, "Voice helper"), "Voice helper · ready");
  assert.match(statusLabel(markFailed(m, "r", "x").r, "Voice helper")!, /failed — tap to retry/);
});

test("readyIds feeds modelPicker(downloadedIds)", () => {
  let m: DownloadMap = {};
  m = markReady(m, "a");
  m = markReady(m, "b");
  m = markFailed(m, "c", "e");
  assert.deepEqual(readyIds(m).sort(), ["a", "b"]);
});
