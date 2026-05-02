# native-android-kotlin — Yaver Todo fixture

Minimal pure-Kotlin Android Todo app. No Compose, no Hilt — just a single
`Activity` with programmatic views so the fixture compiles fast and stays stable
across AGP changes. It is meant to test Yaver remote-runtime text entry, tap
controls, relay fallback, and feedback commands. It is also used by
`desktop/agent/native_build_fixtures_test.go` to verify `yaver androidNative`
and `/builds` (`platform: androidNative`) can build and push real Android apps.

## First-time setup

Requires Android SDK + Gradle wrapper. The repo intentionally does NOT commit
`gradlew` (it's a 60 KB jar). Generate it once with:

```sh
cd tests/fixtures/native-android-kotlin
gradle wrapper --gradle-version 8.5
```

Or have a system-installed `gradle` on PATH; the agent's `resolveBuildCommand`
falls back to plain `gradle` when `./gradlew` is missing.

Set `local.properties` once so the SDK location is unambiguous:

```sh
echo "sdk.dir=$ANDROID_HOME" > local.properties
```

## Manual build via yaver

```sh
yaver androidNative .                          # build debug APK + adb install on connected device
yaver androidNative . --target=local           # build only, no install
yaver androidNative . --target=playstore       # build release AAB
```

## Unit tests

```sh
gradle test                                    # runs TodoStore JUnit tests
```
