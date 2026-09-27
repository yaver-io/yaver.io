/**
 * relayDeny.test.ts — `npx tsx lib/relayDeny.test.ts`.
 *
 * Pins the named-verdict layer for relay denies (audit gaps R3, R13, R14):
 *  1. behavior against the relay's REAL error strings (read from
 *     relay/server.go + relay/bandwidth.go so drift there fails here);
 *  2. web/mobile parity — the two relayDeny.ts twins must carry identical
 *     logic, or the "named on one surface only" defect ships again.
 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { classifyRelayLimit, explainRelayDeny } from "./relayDeny";

const webRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(webRoot, "..");

test("device_mismatch: named, terminal, remedy carries `yaver auth`", () => {
  // The exact string relay/server.go rejects with.
  const relaySrc = readFileSync(join(repoRoot, "relay/server.go"), "utf8");
  assert.match(relaySrc, /reason=device_mismatch/, "relay deny reason renamed — update relayDeny.ts");

  const msg = explainRelayDeny("relay password owner does not own this deviceId (reason=device_mismatch)");
  assert.ok(msg, "device_mismatch must be explained");
  assert.match(msg!, /different Yaver account/);
  assert.match(msg!, /yaver auth/);
  assert.match(msg!, /can't help/i);
});

test("dead_token: named, terminal, remedy names the re-auth", () => {
  // The relay states its own fix in prose — and every client dropped it.
  // `explainRelayDeny` returned null, so web/lib/reconnectLadder.ts kept
  // retrying a session that can never self-heal: "Reconnecting (n/5)" forever,
  // the exact defect this module was written to kill, still live for
  // dead_token. Mobile papered over it with a raw substring check in
  // DeviceContext.tsx; web had nothing.
  const relaySrc = readFileSync(join(repoRoot, "relay/server.go"), "utf8");
  assert.match(relaySrc, /reason=dead_token/, "relay deny reason renamed — update relayDeny.ts");

  const msg = explainRelayDeny("relay session expired — sign in again on this device (reason=dead_token)");
  assert.ok(msg, "dead_token must be explained — retrying it loops forever");
  assert.match(msg!, /expired/i);
  assert.match(msg!, /yaver auth|sign in/i);
});

test("EVERY reason= the relay emits is either explained or a declared healable", () => {
  // THE GUARD THAT WOULD HAVE CAUGHT dead_token. Mapping reasons one at a
  // time is how the second one gets forgotten: the module named
  // device_mismatch, shipped, and left dead_token — equally terminal, equally
  // prose-documented — falling through to null for months. Enumerate from the
  // Go source instead, so a NEW reason fails here the day it lands.
  const relaySrc = readFileSync(join(repoRoot, "relay/server.go"), "utf8");
  const reasons = new Set(Array.from(relaySrc.matchAll(/reason=([a-z_]+)/g), (m) => m[1]));
  assert.ok(reasons.size >= 2, "found no reason= literals — did the relay change format?");

  // Healable by a retry/repair rung, so a terminal explanation would be WRONG.
  const healable = new Set(["bad_password"]);

  for (const reason of reasons) {
    if (healable.has(reason)) {
      assert.equal(explainRelayDeny(`something (reason=${reason})`), null, `${reason} is listed healable but is explained as terminal`);
      continue;
    }
    assert.ok(
      explainRelayDeny(`something (reason=${reason})`),
      `relay emits reason=${reason} and no surface names it — add a remedy to both relayDeny.ts twins, or declare it healable here`,
    );
  }
});

test("healable causes are NOT declared terminal", () => {
  assert.equal(explainRelayDeny("invalid relay password (reason=bad_password)"), null);
  assert.equal(explainRelayDeny("device not connected to relay"), null);
  assert.equal(explainRelayDeny(""), null);
  assert.equal(explainRelayDeny(null), null);
});

test("bandwidth cap: parses the relay's real format, names the reset + unmetered paths", () => {
  const bwSrc = readFileSync(join(repoRoot, "relay/bandwidth.go"), "utf8");
  assert.match(bwSrc, /bandwidth limit exceeded: %dMB used of %dMB daily account limit/, "relay bandwidth string changed — update relayDeny.ts");

  const card = classifyRelayLimit("bandwidth limit exceeded: 120MB used of 100MB daily limit (device abcd1234)");
  assert.ok(card);
  assert.equal(card!.kind, "bandwidth-cap");
  assert.match(card!.detail, /120 MB/);
  assert.match(card!.detail, /100 MB/);
  assert.match(card!.detail, /resets daily/i);
  assert.match(card!.detail, /unmetered/);
  assert.match(card!.detail, /cut by the cap/);
});

test("a stream cut MID-WAY by the cap gets the bandwidth card too", () => {
  // relay/counting_writer.go aborts an in-flight transfer with a message that
  // carries no digits — so it matched none of the three branches, while the
  // bandwidth card's own copy promised "a stream that stops mid-way with this
  // message was cut by the cap". The copy was unreachable from the only error
  // that produces that experience.
  const cwSrc = readFileSync(join(repoRoot, "relay/counting_writer.go"), "utf8");
  assert.match(cwSrc, /bandwidth limit exceeded mid-stream/, "relay mid-stream string changed — update relayDeny.ts");

  const card = classifyRelayLimit("bandwidth limit exceeded mid-stream");
  assert.ok(card, "a mid-stream cap cut must be named, not shown as a raw Go string");
  assert.equal(card!.kind, "bandwidth-cap");
  assert.match(card!.detail, /unmetered/);
});

test("free-tier rate limit: the relay's exact string gets its own card", () => {
  const relaySrc = readFileSync(join(repoRoot, "relay/server.go"), "utf8");
  assert.match(relaySrc, /free relay user rate limit exceeded/);

  const card = classifyRelayLimit("free relay user rate limit exceeded");
  assert.ok(card);
  assert.equal(card!.kind, "free-tier-rate");
  const generic = classifyRelayLimit("rate limit exceeded");
  assert.ok(generic);
  assert.equal(generic!.kind, "rate-limit");
});

test("free capacity reservation names the busy lane and an immediate route", () => {
  const card = classifyRelayLimit("relay.free_capacity_busy: free relay is busy");
  assert.equal(card?.kind, "free-capacity");
  assert.match(card?.title ?? "", /Free relay is busy/);
  assert.match(card?.detail ?? "", /Retry shortly/);
  assert.match(card?.detail ?? "", /self-hosted relay/);
});

test("non-limit errors yield no card", () => {
  assert.equal(classifyRelayLimit("device not connected to relay"), null);
  assert.equal(classifyRelayLimit(null), null);
});

test("web/mobile twins are byte-identical below the header comment", () => {
  const strip = (src: string) => src.split("\n").filter((l) => !l.trimStart().startsWith("//")).join("\n").trim();
  const web = strip(readFileSync(join(webRoot, "lib/relayDeny.ts"), "utf8"));
  const mobile = strip(readFileSync(join(repoRoot, "mobile/src/lib/relayDeny.ts"), "utf8"));
  assert.equal(web, mobile, "relayDeny twins drifted — sync web/lib/relayDeny.ts and mobile/src/lib/relayDeny.ts");
});
