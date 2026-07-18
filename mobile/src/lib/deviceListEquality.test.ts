// Tests for sameDeviceList — the guard that stops a 30s refresh from
// re-triggering a fleet-wide probe storm.
//
// Run: npx tsx src/lib/deviceListEquality.test.ts
//
// Two opposite failures are both severe here, which is why both directions are
// tested:
//
//   too STRICT -> the arrays never compare equal, `devices` gets a new identity
//                 every 30s, and the probe storm returns. This is the likely
//                 regression, because adding a field to the comparison looks
//                 like an improvement in review.
//   too LOOSE  -> the UI silently stops reacting to a real change (a box goes
//                 offline, a runner needs sign-in) and shows stale state.

import { sameDeviceList, type ComparableDevice } from "./deviceListEquality";

let failures = 0;
function check(label: string, actual: boolean, expected: boolean) {
  if (actual === expected) {
    console.log(`ok   ${label}`);
  } else {
    failures++;
    console.error(`FAIL ${label}\n  expected sameDeviceList = ${expected}, got ${actual}`);
  }
}

const base = (): ComparableDevice[] => [
  {
    id: "dev-1",
    name: "Mac-mini",
    host: "192.168.1.10",
    port: 18080,
    online: true,
    os: "macos",
    agentVersion: "1.99.312",
    lanIps: ["192.168.1.10", "100.89.0.1"],
    runners: [{ id: "claude", installed: true, ready: false, authConfigured: false }],
  },
  { id: "dev-2", name: "linux-box", host: "10.0.0.5", port: 18080, online: false, os: "linux" },
];

// --- the whole point ------------------------------------------------------
check("identical payloads are equal", sameDeviceList(base(), base()), true);
check("same array reference is equal", (() => { const a = base(); return sameDeviceList(a, a); })(), true);

// THE regression guard. lastSeen advances on every heartbeat, so if it were
// compared these two would differ and the storm would be back — while the diff
// that reintroduced it would look like a tightening, not a loosening.
{
  const a = base() as any[];
  const b = base() as any[];
  a[0].lastSeen = 1_000_000;
  b[0].lastSeen = 1_000_000 + 30_000; // one refresh tick later
  check("lastSeen alone does NOT create a new identity (storm guard)", sameDeviceList(a, b), true);
}

// --- must NOT be too loose ------------------------------------------------
{
  const b = base(); b[1].online = true;
  check("online change is detected", sameDeviceList(base(), b), false);
}
{
  const b = base(); b[0].host = "192.168.1.99";
  check("host change is detected", sameDeviceList(base(), b), false);
}
{
  const b = base(); b[0].agentVersion = "1.99.313";
  check("agentVersion change is detected", sameDeviceList(base(), b), false);
}
{
  // The exact transition behind "Claude Code needs sign-in" -> signed in.
  const b = base();
  b[0].runners = [{ id: "claude", installed: true, ready: true, authConfigured: true }];
  check("runner auth transition is detected", sameDeviceList(base(), b), false);
}
{
  const b = base(); b[0].lanIps = ["192.168.1.10"]; // tailnet address dropped
  check("lanIps change is detected", sameDeviceList(base(), b), false);
}
{
  const b = base(); b[0].needsAuth = true;
  check("needsAuth change is detected", sameDeviceList(base(), b), false);
}
{
  const b = base(); b.pop();
  check("a device disappearing is detected", sameDeviceList(base(), b), false);
}
{
  const b = base(); b.reverse();
  check("reordering is detected", sameDeviceList(base(), b), false);
}
{
  const b = base(); b[0].alias = "mac-mini";
  check("alias change is detected", sameDeviceList(base(), b), false);
}

// --- edges ---------------------------------------------------------------
check("two empty lists are equal", sameDeviceList([], []), true);
check("empty vs non-empty differs", sameDeviceList([], base()), false);
{
  const a: ComparableDevice[] = [{ id: "x" }];
  const b: ComparableDevice[] = [{ id: "x", runners: [] }];
  check("undefined and empty runners are equivalent", sameDeviceList(a, b), true);
}

if (failures > 0) {
  console.error(`\n${failures} failure(s)`);
  process.exit(1);
}
console.log("\nall passed");
