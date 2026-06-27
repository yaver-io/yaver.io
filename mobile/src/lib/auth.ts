import * as SecureStore from "expo-secure-store";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { getConvexSiteUrlSync, getWebBaseUrlSync } from "./backendConfig";
import type { OptionalMoreToolId } from "./moreOptionalTools";

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
  // emailVerified gates email-keyed OAuth auto-linking on the backend.
  // Settings UI surfaces a "Verify your email to link other sign-in
  // methods" banner when this is false. OAuth signup users are
  // verified-by-construction; email + passkey signups start unverified.
  emailVerified?: boolean;
  // Server-computed owner flag (ownerAllowlist). Gates owner-only experimental
  // hardware cells in the More menu; no owner identity ships in the app bundle.
  isOwner?: boolean;
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
  | { kind: "networkError"; detail?: string };

export async function validateTokenDetailed(token: string): Promise<ValidationResult> {
  // De-dupe concurrent validations of the same token. On Android,
  // an OAuth deep link triggers THREE login() call sites in parallel
  // — Linking listener on the login screen, the
  // WebBrowser.openAuthSessionAsync promise resolution, and the
  // expo-router mount of /oauth-callback — each calling
  // validateTokenDetailed independently. The duplicate fetches flood
  // RN-Android's network bridge and all three wedge for tens of
  // seconds. iOS only fires one path (the in-process
  // ASWebAuthenticationSession resolution) so it never noticed.
  // Returning the same in-flight promise to all three callers is the
  // real fix; the earlier retry/XHR bandages were treating symptoms
  // of the race rather than the race itself.
  const cached = inFlightValidations.get(token);
  if (cached) return cached;

  const inflight = (async (): Promise<ValidationResult> => {
    try {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 10_000);
      // Cache-bust: a unique query param plus no-store + Connection:
      // close make OkHttp use a fresh TCP socket instead of reaching
      // for one in its pool. Pooled sockets go stale across the
      // background-during-OAuth window on RN-Android, which is what
      // turned this fetch into a multi-second hang. Cost: one extra
      // TLS handshake (~50 ms on this device).
      const url = `${getConvexSiteUrl()}/auth/validate?_=${Date.now()}`;
      const response = await fetch(url, {
        method: "GET",
        headers: {
          Authorization: `Bearer ${token}`,
          "Cache-Control": "no-cache, no-store",
          Connection: "close",
        },
        signal: controller.signal,
      });
      clearTimeout(timeout);
      if (response.status === 401 || response.status === 403) {
        return { kind: "invalid" };
      }
      if (!response.ok) {
        return { kind: "networkError", detail: `HTTP ${response.status}` };
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
        emailVerified: u.emailVerified === true,
        isOwner: u.isOwner === true,
      };
      return { kind: "valid", user };
    } catch (e) {
      const detail = e instanceof Error ? e.message : String(e);
      return { kind: "networkError", detail };
    } finally {
      // Clear the cache entry after a short grace window so a manual
      // retry from the user (e.g. tapping "Try again") still goes
      // through. Keeping it forever would let a single bad result
      // freeze the auth flow.
      setTimeout(() => inFlightValidations.delete(token), 500);
    }
  })();

  inFlightValidations.set(token, inflight);
  return inflight;
}

// inFlightValidations holds Promise<ValidationResult> per token so
// concurrent validateTokenDetailed calls coalesce to a single fetch.
// Cleared 500 ms after each promise settles. See the comment in
// validateTokenDetailed for why this matters on Android.
const inFlightValidations = new Map<string, Promise<ValidationResult>>();

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

// Single-flight: multiple independent triggers (app restore, app
// resume, periodic) can call refreshToken concurrently. Rotation is
// destructive — two refreshes racing on the same token means the
// second presents an already-rotated (dead) token and the device gets
// stranded → blanket 401. Coalesce concurrent calls for the same token
// to ONE network round-trip so every caller observes the same result.
const inflightRefreshes = new Map<string, Promise<RefreshResult>>();

export function refreshToken(token: string): Promise<RefreshResult> {
  const existing = inflightRefreshes.get(token);
  if (existing) return existing;
  const p = doRefreshToken(token).finally(() => {
    inflightRefreshes.delete(token);
  });
  inflightRefreshes.set(token, p);
  return p;
}

/**
 * Refresh the session token — extends expiry by 30 days. Opts in to
 * server-side token rotation (`X-Yaver-Rotate-Token: 1`); when the
 * backend rotates, we surface the new token so the caller can persist
 * it. Matches the Go agent's behavior (see `desktop/agent/auth.go`).
 */
async function doRefreshToken(token: string): Promise<RefreshResult> {
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
  return getWebBaseUrlSync();
}

import { CONVEX_SITE_URL } from "./constants";
export { CONVEX_SITE_URL };

