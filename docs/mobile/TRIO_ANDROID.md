# Android trio dogfood — XReal Air + foldable BT keyboard + phone

The cheapest viable "vibe code anywhere" setup. Built around Cagrı
Sofuoğlu's actual rig (May 2026): Android phone with USB-C DisplayPort
Alt Mode, XReal Air via cable, foldable Bluetooth keyboard. No laptop.

## Hardware shopping list

| Item | Approx cost (TR) | Notes |
|---|---|---|
| **Android phone with USB-C DP Alt Mode** | already have | Most Android phones from 2022+. Check: charge cable + a known DP-alt-mode device |
| **XReal Air** (or borrow from Cagrı) | ~13,500 TRY / $380 | Wired display glasses, mirrors phone screen at virtual 1080p |
| **Foldable Bluetooth keyboard** | ~1,000 TRY / $30 | Greethga / Plopite / any clone. The image in our chat is fine — has Ctrl/Alt/Esc, iOS/Android/Windows mode switches |
| **Yaver managed cloud box** | $8/mo Hetzner | Remote dev machine where Claude actually runs |
| **OPTIONAL Deepgram + Cartesia, OR OpenAI key** | ~$3-9/mo voice | Skip entirely for keyboard-only mode |
| | | |
| **TOTAL trio cost** | ~$410 + $8/mo | vs Quest 3 ($499 + import), vs Vision Pro ($3499) |

## Test recipe — first 5 minutes after you have all three

```bash
# 1. On your Mac (the agent host)
yaver serve                              # agent on :18080
yaver sdk token --scope feedback,voice   # → copy the printed token

# 2. On the Android phone
#    - Pair the BT keyboard once via Settings → Bluetooth
#    - Plug XReal Air USB-C into the phone
#    - Phone screen now shows on glasses at virtual ~120"
#    - Open Chrome

# 3. Navigate to:
https://yaver.io/spatial?agent=https://YOUR-AGENT-HOST:18080&token=PASTED_TOKEN

# 4. /spatial detects "Android Trio" automatically:
#    - Green nudge banner appears top center: "🤝 Yaver trio detected..."
#    - 3-pane terminal layout fills the 1920x1080 mirror
#    - Surface badge top-left shows "Android Trio"
#    - Tap "?" on the BT keyboard for the cheat sheet
```

## Keyboard-driven vibe loop

| Shortcut | What it does |
|---|---|
| `j` / `k` | Next / previous Claude session pane |
| `1` `2` `3` | Jump directly to pane N |
| `Space` | Toggle voice (skip when no voice keys configured — orb hides) |
| `Esc` | Cancel voice / close any modal |
| `gg` / `G` | Scroll terminal pane top / bottom |
| `?` / `h` | Toggle help overlay |
| `v` | Enter VR (no-op on XReal — Quest/Vision only) |

You spend most of your time on `j`/`k` swapping between agent sessions and
reading the live terminal output. Voice is the optional layer for moments
when typing is awkward (lying down, eating, walking).

## What works · what's broken · what's irrelevant

| Feature | Status on Android trio |
|---|---|
| `/spatial` 3-pane terminal layout | ✅ Detected, 1080p mirror looks crisp |
| Yaver-trio keyboard shortcuts | ✅ Shipped in `cli/v1.99.225` web bundle |
| Voice (OpenAI default OR Deepgram+Cartesia) | ✅ Optional; configure in `~/.yaver/config.json` if wanted |
| Liquid Glass UI | ⚠️ Falls back to Material 3 surfaces on Android — by design |
| Hermes-push (mobile-app reload) | ✅ Android `YaverBundleLoaderModule.kt` mirrors the iOS path — `loadApp` works identically on both platforms |
| Yaver mobile app (Tasks tab etc.) | ✅ Works on Play Store build 240; complements `/spatial` |
| Linux container via localdesktop.github.io | ✅ Cagrı's setup — runs alongside Yaver, doesn't conflict |
| WebXR immersive-vr scene | ❌ XReal Air mirrors phone screen; no WebXR sessions |

