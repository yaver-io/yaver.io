# WSL to iPhone Quickstart

This is the daily-loop guide for a Windows developer using WSL who wants to hot reload a React Native or Expo app on a real iPhone through Yaver.

The rule is simple:

- WSL runs the Go agent near the code.
- Yaver mobile runs on the phone.
- iPhone reload happens through a Hermes bundle loaded inside Yaver.
- `xcodebuild` is not part of the normal WSL loop.

## What This Supports

- React Native / Expo development from WSL
- real iPhone testing through the Yaver mobile app
- Hermes bundle reload from WSL, Linux, or a remote host
- remote-agent workflows over LAN, relay, or Tailscale

## What This Does Not Support

- native iOS builds from WSL
- App Store signing from WSL
- arbitrary native modules that are not already present in the Yaver mobile container

## Fast Path

1. Install the Yaver mobile app on the iPhone.
2. Install the `yaver` Go agent on the Windows/WSL machine.
3. Run the agent inside WSL.
4. Open your Expo or React Native project from Yaver.
5. Tap `Open in Yaver`.
6. Edit JS/TS code in WSL and reload on the phone.

## Install The Agent

Use any supported Linux path inside WSL:

```bash
brew install kivanccakmak/yaver/yaver
# or apt/AppImage/tarball from https://yaver.io/download
```

Then authenticate and start the agent:

```bash
yaver auth
yaver serve
```

If you want to force the iPhone path to stay on Hermes bundle mode:

```bash
yaver mcp call set_ios_install_method '{"method":"bundle"}'
```

The default `auto` mode already resolves to bundle on WSL.

## Open The Project On iPhone

From the Yaver mobile app:

1. Pair with the agent running in WSL.
2. Pick your React Native or Expo project.
3. Tap `Open in Yaver`.

Expected behavior:

- Yaver starts Metro on the WSL host
- bundles JS on the WSL host
- compiles Hermes on the WSL host
- pushes the bundle to the phone
- loads the app inside Yaver on the iPhone

This is the correct daily loop for projects like `sfmg` when they are standard Expo / React Native apps.

`expo-updates` in the target app does not require a separate WSL workaround here. The Yaver mobile host includes `expo-updates`, and the shared SDK manifest is generated from the host app deps/plugin config and reused by the CLI and embedded iOS bundle, so it should not be treated as an automatic compatibility blocker for `Open in Yaver`.

## What Your Cousin Should Remember

- If he is on WSL, he should not try to make iPhone reload depend on Xcode.
- If he sees native iOS build language, that is the wrong path for daily iteration.
- The right button is `Open in Yaver`.
- The right mental model is `WSL -> Hermes bundle -> Yaver mobile app`.

## Troubleshooting

### It tries to do a native iOS install

That is wrong for WSL. Check the agent status and install method:

```bash
yaver mcp call get_ios_install_method '{}'
```

You want the resolved mode to be `bundle`.

### The app opens but a native module is missing

The app may depend on a native module that is not included in the Yaver mobile container yet. That is a compatibility gap, not a WSL problem.

### The app does not refresh

Check:

- Metro is running on the agent host
- the phone is still paired to the same agent
- the project is actually Expo / React Native
- the host can build the Hermes bundle successfully

### You need a real App Store build

That is outside the WSL hot-reload loop. Use macOS/Xcode for native iOS builds and store shipping.
