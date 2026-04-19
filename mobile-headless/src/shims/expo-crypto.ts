// Shim for expo-crypto → node:crypto.
import * as crypto from "node:crypto";
export enum CryptoDigestAlgorithm { SHA256 = "sha256", SHA1 = "sha1", SHA512 = "sha512", MD5 = "md5" }
export enum CryptoEncoding { HEX = "hex", BASE64 = "base64" }
export async function digestStringAsync(algorithm: string, data: string, options?: { encoding?: string }) {
  const h = crypto.createHash(algorithm);
  h.update(data);
  return h.digest((options?.encoding as any) ?? "hex");
}
export function randomUUID(): string {
  return crypto.randomUUID();
}
export async function getRandomBytesAsync(n: number): Promise<Uint8Array> {
  return new Uint8Array(crypto.randomBytes(n));
}
export default { CryptoDigestAlgorithm, CryptoEncoding, digestStringAsync, randomUUID, getRandomBytesAsync };
