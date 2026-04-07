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

// Set app name before anything else
app.setName('Yaver.io');

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
  fs.mkdirSync(CONFIG_DIR, { recursive: true, mode: 0o700 });
  fs.writeFileSync(CONFIG_FILE, JSON.stringify(cfg, null, 2), { mode: 0o600 });
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

// Desktop app stores its own token separately from CLI token.
// CLI token lives in ~/.yaver/config.json (auth_token).
// Desktop token lives in ~/.yaver/desktop-settings.json (authToken).
// For local agent (IPC), we use the CLI token.
// For remote agents, we use the desktop token.

function getDesktopToken() {
  const s = loadDesktopSettings();
  return s.authToken || '';
}

function setDesktopToken(token) {
  const s = loadDesktopSettings();
  s.authToken = token;
  saveDesktopSettings(s);
}

function getCliToken() {
  const cfg = loadConfig();
  return cfg.auth_token || cfg.authToken || '';
}

function hasToken() {
  return !!(getDesktopToken() || getCliToken());
}

function getToken() {
  // Prefer desktop token, fall back to CLI token
  return getDesktopToken() || getCliToken();
}

// ---------------------------------------------------------------------------
// Window management
// ---------------------------------------------------------------------------

const APP_ICON = nativeImage.createFromPath(path.join(__dirname, '..', '..', 'assets', 'icon.png'));

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1400,
    height: 900,
    minWidth: 900,
    minHeight: 600,
    resizable: true,
    title: 'Yaver.io',
    titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'default',
    backgroundColor: '#0a0a0a',
    icon: APP_ICON,
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
  tray.setToolTip('Yaver.io');

  const menu = Menu.buildFromTemplate([
    { label: 'Open Yaver.io', click: () => { mainWindow ? mainWindow.show() : createWindow(); } },
    { type: 'separator' },
    { label: 'Quit Yaver.io', click: () => app.quit() },
  ]);
  tray.setContextMenu(menu);
}

