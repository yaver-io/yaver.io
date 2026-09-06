import assert from "node:assert/strict";
import test from "node:test";

import { buildRunnerConnectionDiagnostics, isConnectivityCodingTask } from "./logger.ts";

test("runner connection diagnostics are bounded and redact credentials and account email", () => {
  const rows = buildRunnerConnectionDiagnostics([
    {
      timestamp: Date.parse("2026-09-06T18:48:06Z"),
      level: "warn",
      message: "relay failed\nAuthorization: Bearer abcdefghijklmnopqrstuvwxyz for kivanc@example.com",
    },
    {
      timestamp: Date.parse("2026-09-06T18:48:07Z"),
      level: "info",
      message: "retry https://relay.test/connect?access_token=very-secret-token&device=ubuntu at 46.224.110.38",
    },
  ]);
  assert.equal(rows.length, 2);
  assert.match(rows[0], /relay failed Authorization: \[REDACTED\] for \[REDACTED\]/);
  assert.match(rows[1], /access_token=\[REDACTED\]/);
  assert.match(rows[1], /46\.224\.110\.38/);
  assert.doesNotMatch(rows.join("\n"), /very-secret|kivanc@example|abcdefghijklmnop/);
});

test("runner connection diagnostics retain only the newest forty rows", () => {
  const input = Array.from({ length: 45 }, (_, index) => ({
    timestamp: index,
    level: "info" as const,
    message: `row-${index}`,
  }));
  const rows = buildRunnerConnectionDiagnostics(input);
  assert.equal(rows.length, 40);
  assert.doesNotMatch(rows[0], /row-0\b/);
  assert.match(rows[0], /row-5\b/);
  assert.match(rows.at(-1) || "", /row-44\b/);
});

test("the total-size cap keeps the newest evidence", () => {
  const input = Array.from({ length: 40 }, (_, index) => ({
    timestamp: index,
    level: "info" as const,
    message: `row-${index} ${"x".repeat(590)}`,
  }));
  const rows = buildRunnerConnectionDiagnostics(input);
  assert.ok(rows.length < 40);
  assert.match(rows.at(-1) || "", /row-39\b/);
});

test("connectivity task detection covers English and Turkish without matching ordinary UI work", () => {
  assert.equal(isConnectivityCodingTask("Fix connectivity", "The relay drops"), true);
  assert.equal(isConnectivityCodingTask("Bağlantı sorununu düzelt", "Uygulama çevrimdışı kalıyor"), true);
  assert.equal(isConnectivityCodingTask("Polish the header", "Adjust spacing"), false);
});
