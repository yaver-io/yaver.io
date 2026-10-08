import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (name: string) => readFile(new URL(name, import.meta.url), "utf8");

test("machine access remains direct-only and stores both identity and addresses as secrets", async () => {
  const [sync, p2p, ssh, context, cache] = await Promise.all([
    read("./machineAccessSync.ts"), read("./credentialP2P.ts"), read("./plainSSH.ts"), read("../context/DeviceContext.tsx"), read("./connectionCache.ts"),
  ]);
  assert.match(sync, /machine_access_handoff_offer/);
  assert.match(sync, /loadOrCreateSSHIdentity/);
  assert.match(sync, /setSecret\(SSH_IDENTITY_KEY/);
  assert.match(sync, /setSecret\(ACCESS_PROFILES_KEY/);
  assert.match(sync, /saveSyncedSSHHost/);
  assert.match(p2p, /connectionMode !== "direct"/);
  assert.match(ssh, /syncedCredentialPrefix/);
  assert.match(ssh, /setSecret\(`\$\{syncedCredentialPrefix\}/);
  assert.match(context, /loadMachineAccessProfiles/);
  assert.doesNotMatch(context, /lanIps: Array\.isArray\(d\.localIps\)/);
  assert.match(cache, /getSecret\(key\(deviceId\)\)/);
  assert.match(cache, /setSecret\(key\(entry\.deviceId\)/);
  assert.doesNotMatch(cache, /AsyncStorage/);
});
