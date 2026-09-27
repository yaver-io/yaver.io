"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { AgentManager, resolveAgentBinary, probeAgentHealth, healthNeedsPairing, agentEnv, AGENT_PORT, HEALTH_WAIT_MS } = require("../src/agent-manager");

test("AGENT_PORT is 18080 (matches the agent's default)", () => {
  assert.equal(AGENT_PORT, 18080);
});

test("cold desktop startup does not false-fail at the old 15 second boundary", () => {
  assert.ok(HEALTH_WAIT_MS >= 30_000);
});

// The desktop GUI must NEVER trigger a macOS keychain prompt. The agent is
// spawned with YAVER_VAULT_SKIP_KEYCHAIN=1, which the agent honors as a
// global keychain gate (vault_keychain.go::keychainAccessDisabled) for both
// the vault mirror AND the runner-auth probe — the two `security` shell-outs
// that prompt. Pin the env so the gate can't silently regress.
test("agentEnv disables keychain prompts and agent-owned auto-start", () => {
  const env = agentEnv();
  assert.equal(env.YAVER_VAULT_SKIP_KEYCHAIN, "1");
  assert.equal(env.YAVER_SKIP_AUTO_START, "1");
  // And it must be inherited through to the spawned child, not dropped.
  const old = process.env.YAVER_VAULT_SKIP_KEYCHAIN;
  try {
    delete process.env.YAVER_VAULT_SKIP_KEYCHAIN;
    const env2 = agentEnv();
    assert.equal(env2.YAVER_VAULT_SKIP_KEYCHAIN, "1");
    assert.equal(env2.YAVER_SKIP_AUTO_START, "1");
  } finally {
    if (old === undefined) delete process.env.YAVER_VAULT_SKIP_KEYCHAIN;
    else process.env.YAVER_VAULT_SKIP_KEYCHAIN = old;
  }
});

test("probeAgentHealth resolves ok=false on an unbound port", async () => {
  // Bind a port, grab it, close it, then probe — guarantees nothing listens.
  const srv = http.createServer();
  await new Promise((res) => srv.listen(0, "127.0.0.1", res));
  const port = srv.address().port;
  await new Promise((res) => srv.close(res));
  const h = await probeAgentHealth(port, 500);
  assert.equal(h.ok, false);
});

test("probeAgentHealth resolves ok=true on a healthy /health endpoint", async () => {
  const srv = http.createServer((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ ok: true, lifecycle: { state: "ready-to-connect" } }));
  });
  await new Promise((res) => srv.listen(0, "127.0.0.1", res));
  const port = srv.address().port;
  try {
    const h = await probeAgentHealth(port, 1000);
    assert.equal(h.ok, true);
    assert.equal(h.data.lifecycle.state, "ready-to-connect");
  } finally {
    await new Promise((res) => srv.close(res));
  }
});

test("bootstrap health is named as pairing, never a ready false-green", async () => {
  const srv = http.createServer((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ ok: true, mode: "bootstrap", needsAuth: true }));
  });
  await new Promise((res) => srv.listen(0, "127.0.0.1", res));
  const port = srv.address().port;
  const statuses = [];
  const mgr = new AgentManager({ port, onStatus: (s) => statuses.push(s) });
  try {
    const health = await probeAgentHealth(port, 1000);
    assert.equal(healthNeedsPairing(health), true);
    assert.equal(await mgr.start(), "pairing");
    assert.equal(statuses.at(-1).state, "pairing");
    assert.match(statuses.at(-1).detail, /waiting to be paired/);
  } finally {
    await new Promise((res) => srv.close(res));
  }
});

test("restart refuses to kill an adopted externally-managed agent", async () => {
  const mgr = new AgentManager();
  mgr.started = true;
  mgr.child = null;
  const result = await mgr.restart();
  assert.equal(result.ok, false);
  assert.match(result.error, /external service/);
});

test("probeAgentHealth treats non-ok JSON as unhealthy", async () => {
  const srv = http.createServer((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ ok: false, error: "missing auth" }));
  });
  await new Promise((res) => srv.listen(0, "127.0.0.1", res));
  const port = srv.address().port;
  try {
    const h = await probeAgentHealth(port, 1000);
    assert.equal(h.ok, false);
  } finally {
    await new Promise((res) => srv.close(res));
  }
});

