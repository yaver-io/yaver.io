import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../../", import.meta.url);
const read = (path: string) => readFile(new URL(path, root), "utf8");

test("surface matrix is exhaustive and makes unsupported releases explicit", async () => {
  const matrix = JSON.parse(await read("shared/zero-knowledge/hetzner-surface-matrix.json"));
  const expected = [
    "ios", "android", "macos", "windows", "linux", "mcp", "tvos", "visionos",
    "android_tv", "watchos", "wearos", "carplay", "android_auto", "browser_web",
    "xbox", "playstation", "meta_quest",
  ];
  assert.deepEqual(matrix.allowedActions, ["list", "activity", "rename", "power_on", "shutdown"]);
  assert.deepEqual(matrix.surfaces.map((row: any) => row.id).sort(), expected.sort());
  for (const row of matrix.surfaces) {
    assert.match(await read(row.evidence), /./, `${row.id} evidence is missing`);
    if (["xbox", "playstation", "meta_quest"].includes(row.id)) assert.equal(row.state, "planned");
    if (row.id === "browser_web") assert.equal(row.state, "disabled");
    if (["carplay", "android_auto"].includes(row.id)) assert.equal(row.state, "delegated");
    if (["tvos", "visionos", "android_tv", "watchos", "wearos"].includes(row.id)) {
      assert.equal(row.credential, "none", `${row.id} must not hold a provider token`);
    }
  }
});

test("shared Hetzner contract permits only list, activity, rename, power-on, and graceful shutdown", async () => {
  const schema = JSON.parse(await read("shared/zero-knowledge/hetzner-power.schema.json"));
  assert.deepEqual(schema.properties.action.enum, ["list", "activity", "rename", "power_on", "shutdown"]);
  assert.equal(schema.additionalProperties, false);
  assert.equal(schema.properties.serverId.pattern, "^[1-9][0-9]*$");
  assert.doesNotMatch(JSON.stringify(schema), /delete|create|resize|rebuild|poweroff|reset/i);
});

test("every supported native client family uses the same endpoint verb", async () => {
  const clients = [
    "desktop/agent/ops_hetzner_power.go",
    "web/components/dashboard/ByoCloudPanel.tsx", // Electron desktop renderer only
    "tvos/YaverTV/AgentClient.swift", // shared by visionOS
    "androidtv/app/src/main/kotlin/io/yaver/tv/OpsClient.kt",
    "watch/YaverWatch/DesktopVoiceClient.swift",
    "wear/app/src/main/kotlin/io/yaver/wear/DesktopVoiceClient.kt",
  ];
  for (const path of clients) {
    assert.match(await read(path), /hetzner_power/, `${path} drifted from the canonical verb`);
  }
  assert.match(await read("tvos/YaverTV/AgentClient.swift"), /verb == "hetzner_power" && endpoint\.relay/);
  assert.match(await read("androidtv/app/src/main/kotlin/io/yaver/tv/OpsClient.kt"), /allowRelay = verb != "hetzner_power"/);
  assert.match(await read("mobile/src/lib/hetznerHandoff.ts"), /runIsolatedCredentialP2P/);
  assert.match(await read("mobile/src/lib/credentialP2P.ts"), /connectionMode !== "direct"/);
  assert.match(await read("watch/YaverWatch/DesktopVoiceClient.swift"), /hasPrivatePeerRoute/);
  assert.match(await read("wear/app/src/main/kotlin/io/yaver/wear/DesktopVoiceClient.kt"), /hasPrivatePeerRoute/);
});

test("thin clients never accept or store a Hetzner provider credential", async () => {
  for (const path of [
    "tvos/YaverTV/AgentClient.swift",
    "tvos/YaverTV/CredentialStore.swift",
    "androidtv/app/src/main/kotlin/io/yaver/tv/OpsClient.kt",
    "androidtv/app/src/main/kotlin/io/yaver/tv/CredentialStore.kt",
    "watch/YaverWatch/DesktopVoiceClient.swift",
    "wear/app/src/main/kotlin/io/yaver/wear/DesktopVoiceClient.kt",
  ]) {
    assert.doesNotMatch(await read(path), /hetzner-api-token|HCLOUD_TOKEN|Authorization:\s*Bearer\s*\+?.*hetzner/i, path);
  }
});

test("inspectable relay ingress fails before endpoint credential lookup", async () => {
  const source = await read("desktop/agent/ops_hetzner_power.go");
  const relayGuard = source.indexOf('Get("X-Yaver-Via-Relay")');
  const credentialRead = source.indexOf("accountField(ProviderHetzner");
  assert.ok(relayGuard >= 0 && credentialRead > relayGuard);
  assert.match(source, /secure_transport_required/);
  assert.match(source, /SurfaceWeb/);
  assert.match(source, /unsupported_surface/);
});

test("browser dashboard cannot render Hetzner power control", async () => {
  const panel = await read("web/components/dashboard/ByoCloudPanel.tsx");
  const client = await read("web/lib/agent-client.ts");
  assert.match(panel, /surface === "desktop-gui"/);
  assert.match(panel, /if \(!desktopShell/);
  assert.match(client, /desktopShell \? "desktop" : "web"/);
});

test("visionOS really compiles the shared tvOS Hetzner client", async () => {
  const project = await read("visionos/project.yml");
  assert.match(project, /\.\.\/tvos\/YaverTV\/AgentClient\.swift/);
});

test("car surfaces delegate to the phone and expose no driving power mutation", async () => {
  for (const path of [
    "mobile/native-carplay/ios/YaverCarPlaySceneDelegate.swift",
    "mobile/app/car-voice-coding.tsx",
  ]) {
    const source = await read(path);
    assert.doesNotMatch(source, /hetzner_power|power_on|shutdownLocalHetzner|HCLOUD_TOKEN/i, path);
  }
});
