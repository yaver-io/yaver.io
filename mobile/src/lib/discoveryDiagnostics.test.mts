// discoveryDiagnostics.test.mts — every fail/success path for the
// Hot Reload discovery preflight.
// Run: npx tsx src/lib/discoveryDiagnostics.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  runDiscoveryDiagnostics,
  type CheckId,
  type CheckStatus,
  type DiagnosticsProbe,
} from "./discoveryDiagnostics.ts";

// ── tiny fetch mock ──────────────────────────────────────────────────
// Maps a path-suffix → a handler returning {status, body}. Each handler
// gets the call index so /projects/mobile can evolve across polls.

type RouteReply = { status?: number; body?: any; throw?: boolean };
type Route = (callIndex: number, method: string) => RouteReply;

function makeProbe(routes: Record<string, Route>): DiagnosticsProbe {
  const counts: Record<string, number> = {};
  const fetchImpl = (async (url: string, init: RequestInit = {}) => {
    const method = (init.method || "GET").toUpperCase();
    const key = Object.keys(routes).find((k) => String(url).includes(k));
    if (!key) throw new Error(`no route for ${url}`);
    const i = counts[key] ?? 0;
    counts[key] = i + 1;
    const reply = routes[key](i, method);
    if (reply.throw) throw new Error("network");
    const status = reply.status ?? 200;
    return {
      ok: status >= 200 && status < 300,
      status,
      json: async () => reply.body ?? {},
    } as Response;
  }) as unknown as typeof fetch;

  return {
    baseUrl: "https://agent.test",
    authHeaders: { Authorization: "Bearer t" },
    host: "Mac-mini.local",
    fetchImpl,
    now: (() => {
      let t = 0;
      return () => (t += 2000); // each call advances 2s — bounds the scan loop
    })(),
    sleep: async () => {}, // instant
    scanBudgetMs: 14000,
  };
}

function statusOf(checks: { id: CheckId; status: CheckStatus }[], id: CheckId): CheckStatus {
  return checks.find((c) => c.id === id)!.status;
}

// Healthy routes — reused and selectively overridden per test.
const happy: Record<string, Route> = {
  "/health": () => ({ body: { hostname: "Mac-mini.local", version: "1.99.190" } }),
  "/auth/status": () => ({ body: { authenticated: true } }),
  "/info": () => ({ body: { hostname: "Mac-mini.local", version: "1.99.190", workDir: "/Users/x" } }),
  "/runner-auth/status": () => ({ body: { runners: [{ id: "claude-code", name: "Claude Code", authConfigured: true }] } }),
  "/projects/mobile": (i, method) => {
    if (method === "POST") return { body: { ok: true } };
    return { body: { ok: true, scanning: false, projects: [{ name: "app", path: "/Users/x/app" }] } };
  },
};

test("all green → ok", async () => {
  const r = await runDiscoveryDiagnostics(makeProbe(happy));
  assert.equal(r.overall, "ok");
  for (const id of ["reachable", "agentSignedIn", "authorized", "runnerOAuth", "filesystem"] as CheckId[]) {
    assert.equal(statusOf(r.checks, id), "pass", `${id} should pass`);
  }
});

test("unreachable agent → fail + skip downstream, blocked", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({ ...happy, "/health": () => ({ throw: true }) }),
  );
  assert.equal(statusOf(r.checks, "reachable"), "fail");
  assert.equal(statusOf(r.checks, "agentSignedIn"), "skip");
  assert.equal(statusOf(r.checks, "filesystem"), "skip");
  assert.equal(r.overall, "blocked");
  // remediation must be actionable
  const reach = r.checks.find((c) => c.id === "reachable")!;
  assert.ok((reach.remediation?.length ?? 0) >= 2);
  assert.equal(reach.action?.kind, "openDevices");
});

test("health 500 → warn but transport works, continues", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({ ...happy, "/health": () => ({ status: 500 }) }),
  );
  assert.equal(statusOf(r.checks, "reachable"), "warn");
  // continues downstream rather than skipping
  assert.equal(statusOf(r.checks, "authorized"), "pass");
});

test("agent not signed in → fail blocked, includes yaver auth step", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({ ...happy, "/auth/status": () => ({ body: { authenticated: false, reason: "no_token" } }) }),
  );
  assert.equal(statusOf(r.checks, "agentSignedIn"), "fail");
  assert.equal(r.overall, "blocked");
  const step = r.checks.find((c) => c.id === "agentSignedIn")!;
  assert.ok(step.remediation?.some((s) => s.includes("yaver auth")), "should tell user to run yaver auth");
});

test("auth/status missing on old agent → skip, not fail", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({ ...happy, "/auth/status": () => ({ status: 404 }) }),
  );
  assert.equal(statusOf(r.checks, "agentSignedIn"), "skip");
  assert.equal(r.overall, "ok"); // /info still gates real auth
});

