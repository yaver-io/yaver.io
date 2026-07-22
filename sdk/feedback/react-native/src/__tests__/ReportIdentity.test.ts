import { resolveReportIdentity } from '../P2PClient';

// resolveAppIdentity() has always fed /vibing/execute and /dev/reload-app so
// the agent could route "vibe on THIS app" to the right repo, but nothing fed
// /feedback — reports carried no identity at all, so the agent's fix router
// had nothing to resolve and fell back to whatever directory it was sitting
// in. resolveReportIdentity() closes that gap for the feedback path.

jest.mock('react-native', () => ({
  Platform: { OS: 'ios' },
  NativeModules: {},
}));

/** Mirrors mobile/app.json in the Talos repo — the SDK's first external consumer. */
const EXPO_CONFIG = {
  name: 'Talos',
  slug: 'talos-mobile',
  version: '1.9.157',
  ios: { bundleIdentifier: 'works.talos.mobile', buildNumber: '427' },
  android: { package: 'works.talos.mobile', versionCode: 423 },
};

function mockExpoConstants(expoConfig: unknown, extra: Record<string, unknown> = {}) {
  jest.doMock('expo-constants', () => ({ default: { expoConfig, ...extra } }), { virtual: true });
}

beforeEach(() => {
  jest.resetModules();
});

