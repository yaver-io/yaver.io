// Platform-aware hermesc provisioner for yaver-cli.
//
// Runs from `postinstall` on global installs and on-demand from the
// bundler. Avoids shipping a ~6 MB binary inside the tarball that's
// only correct for one platform — instead we detect the host and
// install a matching hermesc at install time.
//
// Lookup order (redundant by design; any one succeeding is enough):
//   1. Cached install under node_modules/yaver-cli/hermesc/<key>/hermesc
//   2. Per-user cache under ~/.yaver/hermesc/<key>/hermesc
//   3. Download from the react-native npm tarball and extract just
//      sdks/hermesc/<rn-dir>/hermesc for this platform
//   4. Build from the project's node_modules/react-native hermes
//      sources (handles linux-arm64 where no prebuilt exists)
//   5. Fall through — bundler.js then resolves from project-local RN
//
// Must never block `npm install`. Every failure is logged + swallowed;
// the CLI still installs fine, the bundler just takes the runtime
// fallback path the next time someone actually tries to push a bundle.

const fs = require("fs");
const os = require("os");
const path = require("path");
const https = require("https");
const { spawnSync } = require("child_process");

const HERMES_RN_VERSION = process.env.YAVER_HERMES_RN_VERSION || "0.81.5";

// Maps node's platform-arch to the subdirectory react-native ships its
// prebuilt hermesc under in the npm tarball (sdks/hermesc/<dir>/hermesc).
// react-native 0.81.x does NOT ship a prebuilt for linux-arm64 —
// those machines build from source instead (see buildFromProject()).
const RN_PREBUILT_DIR = {
  "darwin-arm64": "osx-bin",
  "darwin-x64": "osx-bin",
  "linux-x64": "linux64-bin",
  "win32-x64": "win64-bin",
};

function platformKey() {
  return `${process.platform}-${process.arch}`;
}

function hermescBasename() {
  return process.platform === "win32" ? "hermesc.exe" : "hermesc";
}

function cliInstallDir() {
  return path.join(__dirname, "..", "hermesc", platformKey());
}

function cliInstallPath() {
  return path.join(cliInstallDir(), hermescBasename());
}

function userCacheDir() {
  return path.join(os.homedir(), ".yaver", "hermesc", platformKey());
}

function userCachePath() {
  return path.join(userCacheDir(), hermescBasename());
}

function hermescBinaryRunnable(binaryPath) {
  try {
    const res = spawnSync(binaryPath, ["--version"], {
      stdio: ["ignore", "pipe", "pipe"],
      timeout: 5000,
    });
    if (res.status !== 0) return false;
    const out = (res.stdout?.toString() || "") + (res.stderr?.toString() || "");
    return /hermes/i.test(out) || /bytecode/i.test(out);
  } catch (_err) {
    return false;
  }
}

function chmodExec(binaryPath) {
  if (process.platform === "win32") return;
  try {
    fs.chmodSync(binaryPath, 0o755);
  } catch (_err) {
    /* ignore */
  }
}

function log(message, { quiet }) {
  if (quiet) return;
  console.error(`[yaver hermesc] ${message}`);
}

async function downloadToFile(url, outPath, { maxRedirects = 5 } = {}) {
  return new Promise((resolve, reject) => {
    const fetch = (u, redirectsLeft) => {
      const req = https.get(u, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          if (redirectsLeft <= 0) {
            reject(new Error(`too many redirects for ${url}`));
            return;
          }
          res.resume();
          const next = new URL(res.headers.location, u).toString();
          fetch(next, redirectsLeft - 1);
          return;
        }
        if (res.statusCode !== 200) {
          reject(new Error(`HTTP ${res.statusCode} for ${u}`));
          res.resume();
          return;
        }
        const out = fs.createWriteStream(outPath);
        res.pipe(out);
        out.on("finish", () => out.close(() => resolve()));
        out.on("error", (err) => {
          try {
            fs.rmSync(outPath, { force: true });
          } catch (_err) {
            /* ignore */
          }
          reject(err);
        });
      });
      req.on("error", reject);
      req.setTimeout(60000, () => {
        req.destroy(new Error(`download timed out: ${u}`));
      });
    };
    fetch(url, maxRedirects);
  });
}

async function downloadFromRNTarball(destPath, { quiet }) {
  const rnDir = RN_PREBUILT_DIR[platformKey()];
  if (!rnDir) {
    throw new Error(`no react-native prebuilt for ${platformKey()}`);
  }

  // Redundant registries — npm.js primary, unpkg as fallback CDN.
  const candidates = [
    `https://registry.npmjs.org/react-native/-/react-native-${HERMES_RN_VERSION}.tgz`,
    `https://unpkg.com/react-native@${HERMES_RN_VERSION}/react-native-${HERMES_RN_VERSION}.tgz`,
  ];

  const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), "yaver-hermesc-"));
  const tarball = path.join(tmpRoot, "rn.tgz");

  let lastErr = null;
  for (const url of candidates) {
    try {
      log(`fetching ${url}`, { quiet });
      await downloadToFile(url, tarball);
      lastErr = null;
      break;
    } catch (err) {
      lastErr = err;
      log(`download failed: ${err.message}`, { quiet });
      try {
        fs.rmSync(tarball, { force: true });
      } catch (_) {
        /* ignore */
      }
    }
  }
  if (lastErr) {
    try {
      fs.rmSync(tmpRoot, { recursive: true, force: true });
    } catch (_) {
      /* ignore */
    }
    throw lastErr;
  }

  const rel = `package/sdks/hermesc/${rnDir}/${hermescBasename()}`;
  const res = spawnSync("tar", ["-xzf", tarball, "-C", tmpRoot, rel], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (res.status !== 0) {
    const stderr = res.stderr?.toString() || "unknown tar failure";
    try {
      fs.rmSync(tmpRoot, { recursive: true, force: true });
    } catch (_) {
      /* ignore */
    }
    throw new Error(`tar extract failed: ${stderr}`);
  }

  const extracted = path.join(tmpRoot, rel);
  if (!fs.existsSync(extracted)) {
    try {
      fs.rmSync(tmpRoot, { recursive: true, force: true });
    } catch (_) {
      /* ignore */
    }
    throw new Error(`extracted hermesc missing: ${extracted}`);
  }

  fs.mkdirSync(path.dirname(destPath), { recursive: true });
  fs.copyFileSync(extracted, destPath);
  chmodExec(destPath);

  try {
    fs.rmSync(tmpRoot, { recursive: true, force: true });
  } catch (_) {
    /* ignore */
  }
}

