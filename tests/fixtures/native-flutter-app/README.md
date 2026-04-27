# native-flutter-app — Yaver fixture

Minimal Flutter app with login (hardcoded `admin` / `admin`) → dashboard. Used by
`desktop/agent/native_build_fixtures_test.go` to verify the `yaver flutter` /
`/builds` (`platform: flutter`) pipeline can build and push a real Flutter app.

## First-time setup (regenerates platform shells)

```sh
cd tests/fixtures/native-flutter-app
flutter create . --org io.yaver.fixture --platforms=android,ios --project-name yaver_native_flutter_app
```

## Manual build via yaver

```sh
yaver flutter . --target=local       # build APK only, no install
yaver flutter . --target=device      # build + adb install -r on connected phone (LAN)
yaver flutter . --target=apk         # build APK
yaver flutter . --target=playstore   # build AAB for Play upload
```

## Unit tests

```sh
flutter test
```
