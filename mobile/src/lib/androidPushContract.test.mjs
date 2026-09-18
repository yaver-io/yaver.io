import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");
const projectGradle = read("../../android/build.gradle");
const appGradle = read("../../android/app/build.gradle");
const googleServices = JSON.parse(read("../../google-services.json"));
const pushAuth = read("./pushAuth.ts");
const snapshotBackend = read("../../../backend/convex/agentTaskSnapshots.ts");
const pushBackend = read("../../../backend/convex/pushNotifications.ts");

test("Android release initializes Firebase for io.yaver.mobile", () => {
  assert.match(projectGradle, /com\.google\.gms:google-services:4\.5\.0/);
  assert.match(appGradle, /apply plugin: ["']com\.google\.gms\.google-services["']/);
  const client = googleServices.client.find(
    (item) => item.client_info?.android_client_info?.package_name === "io.yaver.mobile",
  );
  assert.ok(client, "google-services.json must contain the Play package");
  assert.equal(googleServices.project_info?.project_id, "yaver-io");
});

test("Android registers a native FCM token instead of depending on Expo push", () => {
  assert.match(pushAuth, /getDevicePushTokenAsync\(\)/);
  assert.match(pushAuth, /transport\s*=\s*["']fcm["']/);
  assert.match(pushAuth, /ANDROID_TOKEN_RETRY_DELAYS_MS/);
  assert.match(pushAuth, /addPushTokenListener/);
});

test("terminal task snapshot transitions schedule an account-scoped push", () => {
  assert.match(snapshotBackend, /sendTaskLifecyclePush/);
  assert.match(snapshotBackend, /ctx\.scheduler\.runAfter/);
  assert.match(pushBackend, /https:\/\/fcm\.googleapis\.com\/v1\/projects/);
  assert.match(pushBackend, /FIREBASE_SERVICE_ACCOUNT_JSON/);
  assert.match(pushBackend, /kind:\s*["']task-review["']/);
});
