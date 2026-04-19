# Demo Videos

Landing-page + marketing clips. Compiled outputs ship here; full-resolution raw
sources live on GitHub Releases (too heavy for git).

## Current clips

| File | Length | Size | Purpose |
|---|---|---|---|
| `yaver-hosting-demo.mp4` | ~46s | ~1.9 MB | Landing page "Push to Phone / Hosting" tab. Side-by-side: real phone loads SFMG, user shakes the app, types a task, Claude edits on the Mac, Hermes bundle hot-reloads the new splash color. |
| `sources/yaver-hosting-phone-lite.mp4` | 4:18 | ~1.4 MB | Lightweight 640-wide encode of the original phone screen recording (2026-04-20). Keep for re-edits. |
| `sources/yaver-hosting-desktop-lite.mp4` | 5:29 | ~4.7 MB | Lightweight 854-wide / 15 fps encode of the original desktop recording. Keep for re-edits. |

## Full-resolution raw (GitHub Release)

Full 888×1920 / 3416×2214 sources are attached to the release tag
`yaver-hosting-demo-v1` on `kivanccakmak/yaver.io`:

- `yaver-hosting-demo-phone-raw.mp4` — ~77 MB (HEVC, iPhone screen recording)
- `yaver-hosting-demo-desktop-raw.mov` — ~102 MB (H.264, macOS ReplayKit)
- `yaver-hosting-demo.mp4` — the compiled 46s landing-page clip

Fetch with:

```sh
gh release download yaver-hosting-demo-v1 -R kivanccakmak/yaver.io \
  -D ./demo-videos/raw
```

## Shot breakdown (for re-edits)

All timestamps are relative to the lite source files (same as full-res raw).
Phone and desktop were recorded simultaneously; the desktop started ~70 s
before the phone began rolling, so when re-mixing, offset the desktop by
`-ss 70` to align phone t=0 with desktop t=70.

| Segment | Phone time | Desktop time | What it shows |
|---|---|---|---|
| Auth | 00:01 – 00:05 | 01:11 – 01:15 | Yaver login screen + desktop `yaver auth` in tmux, browser OAuth callback. |
| Project loaded | 00:28 – 00:33 | 01:38 – 01:43 | SFMG football manager running on the phone; Metro + Hermes bundle served from the Mac. |
| Shake → task | 01:14 – 01:22 | 02:24 – 02:32 | Shake gesture, "Back to Yaver" overlay, New Task dialog opens. |
| Typing + send | 01:52 – 01:59 | 03:02 – 03:09 | User types "Make background color of SFMG splash screen gray" and hits Send. |
| Claude edits | 02:18 – 02:30 | 03:28 – 03:40 | Task runs: sed / grep commands on `SplashScreen.tsx` and `app/index.tsx`; live Claude stream events in tmux. |
| Hot reload | 03:36 – 03:46 | 04:46 – 04:56 | Yaver rebuilds the Hermes bundle, pushes it to the phone, SFMG reloads with the new gray splash. |

## Regenerate the compiled demo

The steps below assume the raw files are in `~/yaver-videos/` (names below).
Use `gh release download yaver-hosting-demo-v1 -R kivanccakmak/yaver.io -D ~/yaver-videos/` to fetch them.

```sh
cd ~/yaver-videos

# 1. Normalize phone to 332×720 / 30 fps, trimmed to 258 s.
ffmpeg -y -i yaver-hosting-demo-phone-raw.mp4 \
  -vf "scale=-2:720,crop=333:720:(in_w-333)/2:0,setsar=1,fps=30" \
  -t 258 -c:v libx264 -preset veryfast -crf 20 -pix_fmt yuv420p -an \
  phone-norm.mp4

# 2. CFR-normalize the desktop (variable-fps ReplayKit stream), then letterbox.
ffmpeg -y -i yaver-hosting-demo-desktop-raw.mov -vf "fps=30" \
  -c:v libx264 -preset veryfast -crf 22 -pix_fmt yuv420p -an desk-cfr.mp4
ffmpeg -y -ss 70 -i desk-cfr.mp4 \
  -vf "scale=948:614,pad=948:720:0:53:color=black,setsar=1" \
  -t 258 -c:v libx264 -preset veryfast -crf 20 -pix_fmt yuv420p -an \
  desk-norm.mp4

# 3. ImageMagick captions (see `make-captions.sh` in this folder).
./make-captions.sh captions/

# 4. Build six clips with captions baked in (see `build-clips.sh`).
./build-clips.sh

# 5. Concat + re-encode for the final 46-second clip.
cat > concat.txt <<'EOF'
file 'clip-1-auth.mp4'
file 'clip-2-loaded.mp4'
file 'clip-3-shake.mp4'
file 'clip-4-task.mp4'
file 'clip-5-claude.mp4'
file 'clip-6-reload.mp4'
EOF
ffmpeg -y -f concat -safe 0 -i concat.txt \
  -c:v libx264 -preset slow -crf 26 -pix_fmt yuv420p -movflags +faststart -an \
  yaver-hosting-demo.mp4
```

## Notes / gotchas learned

- **Homebrew `ffmpeg` 8.0.1 lacks `drawtext`** (no `--enable-libfreetype`).
  Generate captions as PNG with ImageMagick (`magick -background … label:…`)
  and overlay via the `overlay` filter.
- **ReplayKit `.mov` is variable frame rate**. Filter graphs re-initialize
  mid-stream and `pad` fails with "Padded dimensions cannot be smaller than
  input dimensions". Fix: re-encode to CFR first (`-vf fps=30`) before
  seeking / scaling / padding.
- **x264 + yuv420p needs even dimensions**. 333-wide or 947-wide inputs
  silently break as "Error while opening encoder". Use `scale=-2:…` for
  safe even rounding, or explicit even constants (332, 948).
- **Don't combine `-loop 1` PNG overlays with six chained overlay filters
  in one pass** — the brew build writes unbounded frames (60 MB+ partial at
  46 s output) and never terminates. Safer pattern: bake one caption per
  clip during the per-clip ffmpeg run, then `-f concat` them.

## Reuse ideas

- Same side-by-side rig can reshoot the **Feedback SDK** flow (shake →
  annotated screenshot → SDK POST → Claude fix → hot reload) without
  rebuilding the phone/desktop normalization.
- The caption PNGs are reusable — drop new strings into `captions/` with
  the same size/color and they'll slot into future clips.
- Lite sources in `sources/` are enough to re-cut new landing-page clips
  from this same recording (auth, login, Apple OAuth, projects browse)
  without fetching the full-res raw.
