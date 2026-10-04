/** Native-only credential custody for direct Hetzner Cloud management. */

import { deleteSecret, getSecret, setSecret } from "./secure-storage";
import { LOCAL_KEYS } from "./auth";
import {
  listHetznerServers,
  listHetznerServerActions,
  powerOnHetznerServer,
  renameHetznerServer,
  shutdownHetznerServer,
  validateHetznerToken,
  type HetznerActionLog,
  type HetznerServer,
} from "./hetznerDirectCore";
import { decryptHetznerRecovery, encryptHetznerRecovery, type HetznerRecoveryExport } from "./hetznerRecovery";

export type LocalHetznerManagedServer = Pick<HetznerServer, "id" | "name" | "ip"> & { v: 1 };

async function requireToken(): Promise<string> {
  const token = (await getSecret(LOCAL_KEYS.hetznerToken))?.trim();
  if (!token) throw new Error("Connect Hetzner on this phone first.");
  return token;
}

export async function hasLocalHetznerToken(): Promise<boolean> {
  return Boolean((await getSecret(LOCAL_KEYS.hetznerToken))?.trim());
}

export async function connectLocalHetzner(token: string): Promise<void> {
  const value = token.trim();
  await validateHetznerToken(value);
  await setSecret(LOCAL_KEYS.hetznerToken, value);
}

export async function disconnectLocalHetzner(): Promise<void> {
  await deleteSecret(LOCAL_KEYS.hetznerToken);
  await deleteSecret(LOCAL_KEYS.hetznerManagedServer);
}

export async function getLocalHetznerManagedServer(): Promise<LocalHetznerManagedServer | null> {
  const raw = await getSecret(LOCAL_KEYS.hetznerManagedServer);
  if (!raw) return null;
  try {
    const value = JSON.parse(raw);
    if (value?.v !== 1 || !Number.isSafeInteger(value?.id) || value.id <= 0 || typeof value?.name !== "string") return null;
    return { v: 1, id: value.id, name: value.name, ip: typeof value.ip === "string" ? value.ip : null };
  } catch {
    return null;
  }
}

export async function setLocalHetznerManagedServer(server: HetznerServer): Promise<void> {
  if (!Number.isSafeInteger(server.id) || server.id <= 0 || !server.name.trim()) {
    throw new Error("Choose a valid Hetzner server.");
  }
  await setSecret(LOCAL_KEYS.hetznerManagedServer, JSON.stringify({
    v: 1,
    id: server.id,
    name: server.name,
    ip: server.ip,
  } satisfies LocalHetznerManagedServer));
}

async function requireManagedServer(serverId: number): Promise<LocalHetznerManagedServer> {
  const selected = await getLocalHetznerManagedServer();
  if (!selected) throw new Error("Select the Hetzner server this phone may control first.");
  if (selected.id !== serverId) throw new Error("This phone is not configured to control that Hetzner server.");
  return selected;
}

export async function exportLocalHetznerRecovery(): Promise<HetznerRecoveryExport> {
  return encryptHetznerRecovery(await requireToken());
}

export async function importLocalHetznerRecovery(encryptedBackup: string, recoveryKey: string): Promise<void> {
  const token = decryptHetznerRecovery(encryptedBackup, recoveryKey);
  await validateHetznerToken(token);
  await setSecret(LOCAL_KEYS.hetznerToken, token);
}

export async function getLocalHetznerServers(): Promise<HetznerServer[]> {
  return listHetznerServers(await requireToken());
}

export async function getLocalHetznerServerActions(serverId: number): Promise<HetznerActionLog[]> {
  await requireManagedServer(serverId);
  return listHetznerServerActions(await requireToken(), serverId);
}

export async function powerOnLocalHetznerServer(serverId: number): Promise<HetznerServer> {
  await requireManagedServer(serverId);
  return powerOnHetznerServer(await requireToken(), serverId);
}

export async function shutdownLocalHetznerServer(serverId: number): Promise<HetznerServer> {
  await requireManagedServer(serverId);
  return shutdownHetznerServer(await requireToken(), serverId);
}

export async function renameLocalHetznerServer(serverId: number, name: string): Promise<HetznerServer> {
  await requireManagedServer(serverId);
  const renamed = await renameHetznerServer(await requireToken(), serverId, name);
  await setLocalHetznerManagedServer(renamed);
  return renamed;
}
