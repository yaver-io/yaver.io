# Landing Video Handoff For Claude

## Context

Hero video on `yaver.io`. Current live cut is ~28s, phone-right / console-left, 5 captioned segments. User review on 2026-04-20:

> "this framing is better phone right side console left side but timing etc is horrible it can be 45 seconds etc. show sfmg show shaking etc."

- **Layout is good** — phone on the right, console on the left stays.
- **Timing is horrible** — too rushed. Target ~45s.
- **Caption sizes are inconsistent** — cap-4 is visibly ~2x larger than the other four (confirmed by frame inspection, see below).
- **SFMG (the React Native app) has to actually appear.** The current cut shows the Yaver app's Tasks/Projects UI the whole time. The viewer never sees a real third-party app running on the phone — which is the whole story.
- **The shake has to register as a shake.** A single-frame cut doesn't read.

Reference recording of the current problematic state:

- `/Users/kivanccakmak/Desktop/Screen Recording 2026-04-20 at 02.19.48.mov` (~40s, 3416x2214, 60 fps)

## What's actually in the current live cut (verified from frames)

Five segments, captions as shipped:

| # | Caption (verbatim) | What the phone shows | What the console shows |
|---|---|---|---|
| 1 | `Your app running on a real iPhone` | Yaver Projects tab (sfmg listed) | tmux logs |
| 2 | `Shake — back to Yaver` | Yaver Tasks tab — "All Clear" | tmux logs |
| 3 | `Type what you want to change` | Yaver "New Task" / thinking view | tmux logs |
| 4 | **`Coding agent edits on your Mac — live`** **(oversized font — the regression)** | Yaver task-thinking view | tmux logs |
| 5 | `Change is live on the phone` | Yaver Projects tab, "Reload" button | tmux logs |

The cap-4 size bug is obvious on frame inspection — that caption fills roughly the full width of the text strip while the other four sit at ~half the width. Root cause from the previous session: cap-4 was regenerated with `/System/Library/Fonts/Helvetica.ttc` while the others use `Helvetica-Bold`, producing a bigger rendered glyph.

## The bigger problem: the phone never shows a third-party app

The whole product story is "edit a real app from your phone". The current cut shows **Yaver's own UI** on the phone for all 45 seconds. A viewer who doesn't already know what Yaver is sees:

- a terminal on the left (fine)
- a dark app showing "Tasks / Projects" on the right (what is this?)

They never see SFMG — the thing being edited — and so never understand what the demo is demonstrating. This is more important than the caption-size fix.

The raw phone recording **does** contain SFMG footage. See the timeline below.

## Phone source — use the raw, crop the chrome

**Use `yaver-hosting-demo-phone-raw.mp4` directly (888x1920, 60 fps, 258s), not `phone-norm.mp4`.**

The normalized copy was cropped at 332x720 from the recording's vertical center, which keeps the QuickTime red recording timer, the iOS status bar, and the Metro dev banner (`Connect to Metro to develop JavaScript.`) in frame on most beats. The raw is 888x1920 so you can lop off the top ~250px and everything chrome goes with it.

Recipe for the phone side of the filter graph (replaces the `[0:v]scale=360:-2,crop=360:760:0:(ih-760)/2[p]` line):

```
[0:v]crop=888:1670:0:250,scale=360:-2,crop=360:760:0:(ih-760)/2,setpts=PTS-STARTPTS[p]
```

The initial `crop=888:1670:0:250` drops 250px off the top. What that hides:

| Chrome element | Approx y range at 1920h | Gone after crop? |
|---|---|---|
| QuickTime red recording timer (`00:22`) | 0 – 80 | yes |
| iOS status bar (signal / wifi / battery) | 80 – 180 | yes |
| Metro dev banner (`Connect to Metro to develop JavaScript.`) | 180 – 240 | yes |
| App content (SFMG / Yaver) | 240+ | preserved |

If 250 still shows any banner on some frames (app inserts/removes it on navigation), bump to 280 or 300. Sample a few frames after cropping and verify before running the full cut:

```bash
ffmpeg -y -v error -ss 60 -i yaver-hosting-demo-phone-raw.mp4 -frames:v 1 \
  -vf "crop=888:1670:0:250,scale=360:-2" /tmp/phone-check-60s.jpg
open /tmp/phone-check-60s.jpg
```

