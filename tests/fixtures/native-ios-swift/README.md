# native-ios-swift — Yaver Todo fixture

Minimal SwiftUI iOS Todo app used to exercise Yaver's native remote-runtime
viewer. It has an input field, add button, toggle buttons, and delete buttons
so tap/text control is easy to validate from the remote stream. It is also used
by `desktop/agent/native_build_fixtures_test.go` to verify `yaver iosNative`
and `/builds` (`platform: iosNative`) can build / sign / device-install /
TestFlight-upload real iOS apps.

## Project shape

- `YaverFixture/` — SwiftUI Todo sources (`TodoItem.swift`, `TodoStore.swift`,
  `TodoListView.swift`, `YaverFixtureApp.swift`)
- `YaverFixtureTests/` — XCTest tests for store add/toggle/remove behavior
- `project.yml` — xcodegen manifest; regenerates `YaverFixture.xcodeproj/`

## First-time setup

```sh
cd tests/fixtures/native-ios-swift
brew install xcodegen        # one-time, if not already installed
xcodegen generate            # creates YaverFixture.xcodeproj
```

## Manual build via yaver (macOS only)

```sh
yaver iosNative .                          # build .app + xcrun devicectl install on connected iPhone
yaver iosNative . --target=simulator       # build for simulator
yaver iosNative . --target=testflight      # archive + IPA for App Store Connect upload
yaver iosNative . --target=local           # build IPA without uploading
```

## Unit tests

```sh
xcodebuild test -scheme YaverFixture -destination 'platform=iOS Simulator,name=iPhone 15'
```

(macOS-only; requires xcodegen output. The Go integration test runs this for
you when xcodegen + a booted simulator are available.)
