const { app, BrowserWindow, BrowserView, ipcMain, shell, Tray, Menu, nativeImage, dialog } = require('electron');
const path = require('path');
const fs = require('fs');
const http = require('http');
const https = require('https');
const os = require('os');
const { execSync } = require('child_process');

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const CONVEX_SITE_URL = process.env.CONVEX_SITE_URL || 'https://shocking-echidna-394.eu-west-1.convex.site';
const CONFIG_DIR = process.platform === 'win32'
  ? path.join(process.env.APPDATA || '', 'Yaver')
  : path.join(os.homedir(), '.yaver');
const CONFIG_FILE = path.join(CONFIG_DIR, 'config.json');
const DESKTOP_SETTINGS_FILE = path.join(CONFIG_DIR, 'desktop-settings.json');

let mainWindow = null;
let previewView = null;
let tray = null;

// Current connection state
let agentBaseUrl = null; // set when connected to a device (direct or relay)
let authToken = null;

// ---------------------------------------------------------------------------
// Config helpers
// ---------------------------------------------------------------------------

function loadConfig() {
  try {
    if (fs.existsSync(CONFIG_FILE)) {
      return JSON.parse(fs.readFileSync(CONFIG_FILE, 'utf8'));
    }
  } catch {}
  return {};
}

function saveConfig(cfg) {
  fs.mkdirSync(CONFIG_DIR, { recursive: true });
  fs.writeFileSync(CONFIG_FILE, JSON.stringify(cfg, null, 2));
}

function loadDesktopSettings() {
  try {
    if (fs.existsSync(DESKTOP_SETTINGS_FILE)) {
      return JSON.parse(fs.readFileSync(DESKTOP_SETTINGS_FILE, 'utf8'));
    }
  } catch {}
  return { splitRatio: 0.5 };
}

function saveDesktopSettings(settings) {
  fs.mkdirSync(CONFIG_DIR, { recursive: true });
  fs.writeFileSync(DESKTOP_SETTINGS_FILE, JSON.stringify(settings, null, 2));
}

function hasToken() {
  const cfg = loadConfig();
  return !!(cfg.auth_token || cfg.authToken);
}

function getToken() {
  const cfg = loadConfig();
  return cfg.auth_token || cfg.authToken || '';
}

// ---------------------------------------------------------------------------
// Window management
// ---------------------------------------------------------------------------

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1400,
    height: 900,
    minWidth: 900,
    minHeight: 600,
    resizable: true,
    titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'default',
    backgroundColor: '#0a0a0a',
    show: false,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      webviewTag: true,
    },
  });

  mainWindow.loadFile(path.join(__dirname, '..', 'renderer', 'index.html'));
  mainWindow.once('ready-to-show', () => mainWindow.show());

  mainWindow.on('close', (e) => {
    if (tray && process.platform === 'darwin') {
      e.preventDefault();
      mainWindow.hide();
    }
  });

  mainWindow.on('closed', () => {
    mainWindow = null;
    previewView = null;
  });
}

function createTray() {
  const icon = nativeImage.createFromDataURL(
    'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAmklEQVQ4T2NkoBAwUqifYdAY8J+B4T8jECMDEJuRgQGOQWxkNciADEBDQGyQGqgaFAPABjAw/GdgZPzPwMjw/z8jwBUMDEBnMjD8h7sEZCLIJSA+IwPY1XAXgA0gygCQSxgZ/v9nBLkY7A0GBrigYjYjigEkNSgqQJqQvAFjI/lBZABMPwi5AJlrIGYSFQYY4CUJDJgBAACqEFBE0GFnQAAAABJRU5ErkJggg=='
  );
  tray = new Tray(icon);
  tray.setToolTip('Yaver Desktop');

  const menu = Menu.buildFromTemplate([
    { label: 'Open Yaver', click: () => { mainWindow ? mainWindow.show() : createWindow(); } },
    { type: 'separator' },
    { label: 'Quit', click: () => app.quit() },
  ]);
  tray.setContextMenu(menu);
}

