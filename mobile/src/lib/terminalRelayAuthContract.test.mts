import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const root = path.resolve(import.meta.dirname, "../..");
const quic = fs.readFileSync(path.join(root, "src/lib/quic.ts"), "utf8");

test("the shared native WebSocket builder carries relay query auth", () => {
  const method = quic.match(/agentWebSocketUrl\([\s\S]*?\n  }\n\n  \/\*\* Authed snapshot URL/);
  assert.ok(method, "agentWebSocketUrl must remain the shared WebSocket auth boundary");
  assert.match(method[0], /resolvedRelayPasswordForUrl\(url\)/);
  assert.match(method[0], /__rp=/);
  assert.match(method[0], /query\.set\("token"/);
  assert.match(method[0], /bearerToken \|\| this\.token/);
});

for (const file of [
  "app/shell.tsx",
  "app/(tabs)/terminal.tsx",
  "app/glass-terminal.tsx",
  "app/glass-workspace.tsx",
]) {
  test(`${file} uses the shared authenticated WebSocket builder`, () => {
    const source = fs.readFileSync(path.join(root, file), "utf8");
    assert.match(source, /quicClient\.agentWebSocketUrl\("\/ws\/(?:terminal|runner)"/);
    assert.doesNotMatch(source, /return `\$\{(?:ws|base|wsBase)\}\/ws\/terminal\?/);
  });
}

test("SSH separates REST authorization rejection from a later WS transport rejection", () => {
  const source = fs.readFileSync(path.join(root, "app/shell.tsx"), "utf8");
  assert.match(source, /issueWebSocketSession\(wsPath\)/);
  assert.match(source, /buildPTYWsUrl\(target, token \|\| "", activeDevice\.sshProfile, preflight\.token, requestedLaunch\)/);
  assert.match(source, /headers: quicClient\.agentWebSocketHeaders\(token \|\| ""\)/);
  assert.match(source, /recoverDeviceAuth\(activeDevice, \{ openBrowser: true \}\)/);
  assert.match(source, /Start Yaver authorization/);
  assert.match(source, /<QRCode value=\{authFlow\.deviceCodeUrl\}/);
  assert.match(source, /styles\.authWidget/);
  assert.match(source, /preflight\.status === 401 \|\| preflight\.status === 403/);
  assert.match(source, /!agentAuthorizationAccepted/);
  assert.match(source, /PTY connection/);
  assert.match(source, /WebBrowser\.openBrowserAsync/);
});