Desktop source stays the same: `desk-cfr.mp4` (already CFR from the raw `yaver-hosting-demo-desktop-raw.mov`).

## Phone timeline (time ranges are relative to the raw, which is the same 258s duration as phone-norm)

Verified by sampling frames:

| Range | What the phone shows | Use for |
|---|---|---|
| 0s – 30s | Yaver auth / "Continue with Apple" sign-in sheet | skip |
| ~50s – 75s | **SFMG running** — "Merhaba Kk" hero screen, dark green background, SFMG splash/landing UI | **segment 1: real app on a phone** |
| ~75s – 100s | Yaver "New Task" screen, user typing `Make background color of SFMG` | segment 3: type task |
| ~100s – 175s | Task RUNNING — coding agent streams diffs and tool calls on phone | segment 4: agent edits (mirror on console side too) |
| ~175s – 195s | Task COMPLETED — diff table "rgba(76,175,80)" → gray, SFMG splash rebuild note | segment 5a: completion hint |
| ~195s – 258s | Yaver Projects tab, SFMG row with "Reload" + "Building Hermes bundle" | segment 5b: reload / live on phone |

The shake gesture itself does not have a clean dedicated moment in the raw recording — the switch from "SFMG running" to "Yaver Tasks" happened off-camera. Two ways to fix this:

- **Option A (preferred):** re-record ~10 seconds of phone footage where the shake is obvious — hand visibly shakes, SFMG fades, Yaver feedback modal slides up. Add this clip to the sources.
- **Option B (if no re-record):** fake the beat with a 2-frame crossfade from SFMG → Yaver Tasks + the caption text "Shake to switch to Yaver", and accept that the shake is implied rather than shown.

Confirm with the user which path before cutting.

## Target structure — 45 seconds, 5 beats

| # | Beat | Duration | Phone source range | Caption |
|---|------|----------|---------|--------|
| 1 | **SFMG running on a real phone** | 6s | ~50s – 56s of `phone-norm.mp4` | `SFMG running on iPhone — a real React Native app` |
| 2 | **Shake to switch to Yaver** | 5s | Option A re-record OR crossfade from SFMG at ~70s → Tasks at ~80s | `Shake — back to Yaver` |
| 3 | **Type task on phone** | 8s | ~82s – 90s (typing "Make background color of SFMG") | `Type what you want changed` |
| 4 | **Agent edits on the Mac** | 18s | phone ~110s – 128s (running) + matching 18s of console showing diff scroll | `Coding agent edits on your Mac — live` |
| 5 | **Reload — change is live** | 8s | ~220s – 228s (Projects + Reload + Hermes bundle) | `Change is live on the phone` |

Total = 45s. Segment 4 is the product moment — give it room. The other four exist to set it up and resolve it.

If the user does not want to re-record for segment 2, drop it to 3s and bump segment 4 to 20s.

## Caption regeneration — one font, one size, all five

Fix the cap-4 regression by rebuilding **all five** captions with an identical recipe.

```bash
cd /Users/kivanccakmak/yaver-videos/captions-v3

# Check what bold font actually exists on this Mac
magick -list font | grep -iE 'helvetica|arial' | head

FONT='Helvetica-Bold'   # fall back to Arial-Bold or HelveticaNeue-Bold if missing — same one for all 5
PTSIZE=44               # adjust once, keep it the same for all 5

make_cap() {
  local text="$1" out="$2"
  magick -background '#060912' -fill white -font "$FONT" -pointsize "$PTSIZE" \
    -size 1440x120 -gravity center \
    label:"$text" "$out"
}

make_cap 'SFMG running on iPhone — a real React Native app'  cap-1.png
make_cap 'Shake — back to Yaver'                              cap-2.png
make_cap 'Type what you want changed'                         cap-3.png
make_cap 'Coding agent edits on your Mac — live'              cap-4.png
make_cap 'Change is live on the phone'                        cap-5.png
```

Sanity check before rebuilding clips:

```bash
for f in cap-*.png; do identify "$f"; done   # must all be 1440x120
open cap-1.png cap-2.png cap-3.png cap-4.png cap-5.png   # must look identical in weight / size / baseline
```

