const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('yaver', {
  // ── Auth ────────────────────────────────────────────
  authenticate: (provider) => ipcRenderer.invoke('authenticate', provider),
  emailLogin: (email, pw) => ipcRenderer.invoke('email-login', email, pw),
  emailSignup: (name, email, pw) => ipcRenderer.invoke('email-signup', name, email, pw),
  verifyTotp: (pendingToken, code) => ipcRenderer.invoke('verify-totp', pendingToken, code),
  signOut: () => ipcRenderer.invoke('sign-out'),
  getAuthState: () => ipcRenderer.invoke('get-auth-state'),
  validateToken: () => ipcRenderer.invoke('validate-token'),
  forgotPassword: (email) => ipcRenderer.invoke('forgot-password', email),
  changePassword: (cur, next) => ipcRenderer.invoke('change-password', cur, next),
  updateProfile: (data) => ipcRenderer.invoke('update-profile', data),
  deleteAccount: () => ipcRenderer.invoke('delete-account'),

  // ── Devices ─────────────────────────────────────────
  listDevices: () => ipcRenderer.invoke('list-devices'),
  connectDevice: (device, relays) => ipcRenderer.invoke('connect-device', device, relays),
  connectLocalAgent: () => ipcRenderer.invoke('connect-local-agent'),
  disconnectDevice: () => ipcRenderer.invoke('disconnect-device'),
  getConnectionState: () => ipcRenderer.invoke('get-connection-state'),
  probeLocalAgent: () => ipcRenderer.invoke('probe-local-agent'),

  // ── Agent API proxy ─────────────────────────────────
  agentRequest: (method, path, body) => ipcRenderer.invoke('agent-request', method, path, body),
  convexRequest: (method, path, body) => ipcRenderer.invoke('convex-request', method, path, body),

  // ── Tasks ───────────────────────────────────────────
  listTasks: () => ipcRenderer.invoke('agent-request', 'GET', '/tasks'),
  createTask: (data) => ipcRenderer.invoke('agent-request', 'POST', '/tasks', data),
  getTask: (id) => ipcRenderer.invoke('agent-request', 'GET', `/tasks/${id}`),
  stopTask: (id) => ipcRenderer.invoke('agent-request', 'POST', `/tasks/${id}/stop`),
  deleteTask: (id) => ipcRenderer.invoke('agent-request', 'DELETE', `/tasks/${id}`),
  continueTask: (id, data) => ipcRenderer.invoke('agent-request', 'POST', `/tasks/${id}/continue`, data),

  // ── Agent info ──────────────────────────────────────
  getAgentStatus: () => ipcRenderer.invoke('agent-request', 'GET', '/agent/status'),
  getAgentInfo: () => ipcRenderer.invoke('agent-request', 'GET', '/info'),
  getRunners: () => ipcRenderer.invoke('agent-request', 'GET', '/agent/runners'),
  switchRunner: (id) => ipcRenderer.invoke('agent-request', 'POST', '/agent/runner/switch', { runnerId: id }),
  restartRunner: () => ipcRenderer.invoke('agent-request', 'POST', '/agent/runner/restart'),
  agentShutdown: () => ipcRenderer.invoke('agent-request', 'POST', '/agent/shutdown'),
  agentClean: (days) => ipcRenderer.invoke('agent-request', 'POST', '/agent/clean', { days }),

  // ── Projects ────────────────────────────────────────
  listProjects: () => ipcRenderer.invoke('agent-request', 'GET', '/projects'),
  getProjectActions: (q) => ipcRenderer.invoke('agent-request', 'GET', `/projects/actions?query=${encodeURIComponent(q)}`),

  // ── Dev server ──────────────────────────────────────
  devServerStatus: () => ipcRenderer.invoke('dev-server-status'),
  devServerStart: (opts) => ipcRenderer.invoke('dev-server-start', opts),
  devServerStop: () => ipcRenderer.invoke('dev-server-stop'),
  devServerReload: () => ipcRenderer.invoke('dev-server-reload'),
  getPreviewUrl: () => ipcRenderer.invoke('get-preview-url'),

  // ── Builds ──────────────────────────────────────────
  listBuilds: () => ipcRenderer.invoke('agent-request', 'GET', '/builds'),
  startBuild: (data) => ipcRenderer.invoke('agent-request', 'POST', '/builds', data),
  getBuild: (id) => ipcRenderer.invoke('agent-request', 'GET', `/builds/${id}`),

  // ── Todos ───────────────────────────────────────────
  listTodos: () => ipcRenderer.invoke('list-todos'),
  addTodo: (desc) => ipcRenderer.invoke('add-todo', desc),
  deleteTodo: (id) => ipcRenderer.invoke('delete-todo', id),
  todoCount: () => ipcRenderer.invoke('todo-count'),

  // ── Guests ──────────────────────────────────────────
  inviteGuest: (email) => ipcRenderer.invoke('invite-guest', email),
  listGuests: () => ipcRenderer.invoke('list-guests'),
  revokeGuest: (email) => ipcRenderer.invoke('revoke-guest', email),
  guestConfig: (email) => ipcRenderer.invoke('guest-config', email),
  updateGuestConfig: (data) => ipcRenderer.invoke('update-guest-config', data),
  guestUsage: (date) => ipcRenderer.invoke('guest-usage', date),

  // ── Git ─────────────────────────────────────────────
  gitPull: (wd) => ipcRenderer.invoke('agent-request', 'POST', `/git/pull?workDir=${encodeURIComponent(wd)}`),
  gitStatus: (wd) => ipcRenderer.invoke('agent-request', 'GET', `/git/status?workDir=${encodeURIComponent(wd)}`),

  // ── Health monitoring ───────────────────────────────
  listHealthTargets: () => ipcRenderer.invoke('list-health-targets'),
  addHealthTarget: (data) => ipcRenderer.invoke('add-health-target', data),
  deleteHealthTarget: (id) => ipcRenderer.invoke('delete-health-target', id),
  checkHealthTarget: (id) => ipcRenderer.invoke('check-health-target', id),

  // ── Quality gates ───────────────────────────────────
  listQualityGates: () => ipcRenderer.invoke('list-quality-gates'),
  runQualityGate: (id) => ipcRenderer.invoke('run-quality-gate', id),
  runAllQualityGates: () => ipcRenderer.invoke('run-all-quality-gates'),

  // ── Sandbox ─────────────────────────────────────────
  sandboxStatus: () => ipcRenderer.invoke('sandbox-status'),
  sandboxConfig: (cfg) => ipcRenderer.invoke('sandbox-config', cfg),

  // ── Config & settings ───────────────────────────────
  getConfig: () => ipcRenderer.invoke('get-config'),
  saveConfig: (cfg) => ipcRenderer.invoke('save-config', cfg),
  getDesktopSettings: () => ipcRenderer.invoke('get-desktop-settings'),
  saveDesktopSettings: (s) => ipcRenderer.invoke('save-desktop-settings', s),

  // ── Helpers ─────────────────────────────────────────
  openExternal: (url) => ipcRenderer.invoke('open-external', url),
  pickFile: (opts) => ipcRenderer.invoke('pick-file', opts),
  getPlatform: () => ipcRenderer.invoke('get-platform'),

  // ── Events ──────────────────────────────────────────
  onAuthStateChanged: (cb) => ipcRenderer.on('auth-state-changed', (_e, data) => cb(data)),
});
