// packageShareClient.ts — runner side of Task Package sharing
// (docs/yaver-task-packages.md). Calls the Convex HTTP routes added in
// backend/convex/http.ts: look up a shared package by invite code, accept it
// under consent, and list packages shared with me. Convex holds bookkeeping
// only; the real spec is pulled from the owner box at run time.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { getToken, getConvexSiteUrl } from "./auth";

const SELF_DEVICE_KEY = "yaver.self.deviceId";

export type SharedAllocation = {
  id: string;
  status: string;
  target: string;
  packageName: string;
  kind: string;
  tier: string;
  domains: string[];
  schedule: string;
  consentSummary: string;
  willNot: string[];
  dataShown: string[];
};

export type SharedListRow = {
  id: string;
  packageName: string;
  target: string;
  status: string;
  consentSummary: string;
  inviteCode: string;
  wifiOnly: boolean;
  chargingOnly: boolean;
  runCount: number;
  lastRunAt: number;
  lastStatus: string;
};

// selfDeviceId returns a stable id for THIS phone (minted + persisted once),
// recorded as the runner device on acceptance.
export async function selfDeviceId(): Promise<string> {
  let id = await AsyncStorage.getItem(SELF_DEVICE_KEY);
  if (!id) {
    id = "phone-" + Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
    await AsyncStorage.setItem(SELF_DEVICE_KEY, id);
  }
  return id;
}

export async function lookupSharedPackage(code: string): Promise<SharedAllocation> {
  const res = await fetch(`${getConvexSiteUrl()}/packages/allocation`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code: code.trim() }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data?.error || `lookup failed (${res.status})`);
  return data as SharedAllocation;
}

export async function acceptSharedPackage(
  code: string,
  opts: { wifiOnly?: boolean; chargingOnly?: boolean } = {},
): Promise<{ ok: boolean; allocationId?: string }> {
  const token = await getToken();
  if (!token) throw new Error("sign in first");
  const deviceId = await selfDeviceId();
  const res = await fetch(`${getConvexSiteUrl()}/packages/accept`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ code: code.trim(), deviceId, ...opts }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data?.error || `accept failed (${res.status})`);
  return data;
}

export async function listSharedPackages(): Promise<SharedListRow[]> {
  const token = await getToken();
  if (!token) return [];
  const res = await fetch(`${getConvexSiteUrl()}/packages/shared`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data?.error || `list failed (${res.status})`);
  return (data?.allocations ?? []) as SharedListRow[];
}
