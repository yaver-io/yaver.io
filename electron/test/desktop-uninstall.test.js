"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const { uninstallPlan, executeUninstallPlan, WINDOWS_UNINSTALLER_NAMES } = require("../src/desktop-uninstall");

test("macOS direct build trashes the app bundle and never touches ~/.yaver", () => {
  const plan = uninstallPlan({
    platform: "darwin",
    execPath: "/Applications/Yaver.app/Contents/MacOS/Yaver",
    exists: () => true,
  });
  assert.equal(plan.supported, true);
  assert.equal(plan.action, "trash");
  assert.equal(plan.target, "/Applications/Yaver.app");
  assert.match(plan.detail, /Trash/);
  assert.match(plan.detail, /~\/\.yaver/);
});

test("macOS cannot identify a non-bundle executable and says so", () => {
  const plan = uninstallPlan({ platform: "darwin", execPath: "/tmp/Yaver", exists: () => true });
  assert.equal(plan.supported, false);
  assert.equal(plan.action, "manual");
});

test("Windows direct build runs the adjacent NSIS uninstaller", () => {
  const target = "C:\\Users\\dev\\AppData\\Local\\Programs\\Yaver\\" + WINDOWS_UNINSTALLER_NAMES[0];
  const plan = uninstallPlan({
    platform: "win32",
    execPath: "C:\\Users\\dev\\AppData\\Local\\Programs\\Yaver\\Yaver.exe",
    exists: (p) => p === target,
  });
  assert.equal(plan.supported, true);
  assert.equal(plan.action, "run-uninstaller");
  assert.equal(plan.target, target);
});

test("Windows build without an uninstaller routes to Installed apps, not a fake delete", () => {
  const plan = uninstallPlan({ platform: "win32", execPath: "C:\\Yaver\\Yaver.exe", exists: () => false });
  assert.equal(plan.supported, false);
  assert.equal(plan.action, "package-manager");
  assert.match(plan.detail, /Installed apps/);
});

test("Linux AppImage trashes the single file; deb/rpm routes to the package manager", () => {
  const appimage = uninstallPlan({
    platform: "linux",
    execPath: "/tmp/.mount_Yaver/yaver-gui",
    appImage: "/home/dev/Apps/Yaver.AppImage",
  });
  assert.equal(appimage.action, "trash");
  assert.equal(appimage.target, "/home/dev/Apps/Yaver.AppImage");
  const deb = uninstallPlan({ platform: "linux", execPath: "/usr/bin/yaver-gui", appImage: "" });
  assert.equal(deb.supported, false);
  assert.equal(deb.action, "package-manager");
});

test("Store-managed builds are named, never uninstalled from inside the app", () => {
  const plan = uninstallPlan({ platform: "win32", execPath: "C:\\Yaver\\Yaver.exe", storeManaged: true, storeName: "Microsoft Store" });
  assert.equal(plan.supported, false);
  assert.equal(plan.action, "store");
  assert.match(plan.detail, /Microsoft Store/);
});

test("executeUninstallPlan performs the plan and reports trash failures honestly", async () => {
  const trashed = [];
  const okPlan = uninstallPlan({ platform: "darwin", execPath: "/Applications/Yaver.app/Contents/MacOS/Yaver", exists: () => true });
  const ok = await executeUninstallPlan(okPlan, { trash: async (t) => { trashed.push(t); } });
  assert.deepEqual(ok, { ok: true });
  assert.deepEqual(trashed, ["/Applications/Yaver.app"]);

  const failed = await executeUninstallPlan(okPlan, { trash: async () => { throw new Error("permission denied"); } });
  assert.equal(failed.ok, false);
  assert.match(failed.error, /permission denied/);

  const unsupported = await executeUninstallPlan({ supported: false, detail: "nope" });
  assert.equal(unsupported.ok, false);
  assert.equal(unsupported.error, "nope");
});
