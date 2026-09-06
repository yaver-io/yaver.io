/**
 * agent-client.test.ts — `npx tsx lib/agent-client.test.ts`.
 * Pins task-create request body serialization that Cloud Workspace handoff
 * depends on.
 */
import assert from "node:assert/strict";
import test from "node:test";

import { AgentClient, browserSessionSettings, buildCreateTaskBody } from "./agent-client";

test("web createTask body defaults allowLocalFallback to false", () => {
  const body = buildCreateTaskBody({
    title: "Build apk",
    description: "",
    userPrompt: "secret prompt",
    runner: "codex",
  });
  assert.equal(body.source, "web");
  assert.equal(body.allowLocalFallback, false);
  assert.equal(body.userPrompt, "secret prompt");
  assert.deepEqual(body.sessionSettings, browserSessionSettings());
});

test("web session settings declare browser chat/render capabilities", () => {
  assert.deepEqual(browserSessionSettings("reload-and-chat"), {
    appName: "Yaver web",
    appVersion: "",
    buildNumber: "",
    surface: "yaver-web-dashboard",
    clientSurface: "yaver-web-dashboard",
    platform: "web",
    deviceClass: "browser",
    lane: "browser",
    runtimeMode: "native",
    dogfood: false,
    usageMode: "reload-and-chat",
    chatEnabled: true,
    renderEnabled: true,
  });
});

test("web createTask body can mark final Cloud Workspace handoff", () => {
  const body = buildCreateTaskBody({
    title: "Build apk",
    description: "",
    runner: "codex",
    allowLocalFallback: true,
  });
  assert.equal(body.allowLocalFallback, true);
});

test("web createTask body carries portable project identity and MCP allowlist", () => {
  const body = buildCreateTaskBody({
    title: "Audit Medici",
    description: "",
    runner: "opencode",
    projectName: "medici.ai",
    workDir: "/Users/kivanccakmak/Workspace/medici.ai",
    projectDir: "/Users/kivanccakmak/Workspace/medici.ai",
    mcpServers: ["tusrehber"],
  });
  assert.equal(body.projectName, "medici.ai");
  assert.equal(body.projectDir, "/Users/kivanccakmak/Workspace/medici.ai");
  assert.deepEqual(body.mcpServers, ["tusrehber"]);
});

test("web bundle preview URL preserves agent-minted signature in relay mode", () => {
  const client = new AgentClient() as any;
  client.host = "ignored";
  client.port = 1234;
  client.deviceId = "device-1";
  client._activeRelayUrl = "https://public.yaver.io";

  assert.equal(
    client.webBundlePreviewUrl("/dev/web-bundle/?sig=abc&exp=123"),
    "/d/device-1/dev/web-bundle/?sig=abc&exp=123",
  );
});

// ── model coercion at the dispatch funnel (2026-08-02) ─────────────────────
// The picker fix corrected the DEFAULT; the model is also a stored per-device
// setting, so a saved gpt-5.4 kept being dispatched at a ChatGPT-account Codex
// login that cannot run it. buildCreateTaskBody is the single funnel every web
// dispatch passes through, so the request that leaves the browser must not
// carry a model we have watched this runner refuse.
// Seeded refusal evidence expires after the ledger TTL. Once it does, the
// funnel must permit a fresh probe instead of permanently hiding a model.
{
  const coerced = buildCreateTaskBody({
    title: "t", description: "d", runner: "codex", model: "gpt-5.3-codex",
  });
  if (coerced.model !== "gpt-5.3-codex") {
    console.error(`FAIL expired refusal evidence permanently rewrote the model: ${String(coerced.model)}`);
    process.exitCode = 1;
  } else {
    console.log("ok   expired refusal evidence permits a fresh model probe");
  }

  // NO FALSE RED: a model with no evidence against it is passed through
  // untouched — never silently override a deliberate choice.
  const untouched = buildCreateTaskBody({
    title: "t", description: "d", runner: "codex", model: "gpt-5.5",
  });
  if (untouched.model !== "gpt-5.5") {
    console.error(`FAIL a model with no observed refusal was rewritten: ${String(untouched.model)}`);
    process.exitCode = 1;
  } else {
    console.log("ok   a model with no observed refusal is passed through untouched");
  }

  // AND A MODEL MEASURED TO WORK IS NEVER "CORRECTED" AWAY. This is the exact
  // regression that broke the vibe loop: gpt-5.4 works on a subscription login
  // (probed on two machines, 2026-08-02) but sat in OBSERVED_REFUSALS from a
  // single 400, so the funnel rewrote it into gpt-5.3-codex — a model the
  // account genuinely refuses. Coercion that fires on a working model is worse
  // than no coercion at all.
  const working = buildCreateTaskBody({
    title: "t", description: "d", runner: "codex", model: "gpt-5.4",
  });
  if (working.model !== "gpt-5.4") {
    console.error(`FAIL a WORKING model was coerced away: gpt-5.4 -> ${String(working.model)}`);
    process.exitCode = 1;
  } else {
    console.log("ok   a model measured to work is dispatched as chosen");
  }

  // A runner we hold no opinion on is never rewritten either.
  const claude = buildCreateTaskBody({
    title: "t", description: "d", runner: "claude", model: "claude-opus-4-7",
  });
  if (claude.model !== "claude-opus-4-7") {
    console.error("FAIL a claude model was rewritten");
    process.exitCode = 1;
  } else {
    console.log("ok   a runner with no compat opinion is left alone");
  }
}

/**
 * Regression guard (2026-08-12 "Runtime target probe failed"): the
 * remote-runtime capabilities fetch built its URL with `new URL(relative)`
 * — and devBaseUrl is a RELATIVE same-origin path (`/d/<deviceId>`) when
 * the dashboard is served from yaver.io. new URL() without a base throws
 * "cannot be parsed as a URL" before any request is made, so the probe died
 * with a green connection check: inventory says OK, the operation never
 * happened. The fix passes the relative URL straight to fetch(), which
 * resolves it against the page origin — the same pattern every sibling
 * remote-runtime method uses. This test pins the CLASS: any URL built from
 * the relative dev-base must go through fetch, never new URL().
 */
test("relative role-base URLs must resolve via fetch, never new URL()", () => {
  const relative = "/d/6e8db080-a9d0-443c-a55b-b9c385522a97";
  // The bug: this throws.
  assert.throws(() => new URL(`${relative}/remote-runtime/capabilities`), /Invalid URL/);
  // The fix shape: URLSearchParams for the query + the relative path handed
  // to fetch (resolves against window.location.origin in the browser).
  const params = new URLSearchParams({ workDir: "/w", framework: "swift" });
  const path = `${relative}/remote-runtime/capabilities?${params.toString()}`;
  assert.match(path, /^\/d\//);
  assert.match(path, /remote-runtime\/capabilities/);
  assert.match(path, /workDir=/);
  assert.match(path, /framework=swift/);
  // And the same construction must NOT throw when a base is present (the
  // non-same-origin branch builds an absolute relay URL).
  const absolute = new URL(`https://public.yaver.io${relative}/remote-runtime/capabilities`);
  assert.equal(absolute.pathname, "/d/6e8db080-a9d0-443c-a55b-b9c385522a97/remote-runtime/capabilities");
});