app.whenReady().then(() => {
  createTray();
  createWindow();
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('activate', () => {
  mainWindow ? mainWindow.show() : createWindow();
});

// ---------------------------------------------------------------------------
// Auth flow (OAuth via browser)
// ---------------------------------------------------------------------------

let authCallbackServer = null;

function startAuthFlow(provider) {
  return new Promise((resolve, reject) => {
    // Start local callback server
    if (authCallbackServer) {
      try { authCallbackServer.close(); } catch {}
    }

    authCallbackServer = http.createServer((req, res) => {
      const url = new URL(req.url, 'http://127.0.0.1:19836');
      const token = url.searchParams.get('token');
      if (token) {
        res.writeHead(200, { 'Content-Type': 'text/html' });
        res.end('<html><body style="font-family:system-ui;text-align:center;padding:60px;background:#0a0a0a;color:#fff;"><h2>Signed in!</h2><p>You can close this tab and return to Yaver Desktop.</p></body></html>');
        authCallbackServer.close();
        authCallbackServer = null;

        // Save token
        const cfg = loadConfig();
        cfg.auth_token = token;
        saveConfig(cfg);
        authToken = token;

        resolve(token);
      } else {
        res.writeHead(400);
        res.end('Missing token');
      }
    });

    authCallbackServer.listen(19836, '127.0.0.1', () => {
      const authUrl = `https://yaver.io/auth?client=desktop`;
      shell.openExternal(authUrl);
    });

    authCallbackServer.on('error', (err) => {
      reject(err);
    });

    // 5 min timeout
    setTimeout(() => {
      if (authCallbackServer) {
        authCallbackServer.close();
        authCallbackServer = null;
        reject(new Error('Auth timeout'));
      }
    }, 300000);
  });
}

// ---------------------------------------------------------------------------
// Agent HTTP proxy (works with both local and remote agents via relay)
// ---------------------------------------------------------------------------

async function agentRequest(method, urlPath, body) {
  if (!agentBaseUrl) {
    throw new Error('Not connected to any device');
  }

  const url = `${agentBaseUrl}${urlPath}`;
  const headers = { 'Content-Type': 'application/json' };
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`;
  }

  const opts = { method, headers };
  if (body && method !== 'GET') {
    opts.body = JSON.stringify(body);
  }

  const res = await fetch(url, opts);
  const text = await res.text();
  try {
    return JSON.parse(text);
  } catch {
    return { ok: res.ok, status: res.status, body: text };
  }
}

// ---------------------------------------------------------------------------
// Convex API (direct, for auth/devices/settings — no agent needed)
// ---------------------------------------------------------------------------

async function convexRequest(method, path, body) {
  const url = `${CONVEX_SITE_URL}${path}`;
  const headers = { 'Content-Type': 'application/json' };
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`;
  }
  const opts = { method, headers };
  if (body && method !== 'GET') {
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(url, opts);
  return res.json();
}

// ---------------------------------------------------------------------------
// Device connection (direct or via relay)
// ---------------------------------------------------------------------------

async function connectToDevice(device, relayServers) {
  // Try direct first (if on same LAN)
  if (device.quicHost) {
    try {
      const directUrl = `http://${device.quicHost}:${device.quicPort || 18080}`;
      const res = await fetch(`${directUrl}/health`, {
        headers: { 'Authorization': `Bearer ${authToken}` },
        signal: AbortSignal.timeout(3000),
      });
      if (res.ok) {
        agentBaseUrl = directUrl;
        return { mode: 'direct', url: directUrl };
      }
    } catch {}
  }

  // Try relay servers
  for (const relay of (relayServers || [])) {
    try {
      const relayDeviceUrl = `${relay.httpUrl}/proxy/${device.deviceId}`;
      const headers = { 'Authorization': `Bearer ${authToken}` };
      if (relay.password) headers['X-Relay-Password'] = relay.password;

      const res = await fetch(`${relayDeviceUrl}/health`, {
        headers,
        signal: AbortSignal.timeout(8000),
      });
      if (res.ok) {
        agentBaseUrl = relayDeviceUrl;
        return { mode: 'relay', url: relayDeviceUrl, relayId: relay.id };
      }
    } catch {}
  }

  throw new Error('Could not reach device (direct or relay)');
}

// ---------------------------------------------------------------------------
// IPC handlers
// ---------------------------------------------------------------------------

// Auth
ipcMain.handle('authenticate', async () => {
  try {
    const token = await startAuthFlow();
    return { ok: true, token };
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

ipcMain.handle('sign-out', () => {
  const cfg = loadConfig();
  delete cfg.auth_token;
  delete cfg.authToken;
  saveConfig(cfg);
  authToken = null;
  agentBaseUrl = null;
  return { ok: true };
});

ipcMain.handle('get-auth-state', () => {
  authToken = getToken();
  return {
    isSignedIn: !!authToken,
    token: authToken,
  };
});

ipcMain.handle('validate-token', async () => {
  authToken = getToken();
  if (!authToken) return { valid: false };
  try {
    const data = await convexRequest('GET', '/auth/validate');
    return { valid: !!data?.user, user: data?.user };
  } catch {
    return { valid: false };
  }
});

// Convex API (devices, settings, guests, etc.)
ipcMain.handle('convex-request', async (_e, method, path, body) => {
  return convexRequest(method, path, body);
});

// Device list
ipcMain.handle('list-devices', async () => {
  authToken = getToken();
  if (!authToken) return { devices: [] };
  try {
    return await convexRequest('GET', '/devices/list');
  } catch {
    return { devices: [] };
  }
});

// Connect to device
ipcMain.handle('connect-device', async (_e, device, relayServers) => {
  authToken = getToken();
  try {
    const result = await connectToDevice(device, relayServers);
    return { ok: true, ...result };
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

ipcMain.handle('disconnect-device', () => {
  agentBaseUrl = null;
  return { ok: true };
});

ipcMain.handle('get-connection-state', () => {
  return {
    connected: !!agentBaseUrl,
    baseUrl: agentBaseUrl,
  };
});

// Agent API proxy
ipcMain.handle('agent-request', async (_e, method, urlPath, body) => {
  try {
    return await agentRequest(method, urlPath, body);
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

// Config & settings
ipcMain.handle('get-config', () => loadConfig());
ipcMain.handle('save-config', (_e, cfg) => { saveConfig(cfg); return { ok: true }; });
ipcMain.handle('get-desktop-settings', () => loadDesktopSettings());
ipcMain.handle('save-desktop-settings', (_e, s) => { saveDesktopSettings(s); return { ok: true }; });

// Shell helpers
ipcMain.handle('open-external', (_e, url) => shell.openExternal(url));

// File picker
ipcMain.handle('pick-file', async (_e, options) => {
  const result = await dialog.showOpenDialog(mainWindow, {
    properties: ['openFile'],
    filters: options?.filters || [{ name: 'Images', extensions: ['png', 'jpg', 'jpeg', 'gif', 'webp'] }],
  });
  if (result.canceled || !result.filePaths.length) return null;
  const filePath = result.filePaths[0];
  const data = fs.readFileSync(filePath);
  return {
    name: path.basename(filePath),
    data: data.toString('base64'),
    mimeType: `image/${path.extname(filePath).slice(1)}`,
  };
});

// Password management
ipcMain.handle('forgot-password', async (_e, email) => {
  try {
    const res = await fetch(`${CONVEX_SITE_URL}/auth/forgot-password`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email }),
    });
    return await res.json();
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

ipcMain.handle('change-password', async (_e, currentPassword, newPassword) => {
  authToken = getToken();
  try {
    const res = await fetch(`${CONVEX_SITE_URL}/auth/change-password`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${authToken}`,
      },
      body: JSON.stringify({ currentPassword, newPassword }),
    });
    return await res.json();
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

// Guest management (via agent)
ipcMain.handle('invite-guest', async (_e, email) => agentRequest('POST', '/guests/invite', { email }));
ipcMain.handle('list-guests', async () => agentRequest('GET', '/guests'));
ipcMain.handle('revoke-guest', async (_e, email) => agentRequest('POST', '/guests/revoke', { email }));

// Dev server
ipcMain.handle('dev-server-status', async () => agentRequest('GET', '/dev/status'));
ipcMain.handle('dev-server-start', async (_e, opts) => agentRequest('POST', '/dev/start', opts));
ipcMain.handle('dev-server-stop', async () => agentRequest('POST', '/dev/stop'));
ipcMain.handle('dev-server-reload', async () => agentRequest('POST', '/dev/reload'));

// Get the base URL for loading the dev server preview
ipcMain.handle('get-preview-url', () => {
  if (!agentBaseUrl) return null;
  return `${agentBaseUrl}/dev/`;
});

// Platform info
ipcMain.handle('get-platform', () => ({
  platform: process.platform,
  arch: process.arch,
  hostname: os.hostname(),
}));
