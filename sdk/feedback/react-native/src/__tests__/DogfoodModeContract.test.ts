import { readFileSync } from 'fs';
import { join } from 'path';

describe('Dogfood Settings and Usage contract', () => {
  it('exports separate embeddable settings and usage surfaces', () => {
    const index = readFileSync(join(__dirname, '../index.ts'), 'utf8');
    expect(index).toContain('DogfoodSettings');
    expect(index).toContain('DogfoodUsage');
  });

  it('keeps the compact card focused on Chat, Reload, Settings, Exit, and hiding Y', () => {
    const usage = readFileSync(join(__dirname, '../DogfoodQuickControls.tsx'), 'utf8');
    expect(usage).toContain("usageMode !== 'reload-only'");
    expect(usage).toContain("usageMode !== 'chat-only'");
    expect(usage).toContain('yaver-dogfood-chat');
    expect(usage).toContain('yaver-dogfood-fast-reload');
    expect(usage).toContain('yaver-dogfood-settings');
    expect(usage).toContain('yaver-dogfood-hide');
    expect(usage).toContain('yaver-dogfood-exit');
    expect(usage).toContain('YaverFeedback.exitDogfoodMode()');
    expect(usage).toContain('yaverFeedback:dogfoodUsageRequested');
    expect(usage).toContain("DeviceEventEmitter.emit('yaverFeedback:dogfoodNewChatRequested'");
    expect(usage).toContain('if (state.authorized)');
    expect(usage).not.toContain('DogfoodSettings');
    expect(usage).not.toContain('Update Yaver agent');
    expect(usage).not.toContain('Back to native app');
    expect(usage).not.toContain('Session setup');
    expect(usage).toContain('getDogfoodEntryIconHidden');
    expect(usage).toContain('setDogfoodEntryIconVisible(false)');
    expect(usage).toContain('setDogfoodEntryIconVisible(true)');
    expect(usage).toContain("entryIconHidden ? 'Show Y' : 'Hide Y'");
    expect(usage).not.toContain("{'🧪'}");
  });

  it('auto-launches the shared native menu and removes chat in Reload Only mode', () => {
    const menu = readFileSync(join(__dirname, '../DogfoodNativeMenu.tsx'), 'utf8');
    expect(menu).toContain('onLaunch();');
    expect(menu).toContain('Launching Dogfood…');
    expect(menu).toContain("usageMode !== 'reload-only'");
    expect(menu).not.toContain('testID="dogfood-native-launch"');
  });

  it('renders only the draggable Y while standalone Dogfood is active', () => {
    const modal = readFileSync(join(__dirname, '../FeedbackModal.tsx'), 'utf8');
    expect(modal).toContain('if (dogfood.active) return null');
    expect(modal).toContain('<DogfoodQuickControls suppressed={visible} />');
  });

  it('states that both choices use OAuth and installation approval', () => {
    const settings = readFileSync(join(__dirname, '../DogfoodSettings.tsx'), 'utf8');
    expect(settings).toContain('full Yaver OAuth account and approved installation');
    expect(settings).toContain("accessibilityLabel=\"Dogfood Settings\"");
    expect(settings).toContain("{'🧪'}");
    expect(settings).toContain('Chat Only');
    expect(settings).toContain('Reload Only');
    expect(settings).toContain('Reload + Chat');
    expect(settings).toContain('Configure box, runner, checkout & lane');
    expect(settings).toContain('Sign out of Yaver');
    expect(settings).toContain('Version unavailable');
    expect(settings).toContain('Dogfood mode');
    expect(settings).toContain('Native mode');
    expect(settings).not.toContain('Start & render');
    expect(settings).not.toContain('Runner sessions');
  });

  it('restores durable sessions without globally locking other topics', () => {
    const chat = readFileSync(join(__dirname, '../VibeChatScreen.tsx'), 'utf8');
    expect(chat).toContain('Each topic owns its own runner/tmux seat');
    expect(chat).toContain("const codingLocked = status === 'running'");
    expect(chat).toContain("event.type !== 'runtime_render_requested'");
    expect(chat).toContain("renderBehavior === 'auto-on-request'");
    expect(chat).toContain('transport silence can never look like');
    expect(chat).toContain('client.getVibeThread(taskId)');
  });

  it('targets the current app command channel and requires an exact checkout', () => {
    const feedback = readFileSync(join(__dirname, '../YaverFeedback.ts'), 'utf8');
    expect(feedback).toContain("if (!selection.projectPath?.trim())");
    expect(feedback).toContain('BlackBox.currentDeviceId');
    expect(feedback).toContain('BlackBox.isCommandChannelConnected');
    expect(feedback).toContain('restoreApprovedDogfoodMode');
    expect(feedback).toContain('setDogfoodModeActive(true, options.appId)');
    expect(feedback).toContain('setDogfoodModeActive(false, cfg.appId || config?.bundleId)');
    expect(feedback).toContain('setDogfoodModeActive(false, config?.dogfood?.appId || config?.bundleId)');
  });

  it('reloads browser-lane Reload Only through the selected checkout, not a native command channel', () => {
    const feedback = readFileSync(join(__dirname, '../YaverFeedback.ts'), 'utf8');
    const modal = readFileSync(join(__dirname, '../FeedbackModal.tsx'), 'utf8');
    expect(feedback).toContain("if (selection.lane === 'hermes' && (!BlackBox.isStreaming");
    expect(feedback).toContain("const reloadSelection = selection.lane === 'hermes'");
    expect(feedback).toContain('client.reloadDogfood({');
    expect(modal).toContain('await YaverFeedback.requestDogfoodFastReload();');
  });

  it('opens chat without starting a renderer and restores or creates a session', () => {
    const feedback = readFileSync(join(__dirname, '../YaverFeedback.ts'), 'utf8');
    expect(feedback).toContain("getDogfoodSessionBehavior() !== 'resume-last'");
    expect(feedback).toContain('Promise.race([');
    expect(feedback).toContain('openDogfoodSession(resume.id)');
    expect(feedback).toContain("DeviceEventEmitter.emit('yaverFeedback:dogfoodNewChatRequested', {");
  });
});
