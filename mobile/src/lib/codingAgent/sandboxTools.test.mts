// sandboxTools.test.mts — coding tool registry against an in-memory sandbox.
// Run: npx tsx src/lib/codingAgent/sandboxTools.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  CODING_TOOLS,
  dispatchCodingTool,
  codingToolByName,
  globToRegExp,
  type CodingSandbox,
  type CodingSandboxEntry,
} from "./sandboxTools.ts";

// In-memory CodingSandbox: a flat path→content map. Mirrors phoneSandboxSource
// semantics closely enough for the tools (readFile throws on missing, deleteFile
// is idempotent, listFiles returns sorted entries).
function memSandbox(initial: Record<string, string> = {}): CodingSandbox & { dump: () => Record<string, string> } {
  const files = new Map<string, string>(Object.entries(initial));
  return {
    async readFile(path) {
      if (!files.has(path)) throw new Error(`not found: ${path}`);
      return files.get(path)!;
    },
    async listFiles(): Promise<CodingSandboxEntry[]> {
      return [...files.entries()]
        .map(([path, content]) => ({ path, isDirectory: false, size: Buffer.byteLength(content) }))
        .sort((a, b) => a.path.localeCompare(b.path));
    },
    async writeFile(path, content) {
      files.set(path, content);
    },
    async deleteFile(path) {
      files.delete(path);
    },
    dump: () => Object.fromEntries(files),
  };
}

test("globToRegExp handles *, **, ? and literal segments", () => {
  assert.equal(globToRegExp(undefined), null);
  assert.equal(globToRegExp("")?.source ?? null, null);
  assert.ok(globToRegExp("*.tsx")!.test("App.tsx"));
  assert.ok(!globToRegExp("*.tsx")!.test("src/App.tsx")); // * doesn't cross /
  assert.ok(globToRegExp("**/*.tsx")!.test("src/ui/App.tsx"));
  assert.ok(globToRegExp("**/*.tsx")!.test("App.tsx")); // **/ collapses to optional
  assert.ok(globToRegExp("src/**")!.test("src/a/b/c.ts"));
  assert.ok(globToRegExp("a?.ts")!.test("ab.ts"));
  assert.ok(!globToRegExp("a?.ts")!.test("a/.ts")); // ? doesn't match /
});

test("registry exposes the six coding tools with correct mutating flags", () => {
  const names = CODING_TOOLS.map((t) => t.name).sort();
  assert.deepEqual(names, ["delete_file", "edit_file", "grep", "list_files", "read_file", "write_file"]);
  assert.equal(codingToolByName("read_file")!.mutating, false);
  assert.equal(codingToolByName("list_files")!.mutating, false);
  assert.equal(codingToolByName("grep")!.mutating, false);
  assert.equal(codingToolByName("write_file")!.mutating, true);
  assert.equal(codingToolByName("edit_file")!.mutating, true);
  assert.equal(codingToolByName("delete_file")!.mutating, true);
});

test("list_files returns files (no dirs/contents) and honours a glob", async () => {
  const box = memSandbox({ "App.tsx": "x", "ui/Button.tsx": "y", "data.json": "{}" });
  const all = (await dispatchCodingTool("list_files", {}, box)) as any;
  assert.equal(all.count, 3);
  assert.deepEqual(all.files.map((f: any) => f.path), ["App.tsx", "data.json", "ui/Button.tsx"]);
  const tsx = (await dispatchCodingTool("list_files", { glob: "**/*.tsx" }, box)) as any;
  assert.deepEqual(tsx.files.map((f: any) => f.path).sort(), ["App.tsx", "ui/Button.tsx"]);
});

test("read_file returns contents; missing file returns a structured error", async () => {
  const box = memSandbox({ "App.tsx": "line1\nline2\n" });
  const ok = (await dispatchCodingTool("read_file", { path: "App.tsx" }, box)) as any;
  assert.equal(ok.content, "line1\nline2\n");
  assert.equal(ok.lines, 3);
  assert.equal(ok.truncated, false);
  const missing = (await dispatchCodingTool("read_file", { path: "nope.ts" }, box)) as any;
  assert.match(missing.error, /cannot read nope\.ts/);
});

