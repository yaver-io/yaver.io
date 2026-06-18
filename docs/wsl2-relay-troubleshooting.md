# WSL2 relay troubleshooting

Why your office Ubuntu-on-WSL2 machine appears "online" in the Yaver
mobile app but reconnects forever, and how to fix it end-to-end. This
doc was written after a live outage on `Ofis2` — see the findings
table at the bottom for the exact errors you'll see.

## TL;DR — three independent failure modes

| Symptom in mobile Connection Logs | Root cause on the WSL2 box | Fix |
|---|---|---|
| `sendmsg: invalid argument` with `->:0` as the peer address | `cached_relay_servers[].quic_addr` is empty in `~/.yaver/config.json`; agent knows the HTTPS relay URL but not its UDP address, so QUIC dial is a zero-port write | Patch `quic_addr` to the canonical relay UDP (`relay.example.com:4433` for `public.yaver.io`), then restart. `yaver auth` on a fresh box writes this correctly; an older cached config may be missing it. |
| `dial relay: timeout: no recent network activity` immediately after connect | Kernel UDP buffers too small for the quic-go handshake (WSL2 defaults to ~208 KiB; quic-go wants ≥ 7.5 MiB on every tunnel) | `yaver serve` now raises this automatically (`maybeRunWSL2NetworkTuning` in `desktop/agent/wsl.go`). Manual: `sudo sysctl -w net.core.rmem_max=7500000 net.core.wmem_max=7500000`. |
| Same `no recent network activity` *after* the buffers are raised | WSL2's default NAT drops QUIC handshake packets (Microsoft-side Hyper-V virtual-switch issue; confirmed on Ubuntu 22.04/24.04 + WSL2 kernel 6.6.x) | Switch the Windows host to **mirrored networking** (`[wsl2] networkingMode=mirrored` in `%USERPROFILE%\.wslconfig`, then `wsl --shutdown`). Requires Windows 11 22H2+. If that's not possible, run the agent on the Windows host itself, or use the remote-worker pattern below. |

