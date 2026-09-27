import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { evaluateReadiness, loadContract } from "../readiness.mjs";

const root = path.resolve(import.meta.dirname, "../..");
const contract = loadContract();
const readEvidence = (key) => fs.readFileSync(path.join(root, contract.sourceEvidence[key]), "utf8");

assert.equal(contract.referenceSurface, "tvos");
assert.equal(contract.claimsPlayStationSupport, false);
assert.deepEqual(contract.experience.render.sources, ["browser_remote", "hermes_native_remote", "verified_browser_artifact"]);
assert.equal(contract.experience.render.executesHermesBundleOnConsole, false);
assert.equal(contract.experience.render.embedsThirdPartyWebAppOnConsole, false);
assert.equal(contract.experience.voice.hotMic, false);
assert.equal(contract.experience.voice.transcriptVisibleBeforeSend, true);

assert.match(readEvidence("voiceOutput"), /static func speak/);
assert.match(readEvidence("voiceOutput"), /static func stop/);
assert.match(readEvidence("voiceInput"), /YaverDictationField/);
assert.match(readEvidence("voiceInput"), /createTask/);
assert.match(readEvidence("chatContinuity"), /continueCurrent/);
assert.match(readEvidence("renderViewer"), /WebRTC/);
assert.match(readEvidence("renderViewer"), /authenticated HTTP/i);
assert.match(readEvidence("browserFrames"), /web app can't run in-process/);
assert.match(readEvidence("verifiedTask"), /PlayStation/);
assert.match(readEvidence("verifiedTask"), /authenticated.*MP4/i);

const readiness = evaluateReadiness(contract);
assert.equal(readiness.publishable, false);
for (const gate of ["partnerApproval", "officialSdkAccess", "titleIdsAssigned", "officialHardwareTested", "voicePrivacyCertified"]) {
  assert.ok(readiness.blockers.includes(gate), `missing blocker ${gate}`);
}

const approved = structuredClone(contract);
approved.claimsPlayStationSupport = true;
for (const gate of Object.keys(approved.releaseGates)) approved.releaseGates[gate] = true;
assert.deepEqual(evaluateReadiness(approved), { publishable: true, blockers: [] });

console.log("ok — Yaver PlayStation tvOS parity, voice, render, and publication gates");
