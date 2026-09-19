// previewReadyScript.test.mts — `npx tsx src/lib/previewReadyScript.test.mts`.
//
// Locks the browser-lane contract:
// - Expo's document shell with an empty #root is NOT rendered.
// - A committed SPA root remains rendered for compatibility; visible-box facts
//   are diagnostics, not a hard gate that can break working apps.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  PREVIEW_PROBE_STATE_FUNCTION,
  PREVIEW_READY_PREDICATE,
} from "./previewReadyScript";

const makeFns = () => {
  const fn = new Function(`${PREVIEW_PROBE_STATE_FUNCTION}; ${PREVIEW_READY_PREDICATE}; return { yaverPreviewProbeState, yaverPreviewReady };`);
  return fn() as {
    yaverPreviewProbeState: (doc: any) => any;
    yaverPreviewReady: (doc: any) => boolean;
  };
};

const el = (opts: any = {}) => ({
  id: opts.id || "",
  tagName: opts.tagName || "DIV",
  children: opts.children || [],
  innerText: opts.innerText || "",
  querySelectorAll: () => opts.querySelectorAll || [],
  getBoundingClientRect: () => opts.rect || { width: 0, height: 0 },
});

const doc = (opts: any = {}) => {
  const root = opts.root === undefined ? null : opts.root;
  return {
    title: opts.title || "",
    location: { href: opts.href || "http://agent/dev-web/" },
    body: {
      children: opts.bodyChildren || [],
      innerText: opts.bodyText || "",
    },
    defaultView: {
      getComputedStyle: () => ({ display: "block", visibility: "visible", opacity: "1" }),
    },
    getElementById: (id: string) => {
      if (id === "root" || id === "app") return root;
      if (id === "splash") return opts.splash || null;
      return null;
    },
    querySelector: (selector: string) => {
      if (selector.includes("flutter-view")) return opts.flutterMarker || null;
      if (selector.includes("flutter")) return opts.flutterScript || null;
      return null;
    },
  };
};

const { yaverPreviewProbeState, yaverPreviewReady } = makeFns();

{
  const d = doc({
    root: el({ id: "root", children: [] }),
    bodyChildren: [el({ tagName: "NOSCRIPT" }), el({ id: "root" }), el({ tagName: "SCRIPT" })],
  });
  assert.equal(yaverPreviewReady(d), false, "Expo shell with empty #root must not be rendered");
  assert.equal(yaverPreviewProbeState(d).reason, "empty_mount");
}

{
  const d = doc({
    root: el({ id: "root", children: [el()] }),
    bodyChildren: [el({ tagName: "NOSCRIPT" }), el({ id: "root" }), el({ tagName: "SCRIPT" })],
  });
  assert.equal(yaverPreviewReady(d), true, "committed SPA root remains rendered for compatibility");
  assert.equal(yaverPreviewProbeState(d).reason, "mount_without_visible_content");
}

{
  const d = doc({ bodyText: '{"status":"starting"}', bodyChildren: [el()] });
  assert.equal(yaverPreviewReady(d), false, "agent starting response is not app content");
  assert.equal(yaverPreviewProbeState(d).reason, "agent_starting_response");
}

{
  const d = doc({ bodyText: "404 page not found", bodyChildren: [] });
  assert.equal(yaverPreviewReady(d), false, "the agent's bare 404 must not be application paint");
  assert.equal(yaverPreviewProbeState(d).reason, "http_error_document");
}

{
  const d = doc({ flutterMarker: el({ tagName: "FLUTTER-VIEW" }), bodyChildren: [el()] });
  assert.equal(yaverPreviewReady(d), true, "Flutter engine marker is rendered");
  assert.equal(yaverPreviewProbeState(d).reason, "flutter_engine_attached");
}

console.log("previewReadyScript contract ok");

// ── Lane inject (feedback-sdk-lanes audit 2026-07-28) ───────────────────────
// PREVIEW_LANE_SCRIPT must set window.__yaverLane='browser' BEFORE the guest
// boots, so a lane-aware yaver-feedback SDK self-hosts its draggable icon.
{
  const mod = await import("./previewReadyScript");
  const PREVIEW_LANE_SCRIPT = (mod as any).PREVIEW_LANE_SCRIPT as string;
  assert.ok(PREVIEW_LANE_SCRIPT, "PREVIEW_LANE_SCRIPT is exported");
  const fakeWindow: any = {};
  // Evaluate the injected IIFE against a fake window (matches WebView semantics).
  new Function("window", PREVIEW_LANE_SCRIPT)(fakeWindow);
  assert.equal(fakeWindow.__yaverLane, "browser", "lane stamped as browser");
  // Idempotent + crash-safe: a window that throws on set must not break injection.
  const hostileWindow: any = new Proxy({}, { set() { throw new Error("readonly"); } });
  assert.doesNotThrow(() => new Function("window", PREVIEW_LANE_SCRIPT)(hostileWindow),
    "lane script swallows a hostile window rather than crashing the guest");
  console.log("✓ PREVIEW_LANE_SCRIPT stamps browser lane, crash-safe");
}

// A failed entry bundle is invisible to WebView.onHttpError because the main
// document itself returned 200. The shared early watcher must surface it to
// both preview implementations, without carrying auth material across bridge.
{
  const mod = await import("./previewReadyScript");
  const script = (mod as any).PREVIEW_RESOURCE_ERROR_SCRIPT as string;
  let errorHandler: ((event: any) => void) | null = null;
  const messages: string[] = [];
  const fakeWindow: any = {
    ReactNativeWebView: { postMessage: (value: string) => messages.push(value) },
    addEventListener: (name: string, fn: (event: any) => void) => {
      if (name === "error") errorHandler = fn;
    },
  };
  new Function("window", "location", "URL", script)(fakeWindow, { href: "https://relay.example/d/device/dev-web/" }, URL);
  assert.ok(errorHandler, "resource watcher installs before guest scripts");
  errorHandler!({
    target: {
      tagName: "SCRIPT",
      src: "https://relay.example/d/device/dev-web/entry.bundle?token=secret&__rp=relay-secret",
    },
  });
  const message = JSON.parse(messages[0]);
  assert.equal(message.t, "yaver-preview-resource-error");
  assert.equal(message.tag, "SCRIPT");
  assert.doesNotMatch(message.url, /secret/);
  assert.match(message.url, /%5Bredacted%5D/);
  console.log("✓ resource failures cross the bridge with credentials redacted");
}

{
  const mobileRoot = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
  for (const rel of ["app/(tabs)/apps.tsx", "src/components/DevPreview.tsx"]) {
    const src = readFileSync(join(mobileRoot, rel), "utf8");
    assert.match(src, /PREVIEW_RESOURCE_ERROR_SCRIPT/, `${rel} must consume the shared early resource watcher`);
    assert.match(src, /shouldRetryBrowserResourceFailure/, `${rel} must retry a failed pre-paint entry script`);
    assert.match(src, /reconcileBrowserLaneProbe/, `${rel} must let the device probe override box-local success`);
  }
  console.log("✓ both browser-preview implementations consume the render guards");
}
