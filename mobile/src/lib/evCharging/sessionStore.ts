import AsyncStorage from "@react-native-async-storage/async-storage";

import type { EVChargingIntent } from "./types";

const ACTIVE_EV_SESSION_KEY = "yaver.ev.activeSession.v1";

function isIntent(value: unknown): value is EVChargingIntent {
  if (!value || typeof value !== "object") return false;
  const v = value as Partial<EVChargingIntent>;
  return typeof v.id === "string"
    && typeof v.createdAt === "number"
    && typeof v.updatedAt === "number"
    && typeof v.provider === "string"
    && typeof v.state === "string"
    && Array.isArray(v.approvals)
    && Array.isArray(v.events);
}

export async function loadActiveEVSession(): Promise<EVChargingIntent | null> {
  const raw = await AsyncStorage.getItem(ACTIVE_EV_SESSION_KEY).catch(() => null);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as unknown;
    return isIntent(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export async function saveActiveEVSession(intent: EVChargingIntent): Promise<void> {
  await AsyncStorage.setItem(ACTIVE_EV_SESSION_KEY, JSON.stringify(intent));
}

export async function clearActiveEVSession(): Promise<void> {
  await AsyncStorage.removeItem(ACTIVE_EV_SESSION_KEY);
}
