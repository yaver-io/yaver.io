const { app, BrowserWindow, ipcMain, shell, Tray, Menu, nativeImage, dialog } = require('electron');
const path = require('path');
const fs = require('fs');
const https = require('https');
const http = require('http');
const { execSync, spawn, spawnSync } = require('child_process');
const os = require('os');

const AGENT_REPO = 'kivanccakmak/yaver.io';
const AGENT_BINARY_NAME = process.platform === 'win32' ? 'yaver.exe' : 'yaver';
const INSTALL_DIR = process.platform === 'win32'
  ? path.join(process.env.PROGRAMFILES || 'C:\\Program Files', 'Yaver')
  : process.platform === 'linux'
    ? path.join(os.homedir(), '.local', 'bin')
    : '/usr/local/bin';
const CONFIG_DIR = process.platform === 'win32'
  ? path.join(process.env.APPDATA || '', 'Yaver')
  : path.join(os.homedir(), '.yaver');
// Default hosted Convex instance (public endpoint). Override via CONVEX_SITE_URL env var.
const CONVEX_SITE_URL = process.env.CONVEX_SITE_URL || 'https://shocking-echidna-394.eu-west-1.convex.site';

// Default agent API base URL — configurable via settings
const DEFAULT_AGENT_URL = 'http://localhost:18080';

let mainWindow;
let tray = null;

function isWSL() {
  if (process.platform !== 'linux') return false;
  if (process.env.WSL_DISTRO_NAME || process.env.WSL_INTEROP) return true;
  try {
    const version = fs.readFileSync('/proc/version', 'utf8').toLowerCase();
    return version.includes('microsoft');
  } catch {
    return false;
  }
}

// ---------------------------------------------------------------------------
// Window management
// ---------------------------------------------------------------------------

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1000,
    height: 700,
    minWidth: 800,
    minHeight: 550,
    resizable: true,
    titleBarStyle: 'hiddenInset',
    backgroundColor: '#0f1117',
    show: false,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  mainWindow.loadFile(path.join(__dirname, 'index.html'));

  mainWindow.once('ready-to-show', () => {
    mainWindow.show();
  });

  mainWindow.on('close', (e) => {
    // Hide to tray instead of quitting (macOS / Linux)
    if (tray && process.platform !== 'win32') {
      e.preventDefault();
      mainWindow.hide();
    }
  });
}

function createTray() {
  // Tiny 16x16 template icon for menu bar
  const icon = nativeImage.createFromDataURL(
    'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAmklEQVQ4T2NkoBAwUqifYdAY8J+B4T8jECMDEJuRgQGOQWxkNciADEBDQGyQGqgaFAPABjAw/GdgZPzPwMjw/z8jwBUMDEBnMjD8h7sEZCLIJSA+IwPY1XAXgA0gygCQSxgZ/v9nBLkY7A0GBrigYjYjigEkNSgqQJqQvAFjI/lBZABMPwi5AJlrIGYSFQYY4CUJDJgBAACqEFBE0GFnQAAAABJRU5ErkJggg=='
  );
  tray = new Tray(icon);
  tray.setToolTip('Yaver');

  const updateTrayMenu = () => {
    const isSignedIn = hasToken();
    const agentRunning = isAgentRunning();

    const menu = Menu.buildFromTemplate([
      {
        label: agentRunning ? '● Agent Running' : '○ Agent Stopped',
        enabled: false,
      },
      { type: 'separator' },
      {
        label: 'Open Yaver',
        click: () => {
          if (mainWindow) {
            mainWindow.show();
            mainWindow.focus();
          } else {
            createWindow();
          }
        },
      },
      { type: 'separator' },
      ...(isSignedIn
        ? [{ label: 'Sign Out', click: () => signOut() }]
        : [{ label: 'Sign In...', click: () => { mainWindow?.show(); mainWindow?.focus(); } }]),
      { type: 'separator' },
      { label: 'Quit Yaver', click: () => { app.quit(); } },
    ]);

    tray.setContextMenu(menu);
  };

  updateTrayMenu();
  // Refresh tray menu every 30 seconds
  setInterval(updateTrayMenu, 30000);
}

