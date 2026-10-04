import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const runtimeCryptoModules = [
  "encryptedPair.ts",
  "hetznerRecovery.ts",
  "credentialHandoff.ts",
  "credentialHandoffStore.ts",
  "dogfoodRegistry.ts",
  "identityProof.ts",
];

test("every mobile TweetNaCl caller uses the explicitly configured secure PRNG", async () => {
  const setup = await readFile(new URL("./secureNacl.ts", import.meta.url), "utf8");
  assert.match(setup, /expoCrypto\.getRandomValues\(random\)/);
  assert.match(setup, /nacl\.setPRNG/);
  assert.match(setup, /Secure encryption is unavailable in this Yaver build/);
  assert.doesNotMatch(setup.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, ""), /Math\.random/);

  for (const filename of runtimeCryptoModules) {
    const source = await readFile(new URL(`./${filename}`, import.meta.url), "utf8");
    assert.match(source, /from "\.\/secureNacl"/, `${filename} bypasses the configured mobile PRNG`);
    assert.doesNotMatch(source, /from "tweetnacl"/, `${filename} can freeze TweetNaCl in no-PRNG state`);
  }
});
