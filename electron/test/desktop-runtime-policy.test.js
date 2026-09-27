"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const {
  DEFAULT_SETTINGS,
  normalizeSettings,
  isLoginItemSupported,
  linuxAutostartEntry,
  needsMasJitlessWorkaround,
} = require("../src/desktop-runtime-policy");

test("remote-node availability defaults require explicit user opt-in", () => {
  assert.deepEqual(normalizeSettings(null), DEFAULT_SETTINGS);
  assert.deepEqual(normalizeSettings({}), DEFAULT_SETTINGS);
  assert.deepEqual(normalizeSettings({ keepAwake: true, launchAtLogin: true }), {
    taskNotifications: true,
    keepAwake: true,
    launchAtLogin: true,
    automaticUpdates: true,
  });
  assert.equal(DEFAULT_SETTINGS.keepAwake, false);
  assert.equal(DEFAULT_SETTINGS.launchAtLogin, false);
});

test("invalid persisted values cannot silently enable availability", () => {
  assert.deepEqual(normalizeSettings({ keepAwake: "true", launchAtLogin: 1 }), DEFAULT_SETTINGS);
});

test("automatic updates default on but respect an explicit user opt-out", () => {
  assert.equal(normalizeSettings(null).automaticUpdates, true);
  assert.equal(normalizeSettings({ automaticUpdates: false }).automaticUpdates, false);
});

test("login-item integration covers native APIs plus XDG autostart", () => {
  assert.equal(isLoginItemSupported("win32"), true);
  assert.equal(isLoginItemSupported("darwin"), true);
  assert.equal(isLoginItemSupported("linux"), true);
});

test("Linux autostart keeps AppImage/deb launches hidden and shell-safe", () => {
  const entry = linuxAutostartEntry('/home/dev/My Apps/Yaver.AppImage');
  assert.match(entry, /Exec="\/home\/dev\/My Apps\/Yaver\.AppImage" --hidden/);
  assert.match(entry, /X-GNOME-Autostart-enabled=true/);
});

test("MAS renderer disables JIT only on affected Apple-silicon macOS releases", () => {
  assert.equal(needsMasJitlessWorkaround({
    isMas: true, platform: "darwin", arch: "arm64", release: "25.6.0",
  }), true);
  assert.equal(needsMasJitlessWorkaround({
    isMas: true, platform: "darwin", arch: "arm64", release: "24.6.0",
  }), false);
  assert.equal(needsMasJitlessWorkaround({
    isMas: false, platform: "darwin", arch: "arm64", release: "25.6.0",
  }), false);
  assert.equal(needsMasJitlessWorkaround({
    isMas: true, platform: "darwin", arch: "x64", release: "25.6.0",
  }), false);
  assert.equal(needsMasJitlessWorkaround({
    isMas: true, platform: "win32", arch: "arm64", release: "25.6.0",
  }), false);
  assert.equal(needsMasJitlessWorkaround({
    isMas: true, platform: "darwin", arch: "arm64", release: "unknown",
  }), false);
});
