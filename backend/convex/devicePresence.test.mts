import test from "node:test";
import assert from "node:assert/strict";
import { deriveControlPlaneStatus, HEARTBEAT_STALE_MS } from "./devicePresence.ts";

const NOW = 1_800_000_000_000;

test("fresh relay evidence is relay-online", () => {
  const result = deriveControlPlaneStatus({ isOnline: true, lastHeartbeat: NOW - 60_000, relayConnected: true }, NOW);
  assert.equal(result.state, "relay-online");
  assert.equal(result.relayPath, "available");
});

test("fresh heartbeat with an explicitly dead relay is reporting, not reachable", () => {
  const result = deriveControlPlaneStatus({ isOnline: true, lastHeartbeat: NOW - 60_000, relayConnected: false }, NOW);
  assert.equal(result.state, "reporting");
  assert.equal(result.relayPath, "unavailable");
});

test("auth required outranks optimistic transport inventory", () => {
  const result = deriveControlPlaneStatus({ isOnline: true, needsAuth: true, lastHeartbeat: NOW - 1_000, relayConnected: true }, NOW);
  assert.equal(result.state, "needs-auth");
  assert.equal(result.suggestedAction, "reauth");
});

test("stale heartbeat is offline even if the stored online flag never cleared", () => {
  const result = deriveControlPlaneStatus({ isOnline: true, lastHeartbeat: NOW - HEARTBEAT_STALE_MS, relayConnected: true }, NOW);
  assert.equal(result.state, "offline");
  assert.equal(result.reasonCode, "heartbeat-stale");
});

test("a fresh relay event can prove reporting after the heartbeat lags", () => {
  const result = deriveControlPlaneStatus({
    isOnline: false,
    lastHeartbeat: NOW - HEARTBEAT_STALE_MS - 1,
    lastTunnelEvent: { online: true, at: NOW - 2_000 },
  }, NOW);
  assert.equal(result.state, "relay-online");
});

test("missing history has a named terminal state", () => {
  const result = deriveControlPlaneStatus({ isOnline: false }, NOW);
  assert.equal(result.state, "offline");
  assert.equal(result.reasonCode, "never-reported");
  assert.equal(result.lastSignalAt, null);
});
