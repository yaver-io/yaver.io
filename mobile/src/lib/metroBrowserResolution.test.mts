import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const mobile = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const metro = require(join(mobile, "metro.config.js"));

test("Metro pins isomorphic-git to its Hermes-safe browser entry", () => {
  const resolved: Array<{ moduleName: string; platform: string }> = [];
  const context = {
    resolveRequest(_context: unknown, moduleName: string, platform: string) {
      resolved.push({ moduleName, platform });
      return { type: "sourceFile", filePath: moduleName };
    },
  };

  metro.resolver.resolveRequest(context, "isomorphic-git", "ios");
  assert.deepEqual(resolved[0], {
    moduleName: join(mobile, "node_modules", "isomorphic-git", "index.js"),
    platform: "ios",
  });

  metro.resolver.resolveRequest(context, "isomorphic-git", "android");
  assert.deepEqual(resolved[1], {
    moduleName: join(mobile, "node_modules", "isomorphic-git", "index.js"),
    platform: "android",
  });

  metro.resolver.resolveRequest(context, "isomorphic-git/http/web", "ios");
  assert.deepEqual(resolved[2], {
    moduleName: "isomorphic-git/http/web",
    platform: "ios",
  });
});
