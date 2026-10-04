import assert from "node:assert/strict";
import test from "node:test";

import { decryptHetznerRecovery, encryptHetznerRecovery } from "./hetznerRecovery.ts";

test("encrypted recovery round-trips without plaintext in the backup", () => {
  const token = "HCLOUD_TEST_SECRET_91827";
  const recovery = encryptHetznerRecovery(token);
  assert.doesNotMatch(recovery.encryptedBackup, new RegExp(token));
  assert.equal(decryptHetznerRecovery(recovery.encryptedBackup, recovery.recoveryKey), token);
});

test("wrong recovery key and tampered ciphertext fail closed", () => {
  const recovery = encryptHetznerRecovery("HCLOUD_TEST_SECRET_12345");
  const wrong = encryptHetznerRecovery("another-token");
  assert.throws(() => decryptHetznerRecovery(recovery.encryptedBackup, wrong.recoveryKey));
  assert.throws(() => decryptHetznerRecovery(`${recovery.encryptedBackup}x`, recovery.recoveryKey));
});
