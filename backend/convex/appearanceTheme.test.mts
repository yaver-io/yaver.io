import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

test("appearance settings are validated, surface-scoped, bounded, and forwarded", () => {
  const schema = read("backend/convex/schema.ts");
  const settings = read("backend/convex/userSettings.ts");
  const http = read("backend/convex/http.ts");

  assert.match(schema, /appearanceThemeBySurface:[\s\S]*surface: v\.string\(\)[\s\S]*literal\("light"\)[\s\S]*literal\("dark"\)/);
  for (const surface of ["mobile", "web", "tvos", "androidtv", "xbox", "playstation", "watchos", "wearos", "visionos", "carplay"]) {
    assert.match(settings, new RegExp(`v\\.literal\\("${surface}"\\)`));
  }
  assert.match(settings, /filter\(\(row\) => row\.surface !== patch\.surface\)/);
  assert.match(settings, /slice\(-10\)/);
  assert.equal((settings.match(/patch\.appearanceThemeBySurface = mergeAppearanceTheme/g) ?? []).length, 2);
  assert.match(http, /appearanceThemeForSurface: body\.appearanceThemeForSurface/);
});

test("every interactive client keeps dark fallback plus a surface-specific cloud patch", () => {
  const sources = {
    mobile: read("mobile/app/(tabs)/settings.tsx"),
    web: read("web/components/ThemeProvider.tsx"),
    tvos: read("tvos/YaverTV/YaverStore.swift"),
    androidtv: read("androidtv/app/src/main/kotlin/io/yaver/tv/TvStore.kt"),
    watchos: read("watch/YaverWatch/WatchProtocol.swift"),
    wearos: read("wear/app/src/main/kotlin/io/yaver/wear/WatchProtocol.kt"),
    visionos: read("visionos/YaverVision/YaverVisionApp.swift"),
  };

  assert.match(sources.mobile, /surface: "mobile"/);
  assert.match(sources.web, /surface: "web"/);
  assert.match(sources.tvos, /appearanceSurface = "tvos"/);
  assert.match(sources.androidtv, /TV_SURFACE_ID/);
  assert.match(sources.watchos, /case appearance/);
  assert.match(sources.wearos, /"appearance"/);
  assert.match(sources.visionos, /appearanceSurface: "visionos"/);

  const defaults = [
    read("mobile/src/context/ThemeContext.tsx"),
    sources.web,
    sources.tvos,
    sources.androidtv,
    read("watch/YaverWatch/WatchStore.swift"),
    read("wear/app/src/main/kotlin/io/yaver/wear/WatchState.kt"),
  ];
  for (const source of defaults) assert.match(source, /"dark"/);
});

test("paired wearable appearance writes use the signed-in phone instead of copying its token", () => {
  const bridge = read("mobile/src/components/WatchBridgeHost.tsx");
  const parser = read("mobile/src/lib/watchEntry.ts");
  assert.match(bridge, /appearance: async \(theme\)/);
  assert.match(bridge, /Platform\.OS === "ios" \? "watchos" : "wearos"/);
  assert.match(bridge, /appearanceThemeForSurface: \{ surface, theme \}/);
  assert.match(parser, /case "appearance"/);
});

test("mobile auxiliary chrome consumes theme tokens instead of staying dark-only", () => {
  assert.match(read("mobile/src/components/VoiceTestPanel.tsx"), /useColors\(\)/);
  assert.match(read("mobile/src/components/FeedbackOverlay.tsx"), /backgroundColor: c\.bgCard/);
  assert.match(read("mobile/app/(tabs)/apps.tsx"), /vibeOverlaySheet[\s\S]*backgroundColor: c\.bgCard/);
});
