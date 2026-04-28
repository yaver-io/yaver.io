// phoneSandboxFsExpo.ts — the expo-file-system-backed implementation
// of SandboxFsAdapter. Lives in its own file so mobile-headless can
// import phoneSandboxSource.ts (and createSourceStore) without
// dragging expo-file-system into Bun's resolver.

import * as FileSystem from "expo-file-system";
import type { SandboxFsAdapter, SandboxFsInfo } from "./phoneSandboxFs";

export const expoFsAdapter: SandboxFsAdapter = {
  get documentDirectory(): string {
    const dir = FileSystem.documentDirectory;
    if (!dir) {
      throw new Error("expoFsAdapter: FileSystem.documentDirectory unavailable on this platform");
    }
    return dir;
  },

  async getInfo(uri: string): Promise<SandboxFsInfo> {
    const info = await FileSystem.getInfoAsync(uri);
    if (!info.exists) {
      return { exists: false, isDirectory: false, size: 0, modificationTime: 0 };
    }
    const isDir = !!(info as { isDirectory?: boolean }).isDirectory;
    return {
      exists: true,
      isDirectory: isDir,
      size: (info as { size?: number }).size ?? 0,
      modificationTime: (info as { modificationTime?: number }).modificationTime ?? 0,
    };
  },

  async readText(uri: string): Promise<string> {
    return FileSystem.readAsStringAsync(uri, {
      encoding: FileSystem.EncodingType.UTF8,
    });
  },

  async writeText(uri: string, content: string): Promise<void> {
    await FileSystem.writeAsStringAsync(uri, content, {
      encoding: FileSystem.EncodingType.UTF8,
    });
  },

  async remove(uri: string, opts: { idempotent: boolean }): Promise<void> {
    await FileSystem.deleteAsync(uri, { idempotent: opts.idempotent });
  },

  async mkdirp(uri: string): Promise<void> {
    await FileSystem.makeDirectoryAsync(uri, { intermediates: true }).catch((err: unknown) => {
      const message = String((err as { message?: string })?.message ?? err);
      if (message.includes("already exists") || message.includes("EEXIST")) return;
      throw err;
    });
  },

  async readDir(uri: string): Promise<string[]> {
    return FileSystem.readDirectoryAsync(uri);
  },

  async move(opts: { from: string; to: string }): Promise<void> {
    const renameFn = (FileSystem as unknown as { moveAsync?: (o: { from: string; to: string }) => Promise<void> }).moveAsync;
    if (typeof renameFn === "function") {
      await renameFn.call(FileSystem, opts);
      return;
    }
    const buf = await FileSystem.readAsStringAsync(opts.from, {
      encoding: FileSystem.EncodingType.UTF8,
    });
    await FileSystem.writeAsStringAsync(opts.to, buf, {
      encoding: FileSystem.EncodingType.UTF8,
    });
    await FileSystem.deleteAsync(opts.from, { idempotent: true });
  },
};
