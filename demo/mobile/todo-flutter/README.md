# todo-flutter — Yaver Flutter fixture

Flutter todo app used to validate Yaver's native WebRTC remote-runtime path.
Unlike the React Native fixture, this app runs in its own Flutter process on an
Android emulator or iOS simulator and is viewed through the web dashboard's
WebRTC stream.

The seed data intentionally includes manufacturing and quality-control search
terms used by real workflows:

- GKK
- ÇKK son kontrol formu
- iş emri
- üretim emri

## First-time setup

```sh
cd demo/mobile/todo-flutter
flutter create . --org io.yaver.fixture --platforms=android,ios --project-name yaver_native_flutter_app
```

## Local checks

```sh
flutter test
flutter run -d <device-id>
```

## Yaver WebRTC flow

1. Start the agent on the host that has Flutter + Android SDK installed.
2. Open the Yaver web dashboard and select the Flutter todo project.
3. Choose the Android emulator target first on Linux hosts.
4. Start the remote runtime session.
5. Verify the dashboard viewer can search for `GKK`, `ÇKK`, `iş emri`, and
   `üretim emri`, add a new item, and toggle it through WebRTC pointer input.
