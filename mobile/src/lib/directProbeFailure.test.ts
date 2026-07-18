// Tests for describeDirectProbeFailure — the classifier that decides whether a
// failed direct-connect leg is worth retrying.
//
// Run: npx tsx src/lib/directProbeFailure.test.ts
//
// Why this is tested at all: the "blocked" verdict is the one that changes
// behaviour. An OS-blocked leg (iOS ATS -1022 / Android cleartext policy) can
// NEVER succeed, so retrying it is pure waste — that is precisely the bug where
// a tailnet/mesh candidate was retried forever against a box that was online.
// A timeout, by contrast, MUST be retried. Getting these two backwards is
// invisible in the UI, so it gets a test.
//
// This imports the extracted pure module, NOT quic.ts — quic.ts pulls React
// Native, which cannot be transformed by plain tsx.


import { describeDirectProbeFailure } from "./directProbeFailure";

let failures = 0;
function check(label: string, actual: string, expectSubstring: string) {
  const ok = actual.includes(expectSubstring);
  if (!ok) {
    failures++;
    console.error(`FAIL ${label}\n  expected to contain: ${expectSubstring}\n  actual:              ${actual}`);
  } else {
    console.log(`ok   ${label}`);
  }
}

// --- OS-blocked: must be identifiable as permanently-impossible ------------
// iOS surfaces ATS rejection with the -1022 code in the message.
check(
  "iOS ATS -1022 is classified blocked",
  describeDirectProbeFailure(new Error("The resource could not be loaded because the App Transport Security policy requires the use of a secure connection. (-1022)")),
  "blocked by the OS",
);
check(
  "bare -1022 code is classified blocked",
  describeDirectProbeFailure({ message: "request failed", code: "-1022" }),
  "blocked by the OS",
);
check(
  "Android cleartext policy is classified blocked",
  describeDirectProbeFailure(new Error("CLEARTEXT communication to 100.96.0.1 not permitted by network security policy")),
  "blocked by the OS",
);

// --- Transient: must NOT be classified blocked (these deserve a retry) -----
check(
  "AbortError is a timeout",
  describeDirectProbeFailure(Object.assign(new Error("Aborted"), { name: "AbortError" })),
  "timed out",
);
check(
  "refused is distinct from timeout",
  describeDirectProbeFailure(new Error("connect ECONNREFUSED")),
  "connection refused",
);
check(
  "unreachable is distinct from timeout",
  describeDirectProbeFailure(new Error("EHOSTUNREACH: no route to host")),
  "network unreachable",
);

// --- The classifier must not over-claim ------------------------------------
// An unknown error is NOT blocked; treating it as blocked would make the
// ladder give up on a leg that might work.
const unknown = describeDirectProbeFailure(new Error("socket hang up"));
check("unknown error is passed through verbatim", unknown, "socket hang up");
if (unknown.includes("blocked by the OS")) {
  failures++;
  console.error("FAIL unknown error must not be classified blocked");
}

if (failures > 0) {
  console.error(`\n${failures} failure(s)`);
  process.exit(1);
}
console.log("\nall passed");
