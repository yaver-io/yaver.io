/**
 * loadAppIntent.test.ts — `npx tsx src/lib/voice/loadAppIntent.test.ts`.
 */
import { classifyLoadApp, spokenForLoad } from "./loadAppIntent";

let passed = 0;
let failed = 0;
function ok(cond: boolean, msg: string) {
  if (cond) passed++;
  else {
    failed++;
    console.error("  ✗ " + msg);
  }
}

// ── positive: app + mode extraction ──────────────────────────────────────
const a = classifyLoadApp("load me the todo app with hermes");
ok(a?.app === "todo" && a?.mode === "hermes", "'load me the todo app with hermes' → todo/hermes");
ok(a?.spoken === "Loading todo with Hermes.", "hermes spoken line");

const b = classifyLoadApp("load sfmg over webrtc");
ok(b?.app === "sfmg" && b?.mode === "webrtc", "'load sfmg over webrtc' → sfmg/webrtc");

const c = classifyLoadApp("load talos with a native build");
ok(c?.app === "talos" && c?.mode === "native", "'load talos with a native build' → talos/native");

// "web rtc" spoken as two words normalises to webrtc.
const d = classifyLoadApp("load the app with web rtc");
ok(d?.mode === "webrtc" && d?.app === "", "'web rtc' → webrtc, no name → picker");

// ── positive: no explicit name → picker ("" app) ─────────────────────────
const e = classifyLoadApp("load me the app");
ok(e !== null && e.app === "" && e.mode === "auto", "'load me the app' → picker/auto");
ok(e?.spoken === "Opening your apps.", "no-name spoken line");

const f = classifyLoadApp("just load it");
ok(f !== null && f.app === "" && f.mode === "auto", "'just load it' → picker/auto");

const g = classifyLoadApp("reload the app");
ok(g !== null && g.app === "" , "'reload the app' handled");

// Multi-word app names survive.
const h = classifyLoadApp("load yaver todo rn with hermes");
ok(h?.app === "yaver todo rn" && h?.mode === "hermes", "multi-word app name kept");

// ── "render / launch / show the app here" also load ──────────────────────
const r1 = classifyLoadApp("render the app here");
ok(r1 !== null && r1.app === "", "'render the app here' → load/picker");
const r2 = classifyLoadApp("launch the todo app with webrtc");
ok(r2?.app === "todo" && r2?.mode === "webrtc", "'launch the todo app with webrtc' → todo/webrtc");
const r3 = classifyLoadApp("show me the app");
ok(r3 !== null && r3.app === "", "'show me the app' → load/picker");
const r4 = classifyLoadApp("just render it here");
ok(r4 !== null && r4.app === "", "'just render it here' → load/picker");

// Coding instructions that share a verb must still pass through.
ok(classifyLoadApp("render the login screen and add a button") === null, "'render the login screen' → runner (null)");
ok(classifyLoadApp("run the tests") === null, "'run the tests' → runner (null)");
ok(classifyLoadApp("show me the git log") === null, "'show me the git log' → runner (null)");

// ── negative: real coding instructions must pass through (null) ───────────
ok(classifyLoadApp("load the config from disk") === null, "'load the config from disk' → runner (null)");
ok(classifyLoadApp("add a login button and wire it up") === null, "coding instruction → null");
ok(classifyLoadApp("download the latest release") === null, "'download' does not trigger load");
ok(classifyLoadApp("upload the file to storage") === null, "'upload' does not trigger load");
ok(classifyLoadApp("refactor the auth handler") === null, "unrelated → null");
ok(classifyLoadApp("") === null, "empty → null");

// ── spokenForLoad unit ───────────────────────────────────────────────────
ok(spokenForLoad("sfmg", "webrtc") === "Loading sfmg with WebRTC.", "spokenForLoad webrtc");
ok(spokenForLoad("", "auto") === "Opening your apps.", "spokenForLoad empty");
ok(spokenForLoad("todo", "auto") === "Loading todo.", "spokenForLoad auto no suffix");

console.log(`\nloadAppIntent: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
