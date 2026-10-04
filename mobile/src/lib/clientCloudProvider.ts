import {
  connectLocalHetzner,
  clearLocalHetznerManagedServer,
  disconnectLocalHetzner,
  exportLocalHetznerRecovery,
  getLocalHetznerServers,
  getLocalHetznerServerActions,
  getLocalHetznerManagedServer,
  hasLocalHetznerToken,
  importLocalHetznerRecovery,
  powerOnLocalHetznerServer,
  renameLocalHetznerServer,
  shutdownLocalHetznerServer,
  setLocalHetznerManagedServer,
} from "./hetznerDirect";
import type { HetznerActionLog, HetznerServer } from "./hetznerDirectCore";
import type { HetznerRecoveryExport } from "./hetznerRecovery";

export type ClientCloudPowerAction = "power_on" | "shutdown";
export type ClientCloudManagedResource = { id: number; name: string; ip: string | null };

/**
 * Endpoint-only provider contract. Implementations may be linked into trusted
 * native apps/daemons; backend, Convex, Cloudflare and relay packages must not
 * import this module. The contract deliberately has no create/delete/resize.
 */
export abstract class ClientCloudProviderAdapter<TServer> {
  abstract readonly id: string;
  abstract readonly label: string;
  abstract isConnected(): Promise<boolean>;
  abstract connect(credential: string): Promise<void>;
  abstract disconnect(): Promise<void>;
  abstract listServers(): Promise<TServer[]>;
  abstract getManagedServer(): Promise<ClientCloudManagedResource | null>;
  abstract setManagedServer(server: TServer): Promise<void>;
  abstract clearManagedServer(): Promise<void>;
  abstract setPower(serverId: number, action: ClientCloudPowerAction): Promise<TServer>;
  abstract renameServer(serverId: number, name: string): Promise<TServer>;
  abstract listActions(serverId: number): Promise<HetznerActionLog[]>;
  abstract exportRecovery(): Promise<HetznerRecoveryExport>;
  abstract importRecovery(ciphertext: string, recoveryKey: string): Promise<void>;
}

class HetznerClientCloudAdapter extends ClientCloudProviderAdapter<HetznerServer> {
  readonly id = "hetzner";
  readonly label = "Hetzner Cloud";
  isConnected = hasLocalHetznerToken;
  connect = connectLocalHetzner;
  disconnect = disconnectLocalHetzner;
  listServers = getLocalHetznerServers;
  getManagedServer = getLocalHetznerManagedServer;
  setManagedServer = setLocalHetznerManagedServer;
  clearManagedServer = clearLocalHetznerManagedServer;
  exportRecovery = exportLocalHetznerRecovery;
  importRecovery = importLocalHetznerRecovery;

  async setPower(serverId: number, action: ClientCloudPowerAction): Promise<HetznerServer> {
    if (action === "power_on") return powerOnLocalHetznerServer(serverId);
    return shutdownLocalHetznerServer(serverId);
  }

  renameServer = renameLocalHetznerServer;
  listActions = getLocalHetznerServerActions;
}

// Phase one intentionally registers Hetzner only. Future providers add native
// endpoint adapters here; they do not add provider logic to Yaver Cloud.
export const hetznerClientCloud = new HetznerClientCloudAdapter();
export const clientCloudProviders = Object.freeze([hetznerClientCloud] as const);
