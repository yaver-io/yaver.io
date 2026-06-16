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