test("read_file truncates oversized files and flags it", async () => {
  const big = "a".repeat(70_000);
  const box = memSandbox({ "big.txt": big });
  const r = (await dispatchCodingTool("read_file", { path: "big.txt" }, box)) as any;
  assert.equal(r.truncated, true);
  assert.equal(r.totalBytes, 70_000);
  assert.ok(r.content.length < big.length);
});

test("grep finds matches with path:line and respects glob + bad-regex guard", async () => {
  const box = memSandbox({
    "a.ts": "const x = 1\nfunction foo() {}\n",
    "b.tsx": "function foo() { return null }\n",
  });
  const hits = (await dispatchCodingTool("grep", { pattern: "function\\s+foo" }, box)) as any;
  assert.equal(hits.count, 2);
  assert.deepEqual(hits.matches.map((m: any) => `${m.path}:${m.line}`).sort(), ["a.ts:2", "b.tsx:1"]);
  const only = (await dispatchCodingTool("grep", { pattern: "foo", glob: "*.tsx" }, box)) as any;
  assert.equal(only.matches.every((m: any) => m.path.endsWith(".tsx")), true);
  const bad = (await dispatchCodingTool("grep", { pattern: "(" }, box)) as any;
  assert.match(bad.error, /invalid regex/);
});

test("write_file creates and overwrites; reports byte length", async () => {
  const box = memSandbox();
  const r = (await dispatchCodingTool("write_file", { path: "new.ts", content: "hello" }, box)) as any;
  assert.equal(r.ok, true);
  assert.equal(r.bytes, 5);
  assert.equal(box.dump()["new.ts"], "hello");
  await dispatchCodingTool("write_file", { path: "new.ts", content: "bye" }, box);
  assert.equal(box.dump()["new.ts"], "bye");
});

test("edit_file does an anchored replace and refuses ambiguous/missing anchors", async () => {
  const box = memSandbox({ "App.tsx": "const title = 'old'\nconst other = 'old'\n" });

  // Ambiguous: 'old' appears twice → refused without replaceAll.
  const ambiguous = (await dispatchCodingTool(
    "edit_file",
    { path: "App.tsx", old: "'old'", new: "'new'" },
    box,
  )) as any;
  assert.match(ambiguous.error, /matches 2 times/);
  assert.equal(box.dump()["App.tsx"], "const title = 'old'\nconst other = 'old'\n"); // unchanged

  // Unique anchor with surrounding context → applied.
  const ok = (await dispatchCodingTool(
    "edit_file",
    { path: "App.tsx", old: "title = 'old'", new: "title = 'new'" },
    box,
  )) as any;
  assert.equal(ok.ok, true);
  assert.equal(ok.replaced, 1);
  assert.equal(box.dump()["App.tsx"], "const title = 'new'\nconst other = 'old'\n");

  // replaceAll handles the rest.
  const all = (await dispatchCodingTool(
    "edit_file",
    { path: "App.tsx", old: "'old'", new: "'X'", replaceAll: true },
    box,
  )) as any;
  assert.equal(all.replaced, 1);

  // Missing anchor → structured error, no write.
  const miss = (await dispatchCodingTool(
    "edit_file",
    { path: "App.tsx", old: "does-not-exist", new: "y" },
    box,
  )) as any;
  assert.match(miss.error, /not found/);
});

test("delete_file is idempotent and reports prior existence", async () => {
  const box = memSandbox({ "gone.ts": "x" });
  const first = (await dispatchCodingTool("delete_file", { path: "gone.ts" }, box)) as any;
  assert.equal(first.ok, true);
  assert.equal(first.existed, true);
  assert.equal(box.dump()["gone.ts"], undefined);
  const second = (await dispatchCodingTool("delete_file", { path: "gone.ts" }, box)) as any;
  assert.equal(second.ok, true);
  assert.equal(second.existed, false);
});

test("dispatchCodingTool throws on unknown tool name", async () => {
  const box = memSandbox();
  await assert.rejects(() => dispatchCodingTool("rm_rf", {}, box), /unknown coding tool/);
});
