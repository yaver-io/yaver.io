/**
 * Authentication + device/agent discovery API used by the Yaver Feedback SDK.
 *
 * Mirrors mobile/src/lib/auth.ts:
 *
 *   - Native Apple Sign-In (`POST /auth/apple-native`) on iOS via
 *     `expo-apple-authentication`.
 *   - In-app browser OAuth for Google/Microsoft/GitHub/GitLab via
 *     `expo-web-browser`'s `openAuthSessionAsync` — same callback URL
 *     (`yaver://oauth-callback`) the Yaver mobile app uses.
 *   - Email / password sign-up + login (no 2FA flow — for SDK simplicity).
 *   - Token validation.
 *   - `/devices/list` → owned + shared (guest) remote dev machines.
 *
 * Mobile-only. A web equivalent will ship as a separate `yaver-web-feedback`
 * package; do not import this module from a browser bundle.
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

// Optional peer deps used by native sign-in and in-app browser OAuth. When
// missing the SDK still works — Apple falls back to in-app browser OAuth, and
// providers without expo-web-browser surface a clear error.
type WebBrowserModule = {
  openAuthSessionAsync: (
    url: string,
    redirectUrl: string,
    options?: { showInRecents?: boolean; preferEphemeralSession?: boolean },
  ) => Promise<{ type: string; url?: string }>;
  maybeCompleteAuthSession: () => void;
};
type AppleAuthModule = {
  isAvailableAsync: () => Promise<boolean>;
  signInAsync: (opts: { requestedScopes: number[] }) => Promise<{
    identityToken: string | null;
    fullName?: { givenName?: string | null; familyName?: string | null } | null;
  }>;
  AppleAuthenticationScope: { FULL_NAME: number; EMAIL: number };
};

let WebBrowser: WebBrowserModule | null = null;
try {
  WebBrowser = require('expo-web-browser');
  WebBrowser?.maybeCompleteAuthSession();
} catch {
  // optional
}

let AppleAuth: AppleAuthModule | null = null;
try {
  AppleAuth = require('expo-apple-authentication');
} catch {
  // optional — Apple sign-in falls back to in-app browser OAuth
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

// ─── Native Apple Sign-In ─────────────────────────────────────────────

/**
 * Sign in with Apple using the native ASAuthorization flow. Requires
 * `expo-apple-authentication` installed and the host app's bundle to have
 * the "Sign in with Apple" capability enabled. iOS only.
 *
 * Throws `cancelled` if the user dismisses the sheet.
 */
export async function signInWithApple(): Promise<{ token: string; userId: string }> {
  if (!AppleAuth) {
    throw new Error(
      'expo-apple-authentication is not installed. Add it as a peer dep to enable native Apple Sign-In.',
    );
  }
  const available = await AppleAuth.isAvailableAsync();
  if (!available) {
    throw new Error('Apple Sign-In is not available on this device');
  }

  let credential;
  try {
    credential = await AppleAuth.signInAsync({
      requestedScopes: [
        AppleAuth.AppleAuthenticationScope.FULL_NAME,
        AppleAuth.AppleAuthenticationScope.EMAIL,
      ],
    });
  } catch (err) {
    if ((err as { code?: string } | null)?.code === 'ERR_REQUEST_CANCELED') {
      throw new Error('cancelled');
    }
    throw err;
  }

  if (!credential.identityToken) {
    throw new Error('Apple did not return an identity token');
  }

  const fullName =
    [credential.fullName?.givenName, credential.fullName?.familyName]
      .filter(Boolean)
      .join(' ') || undefined;

  const res = await fetch(`${convexSiteUrl}/auth/apple-native`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ identityToken: credential.identityToken, fullName }),
  });
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new Error(body || 'Apple sign-in failed');
  }
  const data = await res.json();
  return { token: data.token, userId: data.userId };
}

// ─── In-app browser OAuth (Google / GitHub / GitLab / Microsoft) ──────

/**
 * Default OAuth redirect — the same callback the Yaver mobile app uses
 * (`yaver://oauth-callback`). `WebBrowser.openAuthSessionAsync` intercepts
 * this redirect inside the auth session, so the host app does not need to
 * register the scheme on iOS. On Android, add an `<intent-filter>` for
 * `yaver://oauth-callback` in the host app's AndroidManifest.xml.
 */
export const DEFAULT_OAUTH_REDIRECT = 'yaver://oauth-callback';

/**
 * Sign in through the in-app browser via yaver.io. Opens
 * `https://yaver.io/api/auth/oauth/<provider>?client=mobile`, the user picks
 * an OAuth provider, and the web callback redirects back to
 * `yaver://oauth-callback?token=...` which `openAuthSessionAsync` captures
 * inside the auth session. No deep-link wiring required on iOS.
 *
 * Throws `cancelled` if the user dismisses the browser.
 */
export async function signInWithOAuth(
  provider: OAuthProvider,
  opts?: { redirectUrl?: string; preferEphemeralSession?: boolean },
): Promise<{ token: string }> {
  if (!WebBrowser) {
    throw new Error(
      'expo-web-browser is not installed. Add it as a peer dep to enable OAuth sign-in.',
    );
  }
  const redirectUrl = opts?.redirectUrl ?? DEFAULT_OAUTH_REDIRECT;
  const params = new URLSearchParams({ client: 'mobile' });
  const authUrl = `${webBaseUrl}/api/auth/oauth/${provider}?${params.toString()}`;

  const result = await WebBrowser.openAuthSessionAsync(authUrl, redirectUrl, {
    preferEphemeralSession: opts?.preferEphemeralSession ?? false,
    showInRecents: true,
  });

  if (result.type !== 'success' || !result.url) {
    throw new Error('cancelled');
  }

  let token: string | null = null;
  try {
    const parsed = new URL(result.url);
    token = parsed.searchParams.get('token');
  } catch {
    // best-effort fallback for odd schemes
    const match = result.url.match(/[?&]token=([^&]+)/);
    if (match) token = decodeURIComponent(match[1]);
  }

  if (!token) {
    throw new Error('OAuth callback did not include a token');
  }
  return { token };
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
    // SDK login surface does not handle 2FA — direct the user to sign in
    // through one of the OAuth providers, which complete 2FA on the web.
    throw new Error(
      '2FA is enabled on this account. Sign in with Apple/Google/GitHub/GitLab/Microsoft instead.',
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
