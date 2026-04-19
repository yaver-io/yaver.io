/**
 * Authentication + device/agent discovery API used by the Yaver Feedback SDK.
 *
 * This module is a trimmed SDK-local port of mobile/src/lib/auth.ts. It only
 * covers what the embedded login/machine-picker flow needs:
 *
 *   - Device-code login (`POST /auth/device-code` + `GET /auth/device-code/poll`)
 *     so users can sign in via any OAuth provider (apple/google/github/gitlab/
 *     microsoft) on yaver.io without requiring deep-link wiring in the host app.
 *   - Email / password sign-up + login (no 2FA flow — for SDK simplicity).
 *   - Token validation + refresh.
 *   - `/devices/list` → owned + shared (guest) remote dev machines.
 *
 * All calls target the public Yaver Convex site URL by default; callers may
 * override via `init()` config to point at staging.
 *
 * Token persistence uses `@react-native-async-storage/async-storage` (already
 * a peer dep). SecureStore is intentionally avoided to keep the SDK portable
 * to any RN host app.
 */

// AsyncStorage is an optional peer dep — degrade gracefully if missing.
let AsyncStorage: {
  getItem: (key: string) => Promise<string | null>;
  setItem: (key: string, value: string) => Promise<void>;
  removeItem: (key: string) => Promise<void>;
} | null = null;
try {
  AsyncStorage = require('@react-native-async-storage/async-storage').default;
} catch {
  // not installed — token persistence disabled, caller must pass authToken
}

const TOKEN_KEY = 'yaver_feedback_auth_token';
const USER_KEY = 'yaver_feedback_user';
const DEVICE_KEY = 'yaver_feedback_selected_device';

export const DEFAULT_CONVEX_SITE_URL =
  'https://shocking-echidna-394.eu-west-1.convex.site';
export const DEFAULT_WEB_BASE_URL = 'https://yaver.io';

let convexSiteUrl = DEFAULT_CONVEX_SITE_URL;
let webBaseUrl = DEFAULT_WEB_BASE_URL;

/** Override the Convex site URL + web base (staging vs prod). */
export function configureAuthEndpoints(opts: {
  convexSiteUrl?: string;
  webBaseUrl?: string;
}): void {
  if (opts.convexSiteUrl) convexSiteUrl = opts.convexSiteUrl;
  if (opts.webBaseUrl) webBaseUrl = opts.webBaseUrl;
}

export function getConvexSiteUrl(): string {
  return convexSiteUrl;
}
export function getWebBaseUrl(): string {
  return webBaseUrl;
}

export type OAuthProvider =
  | 'google'
  | 'microsoft'
  | 'apple'
  | 'github'
  | 'gitlab';

export interface User {
  id: string;
  email: string;
  name: string;
  provider?: string;
  avatarUrl?: string;
}

// ─── Token persistence ────────────────────────────────────────────────

export async function getToken(): Promise<string | null> {
  if (!AsyncStorage) return null;
  try {
    return await AsyncStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export async function saveToken(token: string): Promise<void> {
  if (!AsyncStorage) return;
  try {
    await AsyncStorage.setItem(TOKEN_KEY, token);
  } catch {
    // best effort
  }
}

export async function clearToken(): Promise<void> {
  if (!AsyncStorage) return;
  try {
    await AsyncStorage.removeItem(TOKEN_KEY);
    await AsyncStorage.removeItem(USER_KEY);
  } catch {
    // best effort
  }
}

export async function getUser(): Promise<User | null> {
  if (!AsyncStorage) return null;
  try {
    const raw = await AsyncStorage.getItem(USER_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as User;
  } catch {
    return null;
  }
}

export async function saveUser(user: User): Promise<void> {
  if (!AsyncStorage) return;
  try {
    await AsyncStorage.setItem(USER_KEY, JSON.stringify(user));
  } catch {
    // best effort
  }
}

export async function getSelectedDeviceId(): Promise<string | null> {
  if (!AsyncStorage) return null;
  try {
    return await AsyncStorage.getItem(DEVICE_KEY);
  } catch {
    return null;
  }
}

export async function saveSelectedDeviceId(deviceId: string): Promise<void> {
  if (!AsyncStorage) return;
  try {
    await AsyncStorage.setItem(DEVICE_KEY, deviceId);
  } catch {
    // best effort
  }
}

export async function clearSelectedDeviceId(): Promise<void> {
  if (!AsyncStorage) return;
  try {
    await AsyncStorage.removeItem(DEVICE_KEY);
  } catch {
    // best effort
  }
}

// ─── Token validation ──────────────────────────────────────────────────

export async function validateToken(token: string): Promise<User | null> {
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5_000);
    const res = await fetch(`${convexSiteUrl}/auth/validate`, {
      method: 'GET',
      headers: { Authorization: `Bearer ${token}` },
      signal: controller.signal,
    });
    clearTimeout(timeout);
    if (!res.ok) return null;
    const data = await res.json();
    const u = data.user;
    return {
      id: u.userId ?? u.id,
      email: u.email,
      name: u.fullName ?? u.name,
      provider: u.provider,
      avatarUrl: u.avatarUrl,
    };
  } catch {
    return null;
  }
}

// ─── Device-code flow (for OAuth via web) ─────────────────────────────

export interface DeviceCodeStart {
  userCode: string;
  deviceCode: string;
  expiresAt: number;
  verificationUrl: string;
}

/**
 * Start a device-code flow. The user opens `verificationUrl`, signs in with
 * any OAuth provider on yaver.io, and the SDK polls `pollDeviceCode` until
 * a session token is issued.
 */
export async function startDeviceCode(opts?: {
  machineName?: string;
  platform?: string;
  preferredProvider?: OAuthProvider;
}): Promise<DeviceCodeStart> {
  const res = await fetch(`${convexSiteUrl}/auth/device-code`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      machineName: opts?.machineName,
      platform: opts?.platform,
      preferredProvider: opts?.preferredProvider,
      environment: 'feedback-sdk',
    }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error ?? 'Failed to start device-code');
  }
  const data = await res.json();
  const params = new URLSearchParams({ code: data.userCode });
  if (opts?.preferredProvider) {
    params.set('preferredProvider', opts.preferredProvider);
  }
  return {
    userCode: data.userCode,
    deviceCode: data.deviceCode,
    expiresAt: data.expiresAt,
    verificationUrl: `${webBaseUrl}/auth/device?${params.toString()}`,
  };
}

