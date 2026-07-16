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
