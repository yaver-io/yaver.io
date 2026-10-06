import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { normalizePrivateVpsUrl, privateVpsUrlFromQr } from "./privateVps";

test("private VPS QR contract matches native clients", () => {
  assert.equal(normalizePrivateVpsUrl(" https://vps.example.com/yaver/// "), "https://vps.example.com/yaver");
  assert.equal(privateVpsUrlFromQr("yaver://private-vps?url=https%3A%2F%2Fvps.example.com"), "https://vps.example.com");
  assert.equal(privateVpsUrlFromQr('{"privateVpsUrl":"https://vps.example.com"}'), "https://vps.example.com");
});
test("private VPS URL never accepts credentials or token-shaped query data", () => {
  assert.equal(normalizePrivateVpsUrl("https://user:secret@vps.example.com"), null);
  assert.equal(normalizePrivateVpsUrl("https://vps.example.com?token=secret"), null);
  assert.equal(normalizePrivateVpsUrl("http://vps.example.com"), null);
  assert.equal(normalizePrivateVpsUrl("file:///tmp/server"), null);
});

test("Cloudflare auth UI selects the server before OAuth without proxying credentials", () => {
  const source = readFileSync(new URL("../app/auth/page.tsx", import.meta.url), "utf8");
  assert.ok(source.indexOf("<PrivateVpsBeforeAuth />") < source.indexOf("Continue with Google"));
  assert.match(source, /const privateBase = storedPrivateVpsUrl\(\)/);
  assert.match(source, /\$\{privateBase \|\| ""\}\/api\/auth\/oauth/);
  assert.doesNotMatch(source, /privateVps(?:Password|Username)/i);
});
