const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn, spawnSync } = require('child_process');
const { pipeline } = require('stream/promises');
const https = require('https');
const semver = require('semver');

const PACKAGE = require('../package.json');

const DEFAULT_REPO = process.env.YAVER_AGENT_REPO || 'kivanccakmak/yaver.io';
const WINDOWS_REPO = process.env.YAVER_WINDOWS_AGENT_REPO || 'kivanccakmak/yaver-cli';
const CACHE_ROOT = process.env.YAVER_AGENT_CACHE_DIR || path.join(os.homedir(), '.yaver', 'bin');
let resolvedAgentVersionPromise = null;
const resolvedAssetPromiseByKey = new Map();

// Magic bytes for executable formats. Used to sanity-check a downloaded
// binary before we spawn it — an HTML error page or a half-written file
// will fail this and we'll redownload instead of producing SIGKILL.
const EXECUTABLE_MAGICS = [
  Buffer.from([0xcf, 0xfa, 0xed, 0xfe]), // Mach-O 64-bit LE (macOS)
  Buffer.from([0xce, 0xfa, 0xed, 0xfe]), // Mach-O 32-bit LE
  Buffer.from([0xca, 0xfe, 0xba, 0xbe]), // Mach-O fat binary
  Buffer.from([0x7f, 0x45, 0x4c, 0x46]), // ELF (Linux) — \x7fELF
  Buffer.from([0x4d, 0x5a]),             // PE (Windows) — MZ
];

async function ensureAgentBinary({ quiet = false } = {}) {
  // Try cache first — when the user already has any version of yaver
  // installed locally, we shouldn't fail just because GitHub's API
  // rate-limited the unauthenticated /releases call. Falling back to
  // the cached binary keeps every yaver subcommand working through
  // GH's daily rate-limit windows. Only call resolveAsset (which can
  // throw on 403) when no cache hit AND no repo binary is present.
  try {
    const asset = await resolveAsset();
    const localAgentPath = resolveLocalAgentBinary(asset);
    if (localAgentPath) return localAgentPath;
    const installDir = path.join(CACHE_ROOT, asset.version, asset.cacheKey);
    const binaryPath = path.join(installDir, asset.binaryName);
    if (fs.existsSync(binaryPath)) {
      if (process.platform !== 'win32') fs.chmodSync(binaryPath, 0o755);
      return binaryPath;
    }
    return await downloadAndCacheAgent(asset, { quiet });
  } catch (err) {
    // GH rate-limit OR network failure — try the most recent cached
    // version so the user can continue using yaver. If nothing is
    // cached either, surface the original error so the install path
    // is clear.
    const fallback = findMostRecentCachedAgent();
    if (fallback) {
      if (!quiet) {
        console.error(
          `[yaver] release lookup failed (${String(err.message || err).split('\n')[0]}); ` +
          `using cached agent at ${fallback}`,
        );
      }
      return fallback;
    }
    throw err;
  }
}

/** Walk CACHE_ROOT and return the path to the newest yaver binary
 *  matching this platform — used as fallback when GH /releases is
 *  rate-limited and no specific version was resolvable. */
function findMostRecentCachedAgent() {
  if (!fs.existsSync(CACHE_ROOT)) return null;
  const platform = process.platform;
  const arch = process.arch;
  const goArch = arch === 'x64' ? 'amd64' : arch === 'arm64' ? 'arm64' : arch;
  const cacheKey = `${platform}-${goArch}`;
  const binaryName = platform === 'win32' ? 'yaver.exe' : 'yaver';
  const versions = fs.readdirSync(CACHE_ROOT).filter((v) => semver.valid(v));
  versions.sort(semver.rcompare);
  for (const v of versions) {
    const p = path.join(CACHE_ROOT, v, cacheKey, binaryName);
    if (fs.existsSync(p)) {
      if (process.platform !== 'win32') {
        try { fs.chmodSync(p, 0o755); } catch (_) {}
      }
      return p;
    }
  }
  return null;
}

