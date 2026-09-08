/**
 * Guard: a NATIVE-ONLY module must never be require()d on web.
 *
 * ── The incident (2026-08-02) ──────────────────────────────────────────────
 *
 * `whisper.rn`'s entry runs `TurboModuleRegistry.get('RNWhisper')` at MODULE
 * LOAD. On react-native-web TurboModuleRegistry is undefined, so the require
 * threw `Cannot read properties of undefined (reading 'get')` during the app's
 * speech pre-init — at startup, before anyone touched a microphone.
 *
 * Driving the RN-web build showed the cost: the fatal fired on load and again
 * on navigation, and the Projects tab changed the URL to /apps while the view
 * never left the Tasks list. The whole project/preview surface was unreachable
 * on that build.
 *
 * This is the drift class CLAUDE.md names: invisible to `tsc`, fatal at
 * RUNTIME, on the surface least likely to be tested. A type-checker cannot see
 * it because `require()` of a native package type-checks fine.
 *
 * A second incident on 2026-09-08 was even simpler: SilentInputModal imported
 * react-native-vision-camera at the top level. VisionCamera deliberately throws
 * on web, so every RN-web route crashed before Login mounted. The old guard
 * passed because it knew only about whisper.rn.
 *
 * So this reads the SOURCE and asserts every known native-only module either
 * sits behind a stopping web guard or has a web sibling that never imports the
 * package. Source-level because the defect is module selection/control flow,
 * not a value a normal unit test can observe.
 *
 * Run: npx tsx mobile/src/lib/nativeOnlyModuleGuard.test.ts
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));

/**
 * Modules that touch native registries at import time.
 *
 * Each entry names the file that requires it and why it cannot work on web —
 * a bare list would be re-litigated by whoever hits it next.
 */
const NATIVE_ONLY = [
  {
    module: "whisper.rn",
    file: "speech.ts",
    why: "its entry calls TurboModuleRegistry.get('RNWhisper') at module load; TurboModuleRegistry is undefined on react-native-web",
  },
];

const PLATFORM_BOUNDARIES = [
  {
    module: "react-native-vision-camera",
    nativeFile: "../components/SilentInputModal.tsx",
    webFile: "../components/SilentInputModal.web.tsx",
    contractFile: "../components/SilentInputModal.types.ts",
    importers: ["../../app/(tabs)/tasks.tsx", "../components/SilentInputControlPanel.tsx"],
    why: "VisionCamera throws at module load on RN-web",
  },
];

let failures = 0;
const ok = (cond: unknown, label: string) => {
  if (cond) console.log(`ok   ${label}`);
  else { console.error(`FAIL ${label}`); failures++; }
};

for (const entry of NATIVE_ONLY) {
  const src = readFileSync(join(here, entry.file), "utf8");
  const requireIdx = src.indexOf(`require("${entry.module}")`);
  ok(requireIdx > 0, `${entry.file} requires ${entry.module} (fixture still valid)`);
  if (requireIdx < 0) continue;

  // A web guard must appear BEFORE the require, in the same function. Checking
  // only that the file mentions Platform.OS somewhere would pass a guard placed
  // after the throw — which is no guard at all.
  const before = src.slice(0, requireIdx);
  const guardIdx = before.lastIndexOf('Platform.OS === "web"');
  ok(guardIdx > 0, `${entry.module} is guarded by a web check BEFORE the require — ${entry.why}`);

  // The guard must actually STOP execution, not merely log.
  const between = before.slice(guardIdx);
  ok(/throw |return[\s;]/.test(between),
    `${entry.module}'s web guard returns or throws — a guard that only logs still reaches the require`);

  // And it must say WHY, so a caller can fall back rather than see a blank mic.
  ok(/native-only|cannot run in a browser/i.test(between),
    `${entry.module}'s web guard NAMES the limitation instead of failing silently`);
}

for (const entry of PLATFORM_BOUNDARIES) {
  const nativeSrc = readFileSync(join(here, entry.nativeFile), "utf8");
  const webSrc = readFileSync(join(here, entry.webFile), "utf8");
  const contractSrc = readFileSync(join(here, entry.contractFile), "utf8");

  ok(nativeSrc.includes(`from "${entry.module}"`), `${entry.nativeFile} imports ${entry.module} (fixture still valid)`);
  const webImportsNativeModule = new RegExp(
    `(?:from\\s+["']${entry.module}["']|import\\s*["']${entry.module}["']|require\\(["']${entry.module}["']\\))`,
  ).test(webSrc);
  ok(!webImportsNativeModule, `${entry.webFile} never imports ${entry.module} — ${entry.why}`);
  ok(/cannot use a camera in RN-web or browser automation/i.test(webSrc), `${entry.webFile} names the browser limitation instead of failing silently`);
  ok(nativeSrc.includes("SilentInputModalProps") && webSrc.includes("SilentInputModalProps"), "native and web SilentInputModal implementations use the shared prop contract");
  ok(/export type SilentInputModalProps/.test(contractSrc), "SilentInputModal shared prop contract exists");

  for (const importer of entry.importers) {
    const importerSrc = readFileSync(join(here, importer), "utf8");
    ok(/from ["'][^"']*\/SilentInputModal["']/.test(importerSrc), `${importer} imports SilentInputModal extensionlessly so Metro can select the web boundary`);
  }
}

// The file must import Platform, or the guard is a ReferenceError at runtime.
ok(/import \{[^}]*Platform[^}]*\} from "react-native"/.test(
  readFileSync(join(here, "speech.ts"), "utf8")),
  "speech.ts imports Platform, so the guard cannot itself throw");

if (failures) { console.error(`\nnativeOnlyModuleGuard: ${failures} FAILED`); process.exitCode = 1; }
else console.log("\nnativeOnlyModuleGuard: ALL PASS");
