const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');
const { pipeline } = require('stream/promises');
const https = require('https');

const PACKAGE = require('../package.json');

const DEFAULT_REPO = process.env.YAVER_AGENT_REPO || 'kivanccakmak/yaver.io';
const AGENT_VERSION = process.env.YAVER_AGENT_VERSION || PACKAGE.version;
const CACHE_ROOT = process.env.YAVER_AGENT_CACHE_DIR || path.join(os.homedir(), '.yaver', 'bin');

async function ensureAgentBinary({ quiet = false } = {}) {
  const asset = resolveAsset();
  const localAgentPath = resolveLocalAgentBinary(asset);
  if (localAgentPath) {
    return localAgentPath;
  }
  const installDir = path.join(CACHE_ROOT, AGENT_VERSION, asset.cacheKey);
  const binaryPath = path.join(installDir, asset.binaryName);

  if (fs.existsSync(binaryPath)) {
    if (process.platform !== 'win32') {
      fs.chmodSync(binaryPath, 0o755);
    }
    return binaryPath;
  }

  fs.mkdirSync(installDir, { recursive: true });
  const tmpPath = path.join(installDir, asset.downloadName);

  if (!quiet) {
    console.error(`Installing Yaver agent ${AGENT_VERSION} for ${asset.cacheKey}...`);
  }

  await downloadToFile(asset.url, tmpPath);

  if (asset.archiveType === 'tar.gz') {
    await extractTarball(tmpPath, installDir);
    fs.rmSync(tmpPath, { force: true });
  } else {
    fs.renameSync(tmpPath, binaryPath);
  }

  if (!fs.existsSync(binaryPath)) {
    throw new Error(`agent binary missing after install: ${binaryPath}`);
  }
  if (process.platform !== 'win32') {
    fs.chmodSync(binaryPath, 0o755);
  }
  return binaryPath;
}

async function runAgentCommand(args, options = {}) {
  const localAgent = resolveLocalAgentCommand(args);
  const spawnSpec = localAgent
    ? localAgent
    : { command: await ensureAgentBinary(options), args };
  const child = spawn(spawnSpec.command, spawnSpec.args, {
    stdio: 'inherit',
    env: process.env,
    cwd: spawnSpec.cwd || process.cwd(),
  });

  await new Promise((resolve, reject) => {
    child.on('error', reject);
    child.on('exit', (code, signal) => {
      if (signal) {
        reject(new Error(`agent terminated by signal ${signal}`));
        return;
      }
      if (code && code !== 0) {
        const error = new Error(`agent exited with code ${code}`);
        error.exitCode = code;
        reject(error);
        return;
      }
      resolve();
    });
  });
}

function resolveAgentInfo() {
  const asset = resolveAsset();
  return {
    version: AGENT_VERSION,
    repo: DEFAULT_REPO,
    asset: asset.downloadName,
    binaryName: asset.binaryName,
    cacheDir: path.join(CACHE_ROOT, AGENT_VERSION, asset.cacheKey),
    downloadUrl: asset.url,
  };
}

function resolveLocalAgentBinary(asset) {
  if (process.env.YAVER_AGENT_BIN && fs.existsSync(process.env.YAVER_AGENT_BIN)) {
    return process.env.YAVER_AGENT_BIN;
  }
  const repoBinary = path.join(repoRoot(), 'desktop', 'agent', asset.downloadName.replace('.tar.gz', '').replace('.exe', '.exe'));
  if (fs.existsSync(repoBinary)) {
    return repoBinary;
  }
  return null;
}

function resolveLocalAgentCommand(agentArgs) {
  if (process.env.YAVER_AGENT_BIN && fs.existsSync(process.env.YAVER_AGENT_BIN)) {
    return { command: process.env.YAVER_AGENT_BIN, args: agentArgs };
  }

  const asset = resolveAsset();
  const localBinary = resolveLocalAgentBinary(asset);
  if (localBinary) {
    return { command: localBinary, args: agentArgs };
  }

  const agentDir = path.join(repoRoot(), 'desktop', 'agent');
  const goMod = path.join(agentDir, 'go.mod');
  if (fs.existsSync(goMod)) {
    return {
      command: 'go',
      args: ['run', '.', ...agentArgs],
      cwd: agentDir,
    };
  }

  return null;
}

function resolveAsset() {
  const platform = process.platform;
  const arch = process.arch;
  const goArch = arch === 'x64' ? 'amd64' : arch === 'arm64' ? 'arm64' : null;

  if (!goArch) {
    throw new Error(`unsupported architecture for npm bootstrap: ${arch}`);
  }

  if (platform === 'win32') {
    return {
      cacheKey: `${platform}-${goArch}`,
      binaryName: 'yaver.exe',
      downloadName: `yaver-windows-${goArch}.exe`,
      archiveType: 'exe',
      url: releaseUrl(`yaver-windows-${goArch}.exe`),
    };
  }

  if (platform === 'darwin' || platform === 'linux') {
    return {
      cacheKey: `${platform}-${goArch}`,
      binaryName: 'yaver',
      downloadName: `yaver-${platform}-${goArch}.tar.gz`,
      archiveType: 'tar.gz',
      url: releaseUrl(`yaver-${platform}-${goArch}.tar.gz`),
    };
  }

  throw new Error(`unsupported platform for npm bootstrap: ${platform}`);
}

function releaseUrl(assetName) {
  return `https://github.com/${DEFAULT_REPO}/releases/download/v${AGENT_VERSION}/${assetName}`;
}

async function downloadToFile(url, destPath) {
  const response = await request(url);
  if (response.statusCode && response.statusCode >= 400) {
    throw new Error(`download failed (${response.statusCode}) from ${url}. Publish the matching CLI release assets or set YAVER_AGENT_BIN.`);
  }
  await pipeline(response, fs.createWriteStream(destPath));
}

function request(url) {
  return new Promise((resolve, reject) => {
    https.get(url, (response) => {
      if (response.statusCode && response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        resolve(request(response.headers.location));
        return;
      }
      resolve(response);
    }).on('error', reject);
  });
}

async function extractTarball(archivePath, destDir) {
  const child = spawn('tar', ['-xzf', archivePath, '-C', destDir], {
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  let stderr = '';
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  await new Promise((resolve, reject) => {
    child.on('error', reject);
    child.on('exit', (code) => {
      if (code !== 0) {
        reject(new Error(stderr.trim() || `tar exited with code ${code}`));
        return;
      }
      resolve();
    });
  });
}

module.exports = {
  ensureAgentBinary,
  resolveAgentInfo,
  runAgentCommand,
};

function repoRoot() {
  return path.resolve(__dirname, '..', '..');
}
