# Yaver Feedback SDK Examples

Visual feedback for vibe coding — record your screen and voice while testing, send to your AI agent, it fixes the bugs.

## Three Runtime Modes

| Mode | Streaming | Agent Fixes | Best For |
|------|-----------|-------------|----------|
| **Full Interactive** | Live video + audio | Yes, hot reload | Active development, quick iterations |
| **Semi Interactive** | Live video + audio | No, conversation only | Code review, discussion, "fix later" |
| **Post Mode** | None (offline) | After submission | QA sessions, slow connections, detailed reports |

Users select the mode at runtime from within their app.

## Examples

### Web
- `web-full-interactive.html` — Live mode with agent commentary + hot reload
- `web-semi-interactive.html` — Streaming with conversation, no auto-fix
- `web-post-mode.html` — Offline recording, submit compressed bundle

### Flutter
- `flutter-example.dart` — Mode selector + floating button + connection widget

### React Native
- `react-native-example.tsx` — Mode selector + connection screen + floating button

### Unity
- `sdk/feedback/unity/Examples~/Basic/YaverExampleBootstrap.cs` — initial bootstrap scaffold
- `sdk/feedback/test-app/unity/Assets/Scripts/YaverBootstrap.cs` — sample bootstrap
- `sdk/feedback/test-app/unity/Assets/Scripts/YaverContentReloadDemo.cs` — content refresh demo
- `sdk/feedback/test-app/unity/Assets/Scripts/YaverGameConfigApplier.cs` — remote-tunable JSON config flow

## Quick Start

### Web
```html
<script type="module">
  import { YaverFeedback } from '@yaver/feedback-web';

  if (location.hostname === 'localhost') {
    YaverFeedback.init({ trigger: 'floating-button' });
  }
</script>
```

### React Native
```tsx
import { YaverFeedback, YaverConnectionScreen } from 'yaver-feedback-react-native';

if (__DEV__) {
  YaverFeedback.init({ trigger: 'shake' });
}

// In your dev settings screen:
export const DevSettings = () => <YaverConnectionScreen />;
```

### Flutter
```dart
import 'package:yaver_feedback/yaver_feedback.dart';

void main() {
  if (kDebugMode) {
    YaverFeedback.init(FeedbackConfig(
      trigger: FeedbackTrigger.floatingButton,
    ));
  }
  runApp(MyApp());
}
```

## Agent Commentary Levels

| Level | Behavior |
|-------|----------|
| 0 | Silent — agent only responds when asked |
| 1-3 | Minimal — agent notes crashes and critical errors |
| 4-6 | Normal — agent comments on obvious UI issues |
| 7-9 | Verbose — agent comments on layout, performance, accessibility |
| 10 | Maximum — agent comments on everything it sees |

## Voice Commands (Full Interactive Mode)

| Say | Agent Does |
|-----|-----------|
| "make this bigger" | Changes font size / padding |
| "fix this bug" | Analyzes current screen, generates fix |
| "that looks good" | Marks current state as correct |
| "keep in mind" | Adds to backlog, doesn't fix now |
| "push to TestFlight" | Runs pipeline: build → test → deploy |
| "run the tests" | Executes test suite, reports results |

## How It Works

```
Your App (with SDK)
  │
  ├── Full Interactive: screen + mic → P2P stream → agent
  │     ↕ bidirectional: agent sends fixes via hot reload
  │
  ├── Semi Interactive: screen + mic → P2P stream → agent
  │     ↓ one-way commentary: agent observes and comments
  │
  └── Post Mode: screen + mic → local recording
        → compress (H.264/VP9, ~2-5 MB/min)
        → upload multipart POST to agent
        → agent processes entire session
```

## Device Discovery

All SDKs auto-discover your Yaver agent on the local network:
1. Check stored connection (from last session)
2. Try localhost:18080
3. Scan common LAN IPs (192.168.x.x, 10.0.x.x)

Or connect manually via the Connection Screen / Widget.
