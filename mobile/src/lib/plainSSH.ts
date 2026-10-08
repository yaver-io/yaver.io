import { NativeModules, Platform } from "react-native";
import AsyncStorage from "@react-native-async-storage/async-storage";
import * as SecureStore from "expo-secure-store";
import { Buffer } from "buffer";
import { getSecret, setSecret, deleteSecret } from "./secure-storage";

export interface SSHHost {
  id: string;
  host: string;
  port: number;
  user: string;
  fingerprint: string;
}
export interface SSHCredentials { password?: string; privateKey?: string; passphrase?: string }
export interface SSHPane { identity: string; id: string; sessionId: string; session: string; window: string; command: string; width: number; height: number }
const hostsKey = "yaver.plainSSH.hosts.v1";
const syncedHostsKey = "yaver.plainSSH.synced-hosts.v1";
const syncedCredentialPrefix = "yaver.plainSSH.synced-credential.v1.";
const bridge = NativeModules.YaverPlainSSH;
const configuredBridge = process.env.EXPO_PUBLIC_PLAIN_SSH_BRIDGE || "";
const bridgeURL = (() => {
  if (Platform.OS !== "web" || !configuredBridge) return "";
  try { const u = new URL(configuredBridge); return u.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(u.hostname) && !u.username && !u.password && !u.search && !u.hash && (u.pathname === "/" || !u.pathname) ? u.origin : ""; } catch { return ""; }
})();
// Browser companion credentials are session-memory only, never localStorage.
let browserHosts: SSHHost[] = [];
const browserCredentials = new Map<string, SSHCredentials>();
export const plainSSHUnavailable = Platform.OS === "web"
  ? bridgeURL ? null : "Direct SSH needs the native iOS or Android app. Browsers cannot open an SSH connection."
  : !bridge ? "This build does not include direct SSH. Install a Yaver build with Plain SSH support." : null;

export async function sshCall<T>(request: Record<string, unknown>): Promise<T> {
  if (plainSSHUnavailable) throw new Error(plainSSHUnavailable);
  let raw: string;
  if (Platform.OS === "web") {
    const controller = new AbortController(); const timer = setTimeout(() => controller.abort(), 45_000);
    try {
      const result = await fetch(`${bridgeURL}/invoke`, {method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(request),signal:controller.signal});
      if (!result.ok) throw new Error("Local SSH companion rejected this browser origin. Start it for this exact local app URL.");
      raw = await result.text();
    } catch (cause) { throw new Error(cause instanceof Error && cause.message.includes("origin") ? cause.message : "Local SSH companion is unavailable. Start npm run ssh:bridge for this browser preview, then retry."); }
    finally {clearTimeout(timer);}
  } else raw = await bridge.invoke(JSON.stringify(request));
  const response = JSON.parse(raw);
  if (!response.ok) {
    const error = new Error(response.error?.message || "SSH operation failed.");
    Object.assign(error, { code: response.error?.code });
    throw error;
  }
  return response.value as T;
}
export async function loadSSHHosts(): Promise<SSHHost[]> {
  if (Platform.OS === "web") return browserHosts;
  const stored = await AsyncStorage.getItem(hostsKey);
  const legacy: SSHHost[] = stored ? JSON.parse(stored) : [];
  let synced: SSHHost[] = [];
  try { synced = JSON.parse((await getSecret(syncedHostsKey)) || "[]"); } catch { /* unavailable secure store */ }
  const merged = [...legacy.filter((host) => !synced.some((item) => item.id === host.id)), ...synced];
  if (legacy.length > 0) {
    await setSecret(syncedHostsKey, JSON.stringify(merged));
    await AsyncStorage.removeItem(hostsKey);
  }
  return merged;
}

export interface GeneratedSSHIdentity { publicKey: string; privateKey: string; fingerprint: string }
export async function generateSSHIdentity(): Promise<GeneratedSSHIdentity> {
  return sshCall<GeneratedSSHIdentity>({ op: "keygen" });
}

/** Store a synchronized machine profile and its private key entirely inside
 * iOS Keychain or Android Keystore-backed SecureStore. */
export async function saveSyncedSSHHost(host: SSHHost, credentials: SSHCredentials): Promise<void> {
  if (Platform.OS === "web") throw new Error("Machine access sync requires the native mobile app.");
  await setSecret(`${syncedCredentialPrefix}${host.id}`, JSON.stringify(credentials));
  const current: SSHHost[] = JSON.parse((await getSecret(syncedHostsKey)) || "[]");
  await setSecret(syncedHostsKey, JSON.stringify([...current.filter((item) => item.id !== host.id), host]));
}

export async function loadSyncedSSHHosts(): Promise<SSHHost[]> {
  if (Platform.OS === "web") return [];
  try { return JSON.parse((await getSecret(syncedHostsKey)) || "[]"); } catch { return []; }
}
export async function saveSSHHost(host: SSHHost, credentials: SSHCredentials): Promise<void> {
  if (Platform.OS === "web") { if (!bridgeURL) throw new Error("Enable the local SSH companion first."); browserHosts=[...browserHosts.filter((h)=>h.id!==host.id),host]; browserCredentials.set(host.id,credentials); return; }
  // Never use secureStoreCompat here: its browser fallback is localStorage.
  await SecureStore.setItemAsync(`plainSSH.${host.id}`, JSON.stringify(credentials), {
    keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
  });
  const hosts = await loadSSHHosts();
  await setSecret(syncedHostsKey, JSON.stringify([...hosts.filter((h) => h.id !== host.id), host]));
}
export async function loadSSHCredentials(id: string): Promise<SSHCredentials> {
  if (Platform.OS === "web") return browserCredentials.get(id) || {};
  try {
    const synced = await getSecret(`${syncedCredentialPrefix}${id}`);
    if (synced) return JSON.parse(synced);
  } catch { /* fall through to legacy credential */ }
  return JSON.parse(await SecureStore.getItemAsync(`plainSSH.${id}`) || "{}");
}
export async function removeSSHHost(id: string): Promise<void> {
  if (Platform.OS === "web") {browserHosts=browserHosts.filter((h)=>h.id!==id);browserCredentials.delete(id);return;}
  await SecureStore.deleteItemAsync(`plainSSH.${id}`);
  await SecureStore.deleteItemAsync(`plainSSH.car.${id}`);
  await deleteSecret(`${syncedCredentialPrefix}${id}`).catch(() => {});
  try {
    const synced: SSHHost[] = JSON.parse((await getSecret(syncedHostsKey)) || "[]");
    await setSecret(syncedHostsKey, JSON.stringify(synced.filter((host) => host.id !== id)));
  } catch { /* best effort */ }
}
export function sshBytes(data: string): Uint8Array { return Buffer.from(data, "base64"); }
export function sshInput(data: string | Uint8Array): string { return Buffer.from(data).toString("base64"); }
