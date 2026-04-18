const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');
const { pipeline } = require('stream/promises');
const https = require('https');
const semver = require('semver');

const PACKAGE = require('../package.json');

const DEFAULT_REPO = process.env.YAVER_AGENT_REPO || 'kivanccakmak/yaver.io';
const WINDOWS_REPO = process.env.YAVER_WINDOWS_AGENT_REPO || 'kivanccakmak/yaver-cli';
const CACHE_ROOT = process.env.YAVER_AGENT_CACHE_DIR || path.join(os.homedir(), '.yaver', 'bin');
let resolvedAgentVersionPromise = null;
const resolvedAssetPromiseByKey = new Map();

async function ensureAgentBinary({ quiet = false } = {}) {
  const asset = await resolveAsset();
  const localAgentPath = resolveLocalAgentBinary(asset);
  if (localAgentPath) {
    return localAgentPath;
  }
  const installDir = path.join(CACHE_ROOT, asset.version, asset.cacheKey);
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
    console.error(`Installing Yaver agent ${asset.version} for ${asset.cacheKey}...`);
  }

  await downloadToFile(asset.url, tmpPath);

  if (asset.archiveType === 'tar.gz') {
    await extractTarball(tmpPath, installDir);
    fs.rmSync(tmpPath, { force: true });
    if (!fs.existsSync(binaryPath) && asset.extractedBinaryName) {
      const extractedPath = path.join(installDir, asset.extractedBinaryName);
      if (fs.existsSync(extractedPath)) {
        fs.renameSync(extractedPath, binaryPath);
      }
    }
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
  return {
    version: process.env.YAVER_AGENT_VERSION || PACKAGE.version,
    repo: DEFAULT_REPO,
    note: 'Run a real agent command once to resolve the current published agent release.',
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

  const asset = resolveLocalAsset();
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

function resolveLocalAsset() {
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
      url: '',
      version: process.env.YAVER_AGENT_VERSION || PACKAGE.version,
    };
  }

  if (platform === 'darwin' || platform === 'linux') {
    return {
      cacheKey: `${platform}-${goArch}`,
      binaryName: 'yaver',
      extractedBinaryName: `yaver-${platform}-${goArch}`,
      downloadName: `yaver-${platform}-${goArch}.tar.gz`,
      archiveType: 'tar.gz',
      url: '',
      version: process.env.YAVER_AGENT_VERSION || PACKAGE.version,
    };
  }

  throw new Error(`unsupported platform for npm bootstrap: ${platform}`);
}

async function resolveAsset() {
  const platform = process.platform;
  const arch = process.arch;
  const goArch = arch === 'x64' ? 'amd64' : arch === 'arm64' ? 'arm64' : null;

  if (!goArch) {
    throw new Error(`unsupported architecture for npm bootstrap: ${arch}`);
  }

  const cacheKey = `${platform}-${goArch}`;
  if (!resolvedAssetPromiseByKey.has(cacheKey)) {
    resolvedAssetPromiseByKey.set(cacheKey, fetchRemoteAsset(platform, goArch));
  }
  return resolvedAssetPromiseByKey.get(cacheKey);
}

async function fetchRemoteAsset(platform, goArch) {
  if (platform === 'win32') {
    const release = await fetchLatestRelease(WINDOWS_REPO);
    const asset = findReleaseAsset(release, [`yaver-windows-${goArch}.exe`]);
    if (!asset) {
      throw new Error(`could not find Windows agent asset for ${goArch} in ${WINDOWS_REPO}`);
    }
    return {
      cacheKey: `${platform}-${goArch}`,
      binaryName: 'yaver.exe',
      downloadName: asset.name,
      archiveType: 'exe',
      url: asset.browser_download_url,
      version: release.tag_name.replace(/^v/, ''),
    };
  }

  if (platform === 'darwin' || platform === 'linux') {
    const release = await fetchLatestRelease(DEFAULT_REPO);
    const version = release.tag_name.replace(/^v/, '');
    const asset = findReleaseAsset(release, [
      `yaver-v${version}-${platform}-${goArch}.tar.gz`,
      `yaver-${platform}-${goArch}.tar.gz`,
    ]);
    if (!asset) {
      throw new Error(`could not find ${platform}/${goArch} agent tarball in ${DEFAULT_REPO}`);
    }
    return {
      cacheKey: `${platform}-${goArch}`,
      binaryName: 'yaver',
      extractedBinaryName: `yaver-${platform}-${goArch}`,
      downloadName: asset.name,
      archiveType: 'tar.gz',
      url: asset.browser_download_url,
      version,
    };
  }

  throw new Error(`unsupported platform for npm bootstrap: ${platform}`);
}

async function fetchLatestRelease(repo) {
  const response = await request(`https://api.github.com/repos/${repo}/releases?per_page=20`, {
    headers: {
      'User-Agent': `yaver-cli/${PACKAGE.version}`,
      Accept: 'application/vnd.github+json',
    },
  });
  const body = await readResponseBody(response);
  if (response.statusCode && response.statusCode >= 400) {
    throw new Error(`failed to resolve latest release (${response.statusCode}) from ${repo}`);
  }

  let releases;
  try {
    releases = JSON.parse(body);
  } catch (error) {
    throw new Error(`invalid release metadata from ${repo}: ${error.message}`);
  }

  const latest = Array.isArray(releases)
    ? releases.find((release) => !release.draft && !release.prerelease && semver.valid(String(release.tag_name || '').replace(/^v/, '')))
    : null;

  if (!latest || !latest.tag_name) {
    throw new Error(`could not find a published semver release in ${repo}`);
  }

  return latest;
}

function findReleaseAsset(release, names) {
  const byName = new Map((release.assets || []).map((asset) => [asset.name, asset]));
  for (const name of names) {
    const asset = byName.get(name);
    if (asset) return asset;
  }
  return null;
}

async function downloadToFile(url, destPath) {
  const response = await request(url);
  if (response.statusCode && response.statusCode >= 400) {
    throw new Error(`download failed (${response.statusCode}) from ${url}. Publish the matching CLI release assets or set YAVER_AGENT_BIN.`);
  }
  await pipeline(response, fs.createWriteStream(destPath));
}

function request(url, options = {}) {
  return new Promise((resolve, reject) => {
    https.get(url, options, (response) => {
      if (response.statusCode && response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        resolve(request(response.headers.location, options));
        return;
      }
      resolve(response);
    }).on('error', reject);
  });
}

function readResponseBody(response) {
  return new Promise((resolve, reject) => {
    let body = '';
    response.setEncoding('utf8');
    response.on('data', (chunk) => {
      body += chunk;
    });
    response.on('end', () => resolve(body));
    response.on('error', reject);
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
