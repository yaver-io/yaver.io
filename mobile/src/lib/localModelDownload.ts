// localModelDownload.ts — the native side of the on-device model download
// pipeline. Resumable fetch (expo-file-system) → cache at the SAME
// localModelPath() codingBackendStore loads from → light GGUF-magic sanity
// check. Drives the pure downloadState.ts transitions via the injected
// callbacks; the UI renders the snapshot. NOT tsx-tested (expo-file-system).
//
// Hosting is GitHub Releases (kivanccakmak/yaver-models); models.ts carries the
// URL + (eventual) sha256. Full sha256 verification of a multi-GB file needs a
// streaming native hash we don't have yet, so today we verify the GGUF magic
// bytes + a non-trivial size and note the sha gap; once a native hash lands the
// markReady step adds it. Until the GGUFs are published the download fails
// gracefully (404 → markFailed, retryable).

import * as FileSystem from "expo-file-system";

import { looksLikeGGUF } from "./localAgent/engine";
import { localModelPath, localModelsDir } from "./codingBackendStore";
import { MODEL_REGISTRY, type ModelEntry } from "./localAgent/models";

const FS = FileSystem as any;

async function ensureDir(): Promise<void> {
  const dir = localModelsDir();
  if (!dir) throw new Error("model cache directory unavailable on this platform");
  await FileSystem.makeDirectoryAsync(dir, { intermediates: true }).catch((err: unknown) => {
    const msg = String((err as { message?: string })?.message ?? err);
    if (msg.includes("already exists") || msg.includes("EEXIST")) return;
    throw err;
  });
}

/** Decode the first up-to-4 bytes of a base64 head into a byte array. */
function base64Head(b64: string, n: number): number[] {
  const TABLE = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  const out: number[] = [];
  let buffer = 0;
  let bits = 0;
  for (const ch of b64) {
    const v = TABLE.indexOf(ch);
    if (v < 0) continue; // skip padding/whitespace
    buffer = (buffer << 6) | v;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out.push((buffer >> bits) & 0xff);
      if (out.length >= n) break;
    }
  }
  return out;
}

/** Best-effort: does the downloaded file start with the GGUF magic? Returns
 *  true when we couldn't read the head (don't false-fail a real file). */
async function looksValidGGUF(fileUri: string): Promise<boolean> {
  try {
    const head = await FileSystem.readAsStringAsync(fileUri, {
      encoding: (FS.EncodingType?.Base64 ?? "base64") as any,
      position: 0,
      length: 4,
    });
    const bytes = base64Head(head, 4);
    if (bytes.length < 4) return true; // couldn't read enough → don't block
    return looksLikeGGUF(bytes);
  } catch {
    return true; // head read unsupported on this platform → skip the check
  }
}

export interface DownloadCallbacks {
  onStart?: (id: string) => void;
  onProgress?: (id: string, received: number, total: number) => void;
  onVerifying?: (id: string) => void;
  onReady?: (id: string) => void;
  onFailed?: (id: string, error: string) => void;
}

/**
 * Download a model GGUF to the local cache. Resolves true on success. Safe to
 * call only after the UI has confirmed the device can run it (RAM gate).
 */
export async function startModelDownload(
  model: ModelEntry,
  cb: DownloadCallbacks = {},
): Promise<boolean> {
  if (model.bundled) {
    cb.onReady?.(model.id);
    return true; // bundled models ship in the binary — nothing to fetch
  }
  if (!model.downloadUrl) {
    cb.onFailed?.(model.id, "model has no download URL");
    return false;
  }
  try {
    await ensureDir();
    const fileUri = localModelPath(model.id);
    cb.onStart?.(model.id);

    const task = FileSystem.createDownloadResumable(
      model.downloadUrl,
      fileUri,
      {},
      (p: { totalBytesWritten: number; totalBytesExpectedToWrite: number }) => {
        cb.onProgress?.(model.id, p.totalBytesWritten, p.totalBytesExpectedToWrite);
      },
    );

    const result = await task.downloadAsync();
    const status = (result as { status?: number } | undefined)?.status;
    if (!result || (status != null && status >= 400)) {
      await FileSystem.deleteAsync(fileUri, { idempotent: true }).catch(() => {});
      cb.onFailed?.(model.id, status != null ? `download failed (HTTP ${status})` : "download failed");
      return false;
    }

    cb.onVerifying?.(model.id);
    const info = await FileSystem.getInfoAsync(fileUri);
    const size = (info as { size?: number }).size ?? 0;
    if (!info.exists || size < 1_000_000) {
      await FileSystem.deleteAsync(fileUri, { idempotent: true }).catch(() => {});
      cb.onFailed?.(model.id, "downloaded file is too small / incomplete");
      return false;
    }
    if (!(await looksValidGGUF(fileUri))) {
      await FileSystem.deleteAsync(fileUri, { idempotent: true }).catch(() => {});
      cb.onFailed?.(model.id, "downloaded file is not a valid GGUF model");
      return false;
    }

    cb.onReady?.(model.id);
    return true;
  } catch (e: any) {
    cb.onFailed?.(model.id, String(e?.message ?? e));
    return false;
  }
}

/** Remove a downloaded model file to reclaim space. */
export async function deleteModelFile(id: string): Promise<void> {
  await FileSystem.deleteAsync(localModelPath(id), { idempotent: true }).catch(() => {});
}

/** Which registry models are present in the cache (downloaded + bundled). */
export async function installedModelIds(): Promise<string[]> {
  const out: string[] = [];
  for (const m of MODEL_REGISTRY) {
    if (m.bundled) {
      out.push(m.id);
      continue;
    }
    try {
      const info = await FileSystem.getInfoAsync(localModelPath(m.id));
      if (info.exists && ((info as { size?: number }).size ?? 0) > 0) out.push(m.id);
    } catch {
      // ignore
    }
  }
  return out;
}
