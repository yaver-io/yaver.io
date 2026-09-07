#!/usr/bin/env node

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

const httpsConfigs = [
  "relay/deploy/nginx-public-relay.conf",
  "relay/deploy/nginx-relay.conf",
  "scripts/setup-relay-wildcard.sh",
];
for (const relative of httpsConfigs) {
  const source = readFileSync(resolve(root, relative), "utf8");
  assert.match(source, /listen\s+443\s+ssl/, `${relative} lost its IPv4 HTTPS listener`);
  assert.match(source, /listen\s+\[::\]:443\s+ssl/, `${relative} lost its IPv6 HTTPS listener`);
}

const bootstrapConfigs = [
  "scripts/provision-relay.sh",
  "scripts/install-relay.sh",
  "web/public/install-relay.sh",
  "backend/convex/provisionRelay.ts",
];
for (const relative of bootstrapConfigs) {
  const source = readFileSync(resolve(root, relative), "utf8");
  assert.match(source, /listen\s+80;/, `${relative} lost its IPv4 ACME/HTTP listener`);
  assert.match(source, /listen\s+\[::\]:80;/, `${relative} lost its IPv6 ACME/HTTP listener`);
}

const mobile = readFileSync(resolve(root, "mobile/src/context/DeviceContext.tsx"), "utf8");
assert.match(mobile, /quicAddr:\s*["']public\.yaver\.io:4433["']/,
  "mobile free-relay fallback must use dual-stack DNS, not a literal address");
assert.doesNotMatch(mobile, /quicAddr:\s*["']46\.224\.110\.38:4433["']/,
  "mobile free-relay fallback regressed to IPv4-only");

const coreDeviceCopies = [
  "sdk/feedback/react-native/src/_core/device.ts",
  "mobile/src/_core/device.ts",
  "web/lib/_core/device.ts",
];
const canonicalCore = readFileSync(resolve(root, coreDeviceCopies[0]), "utf8");
for (const relative of coreDeviceCopies) {
  const source = readFileSync(resolve(root, relative), "utf8");
  assert.equal(source, canonicalCore, `${relative} drifted from the shared device transport core`);
  assert.match(source, /import \{ agentHttpBase \} from ['"]\.\/endpoints['"]/,
    `${relative} must import the shared IPv6-safe endpoint formatter`);
  assert.match(source, /agentHttpBase\(ip, port\)/,
    `${relative} must route probe hosts through the shared formatter`);
}

const endpointCore = readFileSync(resolve(root, "shared/client-core/src/endpoints.ts"), "utf8");
assert.match(endpointCore, /import \{ urlHost \} from ['"]\.\/urlHost['"]/,
  "shared agent endpoint builder must use the canonical IPv6 formatter");

const coreURLHostCopies = [
  "sdk/feedback/react-native/src/_core/urlHost.ts",
  "mobile/src/_core/urlHost.ts",
  "web/lib/_core/urlHost.ts",
];
const canonicalURLHost = readFileSync(resolve(root, coreURLHostCopies[0]), "utf8");
for (const relative of coreURLHostCopies) {
  assert.equal(readFileSync(resolve(root, relative), "utf8"), canonicalURLHost,
    `${relative} drifted from the shared IPv6 URL-host formatter`);
}

const flutterDevice = readFileSync(resolve(root, "sdk/feedback/flutter/lib/src/device.dart"), "utf8");
assert.match(flutterDevice, /ip\.contains\(['"]:['"]\).*ip\.startsWith\(['"]\[['"]\).*ip\.endsWith\(['"]\]['"]\)/,
  "Flutter feedback SDK must bracket IPv6 probe hosts");

const managedProvision = readFileSync(resolve(root, "backend/convex/provisionRelay.ts"), "utf8");
assert.match(managedProvision, /type:\s*["']AAAA["']/,
  "managed relay provisioning must create an IPv6 DNS record");
assert.match(managedProvision, /serverIpv6:\s*existingHost\.serverIpv6/,
  "shared relay tenants must inherit the host IPv6 address");

console.log("relay dual-stack parity: ok");
