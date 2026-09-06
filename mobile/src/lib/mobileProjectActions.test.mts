import test from "node:test";
import assert from "node:assert/strict";
import {
  applyPreviewCapabilities,
  guardYaverSelfDevelopmentActions,
  isYaverSelfDevelopmentProject,
  workspaceAppLanes,
  type MobileProjectAction,
} from "./mobileProjectActions.ts";

const actions: MobileProjectAction[] = [
  { label: "Project Overview", target: ".", type: "project" },
  { label: "Open in Yaver", target: ".", type: "open-native", framework: "react-native", supported: true },
  { label: "Compile Hermes", target: ".", type: "compile-hermes", framework: "react-native", supported: true },
  { label: "Stream over WebRTC", target: ".", type: "remote-runtime", framework: "react-native", supported: true },
  { label: "Git Sync", target: ".", type: "git-sync" },
];

test("detects the Yaver monorepo and mobile app as self-development", () => {
  assert.equal(isYaverSelfDevelopmentProject("yaver.io", "/workspace/yaver.io", ""), true);
  assert.equal(isYaverSelfDevelopmentProject("mobile", "/Users/me/Workspace/yaver.io/mobile", ""), true);
  assert.equal(isYaverSelfDevelopmentProject("Yaver", "/tmp/repo", "git@github.com:yaver-io/yaver.io.git"), true);
});

test("does not classify third-party RN apps as Yaver self-development", () => {
  assert.equal(isYaverSelfDevelopmentProject("todo", "/Users/me/Workspace/todo/mobile", "git@github.com:acme/todo.git"), false);
  assert.equal(isYaverSelfDevelopmentProject("todo", "/Users/me/Workspace/not-yaver.io-copy/mobile", ""), false);
  assert.equal(isYaverSelfDevelopmentProject("todo", "/tmp/repo", "git@github.com:acme/yaver-io-helper.git"), false);
});

test("Yaver self-development preserves browser, Hermes, and WebRTC actions", () => {
  const planned = guardYaverSelfDevelopmentActions(actions, "mobile", "/Users/me/Workspace/yaver.io/mobile");
  assert.deepEqual(planned.map((a) => a.type), actions.map((a) => a.type));
  assert.equal(planned.find((a) => a.type === "open-native")?.supported, true);
  assert.equal(planned.find((a) => a.type === "compile-hermes")?.supported, true);
});

test("third-party RN apps keep the agent/fallback order", () => {
  const planned = guardYaverSelfDevelopmentActions(actions, "todo", "/Users/me/Workspace/todo/mobile");
  assert.deepEqual(planned.map((a) => a.type), actions.map((a) => a.type));
  assert.equal(planned.find((a) => a.type === "open-native")?.supported, true);
});

// ── Detection-driven option lists ────────────────────────────────────────
// Hermes is React Native only. For any other stack the option must be ABSENT
// from the sheet, not greyed out — a disabled button still advertises a
// capability the project does not have.

test("applyPreviewCapabilities strips Hermes actions for Flutter", () => {
  const actions: MobileProjectAction[] = [
    { label: "Open in Yaver", target: ".", type: "open-native", framework: "expo" },
    { label: "Compile", target: ".", type: "compile-hermes", framework: "expo" },
    { label: "Dev server", target: ".", type: "dev-server", framework: "flutter" },
  ];
  const out = applyPreviewCapabilities(actions, {
    framework: "flutter",
    options: [
      { id: "dev-server", primary: true, supported: true },
      { id: "remote-runtime", supported: true },
    ],
  });
  assert.ok(!out.some((a) => a.type === "open-native"), "open-native survived for Flutter");
  assert.ok(!out.some((a) => a.type === "compile-hermes"), "compile-hermes survived for Flutter");
});

test("applyPreviewCapabilities strips Hermes actions for Kotlin and Swift", () => {
  for (const framework of ["kotlin", "swift"]) {
    const out = applyPreviewCapabilities(
      [
        { label: "Compile", target: ".", type: "compile-hermes", framework: "expo" },
        { label: "Remote Runtime", target: ".", type: "remote-runtime", framework },
      ],
      { framework, options: [{ id: "remote-runtime", primary: true, supported: true }] },
    );
    assert.ok(!out.some((a) => a.type === "compile-hermes"), `hermes survived for ${framework}`);
    assert.equal(out[0].type, "remote-runtime");
  }
});

