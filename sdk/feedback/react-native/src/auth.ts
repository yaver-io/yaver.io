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
 *   - `/devices/list` → owner remote dev machines.
 *
 * Mobile-only. A web equivalent will ship as a separate `yaver-web-feedback`
 * package; do not import this module from a browser bundle.
 */
import { agentHttpBase } from './_core/endpoints';

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

type SecureStoreModule = {
  getItemAsync: (key: string) => Promise<string | null>;
  setItemAsync: (key: string, value: string) => Promise<void>;
  deleteItemAsync: (key: string) => Promise<void>;
};

let SecureStore: SecureStoreModule | null = null;
try {
  SecureStore = require('expo-secure-store');
} catch {
  // Bare RN hosts may pass authToken for the current run. Native OAuth tokens
  // are never deliberately persisted to plaintext AsyncStorage.
}

function isNativeRuntime(): boolean {
  try {
    return require('react-native').Platform?.OS !== 'web';
  } catch {
    return false;
  }
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

// Source of truth: `shared/client-core/src/constants.ts`, mirrored to
// `./_core/constants` via `scripts/sync-client-core.sh`. Keeps the
// SDK, mobile app, and web dashboard on the same Convex deployment
// string at all times — previously this drifted and produced 403
// "invalid token" from the agent's authSDK middleware. See
// ARCHITECTURE_CLIENT_CORE.md.
import { CONVEX_SITE_URL, WEB_BASE_URL, OAUTH_REDIRECT } from './_core/constants';
export const DEFAULT_CONVEX_SITE_URL = CONVEX_SITE_URL;
export const DEFAULT_WEB_BASE_URL = WEB_BASE_URL;

let convexSiteUrl = DEFAULT_CONVEX_SITE_URL;
let webBaseUrl = DEFAULT_WEB_BASE_URL;
let strictNativeAuth = false;

/** Override the Convex site URL + web base (staging vs prod). */
export function configureAuthEndpoints(opts: {
  convexSiteUrl?: string;
  webBaseUrl?: string;
}): void {
  if (opts.convexSiteUrl) convexSiteUrl = opts.convexSiteUrl;
  if (opts.webBaseUrl) webBaseUrl = opts.webBaseUrl;
}

/**
 * Enable strict native auth: refuse any fallback that would redirect the
 * user to an external browser (Safari / Chrome) or show a device code.
 * See FeedbackConfig.strictNativeAuth for rationale.
 */
export function setStrictNativeAuth(enabled: boolean): void {
  strictNativeAuth = enabled;
}

export function isStrictNativeAuth(): boolean {
  return strictNativeAuth;
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
  if (isNativeRuntime() && SecureStore) {
    try {
      const secure = await SecureStore.getItemAsync(TOKEN_KEY);
      if (secure) return secure;

      // One-time migration for SDK releases that stored the OAuth bearer in
      // AsyncStorage. Move it before returning it so future launches use the
      // platform keychain/keystore only.
      const legacy = await AsyncStorage?.getItem(TOKEN_KEY) || null;
      if (legacy) {
        await SecureStore.setItemAsync(TOKEN_KEY, legacy);
        await AsyncStorage?.removeItem(TOKEN_KEY);
        return legacy;
      }
      return null;
    } catch {
      // A locked keychain is not a logout. Do not delete any cached state.
      return null;
    }
  }
  if (isNativeRuntime() || !AsyncStorage) return null;
  try {
    return await AsyncStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export async function saveToken(token: string): Promise<void> {
  if (isNativeRuntime() && SecureStore) {
    await SecureStore.setItemAsync(TOKEN_KEY, token);
    await AsyncStorage?.removeItem(TOKEN_KEY).catch(() => {});
    return;
  }
  if (isNativeRuntime() || !AsyncStorage) return;
  try {
    await AsyncStorage.setItem(TOKEN_KEY, token);
  } catch {
    // best effort
  }
}

export async function clearToken(): Promise<void> {
  await SecureStore?.deleteItemAsync(TOKEN_KEY).catch(() => {});
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

/** Verify that this full Yaver session owns the exact registered Dogfood app.
 * Any login is not enough, and installation-scoped SDK tokens return false. */
export async function getDogfoodAccountAccess(appId: string, token: string, installationId?: string): Promise<{
  authenticated: boolean;
  ownerAuthorized: boolean;
  accountAuthorized: boolean;
  installationAuthorized: boolean;
  controlPresentation?: 'auto' | 'minimized-y';
  gestureSupported?: boolean;
  gestureCapabilityReason?: string;
  controlOnboardingSeen?: boolean;
}> {
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5_000);
    const query = new URLSearchParams({ appId });
    if (installationId) query.set('installationId', installationId);
    const response = await fetch(`${convexSiteUrl}/dogfood/access?${query.toString()}`, {
      headers: { Authorization: `Bearer ${token}` },
      signal: controller.signal,
    });
    clearTimeout(timeout);
    if (!response.ok) return { authenticated: false, ownerAuthorized: false, accountAuthorized: false, installationAuthorized: false };
    const result = await response.json();
    return {
      authenticated: result?.authenticated === true,
      ownerAuthorized: result?.ownerAuthorized === true,
      accountAuthorized: result?.accountAuthorized === true,
      installationAuthorized: result?.installationAuthorized === true,
      controlPresentation: result?.controlPresentation === 'minimized-y' ? 'minimized-y'
        : result?.controlPresentation === 'auto' ? 'auto' : undefined,
      gestureSupported: typeof result?.gestureSupported === 'boolean' ? result.gestureSupported : undefined,
      gestureCapabilityReason: typeof result?.gestureCapabilityReason === 'string'
        ? result.gestureCapabilityReason
        : undefined,
      controlOnboardingSeen: result?.controlOnboardingSeen === true,
    };
  } catch {
    return { authenticated: false, ownerAuthorized: false, accountAuthorized: false, installationAuthorized: false };
  }
}

export async function setDogfoodControlPreference(input: {
  appId: string;
  installationId: string;
  token: string;
  presentation: 'auto' | 'minimized-y';
  gestureSupported: boolean;
  gestureCapabilityReason: string;
  gesturePlatform: string;
  controlOnboardingSeen?: boolean;
}): Promise<boolean> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 5_000);
  try {
    const response = await fetch(`${convexSiteUrl}/dogfood/control-preference`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${input.token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        appId: input.appId,
        installationId: input.installationId,
        presentation: input.presentation,
        gestureSupported: input.gestureSupported,
        gestureCapabilityReason: input.gestureCapabilityReason,
        gesturePlatform: input.gesturePlatform,
        controlOnboardingSeen: input.controlOnboardingSeen === true,
      }),
      signal: controller.signal,
    });
    return response.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(timeout);
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
export const DEFAULT_OAUTH_REDIRECT = OAUTH_REDIRECT;

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
    // In strictNativeAuth we hard-fail rather than letting the caller
    // fall back to any homegrown `Linking.openURL(…)` flow that would
    // leave the app for Safari. Without strict mode we still can't
    // proceed (no browser module available) so the behavior is the same
    // — just a clearer error message.
    throw new Error(
      'expo-web-browser is not installed. Add it as a peer dep to enable in-app OAuth sign-in.',
    );
  }
  const redirectUrl = opts?.redirectUrl ?? DEFAULT_OAUTH_REDIRECT;
  const params = new URLSearchParams({ client: 'mobile' });
  const authUrl = `${webBaseUrl}/api/auth/oauth/${provider}?${params.toString()}`;

  // In strict mode force ephemeral session (ASWebAuthenticationSession
  // with no shared cookie jar) so the OAuth dance is visibly native and
  // can never hand off to the user's default browser.
  const prefer =
    strictNativeAuth || opts?.preferEphemeralSession
      ? true
      : false;
  const result = await WebBrowser.openAuthSessionAsync(authUrl, redirectUrl, {
    preferEphemeralSession: prefer,
    showInRecents: !strictNativeAuth,
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

// ─── Owner devices ────────────────────────────────────────────────────

export interface RemoteDevice {
  deviceId: string;
  name: string;
  platform: string;
  isOnline: boolean;
  needsAuth: boolean;
  runnerDown: boolean;
  lastHeartbeat: number;
  quicHost: string;
  quicPort: number;
  /** Agent HTTP port — preferred over quicPort when present. */
  httpPort?: number;
  publicKey?: string;
  /** Hardware identifier — used for dedup across re-pair events. */
  hwid?: string;
  /**
   * Every LAN IP the agent reported in its last heartbeat. Useful on
   * multi-homed hosts — probing all of them in parallel is the same
   * trick the Yaver mobile app uses to "just work" on the same Wi-Fi.
   */
  localIps?: string[];
}

export interface DeviceList {
  owned: RemoteDevice[];
}

export interface DeviceReachability {
  reachable: boolean;
  url?: string;
}

/**
 * Fetch the signed-in owner's remote dev machines. Legacy non-owner rows are
 * discarded even if a stale backend still returns them.
 *
 * Collapses duplicate rows before splitting — Convex can return multiple
 * rows per physical machine after a re-pair or hostname change, and the
 * raw list used to render as "Kvancs-MacBook-Air.local ×3" in the picker.
 */
export async function listReachableDevices(
  token: string,
): Promise<DeviceList> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 10_000);
  try {
    const res = await fetch(`${convexSiteUrl}/devices/list`, {
      headers: { Authorization: `Bearer ${token}` },
      signal: controller.signal,
    });
    if (!res.ok) return { owned: [] };
    const data = await res.json();
    const raw = (data.devices ?? []) as any[];
    // Normalise Convex field names → SDK's RemoteDevice shape. The
    // backend returns `localIps`, sometimes the mobile-side mapping
    // surfaces `lanIps` — accept either so the field survives.
    const normalised: RemoteDevice[] = raw.map((d) => ({
      deviceId: d.deviceId ?? d.id,
      name: d.name ?? '',
      platform: d.platform ?? d.os ?? '',
      isOnline: !!d.isOnline,
      needsAuth: !!d.needsAuth,
      runnerDown: !!d.runnerDown,
      lastHeartbeat: d.lastHeartbeat ?? 0,
      quicHost: d.quicHost ?? d.host ?? '',
      quicPort: d.quicPort ?? 0,
      httpPort: d.httpPort ?? d.quicPort,
      publicKey: d.publicKey,
      hwid: d.hardwareId ?? d.hwid,
      localIps: Array.isArray(d.localIps)
        ? d.localIps
        : Array.isArray(d.lanIps)
          ? d.lanIps
          : undefined,
    }));
    // Lazy require so Jest + tree-shakers don't choke on a circular import.
    const { collapseRemoteDevices } = require('./deviceDedup') as typeof import('./deviceDedup');
    const deduped = collapseRemoteDevices(normalised);
    return { owned: deduped };
  } catch (error) {
    if (controller.signal.aborted) throw new Error('Machine discovery timed out. Check your connection and try again.');
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

export function buildDeviceCandidateUrls(device: RemoteDevice): string[] {
  const port = device.httpPort ?? device.quicPort ?? 18080;
  const hosts = new Set<string>();
  if (device.quicHost) hosts.add(device.quicHost);
  for (const ip of device.localIps ?? []) {
    if (ip) hosts.add(ip);
  }
  return Array.from(hosts).map((host) => agentHttpBase(host, port));
}

export async function probeDeviceReachability(
  device: RemoteDevice,
  timeoutMs = 2500,
): Promise<DeviceReachability> {
  const candidates = buildDeviceCandidateUrls(device);
  if (candidates.length === 0) return { reachable: false };

  const probeOne = async (baseUrl: string): Promise<string> => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const response = await fetch(`${baseUrl}/health`, {
        method: 'GET',
        signal: controller.signal,
      });
      if (!response.ok) throw new Error(`health ${response.status}`);
      return baseUrl;
    } finally {
      clearTimeout(timer);
    }
  };

  const settled = await Promise.allSettled(candidates.map((url) => probeOne(url)));
  for (const result of settled) {
    if (result.status === 'fulfilled') {
      return { reachable: true, url: result.value };
    }
  }
  return { reachable: false };
}
