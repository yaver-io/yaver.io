import assert from "node:assert/strict";
import test from "node:test";

import { asCiphertext, assertRelayEnvelope } from "./relay-envelope.ts";

const valid = () => ({
  version: 1 as const,
  sessionId: "session_0123456789abcdef",
  destinationDeviceId: "device_destination_1234",
  senderDeviceId: "device_sender_12345678",
  packetId: "packet_0123456789abcdef",
  sequence: "42",
  nonce: new Uint8Array(24),
  ciphertext: asCiphertext(new Uint8Array(32)),
});

test("accepts only opaque routing metadata and ciphertext", () => {
  assert.doesNotThrow(() => assertRelayEnvelope(valid()));
});

for (const forbidden of ["type", "tool", "command", "filename", "model", "prompt", "payload"]) {
  test(`rejects semantic outer field ${forbidden}`, () => {
    assert.throws(() => assertRelayEnvelope({ ...valid(), [forbidden]: "SECRET_MARKER_YAVER_91827" }));
  });
}

test("rejects malformed nonce and sequence", () => {
  assert.throws(() => assertRelayEnvelope({ ...valid(), nonce: new Uint8Array(12) }));
  assert.throws(() => assertRelayEnvelope({ ...valid(), sequence: "1e3" }));
});