describe('resolveReportIdentity', () => {
  it('resolves app name and bundle id from the Expo config', () => {
    mockExpoConstants(EXPO_CONFIG);
    const { resolveReportIdentity: resolve } = require('../P2PClient');

    const identity = resolve();

    // The agent's fix router reads DeviceInfo.AppName first.
    expect(identity.appName).toBe('Talos');
    // ...and resolves the project by bundle id, the only unambiguous key.
    expect(identity.project?.bundleId).toBe('works.talos.mobile');
    expect(identity.project?.projectName).toBe('Talos');
    expect(identity.project?.appName).toBe('Talos');
    expect(identity.project?.surface).toBe('mobile');
    expect(identity.app.bundleId).toBe('works.talos.mobile');
  });

  it('never reports a project path', () => {
    mockExpoConstants(EXPO_CONFIG);
    const { resolveReportIdentity: resolve } = require('../P2PClient');

    // The agent ignores client-supplied paths on feedback reports (an
    // untrusted guest could otherwise aim the fix task's CWD at ~/.ssh).
    // Sending one would be misleading at best.
    expect(resolve().project?.projectPath).toBeUndefined();
  });

  it('prefers the native runtime version over the manifest version', () => {
    mockExpoConstants(EXPO_CONFIG, { nativeAppVersion: '1.9.158', nativeBuildVersion: '428' });
    const { resolveReportIdentity: resolve } = require('../P2PClient');

    const identity = resolve();
    // A stale manifest shouldn't misreport which build is actually running.
    expect(identity.app.version).toBe('1.9.158');
    expect(identity.app.buildNumber).toBe('428');
  });

  it('falls back to the manifest version when no native version is exposed', () => {
    mockExpoConstants(EXPO_CONFIG);
    const { resolveReportIdentity: resolve } = require('../P2PClient');

    const identity = resolve();
    expect(identity.app.version).toBe('1.9.157');
    expect(identity.app.buildNumber).toBe('427');
  });

  // Inside Yaver's container the ambient runtime IS Yaver. These are the
  // cases where getting it wrong routes a fix task into yaver.io and edits
  // the SDK instead of the app under test.
  describe('inside the Yaver container (Hermes guest)', () => {
    const YAVER_CONFIG = {
      name: 'Yaver',
      version: '1.99.306',
      ios: { bundleIdentifier: 'io.yaver.mobile' },
      android: { package: 'io.yaver.mobile' },
    };

    function mockContainer(yaverInfo: Record<string, unknown>) {
      jest.doMock('react-native', () => ({
        Platform: { OS: 'ios' },
        NativeModules: { YaverInfo: { isYaver: true, ...yaverInfo } },
      }));
      // The host's manifest — what expo-constants answers for a guest bundle.
      mockExpoConstants(YAVER_CONFIG);
    }

    it("never reports the host's bundle id as the guest's", () => {
      mockContainer({ inheritedGuestProjectName: 'talos / mobile' });
      const { resolveReportIdentity: resolve } = require('../P2PClient');

      const identity = resolve();

      // The agent routes on bundle id FIRST. Leaking io.yaver.mobile here
      // would resolve to yaver.io and the project name would never be read.
      expect(identity.project?.bundleId).toBeUndefined();
      expect(identity.app.bundleId).toBeUndefined();
      // Nor should the host's version masquerade as the guest's build.
      expect(identity.app.version).toBeUndefined();
    });

    it('reports the guest project Yaver pinned', () => {
      mockContainer({ inheritedGuestProjectName: 'talos / mobile' });
      const { resolveReportIdentity: resolve } = require('../P2PClient');

      const identity = resolve();
      expect(identity.appName).toBe('talos / mobile');
      expect(identity.project?.projectName).toBe('talos / mobile');
    });

    it("lets the app's own declaration win over the pinned project", () => {
      mockContainer({ inheritedGuestProjectName: 'something-else' });
      const { resolveReportIdentity: resolve } = require('../P2PClient');

      // A bundle knows its own identity; nothing ambient should override it.
      const identity = resolve({ projectName: 'Talos', bundleId: 'works.talos.mobile' });
      expect(identity.appName).toBe('Talos');
      expect(identity.project?.bundleId).toBe('works.talos.mobile');
    });

    it('reports nothing rather than something wrong when no project is pinned', () => {
      mockContainer({});
      const { resolveReportIdentity: resolve } = require('../P2PClient');

      // Better for the agent to reject/fall back than to confidently edit
      // the wrong repo.
      const identity = resolve();
      expect(identity.project).toBeUndefined();
      expect(identity.appName).toBeUndefined();
      expect(identity.app).toEqual({});
    });
  });

  it('lets an explicit declaration override the ambient identity', () => {
    mockExpoConstants(EXPO_CONFIG);
    const { resolveReportIdentity: resolve } = require('../P2PClient');

    const identity = resolve({ projectName: 'Override', bundleId: 'com.override.app' });
    expect(identity.appName).toBe('Override');
    expect(identity.project?.bundleId).toBe('com.override.app');
  });

  it('passes declared surfaces stacks and voice metadata through', () => {
    mockExpoConstants(EXPO_CONFIG);
    const { resolveReportIdentity: resolve } = require('../P2PClient');

    const identity = resolve({
      projectName: 'Omni',
      bundleId: 'io.example.omni',
      surface: 'vision',
      surfaces: ['mobile', 'web', 'watch', 'tv', 'car', 'vision'],
      stack: 'react-native-expo',
      stacks: ['react-native-expo', 'nextjs', 'yaver-xml'],
      testSurfaces: ['rn-hermes', 'browser', 'visionos-simulator'],
      feedbackSdk: 'yaver-feedback-react-native',
      feedbackTransport: 'device-sdk',
      voiceCapabilities: ['stt', 'tts', 'device-mic'],
      sttProvider: 'deepgram',
      ttsProvider: 'local',
    });

    expect(identity.project).toMatchObject({
      surface: 'vision',
      surfaces: ['mobile', 'web', 'watch', 'tv', 'car', 'vision'],
      stack: 'react-native-expo',
      stacks: ['react-native-expo', 'nextjs', 'yaver-xml'],
      testSurfaces: ['rn-hermes', 'browser', 'visionos-simulator'],
      feedbackSdk: 'yaver-feedback-react-native',
      feedbackTransport: 'device-sdk',
      voiceCapabilities: ['stt', 'tts', 'device-mic'],
      sttProvider: 'deepgram',
      ttsProvider: 'local',
    });
    expect(identity.project?.projectPath).toBeUndefined();
  });

  it('omits the project block when nothing identifies the app', () => {
    // Bare RN with no expo-constants and no native modules. The report must
    // still upload — the agent just resolves it by its own means.
    jest.doMock(
      'expo-constants',
      () => {
        throw new Error('not installed');
      },
      { virtual: true },
    );
    const { resolveReportIdentity: resolve } = require('../P2PClient');

    const identity = resolve();
    expect(identity.project).toBeUndefined();
    expect(identity.appName).toBeUndefined();
    expect(identity.app).toEqual({});
  });
});
