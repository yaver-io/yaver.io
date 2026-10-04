import { Platform } from "react-native";
import * as SecureStore from "expo-secure-store";
import AsyncStorage from "@react-native-async-storage/async-storage";

// expo-secure-store is native-only. Expo web can still use the settings
// screens, so keep the same async API with a browser-local fallback.
const memoryFallback: Record<string, string> = {};
const isTV = Boolean((Platform as typeof Platform & { isTV?: boolean }).isTV);
const tvSessionKey = (key: string) => `@yaver/tvos_session/${key}`;
const SECRET_KEYCHAIN_SERVICE = "works.yaver.credentials.v1";
const secretOptions: SecureStore.SecureStoreOptions = {
  // Stable service/alias across application upgrades. WHEN_UNLOCKED is not
  // device-only on iOS, so normal encrypted device migration remains possible.
  // We deliberately avoid requireAuthentication: biometric enrollment changes
  // must not silently make infrastructure credentials unrecoverable.
  keychainService: SECRET_KEYCHAIN_SERVICE,
  keychainAccessible: SecureStore.WHEN_UNLOCKED,
};

function webStorage(): Storage | null {
  if (Platform.OS !== "web") return null;
  try {
    return typeof window !== "undefined" ? window.localStorage : null;
  } catch {
    return null;
  }
}

export async function getItem(key: string): Promise<string | null> {
  const storage = webStorage();
  if (storage) {
    try { return storage.getItem(key); } catch { return memoryFallback[key] ?? null; }
  }
  try {
    const secureValue = await SecureStore.getItemAsync(key);
    if (secureValue !== null) return secureValue;
  } catch { /* use the session fallback below */ }
  if (isTV) {
    try {
      const persisted = await AsyncStorage.getItem(tvSessionKey(key));
      if (persisted !== null) return persisted;
    } catch { /* use memory below */ }
  }
  return memoryFallback[key] ?? null;
}

export async function setItem(key: string, value: string): Promise<void> {
  memoryFallback[key] = value;
  const storage = webStorage();
  if (storage) {
    try { storage.setItem(key, value); } catch { /* memory fallback */ }
    return;
  }
  try { await SecureStore.setItemAsync(key, value); } catch { /* fallback below */ }
  // Expo SecureStore is not available in every tvOS build. Session tokens and
  // cached user identity still need to survive an app restart, so keep this
  // best-effort API in the application sandbox. getSecret/setSecret below do
  // not use this path and continue to require real secure storage.
  if (isTV) {
    try { await AsyncStorage.setItem(tvSessionKey(key), value); } catch { /* memory fallback */ }
  }
}

export async function deleteItem(key: string): Promise<void> {
  delete memoryFallback[key];
  const storage = webStorage();
  if (storage) {
    try { storage.removeItem(key); } catch { /* best effort */ }
    return;
  }
  try { await SecureStore.deleteItemAsync(key); } catch { /* best effort */ }
  if (isTV) {
    try { await AsyncStorage.removeItem(tvSessionKey(key)); } catch { /* best effort */ }
  }
}

/**
 * Best-effort storage is suitable for non-sensitive UI state and sessions, but
 * never for a provider key or Git credential.  In particular, a tvOS Keychain
 * entitlement/configuration error must not turn a long-lived token into an
 * in-memory secret without telling the user.
 */
export async function isPersistentSecureStorageAvailable(): Promise<boolean> {
  if (Platform.OS === "web") return false;
  try {
    return await SecureStore.isAvailableAsync();
  } catch {
    return false;
  }
}

async function requirePersistentSecureStorage(): Promise<void> {
  if (!(await isPersistentSecureStorageAvailable())) {
    throw new Error("Secure device storage is unavailable. Local model and Git credentials cannot be saved on this device.");
  }
}

export async function getSecret(key: string): Promise<string | null> {
  await requirePersistentSecureStorage();
  try {
    const current = await SecureStore.getItemAsync(key, secretOptions);
    if (current !== null) return current;
    // One-time migration for credentials written before the stable service was
    // introduced. Never delete the legacy value until the new write succeeds.
    const legacy = await SecureStore.getItemAsync(key);
    if (legacy !== null) {
      await SecureStore.setItemAsync(key, legacy, secretOptions);
      await SecureStore.deleteItemAsync(key).catch(() => {});
    }
    return legacy;
  } catch {
    throw new Error("Secure device storage could not read this credential.");
  }
}

export async function setSecret(key: string, value: string): Promise<void> {
  await requirePersistentSecureStorage();
  try {
    await SecureStore.setItemAsync(key, value, secretOptions);
  } catch {
    throw new Error("Secure device storage could not save this credential.");
  }
}

export async function deleteSecret(key: string): Promise<void> {
  await requirePersistentSecureStorage();
  try {
    await SecureStore.deleteItemAsync(key, secretOptions);
    await SecureStore.deleteItemAsync(key).catch(() => {});
  } catch {
    throw new Error("Secure device storage could not remove this credential.");
  }
}

// Keep the SecureStore-shaped names so existing callers remain unchanged.
export const getItemAsync = getItem;
export const setItemAsync = setItem;
export const deleteItemAsync = deleteItem;
