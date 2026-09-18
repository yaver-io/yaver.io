import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const read = (relativePath) => readFileSync(join(here, relativePath), "utf8");

const mainManifest = read("../../android/app/src/main/AndroidManifest.xml");
const releaseManifest = read("../../android/app/src/release/AndroidManifest.xml");
const service = read("../../android/app/src/main/java/io/yaver/mobile/sandbox/SandboxService.kt");

assert.match(mainManifest, /android:foregroundServiceType="specialUse"/,
  "the phone-host agent must declare its specialUse foreground-service type");
assert.match(mainManifest, /android:foregroundServiceType="dataSync"/,
  "finite phone-local tasks must declare their dataSync foreground-service type");
assert.match(mainManifest, /user_started_on_device_coding_agent_for_local_development_tasks_while_app_is_backgrounded/,
  "the specialUse subtype must explain the reviewed user-facing operation");

assert.doesNotMatch(releaseManifest, /FOREGROUND_SERVICE(?:_SPECIAL_USE|_DATA_SYNC)?[^\n]*tools:node="remove"/,
  "release builds must not strip permissions while retaining foreground services");
assert.doesNotMatch(releaseManifest, /SandboxService[^\n]*tools:node="remove"/,
  "the Play-review build must contain the user-facing phone-host service it declares");

assert.match(service, /\.setContentIntent\(openTaskIntent\(ctx, null\)\)/,
  "the ongoing sandbox notification must return the user to Yaver");
assert.match(service, /NotificationCompat\.Action\([^\n]+"Stop"/,
  "the ongoing sandbox notification must let the user stop the service directly");
assert.match(service, /private fun onAgentExited\(/,
  "unexpected native-agent exit must have an explicit cleanup path");
assert.match(service, /releaseWakeLock\(\)[\s\S]*stopForeground\(STOP_FOREGROUND_REMOVE\)[\s\S]*stopSelf\(\)/,
  "agent-exit cleanup must release power and remove the truthful ongoing notification");

console.log("Android foreground-service contract passed");