If the relay tunnel still can't come up after all three, the agent is
only reachable via LAN beacon from phones on the same Wi-Fi as the
Windows host (not the phone's carrier). Relay roaming will not work.

## Why this keeps biting users on WSL2

- `ip link show eth0` on WSL2 shows `mtu 1280`. QUIC-go paces around
  1200 bytes/packet, which is fine, but combined with the small
  default UDP buffers the handshake batch can overflow and get
  silently dropped.
- WSL2's default network is a Hyper-V NAT. It handles TCP and plain
  UDP fine (ping, nc, HTTPS) but Microsoft has multiple open issues
  where QUIC Initial packets get dropped on the virtual switch.
  "Mirrored" mode bypasses this by putting WSL on the same link as
  Windows, so the relay's QUIC packets are forwarded natively.
- The mobile app looked connected briefly because a direct LAN
  probe to `172.29.0.10:18080` (the WSL internal NAT IP) happened
  to succeed during the 2 s direct-first race. That IP is not
  reachable from the phone's actual network; subsequent HTTP calls
  all timed out and triggered reconnect.

## What `yaver serve` now does for you

On every start:

1. `detectWSLRuntime()` reads `/proc/version` + env vars to
   distinguish WSL1 / WSL2 / bare Linux.
2. On WSL2 only: reads `net.core.rmem_max` and `net.core.wmem_max`.
3. If either is below 7.5 MiB, writes `/proc/sys/net/core/rmem_max`
   and `wmem_max` directly (works if the process is root, which is
   the default inside WSL2; otherwise no-op).
4. If we can't write, prints a one-screen remediation banner with
   the exact `sudo sysctl` commands, and points at this doc for the
   mirrored-networking follow-up.

Code path: `desktop/agent/wsl.go::maybeRunWSL2NetworkTuning` called
from `runServe` and `runAuth`.

## Manual steps for an already-deployed box

If you have a v1.95.6 (or older) agent already running and the user
can't update right away:

```bash
# 1. Fix the cached relay config (make sure quic_addr is set)
python3 - <<'PY'
import json
p = "/home/$USER/.yaver/config.json"
cfg = json.load(open(p))
changed = False
for r in cfg.get("cached_relay_servers", []):
    if not r.get("quic_addr"):
        r["quic_addr"] = "relay.example.com:4433"
        changed = True
if changed:
    json.dump(cfg, open(p, "w"), indent=2)
    print("patched")
PY

# 2. Raise UDP buffers
sudo sysctl -w net.core.rmem_max=7500000 net.core.wmem_max=7500000
grep -q "net.core.rmem_max" /etc/sysctl.conf || \
  printf 'net.core.rmem_max=7500000\nnet.core.wmem_max=7500000\n' | sudo tee -a /etc/sysctl.conf

# 3. Install Node if missing (Expo won't start without it)
yaver install node   # sudo-free, installs to ~/.yaver/runtimes/node

# 4. Restart the agent
pkill -f "yaver serve" || true
nohup yaver serve --debug --port=18080 --quic-port=4433 --work-dir="$HOME" >> ~/.yaver/agent.log 2>&1 &

# 5. Watch the relay log
tail -f ~/.yaver/agent.log | grep RELAY
```

If step 5 still shows `timeout: no recent network activity` in a
loop, the Windows host's NAT is dropping QUIC. Move to mirrored
networking:

```powershell
# On Windows, in PowerShell
notepad $env:USERPROFILE\.wslconfig
# Add:
# [wsl2]
# networkingMode=mirrored
wsl --shutdown
```

## When mirrored networking isn't an option

Some Windows 10 / older Windows 11 hosts don't support mirrored
mode. In that case the WSL2 agent is effectively LAN-only. Two
remaining options:

1. **Run `yaver serve` on the Windows host**, not inside WSL. Go
   builds a Windows binary natively. Hermes push-to-phone works
   identically. Windows uses a Scheduled Task for auto-start, and is
   the right place to run Tailscale plus the host power settings for
   unattended remote use.
2. **Use the remote-worker pattern** — have the phone connect to a
   Mac / Linux box that *does* have a working relay tunnel, and
   have that box drive Hermes builds for the WSL project via `git
   clone` or rsync. See `architecture/REMOTE_WORKER.md` Layer 1 (`dev_*` tools
   with `device_id=`).

## Verifying end-to-end after a fix

From the WSL box:

```bash
# UDP path clear to the relay?
nc -vzu -w 3 relay.example.com 4433

# Buffers big enough?
sysctl net.core.rmem_max net.core.wmem_max

# Agent registered?
tail -30 ~/.yaver/agent.log | grep -E "RELAY|registered"
# Want: "Connected to relay ... registered as <deviceId>"
```

From a second machine:

```bash
curl -s https://public.yaver.io/health
# tunnels should increment by 1 for each connected agent
```

From the phone:

1. Open Yaver app, go to Devices tab.
2. Tap the WSL box. Mode badge should flip to `relay`, not `null`.
3. Open the Hot Reload tab and tap a project. If Metro starts
   without a 15× reconnect loop, the tunnel is healthy.

## Field findings reference (`Ofis2`, 2026-04-18)

The incident that motivated this doc:

- Config had `quic_addr: ""` — the agent dialed `relay.example.com:0`
  which returned `sendmsg: invalid argument` every 30 s.
- Node was not installed, so every attempt to run a mobile project
  would have failed at `/usr/bin/env: 'node': No such file or
  directory` even if the tunnel had been up.
- Default UDP buffers were `rmem_max=wmem_max=212992` (208 KiB).
  After raising to 7.5 MiB, the error changed from
  `sendmsg: invalid argument` to `timeout: no recent network
  activity` — progress, but not enough on default WSL2 NAT.
- `nc -u -w 3 relay.example.com 4433` from WSL succeeded. That's what
  made this one confusing: plain UDP connectivity tests pass but
  real QUIC handshakes don't.

The permanent fix on that machine was mirrored-mode networking plus
the automatic buffer tuning now shipped in `wsl.go`.
