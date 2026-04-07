const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('yaver', {
  // Auth
  authenticate: () => ipcRenderer.invoke('authenticate'),
  signOut: () => ipcRenderer.invoke('sign-out'),
  getAuthState: () => ipcRenderer.invoke('get-auth-state'),
  validateToken: () => ipcRenderer.invoke('validate-token'),
  forgotPassword: (email) => ipcRenderer.invoke('forgot-password', email),
  changePassword: (currentPw, newPw) => ipcRenderer.invoke('change-password', currentPw, newPw),

  // Devices
  listDevices: () => ipcRenderer.invoke('list-devices'),
  connectDevice: (device, relays) => ipcRenderer.invoke('connect-device', device, relays),
  disconnectDevice: () => ipcRenderer.invoke('disconnect-device'),
  getConnectionState: () => ipcRenderer.invoke('get-connection-state'),

  // Agent API proxy (all go through connected agent)
  agentRequest: (method, path, body) => ipcRenderer.invoke('agent-request', method, path, body),

  // Convex API (direct, no agent needed)
  convexRequest: (method, path, body) => ipcRenderer.invoke('convex-request', method, path, body),

  // Tasks
  listTasks: () => ipcRenderer.invoke('agent-request', 'GET', '/tasks'),
  createTask: (data) => ipcRenderer.invoke('agent-request', 'POST', '/tasks', data),
  getTask: (id) => ipcRenderer.invoke('agent-request', 'GET', `/tasks/${id}`),
  stopTask: (id) => ipcRenderer.invoke('agent-request', 'POST', `/tasks/${id}/stop`),
  deleteTask: (id) => ipcRenderer.invoke('agent-request', 'DELETE', `/tasks/${id}`),
  continueTask: (id, data) => ipcRenderer.invoke('agent-request', 'POST', `/tasks/${id}/continue`, data),

  // Agent info
  getAgentStatus: () => ipcRenderer.invoke('agent-request', 'GET', '/agent/status'),
  getAgentInfo: () => ipcRenderer.invoke('agent-request', 'GET', '/info'),
  getRunners: () => ipcRenderer.invoke('agent-request', 'GET', '/agent/runners'),

  // Projects
  listProjects: () => ipcRenderer.invoke('agent-request', 'GET', '/projects'),
  getProjectActions: (query) => ipcRenderer.invoke('agent-request', 'GET', `/projects/actions?query=${encodeURIComponent(query)}`),

  // Dev server
  devServerStatus: () => ipcRenderer.invoke('dev-server-status'),
  devServerStart: (opts) => ipcRenderer.invoke('dev-server-start', opts),
  devServerStop: () => ipcRenderer.invoke('dev-server-stop'),
  devServerReload: () => ipcRenderer.invoke('dev-server-reload'),
  getPreviewUrl: () => ipcRenderer.invoke('get-preview-url'),

  // Guests
  inviteGuest: (email) => ipcRenderer.invoke('invite-guest', email),
  listGuests: () => ipcRenderer.invoke('list-guests'),
  revokeGuest: (email) => ipcRenderer.invoke('revoke-guest', email),

  // Git
  gitPull: (workDir) => ipcRenderer.invoke('agent-request', 'POST', `/git/pull?workDir=${encodeURIComponent(workDir)}`),
  gitStatus: (workDir) => ipcRenderer.invoke('agent-request', 'GET', `/git/status?workDir=${encodeURIComponent(workDir)}`),

  // Config & settings
  getConfig: () => ipcRenderer.invoke('get-config'),
  saveConfig: (cfg) => ipcRenderer.invoke('save-config', cfg),
  getDesktopSettings: () => ipcRenderer.invoke('get-desktop-settings'),
  saveDesktopSettings: (s) => ipcRenderer.invoke('save-desktop-settings', s),

  // Helpers
  openExternal: (url) => ipcRenderer.invoke('open-external', url),
  pickFile: (opts) => ipcRenderer.invoke('pick-file', opts),
  getPlatform: () => ipcRenderer.invoke('get-platform'),

  // Event listeners
  onAuthStateChanged: (cb) => ipcRenderer.on('auth-state-changed', (_e, data) => cb(data)),
});
