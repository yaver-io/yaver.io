// deviceCapability.ts — reads this phone's real capability from expo-device and
// turns it into the DeviceCapability tiers.ts/models.ts consume. RN-bound (imports
// expo-device) so NOT tsx-tested; the gate math it relies on is tested in
// deviceCapabilityCore.test.mts.
//
// Single source of truth for "what can this device run?" — used by the
// on-device model picker (runnable gating + recommendation), the coding-backend
// availability (don't mark on-device ready on a phone that can't load a coder),
// and the voice helper (router vs scripted tier).

import { useEffect, useState } from "react";
import * as Device from "expo-device";

import { bytesToMb, ramToModelClass } from "./deviceCapabilityCore";
import { selectModelTier, type DeviceCapability, type ModelTier } from "./localAgent/tiers";

/** Read the device's capability synchronously from expo-device. */
export function readDeviceCapability(): DeviceCapability {
  const totalRamMb = bytesToMb(Device.totalMemory);
  return {
    totalRamMb,
    // RAM-derived class so tiers' coder gate (needs medium) fires on 8GB+
    // without a per-model chip table. chip/thermal aren't reliably exposed by
    // expo-device cross-platform, so we leave them undefined (treated as
    // not-hot / unknown — tiers + canRunModel stay conservative).
    maxModelClass: ramToModelClass(totalRamMb),
  };
}

/** The highest model tier this device can safely run right now. */
export function deviceModelTier(): ModelTier {
  return selectModelTier(readDeviceCapability());
}

export interface DeviceCapabilityInfo {
  capability: DeviceCapability;
  tier: ModelTier;
  totalRamMb?: number;
}

/** React hook: capability + derived tier, read once on mount. */
export function useDeviceCapability(): DeviceCapabilityInfo {
  const [info, setInfo] = useState<DeviceCapabilityInfo>(() => ({
    capability: {},
    tier: "none",
  }));
  useEffect(() => {
    const capability = readDeviceCapability();
    setInfo({ capability, tier: selectModelTier(capability), totalRamMb: capability.totalRamMb });
  }, []);
  return info;
}
