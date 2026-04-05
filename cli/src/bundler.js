const { execSync, execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const os = require('os');

function getHermescPath() {
  const key = `${os.platform()}-${os.arch()}`;
  const dir = path.join(__dirname, '..', 'hermesc');
  const ext = os.platform() === 'win32' ? 'hermesc.exe' : 'hermesc';

  const candidates = [
    path.join(dir, key, ext),
    path.join(dir, 'darwin-arm64', ext),
    path.join(dir, 'linux-x64', ext),
  ];

  const found = candidates.find(p => fs.existsSync(p));
  if (!found) {
    // Fall back to project's hermesc (react-native ships one)
    const rnHermesc = findRNHermesc();
    if (rnHermesc) return rnHermesc;

    throw new Error(
      `hermesc not found for ${key}. Install yaver-cli hermesc binaries or ensure react-native is installed.`
    );
  }

  try { fs.chmodSync(found, 0o755); } catch {}
  return found;
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
  const hermesc = getHermescPath();
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

module.exports = { bundle, compileHermes, readBytecodeVersion, getHermescPath };
