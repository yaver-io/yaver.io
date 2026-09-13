/**
 * webViewCompatParity.test.ts — `npx tsx src/components/webViewCompatParity.test.ts`.
 * No RN, no jest — the tiny assert harness the rest of the repo uses.
 *
 * WebViewCompat.tsx and WebViewCompat.web.tsx are chosen by Metro's platform
 * extension, so they are two INDEPENDENT modules. Drift is invisible to tsc and
 * detonates at runtime — the shape that shipped
 * "beaconListener.getBootstrapDevices is not a function" earlier the same day.
 *
 * Source is read rather than imported: importing the native file resolves
 * react-native-webview and would prove nothing about the browser build.
 */
import { readFileSync } from "fs";
import { join } from "path";

let passed = 0;
let failed = 0;
function ok(cond: boolean, msg: string) {
  if (cond) {
    passed++;
  } else {
    failed++;
    console.error("  ✗ " + msg);
  }
}

// Comments are STRIPPED before matching. The first version of this test matched
// the word "iframe" inside a doc comment, so replacing the real <iframe> with a
// <div> still passed — a guard that cannot fail is a guess, not a guard.
const stripComments = (src: string) =>
  src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
const read = (f: string) => stripComments(readFileSync(join(__dirname, f), "utf8"));
const nativeSrc = read("WebViewCompat.tsx");
const webSrc = read("WebViewCompat.web.tsx");

// Both must export the names every caller relies on.
for (const name of ["WebView", "WEBVIEW_SUPPORTED", "WEBVIEW_UNSUPPORTED_REASON"]) {
  ok(new RegExp(`export (const |{[^}]*\\b${name}\\b|function ${name}|type )`).test(nativeSrc) || nativeSrc.includes(name), `native exports ${name}`);
  ok(webSrc.includes(name), `web exports ${name}`);
}

// The web sibling must actually be an iframe — not a stub that renders nothing,
// which would reproduce the blank screen it exists to fix.
ok(/<iframe/.test(webSrc), "web sibling renders a real <iframe>");

// Guest pages written for the native container post through
// window.ReactNativeWebView. The web sibling must provide that bridge or those
// pages silently stop talking.
ok(webSrc.includes("ReactNativeWebView"), "web sibling bridges window.ReactNativeWebView");

// reload() is called imperatively by the preview screen.
ok(/reload\s*\(/.test(webSrc), "web sibling implements reload()");

// ── The guard that was missing, and the drift it would have caught ──────────
//
// The twins agreeing with each other proves nothing if the PREVIEW SCREENS do
// not use them. Measured 2026-08-04: apps.tsx had been migrated to the shim
// while DevPreview.tsx and app/(tabs)/project.tsx still imported
// react-native-webview directly — so on RN-web those screens rendered the
// library's own "React Native WebView does not support this platform." text.
// That is the "a fix that lands in one of two browser-preview implementations
// is not landed" rule (CLAUDE.md), and the parity test above could never see it
// because it only ever read the two shim files.
//
// Only the BROWSER-PREVIEW surfaces are listed. Screens that are native-only by
// nature (camera, terminal, remote desktop, droid control) may import the real
// WebView — forbidding it everywhere would be a rule people route around.
const previewSurfaces = [
  "../../app/(tabs)/apps.tsx",
  "../../app/(tabs)/project.tsx",
  "../../app/attach.tsx",
  "./DevPreview.tsx",
];
for (const rel of previewSurfaces) {
  const src = stripComments(readFileSync(join(__dirname, rel), "utf8"));
  ok(
    !/from ["']react-native-webview["']/.test(src),
    `${rel} must import WebViewCompat, not react-native-webview — the raw library renders an error string on RN-web`,
  );
  ok(
    /WebViewCompat/.test(src),
    `${rel} must reference WebViewCompat`,
  );
}

// BOTH browser-preview implementations must consume the shared wait narration
// and strict paint gate. These are independent React trees; landing a fix in
// only one is the drift that shipped different browser behavior on Projects
// and Tasks.
for (const rel of ["./DevPreview.tsx", "../../app/(tabs)/apps.tsx"]) {
  const src = stripComments(readFileSync(join(__dirname, rel), "utf8"));
  // `=== WEBVIEW_PROBE_UNSUPPORTED` and not just the identifier: the import line
  // alone satisfied a bare includes(), so deleting the actual handler still
  // passed. Proven by trying exactly that.
  ok(
    src.includes("=== WEBVIEW_PROBE_UNSUPPORTED"),
    `${rel} must handle WEBVIEW_PROBE_UNSUPPORTED — on RN-web the preview iframe is cross-origin, so the host ready-probe can never fire; this names the limitation while the in-frame probe checks paint`,
  );
}

// Paint gating is negotiated from the agent's status. A current agent's
// in-frame signal remains strict; v1.99.418 has no such code in its binary, so
// waiting for it behind an opaque overlay can never terminate. Both preview
// implementations must consume the shared decision and render the named
// unverified-visible state.
for (const rel of ["./DevPreview.tsx", "../../app/(tabs)/apps.tsx"] as const) {
  const src = stripComments(readFileSync(join(__dirname, rel), "utf8"));
  ok(src.includes("previewPaintGateMode"), `${rel} must negotiate the agent paint signal`);
  ok(src.includes('paintGateMode === "blocking"'), `${rel} must keep strict gating when a signal can arrive`);
  ok(src.includes('paintGateMode === "blocking"'), `${rel} must keep the strict paint gate while the first frame is unconfirmed`);
  ok(src.includes("previewWaitLine"), `${rel} must narrate elapsed time and recent output while waiting`);
}

console.log(`\nwebViewCompatParity: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
