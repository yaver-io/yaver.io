import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const mobileRoot = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const reload = readFileSync(join(mobileRoot, "app/(tabs)/hotreload.tsx"), "utf8");
const banner = readFileSync(join(mobileRoot, "src/components/RemoteBoxBanner.tsx"), "utf8");
const taskTarget = readFileSync(join(mobileRoot, "src/components/TaskTargetWizard.tsx"), "utf8");
const cloudProviders = readFileSync(join(mobileRoot, "src/components/CloudProvidersSection.tsx"), "utf8");

assert.doesNotMatch(
  reload,
  /remoteHermesReady|Hermes reload prerequisites missing|Hermes reload ready/,
  "the machine connection banner must not present Hermes as a connection prerequisite",
);
assert.doesNotMatch(
  reload,
  /capabilitySnapshot\(\)/,
  "the connection banner must not fetch preview capability merely to describe a machine connection",
);
assert.match(
  banner,
  /connecting is useful for SSH\/tasks/,
  "the shared banner contract must preserve SSH/task connectivity independently of preview runtimes",
);
assert.doesNotMatch(
  taskTarget,
  /Live · tap to connect/,
  "a heartbeat must not be presented as proven live transport",
);
assert.match(
  taskTarget,
  /Reported online · connect to verify/,
  "an unprobed task target must state that connecting is the verification",
);
assert.match(
  cloudProviders,
  /host: "127\.0\.0\.1"[\s\S]{0,100}port: 18080/,
  "Android cable handoff must use the explicit adb-reversed loopback endpoint",
);
assert.match(
  cloudProviders,
  /receiveLocalHetznerFromConnectedEndpoint\(user\.id, token \|\| "", \{/,
  "USB retrieval must reuse the encrypted same-account handoff protocol",
);
assert.doesNotMatch(
  cloudProviders,
  /adb (?:shell|push|am)|Clipboard\.setStringAsync\([^)]*token/,
  "the provider credential must not enter adb arguments or the Android clipboard",
);

console.log("Connection and Hermes capability stay separate");
