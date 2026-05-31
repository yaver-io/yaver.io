// sandboxPrompt.test.mts — monorepo-aware prompt builder + mode inference.
// Run: npx tsx src/lib/localAgent/sandboxPrompt.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { buildSandboxPrompt, inferSandboxMode, type SandboxContext } from "./sandboxPrompt.ts";

const CTX: SandboxContext = {
  packages: [
    { name: "@app/web", path: "packages/web", role: "Next.js frontend" },
    { name: "@app/api", path: "packages/api", role: "Convex backend" },
  ],
  fileTree: ["packages/web/app/page.tsx", "packages/api/convex/schema.ts"],
  openFile: { path: "packages/web/app/page.tsx", contents: "export default function Page(){return null}" },
  targetPackage: "@app/web",
  stack: ["typescript", "react", "convex"],
};

test("includes monorepo workspace + marks the target package", () => {
  const p = buildSandboxPrompt(CTX, { instruction: "add a button", mode: "edit" });
  assert.match(p, /Monorepo workspace/);
  assert.match(p, /→ @app\/web/); // target marked
  assert.match(p, /Target package: @app\/web/);
});

test("includes stack, open file, request, and mode guidance", () => {
  const p = buildSandboxPrompt(CTX, { instruction: "add a button", mode: "edit" });
  assert.match(p, /typescript, react, convex/);
  assert.match(p, /Open file — packages\/web\/app\/page\.tsx/);
  assert.match(p, /Request: add a button/);
  assert.match(p, /Modify the open file/); // edit-mode guidance
});

test("single-package project omits the workspace block", () => {
  const p = buildSandboxPrompt({ openFile: { path: "index.ts", contents: "x" } }, { instruction: "go", mode: "edit" });
  assert.doesNotMatch(p, /Monorepo workspace/);
});

test("budget trim drops file tree first, keeps request + workspace", () => {
  const big: SandboxContext = {
    ...CTX,
    fileTree: Array.from({ length: 500 }, (_, i) => `packages/web/file${i}.tsx`),
    openFile: { path: "x.ts", contents: "a".repeat(50_000) },
  };
  const p = buildSandboxPrompt(big, { instruction: "fix it", mode: "fix" }, { maxChars: 2000 });
  assert.ok(p.length <= 2000 + 200); // within budget (+ small join slack)
  assert.match(p, /Request: fix it/); // request always survives
});

test("mode inference", () => {
  assert.equal(inferSandboxMode("explain what this does"), "explain");
  assert.equal(inferSandboxMode("fix the broken login"), "fix");
  assert.equal(inferSandboxMode("create a new settings page"), "scaffold");
  assert.equal(inferSandboxMode("rename the variable"), "edit");
});
