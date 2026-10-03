# Mac mini remote worker

Use this when a user-owned Mac mini is the Yaver remote slave for developing
Yaver and Talos through Yaver itself.

The Mac mini role is:

- run `yaver serve` as a same-user remote device
- run Codex as the coding runner, with `gpt-5.5` as the default model
- host Xcode, Apple SDKs, and simulators for mobile, tablet, TV, watch, and
  vision surfaces
- build and test Yaver/Talos work remotely through `ops`, MCP, `yaver code
  --attach`, or `yaver runner <machine> codex`

It is not a GUI editor workstation. Do not make Cursor, VS Code, Windsurf, or
similar editor state a prerequisite for the remote worker. If they are already
installed and you want them removed, run the bootstrap with
`REMOVE_GUI_EDITORS=1`; the script only uninstalls Homebrew-managed casks and
prints unmanaged app paths instead of deleting them.

## Bootstrap

From another machine:

```bash
scp scripts/setup-mac-mini-dev.sh mac-mini:/tmp/
ssh mac-mini bash /tmp/setup-mac-mini-dev.sh
```

Then on the Mac mini:

```bash
yaver auth --headless
codex login --device-auth
yaver serve
yaver-mac-mini-status
```

Useful overrides:

```bash
CODEX_MODEL=gpt-5.5 bash /tmp/setup-mac-mini-dev.sh
SKIP_XCODE_DOWNLOAD=1 bash /tmp/setup-mac-mini-dev.sh
REMOVE_GUI_EDITORS=1 bash /tmp/setup-mac-mini-dev.sh
YAVER_PROJECTS="$HOME/Workspace/yaver.io $HOME/Workspace/talos" bash /tmp/setup-mac-mini-dev.sh
```

### Permanent SSH-only worker

When the Mac is an always-on development worker rather than an interactive
desktop, run the bootstrap once at its local screen with:

```bash
HEADLESS_PERMANENT=1 HEADLESS_KEYCHAIN=1 bash /tmp/setup-mac-mini-dev.sh
```

This is intentionally an explicit opt-in. It enables Remote Login, disables
computer and disk sleep, disables Power Nap when available, enables automatic
restart after power loss, and installs Yaver as a system LaunchDaemon so it
starts before a user logs in. It preserves the invoking developer's home while
installing the daemon; the running service therefore uses that account's
projects, Yaver vault, and logs rather than `/var/root`.

`HEADLESS_KEYCHAIN=1` asks for the local macOS password once, without echoing
it, and writes an owner-only `~/.yaver/local-secrets.env`. It unlocks the login
keychain and grants `codesign` non-GUI access through the key partition list.
This removes the normal signing Keychain dialogs for SSH/agent builds; the
password must never be committed, synced, pasted into a chat, or placed in a
cloud secret. If a dedicated Yaver signing keychain is used, add its separate
password through the same local-secret mechanism before autonomous releases.

Do not weaken FileVault, the firewall, or SSH authentication to suppress
prompts. Restrict Remote Login to the developer account and use SSH keys. On
Apple-silicon Macs running macOS 26 or later, FileVault can be unlocked over
SSH after restart when Remote Login and networking are available; the unlock
still requires the user's credentials. [Apple's Remote Login guide](https://support.apple.com/en-my/guide/mac-help/mchlp1066/mac) and [FileVault deployment guidance](https://support.apple.com/en-ca/guide/deployment/dep82064ec40/web) cover those platform constraints.

### SSD, RAM, simulator, and removable-media policy

Treat an entry-level Mac mini as a constrained build worker, not an infinite
CI runner. The worker must measure free disk space and memory pressure before
starting expensive work, and it must serialize heavyweight jobs:

- Run **one** Xcode archive, `xcodebuild test`, Gradle release build, simulator
  boot, or large dependency install at a time. On an 8 GB Mac, overlapping
  Xcode, Gradle, Metro, and a simulator produces swap pressure that is slower
  than a queue and adds needless SSD writes.
- Keep the checked-in Gradle caps (`org.gradle.parallel=false`, at most two
  workers, mobile heap at 3 GiB; Wear/TV at 2 GiB). Do not raise them to make a
  single build appear faster.
- Keep at least 20 GiB free on the internal APFS volume for normal work and
  10 GiB beyond the expected cold-build output. Refuse or queue a build below
  that budget rather than filling the startup volume. Clean only the exact
  generated build/DerivedData paths after a successful verification; repeatedly
  purging SDK, simulator, npm, CocoaPods, or Gradle caches creates download
  churn and SSD wear.
- Keep source checkouts, the Yaver vault, Keychains, the macOS SDK, and active
  simulator runtimes on the encrypted internal SSD. They need APFS semantics,
  stable permissions, symlinks, and reliable latency.
- An SD card is for **offline, encrypted backup/export only**, never active
  source, `node_modules`, simulator runtimes, Xcode DerivedData, or Keychains.
  Its endurance, latency, and filesystem semantics are unsuitable for build
  churn. For overflow build artifacts, use a quality external SSD formatted
  APFS and opt in with the `.yaver-artifact-volume` marker described in the
  disk-space preflight guide.
- When a removable artifact volume disappears, Yaver must fail with a named
  "artifact volume unavailable" result and leave source and credentials on the
  internal disk. It must never silently fall back to writing a huge archive to
  the startup volume.

## Xcode surfaces

The bootstrap runs full-Xcode first launch tasks, downloads matching simulator
runtimes with `xcodebuild -downloadPlatform`, and creates named simulators:

| Surface | Simulator name |
|---|---|
| mobile | `Yaver-Mobile` |
| tablet | `Yaver-Tablet` |
| TV | `Yaver-TV` |
| watch | `Yaver-Watch` |
| AR/VR | `Yaver-Vision` |

CarPlay is not a separate simulator runtime. It uses the iOS simulator/runtime
plus the app's CarPlay scene and entitlement setup.

If Xcode cannot download a runtime automatically, install it from Xcode
Settings > Components and rerun the script. The script is idempotent.

## Codex alignment

The script writes `~/.codex/config.toml` so the Mac mini uses:

```toml
model = "gpt-5.5"
model_reasoning_effort = "medium"
```

It also marks the Yaver and Talos workspaces trusted. This avoids the local
machine and the remote Mac mini running Codex with different defaults.

## Yaver integration

The script installs `yaver-cli` from npm and runs:

```bash
yaver mcp setup codex
```

After auth, set the mini as the primary remote device if that is the intended
default target:

```bash
yaver devices
yaver primary set <deviceId-or-alias>
yaver primary ping
```

Prefer the stable `ops` facade for Talos/Yaver automation:

```json
{
  "machine": "primary",
  "verb": "playwright_run",
  "payload": {
    "dir": "/Users/<user>/Workspace/talos",
    "root": "/Users/<user>/Workspace/talos/yaver-tests",
    "trace": true,
    "video": true
  }
}
```
