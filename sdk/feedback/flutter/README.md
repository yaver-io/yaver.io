# yaver_feedback

Visual feedback SDK for Yaver -- shake-to-report, screenshots, voice annotations, P2P device discovery, and real-time agent connectivity for vibe coding workflows.

Collect visual bug reports from your Flutter app and send them directly to your Yaver AI agent. Designed for development and QA workflows where you want to quickly capture what's on screen, record a voice note explaining the issue, and ship it all to your coding agent in one tap.

## Features

- P2P device discovery -- auto-find Yaver agents on your local network
- Connection widget with dark-themed UI for device management
- Three feedback modes: live streaming, narrated, and batch
- Agent commentary -- receive real-time messages from your AI agent
- Draggable floating feedback button (debug builds only)
- Screenshot capture via `RepaintBoundary`
- Voice annotation recording (bring your own audio recorder package)
- Timeline-based feedback bundles with metadata
- Direct upload to Yaver agent HTTP API
- Shake-to-report trigger (with `sensors_plus` integration)
- Runtime enable/disable toggle

## Installation

Add to your `pubspec.yaml`:

```yaml
dependencies:
  yaver_feedback: ^0.1.0
```

Then run:

```bash
flutter pub get
```

## Quick Start

```dart
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:yaver_feedback/yaver_feedback.dart';

void main() {
  // Only enable in debug builds
  if (kDebugMode) {
    YaverFeedback.init(FeedbackConfig(
      agentUrl: 'http://192.168.1.100:18080',
      authToken: 'your-token',
    ));
  }

  runApp(
    MaterialApp(
      builder: (context, child) => Stack(
        children: [child!, const YaverFeedbackButton()],
      ),
      home: const MyApp(),
    ),
  );
}
```

## Device Discovery

The SDK can auto-discover Yaver agents on your local network by scanning common LAN subnets. If no `agentUrl` is provided, discovery runs automatically when a report is started.

### Auto-discovery

```dart
// Initialize without a URL -- discovery happens automatically
YaverFeedback.init(FeedbackConfig(
  agentUrl: '', // empty = auto-discover
  authToken: 'your-token',
));

// Or discover manually
final agent = await YaverDiscovery.discover();
if (agent != null) {
  print('Found ${agent.hostname} at ${agent.url} (${agent.latencyMs}ms)');
}
```

### Manual connection

```dart
// Connect to a known host
final agent = await YaverDiscovery.connect('192.168.1.42:18080');
// Scheme and port are auto-added if missing

// Or probe a specific URL
final result = await YaverDiscovery.probe('http://10.0.0.5:18080');
```

### From YaverFeedback

```dart
// Ensure a connection exists (auto-discovers if needed)
final connected = await YaverFeedback.ensureConnected();

// Connect to a specific URL
final result = await YaverFeedback.connectTo('192.168.1.42:18080');
```

## Connection Widget

The `YaverConnectionWidget` provides a complete UI for device discovery and connection management. Dark-themed to match the feedback overlay.

```dart
YaverConnectionWidget(
  authToken: 'your-token',
  commentaryLevel: 5,
  onConnected: (client) {
    print('Connected to agent');
  },
  onDisconnected: () {
    print('Disconnected');
  },
  onTestingToggled: (isTesting) {
    print('Testing: $isTesting');
  },
)
```

The widget shows:
- Connection status indicator (disconnected, connecting, connected, error)
- URL input field for manual connection
- Discover button to scan the local network
- Connect button for manual URL entry
- Agent info (hostname, URL, version, latency) when connected
- Start/Stop testing toggle
- Agent commentary messages in real-time

## Feedback Modes

The SDK supports three feedback delivery modes:

| Mode | Description | Use case |
|------|-------------|----------|
| `FeedbackMode.narrated` | Collect events, narrate, then send (default) | Standard bug reports |
| `FeedbackMode.live` | Stream events to agent in real-time | Live testing sessions |
| `FeedbackMode.batch` | Collect silently, upload as batch | Automated QA |

```dart
YaverFeedback.init(FeedbackConfig(
  agentUrl: 'http://192.168.1.100:18080',
  authToken: 'your-token',
  mode: FeedbackMode.live, // real-time streaming
));
```

### Live mode

In live mode, events are streamed to the agent's `/feedback/stream` endpoint as they occur:

```dart
// Screenshots are automatically streamed in live mode
final path = await YaverFeedback.captureScreenshot();

// Or stream custom events
await YaverFeedback.streamEvent({
  'type': 'annotation',
  'text': 'Button not responding',
  'timestamp': DateTime.now().millisecondsSinceEpoch,
});
```

## Agent Commentary

The agent can send commentary messages back to the SDK. Configure the verbosity level (0-10) to filter messages:

| Level | Description |
|-------|-------------|
| 0 | No commentary |
| 1-3 | Critical issues only |
| 4-5 | Normal verbosity (default) |
| 6-8 | Detailed analysis |
| 9-10 | Everything |

```dart
YaverFeedback.init(FeedbackConfig(
  agentUrl: 'http://192.168.1.100:18080',
  authToken: 'your-token',
  agentCommentaryLevel: 7, // show detailed analysis
));

// Listen to commentary
YaverFeedback.commentaryStream?.listen((message) {
  print('Agent says: $message');
});

// The connection widget displays commentary automatically
```

## P2P Client

The `P2PClient` class provides direct HTTP access to the Yaver agent:

```dart
final client = P2PClient(
  baseUrl: 'http://192.168.1.42:18080',
  authToken: 'your-token',
);

// Health check
final isAlive = await client.health();

// Agent info
final info = await client.info();
print('Agent: ${info['hostname']} v${info['version']}');

// Upload feedback
final reportId = await client.uploadFeedback(bundle);

// Build management
final builds = await client.listBuilds();
final build = await client.startBuild('ios');
final url = client.getArtifactUrl(build['id']);

// Cleanup
client.dispose();
```

