/**
 * connectedDeviceCard.test.ts — `npx tsx lib/connectedDeviceCard.test.ts`
 *
 * Pins the "which card am I on?" affordance two ways, the same shape as
 * endpoints.test.ts:
 *
 *  1. behaviour — the pure helpers (id equality, the status sentence, the
 *     per-deviceId latency registry).
 *  2. structure — the ONE call site in DevicesView.tsx actually keys off
 *     deviceId, actually gates the status line, and never composes a value
 *     from the `agentClient` singleton onto a card whose id doesn't match.
 *
 * Structure matters more than usual here. The incident this fixes was two
 * identically-named machines and a status line assembled from two different
 * devices; a correct helper called with `device.name` — or a status line
 * rendered unconditionally — reproduces the bug with every unit test green.
 * Every assertion below has been proven by breaking it and watching it fail:
 * swap `device.id` for `device.name`; drop the `isConnectedCard &&` guard so
 * the pill renders on every card; ungate `transportFor(device)`; key the
 * latency off the connected singleton's id instead of this card's; make
 * `isBrowserConnectedToDevice` stop comparing; make the connected surface
 * identical to the default. Each broke exactly one test and nothing else.
 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  connectedStatusLine,
  DEVICE_CARD_SURFACE_AUTH,
  DEVICE_CARD_SURFACE_CLAIMED,
  DEVICE_CARD_SURFACE_CONNECTED,
  DEVICE_CARD_SURFACE_DEFAULT,
  DEVICE_CARD_SURFACE_OFFLINE,
  DEVICE_CARD_SURFACE_REACHABLE,
  deviceCardSurfaceClasses,
  deviceCardSurfaceState,
  isBrowserConnectedToDevice,
  noteDeviceReachRttMs,
  readDeviceReachRttMs,
  REACH_SAMPLE_MAX_AGE_MS,
  resetDeviceReachSamples,
} from "./connectedDeviceCard";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const devicesView = readFileSync(
  join(root, "components/dashboard/DevicesView.tsx"),
  "utf8",
);

/** The body of the device-list `.map()` — from the map through the end of the
 *  DevicesView component (the next top-level `function` declaration). */
function cardRenderBlock(): string {
  const start = devicesView.indexOf("renderedDevices.map((device) => {");
  assert.notEqual(start, -1, "device list map not found — did the render move?");
  const end = devicesView.indexOf("\nfunction ", start);
  assert.notEqual(end, -1, "could not bound the card render block");
  return devicesView.slice(start, end);
}

// ── behaviour ──────────────────────────────────────────────────────────────

test("connected-card identity is deviceId equality, never name", () => {
  const A = "6f1c9d2a-aaaa-4aaa-8aaa-000000000001";
  const B = "6f1c9d2a-bbbb-4bbb-8bbb-000000000002";
  assert.equal(isBrowserConnectedToDevice(A, A, "connected"), true);
  // Two agents on one box register the SAME display name. Identical names must
  // never be evidence — only the ids decide.
  assert.equal(isBrowserConnectedToDevice(A, B, "connected"), false);
  assert.equal(isBrowserConnectedToDevice("mac-mini", "mac-mini", "connected"), true,
    "an id that happens to look like a name still compares by value");
  assert.equal(isBrowserConnectedToDevice(A, A, "connecting"), false);
  assert.equal(isBrowserConnectedToDevice(A, A, "disconnected"), false);
  assert.equal(isBrowserConnectedToDevice(A, null, "connected"), false);
  assert.equal(isBrowserConnectedToDevice(null, A, "connected"), false);
  assert.equal(isBrowserConnectedToDevice("", "", "connected"), false,
    "two unknowns are not a match");
  assert.equal(isBrowserConnectedToDevice(` ${A} `, A, "connected"), true);
});

test("status line names the transport only when it is evidenced", () => {
  assert.equal(
    connectedStatusLine({ transportLabel: "Yaver public relay", transportPrimary: "yaver-public-relay", latencyMs: 604 }),
    "Connected · Yaver public relay · 604ms",
  );
  assert.equal(
    connectedStatusLine({ transportLabel: "Private LAN", transportPrimary: "private-lan" }),
    "Connected · Private LAN",
  );
  // An "unknown" classification is not a transport — say less, not something wrong.
  assert.equal(
    connectedStatusLine({ transportLabel: "Unknown", transportPrimary: "unknown", latencyMs: 12 }),
    "Connected · 12ms",
  );
  assert.equal(connectedStatusLine({}), "Connected");
  assert.equal(connectedStatusLine({ latencyMs: null }), "Connected");
  assert.equal(connectedStatusLine({ latencyMs: Number.NaN }), "Connected");
  assert.equal(
    connectedStatusLine({ transportLabel: "Custom relay", transportPrimary: "self-hosted-relay", latencyMs: 30.4 }),
    "Connected · Custom relay · 30ms",
  );
});