test("resolveAgentBinary returns null when nothing is available", () => {
  // PATH is scrubbed to empty and the CLI cache is pointed at a temp dir that
  // does not exist, so every resolution layer must come up empty.
  const oldPath = process.env.PATH;
  const oldHome = process.env.HOME;
  const oldResources = process.resourcesPath;
  const oldExplicit = process.env.YAVER_AGENT_BINARY;
  try {
    delete process.env.YAVER_AGENT_BINARY;
    delete process.env.PATH;
    process.env.HOME = "/nonexistent-yaver-test-home-xyz";
    delete process.resourcesPath;
    assert.equal(resolveAgentBinary(), null);
  } finally {
    process.env.PATH = oldPath;
    process.env.HOME = oldHome;
    if (oldResources === undefined) delete process.resourcesPath;
    else process.resourcesPath = oldResources;
    if (oldExplicit === undefined) delete process.env.YAVER_AGENT_BINARY;
    else process.env.YAVER_AGENT_BINARY = oldExplicit;
  }
});

test("YAVER_AGENT_BINARY selects the exact executable under test", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "yaver-electron-agent-"));
  const bin = path.join(dir, process.platform === "win32" ? "yaver.exe" : "yaver");
  fs.writeFileSync(bin, "test", { mode: 0o700 });
  const old = process.env.YAVER_AGENT_BINARY;
  try {
    process.env.YAVER_AGENT_BINARY = bin;
    assert.equal(resolveAgentBinary(), bin);
    process.env.YAVER_AGENT_BINARY = path.join(dir, "missing");
    assert.notEqual(resolveAgentBinary(), process.env.YAVER_AGENT_BINARY);
  } finally {
    if (old === undefined) delete process.env.YAVER_AGENT_BINARY;
    else process.env.YAVER_AGENT_BINARY = old;
  }
});

test("AgentManager adopts a healthy running agent without spawning", async () => {
  const srv = http.createServer((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ ok: true, version: "test" }));
  });
  await new Promise((res) => srv.listen(0, "127.0.0.1", res));
  const port = srv.address().port;
  const statuses = [];
  const mgr = new AgentManager({ port, onStatus: (s) => statuses.push(s.state) });
  try {
    const result = await mgr.start();
    assert.equal(result, "adopted");
    assert.deepEqual(statuses, ["adopted"]);
    assert.equal(mgr.child, null); // adopted — nothing of ours to kill
  } finally {
    await new Promise((res) => srv.close(res));
  }
});

test("concurrent Desktop starts share one adoption probe and one lifecycle result", async () => {
  let healthRequests = 0;
  const srv = http.createServer((_req, res) => {
    healthRequests += 1;
    setTimeout(() => {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ ok: true, version: "standalone" }));
    }, 20);
  });
  await new Promise((res) => srv.listen(0, "127.0.0.1", res));
  const port = srv.address().port;
  const statuses = [];
  const mgr = new AgentManager({ port, onStatus: (status) => statuses.push(status.state) });
  try {
    const [first, second] = await Promise.all([mgr.start(), mgr.start()]);
    assert.equal(first, "adopted");
    assert.equal(second, "adopted");
    assert.equal(healthRequests, 1);
    assert.deepEqual(statuses, ["adopted"]);
    assert.equal(mgr.child, null);
  } finally {
    await new Promise((res) => srv.close(res));
  }
});

test("AgentManager reports 'missing' when no binary and nothing is listening", async () => {
  // Reserve + free a port so nothing listens on it.
  const srv = http.createServer();
  await new Promise((res) => srv.listen(0, "127.0.0.1", res));
  const port = srv.address().port;
  await new Promise((res) => srv.close(res));

  const oldPath = process.env.PATH;
  const oldHome = process.env.HOME;
  const oldResources = process.resourcesPath;
  const statuses = [];
  const mgr = new AgentManager({ port, onStatus: (s) => statuses.push(s.state) });
  try {
    delete process.env.PATH;
    process.env.HOME = "/nonexistent-yaver-test-home-xyz";
    delete process.resourcesPath;
    const result = await mgr.start();
    assert.equal(result, "missing");
    assert.ok(statuses.includes("missing"));
  } finally {
    process.env.PATH = oldPath;
    process.env.HOME = oldHome;
    if (oldResources === undefined) delete process.resourcesPath;
    else process.resourcesPath = oldResources;
    await mgr.stop();
  }
});
