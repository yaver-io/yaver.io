import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { buildYaverSignInSignal } from "./accessChannel";

test("remote sign-in signal is bounded and contains no credential payload", () => {
  const signal = buildYaverSignInSignal("yavc_controller", "box-1", 1_000);
  assert.equal(signal.version, 1);
  assert.equal(signal.kind, "requests.auth");
  assert.equal(signal.provider, "yaver");
  assert.equal(signal.targetDeviceId, "box-1");
  assert.equal(signal.issuerDeviceId, "yavc_controller");
  assert.equal(signal.issuedAt, 1_000);
  assert.equal(signal.expiresAt, 121_000);
  assert.ok(signal.requestId.length > 8);
  assert.ok(signal.nonce.length > 8);
  const encoded = JSON.stringify(signal).toLowerCase();
  for (const forbidden of ["token", "password", "source", "command", "keystroke"]) {
    assert.equal(encoded.includes(forbidden), false, `signal must not carry ${forbidden}`);
  }
});

test("the dashboard Devices pane consumes remote recovery and pending approvals", () => {
  const root = join(import.meta.dirname, "..");
  const dashboard = readFileSync(join(root, "app/dashboard/page.tsx"), "utf8");
  const panel = readFileSync(join(root, "components/dashboard/AccessRecoveryPanel.tsx"), "utf8");
  assert.match(dashboard, /<AccessRecoveryPanel token=\{token\} devices=\{displayDevices\} \/>/);
  assert.match(panel, /requestYaverSignIn\(token, hosted\.brokerId, device\.id\)/);
  assert.match(panel, /<PendingSignInsPanel key=\{approvalKey\} token=\{token\} \/>/);
});
