import * as SecureStore from "expo-secure-store";
import AsyncStorage from "@react-native-async-storage/async-storage";

const TOKEN_KEY = "yaver_auth_token";
const USER_KEY = "yaver_user";
const INSTALLED_FLAG = "yaver_installed";

/**
 * On iOS, Keychain data survives app uninstall/reinstall.
 * AsyncStorage (backed by NSUserDefaults/files) does NOT survive uninstall.
 * So we use an AsyncStorage flag to detect fresh installs and wipe stale Keychain tokens.
 */
export async function clearKeychainIfFreshInstall(): Promise<void> {
  try {
    const installed = await AsyncStorage.getItem(INSTALLED_FLAG);
    if (!installed) {
      // Fresh install — wipe any leftover Keychain data
      await SecureStore.deleteItemAsync(TOKEN_KEY);
      await SecureStore.deleteItemAsync(USER_KEY);
      await AsyncStorage.setItem(INSTALLED_FLAG, "1");
    }
  } catch {
    // Best-effort
  }
}

export type OAuthProvider = "google" | "microsoft" | "apple";

export interface User {
  id: string;
  email: string;
  name: string;
  provider?: string;
  avatarUrl?: string;
  surveyCompleted?: boolean;
}

export async function getToken(): Promise<string | null> {
  try {
    return await SecureStore.getItemAsync(TOKEN_KEY);
  } catch {
    return null;
  }
}

export async function saveToken(token: string): Promise<void> {
  await SecureStore.setItemAsync(TOKEN_KEY, token);
}

export async function clearToken(): Promise<void> {
  await SecureStore.deleteItemAsync(TOKEN_KEY);
  await SecureStore.deleteItemAsync(USER_KEY);
}

export async function getUser(): Promise<User | null> {
  try {
    const raw = await SecureStore.getItemAsync(USER_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as User;
  } catch {
    return null;
  }
}

export async function saveUser(user: User): Promise<void> {
  await SecureStore.setItemAsync(USER_KEY, JSON.stringify(user));
}

export async function validateToken(token: string): Promise<User | null> {
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5_000);
    const response = await fetch(
      `${getConvexSiteUrl()}/auth/validate`,
      {
        method: "GET",
        headers: {
          Authorization: `Bearer ${token}`,
        },
        signal: controller.signal,
      }
    );
    clearTimeout(timeout);
    if (!response.ok) return null;
    const data = await response.json();
    const u = data.user;
    return {
      id: u.userId ?? u.id,
      email: u.email,
      name: u.fullName ?? u.name,
      provider: u.provider,
      avatarUrl: u.avatarUrl,
      surveyCompleted: u.surveyCompleted ?? false,
    } as User;
  } catch {
    return null;
  }
}

/**
 * Refresh the session token — extends expiry by 30 days.
 * Returns true if refreshed, false if expired/invalid (needs re-login).
 */
export async function refreshToken(token: string): Promise<boolean> {
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5_000);
    const response = await fetch(`${getConvexSiteUrl()}/auth/refresh`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
      signal: controller.signal,
    });
    clearTimeout(timeout);
    return response.ok;
  } catch {
    // Network error — assume token is still valid
    return true;
  }
}

export function getWebBaseUrl(): string {
  return "https://yaver.io";
}

import { CONVEX_SITE_URL } from "./constants";
export { CONVEX_SITE_URL };

export function getConvexSiteUrl(): string {
  return CONVEX_SITE_URL;
}

export interface DeviceMetric {
  timestamp: number;
  cpuPercent: number;
  memoryUsedMb: number;
  memoryTotalMb: number;
}

export interface DeviceEvent {
  event: "crash" | "restart" | "oom" | "started" | "stopped";
  details?: string;
  timestamp: number;
}

export async function getDeviceMetrics(token: string, deviceId: string): Promise<DeviceMetric[]> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/devices/metrics?deviceId=${deviceId}`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return [];
    const data = await res.json();
    return data.metrics || [];
  } catch {
    return [];
  }
}

export async function getDeviceEvents(token: string, deviceId: string): Promise<DeviceEvent[]> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/devices/events?deviceId=${deviceId}`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return [];
    const data = await res.json();
    return data.events || [];
  } catch {
    return [];
  }
}

export interface UsageDaySummary {
  date: string;
  totalSec: number;
  taskCount: number;
  runners: Record<string, number>;
}

export interface UsageSummary {
  entries: any[];
  daily: UsageDaySummary[];
  totalSeconds: number;
}

