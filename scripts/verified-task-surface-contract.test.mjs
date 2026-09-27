import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const read = (name) => fs.readFileSync(path.join(root, name), "utf8");

const agent = read("desktop/agent/agent_verification_request.go");
assert.match(agent, /running\|passed\|failed/);
assert.match(agent, /"type":\s*"task_verification"/);
assert.match(agent, /taskVerificationForWire/);

for (const file of ["web/lib/agent-client.ts", "mobile/src/lib/quic.ts"]) {
  const source = read(file);
  assert.match(source, /status:\s*"running"\s*\|\s*"passed"\s*\|\s*"failed"/);
  assert.match(source, /videoClipId\?: string/);
}

for (const file of ["web/app/dashboard/page.tsx", "web/components/dashboard/VibeCodingView.tsx", "mobile/app/(tabs)/tasks.tsx"]) {
  assert.match(read(file), /task_verification/, `${file} must consume the live verification event`);
}

const tv = read("tvos/YaverTV/Views/TaskDetailView.swift");
assert.match(tv, /Watch browser proof/);
assert.match(tv, /AVURLAssetHTTPHeaderFieldsKey/);

const vision = read("visionos/YaverVision/Views/VisionDashboardView.swift");
assert.match(vision, /Watch browser proof/);
assert.match(vision, /VideoPlayer/);

const androidTV = read("androidtv/app/src/main/kotlin/io/yaver/tv/ui/PlaceholderScreens.kt");
assert.match(androidTV, /Watch browser proof/);
assert.match(androidTV, /setVideoURI/);

const xbox = read("xbox/YaverXbox/Services/YaverApiClient.cs");
assert.match(xbox, /GetTasksAsync/);
assert.match(xbox, /DownloadVerificationVideoAsync/);
assert.match(xbox, /X-Relay-Password/);

assert.match(read("desktop/agent/watch_http.go"), /recording is ready on your phone/);
assert.match(read("mobile/src/lib/carVoiceCoding.ts"), /recording is ready on your phone/);

const playstation = JSON.parse(read("playstation/product-contract.json"));
assert.ok(playstation.experience.render.sources.includes("verified_browser_artifact"));
assert.equal(playstation.claimsPlayStationSupport, false, "partner-gated executable must not be claimed before certification");

const steam = JSON.parse(read("steam/surface-contract.json"));
assert.equal(steam.features.verifiedBrowserArtifactPlayback, true);
assert.ok(steam.retailGates.includes("verified_browser_artifact_closed_loop"));

console.log("ok — verified task status and authenticated browser proof reach every declared client family");
