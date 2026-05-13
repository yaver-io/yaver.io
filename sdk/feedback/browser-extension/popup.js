/* eslint-disable no-undef */

const $ = (id) => document.getElementById(id);

function bg(msg) {
  return new Promise((resolve) => chrome.runtime.sendMessage(msg, (r) => resolve(r || {})));
}

async function activeTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  return tab;
}

async function load() {
  const s = await bg({ type: 'yaver:get-settings' });
  $('agent-url').value = s.agentUrl || '';
  $('auth-token').value = s.authToken || '';
  $('auto-send').checked = s.autoSend !== false;
  pingAgent();
}

async function save() {
  await bg({
    type: 'yaver:set-settings',
    settings: {
      agentUrl: $('agent-url').value.trim(),
      authToken: $('auth-token').value.trim(),
      autoSend: $('auto-send').checked,
      kind: 'design-reference',
    },
  });
  pingAgent();
}

async function pingAgent() {
  const dot = $('status-dot');
  dot.className = 'dot';
  const r = await bg({ type: 'yaver:ping-agent' });
  dot.className = `dot ${r.ok ? 'ok' : 'bad'}`;
  dot.title = r.ok ? `Connected to ${r.agentUrl}` : `Unreachable: ${r.agentUrl} (${r.error || r.status})`;
  loadRecent(r.ok);
}

async function loadRecent(agentOk) {
  const container = $('recent');
  if (!agentOk) {
    container.innerHTML = '<div class="recent-empty">Agent unreachable.</div>';
    return;
  }
  const r = await bg({ type: 'yaver:list-references' });
  if (!r.ok || !Array.isArray(r.items) || r.items.length === 0) {
    container.innerHTML = '<div class="recent-empty">No captures yet.</div>';
    return;
  }
  container.innerHTML = '';
  for (const item of r.items.slice(0, 8)) {
    const row = document.createElement('div');
    row.className = 'recent-item';
    const url = document.createElement('span');
    url.className = 'url';
    url.textContent = item.title || item.url || item.id;
    url.title = item.url || item.id;
    const mode = document.createElement('span');
    mode.className = 'mode';
    mode.textContent = item.mode || '—';
    row.appendChild(url);
    row.appendChild(mode);
    container.appendChild(row);
  }
}

async function sendToContent(type) {
  const tab = await activeTab();
  if (!tab?.id) return;
  chrome.tabs.sendMessage(tab.id, { type });
  window.close();
}

$('capture-element').addEventListener('click', () => sendToContent('yaver:start-element-pick'));
$('capture-page').addEventListener('click', () => sendToContent('yaver:capture-page-now'));
$('capture-fullpage').addEventListener('click', () => sendToContent('yaver:capture-fullpage-now'));
$('save').addEventListener('click', save);

// Show platform-correct shortcut hints.
if (!navigator.platform.toLowerCase().includes('mac')) {
  $('kbd-elem').textContent = 'Ctrl+Shift+Y';
  $('kbd-page').textContent = 'Ctrl+Shift+P';
}

load();
