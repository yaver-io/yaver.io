import type { AccessClaims } from "./protocol";

type Jwk = JsonWebKey & { kid?: string; alg?: string; kty?: string };
let cache: { until: number; keys: Jwk[] } | undefined;

function decodePart(part: string): Uint8Array {
  const normalized = part.replace(/-/g, "+").replace(/_/g, "/");
  const bytes = atob(normalized + "=".repeat((4 - normalized.length % 4) % 4));
  return Uint8Array.from(bytes, (char) => char.charCodeAt(0));
}

function decodeJson(part: string): Record<string, unknown> {
  return JSON.parse(new TextDecoder().decode(decodePart(part))) as Record<string, unknown>;
}

async function keys(jwksUrl: string): Promise<Jwk[]> {
  if (cache && cache.until > Date.now()) return cache.keys;
  const response = await fetch(jwksUrl, { headers: { Accept: "application/json" } });
  if (!response.ok) throw new Error("jwks_unavailable");
  const body = await response.json() as { keys?: Jwk[] };
  if (!Array.isArray(body.keys) || body.keys.length === 0) throw new Error("jwks_unavailable");
  cache = { keys: body.keys, until: Date.now() + 300_000 };
  return body.keys;
}

export function bearerFromProtocols(value: string | null): string {
  const protocols = (value || "").split(",").map((item) => item.trim());
  if (!protocols.includes("yaver-access-v1")) throw new Error("protocol_required");
  const auth = protocols.find((item) => item.startsWith("auth."));
  if (!auth || auth.length > 8192) throw new Error("credential_required");
  return auth.slice(5);
}

export async function verifyAccessJwt(token: string, jwksUrl: string, issuer: string): Promise<AccessClaims> {
  const parts = token.split(".");
  if (parts.length !== 3) throw new Error("invalid_credential");
  const header = decodeJson(parts[0]);
  const payload = decodeJson(parts[1]);
  if (header.alg !== "EdDSA" || typeof header.kid !== "string") throw new Error("invalid_credential");
  const jwk = (await keys(jwksUrl)).find((candidate) => candidate.kid === header.kid && candidate.kty === "OKP");
  if (!jwk) throw new Error("unknown_signing_key");
  const key = await crypto.subtle.importKey("jwk", jwk, { name: "Ed25519" }, false, ["verify"]);
  const valid = await crypto.subtle.verify("Ed25519", key, decodePart(parts[2]), new TextEncoder().encode(`${parts[0]}.${parts[1]}`));
  if (!valid) throw new Error("invalid_credential");
  const now = Math.floor(Date.now() / 1000);
  const lifetimeLimit = payload.role === "device" ? 3610 : 310;
  if (payload.iss !== issuer || typeof payload.exp !== "number" || payload.exp <= now ||
      typeof payload.nbf !== "number" || payload.nbf > now + 10 ||
      typeof payload.iat !== "number" || payload.iat > now + 10 || payload.exp - payload.iat > lifetimeLimit ||
      (payload.role !== "controller" && payload.role !== "device") ||
      typeof payload.tenant !== "string" || !/^act_[a-f0-9]{32}$/.test(payload.tenant) ||
      typeof payload.broker !== "string" || typeof payload.sub !== "string" ||
      typeof payload.jti !== "string" || typeof payload.actor !== "string") {
    throw new Error("invalid_credential");
  }
  if (payload.aud !== payload.broker) throw new Error("invalid_credential");
  if (payload.role === "device" && (typeof payload.device !== "string" || payload.actor !== payload.device)) {
    throw new Error("invalid_credential");
  }
  return payload as unknown as AccessClaims;
}
