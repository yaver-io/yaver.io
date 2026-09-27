"use strict";

// Microsoft Store package configuration. This is a full-trust packaged desktop
// app containing the native x64 Yaver agent. It supports client, agent, and
// combined use without bundling WSL. Partner Center signs and hosts the
// accepted package, so these identity values are public metadata, not keys.
const base = require("./package.json").build;
const production = process.env.YAVER_STORE_PRODUCTION === "1";

function identity(name, validationDefault) {
  const value = String(process.env[name] || "").trim();
  if (production && (!value || /PENDING|VALIDATION/i.test(value))) {
    throw new Error(`${name} must contain the exact case-sensitive Partner Center value.`);
  }
  return value || validationDefault;
}

const { target: _directTarget, artifactName: _directArtifactName, ...storeWin } = base.win;

module.exports = {
  ...base,
  directories: {
    ...base.directories,
    output: "dist-microsoft-store",
  },
  extraResources: [
    { from: "resources/bin/yaver.exe", to: "bin/yaver.exe" },
    { from: "../LICENSE", to: "LICENSE.txt" },
    { from: "store/microsoft-store-channel.json", to: "yaver-store-channel.json" },
  ],
  extraMetadata: {
    yaverDistributionChannel: "microsoft-store-full-node",
  },
  win: {
    ...storeWin,
    target: ["appx"],
    artifactName: "Yaver-${version}-${arch}-store.${ext}",
  },
  appx: {
    identityName: identity("YAVER_STORE_IDENTITY_NAME", "Yaver.Validation"),
    publisher: identity("YAVER_STORE_PUBLISHER", "CN=Yaver Validation"),
    publisherDisplayName: identity("YAVER_STORE_PUBLISHER_DISPLAY_NAME", "SIMKAB"),
    applicationId: "Yaver",
    displayName: identity("YAVER_STORE_DISPLAY_NAME", "Yaver"),
    languages: ["en-US"],
    minVersion: "10.0.17763.0",
    maxVersionTested: "10.0.26100.0",
    capabilities: ["internetClient", "privateNetworkClientServer", "runFullTrust"],
  },
};
