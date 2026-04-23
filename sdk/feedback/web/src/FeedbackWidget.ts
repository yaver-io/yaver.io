import { YaverFeedback } from './YaverFeedback';
import { YaverDiscovery } from './discovery';

/**
 * FeedbackWidget — a connection + feedback UI panel for web apps.
 *
 * Provides dev machine discovery, connection status, and feedback controls.
 * Renders as a small panel that developers can embed in their dev tools or settings page.
 *
 * @example
 * ```ts
 * import { FeedbackWidget } from '@yaver/feedback-web';
 *
 * // Mount in your dev tools panel
 * FeedbackWidget.mount(document.getElementById('yaver-panel'));
 * ```
 */
export class FeedbackWidget {
  private static container: HTMLElement | null = null;

  /**
   * Mount the feedback widget into a DOM element.
   * Shows: connection status, agent URL input, connect button, feedback controls.
   */
  static mount(element: HTMLElement): void {
    FeedbackWidget.container = element;
    FeedbackWidget.render();
  }

  /** Unmount and clean up the widget. */
  static unmount(): void {
    if (FeedbackWidget.container) {
      FeedbackWidget.container.innerHTML = '';
      FeedbackWidget.container = null;
    }
  }

  private static async render(): Promise<void> {
    if (!FeedbackWidget.container) return;

    const stored = YaverDiscovery.getStored();
    const connected = stored !== null;

    FeedbackWidget.container.innerHTML = `
      <div style="font-family:-apple-system,sans-serif;background:#1a1a2e;color:#e0e0e0;padding:16px;border-radius:8px;font-size:13px;">
        <div style="display:flex;align-items:center;gap:8px;margin-bottom:12px;">
          <div style="width:8px;height:8px;border-radius:50%;background:${connected ? '#22c55e' : '#ef4444'};"></div>
          <span style="font-weight:600;">${connected ? `Connected: ${stored.hostname}` : 'Not connected'}</span>
        </div>

        <div style="margin-bottom:12px;">
          <input id="yaver-widget-url" type="text" placeholder="Agent URL (e.g., http://192.168.1.100:18080)"
            value="${stored?.url || ''}"
            style="width:100%;padding:8px;border:1px solid #333;border-radius:6px;background:#0d0d1a;color:#e0e0e0;font-size:12px;box-sizing:border-box;" />
        </div>

        <div style="display:flex;gap:8px;margin-bottom:12px;">
          <button id="yaver-widget-discover" style="flex:1;padding:8px;border:none;border-radius:6px;background:#2563eb;color:white;cursor:pointer;font-size:12px;">
            Auto-discover
          </button>
          <button id="yaver-widget-connect" style="flex:1;padding:8px;border:none;border-radius:6px;background:#16a34a;color:white;cursor:pointer;font-size:12px;">
            Connect
          </button>
        </div>

        ${connected ? `
        <div style="border-top:1px solid #333;padding-top:12px;">
          <div style="font-weight:600;margin-bottom:8px;">Feedback</div>
          <div style="display:flex;flex-direction:column;gap:6px;">
            <button id="yaver-widget-report" style="padding:8px;border:none;border-radius:6px;background:#dc2626;color:white;cursor:pointer;font-size:12px;">
              Start Bug Report
            </button>
            <button id="yaver-widget-reload" style="padding:8px;border:none;border-radius:6px;background:#7c3aed;color:white;cursor:pointer;font-size:12px;">
              Hot Reload
            </button>
            <textarea id="yaver-widget-vibe-prompt" placeholder="Tell Yaver what to work on..." style="min-height:72px;padding:8px;border:1px solid #333;border-radius:6px;background:#0d0d1a;color:#e0e0e0;font-size:12px;box-sizing:border-box;resize:vertical;"></textarea>
            <button id="yaver-widget-vibe" style="padding:8px;border:none;border-radius:6px;background:#0891b2;color:white;cursor:pointer;font-size:12px;">
              Start Vibing
            </button>
          </div>
        </div>
        ` : ''}

        <div id="yaver-widget-status" style="margin-top:8px;font-size:11px;color:#666;"></div>
      </div>
    `;

    // Wire up buttons
    const discoverBtn = FeedbackWidget.container.querySelector('#yaver-widget-discover') as HTMLButtonElement;
    const connectBtn = FeedbackWidget.container.querySelector('#yaver-widget-connect') as HTMLButtonElement;
    const urlInput = FeedbackWidget.container.querySelector('#yaver-widget-url') as HTMLInputElement;
    const statusEl = FeedbackWidget.container.querySelector('#yaver-widget-status') as HTMLElement;
    const reportBtn = FeedbackWidget.container.querySelector('#yaver-widget-report') as HTMLButtonElement | null;
    const reloadBtn = FeedbackWidget.container.querySelector('#yaver-widget-reload') as HTMLButtonElement | null;
    const vibeBtn = FeedbackWidget.container.querySelector('#yaver-widget-vibe') as HTMLButtonElement | null;
    const vibePrompt = FeedbackWidget.container.querySelector('#yaver-widget-vibe-prompt') as HTMLTextAreaElement | null;

    discoverBtn.onclick = async () => {
      statusEl.textContent = 'Scanning network...';
      const result = await YaverDiscovery.discover();
      if (result) {
        urlInput.value = result.url;
        statusEl.textContent = `Found: ${result.hostname} (${result.latency}ms)`;
        FeedbackWidget.render();
      } else {
        statusEl.textContent = 'No agent found. Enter URL manually.';
      }
    };

    connectBtn.onclick = async () => {
      const url = urlInput.value.trim();
      if (!url) { statusEl.textContent = 'Enter a URL'; return; }
      statusEl.textContent = 'Connecting...';
      const result = await YaverDiscovery.connect(url);
      if (result) {
        statusEl.textContent = `Connected to ${result.hostname}`;
        // Update YaverFeedback config
        if (YaverFeedback.isInitialized) {
          // Re-init with new URL
        }
        FeedbackWidget.render();
      } else {
        statusEl.textContent = 'Connection failed. Check URL and ensure "yaver serve" is running.';
      }
    };

    if (reportBtn) {
      reportBtn.onclick = () => YaverFeedback.startReport();
    }
    if (reloadBtn) {
      reloadBtn.onclick = async () => {
        statusEl.textContent = 'Requesting reload...';
        try {
          const ack = await YaverFeedback.reloadApp('dev');
          statusEl.textContent = ack.message;
        } catch (err) {
          statusEl.textContent = err instanceof Error ? err.message : 'Reload failed.';
        }
      };
    }
    if (vibeBtn && vibePrompt) {
      vibeBtn.onclick = async () => {
        const prompt = vibePrompt.value.trim();
        if (!prompt) {
          statusEl.textContent = 'Enter a vibing prompt first.';
          return;
        }
        statusEl.textContent = 'Checking vibing access...';
        try {
          const eligibility = await YaverFeedback.getVibingEligibility();
          if (!eligibility.canVibe) {
            statusEl.textContent =
              eligibility.guidance && eligibility.guidance.trim()
                ? `${eligibility.reason ?? 'Vibing unavailable.'} ${eligibility.guidance}`
                : eligibility.reason ?? 'Vibing unavailable.';
            return;
          }
          const result = await YaverFeedback.vibing(prompt);
          statusEl.textContent = `Vibing task created: ${result.taskId}`;
        } catch (err) {
          statusEl.textContent = err instanceof Error ? err.message : 'Vibing failed.';
        }
      };
    }
  }
}
