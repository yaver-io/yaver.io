# Yaver SDK for Flutter & Dart

Embed [Yaver](https://yaver.io)'s local-first agent runtime into your Flutter and Dart apps. Create tasks, stream output, inspect devices, and connect your app to Yaver-powered developer workflows from any platform.

## Install

```yaml
dependencies:
  yaver: ^0.2.1
```

```bash
flutter pub add yaver
```

## Quick Start

```dart
import 'package:yaver/yaver.dart';

final client = YaverClient('http://localhost:18080', 'your-auth-token');

// Create a task
final task = await client.createTask('Fix the login bug');

// Stream output as it arrives
await for (final chunk in client.streamOutput(task.id)) {
  stdout.write(chunk);
}
```

## Features

### Task Management

```dart
// Create with options
final task = await client.createTask(
  'Refactor the auth module',
  CreateTaskOptions(model: 'sonnet', runner: 'claude'),
);

// List, stop, delete
final tasks = await client.listTasks();
await client.stopTask(task.id);
await client.deleteTask(task.id);

// Follow-up messages
await client.continueTask(task.id, 'Also update the tests');
```

### Image Attachments

```dart
import 'dart:convert';
import 'dart:io';

final bytes = await File('screenshot.png').readAsBytes();
final task = await client.createTask(
  'What is wrong with this UI?',
  CreateTaskOptions(images: [
    ImageAttachment(
      base64: base64Encode(bytes),
      mimeType: 'image/png',
      filename: 'screenshot.png',
    ),
  ]),
);
```

### Speech-to-Text

```dart
import 'dart:io';

final audioBytes = await File('recording.m4a').readAsBytes();
final result = await transcribe(
  audioBytes,
  SpeechProvider.openai,
  'sk-your-openai-key',
);
print(result.text); // transcribed text
```

### Auth & Devices

```dart
final auth = YaverAuthClient('your-auth-token');

final user = await auth.validateToken();
print('Hello, ${user.fullName}');

final devices = await auth.listDevices();
for (final d in devices) {
  print('${d.name} — ${d.isOnline ? "online" : "offline"}');
}
```

### Agent Info & Health

```dart
final rtt = await client.ping();
print('RTT: ${rtt}ms');

final info = await client.info();
print('${info.hostname} running v${info.agentVersion}');
```

## API Reference

### YaverClient

| Method | Description |
|--------|-------------|
| `health()` | Check agent reachability |
| `ping()` | Measure round-trip time (ms) |
| `info()` | Get agent hostname, version, platform |
| `createTask(prompt, opts?)` | Create a new task (supports images) |
| `getTask(taskId)` | Get task details |
| `listTasks()` | List all tasks |
| `stopTask(taskId)` | Stop a running task |
| `deleteTask(taskId)` | Delete a task |
| `continueTask(taskId, message, images?)` | Send follow-up message |
| `streamOutput(taskId)` | Stream output as a `Stream<String>` |
| `clean(days)` | Remove old tasks/images/logs |

### YaverAuthClient

| Method | Description |
|--------|-------------|
| `validateToken()` | Verify auth token, get user info |
| `listDevices()` | List registered devices |
| `getSettings()` | Get user preferences |
| `saveSettings(settings)` | Update user preferences |

### transcribe()

Cloud speech-to-text via OpenAI, Deepgram, or AssemblyAI.

## License

MIT
