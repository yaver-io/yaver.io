// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

let currentTaskId = null;
let currentExecId = null;
let taskPollInterval = null;
let dashPollInterval = null;
let execPollInterval = null;
let pendingImages = []; // for create-task modal
let cachedConfig = {};

// ---------------------------------------------------------------------------
// Survey state
// ---------------------------------------------------------------------------

let surveyPage = 0;
let surveyData = {
  fullName: '',
  identity: null,
  selectedRunner: 'claude',
  customCommand: '',
  speechProvider: 'on-device',
  speechApiKey: '',
  verbosity: 10,
  relayUrl: '',
  relayPassword: '',
  relayLabel: '',
  relayOptOut: false,
  languages: [],
  experience: null,
  useCase: null,
  companySize: null,
};
let surveyRunners = [];
let surveyIsSubmitting = false;

const SURVEY_IDENTITIES = [
  { id: 'developer', label: 'Developer' },
  { id: 'business', label: 'Business Owner' },
  { id: 'student', label: 'Student / Academic' },
  { id: 'other', label: 'Other' },
];

const SURVEY_LANGUAGES = [
  'JavaScript/TypeScript', 'Python', 'Go', 'Rust', 'Java',
  'C/C++', 'Ruby', 'PHP', 'Swift', 'Kotlin', 'C#', 'Other',
];

const SURVEY_EXPERIENCE_LEVELS = ['Junior', 'Mid-Level', 'Senior', 'Staff/Lead'];

const SURVEY_USE_CASES = [
  'Work / Business', 'Hobby Projects', 'Academic / Research',
  'Open Source', 'Freelance / Consulting', 'Other',
];

const SURVEY_COMPANY_SIZES = ['Solo', '2-10', '11-50', '51-200', '201-1000', '1000+'];

const SURVEY_SPEECH_PROVIDERS = [
  { id: 'on-device', name: 'On-Device (Free)', description: 'Runs locally using Whisper. No API key needed.', requiresKey: false },
  { id: 'openai', name: 'OpenAI', description: 'GPT-4o Mini Transcribe. Fast, accurate. $0.003/min.', requiresKey: true, keyPlaceholder: 'sk-...', keyHint: 'Get your key at platform.openai.com/api-keys' },
  { id: 'deepgram', name: 'Deepgram', description: 'Nova-2. Real-time capable, top accuracy. $0.0043/min.', requiresKey: true, keyPlaceholder: 'Your Deepgram API key', keyHint: 'Get your key at console.deepgram.com' },
  { id: 'assemblyai', name: 'AssemblyAI', description: 'Universal-2. Cheapest async option. $0.002/min.', requiresKey: true, keyPlaceholder: 'Your AssemblyAI API key', keyHint: 'Get your key at assemblyai.com/dashboard' },
];

// ---------------------------------------------------------------------------
// View management (auth flow views)
// ---------------------------------------------------------------------------

function showView(id) {
  document.querySelectorAll('#auth-flow .view').forEach((el) => el.classList.remove('active'));
  const target = document.getElementById(id);
  if (target) target.classList.add('active');
}

function showMainApp() {
  document.getElementById('auth-flow').style.display = 'none';
  document.getElementById('main-app').style.display = 'flex';
  startDashboardPolling();
}

function hideMainApp() {
  document.getElementById('auth-flow').style.display = 'flex';
  document.getElementById('main-app').style.display = 'none';
  stopAllPolling();
}

// ---------------------------------------------------------------------------
// Panel switching (sidebar navigation)
// ---------------------------------------------------------------------------

function switchPanel(navEl) {
  const panelId = navEl.getAttribute('data-panel');
  // Update nav
  document.querySelectorAll('.nav-item').forEach((el) => el.classList.remove('active'));
  navEl.classList.add('active');
  // Update panels
  document.querySelectorAll('.panel').forEach((el) => el.classList.remove('active'));
  const panel = document.getElementById(panelId);
  if (panel) panel.classList.add('active');

  // Start/stop relevant polling
  if (panelId === 'panel-dashboard') {
    startDashboardPolling();
  }
  if (panelId === 'panel-tasks') {
    refreshTaskList();
  }
}

// ---------------------------------------------------------------------------
// App initialization
// ---------------------------------------------------------------------------

