// Closed-loop RN browser-vibing test, run against a REAL Chromium on this Mac.
//
// Serves the actual Expo-web export of an RN project, loads it in a real
// browser, and evaluates the OLD and NEW readiness predicates against the live
// DOM at the exact moment the old probe would have fired ("document-end") and
// again after React has mounted.
//
// The claim under test: for an Expo Web app the OLD predicate reports
// "rendered" while #root is still empty — which lifts the loading overlay onto
// a blank page.

import { chromium } from "playwright";
import http from "node:http";
import fs from "node:fs";
import path from "node:path";

const root = process.argv[2];
const label = process.argv[3] || path.basename(root);
if (!root) { console.error("usage: node rn-browser-loop.mjs <export-dir> [label]"); process.exit(2); }

const MIME = { ".html": "text/html", ".js": "text/javascript", ".json": "application/json",
  ".ico": "image/x-icon", ".png": "image/png", ".ttf": "font/ttf", ".css": "text/css" };

// Deliberately slow the entry bundle: the real lane fetches 6.8–7.6 MB through
// the relay, so "the HTML is up but the JS has not executed yet" is the normal
// case, not an edge case. This makes that window observable.
const BUNDLE_DELAY_MS = Number(process.env.BUNDLE_DELAY_MS || 1500);

const server = http.createServer((req, res) => {
  const urlPath = decodeURIComponent(req.url.split("?")[0]);
  const rel = urlPath === "/" ? "/index.html" : urlPath;
  const file = path.join(root, rel);
  if (!file.startsWith(root) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
    res.writeHead(404); res.end("not found"); return;
  }
  const send = () => {
    res.writeHead(200, { "Content-Type": MIME[path.extname(file)] || "application/octet-stream" });
    fs.createReadStream(file).pipe(res);
  };
  if (rel.includes("/_expo/static/js/")) setTimeout(send, BUNDLE_DELAY_MS); else send();
});

// ── the two predicates, verbatim ──────────────────────────────────────────────
const OLD = `function(doc){
  var b = doc.body; var bt = (b && b.innerText || '').trim();
  if (bt.indexOf('"status":"starting"')>=0 || bt.indexOf('did not become ready')>=0) return false;
  var f = doc.querySelector('flutter-view,flt-glass-pane,flt-scene-host');
  var d = b && (b.children.length>1 || bt.length>0);
  return !!(f || d);
}`;

const NEW = fs.readFileSync(
  path.join(process.env.REPO, "mobile/src/lib/previewReadyScript.ts"), "utf8",
).match(/export const PREVIEW_READY_PREDICATE = `([\s\S]*?)`;/)[1];

const probe = async (page) => page.evaluate(({ oldSrc, newSrc }) => {
  const oldFn = new Function("return (" + oldSrc + ")")();
  const newFn = new Function(newSrc + "; return yaverPreviewReady;")();
  const mount = document.getElementById("root");
  return {
    old: !!oldFn(document),
    neu: !!newFn(document),
    bodyChildren: document.body ? document.body.children.length : -1,
    rootChildren: mount ? mount.children.length : -1,
    visibleText: (document.body?.innerText || "").trim().slice(0, 60),
  };
}, { oldSrc: OLD, newSrc: NEW });

await new Promise((r) => server.listen(0, "127.0.0.1", r));
const port = server.address().port;
const url = `http://127.0.0.1:${port}/`;

const browser = await chromium.launch();
const page = await browser.newPage();

console.log(`\n=== ${label} — ${url} ===`);

// t0: HTML received, deferred entry bundle NOT yet executed. `defer` scripts
// run BEFORE DOMContentLoaded, so waiting for that event would miss the window
// entirely on an app that mounts synchronously — which is exactly what talos
// does. "commit" is the honest pre-mount observation point.
await page.goto(url, { waitUntil: "commit" });
await page.waitForFunction(() => !!document.body, { timeout: 10000 });
const t0 = await probe(page);
console.log(`t0  (DOM parsed, bundle in flight)  body=${t0.bodyChildren} #root=${t0.rootChildren}  OLD=${t0.old}  NEW=${t0.neu}  text=${JSON.stringify(t0.visibleText)}`);

// t1: after React has committed into #root.
let t1;
try {
  await page.waitForFunction(() => {
    const m = document.getElementById("root");
    return m && m.children.length > 0;
  }, { timeout: 45000 });
  t1 = await probe(page);
  console.log(`t1  (react mounted)                 body=${t1.bodyChildren} #root=${t1.rootChildren}  OLD=${t1.old}  NEW=${t1.neu}  text=${JSON.stringify(t1.visibleText)}`);
} catch {
  t1 = await probe(page);
  console.log(`t1  (TIMED OUT waiting for mount)   body=${t1.bodyChildren} #root=${t1.rootChildren}  OLD=${t1.old}  NEW=${t1.neu}  text=${JSON.stringify(t1.visibleText)}`);
}

const shot = path.join(process.env.SCRATCH, `${label}-mounted.png`);
await page.screenshot({ path: shot });

// ── verdict ───────────────────────────────────────────────────────────────────
// Invariants must hold for EVERY app. Whether the pre-mount window is even
// observable depends on how fast the app mounts, so "the old probe is wrong"
// is reported as an observation, not asserted — an app that mounts before the
// probe runs simply never had the bug.
const caughtPreMount = t0.rootChildren === 0;
const results = [];
results.push(["NEW never claims rendered on an empty #root", !(t0.rootChildren === 0 && t0.neu === true)]);
results.push(["NEW reports rendered once mounted",           t1.neu === true && t1.rootChildren > 0]);
results.push(["NEW is never worse than OLD after mount",     !(t1.old === true && t1.neu === false)]);

let ok = true;
console.log("");
for (const [name, pass] of results) { console.log(`  ${pass ? "PASS" : "FAIL"}  ${name}`); if (!pass) ok = false; }
console.log(caughtPreMount
  ? `  NOTE  pre-mount window observed: OLD=${t0.old} (would lift the overlay onto an empty #root), NEW=${t0.neu}`
  : `  NOTE  app mounted before the probe ran — no pre-mount window on this app`);
console.log(`\nscreenshot: ${shot}`);

await browser.close();
server.close();
process.exit(ok ? 0 : 1);
