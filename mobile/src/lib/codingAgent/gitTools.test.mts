// gitTools.test.mts — the agentic git tool surface: dispatch a few tools as the
// coding loop would and confirm they drive real git operations end-to-end.
// Run: npx tsx src/lib/codingAgent/gitTools.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { makeGitFs } from "./gitFsExpo.ts";
import { memoryExpoFs } from "./_memoryExpoFs.ts";
import { ensureRepo } from "./sandboxGit.ts";
import { makeGitTools, gitToolNames } from "./gitTools.ts";
import type { CodingSandbox, CodingTool } from "./sandboxTools.ts";

const BASE = "mem:///data/app/";
const DIR = "/proj";

// The git tools ignore the box; a no-op satisfies the type.
const NOOP_BOX = {
  readFile: async () => "",
  listFiles: async () => [],
  writeFile: async () => {},
  deleteFile: async () => {},
} as CodingSandbox;

async function setup() {
  const backend = memoryExpoFs();
  const fs = makeGitFs(backend, BASE);
  await backend.makeDirectoryAsync("mem:///data/app/proj", { intermediates: true });
  const git = { fs, dir: DIR };
  await ensureRepo(git);
  return git;
}

function tool(tools: CodingTool[], name: string): CodingTool {
  const t = tools.find((x) => x.name === name);
  if (!t) throw new Error(`tool ${name} not found`);
  return t;
}

async function write(git: any, rel: string, content: string) {
  await git.fs.promises.writeFile(`${DIR}/${rel}`, content, { encoding: "utf8" });
}

test("git_push is omitted without a network context, present with one", () => {
  const git = { fs: {} as any, dir: DIR };
  assert.ok(!makeGitTools(git).some((t) => t.name === "git_push"));
  assert.ok(makeGitTools(git, { http: {} as any }).some((t) => t.name === "git_push"));
  assert.equal(gitToolNames(false).includes("git_push"), false);
  assert.equal(gitToolNames(true).includes("git_push"), true);
});

test("mutating flags are set so the runner gates them", () => {
  const tools = makeGitTools({ fs: {} as any, dir: DIR });
  assert.equal(tool(tools, "git_status").mutating, false);
  assert.equal(tool(tools, "git_diff").mutating, false);
  assert.equal(tool(tools, "git_commit").mutating, true);
  assert.equal(tool(tools, "git_merge").mutating, true);
  assert.equal(tool(tools, "git_resolve_conflict").mutating, true);
});

test("agentic flow: commit → branch → status via tools", async () => {
  const git = await setup();
  const tools = makeGitTools(git);

  await write(git, "a.txt", "v1\n");
  const c = (await tool(tools, "git_commit").invoke({ message: "init" }, NOOP_BOX)) as { oid: string | null };
  assert.ok(c.oid, "git_commit returns an oid");

  const br = (await tool(tools, "git_branch").invoke({ action: "create", name: "feature", checkout: true }, NOOP_BOX)) as {
    ok: boolean;
    current: string;
  };
  assert.equal(br.current, "feature");

  await write(git, "a.txt", "v2\n");
  const st = (await tool(tools, "git_status").invoke({}, NOOP_BOX)) as {
    branch: string;
    changes: Array<{ path: string; status: string }>;
  };
  assert.equal(st.branch, "feature");
  assert.deepEqual(st.changes, [{ path: "a.txt", status: "modified" }]);
});

test("agentic conflict resolution: git_merge → git_resolve_conflict → git_complete_merge", async () => {
  const git = await setup();
  const tools = makeGitTools(git);

  await write(git, "f.txt", "x\nshared\ny\n");
  await tool(tools, "git_commit").invoke({ message: "base" }, NOOP_BOX);
  await tool(tools, "git_branch").invoke({ action: "create", name: "feature", checkout: true }, NOOP_BOX);
  await write(git, "f.txt", "x\nFEAT\ny\n");
  await tool(tools, "git_commit").invoke({ message: "feat" }, NOOP_BOX);
  await tool(tools, "git_branch").invoke({ action: "switch", name: "main" }, NOOP_BOX);
  await write(git, "f.txt", "x\nMAIN\ny\n");
  await tool(tools, "git_commit").invoke({ message: "main" }, NOOP_BOX);

  const merge = (await tool(tools, "git_merge").invoke({ branch: "feature" }, NOOP_BOX)) as {
    status: string;
    conflicts?: string[];
    theirsOid?: string;
  };
  assert.equal(merge.status, "conflict");
  assert.deepEqual(merge.conflicts, ["f.txt"]);

  const conflicts = (await tool(tools, "git_list_conflicts").invoke({}, NOOP_BOX)) as { conflicts: string[] };
  assert.deepEqual(conflicts.conflicts, ["f.txt"]);

  const r = (await tool(tools, "git_resolve_conflict").invoke(
    { path: "f.txt", content: "x\nMAIN+FEAT\ny\n" },
    NOOP_BOX,
  )) as { ok: boolean; remaining: string[] };
  assert.deepEqual(r.remaining, []);

  const done = (await tool(tools, "git_complete_merge").invoke(
    { message: "merge feature", theirsOid: merge.theirsOid },
    NOOP_BOX,
  )) as { oid: string };
  assert.ok(done.oid);
});
