import nacl from "./secureNacl.ts";
import util from "tweetnacl-util";

const { decodeBase64, encodeBase64 } = util;

export const CREDENTIAL_HANDOFF_VERSION = 1 as const;
export const CREDENTIAL_HANDOFF_TTL_MS = 2 * 60 * 1000;
export const CREDENTIAL_HANDOFF_MAX_TTL_MS = 5 * 60 * 1000;
const MAX_SECRET_BYTES = 32 * 1024;
const QR_PREFIX = "yaver-credential:v1:";
const MAX_QR_LENGTH = 16 * 1024;

export const HANDOFF_CREDENTIAL_KINDS = [
  "deepseek-api-key",
  "openai-api-key",
  "anthropic-api-key",
  "glm-api-key",
  "github-token",
  "gitlab-token",
  "bitbucket-token",
  "hetzner-api-token",
] as const;

export type HandoffCredentialKind = (typeof HANDOFF_CREDENTIAL_KINDS)[number];
export type HandoffPayloadKind = HandoffCredentialKind | "machine-access-bundle";

export type CredentialHandoffErrorCode =
  | "HANDOFF_MALFORMED"
  | "HANDOFF_EXPIRED"
  | "HANDOFF_WRONG_ACCOUNT"
  | "HANDOFF_WRONG_DEVICE"
  | "HANDOFF_REPLAYED"
  | "HANDOFF_DECRYPT_FAILED"
  | "HANDOFF_UNSUPPORTED_CREDENTIAL";

export class CredentialHandoffError extends Error {
  readonly code: CredentialHandoffErrorCode;

  constructor(code: CredentialHandoffErrorCode, message: string) {
    super(message);
    this.name = "CredentialHandoffError";
    this.code = code;
  }
}

export interface CredentialHandoffRequest {
  version: typeof CREDENTIAL_HANDOFF_VERSION;
  type: "yaver-credential-request";
  handoffId: string;
  targetDeviceId: string;
  targetPublicKey: string;
  accountFingerprint: string;
  createdAt: number;
  expiresAt: number;
}

export interface CredentialHandoffEnvelope {
  version: typeof CREDENTIAL_HANDOFF_VERSION;
  type: "yaver-credential-envelope";
  handoffId: string;
  targetDeviceId: string;
  accountFingerprint: string;
  senderPublicKey: string;
  nonce: string;
  ciphertext: string;
}

interface EncryptedCredentialPayload {
  version: typeof CREDENTIAL_HANDOFF_VERSION;
  handoffId: string;
  targetDeviceId: string;
  accountFingerprint: string;
  createdAt: number;
  expiresAt: number;
  kind: HandoffPayloadKind;
  value: string;
}

export interface OpenCredentialHandoffOptions {
  expectedDeviceId: string;
  expectedAccountFingerprint: string;
  recipientSecretKey: Uint8Array;
  now?: number;
  consumedHandoffIds?: ReadonlySet<string>;
}

function fail(code: CredentialHandoffErrorCode, message: string): never {
  throw new CredentialHandoffError(code, message);
}

function bytes(value: string): Uint8Array {
  return new TextEncoder().encode(value);
}

