import nacl from 'tweetnacl';
import { Buffer } from 'buffer';
import { DEFAULT_CONVEX_SITE_URL } from './auth';

type SecureStoreLike = {
  getItemAsync(key: string): Promise<string | null>;
  setItemAsync(key: string, value: string): Promise<void>;
  deleteItemAsync(key: string): Promise<void>;
};

export type DeviceDogfoodState = 'unregistered' | 'pending' | 'active' | 'cancelled' | 'revoked' | 'superseded';

export interface DeviceDogfoodOptions {
  /** Public identifier registered by the app owner in Yaver Settings. */
  appId: string;
  label?: string;
  backendUrl?: string;
  /** Full Yaver OAuth token. Required for enrollment; never persisted with the
   * installation private key. The backend binds its user to this key. */
  authToken?: string;
  /** Advanced bare-RN integration. Expo apps use SecureStore automatically. */
  secureStore?: SecureStoreLike;
}

export interface DeviceDogfoodSession {
  active: true;
  appId: string;
  installationId: string;
  token: string;
  expiresAt: number;
  scopes: string[];
  projectSlug?: string;
  targetDeviceId?: string;
}

interface StoredIdentity {
  installationId: string;
  registrationSlot: string;
  publicKey: string;
  secretKey: string;
}

function b64(bytes: Uint8Array): string { return Buffer.from(bytes).toString('base64'); }
function fromB64(value: string): Uint8Array { return new Uint8Array(Buffer.from(value, 'base64')); }
function b64url(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString('base64').replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
}

function randomBytes(length: number): Uint8Array {
  const cryptoObject = (globalThis as any)?.crypto;
  if (cryptoObject?.getRandomValues) {
    const out = new Uint8Array(length);
    cryptoObject.getRandomValues(out);
    return out;
  }
  try {
    return require('expo-crypto').getRandomBytes(length);
  } catch {
    throw new Error('Secure randomness is unavailable. Install expo-crypto or provide global crypto.getRandomValues.');
  }
}

function defaultSecureStore(): SecureStoreLike {
  let isWeb = false;
  try { isWeb = require('react-native').Platform?.OS === 'web'; } catch { /* native fallback below */ }
  if (!isWeb) {
    try {
      const store = require('expo-secure-store');
      if (store?.getItemAsync && store?.setItemAsync) return store;
    } catch { /* named error below */ }
  }
  // expo-secure-store deliberately has no browser implementation. RN-web
  // still needs a stable per-browser signing identity, and AsyncStorage maps
  // to the browser's origin-scoped storage. Keep this fallback web-only:
  // native installations must continue to require Keychain/Keystore rather
  // than silently persisting a private installation key in plaintext.
  try {
    if (isWeb) {
      const storage = require('@react-native-async-storage/async-storage')?.default;
      if (storage?.getItem && storage?.setItem && storage?.removeItem) {
        return {
          getItemAsync: (key) => storage.getItem(key),
          setItemAsync: (key, value) => storage.setItem(key, value),
          deleteItemAsync: (key) => storage.removeItem(key),
        };
      }
    }
  } catch { /* named error below */ }
  throw new Error('Secure Dogfood identity storage is unavailable. Install expo-secure-store or pass secureStore.');
}

async function responseJSON(response: Response): Promise<any> {
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body?.error || `Yaver Dogfood request failed (HTTP ${response.status})`);
  return body;
}

