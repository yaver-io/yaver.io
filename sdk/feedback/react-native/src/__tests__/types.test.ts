import type {
  FeedbackConfig,
  FeedbackBundle,
  FeedbackMetadata,
  TimelineEvent,
  DeviceInfo,
  AppInfo,
  FeedbackReport,
  FeedbackStreamEvent,
} from '../types';

describe('React Native SDK types', () => {
  describe('FeedbackConfig', () => {
    it('can be constructed with only required fields', () => {
      const config: FeedbackConfig = {
        authToken: 'test-token',
      };
      expect(config.authToken).toBe('test-token');
      expect(config.agentUrl).toBeUndefined();
      expect(config.trigger).toBeUndefined();
      expect(config.enabled).toBeUndefined();
      expect(config.maxRecordingDuration).toBeUndefined();
    });

    it('can be constructed with all optional fields', () => {
      const config: FeedbackConfig = {
        authToken: 'tok',
        agentUrl: 'http://192.168.1.10:18080',
        trigger: 'shake',
        disableShakeGesture: true,
        enabled: true,
        maxRecordingDuration: 60,
        strictNativeAuth: true,
      };
      expect(config.trigger).toBe('shake');
      expect(config.disableShakeGesture).toBe(true);
      expect(config.strictNativeAuth).toBe(true);
    });

    it('accepts all trigger types', () => {
      const triggers: FeedbackConfig['trigger'][] = ['shake', 'floating-button', 'manual'];
      triggers.forEach((trigger) => {
        const config: FeedbackConfig = { authToken: 'tok', trigger };
        expect(config.trigger).toBe(trigger);
      });
    });
  });

  describe('FeedbackBundle', () => {
    it('can be constructed with required fields', () => {
      const bundle: FeedbackBundle = {
        metadata: {
          timestamp: '2026-03-24T12:00:00Z',
          deviceInfo: {
            platform: 'ios',
            osVersion: '18.0',
            model: 'iPhone 16 Pro',
            screenWidth: 393,
            screenHeight: 852,
          },
          app: {
            bundleId: 'com.example.app',
            version: '1.0.0',
            buildNumber: '42',
          },
        },
        screenshots: [],
      };

      expect(bundle.metadata.timestamp).toBe('2026-03-24T12:00:00Z');
      expect(bundle.metadata.deviceInfo.platform).toBe('ios');
      expect(bundle.screenshots).toEqual([]);
      expect(bundle.video).toBeUndefined();
    });

    it('can include optional video + screenshots', () => {
      const bundle: FeedbackBundle = {
        metadata: {
          timestamp: '2026-03-24T12:00:00Z',
          deviceInfo: {
            platform: 'android',
            osVersion: '15',
            model: 'Pixel 9',
            screenWidth: 412,
            screenHeight: 915,
          },
          app: {},
          userNote: 'This button does not work',
        },
        video: '/tmp/recording.mp4',
        screenshots: ['/tmp/ss1.png', '/tmp/ss2.png'],
      };

      expect(bundle.video).toBe('/tmp/recording.mp4');
      expect(bundle.screenshots).toHaveLength(2);
      expect(bundle.metadata.userNote).toBe('This button does not work');
    });
  });

  describe('TimelineEvent', () => {
    it('has correct structure for screenshot event', () => {
      const event: TimelineEvent = {
        type: 'screenshot',
        path: '/tmp/screenshot.png',
        timestamp: '2026-03-24T12:00:05Z',
      };
      expect(event.type).toBe('screenshot');
      expect(event.path).toBe('/tmp/screenshot.png');
      expect(event.duration).toBeUndefined();
    });

    it('supports optional duration for audio/video events', () => {
      const event: TimelineEvent = {
        type: 'audio',
        path: '/tmp/voice.m4a',
        timestamp: '2026-03-24T12:00:10Z',
        duration: 15.5,
      };
      expect(event.duration).toBe(15.5);
    });

    it('accepts all event types', () => {
      const types: TimelineEvent['type'][] = ['screenshot', 'audio', 'video'];
      types.forEach((type) => {
        const event: TimelineEvent = { type, path: '/tmp/file', timestamp: 'now' };
        expect(event.type).toBe(type);
      });
    });
  });

  describe('DeviceInfo', () => {
    it('has all required fields', () => {
      const device: DeviceInfo = {
        platform: 'ios',
        osVersion: '18.0',
        model: 'iPhone 16 Pro Max',
        screenWidth: 430,
        screenHeight: 932,
      };
      expect(device.platform).toBe('ios');
      expect(device.screenWidth).toBe(430);
      expect(device.screenHeight).toBe(932);
    });
  });

  describe('AppInfo', () => {
    it('can be empty (all fields optional)', () => {
      const app: AppInfo = {};
      expect(app.bundleId).toBeUndefined();
      expect(app.version).toBeUndefined();
      expect(app.buildNumber).toBeUndefined();
    });

    it('can have all fields populated', () => {
      const app: AppInfo = {
        bundleId: 'com.yaver.test',
        version: '2.0.0',
        buildNumber: '100',
      };
      expect(app.bundleId).toBe('com.yaver.test');
    });
  });

  describe('FeedbackReport', () => {
    it('has correct structure', () => {
      const report: FeedbackReport = {
        id: 'report-123',
        bundle: {
          metadata: {
            timestamp: 'now',
            deviceInfo: { platform: 'ios', osVersion: '18', model: 'iPhone', screenWidth: 393, screenHeight: 852 },
            app: {},
          },
          screenshots: [],
        },
        status: 'pending',
      };
      expect(report.status).toBe('pending');
      expect(report.error).toBeUndefined();
    });

    it('accepts all status values', () => {
      const statuses: FeedbackReport['status'][] = ['pending', 'uploading', 'uploaded', 'failed'];
      statuses.forEach((status) => {
        const report: FeedbackReport = {
          id: '1',
          bundle: {
            metadata: {
              timestamp: 'now',
              deviceInfo: { platform: 'ios', osVersion: '18', model: 'iPhone', screenWidth: 393, screenHeight: 852 },
              app: {},
            },
            screenshots: [],
          },
          status,
        };
        expect(report.status).toBe(status);
      });
    });
  });

  describe('FeedbackStreamEvent', () => {
    it('has correct structure', () => {
      const event: FeedbackStreamEvent = {
        type: 'tap',
        timestamp: '2026-03-24T12:00:00Z',
        data: { x: 100, y: 200 },
      };
      expect(event.type).toBe('tap');
      expect(event.data.x).toBe(100);
    });
  });
});
