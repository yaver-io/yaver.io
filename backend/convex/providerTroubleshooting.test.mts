import test from "node:test";
import assert from "node:assert/strict";

import { classifyRuntimeTroubleshooting } from "./providerTroubleshooting.js";

test("provider create failure is classified before agent or relay checks", () => {
  const verdict = classifyRuntimeTroubleshooting({
    provider: "aws",
    providerCreateOk: false,
    agentHeartbeatOk: false,
    relayDataPathOk: false,
    lastProviderError: "quota exceeded",
  });

  assert.equal(verdict.ok, false);
  assert.equal(verdict.plane, "provider");
  assert.equal(verdict.code, "provider_create_failed");
  assert.match(verdict.summary, /quota exceeded/);
});

test("running provider without heartbeat points at cloud-init and agent service", () => {
  const verdict = classifyRuntimeTroubleshooting({
    provider: "gcp",
    providerCreateOk: true,
    providerStatus: "running",
    agentHeartbeatOk: false,
  });

  assert.equal(verdict.plane, "agent");
  assert.equal(verdict.code, "agent_not_online");
  assert.match(verdict.nextProbe, /cloud-init/);
});

test("agent heartbeat with broken relay is a relay data path issue", () => {
  const verdict = classifyRuntimeTroubleshooting({
    provider: "azure",
    providerCreateOk: true,
    providerStatus: "running",
    agentHeartbeatOk: true,
    relayDataPathOk: false,
  });

  assert.equal(verdict.plane, "relay");
  assert.equal(verdict.code, "relay_data_path_down");
  assert.match(verdict.nextProbe, /relay presence/);
});

test("direct ssh can fail after Yaver relay control is ready", () => {
  const verdict = classifyRuntimeTroubleshooting({
    provider: "hetzner",
    providerCreateOk: true,
    providerStatus: "running",
    agentHeartbeatOk: true,
    relayDataPathOk: true,
    sshReachable: false,
  });

  assert.equal(verdict.plane, "ssh");
  assert.equal(verdict.code, "ssh_unreachable");
  assert.match(verdict.summary, /controllable through Yaver/);
});
