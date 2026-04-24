const { execSync, execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { findExistingHermesc, ensureHermesc } = require('./hermesc-runtime');

function getHermescPath() {
  const key = `${os.platform()}-${os.arch()}`;
  const dir = path.join(__dirname, '..', 'hermesc');
  const ext = os.platform() === 'win32' ? 'hermesc.exe' : 'hermesc';

  // 1. Cache populated by postinstall (or previous on-demand install).
  //    Covers the common case without any network access at push time.
  const cached = findExistingHermesc();
  if (cached) return cached;

  // 2. Legacy/redundant lookup: packages built before the platform-aware
  //    install may have raw hermesc subdirs left over, and third-party
  //    forks sometimes drop their own binaries into hermesc/ directly.
  const candidates = [
    path.join(dir, key, ext),
    path.join(dir, 'darwin-arm64', ext),
    path.join(dir, 'linux-x64', ext),
    path.join(dir, ext),
  ];
  const found = candidates.find(p => fs.existsSync(p));
  if (found) {
    try { fs.chmodSync(found, 0o755); } catch {}
    return found;
  }

  // 3. Project's own react-native installation (ships hermesc at
  //    RN 0.81+). Covers `--ignore-scripts` installs and linux-arm64
  //    where we don't have a prebuilt binary.
  const rnHermesc = findRNHermesc();
  if (rnHermesc) return rnHermesc;

  throw new Error(
    `hermesc not found for ${key}. Run \`npm rebuild yaver-cli\` to re-trigger the platform-aware installer, or ensure react-native is installed locally.`,
  );
}

// Async variant that will provision hermesc on demand if the sync path
// can't find one. Callers that can `await` (push command, etc.) should
// prefer this so the first push works even when postinstall was skipped.
async function getHermescPathAsync({ quiet = true, allowBuildFromSource = false } = {}) {
  try {
    return getHermescPath();
  } catch (_err) {
    const provisioned = await ensureHermesc({ quiet, allowBuildFromSource });
    if (provisioned) return provisioned;
    const rnHermesc = findRNHermesc();
    if (rnHermesc) return rnHermesc;
    throw _err;
  }
}

/** Find hermesc from the project's react-native installation */
function findRNHermesc() {
  const candidates = [
    // RN 0.81+
    path.join('node_modules', 'react-native', 'sdks', 'hermesc', getPlatformBin(), 'hermesc'),
    // Older RN
    path.join('node_modules', 'hermes-engine', getPlatformBin(), 'hermesc'),
  ];

  for (const c of candidates) {
    if (fs.existsSync(c)) {
      try { fs.chmodSync(c, 0o755); } catch {}
      return c;
    }
  }
  return null;
}

function getPlatformBin() {
  const p = os.platform();
  const a = os.arch();
  if (p === 'darwin') return a === 'arm64' ? 'osx-bin' : 'osx-bin';
  if (p === 'linux') return 'linux64-bin';
  if (p === 'win32') return 'win64-bin';
  return 'osx-bin';
}

async function bundle({ platform, entryFile, outputDir, dev = false, minify = true }) {
  fs.rmSync(outputDir, { recursive: true, force: true });
  fs.mkdirSync(outputDir, { recursive: true });

  const bundlePath = path.join(outputDir, 'main.jsbundle');
  const assetsDir = path.join(outputDir, 'assets');

  const cmd = [
    'npx react-native bundle',
    `--platform ${platform}`,
    `--entry-file ${entryFile}`,
    `--bundle-output ${bundlePath}`,
    `--assets-dest ${assetsDir}`,
    `--dev ${dev}`,
    `--minify ${minify}`,
    '--reset-cache',
  ].join(' ');

  execSync(cmd, { stdio: 'inherit' });

  if (!fs.existsSync(bundlePath)) {
    throw new Error(`Bundle not found at ${bundlePath}. Check react-native bundle output.`);
  }

  return bundlePath;
}

async function compileHermes({ inputPath, outputPath }) {
  // Prefer the async resolver: if the cache is empty (install did
  // --ignore-scripts, or this is linux-arm64 and we need a build-
  // from-source) it'll provision one on demand instead of throwing.
  const hermesc = await getHermescPathAsync({ quiet: false, allowBuildFromSource: true });
  const tmp = inputPath + '.tmp';
  fs.renameSync(inputPath, tmp);

  try {
    execFileSync(hermesc, ['-emit-binary', '-out', outputPath, '-O', tmp], { stdio: 'pipe' });
  } catch (err) {
    // Restore original on failure
    fs.renameSync(tmp, inputPath);
    const stderr = err.stderr?.toString() || err.message;
    throw new Error(`Hermes compile failed: ${stderr}`);
  }

  fs.unlinkSync(tmp);
  return outputPath;
}

function readBytecodeVersion(hbcPath) {
  const buf = fs.readFileSync(hbcPath);
  if (buf.length < 12) return null;

  // Hermes HBC format: magic at offset 4, BC version at offset 8
  const magic = buf.readUInt32LE(4);
  if (magic !== 0x1F1903C1) return null;

  return buf.readUInt32LE(8);
}

module.exports = {
  bundle,
  compileHermes,
  readBytecodeVersion,
  getHermescPath,
  getHermescPathAsync,
};
