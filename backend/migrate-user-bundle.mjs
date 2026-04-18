#!/usr/bin/env node
import { ConvexHttpClient } from "convex/browser";
import { api } from "./convex/_generated/api.js";

const email = process.argv[2] || "kivanc.cakmak@icloud.com";
const sourceUrl = process.env.SOURCE_CONVEX_URL || "https://shocking-echidna-394.eu-west-1.convex.cloud";
const targetUrl = process.env.TARGET_CONVEX_URL || "https://perceptive-minnow-557.eu-west-1.convex.cloud";

async function main() {
  const source = new ConvexHttpClient(sourceUrl);
  const target = new ConvexHttpClient(targetUrl);

  const users = await source.query(api.admin.getUsersByEmail, { email });
  const user = users?.[0];
  if (!user) {
    throw new Error(`No source user found for ${email}`);
  }
  const settings = await source.query(api.userSettings.get, { userId: user._id });
  const allDevices = await source.query(api.admin.listAllDevices, {});
  const bundle = {
    user,
    settings,
    devices: allDevices.filter((device) => device.userId === user._id),
  };

  const normalizedSettings = bundle.settings
    ? {
        forceRelay: bundle.settings.forceRelay,
        runnerId: bundle.settings.runnerId,
        customRunnerCommand: bundle.settings.customRunnerCommand,
        relayUrl: bundle.settings.relayUrl,
        relayPassword: bundle.settings.relayPassword,
        tunnelUrl: bundle.settings.tunnelUrl,
        speechProvider: bundle.settings.speechProvider,
        speechApiKey: bundle.settings.speechApiKey,
        ttsEnabled: bundle.settings.ttsEnabled,
        verbosity: bundle.settings.verbosity,
        keyStorage: bundle.settings.keyStorage,
      }
    : undefined;

  const devices = bundle.devices.map((device) => ({
    deviceId: device.deviceId,
    name: device.name,
    platform: device.platform,
    quicHost: device.quicHost,
    quicPort: device.quicPort,
    isOnline: device.isOnline,
    lastHeartbeat: device.lastHeartbeat,
    createdAt: device.createdAt,
    deviceClass: device.deviceClass,
    edgeProfile: device.edgeProfile,
    publicKey: device.publicKey,
    runnerDown: device.runnerDown,
    runners: device.runners,
    needsAuth: device.needsAuth,
    hardwareId: device.hardwareId,
  }));

  const result = await target.mutation(api.admin.importUserBundle, {
    user: {
      email: bundle.user.email,
      fullName: bundle.user.fullName,
      provider: bundle.user.provider,
      providerId: bundle.user.providerId,
      userId: bundle.user.userId,
      createdAt: bundle.user.createdAt,
      surveyCompleted: bundle.user.surveyCompleted,
      avatarUrl: bundle.user.avatarUrl,
      passwordHash: bundle.user.passwordHash,
      totpSecret: bundle.user.totpSecret,
      totpEnabled: bundle.user.totpEnabled,
      totpRecoveryCodes: bundle.user.totpRecoveryCodes,
    },
    settings: normalizedSettings,
    devices,
  });

  console.log(JSON.stringify({
    sourceUrl,
    targetUrl,
    email,
    sourceDevices: devices.length,
    result,
  }, null, 2));
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