async function dogfoodRequest(url: string, init: RequestInit = {}, timeoutMs = 10_000): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(url, { ...init, signal: controller.signal });
  } catch (error) {
    if (controller.signal.aborted) throw new Error('Yaver took too long to respond. Check your connection and try again.');
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

function enrollmentMessage(appId: string, installationId: string, challenge: string): Uint8Array {
  return new TextEncoder().encode(`yaver-dogfood-enroll-v1\n${appId}\n${installationId}\n${challenge}`);
}

function sessionMessage(appId: string, installationId: string, challenge: string): Uint8Array {
  return new TextEncoder().encode(`yaver-dogfood-session-v1\n${appId}\n${installationId}\n${challenge}`);
}

/** Account-bound third-party Dogfood enrollment. The installation ID is a
 * public lookup handle; possession is proven by the private key retained in
 * this app, while enrollment is bound to the signed-in Yaver account. */
export class YaverDeviceDogfood {
  private readonly backendUrl: string;
  private readonly store: SecureStoreLike;
  private readonly storageKey: string;

  constructor(private readonly options: DeviceDogfoodOptions) {
    if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]{2,127}$/.test(options.appId)) throw new Error('A valid Dogfood appId is required');
    this.backendUrl = (options.backendUrl || DEFAULT_CONVEX_SITE_URL).replace(/\/$/, '');
    this.store = options.secureStore || defaultSecureStore();
    this.storageKey = `yaver_feedback_dogfood_v1_${options.appId}`;
  }

  private async identity(): Promise<StoredIdentity> {
    const raw = await this.store.getItemAsync(this.storageKey);
    if (raw) {
      try {
        const saved = JSON.parse(raw) as StoredIdentity;
        if (fromB64(saved.publicKey).length === nacl.sign.publicKeyLength && fromB64(saved.secretKey).length === nacl.sign.secretKeyLength) return saved;
      } catch { /* rotate corrupt identity below */ }
    }
    const seed = randomBytes(32);
    const pair = nacl.sign.keyPair.fromSeed(seed);
    seed.fill(0);
    const identity: StoredIdentity = {
      installationId: b64url(randomBytes(18)), registrationSlot: b64url(randomBytes(18)),
      publicKey: b64(pair.publicKey), secretKey: b64(pair.secretKey),
    };
    await this.store.setItemAsync(this.storageKey, JSON.stringify(identity));
    return identity;
  }

  async enrollmentInfo(): Promise<{ appId: string; installationId: string; registrationSlot: string; publicKey: string }> {
    const identity = await this.identity();
    return { appId: this.options.appId, installationId: identity.installationId, registrationSlot: identity.registrationSlot, publicKey: identity.publicKey };
  }

  async enroll(platform = 'unknown'): Promise<{ status: DeviceDogfoodState; installationId: string }> {
    if (!this.options.authToken) throw new Error('Sign in to Yaver before registering this device for Dogfood.');
    const identity = await this.identity();
    const started = await responseJSON(await dogfoodRequest(`${this.backendUrl}/dogfood/enroll/start`, {
      method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${this.options.authToken}` },
      body: JSON.stringify({ appId: this.options.appId, installationId: identity.installationId, registrationSlot: identity.registrationSlot, publicKey: identity.publicKey, platform, label: this.options.label }),
    }));
    if (started.status === 'active') return { status: 'active', installationId: identity.installationId };
    const signature = b64(nacl.sign.detached(enrollmentMessage(this.options.appId, identity.installationId, started.challenge), fromB64(identity.secretKey)));
    await responseJSON(await dogfoodRequest(`${this.backendUrl}/dogfood/enroll/prove`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ appId: this.options.appId, installationId: identity.installationId, signature }),
    }));
    return { status: 'pending', installationId: identity.installationId };
  }

  async status(): Promise<DeviceDogfoodState> {
    const identity = await this.identity();
    const response = await dogfoodRequest(`${this.backendUrl}/dogfood/enroll/status?appId=${encodeURIComponent(this.options.appId)}&installationId=${encodeURIComponent(identity.installationId)}`);
    if (response.status === 404) return 'unregistered';
    return (await responseJSON(response)).status as DeviceDogfoodState;
  }

  async session(): Promise<DeviceDogfoodSession | null> {
    const identity = await this.identity();
    if (await this.status() !== 'active') return null;
    const challenge = await responseJSON(await dogfoodRequest(`${this.backendUrl}/dogfood/session/challenge`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ appId: this.options.appId, installationId: identity.installationId }),
    }));
    const signature = b64(nacl.sign.detached(sessionMessage(this.options.appId, identity.installationId, challenge.challenge), fromB64(identity.secretKey)));
    const result = await responseJSON(await dogfoodRequest(`${this.backendUrl}/dogfood/session`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ appId: this.options.appId, installationId: identity.installationId, signature }),
    }));
    return { active: true, appId: this.options.appId, installationId: identity.installationId, token: result.token, expiresAt: result.expiresAt, scopes: result.scopes || [], projectSlug: result.projectSlug, targetDeviceId: result.targetDeviceId };
  }

  /** Rotate the key but keep the logical slot. Approval supersedes the prior
   * generation, making its already-issued sessions fail immediately. */
  async reRegister(platform = 'unknown'): Promise<{ status: DeviceDogfoodState; installationId: string }> {
    const previous = await this.identity();
    const seed = randomBytes(32);
    const pair = nacl.sign.keyPair.fromSeed(seed);
    seed.fill(0);
    const next: StoredIdentity = {
      installationId: b64url(randomBytes(18)), registrationSlot: previous.registrationSlot,
      publicKey: b64(pair.publicKey), secretKey: b64(pair.secretKey),
    };
    await this.store.setItemAsync(this.storageKey, JSON.stringify(next));
    return this.enroll(platform);
  }
}
