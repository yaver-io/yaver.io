/**
 * Structural equality for the device list. Pure — no React Native imports — so
 * it is unit-testable (`npx tsx src/lib/deviceListEquality.test.ts`).
 *
 * WHY
 *
 * refreshDevices runs every 30s and handed setDevices a freshly built array
 * every time, so `devices` got a new identity on every tick even when the fleet
 * was unchanged. Every effect keyed on `devices` therefore re-ran on a 30s
 * metronome — including the one that re-enters setFocused/clientFor/
 * setConnectionStatus, which re-triggers the direct-candidate race for every
 * known box.
 *
 * Measured: ~15 probes per cycle across 6 devices at 2.5s each, plus four
 * concurrent reconnect ladders whose backoff never escaped because the tick
 * kept restarting them. That storm starved the connection that was working.
 *
 * THE TRAP, and the one deliberate omission
 *
 * `lastSeen` advances on every heartbeat. Including it here would make two
 * device lists NEVER compare equal, which would silently restore the exact
 * behaviour this exists to prevent — while looking perfectly correct in review.
 * It is therefore excluded ON PURPOSE.
 *
 * That is safe because `online` already encodes the transition that matters: it
 * is derived from heartbeat freshness plus live relay presence, so a box going
 * away flips `online` and DOES produce a new array. What is lost is only the
 * per-tick advance of a relative "last seen 3m ago" label; anything needing
 * that should re-render on its own timer rather than by mutating the device
 * list, which is the cheaper and more honest mechanism anyway.
 *
 * Everything else that drives connection behaviour or is user-visible IS
 * compared — miss a field here and the UI silently stops reacting to it, which
 * is the opposite and equally bad failure.
 */

/** The subset compared. Structural rather than importing the Device type so
 *  this module stays free of the React Native dependency chain. */
export interface ComparableDevice {
  id: string;
  name?: string;
  alias?: string;
  host?: string;
  port?: number;
  online?: boolean;
  os?: string;
  needsAuth?: boolean;
  local?: boolean;
  hwid?: string;
  agentVersion?: string;
  publicKey?: string;
  installedRunnerIds?: string[];
  voiceHints?: string[];
  lanIps?: (string | null | undefined)[] | null;
  publicEndpoints?: (string | null | undefined)[] | null;
  runners?: { id?: string; installed?: boolean; ready?: boolean; authConfigured?: boolean; error?: string }[];
}

function sameStringish(a?: unknown[] | null, b?: unknown[] | null): boolean {
  const x = a ?? [];
  const y = b ?? [];
  if (x.length !== y.length) return false;
  for (let i = 0; i < x.length; i++) {
    if (String(x[i] ?? "") !== String(y[i] ?? "")) return false;
  }
  return true;
}

/** Runner state drives the banner ("needs sign-in"), the composer chip, and
 *  which agents a task can be sent to — so a change here MUST produce a new
 *  array even though nothing about reachability moved. */
function sameRunners(a: ComparableDevice["runners"], b: ComparableDevice["runners"]): boolean {
  const x = a ?? [];
  const y = b ?? [];
  if (x.length !== y.length) return false;
  for (let i = 0; i < x.length; i++) {
    const p = x[i] ?? {};
    const q = y[i] ?? {};
    if (
      p.id !== q.id ||
      p.installed !== q.installed ||
      p.ready !== q.ready ||
      p.authConfigured !== q.authConfigured ||
      (p.error ?? "") !== (q.error ?? "")
    ) {
      return false;
    }
  }
  return true;
}

/** True when two device lists are materially identical — same devices, same
 *  order, same connection-relevant and user-visible state. `lastSeen` is
 *  deliberately not compared; see the module header. */
export function sameDeviceList(a: ComparableDevice[], b: ComparableDevice[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const p = a[i];
    const q = b[i];
    if (
      p.id !== q.id ||
      p.name !== q.name ||
      p.alias !== q.alias ||
      p.host !== q.host ||
      p.port !== q.port ||
      p.online !== q.online ||
      p.os !== q.os ||
      p.needsAuth !== q.needsAuth ||
      p.local !== q.local ||
      p.hwid !== q.hwid ||
      p.agentVersion !== q.agentVersion ||
      p.publicKey !== q.publicKey
    ) {
      return false;
    }
    if (!sameStringish(p.installedRunnerIds, q.installedRunnerIds)) return false;
    if (!sameStringish(p.voiceHints, q.voiceHints)) return false;
    if (!sameStringish(p.lanIps, q.lanIps)) return false;
    if (!sameStringish(p.publicEndpoints, q.publicEndpoints)) return false;
    if (!sameRunners(p.runners, q.runners)) return false;
  }
  return true;
}
