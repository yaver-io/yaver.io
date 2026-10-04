import { Platform } from "react-native";
import nacl from "./secureNacl";
import { decodeBase64, encodeBase64 } from "tweetnacl-util";
import { getConvexSiteUrl } from "./auth";
import { getItem, getSecret, setItem, setSecret } from "./secure-storage";

const CONTROL_IDENTITY_KEY = "yaver_dogfood_control_identity_v1";

export interface DogfoodAppRow {
  _id: string;
  appId: string;
  label: string;
  projectSlug?: string;
  targetDeviceId?: string;
  allowedScopes: string[];
  enabled: boolean;
  activationUrl?: string;
}

export interface DogfoodCatalogRow {
  appId: string;
  label: string;
  activationUrl: string;
}

export interface DogfoodInstallationRow {
  _id: string;
  appId: string;
  installationId: string;
  platform: string;
  label?: string;
  status: "pending" | "active" | "cancelled" | "revoked" | "superseded";
  proofVerifiedAt?: number;
  approvedAt?: number;
  lastSeenAt?: number;
  controlPresentation?: "auto" | "minimized-y";
  gestureSupported?: boolean;
  gestureCapabilityReason?: string;
  gesturePlatform?: string;
  gestureCheckedAt?: number;
  controlOnboardingSeenAt?: number;
  tester?: { name: string; email: string };
}

export interface DogfoodTesterRow {
  _id: string;
  appId: string;
  testerEmail: string;
  status: "active" | "revoked";
  testerUserId?: string;
  tester?: { name?: string; email: string };
}

interface ControlIdentity { deviceId: string; publicKey: string; secretKey: string }

async function readIdentity(): Promise<string | null> {
  return Platform.OS === "web" ? getItem(CONTROL_IDENTITY_KEY) : getSecret(CONTROL_IDENTITY_KEY);
}

async function writeIdentity(value: string): Promise<void> {
  return Platform.OS === "web" ? setItem(CONTROL_IDENTITY_KEY, value) : setSecret(CONTROL_IDENTITY_KEY, value);
}

function deviceId(bytes: Uint8Array): string {
  return `mobile_${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`;
}

export async function getOrCreateDogfoodControlIdentity(): Promise<ControlIdentity> {
  const raw = await readIdentity();
  if (raw) {
    try {
      const parsed = JSON.parse(raw) as ControlIdentity;
      if (parsed.deviceId && parsed.publicKey && parsed.secretKey) return parsed;
    } catch { /* replace corrupt local identity */ }
  }
  const pair = nacl.sign.keyPair();
  const identity = { deviceId: deviceId(nacl.randomBytes(12)), publicKey: encodeBase64(pair.publicKey), secretKey: encodeBase64(pair.secretKey) };
  await writeIdentity(JSON.stringify(identity));
  return identity;
}

async function request(token: string, path: string, init?: RequestInit): Promise<any> {
  const response = await fetch(`${getConvexSiteUrl()}${path}`, {
    ...init,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", ...(init?.headers || {}) },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body?.error || `Dogfood registry failed (HTTP ${response.status})`);
  return body;
}

export async function registerThisDogfoodControlDevice(token: string, label = "This phone"): Promise<{ generation: number; status: string; deviceId: string; assurance: "native-secure" | "browser-dev" }> {
  const identity = await getOrCreateDogfoodControlIdentity();
  const signedAt = Date.now();
  const message = new TextEncoder().encode(`yaver-dogfood-control-v1\n${identity.deviceId}\n${identity.publicKey}\n${signedAt}`);
  const signature = encodeBase64(nacl.sign.detached(message, decodeBase64(identity.secretKey)));
  const result = await request(token, "/dogfood/control-devices", {
    method: "POST", body: JSON.stringify({ deviceId: identity.deviceId, publicKey: identity.publicKey, platform: Platform.OS, label, signedAt, signature }),
  });
  return { ...result, deviceId: identity.deviceId, assurance: Platform.OS === "web" ? "browser-dev" : "native-secure" };
}

export async function listDogfoodApps(token: string): Promise<DogfoodAppRow[]> {
  return (await request(token, "/dogfood/apps")).apps || [];
}

export async function listDogfoodCatalog(token: string): Promise<DogfoodCatalogRow[]> {
  return (await request(token, "/dogfood/catalog")).apps || [];
}

export async function saveDogfoodApp(token: string, app: { appId: string; label: string; projectSlug?: string; targetDeviceId?: string; allowedScopes?: string[]; enabled?: boolean }): Promise<DogfoodAppRow> {
  return (await request(token, "/dogfood/apps", { method: "POST", body: JSON.stringify(app) })).app;
}

export async function listDogfoodTesters(token: string, appId?: string): Promise<DogfoodTesterRow[]> {
  const query = appId ? `?appId=${encodeURIComponent(appId)}` : "";
  return (await request(token, `/dogfood/testers${query}`)).testers || [];
}

export async function setDogfoodTester(token: string, appId: string, testerEmail: string, enabled: boolean): Promise<{ status: string; revokedInstallations?: number }> {
  return request(token, "/dogfood/testers", {
    method: "POST", body: JSON.stringify({ appId, testerEmail, enabled }),
  });
}

export async function listDogfoodInstallations(token: string, appId?: string): Promise<DogfoodInstallationRow[]> {
  const query = appId ? `?appId=${encodeURIComponent(appId)}` : "";
  return (await request(token, `/dogfood/installations${query}`)).installations || [];
}

export async function setDogfoodInstallationAction(token: string, installationId: string, action: "approve" | "cancel" | "revoke"): Promise<{ status: string; superseded?: number }> {
  const identity = await getOrCreateDogfoodControlIdentity();
  const signedAt = Date.now();
  const message = new TextEncoder().encode(`yaver-dogfood-control-action-v1\n${identity.deviceId}\n${installationId}\n${action}\n${signedAt}`);
  const signature = encodeBase64(nacl.sign.detached(message, decodeBase64(identity.secretKey)));
  return request(token, "/dogfood/installations/action", {
    method: "POST",
    body: JSON.stringify({ installationId, action, controlDeviceId: identity.deviceId, signedAt, signature }),
  });
}