// Build hermesc from the project's own node_modules/react-native
// sources. Covers linux-arm64 and any other platform where we don't
// have a prebuilt. Mirrors desktop/agent/hermesc_resolver.go's
// buildProjectHermesc path.
function buildFromProject(destPath, { quiet }) {
  const projectRoots = [
    process.cwd(),
    path.join(process.cwd(), ".."),
  ];
  for (const root of projectRoots) {
    const hermesSrc = path.join(
      root,
      "node_modules",
      "react-native",
      "sdks",
      "hermes",
    );
    if (!fs.existsSync(path.join(hermesSrc, "CMakeLists.txt"))) continue;
    try {
      log(`building hermesc from ${hermesSrc} (one-time, ~3–5 min)…`, { quiet });
      const buildDir = fs.mkdtempSync(path.join(os.tmpdir(), "hermes-build-"));
      const cmake = spawnSync(
        "cmake",
        ["-S", hermesSrc, "-B", buildDir, "-DCMAKE_BUILD_TYPE=Release"],
        { stdio: quiet ? "ignore" : "inherit" },
      );
      if (cmake.status !== 0) throw new Error(`cmake configure failed`);
      const build = spawnSync(
        "cmake",
        ["--build", buildDir, "--target", "hermesc", "--config", "Release", "-j"],
        { stdio: quiet ? "ignore" : "inherit" },
      );
      if (build.status !== 0) throw new Error(`cmake build failed`);
      const built = path.join(buildDir, "bin", hermescBasename());
      if (!fs.existsSync(built)) throw new Error(`hermesc missing after build: ${built}`);
      fs.mkdirSync(path.dirname(destPath), { recursive: true });
      fs.copyFileSync(built, destPath);
      chmodExec(destPath);
      try {
        fs.rmSync(buildDir, { recursive: true, force: true });
      } catch (_) {
        /* ignore */
      }
      return true;
    } catch (err) {
      log(`build-from-source failed at ${hermesSrc}: ${err.message}`, { quiet });
    }
  }
  return false;
}

function findExistingHermesc() {
  const candidates = [cliInstallPath(), userCachePath()];
  for (const p of candidates) {
    if (fs.existsSync(p)) {
      chmodExec(p);
      if (hermescBinaryRunnable(p)) return p;
    }
  }
  return null;
}

async function ensureHermesc({ quiet = false, allowBuildFromSource = false } = {}) {
  const existing = findExistingHermesc();
  if (existing) return existing;

  const key = platformKey();

  if (RN_PREBUILT_DIR[key]) {
    // Prefer cli-local install so `yaver-push` works offline after the
    // first install (user cache also works; we write to both so either
    // path resolves).
    const dest = cliInstallPath();
    try {
      await downloadFromRNTarball(dest, { quiet });
      if (hermescBinaryRunnable(dest)) {
        // Also mirror into ~/.yaver so a later `npm uninstall` + reinstall
        // doesn't re-download. Redundancy is the point.
        try {
          fs.mkdirSync(userCacheDir(), { recursive: true });
          fs.copyFileSync(dest, userCachePath());
          chmodExec(userCachePath());
        } catch (_) {
          /* ignore mirror errors */
        }
        log(`installed at ${dest}`, { quiet });
        return dest;
      }
      log(`downloaded binary at ${dest} did not run — discarding`, { quiet });
      try {
        fs.rmSync(dest, { force: true });
      } catch (_) {
        /* ignore */
      }
    } catch (err) {
      log(`prebuilt download failed: ${err.message}`, { quiet });
    }
  } else {
    log(`no prebuilt available for ${key} — will try build-from-source on demand`, { quiet });
  }

  if (allowBuildFromSource) {
    const dest = cliInstallPath();
    if (buildFromProject(dest, { quiet }) && hermescBinaryRunnable(dest)) {
      log(`built hermesc from source at ${dest}`, { quiet });
      return dest;
    }
  }

  // Fall through — caller should rely on bundler.js's project-local RN
  // fallback (node_modules/react-native/sdks/hermesc/...). Return null
  // rather than throwing so postinstall never fails npm install.
  return null;
}

module.exports = {
  ensureHermesc,
  findExistingHermesc,
  cliInstallPath,
  userCachePath,
  platformKey,
  hermescBasename,
};