function base64Url(value: Uint8Array): string {
  return encodeBase64(value).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function decodeKey(value: string, label: string): Uint8Array {
  try {
    const decoded = decodeBase64(value);
    if (decoded.length !== nacl.box.publicKeyLength) throw new Error("wrong length");
    return decoded;
  } catch {
    return fail("HANDOFF_MALFORMED", `${label} is not a valid X25519 public key.`);
  }
}

function isCredentialKind(value: unknown): value is HandoffPayloadKind {
  return value === "machine-access-bundle" || (typeof value === "string" && (HANDOFF_CREDENTIAL_KINDS as readonly string[]).includes(value));
}

/** Opaque account binding for direct transports; the raw account id never leaves the device. */
export function credentialAccountFingerprint(accountId: string): string {
  const normalized = accountId.trim();
  if (!normalized) fail("HANDOFF_MALFORMED", "A signed-in account is required for credential handoff.");
  return base64Url(nacl.hash(bytes(`yaver-credential-account-v1\0${normalized}`)).slice(0, 18));
}

export function createCredentialHandoffRequest(args: {
  targetDeviceId: string;
  targetPublicKey: Uint8Array;
  accountFingerprint: string;
  now?: number;
  ttlMs?: number;
  randomBytes?: (length: number) => Uint8Array;
}): CredentialHandoffRequest {
  const now = args.now ?? Date.now();
  const ttl = args.ttlMs ?? CREDENTIAL_HANDOFF_TTL_MS;
  if (!args.targetDeviceId.trim() || !args.accountFingerprint.trim()) {
    fail("HANDOFF_MALFORMED", "The target device and signed-in account are required.");
  }
  if (args.targetPublicKey.length !== nacl.box.publicKeyLength) {
    fail("HANDOFF_MALFORMED", "The target device key is invalid.");
  }
  if (!Number.isFinite(ttl) || ttl <= 0 || ttl > CREDENTIAL_HANDOFF_MAX_TTL_MS) {
    fail("HANDOFF_MALFORMED", "Credential handoff expiry must be within five minutes.");
  }
  const random = args.randomBytes ?? nacl.randomBytes;
  return {
    version: CREDENTIAL_HANDOFF_VERSION,
    type: "yaver-credential-request",
    handoffId: base64Url(random(18)),
    targetDeviceId: args.targetDeviceId.trim(),
    targetPublicKey: encodeBase64(args.targetPublicKey),
    accountFingerprint: args.accountFingerprint,
    createdAt: now,
    expiresAt: now + ttl,
  };
}

function validateRequest(request: CredentialHandoffRequest, now: number): Uint8Array {
  if (
    request?.version !== CREDENTIAL_HANDOFF_VERSION ||
    request.type !== "yaver-credential-request" ||
    !request.handoffId ||
    !request.targetDeviceId ||
    !request.accountFingerprint ||
    !Number.isFinite(request.createdAt) ||
    !Number.isFinite(request.expiresAt) ||
    request.expiresAt <= request.createdAt ||
    request.expiresAt - request.createdAt > CREDENTIAL_HANDOFF_MAX_TTL_MS
  ) {
    fail("HANDOFF_MALFORMED", "The credential request is invalid.");
  }
  if (now < request.createdAt - 30_000 || now >= request.expiresAt) {
    fail("HANDOFF_EXPIRED", "The credential request expired. Start a new handoff on the receiving device.");
  }
  return decodeKey(request.targetPublicKey, "The target device key");
}

export function sealCredentialForHandoff(args: {
  request: CredentialHandoffRequest;
  expectedAccountFingerprint: string;
  kind: HandoffPayloadKind;
  value: string;
  now?: number;
  senderKeyPair?: nacl.BoxKeyPair;
  nonce?: Uint8Array;
}): CredentialHandoffEnvelope {
  const now = args.now ?? Date.now();
  const recipientPublicKey = validateRequest(args.request, now);
  if (args.request.accountFingerprint !== args.expectedAccountFingerprint) {
    fail("HANDOFF_WRONG_ACCOUNT", "The receiving device is signed in to a different Yaver account.");
  }
  if (!isCredentialKind(args.kind)) {
    fail("HANDOFF_UNSUPPORTED_CREDENTIAL", "This credential type cannot be handed off.");
  }
  const value = args.value.trim();
  if (!value || bytes(value).length > MAX_SECRET_BYTES) {
    fail("HANDOFF_MALFORMED", "The credential is empty or too large.");
  }

  const sender = args.senderKeyPair ?? nacl.box.keyPair();
  const nonce = args.nonce ?? nacl.randomBytes(nacl.box.nonceLength);
  if (nonce.length !== nacl.box.nonceLength) fail("HANDOFF_MALFORMED", "The handoff nonce is invalid.");
  const payload: EncryptedCredentialPayload = {
    version: CREDENTIAL_HANDOFF_VERSION,
    handoffId: args.request.handoffId,
    targetDeviceId: args.request.targetDeviceId,
    accountFingerprint: args.request.accountFingerprint,
    createdAt: now,
    expiresAt: args.request.expiresAt,
    kind: args.kind,
    value,
  };
  const ciphertext = nacl.box(bytes(JSON.stringify(payload)), nonce, recipientPublicKey, sender.secretKey);
  return {
    version: CREDENTIAL_HANDOFF_VERSION,
    type: "yaver-credential-envelope",
    handoffId: args.request.handoffId,
    targetDeviceId: args.request.targetDeviceId,
    accountFingerprint: args.request.accountFingerprint,
    senderPublicKey: encodeBase64(sender.publicKey),
    nonce: encodeBase64(nonce),
    ciphertext: encodeBase64(ciphertext),
  };
}

export function openCredentialHandoff(
  envelope: CredentialHandoffEnvelope,
  options: OpenCredentialHandoffOptions,
): { handoffId: string; kind: HandoffPayloadKind; value: string; expiresAt: number } {
  if (
    envelope?.version !== CREDENTIAL_HANDOFF_VERSION ||
    envelope.type !== "yaver-credential-envelope" ||
    !envelope.handoffId ||
    !envelope.targetDeviceId ||
    !envelope.accountFingerprint
  ) {
    fail("HANDOFF_MALFORMED", "The credential envelope is invalid.");
  }
  if (envelope.targetDeviceId !== options.expectedDeviceId) {
    fail("HANDOFF_WRONG_DEVICE", "This credential was encrypted for a different device.");
  }
  if (envelope.accountFingerprint !== options.expectedAccountFingerprint) {
    fail("HANDOFF_WRONG_ACCOUNT", "This credential belongs to a different Yaver account.");
  }
  if (options.consumedHandoffIds?.has(envelope.handoffId)) {
    fail("HANDOFF_REPLAYED", "This one-time credential handoff was already used.");
  }
  if (options.recipientSecretKey.length !== nacl.box.secretKeyLength) {
    fail("HANDOFF_MALFORMED", "The receiving device key is invalid.");
  }

  let plaintext: Uint8Array | null = null;
  try {
    const senderPublicKey = decodeKey(envelope.senderPublicKey, "The sender key");
    const nonce = decodeBase64(envelope.nonce);
    if (nonce.length !== nacl.box.nonceLength) throw new Error("wrong nonce length");
    plaintext = nacl.box.open(decodeBase64(envelope.ciphertext), nonce, senderPublicKey, options.recipientSecretKey);
  } catch (error) {
    if (error instanceof CredentialHandoffError) throw error;
    plaintext = null;
  }
  if (!plaintext) fail("HANDOFF_DECRYPT_FAILED", "The credential envelope could not be authenticated.");

  let payload: EncryptedCredentialPayload;
  try {
    payload = JSON.parse(new TextDecoder().decode(plaintext)) as EncryptedCredentialPayload;
  } catch {
    return fail("HANDOFF_DECRYPT_FAILED", "The credential envelope could not be decoded.");
  }
  if (
    payload.version !== CREDENTIAL_HANDOFF_VERSION ||
    payload.handoffId !== envelope.handoffId ||
    payload.targetDeviceId !== envelope.targetDeviceId ||
    payload.accountFingerprint !== envelope.accountFingerprint ||
    !Number.isFinite(payload.createdAt) ||
    !Number.isFinite(payload.expiresAt) ||
    payload.expiresAt <= payload.createdAt ||
    payload.expiresAt - payload.createdAt > CREDENTIAL_HANDOFF_MAX_TTL_MS ||
    typeof payload.value !== "string" ||
    !payload.value ||
    bytes(payload.value).length > MAX_SECRET_BYTES
  ) {
    fail("HANDOFF_DECRYPT_FAILED", "The authenticated credential payload is inconsistent.");
  }
  const now = options.now ?? Date.now();
  if (now < payload.createdAt - 30_000 || now >= payload.expiresAt) {
    fail("HANDOFF_EXPIRED", "The credential handoff expired. Ask the sending device to approve it again.");
  }
  if (!isCredentialKind(payload.kind)) {
    fail("HANDOFF_UNSUPPORTED_CREDENTIAL", "This credential type cannot be stored by this app version.");
  }
  return { handoffId: payload.handoffId, kind: payload.kind, value: payload.value, expiresAt: payload.expiresAt };
}

/** Six digits shown on both devices before acceptance; it contains no secret material. */
export function credentialHandoffVerificationCode(
  request: CredentialHandoffRequest,
  envelope: CredentialHandoffEnvelope,
): string {
  if (request.handoffId !== envelope.handoffId || request.targetDeviceId !== envelope.targetDeviceId) {
    fail("HANDOFF_MALFORMED", "The request and response do not belong to the same handoff.");
  }
  const digest = nacl.hash(bytes([
    "yaver-credential-sas-v1",
    request.handoffId,
    request.targetDeviceId,
    request.targetPublicKey,
    request.accountFingerprint,
    envelope.senderPublicKey,
  ].join("\0")));
  const number = (((digest[0] << 16) | (digest[1] << 8) | digest[2]) >>> 0) % 1_000_000;
  return number.toString().padStart(6, "0");
}

export function encodeCredentialHandoffQr(
  payload: CredentialHandoffRequest | CredentialHandoffEnvelope,
): string {
  return `${QR_PREFIX}${base64Url(bytes(JSON.stringify(payload)))}`;
}

export function parseCredentialHandoffQr(
  value: string,
): CredentialHandoffRequest | CredentialHandoffEnvelope | null {
  if (!value.startsWith(QR_PREFIX) || value.length > MAX_QR_LENGTH) return null;
  try {
    const encoded = value.slice(QR_PREFIX.length).replace(/-/g, "+").replace(/_/g, "/");
    const padded = encoded + "=".repeat((4 - (encoded.length % 4)) % 4);
    const parsed = JSON.parse(new TextDecoder().decode(decodeBase64(padded))) as Record<string, unknown>;
    if (parsed.version !== CREDENTIAL_HANDOFF_VERSION) return null;
    if (parsed.type !== "yaver-credential-request" && parsed.type !== "yaver-credential-envelope") return null;
    return parsed as unknown as CredentialHandoffRequest | CredentialHandoffEnvelope;
  } catch {
    return null;
  }
}
