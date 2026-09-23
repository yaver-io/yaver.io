"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");

const electronRoot = join(__dirname, "..");

test("MAS config is sandboxed client-only and excludes the embedded agent", () => {
  const oldBuild = process.env.YAVER_MAC_BUILD_NUMBER;
  const oldTarget = process.env.YAVER_MAS_TARGET;
  try {
    process.env.YAVER_MAC_BUILD_NUMBER = "202608160001";
    delete process.env.YAVER_MAS_TARGET;
    const configPath = require.resolve("../electron-builder.mas.cjs");
    delete require.cache[configPath];
    const config = require(configPath);
    assert.equal(config.appId, "io.yaver.mobile");
    assert.equal(config.buildVersion, "202608160001");
    assert.deepEqual(config.mac.target, ["mas"]);
    assert.equal(config.mac.notarize, false);
    assert.equal(config.extraResources, undefined);
    assert.match(config.mas.entitlements, /entitlements\.mas\.plist$/);
  } finally {
    if (oldBuild === undefined) delete process.env.YAVER_MAC_BUILD_NUMBER;
    else process.env.YAVER_MAC_BUILD_NUMBER = oldBuild;
    if (oldTarget === undefined) delete process.env.YAVER_MAS_TARGET;
    else process.env.YAVER_MAS_TARGET = oldTarget;
  }
});

test("MAS entitlements are least-privilege network client permissions", () => {
  const main = readFileSync(join(electronRoot, "assets", "entitlements.mas.plist"), "utf8");
  const child = readFileSync(join(electronRoot, "assets", "entitlements.mas.inherit.plist"), "utf8");
  assert.match(main, /com\.apple\.security\.app-sandbox/);
  assert.match(main, /com\.apple\.security\.network\.client/);
  assert.match(main, /com\.apple\.security\.cs\.allow-jit/);
  assert.doesNotMatch(main, /network\.server|automation\.apple-events|device\.camera|device\.microphone/);
  assert.match(child, /com\.apple\.security\.inherit/);
});

test("MAS deploy preserves the existing Yaver app identity and verifies the packaged build", () => {
  const deploy = readFileSync(join(electronRoot, "..", "scripts", "deploy-macos-testflight.sh"), "utf8");
  assert.match(deploy, /MAS_BUNDLE_ID="io\.yaver\.mobile"/);
  assert.match(deploy, /manageAppVersionAndBuildNumber -bool NO/);
  assert.match(deploy, /ApplicationProperties\.CFBundleShortVersionString/);
  assert.match(deploy, /ApplicationProperties\.CFBundleVersion/);
  assert.match(deploy, /PACKAGED_BUNDLE_ID/);
  assert.match(deploy, /PACKAGED_BUILD/);
  assert.match(deploy, /codesign --verify --deep --strict/);
  assert.match(deploy, /find "\$DIST_MAS_DIR" -depth -delete/);
  assert.match(deploy, /retrying the same verified archive once/);
  assert.match(deploy, /chmod 0644 "\$PROFILE_BUILD_COPY"/);
  assert.match(deploy, /VERIFY FAILED\|Validation failed\|Failed to validate package/);
  assert.match(deploy, /upload was not attempted/);
});
