import test from "node:test";
import assert from "node:assert/strict";

import {
  deriveServerPhase,
  PHASE_META,
  type LifecyclePhase,
} from "./wakeMachineCore.ts";

function phase(
  status: string,
  provisionPhase: string | null,
  deviceReachable: boolean,
  runnersAuthorized?: boolean,
): LifecyclePhase {
  return deriveServerPhase(
    { status, provisionPhase, runnersAuthorized },
    deviceReachable,
  );
}

test("deriveServerPhase keeps active-but-unreachable machines at registering, not ready", () => {
  assert.equal(phase("active", "ready", false), "registering");
  assert.equal(phase("active", "authorizing-runners", false, false), "registering");
  assert.equal(PHASE_META.registering.percent, 80);
});

test("deriveServerPhase only reports ready when the device is reachable and runners are authorized", () => {
  assert.equal(phase("active", "ready", true), "ready");
  assert.equal(phase("active", "authorizing-runners", true, false), "online");
});

test("deriveServerPhase maps resume and stop lifecycle signals to visible progress", () => {
  assert.equal(phase("resuming", "ready", false), "resuming");
  assert.equal(phase("provisioning", "pulling-image", false), "booting");
  assert.equal(phase("stopping", "deleting", false), "powering-down");
  assert.equal(phase("stopped", "ready", false), "asleep");
});
