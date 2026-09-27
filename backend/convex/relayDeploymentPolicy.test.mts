import test from "node:test";
import assert from "node:assert/strict";
import {
  relayDynamicProvisioningEnabled,
  relayFleetCreationDecision,
  relayImageRef,
  relayMaxHourlyEur,
  relayMaxDynamicHosts,
  pinnedHybridRelay,
} from "./relayDeploymentPolicy.ts";

const digest = "a".repeat(64);

test("managed relay image requires an immutable sha256 digest", () => {
  assert.equal(relayImageRef({ YAVER_RELAY_IMAGE: `ghcr.io/yaver/relay@sha256:${digest}` }), `ghcr.io/yaver/relay@sha256:${digest}`);
  assert.equal(relayImageRef({ YAVER_RELAY_IMAGE: "ghcr.io/yaver/relay:latest" }), null);
  assert.equal(relayImageRef({}), null);
});

test("managed relay image rejects shell or yaml injection", () => {
  assert.equal(relayImageRef({ YAVER_RELAY_IMAGE: `ghcr.io/yaver/relay@sha256:${digest}\ncommand: bad` }), null);
  assert.equal(relayImageRef({ YAVER_RELAY_IMAGE: `$(touch /tmp/no)@sha256:${digest}` }), null);
});

test("dynamic provisioning is opt-in and fleet capped", () => {
  assert.equal(relayDynamicProvisioningEnabled({}), false);
  assert.equal(relayDynamicProvisioningEnabled({ YAVER_RELAY_DYNAMIC_PROVISIONING_ENABLED: "true" }), true);
  assert.equal(relayDynamicProvisioningEnabled({ YAVER_RELAY_DYNAMIC_PROVISIONING_ENABLED: "1" }), false);
  assert.equal(relayMaxDynamicHosts({}), 2);
  assert.equal(relayMaxDynamicHosts({ YAVER_RELAY_MAX_DYNAMIC_HOSTS: "0" }), 0);
  assert.equal(relayMaxDynamicHosts({ YAVER_RELAY_MAX_DYNAMIC_HOSTS: "7" }), 7);
});

test("hourly relay spend can be lowered by env but never raised without code", () => {
  assert.equal(relayMaxHourlyEur({}), 0.04);
  assert.equal(relayMaxHourlyEur({ YAVER_RELAY_MAX_HOURLY_EUR: "0.025" }), 0.025);
  assert.equal(relayMaxHourlyEur({ YAVER_RELAY_MAX_HOURLY_EUR: "4" }), 0.04);
  assert.equal(relayMaxHourlyEur({ YAVER_RELAY_MAX_HOURLY_EUR: "garbage" }), 0.04);
});

test("fleet creation denies disabled and exhausted configurations", () => {
  assert.equal(relayFleetCreationDecision({
    dynamicProvisioningEnabled: false,
    currentDynamicHosts: 0,
    maxDynamicHosts: 2,
  }).allow, false);
  assert.equal(relayFleetCreationDecision({
    dynamicProvisioningEnabled: true,
    currentDynamicHosts: 2,
    maxDynamicHosts: 2,
  }).allow, false);
  assert.equal(relayFleetCreationDecision({
    dynamicProvisioningEnabled: true,
    currentDynamicHosts: 1,
    maxDynamicHosts: 2,
  }).allow, true);
});

test("pinned hybrid inventory is all-or-nothing and defaults conservatively", () => {
  assert.equal(pinnedHybridRelay({}), null);
  assert.throws(
    () => pinnedHybridRelay({ YAVER_RELAY_HYBRID_HOST_KEY: "relay-anchor-eu" }),
    /partial/,
  );
  assert.deepEqual(pinnedHybridRelay({
    YAVER_RELAY_HYBRID_HOST_KEY: "relay-anchor-eu",
    YAVER_RELAY_HYBRID_REGION: "EU",
    YAVER_RELAY_HYBRID_DOMAIN: "public.relay.example.com",
    YAVER_RELAY_HYBRID_SERVER_ID: "123",
    YAVER_RELAY_HYBRID_SERVER_IP: "203.0.113.10",
  }), {
    hostKey: "relay-anchor-eu",
    region: "eu",
    hostname: "public.relay.example.com",
    serverId: "123",
    serverIp: "203.0.113.10",
    capacity: 10,
  });
});
