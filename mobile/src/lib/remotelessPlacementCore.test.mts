import assert from "node:assert/strict";
import test from "node:test";

import { remotelessCapability, resolveRemotelessPlacement } from "../_core/remoteless.ts";

const candidates = [
  { id: "primary", name: "Mac mini", role: "primary" as const, connected: true },
  { id: "secondary", name: "Linux box", role: "secondary" as const, connected: true },
];

test("a connected primary always wins over remoteless", () => {
  const result = resolveRemotelessPlacement({ capability: "code-edit", surface: "ios", candidates });
  assert.equal(result.lane, "remote");
  if (result.lane === "remote") assert.equal(result.target.id, "primary");
});

test("the core enforces primary-secondary order even when callers do not", () => {
  const result = resolveRemotelessPlacement({
    capability: "code-edit",
    surface: "ios",
    candidates: [candidates[1], candidates[0]],
  });
  assert.equal(result.lane, "remote");
  if (result.lane === "remote") assert.equal(result.target.id, "primary");
});

test("an explicit target wins without changing automatic primary precedence", () => {
  const result = resolveRemotelessPlacement({
    capability: "code-edit",
    surface: "ios",
    candidates: [...candidates, { id: "explicit", name: "Chosen box", role: "explicit", connected: true }],
  });
  assert.equal(result.lane, "remote");
  if (result.lane === "remote") assert.equal(result.target.id, "explicit");
});

test("a connected secondary is an explicit degraded remote lane, not remoteless", () => {
  const result = resolveRemotelessPlacement({
    capability: "code-edit",
    surface: "android",
    candidates: [{ ...candidates[0], connected: false }, candidates[1]],
  });
  assert.equal(result.lane, "remote");
  if (result.lane === "remote") {
    assert.equal(result.target.id, "secondary");
    assert.equal(result.degraded, true);
    assert.match(result.banner || "", /primary unavailable/i);
  }
});

test("editing falls back locally only after configured devices are unavailable", () => {
  const result = resolveRemotelessPlacement({
    capability: "code-edit",
    surface: "ios",
    candidates: candidates.map((candidate) => ({ ...candidate, connected: false })),
  });
  assert.equal(result.lane, "remoteless");
  assert.match(result.banner, /primary and secondary/i);
});

test("Flutter rendering on iPhone is a named blocked capability with a route", () => {
  const result = resolveRemotelessPlacement({ capability: "flutter-render", surface: "ios", candidates: [] });
  assert.equal(result.lane, "blocked");
  if (result.lane === "blocked") {
    assert.equal(result.capability.code, "remoteless.flutter-render.unavailable");
    assert.match(result.capability.detail, /Flutter SDK/);
    assert.equal(result.capability.route.path, "/devices");
    assert.equal(result.capability.alternateRoute?.path, "/devices");
  }
});

test("companion surfaces keep analysis but never invent a coding runtime", () => {
  assert.equal(remotelessCapability("analysis-chat", "companion").support, "supported");
  assert.equal(remotelessCapability("code-edit", "companion").support, "unavailable");
});

test("forceLocal remains an explicit override but never invents unavailable capability", () => {
  const selected = resolveRemotelessPlacement({ capability: "git-push", surface: "android", candidates, forceLocal: true });
  assert.equal(selected.lane, "remoteless");
  assert.match(selected.banner, /No remote box selected/);
  assert.equal(resolveRemotelessPlacement({ capability: "test", surface: "android", candidates, forceLocal: true }).lane, "blocked");
  assert.equal(remotelessCapability("existing-web-artifact", "ios").support, "supported");
});
