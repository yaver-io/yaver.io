// previewPhase.test.mts — the overlay must narrate the phase that is TRUE.
// Run: node --experimental-strip-types --test src/lib/previewPhase.test.mts

import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { previewPhaseTitle, previewTimeoutExplanation } from "./previewPhase.ts";

test("no status / not running → starting", () => {
  assert.equal(previewPhaseTitle(null, null), "Starting web dev server…");
  assert.equal(
    previewPhaseTitle({ framework: "flutter", running: false, building: true }, null),
    "Starting flutter dev server…",
  );
});

test("the regression: server running + probe flutter_booting must NOT say 'starting dev server'", () => {
  const title = previewPhaseTitle(
    { framework: "flutter", running: true, serving: true },
    { reason: "flutter_booting" },
  );
  assert.ok(!/starting .*dev server/i.test(title), `still lying about the server: "${title}"`);
  assert.match(title, /engine booting/i);
});

test("server up, no probe yet → loading page, not starting", () => {
  assert.equal(
    previewPhaseTitle({ framework: "expo", running: true }, null),
    "expo server ready — loading page…",
  );
  assert.equal(
    previewPhaseTitle({ framework: "expo", running: true }, { reason: "document_not_ready" }),
    "expo server ready — loading page…",
  );
});

test("agent 503 placeholder → server compiling", () => {
  assert.match(
    previewPhaseTitle({ framework: "expo", running: true }, { reason: "agent_starting_response" }),
    /compiling/i,
  );
});

test("loaded-but-unpainted reasons name the render gap, not the server", () => {
  for (const reason of ["empty_mount", "mount_without_visible_content", "empty_body"]) {
    assert.equal(
      previewPhaseTitle({ framework: "expo", running: true }, { reason }),
      "Page loaded — app hasn't painted yet",
    );
  }
});

test("content-confirmed reasons say rendering", () => {
  for (const reason of ["flutter_engine_attached", "mount_has_visible_content", "plain_body_content"]) {
    assert.match(previewPhaseTitle({ running: true }, { reason }), /rendering first frame/i);
  }
});

test("a bare agent 404 is named as the wrong route", () => {
  assert.match(
    previewPhaseTitle({ framework: "expo", running: true }, { reason: "http_error_document" }),
    /404/,
  );
  assert.match(previewTimeoutExplanation("http_error_document", "expo"), /update the agent/i);
});

test("timeout explanation names asset/bootstrap failure for flutter_booting", () => {
  const text = previewTimeoutExplanation("flutter_booting", "flutter");
  assert.match(text, /engine never attached/i);
  assert.match(text, /asset|CanvasKit/i);
});

test("timeout explanation for a mounted-but-blank app points at runtime errors", () => {
  assert.match(previewTimeoutExplanation("empty_mount", "expo"), /runtime error/i);
});

test("unknown timeout reason still gives an actionable generic line", () => {
  assert.match(previewTimeoutExplanation(undefined), /retry/i);
});

// ── PARITY ────────────────────────────────────────────────────────────────
//
// previewPhase.ts is the last of the preview twins with no parity guard.
// capabilityGap, relayDeny and taskStreamRecovery each pin their two copies
// byte-for-byte; this pair was pinned by nothing, and web has no test file of
// its own at all — so a drift here fails on neither surface. The twins agree
// today (the only textual difference is prose: "inside the WebView" vs
// "inside the iframe"), which is exactly when the guard is cheap to add and
// exactly when nobody adds it.
//
// Comparing SOURCE rather than behaviour is deliberate and is the idiom the
// other three pairs use: web/ and mobile/ have no shared build, so a test
// cannot import both. For pure functions, byte-identity is strictly stronger
// than any fixture table.
test("web/mobile twins are byte-identical below the header comment", () => {
  const strip = (src: string) =>
    src
      .split("\n")
      .filter((l) => !l.trimStart().startsWith("//"))
      .join("\n")
      .trim();
  const here = dirname(fileURLToPath(import.meta.url));
  const repoRoot = join(here, "..", "..", "..");
  const mobile = strip(readFileSync(join(repoRoot, "mobile/src/lib/previewPhase.ts"), "utf8"));
  const web = strip(readFileSync(join(repoRoot, "web/lib/previewPhase.ts"), "utf8"));
  assert.equal(
    mobile,
    web,
    "previewPhase twins drifted — sync web/lib/previewPhase.ts and mobile/src/lib/previewPhase.ts",
  );
});
