import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { normalizePrivateVpsUrl, privateVpsUrlFromQr } from "./privateVps.ts";

test("normalizes a private VPS origin and optional path", () => {
  assert.equal(normalizePrivateVpsUrl(" https://vps.example.com/yaver/// "), "https://vps.example.com/yaver");
  assert.equal(normalizePrivateVpsUrl("http://10.0.0.8:3210"), null);
});

test("rejects credential-bearing and ambiguous URLs", () => {
  assert.equal(normalizePrivateVpsUrl("https://user:secret@vps.example.com"), null);
  assert.equal(normalizePrivateVpsUrl("https://vps.example.com?token=secret"), null);
  assert.equal(normalizePrivateVpsUrl("http://vps.example.com"), null);
  assert.equal(normalizePrivateVpsUrl("ftp://vps.example.com"), null);
  assert.equal(normalizePrivateVpsUrl("vps.example.com"), null);
});

test("reads the cross-surface QR formats", () => {
  assert.equal(privateVpsUrlFromQr("https://vps.example.com"), "https://vps.example.com");
  assert.equal(
    privateVpsUrlFromQr("yaver://private-vps?url=https%3A%2F%2Fvps.example.com%2Fyaver"),
    "https://vps.example.com/yaver",
  );
  assert.equal(privateVpsUrlFromQr('{"privateVpsUrl":"https://vps.example.com"}'), "https://vps.example.com");
  assert.equal(privateVpsUrlFromQr('{"url":"https://user:secret@vps.example.com"}'), null);
});

test("login chooses the persisted server before OAuth controls", () => {
  const source = readFileSync(new URL("../../app/login.tsx", import.meta.url), "utf8");
  const chooser = source.indexOf('accessibilityLabel="Choose Yaver server before sign in"');
  const google = source.indexOf("Continue with Google");
  assert.ok(chooser >= 0 && google > chooser);
  assert.match(source, /await setPrivateVpsUrl\(normalized\)/);
  assert.doesNotMatch(source, /privateVps(?:Password|Username)/i);
});
