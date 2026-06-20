// startCoding.test.mts — the unified "Start coding" router collapses 3 surfaces
// into one decision. Run: npx tsx src/lib/startCoding.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { routeCoding, describeRoute, type StartCodingRequest } from "./startCoding.ts";
import type { CodingBackendAvailability } from "./codingBackend.ts";

const NONE: CodingBackendAvailability = {
  localModelReady: false,
  claudeSubscription: false,
  anthropicKey: false,
  openaiKey: false,
  glmKey: false,
  remoteRunner: false,
};
const PLAN: CodingBackendAvailability = { ...NONE, claudeSubscription: true };
const GLM: CodingBackendAvailability = { ...NONE, glmKey: true };

function req(p: Partial<StartCodingRequest>): StartCodingRequest {
  return { env: { platform: "ios", online: true, backend: NONE }, ...p } as StartCodingRequest;
}

test("a data/backend app routes to the Hermes phone-backend", () => {
  const r = routeCoding(req({ appKind: "backend", env: { platform: "ios", backend: NONE } }));
  assert.equal(r.surface, "phone-backend");
  assert.equal(r.screen, "phone-projects");
});

test("backend app with a slug opens that project", () => {
  const r = routeCoding(req({ appKind: "backend", slug: "todos", env: { platform: "ios", backend: NONE } }));
  assert.equal(r.surface, "phone-backend");
  assert.equal(r.screen, "phone-project/todos");
});

test("code app, no box, with a backend → phone-local sandbox", () => {
  const r = routeCoding(req({ appKind: "code", env: { platform: "ios", backend: GLM } }));
  assert.equal(r.surface, "sandbox");
  assert.equal(r.screen, "sandbox-ai");
  assert.equal(r.deviceId, null);
});

test("code app, no box, NOTHING configured → needs-setup", () => {
  const r = routeCoding(req({ appKind: "code", env: { platform: "ios", backend: NONE } }));
  assert.equal(r.surface, "needs-setup");
});

test("Android with on-device CLI → sandbox even with no cloud backend", () => {
  const r = routeCoding(req({ appKind: "code", env: { platform: "android", onDeviceCliReady: true, backend: NONE } }));
  assert.equal(r.surface, "sandbox");
});

test("reachable box + compliant phone backend (GLM) → Hermes-only remote (auth-free box)", () => {
  const r = routeCoding(
    req({ appKind: "code", env: { platform: "ios", online: true, boxDeviceId: "box-1", boxRunnerReady: false, backend: GLM } }),
  );
  assert.equal(r.surface, "hermes-remote");
  assert.equal(r.deviceId, "box-1");
  assert.equal(r.screen, "sandbox-ai");
  assert.ok(r.session?.boxAuthFree);
});

test("COMPLIANCE: plan token + UNauthed box → NOT hermes-remote (subscription is CLI-only)", () => {
  // A plan token can't compliantly drive an unauthed box from the phone; without
  // another backend that's a setup prompt, not a silent subscription-mimic.
  const r = routeCoding(
    req({ appKind: "code", env: { platform: "ios", online: true, boxDeviceId: "box-1", boxRunnerReady: false, backend: PLAN } }),
  );
  assert.notEqual(r.surface, "hermes-remote");
});

test("reachable authed box + remoteEngine:'cli' → remote-task on the box runner", () => {
  const r = routeCoding(
    req({
      appKind: "code",
      env: { platform: "ios", online: true, boxDeviceId: "box-1", boxRunnerReady: true, boxRunner: "claude", backend: NONE },
      prefs: { remoteEngine: "cli" },
    }),
  );
  assert.equal(r.surface, "remote-task");
  assert.equal(r.screen, "tasks");
  assert.equal(r.deviceId, "box-1");
});

test("a slug routes code surfaces to the project's editor screen", () => {
  const r = routeCoding(req({ appKind: "code", slug: "my-app", env: { platform: "ios", backend: GLM } }));
  assert.equal(r.screen, "phone-project/code/my-app");
});

test("offline box is ignored → falls back to phone-local", () => {
  const r = routeCoding(
    req({ appKind: "code", env: { platform: "ios", online: false, boxDeviceId: "box-1", boxRunnerReady: true, backend: GLM } }),
  );
  assert.equal(r.surface, "sandbox");
});

test("describeRoute gives a one-liner per surface", () => {
  const r = routeCoding(req({ appKind: "code", env: { platform: "ios", backend: GLM } }));
  assert.match(describeRoute(r), /Edit on this phone/);
});
