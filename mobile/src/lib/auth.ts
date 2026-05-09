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

export type OAuthProvider = "google" | "microsoft" | "apple" | "github" | "gitlab";

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
  // Mirror into UserDefaults so guest bundles loaded inside Yaver can
  // read the user's bearer via NativeModules.YaverInfo and skip their
  // own login. Best-effort — module is iOS-only, no-ops elsewhere.
  try {
    const { NativeModules } = require("react-native");
    NativeModules.YaverInfo?.setInheritedAuth?.(token, "", "");
  } catch {}
}

export async function clearToken(): Promise<void> {
  await SecureStore.deleteItemAsync(TOKEN_KEY);
  await SecureStore.deleteItemAsync(USER_KEY);
  try {
    const { NativeModules } = require("react-native");
    NativeModules.YaverInfo?.clearInheritedAuth?.();
  } catch {}
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

/**
 * Detailed result of a token validation call. Distinguishes between
 * "server actually said this token is invalid" (kind=invalid → caller
 * should log out) and "we could not reach the server" (kind=networkError
 * → caller should keep the cached session and retry later). The old
 * null-returning `validateToken` collapsed both into the same signal
 * which caused spurious logouts on brief network hiccups.
 */
export type ValidationResult =
  | { kind: "valid"; user: User }
  | { kind: "invalid" }
  | { kind: "networkError" };

export async function validateTokenDetailed(token: string): Promise<ValidationResult> {
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
    if (response.status === 401 || response.status === 403) {
      return { kind: "invalid" };
    }
    if (!response.ok) {
      // 5xx / unexpected — treat as transient, do NOT nuke the session.
      return { kind: "networkError" };
    }
    const data = await response.json();
    const u = data.user;
    const user: User = {
      id: u.userId ?? u.id,
      email: u.email,
      name: u.fullName ?? u.name,
      provider: u.provider,
      avatarUrl: u.avatarUrl,
      surveyCompleted: u.surveyCompleted ?? false,
    };
    return { kind: "valid", user };
  } catch {
    return { kind: "networkError" };
  }
}

/** Legacy wrapper — returns the user on success, null on invalid OR
 *  network error. Prefer `validateTokenDetailed` in new code. */
export async function validateToken(token: string): Promise<User | null> {
  const result = await validateTokenDetailed(token);
  return result.kind === "valid" ? result.user : null;
}

/**
 * Result of a refresh call.
 *  - `ok: false`                    → server said the token is revoked/expired; log out
 *  - `ok: true, networkError: true` → couldn't reach the server; keep cached token
 *  - `ok: true, newToken: "..."`    → server rotated the token; persist it
 *  - `ok: true`                     → extended; no action needed
 */
export interface RefreshResult {
  ok: boolean;
  newToken?: string;
  networkError?: boolean;
}

/**
 * Refresh the session token — extends expiry by 30 days. Opts in to
 * server-side token rotation (`X-Yaver-Rotate-Token: 1`); when the
 * backend rotates, we surface the new token so the caller can persist
 * it. Matches the Go agent's behavior (see `desktop/agent/auth.go`).
 */
export async function refreshToken(token: string): Promise<RefreshResult> {
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5_000);
    const response = await fetch(`${getConvexSiteUrl()}/auth/refresh`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        "X-Yaver-Rotate-Token": "1",
      },
      signal: controller.signal,
    });
    clearTimeout(timeout);
    if (response.status === 401 || response.status === 403) {
      return { ok: false };
    }
    if (!response.ok) {
      return { ok: true, networkError: true };
    }
    try {
      const body = await response.json();
      if (
        body?.rotated === true &&
        typeof body.token === "string" &&
        body.token.length > 0
      ) {
        return { ok: true, newToken: body.token };
      }
    } catch {
      // Older backend without JSON body — no rotation; keep cached token.
    }
    return { ok: true };
  } catch {
    return { ok: true, networkError: true };
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
  openAiApiKey?: string;
  glmApiKey?: string;
  anthropicApiKey?: string;
  mobileCodingProvider?: "openai" | "glm";
  ttsEnabled?: boolean;
  verbosity?: number; // 0-10: response detail level
  keyStorage?: KeyStorage; // "local" = device Keychain only, "cloud" = sync to Convex
  /** Preferred device for auto-connect when user has multiple machines.
   * Send `null` to clear; omit to leave untouched. Single-device users
   * auto-connect regardless of this field. */
  primaryDeviceId?: string | null;
  /** Optional secondary elevated device. When primary is offline, the
   * mobile auto-connect falls back to secondary before showing the
   * picker. Same semantics as primaryDeviceId on the wire. */
  secondaryDeviceId?: string | null;
  /** Per-device primary coding agent + optional model hint. The full
   * list is stored on the server as primaryRunnerByDevice; mutations
   * send a single-entry patch via this field so we never round-trip
   * the whole array. runnerId=null clears the entry. model=null
   * clears just the model (runner stays); model=undefined leaves the
   * existing model alone. */
  primaryRunnerForDevice?: {
    deviceId: string;
    runnerId: string | null;
    model?: string | null;
    mode?: string | null;
    provider?: string | null;
  };
  /** Read-only: full per-device runner map populated by the server on
   * GET /settings. Clients should not write this directly — write via
   * primaryRunnerForDevice instead. */
  primaryRunnerByDevice?: Array<{ deviceId: string; runnerId: string; model?: string; mode?: string; provider?: string }>;
}

