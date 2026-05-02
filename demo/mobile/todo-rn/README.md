# Todo RN — Yaver Feedback SDK showcase

Minimal Expo / React Native Todo app used to demo the Yaver Feedback SDK
(`yaver-feedback-react-native`). Runs in two modes:

1. **Standalone** — shipped to TestFlight / Play Store with the SDK
   embedded. Shake → feedback modal → user reports straight to the
   developer's Yaver agent.
2. **Inside Yaver** — same source bundled by `yaver dev build-native`
   and loaded via Hermes into the Yaver mobile container. Yaver's
   own shake overlay owns the gesture and dispatches
   `yaverFeedback:startReport` into this app's bridge. Detection is
   automatic via the `YaverInfo` native module.

## Quick start

```bash
cd demo/mobile/todo-rn
npm install --legacy-peer-deps
npx expo prebuild --clean
npx expo run:ios       # device or simulator
# or: npx expo run:android
```

In the Yaver hot-reload flow the `npm install` + `prebuild` are handled
by the agent's `/dev/build-native` endpoint — no manual setup beyond
cloning this repo onto the dev box.

## Files

- `app/_layout.tsx` — boots the SDK, mounts `<FeedbackModal />`.
- `app/index.tsx` — the Todo screen (single screen).
- `app.json` — Expo config; `io.yaver.todorn` bundle id, dark splash.

## Why no auth, no backend?

The showcase is about feedback flow, not Todo correctness. State lives
in `AsyncStorage` so the demo persists across hot reloads.
