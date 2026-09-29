// AUTO-SYNCED from shared/client-core/src/trace.test.ts.
// DO NOT EDIT IN PLACE. Edit the source and re-run
// scripts/sync-client-core.sh. CI checks drift via `--check`.

/**
 * trace.test.ts — guards for the shared trace assembler.
 *
 * Run: npx tsx shared/client-core/src/trace.test.ts
 */
import { assembleTrace } from "./trace";

let failures = 0;
const eq = (got: unknown, want: unknown, label: string) => {
  if (JSON.stringify(got) === JSON.stringify(want)) console.log(`ok   ${label}`);
  else { console.error(`FAIL ${label}:\n  got  ${JSON.stringify(got)}\n  want ${JSON.stringify(want)}`); failures++; }
};
const ok = (c: unknown, label: string) => eq(Boolean(c), true, label);

{
  const t = assembleTrace({
    surface: "web",
    surfaceVersion: "1.1.164",
    agentVersion: "1.99.409",
    device: "ubuntu-4gb-hel1-1 (2ed7da41…)",
    relay: "public-free",
    task: { id: "abc123", status: "failed", runner: "opencode", model: "deepseek/deepseek-flash", title: "build" },
    error: "flutter exited before becoming ready",
    raw: "the raw failure bytes",
    logTail: "line1\nline2",
    ts: 1700000000000,
  });
  ok(t.includes("surface: web"), "surface line present");
  ok(t.includes("agent.version: 1.99.409"), "agent version present");
  ok(t.includes("task: abc123 status=failed runner=opencode"), "task line present");
  ok(t.includes("error: flutter exited before becoming ready"), "error present");
  ok(t.includes("log-tail:\nline1\nline2"), "log tail present under its label");
  ok(t.includes("ts: 1700000000000"), "timestamp present");
}
{
  // No invented fields when absent.
  const t = assembleTrace({ surface: "mobile", task: { id: "x" } });
  ok(!t.includes("agent.version"), "no agent.version when absent");
  ok(!t.includes("error:"), "no error when absent");
  ok(t.includes("surface: mobile"), "surface still present");
}
{
  // Secrets are redacted.
  const t = assembleTrace({ surface: "web", error: "failed with token=abc123xyz" });
  ok(t.includes("[redacted]") && !t.includes("abc123xyz"), "token redacted from error");
  const t2 = assembleTrace({ surface: "mobile", raw: "Authorization: Bearer deadbeef" });
  ok(t2.includes("[redacted]") && !t2.includes("deadbeef"), "bearer redacted from raw");
}

if (failures > 0) {
  console.error(`\n${failures} FAILURE(s)`);
  process.exit(1);
}
console.log("\nall trace tests pass");