test("applyPreviewCapabilities keeps Hermes for react-native", () => {
  const out = applyPreviewCapabilities(
    [
      { label: "Compile", target: ".", type: "compile-hermes", framework: "expo" },
      { label: "Dev server", target: ".", type: "dev-server", framework: "expo" },
    ],
    {
      framework: "expo",
      options: [
        { id: "compile-hermes", supported: true },
        { id: "open-native", supported: true, primary: true },
        { id: "dev-server", supported: true },
      ],
    },
  );
  assert.ok(out.some((a) => a.type === "compile-hermes"), "hermes stripped from an RN project");
});

test("Hermes shows only Compile before a bundle exists", () => {
  const out = applyPreviewCapabilities(
    [
      { label: "Hermes Reload", target: ".", type: "open-native", framework: "expo" },
      { label: "Compile Hermes bundle", target: ".", type: "compile-hermes", framework: "expo" },
    ],
    {
      framework: "expo",
      hermesBuildState: "needs_build",
      options: [
        { id: "open-native", supported: false },
        { id: "compile-hermes", supported: true },
      ],
    },
  );
  assert.deepEqual(out.map((action) => action.type), ["compile-hermes"]);
});

test("Hermes shows only Reload after a usable bundle exists", () => {
  const out = applyPreviewCapabilities(
    [
      { label: "Hermes Reload", target: ".", type: "open-native", framework: "expo" },
      { label: "Compile Hermes bundle", target: ".", type: "compile-hermes", framework: "expo" },
    ],
    {
      framework: "expo",
      hermesBuildState: "ready",
      options: [
        { id: "open-native", supported: true },
        { id: "compile-hermes", supported: true },
      ],
    },
  );
  assert.deepEqual(out.map((action) => action.type), ["open-native"]);
});

test("an old agent that returns both Hermes actions defaults safely to Compile", () => {
  const out = applyPreviewCapabilities(
    [
      { label: "Hermes Reload", target: ".", type: "open-native", framework: "expo" },
      { label: "Compile Hermes bundle", target: ".", type: "compile-hermes", framework: "expo" },
    ],
    {
      framework: "expo",
      options: [
        { id: "open-native", supported: true },
        { id: "compile-hermes", supported: true },
      ],
    },
  );
  assert.deepEqual(out.map((action) => action.type), ["compile-hermes"]);
});

test("applyPreviewCapabilities keeps Browser Reload first for react-native when the agent says so", () => {
  const out = applyPreviewCapabilities(
    [
      { label: "Browser Reload", target: ".", type: "dev-server", framework: "expo" },
      { label: "Hermes Reload", target: ".", type: "open-native", framework: "expo" },
      { label: "Compile", target: ".", type: "compile-hermes", framework: "expo" },
      { label: "WebRTC Reload", target: ".", type: "remote-runtime", framework: "expo" },
    ],
    {
      framework: "expo",
      options: [
        { id: "dev-server", supported: true, primary: true },
        { id: "open-native", supported: true },
        { id: "remote-runtime", supported: true },
      ],
    },
  );
  assert.deepEqual(out.map((a) => a.type), ["dev-server", "open-native", "remote-runtime"]);
});

test("applyPreviewCapabilities carries the agent's reason onto a disabled action", () => {
  const out = applyPreviewCapabilities(
    [{ label: "Open in Yaver", target: ".", type: "open-native", framework: "expo", supported: true }],
    {
      framework: "expo",
      options: [
        { id: "open-native", supported: false, reason: "no paired device — connect one to use this" },
      ],
    },
  );
  assert.equal(out[0].supported, false);
  assert.match(out[0].reason || "", /no paired device/);
});

test("applyPreviewCapabilities leads with the agent's primary option", () => {
  const out = applyPreviewCapabilities(
    [
      { label: "Dev server", target: ".", type: "dev-server", framework: "expo" },
      { label: "Stream", target: ".", type: "remote-runtime", framework: "expo" },
    ],
    { framework: "expo", options: [{ id: "remote-runtime", primary: true, supported: true }] },
  );
  assert.equal(out[0].type, "remote-runtime");
});

