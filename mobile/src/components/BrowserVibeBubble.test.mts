import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const here = dirname(fileURLToPath(import.meta.url));
const wrapper = readFileSync(join(here, "BrowserVibeBubble.tsx"), "utf8");
const entry = readFileSync(join(here, "../../../sdk/feedback/react-native/src/DogfoodEntryIcon.tsx"), "utf8");
const controlMenu = readFileSync(join(here, "../../../sdk/feedback/react-native/src/DogfoodControlMenu.tsx"), "utf8");
const quickControls = readFileSync(join(here, "../../../sdk/feedback/react-native/src/DogfoodQuickControls.tsx"), "utf8");
const overlay = readFileSync(join(here, "../context/DogfoodOverlayContext.tsx"), "utf8");
const menu = readFileSync(join(here, "../../../sdk/feedback/react-native/src/DogfoodNativeMenu.tsx"), "utf8");
const dogfood = readFileSync(join(here, "../../app/(tabs)/dogfood.tsx"), "utf8");
const more = readFileSync(join(here, "../../app/(tabs)/more.tsx"), "utf8");

test("the running app Y opens the same explicit SDK menu and message composer", () => {
  assert.match(wrapper, /<DogfoodEntryIcon/);
  assert.match(wrapper, /<DogfoodControlMenu/);
  assert.match(wrapper, /<StudioChatPane/);
  assert.match(entry, /testID="yaver-dogfood-entry"/);
  assert.doesNotMatch(entry, /<Modal/);
  assert.match(wrapper, /onPress=\{\(\) => setMenuOpen\(true\)\}/);
  assert.match(quickControls, /<DogfoodControlMenu/);
  assert.match(controlMenu, /yaver-dogfood-chat/);
  assert.match(controlMenu, /yaver-dogfood-settings/);
  assert.match(controlMenu, /yaver-dogfood-exit/);
});

test("the Y is draggable, visible by default, and can hide itself", () => {
  assert.match(entry, /PanResponder\.create/);
  assert.match(entry, /hidden \?\? preferenceHidden/);
  assert.match(entry, /onLongPress/);
  assert.match(entry, /setDogfoodEntryIconHidden\(true, preferenceScope\)/);
  assert.match(entry, /restore it in Dogfood Settings/);
  assert.match(entry, /width - iconSize - edgeInset/);
});

test("the native library menu owns stateful runtime, tasks, and settings", () => {
  assert.match(menu, /active \? \(/);
  assert.match(menu, /Reload Dogfood/);
  assert.match(menu, /'Exit Dogfood'/);
  assert.match(menu, /'Stop Dogfood'/);
  assert.match(menu, /DogfoodLaunchingWidget/);
  assert.match(menu, />Tasks</);
  assert.match(menu, />Settings</);
  assert.match(dogfood, /<DogfoodNativeMenu/);
  assert.match(dogfood, /runtime\.reload\("fast"\)/);
  assert.match(dogfood, /runtime\.end\(\)/);
  assert.match(dogfood, /colors=\{\{ card: c\.bgCard/);
});

test("Exit Dogfood tears down the remote browser lane instead of only dismissing UI", () => {
  assert.match(wrapper, /onPress: \(\) => \{ setMenuOpen\(false\); onExitPreview\(\); \}/);
  assert.match(overlay, /onExitPreview=\{\(\) => \{ void end\(\); \}\}/);
  assert.match(overlay, /registerCleanup\(async \(\) => \{ await stopAttachSession\(next\.deviceId\); \}, "session"\)/);
  assert.match(overlay, /registerCleanup\(async \(\) => \{ await stopDogfoodDevLane\(next\.deviceId\); \}, "session"\)/);
  assert.match(overlay, /if \(controller\) await controller\.stop\(\)/);
});

test("More exposes one Dogfood destination", () => {
  const labels = more.match(/>Dogfood(?: Settings)?</g) || [];
  assert.equal(labels.length, 1);
  assert.match(more, /Launch, reload, tasks, and settings/);
});
