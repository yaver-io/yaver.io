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

On WSL, `yaver auth` auto-detects the environment — if there is no `DISPLAY`
set (the normal WSL case) or you are on SSH, it switches to the device-code
flow automatically: it prints a URL like `https://yaver.io/auth/device?code=XXXX`,
you open it on your phone, sign in with Apple / Google / Microsoft, and the
token flows back to the waiting shell. No browser ever opens on the Windows
host.

If you want to force that flow in a shell where auto-detection guesses wrong
(some cloud shells, terminal-in-an-editor bridges, etc.):

```bash
yaver auth --headless        # explicit flag
# or, as a sticky env var:
YAVER_HEADLESS=1 yaver auth
```

### Driving the install from outside the LAN (non-developer user)

Target user: someone who is not going to type commands. They only
know how to talk to an AI coding agent (Claude Code / Codex / …)
that is already running on their home WSL box, and tap a link on
their phone. They are on cellular at a cafe; the WSL box is at home.

The agent runs, in order:

```bash
npm install -g yaver-cli
yaver auth                    # auto-picks device-code flow on WSL / SSH / YAVER_HEADLESS
```

`yaver auth` on WSL-without-DISPLAY prints a single line of the form

```
    https://yaver.io/auth/device?code=XXXX-NNNN
```

plus (on a real terminal only) an ASCII QR. The agent surfaces that
URL to the user; the user taps it, signs in with Apple / Google /
Microsoft on their phone, and the agent keeps polling.

**The flow is resumable.** `yaver auth` caps each invocation's
blocking wait at ~2.5 minutes (safely inside a bash-tool timeout) and
persists the pending code to `~/.yaver/pending-auth.json`. If the
tool call returns before the human finished signing in, the agent
just re-runs `yaver auth` — the same URL is reused, the human does
**not** sign in a second time. Sign-in burns exactly one OAuth round
trip on the phone, no matter how many times the wrapper retries.

After success the daemon is forked, `autoSetupMCP()` registers the
Yaver MCP server in every installed coding agent, and the post-auth
block nudges the agent to finish with:

```bash
yaver config set auto-start true   # survive reboots (WSL helper + Windows Startup wrapper)
yaver init                         # optional: sets a bootstrap secret so
                                   #  the mobile app can remotely re-auth
                                   #  this box if the token ever expires
```

### MCP alternative (same flow through tools)

Agents that prefer MCP over bash can drive the same flow through
tools that share the `~/.yaver/pending-auth.json` record:

```
yaver_auth_start        → { url, user_code, device_code, qr_ascii }
yaver_auth_wait         → blocks up to `timeout_seconds` (default 120),
                          returns pending|authorized|expired.
                          Re-invoke on pending until authorized.
```

Mixing surfaces is safe: if the agent started the flow via `yaver auth`
and then switched to `yaver_auth_start`, the MCP call resumes the same
code — and vice-versa.

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