export type DeviceCodePoll =
  | { status: 'pending' }
  | { status: 'authorized'; token: string }
  | { status: 'expired' };

export async function pollDeviceCode(
  deviceCode: string,
): Promise<DeviceCodePoll> {
  try {
    const res = await fetch(
      `${convexSiteUrl}/auth/device-code/poll?device_code=${encodeURIComponent(deviceCode)}`,
    );
    if (!res.ok) return { status: 'expired' };
    const data = await res.json();
    if (data.status === 'authorized' && typeof data.token === 'string') {
      return { status: 'authorized', token: data.token };
    }
    if (data.status === 'pending') return { status: 'pending' };
    return { status: 'expired' };
  } catch {
    return { status: 'pending' }; // network blip — let caller keep polling
  }
}

// ─── Email / password (no 2FA) ────────────────────────────────────────

export async function signupWithEmail(
  fullName: string,
  email: string,
  password: string,
): Promise<{ token: string; userId: string }> {
  const res = await fetch(`${convexSiteUrl}/auth/signup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ fullName, email, password }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error ?? 'Signup failed');
  }
  return res.json();
}

export async function loginWithEmail(
  email: string,
  password: string,
): Promise<{ token: string; userId: string; requires2fa?: boolean }> {
  const res = await fetch(`${convexSiteUrl}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error ?? 'Login failed');
  }
  const data = await res.json();
  if (data?.requires2fa) {
    // SDK login surface does not handle 2FA — direct the user to complete
    // sign-in through the web flow (device-code) which supports it.
    throw new Error(
      '2FA is enabled on this account. Sign in via the device-code flow instead.',
    );
  }
  return { token: data.token, userId: data.userId };
}

// ─── Devices (owned + shared) ─────────────────────────────────────────

export interface RemoteDevice {
  deviceId: string;
  name: string;
  platform: string;
  isOnline: boolean;
  needsAuth: boolean;
  runnerDown: boolean;
  lastHeartbeat: number;
  isGuest: boolean;
  hostName?: string;
  hostEmail?: string;
  accessScope: 'owner' | 'shared-scoped' | 'shared-legacy';
  quicHost: string;
  quicPort: number;
  publicKey?: string;
}

export interface DeviceList {
  owned: RemoteDevice[];
  shared: RemoteDevice[];
}

/**
 * Fetch the set of remote dev machines this user can reach. Splits into
 * owned (user is the host) vs shared (host invited them as a guest).
 */
export async function listReachableDevices(
  token: string,
): Promise<DeviceList> {
  try {
    const res = await fetch(`${convexSiteUrl}/devices/list`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return { owned: [], shared: [] };
    const data = await res.json();
    const all = (data.devices ?? []) as RemoteDevice[];
    return {
      owned: all.filter((d) => !d.isGuest),
      shared: all.filter((d) => d.isGuest),
    };
  } catch {
    return { owned: [], shared: [] };
  }
}
