/**
 * Expo Router turns every top-level file under app/(tabs) into a tab unless
 * the layout declares it. A missing declaration shipped `projects` and
 * `vibing` as two anonymous down-arrow slots after More on iOS.
 */
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, extname, join, basename } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const tabsDir = join(here, "../../app/(tabs)");
const layout = readFileSync(join(tabsDir, "_layout.tsx"), "utf8");

test("every top-level tab route is explicitly visible or hidden", () => {
  const routes = readdirSync(tabsDir)
    .filter((name) => extname(name) === ".tsx" && name !== "_layout.tsx")
    .map((name) => basename(name, ".tsx"))
    .sort();
  const declared = new Set(
    Array.from(layout.matchAll(/<Tabs\.Screen\s+name="([^"]+)"/g), (match) => match[1]),
  );
  const leaked = routes.filter((route) => !declared.has(route));
  assert.deepEqual(leaked, [], `undeclared routes become anonymous tab slots: ${leaked.join(", ")}`);
});

test("the developer-tool bar exposes Tasks, SSH, Studio, and More", () => {
  for (const route of ["tasks", "ssh", "apps", "more"]) {
    assert.match(layout, new RegExp(`<Tabs\\.Screen\\s+name="${route}"`));
  }
  for (const route of ["projects", "vibing"]) {
    assert.match(
      layout,
      new RegExp(`<Tabs\\.Screen\\s+name="${route}"\\s+options=\\{\\{ href: null`),
      `${route} must stay reachable without becoming a tab`,
    );
  }
});
