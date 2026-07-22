import test from "node:test";
import assert from "node:assert/strict";
import {
  applyPreviewCapabilities,
  guardYaverSelfDevelopmentActions,
  isYaverSelfDevelopmentProject,
  YAVER_SELF_DEV_HERMES_BLOCK_REASON,
  type MobileProjectAction,
} from "./mobileProjectActions";

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
});

test("Yaver self-development puts WebRTC first and blocks Hermes actions", () => {
  const planned = guardYaverSelfDevelopmentActions(actions, "mobile", "/Users/me/Workspace/yaver.io/mobile");

  assert.equal(planned[0].type, "remote-runtime");
  assert.equal(planned[0].label, "Stream over WebRTC");

  const openNative = planned.find((a) => a.type === "open-native");
  const compile = planned.find((a) => a.type === "compile-hermes");
  assert.equal(openNative?.supported, false);
  assert.equal(compile?.supported, false);
  assert.equal(openNative?.reason, YAVER_SELF_DEV_HERMES_BLOCK_REASON);
});

test("third-party RN apps keep the existing Hermes-first order", () => {
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
