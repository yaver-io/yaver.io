import 'package:flutter_test/flutter_test.dart';
import 'package:yaver_feedback/yaver_feedback.dart';

void main() {
  group('IPv6 probe candidates', () {
    test('brackets IPv6 literals and preserves IPv4 hosts', () {
      const device = RemoteDevice(
        deviceId: 'ipv6-host',
        name: 'IPv6 host',
        platform: 'linux',
        isOnline: true,
        needsAuth: false,
        runnerDown: false,
        lastHeartbeat: 0,
        quicHost: '2001:db8::10',
        quicPort: 18080,
        localIps: ['192.0.2.10', 'fd00::20'],
      );
      expect(buildProbeCandidates(device), [
        'http://[2001:db8::10]:18080',
        'http://192.0.2.10:18080',
        'http://[fd00::20]:18080',
      ]);
    });
  });

  group('FeedbackConfig', () {
    test('creates with required fields and defaults', () {
      final config = FeedbackConfig(
        agentUrl: 'http://localhost:18080',
        authToken: 'test-token',
      );

      expect(config.agentUrl, 'http://localhost:18080');
      expect(config.authToken, 'test-token');
      expect(config.trigger, FeedbackTrigger.floatingButton);
      expect(config.enabled, true);
      expect(config.maxRecordingDuration, 60);
      expect(config.mode, FeedbackMode.narrated);
      expect(config.agentCommentaryLevel, 5);
    });

    test('creates with custom values', () {
      final config = FeedbackConfig(
        agentUrl: 'http://192.168.1.50:18080',
        authToken: 'my-token',
        trigger: FeedbackTrigger.shake,
        enabled: false,
        maxRecordingDuration: 120,
        mode: FeedbackMode.live,
        agentCommentaryLevel: 8,
      );

      expect(config.trigger, FeedbackTrigger.shake);
      expect(config.enabled, false);
      expect(config.maxRecordingDuration, 120);
      expect(config.mode, FeedbackMode.live);
      expect(config.agentCommentaryLevel, 8);
    });

    test('copyWith replaces fields', () {
      final original = FeedbackConfig(
        agentUrl: 'http://localhost:18080',
        authToken: 'token-1',
      );
      final updated = original.copyWith(
        authToken: 'token-2',
        enabled: false,
        mode: FeedbackMode.batch,
        agentCommentaryLevel: 3,
      );

      expect(updated.agentUrl, 'http://localhost:18080');
      expect(updated.authToken, 'token-2');
      expect(updated.enabled, false);
      expect(updated.mode, FeedbackMode.batch);
      expect(updated.agentCommentaryLevel, 3);
    });

    test('copyWith preserves unset fields', () {
      final original = FeedbackConfig(
        agentUrl: 'http://localhost:18080',
        authToken: 'token-1',
        mode: FeedbackMode.live,
        agentCommentaryLevel: 9,
      );
      final updated = original.copyWith(enabled: false);

      expect(updated.mode, FeedbackMode.live);
      expect(updated.agentCommentaryLevel, 9);
      expect(updated.agentUrl, 'http://localhost:18080');
    });
  });

  group('FeedbackMode', () {
    test('has expected values', () {
      expect(FeedbackMode.values, hasLength(3));
      expect(FeedbackMode.values, contains(FeedbackMode.live));
      expect(FeedbackMode.values, contains(FeedbackMode.narrated));
      expect(FeedbackMode.values, contains(FeedbackMode.batch));
    });
  });

  group('TimelineEvent', () {
    test('serializes to JSON', () {
      final event = TimelineEvent(
        time: 3.5,
        type: 'screenshot',
        filePath: '/tmp/screenshot.png',
      );

      final json = event.toJson();
      expect(json['time'], 3.5);
      expect(json['type'], 'screenshot');
      expect(json['filePath'], '/tmp/screenshot.png');
      expect(json.containsKey('text'), false);
    });

    test('deserializes from JSON', () {
      final json = {
        'time': 1.2,
        'type': 'voice',
        'text': 'Bug on login screen',
        'filePath': '/tmp/audio.m4a',
      };

      final event = TimelineEvent.fromJson(json);
      expect(event.time, 1.2);
      expect(event.type, 'voice');
      expect(event.text, 'Bug on login screen');
      expect(event.filePath, '/tmp/audio.m4a');
    });

    test('omits null fields in JSON', () {
      final event = TimelineEvent(time: 0.0, type: 'annotation');
      final json = event.toJson();

      expect(json.containsKey('text'), false);
      expect(json.containsKey('filePath'), false);
    });
  });

  group('DeviceInfo', () {
    test('serializes to JSON', () {
      final info = DeviceInfo(
        platform: 'ios',
        model: 'iPhone 15 Pro',
        osVersion: '17.4',
        appName: 'MyApp',
      );

      final json = info.toJson();
      expect(json['platform'], 'ios');
      expect(json['model'], 'iPhone 15 Pro');
      expect(json['osVersion'], '17.4');
      expect(json['appName'], 'MyApp');
    });

    test('deserializes from JSON', () {
      final json = {
        'platform': 'android',
        'model': 'Pixel 8',
        'osVersion': '14',
      };

      final info = DeviceInfo.fromJson(json);
      expect(info.platform, 'android');
      expect(info.model, 'Pixel 8');
      expect(info.osVersion, '14');
      expect(info.appName, isNull);
    });

    test('omits null appName in JSON', () {
      final info = DeviceInfo(
        platform: 'ios',
        model: 'iPad',
        osVersion: '17.0',
      );

      final json = info.toJson();
      expect(json.containsKey('appName'), false);
    });
  });

  group('FeedbackBundle', () {
    test('serializes to JSON', () {
      final bundle = FeedbackBundle(
        metadata: {'userId': 'u123', 'screen': 'home'},
        screenshotPaths: ['/tmp/s1.png', '/tmp/s2.png'],
        timeline: [
          TimelineEvent(time: 0.0, type: 'screenshot', filePath: '/tmp/s1.png'),
          TimelineEvent(time: 2.5, type: 'voice', text: 'This is broken'),
        ],
        deviceInfo: DeviceInfo(
          platform: 'ios',
          model: 'iPhone 15',
          osVersion: '17.4',
        ),
      );

      final json = bundle.toJson();
      expect(json['metadata']['userId'], 'u123');
      expect(json['screenshotPaths'], hasLength(2));
      expect(json['timeline'], hasLength(2));
      expect(json['deviceInfo']['platform'], 'ios');
      expect(json.containsKey('videoPath'), false);
      expect(json.containsKey('audioPath'), false);
    });
  });

  group('FeedbackTrigger', () {
    test('has expected values', () {
      expect(FeedbackTrigger.values, hasLength(3));
      expect(FeedbackTrigger.values, contains(FeedbackTrigger.shake));
      expect(FeedbackTrigger.values, contains(FeedbackTrigger.floatingButton));
      expect(FeedbackTrigger.values, contains(FeedbackTrigger.manual));
    });
  });

  group('DiscoveryResult', () {
    test('stores fields correctly', () {
      final result = DiscoveryResult(
        url: 'http://192.168.1.42:18080',
        hostname: 'MacBook-Air',
        version: '1.44.0',
        latencyMs: 12,
      );

      expect(result.url, 'http://192.168.1.42:18080');
      expect(result.hostname, 'MacBook-Air');
      expect(result.version, '1.44.0');
      expect(result.latencyMs, 12);
    });

    test('toString includes all fields', () {
      final result = DiscoveryResult(
        url: 'http://10.0.0.1:18080',
        hostname: 'dev-machine',
        version: '1.0.0',
        latencyMs: 5,
      );

      final str = result.toString();
      expect(str, contains('10.0.0.1'));
      expect(str, contains('dev-machine'));
      expect(str, contains('1.0.0'));
      expect(str, contains('5ms'));
    });
  });

  group('YaverDiscovery', () {
    test('cached is null initially', () {
      YaverDiscovery.clearCache();
      expect(YaverDiscovery.cached, isNull);
    });

    test('clearCache removes cached result', () {
      YaverDiscovery.clearCache();
      expect(YaverDiscovery.cached, isNull);
    });
  });
}
