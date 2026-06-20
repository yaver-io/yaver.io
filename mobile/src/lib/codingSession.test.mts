// codingSession.test.mts — topology policy: engine × target, incl. the
// Hermes-only-remote (auth-free box) case and opencode.
// Run: npx tsx src/lib/codingSession.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  resolveCodingSession,
  phoneCanDriveHermes,
  isCliSession,
  sessionEndpointDeviceId,
  type CodingEnv,
} from "./codingSession.ts";
import type { CodingBackendAvailability } from "./codingBackend.ts";

const NO_BACKEND: CodingBackendAvailability = {
  localModelReady: false,
  claudeSubscription: false,
  anthropicKey: false,
  openaiKey: false,
  glmKey: false,
  remoteRunner: false,
};
const PLAN: CodingBackendAvailability = { ...NO_BACKEND, claudeSubscription: true };
const GLM: CodingBackendAvailability = { ...NO_BACKEND, glmKey: true };
const LOCAL: CodingBackendAvailability = { ...NO_BACKEND, localModelReady: true };

function env(p: Partial<CodingEnv>): CodingEnv {
  return { platform: "ios", online: true, backend: NO_BACKEND, ...p };
}

// ── SANDBOX ────────────────────────────────────────────────────────────

test("sandbox on Android with on-device CLI runs the real runner on the phone", () => {
  const s = resolveCodingSession("sandbox", env({ platform: "android", onDeviceCliReady: true }));
  assert.deepEqual(s.engine, { kind: "cli-on-device", runner: "claude" });
  assert.equal(s.target.kind, "phone");
  assert.ok(isCliSession(s));
  assert.equal(sessionEndpointDeviceId(s), null); // loopback
});

test("sandbox on Android honours the rootfs runner + a forced opencode override", () => {
  const codex = resolveCodingSession(
    "sandbox",
    env({ platform: "android", onDeviceCliReady: true, onDeviceRunner: "codex" }),
  );
  assert.deepEqual(codex.engine, { kind: "cli-on-device", runner: "codex" });

  const oc = resolveCodingSession(
    "sandbox",
    env({ platform: "android", onDeviceCliReady: true }),
    { runner: "opencode" },
  );
  assert.deepEqual(oc.engine, { kind: "cli-on-device", runner: "opencode" });
});

test("sandbox on iOS falls back to the Hermes loop on a BYO backend (GLM)", () => {
  const s = resolveCodingSession("sandbox", env({ platform: "ios", backend: GLM }));
  assert.deepEqual(s.engine, { kind: "hermes", backend: "glm" });
  assert.equal(s.target.kind, "phone");
  assert.ok(!isCliSession(s));
});

test("COMPLIANCE: a plan token alone never enables the in-app loop (iOS)", () => {
  // Subscription is CLI-only; on iOS there's no CLI, so PLAN-only = needs setup.
  const s = resolveCodingSession("sandbox", env({ platform: "ios", backend: PLAN }));
  assert.notEqual((s.engine as any).backend, "subscription");
  assert.match(s.reason, /set one up/);
});

test("sandbox with no backend at all surfaces a null-ish hermes (UI prompts setup)", () => {
  const s = resolveCodingSession("sandbox", env({ platform: "ios", backend: NO_BACKEND }));
  assert.equal(s.engine.kind, "hermes");
  assert.match(s.reason, /set one up/);
});

test("Android sandbox can be forced to the IN-APP Hermes loop (no proot)", () => {
  // The user's ask: Android runs Hermes in-app too, dodging proot/backgrounding.
  const s = resolveCodingSession(
    "sandbox",
    env({ platform: "android", onDeviceCliReady: true, backend: GLM }),
    { onDeviceEngine: "hermes" },
  );
  assert.deepEqual(s.engine, { kind: "hermes", backend: "glm" });
  assert.equal(s.target.kind, "phone");
});

test("Android sandbox auto still prefers the real CLI when the rootfs is ready", () => {
  const s = resolveCodingSession(
    "sandbox",
    env({ platform: "android", onDeviceCliReady: true, backend: PLAN }),
    { onDeviceEngine: "auto" },
  );
  assert.equal(s.engine.kind, "cli-on-device");
});