export async function getUsageSummary(token: string, since?: number): Promise<UsageSummary> {
  try {
    const params = since ? `?since=${since}` : "";
    const res = await fetch(`${getConvexSiteUrl()}/usage${params}`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return { entries: [], daily: [], totalSeconds: 0 };
    return await res.json();
  } catch {
    return { entries: [], daily: [], totalSeconds: 0 };
  }
}

export function getOAuthUrl(provider: OAuthProvider): string {
  const base = getWebBaseUrl();
  return `${base}/api/auth/oauth/${provider}?client=mobile`;
}

export async function signupWithEmail(
  fullName: string,
  email: string,
  password: string
): Promise<{ token: string; userId: string }> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/signup`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ fullName, email, password }),
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(data.error ?? "Signup failed");
  }
  return response.json();
}

/**
 * Result of a login attempt.
 *  - `session`: the user had no 2FA enabled → immediate session token.
 *  - `2fa`:    the user has 2FA enabled; caller must prompt for a 6-digit
 *              code and complete the flow via `verifyTotpChallenge()`.
 *
 * 2FA is strictly optional. Accounts without it enrolled always see the
 * `session` path. Recovery codes issued at enrollment are also accepted
 * by `verifyTotpChallenge`, so a user who loses their authenticator can
 * still sign in with a recovery code — we never kill recovery auth.
 */
export type LoginResult =
  | { kind: "session"; token: string; userId: string }
  | { kind: "2fa"; pendingToken: string };

export async function loginWithEmail(
  email: string,
  password: string
): Promise<LoginResult> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(data.error ?? "Login failed");
  }
  const data = await response.json();
  if (data?.requires2fa && typeof data.pendingToken === "string") {
    return { kind: "2fa", pendingToken: data.pendingToken };
  }
  return { kind: "session", token: data.token, userId: data.userId };
}

/**
 * Exchange a 2FA pending token + current TOTP code (or a recovery code)
 * for a session token. Accepts both forms — the backend falls through to
 * recovery-code matching if the 6-digit code fails.
 */
export async function verifyTotpChallenge(
  pendingToken: string,
  code: string
): Promise<{ token: string; userId: string }> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/verify-totp`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ pendingToken, code: code.trim() }),
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(data.error ?? "Two-factor verification failed");
  }
  return response.json();
}

/** Status of 2FA enrollment for the signed-in user. */
export async function fetchTotpStatus(
  token: string
): Promise<{ enabled: boolean; recoveryCodesRemaining: number }> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/totp/status`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!response.ok) {
    return { enabled: false, recoveryCodesRemaining: 0 };
  }
  const data = await response.json();
  return {
    enabled: Boolean(data?.enabled),
    recoveryCodesRemaining: Number(data?.recoveryCodesRemaining ?? 0),
  };
}

/** Start TOTP enrollment. Returns a fresh secret + otpauth URL for the
 *  authenticator app to scan. Enrollment does NOT enable 2FA yet — that
 *  requires confirming with a code via `confirmTotpEnrollment`. */
export async function beginTotpEnrollment(
  token: string
): Promise<{ secret: string; otpAuthUrl: string }> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/totp/setup`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(data.error ?? "Failed to start 2FA enrollment");
  }
  return response.json();
}

export async function confirmTotpEnrollment(
  token: string,
  code: string
): Promise<{ recoveryCodes: string[] }> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/totp/enable`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ code: code.trim() }),
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(data.error ?? "Two-factor enrollment failed");
  }
  return response.json();
}

export async function disableTotp(token: string, code: string): Promise<void> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/totp/disable`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ code: code.trim() }),
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(data.error ?? "Failed to disable 2FA");
  }
}

export async function changePassword(
  token: string,
  currentPassword: string,
  newPassword: string
): Promise<void> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/change-password`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ currentPassword, newPassword }),
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(data.error ?? "Password change failed");
  }
}

export async function requestPasswordReset(email: string): Promise<void> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/forgot-password`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email }),
  });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    throw new Error(data.error ?? "Password reset request failed");
  }
}

export async function updateProfile(
  token: string,
  data: { fullName?: string }
): Promise<void> {
  const response = await fetch(`${getConvexSiteUrl()}/auth/update-profile`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(data),
  });
  if (!response.ok) {
    const err = await response.json().catch(() => ({}));
    throw new Error(err.error ?? "Failed to update profile");
  }
}

