"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const root = path.join(__dirname, "..", "..");
const read = (relative) => fs.readFileSync(path.join(root, relative), "utf8");

test("Microsoft Store package is x64 full-node AppX with bounded capabilities", () => {
  const old = {
    production: process.env.YAVER_STORE_PRODUCTION,
    identity: process.env.YAVER_STORE_IDENTITY_NAME,
    publisher: process.env.YAVER_STORE_PUBLISHER,
    display: process.env.YAVER_STORE_PUBLISHER_DISPLAY_NAME,
    productDisplay: process.env.YAVER_STORE_DISPLAY_NAME,
  };
  try {
    delete process.env.YAVER_STORE_PRODUCTION;
    delete process.env.YAVER_STORE_IDENTITY_NAME;
    delete process.env.YAVER_STORE_PUBLISHER;
    delete process.env.YAVER_STORE_PUBLISHER_DISPLAY_NAME;
    delete process.env.YAVER_STORE_DISPLAY_NAME;
    const configPath = require.resolve("../electron-builder.microsoft-store.cjs");
    delete require.cache[configPath];
    const config = require(configPath);
    assert.deepEqual(config.win.target, ["appx"]);
    assert.equal(config.directories.output, "dist-microsoft-store");
    assert.deepEqual(config.appx.capabilities, ["internetClient", "privateNetworkClientServer", "runFullTrust"]);
    assert.equal(config.appx.identityName, "Yaver.Validation");
    assert.equal(config.appx.publisherDisplayName, "SIMKAB");
    assert.equal(config.appx.applicationId, "Yaver");
    assert.equal(config.appx.displayName, "Yaver");
    assert.equal(config.extraResources.some((entry) => entry.from === "resources/bin/yaver.exe"), true);
    assert.equal(config.extraResources.some((entry) => entry.to === "LICENSE.txt"), true);
  } finally {
    if (old.production === undefined) delete process.env.YAVER_STORE_PRODUCTION; else process.env.YAVER_STORE_PRODUCTION = old.production;
    if (old.identity === undefined) delete process.env.YAVER_STORE_IDENTITY_NAME; else process.env.YAVER_STORE_IDENTITY_NAME = old.identity;
    if (old.publisher === undefined) delete process.env.YAVER_STORE_PUBLISHER; else process.env.YAVER_STORE_PUBLISHER = old.publisher;
    if (old.display === undefined) delete process.env.YAVER_STORE_PUBLISHER_DISPLAY_NAME; else process.env.YAVER_STORE_PUBLISHER_DISPLAY_NAME = old.display;
    if (old.productDisplay === undefined) delete process.env.YAVER_STORE_DISPLAY_NAME; else process.env.YAVER_STORE_DISPLAY_NAME = old.productDisplay;
  }
});

test("production Store package fails closed without exact Partner Center identity", () => {
  const old = process.env.YAVER_STORE_PRODUCTION;
  try {
    process.env.YAVER_STORE_PRODUCTION = "1";
    delete process.env.YAVER_STORE_IDENTITY_NAME;
    const configPath = require.resolve("../electron-builder.microsoft-store.cjs");
    delete require.cache[configPath];
    assert.throws(() => require(configPath), /YAVER_STORE_IDENTITY_NAME/);
  } finally {
    if (old === undefined) delete process.env.YAVER_STORE_PRODUCTION; else process.env.YAVER_STORE_PRODUCTION = old;
  }
});

test("GitHub builds and lane-tests the Store package without Convex or a private signing key", () => {
  const workflow = read(".github/workflows/microsoft-store-release.yml");
  assert.match(workflow, /runs-on: windows-2022/);
  assert.match(workflow, /build-microsoft-store\.ps1/);
  assert.match(workflow, /Browser, Hermes, and WebRTC contracts/);
  assert.match(workflow, /YAVER_STORE_IDENTITY_NAME/);
  assert.match(workflow, /YAVER_STORE_DISPLAY_NAME/);
  assert.match(workflow, /Microsoft-signs-and-hosts/);
  assert.doesNotMatch(workflow, /secrets\.(?:CONVEX|WIN_CSC_LINK|YAVER_WINDOWS_CERT|CLOUDFLARE|HCLOUD)/i);
  assert.match(workflow, /if: \$\{\{ github\.event_name == 'push' \}\}/);
  const submitter = read("scripts/microsoft-store-appx-submission.mjs");
  assert.match(submitter, /acceptedStatuses/);
  assert.match(submitter, /PreProcessing/);
  assert.match(submitter, /statusDetails/);
});

test("final package assertion requires the x64 native agent and rejects WSL payloads", () => {
  const assertion = read("electron/store/assert-microsoft-store-package.ps1");
  const builder = read("electron/scripts/build-microsoft-store.ps1");
  assert.match(assertion, /resources\\bin\\yaver\.exe/);
  assert.match(assertion, /Native x64 agent \/health passed without WSL or a scheduled task/);
  assert.match(assertion, /\.vhdx/);
  assert.match(assertion, /privateNetworkClientServer/);
  assert.match(assertion, /ExpectedArchitecture = "x64"/);
  assert.doesNotMatch(builder, /assert-microsoft-store-package[\s\S]*LASTEXITCODE/);
});
