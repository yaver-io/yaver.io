/** Native-only credential custody for direct Hetzner Cloud management. */

import { deleteSecret, getSecret, setSecret } from "./secure-storage";
import { LOCAL_KEYS } from "./auth";
import {
  listHetznerServers,
  powerOnHetznerServer,
  shutdownHetznerServer,
  validateHetznerToken,
  type HetznerServer,
} from "./hetznerDirectCore";
import { decryptHetznerRecovery, encryptHetznerRecovery, type HetznerRecoveryExport } from "./hetznerRecovery";

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

export async function powerOnLocalHetznerServer(serverId: number): Promise<void> {
  await powerOnHetznerServer(await requireToken(), serverId);
}

export async function shutdownLocalHetznerServer(serverId: number): Promise<void> {
  await shutdownHetznerServer(await requireToken(), serverId);
}