app.whenReady().then(() => {
  createTray();
  createWindow();
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('activate', () => {
  if (mainWindow) {
    mainWindow.show();
  } else {
    createWindow();
  }
});

// ---------------------------------------------------------------------------
// Auth helpers
// ---------------------------------------------------------------------------

function getTokenPath() {
  return path.join(CONFIG_DIR, 'token');
}

function hasToken() {
  return fs.existsSync(getTokenPath());
}

function getToken() {
  try {
    return fs.readFileSync(getTokenPath(), 'utf8').trim();
  } catch {
    return null;
  }
}

function clearToken() {
  try {
    fs.unlinkSync(getTokenPath());
  } catch { /* ignore */ }
}

function signOut() {
  clearToken();
  // Stop agent service
  try {
    if (process.platform === 'darwin') {
      execSync('launchctl unload ~/Library/LaunchAgents/io.yaver.agent.plist 2>/dev/null', { stdio: 'ignore' });
    } else if (process.platform === 'linux') {
      if (isWSL()) {
        execSync('pkill -f "yaver serve" 2>/dev/null', { stdio: 'ignore' });
      } else {
        execSync('systemctl --user stop yaver 2>/dev/null', { stdio: 'ignore' });
      }
    } else if (process.platform === 'win32') {
      execSync('sc stop YaverAgent 2>nul', { stdio: 'ignore' });
    }
  } catch { /* ignore */ }

  if (mainWindow) {
    mainWindow.webContents.send('auth-state-changed', { signedIn: false });
    mainWindow.show();
    mainWindow.focus();
  }
}

function isAgentRunning() {
  try {
    if (process.platform === 'darwin') {
      const out = execSync('launchctl list io.yaver.agent 2>&1', { encoding: 'utf8' });
      return !out.includes('Could not find');
    } else if (process.platform === 'linux') {
      if (isWSL()) {
        const out = execSync('pgrep -af "yaver serve" 2>&1', { encoding: 'utf8' });
        return out.trim().length > 0;
      }
      const out = execSync('systemctl --user is-active yaver 2>&1', { encoding: 'utf8' });
      return out.trim() === 'active';
    } else if (process.platform === 'win32') {
      const out = execSync('sc query YaverAgent 2>&1', { encoding: 'utf8' });
      return out.includes('RUNNING');
    }
  } catch { /* */ }
  return false;
}

function isAgentInstalled() {
  return fs.existsSync(path.join(INSTALL_DIR, AGENT_BINARY_NAME));
}

// ---------------------------------------------------------------------------
// Config helpers (~/.yaver/config.json)
// ---------------------------------------------------------------------------

function getConfigPath() {
  return path.join(CONFIG_DIR, 'config.json');
}

function readConfig() {
  try {
    const raw = fs.readFileSync(getConfigPath(), 'utf8');
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

function writeConfig(config) {
  if (!fs.existsSync(CONFIG_DIR)) {
    fs.mkdirSync(CONFIG_DIR, { recursive: true });
  }
  fs.writeFileSync(getConfigPath(), JSON.stringify(config, null, 2), { mode: 0o600 });
}

// ---------------------------------------------------------------------------
// Settings helpers (stored in ~/.yaver/desktop-settings.json)
// ---------------------------------------------------------------------------

function getSettingsPath() {
  return path.join(CONFIG_DIR, 'desktop-settings.json');
}

function readSettings() {
  try {
    const raw = fs.readFileSync(getSettingsPath(), 'utf8');
    return JSON.parse(raw);
  } catch {
    return {
      agentBaseUrl: DEFAULT_AGENT_URL,
      autoStart: false,
      speechProvider: 'whisper',
      speechApiKey: '',
    };
  }
}

function writeSettings(settings) {
  if (!fs.existsSync(CONFIG_DIR)) {
    fs.mkdirSync(CONFIG_DIR, { recursive: true });
  }
  fs.writeFileSync(getSettingsPath(), JSON.stringify(settings, null, 2), { mode: 0o600 });
}

// ---------------------------------------------------------------------------
// Agent HTTP API proxy
// ---------------------------------------------------------------------------

function getAgentBaseUrl() {
  const settings = readSettings();
  return settings.agentBaseUrl || DEFAULT_AGENT_URL;
}

function agentRequest(method, urlPath, body) {
  return new Promise((resolve) => {
    const baseUrl = getAgentBaseUrl();
    const token = getToken();
    const url = new URL(urlPath, baseUrl);

    const options = {
      method,
      hostname: url.hostname,
      port: url.port,
      path: url.pathname + url.search,
      headers: {
        'Content-Type': 'application/json',
      },
      timeout: 15000,
    };

    if (token) {
      options.headers['Authorization'] = `Bearer ${token}`;
    }

    const bodyStr = body ? JSON.stringify(body) : null;
    if (bodyStr) {
      options.headers['Content-Length'] = Buffer.byteLength(bodyStr);
    }

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', (chunk) => (data += chunk));
      res.on('end', () => {
        try {
          resolve(JSON.parse(data));
        } catch {
          resolve({ ok: false, error: `Invalid response: ${data.substring(0, 200)}` });
        }
      });
    });

    req.on('error', (err) => {
      resolve({ ok: false, error: `Agent connection failed: ${err.message}` });
    });

    req.on('timeout', () => {
      req.destroy();
      resolve({ ok: false, error: 'Agent request timed out' });
    });

    if (bodyStr) {
      req.write(bodyStr);
    }
    req.end();
  });
}

// ---------------------------------------------------------------------------
// IPC Handlers
// ---------------------------------------------------------------------------

ipcMain.handle('get-app-state', async () => {
  const token = getToken();
  let tokenValid = false;

  if (token) {
    try {
      tokenValid = await validateToken(token);
    } catch {
      tokenValid = false;
    }
  }

  return {
    hasToken: !!token,
    tokenValid,
    agentInstalled: isAgentInstalled(),
    agentRunning: isAgentRunning(),
    platform: process.platform,
    arch: process.arch,
  };
});

ipcMain.handle('check-prerequisites', async () => {
  const results = { claude: false, platform: process.platform, arch: process.arch };

  try {
    execSync('claude --version', { stdio: 'ignore' });
    results.claude = true;
  } catch { /* not found */ }

  return results;
});

ipcMain.handle('download-agent', async () => {
  try {
    const platformMap = { darwin: 'darwin', linux: 'linux', win32: 'windows' };
    const archMap = { x64: 'amd64', arm64: 'arm64' };
    const plat = platformMap[process.platform] || process.platform;
    const arch = archMap[process.arch] || process.arch;
    const releaseMeta = await getLatestAgentRelease();
    const assetName = process.platform === 'win32'
      ? `yaver-${releaseMeta.tag_name}-${plat}-${arch}.exe`
      : `yaver-${releaseMeta.tag_name}-${plat}-${arch}.tar.gz`;

    const asset = releaseMeta.assets && releaseMeta.assets.find((a) => a.name === assetName);
    if (!asset) {
      return { success: false, error: `No release asset found for ${assetName}. You may need to build from source.` };
    }

    const destDir = INSTALL_DIR;
    if (!fs.existsSync(destDir)) {
      fs.mkdirSync(destDir, { recursive: true });
    }
    const destPath = path.join(destDir, AGENT_BINARY_NAME);
    if (process.platform === 'win32') {
      await downloadFile(asset.browser_download_url, destPath);
    } else {
      const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'yaver-agent-'));
      const archivePath = path.join(tempDir, assetName);
      await downloadFile(asset.browser_download_url, archivePath);
      const extractResult = spawnSync('tar', ['-xzf', archivePath, '-C', tempDir], { encoding: 'utf8' });
      if (extractResult.status !== 0) {
        throw new Error(extractResult.stderr || 'Failed to extract agent archive');
      }
      const extractedBinary = path.join(tempDir, `yaver-${plat}-${arch}`);
      if (!fs.existsSync(extractedBinary)) {
        throw new Error(`Extracted archive did not contain yaver-${plat}-${arch}`);
      }
      fs.copyFileSync(extractedBinary, destPath);
      fs.rmSync(tempDir, { recursive: true, force: true });
    }

    if (process.platform !== 'win32') {
      fs.chmodSync(destPath, 0o755);
    }

    return { success: true, path: destPath };
  } catch (err) {
    return { success: false, error: err.message };
  }
});

