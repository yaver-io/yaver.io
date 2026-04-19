// Smoke test against a real `yaver serve`. Opt-in via the env var
// YMH_SMOKE_AGENT_URL; if unset, the test is skipped so `bun test`
// on a fresh clone doesn't need a Go toolchain.
//
// The CI workflow (.github/workflows/mobile-headless.yml) boots an
// agent and points this test at http://127.0.0.1:18080 with a
// shared CI token.

import { describe, it, expect } from "bun:test";
import { MobileClient } from "../src/mobile-client";

const AGENT_URL = process.env.YMH_SMOKE_AGENT_URL || "";
const AGENT_TOKEN = process.env.YMH_SMOKE_AGENT_TOKEN || "";

const maybe = AGENT_URL ? describe : describe.skip;

maybe("smoke against live yaver agent", () => {
  const mobile = new MobileClient({
    agentBaseUrl: AGENT_URL,
    authToken: AGENT_TOKEN,
  });

  it("GET /info responds", async () => {
    const res = await mobile.raw.get("/info");
    expect(res.status).toBe(200);
    expect(res.body).toBeDefined();
  });

  it("install catalogue is non-empty", async () => {
    const list = await mobile.listInstallables();
    // A real agent ships >= the built-in list (git, docker, etc.).
    expect(list.length).toBeGreaterThan(3);
  });

  it("infra summary has machine + package managers", async () => {
    const s = await mobile.infraSummary();
    expect(s.machine).toBeDefined();
    expect(Array.isArray(s.packageManagers)).toBe(true);
  });

  it("wizard round-trip: start → answer → generate (dummy dir)", async () => {
    const start = await mobile.wizard.start();
    expect(start?.session?.id).toBeDefined();
    // Walk straight to done by accepting defaults for every question.
    // The real wizard has ~30 questions; just ensure start() + one
    // answer() round-trips cleanly. Skipping generate() — the real
    // agent would actually scaffold a monorepo on disk.
    const ans = await mobile.wizard.answer(start.session.id, "app_name", "ymh-smoke");
    expect(ans?.session?.id).toBe(start.session.id);
  });
});
