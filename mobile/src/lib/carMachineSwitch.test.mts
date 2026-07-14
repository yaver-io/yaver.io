// carMachineSwitch.test.mts — voice machine picking, the only way to retarget a
// turn on CarPlay (no picker is allowed on the car screen).
// Run: npx tsx --test src/lib/carMachineSwitch.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  classifyMachineSwitch,
  matchMachine,
  spokenForMachineSwitch,
  type MachineLike,
} from "./carMachineSwitch.ts";

const MACHINES: MachineLike[] = [
  { id: "d1", name: "pokayoke", aliases: ["mac-mini"] },
  { id: "d2", name: "yaver-primary-kivanc", aliases: ["primary"] },
  { id: "d3", name: "ubuntu-4gb-hel1-1", aliases: ["test"] },
];

test("detects English switch phrasings", () => {
  for (const s of [
    "switch to pokayoke",
    "use the mac-mini",
    "run it on primary",
    "connect to pokayoke",
    "move this to primary",
  ]) {
    assert.ok(classifyMachineSwitch(s), `expected a switch: ${s}`);
  }
});

test("detects Turkish switch phrasings", () => {
  assert.ok(classifyMachineSwitch("pokayoke'ye geç"));
  assert.ok(classifyMachineSwitch("primary'yi kullan"));
});

test("does NOT hijack ordinary coding commands", () => {
  // These must fall through to the coding agent, not be read as a machine switch.
  for (const s of [
    "fix the failing test",
    "what's the build status",
    "how much disk is left",
    "deploy the web app",
  ]) {
    assert.equal(classifyMachineSwitch(s), null, `should not be a switch: ${s}`);
  }
});

test("matches despite STT garbling the name", () => {
  // This is the whole point: STT mangles hostnames, and a driver cannot be made
  // to enunciate. All of these must land on pokayoke.
  for (const heard of ["pokayoke", "poka yoke", "pokayoka", "poke a yoke"]) {
    const m = matchMachine(heard, MACHINES);
    assert.equal(m?.id, "d1", `"${heard}" should match pokayoke, got ${m?.name}`);
  }
});

test("matches on alias as well as name", () => {
  assert.equal(matchMachine("primary", MACHINES)?.id, "d2");
  assert.equal(matchMachine("test", MACHINES)?.id, "d3");
});

test("refuses a wrong guess rather than running work on the wrong box", () => {
  // Nothing here is close to any machine. Returning a match would mean silently
  // dispatching a build to a box the driver didn't name.
  assert.equal(matchMachine("zeppelin", MACHINES), null);
  assert.equal(matchMachine("", MACHINES), null);
});

test("always speaks the machine back, so a misheard pick is caught by ear", () => {
  const m = matchMachine("poka yoke", MACHINES);
  assert.match(spokenForMachineSwitch(m, "poka yoke"), /Switched to pokayoke/);
  assert.match(
    spokenForMachineSwitch(null, "zeppelin"),
    /couldn't find a machine called zeppelin/i,
  );
});

test("end to end: transcript → machine", () => {
  const req = classifyMachineSwitch("switch to poka yoke please");
  assert.ok(req);
  const m = matchMachine(req!.spokenName, MACHINES);
  assert.equal(m?.id, "d1");
});