test("reach samples are keyed by deviceId and expire", () => {
  resetDeviceReachSamples();
  const A = "aaaaaaaa-0000-4000-8000-000000000001";
  const B = "bbbbbbbb-0000-4000-8000-000000000002";
  const t0 = 1_000_000;
  noteDeviceReachRttMs(A, 604, t0);
  assert.equal(readDeviceReachRttMs(A, t0), 604);
  // One device's latency can never leak onto another card.
  assert.equal(readDeviceReachRttMs(B, t0), null);
  // A stale number is a lie with a decimal point.
  assert.equal(readDeviceReachRttMs(A, t0 + REACH_SAMPLE_MAX_AGE_MS + 1), null);
  assert.equal(readDeviceReachRttMs(A, t0 + REACH_SAMPLE_MAX_AGE_MS - 1), 604);
  noteDeviceReachRttMs("", 5, t0);
  assert.equal(readDeviceReachRttMs("", t0), null);
  resetDeviceReachSamples();
  assert.equal(readDeviceReachRttMs(A, t0), null);
});

test("surface classes differ, tint both themes, and stay non-shouty", () => {
  assert.equal(deviceCardSurfaceClasses(true), DEVICE_CARD_SURFACE_CONNECTED);
  assert.equal(deviceCardSurfaceClasses(false), DEVICE_CARD_SURFACE_DEFAULT);
  assert.equal(deviceCardSurfaceClasses(false, "reachable"), DEVICE_CARD_SURFACE_REACHABLE);
  assert.equal(deviceCardSurfaceClasses(false, "claimed"), DEVICE_CARD_SURFACE_CLAIMED);
  assert.equal(deviceCardSurfaceClasses(false, "auth"), DEVICE_CARD_SURFACE_AUTH);
  assert.equal(deviceCardSurfaceClasses(false, "offline"), DEVICE_CARD_SURFACE_OFFLINE);
  assert.equal(deviceCardSurfaceClasses(true, "offline"), DEVICE_CARD_SURFACE_CONNECTED);
  assert.notEqual(DEVICE_CARD_SURFACE_CONNECTED, DEVICE_CARD_SURFACE_DEFAULT);
  assert.notEqual(DEVICE_CARD_SURFACE_AUTH, DEVICE_CARD_SURFACE_DEFAULT);
  assert.notEqual(DEVICE_CARD_SURFACE_OFFLINE, DEVICE_CARD_SURFACE_DEFAULT);
  // Both themes must be styled — a light-only tint is invisible in dark mode.
  assert.ok(/dark:/.test(DEVICE_CARD_SURFACE_CONNECTED), "connected surface must style dark mode");
  assert.ok(/dark:/.test(DEVICE_CARD_SURFACE_DEFAULT), "default surface must style dark mode");
  assert.ok(/dark:/.test(DEVICE_CARD_SURFACE_AUTH), "auth surface must style dark mode");
  assert.ok(/dark:/.test(DEVICE_CARD_SURFACE_OFFLINE), "offline surface must style dark mode");
  // Success/brand vocabulary, at low opacity — never a solid alert fill.
  assert.ok(/success/.test(DEVICE_CARD_SURFACE_CONNECTED), "use the theme's success tokens");
  assert.ok(
    !/\bbg-success\b(?!-)/.test(DEVICE_CARD_SURFACE_CONNECTED),
    "a solid success fill is an alert, not an affordance",
  );
});

test("card surface state follows the row's own lifecycle and reachability", () => {
  assert.equal(deviceCardSurfaceState({ lifecycle: "connected", reach: { state: "reachable", verified: true } }), "reachable");
  assert.equal(deviceCardSurfaceState({ lifecycle: "ready-to-connect", reach: { state: "claimed" } }), "claimed");
  assert.equal(deviceCardSurfaceState({ lifecycle: "bootstrap", reach: { state: "claimed" } }), "auth");
  assert.equal(deviceCardSurfaceState({ lifecycle: "yaver-auth-expired" }), "auth");
  assert.equal(deviceCardSurfaceState({ lifecycle: "ready-to-connect", reach: { state: "unreachable", unreachable: true } }), "offline");
  assert.equal(deviceCardSurfaceState({ lifecycle: "offline" }), "offline");
  assert.equal(deviceCardSurfaceState({}), "default");
});

