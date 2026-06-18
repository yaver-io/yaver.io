# Yaver's no-root contract

**Yaver's normal runtime path NEVER requires root.** Every state file
lives under `$HOME/.yaver/`. Every listening port is `>1024`. No
sudo at startup, no sudo on routine commands.

This document spells out the contract + the explicit exceptions, so
trio users (phone + glasses + keyboard) and managed-cloud users alike
know exactly what to expect.

## The regular path — zero root, ever

| Command | What it does | Root needed? |
|---|---|---|
| `yaver auth` | OAuth in browser → token to `$HOME/.yaver/config.json` | **No** |
| `yaver auth --headless` | Short-code flow for SSH-only boxes | **No** |
| `yaver serve` | Agent on `:18080` HTTP, `:4433` QUIC, `:19837` UDP | **No** — all ports >1024 |
| `yaver code` | TUI / attached coding sessions | **No** |
| `yaver vault {add,get,env}` | Reads/writes `$HOME/.yaver/vault.json` (encrypted) | **No** |
| `yaver wire {detect,push}` | USB device install + push | **No** |
| `yaver wireless push` | WiFi-paired install + push | **No** |
| `yaver insert <app>` | Hermes push to paired phones via blackbox bus | **No** |
| `yaver primary {status,auth,set,ping}` | Primary device management | **No** |
| `yaver devices` | List registered devices | **No** |
| `yaver ssh <alias>` | SSH wrapper resolving LAN → Tailscale → device row | **No** |
| `yaver alias set` | Local alias storage in `$HOME/.yaver/` | **No** |
| `yaver doctor` | Diagnostics, read-only | **No** |
| `yaver status` | Agent + auth + relay state | **No** |

## All state lives in `$HOME/.yaver/`

```
$HOME/.yaver/
├── config.json              ← auth token, device ID, voice config
├── vault.json               ← encrypted secrets (NaCl secretbox)
├── paired_tokens.json       ← additional accepted auth tokens
├── runner_tokens.json       ← runner OAuth ledger (hashes only)
├── bin/<version>/...        ← downloaded signed binaries per platform
├── voice-input/             ← test WAV fixtures
└── (other host-local state)
```

**Nothing writes to** `/etc`, `/var`, `/opt`, `/usr/local` during normal
operation. Confirmed by repo-wide grep over `desktop/agent/*.go`.

## Explicit root opt-ins (each one prompts via sudo at the moment of use)

These commands DO need root, but never silently — they're explicit user
choices and they prompt via `sudo` at the moment they need it:

| Command | Why root | Where it writes |
|---|---|---|
| `yaver serve --install-systemd` | One-time systemd unit install | `/etc/systemd/system/yaver-agent.service` |
| `yaver serve --install-launchd-daemon` | One-time launchd install (macOS) | `/Library/LaunchDaemons/io.yaver.agent.plist` |
| `yaver install <pkg>` | Dispatches apt/dnf/pacman/brew | System package manager |
| `yaver domain add <host>` | Rewrites local DNS | `/etc/hosts` |
| Some MCP sysadmin tools | Explicit LLM-invoked verbs | Varies — each runs `sudo <cmd>` |

## Install-time root (npm-install convention, not Yaver-specific)

`npm install -g yaver-cli` historically writes to `/usr/local/lib/node_modules/`
which needs root. This is npm's behavior, not Yaver's. Users on `nvm`
or other per-user node setups can install Yaver without sudo because
their global node_modules lives in `$HOME`.

The `cli/src/postinstall.js` opportunistically installs `tmux`,
`bubblewrap`, and `uidmap` ONLY when:
- `process.geteuid() === 0` (running as root anyway, typical -g install)
- `apt-get` is available

When NOT root, postinstall prints `yaver install tmux` hint and continues.
**Yaver itself runs fine without these packages** — you just lose tmux
adoption (`/spatial`'s 3-pane terminal layout shows the bootstrap pane
asking you to install tmux).

## Startup guard against `sudo yaver serve`

If a user accidentally runs `sudo yaver serve` (out of habit / copy-paste),
the agent prints a clear warning **but continues running** — because some
environments (managed-cloud boxes, containers without USER directive)
legitimately have root as the only user. The warning makes the footgun
visible so users can Ctrl-C and re-run as themselves before state files
get created with root ownership.

Implementation: `desktop/agent/no_root_check.go::warnIfRunningAsRoot()`.

## Verification recipe

To prove the no-root contract on your own machine:

```bash
# 1. Install yaver-cli (npm convention — may need sudo for global install)
npm install -g yaver-cli@latest

# 2. From here on, NEVER use sudo with yaver
whoami                                       # confirm you're not root
yaver auth                                    # OAuth, browser
yaver serve &                                 # agent starts, no sudo

# 3. Confirm zero root-owned files
ls -la ~/.yaver/                              # all owned by you
ss -tlnp 2>/dev/null | grep -E ':18080|:4433|:19837'
                                              # ports >1024, no CAP_NET_BIND_SERVICE

# 4. Run a few regular commands — all should succeed without root
yaver status
yaver devices
yaver vault add TEST_KEY --value "hello"
yaver vault get TEST_KEY
```

If any of these prompt for sudo, file a bug at github.com/kivanccakmak/yaver.io
— it's a regression of the no-root contract.

## Why this matters for Cagrı's trio

Cagrı's Linux PC runs `yaver serve` as himself. His tmux sessions are
owned by him. His vault, config, runner tokens all live in his `~/.yaver/`.
When his Android phone connects via /spatial, the agent uses HIS user
identity to spawn PTYs and attach to HIS tmux sessions. No privilege
escalation anywhere in the loop.

A sudo'd `yaver serve` would create root-owned state files that break
his subsequent non-sudo invocations — which is exactly the footgun the
startup warning catches.

## Audit summary

Confirmed 2026-05-28 via repo-wide grep:

- ✅ `desktop/agent/*.go` build tags: only `*_windows.go` use platform tags. Linux + macOS share code paths.
- ✅ Sudo calls in agent: 8 occurrences, ALL in opt-in features (dns.go DNS rewriting, mcp_sysadmin.go LLM-invoked tools, tier_a_polish.go caddy install). Never in the regular path.
- ✅ `/etc`, `/var`, `/opt`, `/usr/local` writes: only `/etc/systemd/system/yaver-agent.service` (explicit `--install-systemd` flag) and `/Library/LaunchDaemons/...` (explicit `--install-launchd-daemon`). No silent writes.
- ✅ `os.Geteuid()` checks: used for permission-aware logic, not blocking. Yaver continues without root.
- ✅ Privileged ports: zero. 18080 / 4433 / 18443 / 19837 all >1024.
- ✅ ConfigDir = `$HOME/.yaver/`. All state derives from this.