// Shared auth handler — opens yaver.io auth page, waits for local callback
function startOAuthFlow(provider) {
  // Open the web auth page with provider pre-selected, or generic page
  const authUrl = provider
    ? `https://yaver.io/api/auth/oauth/${provider}?client=desktop`
    : 'https://yaver.io/auth?client=desktop';
  shell.openExternal(authUrl);

  return new Promise((resolve) => {
    const server = http.createServer((req, res) => {
      const url = new URL(req.url, 'http://localhost');
      const token = url.searchParams.get('token');
      if (token) {
        if (!fs.existsSync(CONFIG_DIR)) {
          fs.mkdirSync(CONFIG_DIR, { recursive: true });
        }
        fs.writeFileSync(getTokenPath(), token, { mode: 0o600 });

        res.writeHead(200, { 'Content-Type': 'text/html' });
        res.end(`<html><body style="background:#0f1117;color:#fff;font-family:system-ui;display:flex;align-items:center;justify-content:center;height:100vh;flex-direction:column">
          <h2 style="margin-bottom:8px">Authenticated!</h2>
          <p style="color:#9ca3af">You can close this tab and return to Yaver.</p>
        </body></html>`);
        server.close();

        if (mainWindow) {
          mainWindow.webContents.send('auth-state-changed', { signedIn: true });
        }

        resolve({ success: true });
      } else {
        res.writeHead(400);
        res.end('Missing token');
      }
    });

    server.listen(19836, '127.0.0.1');

    setTimeout(() => {
      server.close();
      resolve({ success: false, error: 'Authentication timed out.' });
    }, 5 * 60 * 1000);
  });
}

