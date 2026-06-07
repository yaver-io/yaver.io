// repoSandbox.test.mts — proves the whole-repo CodingSandbox over the same
// in-memory expo→isomorphic-git backend gitFsExpo.test uses. Confirms: recursive
// listing, nested-dir auto-create on write, read/edit/delete round-trips, .git is
// never exposed, and path-traversal is rejected.
// Run: npx tsx src/lib/codingAgent/repoSandbox.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { makeGitFs, bytesToBase64, base64ToBytes, type ExpoFsBackend } from "./gitFsExpo.ts";
import { makeRepoSandbox } from "./repoSandbox.ts";

// ── In-memory backend (same shape as gitFsExpo.test) ─────────────────────
// Base has a path component (mem:///data/app/) so first-level mkdir has a real
// parent — matching gitFsExpo.test's BASE convention.
const BASE = "mem:///data/app/";
const DIR = "/phone-projects/sfmg";

function memoryExpoFs(): ExpoFsBackend {
  const files = new Map<string, { bytes: Uint8Array; mtime: number }>();
  const dirs = new Set<string>(["mem:///", "mem:///data", "mem:///data/app"]);
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

function newSandbox() {
  const fs = makeGitFs(memoryExpoFs(), BASE);
  return makeRepoSandbox(fs as any, DIR);
}

test("write auto-creates nested dirs; read + list round-trip across the whole tree", async () => {
  const box = newSandbox();
  await box.writeFile("package.json", '{"name":"sfmg"}');
  await box.writeFile("src/App.tsx", "export default function App(){}");
  await box.writeFile("convex/schema.ts", "// schema");

  assert.equal(await box.readFile("convex/schema.ts"), "// schema");

  const files = (await box.listFiles()).filter((e) => !e.isDirectory).map((e) => e.path);
  assert.deepEqual(files.sort(), ["convex/schema.ts", "package.json", "src/App.tsx"]);
});

test("edit (overwrite) then delete is reflected; delete is idempotent", async () => {
  const box = newSandbox();
  await box.writeFile("src/a.ts", "v1");
  await box.writeFile("src/a.ts", "v2");
  assert.equal(await box.readFile("src/a.ts"), "v2");

  await box.deleteFile("src/a.ts");
  await assert.rejects(() => box.readFile("src/a.ts"));
  await box.deleteFile("src/a.ts"); // idempotent — no throw
});

test(".git internals are never listed or readable through the sandbox", async () => {
  const fs = makeGitFs(memoryExpoFs(), BASE);
  const box = makeRepoSandbox(fs as any, DIR);
  // Simulate git state on disk, written directly through the fs (bypassing the box).
  await fs.promises.mkdir("/phone-projects");
  await fs.promises.mkdir("/phone-projects/sfmg");
  await fs.promises.mkdir("/phone-projects/sfmg/.git");
  await fs.promises.writeFile("/phone-projects/sfmg/.git/HEAD", "ref: refs/heads/main", "utf8");
  await box.writeFile("README.md", "# sfmg");

  const paths = (await box.listFiles()).map((e) => e.path);
  assert.ok(paths.includes("README.md"));
  assert.ok(!paths.some((p) => p === ".git" || p.startsWith(".git/")), "git internals leaked into listing");

  await assert.rejects(() => box.readFile(".git/HEAD"), /\.git/);
  await assert.rejects(() => box.writeFile(".git/config", "x"), /\.git/);
});

test("path traversal is rejected", async () => {
  const box = newSandbox();
  await assert.rejects(() => box.readFile("../other/secret"));
  await assert.rejects(() => box.writeFile("../escape.ts", "x"));
});
