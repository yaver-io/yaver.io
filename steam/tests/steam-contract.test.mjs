import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
const root = path.resolve(import.meta.dirname, "..");
const read = (name) => fs.readFileSync(path.join(root, name), "utf8");
const upload = read("upload.sh");
const build = read("app_build.vdf.template");
const prep = read("prepare-depot.sh");
const surface = JSON.parse(read("surface-contract.json"));
assert.match(upload, /--upload/);
assert.match(upload, /STEAM_USERNAME/);
assert.doesNotMatch(upload + build, /password|["']\d{5,}["']/i);
assert.match(build, /__DEPOT_WINDOWS__/);
assert.match(build, /__DEPOT_MACOS__/);
assert.match(build, /__DEPOT_LINUX__/);
assert.match(build, /depot_windows\.vdf/);
for (const platform of ["windows", "macos", "linux"]) {
  const depot = read(`depot_${platform}.vdf.template`);
  assert.match(depot, /DepotBuildConfig/);
  assert.match(depot, /ContentRoot/);
}
assert.match(prep, /Windows depots must be built and signed on Windows/);
assert.match(prep, /Linux depots must be built on Linux/);
assert.match(prep, /stapler validate/);
assert.match(prep, /STEAM_APP_BUNDLE/);
assert.match(prep, /refusing a false-ready Steam depot/);
assert.equal(surface.features.chat, true);
assert.equal(surface.features.pushToTalkStt, true);
assert.equal(surface.features.ttsWithStopAndReplay, true);
assert.equal(surface.features.browserRemoteRender, true);
assert.equal(surface.features.verifiedBrowserArtifactPlayback, true);
assert.equal(surface.features.hermesNativeRemoteRender, true);
assert.equal(surface.features.hotMic, false);
assert.equal(surface.renderPolicy.executesUntrustedHermesInSteamShell, false);
for (const evidence of surface.evidence) assert.ok(fs.existsSync(path.join(root, "..", evidence)), `missing ${evidence}`);
for (const gate of ["steam_deck_controller_navigation", "deck_text_and_push_to_talk", "voice_privacy_and_permissions", "browser_and_hermes_render_closed_loop", "verified_browser_artifact_closed_loop"]) {
  assert.ok(surface.retailGates.includes(gate), `missing Steam retail gate ${gate}`);
}
console.log("ok — Yaver Steam depot boundaries and guarded upload");
