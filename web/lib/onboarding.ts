"use client";

import { CONVEX_URL } from "@/lib/constants";

export async function hasRegisteredMachine(token: string): Promise<boolean> {
  try {
    const res = await fetch(`${CONVEX_URL}/devices/list`, {
      method: "GET",
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return false;
    const raw = await res.json();
    const devices = Array.isArray(raw) ? raw : (raw?.devices ?? []);
    return Array.isArray(devices) && devices.length > 0;
  } catch {
    return false;
  }
}