// An older agent that doesn't know the verb must not produce an empty sheet.
test("applyPreviewCapabilities degrades to the composed actions when the agent cannot answer", () => {
  const actions: MobileProjectAction[] = [
    { label: "Compile", target: ".", type: "compile-hermes", framework: "expo" },
  ];
  assert.deepEqual(applyPreviewCapabilities(actions, undefined), actions);
  assert.deepEqual(applyPreviewCapabilities(actions, null), actions);
  assert.deepEqual(applyPreviewCapabilities(actions, { options: [] }), actions);
});

// ── Composition from agent capabilities (1g) ─────────────────────────────
// The agent's options COMPOSE the sheet; the locally built lanes are
// templates + fallback. Two regressions pinned here:
//   1. an agent-offered option with no local template (wire-push) was
//      silently DROPPED — the phone hid a capability the box explicitly
//      advertised;
//   2. an option id this app version doesn't know yet must render DISABLED
//      with the agent's label+reason — never vanish.

test("an agent-offered wire-push option is composed, not dropped", () => {
  const out = applyPreviewCapabilities(
    [
      { label: "Browser Reload", target: ".", type: "dev-server", framework: "expo", supported: true },
      { label: "WebRTC Reload", target: ".", type: "remote-runtime", framework: "expo", supported: true },
    ],
    {
      framework: "expo",
      options: [
        { id: "dev-server", supported: true, primary: true },
        { id: "wire-push", label: "Install on connected device", supported: true },
      ],
    },
  );
  const wire = out.find((a) => a.type === "wire-push");
  assert.ok(wire, "wire-push was dropped");
  assert.equal(wire?.supported, true);
  assert.equal(wire?.label, "Install on connected device");
});

test("an unknown option id renders disabled with the agent's label and reason", () => {
  const out = applyPreviewCapabilities(
    [{ label: "Browser Reload", target: ".", type: "dev-server", framework: "expo", supported: true }],
    {
      framework: "expo",
      options: [
        { id: "dev-server", supported: true },
        { id: "holo-projector", label: "Holo projector", supported: true, reason: "renders on the desk" },
      ],
    },
  );
  const unknown = out.find((a) => a.type === "holo-projector");
  assert.ok(unknown, "unknown option vanished — the agent advertised it");
  assert.equal(unknown?.supported, false, "unknown option must render disabled");
  assert.equal(unknown?.label, "Holo projector");
  assert.ok(unknown?.reason, "needs a reason line for the disabled state");
});

test("agent option order + primary-first is preserved in composition", () => {
  const out = applyPreviewCapabilities(
    [
      { label: "Browser Reload", target: ".", type: "dev-server", framework: "expo" },
      { label: "WebRTC Reload", target: ".", type: "remote-runtime", framework: "expo" },
    ],
    {
      framework: "expo",
      options: [
        { id: "remote-runtime", supported: true },
        { id: "dev-server", supported: true, primary: true },
      ],
    },
  );
  assert.equal(out[0].type, "dev-server");
  assert.equal(out[1].type, "remote-runtime");
});

// ── Monorepo sub-app lanes (1g) ──────────────────────────────────────────
// Tapping a monorepo project must offer its sub-apps (mobile · expo /
// web · next) as concrete browser lanes — web has this via /workspace/apps;
// mobile previously had no client for the route at all.

test("workspaceAppLanes maps sub-apps to per-target dev-server actions", () => {
  const lanes = workspaceAppLanes([
    { name: "mobile", path: "mobile", framework: "expo", kind: "mobile", exists: true },
    { name: "web", path: "web", framework: "nextjs", kind: "web", exists: true },
    { name: "ghost", path: "gone", framework: "vite", exists: false },
  ]);
  assert.equal(lanes.length, 2, "non-existent app must be excluded");
  assert.deepEqual(lanes.map((l) => l.target), ["mobile", "web"]);
  assert.ok(lanes.every((l) => l.type === "dev-server"));
  assert.match(lanes[0].label, /mobile.*expo/i);
  assert.match(lanes[1].label, /web.*next/i);
});

test("workspaceAppLanes yields nothing for a single-app or empty workspace", () => {
  assert.deepEqual(workspaceAppLanes([]), []);
  assert.deepEqual(
    workspaceAppLanes([{ name: "app", path: ".", framework: "expo", exists: true }]),
    [],
    "a single app is the project itself — no sub-app step",
  );
});
