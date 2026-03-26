/**
 * LAN Beacon Listener — Yaver proprietary discovery protocol.
 *
 * Listens for UDP broadcast beacons from the CLI agent on port 19837.
 * Beacons are auth-aware: they include a token fingerprint (SHA256 of userId)
 * so only same-user devices match.
 *
 * All discovery events are logged via telemetry (peer-seen, peer-matched, etc.)
 * but are invisible to the user — silent roaming.
 */
import dgram from "react-native-udp";
import { appLog } from "./logger";

const BEACON_PORT = 19837;
const BEACON_STALE_MS = 10_000; // device expires after 10s of no beacons

export interface DiscoveredDevice {
  deviceId: string;  // short ID (first 8 chars)
  ip: string;        // sender IP from UDP packet
  port: number;      // HTTP port advertised in beacon
  name: string;      // hostname
  lastSeen: number;  // timestamp
  hwid?: string;     // stable hardware ID from beacon
}

interface BeaconPayload {
  v: number;   // protocol version
  id: string;  // deviceId (first 8 chars)
  p: number;   // HTTP port
  n: string;   // hostname
  th: string;  // token fingerprint
  hw?: string; // stable hardware ID (P2P only)
}

type DiscoveryCallback = (device: DiscoveredDevice) => void;
type LostCallback = (deviceId: string) => void;

/** Compute the same token fingerprint as the CLI: first 8 hex chars of SHA256(userId). */
async function computeFingerprint(userId: string): Promise<string> {
  // Use SubtleCrypto (available in React Native via Hermes/JSC polyfill)
  const encoder = new TextEncoder();
  const data = encoder.encode(userId);
  const hashBuffer = await crypto.subtle.digest("SHA-256", data);
  const hashArray = new Uint8Array(hashBuffer);
  return Array.from(hashArray.slice(0, 4))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

class BeaconListener {
  private socket: ReturnType<typeof dgram.createSocket> | null = null;
  private devices = new Map<string, DiscoveredDevice>();
  private fingerprint: string | null = null;
  private knownDeviceIds = new Set<string>(); // from Convex, for matching
  private cleanupTimer: ReturnType<typeof setInterval> | null = null;
  private onDiscoveredCallbacks: DiscoveryCallback[] = [];
  private onLostCallbacks: LostCallback[] = [];
  private running = false;

  /** Set the user ID to compute the auth fingerprint. */
  async setUserId(userId: string): Promise<void> {
    this.fingerprint = await computeFingerprint(userId);
    appLog("info", `[beacon] Fingerprint computed: ${this.fingerprint}`);
  }

  /** Set known device IDs from Convex (for matching). Pass short IDs (first 8 chars). */
  setKnownDevices(deviceIds: string[]): void {
    this.knownDeviceIds = new Set(deviceIds.map((id) => id.slice(0, 8)));
  }

  /** Start listening for beacons. */
  start(): void {
    if (this.running) return;
    this.running = true;

    try {
      const socket = dgram.createSocket({ type: "udp4", reusePort: true });

      socket.on("message", (data: Buffer, rinfo: { address: string; port: number }) => {
        this.handleBeacon(data, rinfo.address);
      });

      socket.on("error", (err: Error) => {
        appLog("error", `[beacon] Socket error: ${err.message}`);
      });

      socket.bind(BEACON_PORT, () => {
        appLog("info", `[beacon] Listening on UDP port ${BEACON_PORT}`);
      });

      this.socket = socket;
    } catch (err) {
      appLog("error", `[beacon] Failed to start: ${err}`);
      this.running = false;
      return;
    }

    // Cleanup stale devices every 5 seconds
    this.cleanupTimer = setInterval(() => this.cleanupStale(), 5000);
  }

  /** Stop listening. */
  stop(): void {
    if (!this.running) return;
    this.running = false;

    if (this.socket) {
      try { this.socket.close(); } catch {}
      this.socket = null;
    }
    if (this.cleanupTimer) {
      clearInterval(this.cleanupTimer);
      this.cleanupTimer = null;
    }
    this.devices.clear();
    appLog("info", "[beacon] Stopped.");
  }

  /** Subscribe to device discovered events. */
  onDiscovered(cb: DiscoveryCallback): () => void {
    this.onDiscoveredCallbacks.push(cb);
    return () => {
      this.onDiscoveredCallbacks = this.onDiscoveredCallbacks.filter((c) => c !== cb);
    };
  }

  /** Subscribe to device lost events. */
  onLost(cb: LostCallback): () => void {
    this.onLostCallbacks.push(cb);
    return () => {
      this.onLostCallbacks = this.onLostCallbacks.filter((c) => c !== cb);
    };
  }

  /** Get currently discovered devices. */
  getDevices(): DiscoveredDevice[] {
    return Array.from(this.devices.values());
  }

  /** Check if a device (by full or short ID) is locally discovered. */
  isLocal(deviceId: string): boolean {
    const shortId = deviceId.slice(0, 8);
    return this.devices.has(shortId);
  }

  /** Get the LAN IP for a device (by full or short ID), or null if not local. */
  getLocalIP(deviceId: string): { ip: string; port: number } | null {
    const shortId = deviceId.slice(0, 8);
    const dev = this.devices.get(shortId);
    return dev ? { ip: dev.ip, port: dev.port } : null;
  }

  private handleBeacon(data: Buffer, senderIP: string): void {
    try {
      const payload: BeaconPayload = JSON.parse(data.toString("utf8"));

      // Protocol version check
      if (payload.v !== 1) return;

      // Auth check: fingerprint must match
      if (!this.fingerprint || payload.th !== this.fingerprint) {
        // Beacon from a different user — ignore silently
        return;
      }

      // Device ID check: must be in our known device list
      if (!this.knownDeviceIds.has(payload.id)) {
        appLog("info", `[beacon] peer-seen: unknown device ${payload.id} from ${senderIP}`);
        return;
      }

      const isNew = !this.devices.has(payload.id);
      const existing = this.devices.get(payload.id);
      const ipChanged = existing && existing.ip !== senderIP;

      const device: DiscoveredDevice = {
        deviceId: payload.id,
        ip: senderIP,
        port: payload.p,
        name: payload.n,
        lastSeen: Date.now(),
        hwid: payload.hw,
      };

      this.devices.set(payload.id, device);

      if (isNew) {
        appLog("info", `[beacon] peer-matched: ${payload.n} (${payload.id}) at ${senderIP}:${payload.p}`);
        for (const cb of this.onDiscoveredCallbacks) cb(device);
      } else if (ipChanged) {
        appLog("info", `[beacon] peer-ip-changed: ${payload.id} ${existing!.ip} → ${senderIP}`);
        for (const cb of this.onDiscoveredCallbacks) cb(device);
      }
    } catch {
      // Malformed beacon — ignore
    }
  }

  private cleanupStale(): void {
    const now = Date.now();
    for (const [id, dev] of this.devices) {
      if (now - dev.lastSeen > BEACON_STALE_MS) {
        this.devices.delete(id);
        appLog("info", `[beacon] peer-lost: ${dev.name} (${id})`);
        for (const cb of this.onLostCallbacks) cb(id);
      }
    }
  }
}

/** Singleton beacon listener. */
export const beaconListener = new BeaconListener();
