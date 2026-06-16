# Apple TV control + home capture-card streaming

Control an Apple TV from the Yaver mobile app (or any MCP client) via a Yaver
agent running on a home box — typically a Raspberry Pi (`linux/arm64`, already a
shipped build target). Optionally stream the Pi's **own non-protected** HDMI
capture card to the phone / a car head unit / a glass HUD.

Deep design + the multi-surface analysis: `docs/yaver-appletv-remote-control.md`.

## What this is / isn't

- ✅ **Control + now-playing**: power, app launch, D-pad, transport, seek,
  metadata + artwork — over the LAN via pyatv (MRP/AirPlay/Companion). No HDMI,
  no IR. Works at home (LAN-direct) and away (relay), no call-site change.
- ✅ **Capture card video**: stream YOUR OWN non-protected HDMI sources (games
  console, camera, a laptop you own, non-DRM player) from the Pi.
- ❌ **No HDCP capture of the Apple TV.** Premium video blanks the HDMI output;
  a compliant capture card receives black. We DETECT persistent-black and report
  "source is HDCP-protected — capture unavailable". We never strip HDCP.
- ❌ **No CarPlay/Android-Auto video** (Apple/Google forbid it while driving).
  Those surfaces get audio + now-playing + controls only.

## Install on the Pi

```bash
npm install -g yaver-cli            # downloads the linux-arm64 agent
pip3 install pyatv                  # Apple TV control engine
sudo apt install ffmpeg             # capture-card streaming (optional)
yaver serve                         # start the agent
yaver doctor                        # confirms python3 / pyatv / ffmpeg / devices
```

The agent embeds the pyatv sidecar (`yaver_atv_bridge.py`), extracts it to
`~/.yaver/appletv/`, and supervises it on `127.0.0.1:17645` (never LAN-reachable).

## Pair (one-time, PIN on the TV)

```bash
yaver appletv scan                  # find Apple TVs; copy the identifier
yaver appletv pair <identifier>     # enter the PIN shown on the TV
yaver appletv list                  # confirm — credentials are in the vault
```

Pairing credentials are stored in the encrypted vault (project `appletv`), never
plaintext, never Convex. The vault is master-key (v2) backed, so credentials
survive auth-token rotation.

## Control (CLI)

```bash
yaver appletv key select            # up|down|left|right|select|menu|home|...
yaver appletv transport play_pause  # play|pause|stop|next|previous|play_pause
yaver appletv power on              # on|off
yaver appletv app com.apple.TVMusic
yaver appletv seek 120
yaver appletv now-playing           # JSON incl. artwork
# add --device <identifier|name> to target a non-default TV
```

## Capture card

```bash
yaver appletv capture devices       # list /dev/video* + ffmpeg status
yaver appletv capture start         # ffmpeg → MJPEG on /capture/stream + /capture/frame.jpg
yaver appletv capture status
yaver appletv capture stop
```

## From an MCP client / agent

All control verbs are available through the generic `ops` tool
(`appletv_remote_key`, `appletv_transport`, `appletv_power`, `appletv_launch_app`,
`appletv_seek`, `appletv_scan`, `appletv_list`, `appletv_pair_begin/finish`,
`capture_*`). The artwork is also a first-class **viewable** tool:
`appletv_now_playing {machine, device?}` returns metadata + the artwork image
block (the `robot_camera` pattern).

## Surfaces

- **Phone** — Device card → 📺 Apple TV (`app/appletv-remote.tsx`).
- **Android head unit / Android glasses** — the same APK; glasses use the
  compact HUD via route `?surface=glass`.
- **CarPlay / Android Auto** — audio + now-playing + controls only; native
  scene/service + store provisioning required (see the design doc, Part B).
