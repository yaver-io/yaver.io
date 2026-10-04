import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./byoMachines.ts", import.meta.url), "utf8");

test("existing VPS adoption is metadata-only, identity preserving, and credential blind", () => {
  assert.match(source, /adoptExistingClientManaged/);
  assert.match(source, /SERVER_ID_MISMATCH/);
  assert.match(source, /PRIMARY_IP_MISMATCH/);
  assert.match(source, /origin: "self-hosted"/);
  assert.match(source, /tier: "byok"/);
  assert.doesNotMatch(source, /HCLOUD_TOKEN|Authorization|Bearer/);
});
