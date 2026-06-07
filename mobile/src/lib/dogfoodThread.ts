/**
 * dogfoodThread — the persisted "thread" of dogfood items (screenshots + notes
 * + their agent task) plus the dispatch logic that turns an item (or a batch)
 * into a coding task on the chosen dev box.
 *
 * Privacy: screenshots can contain UI data → thread + images stay on-device
 * (AsyncStorage + documentDirectory), and the image only ever leaves the phone
 * P2P as a task attachment to the user's own box. Never Convex.
 */

import AsyncStorage from "@react-native-async-storage/async-storage";
import * as FileSystem from "expo-file-system/legacy";
import { connectionManager } from "./connectionManager";
import type { ImageAttachment } from "./quic";
import type { DogfoodMode } from "./dogfoodConfig";

export type DogfoodItemStatus = "draft" | "sent" | "working" | "done" | "failed";

export interface DogfoodItem {
  id: string;
  /** Absolute path to the (annotated) JPEG in documentDirectory/dogfood/. */
  imagePath: string;
  caption: string;
  route?: string;
  /** Formatted breadcrumb trail captured at screenshot time. */
  breadcrumbs?: string;
  createdAt: number;
  mode: DogfoodMode;
  status: DogfoodItemStatus;
  taskId?: string;
  deviceId?: string;
  deviceName?: string;
  error?: string;
}

const MAX_ITEMS = 60;

function key(uid?: string | null): string {
  return uid ? `@yaver/u/${uid}/dogfood_thread` : "@yaver/dogfood_thread";
}

let currentUid: string | null = null;
let items: DogfoodItem[] = [];
const listeners = new Set<(items: DogfoodItem[]) => void>();

function emit(): void {
  const snapshot = items.slice();
  listeners.forEach((l) => {
    try {
      l(snapshot);
    } catch {
      // ignore
    }
  });
}

async function persist(): Promise<void> {
  try {
    await AsyncStorage.setItem(key(currentUid), JSON.stringify(items.slice(0, MAX_ITEMS)));
  } catch {
    // best-effort
  }
}

export function getDogfoodItems(): DogfoodItem[] {
  return items.slice();
}

export function subscribeDogfoodThread(cb: (items: DogfoodItem[]) => void): () => void {
  listeners.add(cb);
  cb(items.slice());
  return () => {
    listeners.delete(cb);
  };
}

export async function loadDogfoodThread(uid?: string | null): Promise<DogfoodItem[]> {
  currentUid = uid ?? null;
  try {
    const raw = await AsyncStorage.getItem(key(currentUid));
    items = raw ? (JSON.parse(raw) as DogfoodItem[]) : [];
  } catch {
    items = [];
  }
  emit();
  return items.slice();
}

export async function addDogfoodItem(item: DogfoodItem): Promise<void> {
  items = [item, ...items].slice(0, MAX_ITEMS);
  emit();
  await persist();
}

export async function updateDogfoodItem(id: string, patch: Partial<DogfoodItem>): Promise<void> {
  items = items.map((it) => (it.id === id ? { ...it, ...patch } : it));
  emit();
  await persist();
}

export async function removeDogfoodItem(id: string): Promise<void> {
  const target = items.find((it) => it.id === id);
  items = items.filter((it) => it.id !== id);
  emit();
  await persist();
  if (target?.imagePath) {
    try {
      await FileSystem.deleteAsync(target.imagePath, { idempotent: true });
    } catch {
      // ignore
    }
  }
}

export async function clearDogfoodThread(): Promise<void> {
  items = [];
  emit();
  await persist();
}

const DOGFOOD_IMAGE_DIR = `${FileSystem.documentDirectory}dogfood/`;

async function ensureImageDir(): Promise<void> {
  const info = await FileSystem.getInfoAsync(DOGFOOD_IMAGE_DIR);
  if (!info.exists) {
    await FileSystem.makeDirectoryAsync(DOGFOOD_IMAGE_DIR, { intermediates: true });
  }
}

/**
 * Persist a captured/annotated image (cache dir or tmp) into the durable
 * dogfood image dir so the thread survives cache eviction. Returns the new
 * absolute path. Accepts a source file path OR a base64 string.
 */
export async function persistDogfoodImage(opts: {
  id: string;
  sourcePath?: string;
  base64?: string;
}): Promise<string> {
  await ensureImageDir();
  const dest = `${DOGFOOD_IMAGE_DIR}${opts.id}.jpg`;
  if (opts.base64) {
    await FileSystem.writeAsStringAsync(dest, opts.base64, {
      encoding: FileSystem.EncodingType.Base64,
    });
  } else if (opts.sourcePath) {
    await FileSystem.copyAsync({ from: opts.sourcePath, to: dest });
  }
  return dest;
}

/**
 * Persist a captured/annotated screenshot into the thread as a draft item.
 * Shared by the global capture host and the screen's manual "＋ add" path.
 */
