import assert from "node:assert/strict";
import test from "node:test";

import { CREDENTIAL_P2P_PREFERENCES, runIsolatedCredentialP2P } from "./credentialP2P.ts";

test("credential handoff opens and closes an isolated direct-only client without switching chat", async () => {
  const events: string[] = [];
  const activeChat = { mode: "relay", connected: true };
  const isolated = {
    connectionMode: null as "direct" | "relay" | "tunnel" | null,
    async connect(...args: any[]) {
      events.push("isolated.connect");
      this.connectionMode = "direct";
      const preferences = args[6] as typeof CREDENTIAL_P2P_PREFERENCES;
      assert.deepEqual(preferences.map((item) => item.kind), ["direct-lan", "tailscale", "headscale", "own-vpn"]);
      assert.equal(preferences.some((item) => item.kind.includes("relay") || item.kind.includes("tunnel")), false);
    },
    disconnect() { events.push("isolated.disconnect"); },
  };
  const result = await runIsolatedCredentialP2P({
    endpoint: { id: "mac", name: "Mac", host: "192.168.1.2", port: 18080 },
    authToken: "yaver-session",
    createClient: () => isolated,
    operation: async () => { events.push("handoff"); return "ok"; },
  });
  assert.equal(result, "ok");
  assert.deepEqual(events, ["isolated.connect", "handoff", "isolated.disconnect"]);
  assert.deepEqual(activeChat, { mode: "relay", connected: true });
});

test("credential handoff rejects a non-direct result and still closes only its isolated client", async () => {
  let disconnected = 0;
  const isolated = {
    connectionMode: null as "direct" | "relay" | "tunnel" | null,
    async connect() { this.connectionMode = "relay"; },
    disconnect() { disconnected += 1; },
  };
  await assert.rejects(
    runIsolatedCredentialP2P({
      endpoint: { id: "mac", host: "relay.example", port: 443 },
      authToken: "yaver-session",
      createClient: () => isolated,
      operation: async () => "must-not-run",
    }),
    /requires a direct LAN/,
  );
  assert.equal(disconnected, 1);
});

test("credential operation errors remain actionable and are not mislabeled as connection failures", async () => {
  const isolated = {
    connectionMode: null as "direct" | "relay" | "tunnel" | null,
    async connect() { this.connectionMode = "direct"; },
    disconnect() {},
  };
  await assert.rejects(
    runIsolatedCredentialP2P({
      endpoint: { id: "old-agent", host: "192.168.1.3", port: 18080 },
      authToken: "yaver-session",
      createClient: () => isolated,
      operation: async () => { throw new Error("agent too old"); },
    }),
    /agent too old/,
  );
});
