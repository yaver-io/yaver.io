# YaverKeyboardRouter — native modules

The phone-side multiplexer for a paired BT keyboard. Lets the wearer
type from one keyboard into many remote sinks (terminal pty on the
agent, remote browser window in the spatial scene, voice control,
phone OS default).

## Files

| Platform | Module | Bridge | Registered in |
|---|---|---|---|
| iOS | `ios/Yaver/YaverKeyboardRouter.swift` (RCTEventEmitter) | `ios/Yaver/YaverKeyboardRouter.m` (RCT_EXTERN_MODULE) | `ios/Yaver.xcodeproj/project.pbxproj` (PBXBuildFile, PBXFileReference, group, Sources) |
| Android | `android/app/src/main/java/io/yaver/mobile/YaverKeyboardRouterModule.kt` | `YaverKeyboardRouterPackage.kt` | `MainApplication.kt::getPackages` + `MainActivity.kt::dispatchKeyEvent` |
| TS | `src/lib/keyboardRouter.ts` | — | Imported by screens that want a sink |

## JS surface

```ts
import { keyboardRouter } from "@/lib/keyboardRouter";

keyboardRouter.configure({ agentUrl, token });
await keyboardRouter.grabNative();              // best-effort; returns false when module absent
keyboardRouter.setSink({ kind: "browser", sessionId });
// keystrokes now flow to the agent's /remote-runtime/sessions/<id>/control
keyboardRouter.setSink({ kind: "terminal", paneId });
// keystrokes flow to /tasks/<id>/stdin (printable as text, named keys
// as the right escape sequences)
await keyboardRouter.releaseNative();
```

The native module emits `YaverKey` events with shape
`{ key: string, modifiers: { shift, ctrl, alt, meta } }`. Plain
printable keys flow as single characters; named keys (Enter, Tab,
Backspace, Arrow*, Escape, Home/End, PageUp/Down, Delete, space) are
spelled out as those strings — matches the JS `NAMED_KEYS` set so
both ingestion paths produce identical sink dispatches.

## Build expectations

iOS source files are force-tracked overlays per `CLAUDE.md`. After
`expo prebuild --clean`, run `git checkout -- mobile/ios/` to restore
them along with the pbxproj entries. Android Kotlin files live under
the normal `app/src/main/java/...` tree and are picked up by Gradle
without any special preservation.

`yaver wire push` rebuilds the native side. After the first build,
the module is available at `NativeModules.YaverKeyboardRouter`.

## What HID grab can and can't do

iOS:
- ✓ All printable keys + named keys ARE captured via GCKeyboard
- ✗ ⌘-H (Home), ⌘-Space (Spotlight) are intercepted by iPadOS first

Android:
- ✓ Activity-level dispatchKeyEvent runs BEFORE RN's TextInput, so
  the router intercepts even when a TextInput is focused
- ✗ System gestures (Recent Apps, Home button) bypass the activity
