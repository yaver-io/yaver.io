import nacl from 'tweetnacl';
import { Buffer } from 'buffer';
import { webcrypto } from 'crypto';
import { YaverDeviceDogfood } from '../deviceDogfood';

const mockBrowserValues = new Map<string, string>();
const mockBrowserStorage = {
  getItem: jest.fn(async (key: string) => mockBrowserValues.get(key) ?? null),
  setItem: jest.fn(async (key: string, value: string) => { mockBrowserValues.set(key, value); }),
  removeItem: jest.fn(async (key: string) => { mockBrowserValues.delete(key); }),
};
const mockNativeSecureStore = {
  getItemAsync: jest.fn(async () => { throw new Error('web stub called native'); }),
  setItemAsync: jest.fn(async () => { throw new Error('web stub called native'); }),
  deleteItemAsync: jest.fn(async () => { throw new Error('web stub called native'); }),
};

jest.mock('react-native', () => ({ Platform: { OS: 'web' } }));
jest.mock('@react-native-async-storage/async-storage', () => ({ default: mockBrowserStorage }));
jest.mock('expo-secure-store', () => mockNativeSecureStore, { virtual: true });

class MemorySecureStore {
  values = new Map<string, string>();
  async getItemAsync(key: string) { return this.values.get(key) ?? null; }
  async setItemAsync(key: string, value: string) { this.values.set(key, value); }
  async deleteItemAsync(key: string) { this.values.delete(key); }
}

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('YaverDeviceDogfood', () => {
  beforeAll(() => {
    if (!(globalThis as any).crypto) (globalThis as any).crypto = webcrypto;
  });

  afterEach(() => { jest.restoreAllMocks(); });

  test('persists the browser signing identity with the RN-web storage adapter', async () => {
    mockBrowserValues.clear();
    const firstClient = new YaverDeviceDogfood({ appId: 'io.example.browser' });
    const first = await firstClient.enrollmentInfo();
    const secondClient = new YaverDeviceDogfood({ appId: 'io.example.browser' });
    expect(await secondClient.enrollmentInfo()).toEqual(first);
    expect(mockBrowserStorage.setItem).toHaveBeenCalled();
    expect(mockNativeSecureStore.getItemAsync).not.toHaveBeenCalled();
  });

  test('keeps identity stable and proves possession rather than trusting the UUID', async () => {
    const store = new MemorySecureStore();
    const client = new YaverDeviceDogfood({ appId: 'io.example.test', authToken: 'full-yaver-token', secureStore: store, backendUrl: 'https://dogfood.test' });
    const first = await client.enrollmentInfo();
    const second = await client.enrollmentInfo();
    expect(second).toEqual(first);

    let proof: any;
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input);
      if (url.endsWith('/dogfood/enroll/start')) return response({ status: 'pending', challenge: 'server-nonce' });
      if (url.endsWith('/dogfood/enroll/prove')) { proof = JSON.parse(String(init?.body)); return response({ status: 'pending', proofVerified: true }); }
      throw new Error(`unexpected URL ${url}`);
    });
    await client.enroll('ios');
    const message = new TextEncoder().encode(`yaver-dogfood-enroll-v1\nio.example.test\n${first.installationId}\nserver-nonce`);
    expect(nacl.sign.detached.verify(message, new Uint8Array(Buffer.from(proof.signature, 'base64')), new Uint8Array(Buffer.from(first.publicKey, 'base64')))).toBe(true);

    const attacker = nacl.sign.keyPair();
    expect(nacl.sign.detached.verify(message, new Uint8Array(Buffer.from(proof.signature, 'base64')), attacker.publicKey)).toBe(false);
  });

  test('re-register rotates key and installation while preserving only the logical slot', async () => {
    const store = new MemorySecureStore();
    const client = new YaverDeviceDogfood({ appId: 'io.example.test', authToken: 'full-yaver-token', secureStore: store, backendUrl: 'https://dogfood.test' });
    const before = await client.enrollmentInfo();
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      if (String(input).endsWith('/dogfood/enroll/start')) return response({ status: 'pending', challenge: 'rotate-nonce' });
      return response({ status: 'pending', proofVerified: true });
    });
    await client.reRegister('ios');
    const after = await client.enrollmentInfo();
    expect(after.registrationSlot).toBe(before.registrationSlot);
    expect(after.installationId).not.toBe(before.installationId);
    expect(after.publicKey).not.toBe(before.publicKey);
  });

  test('exchanges an approved key proof for a short-lived scoped session', async () => {
    const client = new YaverDeviceDogfood({ appId: 'io.example.test', secureStore: new MemorySecureStore(), backendUrl: 'https://dogfood.test' });
    const identity = await client.enrollmentInfo();
    let sessionProof: any;
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input);
      if (url.includes('/dogfood/enroll/status')) return response({ status: 'active' });
      if (url.endsWith('/dogfood/session/challenge')) return response({ challenge: 'session-nonce' });
      if (url.endsWith('/dogfood/session')) { sessionProof = JSON.parse(String(init?.body)); return response({ token: 'short-token', expiresAt: 123, scopes: ['feedback', 'blackbox'], projectSlug: 'example' }); }
      throw new Error(`unexpected URL ${url}`);
    });
    const session = await client.session();
    expect(session).toMatchObject({ active: true, token: 'short-token', scopes: ['feedback', 'blackbox'] });
    const message = new TextEncoder().encode(`yaver-dogfood-session-v1\nio.example.test\n${identity.installationId}\nsession-nonce`);
    expect(nacl.sign.detached.verify(message, new Uint8Array(Buffer.from(sessionProof.signature, 'base64')), new Uint8Array(Buffer.from(identity.publicKey, 'base64')))).toBe(true);
  });
});
