import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const read = (relativePath) => readFileSync(join(here, relativePath), "utf8");

const appConfig = JSON.parse(read("../../app.json"));
const mainManifest = read("../../android/app/src/main/AndroidManifest.xml");
const localReleaseScript = read("../../../scripts/deploy-playstore.sh");
const ciReleaseWorkflow = read("../../../.github/workflows/release-mobile.yml");
const pickerCallSites = [
  read("../../app/phone-projects.tsx"),
  read("../../app/(tabs)/designmode.tsx"),
  read("../../app/(tabs)/tasks.tsx"),
];

const broadMediaPermissions = ["READ_MEDIA_IMAGES", "READ_MEDIA_VIDEO"];
const blockedPermissions = appConfig.expo.android.blockedPermissions ?? [];

for (const permission of broadMediaPermissions) {
  assert.ok(
    blockedPermissions.includes(permission),
    `${permission} must stay blocked in Expo config so clean prebuilds cannot restore broad media access`,
  );
  assert.match(
    mainManifest,
    new RegExp(
      `<uses-permission android:name="android\\.permission\\.${permission}" tools:node="remove"\\s*/>`,
    ),
    `${permission} must stay removed from the tracked native manifest used by local releases`,
  );
}

for (const releaseLane of [localReleaseScript, ciReleaseWorkflow]) {
  assert.match(
    releaseLane,
    /READ_MEDIA_\(IMAGES\|VIDEO\)/,
    "every Play release lane must reject a merged manifest containing broad media permissions",
  );
}

for (const source of pickerCallSites) {
  assert.doesNotMatch(
    source,
    /requestMediaLibraryPermissionsAsync/,
    "system photo-picker call sites must not request broad library access first",
  );
}

console.log("Android system photo-picker contract passed");