async function downloadAndCacheAgent(asset, { quiet }) {
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
  // Proactively strip macOS quarantine + re-adhoc-sign on
  // freshly-downloaded binaries. Without this:
  //   - Gatekeeper SIGKILLs the first exec because Yaver release
  //     tarballs are not notarized (adhoc-signed at link time only).
  //   - If the Go linker's adhoc signature didn't survive the tarball
  //     round-trip, the kernel refuses to load it ("load code
  //     signature error 2"). codesign --force --sign - rebuilds a
  //     valid adhoc signature against the current bytes.
  // Doing both here means the happy path never sees SIGKILL in the
  // first place.
  if (process.platform === 'darwin') {
    try {
      spawnSync('xattr', ['-dr', 'com.apple.quarantine', binaryPath], { stdio: 'ignore' });
    } catch (_err) {}
    try {
      spawnSync('codesign', ['--force', '--sign', '-', binaryPath], { stdio: 'ignore' });
    } catch (_err) {}
  }
  // Sanity-check the extracted binary. A truncated tarball or an
  // HTML error page saved as ".tar.gz" can leave us with a file that
  // exists but isn't actually executable — spawning it produces
  // SIGKILL and a cryptic error. Fail loudly here with a message
  // that points at the actual problem (bad download) so the caller
  // can redownload.
  if (!looksLikeExecutable(binaryPath)) {
    try { fs.rmSync(binaryPath, { force: true }); } catch (_err) {}
    throw new Error(
      `downloaded agent binary at ${binaryPath} is not a valid executable ` +
        `(likely a truncated download or GitHub rate-limit HTML page). ` +
        `Retry in a minute, or set YAVER_AGENT_BIN to a local yaver binary.`,
    );
  }
  return binaryPath;
}

async function runAgentCommand(args, options = {}) {
  // One auto-recovery retry on SIGKILL of a cached binary. SIGKILL on a
  // freshly-downloaded binary almost always means macOS Gatekeeper
  // quarantined it (no notarization), or the download was truncated.
  // Both are self-healing: strip quarantine + redownload + retry once.
  // We never retry for user-initiated signals or normal exits.
  let attempt = 0;
  const maxAttempts = 2;

  while (true) {
    attempt += 1;
    const spawnSpec = await resolveSpawnSpec(args, options);
    const isCachedBinary = spawnSpec.command.startsWith(CACHE_ROOT);

    try {
      await spawnAndWait(spawnSpec, args);
      return;
    } catch (err) {
      const { signal, recoverable } = err;
      if (
        recoverable &&
        signal === 'SIGKILL' &&
        isCachedBinary &&
        attempt < maxAttempts
      ) {
        const healed = await attemptSigkillRecovery(spawnSpec.command, { quiet: options.quiet });
        if (healed) {
          console.error(`Yaver agent: recovered from SIGKILL (${healed}). Retrying...`);
          continue;
        }
      }
      throw err;
    }
  }
}

async function resolveSpawnSpec(args, options) {
  const localAgent = resolveLocalAgentCommand(args);
  if (localAgent) return localAgent;
  return { command: await ensureAgentBinary(options), args };
}

