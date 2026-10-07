// xtermBridge.test.mts — pure protocol/encoding round-trips for the in-app
// xterm terminal bridge. Run: npx tsx src/lib/xtermBridge.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  bytesToBase64,
  base64ToBytes,
  resizeFrame,
  isTerminalMetaFrame,
  parseBridgeMessage,
  writeCommand,
} from "./xtermBridge.ts";

test("base64 round-trips arbitrary bytes incl. control chars + high bytes", () => {
  const samples: number[][] = [
    [],
    [0],
    [0x1b, 0x5b, 0x32, 0x4a], // ESC [ 2 J  (clear screen)
    [0x00, 0xff, 0x7f, 0x80, 0x0a, 0x0d],
    [...Array(256).keys()],
  ];
  for (const s of samples) {
    const u = new Uint8Array(s);
    const round = base64ToBytes(bytesToBase64(u));
    assert.deepEqual([...round], s);
  }
});

test("base64 matches the canonical encoding", () => {
  // "tmux" → dG11eA==
  assert.equal(bytesToBase64(new TextEncoder().encode("tmux")), "dG11eA==");
  assert.equal(new TextDecoder().decode(base64ToBytes("dG11eA==")), "tmux");
});

test("resizeFrame matches console_terminal.go's {\"resize\":{cols,rows}}", () => {
  assert.equal(resizeFrame(120, 40), '{"resize":{"cols":120,"rows":40}}');
  // clamps to >= 1, integer-coerced
  assert.equal(resizeFrame(0, -3), '{"resize":{"cols":1,"rows":1}}');
  assert.equal(resizeFrame(80.9, 24.9), '{"resize":{"cols":80,"rows":24}}');
});

test("isTerminalMetaFrame: control JSON is meta, plain output is not", () => {
  assert.equal(isTerminalMetaFrame('{"type":"terminal_session","sessionId":"abc"}'), true);
  assert.equal(isTerminalMetaFrame('{"type":"sudo_prompt","prompt":"[sudo]"}'), true);
  assert.equal(isTerminalMetaFrame('{"sessionId":"x"}'), true);
  assert.equal(isTerminalMetaFrame("$ ls -la"), false);
  assert.equal(isTerminalMetaFrame("{not json"), false);
});

// THE 2026-07-27 KEEPALIVE LEAK. The agent answers a client keepalive on the
// SAME socket that carries PTY bytes, and it shipped as an anonymous
// `{"pong":1}` — no `type`, no `sessionId`. This helper used to require one of
// those fields, so the reply classified as OUTPUT. On the web twin, which had
// no filter at all, that is literally what a user photographed: an idle prompt
// line reading `{"pong":1}{"pong":1}`, Yaver's own heartbeat rendered as if
// they had typed it.
//
// The old assertion here was `isTerminalMetaFrame('{"cols":80}') === false`
// with the comment "json but not a control frame" — i.e. this file encoded the
// bug as intended behaviour. A whitelist of known fields cannot classify a
// frame a NEWER agent invents; structured-but-unknown is control by
// definition, and painting it is the failure mode.
test("isTerminalMetaFrame: any JSON object is control, known fields or not", () => {
  assert.equal(isTerminalMetaFrame('{"pong":1}'), true); // legacy keepalive reply
  assert.equal(isTerminalMetaFrame('{"type":"pong","pong":1}'), true); // current shape
  assert.equal(isTerminalMetaFrame('{"cols":80}'), true); // unknown-but-structured
  assert.equal(isTerminalMetaFrame('  {"future_frame":true}  '), true);
  assert.equal(isTerminalMetaFrame("{}"), true);
  // Still NOT control: anything that isn't a JSON object is PTY output, so a
  // shell printing an array or a brace can never be swallowed.
  assert.equal(isTerminalMetaFrame('["type","ping"]'), false);
  assert.equal(isTerminalMetaFrame('echo \'{"pong":1}\''), false);
  assert.equal(isTerminalMetaFrame("{"), false);
});

test("parseBridgeMessage: ready / data / resize / junk", () => {
  assert.deepEqual(parseBridgeMessage('{"t":"ready"}'), { type: "ready" });

  const dataMsg = parseBridgeMessage('{"t":"d","b":"dG11eA=="}');
  assert.equal(dataMsg?.type, "data");
  if (dataMsg?.type === "data") {
    assert.equal(new TextDecoder().decode(dataMsg.bytes), "tmux");
  }

  assert.deepEqual(parseBridgeMessage('{"t":"r","c":100,"r":30}'), {
    type: "resize",
    cols: 100,
    rows: 30,
  });

  assert.equal(parseBridgeMessage("not json"), null);
  assert.equal(parseBridgeMessage('{"t":"d"}'), null); // missing b
  assert.equal(parseBridgeMessage('{"t":"nope"}'), null);
});

test("writeCommand emits a safe injectable __yvWrite call", () => {
  const cmd = writeCommand(new TextEncoder().encode("hi"));
  assert.equal(cmd, 'window.__yvWrite("aGk=");true;');
  // base64 is JSON-quoted so control bytes can't break the injected JS
  const cmd2 = writeCommand(new Uint8Array([0x1b, 0x22, 0x0a]));
  assert.ok(cmd2.startsWith("window.__yvWrite(") && cmd2.endsWith(");true;"));
  assert.ok(!cmd2.includes("\n")); // no raw newline in the injected string
});

 test("screen snapshots are bounded and malformed bridge values are ignored",()=>{
 assert.equal(parseBridgeMessage("null"),null);
 assert.equal(parseBridgeMessage("[]"),null);
 assert.deepEqual(parseBridgeMessage(JSON.stringify({t:"screen",text:"x".repeat(25000)})),{type:"screen",text:"x".repeat(24000)});
 assert.equal(parseBridgeMessage('{"t":"screen","text":5}'),null);
 });