## Screenshot Capture

To enable screenshot capture, wrap your app in a `RepaintBoundary` and pass the key to `YaverFeedback`:

```dart
final _boundaryKey = GlobalKey();

@override
Widget build(BuildContext context) {
  return RepaintBoundary(
    key: _boundaryKey,
    child: MaterialApp(
      // ...
    ),
  );
}

@override
void initState() {
  super.initState();
  YaverFeedback.setRepaintBoundaryKey(_boundaryKey);
}
```

You can also capture screenshots programmatically:

```dart
final path = await YaverFeedback.captureScreenshot();
if (path != null) {
  print('Screenshot saved to: $path');
}
```

## Error Capture

Capture Flutter and async errors with full stack traces. The agent gets the exact error, stack frames, and optional context.

**No conflicts with Sentry, Crashlytics, Firebase, or any other tool.** The SDK never auto-hooks `FlutterError.onError` or `PlatformDispatcher.instance.onError`. You explicitly insert it into your error chain.

### Option 1: Wrap the error handlers (recommended)

```dart
// Insert Yaver into the Flutter error chain
final previous = FlutterError.onError;
FlutterError.onError = YaverFeedback.wrapFlutterErrorHandler(previous);

// And for async errors
final prevPlatform = PlatformDispatcher.instance.onError;
PlatformDispatcher.instance.onError =
    YaverFeedback.wrapPlatformErrorHandler(prevPlatform);

// Sentry/Crashlytics can still wrap after this — the chain stays intact.
```

### Option 2: Manual attach (in catch blocks)

```dart
try {
  await riskyOperation();
} catch (e, stack) {
  YaverFeedback.attachError(e, stack, metadata: {
    'context': 'checkout-flow',
    'cartItems': cart.length,
  });
  rethrow;
}
```

### API

| Method | Description |
|--------|-------------|
| `attachError(error, stackTrace, {metadata})` | Manually attach an error |
| `wrapFlutterErrorHandler(next)` | Returns a pass-through `FlutterExceptionHandler` |
| `wrapPlatformErrorHandler(next)` | Returns a pass-through `ErrorCallback` |
| `getCapturedErrors()` | Get the current error buffer |
| `clearCapturedErrors()` | Clear the error buffer |

## Configuration Options

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `agentUrl` | `String` | required | Yaver agent HTTP URL (empty for auto-discover) |
| `authToken` | `String` | required | Auth token for the agent |
| `trigger` | `FeedbackTrigger` | `.floatingButton` | How to trigger feedback (shake, floatingButton, manual) |
| `enabled` | `bool` | `true` | Whether feedback is active |
| `maxRecordingDuration` | `int` | `60` | Max voice recording seconds |
| `mode` | `FeedbackMode` | `.narrated` | Feedback delivery mode (live, narrated, batch) |
| `agentCommentaryLevel` | `int` | `5` | Commentary verbosity 0-10 |
| `maxCapturedErrors` | `int` | `5` | Error ring buffer size |

## Floating Button

The `YaverFeedbackButton` is a draggable circle that appears over your app. Tap it to open the feedback overlay. It's only visible when `YaverFeedback.isEnabled` is true.

Customize appearance:

```dart
YaverFeedbackButton(
  initialRight: 20,
  initialBottom: 120,
  size: 56,
  backgroundColor: Colors.deepPurple,
  icon: Icons.feedback,
)
```

## Shake Detection

The SDK includes a `ShakeDetector` class. To use it with real accelerometer data, add `sensors_plus` to your app and wire it up:

```dart
import 'package:sensors_plus/sensors_plus.dart';

final detector = ShakeDetector();
accelerometerEventStream().listen((event) {
  detector.onAccelerometerEvent(event.x, event.y, event.z, () {
    YaverFeedback.startReport(context);
  });
});
```

## Manual Feedback Trigger

Open the feedback overlay programmatically:

```dart
final sent = await YaverFeedback.startReport(context);
if (sent) {
  print('Feedback submitted');
}
```

## Runtime Control

```dart
// Disable feedback collection
YaverFeedback.setEnabled(false);

// Re-enable
YaverFeedback.setEnabled(true);

// Check state
print(YaverFeedback.isInitialized); // true
print(YaverFeedback.isEnabled);     // true/false
print(YaverFeedback.mode);          // FeedbackMode.narrated

// Cleanup
YaverFeedback.dispose();
```

## How It Connects to Yaver

The feedback bundle is uploaded as a multipart POST to your Yaver agent's `/feedback` endpoint. The agent receives:

- Screenshot images as file attachments
- Voice recording as a file attachment
- Metadata JSON with timeline events, device info, and custom fields

In live mode, events stream to `/feedback/stream` as they occur. The agent can respond with commentary messages via `/feedback/commentary`.

The agent can then use this context when processing tasks -- your AI coding agent sees exactly what you see.

## Development vs Production

This SDK is designed for **development and QA workflows**. Guard initialization with `kDebugMode`:

```dart
if (kDebugMode) {
  YaverFeedback.init(config);
}
```

The floating button and all feedback features are completely inert when not initialized -- zero runtime overhead in production builds.

## Voice Recording

The SDK provides the UI and timeline infrastructure for voice notes, but does not bundle an audio recording package to keep dependencies minimal. Integrate your preferred recorder (e.g., `record`, `flutter_sound`, `audio_recorder`) and pass the recorded file path into the feedback bundle.

## License

AGPL-3.0-only
