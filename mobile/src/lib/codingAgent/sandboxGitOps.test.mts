// sandboxGitOps.test.mts — the full git surface: pure conflict/diff helpers +
// a real branch→diverge→merge→conflict→resolve→commit cycle through isomorphic-
// git over the in-memory expo backend. Run: npx tsx src/lib/codingAgent/sandboxGitOps.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { makeGitFs, bytesToBase64, base64ToBytes, type ExpoFsBackend } from "./gitFsExpo.ts";
import { ensureRepo, commitAll, log } from "./sandboxGit.ts";
import {
  currentBranch,
  listBranches,
  createBranch,
  switchBranch,
  deleteBranch,
  mergeBranch,
  listConflicts,
  resolveConflict,
  completeMerge,
  hasConflictMarkers,
  parseConflictRegions,
  resolveAllRegions,
  diffStatus,
  fileLineDiff,
  lineDiff,
  runWithCheckpoints,
} from "./sandboxGitOps.ts";
import { revertTo } from "./sandboxGit.ts";

function memoryExpoFs(): ExpoFsBackend {
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

const BASE = "mem:///data/app/";
const DIR = "/proj";

async function setup() {
  const backend = memoryExpoFs();
  const fs = makeGitFs(backend, BASE);
  await backend.makeDirectoryAsync("mem:///data/app/proj", { intermediates: true });
  const o = { fs, dir: DIR };
  await ensureRepo(o);
  return o;
}

async function write(o: any, rel: string, content: string) {
  const parts = rel.split("/");
  let cur = DIR;
  for (let i = 0; i < parts.length - 1; i++) {
    cur += "/" + parts[i];
    try {
      await o.fs.promises.mkdir(cur);
    } catch (e: any) {
      if (e.code !== "EEXIST") throw e;
    }
  }
  await o.fs.promises.writeFile(`${DIR}/${rel}`, content, { encoding: "utf8" });
}

// ── pure helpers ──────────────────────────────────────────────────────

test("conflict-marker helpers parse + resolve regions", () => {
  const conflicted = [
    "top",
    "<<<<<<< HEAD",
    "ours-line",
    "=======",
    "theirs-line",
    ">>>>>>> feature",
    "bottom",
  ].join("\n");
  assert.ok(hasConflictMarkers(conflicted));
  const regions = parseConflictRegions(conflicted);
  assert.equal(regions.length, 1);
  assert.equal(regions[0].ours, "ours-line");
  assert.equal(regions[0].theirs, "theirs-line");
  assert.equal(regions[0].oursLabel, "HEAD");
  assert.equal(regions[0].theirsLabel, "feature");

  assert.equal(resolveAllRegions(conflicted, "ours"), "top\nours-line\nbottom");
  assert.equal(resolveAllRegions(conflicted, "theirs"), "top\ntheirs-line\nbottom");
  assert.ok(!hasConflictMarkers(resolveAllRegions(conflicted, "ours")));
});

test("lineDiff marks added/removed/context", () => {
  const d = lineDiff("a\nb\nc", "a\nB\nc\nd");
  assert.deepEqual(d, [
    { op: " ", text: "a" },
    { op: "-", text: "b" },
    { op: "+", text: "B" },
    { op: " ", text: "c" },
    { op: "+", text: "d" },
  ]);
});

// ── branches ──────────────────────────────────────────────────────────

test("branch create / list / switch / delete", async () => {
  const o = await setup();
  await write(o, "a.txt", "hello\n");
  await commitAll(o, "init");

  assert.equal(await currentBranch(o), "main");
  await createBranch(o, "feature", { checkout: true });
  assert.equal(await currentBranch(o), "feature");
  assert.deepEqual((await listBranches(o)).sort(), ["feature", "main"]);

  await switchBranch(o, "main");
  assert.equal(await currentBranch(o), "main");
  await deleteBranch(o, "feature");
  assert.deepEqual(await listBranches(o), ["main"]);
});

// ── the headline: merge → conflict → resolve → merge-commit ───────────

test("merge conflict is detected, resolvable, and completes as a 2-parent commit", async () => {
  const o = await setup();
  await write(o, "f.txt", "line1\nshared\nline3\n");
  await commitAll(o, "base");

  // feature edits the shared line one way…
  await createBranch(o, "feature", { checkout: true });
  await write(o, "f.txt", "line1\nFEATURE\nline3\n");
  await commitAll(o, "feature change");

  // …main edits the same line another way.
  await switchBranch(o, "main");
  await write(o, "f.txt", "line1\nMAIN\nline3\n");
  await commitAll(o, "main change");

  // Merge feature into main → conflict on the shared line.
  const res = await mergeBranch(o, "feature");
  assert.equal(res.status, "conflict");
  assert.deepEqual(res.conflicts, ["f.txt"]);
  assert.ok(res.theirsOid, "merge result carries theirs oid for the 2nd parent");

  // The working file now has markers; both sides present.
  const conflicted = (await o.fs.promises.readFile(`${DIR}/f.txt`, { encoding: "utf8" })) as string;
  assert.ok(hasConflictMarkers(conflicted));
  const regions = parseConflictRegions(conflicted);
  assert.ok(regions.some((r) => r.ours.includes("MAIN") && r.theirs.includes("FEATURE")));
  assert.deepEqual(await listConflicts(o), ["f.txt"]);

  // Resolve by keeping a hand-merged version, then complete the merge.
  await resolveConflict(o, "f.txt", "line1\nMAIN+FEATURE\nline3\n");
  assert.deepEqual(await listConflicts(o), []);
  const mergeOid = await completeMerge(o, "merge feature", { theirsOid: res.theirsOid });
  assert.ok(mergeOid);

  // History: the merge commit has TWO parents.
  const entries = await log(o);
  assert.equal(entries[0].message.trim(), "merge feature");
  const { default: git } = await import("isomorphic-git");
  const commit = await git.readCommit({ fs: o.fs, dir: o.dir, oid: mergeOid });
  assert.equal(commit.commit.parent.length, 2);

  // Final content is the resolved version, no markers.
  const final = (await o.fs.promises.readFile(`${DIR}/f.txt`, { encoding: "utf8" })) as string;
  assert.equal(final, "line1\nMAIN+FEATURE\nline3\n");
  assert.ok(!hasConflictMarkers(final));
});

test("clean (non-overlapping) merge fast-forwards/auto-commits without conflict", async () => {
  const o = await setup();
  await write(o, "f.txt", "a\nb\nc\n");
  await commitAll(o, "base");
  await createBranch(o, "feature", { checkout: true });
  await write(o, "g.txt", "new file\n");
  await commitAll(o, "add g");
  await switchBranch(o, "main");
  const res = await mergeBranch(o, "feature");
  assert.ok(res.status === "fast-forward" || res.status === "merged");
  assert.equal((await listConflicts(o)).length, 0);
});

// ── agentic run safety net ────────────────────────────────────────────

test("runWithCheckpoints brackets a run and supports one-tap revert", async () => {
  const o = await setup();
  await write(o, "app.txt", "original\n");
  await commitAll(o, "seed");

  // An "agentic run" that edits the file (as the loop's tools would).
  const { result, before, after, changed } = await runWithCheckpoints(o, "add feature", async () => {
    await write(o, "app.txt", "original\nfeature\n");
    await write(o, "new.txt", "added by agent\n");
    return "done";
  });

  assert.equal(result, "done");
  assert.ok(after, "after-checkpoint committed the run's output");
  assert.deepEqual(
    changed.map((c) => c.path).sort(),
    ["app.txt", "new.txt"],
  );
  // Tree was clean at "seed" → before is null (HEAD is already the restore point).
  assert.equal(before, null);
});

test("runWithCheckpoints revert restores pre-run state", async () => {
  const o = await setup();
  await write(o, "app.txt", "v1\n");
  // No prior commit → before-checkpoint will commit v1 as the restore point.
  const { before } = await runWithCheckpoints(o, "run", async () => {
    await write(o, "app.txt", "v1\nDANGER\n");
  });
  assert.ok(before);
  await revertTo(o, before!);
  const restored = (await o.fs.promises.readFile(`${DIR}/app.txt`, { encoding: "utf8" })) as string;
  assert.equal(restored, "v1\n");
});

// ── diff ──────────────────────────────────────────────────────────────

test("diffStatus + fileLineDiff reflect working-tree changes vs HEAD", async () => {
  const o = await setup();
  await write(o, "x.txt", "one\ntwo\n");
  await commitAll(o, "init");
  await write(o, "x.txt", "one\nTWO\nthree\n");
  await write(o, "y.txt", "brand new\n");

  const status = await diffStatus(o);
  assert.deepEqual(status, [
    { path: "x.txt", status: "modified" },
    { path: "y.txt", status: "added" },
  ]);

  const d = await fileLineDiff(o, "x.txt");
  assert.deepEqual(d, [
    { op: " ", text: "one" },
    { op: "-", text: "two" },
    { op: "+", text: "TWO" },
    { op: "+", text: "three" },
    { op: " ", text: "" }, // trailing newline → empty last line
  ]);
});
