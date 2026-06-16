# Yaver streaming — go-live & verification checklist

> Operational companion to `docs/yaver-appletv-remote-control.md` (the design).
> Everything below is built + on `main` (web pieces are deployed to yaver.io).
> The **agent-side** features only run on a box after a **CLI release** (so the
> box downloads the new `yaver` binary). This checklist is how to enable + test
> each path on a real Raspberry Pi.

## 0. Before any release — `main` must build

A CLI release (`git tag cli/vX.Y.Z`) builds the agent from `main`. As of
2026-06-17 `main` has an **unrelated concurrent-session build break**
(`watch_risk.go`: `containsStr` redeclared with `pipeline_cmd.go`). **Confirm a
clean build first:**
```bash
cd desktop/agent && go build ./...        # must exit 0
```
Don't tag a release while this fails — CI would ship a broken or unbuildable
binary globally. (My streaming code builds clean in isolation; the break is in
another in-flight feature.)

## 1. Box prerequisites (Raspberry Pi / Linux)

```bash
npm install -g yaver-cli           # the agent (linux/arm64 auto-detected)
pip3 install pyatv                 # Apple TV control
sudo apt install -y ffmpeg         # capture / WebRTC / RTMP encode (+ libopus, alsa)
yaver auth                         # sign in
yaver serve                        # start the agent
yaver doctor                       # confirms python3 / pyatv / ffmpeg / capture devices
```
Plug the **capture card** (USB/HDMI) into the Pi → it shows as `/dev/video*`
(video) + an ALSA card (audio, `audio_devices` lists it as `hw:N,0`).

## 2. Per-feature verification

| Feature | How to test | Pass = |
|---|---|---|
| **Apple TV control** | `yaver appletv scan` → `yaver appletv pair <id>` (PIN) → `yaver appletv key select` | TV reacts; `yaver appletv list` shows it |
| **Now-playing** | play something on the TV → `yaver appletv now-playing` | JSON with title + artwork |
| **Capture card** | `yaver appletv capture start` then open `/capture/stream` (web dashboard → 📺 Apple TV) | live video of the card's source |
| **HDCP source** | point the card at premium playback | frames go black + `blackHint` (expected; we stream as-is, never strip) |
| **WebRTC live** | web dashboard → Apple TV view → "Live (WebRTC)" → Capture | sub-second video in `<video>` |
| **WebRTC audio** | toggle 🔊 on, restart the stream | you HEAR the source (Opus) |
| **Adaptive** | throttle the network (or pick a tier) | quality drops on loss, rises on recovery; `link: loss x% · tier` shown |
| **RTMP broadcast** | `ops stream_broadcast {rtmpUrl, audioDevice:"hw:1,0"}` to a test RTMP server | stream appears on the platform with sound |
| **Scene (OBS-wrap)** | `ops scene_set {sources:["capture","screen"], layout:"row"}` → watch the `scene` source | composited frame |
| **Phone camera source** | mobile → 📷 Stream this phone → pick the box | `phone` source appears in `stream_list` |
| **Guest watch link** | web → "Create link" → open the `/watch#…` URL in a private window | live view, NO controls |
| **stream_status** | `ops stream_status` | one JSON overview of everything above |

## 3. Remote (off-LAN) WebRTC — enable TURN

Same-network WebRTC works with STUN only. For remote viewing (CG-NAT both ends),
enable the relay's **already-shipped** TURN (no relay code change):
```bash
# relay host (needs a WAN-reachable IP):
yaver relay serve --password <secret> --turn-port 3478 --turn-public-ip <WAN_IP>
# the box:
export YAVER_TURN_URL="turn:<WAN_IP>:3478"     # auth shares RELAY_PASSWORD
yaver serve
```
Verify: open the WebRTC viewer from a different network → it connects via the
relay candidate.

## 4. Cut the agent release (when `main` builds)

```bash
# bump cli/package.json + versions.json, then:
git tag cli/v1.99.XXX && git push github cli/v1.99.XXX
# → release-cli.yml builds + signs + notarizes (CI, no local keychain) + publishes npm.
# On the Pi: npm install -g yaver-cli@latest && yaver serve
```
Web is already live (Cloudflare) — no action needed there.

## 5. What's NOT shippable yet

- **Live audio sync** — the Opus/AAC code compiles but glass-to-glass sync is
  unverified without the hardware above. Test in step 2.
- **Q5 sink discovery** (phone → cast → TV re-profile) — needs a mobile build.
- **tvOS app** — owner-run native build (see `docs/yaver-tvos-fork-adr.md`).