## Why this combo is the wedge

**Quest 3** = the showy demo (3 floating 3D panes around you), but $499
and standalone — overkill for daily vibe coding.

**Vision Pro** = same shape as Quest 3 via Safari, but $3499 — unjustifiable
for an indie dev tool unless you already own one.

**Android trio** = $410 in hardware + already-owned phone, leans on Yaver
managed cloud for the expensive bit (dev machine), runs everywhere there's
cellular. Beach, train, plane, café. Cable is the only friction.

**The product story for early Yaver users:**

> "You don't need a laptop. Pair this $30 keyboard, plug these $380 glasses
> into your existing Android phone, and you've got a real dev environment
> that fits in a pocket. The expensive box stays at home."

When Cagrı's XReal arrives or you borrow his for an evening: this is the
path of least resistance to validating the wedge.

## Battery + connection caveats

- XReal Air draws power from the phone via USB-C → expect ~2 hr of vibing
  before the phone needs a charger. Carry a small USB-C power bank for
  longer sessions.
- Phone connects to your agent over LAN (Tailscale recommended) or via
  Yaver managed cloud + relay. Cellular works but voice latency may
  increase by 100-200 ms.
- BT keyboard battery: ~3 months on a charge for most foldables. Once it
  dies mid-session you fall back to the on-screen keyboard, which is
  miserable on a glass-mirrored display. Charge it weekly.

## Cagrı's specific setup: Linux PC as the remote dev box

Cagrı already runs everything on Linux (per his localdesktop.github.io
container setup, May 2026 chat). His full Yaver path:

```
LINUX PC (his actual dev box)
  ├─ npm install -g yaver-cli@latest   ← signed agent binary installs
  ├─ yaver auth                          OAuth (Apple / Google / GitHub / GitLab)
  │                                       Or yaver auth --headless if SSH-only
  └─ yaver serve                         agent on :18080, tmux ready
                  │
                  │  HTTPS over LAN / Tailscale / yaver.io QUIC relay
                  ▼
ANDROID PHONE (same Convex account)
  ├─ Play Store: install Yaver app       ← needs internal-track tester invite
  ├─ Chrome: yaver.io/spatial?…           ← OR drive everything via web
  ├─ Foldable BT keyboard paired
  └─ XReal Air via USB-C
```

### Linux install — verified working

The agent has ZERO platform-specific code outside `*_windows.go` files.
Linux x64 + arm64 are first-class:

```bash
# Cagri's Linux box (assumes Node.js + npm available)
sudo apt install tmux                    # if not already (Cagri has it)
npm install -g yaver-cli@latest          # downloads signed agent binary
                                          # auto-provisions hermesc from
                                          # source on linux/arm64 if needed
yaver auth                                # OAuth in browser, or:
yaver auth --headless                     # short-code flow for SSH-only

yaver serve                               # agent on :18080
                                          # Auto-publishes external IP
                                          # to Convex device row so phone
                                          # can find it via discovery
```

That's it. Cagri's Linux PC is now a remote dev box reachable from his
phone over yaver.io's QUIC relay or directly on LAN.

### Network reachability — three paths, agent picks the best

When Cagri's phone connects from a coffee shop / airport / beach:

1. **Tailscale recommended** — same Tailnet on his phone + Linux. Direct
   100.x IPs work as if on LAN, no public exposure. He adds his phone to
   his Tailnet once.
2. **yaver.io QUIC relay** — built-in fallback. The Yaver server farm
   relays QUIC packets between phone and Linux PC, password-protected,
   pass-through (no task data stored).
3. **Direct LAN** — when both are on the same WiFi, the phone finds the
   Linux box via Yaver's UDP beacon (port 19837) and connects directly.

The agent's connection manager tries direct → Tailscale → relay in that
order; no config required from Cagri.

### TestFlight + Play tester invite — what you do (kivanc) to add Cagri

I can't add testers autonomously (needs your dashboards + Cagri's emails).
Both invites are 60-second flows:

