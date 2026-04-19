// Shim for expo-constants.

export const expoConfig = {
  name: "yaver-mobile-headless",
  slug: "yaver-mobile-headless",
  version: "0.1.0",
  extra: {} as Record<string, any>,
};

export const manifest = {
  id: "yaver-mobile-headless",
  name: "yaver-mobile-headless",
  extra: {} as Record<string, any>,
};

export const executionEnvironment = "storeClient";
export const sessionId = "mobile-headless-session";
export const platform = { ios: {}, android: {} };
export const deviceName = process.env.YMH_DEVICE_NAME || "mobile-headless";

export default {
  expoConfig, manifest, executionEnvironment, sessionId, platform, deviceName,
};
