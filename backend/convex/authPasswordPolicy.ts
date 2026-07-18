import { isOwnerEmail } from "./ownerAllowlist";

type Env = Record<string, string | undefined>;

export function parseBooleanEnv(value: string | undefined, fallback: boolean): boolean {
  if (value === undefined) return fallback;
  const v = value.trim().toLowerCase();
  if (["1", "true", "yes", "on"].includes(v)) return true;
  if (["0", "false", "no", "off"].includes(v)) return false;
  return fallback;
}

export function emailPasswordAuthEnabled(env: Env = process.env): boolean {
  return parseBooleanEnv(env.YAVER_EMAIL_PASSWORD_AUTH_ENABLED, false);
}

export function emailPasswordAllowedEmails(env: Env = process.env): string[] {
  return (env.YAVER_EMAIL_PASSWORD_AUTH_ALLOWED_EMAILS || "")
    .split(",")
    .map((item) => item.trim().toLowerCase())
    .filter(Boolean);
}

export function emailPasswordEmailAllowed(
  email: unknown,
  env: Env = process.env,
  ownerEmailCheck: (email?: string | null) => boolean = isOwnerEmail,
): boolean {
  const normalized = String(email || "").trim().toLowerCase();
  if (!normalized) return false;
  const explicit = emailPasswordAllowedEmails(env);
  if (explicit.length > 0) return explicit.includes(normalized);
  return ownerEmailCheck(normalized);
}

// ── Password Hashing Helpers (PBKDF2-SHA256) ────────────────────────

export async function hashPassword(password: string): Promise<string> {
  const encoder = new TextEncoder();
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const keyMaterial = await crypto.subtle.importKey(
    "raw",
    encoder.encode(password),
    "PBKDF2",
    false,
    ["deriveBits"]
  );
  const hash = await crypto.subtle.deriveBits(
    { name: "PBKDF2", salt, iterations: 100000, hash: "SHA-256" },
    keyMaterial,
    256
  );
  const saltB64 = btoa(String.fromCharCode(...salt));
  const hashB64 = btoa(String.fromCharCode(...new Uint8Array(hash)));
  return `${saltB64}:${hashB64}`;
}

export async function verifyPassword(password: string, stored: string): Promise<boolean> {
  const [saltB64, hashB64] = stored.split(":");
  if (!saltB64 || !hashB64) return false;
  const encoder = new TextEncoder();
  const salt = Uint8Array.from(atob(saltB64), (c) => c.charCodeAt(0));
  const expected = Uint8Array.from(atob(hashB64), (c) => c.charCodeAt(0));
  const keyMaterial = await crypto.subtle.importKey(
    "raw",
    encoder.encode(password),
    "PBKDF2",
    false,
    ["deriveBits"]
  );
  const hash = await crypto.subtle.deriveBits(
    { name: "PBKDF2", salt, iterations: 100000, hash: "SHA-256" },
    keyMaterial,
    256
  );
  const computed = new Uint8Array(hash);
  if (computed.length !== expected.length) return false;
  let diff = 0;
  for (let i = 0; i < computed.length; i++) {
    diff |= computed[i] ^ expected[i];
  }
  return diff === 0;
}
