"use strict";

// Windows has three supported signing topologies:
//   1. WIN_CSC_LINK + WIN_CSC_KEY_PASSWORD (exportable CI PFX), or
//   2. WIN_CERTIFICATE_SHA1 on the dedicated SimplySign Windows runner.
//   3. YAVER_WINDOWS_CERT_ALIAS + YAVER_WINDOWS_CERT_SHA1 through the active
//      SimplySign PKCS#11 session on the release Mac.
//
// The second form selects the cloud certificate exposed in CurrentUser\My by
// SimplySign Desktop. electron-builder signs the application, helpers,
// uninstaller and outer NSIS installer through the same certificate-store
// identity. The release workflow verifies every produced PE afterwards.

const base = require("./package.json").build;
const certificateSha1 = String(process.env.WIN_CERTIFICATE_SHA1 || "").replace(/\s/g, "");
const hasPfx = Boolean(process.env.CSC_LINK && process.env.CSC_KEY_PASSWORD);
const hasMacSimplySign = Boolean(process.env.YAVER_WINDOWS_CERT_ALIAS && process.env.YAVER_WINDOWS_CERT_SHA1);

if (!hasPfx && !certificateSha1 && !hasMacSimplySign) {
  throw new Error(
    "Windows packaging requires a PFX, a Windows SimplySign certificate, or the pinned macOS SimplySign PKCS#11 identity.",
  );
}

module.exports = {
  ...base,
  // The shared desktop config carries both `yaver` and `yaver.exe` so native
  // macOS/Linux builds can select their host agent. A macOS cross-build must
  // never copy the host Mach-O binary into the Windows installer. Keep the
  // Store payload Windows-only and carry the repository license with every
  // redistributed binary.
  extraResources: [
    { from: "resources/bin/yaver.exe", to: "bin/yaver.exe" },
    { from: "../LICENSE", to: "LICENSE.txt" },
  ],
  win: {
    ...base.win,
    // Microsoft Store's EXE lane requires every installed PE, not only the
    // outer installer, to carry a trusted signature. Electron native modules
    // use .node even though they are PE DLLs internally.
    signExts: [".exe", ".dll", ".node"],
    ...(certificateSha1 ? { certificateSha1 } : {}),
    ...(hasMacSimplySign ? {
      signtoolOptions: {
        signingHashAlgorithms: ["sha256"],
        sign: "./sign-windows-hook.js",
      },
    } : {}),
  },
};
