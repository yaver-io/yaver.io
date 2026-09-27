# Yaver on Steam

Steam is a Windows/macOS/Linux and SteamOS distribution channel. It does not
publish Yaver to Xbox or PlayStation.

The Steam edition is the desktop application plus its matching embedded Yaver
agent. Steam owns updates for this edition; the app's normal auto-updater must
be disabled by `YAVER_DISTRIBUTION=steam` before a retail depot is approved.

## Current gate

The scripts below prepare and validate depots but cannot upload until Valve has
approved a **Software** application and assigned one app ID plus depot IDs.
Steam Direct currently charges USD 100 for that product. Never invent IDs or
put Steam credentials in this repository.

## Depot layout

- Windows x64: signed unpacked Electron app with the matching signed x64 agent.
- macOS: signed/notarized application bundle, built separately from the Mac App
  Store flavor.
- Linux/SteamOS x64: native unpacked Electron app and native Linux agent; do
  not rely on Proton for the supported Deck lane.

Run `bash steam/prepare-depot.sh <windows|macos|linux>` on the corresponding
native build host. Then run `node steam/tests/steam-contract.test.mjs`.
For macOS release staging, point `STEAM_APP_BUNDLE` at the exact app bundle
produced by the notarized release lane; an ordinary `electron-builder --dir`
development output is intentionally rejected.

`bash steam/upload.sh --upload` is intentionally gated and requires
`STEAM_APP_ID`, all three `STEAM_DEPOT_ID_*` values, `STEAM_CONTENTBUILDER`, and
`STEAM_USERNAME`. Steam Guard/password prompts stay inside `steamcmd`; secrets
must not be passed as command-line arguments or committed.

Store release remains blocked until Big Picture/Deck controller navigation,
text entry, suspend/resume, offline errors, sign-in, task streaming, preview,
and updater ownership pass on real Steam Deck hardware.

`surface-contract.json` also pins chat, push-to-talk STT, interruptible/replayable
TTS, and both Yaver render sources. Browser and Hermes-native apps run on the
authorized render machine and reach Steam as authenticated remote-runtime
frames; the Steam shell does not execute an arbitrary Hermes guest bundle.
