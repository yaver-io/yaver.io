"use strict";

// Fail-closed Electron Windows signer. On macOS this uses the Certum
// SimplySign PKCS#11 provider; on Windows it uses the certificate exposed by
// SimplySign Desktop in the user's certificate store. Only public certificate
// identifiers enter the process environment.
const { execFileSync } = require("node:child_process");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const JSIGN_VERSION = "7.5";
const JSIGN_SHA256 = "602a51c3545a6dc4fb99bd2ea7152b26d1345916d0c93ddfbd5936cb735af91c";
const CERTUM_PLUGIN_DIR = "/Applications/proCertumSmartSign.app/Contents/PlugIns";
const DEFAULT_PKCS11 = "/usr/local/lib/libSimplySignPKCS.dylib";
const TIMESTAMP_URL = process.env.WINDOWS_TIMESTAMP_URL || "http://time.certum.pl";

function certificateThumbprint() {
  const value = String(process.env.YAVER_WINDOWS_CERT_SHA1 || process.env.WIN_CERTIFICATE_SHA1 || "")
    .replace(/[\s:]/g, "")
    .toUpperCase();
  if (!/^[A-F0-9]{40}$/.test(value)) {
    throw new Error("[sign] Set YAVER_WINDOWS_CERT_SHA1 to the pinned 40-hex certificate thumbprint");
  }
  return value;
}

function certificateAlias() {
  const value = String(process.env.YAVER_WINDOWS_CERT_ALIAS || "").trim().toUpperCase();
  if (!/^[A-F0-9]{32}$/.test(value)) {
    throw new Error("[sign] Set YAVER_WINDOWS_CERT_ALIAS to the 32-hex SimplySign certificate alias");
  }
  return value;
}

function sha256(file) {
  return crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
}

function jsignJar() {
  const file = process.env.YAVER_JSIGN_JAR || path.join(os.tmpdir(), `yaver-jsign-${JSIGN_VERSION}.jar`);
  if (!fs.existsSync(file) || sha256(file) !== JSIGN_SHA256) {
    throw new Error(`[sign] Jsign is missing or untrusted at ${file}; run scripts/prepare-jsign.sh`);
  }
  return file;
}

function signingJava() {
  const override = String(process.env.YAVER_SIGNING_JAVA_HOME || "").trim();
  if (override) return path.join(override, "bin", "java");
  try {
    const homes = fs.readdirSync(CERTUM_PLUGIN_DIR, { withFileTypes: true })
      .filter((entry) => entry.isDirectory() && /^jdk-.+[.]jdk$/.test(entry.name))
      .map((entry) => path.join(CERTUM_PLUGIN_DIR, entry.name, "Contents", "Home"))
      .filter((home) => fs.existsSync(path.join(home, "bin", "java")))
      .sort((a, b) => b.localeCompare(a, undefined, { numeric: true }));
    if (homes[0]) return path.join(homes[0], "bin", "java");
  } catch {
    // Named by the caller below; no version-specific fallback can be honest.
  }
  return path.join(CERTUM_PLUGIN_DIR, "missing", "bin", "java");
}

function signOnMac(file) {
  const java = signingJava();
  const provider = process.env.YAVER_SIMPLYSIGN_PKCS11_LIB || DEFAULT_PKCS11;
  if (!fs.existsSync(java)) throw new Error(`[sign] Signing Java was not found at ${java}`);
  if (!fs.existsSync(provider)) throw new Error(`[sign] SimplySign PKCS#11 provider was not found at ${provider}`);

  const temp = fs.mkdtempSync(path.join(os.tmpdir(), "yaver-pkcs11-"));
  const config = path.join(temp, "provider.cfg");
  try {
    fs.writeFileSync(config, `name=SimplySign\nlibrary=${provider}\n`, { mode: 0o600, flag: "wx" });
    const args = [
      "-jar", jsignJar(),
      "--storetype", "PKCS11",
      "--storepass", "",
      "--keystore", config,
      "--alias", certificateAlias(),
      "--tsaurl", TIMESTAMP_URL,
      "--alg", "SHA-256",
      file,
    ];
    let lastError;
    for (let attempt = 1; attempt <= 3; attempt += 1) {
      try {
        execFileSync(java, args, { stdio: ["ignore", "pipe", "pipe"] });
        lastError = undefined;
        break;
      } catch (error) {
        lastError = error;
        const diagnostic = `${error?.stdout || ""}\n${error?.stderr || ""}`;
        if (!diagnostic.includes("CKR_FUNCTION_FAILED") || attempt === 3) break;
        Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, attempt * 1500);
      }
    }
    if (lastError) throw new Error(`[sign] SimplySign provider failed for ${path.basename(file)}`);
  } finally {
    fs.rmSync(temp, { recursive: true, force: true });
  }

  execFileSync("osslsigncode", [
    "verify", "-require-leaf-hash", `sha1:${certificateThumbprint()}`, "-in", file,
  ], { stdio: "pipe" });
}

function signOnWindows(file) {
  const signTool = process.env.YAVER_SIGNTOOL_PATH || "signtool.exe";
  const thumbprint = certificateThumbprint();
  execFileSync(signTool, [
    "sign", "/sha1", thumbprint, "/fd", "SHA256", "/tr", TIMESTAMP_URL, "/td", "SHA256", file,
  ], { stdio: "inherit" });
  execFileSync(signTool, ["verify", "/pa", "/all", "/tw", file], { stdio: "inherit" });
}

exports.default = async function signWindows(configuration) {
  const file = configuration.path;
  if (!file || !/[.](exe|dll|node)$/i.test(file)) return;
  console.log(`[sign] Signing ${path.basename(file)}`);
  if (process.platform === "darwin") signOnMac(file);
  else if (process.platform === "win32") signOnWindows(file);
  else throw new Error(`[sign] Unsupported signing host: ${process.platform}`);
  console.log(`[sign] Signed and certificate-pinned ${path.basename(file)}`);
};

exports.signingJava = signingJava;