app.whenReady().then(() => {
  // Set dock icon on macOS
  if (process.platform === 'darwin' && app.dock) {
    app.dock.setIcon(APP_ICON);
  }

  // Set application menu with proper name
  const menuTemplate = [
    ...(process.platform === 'darwin' ? [{
      label: 'Yaver.io',
      submenu: [
        { role: 'about', label: 'About Yaver.io' },
        { type: 'separator' },
        { role: 'services' },
        { type: 'separator' },
        { role: 'hide', label: 'Hide Yaver.io' },
        { role: 'hideOthers' },
        { role: 'unhide' },
        { type: 'separator' },
        { role: 'quit', label: 'Quit Yaver.io' },
      ],
    }] : []),
    {
      label: 'Edit',
      submenu: [
        { role: 'undo' }, { role: 'redo' }, { type: 'separator' },
        { role: 'cut' }, { role: 'copy' }, { role: 'paste' },
        { role: 'selectAll' },
      ],
    },
    {
      label: 'View',
      submenu: [
        { role: 'reload' }, { role: 'forceReload' },
        { role: 'toggleDevTools' },
        { type: 'separator' },
        { role: 'resetZoom' }, { role: 'zoomIn' }, { role: 'zoomOut' },
        { type: 'separator' },
        { role: 'togglefullscreen' },
      ],
    },
    {
      label: 'Window',
      submenu: [
        { role: 'minimize' }, { role: 'zoom' },
        ...(process.platform === 'darwin' ? [
          { type: 'separator' }, { role: 'front' },
        ] : [{ role: 'close' }]),
      ],
    },
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(menuTemplate));

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
    if (authCallbackServer) {
      try { authCallbackServer.close(); } catch {}
    }

    authCallbackServer = http.createServer((req, res) => {
      const url = new URL(req.url, 'http://127.0.0.1:19836');
      const token = url.searchParams.get('token');
      if (token) {
        res.writeHead(200, { 'Content-Type': 'text/html' });
        res.end('<html><body style="font-family:system-ui;text-align:center;padding:60px;background:#0a0a0a;color:#fff;"><h2>Signed in!</h2><p>You can close this tab and return to Yaver.io.</p></body></html>');
        authCallbackServer.close();
        authCallbackServer = null;

        // Store in desktop settings (don't overwrite CLI's config.json)
        setDesktopToken(token);
        authToken = token;

        if (mainWindow) mainWindow.webContents.send('auth-state-changed', { signedIn: true });
        resolve(token);
      } else {
        res.writeHead(400);
        res.end('Missing token');
      }
    });

    authCallbackServer.listen(19836, '127.0.0.1', () => {
      // If provider specified, go directly to that OAuth flow; otherwise show auth page
      const authUrl = provider
        ? `https://yaver.io/api/auth/oauth/${provider}?client=desktop`
        : 'https://yaver.io/auth?client=desktop';
      shell.openExternal(authUrl);
    });

    authCallbackServer.on('error', reject);

    setTimeout(() => {
      if (authCallbackServer) { authCallbackServer.close(); authCallbackServer = null; reject(new Error('Auth timeout')); }
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

  // Ensure we have a token
  if (!authToken) authToken = getToken();

  const url = `${agentBaseUrl}${urlPath}`;
  const headers = { 'Content-Type': 'application/json' };
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`;
  }

  const opts = { method, headers, signal: AbortSignal.timeout(30000) };
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
ipcMain.handle('authenticate', async (_e, provider) => {
  try {
    const token = await startAuthFlow(provider || null);
    return { ok: true, token };
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

ipcMain.handle('sign-out', () => {
  // Only clear desktop token, don't touch CLI's config
  setDesktopToken('');
  authToken = null;
  agentBaseUrl = null;
  return { ok: true };
});

ipcMain.handle('get-auth-state', () => {
  // Check desktop token first, then CLI token
  authToken = getDesktopToken() || getCliToken();
  return {
    isSignedIn: !!authToken,
    token: authToken,
  };
});

ipcMain.handle('validate-token', async () => {
  // Try desktop token first, then CLI token
  const deskTok = getDesktopToken();
  const cliTok = getCliToken();

  // Try desktop token
  if (deskTok) {
    authToken = deskTok;
    try {
      const data = await convexRequest('GET', '/auth/validate');
      if (data?.user) return { valid: true, user: data.user };
    } catch {}
  }

  // Try CLI token (user may not have done desktop OAuth yet but has CLI auth)
  if (cliTok && cliTok !== deskTok) {
    authToken = cliTok;
    try {
      const data = await convexRequest('GET', '/auth/validate');
      if (data?.user) {
        // CLI token works — save it as desktop token too for convenience
        setDesktopToken(cliTok);
        return { valid: true, user: data.user };
      }
    } catch {}
  }

  return { valid: false };
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

// ---------------------------------------------------------------------------
// Local agent detection (IPC — same machine)
// ---------------------------------------------------------------------------

async function probeLocalAgent() {
  // Try CLI token first (that's what the local agent accepts), then desktop token
  const cliTok = getCliToken();
  const deskTok = getDesktopToken();
  const tokens = [];
  if (cliTok) tokens.push(cliTok);
  if (deskTok && deskTok !== cliTok) tokens.push(deskTok);
  if (authToken && !tokens.includes(authToken)) tokens.push(authToken);

  for (const tok of tokens) {
    try {
      const res = await fetch('http://localhost:18080/health', {
        headers: { 'Authorization': `Bearer ${tok}` },
        signal: AbortSignal.timeout(2000),
      });
      if (res.ok) {
        const data = await res.json();
        // Verify the token actually works for API calls
        const testRes = await fetch('http://localhost:18080/info', {
          headers: { 'Authorization': `Bearer ${tok}` },
          signal: AbortSignal.timeout(2000),
        });
        const authWorks = testRes.ok;
        return {
          found: true,
          version: data.version,
          hostname: data.hostname,
          authExpired: !!data.authExpired,
          authWorks,
          workingToken: authWorks ? tok : null,
        };
      }
    } catch {}
  }

  // Try without auth (health is public)
  try {
    const res = await fetch('http://localhost:18080/health', { signal: AbortSignal.timeout(2000) });
    if (res.ok) {
      const data = await res.json();
      return { found: true, version: data.version, hostname: data.hostname, authExpired: !!data.authExpired, authWorks: false, workingToken: null };
    }
  } catch {}

  return { found: false };
}

ipcMain.handle('probe-local-agent', async () => probeLocalAgent());

ipcMain.handle('connect-local-agent', async () => {
  const probe = await probeLocalAgent();
  if (!probe.found) return { ok: false, error: 'Local agent not found' };
  if (!probe.authWorks) return { ok: false, error: 'Local agent found but auth failed — run "yaver auth" first' };

  agentBaseUrl = 'http://localhost:18080';

  // Use the working token (CLI token or desktop token)
  // Security: token is read from ~/.yaver/config.json which is only readable by the current user (0600)
  // The agent also validates the token against Convex — no one else's token works
  if (probe.workingToken) {
    authToken = probe.workingToken;
  } else {
    authToken = getToken();
  }

  // Verify token ownership: the agent checks that the token belongs to the same Convex user
  // that registered this device. Different user's tokens get rejected with 403.
  try {
    const infoRes = await fetch('http://localhost:18080/info', {
      headers: { 'Authorization': `Bearer ${authToken}` },
      signal: AbortSignal.timeout(3000),
    });
    if (!infoRes.ok) return { ok: false, error: 'Auth rejected by local agent' };
  } catch (err) {
    return { ok: false, error: 'Could not verify auth with local agent' };
  }

  return { ok: true, mode: 'local' };
});

// ---------------------------------------------------------------------------
// Email/password auth (inline — no browser redirect)
// ---------------------------------------------------------------------------

ipcMain.handle('email-login', async (_e, email, password) => {
  try {
    const res = await fetch(`${CONVEX_SITE_URL}/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });
    const data = await res.json();
    if (!res.ok) return { ok: false, error: data.error || 'Login failed' };

    if (data.requires2fa) {
      return { ok: true, requires2fa: true, pendingToken: data.pendingToken };
    }

    setDesktopToken(data.token);
    authToken = data.token;
    return { ok: true, token: data.token };
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

ipcMain.handle('email-signup', async (_e, fullName, email, password) => {
  try {
    const res = await fetch(`${CONVEX_SITE_URL}/auth/signup`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ fullName, email, password }),
    });
    const data = await res.json();
    if (!res.ok) return { ok: false, error: data.error || 'Signup failed' };

    setDesktopToken(data.token);
    authToken = data.token;
    return { ok: true, token: data.token };
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

ipcMain.handle('verify-totp', async (_e, pendingToken, code) => {
  try {
    const res = await fetch(`${CONVEX_SITE_URL}/auth/verify-totp`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pendingToken, code }),
    });
    const data = await res.json();
    if (!res.ok) return { ok: false, error: data.error || 'Verification failed' };

    setDesktopToken(data.token);
    authToken = data.token;
    return { ok: true, token: data.token };
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

// ---------------------------------------------------------------------------
// Profile management
// ---------------------------------------------------------------------------

ipcMain.handle('update-profile', async (_e, data) => {
  authToken = getToken();
  try {
    const res = await fetch(`${CONVEX_SITE_URL}/auth/update-profile`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${authToken}` },
      body: JSON.stringify(data),
    });
    return await res.json();
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

ipcMain.handle('delete-account', async () => {
  authToken = getToken();
  try {
    const res = await fetch(`${CONVEX_SITE_URL}/auth/delete-account`, {
      method: 'POST',
      headers: { 'Authorization': `Bearer ${authToken}` },
    });
    if (res.ok) {
      const cfg = loadConfig();
      delete cfg.auth_token;
      delete cfg.authToken;
      saveConfig(cfg);
      authToken = null;
      agentBaseUrl = null;
    }
    return await res.json();
  } catch (err) {
    return { ok: false, error: err.message };
  }
});

// ---------------------------------------------------------------------------
// Todo management (via agent)
// ---------------------------------------------------------------------------

ipcMain.handle('list-todos', async () => agentRequest('GET', '/todolist'));
ipcMain.handle('add-todo', async (_e, desc) => agentRequest('POST', '/todolist', { description: desc, source: 'desktop' }));
ipcMain.handle('delete-todo', async (_e, id) => agentRequest('DELETE', `/todolist/${id}`));
ipcMain.handle('todo-count', async () => agentRequest('GET', '/todolist/count'));

// Health monitoring (via agent)
ipcMain.handle('list-health-targets', async () => agentRequest('GET', '/healthmon'));
ipcMain.handle('add-health-target', async (_e, data) => agentRequest('POST', '/healthmon', data));
ipcMain.handle('delete-health-target', async (_e, id) => agentRequest('DELETE', `/healthmon/${id}`));
ipcMain.handle('check-health-target', async (_e, id) => agentRequest('POST', `/healthmon/${id}/check`));

// Quality gates (via agent)
ipcMain.handle('list-quality-gates', async () => agentRequest('GET', '/quality'));
ipcMain.handle('run-quality-gate', async (_e, id) => agentRequest('POST', `/quality/${id}/run`));
ipcMain.handle('run-all-quality-gates', async () => agentRequest('POST', '/quality/run-all'));

// Sandbox (via agent)
ipcMain.handle('sandbox-status', async () => agentRequest('GET', '/sandbox/status'));
ipcMain.handle('sandbox-config', async (_e, cfg) => agentRequest('POST', '/sandbox/config', cfg));

// Guest config (via agent)
ipcMain.handle('guest-config', async (_e, email) => {
  const path = email ? `/guests/config?email=${encodeURIComponent(email)}` : '/guests/config';
  return agentRequest('GET', path);
});
ipcMain.handle('update-guest-config', async (_e, data) => agentRequest('POST', '/guests/config', data));
ipcMain.handle('guest-usage', async (_e, date) => {
  const path = date ? `/guests/usage?date=${date}` : '/guests/usage';
  return agentRequest('GET', path);
});

// Keyboard shortcuts
ipcMain.handle('register-shortcuts', () => {
  // Handled in renderer via DOM events
  return { ok: true };
});
