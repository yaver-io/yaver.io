// pushAuth.ts — device-auth approval push channel (P2, receive side).
//
// Registers this phone's push token with the backend so a remote box's
// re-auth can ring it; and routes an incoming "device_auth_request"
// notification to the Face-ID approval screen (app/approve-device.tsx).
//
// DORMANT until a transport exists: native builds have no EAS projectId yet,
// so getExpoPushTokenAsync() throws and registerForAuthPush() returns quietly.
// Activate by giving the app an EAS projectId (Expo brokers APNs/FCM — no
// provider key needed) or wiring native APNs/FCM and storing those tokens.

import * as Notifications from "expo-notifications";
import * as Device from "expo-device";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { router } from "expo-router";
import { Platform } from "react-native";
import { getConvexSiteUrlSync as getConvexSiteUrl } from "./backendConfig";
import { appLog } from "./logger";

const INSTALL_ID_KEY = "@yaver/push_install_id";

async function getInstallId(): Promise<string> {
  let id = await AsyncStorage.getItem(INSTALL_ID_KEY);
  if (!id) {
    id = `inst_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
    await AsyncStorage.setItem(INSTALL_ID_KEY, id);
  }
  return id;
}

/** Register this phone for device-auth approval pushes. No-ops on simulators,
 *  without permission, or until a push transport is configured. */
export async function registerForAuthPush(token: string): Promise<void> {
  if (!token) return;
  try {
    if (!Device.isDevice) return; // simulators can't receive push
    let { status } = await Notifications.getPermissionsAsync();
    if (status !== "granted") {
      status = (await Notifications.requestPermissionsAsync()).status;
    }
    if (status !== "granted") return;

    let pushToken: string;
    try {
      pushToken = (await Notifications.getExpoPushTokenAsync()).data;
    } catch (e) {
      // No EAS projectId / native push yet → stay dormant, not an error.
      appLog("info", `[push] device-auth push dormant (no transport): ${e}`);
      return;
    }

    const installId = await getInstallId();
    await fetch(`${getConvexSiteUrl()}/push/register`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
      body: JSON.stringify({ installId, pushToken, transport: "expo", platform: Platform.OS }),
    });
    appLog("info", "[push] registered device-auth push token");
  } catch (e) {
    appLog("warn", `[push] register failed: ${e}`);
  }
}

/** Route an incoming device-auth push to the Face-ID approval screen.
 *  Returns an unsubscribe fn. */
export function installAuthPushListener(): () => void {
  const sub = Notifications.addNotificationResponseReceivedListener((resp) => {
    const data = resp.notification.request.content.data as { type?: string; userCode?: string };
    if (data?.type === "device_auth_request" && data?.userCode) {
      try {
        router.push(`/approve-device?code=${encodeURIComponent(String(data.userCode))}`);
      } catch {
        /* navigation may not be ready; the user can still open it manually */
      }
    }
  });
  return () => sub.remove();
}
