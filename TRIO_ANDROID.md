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

## Distinct from other surfaces

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