// ── Local secret storage (iOS Keychain / Android SecureStore) ───────
// All sensitive keys can be stored locally instead of syncing to Convex.

const LOCAL_KEY_PREFIX = "yaver_key_";

/** Known local key names */
export const LOCAL_KEYS = {
  speechApiKey: `${LOCAL_KEY_PREFIX}speech`,
  openAiApiKey: `${LOCAL_KEY_PREFIX}openai_api_key`,
  glmApiKey: `${LOCAL_KEY_PREFIX}glm_api_key`,
  anthropicApiKey: `${LOCAL_KEY_PREFIX}anthropic_api_key`,
  figmaAccessToken: `${LOCAL_KEY_PREFIX}figma_access_token`,
  mobileCodingProvider: `${LOCAL_KEY_PREFIX}mobile_coding_provider`,
  relayPassword: `${LOCAL_KEY_PREFIX}relay_password`,
  relayUrl: `${LOCAL_KEY_PREFIX}relay_url`,
  tunnelUrl: `${LOCAL_KEY_PREFIX}tunnel_url`,
  bootstrapSecret: `${LOCAL_KEY_PREFIX}bootstrap_secret`,
  // Optional managed-cloud auth override for legacy/shared-tenant paths.
  // Kept in the device keychain — never synced.
  yaverCloudToken: `${LOCAL_KEY_PREFIX}yaver_cloud_token`,
  selfHostedBaseUrl: `${LOCAL_KEY_PREFIX}self_hosted_base_url`,
  selfHostedAuthToken: `${LOCAL_KEY_PREFIX}self_hosted_auth_token`,
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

// ── Account linking / unlink / merge ──────────────────────────────────
// These helpers mirror what web SettingsView does so the mobile Settings
// screen can list providers, add Google/Apple/Microsoft to the current
// account, remove one (refusing if it's the last), and kick off a manual
// account merge. All endpoints require the user's session bearer token.

export interface AuthIdentity {
  provider: "google" | "microsoft" | "apple" | "github" | "gitlab" | "email";
  email: string | null;
  isPrimary: boolean;
  createdAt?: number;
  lastUsedAt?: number;
}

export async function listAuthIdentities(token: string): Promise<AuthIdentity[]> {
  try {
    const res = await fetch(`${getConvexSiteUrl()}/auth/providers`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return [];
    const data = await res.json();
    return (data.identities || []) as AuthIdentity[];
  } catch {
    return [];
  }
}

/**
 * Start a linking intent for an additional OAuth provider. Returns the
 * browser URL the caller should open (we return it rather than calling
 * Linking.openURL here so the screen can wrap it with Platform-specific UX).
 */
export async function startLinkIntent(
  token: string,
  provider: OAuthProvider,
): Promise<{ url: string; linkToken: string }> {
  const res = await fetch(`${getConvexSiteUrl()}/auth/oauth-link/start`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ provider, client: "mobile", returnTo: "/dashboard" }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok || !data?.token) {
    throw new Error(data?.error || "Failed to start link");
  }
  const params = new URLSearchParams({
    client: "mobile",
    intent: "link",
    linkToken: data.token,
    return: "/dashboard",
  });
  return {
    url: `${getWebBaseUrl()}/api/auth/oauth/${provider}?${params.toString()}`,
    linkToken: data.token,
  };
}

/**
 * Remove an OAuth provider from the current account. Throws with a
 * user-visible message when the backend refuses (e.g., it's the only
 * linked provider).
 */
export async function unlinkProvider(
  token: string,
  provider: AuthIdentity["provider"],
): Promise<void> {
  const res = await fetch(
    `${getConvexSiteUrl()}/auth/oauth-link/${encodeURIComponent(provider)}`,
    {
      method: "DELETE",
      headers: { Authorization: `Bearer ${token}` },
    },
  );
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Failed to unlink ${provider}`);
  }
}

export interface MergeIntent {
  mergeToken: string;
  approvalUrl: string;
  expiresAt: number;
  targetEmail: string;
}

/** Start a manual merge intent. The target of the merge is the currently signed-in account. */
export async function startMergeIntent(token: string): Promise<MergeIntent> {
  const res = await fetch(`${getConvexSiteUrl()}/auth/account/merge/start`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ client: "mobile" }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok || !data?.mergeToken) {
    throw new Error(data?.error || "Failed to start merge");
  }
  return {
    mergeToken: data.mergeToken,
    approvalUrl: `${getWebBaseUrl()}/account/merge?token=${encodeURIComponent(data.mergeToken)}`,
    expiresAt: data.expiresAt,
    targetEmail: data.targetEmail,
  };
}

/** Cancel a pending merge intent (target-side). */
export async function cancelMergeIntent(token: string, mergeToken: string): Promise<void> {
  await fetch(`${getConvexSiteUrl()}/auth/account/merge/cancel`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ mergeToken }),
  }).catch(() => undefined);
}

/** Poll the public status of a merge intent (no auth required). */
export async function getMergeIntentStatus(
  mergeToken: string,
): Promise<"pending" | "completed" | "cancelled" | "expired" | "unknown"> {
  try {
    const res = await fetch(
      `${getConvexSiteUrl()}/auth/account/merge/status?token=${encodeURIComponent(mergeToken)}`,
    );
    if (!res.ok) return "unknown";
    const data = await res.json();
    return data.status as any;
  } catch {
    return "unknown";
  }
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