function spawnAndWait(spawnSpec, args) {
  const child = spawn(spawnSpec.command, spawnSpec.args, {
    stdio: 'inherit',
    env: spawnSpec.env || process.env,
    cwd: spawnSpec.cwd || process.cwd(),
  });

  // Forward Ctrl-C / SIGTERM / SIGHUP from the wrapper to the child
  // so the user's interrupt actually reaches the running binary
  // (instead of killing the wrapper while leaving the child orphaned).
  // Without this, Ctrl-C would just kill the Node wrapper and the
  // wrapper's exit handler would then report "terminated by signal
  // SIGINT" as if it were an error.
  const forwarded = ['SIGINT', 'SIGTERM', 'SIGHUP'];
  const forwarders = forwarded.map((sig) => {
    const handler = () => {
      try { child.kill(sig); } catch (_e) { /* child already gone */ }
    };
    process.on(sig, handler);
    return [sig, handler];
  });

  return new Promise((resolve, reject) => {
    child.on('error', reject);
    child.on('exit', (code, signal) => {
      for (const [sig, handler] of forwarders) {
        process.removeListener(sig, handler);
      }
      // User-initiated interrupts — exit quietly. The user already
      // knows they hit Ctrl-C; surfacing a red ❌ is noise.
      if (signal === 'SIGINT' || signal === 'SIGTERM' || signal === 'SIGHUP') {
        resolve();
        return;
      }
      if (signal) {
        const error = new Error(diagnoseAbnormalSignal(signal, args));
        error.signal = signal;
        error.recoverable = true;
        reject(error);
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

// attemptSigkillRecovery tries the three things that actually fix a
// SIGKILL on a cached Yaver binary:
//   1. strip macOS quarantine xattrs (Gatekeeper-killed Mach-Os)
//   2. re-adhoc-sign the Mach-O (fixes broken signature from the
//      agent's in-place auto-update — kernel refuses to exec, logs
//      "load code signature error 2")
//   3. redownload the binary if the magic bytes don't look like an
//      executable (truncated download / HTML error page)
// Returns the recovery action taken, or null if nothing could be done.
async function attemptSigkillRecovery(binaryPath, { quiet = false } = {}) {
  if (!fs.existsSync(binaryPath)) {
    // The binary went away mid-flight; re-download by returning null
    // so the caller retries the full resolve path next loop.
    return null;
  }

  // 1. Sanity check first — if the binary isn't even executable-shaped,
  // nothing we do to it will help. Redownload.
  if (!looksLikeExecutable(binaryPath)) {
    try {
      fs.rmSync(binaryPath, { force: true });
      resolvedAssetPromiseByKey.clear();
    } catch (_err) {}
    if (!quiet) console.error('Yaver agent: cached binary was corrupt, redownloading...');
    return 'redownload-corrupt-binary';
  }

  if (process.platform === 'darwin') {
    // 2. Strip quarantine. Unsigned binaries downloaded via https get
    // tagged com.apple.quarantine and the kernel SIGKILLs them on
    // first exec (no dialog, because the parent is a CLI).
    try {
      spawnSync('xattr', ['-dr', 'com.apple.quarantine', binaryPath], { stdio: 'ignore' });
    } catch (_err) {}
    try { fs.chmodSync(binaryPath, 0o755); } catch (_err) {}

    // 3. Re-adhoc-sign. When the agent's own self-update rewrites the
    // binary in place, the original adhoc signature no longer
    // matches the new contents and the kernel refuses to exec it
    // (dmesg: "load code signature error 2"). `codesign --force
    // --sign -` rebuilds the adhoc signature against the current
    // bytes, which is all the kernel needs to let exec proceed.
    try {
      const result = spawnSync('codesign', ['--force', '--sign', '-', binaryPath], {
        stdio: ['ignore', 'pipe', 'pipe'],
        encoding: 'utf8',
      });
      if (result.status === 0) {
        // Verify the resigned binary actually passes the kernel check
        // now. If codesign succeeded we're done — the retry will work.
        return 'resign-macos-adhoc';
      }
      // If codesign failed (e.g. missing developer tools), fall through
      // to the quarantine-only return below.
    } catch (_err) {}

    return 'strip-macos-quarantine';
  }

  // On Linux / Windows a SIGKILL of an intact binary usually means an
  // OOM kill or external pkill; re-exec is unlikely to help, so
  // return null and let the original diagnostic message stand.
  return null;
}

function looksLikeExecutable(binaryPath) {
  try {
    const fd = fs.openSync(binaryPath, 'r');
    const buf = Buffer.alloc(4);
    const bytesRead = fs.readSync(fd, buf, 0, 4, 0);
    fs.closeSync(fd);
    if (bytesRead < 2) return false;
    return EXECUTABLE_MAGICS.some((magic) => {
      if (magic.length > bytesRead) return false;
      return buf.slice(0, magic.length).equals(magic);
    });
  } catch (_err) {
    return false;
  }
}

// diagnoseAbnormalSignal turns a child-process termination signal into
// a concrete next-step the user can actually take. Each branch points
// at the most-common cause — pulled from real bug reports — and lists
// the exact command that resolves it.
function diagnoseAbnormalSignal(signal, args = []) {
  const cmd = args[0] || 'agent';
  switch (signal) {
    case 'SIGKILL': {
      const lines = [
        `agent process was killed (SIGKILL) — this is usually NOT a bug, ` +
          `the OS or another process forced it down.`,
        ``,
        `Most-likely cause first:`,
        `  1. Another yaver agent is already running (port 18080 is taken).`,
        `       check:  lsof -i :18080`,
        `       fix:    yaver stop   (or:  pkill -f 'yaver.*serve')`,
      ];
      if (cmd === 'auth') {
        lines.push(
          `       Note: \`yaver auth\` doesn't need a running agent. If one is`,
          `       already up in bootstrap mode, pair from your Yaver mobile app instead`,
          `       of running \`yaver auth\` again.`,
        );
      }
      lines.push(
        ``,
        `Other possibilities:`,
        `  2. macOS Gatekeeper quarantined the binary. Run:`,
        `       xattr -dr com.apple.quarantine ~/.yaver/bin`,
        `  3. The OS killed it for OOM / power. Check Console.app → Crash Reports.`,
        `  4. The binary is corrupt. Reinstall:  npm i -g yaver-cli@latest`,
      );
      return lines.join('\n');
    }
    case 'SIGABRT':
      return `agent aborted (SIGABRT). Most likely a Go panic — check ~/.yaver/agent.log for the stack trace.`;
    case 'SIGSEGV':
    case 'SIGBUS':
      return (
        `agent crashed (${signal}). Likely binary corruption or an arch mismatch.\n` +
        `Try:  npm i -g yaver-cli@latest    to reinstall a clean binary.`
      );
    case 'SIGPIPE':
      return `agent's stdout/stderr pipe was closed. If you piped through head/less, that's expected — re-run without the pipe.`;
    default:
      return `agent terminated by signal ${signal}. Check ~/.yaver/agent.log for details.`;
  }
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
    // `go run` requires the module dir as cwd, so we chdir into agentDir
    // for the Go toolchain itself. The compiled agent inherits that cwd
    // and would lose the user's actual working directory — clobbering
    // every command that resolves a project from os.Getwd() (wire push,
    // wireless push, code, etc.). Pass it through as YAVER_USER_CWD so
    // the agent can chdir back to it before any cwd-sensitive logic.
    return {
      command: 'go',
      args: ['run', '.', ...agentArgs],
      cwd: agentDir,
      env: { ...process.env, YAVER_USER_CWD: process.cwd() },
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
      version: stripCliTagPrefix(release.tag_name),
    };
  }

  if (platform === 'darwin' || platform === 'linux') {
    const release = await fetchLatestRelease(DEFAULT_REPO);
    const version = stripCliTagPrefix(release.tag_name);
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

// Strip the CLI release tag prefix and return the bare semver — or
// null if this isn't a CLI release at all. Tags now live in per-surface
// namespaces: `cli/v1.99.167`, `mobile/v1.18.91`, `web/v1.1.131`, etc.
// Pre-1.99.124 releases were just `v1.99.149`. Only those two shapes
// belong to the CLI; mobile/web/relay tags must NOT pass through, or
// `findReleaseAsset` will look for a yaver-linux-arm64 tarball on a
// mobile release and silently fail. Returning null here lets
// `semver.valid(stripCliTagPrefix(...))` filter non-CLI tags out.
function stripCliTagPrefix(tag) {
  const match = String(tag || '').match(/^(?:cli\/)?v(.+)$/);
  return match ? match[1] : null;
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
    ? releases.find((release) => !release.draft && !release.prerelease && semver.valid(stripCliTagPrefix(String(release.tag_name || ''))))
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
