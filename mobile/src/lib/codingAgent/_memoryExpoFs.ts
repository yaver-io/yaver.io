// _memoryExpoFs.ts — shared TEST helper (underscore prefix → not a test file): an
// in-memory backend implementing the ExpoFsBackend slice, used to run REAL
// isomorphic-git in headless tsx tests. Not shipped to the app.

import { bytesToBase64, base64ToBytes, type ExpoFsBackend } from "./gitFsExpo";

export function memoryExpoFs(): ExpoFsBackend {
  const files = new Map<string, { bytes: Uint8Array; mtime: number }>();
  const dirs = new Set<string>(["mem:///"]);
  const enc = new TextEncoder();
  const dec = new TextDecoder();
  const norm = (u: string) => u.replace(/\/+$/, "") || "mem:///";
  let clock = 1;
  return {
    EncodingType: { UTF8: "utf8", Base64: "base64" },
    async getInfoAsync(uri) {
      const u = norm(uri);
      if (dirs.has(u)) return { exists: true, isDirectory: true, size: 0, modificationTime: 1 };
      const f = files.get(u);
      if (f) return { exists: true, isDirectory: false, size: f.bytes.length, modificationTime: f.mtime };
      return { exists: false };
    },
    async readAsStringAsync(uri, opts) {
      const f = files.get(norm(uri));
      if (!f) throw new Error("ENOENT");
      return opts.encoding === "base64" ? bytesToBase64(f.bytes) : dec.decode(f.bytes);
    },
    async writeAsStringAsync(uri, content, opts) {
      const u = norm(uri);
      const parent = u.replace(/\/[^/]*$/, "") || "mem:///";
      if (!dirs.has(parent)) throw new Error("ENOENT parent");
      files.set(u, { bytes: opts.encoding === "base64" ? base64ToBytes(content) : enc.encode(content), mtime: ++clock });
    },
    async deleteAsync(uri) {
      const u = norm(uri);
      files.delete(u);
      if (dirs.has(u)) {
        for (const d of [...dirs]) if (d.startsWith(u + "/")) dirs.delete(d);
        for (const f of [...files.keys()]) if (f.startsWith(u + "/")) files.delete(f);
        dirs.delete(u);
      }
    },
    async makeDirectoryAsync(uri, opts) {
      const u = norm(uri);
      if (opts?.intermediates) {
        const parts = u.replace("mem:///", "").split("/").filter(Boolean);
        let cur = "mem://";
        for (const p of parts) {
          cur += "/" + p;
          dirs.add(cur);
        }
        return;
      }
      const parent = u.replace(/\/[^/]*$/, "") || "mem:///";
      if (!dirs.has(parent)) throw new Error("ENOENT parent");
      if (dirs.has(u) || files.has(u)) throw new Error("EEXIST");
      dirs.add(u);
    },
    async readDirectoryAsync(uri) {
      const u = norm(uri);
      const prefix = u + "/";
      const names = new Set<string>();
      for (const k of [...files.keys(), ...dirs]) {
        if (k.startsWith(prefix)) {
          const name = k.slice(prefix.length).split("/")[0];
          if (name) names.add(name);
        }
      }
      return [...names];
    },
  };
}