export async function submitSurvey(
  token: string,
  data: {
    isDeveloper: boolean;
    fullName?: string;
    languages?: string[];
    experienceLevel?: string;
    role?: string;
    companySize?: string;
    useCase?: string;
  }
): Promise<void> {
  const response = await fetch(`${getConvexSiteUrl()}/survey/submit`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(data),
  });
  if (!response.ok) {
    const err = await response.json().catch(() => ({}));
    throw new Error(err.error ?? "Failed to submit survey");
  }
}

export async function getSurveyStatus(
  token: string
): Promise<{ completed: boolean }> {
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5_000);
    const response = await fetch(`${getConvexSiteUrl()}/survey`, {
      method: "GET",
      headers: {
        Authorization: `Bearer ${token}`,
      },
      signal: controller.signal,
    });
    clearTimeout(timeout);
    if (!response.ok) {
      return { completed: false };
    }
    return response.json();
  } catch {
    return { completed: false };
  }
}

export type KeyStorage = "local" | "cloud";

export interface UserSettings {
  forceRelay?: boolean;
  runnerId?: string;
  customRunnerCommand?: string;
  relayUrl?: string;
  relayPassword?: string;
  tunnelUrl?: string;
  speechProvider?: SpeechProvider;
  speechApiKey?: string;
  ttsEnabled?: boolean;
  verbosity?: number; // 0-10: response detail level
  keyStorage?: KeyStorage; // "local" = device Keychain only, "cloud" = sync to Convex
}

// ── Local secret storage (iOS Keychain / Android SecureStore) ───────
// All sensitive keys can be stored locally instead of syncing to Convex.

const LOCAL_KEY_PREFIX = "yaver_key_";

/** Known local key names */
export const LOCAL_KEYS = {
  speechApiKey: `${LOCAL_KEY_PREFIX}speech`,
  openAiApiKey: `${LOCAL_KEY_PREFIX}openai_api_key`,
  relayPassword: `${LOCAL_KEY_PREFIX}relay_password`,
  relayUrl: `${LOCAL_KEY_PREFIX}relay_url`,
  tunnelUrl: `${LOCAL_KEY_PREFIX}tunnel_url`,
  bootstrapSecret: `${LOCAL_KEY_PREFIX}bootstrap_secret`,
} as const;

export async function getLocalSecret(key: string): Promise<string | null> {
  try {
    return await SecureStore.getItemAsync(key);
  } catch {
    return null;
  }
}

export async function saveLocalSecret(key: string, value: string): Promise<void> {
  await SecureStore.setItemAsync(key, value);
}

export async function deleteLocalSecret(key: string): Promise<void> {
  await SecureStore.deleteItemAsync(key).catch(() => {});
}

/** Get the effective key storage preference (defaults to "local"). */
export async function getKeyStoragePreference(): Promise<KeyStorage> {
  try {
    const val = await SecureStore.getItemAsync(`${LOCAL_KEY_PREFIX}storage_pref`);
    return val === "cloud" ? "cloud" : "local";
  } catch {
    return "local";
  }
}

export async function saveKeyStoragePreference(pref: KeyStorage): Promise<void> {
  await SecureStore.setItemAsync(`${LOCAL_KEY_PREFIX}storage_pref`, pref);
}

export type SpeechProvider = "on-device" | "openai" | "deepgram" | "assemblyai";

export interface AiRunner {
  runnerId: string;
  name: string;
  command: string;
  description: string;
  outputMode: string;
  isDefault?: boolean;
  sortOrder: number;
}

export async function getAiRunners(): Promise<AiRunner[]> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/runners`);
    if (!res.ok) return [];
    const data = await res.json();
    return data.runners ?? data ?? [];
  } catch {
    return [];
  }
}

export async function getUserSettings(token: string): Promise<UserSettings> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/settings`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return {};
    const data = await res.json();
    return data.settings || {};
  } catch {
    return {};
  }
}

export async function saveUserSettings(token: string, settings: Partial<UserSettings>): Promise<void> {
  await fetch(`${getConvexSiteUrl()}/settings`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify(settings),
  }).catch(() => {});
}

export async function deleteAccount(): Promise<boolean> {
  const token = await getToken();
  if (!token) return false;

  try {
    const response = await fetch(`${getConvexSiteUrl()}/auth/delete-account`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
    });
    if (!response.ok) return false;
    await clearToken();
    return true;
  } catch {
    return false;
  }
}
