/**
 * probeTargets.test.ts — `npx tsx src/lib/probeTargets.test.ts`.
 * No RN, no jest — the tiny assert harness the voice libs use.
 *
 * These pin the `.local` exclusion. The bug was silent and expensive: Convex
 * stores a macOS agent's hostname as `<Name>-Mac-mini.local`, the probe dialled
 * it, and on iOS that mDNS lookup HANGS until Local Network permission is
 * granted — burning the entire probe budget on an address the real connector
 * (quic.ts, which requires a private IP) would never have used. The probe then
 * reported "unreachable" for a box that was one relay hop away.
 */
import { buildDirectProbeTargets, isMdnsName } from "./probeTargets";

let failures = 0;
function check(name: string, cond: boolean) {
  if (cond) {
    console.log(`  ok  ${name}`);
  } else {
    failures++;
    console.error(`FAIL  ${name}`);
  }
}
function eq<T>(name: string, actual: T, expected: T) {
  check(`${name} (got ${JSON.stringify(actual)})`, JSON.stringify(actual) === JSON.stringify(expected));
}

console.log("isMdnsName");
check("plain .local", isMdnsName("Mobiles-Mac-mini.local"));
check("trailing dot", isMdnsName("Mobiles-Mac-mini.local."));
check("case-insensitive", isMdnsName("BOX.LOCAL"));
check("private IP is not mDNS", !isMdnsName("192.168.111.38"));
check("public host is not mDNS", !isMdnsName("box.example.com"));
check("a name merely containing 'local' is not mDNS", !isMdnsName("localhost"));
check("undefined", !isMdnsName(undefined));
check("empty", !isMdnsName(""));

console.log("buildDirectProbeTargets");

// The exact production case from the 2026-07-18 report.
eq(
  ".local host is excluded, lanIps survive",
  buildDirectProbeTargets({ host: "Mobiles-Mac-mini.local", port: 18080, lanIps: ["192.168.111.38"] }),
  ["http://192.168.111.38:18080"],
);

eq(
  ".local host with no lanIps yields NO direct legs (relay-only, and fast)",
  buildDirectProbeTargets({ host: "Mobiles-Mac-mini.local", port: 18080, lanIps: [] }),
  [],
);

eq(
  "routable host is kept, both ports",
  buildDirectProbeTargets({ host: "192.168.1.5", port: 4433, lanIps: [] }),
  ["http://192.168.1.5:4433", "http://192.168.1.5:18080"],
);

eq(
  "port defaults to 18080 and dedupes against the explicit 18080 leg",
  buildDirectProbeTargets({ host: "10.0.0.2", port: undefined, lanIps: [] }),
  ["http://10.0.0.2:18080"],
);

eq(
  "lanIps are expanded on both ports and deduped against the host legs",
  buildDirectProbeTargets({ host: "10.0.0.2", port: 4433, lanIps: ["10.0.0.2", "100.64.0.1"] }),
  [
    "http://10.0.0.2:4433",
    "http://10.0.0.2:18080",
    "http://100.64.0.1:4433",
    "http://100.64.0.1:18080",
  ],
);

eq(
  "null/empty lanIps entries are dropped, not stringified into a URL",
  buildDirectProbeTargets({ host: null, port: 18080, lanIps: [null, "", "10.1.1.1", undefined] }),
  ["http://10.1.1.1:18080"],
);

eq("no host and no lanIps yields nothing", buildDirectProbeTargets({}), []);

console.log(failures === 0 ? "\nAll probeTargets tests passed." : `\n${failures} FAILED`);
process.exit(failures === 0 ? 0 : 1);
