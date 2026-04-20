/**
 * Re-export the canonical dedup / freshness / race-probe helpers from
 * @yaver/client-core (mirrored into `./_core/`).
 *
 * The SDK previously carried its own ~180-line copy of
 * `collapseAliasDevices` ported by hand from mobile's
 * DeviceContext.tsx. The copies drifted on `hwid` strong-identity,
 * `runners` / `local` field preservation, and a few other subtle
 * merge rules. Canonicalising via client-core eliminates that drift
 * class — mobile's test case automatically covers the SDK's dedup
 * behaviour too.
 *
 * The shared module operates on a `CoreDevice` shape. The SDK's
 * `RemoteDevice` is structurally compatible (same field names, same
 * types), so the casts here are a no-op at runtime.
 */

import {
  collapseDevices,
  isDeviceFresh as isCoreDeviceFresh,
  pickTargetDevice as pickCoreTargetDevice,
  type CoreDevice,
} from './_core/device';
import type { RemoteDevice } from './auth';

export { HEARTBEAT_STALE_MS } from './_core/constants';

export function collapseRemoteDevices(devices: RemoteDevice[]): RemoteDevice[] {
  return collapseDevices(devices as unknown as CoreDevice[]) as unknown as RemoteDevice[];
}

export function isDeviceFresh(d: RemoteDevice): boolean {
  return isCoreDeviceFresh(d as unknown as CoreDevice);
}

export function pickTargetDevice(
  devices: RemoteDevice[],
  preferredDeviceId?: string,
): RemoteDevice | null {
  const pick = pickCoreTargetDevice(
    devices as unknown as CoreDevice[],
    preferredDeviceId,
  );
  return (pick ?? null) as RemoteDevice | null;
}
