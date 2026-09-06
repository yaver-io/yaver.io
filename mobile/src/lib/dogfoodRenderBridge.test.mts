import assert from "node:assert/strict";
import test from "node:test";
import {
  attachedDogfoodCheckout,
  dogfoodGuestProjectName,
  dogfoodProjectRootPath,
  DOGFOOD_RENDER_MESSAGE,
  isAttachedDogfoodWebRuntime,
  isPathInsideAttachedDogfoodCheckout,
  makeDogfoodRenderMessage,
  normalizedDogfoodPath,
  parseDogfoodRenderMessage,
} from "./dogfoodRenderBridge.ts";

test("dogfood render messages round-trip without carrying authority", () => {
  assert.deepEqual(parseDogfoodRenderMessage(makeDogfoodRenderMessage("task-1")), {
    type: DOGFOOD_RENDER_MESSAGE,
    source: "task-1",
  });
  assert.equal(parseDogfoodRenderMessage('{"type":"something-else"}'), null);
  assert.equal(parseDogfoodRenderMessage("not-json"), null);
});

test("only an attached WebView runtime may use the bridge", () => {
  const postMessage = () => {};
  assert.equal(isAttachedDogfoodWebRuntime({
    localStorage: { getItem: () => "1" },
    ReactNativeWebView: { postMessage },
  }), true);
  assert.equal(isAttachedDogfoodWebRuntime({
    localStorage: { getItem: () => null },
    ReactNativeWebView: { postMessage },
  }), false);
  assert.equal(isAttachedDogfoodWebRuntime({ localStorage: { getItem: () => "1" } }), false);
});

test("the attached checkout marker hides only Yaver's active checkout tree", () => {
  const values: Record<string, string> = {
    "yaver.attach.mode": "1",
    "yaver.attach.checkout": "/work/yaver.io/",
  };
  const scope = {
    localStorage: { getItem: (key: string) => values[key] ?? null },
    ReactNativeWebView: { postMessage: () => {} },
  };
  assert.equal(attachedDogfoodCheckout(scope), "/work/yaver.io");
  assert.equal(isPathInsideAttachedDogfoodCheckout("/work/yaver.io", "/work/yaver.io"), true);
  assert.equal(isPathInsideAttachedDogfoodCheckout("/work/yaver.io/mobile", "/work/yaver.io"), true);
  assert.equal(isPathInsideAttachedDogfoodCheckout("/work/yaver.io-copy", "/work/yaver.io"), false);
  assert.equal(isPathInsideAttachedDogfoodCheckout("/work/another-app", "/work/yaver.io"), false);
  assert.equal(isPathInsideAttachedDogfoodCheckout("file:///work/yaver.io/mobile?lane=browser", "/work/yaver.io"), true);
  assert.equal(isPathInsideAttachedDogfoodCheckout("C:\\Workspace\\YAVER.IO\\mobile", "c:/workspace/yaver.io"), true);
  assert.equal(attachedDogfoodCheckout({
    localStorage: { getItem: (key: string) => key === "yaver.attach.checkout" ? "/work/yaver.io" : null },
    ReactNativeWebView: { postMessage: () => {} },
  }), null, "checkout identity must not affect Production mode");
});

test("dogfood path helpers normalize slashes and recover the repo root", () => {
  assert.equal(normalizedDogfoodPath("/work/yaver.io/mobile/"), "/work/yaver.io/mobile");
  assert.equal(normalizedDogfoodPath("C:\\Workspace\\yaver.io\\mobile\\"), "c:/workspace/yaver.io/mobile");
  assert.equal(dogfoodProjectRootPath("/work/yaver.io/mobile", "/work/yaver.io"), "/work/yaver.io");
  assert.equal(dogfoodProjectRootPath("/work/other/mobile", "/work/yaver.io"), "/work/other/mobile");
});

test("the Yaver container names SFMG and Talos as guests, not as root", () => {
  assert.equal(dogfoodGuestProjectName("/workspaces/sfmg", "root (sfmg) / mobile"), "sfmg");
  assert.equal(dogfoodGuestProjectName("/workspaces/talos", "root (talos)"), "talos");
  assert.equal(dogfoodGuestProjectName("C:\\Workspace\\talos", ""), "talos");
  assert.equal(dogfoodGuestProjectName("/Users/me/Workspace/yaver.io/mobile", ""), "yaver.io");
});