export async function stageDogfoodItem(opts: {
  shotPath: string;
  base64?: string;
  caption: string;
  mode: DogfoodMode;
  route?: string;
  breadcrumbs?: string;
}): Promise<DogfoodItem> {
  const id = `df_${Date.now().toString(36)}${Math.floor(Math.random() * 1e4).toString(36)}`;
  let imagePath = "";
  try {
    imagePath = opts.base64
      ? await persistDogfoodImage({ id, base64: opts.base64 })
      : await persistDogfoodImage({ id, sourcePath: opts.shotPath });
  } catch {
    imagePath = opts.shotPath;
  }
  const item: DogfoodItem = {
    id,
    imagePath,
    caption: opts.caption,
    route: opts.route,
    breadcrumbs: opts.breadcrumbs,
    createdAt: Date.now(),
    mode: opts.mode,
    status: "draft",
  };
  await addDogfoodItem(item);
  return item;
}

async function imageToAttachment(path: string, index: number): Promise<ImageAttachment> {
  const base64 = await FileSystem.readAsStringAsync(path, {
    encoding: FileSystem.EncodingType.Base64,
  });
  return { base64, mimeType: "image/jpeg", filename: `dogfood_${index}.jpg` };
}

function modePreamble(mode: DogfoodMode, repoDir: string): string {
  const common =
    "You are improving the Yaver mobile app itself (this repo). The attached " +
    "screenshot(s) show the running Yaver UI. Make the change(s) the user describes. " +
    "Prefer JS/TS-only changes so Hermes hot-reload can apply them instantly; keep " +
    "the mobile app loadable in the Yaver container (no WebView for RN); match " +
    "surrounding code style and keep the diff small.";
  if (mode === "pr") {
    return (
      common +
      ` Work in ${repoDir || "the yaver.io checkout"}. When done, commit to a new ` +
      "branch and open a GitHub pull request via `gh pr create`, then report the PR URL. " +
      "Do NOT push to main."
    );
  }
  return (
    common +
    " If a native change is unavoidable, say so explicitly — it needs a wire/TestFlight " +
    "build and can't Hermes-reload."
  );
}

function buildPrompt(opts: {
  items: DogfoodItem[];
  mode: DogfoodMode;
  repoDir: string;
  basePrompt: string;
}): string {
  const lines: string[] = [modePreamble(opts.mode, opts.repoDir)];
  if (opts.basePrompt.trim()) {
    lines.push("", opts.basePrompt.trim());
  }
  lines.push("", opts.items.length > 1 ? "Requests (one per screenshot):" : "Request:");
  opts.items.forEach((it, i) => {
    const ctx: string[] = [];
    if (it.route) ctx.push(`screen: ${it.route}`);
    if (it.breadcrumbs) ctx.push(`context: ${it.breadcrumbs}`);
    const head = opts.items.length > 1 ? `Screenshot ${i + 1}` : "Screenshot";
    const meta = ctx.length ? ` (${ctx.join("; ")})` : "";
    lines.push(`- ${head}${meta}: ${it.caption.trim() || "(see screenshot)"}`);
  });
  return lines.join("\n");
}

export interface DispatchResult {
  ok: boolean;
  taskId?: string;
  error?: string;
}

/**
 * Turn one or more dogfood items into a single coding task on `deviceId`,
 * attaching the screenshots as image attachments. Marks the items
 * sent→working with the resulting taskId. Returns the taskId on success.
 */
export async function dispatchDogfoodItems(opts: {
  items: DogfoodItem[];
  mode: DogfoodMode;
  deviceId: string;
  deviceName?: string;
  repoDir: string;
  basePrompt: string;
  runner: string;
  model?: string;
}): Promise<DispatchResult> {
  const { items: batch, mode, deviceId } = opts;
  if (!batch.length) return { ok: false, error: "Nothing to send." };
  const client = connectionManager.clientFor(deviceId);
  if (!client?.isConnected) {
    return { ok: false, error: "Selected box isn't connected." };
  }
  try {
    const images = await Promise.all(batch.map((it, i) => imageToAttachment(it.imagePath, i)));
    const prompt = buildPrompt({ items: batch, mode, repoDir: opts.repoDir, basePrompt: opts.basePrompt });
    const firstCaption = batch[0].caption.trim();
    const title =
      (mode === "pr" ? "Dogfood PR: " : "Dogfood: ") +
      (batch.length > 1
        ? `${batch.length} screenshots`
        : firstCaption
          ? firstCaption.slice(0, 48)
          : "screenshot");
    const task = await client.sendTask(
      title,
      prompt,
      opts.model,
      opts.runner,
      undefined,
      undefined,
      images,
      mode === "vibe" || mode === "pr" ? opts.repoDir || undefined : undefined,
      undefined,
      undefined,
      true, // codeMode
    );
    const taskId = (task as any)?.id as string | undefined;
    await Promise.all(
      batch.map((it) =>
        updateDogfoodItem(it.id, {
          status: "working",
          taskId,
          deviceId,
          deviceName: opts.deviceName,
          mode,
          error: undefined,
        }),
      ),
    );
    return { ok: true, taskId };
  } catch (err: any) {
    const msg = err?.message || "Failed to send to the agent.";
    await Promise.all(batch.map((it) => updateDogfoodItem(it.id, { status: "failed", error: msg })));
    return { ok: false, error: msg };
  }
}
