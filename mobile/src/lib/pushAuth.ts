// pushAuth.ts — device-auth approval push channel (P2, receive side).
//
// Registers this phone's push token with the backend so a remote box's
// re-auth can ring it; and routes an incoming "device_auth_request"
// notification to the Face-ID approval screen (app/approve-device.tsx).
//
// Android uses its native FCM token. This deliberately avoids coupling the
// Play build to an Expo account: Firebase initializes from google-services.json
// and Convex sends through the authenticated FCM HTTP v1 API.

import * as Notifications from "expo-notifications";
import * as Device from "expo-device";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { router } from "expo-router";
import { Platform } from "react-native";
import { getConvexSiteUrlSync as getConvexSiteUrl } from "./backendConfig";
import { appLog } from "./logger";
import { mobileRuntimeIdentity } from "./appVersion";

const INSTALL_ID_KEY = "@yaver/push_install_id";
const ANDROID_TOKEN_RETRY_DELAYS_MS = [0, 5_000, 15_000, 60_000] as const;
let registrationInFlight: Promise<void> | null = null;

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
async function registerForAuthPushOnce(token: string): Promise<void> {
  if (!token) return;
  if (!Device.isDevice) return; // simulators can't receive push
  let { status } = await Notifications.getPermissionsAsync();
  if (status !== "granted") {
    status = (await Notifications.requestPermissionsAsync()).status;
  }
  if (status !== "granted") return;

  let pushToken: string;
  let transport: "expo" | "fcm";
  if (Platform.OS === "android") {
    const nativeToken = await Notifications.getDevicePushTokenAsync();
    pushToken = String(nativeToken?.data || "").trim();
    transport = "fcm";
  } else {
    pushToken = (await Notifications.getExpoPushTokenAsync()).data;
    transport = "expo";
  }
  if (!pushToken) throw new Error("push provider returned an empty token");

  const installId = await getInstallId();
  const response = await fetch(`${getConvexSiteUrl()}/push/register`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ installId, pushToken, transport, platform: Platform.OS, ...mobileRuntimeIdentity() }),
  });
  if (!response.ok) throw new Error(`backend returned HTTP ${response.status}`);
  appLog("info", "[push] registered device-auth push token");
}

/** Firebase can return SERVICE_NOT_AVAILABLE while Play Services or a newly
 * enabled project settles. Retry in-process so a transient startup edge does
 * not silently disable background task notifications until the next launch. */
export async function registerForAuthPush(token: string): Promise<void> {
  if (!token) return;
  if (registrationInFlight) return registrationInFlight;
  registrationInFlight = (async () => {
    const delays = Platform.OS === "android" ? ANDROID_TOKEN_RETRY_DELAYS_MS : [0] as const;
    let lastError: unknown;
    for (let attempt = 0; attempt < delays.length; attempt += 1) {
      const delay = delays[attempt];
      if (delay > 0) await new Promise((resolve) => setTimeout(resolve, delay));
      try {
        await registerForAuthPushOnce(token);
        return;
      } catch (error) {
        lastError = error;
        if (attempt + 1 < delays.length) {
          appLog("info", `[push] token unavailable; retrying (${attempt + 1}/${delays.length - 1})`);
        }
      }
    }
    appLog("warn", `[push] register failed after retries: ${lastError}`);
  })().finally(() => {
    registrationInFlight = null;
  });
  return registrationInFlight;
}

/** Persist token rotations immediately; FCM tokens are routing identifiers,
 * not stable device identities. */
export function installPushTokenRefreshListener(token: string): () => void {
  if (!token || typeof Notifications.addPushTokenListener !== "function") return () => {};
  const subscription = Notifications.addPushTokenListener(() => {
    void registerForAuthPush(token);
  });
  return () => subscription.remove();
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
