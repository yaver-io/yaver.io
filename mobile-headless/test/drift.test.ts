// Drift detector — only the handful of QuicClient methods that
// mobile-headless commits to mirroring need to survive in the
// mobile lib. Everything else is app-internal and the harness
// doesn't care. Failing CI here is a signal that mobile broke an
// expectation the headless surrogate (and everyone who tests
// against it) relies on.

import { describe, it, expect } from "bun:test";
import * as fs from "node:fs";
import * as path from "node:path";

const REQUIRED_ON_QUIC_CLIENT = [
  // devices + agent
  "infraSummary",
  "getRunners",
  "startExec",
  "getExec",
  "listExecs",
  // install catalogue (new this cycle, easy to remove accidentally)
  "listInstallables",
  "installTool",
  "respondInstallSudo",
  "subscribeStream",
  // wizard — the mobile "new project" flow hinges on these
  "wizardStart",
  "wizardAnswer",
  "wizardGenerate",
  "wizardQuestions",
];

describe("mobile lib surface drift", () => {
  it("keeps the methods mobile-headless expects to exist", () => {
    const quicPath = path.resolve(__dirname, "../../mobile/src/lib/quic.ts");
    const src = fs.readFileSync(quicPath, "utf8");
    const missing = REQUIRED_ON_QUIC_CLIENT.filter(
      (name) => !new RegExp(`\\b(?:async\\s+)?${name}\\s*\\(`).test(src),
    );
    if (missing.length) {
      console.error(
        "Methods disappeared from mobile/src/lib/quic.ts — the headless\n" +
        "surrogate and any test that calls these will break:\n" +
        missing.map((m) => "  - " + m).join("\n"),
      );
    }
    expect(missing).toEqual([]);
  });

  it("keeps the multi-IP + parallel race + presence hooks wired in quic.ts", () => {
    const quicPath = path.resolve(__dirname, "../../mobile/src/lib/quic.ts");
    const src = fs.readFileSync(quicPath, "utf8");
    // These names are the contract the headless surrogate's `raceDevicePaths`
    // + applyRelayPresence mirror. If any vanishes from quic.ts the real
    // mobile connect loop has lost the matching behaviour and tests using
    // the headless harness would silently pass with stale semantics.
    const required = [
      "raceDirectCandidates",      // parallel /health race across beacon + lanIps + host
      "relayServersSnapshot",      // DeviceContext hits /presence on the primary relay via this getter
      "lan-tailscale",             // connection-path label for 100.64.0.0/10 candidates
      "lan-heartbeat",             // connection-path label for other heartbeat-advertised IPs
      "_lanIps",                   // private field that threads Device.lanIps into the race
    ];
    const missing = required.filter((name) => !src.includes(name));
    expect(missing).toEqual([]);
  });

  it("keeps the auto-connect + primary-device plumbing on DeviceContext", () => {
    const ctxPath = path.resolve(__dirname, "../../mobile/src/context/DeviceContext.tsx");
    const src = fs.readFileSync(ctxPath, "utf8");
    const required = [
      "primaryDeviceId",           // context state + API surfaced to screens
      "setPrimaryDevice",          // public setter that POSTs to /settings
      "applyRelayPresence",        // merge relay tunnel-up state into the device list
      "lanIps",                    // normalised Device field fed into quicClient.connect()
    ];
    const missing = required.filter((name) => !src.includes(name));
    expect(missing).toEqual([]);
  });

  it("keeps the Convex + agent wire contract for localIps + primaryDeviceId", () => {
    const schemaPath = path.resolve(__dirname, "../../backend/convex/schema.ts");
    const devicesPath = path.resolve(__dirname, "../../backend/convex/devices.ts");
    const authPath = path.resolve(__dirname, "../../desktop/agent/auth.go");
    const mainPath = path.resolve(__dirname, "../../desktop/agent/main.go");
    const schema = fs.readFileSync(schemaPath, "utf8");
    const devices = fs.readFileSync(devicesPath, "utf8");
    const authGo = fs.readFileSync(authPath, "utf8");
    const mainGo = fs.readFileSync(mainPath, "utf8");
    // Schema: both optional fields are declared on devices + userSettings.
    expect(schema).toContain("localIps: v.optional(v.array(v.string()))");
    expect(schema).toContain("primaryDeviceId: v.optional(v.string())");
    // devices.ts: heartbeat accepts localIps and listMyDevices surfaces it.
    expect(devices).toContain("localIps: v.optional(v.array(v.string()))");
    expect(devices).toContain("localIps: d.localIps ?? []");
    // Agent: SendHeartbeat ships localIps, and getLocalIPs enumerates them.
    expect(authGo).toContain("localIps []string");
    expect(mainGo).toContain("func getLocalIPs()");
  });
});
