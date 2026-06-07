// Yaver BLE transport — the no-Wi-Fi link to a Pi edge widget.
//
// When mesh/LAN are unreachable (locked-down production floor), the phone reaches
// the Pi's Yaver agent over Bluetooth LE: the Pi runs the GATT bridge
// (hardware/yaver-edge-pi/ble-bridge/peripheral.py) which tunnels the agent's
// HTTP/ops API. This module is the phone side of GATT_PROTOCOL.md: scan → connect
// → chunked request/response → JSON. It forwards the phone's own Yaver bearer, so
// agent auth is identical to the IP path.
//
// linkCallOps() is the drop-in the UI uses: try IP first, fall back to BLE.

import { BleManager, Device } from "react-native-ble-plx";
import { Platform, PermissionsAndroid } from "react-native";
import { quicClient } from "./quic";

const SVC = "59415645-0001-4d65-7368-0000000000a0";
const REQ = "59415645-0003-4d65-7368-0000000000a0";
const RESP = "59415645-0004-4d65-7368-0000000000a0";
const HDR = 4; // [msgId][seq:2][flags]

type OpsResult = { ok?: boolean; error?: string; code?: string; initial?: any; via?: string };

let manager: BleManager | null = null;
let connected: Device | null = null;
let msgSeq = 1;

function mgr(): BleManager {
  if (!manager) manager = new BleManager();
  return manager;
}

// ── base64 (ble-plx characteristic values are base64) ──
const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
function toB64(bytes: number[]): string {
  let out = "";
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i], b1 = bytes[i + 1], b2 = bytes[i + 2];
    out += B64[b0 >> 2];
    out += B64[((b0 & 3) << 4) | ((b1 ?? 0) >> 4)];
    out += i + 1 < bytes.length ? B64[((b1 & 15) << 2) | ((b2 ?? 0) >> 6)] : "=";
    out += i + 2 < bytes.length ? B64[b2 & 63] : "=";
  }
  return out;
}
function fromB64(s: string): number[] {
  const clean = s.replace(/[^A-Za-z0-9+/]/g, "");
  const out: number[] = [];
  for (let i = 0; i < clean.length; i += 4) {
    const n = (B64.indexOf(clean[i]) << 18) | (B64.indexOf(clean[i + 1]) << 12) |
      (B64.indexOf(clean[i + 2]) << 6) | B64.indexOf(clean[i + 3]);
    out.push((n >> 16) & 0xff);
    if (clean[i + 2] !== undefined) out.push((n >> 8) & 0xff);
    if (clean[i + 3] !== undefined) out.push(n & 0xff);
  }
  return out;
}
const enc = (s: string): number[] => Array.from(new TextEncoder().encode(s));
const dec = (b: number[]): string => new TextDecoder().decode(new Uint8Array(b));

async function ensurePermissions(): Promise<boolean> {
  if (Platform.OS !== "android") return true; // iOS: NSBluetoothAlwaysUsageDescription in Info.plist
  const perms = [
    PermissionsAndroid.PERMISSIONS.BLUETOOTH_SCAN,
    PermissionsAndroid.PERMISSIONS.BLUETOOTH_CONNECT,
    PermissionsAndroid.PERMISSIONS.ACCESS_FINE_LOCATION,
  ].filter(Boolean) as string[];
  try {
    const res = await PermissionsAndroid.requestMultiple(perms as any);
    return Object.values(res).every((v) => v === PermissionsAndroid.RESULTS.GRANTED);
  } catch {
    return false;
  }
}

/** Scan for a Yaver-Edge box and connect (cached). Rejects on timeout. */
export async function bleConnect(timeoutMs = 8000): Promise<Device> {
  if (connected) {
    try {
      if (await connected.isConnected()) return connected;
    } catch {
      /* fallthrough to reconnect */
    }
    connected = null;
  }
  if (!(await ensurePermissions())) throw new Error("Bluetooth permission denied");

  const dev = await new Promise<Device>((resolve, reject) => {
    const t = setTimeout(() => {
      mgr().stopDeviceScan();
      reject(new Error("no Yaver-Edge box found over BLE"));
    }, timeoutMs);
    mgr().startDeviceScan([SVC], null, (err, d) => {
      if (err) {
        clearTimeout(t);
        mgr().stopDeviceScan();
        reject(err);
        return;
      }
      if (d) {
        clearTimeout(t);
        mgr().stopDeviceScan();
        resolve(d);
      }
    });
  });

  let device = await dev.connect();
  device = await device.discoverAllServicesAndCharacteristics();
  try {
    device = await device.requestMTU(247);
  } catch {
    /* keep default MTU */
  }
  connected = device;
  return device;
}

