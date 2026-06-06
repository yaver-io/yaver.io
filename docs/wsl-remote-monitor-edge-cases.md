# Remote screen-monitoring of a WSL2 PC — edge-case analysis

Field notes from getting `yaver screenlog` running on a real Windows 11 + WSL2
box (AHMET-LENOVO), authed remotely under another user's iCloud, recording the
**Windows** desktop, surviving reboots, and (the hard part) being **reachable
for viewing from a phone/web app**. Most of this generalises to "manage a
family member's PC over the internet."

The TL;DR up front: **local capture + reboot-durability is solved and robust.
Remote *reachability* of a WSL2-hosted agent is where every edge case lives**,
because the agent runs inside a NAT'd VM. The clean exits are *mirrored
networking*, *a WireGuard mesh*, or *not using WSL at all*.

---

## 0. The layered architecture (where each layer can bite)

```
 your phone / web  ──┐
                     │  TRANSPORT  (LAN-direct | relay | mesh)        ← §1  most edge cases
 your Mac (kivanc) ──┘        │
                              ▼
              ┌──────────── Windows 11 host (ahmet) ───────────┐
              │  sshd? OpenSSH? RustDesk?   ← §3 management access │
              │  ┌────────── WSL2 VM (Hyper-V NAT) ───────────┐  │
              │  │  yaver agent (Linux binary)                │  │
              │  │    capture ──interop(powershell.exe)──►  Windows desktop  ← §2
              │  │    autostart: wsl.conf / systemd / VBS     │  ← §4
              │  └────────────────────────────────────────────┘  │
              └────────────────────────────────────────────────┘
```

