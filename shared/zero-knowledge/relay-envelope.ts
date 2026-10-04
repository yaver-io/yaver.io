/**
 * The complete application-visible contract accepted by Yaver-operated relay
 * code. Semantics such as message type, tool name, model, filename and command
 * belong inside `ciphertext` and are intentionally unrepresentable here.
 */
export type Ciphertext = Uint8Array & { readonly __brand: "Ciphertext" };

export interface RelayEnvelopeV1 {
  readonly version: 1;
  readonly sessionId: string;
  readonly destinationDeviceId: string;
  readonly senderDeviceId: string;
  readonly packetId: string;
  readonly sequence: string;
  readonly nonce: Uint8Array;
  readonly ciphertext: Ciphertext;
}

const OPAQUE_ID = /^[A-Za-z0-9_-]{16,128}$/;
const DECIMAL_SEQUENCE = /^(0|[1-9][0-9]{0,19})$/;
const OUTER_FIELDS = new Set([
  "version",
  "sessionId",
  "destinationDeviceId",
  "senderDeviceId",
  "packetId",
  "sequence",
  "nonce",
  "ciphertext",
]);

export function asCiphertext(bytes: Uint8Array): Ciphertext {
  return bytes as Ciphertext;
}

export function assertRelayEnvelope(value: unknown): asserts value is RelayEnvelopeV1 {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("invalid relay envelope");
  }
  const record = value as Record<string, unknown>;
  for (const field of Object.keys(record)) {
    if (!OUTER_FIELDS.has(field)) throw new Error(`forbidden relay field: ${field}`);
  }
  if (record.version !== 1) throw new Error("unsupported relay version");
  for (const field of ["sessionId", "destinationDeviceId", "senderDeviceId", "packetId"] as const) {
    if (typeof record[field] !== "string" || !OPAQUE_ID.test(record[field])) {
      throw new Error(`invalid opaque identifier: ${field}`);
    }
  }
  if (typeof record.sequence !== "string" || !DECIMAL_SEQUENCE.test(record.sequence)) {
    throw new Error("invalid sequence");
  }
  if (!(record.nonce instanceof Uint8Array) || record.nonce.byteLength !== 24) {
    throw new Error("invalid XChaCha20 nonce");
  }
  if (!(record.ciphertext instanceof Uint8Array) || record.ciphertext.byteLength < 16) {
    throw new Error("invalid ciphertext");
  }
}
