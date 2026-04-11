/**
 * Web stub for the LAN beacon listener.
 *
 * The native implementation uses `react-native-udp` to listen for UDP
 * broadcasts on port 19837. Browsers cannot bind UDP sockets, so on web
 * we expose a no-op implementation with the same shape — discovery just
 * always reports "no local devices", and the QUIC client falls through to
 * its Convex-known-IP / relay paths.
 */

export interface DiscoveredDevice {
  deviceId: string;
  ip: string;
  port: number;
  name: string;
  lastSeen: number;
  hwid?: string;
}

type DiscoveryCallback = (device: DiscoveredDevice) => void;
type LostCallback = (deviceId: string) => void;

class BeaconListenerWeb {
  async setUserId(_userId: string): Promise<void> {}
  setKnownDevices(_deviceIds: string[]): void {}
  start(): void {}
  stop(): void {}
  onDiscovered(_cb: DiscoveryCallback): () => void {
    return () => {};
  }
  onLost(_cb: LostCallback): () => void {
    return () => {};
  }
  getDevices(): DiscoveredDevice[] {
    return [];
  }
  isLocal(_deviceId: string): boolean {
    return false;
  }
  getLocalIP(_deviceId: string): { ip: string; port: number } | null {
    return null;
  }
}

export const beaconListener = new BeaconListenerWeb();
