import AsyncStorage from "@react-native-async-storage/async-storage";
import { NativeModules, Platform } from "react-native";
import { callMcpDirect, type McpDirectResult } from "./yaverMcpDirect";

const SELECTED_KEY = "yaver.appSync.selectedPackages";

export type PhoneInstalledApp = {
  packageName: string;
  label: string;
  activityName?: string;
  versionName?: string;
  versionCode?: number;
  system?: boolean;
  launchable?: boolean;
  requestedPermissions?: string[];
};

type NativeInventory = {
  listLaunchableApps?: () => Promise<PhoneInstalledApp[]>;
  getPackageInfo?: (packageName: string) => Promise<PhoneInstalledApp>;
};

function nativeInventory(): NativeInventory | null {
  return (NativeModules as { YaverAppInventory?: NativeInventory }).YaverAppInventory ?? null;
}

export async function listPhoneApps(): Promise<PhoneInstalledApp[]> {
  if (Platform.OS !== "android") return [];
  const mod = nativeInventory();
  if (!mod?.listLaunchableApps) return [];
  return mod.listLaunchableApps();
}

// PHONE_ID_KEY holds this device's stable, self-minted inventory id. It is NOT
// the Convex device id — it's a local, zero-config identifier so the phone can
// report its app inventory the very first time the app opens, with no
// registration round-trip. Stable across launches; survives until reinstall.
const PHONE_ID_KEY = "yaver.appSync.phoneId";

/** getOrCreatePhoneId returns this phone's stable inventory id, minting + saving
 *  one on first use. Zero-friction: no user action, no registration dependency.
 *  The same scheme works identically for the user's daily phone and a
 *  second-hand handset — each just self-identifies. */
export async function getOrCreatePhoneId(): Promise<string> {
  const existing = await AsyncStorage.getItem(PHONE_ID_KEY);
  if (existing && existing.trim()) return existing.trim();
  const rand = Math.random().toString(36).slice(2, 10);
  const id = `phone-${Platform.OS}-${rand}`;
  await AsyncStorage.setItem(PHONE_ID_KEY, id);
  return id;
}

/** reportPhoneInventory PUSHES this phone's app list up to the connected agent
 *  (gateway_phone_inventory_report) so it can be mirrored onto a clone
 *  (redroid or a second-hand phone). Best-effort + idempotent — safe to call on
 *  every app-sync screen open. No-op on iOS (it cannot enumerate apps). Accepts
 *  the already-listed apps to avoid a second native call. */
export async function reportPhoneInventory(
  apps: PhoneInstalledApp[],
): Promise<{ ok: boolean; device?: string; count?: number; error?: string }> {
  if (Platform.OS !== "android" || apps.length === 0) {
    return { ok: false, error: "nothing to report (non-android or empty)" };
  }
  const device = await getOrCreatePhoneId();
  const payload = apps.map((a) => ({
    packageName: a.packageName,
    label: a.label,
    system: a.system ?? false,
  }));
  const res = await callMcpDirect<{ ok?: boolean; count?: number; error?: string }>(
    "gateway_phone_inventory_report",
    { device, apps: payload },
  );
  if (!res.ok || res.result?.error) {
    return { ok: false, device, error: res.result?.error || res.error || "report failed" };
  }
  return { ok: true, device, count: res.result?.count };
}

/** relayOtpToAgent forwards an OTP/2FA code the phone received up to the connected
 *  agent so a redroid/device login blocked waiting for it completes seamlessly —
 *  no gate id, no typing into a remote view. The agent matches it to the oldest
 *  pending code-gate for the connector; an unmatched code is a safe no-op. This is
 *  the call an SMS-capture (e.g. the SMS User Consent API) wires into. */
export async function relayOtpToAgent(
  connector: string,
  code: string,
): Promise<{ ok: boolean; gateId?: string; error?: string }> {
  const c = connector.trim();
  const v = code.trim();
  if (!c || !v) return { ok: false, error: "connector and code required" };
  const res = await callMcpDirect<{ ok?: boolean; gateId?: string; error?: string }>(
    "gateway_provide_otp",
    { connector: c, code: v },
  );
  if (!res.ok || res.result?.ok === false) {
    return { ok: false, error: res.result?.error || res.error || "relay failed" };
  }
  return { ok: true, gateId: res.result?.gateId };
}

export async function getSelectedAppPackages(): Promise<string[]> {
  const raw = await AsyncStorage.getItem(SELECTED_KEY);
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((v) => typeof v === "string") : [];
  } catch {
    return [];
  }
}

export async function setSelectedAppPackages(packages: string[]): Promise<void> {
  const clean = Array.from(new Set(packages.map((p) => p.trim()).filter(Boolean))).sort();
  await AsyncStorage.setItem(SELECTED_KEY, JSON.stringify(clean));
}

export type RedroidAppStatus = {
  ok: boolean;
  packageName?: string;
  installed?: boolean;
  state?: string;
  visibleText?: string;
  error?: string;
  [k: string]: unknown;
};

export function remoteAndroidAppStatus(packageName: string, deviceId?: string): Promise<McpDirectResult<RedroidAppStatus>> {
  return callMcpDirect<RedroidAppStatus>("android_app_status", { package_name: packageName, device_id: deviceId });
}

export function remoteAndroidAppLaunch(packageName: string, deviceId?: string): Promise<McpDirectResult<RedroidAppStatus>> {
  return callMcpDirect<RedroidAppStatus>("android_app_launch", { package_name: packageName, device_id: deviceId });
}

export function remoteAndroidAppQuery(opts: {
  packageName: string;
  query: string;
  waitText?: string;
  deviceId?: string;
}): Promise<McpDirectResult<RedroidAppStatus>> {
  return callMcpDirect<RedroidAppStatus>("android_app_query", {
    package_name: opts.packageName,
    query: opts.query,
    wait_text: opts.waitText,
    device_id: opts.deviceId,
  });
}
