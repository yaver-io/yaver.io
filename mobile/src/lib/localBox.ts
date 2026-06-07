// localBox.ts — the phone-as-its-own-device. When the Android on-device agent
// (libyaver.so serve, started by SandboxService) is up on loopback, we surface
// it as a synthetic "This phone" Device so every existing screen — the terminal,
// the box picker, runner toggles — drives it exactly like a remote box, with
// ZERO protocol changes. The terminal opens /ws/terminal against 127.0.0.1:18080
// just as it would against a Hetzner box.
//
// Pure core (id, base URL, device builder, classifier) is tsx-tested; the
// reachability probe takes an injected fetch so it's testable too. The RN glue
// (NativeModules.YaverSandbox status, DeviceContext injection) lives in
// sandboxControl.ts / DeviceContext.

import type { Device } from "../context/DeviceContext";

/** Synthetic device id for this phone's loopback agent. Stable + namespaced so
 *  it can never collide with a Convex-issued device id. */
export const LOCAL_BOX_DEVICE_ID = "__this_phone__";

/** The on-device agent's loopback HTTP base. Mirrors the desktop agent port
 *  (18080) — SandboxService launches `libyaver.so serve` with the default port. */
export const LOCAL_BOX_BASE_URL = "http://127.0.0.1:18080";

export function isLocalBoxId(id: string | null | undefined): boolean {
  return id === LOCAL_BOX_DEVICE_ID;
}

/** Build the synthetic Device row for this phone. `platform` is Platform.OS;
 *  `runnerIds` are the runners the rootfs has installed (claude/codex/opencode),
 *  surfaced so the picker + runner toggles light up. `agentVersion` is optional
 *  (filled from /info when known). The shape mirrors a normal online device so
 *  selectDevice / connectionManager / the terminal treat it identically. */
export function buildLocalBoxDevice(opts: {
  platform: "ios" | "android" | "web";
  runnerIds?: string[];
  agentVersion?: string;
  online?: boolean;
}): Device {
  const online = opts.online ?? true;
  // Cast through a structural literal: we set every field the UI reads for a
  // device card + connection, and leave the long-tail optional fields unset.
  return {
    id: LOCAL_BOX_DEVICE_ID,
    name: "This phone",
    alias: "this phone",
    host: "127.0.0.1",
    port: 18080,
    online,
    lastSeen: 0, // stamped by the caller (Date.now is RN-side); 0 = "just now" sentinel
    os: opts.platform,
    runners: [],
    installedRunnerIds: opts.runnerIds ?? [],
    local: true,
    isPhone: true,
    agentVersion: opts.agentVersion,
    deviceClass: "edge-mobile",
  } as unknown as Device;
}

/** Probe whether the on-device agent is actually serving on loopback. We hit an
 *  unauthenticated route and treat any HTTP response (even 401/404) as "the
 *  socket is up" — a connection error means the agent isn't running. A 404 on a
 *  known route means the binary is stale (see the daemon-404 memory), which we
 *  report distinctly so the UI can prompt a sandbox restart. */
export interface LocalBoxProbe {
  reachable: boolean;
  /** True when the socket answered but the route 404'd → stale/older binary. */
  stale?: boolean;
  agentVersion?: string;
}

export async function probeLocalBox(
  fetchImpl: typeof fetch = fetch,
  baseUrl: string = LOCAL_BOX_BASE_URL,
  timeoutMs = 1500,
): Promise<LocalBoxProbe> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    const res = await fetchImpl(`${baseUrl}/info`, { signal: ctrl.signal });
    if (res.status === 404) return { reachable: true, stale: true };
    let agentVersion: string | undefined;
    try {
      const body: any = await res.json();
      agentVersion = body?.version ?? body?.agentVersion;
    } catch {
      // /info may be auth-gated or non-JSON; the socket answering is enough.
    }
    return { reachable: true, agentVersion };
  } catch {
    return { reachable: false };
  } finally {
    clearTimeout(timer);
  }
}
