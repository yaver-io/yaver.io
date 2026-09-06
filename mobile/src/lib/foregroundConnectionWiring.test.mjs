import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const manager = readFileSync(new URL("./connectionManager.ts", import.meta.url), "utf8");
const context = readFileSync(new URL("../context/DeviceContext.tsx", import.meta.url), "utf8");
const quic = readFileSync(new URL("./quic.ts", import.meta.url), "utf8");

test("AppState is fanned to the entire connection pool", () => {
  assert.match(manager, /setForegroundStateOnAll\(isForeground: boolean\)/);
  assert.match(manager, /this\.applyToAll\(\(client\) => client\.setForegroundState\(isForeground\)\)/);
  assert.match(context, /connectionManager\.setForegroundStateOnAll\(AppState\.currentState === "active"\)/);
  assert.match(context, /connectionManager\.setForegroundStateOnAll\(nextState === "active"\)/);
  assert.doesNotMatch(context, /quicClient\.setForegroundState\(/);
});

test("clients created while backgrounded inherit the pool lifecycle", () => {
  assert.match(manager, /fresh\.setForegroundState\(this\.isForeground\)/);
});

test("a connected client proves health on foreground instead of trusting memory", () => {
  assert.match(quic, /recoverStaleConnectionOnForeground\(\{/);
  assert.match(quic, /isCurrent: \(\) => this\._isForeground && this\._foregroundEpoch === epoch/);
});

test("disconnect invalidates a timed-out pool attempt", () => {
  assert.match(manager, /this\.inflightConnects\.invalidate\(id\)/);
  assert.match(manager, /this\.inflightConnects\.release\(id, promise\)/);
  assert.doesNotMatch(manager, /this\.inflightConnects\.delete\(id\)/);
});

test("a retired client's late connect result cannot resurrect background timers", () => {
  assert.match(quic, /disconnect\(\): void \{\s*this\._clientEpoch\+\+/);
  assert.match(quic, /const attemptEpoch = this\._clientEpoch;\s*await this\._doAttemptConnect\(attemptEpoch\)/);
  const retirementGuards = quic.match(/if \(attemptEpoch !== this\._clientEpoch\) return;/g) ?? [];
  assert.equal(retirementGuards.length, 2, "success and failure paths both need retirement guards");
});