**TestFlight (iOS) — add Cagri's Apple ID email:**
- https://appstoreconnect.apple.com → Yaver → TestFlight → Internal Testing
- Click "+" next to your existing Internal group (or create a new one)
- Enter Cagri's Apple ID email → Yaver sends invite email
- Build 353 (v1.18.124) is already processing; he gets it when Apple
  finishes (usually 10-30 min from upload)

**Play Store (Android) — add Cagri's Google account to internal track:**
- https://play.google.com/console → Yaver → Testing → Internal testing
- Tester list → add tester
- Enter Cagri's Google account email
- Click "Copy link" under Testers → DM the link to Cagri
- He opens it on his Android, opts in, then installs from Play Store
  (build 241 / v1.18.124 already live)

### Tmux on Cagri's Linux box

Already wired and battle-tested. Specific guarantees:

- `/tmux/sessions` enumerates running sessions on his Linux box
- `/tmux/adopt`, `/tmux/detach`, `/tmux/input` work the same as on macOS
- `/ws/terminal` opens a PTY-backed shell via `creack/pty` library
  (Linux + macOS share the same Go API; `pty.Start(cmd)` works identically)
- His existing `.tmux.conf` on Linux is what `/spatial` projects onto
  the glass — prefix Ctrl-b h to split, etc. We don't ship a separate
  config.

If Cagri's Linux box doesn't have tmux installed, `apt install tmux`
fixes it. Yaver gracefully reports "no tmux sessions" until he creates
his first one with `tmux new -s yaver`.

### One concrete walkthrough for Cagri's first vibe

```bash
# === On his Linux PC ===
npm install -g yaver-cli@latest
yaver auth          # Google OAuth
yaver serve         # leave running

# He sees: Listening on http://0.0.0.0:18080
#           Reachable as primary-LINUX-HOST via Tailnet / relay
#           Convex device row populated as kivanc.tail-XYZ.ts.net

# === On Android phone (Cagri's) ===
# 1. Tap TestFlight invite link → install Yaver mobile (if iOS)
#    OR open the Play Store internal-testing opt-in link → install
#    OR skip the app entirely and use the web route below
#
# 2. Plug XReal Air USB-C
# 3. Pair foldable BT keyboard
# 4. Chrome → yaver.io/spatial?surface=android-trio
#    (sign in with same Google account → token auto-provisioned)
#
# 5. Three terminal panes appear, attached to his Linux tmux sessions.
#    Click center pane → keystrokes go to PTY.
#    Ctrl-b h splits his real tmux in same cwd.
#    He vibe codes on his Linux box from the beach.
```

### What COULD go wrong for Cagri (and fixes)

| Symptom | Likely cause | Fix |
|---|---|---|
| `npm install -g yaver-cli` fails on linux/arm64 | hermesc not pre-provisioned | Re-run as root, or `apt install cmake ninja-build clang libicu-dev` then re-run |
| Phone can't reach Linux | NAT + no Tailscale + relay timing out | Install Tailscale on both, login same account, retry |
| `/spatial` says "no tmux sessions" | tmux not installed on Linux | `apt install tmux && tmux new -s yaver` |
| Keystrokes feel laggy | Cellular round-trip from coffee shop → home | Try Tailscale (faster, lower jitter than yaver.io relay) |
| XReal sun glare | Classic Air, not Air 2 Pro | Test in shade first; consider Air 2 Pro upgrade later |
| BT keyboard layout wrong | Foldable kbd Cmd vs Ctrl mapping on Android | Settings → System → Keyboard → physical keyboard → change layout |



The `/spatial` route now auto-detects FIVE distinct surfaces:

```
quest          → Meta Quest Browser            → full WebXR immersive-vr scene
vision-pro     → Apple Vision Pro Safari       → immersive-vr + Liquid Glass nudge
ray-ban-display→ Meta Wearables Web App        → 600x600 compact layout
android-trio   → Android Chrome 1920x1080      → 3-pane + keyboard nudge   ← NEW
mobile-webview → Yaver RN app's preview pane   → small viewport
```

Force any via `?surface=android-trio` for testing without the actual
hardware in hand.
