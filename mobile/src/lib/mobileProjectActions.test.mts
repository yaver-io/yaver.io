import test from "node:test";
import assert from "node:assert/strict";
import {
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
