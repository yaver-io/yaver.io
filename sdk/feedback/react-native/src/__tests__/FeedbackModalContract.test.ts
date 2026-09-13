import { readFileSync } from 'fs';
import { join } from 'path';
import {
  FEEDBACK_DOGFOOD_CONSOLE_COLORS,
  FEEDBACK_DOGFOOD_LIGHT_COLORS,
} from '../FeedbackModalTheme';

const SOURCE_PATH = join(__dirname, '..', 'FeedbackModal.tsx');

describe('FeedbackModal authenticated chat contract', () => {
  const source = readFileSync(SOURCE_PATH, 'utf8');

  it('themes every shared Dogfood surface for the light feedback sheet', () => {
    expect(source).toMatch(/<DogfoodStatusRail[\s\S]*?colors=\{FEEDBACK_DOGFOOD_LIGHT_COLORS\}/);
    expect(source).toMatch(/<DogfoodLanePicker[\s\S]*?colors=\{FEEDBACK_DOGFOOD_LIGHT_COLORS\}/);
    expect(source).toMatch(/<DogfoodLiveConsole[\s\S]*?colors=\{FEEDBACK_DOGFOOD_CONSOLE_COLORS\}/);
  });

  it('keeps normal light-sheet copy at WCAG AA contrast', () => {
    const rgb = (hex: string) => hex.match(/[a-f\d]{2}/gi)!.map((part) => parseInt(part, 16) / 255);
    const luminance = (hex: string) => {
      const channels = rgb(hex).map((channel) => channel <= 0.03928
        ? channel / 12.92
        : ((channel + 0.055) / 1.055) ** 2.4);
      return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
    };
    const ratio = (foreground: string, background: string) => {
      const [lighter, darker] = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
      return (lighter + 0.05) / (darker + 0.05);
    };

    for (const foreground of [
      FEEDBACK_DOGFOOD_LIGHT_COLORS.text,
      FEEDBACK_DOGFOOD_LIGHT_COLORS.muted,
      FEEDBACK_DOGFOOD_LIGHT_COLORS.accent,
      FEEDBACK_DOGFOOD_LIGHT_COLORS.ready,
      FEEDBACK_DOGFOOD_LIGHT_COLORS.attention,
      FEEDBACK_DOGFOOD_LIGHT_COLORS.blocked,
    ]) {
      expect(ratio(foreground, FEEDBACK_DOGFOOD_LIGHT_COLORS.background)).toBeGreaterThanOrEqual(4.5);
    }
    expect(ratio(FEEDBACK_DOGFOOD_CONSOLE_COLORS.text, FEEDBACK_DOGFOOD_CONSOLE_COLORS.console))
      .toBeGreaterThanOrEqual(4.5);
    expect(ratio(FEEDBACK_DOGFOOD_CONSOLE_COLORS.muted, FEEDBACK_DOGFOOD_CONSOLE_COLORS.console))
      .toBeGreaterThanOrEqual(4.5);
  });

  it('uses Chat as the authenticated entry surface without legacy command buttons', () => {
    expect(source).toContain("useState<'chat' | 'settings'>('chat')");
    expect(source).toContain('setShowVibeInput(authenticated || directDogfood)');
    expect(source).not.toContain('Screenshot & Fix');
    expect(source).not.toContain('<DeployPanel');
  });

  it('opens explicit Dogfood onboarding on setup and makes the runtime console the first live surface', () => {
    expect(source).toContain("setActiveTab('settings')");
    expect(source).toContain("setDogfoodSetupStage('runtime')");
    expect(source).toMatch(/dogfoodSetupStage === 'runtime'[\s\S]*?<DogfoodLiveConsole/);
    expect(source).toContain('startDogfoodRuntime');
    expect(source).toContain('Opening dogfooded app…');
    expect(source).toContain('if (dogfoodRuntime.result?.url) await Linking.openURL(dogfoodRuntime.result.url)');
    expect(source).not.toContain('Open dogfooded app');
    expect(source).toContain("dogfoodRuntime?.phase !== 'ready'");
    expect(source).not.toContain('Continue in app');
    expect(source).toContain("['preparing', 'starting', 'compiling'].includes(dogfoodRuntime.phase) ? 'Stop' : 'Change'");
    expect(source).toContain('void dogfoodControllerRef.current?.stop()');
  });

  it('keeps Dogfood setup to box, runner, and checkout before asking for a runtime', () => {
    const setupSteps = source.match(/const dogfoodSetupSteps = \[([\s\S]*?)\n  \];/)?.[1] || '';
    expect([...setupSteps.matchAll(/key: '([^']+)'/g)].map((match) => match[1]))
      .toEqual(['box', 'runner', 'checkout']);
    expect(setupSteps).not.toContain("key: 'oauth'");
    expect(setupSteps).not.toContain("key: 'installation'");
    expect(setupSteps).not.toContain("key: 'model'");
    expect(setupSteps).not.toContain("key: 'lane'");
    expect(source).toContain('Your saved choices are ready. No second tap is needed.');
    expect(source).toContain("dogfoodSetupStage !== 'setup'");
    expect(source).not.toContain("label={dogfoodSetupReady ? 'Launch Dogfood' : 'Complete the choices above'}");
    expect(source).not.toContain("type DogfoodSetupStage = 'setup' | 'lane' | 'runtime'");
    expect(source).toContain('<DogfoodLanePicker');
    expect(source).toContain('Browser Logs open first');
    expect(source).toContain('{!dogfoodOnboarding ? <>');
    expect(source).toContain("? `Set up ${dogfoodOnboarding.projectName || dogfoodOnboarding.label || 'this app'} Dogfood`");
  });

  it('passes the selected native target and labels the live log source', () => {
    expect(source).toContain("nativeTargetId: lanePlan.preferred === 'webrtc' ? dogfoodNativeTargetId : undefined");
    expect(source).toContain('fallbackLane: lanePlan.fallback');
    expect(source).toContain('fallbackLane={dogfoodLanePolicy.fallback}');
    expect(source).toMatch(/<DogfoodLiveConsole[\s\S]*?sourceLabel=\{dogfoodSourceLabel\}/);
    expect(source).toContain('Simulator, emulator, or device');
  });

  it('has one keyboard inset owner and no gesture-stealing sheet Pressable', () => {
    expect(source).toContain("behavior={Platform.OS === 'ios' ? 'padding' : 'height'}");
    expect(source).toContain('automaticallyAdjustKeyboardInsets={false}');
    expect(source).toContain('<KeyboardAvoidingView');
    expect(source).not.toContain('keyboardInset');
    expect(source).toContain('<Pressable style={styles.backdrop} onPress={handleClose}');
    expect(source).toMatch(/<View[\s\S]{0,300}?style=\{\[\s*styles\.modal/);
  });
});
