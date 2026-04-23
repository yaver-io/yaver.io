import {
  listReachableDevices,
  saveSelectedDeviceId,
  type RemoteDevice,
} from './auth';

const STYLE_ID = 'yaver-feedback-device-picker-style';

const CSS = `
.yvr-fb-device-overlay {
  position: fixed;
  inset: 0;
  z-index: 2147483646;
  background: rgba(10, 10, 18, 0.65);
  backdrop-filter: blur(6px);
  display: flex;
  align-items: center;
  justify-content: center;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  color: #e5e7eb;
}
.yvr-fb-device-card {
  width: min(420px, calc(100vw - 24px));
  max-height: min(520px, calc(100vh - 32px));
  overflow: auto;
  background: #111827;
  border: 1px solid rgba(255,255,255,0.08);
  border-radius: 14px;
  padding: 20px;
  box-shadow: 0 24px 60px rgba(0,0,0,0.45);
}
.yvr-fb-device-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}
.yvr-fb-device-title { font-size: 15px; font-weight: 700; }
.yvr-fb-device-sub { font-size: 12px; color: #9ca3af; margin-top: 3px; }
.yvr-fb-device-close {
  border: none;
  background: none;
  color: #9ca3af;
  cursor: pointer;
  font: inherit;
}
.yvr-fb-device-groups { display: grid; gap: 10px; }
.yvr-fb-device-group-label {
  font-size: 11px;
  color: #6b7280;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.yvr-fb-device-list { display: grid; gap: 8px; }
.yvr-fb-device-row {
  width: 100%;
  text-align: left;
  border: 1px solid rgba(255,255,255,0.08);
  background: rgba(255,255,255,0.04);
  color: inherit;
  border-radius: 10px;
  padding: 10px 12px;
  cursor: pointer;
}
.yvr-fb-device-row:hover { background: rgba(255,255,255,0.08); }
.yvr-fb-device-row:disabled { opacity: 0.55; cursor: not-allowed; }
.yvr-fb-device-name { font-size: 13px; font-weight: 600; }
.yvr-fb-device-meta { font-size: 11px; color: #9ca3af; margin-top: 4px; }
.yvr-fb-device-empty { font-size: 12px; color: #9ca3af; }
.yvr-fb-device-error { margin-top: 12px; font-size: 12px; color: #f87171; }
`;

function injectStyles(): void {
  if (typeof document === 'undefined') return;
  if (document.getElementById(STYLE_ID)) return;
  const style = document.createElement('style');
  style.id = STYLE_ID;
  style.textContent = CSS;
  document.head.appendChild(style);
}

export async function openDevicePickerModal(token: string): Promise<RemoteDevice> {
  if (typeof document === 'undefined') {
    throw new Error('DevicePickerModal requires a browser');
  }
  injectStyles();
  const devices = await listReachableDevices(token);
  const owned = devices.owned;
  const shared = devices.shared;
  if (owned.length === 0 && shared.length === 0) {
    throw new Error('No reachable Yaver machines found for this account.');
  }

  return new Promise<RemoteDevice>((resolve, reject) => {
    const overlay = document.createElement('div');
    overlay.className = 'yvr-fb-device-overlay';

    const card = document.createElement('div');
    card.className = 'yvr-fb-device-card';
    overlay.appendChild(card);

    const close = (error?: Error) => {
      overlay.remove();
      if (error) reject(error);
    };

    const select = (device: RemoteDevice) => {
      saveSelectedDeviceId(device.deviceId);
      overlay.remove();
      resolve(device);
    };

    const renderGroup = (label: string, group: RemoteDevice[]): string => {
      if (group.length === 0) {
        return `
          <div class="yvr-fb-device-group">
            <div class="yvr-fb-device-group-label">${label}</div>
            <div class="yvr-fb-device-empty">None</div>
          </div>
        `;
      }
      return `
        <div class="yvr-fb-device-group">
          <div class="yvr-fb-device-group-label">${label}</div>
          <div class="yvr-fb-device-list">
            ${group
              .map((device) => {
                const online = device.isOnline && !device.runnerDown && !device.needsAuth;
                const state = online ? 'Online' : device.needsAuth ? 'Needs auth' : 'Offline';
                const host =
                  device.isGuest && device.hostName ? `Shared by ${device.hostName}` : device.platform;
                return `
                  <button
                    class="yvr-fb-device-row"
                    data-device-id="${device.deviceId}"
                    ${online ? '' : 'disabled'}
                  >
                    <div class="yvr-fb-device-name">${escapeHtml(device.name || device.deviceId)}</div>
                    <div class="yvr-fb-device-meta">${escapeHtml(host || 'Unknown')} • ${escapeHtml(state)}</div>
                  </button>
                `;
              })
              .join('')}
          </div>
        </div>
      `;
    };

    card.innerHTML = `
      <div class="yvr-fb-device-header">
        <div>
          <div class="yvr-fb-device-title">Choose a Yaver machine</div>
          <div class="yvr-fb-device-sub">Reload, vibing, and feedback go to the selected agent.</div>
        </div>
        <button class="yvr-fb-device-close" type="button">Cancel</button>
      </div>
      <div class="yvr-fb-device-groups">
        ${renderGroup('Your Machines', owned)}
        ${renderGroup('Shared With You', shared)}
      </div>
      <div class="yvr-fb-device-error" style="display:none;"></div>
    `;

    card.querySelector<HTMLButtonElement>('.yvr-fb-device-close')!.onclick = () =>
      close(new Error('cancelled'));
    card.querySelectorAll<HTMLButtonElement>('[data-device-id]').forEach((button) => {
      button.onclick = () => {
        const deviceId = button.dataset.deviceId;
        const device = [...owned, ...shared].find((candidate) => candidate.deviceId === deviceId);
        if (!device) return;
        select(device);
      };
    });

    overlay.addEventListener('click', (event) => {
      if (event.target === overlay) close(new Error('cancelled'));
    });

    document.body.appendChild(overlay);
  });
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}