A WSL2 agent is **two NATs deep** from the internet (Hyper-V NAT inside the
LAN's NAT), and capture reaches *sideways* into Windows over interop. Every
arrow is a failure surface.

---

## 1. Transport reachability — the core problem

The agent must be reachable from outside for the phone/web app to view it.
Three transports, each blocked differently by WSL2:

### 1a. LAN-direct — blocked
- **Symptom:** `curl http://10.0.0.13:18080/health` from a same-LAN Mac → empty/timeout, even though the agent's `/health` is fine *inside* WSL.
- **Root cause:** the agent binds `0.0.0.0:18080` **inside the WSL2 VM** (`172.31.x.x`). WSL2 uses Hyper-V NAT, so the service is **not** published on the Windows host IP (`10.0.0.13`). The host doesn't forward to it.
- **Fixes:** (a) `netsh interface portproxy` on Windows (admin) forwarding `10.0.0.13:18080 → <wsl-ip>:18080` — but the WSL IP changes every boot, so it's brittle; (b) **mirrored networking** (§1d) — WSL shares the host's interfaces, so the bind lands on `10.0.0.13` directly.

### 1b. Relay (`public.yaver.io`) — blocked by the Hyper-V NAT
- **Symptom:** `yaver ping <device>` from the Mac → `unreachable — every transport candidate failed`, even after fixing buffers.
- **Root cause — three stacked bugs** (documented historically in `docs/wsl2-relay-troubleshooting.md`):
  1. **Tiny UDP socket buffers.** WSL defaults `net.core.rmem_max/wmem_max` to ~208 KB; QUIC wants ~7.5 MB. Undersized buffers drop the relay's QUIC handshake. *Necessary to fix, not sufficient.*
  2. **Empty advertised QUIC addr** (older agents) — the agent didn't know its reachable address.
  3. **Hyper-V NAT dropping UDP handshakes.** Even with big buffers, the outbound QUIC handshake to the relay gets eaten by the NAT. **This is the one that kills it.**
- **What we tried:** set `net.core.rmem_max=net.core.wmem_max=7500000` (as root via `wsl.exe -u root sysctl`, *no sudo needed*), persisted in `/etc/sysctl.d/99-yaver.conf`. Buffers fixed → relay *still* unreachable. Confirms bug #3 is the blocker.
- **Why buffers-as-root-via-interop is a neat trick:** ahmet isn't a Linux sudoer, but `wsl.exe -u root -- sysctl -w …` runs as root in the VM because the *Windows* user controls WSL — sidesteps the missing sudo entirely.

### 1c. Mesh (WireGuard overlay) — the cleanest traversal, but unshipped
- A `100.96/12` WireGuard overlay gives both ends a stable overlay IP and does NAT traversal properly (the agent isn't *behind* the NAT on the overlay). This is the *right* answer for double-NAT.
- **Status:** built but uncommitted/experimental (`project_yaver_mesh_wireguard`). Not yet a turnkey path.

### 1d. Mirrored networking — fixes transport, **breaks SSH** (the central trade-off)
- `%USERPROFILE%\.wslconfig` → `[wsl2] networkingMode=mirrored` + `wsl --shutdown`. WSL then **shares the Windows host's network namespace** → the agent's bind lands on `10.0.0.13`, and outbound QUIC bypasses the Hyper-V NAT → **relay + LAN both work**.
- **The catch:** our SSH-in was via a **pre-existing Windows portproxy** (`10.0.0.13:2222 → wsl-nat-ip:2222`). Under mirrored mode that portproxy points at a now-invalid NAT IP and **conflicts** → SSH gives `banner exchange timeout` / `connection reset` (port accepts TCP, nothing answers). Net: **mirrored fixes the *app* transport but kills the *management* transport.**
- **Requires:** `networkingMode=mirrored` needs **Windows 11 22H2+** (build ≥ 22621). AHMET-LENOVO is 26200 → supported.

> **Key insight:** for *viewing*, SSH is irrelevant — the relay is the path. So mirrored networking is acceptable *if* you accept managing the box via RustDesk instead of SSH, and validate the relay directly.

---

## 2. Capture (WSL → Windows desktop) — solved, with sharp edges

### 2a. `powershell.exe` not on `$PATH` in non-interactive contexts — FIXED
- **Symptom:** the agent fell back to the Linux (`scrot`) path and errored "no screenshot tool" — even though interop works — whenever serve ran from **sshd** or a **systemd service**.
- **Root cause:** WSL auto-appends the Windows `PATH` only for **interactive login** shells. An sshd-spawned shell or a systemd service has a minimal `PATH` → `LookPath("powershell.exe")` fails → `wslShouldUseHost()` returns false → Linux path.
- **Fix (shipped in 1.99.262):** resolve `powershell.exe` by **absolute path** (`/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe`) when it's not on `$PATH`. Now capture works from *any* context with zero PATH plumbing — essential for autostart.

### 2b. Interop works even with `WSL_INTEROP` unset
- Over SSH, `WSL_INTEROP` and `WSL_DISTRO_NAME` are empty, yet `powershell.exe` (by abs path) still runs. Interop is registered via `binfmt_misc` at VM boot, system-wide — not gated on the per-session env var. (Useful: don't gate logic on `WSL_INTEROP`.)

### 2c. Capture needs an **unlocked, interactive** Windows session
- `CopyFromScreen` (GDI) returns **black** on the lock screen, a logged-off session, or DRM-protected surfaces (Netflix etc.). So "monitor after reboot" only yields real frames **after the user logs in**. Validated: a real 1920×1080, 200 KB+ frame requires the desktop to be present.

### 2d. Multi-monitor & DPI
- `VirtualScreen` captures the union of all monitors at full res; we downscale to 1920px-wide jpg. Mixed-DPI multi-monitor can clip at the edges (minor). Future fidelity: DXGI Desktop Duplication (captures DRM/HW-accelerated content too).

---

## 3. Management access (SSH) — the fragile layer

### 3a. sshd not enabled at WSL boot — FIXED
- **Symptom:** after `wsl --shutdown`, sshd never came back → locked out.
- **Root cause:** `ssh.service` was **`disabled`** in systemd (someone started it by hand). A shutdown → reboot left no sshd.
- **Fix:** `wsl -u root systemctl enable ssh` → symlink in `multi-user.target.wants`. Now sshd auto-starts on every WSL boot.

### 3b. WSL doesn't auto-start after a host reboot
- **Symptom:** after the mirrored reboot, the box pinged (Windows up) but `2222` had nothing behind it — WSL was simply **off**. WSL2 never auto-boots; *something* must run `wsl.exe`.
- **No remote trigger exists:** once the VM is down, interop is dead, so you can't `wsl.exe` it back from inside. Only a **Windows-side** action boots it. (This is why a `wsl --shutdown` done remotely without a recovery trigger = lockout.)

### 3c. SSH `banner exchange timeout` vs `connection reset`
- `timeout` = a portproxy accepts the TCP but the WSL sshd behind it is down (VM off).
- `connection reset` = sshd/VM is in a transitional/broken state.
- Both mean "TCP reachable, SSH not serving" — distinguish from a clean "refused" (nothing listening).

---

## 4. Autostart / reboot-durability — SOLVED (validated on a real reboot)

Getting "agent + recording back after reboot, hands-off" required navigating
several traps:

### 4a. `yaver serve` **double-forks** (daemonizes) — breaks naive systemd
- **Symptom:** `systemctl start yaver` → "Yaver agent started (PID N)" then immediately `Deactivated successfully`; service `inactive` while an orphan agent runs.
- **Root cause:** serve forks a background agent and the launcher exits. `Type=simple` tracks the launcher → thinks it died. `-debug` only changes logging, **not** the fork. `Type=forking` + `PIDFile` raced and didn't latch cleanly.
- **What worked:** drop systemd entirely for this; use the **WSL-native** `/etc/wsl.conf` `[boot] command = su - ahmet -c '…/yaver serve'`, which runs on every VM boot and doesn't care about daemonization. (A `Type=oneshot RemainAfterExit=yes` service is an alternative but the cgroup can reap the forked child.)

### 4b. "Already running on :18080 — reusing" + port reaping
- Restarting serve while an old one holds `:18080` → the new launcher exits without forking. Must `fuser -k 18080/tcp` + `pkill` the **port holder** (not just by name) before restart.

### 4c. Silent WSL boot at logon
- A hidden VBS in the **Startup** folder (`WScript.Shell.Run "wsl.exe -d Ubuntu -e true", 0, False`) boots WSL at logon with no visible window → triggers `wsl.conf` → serve → screenlog. Runs as the user, **no admin**.

### 4d. screenlog auto-resume with **no auth/internet** — shipped in 1.99.262
- A local `~/.yaver/screenlog/autostart.json` marker (set by `start --persist`). On serve start, `resumeScreenlogIfEnabled()` restarts recording — gated only by the local kill-switch, **independent of auth/relay/internet**. So a signed-out, offline, just-rebooted box still records. **Validated across a real reboot: WSL booted silently → serve started → recording resumed on its own.**

---

## 5. Privilege & distribution edge cases

### 5a. The recorded user is **not a local admin**
This single fact blocks a whole column of "clean" options:
- ❌ OpenSSH **Server** install (`Add-WindowsCapability`) — admin.
- ❌ `netsh portproxy` — admin.
- ❌ Network-adapter "don't power off to save power", system sleep/power-plan — admin (Device Manager / `powercfg`).
- ❌ RustDesk-as-**service** — admin.
- ✅ Still doable user-level: WSL root via `wsl -u root`, user scheduled tasks / Startup VBS, `.wslconfig`, RustDesk **portable** + logon autostart, a `SetThreadExecutionState` **keep-awake** loop (prevents sleep without admin).

### 5b. The npm launcher **re-downloads** a replaced binary
- **Symptom:** `cp my-build ~/.yaver/bin/<ver>/linux-amd64/yaver` → next run still the old code.
- **Root cause:** the `yaver` shim validates the binary (signature/size) and **re-downloads** the official one on mismatch (our custom build fails the check).
- **Fix:** run the custom binary from a **separate path** (`~/.yaver/yaver-custom`) and point autostart at it directly — don't fight the shim. (Proper fix: ship the change as a signed release, which we did: 1.99.262.)

### 5c. `cp` over a **running** executable → `text file busy`
- Must `pkill` the agent (and free `:18080`) **before** replacing the binary, then restart. Overwriting a live ELF fails with ETXTBSY.

### 5d. npm dist-tag lag
- Right after a release, `npm install yaver-cli@latest` briefly resolved to the *previous* version (dist-tag propagation). Pin the exact version (`@1.99.262`) when you need it now.

### 5e. Custom unsigned binary vs signed release
- A scp'd dev binary is unsigned/unnotarized and self-reports a stale version string (`1.99.258`) even with new code. Fine for validation; ship the **tagged release** (`cli/v*` → signed) for production.

---

## 6. Operational gotcha: SSH + interop output truncation
Long `ssh … 'bash -s' <<HEREDOC` runs that shell `wsl.exe`/`cmd.exe`/
`powershell.exe` frequently returned **partial output** — the command *ran*
but the captured stdout was cut. Lesson: **one verifiable action per SSH
call**, and *verify state in a separate call* rather than trusting the inline
echo. (Operational, not architectural — but it cost real debugging time.)

---

## 7. Decision matrix — making remote viewing actually work

| Option | Relay/LAN viewing | Management access | Admin needed | Reboot-durable | Verdict |
|---|---|---|---|---|---|
| **WSL + relay, NAT** | ❌ (Hyper-V NAT) | ✅ SSH (fragile) | no | ✅ (we built it) | recording yes, **viewing no** |
| **WSL + mirrored networking** | ✅ relay + LAN | ⚠️ SSH-2222 breaks → use RustDesk | no | ✅ | **fastest to viewing**; manage via RustDesk |
| **WSL + WireGuard mesh** | ✅ direct overlay P2P | ✅ over overlay | no | ✅ | cleanest traversal, **but mesh unshipped** |
| **Native Windows yaver** | ✅ no NAT at all | ✅ native OpenSSH | **yes** (OpenSSH Server) | ✅ (Task Scheduler) | **cleanest long-term**; needs an admin login once |
| **WSL + Windows portproxy** | ⚠️ LAN only, breaks each boot (WSL IP changes) | ✅ | **yes** (netsh) | ⚠️ | brittle, not recommended |

**Recommendations**
1. **Want viewing tonight, no admin:** mirrored networking. Accept SSH-2222 loss; manage via RustDesk; **validate the relay from the Mac** (we're on both sides). Persist the buffer fix (done) so it survives reboots.
2. **Want it permanently clean:** native Windows yaver + OpenSSH Server (one admin login). No WSL, no NAT, no fragile SSH. The binary is already built.
3. **Strategic:** finish + ship the **WireGuard mesh** — it's the only option that's both no-admin *and* not-fragile, and it solves this for every double-NAT user, not just WSL.

---

## 8. Status snapshot (as of this session)

| Capability | State |
|---|---|
| WSL→Windows capture | ✅ validated (real desktop, dedup, active-window via interop) |
| Reboot-durable auto-resume | ✅ validated across a **real reboot**, auth/internet-independent |
| Resource bounds | ✅ jpg/1920/dedup, 4 GB disk cap, 5000-frame RAM cap, 7-day retention |
| Optimized cheap defaults | ✅ jpg q60 + downscale (CPU 38%→11% on Retina) |
| Agent release | ✅ `yaver-cli 1.99.262` (persist + powershell-abs-path) |
| Web + mobile DVR scrubber | ✅ shipped (forward/back/play) |
| RustDesk fallback + keep-awake + ssh-autostart | ✅ in place |
| **Remote viewing transport (relay/LAN to his box)** | ❌ blocked by WSL2 NAT — needs mirrored / mesh / native |
| Mobile app build with screenlog UI | ⏳ needs a build (`yaver wireless push` / store) |
| Input capture (keylogger/mouse, timestamp-synced) | ⏳ off by default; opt-in available |
