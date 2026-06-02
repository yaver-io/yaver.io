// localAgent/adapterBindings.ts — the RN-bound wiring that turns the live
// app's real clients into the adapter's injected interfaces. NOT part of the
// pure barrel (index.ts) and NOT tsx-tested: it imports quic.ts / yaverMcpDirect
// / DeviceContext. The pure, testable logic lives in adapter.ts; this is the
// 30-line glue the voice runtime imports.

import { quicClient } from "../quic";
import { callMcpDirect, callMobileHermesDoctor } from "../yaverMcpDirect";
import type { Device, DeviceState } from "../../context/DeviceContext";
import type { DispatchDeps, DeviceProbe } from "./adapter";

/** Build DispatchDeps from a live DeviceState (from useDevice()). The context
 *  fns take the real Device; the adapter passes a DeviceLike that is, at
 *  runtime, the full Device the resolver picked — so the casts are safe. */
export function makeDispatchDeps(
  ds: DeviceState,
  opts: { confirmed?: boolean } = {},
): DispatchDeps {
  return {
    confirmed: opts.confirmed,
    context: {
      selectDevice: (d) => ds.selectDevice(d as Device),
      recoverDeviceAuth: (d) => ds.recoverDeviceAuth(d as Device),
      setPrimaryDevice: (id) => ds.setPrimaryDevice(id),
      setSecondaryDevice: (id) => ds.setSecondaryDevice(id),
      setDeviceAlias: (d, alias) => ds.setDeviceAlias(d as Device, alias),
      claimPendingDevice: (id, name) => ds.claimPendingDevice(id, name),
      setPrimaryRunner: (id, runnerId, model) => ds.setPrimaryRunnerForDevice(id, runnerId, model),
      refreshDevices: () => ds.refreshDevices(),
    },
    // callOps targets the active (connected) box — exactly the device the
    // reachability gate guarantees we're connected to.
    ops: (verb, payload) => quicClient.callOps(verb, payload),
    mcp: async (tool, args) => {
      const r = await callMcpDirect(tool, args);
      return { ok: r.ok, error: r.error };
    },
  };
}

/** Probe a device for the facts a plain device row doesn't carry: runner
 *  readiness (audit), projects, and optionally the Hermes stack. gitAuthed has
 *  no mobile query yet → left undefined (ladder will prompt git.connect, the
 *  safe default). activeProjectSlug is tracked by the voice runtime. */
export async function probeDevice(opts: {
  probeHermes?: boolean;
  activeProjectSlug?: string;
  gitAuthed?: boolean;
  deployTargetConfigured?: boolean;
} = {}): Promise<DeviceProbe> {
  const [audit, projects] = await Promise.all([
    quicClient.yaverAgentAudit().catch(() => undefined),
    quicClient.listProjects().catch(() => [] as { name: string; branch?: string }[]),
  ]);

  let hermesReady: boolean | undefined;
  if (opts.probeHermes) {
    const doc = await callMobileHermesDoctor({}).catch(() => undefined);
    // Conservative: only "ready" when the doctor clearly says so; unknown →
    // undefined so the ladder offers to provision rather than assume.
    const res = (doc?.result ?? {}) as Record<string, unknown>;
    hermesReady = doc?.ok ? res.ready === true || res.installed === true || undefined : undefined;
  }

  return {
    audit,
    projects: (projects ?? []).map((p) => ({ name: p.name, branch: p.branch })),
    gitAuthed: opts.gitAuthed,
    hermesReady,
    activeProjectSlug: opts.activeProjectSlug,
    deployTargetConfigured: opts.deployTargetConfigured,
  };
}
