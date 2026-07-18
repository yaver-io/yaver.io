/**
 * probeWithRepair.test.ts — `npx tsx src/lib/probeWithRepair.test.ts`.
 * No RN, no jest — the tiny assert harness the voice libs use.
 *
 * Pins the relay-credential self-heal ladder that BOTH the manual switch and
 * the automatic connect now share. The regression this guards against is the
 * asymmetry itself: auto-connect used to do a bare `if (!probe.reachable)
 * continue`, so an account with a drifted per-user relay password failed every
 * automatic connect while a manual tap on the same box repaired and connected.
 */
import { probeDeviceWithRepair, type ProbeFn } from "./probeWithRepair";
import type { MobileDeviceStatusProbe } from "./deviceStatus";

let failures = 0;
function check(name: string, cond: boolean) {
  if (cond) {
    console.log(`  ok  ${name}`);
  } else {
    failures++;
    console.error(`FAIL  ${name}`);
  }
}

const TARGET = { id: "dev-1", name: "Mac mini" };

function probeResult(over: Partial<MobileDeviceStatusProbe> = {}): MobileDeviceStatusProbe {
  return {
    reachable: false,
    bootstrap: false,
    authExpired: false,
    codingReady: false,
    codingRunners: [],
    lifecycleState: null,
    checkedAt: 0,
    ...over,
  } as MobileDeviceStatusProbe;
}

/** A prober that returns each queued result in order. */
function scriptedProbe(results: MobileDeviceStatusProbe[]): { fn: ProbeFn; calls: () => number } {
  let i = 0;
  return {
    fn: async () => results[Math.min(i++, results.length - 1)],
    calls: () => i,
  };
}

async function main() {
  {
    const p = scriptedProbe([probeResult({ reachable: true, path: "relay" })]);
    const stages: string[] = [];
    let repairCalls = 0;
    const r = await probeDeviceWithRepair(TARGET, {
      probe: p.fn,
      onStage: (s) => stages.push(s),
      repairRelay: async () => {
        repairCalls++;
        return { ok: true, relays: 1 };
      },
    });
    check("reachable on first probe → no repair attempted", repairCalls === 0);
    check("reachable on first probe → single probe call", p.calls() === 1);
    check("reachable → result surfaced", r.probe?.reachable === true && r.repaired === false);
    check("narrates the ping", stages[0] === "Pinging Mac mini…");
  }

  {
    // The production failure: relays configured, none carries a password.
    const p = scriptedProbe([
      probeResult({ errorCode: "relay-credentials-missing" }),
      probeResult({ reachable: true, path: "relay" }),
    ]);
    const stages: string[] = [];
    const r = await probeDeviceWithRepair(TARGET, {
      probe: p.fn,
      onStage: (s) => stages.push(s),
      repairRelay: async () => ({ ok: true, relays: 2 }),
    });
    check("stale relay credential → repaired and re-probed", r.repaired === true);
    check("stale relay credential → ends reachable", r.probe?.reachable === true);
    check("stale relay credential → exactly two probes", p.calls() === 2);
    check(
      "narrates the full ladder",
      stages.join("|") ===
        "Pinging Mac mini…|Relay credential looks stale — repairing…|Repaired — re-checking Mac mini…",
    );
  }

  {
    // A genuine outage must not spin the repair loop.
    const p = scriptedProbe([probeResult({ errorCode: "relay-credentials-missing" })]);
    let repairCalls = 0;
    const r = await probeDeviceWithRepair(TARGET, {
      probe: p.fn,
      repairRelay: async () => {
        repairCalls++;
        return { ok: true, relays: 1 };
      },
    });
    check("repair runs at most once", repairCalls === 1);
    check("still-unreachable after repair → reported unreachable", r.probe?.reachable === false);
    check("still-unreachable after repair → exactly two probes, no loop", p.calls() === 2);
  }

  {
    // Any other failure code is NOT self-healable — don't call the backend.
    const p = scriptedProbe([probeResult({ errorCode: "no-transport" })]);
    let repairCalls = 0;
    await probeDeviceWithRepair(TARGET, {
      probe: p.fn,
      repairRelay: async () => {
        repairCalls++;
        return { ok: true, relays: 1 };
      },
    });
    check("no-transport is not treated as a credential problem", repairCalls === 0);
    check("no-transport → no re-probe", p.calls() === 1);
  }

  {
    // Repair that yields zero relays must not re-probe (nothing changed).
    const p = scriptedProbe([probeResult({ errorCode: "relay-credentials-missing" })]);
    const r = await probeDeviceWithRepair(TARGET, {
      probe: p.fn,
      repairRelay: async () => ({ ok: false, relays: 0, error: "session expired" }),
    });
    check("failed repair → no re-probe", p.calls() === 1);
    check("failed repair → repaired flag stays false", r.repaired === false);
  }

  {
    // Cancellation between rungs must stop the ladder immediately.
    const p = scriptedProbe([probeResult({ errorCode: "relay-credentials-missing" })]);
    let repairCalls = 0;
    await probeDeviceWithRepair(TARGET, {
      probe: p.fn,
      isCancelled: () => true,
      repairRelay: async () => {
        repairCalls++;
        return { ok: true, relays: 1 };
      },
    });
    check("cancelled after first probe → repair never runs", repairCalls === 0);
    check("cancelled after first probe → no re-probe", p.calls() === 1);
  }

  {
    // No repairRelay handle (callers outside DeviceContext) → probe still works.
    const p = scriptedProbe([probeResult({ errorCode: "relay-credentials-missing" })]);
    const r = await probeDeviceWithRepair(TARGET, { probe: p.fn });
    check("missing repairRelay handle → probe still returns", r.probe?.errorCode === "relay-credentials-missing");
    check("missing repairRelay handle → no crash, no re-probe", p.calls() === 1);
  }

  {
    // A throwing prober must degrade to null, not reject the caller.
    const r = await probeDeviceWithRepair(TARGET, {
      probe: async () => {
        throw new Error("network down");
      },
    });
    check("throwing prober → null probe, no rejection", r.probe === null && r.repaired === false);
  }

  console.log(failures === 0 ? "\nAll probeWithRepair tests passed." : `\n${failures} FAILED`);
  process.exit(failures === 0 ? 0 : 1);
}

void main();
