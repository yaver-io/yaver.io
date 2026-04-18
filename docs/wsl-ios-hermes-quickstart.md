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
- headless device-code auth from phone or laptop browser

## What This Does Not Support

- native iOS builds from WSL
- App Store signing from WSL
- arbitrary native modules that are not already present in the Yaver mobile container
- native Yaver auto-start via systemd inside WSL
- the same reboot persistence guarantees as native Linux/macOS

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
# `yaver auth` starts the agent automatically if needed
```

If you are on a remote or SSH-only WSL session:

```bash
yaver auth --headless
```

If you want to force the iPhone path to stay on Hermes bundle mode:

```bash
yaver mcp call set_ios_install_method '{"method":"bundle"}'
```

The default `auto` mode already resolves to bundle on WSL.

## WSL Support Boundary

Treat WSL as a supported development host, not as the primary always-on deployment target for Yaver itself.

What works well:

- edit code in WSL
- run `yaver`
- authenticate from your phone or browser
- build Metro + Hermes on WSL
- test on iPhone through the Yaver mobile app

What is different from native Linux:

- Yaver does not install a native systemd auto-start service inside WSL
- Yaver can install a WSL startup helper and, when Windows is visible from WSL, a Windows Startup wrapper
- after a Windows reboot or power loss, native Linux/macOS still provide the cleaner always-on path
- if you want the strongest "box comes back by itself and the phone can re-auth it" behavior, use native Linux or macOS

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

If this repo has never been compiled on that machine before, that is fine. Yaver now reports the project as source-only and exposes `Compile Hermes` / `Rebuild Hermes` in the phone UI before `Open in Yaver`.

This is the correct daily loop for projects like `sfmg` when they are standard Expo / React Native apps.

`expo-updates` in the target app does not require a separate WSL workaround here. The Yaver mobile host includes `expo-updates`, and the shared SDK manifest is generated from the host app deps/plugin config and reused by the CLI and embedded iOS bundle, so it should not be treated as an automatic compatibility blocker for `Open in Yaver`.

## What Your Cousin Should Remember

- If he is on WSL, he should not try to make iPhone reload depend on Xcode.
- If he sees native iOS build language, that is the wrong path for daily iteration.
- If the phone says source-only or needs build, tap `Compile Hermes`.
- The right button is `Open in Yaver`.
- The right mental model is `WSL -> Hermes bundle -> Yaver mobile app`.
- The wrong expectation is `WSL behaves exactly like native Linux for system service auto-start`.
- The right expectation is `WSL uses a Yaver helper path, not native systemd`.

## Contributor to TestFlight Workflow

This is a normal split:

1. contributor edits source in WSL or Linux
2. contributor runs `yaver serve`
3. contributor compiles Hermes in Yaver and tests on the iPhone inside the Yaver app
4. contributor commits and pushes
5. maintainer deploys the real TestFlight build later from macOS/Xcode

Yaver covers the contributor device-test loop. TestFlight stays the maintainer/release step.

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
