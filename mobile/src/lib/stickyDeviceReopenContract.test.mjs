import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = readFileSync(new URL("../context/DeviceContext.tsx", import.meta.url), "utf8");

test("explicit device picks persist across a cold reopen", () => {
  assert.match(source, /function lastSelectedDeviceKey\(userId\?: string\): string/);
  assert.match(source, /AsyncStorage\.getItem\(lastSelectedDeviceKey\(user\.id\)\)/);
  assert.match(source, /userSelectedDeviceIdRef\.current = next \|\| null/);
  assert.match(source, /AsyncStorage\.setItem\(lastSelectedDeviceKey\(user\.id\), device\.id\)/);
  assert.match(source, /!settingsReady \|\| !codingModeReady \|\| !stickyDeviceReady \|\| !allowsRemoteAutoConnect\(codingMode\)/);
  const stickyPriority = source.indexOf('pushPriority(userSelectedDeviceIdRef.current, "recent")');
  const cachedPriority = source.indexOf('pushPriority(mostRecentSuccessfulDeviceId(cachedConnections), "recent")');
  assert.ok(stickyPriority >= 0, "the restored explicit pick must enter the auto-connect ladder");
  assert.ok(cachedPriority > stickyPriority, "the explicit pick must win over a merely recent connection");
  assert.match(source, /await selectDeviceRef\.current\(device, true\);\s*if \(connectionManager\.clientFor\(device\.id\)\.isConnected\) return;/,
    "a probe success is not a connect success; failed primary connects must continue to secondary");
});

test("a manual device pick cancels any already-running automatic selection", () => {
  const selectStart = source.indexOf("const selectDevice = useCallback(");
  const stickyWrite = source.indexOf("userSelectedDeviceIdRef.current = device.id", selectStart);
  const manualCancel = source.indexOf("if (!automatic) {", selectStart);
  assert.ok(selectStart >= 0 && manualCancel > selectStart && manualCancel < stickyWrite,
    "manual cancellation must happen synchronously before the selected device is persisted");
  const block = source.slice(manualCancel, stickyWrite);
  assert.match(block, /autoConnectCancelRef\.current = true/);
  assert.match(block, /autoConnectInFlightRef\.current = false/);
  assert.match(block, /autoConnectAttemptedNonceRef\.current = autoConnectNonce/,
    "the cancelled nonce must not immediately re-arm beside the manual connection");
  assert.match(block, /setAutoConnectTarget\(null\)/);
});
