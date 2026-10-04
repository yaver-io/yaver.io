import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (relative: string) => readFile(new URL(relative, import.meta.url), "utf8");

test("Hetzner credentials use a stable native credential service with legacy migration", async () => {
  const source = await read("./secure-storage.ts");
  const appConfig = await read("../../app.json");
  assert.match(source, /works\.yaver\.credentials\.v1/);
  assert.match(source, /keychainAccessible:\s*SecureStore\.WHEN_UNLOCKED/);
  assert.match(source, /SecureStore\.getItemAsync\(key, secretOptions\)/);
  assert.match(source, /const legacy = await SecureStore\.getItemAsync\(key\)/);
  assert.doesNotMatch(source, /requireAuthentication:\s*true/);
  assert.match(appConfig, /"expo-secure-store"/);
  assert.match(appConfig, /"configureAndroidBackup": true/);
});

test("tenant Hetzner surface is direct, recovery-capable, and limited to up/down", async () => {
  const core = await read("./hetznerDirectCore.ts");
  const adapter = await read("./hetznerDirect.ts");
  const ui = await read("../components/CloudProvidersSection.tsx");
  assert.match(core, /https:\/\/api\.hetzner\.cloud\/v1/);
  assert.match(core, /actions\/poweron/);
  assert.match(core, /actions\/shutdown/);
  assert.match(core, /method:\s*["']PUT["']/);
  assert.doesNotMatch(core, /method:\s*["']DELETE["']/);
  assert.doesNotMatch(adapter, /deleteHetznerServer/);
  assert.match(adapter, /exportLocalHetznerRecovery/);
  assert.match(adapter, /importLocalHetznerRecovery/);
  assert.match(adapter, /requireManagedServer/);
  assert.match(adapter, /hetznerManagedServer/);
  assert.match(ui, /Create encrypted backup/);
  assert.match(ui, /Validate and restore/);
  assert.match(ui, /Manage this server/);
  assert.match(ui, /Save name/);
  assert.match(ui, /Hetzner activity/);
  assert.doesNotMatch(ui, />Delete</);
});

test("Devices renders provider-authoritative status without enabling web credential use", async () => {
  const screen = await read("../../app/(tabs)/devices.tsx");
  const card = await read("../components/HetznerManagedDeviceCard.tsx");
  assert.match(screen, /HetznerManagedDeviceCard/);
  assert.match(card, /Provider status:/);
  assert.match(card, /Platform\.OS === "web"/);
  assert.match(card, /separate from Yaver agent connectivity/);
  assert.doesNotMatch(card, /Convex|Cloudflare|relayPacket/);
});

test("Hetzner token can move only in an authenticated encrypted device handoff", async () => {
  const protocol = await read("./credentialHandoff.ts");
  const store = await read("./credentialHandoffStore.ts");
  const screen = await read("../../app/secure-handoff.tsx");
  assert.match(protocol, /"hetzner-api-token"/);
  assert.match(protocol, /nacl\.box\(/);
  assert.match(store, /"hetzner-api-token": LOCAL_KEYS\.hetznerToken/);
  assert.match(screen, /"hetzner-api-token": "Hetzner API token"/);
});

test("client cloud abstraction is endpoint-only and registers Hetzner alone", async () => {
  const adapter = await read("./clientCloudProvider.ts");
  assert.match(adapter, /abstract class ClientCloudProviderAdapter/);
  assert.match(adapter, /class HetznerClientCloudAdapter/);
  assert.doesNotMatch(adapter, /\b(?:AWS|GCP|Azure|Alibaba)ClientCloudAdapter\b/);
  assert.doesNotMatch(adapter, /createServer|deleteServer|resizeServer/);
});