export function getConvexSiteUrl(): string {
  return getConvexSiteUrlSync();
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
  ttsProvider?: TtsProvider;
  openAiApiKey?: string;
  glmApiKey?: string;
  anthropicApiKey?: string;
  mobileCodingProvider?: "openai" | "glm";
  ttsEnabled?: boolean;
  ttsTaskMode?: boolean; // run tasks in TTS mode: agent leads replies with a spoken-style summary (text only)
  verbosity?: number; // 0-10: response detail level
  keyStorage?: KeyStorage; // "local" = device Keychain only, "cloud" = sync to Convex
  /** When true, the mobile tasks `+` button opens a device + agent
   *  picker before the compose modal. Stored on the user record so it
   *  roams across phones / re-installs. Default: undefined → off. */
  multiTargetMode?: boolean;
  /** Rare/specialized More-tab tools the user explicitly opted into.
   *  Omitted/empty means the default More menu stays focused. */
  moreOptionalTools?: OptionalMoreToolId[];
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
  // Git provider PATs for pushing/cloning phone-local sandbox repos, directly
  // from the phone (no dev box). GitHub kept under its original key for compat.
  githubToken: `${LOCAL_KEY_PREFIX}github_token`,
  gitlabToken: `${LOCAL_KEY_PREFIX}gitlab_token`,
  bitbucketToken: `${LOCAL_KEY_PREFIX}bitbucket_token`,
  // Generic/self-hosted git: JSON { host, username, token }.
  gitGenericConfig: `${LOCAL_KEY_PREFIX}git_generic_config`,
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
  // BYO Hetzner Cloud API token for phone-DIRECT box management +
  // provisioning (no paired agent needed). Lives ONLY in the device
  // keychain; the phone calls api.hetzner.cloud directly, so it never
  // transits Convex or any relay — same on-device-only posture as the
  // BYO API keys above.
  hetznerToken: `${LOCAL_KEY_PREFIX}hetzner_token`,
  // Yaver Premium MANAGED coding: when "1", the agentic coding loop talks to
  // the Yaver Gateway (captive OpenRouter) authed by the user's session token
  // instead of a BYO model key — Yaver holds the upstream key, meters tokens
  // into the prepaid wallet. Off by default (free/BYO path unchanged).
  managedCoding: `${LOCAL_KEY_PREFIX}managed_coding`,
  // Device-local override for the gateway origin, used to point at the
  // deployed Worker for testing before /api/mobile-config advertises it.
  gatewayUrl: `${LOCAL_KEY_PREFIX}gateway_url`,
  // Scoped ygw_ inference token for the managed/beta lane (keyless GLM via the
  // gateway). Minted server-side for beta users; the raw upstream key never
  // reaches the device. Used by the sandbox generation when inferenceMode=managed.
  managedInferenceToken: `${LOCAL_KEY_PREFIX}managed_inference_token`,
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

/** Whether Yaver Premium managed coding (gateway-routed inference, wallet-
 *  metered) is enabled on this device. Defaults false → free/BYO path. */
export async function getManagedCodingEnabled(): Promise<boolean> {
  return (await getLocalSecret(LOCAL_KEYS.managedCoding)) === "1";
}

export async function setManagedCodingEnabled(on: boolean): Promise<void> {
  if (on) await saveLocalSecret(LOCAL_KEYS.managedCoding, "1");
  else await deleteLocalSecret(LOCAL_KEYS.managedCoding);
}

export type SpeechProvider = "on-device" | "openai" | "openrouter" | "deepgram" | "assemblyai";
export type TtsProvider = "device" | "openai" | "openrouter" | "cartesia";

/**
 * Speech config is LOCAL ONLY by product decision — neither the audio,
 * the transcripts, nor the provider/key/model selection are ever sent
 * to Convex. These SecureStore-only helpers are the single source of
 * truth so no caller can accidentally route speech config through
 * saveUserSettings (which syncs to the cloud).
 */
export interface LocalSpeechConfig {
  sttProvider: SpeechProvider;
  sttModel: string;
  ttsProvider: TtsProvider;
  ttsModel: string;
  ttsVoice: string;
  apiKey: string;
}

const SPEECH_LK = {
  sttProvider: `${LOCAL_KEY_PREFIX}speech_stt_provider`,
  sttModel: `${LOCAL_KEY_PREFIX}speech_stt_model`,
  ttsProvider: `${LOCAL_KEY_PREFIX}speech_tts_provider`,
  ttsModel: `${LOCAL_KEY_PREFIX}speech_tts_model`,
  ttsVoice: `${LOCAL_KEY_PREFIX}speech_tts_voice`,
} as const;

export async function loadLocalSpeechConfig(): Promise<Partial<LocalSpeechConfig>> {
  const [sttProvider, sttModel, ttsProvider, ttsModel, ttsVoice, apiKey] = await Promise.all([
    getLocalSecret(SPEECH_LK.sttProvider),
    getLocalSecret(SPEECH_LK.sttModel),
    getLocalSecret(SPEECH_LK.ttsProvider),
    getLocalSecret(SPEECH_LK.ttsModel),
    getLocalSecret(SPEECH_LK.ttsVoice),
    getLocalSecret(LOCAL_KEYS.speechApiKey),
  ]);
  return {
    sttProvider: (sttProvider as SpeechProvider) || undefined,
    sttModel: sttModel || undefined,
    ttsProvider: (ttsProvider as TtsProvider) || undefined,
    ttsModel: ttsModel || undefined,
    ttsVoice: ttsVoice || undefined,
    apiKey: apiKey || undefined,
  };
}

export async function saveLocalSpeechConfig(cfg: LocalSpeechConfig): Promise<void> {
  await Promise.all([
    saveLocalSecret(SPEECH_LK.sttProvider, cfg.sttProvider),
    saveLocalSecret(SPEECH_LK.sttModel, cfg.sttModel),
    saveLocalSecret(SPEECH_LK.ttsProvider, cfg.ttsProvider),
    saveLocalSecret(SPEECH_LK.ttsModel, cfg.ttsModel),
    saveLocalSecret(SPEECH_LK.ttsVoice, cfg.ttsVoice),
    cfg.apiKey
      ? saveLocalSecret(LOCAL_KEYS.speechApiKey, cfg.apiKey)
      : deleteLocalSecret(LOCAL_KEYS.speechApiKey),
  ]);
}

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
  // Speech credentials are local-only. Keep API keys in SecureStore or
  // the agent vault-backed /voice/config path; never send them to Convex.
  const safeSettings = { ...settings };
  delete safeSettings.speechApiKey;
  await fetch(`${getConvexSiteUrl()}/settings`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify(safeSettings),
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
