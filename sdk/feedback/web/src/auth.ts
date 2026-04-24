/**
 * Authentication API for `yaver-feedback-web`.
 *
 * Mirrors `yaver-feedback-react-native` 0.6+ but adapted for the browser:
 *
 *   - Popup OAuth (Google / GitHub / GitLab / Microsoft / Apple) via
 *     `window.open` to `https://yaver.io/api/auth/oauth/<provider>?client=sdk`
 *     and a `postMessage` callback page that delivers the issued session token
 *     back to the opener window. No deep links, no codes, no leaving the page.
 *   - Email / password sign-up + login (no 2FA — accounts with TOTP are
 *     directed to OAuth, which completes the second factor on yaver.io).
 *   - Token validation against the same Convex `/auth/validate` endpoint.
 *   - `/devices/list` → owned + shared (guest) remote dev machines.
 *
 * Token persistence uses `localStorage`. There is no native browser equivalent
 * of iOS Keychain, so the consumer is expected to scope the SDK to dev/staging
 * builds (this is already the case — `init()` defaults `enabled` to
 * `NODE_ENV === 'development'`).
 *
 * Browser-only — do not import from a Node bundle.
 */

const TOKEN_KEY = 'yaver_feedback_auth_token';
const USER_KEY = 'yaver_feedback_user';
const DEVICE_KEY = 'yaver_feedback_selected_device';

export const DEFAULT_CONVEX_SITE_URL =
  'https://perceptive-minnow-557.eu-west-1.convex.site';
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

// ─── Token persistence (localStorage) ─────────────────────────────────

function safeStorage(): Storage | null {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return null;
    return window.localStorage;
  } catch {
    return null;
  }
}

export function getToken(): string | null {
  return safeStorage()?.getItem(TOKEN_KEY) ?? null;
}

export function saveToken(token: string): void {
  safeStorage()?.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  const s = safeStorage();
  if (!s) return;
  s.removeItem(TOKEN_KEY);
  s.removeItem(USER_KEY);
}

export function getUser(): User | null {
  const raw = safeStorage()?.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as User;
  } catch {
    return null;
  }
}

export function saveUser(user: User): void {
  safeStorage()?.setItem(USER_KEY, JSON.stringify(user));
}

export function getSelectedDeviceId(): string | null {
  return safeStorage()?.getItem(DEVICE_KEY) ?? null;
}

export function saveSelectedDeviceId(deviceId: string): void {
  safeStorage()?.setItem(DEVICE_KEY, deviceId);
}

export function clearSelectedDeviceId(): void {
  safeStorage()?.removeItem(DEVICE_KEY);
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

// ─── Popup OAuth (Google / GitHub / GitLab / Microsoft / Apple) ───────

/**
 * Open `https://yaver.io/api/auth/oauth/<provider>?client=sdk&origin=<host-origin>` in a popup
 * window. The web callback (`/auth/sdk-callback`) calls
 * `window.opener.postMessage({ type: 'yaver-feedback-auth', token })` and
 * closes the popup. Resolves with the session token.
 *
 * Throws `cancelled` if the user closes the popup before completing sign-in,
 * or `popup_blocked` if the browser blocked the popup.
 */
export function signInWithOAuth(
  provider: OAuthProvider,
  opts: { popupName?: string; width?: number; height?: number } = {},
): Promise<{ token: string }> {
  if (typeof window === 'undefined') {
    return Promise.reject(new Error('signInWithOAuth requires a browser'));
  }

  const width = opts.width ?? 480;
  const height = opts.height ?? 640;
  const left = window.screenX + (window.outerWidth - width) / 2;
  const top = window.screenY + (window.outerHeight - height) / 2;

  const params = new URLSearchParams({
    client: 'sdk',
    origin: window.location.origin,
  });
  const authUrl = `${webBaseUrl}/api/auth/oauth/${provider}?${params.toString()}`;

  const popup = window.open(
    authUrl,
    opts.popupName ?? 'yaver-feedback-auth',
    `width=${width},height=${height},left=${left},top=${top},popup=1`,
  );

  if (!popup) {
    return Promise.reject(new Error('popup_blocked'));
  }

  return new Promise<{ token: string }>((resolve, reject) => {
    const expectedOrigin = new URL(webBaseUrl).origin;
    let settled = false;

    const cleanup = () => {
      window.removeEventListener('message', onMessage);
      clearInterval(closeWatcher);
    };

    const onMessage = (event: MessageEvent) => {
      if (event.origin !== expectedOrigin) return;
      if (event.source !== popup) return;
      const data = event.data as { type?: string; token?: string; error?: string } | null;
      if (!data || data.type !== 'yaver-feedback-auth') return;
      settled = true;
      cleanup();
      try {
        popup.close();
      } catch {
        // popup may already be closed
      }
      if (data.error) {
        reject(new Error(data.error));
      } else if (data.token) {
        resolve({ token: data.token });
      } else {
        reject(new Error('Auth callback did not include a token'));
      }
    };

    const closeWatcher = setInterval(() => {
      if (popup.closed && !settled) {
        cleanup();
        reject(new Error('cancelled'));
      }
    }, 500);

    window.addEventListener('message', onMessage);
  });
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
): Promise<{ token: string; userId: string }> {
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

export async function listReachableDevices(token: string): Promise<DeviceList> {
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
