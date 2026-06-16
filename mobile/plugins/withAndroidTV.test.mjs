// withAndroidTV.test.mjs — verifies the Android TV (leanback) manifest mods.
// Run: node --test plugins/withAndroidTV.test.mjs
//
// Exercises the real withAndroidManifest mod the plugin registers by driving it
// the same way `expo prebuild` does: build a synthetic AndroidManifest tree,
// run the mod, and assert leanback launcher + banner landed without disturbing
// the existing phone launcher entry.

import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const withAndroidTV = require("./withAndroidTV.js");

const { LEANBACK_LAUNCHER, TV_BANNER, withTVManifest } = withAndroidTV;

// Minimal manifest shaped like the one @expo/config-plugins hands a mod.
function freshManifest() {
  return {
    manifest: {
      $: { "xmlns:android": "http://schemas.android.com/apk/res/android" },
      "uses-feature": [],
      application: [
        {
          $: { "android:name": ".MainApplication" },
          activity: [
            {
              $: { "android:name": ".MainActivity" },
              "intent-filter": [
                {
                  action: [{ $: { "android:name": "android.intent.action.MAIN" } }],
                  category: [{ $: { "android:name": "android.intent.category.LAUNCHER" } }],
                },
              ],
            },
          ],
        },
      ],
    },
  };
}

// Run the registered withAndroidManifest mod against a synthetic manifest.
async function runManifestMod(modResults) {
  const config = withTVManifest({ name: "Yaver", slug: "yaver" });
  const mod = config.mods?.android?.manifest;
  assert.ok(typeof mod === "function", "withTVManifest should register an android.manifest mod");
  const out = await mod({
    modResults,
    modRequest: { platformProjectRoot: "/tmp/android", projectRoot: "/tmp" },
    modRawConfig: config,
  });
  return out.modResults;
}

test("adds leanback launcher category to the main launcher intent-filter", async () => {
  const result = await runManifestMod(freshManifest());
  const activity = result.manifest.application[0].activity[0];
  const filter = activity["intent-filter"][0];
  const cats = filter.category.map((c) => c.$["android:name"]);
  assert.ok(cats.includes("android.intent.category.LAUNCHER"), "keeps phone launcher");
  assert.ok(cats.includes(LEANBACK_LAUNCHER), "adds TV leanback launcher");
});

test("marks leanback + touchscreen as not required so one APK serves phone + TV", async () => {
  const result = await runManifestMod(freshManifest());
  const features = result.manifest["uses-feature"];
  const leanback = features.find((f) => f.$["android:name"] === "android.software.leanback");
  const touch = features.find((f) => f.$["android:name"] === "android.hardware.touchscreen");
  assert.equal(leanback?.$["android:required"], "false");
  assert.equal(touch?.$["android:required"], "false");
});

test("sets the application banner + non-game flag for the TV home row", async () => {
  const result = await runManifestMod(freshManifest());
  const app = result.manifest.application[0];
  assert.equal(app.$["android:banner"], TV_BANNER);
  assert.equal(app.$["android:isGame"], "false");
});

test("is idempotent — re-running does not duplicate categories or features", async () => {
  const manifest = freshManifest();
  await runManifestMod(manifest);
  const result = await runManifestMod(manifest);
  const filter = result.manifest.application[0].activity[0]["intent-filter"][0];
  const leanbackCount = filter.category.filter((c) => c.$["android:name"] === LEANBACK_LAUNCHER).length;
  assert.equal(leanbackCount, 1, "leanback category added exactly once");
  const featCount = result.manifest["uses-feature"].filter(
    (f) => f.$["android:name"] === "android.software.leanback",
  ).length;
  assert.equal(featCount, 1, "leanback uses-feature added exactly once");
});
