// Shim for expo-device. Static constants, overridable via env so a
// test can impersonate a specific iOS/Android device profile.

import * as os from "node:os";

function env(k: string, fallback: string): string {
  const v = process.env[k];
  return v && v.trim() ? v : fallback;
}

export const osName = env("YMH_OS_NAME", (process.env.YMH_PLATFORM === "android" ? "Android" : "iOS"));
export const osVersion = env("YMH_OS_VERSION", process.env.YMH_PLATFORM === "android" ? "15" : "18.0");
export const deviceName = env("YMH_DEVICE_NAME", `mobile-headless-${os.hostname()}`);
export const modelName = env("YMH_MODEL_NAME", process.env.YMH_PLATFORM === "android" ? "Pixel 8 Pro" : "iPhone 16 Pro");
export const brand = env("YMH_BRAND", process.env.YMH_PLATFORM === "android" ? "Google" : "Apple");
export const manufacturer = env("YMH_MANUFACTURER", process.env.YMH_PLATFORM === "android" ? "Google" : "Apple");
export const isDevice = true;
export const deviceType = 1; // PHONE
export const supportedCpuArchitectures = [os.arch()];

export const DeviceType = {
  UNKNOWN: 0,
  PHONE: 1,
  TABLET: 2,
  DESKTOP: 3,
  TV: 4,
};

export async function getDeviceTypeAsync() {
  return DeviceType.PHONE;
}

export default {
  osName, osVersion, deviceName, modelName, brand, manufacturer,
  isDevice, deviceType, supportedCpuArchitectures, DeviceType, getDeviceTypeAsync,
};
