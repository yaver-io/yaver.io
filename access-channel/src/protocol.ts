export const MAX_SIGNAL_BYTES = 2048;
const ID = /^[A-Za-z0-9._:-]{1,128}$/;
const SIGNAL_KEYS = new Set([
  "version", "kind", "requestId", "targetDeviceId", "issuerDeviceId",
  "issuedAt", "expiresAt", "nonce", "provider",
]);
const KINDS = new Set([
  "requests.access", "requests.auth", "requests.wake",
  "events.presence", "events.auth", "events.access", "events.result",
]);

export type AccessRole = "controller" | "device";
export type AccessClaims = {
  role: AccessRole;
  tenant: string;
  broker: string;
  actor: string;
  device?: string;
  sub: string;
  jti: string;
  exp: number;
};
export type AccessSignal = {
  version: number;
  kind: string;
  requestId: string;
  targetDeviceId: string;
  issuerDeviceId: string;
  issuedAt: number;
  expiresAt: number;
  nonce: string;
  provider?: string;
};

export function parseSignal(text: string, claims: AccessClaims, now = Date.now()): AccessSignal {
  if (new TextEncoder().encode(text).byteLength > MAX_SIGNAL_BYTES) throw new Error("signal_too_large");
  let value: unknown;
  try { value = JSON.parse(text); } catch { throw new Error("invalid_json"); }
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_signal");
  const raw = value as Record<string, unknown>;
  if (Object.keys(raw).some((key) => !SIGNAL_KEYS.has(key))) throw new Error("unknown_signal_field");
  const signal = raw as unknown as AccessSignal;
  if (signal.version !== 1 || !KINDS.has(signal.kind) ||
      !ID.test(signal.requestId) || !ID.test(signal.targetDeviceId) ||
      !ID.test(signal.issuerDeviceId) || !ID.test(signal.nonce) ||
      typeof signal.issuedAt !== "number" || typeof signal.expiresAt !== "number") {
    throw new Error("invalid_signal");
  }
  if (signal.provider !== undefined && signal.provider !== "yaver") throw new Error("invalid_provider");
  if (signal.issuerDeviceId !== claims.actor) throw new Error("issuer_mismatch");
  if (signal.issuedAt > now + 120_000 || signal.expiresAt <= now ||
      signal.expiresAt <= signal.issuedAt || signal.expiresAt - signal.issuedAt > 600_000) {
    throw new Error("invalid_signal_lifetime");
  }
  if (claims.role === "controller" && !signal.kind.startsWith("requests.")) throw new Error("direction_denied");
  if (claims.role === "device" && (!signal.kind.startsWith("events.") || signal.targetDeviceId !== claims.device)) {
    throw new Error("direction_denied");
  }
  return signal;
}