ipcMain.handle('authenticate', () => startOAuthFlow('google'));
ipcMain.handle('authenticate-microsoft', () => startOAuthFlow('microsoft'));
ipcMain.handle('authenticate-apple', () => startOAuthFlow('apple'));

ipcMain.handle('install-service', async () => {
  try {
    const agentPath = path.join(INSTALL_DIR, AGENT_BINARY_NAME);

    if (!fs.existsSync(agentPath)) {
      return { success: false, error: 'Agent binary not found. Please download first.' };
    }

    if (process.platform === 'darwin') {
      return installLaunchd(agentPath);
    } else if (process.platform === 'linux') {
      if (isWSL()) {
        return startAgentDetached(agentPath);
      }
      return installSystemd(agentPath);
    } else if (process.platform === 'win32') {
      return installWindowsService(agentPath);
    }

    return { success: false, error: `Unsupported platform: ${process.platform}` };
  } catch (err) {
    return { success: false, error: err.message };
  }
});

ipcMain.handle('restart-service', async () => {
  try {
    if (process.platform === 'darwin') {
      execSync('launchctl unload ~/Library/LaunchAgents/io.yaver.agent.plist 2>/dev/null || true', { stdio: 'ignore' });
      execSync('launchctl load -w ~/Library/LaunchAgents/io.yaver.agent.plist', { stdio: 'ignore' });
    } else if (process.platform === 'linux') {
      if (isWSL()) {
        const agentPath = path.join(INSTALL_DIR, AGENT_BINARY_NAME);
        execSync('pkill -f "yaver serve" 2>/dev/null || true');
        return startAgentDetached(agentPath);
      }
      execSync('systemctl --user restart yaver');
    } else if (process.platform === 'win32') {
      execSync('sc stop YaverAgent 2>nul & sc start YaverAgent');
    }
    return { success: true };
  } catch (err) {
    return { success: false, error: err.message };
  }
});

ipcMain.handle('get-status', async () => {
  return {
    running: isAgentRunning(),
    installed: isAgentInstalled(),
    hasToken: hasToken(),
  };
});

ipcMain.handle('sign-out', async () => {
  signOut();
  return { success: true };
});

ipcMain.handle('validate-token', async () => {
  const token = getToken();
  if (!token) return { valid: false };
  try {
    const valid = await validateToken(token);
    return { valid };
  } catch {
    return { valid: false };
  }
});

// ---------------------------------------------------------------------------
// Agent API proxy handler
// ---------------------------------------------------------------------------

ipcMain.handle('agent-request', async (_event, method, urlPath, body) => {
  return agentRequest(method, urlPath, body);
});

// ---------------------------------------------------------------------------
// Config handlers
// ---------------------------------------------------------------------------

ipcMain.handle('get-config', async () => {
  return readConfig();
});

ipcMain.handle('save-config', async (_event, config) => {
  try {
    writeConfig(config);
    return { success: true };
  } catch (err) {
    return { success: false, error: err.message };
  }
});

// ---------------------------------------------------------------------------
// Settings handlers
// ---------------------------------------------------------------------------

ipcMain.handle('get-settings', async () => {
  return readSettings();
});

ipcMain.handle('save-settings', async (_event, settings) => {
  try {
    writeSettings(settings);
    return { success: true };
  } catch (err) {
    return { success: false, error: err.message };
  }
});

