import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("missing BYO resource cleanup requires the full tenant and resource identity", async () => {
  const source = await readFile(new URL("./byoMachines.ts", import.meta.url), "utf8");
  assert.match(source, /purgeMissingResourceBinding/);
  assert.match(source, /q\.eq\("userId", args\.userId\)\.eq\("serverId", args\.serverId\)/);
  assert.match(source, /row\.provider !== args\.provider/);
  assert.match(source, /row\.deviceId !== args\.deviceId/);
  assert.match(source, /row\.serverIp !== args\.serverIp/);
  assert.doesNotMatch(source, /purgeMissingResourceBinding[\s\S]*fetch\(`https:\/\/api\.hetzner/);
});

test("primary-device repair cannot bind another tenant's device", async () => {
  const source = await readFile(new URL("./userSettings.ts", import.meta.url), "utf8");
  assert.match(source, /setPrimaryDeviceByEmail/);
  assert.match(source, /device\.userId !== user\._id/);
  assert.match(source, /device\.removed === true/);
  assert.match(source, /OWNED_ACTIVE_DEVICE_NOT_FOUND/);
});

test("bulk stale cloud cleanup is exact-id, owner-scoped, and provider-blind", async () => {
  const source = await readFile(new URL("./cloudMachines.ts", import.meta.url), "utf8");
  assert.match(source, /purgeMissingProviderRows/);
  assert.match(source, /row\.userId !== args\.userId/);
  assert.match(source, /MACHINE_OWNERSHIP_MISMATCH/);
  assert.match(source, /DUPLICATE_MACHINE_ID/);
  const block = source.slice(source.indexOf("export const purgeMissingProviderRows"), source.indexOf("export const get =", source.indexOf("export const purgeMissingProviderRows")));
  assert.doesNotMatch(block, /fetch\(|api\.hetzner|deleteMachine|poweroff/);
});
