/**
 * Shared "probe, and self-heal a stale relay credential" step.
 *
 * Why this exists: the manual switch path (RemoteBoxPickerModal) and the
 * automatic connect path (DeviceContext) both need to decide "is this box
 * reachable?", but only the manual one ever learned to repair a stale relay
 * password before answering. The automatic path did a bare
 *
 *     if (!probe?.reachable) continue;
 *
 * and never inspected `errorCode` — so an account whose per-user relay
 * credential had drifted would fail EVERY auto-connect, silently, and drop the
 * user on "No machine selected". Tapping the box by hand then worked, because
 * the picker repaired the credential on the way through. The default path was
 * the degraded one; the manual fallback was the only complete one.
 *
 * Keeping the ladder in one place is the point. If a future rung is added
 * (another self-healable errorCode), both surfaces get it or neither does.
 *
 * `onStage` lets each caller narrate with its own copy: the picker writes into
 * its "Switching" modal, auto-connect writes into the banner. Callers that want
 * no narration can omit it.
 */

import type { MobileDeviceStatusProbe } from "./deviceStatus";

/** Signature of `probeMobileDeviceStatus`, so tests can substitute a fake. */
export type ProbeFn = (
  device: { id: string; host?: string; port?: number; lanIps?: string[] },
  token?: string | null,
  timeoutMs?: number,
) => Promise<MobileDeviceStatusProbe>;

/**
 * The real prober is resolved lazily. deviceStatus.ts imports React Native, so
 * a static import here would make this module unimportable from the RN-free
 * tsx test harness — and the ladder below is precisely what needs pinning.
 */
async function defaultProbe(...args: Parameters<ProbeFn>): Promise<MobileDeviceStatusProbe> {
  const { probeMobileDeviceStatus } = await import("./deviceStatus");
  return probeMobileDeviceStatus(...args);
}

export interface ProbeTarget {
  id: string;
  name: string;
  host?: string;
  port?: number;
  lanIps?: string[];
}

export interface ProbeWithRepairOptions {
  token?: string | null;
  timeoutMs?: number;
  /** Called with human-readable progress. Same strings on every surface. */
  onStage?: (stage: string) => void;
  /**
   * DeviceContext.repairRelay. Omit to disable the repair rung entirely (the
   * probe still runs) — used by callers that have no DeviceContext handle.
   */
  repairRelay?: () => Promise<{ ok: boolean; relays: number; error?: string }>;
  /** Bail out between rungs when the caller has been cancelled. */
  isCancelled?: () => boolean;
  /** Injectable prober. Tests substitute a fake; production uses the real one. */
  probe?: ProbeFn;
}

export interface ProbeWithRepairResult {
  probe: MobileDeviceStatusProbe | null;
  /** True when the relay credential was repaired and the box re-probed. */
  repaired: boolean;
}

/**
 * Probe `target`; if the only reason it failed is that relay servers are
 * configured but none carries a password (a stale/absent per-user credential,
 * which `/settings/repair-relay` re-copies from the platform value), repair it
 * and re-probe exactly ONCE.
 *
 * Once only, deliberately: a genuine outage must fail fast rather than spin a
 * repair loop against the backend.
 */
export async function probeDeviceWithRepair(
  target: ProbeTarget,
  opts: ProbeWithRepairOptions = {},
): Promise<ProbeWithRepairResult> {
  const { token, timeoutMs = 4000, onStage, repairRelay, isCancelled, probe: probeFn = defaultProbe } = opts;
  const probeArgs = {
    id: target.id,
    host: target.host,
    port: target.port,
    lanIps: target.lanIps,
  };

  onStage?.(`Pinging ${target.name}…`);
  let probe = await probeFn(probeArgs, token, timeoutMs).catch(() => null);

  if (isCancelled?.()) return { probe, repaired: false };

  if (!probe?.reachable && probe?.errorCode === "relay-credentials-missing" && repairRelay) {
    onStage?.("Relay credential looks stale — repairing…");
    const repair = await repairRelay();
    if (isCancelled?.()) return { probe, repaired: false };
    if (repair.ok && repair.relays > 0) {
      onStage?.(`Repaired — re-checking ${target.name}…`);
      probe = await probeFn(probeArgs, token, timeoutMs).catch(() => null);
      return { probe, repaired: true };
    }
  }

  return { probe, repaired: false };
}