// ── structure — the call site in DevicesView.tsx ───────────────────────────

test("the connected card is selected by deviceId, not name and not index", () => {
  const block = cardRenderBlock();
  assert.match(
    block,
    /const isConnectedCard = isBrowserConnectedToDevice\(\s*device\.id,/,
    "the connected-card flag must be derived from device.id",
  );
  assert.ok(
    !/isBrowserConnectedToDevice\(\s*device\.name/.test(block),
    "device.name must never decide which card is connected — names collide",
  );
  // A list index is meaningless across renders: renderedDevices re-sorts by
  // role / lifecycle / managed state on nearly every heartbeat.
  assert.match(
    devicesView,
    /renderedDevices\.map\(\(device\) => \{/,
    "the map callback must not take an index parameter",
  );
  assert.match(
    block,
    /deviceCardSurfaceClasses\(isConnectedCard,\s*cardSurfaceState\)/,
    "the card surface must keep the id-derived connected flag and add row-local status tint",
  );
  assert.match(
    block,
    /const cardSurfaceState = deviceCardSurfaceState\(\{\s*lifecycle,\s*reach,\s*needsAuth: device\.needsAuth,\s*probeState: device\.probeState,\s*\}\);/,
    "non-connected card tint must be derived from this row's own lifecycle/reach fields",
  );
});

test("the connection status line renders ONLY on the connected card", () => {
  const block = cardRenderBlock();
  // The pill and its live dot must sit behind the id-derived guard. A selected
  // card may narrate its connecting window before the plain transport fallback.
  assert.match(
    block,
    /\{isConnectedCard && connectedLine \? \([\s\S]{0,900}?animate-live-pulse[\s\S]{0,900}?: isWorkspaceConnecting \? \([\s\S]{0,900}?: \(\s*<TransportBadge device=\{device\} \/>\s*\)\}/,
    "the connected status pill must be gated on isConnectedCard, with TransportBadge as the fallback",
  );
  // …and it must be the only place the sentence is produced.
  const built = block.match(/connectedStatusLine\(/g) || [];
  assert.equal(built.length, 1, "the status sentence should be built exactly once, under the guard");
});

test("no card composes an agentClient-singleton value unless its deviceId matches", () => {
  const block = cardRenderBlock();
  // transportFor() reads agentClient.activeRelayUrl / connectionState — one
  // device's connection. Inside the card body it may only be evaluated for the
  // card that IS that device.
  assert.match(
    block,
    /const connectedTransport = isConnectedCard \? transportFor\(device\) : null;/,
    "transportFor() must be gated on isConnectedCard inside the card body",
  );
  assert.equal(
    (block.match(/transportFor\(device\)/g) || []).length,
    1,
    "transportFor() must not be called ungated anywhere in the card body",
  );
  // The latency likewise: read only inside the guarded ternary.
  assert.match(
    block,
    /const connectedLine = isConnectedCard\s*\?\s*connectedStatusLine\(\{[\s\S]{0,400}?readDeviceReachRttMs\(device\.id\)[\s\S]{0,200}?\}\)\s*:\s*null;/,
    "latency must be read inside the isConnectedCard branch, keyed by this card's device.id",
  );
  assert.equal(
    (block.match(/readDeviceReachRttMs\(/g) || []).length,
    1,
    "latency must be read exactly once, under the guard",
  );
});

test("the connected-device id comes from agentClient, not from the Convex row", () => {
  // device.workspaceLive / lifecycle "connected" is a claim about the AGENT
  // (Convex heartbeat). It is not evidence that THIS browser holds a session
  // with that box — conflating them is how the dashboard lied before.
  assert.match(
    devicesView,
    /function useConnectedAgentDeviceId\(\)[\s\S]{0,600}?agentClient\.connectedDeviceId/,
    "the connected deviceId must be read from agentClient",
  );
  const block = cardRenderBlock();
  assert.ok(
    !/isBrowserConnectedToDevice\([\s\S]{0,120}?workspaceLive/.test(block),
    "workspaceLive must not stand in for a browser session",
  );
});
