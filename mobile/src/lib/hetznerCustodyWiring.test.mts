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
  assert.match(adapter, /clearLocalHetznerManagedServer/);
  assert.match(ui, /Create encrypted backup/);
  assert.match(ui, /Validate and restore/);
  assert.match(ui, /Manage this Yaver VPS/);
  assert.match(ui, /Not a Yaver device/);
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
  assert.match(card, /Power control blocked/);
  assert.match(card, /isBoundHetznerDevice/);
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
  const handoff = await read("./hetznerHandoff.ts");
  assert.match(handoff, /credential_handoff_offer/);
  assert.match(handoff, /registerCredentialHandoffDevice/);
  assert.match(handoff, /createQuicClient/);
  assert.match(handoff, /runIsolatedCredentialP2P/);
  assert.doesNotMatch(handoff, /\bquicClient\b/);
  assert.match(await read("../components/CloudProvidersSection.tsx"), /Get from \{activeDevice\.name\} over P2P/);
});

test("boxless UI is globally disabled while active devices remain visible", async () => {
  const flags = await read("./launchFlags.ts");
  const picker = await read("../components/RemoteBoxPickerModal.tsx");
  const devices = await read("../../app/(tabs)/devices.tsx");
  const settings = await read("../../app/(tabs)/settings.tsx");
  const tvStore = await read("../../../tvos/YaverTV/YaverStore.swift");
  const vision = await read("../../../visionos/YaverVision/Views/VisionDashboardView.swift");
  const webFlags = await read("../../../web/lib/launchFlags.ts");
  const webVibe = await read("../../../web/components/dashboard/VibeCodingView.tsx");
  assert.match(flags, /ENABLE_BOXLESS_UI\s*=\s*false/);
  assert.match(picker, /ENABLE_BOXLESS_UI\s*&&/);
  assert.match(devices, /ENABLE_BOXLESS_UI\s*&&/);
  assert.match(settings, /ENABLE_BOXLESS_UI\s*&&/);
  assert.match(tvStore, /boxlessUIEnabled\s*=\s*false/);
  assert.match(vision, /if store\.remotelessAllowed/);
  assert.match(webFlags, /ENABLE_BOXLESS_UI\s*=\s*false/);
  assert.match(webVibe, /ENABLE_BOXLESS_UI \|\| runner\.id !== "remoteless"/);
  assert.match(devices, /device\.id === primaryDeviceId \|\| device\.id === activeDevice\?\.id/);
});

test("native login uses the canonical app icon component instead of a recreated glyph", async () => {
  const login = await read("../../app/login.tsx");
  const icon = await read("../components/YaverAppIcon.tsx");
  assert.match(login, /YaverAppIcon/);
  assert.match(icon, /assets\/icon\.png/);
  assert.doesNotMatch(login, /M150 150 L256 288/);
});

test("remote box picker wakes only the locally bound Hetzner VPS before connecting", async () => {
  const picker = await read("../components/RemoteBoxPickerModal.tsx");
  assert.match(picker, /isBoundHetznerDevice\(target, managed\)/);
  assert.match(picker, /setPower\(managed\.id, "power_on"\)/);
  assert.match(picker, /waiting for the Yaver agent/);
  assert.match(picker, /const probeAttempts = waitForHetznerBoot \? 24 : 1/);
});

test("client cloud abstraction is endpoint-only and registers Hetzner alone", async () => {
  const adapter = await read("./clientCloudProvider.ts");
  assert.match(adapter, /abstract class ClientCloudProviderAdapter/);
  assert.match(adapter, /class HetznerClientCloudAdapter/);
  assert.doesNotMatch(adapter, /\b(?:AWS|GCP|Azure|Alibaba)ClientCloudAdapter\b/);
  assert.doesNotMatch(adapter, /createServer|deleteServer|resizeServer/);
});
