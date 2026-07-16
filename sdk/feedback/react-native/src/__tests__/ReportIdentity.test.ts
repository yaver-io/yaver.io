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
