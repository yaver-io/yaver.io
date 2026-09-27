#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..");
const versions = JSON.parse(readFileSync(join(root, "versions.json"), "utf8"));
const version = String(versions.gui || "").trim();

if (!/^\d+\.\d+\.\d+$/.test(version)) {
  throw new Error(`versions.json gui is not a release version: ${version || "<empty>"}`);
}

const filename = `yaver-gui-${version}-win-x64-setup.exe`;
const packageUrl = `https://download.yaver.io/windows/${version}/${filename}`;

const metadata = {
  productName: "Yaver",
  productType: "EXE",
  packageUrl,
  filename,
  architecture: "x64",
  languages: ["en-us"],
  installerParameters: "/S",
  installerReturnCodeDocumentationUrl: "https://yaver.io/support/windows-installer-exit-codes",
  installerReturnCodes: {
    success: 0,
    cancelledByUser: 1,
    miscellaneousFailure: 2,
  },
  installerMode: "offline",
  scope: "per-user",
  requiresElevation: false,
  privacyPolicyUrl: "https://yaver.io/privacy",
  supportUrl: "https://yaver.io/terms#contact",
  notesForCertificationFile: "electron/store/microsoft-store-certification-notes.md",
};

if (process.argv.includes("--verify-url")) {
  const response = await fetch(packageUrl, { method: "HEAD", redirect: "follow" });
  if (!response.ok) {
    throw new Error(`versioned Store package URL is not live (${response.status}): ${packageUrl}`);
  }
  metadata.packageUrlVerified = true;
  metadata.contentLength = Number(response.headers.get("content-length") || 0) || undefined;
}

process.stdout.write(`${JSON.stringify(metadata, null, 2)}\n`);
