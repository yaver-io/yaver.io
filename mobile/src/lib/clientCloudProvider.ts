import {
  connectLocalHetzner,
  disconnectLocalHetzner,
  exportLocalHetznerRecovery,
  getLocalHetznerServers,
  hasLocalHetznerToken,
  importLocalHetznerRecovery,
  powerOnLocalHetznerServer,
  shutdownLocalHetznerServer,
} from "./hetznerDirect";
import type { HetznerServer } from "./hetznerDirectCore";
import type { HetznerRecoveryExport } from "./hetznerRecovery";

export type ClientCloudPowerAction = "power_on" | "shutdown";

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
  abstract setPower(serverId: number, action: ClientCloudPowerAction): Promise<void>;
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
  exportRecovery = exportLocalHetznerRecovery;
  importRecovery = importLocalHetznerRecovery;

  async setPower(serverId: number, action: ClientCloudPowerAction): Promise<void> {
    if (action === "power_on") return powerOnLocalHetznerServer(serverId);
    return shutdownLocalHetznerServer(serverId);
  }
}

// Phase one intentionally registers Hetzner only. Future providers add native
// endpoint adapters here; they do not add provider logic to Yaver Cloud.
export const hetznerClientCloud = new HetznerClientCloudAdapter();
export const clientCloudProviders = Object.freeze([hetznerClientCloud] as const);