async function init() {
  showView('view-loading');

  const state = await window.yaver.getAppState();

  if (!state.hasToken) {
    showView('view-signin');
    return;
  }

  if (!state.tokenValid) {
    showView('view-reauth');
    return;
  }

  if (!state.agentInstalled) {
    showView('view-setup-prereqs');
    runPrerequisites();
    return;
  }

  // Check if survey is needed
  const userInfo = await window.yaver.getUserInfo();
  if (userInfo && userInfo.signedIn && userInfo.user && !userInfo.user.surveyCompleted) {
    if (userInfo.user.name) surveyData.fullName = userInfo.user.name;
    showSurvey();
    return;
  }

  // Load settings
  const settings = await window.yaver.getSettings();
  if (settings.agentBaseUrl) {
    document.getElementById('setting-agent-url').value = settings.agentBaseUrl;
  }

  // Show main app
  showMainApp();
  loadConfig();
  loadSettingsUI();
  updateDashboard();
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

async function updateDashboard() {
  const state = await window.yaver.getAppState();
  const agentVal = document.getElementById('dash-agent-val');

  // Try to get agent info from the HTTP API
  const info = await window.yaver.getAgentInfo();
  const status = await window.yaver.getAgentStatus();
  const taskListResp = await window.yaver.listTasks();

  if (info && info.ok) {
    agentVal.innerHTML = '<span class="status-dot green"></span><span class="text-green">Running</span>';
    document.getElementById('dash-connection-val').innerHTML = '<span class="text-green">Connected</span>';
    document.getElementById('dash-hostname').textContent = info.hostname || '--';
    document.getElementById('dash-version').textContent = info.version || '--';
    document.getElementById('dash-workdir').textContent = info.workDir || '--';
    document.getElementById('dash-platform').textContent = `${friendlyPlatform(state.platform)} (${state.arch})`;
  } else if (state.agentRunning) {
    agentVal.innerHTML = '<span class="status-dot yellow"></span><span class="text-yellow">Starting...</span>';
    document.getElementById('dash-connection-val').innerHTML = '<span class="text-yellow">Connecting...</span>';
    document.getElementById('dash-platform').textContent = `${friendlyPlatform(state.platform)} (${state.arch})`;
  } else {
    agentVal.innerHTML = '<span class="status-dot red"></span><span class="text-red">Stopped</span>';
    document.getElementById('dash-connection-val').innerHTML = '<span class="text-red">Disconnected</span>';
    document.getElementById('dash-platform').textContent = `${friendlyPlatform(state.platform)} (${state.arch})`;
  }

  // Runner info
  if (status && status.ok && status.status) {
    const s = status.status;
    document.getElementById('dash-runner-val').textContent = s.runner || '--';
  }

  // Active tasks count
  if (taskListResp && taskListResp.ok && taskListResp.tasks) {
    const active = taskListResp.tasks.filter((t) => t.status === 'running' || t.status === 'queued').length;
    document.getElementById('dash-tasks-val').textContent = `${active} / ${taskListResp.tasks.length}`;
  }
}

function startDashboardPolling() {
  if (dashPollInterval) clearInterval(dashPollInterval);
  dashPollInterval = setInterval(() => {
    const activePanel = document.querySelector('.panel.active');
    if (activePanel && activePanel.id === 'panel-dashboard') {
      updateDashboard();
    }
  }, 10000);
}

// ---------------------------------------------------------------------------
// Tasks
// ---------------------------------------------------------------------------

async function refreshTaskList() {
  const resp = await window.yaver.listTasks();
  const container = document.getElementById('task-list-items');
  const emptyEl = document.getElementById('task-list-empty');

  if (!resp || !resp.ok || !resp.tasks || resp.tasks.length === 0) {
    container.innerHTML = '';
    container.appendChild(createEmptyState('&#9998;', 'No tasks yet'));
    return;
  }

  // Sort tasks: running first, then by creation time (newest first)
  const tasks = resp.tasks.sort((a, b) => {
    const statusOrder = { running: 0, queued: 1, completed: 2, stopped: 3, failed: 4 };
    const sa = statusOrder[a.status] !== undefined ? statusOrder[a.status] : 5;
    const sb = statusOrder[b.status] !== undefined ? statusOrder[b.status] : 5;
    if (sa !== sb) return sa - sb;
    return (b.createdAt || '').localeCompare(a.createdAt || '');
  });

  container.innerHTML = '';
  tasks.forEach((task) => {
    const div = document.createElement('div');
    div.className = 'task-item' + (task.id === currentTaskId ? ' active' : '');
    div.onclick = () => selectTask(task.id);
    div.innerHTML = `
      <div class="task-title">${escapeHtml(task.title)}</div>
      <div class="task-meta">
        <span class="task-status-badge ${task.status}">${task.status}</span>
        <span>${task.runnerId || ''}</span>
        <span>${formatTime(task.createdAt)}</span>
      </div>
    `;
    container.appendChild(div);
  });
}

async function selectTask(taskId) {
  currentTaskId = taskId;

  // Highlight in list
  document.querySelectorAll('.task-item').forEach((el) => el.classList.remove('active'));
  const items = document.querySelectorAll('.task-item');
  items.forEach((el) => {
    if (el.onclick && el.querySelector('.task-title')) {
      // re-add active class based on click handler
    }
  });

  // Fetch task detail
  const resp = await window.yaver.getTask(taskId);
  if (!resp || !resp.ok || !resp.task) {
    document.getElementById('task-detail-content').innerHTML = '<div class="empty-state" style="height:100%"><div>Task not found</div></div>';
    return;
  }

  const task = resp.task;
  renderTaskDetail(task);
  startTaskPolling(taskId);
  refreshTaskList(); // update active highlight
}

function buildChatMessages(task) {
  const messages = [];

  if (task.turns && task.turns.length > 0) {
    for (const turn of task.turns) {
      messages.push({ role: turn.role, content: turn.content });
    }
  } else {
    messages.push({ role: 'user', content: task.title });
    if (task.resultText) {
      messages.push({ role: 'assistant', content: task.resultText });
    }
  }

  // If running and we have streaming output, replace/add last assistant message
  if (task.status === 'running' && task.output && task.output.trim()) {
    const lastIdx = messages.length - 1;
    if (lastIdx >= 0 && messages[lastIdx].role === 'assistant') {
      messages[lastIdx].content = task.output;
    } else {
      messages.push({ role: 'assistant', content: task.output });
    }
  }

  return messages;
}

function renderTaskDetail(task) {
  const container = document.getElementById('task-detail-content');
  const isActive = task.status === 'running' || task.status === 'queued';

  const messages = buildChatMessages(task);
  let bubblesHtml = messages.map((msg) => {
    if (msg.role === 'user') {
      return `<div class="chat-bubble user">${escapeHtml(msg.content)}</div>`;
    }
    return `<div class="chat-bubble assistant">${renderMarkdown(msg.content)}</div>`;
  }).join('');

  // Typing indicator for running tasks with no assistant output yet
  if (isActive && (messages.length === 0 || messages[messages.length - 1].role === 'user')) {
    bubblesHtml += `<div class="chat-typing"><div class="chat-typing-dot"></div><div class="chat-typing-dot"></div><div class="chat-typing-dot"></div></div>`;
  }

  container.innerHTML = `
    <div class="task-detail-header">
      <div style="display:flex; align-items:center; justify-content:space-between">
        <h3 style="flex:1; margin-right:12px">${escapeHtml(task.title)}</h3>
        <div class="btn-row">
          ${isActive ? '<button class="btn btn-danger btn-sm" onclick="stopCurrentTask()">Stop</button>' : ''}
          <button class="btn btn-outline btn-sm" onclick="deleteCurrentTask()">Delete</button>
        </div>
      </div>
      <div style="margin-top:6px; display:flex; gap:12px; font-size:12px; color:var(--text-dim)">
        <span class="task-status-badge ${task.status}">${task.status}</span>
        ${task.runnerId ? `<span>Runner: ${task.runnerId}</span>` : ''}
        ${task.costUSD ? `<span>Cost: $${task.costUSD.toFixed(4)}</span>` : ''}
        ${task.turns && task.turns.length ? `<span>Turns: ${task.turns.length}</span>` : ''}
        <span>${formatTime(task.createdAt)}</span>
      </div>
    </div>
    <div class="chat-messages" id="chat-messages">${bubblesHtml}</div>
    <div class="task-continue-bar">
      <input type="text" id="task-continue-input" placeholder="${isActive ? 'Waiting for response...' : 'Send a follow-up...'}" ${isActive ? 'disabled' : ''} onkeydown="if(event.key==='Enter')continueCurrentTask()">
      <button class="btn btn-primary btn-sm" onclick="continueCurrentTask()" ${isActive ? 'disabled' : ''}>Send</button>
    </div>
  `;
}

function startTaskPolling(taskId) {
  if (taskPollInterval) clearInterval(taskPollInterval);
  taskPollInterval = setInterval(async () => {
    if (currentTaskId !== taskId) return;
    const resp = await window.yaver.getTask(taskId);
    if (resp && resp.ok && resp.task) {
      renderTaskDetail(resp.task);
      // Auto-scroll output
      const chatArea = document.getElementById('chat-messages');
      if (chatArea) chatArea.scrollTop = chatArea.scrollHeight;

      // Stop polling if task is done
      if (resp.task.status !== 'running' && resp.task.status !== 'queued') {
        clearInterval(taskPollInterval);
        taskPollInterval = null;
        refreshTaskList();
      }
    }
  }, 2000);
}

async function stopCurrentTask() {
  if (!currentTaskId) return;
  await window.yaver.stopTask(currentTaskId);
  setTimeout(() => selectTask(currentTaskId), 500);
}

async function deleteCurrentTask() {
  if (!currentTaskId) return;
  await window.yaver.deleteTask(currentTaskId);
  currentTaskId = null;
  if (taskPollInterval) { clearInterval(taskPollInterval); taskPollInterval = null; }
  document.getElementById('task-detail-content').innerHTML = '<div class="empty-state" style="height:100%"><div class="empty-icon">&#8592;</div><div>Select a task to view details</div></div>';
  refreshTaskList();
}

async function continueCurrentTask() {
  if (!currentTaskId) return;
  const input = document.getElementById('task-continue-input');
  if (!input || !input.value.trim()) return;
  const text = input.value.trim();
  input.value = '';
  await window.yaver.continueTask(currentTaskId, { input: text });
  startTaskPolling(currentTaskId);
  setTimeout(() => selectTask(currentTaskId), 500);
}

// ---- Create Task Modal ----

function showCreateTaskModal() {
  document.getElementById('create-task-modal').classList.add('active');
  pendingImages = [];
  document.getElementById('new-task-images').innerHTML = '';
  document.getElementById('new-task-title').value = '';
  document.getElementById('new-task-description').value = '';
  document.getElementById('new-task-model').value = '';
  document.getElementById('new-task-runner').value = '';
  document.getElementById('new-task-title').focus();
}

function hideCreateTaskModal() {
  document.getElementById('create-task-modal').classList.remove('active');
  pendingImages = [];
}

async function attachImage() {
  const file = await window.yaver.pickFile();
  if (!file) return;
  pendingImages.push({ base64: file.base64, mimeType: file.mimeType });

  const container = document.getElementById('new-task-images');
  const idx = pendingImages.length - 1;
  const thumb = document.createElement('div');
  thumb.className = 'image-thumb';
  thumb.innerHTML = `
    <img src="data:${file.mimeType};base64,${file.base64.substring(0, 100)}..." alt="">
    <button class="remove-img" onclick="removeImage(${idx}, this)">x</button>
  `;
  // Set proper src
  thumb.querySelector('img').src = `data:${file.mimeType};base64,${file.base64}`;
  container.appendChild(thumb);
}

function removeImage(idx, btn) {
  pendingImages[idx] = null;
  btn.parentElement.remove();
}

async function createNewTask() {
  const title = document.getElementById('new-task-title').value.trim();
  if (!title) return;

  const data = {
    title,
    description: document.getElementById('new-task-description').value.trim(),
    model: document.getElementById('new-task-model').value.trim(),
    runner: document.getElementById('new-task-runner').value,
    source: 'desktop-app',
    images: pendingImages.filter(Boolean),
  };

  hideCreateTaskModal();

  const resp = await window.yaver.createTask(data);
  if (resp && resp.ok && resp.taskId) {
    await refreshTaskList();
    selectTask(resp.taskId);
  }
}

// ---------------------------------------------------------------------------
// Terminal
// ---------------------------------------------------------------------------

async function runTerminalCommand() {
  const input = document.getElementById('terminal-command-input');
  const cmd = input.value.trim();
  if (!cmd) return;
  input.value = '';

  const output = document.getElementById('terminal-output');
  appendTerminalLine('system', `$ ${cmd}`);

  const workDir = document.getElementById('terminal-workdir').textContent;

  const resp = await window.yaver.startExec({
    command: cmd,
    workDir: workDir === '~/' ? '' : workDir,
  });

  if (!resp || !resp.ok) {
    appendTerminalLine('stderr', `Error: ${resp?.error || 'Failed to start command'}`);
    return;
  }

  currentExecId = resp.execId;
  document.getElementById('terminal-exec-status').textContent = `PID ${resp.pid} - running`;
  startExecPolling(resp.execId);
}

function handleTerminalKeydown(event) {
  if (event.key === 'Enter') {
    event.preventDefault();
    runTerminalCommand();
  }
}

function startExecPolling(execId) {
  let lastStdoutLen = 0;
  let lastStderrLen = 0;

  if (execPollInterval) clearInterval(execPollInterval);
  execPollInterval = setInterval(async () => {
    if (currentExecId !== execId) return;

    const resp = await window.yaver.getExec(execId);
    if (!resp || !resp.ok || !resp.exec) return;

    const exec = resp.exec;

    // Print new stdout
    if (exec.stdout && exec.stdout.length > lastStdoutLen) {
      appendTerminalLine('stdout', exec.stdout.substring(lastStdoutLen));
      lastStdoutLen = exec.stdout.length;
    }

    // Print new stderr
    if (exec.stderr && exec.stderr.length > lastStderrLen) {
      appendTerminalLine('stderr', exec.stderr.substring(lastStderrLen));
      lastStderrLen = exec.stderr.length;
    }

    const statusEl = document.getElementById('terminal-exec-status');
    if (exec.status === 'completed' || exec.status === 'failed' || exec.status === 'killed') {
      const exitCode = exec.exitCode !== undefined && exec.exitCode !== null ? exec.exitCode : '?';
      appendTerminalLine('system', `Process exited with code ${exitCode}`);
      statusEl.textContent = `Exited (${exitCode})`;
      clearInterval(execPollInterval);
      execPollInterval = null;
      currentExecId = null;
    } else {
      statusEl.textContent = `PID ${exec.pid || '?'} - ${exec.status}`;
    }
  }, 500);
}

function appendTerminalLine(type, text) {
  const output = document.getElementById('terminal-output');
  const span = document.createElement('span');
  span.className = type;
  span.textContent = text;
  if (!text.endsWith('\n')) span.textContent += '\n';
  output.appendChild(span);
  output.scrollTop = output.scrollHeight;
}

async function signalExec(signal) {
  if (!currentExecId) return;
  await window.yaver.signalExec(currentExecId, signal);
}

async function killCurrentExec() {
  if (!currentExecId) return;
  await window.yaver.killExec(currentExecId);
  appendTerminalLine('system', 'Process killed');
  document.getElementById('terminal-exec-status').textContent = 'Killed';
  currentExecId = null;
  if (execPollInterval) { clearInterval(execPollInterval); execPollInterval = null; }
}

function clearTerminal() {
  const output = document.getElementById('terminal-output');
  output.innerHTML = '<span class="system">Terminal cleared.\n</span>';
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

async function loadConfig() {
  cachedConfig = await window.yaver.getConfig();
  renderRelayList();
  renderTunnelList();
}

async function loadSettingsUI() {
  const settings = await window.yaver.getSettings();
  document.getElementById('setting-agent-url').value = settings.agentBaseUrl || 'http://localhost:18080';
  document.getElementById('setting-speech-provider').value = settings.speechProvider || 'whisper';
  document.getElementById('setting-speech-api-key').value = settings.speechApiKey || '';

  const autoToggle = document.getElementById('toggle-autostart');
  if (settings.autoStart) autoToggle.classList.add('on');
  else autoToggle.classList.remove('on');

  // Load managed relay status
  loadManagedRelayStatus();

  // Load runner list from agent
  const runners = await window.yaver.getRunners();
  if (runners && runners.ok && runners.runners) {
    const select = document.getElementById('setting-runner');
    select.innerHTML = '';
    runners.runners.forEach((r) => {
      const opt = document.createElement('option');
      opt.value = r.id;
      opt.textContent = `${r.name}${r.installed ? '' : ' (not installed)'}`;
      if (r.isDefault) opt.selected = true;
      select.appendChild(opt);
    });

    // Also update the create-task runner dropdown
    const taskRunnerSelect = document.getElementById('new-task-runner');
    taskRunnerSelect.innerHTML = '<option value="">Default</option>';
    runners.runners.forEach((r) => {
      const opt = document.createElement('option');
      opt.value = r.id;
      opt.textContent = r.name;
      taskRunnerSelect.appendChild(opt);
    });
  }
}

async function saveAllSettings() {
  // Save desktop settings
  const settings = {
    agentBaseUrl: document.getElementById('setting-agent-url').value.trim() || 'http://localhost:18080',
    speechProvider: document.getElementById('setting-speech-provider').value,
    speechApiKey: document.getElementById('setting-speech-api-key').value,
    autoStart: document.getElementById('toggle-autostart').classList.contains('on'),
  };
  await window.yaver.saveSettings(settings);

  // Save config with relay/tunnel changes
  await window.yaver.saveConfig(cachedConfig);

  // Switch runner if changed
  const runnerSel = document.getElementById('setting-runner');
  if (runnerSel.value) {
    await window.yaver.switchRunner(runnerSel.value);
  }

  showToast('Settings saved');
}

// ---- Managed infrastructure ----

const MANAGED_RELAY_STEPS = [
  { label: 'Creating your dedicated server...', key: 'creating' },
  { label: 'Setting up DNS (yourname.relay.yaver.io)...', key: 'dns' },
  { label: 'Installing SSL certificate...', key: 'ssl' },
  { label: 'Deploying relay service...', key: 'deploying' },
  { label: 'Running health checks...', key: 'health' },
  { label: 'Your relay is ready!', key: 'ready' },
];

let managedRelayPollTimer = null;

function machineLabel(machine) {
  const type = machine.machineType === 'gpu' ? 'GPU cloud machine' : 'CPU cloud machine';
  const region = machine.region ? ` · ${String(machine.region).toUpperCase()}` : '';
  return `${type}${region}`;
}

function renderMachineSummary(machine) {
  const address = escapeHtml(machine.hostname || machine.serverIp || 'Provisioning…');
  const tone =
    machine.status === 'active'
      ? 'rgba(34,197,94,0.08)'
      : machine.status === 'error'
        ? 'rgba(239,68,68,0.08)'
        : 'rgba(99,102,241,0.06)';
  const border =
    machine.status === 'active'
      ? 'rgba(34,197,94,0.2)'
      : machine.status === 'error'
        ? 'rgba(239,68,68,0.2)'
        : 'rgba(99,102,241,0.2)';
  const title =
    machine.status === 'active'
      ? 'Active cloud machine'
      : machine.status === 'error'
        ? 'Cloud machine needs attention'
        : 'Cloud machine provisioning';

  return `
    <div style="padding: 12px; background: ${tone}; border-radius: 8px; border: 1px solid ${border};">
      <div style="display: flex; align-items: center; gap: 6px; margin-bottom: 6px;">
        <strong style="color: #e5e5e5; font-size: 13px;">${title}</strong>
      </div>
      <div style="font-size: 12px; color: #aaa; margin-bottom: 4px;">${machineLabel(machine)}</div>
      <div style="font-family: monospace; font-size: 13px; color: #6366f1;">${address}</div>
      ${machine.errorMessage ? `<div style="font-size: 12px; color: #fca5a5; margin-top: 8px;">${escapeHtml(machine.errorMessage)}</div>` : ''}
    </div>
  `;
}

async function loadManagedRelayStatus() {
  const container = document.getElementById('managed-relay-status');
  if (!container) return;

  try {
    const sub = await window.yaver.getSubscription();
    if (!sub) {
      container.innerHTML = `
        <p style="color: var(--text-muted, #888); font-size: 13px; margin-bottom: 8px;">
          Connect your own infrastructure for free, or buy a managed Yaver machine on the web.
        </p>
        <button class="btn btn-primary btn-sm" onclick="openManagedRelayPurchase()">
          Open managed cloud options
        </button>
      `;
      return;
    }

    const machines = Array.isArray(sub.machines) ? sub.machines : [];
    const subscription = sub.subscription || null;
    const relay = sub.relay || null;
    const activeMachine = machines.find((machine) => machine.status === 'active');
    const pendingMachine = machines.find((machine) => machine.status !== 'stopped');
    if (activeMachine || pendingMachine) {
      const machine = activeMachine || pendingMachine;
      container.innerHTML = renderMachineSummary(machine);
      if (machine.status === 'active' || machine.status === 'error') {
        if (managedRelayPollTimer) { clearInterval(managedRelayPollTimer); managedRelayPollTimer = null; }
      } else if (!managedRelayPollTimer) {
        managedRelayPollTimer = setInterval(loadManagedRelayStatus, 3000);
      }
      return;
    }

    if (subscription?.status === 'active' && relay?.status === 'active') {
      container.innerHTML = `
        <div style="padding: 12px; background: rgba(34,197,94,0.08); border-radius: 8px; border: 1px solid rgba(34,197,94,0.2);">
          <div style="display: flex; align-items: center; gap: 6px; margin-bottom: 6px;">
            <span style="color: #22c55e; font-size: 14px;">&#10003;</span>
            <strong style="color: #e5e5e5; font-size: 13px;">Active</strong>
          </div>
          <div style="font-size: 12px; color: #aaa; margin-bottom: 4px;">Relay URL</div>
          <div style="font-family: monospace; font-size: 13px; color: #6366f1;">${escapeHtml(relay.domain || 'N/A')}</div>
          ${relay.region ? `<div style="font-size: 12px; color: #888; margin-top: 6px;">Region: ${escapeHtml(relay.region)}</div>` : ''}
        </div>
      `;
      if (managedRelayPollTimer) { clearInterval(managedRelayPollTimer); managedRelayPollTimer = null; }
      return;
    }

    if (!relay) {
      container.innerHTML = `
        <p style="color: var(--text-muted, #888); font-size: 13px; margin-bottom: 8px;">
          No managed infrastructure is attached to this account yet.
        </p>
        <button class="btn btn-primary btn-sm" onclick="openManagedRelayPurchase()">
          Open managed cloud options
        </button>
      `;
      if (managedRelayPollTimer) { clearInterval(managedRelayPollTimer); managedRelayPollTimer = null; }
      return;
    }

    // Provisioning in progress
    const currentIndex = relay?.status === 'provisioning'
      ? 0
      : relay?.status === 'error'
        ? -1
        : MANAGED_RELAY_STEPS.length - 2;
    let stepsHtml = MANAGED_RELAY_STEPS.map((step, i) => {
      const isReady = relay?.status === 'active';
      const isComplete = i < currentIndex || isReady;
      const isCurrent = i === currentIndex && !isReady;
      const icon = isComplete ? '<span style="color:#22c55e;">&#10003;</span>'
        : isCurrent ? '<span class="spinner-sm"></span>'
        : '<span style="color:#555;">&#9679;</span>';
      const textColor = isComplete ? '#aaa' : isCurrent ? '#e5e5e5' : '#555';
      return `<div style="display:flex;align-items:center;gap:8px;margin-bottom:6px;">
        <span style="width:18px;text-align:center;">${icon}</span>
        <span style="font-size:13px;color:${textColor};">${step.label}</span>
      </div>`;
    }).join('');

    container.innerHTML = `
      <div style="padding: 12px; background: rgba(99,102,241,0.06); border-radius: 8px; border: 1px solid rgba(99,102,241,0.2);">
        <strong style="color: #e5e5e5; font-size: 13px; display: block; margin-bottom: 10px;">Setting up your relay...</strong>
        ${stepsHtml}
        ${relay?.errorMessage ? `<div style="font-size: 12px; color: #fca5a5; margin-top: 8px;">${escapeHtml(relay.errorMessage)}</div>` : ''}
      </div>
    `;

    // Poll every 3 seconds while provisioning
    if (!managedRelayPollTimer) {
      managedRelayPollTimer = setInterval(loadManagedRelayStatus, 3000);
    }
  } catch (e) {
    container.innerHTML = `
      <p style="color: var(--text-muted, #888); font-size: 13px; margin-bottom: 8px;">
        Connect your own infrastructure for free, or buy a managed Yaver machine on the web.
      </p>
      <button class="btn btn-primary btn-sm" onclick="openManagedRelayPurchase()">
        Open managed cloud options
      </button>
    `;
  }
}

function openManagedRelayPurchase() {
  window.yaver.openExternal('https://yaver.io/pricing');
}

// ---- Relay servers ----

function renderRelayList() {
  const container = document.getElementById('relay-list');
  const relays = cachedConfig.relay_servers || [];
  if (relays.length === 0) {
    container.innerHTML = '<p class="text-dim" style="font-size:12px">No relay servers configured.</p>';
    return;
  }
  container.innerHTML = '';
  relays.forEach((r, i) => {
    const div = document.createElement('div');
    div.className = 'relay-item';
    div.innerHTML = `
      <div>
        <div class="relay-info">${escapeHtml(r.label || r.quic_addr)}</div>
        <div class="relay-meta">${escapeHtml(r.quic_addr)}${r.region ? ' - ' + r.region : ''}</div>
      </div>
      <button class="btn-icon" onclick="removeRelay(${i})" title="Remove">&#10005;</button>
    `;
    container.appendChild(div);
  });
}

function showAddRelayForm() { document.getElementById('relay-add-form').style.display = 'block'; }
function hideAddRelayForm() { document.getElementById('relay-add-form').style.display = 'none'; }

function addRelay() {
  const quicAddr = document.getElementById('relay-quic-addr').value.trim();
  if (!quicAddr) return;

  if (!cachedConfig.relay_servers) cachedConfig.relay_servers = [];
  cachedConfig.relay_servers.push({
    id: generateId(),
    quic_addr: quicAddr,
    http_url: document.getElementById('relay-http-url').value.trim(),
    password: document.getElementById('relay-password').value,
    label: document.getElementById('relay-label').value.trim(),
    priority: cachedConfig.relay_servers.length,
  });

  // Clear form
  document.getElementById('relay-quic-addr').value = '';
  document.getElementById('relay-http-url').value = '';
  document.getElementById('relay-password').value = '';
  document.getElementById('relay-label').value = '';
  hideAddRelayForm();
  renderRelayList();
}

function removeRelay(idx) {
  if (!cachedConfig.relay_servers) return;
  cachedConfig.relay_servers.splice(idx, 1);
  renderRelayList();
}

// ---- Survey relay opt-out toggle ----

function toggleRelayOptOut() {
  surveyData.relayOptOut = !surveyData.relayOptOut;
  const optOutBtn = document.getElementById('survey-relay-optout');
  const freeRelayBox = document.getElementById('survey-free-relay-box');
  const badge = document.getElementById('survey-free-relay-badge');
  const customSection = document.getElementById('survey-custom-relay-section');
  if (surveyData.relayOptOut) {
    optOutBtn.style.background = 'var(--accent)';
    optOutBtn.style.borderColor = 'var(--accent)';
    optOutBtn.style.color = '#fff';
    freeRelayBox.style.borderColor = 'var(--border)';
    freeRelayBox.style.background = 'var(--bg)';
    badge.textContent = 'DISABLED';
    badge.style.color = 'var(--text-dim)';
    customSection.style.display = 'none';
  } else {
    optOutBtn.style.background = '';
    optOutBtn.style.borderColor = '';
    optOutBtn.style.color = '';
    freeRelayBox.style.borderColor = 'var(--accent)';
    freeRelayBox.style.background = 'rgba(99,102,241,0.08)';
    badge.textContent = 'ACTIVE';
    badge.style.color = 'var(--accent)';
    customSection.style.display = '';
  }
}

// ---- Cloudflare tunnels ----

function renderTunnelList() {
  const container = document.getElementById('tunnel-list');
  const tunnels = cachedConfig.cloudflare_tunnels || [];
  if (tunnels.length === 0) {
    container.innerHTML = '<p class="text-dim" style="font-size:12px">No tunnels configured.</p>';
    return;
  }
  container.innerHTML = '';
  tunnels.forEach((t, i) => {
    const div = document.createElement('div');
    div.className = 'tunnel-item';
    div.innerHTML = `
      <div>
        <div class="tunnel-info">${escapeHtml(t.label || t.url)}</div>
        <div class="tunnel-meta">${escapeHtml(t.url)}</div>
      </div>
      <button class="btn-icon" onclick="removeTunnel(${i})" title="Remove">&#10005;</button>
    `;
    container.appendChild(div);
  });
}

function showAddTunnelForm() { document.getElementById('tunnel-add-form').style.display = 'block'; }
function hideAddTunnelForm() { document.getElementById('tunnel-add-form').style.display = 'none'; }

function addTunnel() {
  const url = document.getElementById('tunnel-url').value.trim();
  if (!url) return;

  if (!cachedConfig.cloudflare_tunnels) cachedConfig.cloudflare_tunnels = [];
  cachedConfig.cloudflare_tunnels.push({
    id: generateId(),
    url,
    cf_access_client_id: document.getElementById('tunnel-client-id').value.trim(),
    cf_access_client_secret: document.getElementById('tunnel-client-secret').value,
    label: document.getElementById('tunnel-label').value.trim(),
    priority: cachedConfig.cloudflare_tunnels.length,
  });

  document.getElementById('tunnel-url').value = '';
  document.getElementById('tunnel-client-id').value = '';
  document.getElementById('tunnel-client-secret').value = '';
  document.getElementById('tunnel-label').value = '';
  hideAddTunnelForm();
  renderTunnelList();
}

function removeTunnel(idx) {
  if (!cachedConfig.cloudflare_tunnels) return;
  cachedConfig.cloudflare_tunnels.splice(idx, 1);
  renderTunnelList();
}

// ---- Toggle switch ----

function toggleSwitch(el) {
  el.classList.toggle('on');
}

// ---- Integrations ----

async function loadIntegrations() {
  try {
    const resp = await window.yaver.getNotificationsConfig();
    if (!resp || !resp.ok || !resp.config) return;
    const c = resp.config;

    // Telegram
    if (c.telegram) {
      document.getElementById('intg-telegram-token').value = c.telegram.botToken || '';
      document.getElementById('intg-telegram-chatid').value = c.telegram.chatId || '';
      setToggle('intg-telegram-enabled', c.telegram.enabled);
    }
    // Discord
    if (c.discord) {
      document.getElementById('intg-discord-url').value = c.discord.webhookUrl || '';
      setToggle('intg-discord-enabled', c.discord.enabled);
    }
    // Slack
    if (c.slack) {
      document.getElementById('intg-slack-url').value = c.slack.webhookUrl || '';
      setToggle('intg-slack-enabled', c.slack.enabled);
    }
    // Teams
    if (c.teams) {
      document.getElementById('intg-teams-url').value = c.teams.webhookUrl || '';
      setToggle('intg-teams-enabled', c.teams.enabled);
    }
    // Email
    if (c.email_notify) {
      document.getElementById('intg-email-to').value = c.email_notify.to || '';
      setToggle('intg-email-enabled', c.email_notify.enabled);
    }
    // Linear
    if (c.linear) {
      document.getElementById('intg-linear-key').value = c.linear.apiKey || '';
      document.getElementById('intg-linear-team').value = c.linear.teamId || '';
      setToggle('intg-linear-enabled', c.linear.enabled);
    }
    // Jira
    if (c.jira) {
      document.getElementById('intg-jira-url').value = c.jira.baseUrl || '';
      document.getElementById('intg-jira-email').value = c.jira.email || '';
      document.getElementById('intg-jira-token').value = c.jira.apiToken || '';
      document.getElementById('intg-jira-project').value = c.jira.projectKey || '';
      setToggle('intg-jira-enabled', c.jira.enabled);
    }
    // PagerDuty
    if (c.pagerduty) {
      document.getElementById('intg-pd-key').value = c.pagerduty.routingKey || '';
      setToggle('intg-pd-enabled', c.pagerduty.enabled);
      setToggle('intg-pd-failonly', c.pagerduty.onFailOnly);
    }
    // Opsgenie
    if (c.opsgenie) {
      document.getElementById('intg-og-key').value = c.opsgenie.apiKey || '';
      setToggle('intg-og-enabled', c.opsgenie.enabled);
      setToggle('intg-og-failonly', c.opsgenie.onFailOnly);
    }
  } catch (e) {
    console.error('Failed to load integrations:', e);
  }
}

function setToggle(id, val) {
  const el = document.getElementById(id);
  if (el) { if (val) el.classList.add('on'); else el.classList.remove('on'); }
}

function isToggleOn(id) {
  const el = document.getElementById(id);
  return el ? el.classList.contains('on') : false;
}

async function saveIntegrations() {
  const config = {
    telegram: {
      botToken: document.getElementById('intg-telegram-token').value.trim(),
      chatId: document.getElementById('intg-telegram-chatid').value.trim(),
      enabled: isToggleOn('intg-telegram-enabled'),
    },
    discord: {
      webhookUrl: document.getElementById('intg-discord-url').value.trim(),
      enabled: isToggleOn('intg-discord-enabled'),
    },
    slack: {
      webhookUrl: document.getElementById('intg-slack-url').value.trim(),
      enabled: isToggleOn('intg-slack-enabled'),
    },
    teams: {
      webhookUrl: document.getElementById('intg-teams-url').value.trim(),
      enabled: isToggleOn('intg-teams-enabled'),
    },
    email_notify: {
      to: document.getElementById('intg-email-to').value.trim(),
      enabled: isToggleOn('intg-email-enabled'),
    },
    linear: {
      apiKey: document.getElementById('intg-linear-key').value.trim(),
      teamId: document.getElementById('intg-linear-team').value.trim(),
      enabled: isToggleOn('intg-linear-enabled'),
    },
    jira: {
      baseUrl: document.getElementById('intg-jira-url').value.trim(),
      email: document.getElementById('intg-jira-email').value.trim(),
      apiToken: document.getElementById('intg-jira-token').value.trim(),
      projectKey: document.getElementById('intg-jira-project').value.trim(),
      enabled: isToggleOn('intg-jira-enabled'),
    },
    pagerduty: {
      routingKey: document.getElementById('intg-pd-key').value.trim(),
      enabled: isToggleOn('intg-pd-enabled'),
      onFailOnly: isToggleOn('intg-pd-failonly'),
    },
    opsgenie: {
      apiKey: document.getElementById('intg-og-key').value.trim(),
      enabled: isToggleOn('intg-og-enabled'),
      onFailOnly: isToggleOn('intg-og-failonly'),
    },
  };

  try {
    await window.yaver.saveNotificationsConfig(config);
    alert('Integrations saved!');
  } catch (e) {
    alert('Failed to save: ' + e.message);
  }
}

async function testIntegration(channel) {
  try {
    const resp = await window.yaver.testNotification(channel);
    alert(resp && resp.result ? resp.result : 'Test sent');
  } catch (e) {
    alert('Test failed: ' + e.message);
  }
}

// ---- Clean ----

async function cleanAgent(days) {
  const resp = await window.yaver.agentClean(days || 0);
  if (resp && resp.ok && resp.result) {
    const r = resp.result;
    showToast(`Cleaned: ${r.tasksRemoved || 0} tasks, ${r.imagesRemoved || 0} images`);
  } else {
    showToast('Clean failed: ' + (resp?.error || 'unknown error'));
  }
}

// ---------------------------------------------------------------------------
// Sign in
// ---------------------------------------------------------------------------

async function handlePostAuth() {
  const state = await window.yaver.getAppState();
  if (!state.agentInstalled) {
    showView('view-setup-prereqs');
    runPrerequisites();
    return;
  }

  // Check if survey needed
  const userInfo = await window.yaver.getUserInfo();
  if (userInfo && userInfo.signedIn && userInfo.user && !userInfo.user.surveyCompleted) {
    if (userInfo.user.name) surveyData.fullName = userInfo.user.name;
    showSurvey();
    return;
  }

  showMainApp();
  loadConfig();
  loadSettingsUI();
  updateDashboard();
}

async function signInGoogle() {
  disableAuthButtons();
  clearSigninError();
  const result = await window.yaver.authenticate();
  if (result.success) {
    await handlePostAuth();
  } else {
    enableAuthButtons();
    showSigninError(result.error || 'Authentication failed. Please try again.');
  }
}

async function signInMicrosoft() {
  disableAuthButtons();
  clearSigninError();
  const result = await window.yaver.authenticateMicrosoft();
  if (result.success) {
    await handlePostAuth();
  } else {
    enableAuthButtons();
    showSigninError(result.error || 'Authentication failed. Please try again.');
  }
}

async function signInApple() {
  disableAuthButtons();
  clearSigninError();
  const result = await window.yaver.authenticateApple();
  if (result.success) {
    await handlePostAuth();
  } else {
    enableAuthButtons();
    showSigninError(result.error || 'Authentication failed. Please try again.');
  }
}

function disableAuthButtons() {
  document.querySelectorAll('.btn-apple, .btn-google, .btn-microsoft').forEach((b) => (b.disabled = true));
}

function enableAuthButtons() {
  document.querySelectorAll('.btn-apple, .btn-google, .btn-microsoft').forEach((b) => (b.disabled = false));
}

function showSigninError(msg) {
  const el = document.getElementById('signin-error');
  if (el) {
    el.innerHTML = `<div class="banner banner-error"><span class="icon">&#10007;</span><span>${escapeHtml(msg)}</span></div>`;
  }
}

function clearSigninError() {
  const el = document.getElementById('signin-error');
  if (el) el.innerHTML = '';
}

// ---------------------------------------------------------------------------
// Sign out
// ---------------------------------------------------------------------------

async function doSignOut() {
  await window.yaver.signOut();
  hideMainApp();
  showView('view-signin');
}

// ---------------------------------------------------------------------------
// Restart agent
// ---------------------------------------------------------------------------

async function restartAgent() {
  await window.yaver.restartService();
  showToast('Agent restarting...');
  setTimeout(updateDashboard, 2000);
}

// ---------------------------------------------------------------------------
// Prerequisites check
// ---------------------------------------------------------------------------

async function runPrerequisites() {
  const result = await window.yaver.checkPrerequisites();

  setCheckIcon('icon-claude', result.claude);

  const platLabel = document.getElementById('label-platform');
  platLabel.textContent = `${friendlyPlatform(result.platform)} (${result.arch})`;
  setCheckIcon('icon-platform', true);

  const btn = document.getElementById('btn-prereq-continue');
  btn.disabled = false;
}

function setCheckIcon(id, pass) {
  const el = document.getElementById(id);
  if (!el) return;
  el.className = `check-icon ${pass ? 'pass' : 'fail'}`;
  el.textContent = pass ? '\u2713' : '\u2717';
}

// ---------------------------------------------------------------------------
// Install agent
// ---------------------------------------------------------------------------

async function startInstall() {
  showView('view-setup-install');

  const fill = document.getElementById('progress-fill');
  const label = document.getElementById('progress-label');
  const errDiv = document.getElementById('install-error');
  const retryBtn = document.getElementById('btn-install-retry');
  const skipBtn = document.getElementById('btn-install-skip');

  errDiv.innerHTML = '';
  retryBtn.style.display = 'none';
  skipBtn.style.display = 'none';

  // Phase 1: Download
  label.textContent = 'Downloading agent binary...';
  fill.style.width = '20%';

  const dlResult = await window.yaver.downloadAgent();

  if (!dlResult.success) {
    fill.style.width = '20%';
    label.textContent = 'Download failed';
    errDiv.innerHTML = `<div class="banner banner-error"><span class="icon">&#10007;</span><span>${escapeHtml(dlResult.error)}</span></div>`;
    retryBtn.style.display = 'inline-flex';
    skipBtn.style.display = 'inline-flex';
    return;
  }

  // Phase 2: Install service
  fill.style.width = '60%';
  label.textContent = 'Configuring system service...';

  const svcResult = await window.yaver.installService();

  if (!svcResult.success) {
    fill.style.width = '80%';
    label.textContent = 'Service setup failed';
    errDiv.innerHTML = `<div class="banner banner-error"><span class="icon">&#10007;</span><span>${escapeHtml(svcResult.error)}</span></div>`;
    retryBtn.style.display = 'inline-flex';
    skipBtn.style.display = 'inline-flex';
    return;
  }

  fill.style.width = '100%';
  label.textContent = 'Installation complete!';

  setTimeout(finishSetup, 800);
}

async function finishSetup() {
  // Check if survey needed after install
  const userInfo = await window.yaver.getUserInfo();
  if (userInfo && userInfo.signedIn && userInfo.user && !userInfo.user.surveyCompleted) {
    if (userInfo.user.name) surveyData.fullName = userInfo.user.name;
    showSurvey();
    return;
  }
  showMainApp();
  loadConfig();
  loadSettingsUI();
  updateDashboard();
}

// ---------------------------------------------------------------------------
// Listen for auth state changes from main process
// ---------------------------------------------------------------------------

window.yaver.onAuthStateChanged((data) => {
  if (data.signedIn) {
    init();
  } else {
    hideMainApp();
    showView('view-signin');
  }
});

// ---------------------------------------------------------------------------
// Markdown rendering (simple regex-based)
// ---------------------------------------------------------------------------

function renderMarkdown(text) {
  if (!text) return '';

  // Escape HTML first
  let html = escapeHtml(text);

  // Code blocks (``` ... ```)
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) => {
    return `<div class="md-code">${code}</div>`;
  });

  // Inline code (`...`)
  html = html.replace(/`([^`]+)`/g, '<span class="md-inline-code">$1</span>');

  // Headers
  html = html.replace(/^### (.+)$/gm, '<div class="md-h3">$1</div>');
  html = html.replace(/^## (.+)$/gm, '<div class="md-h2">$1</div>');
  html = html.replace(/^# (.+)$/gm, '<div class="md-h1">$1</div>');

  // Bold
  html = html.replace(/\*\*(.+?)\*\*/g, '<span class="md-bold">$1</span>');

  // Italic
  html = html.replace(/\*(.+?)\*/g, '<span class="md-italic">$1</span>');

  // Horizontal rule
  html = html.replace(/^---$/gm, '<hr class="md-hr">');

  return html;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function friendlyPlatform(p) {
  const map = { darwin: 'macOS', linux: 'Linux', win32: 'Windows' };
  return map[p] || p;
}

function escapeHtml(str) {
  if (!str) return '';
  return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function formatTime(iso) {
  if (!iso) return '';
  try {
    const d = new Date(iso);
    const now = new Date();
    const diff = now - d;
    if (diff < 60000) return 'just now';
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
    return d.toLocaleDateString();
  } catch {
    return iso;
  }
}

function generateId() {
  return Math.random().toString(36).substring(2, 10);
}

function createEmptyState(icon, text) {
  const div = document.createElement('div');
  div.className = 'empty-state';
  div.innerHTML = `<div class="empty-icon">${icon}</div><div>${text}</div>`;
  return div;
}

function stopAllPolling() {
  if (dashPollInterval) { clearInterval(dashPollInterval); dashPollInterval = null; }
  if (taskPollInterval) { clearInterval(taskPollInterval); taskPollInterval = null; }
  if (execPollInterval) { clearInterval(execPollInterval); execPollInterval = null; }
}

// Simple toast notification
function showToast(message) {
  const toast = document.createElement('div');
  toast.style.cssText = 'position:fixed;bottom:24px;right:24px;background:#1a1d27;border:1px solid #2a2d3a;color:#e1e4e8;padding:10px 20px;border-radius:8px;font-size:13px;z-index:200;transition:opacity 0.3s;';
  toast.textContent = message;
  document.body.appendChild(toast);
  setTimeout(() => {
    toast.style.opacity = '0';
    setTimeout(() => toast.remove(), 300);
  }, 2500);
}

// ---------------------------------------------------------------------------
// Terms & Privacy views
// ---------------------------------------------------------------------------

function showTermsView() {
  document.getElementById('terms-overlay').classList.add('active');
}

function hideTermsView() {
  document.getElementById('terms-overlay').classList.remove('active');
}

function showPrivacyView() {
  document.getElementById('privacy-overlay').classList.add('active');
}

function hidePrivacyView() {
  document.getElementById('privacy-overlay').classList.remove('active');
}

// ---------------------------------------------------------------------------
// Survey logic
// ---------------------------------------------------------------------------

function getSurveyTotalPages() {
  return surveyData.identity === 'developer' ? 8 : 7;
}

function showSurvey() {
  document.getElementById('auth-flow').style.display = 'none';
  document.getElementById('main-app').style.display = 'none';
  document.getElementById('survey-overlay').classList.add('active');

  // Set name input value
  document.getElementById('survey-name').value = surveyData.fullName;

  // Build role grid
  const roleGrid = document.getElementById('survey-role-grid');
  roleGrid.innerHTML = '';
  SURVEY_IDENTITIES.forEach((item) => {
    const card = document.createElement('div');
    card.className = 'survey-card' + (surveyData.identity === item.id ? ' selected' : '');
    card.innerHTML = `<div class="survey-card-title">${item.label}</div>`;
    card.onclick = () => {
      surveyData.identity = item.id;
      roleGrid.querySelectorAll('.survey-card').forEach((c) => c.classList.remove('selected'));
      card.classList.add('selected');
    };
    roleGrid.appendChild(card);
  });

  // Build speech provider grid
  const speechGrid = document.getElementById('survey-speech-grid');
  speechGrid.innerHTML = '';
  SURVEY_SPEECH_PROVIDERS.forEach((provider) => {
    const card = document.createElement('div');
    card.className = 'survey-card' + (surveyData.speechProvider === provider.id ? ' selected' : '');
    card.innerHTML = `<div class="survey-card-title">${escapeHtml(provider.name)}</div><div class="survey-card-desc">${escapeHtml(provider.description)}</div>`;
    card.onclick = () => {
      surveyData.speechProvider = provider.id;
      speechGrid.querySelectorAll('.survey-card').forEach((c) => c.classList.remove('selected'));
      card.classList.add('selected');
      updateSpeechKeySection();
    };
    speechGrid.appendChild(card);
  });

  // Build verbosity bar
  buildVerbosityBar();

  // Build languages grid
  const langGrid = document.getElementById('survey-languages-grid');
  langGrid.innerHTML = '';
  SURVEY_LANGUAGES.forEach((lang) => {
    const chip = document.createElement('div');
    chip.className = 'survey-chip' + (surveyData.languages.includes(lang) ? ' selected' : '');
    chip.textContent = lang;
    chip.onclick = () => {
      if (surveyData.languages.includes(lang)) {
        surveyData.languages = surveyData.languages.filter((l) => l !== lang);
        chip.classList.remove('selected');
      } else {
        surveyData.languages.push(lang);
        chip.classList.add('selected');
      }
    };
    langGrid.appendChild(chip);
  });

  // Build experience list
  const expList = document.getElementById('survey-experience-list');
  expList.innerHTML = '';
  SURVEY_EXPERIENCE_LEVELS.forEach((level) => {
    const opt = document.createElement('div');
    opt.className = 'survey-option' + (surveyData.experience === level ? ' selected' : '');
    opt.textContent = level;
    opt.onclick = () => {
      surveyData.experience = level;
      expList.querySelectorAll('.survey-option').forEach((o) => o.classList.remove('selected'));
      opt.classList.add('selected');
    };
    expList.appendChild(opt);
  });

  // Build use case list
  const ucList = document.getElementById('survey-usecase-list');
  ucList.innerHTML = '';
  SURVEY_USE_CASES.forEach((uc) => {
    const opt = document.createElement('div');
    opt.className = 'survey-option' + (surveyData.useCase === uc ? ' selected' : '');
    opt.textContent = uc;
    opt.onclick = () => {
      surveyData.useCase = uc;
      ucList.querySelectorAll('.survey-option').forEach((o) => o.classList.remove('selected'));
      opt.classList.add('selected');
    };
    ucList.appendChild(opt);
  });

  // Build company size grid
  const compGrid = document.getElementById('survey-company-grid');
  compGrid.innerHTML = '';
  SURVEY_COMPANY_SIZES.forEach((size) => {
    const btn = document.createElement('div');
    btn.className = 'survey-company-btn' + (surveyData.companySize === size ? ' selected' : '');
    btn.textContent = size;
    btn.onclick = () => {
      surveyData.companySize = size;
      compGrid.querySelectorAll('.survey-company-btn').forEach((b) => b.classList.remove('selected'));
      btn.classList.add('selected');
    };
    compGrid.appendChild(btn);
  });

  // Fetch runners from agent
  loadSurveyRunners();

  // Wire name input
  const nameInput = document.getElementById('survey-name');
  nameInput.oninput = () => {
    surveyData.fullName = nameInput.value;
    document.getElementById('survey-name-continue').disabled = !nameInput.value.trim();
  };
  document.getElementById('survey-name-continue').disabled = !surveyData.fullName.trim();

  surveyPage = 0;
  updateSurveyUI();
}

async function loadSurveyRunners() {
  const runners = await window.yaver.getRunners();
  if (runners && runners.ok && runners.runners) {
    surveyRunners = runners.runners;
  } else {
    // Fallback defaults
    surveyRunners = [
      { id: 'claude', name: 'Claude Code', description: 'Anthropic CLI agent', isDefault: true },
      { id: 'codex', name: 'OpenAI Codex', description: 'OpenAI CLI agent' },
      { id: 'aider', name: 'Aider', description: 'Open-source AI pair programmer' },
      { id: 'custom', name: 'Custom', description: 'Any terminal command' },
    ];
  }
  buildRunnerGrid();
}

function buildRunnerGrid() {
  const grid = document.getElementById('survey-runner-grid');
  grid.innerHTML = '';
  surveyRunners.forEach((runner) => {
    const runnerId = runner.id || runner.runnerId;
    const card = document.createElement('div');
    card.className = 'survey-card' + (surveyData.selectedRunner === runnerId ? ' selected' : '');
    card.innerHTML = `<div class="survey-card-title">${escapeHtml(runner.name)}</div><div class="survey-card-desc">${escapeHtml(runner.description || '')}</div>`;
    card.onclick = () => {
      surveyData.selectedRunner = runnerId;
      grid.querySelectorAll('.survey-card').forEach((c) => c.classList.remove('selected'));
      card.classList.add('selected');
      document.getElementById('survey-custom-command').style.display = runnerId === 'custom' ? 'block' : 'none';
      updateSurveyButtons();
    };
    grid.appendChild(card);
  });

  const customInput = document.getElementById('survey-custom-command');
  customInput.value = surveyData.customCommand;
  customInput.oninput = () => {
    surveyData.customCommand = customInput.value;
    updateSurveyButtons();
  };
  customInput.style.display = surveyData.selectedRunner === 'custom' ? 'block' : 'none';
}

function buildVerbosityBar() {
  const bar = document.getElementById('survey-verbosity-bar');
  bar.innerHTML = '';
  for (let i = 0; i <= 10; i++) {
    const btn = document.createElement('div');
    btn.className = 'survey-verbosity-btn' + (i <= surveyData.verbosity ? ' filled' : '');
    if (i === surveyData.verbosity) btn.textContent = String(i);
    btn.onclick = () => {
      surveyData.verbosity = i;
      updateVerbosityUI();
    };
    bar.appendChild(btn);
  }
  updateVerbosityUI();
}

function updateVerbosityUI() {
  document.getElementById('survey-verbosity-number').textContent = surveyData.verbosity;
  const v = surveyData.verbosity;
  let desc;
  if (v <= 2) desc = 'Minimal -- just confirm what was done';
  else if (v <= 4) desc = 'Brief -- summarize in a few sentences';
  else if (v <= 6) desc = 'Moderate -- key changes and reasoning';
  else if (v <= 8) desc = 'Detailed -- code changes and explanations';
  else desc = 'Full -- everything: diffs, reasoning, alternatives';
  document.getElementById('survey-verbosity-desc').textContent = desc;

  const btns = document.getElementById('survey-verbosity-bar').children;
  for (let i = 0; i < btns.length; i++) {
    btns[i].className = 'survey-verbosity-btn' + (i <= surveyData.verbosity ? ' filled' : '');
    btns[i].textContent = i === surveyData.verbosity ? String(i) : '';
  }
}

function updateSpeechKeySection() {
  const provider = SURVEY_SPEECH_PROVIDERS.find((p) => p.id === surveyData.speechProvider);
  const section = document.getElementById('survey-speech-key-section');
  if (provider && provider.requiresKey) {
    section.style.display = 'block';
    const keyInput = document.getElementById('survey-speech-api-key');
    keyInput.placeholder = provider.keyPlaceholder || 'API Key';
    keyInput.value = surveyData.speechApiKey;
    keyInput.oninput = () => { surveyData.speechApiKey = keyInput.value; };
    document.getElementById('survey-speech-key-hint').textContent = provider.keyHint || '';
  } else {
    section.style.display = 'none';
  }
}

function getEffectiveSurveyPage(page) {
  // If not developer, skip page 6 (tech stack)
  if (surveyData.identity !== 'developer' && page >= 6) {
    return page + 1; // map page 6 -> page 7 (use case)
  }
  return page;
}

function updateSurveyUI() {
  const totalPages = getSurveyTotalPages();
  const effectivePage = getEffectiveSurveyPage(surveyPage);

  // Update dots
  const dotsContainer = document.getElementById('survey-dots');
  dotsContainer.innerHTML = '';
  for (let i = 0; i < totalPages; i++) {
    const dot = document.createElement('div');
    dot.className = 'survey-dot';
    const isCurrent = i === surveyPage;
    const isPast = i < surveyPage;
    dot.style.width = isCurrent ? '24px' : '16px';
    dot.style.backgroundColor = isCurrent ? '#e1e4e8' : isPast ? '#9ca3af' : '#2a2d3a';
    dotsContainer.appendChild(dot);
  }

  // Show correct page
  for (let i = 0; i <= 7; i++) {
    const el = document.getElementById('survey-page-' + i);
    if (el) el.classList.toggle('active', i === effectivePage);
  }

  // Show/hide bottom bar (hidden on page 0 since it has inline continue)
  const bottomBar = document.getElementById('survey-bottom-bar');
  bottomBar.style.display = surveyPage > 0 ? 'flex' : 'none';

  // Show skip after relay page (page 5 in actual pages, which is surveyPage 5)
  const skipBar = document.getElementById('survey-skip-bar');
  skipBar.style.display = surveyPage >= 6 ? 'block' : 'none';

  // Update next button text
  const nextBtn = document.getElementById('survey-btn-next');
  const isLast = surveyPage === totalPages - 1;
  nextBtn.textContent = surveyIsSubmitting ? '...' : isLast ? 'Finish' : 'Continue';

  updateSurveyButtons();
  updateSpeechKeySection();
}

function updateSurveyButtons() {
  const nextBtn = document.getElementById('survey-btn-next');
  let disabled = surveyIsSubmitting;

  if (surveyPage === 0 && !surveyData.fullName.trim()) disabled = true;
  if (surveyPage === 1 && !surveyData.identity) disabled = true;
  if (surveyPage === 2 && surveyData.selectedRunner === 'custom' && !surveyData.customCommand.trim()) disabled = true;

  nextBtn.disabled = disabled;
}

function surveyNext() {
  const totalPages = getSurveyTotalPages();
  if (surveyPage < totalPages - 1) {
    surveyPage++;
    updateSurveyUI();
  } else {
    finishSurvey();
  }
}

function surveyBack() {
  if (surveyPage > 0) {
    surveyPage--;
    updateSurveyUI();
  }
}

async function finishSurvey() {
  if (surveyIsSubmitting) return;
  surveyIsSubmitting = true;
  updateSurveyButtons();

  const isDev = surveyData.identity === 'developer';

  try {
    // Submit survey to Convex
    await window.yaver.submitSurvey({
      isDeveloper: isDev,
      fullName: surveyData.fullName.trim() || undefined,
      languages: isDev && surveyData.languages.length > 0 ? surveyData.languages : undefined,
      experienceLevel: isDev ? surveyData.experience || undefined : undefined,
      role: surveyData.identity || undefined,
      companySize: surveyData.companySize || undefined,
      useCase: surveyData.useCase || undefined,
    });

    // Save runner + speech preferences via agent API
    const agentSettings = { runnerId: surveyData.selectedRunner };
    if (surveyData.selectedRunner === 'custom' && surveyData.customCommand.trim()) {
      agentSettings.customRunnerCommand = surveyData.customCommand.trim();
    }
    agentSettings.speechProvider = surveyData.speechProvider || 'on-device';
    agentSettings.verbosity = surveyData.verbosity;

    if (surveyData.speechApiKey.trim()) {
      agentSettings.speechApiKey = surveyData.speechApiKey.trim();
    }

    // Save settings to agent
    const currentSettings = await window.yaver.getSettings();
    await window.yaver.saveSettings(Object.assign({}, currentSettings, agentSettings));

    // Switch runner if agent is running
    if (surveyData.selectedRunner) {
      await window.yaver.switchRunner(surveyData.selectedRunner);
    }

    // Read relay values from inputs
    surveyData.relayUrl = document.getElementById('survey-relay-url').value || '';
    surveyData.relayPassword = document.getElementById('survey-relay-password').value || '';
    surveyData.relayLabel = document.getElementById('survey-relay-label').value || '';

    // Save relay server if configured (skip if user opted out of relay)
    if (!surveyData.relayOptOut && surveyData.relayUrl.trim()) {
      const url = surveyData.relayUrl.trim().replace(/\/+$/, '');
      const host = url.replace(/^https?:\/\//, '').replace(/:\d+$/, '').replace(/\/.*$/, '');
      const relay = {
        id: generateId(),
        quic_addr: host + ':4433',
        http_url: url,
        region: surveyData.relayLabel.trim() || 'custom',
        priority: 0,
        password: surveyData.relayPassword.trim() || undefined,
      };
      const config = await window.yaver.getConfig();
      if (!config.relay_servers) config.relay_servers = [];
      config.relay_servers.push(relay);
      await window.yaver.saveConfig(config);
    }
  } catch (err) {
    // Continue even if survey submission fails
    console.error('Survey submission error:', err);
  }

  surveyIsSubmitting = false;

  // Hide survey, show main app
  document.getElementById('survey-overlay').classList.remove('active');
  showMainApp();
  loadConfig();
  loadSettingsUI();
  updateDashboard();
}

// ---------------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------------

init();
