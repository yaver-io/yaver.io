import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { parseHetznerCloudTokenText, parseRecognizedAPIKeyText, parseScannedAPIKey } from "./apiKeyScan.ts";

test("API key scanner accepts plain, JSON, and URL handoffs", () => {
  assert.deepEqual(parseScannedAPIKey("sk-a-very-long-secret"), { apiKey: "sk-a-very-long-secret" });
  assert.deepEqual(parseScannedAPIKey('{"provider":"deepseek","apiKey":"ds-secret-value"}'), {
    apiKey: "ds-secret-value",
    provider: "deepseek",
  });
  assert.deepEqual(parseScannedAPIKey("yaver://opencode-key?provider=deepseek&key=ds-secret-value"), {
    apiKey: "ds-secret-value",
    provider: "deepseek",
  });
});

test("API key scanner rejects whitespace, short values, and malformed JSON", () => {
  assert.equal(parseScannedAPIKey("short"), null);
  assert.equal(parseScannedAPIKey("secret with spaces"), null);
  assert.equal(parseScannedAPIKey("{bad"), null);
});

test("on-device OCR parser prefers labelled and known-prefix key strings", () => {
  assert.deepEqual(
    parseRecognizedAPIKeyText("DeepSeek settings\nAPI key: ds-abc1-DEF2-ghi3\nSave"),
    { apiKey: "ds-abc1-DEF2-ghi3" },
  );
  assert.deepEqual(
    parseRecognizedAPIKeyText("Account\nrandom-setting-1234\nsk-live_ABC1234567890\nDone"),
    { apiKey: "sk-live_ABC1234567890" },
  );
  assert.equal(parseRecognizedAPIKeyText("OpenCode provider settings Save Cancel"), null);
});

test("Hetzner scanner accepts only one Cloud project token and rejects S3 pairs", () => {
  const token = "A".repeat(32) + "b7_" + "C".repeat(29);
  assert.equal(token.length, 64);
  assert.deepEqual(parseHetznerCloudTokenText(`Hetzner Cloud\nAPI token: ${token}`), {
    apiKey: token,
    provider: "hetzner",
  });
  assert.deepEqual(parseHetznerCloudTokenText(JSON.stringify({ provider: "hetzner", apiKey: token })), {
    apiKey: token,
    provider: "hetzner",
  });
  assert.equal(parseHetznerCloudTokenText("access_key = ABCDEFGHIJKLMNOPQRST\nsecret_key = " + "z".repeat(40)), null);
  assert.deepEqual(parseHetznerCloudTokenText(`Hetzner Cloud API token\n${token.slice(0, 32)}\n${token.slice(32)}`), {
    apiKey: token,
    provider: "hetzner",
  });
  assert.equal(parseHetznerCloudTokenText("A".repeat(63)), null);
  assert.equal(parseHetznerCloudTokenText(`${"A".repeat(64)}\n${"B".repeat(64)}`), null);
});

test("scanner defaults to on-device text OCR, retains QR, and has no cloud persistence path", () => {
  const scanner = fs.readFileSync(new URL("../components/ApiKeyScanner.tsx", import.meta.url), "utf8");
  assert.match(scanner, /useState<ScanMode>\("text"\)/);
  assert.match(scanner, /@react-native-ml-kit\/text-recognition/);
  assert.match(scanner, /mode === "qr"/);
  assert.match(scanner, /launchImageLibraryAsync/);
  assert.match(scanner, /base64: false/);
  assert.doesNotMatch(scanner, /CONVEX_URL|AsyncStorage|SecureStore|fetch\(/);

  const settings = fs.readFileSync(new URL("../components/OpenCodeConfigModal.tsx", import.meta.url), "utf8");
  assert.match(settings, /saveOpenCodeConfig\(patch, target\)/);
  assert.doesNotMatch(settings, /setPrimaryRunnerForDevice\([^)]*apiKey/s);
});

test("Hetzner credential scan fills only transient native settings state", () => {
  const settings = fs.readFileSync(new URL("../components/CloudProvidersSection.tsx", import.meta.url), "utf8");
  assert.match(settings, /Scan camera\/photo/);
  assert.match(settings, /Paste and connect/);
  assert.match(settings, /Platform\.OS !== "web"/);
  assert.match(settings, /setSecret\(apiKey\)/);
  assert.match(settings, /hetznerClientCloud\.connect\(value\)/);
  assert.doesNotMatch(settings, /setPrimaryRunnerForDevice\([^)]*secret/s);
});
