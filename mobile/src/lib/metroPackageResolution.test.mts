import assert from "node:assert/strict";
import { createRequire } from "node:module";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);

test("Metro keeps isomorphic-git on its portable ESM entries", () => {
  const config = require("../../metro.config.js");
  const requested: string[] = [];
  const context = {
    resolveRequest(_context: unknown, moduleName: string) {
      requested.push(moduleName);
      return { filePath: moduleName, type: "sourceFile" };
    },
  };

  config.resolver.resolveRequest(context, "isomorphic-git", "ios");
  config.resolver.resolveRequest(context, "isomorphic-git/http/web", "ios");

  const mobileRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
  assert.deepEqual(requested, [
    path.join(mobileRoot, "node_modules", "isomorphic-git", "index.js"),
    path.join(mobileRoot, "node_modules", "isomorphic-git", "http", "web", "index.js"),
  ]);
});
