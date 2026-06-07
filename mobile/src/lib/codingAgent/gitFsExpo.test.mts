// gitFsExpo.test.mts — proves the expo→isomorphic-git adapter by running REAL
// isomorphic-git (via sandboxGit) over an in-memory expo-file-system backend.
// If init→commit→log→revert round-trips here, the on-device adapter is correct.
// Run: npx tsx src/lib/codingAgent/gitFsExpo.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { makeGitFs, bytesToBase64, base64ToBytes, type ExpoFsBackend } from "./gitFsExpo.ts";
import {
  ensureRepo,
  isRepo,
  changedFiles,
  commitAll,
  log,
  headOid,
  revertTo,
} from "./sandboxGit.ts";

// ── In-memory backend mimicking the slice of expo-file-system we use ──────
function memoryExpoFs(): ExpoFsBackend {
  const files = new Map<string, { bytes: Uint8Array; mtime: number }>();
  const dirs = new Set<string>(["mem:///"]);
  const enc = new TextEncoder();
  const dec = new TextDecoder();
  const norm = (u: string) => u.replace(/\/+$/, "") || "mem:///";
  let clock = 1; // a real fs bumps mtime on write; constant mtime would trip ig's racy-git cache

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
      const bytes = opts.encoding === "base64" ? base64ToBytes(content) : enc.encode(content);
      files.set(u, { bytes, mtime: ++clock });
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
          const rest = k.slice(prefix.length);
          const name = rest.split("/")[0];
          if (name) names.add(name);
        }
      }
      return [...names];
    },
  };
}

// isomorphic-git operates on clean POSIX paths in a virtual root; the adapter
// maps them under baseUri (the expo documentDirectory). So `dir` is a plain
// POSIX path, NOT a file:// URI.
const BASE = "mem:///data/app/";
const DIR = "/proj";
const projUri = "mem:///data/app/proj";

async function writeFile(fs: any, rel: string, content: string) {
  // Use the adapter itself to write (mkdir parents first), exactly as the app would.
  const parts = rel.split("/");
  let cur = DIR;
  for (let i = 0; i < parts.length - 1; i++) {
    cur += "/" + parts[i];
    try {
      await fs.promises.mkdir(cur);
    } catch (e: any) {
      if (e.code !== "EEXIST") throw e;
    }
  }
  await fs.promises.writeFile(`${DIR}/${rel}`, content, { encoding: "utf8" });
}

test("base64 round-trips arbitrary bytes (git objects are binary)", () => {
  const bytes = new Uint8Array([0, 1, 2, 250, 255, 128, 64, 13, 10, 0]);
  assert.deepEqual(base64ToBytes(bytesToBase64(bytes)), bytes);
  // every byte value
  const all = new Uint8Array(256).map((_, i) => i);
  assert.deepEqual(base64ToBytes(bytesToBase64(all)), all);
});

test("init → commit → log → revert round-trips through REAL isomorphic-git", async () => {
  const backend = memoryExpoFs();
  const fs = makeGitFs(backend, BASE);
  const o = { fs, dir: DIR };

  // Project root pre-created with intermediates, as phoneSandboxSource does.
  await backend.makeDirectoryAsync(projUri, { intermediates: true });

  assert.equal(await isRepo(o), false);
  assert.equal(await ensureRepo(o), true);
  assert.equal(await isRepo(o), true);
  assert.equal(await headOid(o), null); // no commits yet

  // First commit.
  await writeFile(fs, "src/App.tsx", "export const A = 1;\n");
  const c1 = await commitAll(o, "first");
  assert.ok(c1, "first commit returns an oid");
  assert.equal((await changedFiles(o)).length, 0); // clean tree after commit

  // Second commit (modify).
  await writeFile(fs, "src/App.tsx", "export const A = 2;\n");
  const changes = await changedFiles(o);
  assert.deepEqual(changes, [{ path: "src/App.tsx", status: "modified" }]);
  const c2 = await commitAll(o, "second");
  assert.ok(c2 && c2 !== c1);

  // Log shows both, newest first.
  const entries = await log(o);
  assert.equal(entries.length, 2);
  assert.equal(entries[0].message.trim(), "second");
  assert.equal(entries[1].message.trim(), "first");

  // Revert to the first commit → file content goes back.
  await revertTo(o, c1!);
  const back = await fs.promises.readFile(`${DIR}/src/App.tsx`, { encoding: "utf8" });
  assert.equal(back, "export const A = 1;\n");
});

test("adapter raises ENOENT/EEXIST/ENOTDIR codes ig depends on", async () => {
  const backend = memoryExpoFs();
  const fs = makeGitFs(backend, "mem:///data/app/"); // root pre-seeded below
  await backend.makeDirectoryAsync("mem:///data/app", { intermediates: true });
  await assert.rejects(() => fs.promises.readFile("/nope"), (e: any) => e.code === "ENOENT");
  await assert.rejects(() => fs.promises.readdir("/nope"), (e: any) => e.code === "ENOENT");
  await fs.promises.mkdir("/d");
  await assert.rejects(() => fs.promises.mkdir("/d"), (e: any) => e.code === "EEXIST");
  await fs.promises.writeFile("/d/f", "x", { encoding: "utf8" });
  await assert.rejects(() => fs.promises.readdir("/d/f"), (e: any) => e.code === "ENOTDIR");
});