test("Android sandbox with no rootfs uses in-app Hermes even when 'cli' is forced", () => {
  const s = resolveCodingSession(
    "sandbox",
    env({ platform: "android", onDeviceCliReady: false, backend: LOCAL }),
    { onDeviceEngine: "cli" },
  );
  assert.equal(s.engine.kind, "hermes"); // can't force CLI without a rootfs
});

// ── PROJECT: the Hermes-only-remote win ─────────────────────────────────

test("project on a reachable box defaults to Hermes-only-remote — box stays auth-free", () => {
  const s = resolveCodingSession(
    "project",
    env({ boxDeviceId: "box-1", boxRunnerReady: false, backend: GLM }),
  );
  assert.deepEqual(s.engine, { kind: "hermes", backend: "glm" });
  assert.deepEqual(s.target, { kind: "box", deviceId: "box-1" });
  assert.equal(s.boxAuthFree, true);
  assert.equal(sessionEndpointDeviceId(s), "box-1");
});

test("Hermes-only-remote is preferred even when the box is already authed (default auto)", () => {
  // The whole point: one token on the phone beats mirroring creds to every box.
  const s = resolveCodingSession(
    "project",
    env({ boxDeviceId: "box-1", boxRunnerReady: true, boxRunner: "claude", backend: LOCAL }),
  );
  assert.equal(s.engine.kind, "hermes");
  assert.equal(s.boxAuthFree, true);
});

test("remoteEngine:'cli' forces the box's own authed runner", () => {
  const s = resolveCodingSession(
    "project",
    env({ boxDeviceId: "box-1", boxRunnerReady: true, boxRunner: "opencode", backend: PLAN }),
    { remoteEngine: "cli" },
  );
  assert.deepEqual(s.engine, { kind: "cli-on-box", runner: "opencode", deviceId: "box-1" });
  assert.equal(s.boxAuthFree, false);
});

test("remoteEngine:'cli' with an UNauthed box + no phone backend asks for setup", () => {
  const s = resolveCodingSession(
    "project",
    env({ boxDeviceId: "box-1", boxRunnerReady: false, backend: NO_BACKEND }),
    { remoteEngine: "cli" },
  );
  assert.equal(s.target.kind, "box");
  assert.match(s.reason, /authorize a runner|set up a phone backend/);
});

test("project with an unauthed box but a phone backend still drives it auth-free", () => {
  const s = resolveCodingSession(
    "project",
    env({ boxDeviceId: "box-1", boxRunnerReady: false, backend: LOCAL }),
    { remoteEngine: "cli" }, // even forcing cli, there's no box auth → fall to hermes
  );
  assert.equal(s.engine.kind, "hermes");
  assert.equal(s.boxAuthFree, true);
});

// ── PROJECT: no box ─────────────────────────────────────────────────────

test("project with no box on Android runs the on-device CLI", () => {
  const s = resolveCodingSession(
    "project",
    env({ platform: "android", onDeviceCliReady: true, boxDeviceId: null }),
  );
  assert.equal(s.engine.kind, "cli-on-device");
  assert.equal(s.target.kind, "phone");
});

test("project with no box on iOS uses the Hermes loop + reach-for-a-machine framing", () => {
  const s = resolveCodingSession("project", env({ platform: "ios", boxDeviceId: null, backend: GLM }));
  assert.deepEqual(s.engine, { kind: "hermes", backend: "glm" });
  assert.match(s.reason, /reaches for a machine/);
});

test("offline box is treated as unreachable", () => {
  const s = resolveCodingSession(
    "project",
    env({ online: false, boxDeviceId: "box-1", boxRunnerReady: true, backend: PLAN }),
  );
  assert.equal(s.target.kind, "phone"); // box ignored when offline
});

// ── helpers ─────────────────────────────────────────────────────────────

test("phoneCanDriveHermes reflects COMPLIANT backend availability (not the plan token)", () => {
  assert.equal(phoneCanDriveHermes(env({ backend: NO_BACKEND })), false);
  assert.equal(phoneCanDriveHermes(env({ backend: PLAN })), false); // subscription is CLI-only
  assert.equal(phoneCanDriveHermes(env({ backend: GLM })), true);
  assert.equal(phoneCanDriveHermes(env({ backend: LOCAL })), true);
});
