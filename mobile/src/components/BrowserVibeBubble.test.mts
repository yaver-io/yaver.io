import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const here = dirname(fileURLToPath(import.meta.url));
const wrapper = readFileSync(join(here, "BrowserVibeBubble.tsx"), "utf8");
const entry = readFileSync(join(here, "../../../sdk/feedback/react-native/src/DogfoodEntryIcon.tsx"), "utf8");
const menu = readFileSync(join(here, "../../../sdk/feedback/react-native/src/DogfoodNativeMenu.tsx"), "utf8");
const dogfood = readFileSync(join(here, "../../app/(tabs)/dogfood.tsx"), "utf8");
const more = readFileSync(join(here, "../../app/(tabs)/more.tsx"), "utf8");

test("the running app receives one shared-library Y and no overlay card", () => {
  assert.match(wrapper, /<DogfoodEntryIcon/);
  assert.doesNotMatch(wrapper, /Modal|StudioChatPane|KeyboardAvoidingView|Fast Reload/);
  assert.match(entry, /testID="yaver-dogfood-entry"/);
  assert.doesNotMatch(entry, /<Modal/);
  assert.match(wrapper, /onPress=\{onGoHome \|\| onExitPreview\}/);
});

test("the Y is draggable, visible by default, and can hide itself", () => {
  assert.match(entry, /PanResponder\.create/);
  assert.match(entry, /hidden \?\? preferenceHidden/);
  assert.match(entry, /onLongPress/);
  assert.match(entry, /setDogfoodEntryIconHidden\(true, preferenceScope\)/);
  assert.match(entry, /restore it in Dogfood Settings/);
});

test("the native library menu owns stateful runtime, tasks, and settings", () => {
  assert.match(menu, /active \? \(/);
  assert.match(menu, /Reload Dogfood/);
  assert.match(menu, /'Exit Dogfood'/);
  assert.match(menu, /'Stop Dogfood'/);
  assert.match(menu, /Launch Dogfood/);
  assert.match(menu, />Tasks</);
  assert.match(menu, />Settings</);
  assert.match(dogfood, /<DogfoodNativeMenu/);
  assert.match(dogfood, /runtime\.reload\("fast"\)/);
  assert.match(dogfood, /runtime\.end\(\)/);
  assert.match(dogfood, /colors=\{\{ card: c\.bgCard/);
});

test("More exposes one Dogfood destination", () => {
  const labels = more.match(/>Dogfood(?: Settings)?</g) || [];
  assert.equal(labels.length, 1);
  assert.match(more, /Launch, reload, tasks, and settings/);
});