test("phone not authorized (401 on /info) → fail blocked, skip runner+fs", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({ ...happy, "/info": () => ({ status: 401 }) }),
  );
  assert.equal(statusOf(r.checks, "authorized"), "fail");
  assert.equal(statusOf(r.checks, "runnerOAuth"), "skip");
  assert.equal(statusOf(r.checks, "filesystem"), "skip");
  assert.equal(r.overall, "blocked");
  assert.equal(r.checks.find((c) => c.id === "authorized")!.action?.kind, "reauth");
});

test("no runner OAuth configured → warn (degraded), discovery still passes", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({
      ...happy,
      "/runner-auth/status": () => ({ body: { runners: [{ id: "claude-code", name: "Claude Code", authConfigured: false }] } }),
    }),
  );
  assert.equal(statusOf(r.checks, "runnerOAuth"), "warn");
  assert.equal(statusOf(r.checks, "filesystem"), "pass");
  assert.equal(r.overall, "degraded");
  assert.equal(r.checks.find((c) => c.id === "runnerOAuth")!.action?.kind, "runnerAuth");
});

test("scan never settles (stuck) → filesystem fail, Full Disk Access hint", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({
      ...happy,
      "/projects/mobile": (_i, method) =>
        method === "POST"
          ? { body: { ok: true } }
          : { body: { ok: true, scanning: true, projects: [] } }, // forever scanning
    }),
  );
  assert.equal(statusOf(r.checks, "filesystem"), "fail");
  assert.equal(r.overall, "blocked");
  const fs = r.checks.find((c) => c.id === "filesystem")!;
  assert.ok(fs.remediation?.some((s) => /Full Disk Access/i.test(s)), "should mention Full Disk Access");
  assert.equal(fs.action?.kind, "retryScan");
});

test("scan settles but empty → warn (degraded) with where-to-put-projects help", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({
      ...happy,
      "/projects/mobile": (_i, method) =>
        method === "POST" ? { body: { ok: true } } : { body: { ok: true, scanning: false, projects: [] } },
    }),
  );
  assert.equal(statusOf(r.checks, "filesystem"), "warn");
  assert.equal(r.overall, "degraded");
});

test("scan returns projects while still scanning → pass (don't wait for full settle)", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({
      ...happy,
      "/projects/mobile": (i, method) => {
        if (method === "POST") return { body: { ok: true } };
        // still scanning, but projects are already arriving
        return { body: { ok: true, scanning: true, projects: [{ name: "a", path: "/p/a" }] } };
      },
    }),
  );
  assert.equal(statusOf(r.checks, "filesystem"), "pass");
});

test("agent reports permDenied → filesystem fail with Full Disk Access steps", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({
      ...happy,
      "/projects/mobile": (_i, method) =>
        method === "POST"
          ? { body: { ok: true } }
          : { body: { ok: true, scanning: false, projects: [], permDenied: 7 } },
    }),
  );
  assert.equal(statusOf(r.checks, "filesystem"), "fail");
  const fs = r.checks.find((c) => c.id === "filesystem")!;
  assert.ok(fs.detail?.includes("7 folders"), "should report the count");
  assert.ok(fs.remediation?.some((s) => /Full Disk Access/i.test(s)));
});

test("permDenied but some projects found → warn (partial), still passes overall as degraded", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({
      ...happy,
      "/projects/mobile": (_i, method) =>
        method === "POST"
          ? { body: { ok: true } }
          : { body: { ok: true, scanning: false, projects: [{ name: "a", path: "/p/a" }], permDenied: 2 } },
    }),
  );
  assert.equal(statusOf(r.checks, "filesystem"), "warn");
  assert.equal(r.overall, "degraded");
});

test("agent reports timedOut → filesystem fail with home-dir-size steps", async () => {
  const r = await runDiscoveryDiagnostics(
    makeProbe({
      ...happy,
      "/projects/mobile": (_i, method) =>
        method === "POST"
          ? { body: { ok: true } }
          : { body: { ok: true, scanning: false, projects: [], timedOut: true } },
    }),
  );
  assert.equal(statusOf(r.checks, "filesystem"), "fail");
  assert.ok(r.checks.find((c) => c.id === "filesystem")!.detail?.includes("time limit"));
});

test("onUpdate streams live snapshots ending in terminal states", async () => {
  const snapshots: CheckStatus[][] = [];
  await runDiscoveryDiagnostics(makeProbe(happy), (checks) => {
    snapshots.push(checks.map((c) => c.status));
  });
  assert.ok(snapshots.length > 5, "should emit multiple progress snapshots");
  const last = snapshots[snapshots.length - 1];
  // no check left pending/running at the end
  assert.ok(!last.includes("pending"));
  assert.ok(!last.includes("running"));
});

console.log("discoveryDiagnostics: all tests defined");