/** Tunnel one agent HTTP request over BLE. */
export async function bleFetch(
  method: string,
  path: string,
  headers: Record<string, string>,
  body: string,
): Promise<{ status: number; body: string }> {
  const device = await bleConnect();
  const mtu = (device as any).mtu || 23;
  const payloadMax = Math.max(16, mtu - 3 - HDR);
  const id = msgSeq++ & 0xff;

  const msg = enc(JSON.stringify({ id, method, path, headers, body }));

  return new Promise<{ status: number; body: string }>((resolve, reject) => {
    const chunks: Record<number, number[]> = {};
    let lastSeq = -1;
    const timer = setTimeout(() => {
      sub.remove();
      reject(new Error("BLE response timeout"));
    }, 30000);

    const sub = device.monitorCharacteristicForService(SVC, RESP, (err, ch) => {
      if (err) {
        clearTimeout(timer);
        sub.remove();
        reject(err);
        return;
      }
      if (!ch?.value) return;
      const frame = fromB64(ch.value);
      if (frame.length < HDR || frame[0] !== id) return; // not our message
      const seq = (frame[1] << 8) | frame[2];
      const isLast = frame[3] & 1;
      chunks[seq] = frame.slice(HDR);
      if (isLast) lastSeq = seq;
      if (lastSeq >= 0 && Object.keys(chunks).length === lastSeq + 1) {
        clearTimeout(timer);
        sub.remove();
        let all: number[] = [];
        for (let i = 0; i <= lastSeq; i++) all = all.concat(chunks[i]);
        try {
          const env = JSON.parse(dec(all));
          resolve({ status: env.status ?? 0, body: env.body ?? "" });
        } catch (e) {
          reject(e as Error);
        }
      }
    });

    // write request chunks sequentially after the notify subscription is armed
    (async () => {
      try {
        const total = Math.max(1, Math.ceil(msg.length / payloadMax));
        for (let seq = 0; seq < total; seq++) {
          const part = msg.slice(seq * payloadMax, (seq + 1) * payloadMax);
          const last = seq === total - 1 ? 1 : 0;
          const frame = [id, (seq >> 8) & 0xff, seq & 0xff, last, ...part];
          await device.writeCharacteristicWithoutResponseForService(SVC, REQ, toB64(frame));
        }
      } catch (e) {
        clearTimeout(timer);
        sub.remove();
        reject(e as Error);
      }
    })();
  });
}

/** callOps over BLE (mirrors quicClient.callOps shape). */
export async function bleCallOps(verb: string, payload: Record<string, unknown>): Promise<OpsResult> {
  const headers = { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" };
  const r = await bleFetch("POST", "/ops", headers, JSON.stringify({ verb, payload, machine: "local" }));
  try {
    return { ...(JSON.parse(r.body) as OpsResult), via: "ble" };
  } catch {
    return { ok: false, error: `BLE ops ${verb}: HTTP ${r.status}`, via: "ble" };
  }
}

/** Resilient: try the IP path; on a connectivity failure, fall back to BLE. */
export async function linkCallOps(
  verb: string,
  payload: Record<string, unknown>,
  ipAttempt: () => Promise<OpsResult>,
): Promise<OpsResult> {
  try {
    const r = await ipAttempt();
    if (r?.ok) return { ...r, via: "ip" };
    // IP reachable but verb failed for a real reason (not connectivity) — keep it.
    if (r?.error && !/no relay|no active agent|timeout|failed: 5\d\d|network|unreachable|connect/i.test(r.error)) {
      return { ...r, via: "ip" };
    }
  } catch {
    /* fall through to BLE */
  }
  try {
    return await bleCallOps(verb, payload);
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : String(e), via: "ble" };
  }
}

export function bleDisconnect(): void {
  if (connected) {
    connected.cancelConnection().catch(() => {});
    connected = null;
  }
}