If any one looks visibly different, the font fell back silently — stop and investigate before re-encoding clips.

## Layout (unchanged — this is the good part)

```
1440 x 900 canvas, bg #060912

┌──────────────────────────────────────────────────────────────────┐
│                                                                  │
│  ┌─────────────────────────┐            ┌────────────┐           │
│  │ CONSOLE 900 x 540        │           │ PHONE       │          │
│  │ x=60 y=120               │           │ 360 x 760   │          │
│  │ (tmux / coding agent)    │           │ x=1020 y=10 │          │
│  │                          │           │ (SFMG /     │          │
│  └─────────────────────────┘            │  Yaver)     │          │
│                                         └────────────┘           │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ CAPTION STRIP 1440 x 120, x=0 y=780                        │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

Do not change these offsets.

## Clip rebuild (per-segment, from source)

Sources:
- `/Users/kivanccakmak/yaver-videos/yaver-hosting-demo-phone-raw.mp4` (258s, 888x1920, 60 fps) — **raw, use this, crop chrome at filter stage**
- `/Users/kivanccakmak/yaver-videos/desk-cfr.mp4` (329s, 3416x2214 CFR) — already normalized from the raw `.mov`

Phone/desk slices do not need to be the same wall-clock range — pick what best matches the caption. For segment 4 the phone can sit idle on the "task running" view while the console shows the diff scrolling.

Scan both sources and write down the six numbers per clip before cutting:

```
Segment 1 (6s):  phone=[50, 56]     desk=[?, ?+6]    cap=cap-1.png
Segment 2 (5s):  phone=[?, ?+5]     desk=[?, ?+5]    cap=cap-2.png
Segment 3 (8s):  phone=[82, 90]     desk=[?, ?+8]    cap=cap-3.png
Segment 4 (18s): phone=[110, 128]   desk=[?, ?+18]   cap=cap-4.png
Segment 5 (8s):  phone=[220, 228]   desk=[?, ?+8]    cap=cap-5.png
```

Replace `?` with the desktop ranges that best match each beat (e.g. for segment 4, pick 18 seconds where the console shows the most visible diff / tool-call activity).

```bash
cd /Users/kivanccakmak/yaver-videos

make_clip() {
  local ps="$1" pe="$2" ds="$3" de="$4" cap="$5" out="$6"
  local dur
  dur=$(python3 -c "print($pe - $ps)")
  ffmpeg -y \
    -ss "$ps" -to "$pe" -i yaver-hosting-demo-phone-raw.mp4 \
    -ss "$ds" -to "$de" -i desk-cfr.mp4 \
    -i "captions-v3/$cap" \
    -filter_complex "\
[0:v]crop=888:1670:0:250,scale=360:-2,crop=360:760:0:(ih-760)/2,fps=30,setpts=PTS-STARTPTS[p];\
[1:v]scale=900:-2,crop=900:540:0:(ih-540)/2,setpts=PTS-STARTPTS[d];\
color=c=#060912:s=1440x900:r=30:d=${dur}[bg];\
[bg][p]overlay=x=1020:y=10:shortest=1[t1];\
[t1][d]overlay=x=60:y=120:shortest=1[t2];\
[t2][2:v]overlay=x=0:y=780[v];\
[v]format=yuv420p,setrange=tv[out]" \
    -map "[out]" \
    -c:v libx264 -profile:v main -level 4.0 -preset ultrafast -crf 24 \
    -pix_fmt yuv420p -color_range tv -an "$out"
}
# Note: the raw phone is 60 fps. The `fps=30` after scale drops it to match
# the canvas frame rate cleanly so overlay+concat don't stutter.