// ---------------------------------------------------------------------------
// Survey & User info handlers
// ---------------------------------------------------------------------------

ipcMain.handle('submit-survey', async (_event, data) => {
  const token = getToken();
  if (!token) return { success: false, error: 'Not signed in' };
  try {
    const body = JSON.stringify(data);
    return await new Promise((resolve) => {
      const url = new URL('/auth/survey', CONVEX_SITE_URL);
      const mod = url.protocol === 'https:' ? https : http;
      const req = mod.request(url.toString(), {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${token}`,
          'Content-Type': 'application/json',
          'Content-Length': Buffer.byteLength(body),
        },
      }, (res) => {
        let responseData = '';
        res.on('data', (chunk) => { responseData += chunk; });
        res.on('end', () => {
          resolve({ success: res.statusCode === 200, data: responseData });
        });
      });
      req.on('error', (err) => resolve({ success: false, error: err.message }));
      req.write(body);
      req.end();
    });
  } catch (err) {
    return { success: false, error: err.message };
  }
});

ipcMain.handle('get-user-info', async () => {
  const token = getToken();
  if (!token) return { signedIn: false };
  try {
    return await new Promise((resolve) => {
      const url = new URL('/auth/validate', CONVEX_SITE_URL);
      https.get(url.toString(), {
        headers: { 'Authorization': `Bearer ${token}` },
      }, (res) => {
        let data = '';
        res.on('data', (chunk) => { data += chunk; });
        res.on('end', () => {
          if (res.statusCode === 200) {
            try {
              const parsed = JSON.parse(data);
              resolve({ signedIn: true, user: parsed.user || parsed });
            } catch {
              resolve({ signedIn: true });
            }
          } else {
            resolve({ signedIn: false });
          }
        });
      }).on('error', () => resolve({ signedIn: false }));
    });
  } catch {
    return { signedIn: false };
  }
});

// ---------------------------------------------------------------------------
// Managed relay subscription handler
// ---------------------------------------------------------------------------

ipcMain.handle('get-subscription', async () => {
  const token = getToken();
  if (!token) return null;
  try {
    const url = new URL('/subscription', CONVEX_SITE_URL);
    return await new Promise((resolve) => {
      const req = https.get(url.toString(), {
        headers: {
          'Authorization': `Bearer ${token}`,
          'User-Agent': 'YaverDesktop/1.0',
        },
      }, (res) => {
        let data = '';
        res.on('data', (chunk) => { data += chunk; });
        res.on('end', () => {
          if (res.statusCode === 200) {
            try { resolve(JSON.parse(data)); } catch { resolve(null); }
          } else {
            resolve(null);
          }
        });
      });
      req.on('error', () => resolve(null));
    });
  } catch {
    return null;
  }
});

ipcMain.handle('open-external', async (_event, url) => {
  if (typeof url === 'string' && (url.startsWith('https://') || url.startsWith('http://'))) {
    shell.openExternal(url);
  }
});

// ---------------------------------------------------------------------------
// File picker handler
// ---------------------------------------------------------------------------

ipcMain.handle('pick-file', async (_event, options) => {
  const result = await dialog.showOpenDialog(mainWindow, {
    properties: ['openFile'],
    filters: options?.filters || [
      { name: 'Images', extensions: ['jpg', 'jpeg', 'png', 'gif', 'webp'] },
      { name: 'All Files', extensions: ['*'] },
    ],
  });

  if (result.canceled || result.filePaths.length === 0) {
    return null;
  }

  const filePath = result.filePaths[0];
  const data = fs.readFileSync(filePath);
  const ext = path.extname(filePath).toLowerCase();
  const mimeMap = {
    '.jpg': 'image/jpeg',
    '.jpeg': 'image/jpeg',
    '.png': 'image/png',
    '.gif': 'image/gif',
    '.webp': 'image/webp',
  };

  return {
    path: filePath,
    name: path.basename(filePath),
    base64: data.toString('base64'),
    mimeType: mimeMap[ext] || 'application/octet-stream',
    size: data.length,
  };
});

// ---------------------------------------------------------------------------
// Token validation
// ---------------------------------------------------------------------------

async function validateToken(token) {
  return new Promise((resolve) => {
    const url = new URL('/auth/validate', CONVEX_SITE_URL);
    https.get(url.toString(), {
      headers: {
        'Authorization': `Bearer ${token}`,
        'User-Agent': 'YaverDesktop/1.0',
      },
    }, (res) => {
      resolve(res.statusCode === 200);
    }).on('error', () => resolve(false));
  });
}

// ---------------------------------------------------------------------------
// Platform service installers
// ---------------------------------------------------------------------------

function installLaunchd(agentPath) {
  const plistPath = path.join(os.homedir(), 'Library', 'LaunchAgents', 'io.yaver.agent.plist');
  const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>io.yaver.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>${agentPath}</string>
    <string>serve</string>
    <string>--debug</string>
    <string>--work-dir=${os.homedir()}</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${CONFIG_DIR}/agent.log</string>
  <key>StandardErrorPath</key>
  <string>${CONFIG_DIR}/agent.err</string>
</dict>
</plist>`;

  if (!fs.existsSync(CONFIG_DIR)) {
    fs.mkdirSync(CONFIG_DIR, { recursive: true });
  }
  // Unload first if already loaded
  try {
    execSync(`launchctl unload "${plistPath}" 2>/dev/null`, { stdio: 'ignore' });
  } catch { /* ignore */ }

  fs.writeFileSync(plistPath, plist);
  execSync(`launchctl load -w "${plistPath}"`);
  return { success: true };
}

function installSystemd(agentPath) {
  const unitDir = path.join(os.homedir(), '.config', 'systemd', 'user');
  if (!fs.existsSync(unitDir)) {
    fs.mkdirSync(unitDir, { recursive: true });
  }
  const unitPath = path.join(unitDir, 'yaver.service');
  const unit = `[Unit]
Description=Yaver Desktop Agent
After=network.target

[Service]
ExecStart=${agentPath} serve --debug --work-dir=${os.homedir()}
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`;

  fs.writeFileSync(unitPath, unit);
  execSync('systemctl --user daemon-reload');
  execSync('systemctl --user enable --now yaver');
  return { success: true };
}

function startAgentDetached(agentPath) {
  const logDir = CONFIG_DIR;
  if (!fs.existsSync(logDir)) {
    fs.mkdirSync(logDir, { recursive: true });
  }

  const out = fs.openSync(path.join(logDir, 'agent.log'), 'a');
  const err = fs.openSync(path.join(logDir, 'agent.err'), 'a');
  const child = spawn(agentPath, ['serve'], {
    detached: true,
    stdio: ['ignore', out, err],
  });
  child.unref();
  return { success: true, mode: 'process' };
}

function installWindowsService(agentPath) {
  try {
    execSync(`sc create YaverAgent binPath= "\\"${agentPath}\\" serve --debug --work-dir ${os.homedir()}" start= auto DisplayName= "Yaver Agent"`);
    execSync('sc start YaverAgent');
    return { success: true };
  } catch (err) {
    return { success: false, error: `Failed to create Windows service: ${err.message}` };
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function httpGetJson(url) {
  return new Promise((resolve, reject) => {
    const get = (u) => {
      https.get(u, { headers: { 'User-Agent': 'YaverDesktop/1.0' } }, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          get(res.headers.location);
          return;
        }
        let data = '';
        res.on('data', (c) => (data += c));
        res.on('end', () => {
          try { resolve(JSON.parse(data)); }
          catch (e) { reject(new Error('Invalid JSON response')); }
        });
      }).on('error', reject);
    };
    get(url);
  });
}

async function getLatestAgentRelease() {
  const releases = await httpGetJson(`https://api.github.com/repos/${AGENT_REPO}/releases?per_page=100`);
  if (!Array.isArray(releases)) {
    throw new Error('Unexpected GitHub releases response');
  }

  const release = releases.find((entry) => typeof entry.tag_name === 'string' && /^v\d/.test(entry.tag_name));
  if (!release) {
    throw new Error('No semver Yaver release found');
  }

  return release;
}

function downloadFile(url, dest) {
  return new Promise((resolve, reject) => {
    const download = (u) => {
      const mod = u.startsWith('https') ? https : http;
      mod.get(u, { headers: { 'User-Agent': 'YaverDesktop/1.0' } }, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          download(res.headers.location);
          return;
        }
        if (res.statusCode !== 200) {
          reject(new Error(`Download failed with status ${res.statusCode}`));
          return;
        }
        const file = fs.createWriteStream(dest);
        res.pipe(file);
        file.on('finish', () => file.close(resolve));
      }).on('error', reject);
    };
    download(url);
  });
}
