import test from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const mobileRoot = join(dirname(fileURLToPath(import.meta.url)), "../..");
const scanRoots = ["app", "src"].map((dir) => join(mobileRoot, dir));
const sourceExts = new Set([".ts", ".tsx", ".js", ".jsx", ".mts"]);
const forbidden = [
  "/billing/checkout",
  "/billing/credits/checkout",
  "/billing/portal",
  "/billing/cancel",
  "/billing/yaver-cloud/checkout",
  "/billing/yaver-cloud/change-plan",
  "react-native-purchases",
  "Purchases.configure",
  "Purchases.purchase",
];

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (name === "vendor" || name === "node_modules" || name.startsWith(".")) continue;
    const path = join(dir, name);
    const stat = statSync(path);
    if (stat.isDirectory()) {
      walk(path, out);
      continue;
    }
    const ext = path.slice(path.lastIndexOf("."));
    if (sourceExts.has(ext) && !path.endsWith(".test.mts")) out.push(path);
  }
  return out;
}

test("mobile app has no Yaver infrastructure purchase, checkout, or cancellation endpoints", () => {
  const hits: string[] = [];
  for (const file of scanRoots.flatMap((root) => walk(root))) {
    const text = readFileSync(file, "utf8");
    for (const needle of forbidden) {
      if (text.includes(needle)) hits.push(`${relative(mobileRoot, file)} contains ${needle}`);
    }
  }
  assert.deepEqual(hits, []);
});