# Fill in the numbers, then:
make_clip 50 56  Ds1 De1  cap-1.png  clip-v4-1-sfmg.mp4
make_clip Ps2 Pe2 Ds2 De2  cap-2.png  clip-v4-2-shake.mp4
make_clip 82 90  Ds3 De3  cap-3.png  clip-v4-3-type.mp4
make_clip 110 128 Ds4 De4  cap-4.png  clip-v4-4-agent.mp4
make_clip 220 228 Ds5 De5  cap-5.png  clip-v4-5-reload.mp4
```

Concat:

```bash
cat > concat-v4.txt <<'EOF'
file 'clip-v4-1-sfmg.mp4'
file 'clip-v4-2-shake.mp4'
file 'clip-v4-3-type.mp4'
file 'clip-v4-4-agent.mp4'
file 'clip-v4-5-reload.mp4'
EOF
ffmpeg -y -f concat -safe 0 -i concat-v4.txt \
  -c:v libx264 -profile:v main -level 4.0 -preset slow -crf 24 \
  -pix_fmt yuv420p -color_range tv -movflags +faststart -an \
  yaver-hosting-demo.mp4

ffprobe -v error -show_entries format=duration,size \
  -show_streams -select_streams v:0 \
  yaver-hosting-demo.mp4
# Expect: duration ≈ 45s, pix_fmt=yuv420p, color_range=tv, size ≈ 5–6 MB.
```

## Safari compatibility (do not regress)

Every encode must end with `format=yuv420p,setrange=tv` in the filter graph, and:

```
-c:v libx264 -profile:v main -level 4.0 -pix_fmt yuv420p -color_range tv
```

Safari refuses `pix_fmt=yuvj420p` (JPEG range) and GitHub-Release CDN URLs (served as `application/octet-stream`). Ship from `web/public/`, not a release link.

## Ship

```bash
cp /Users/kivanccakmak/yaver-videos/yaver-hosting-demo.mp4 \
   /Users/kivanccakmak/Workspace/yaver.io/web/public/yaver-hosting-demo.mp4
cp /Users/kivanccakmak/yaver-videos/yaver-hosting-demo.mp4 \
   /Users/kivanccakmak/Workspace/yaver.io/demo-videos/yaver-hosting-demo.mp4

du -sh /Users/kivanccakmak/Workspace/yaver.io/web/public/    # must stay < 10 MB

cd /Users/kivanccakmak/Workspace/yaver.io
./scripts/deploy-web.sh    # ASK THE USER FIRST
open https://yaver.io      # verify on Safari
```

Ask before running the deploy script.

## Checklist for the next session

1. Decide on shake-gesture handling (re-record vs crossfade) — ask the user.
2. Scan `desk-cfr.mp4` (`ffplay` or `open`) and pick 5 clean desktop ranges for the 5 segments. Write them down before cutting.
3. Regenerate all five captions with one font + one point size. Visually confirm they look identical.
4. Rebuild `clip-v4-{1..5}-*.mp4` from source with the `make_clip` helper.
5. Concat → `yaver-hosting-demo.mp4` → verify `duration ≈ 45s`.
6. Copy to `web/public/` and `demo-videos/`, check size < 10 MB.
7. Ask the user before deploying.

## Files touched

- `/Users/kivanccakmak/yaver-videos/captions-v3/cap-{1..5}.png` (regenerate all five)
- `/Users/kivanccakmak/yaver-videos/clip-v4-{1..5}-*.mp4` (new, don't overwrite v3)
- `/Users/kivanccakmak/yaver-videos/yaver-hosting-demo.mp4` (final 45s)
- `web/public/yaver-hosting-demo.mp4` (served)
- `demo-videos/yaver-hosting-demo.mp4` (committed copy)

## Do not touch

- `web/app/page.tsx` hero paragraph — already says `a coding agent edits it on your Mac`.
- Overlay offsets (phone `x=1020 y=10`, desktop `x=60 y=120`, caption `x=0 y=780`).
- `DemoSection` around `web/app/page.tsx` lines 730-755 and 1159 — dead code.

## Gotchas carried over

- Brew ffmpeg 8.0.1 has no `drawtext` — use ImageMagick PNGs + overlay.
- `desk-cfr.mp4` is already CFR; don't pass raw ReplayKit `.mov` into filters.
- x264 + `yuv420p` needs even dimensions — stick to `scale=-2:…` or explicit even sizes.
- Don't chain five `-loop 1` PNG overlays in one pass — brew build loops unbounded. One caption per clip, then concat.
- `web/` must stay under 10 MB (deploy script enforces). Current demo is ~3.3 MB; 45s at same bitrate should land around 5 MB.
