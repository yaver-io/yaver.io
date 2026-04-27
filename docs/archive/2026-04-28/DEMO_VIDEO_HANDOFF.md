# Demo Video Session Handoff

## Goal
Compile a landing-page demo video (`web/public/yaver-hosting-demo.mp4`) from two raw screen recordings — iPhone (SFMG app) + macOS desktop (tmux/Claude Code) — side by side with captions. Ship to landing page `web/app/page.tsx` hero.

## Current Status
- Last commit: `d746e44c` — "landing hero: tighter 28s demo, 1440x900, Safari-compat encoding"
- Pushed to `main`.
- **Deploy pending** — previous `./scripts/deploy-web.sh` run hit a wrangler transient error and needs retry.
- **User's final request (not yet done): replace "Claude" with "coding agent"** in the video caption AND the landing page paragraph.

## Working Directory (outside repo, not committed)
`/Users/kivanccakmak/yaver-videos/`
- `yaver-hosting-demo-phone-raw.mp4` (77 MB) — iPhone screen recording
- `yaver-hosting-demo-desktop-raw.mov` (102 MB) — macOS ReplayKit
- `phone-norm.mp4` — 332×720 @ 30 fps, 258 s, normalized
- `desk-cfr.mp4` — CFR-normalized desktop (3416×2214)
- `captions-v3/cap-*.png` — 1440×120 caption bars (ImageMagick)
- `clip-v3-{1..5}.mp4` — per-segment clips with captions baked in
- `yaver-hosting-demo.mp4` — final 28 s, 3.3 MB, 1440×900

## Key Files (in repo)
- `web/public/yaver-hosting-demo.mp4` — 3.3 MB served copy
- `web/app/page.tsx` — hero section around line 1123-1157 (no fake browser chrome, `<video autoPlay muted loop playsInline>`)
- `demo-videos/README.md` — full recipe + shot breakdown + gotchas
- `demo-videos/yaver-hosting-demo.mp4` — committed compiled clip
- `demo-videos/sources/yaver-hosting-{phone,desktop}-lite.mp4` — small re-edit sources
- GitHub release `yaver-hosting-demo-v1` on `kivanccakmak/yaver.io` — holds raw + lite + compiled assets

## Safari Compatibility (critical — do not regress)
Safari refuses inline playback on:
- `pix_fmt=yuvj420p` (JPEG range) → must be `yuv420p` (TV range)
- GitHub Release CDN URLs (served as `application/octet-stream` + `Content-Disposition: attachment`) → video must be in `web/public/`, not linked from the release

**Encode recipe that works:**
```
-c:v libx264 -profile:v main -level 4.0 -preset ultrafast -crf 24 \
-pix_fmt yuv420p -color_range tv
```
Filter graph must end with `format=yuv420p,setrange=tv[out]` before encode (PNG overlays introduce JPEG range otherwise).

## Video Compilation Recipe
```bash
cd ~/yaver-videos

# 1. Phone → 332×720 @ 30 fps, trimmed
ffmpeg -y -i yaver-hosting-demo-phone-raw.mp4 \
  -vf "scale=-2:720,crop=333:720:(in_w-333)/2:0,setsar=1,fps=30" \
  -t 258 -c:v libx264 -preset veryfast -crf 20 -pix_fmt yuv420p -an \
  phone-norm.mp4

# 2. Desktop → CFR first, then letterbox
ffmpeg -y -i yaver-hosting-demo-desktop-raw.mov -vf "fps=30" \
  -c:v libx264 -preset veryfast -crf 22 -pix_fmt yuv420p -an desk-cfr.mp4

# 3. Captions via ImageMagick (brew ffmpeg lacks drawtext/libfreetype)
#    Each caption is a 1440×120 PNG in captions-v3/cap-{1..5}.png

# 4. Per-clip compile (phone right, desktop left, caption bottom)
make_clip() {
  ffmpeg -y \
    -ss "$ps_start" -to "$ps_end" -i phone-norm.mp4 \
    -ss "$ds_start" -to "$ds_end" -i desk-cfr.mp4 \
    -i "captions-v3/$cap" \
    -filter_complex "\
[0:v]scale=360:-2,crop=360:760:0:(ih-760)/2[p];\
[1:v]scale=900:-2,crop=900:540:0:(ih-540)/2[d];\
color=c=#060912:s=1440x900:r=30[bg];\
[bg][p]overlay=x=1020:y=10:shortest=1[t1];\
[t1][d]overlay=x=60:y=120[t2];\
[t2][2:v]overlay=x=0:y=780[v];\
[v]format=yuv420p,setrange=tv[out]" \
    -map "[out]" \
    -c:v libx264 -profile:v main -level 4.0 -preset ultrafast -crf 24 \
    -pix_fmt yuv420p -color_range tv -an "$out"
}

# 5. Concat 5 clips
cat > concat.txt <<'EOF'
file 'clip-v3-1.mp4'
file 'clip-v3-2.mp4'
file 'clip-v3-3.mp4'
file 'clip-v3-4.mp4'
file 'clip-v3-5.mp4'
EOF
ffmpeg -y -f concat -safe 0 -i concat.txt \
  -c:v libx264 -profile:v main -level 4.0 -preset slow -crf 24 \
  -pix_fmt yuv420p -color_range tv -movflags +faststart -an \
  yaver-hosting-demo.mp4
```

## 28s Layout
- 1440×900 canvas, dark bg `#060912`
- Desktop (console): left, 900×540, at x=60 y=120
- Phone: right, 360×760, at x=1020 y=10
- Caption strip: full width 1440×120, at y=780

## Captions (5 segments)
1. `cap-1.png` — "SFMG running on iPhone — real React Native app"
2. `cap-2.png` — "Shake the phone to file a task"
3. `cap-3.png` — "Type what you want changed"
4. `cap-4.png` — **"Claude edits on your Mac — live"** ← **CHANGE TO "Coding agent edits on your Mac — live"**
5. `cap-5.png` — "Hermes hot-reloads the new splash"

## PENDING — User's Final Request
> "dont say claude say coding agent"

### TODO
1. Regenerate `captions-v3/cap-4.png`:
   ```bash
   magick -background '#060912' -fill white -font 'Helvetica-Bold' \
     -size 1440x120 -gravity center \
     label:'Coding agent edits on your Mac — live' \
     captions-v3/cap-4.png
   ```
2. Rebuild `clip-v3-4.mp4` using `make_clip` with the same phone/desktop time ranges as before.
3. Re-concat into `yaver-hosting-demo.mp4`.
4. Copy to:
   - `/Users/kivanccakmak/Workspace/yaver.io/web/public/yaver-hosting-demo.mp4`
   - `/Users/kivanccakmak/Workspace/yaver.io/demo-videos/yaver-hosting-demo.mp4`
5. Update GitHub release:
   ```bash
   gh release upload yaver-hosting-demo-v1 \
     -R kivanccakmak/yaver.io \
     ~/yaver-videos/yaver-hosting-demo.mp4 --clobber
   ```
6. Update paragraph in `web/app/page.tsx` around line 1145:
   - From: `Claude edits it on your Mac`
   - To: `a coding agent edits it on your Mac`
7. Commit + push.
8. Deploy: `./scripts/deploy-web.sh` (retry if wrangler transient error).
9. Verify Safari playback on https://yaver.io .

## Landing Page Hero (final form)
```jsx
<section id="demo" className="px-6 pb-16 pt-2">
  <div className="mx-auto max-w-5xl">
    <video
      className="w-full rounded-2xl bg-black shadow-2xl shadow-black/50"
      src="/yaver-hosting-demo.mp4"
      autoPlay muted loop playsInline preload="metadata"
    />
    <p className="mx-auto mt-4 max-w-2xl text-center text-xs text-surface-500">
      A real React Native app running on a phone. Shake it, type what you
      want changed, Claude edits it on your Mac, and the phone reloads
      with the change — live. No rebuild, no app-store round trip.
    </p>
  </div>
</section>
```
(Fix "Claude" → "coding agent" in the `<p>`.)

## Gotchas / Hard-Won Lessons
- **Brew ffmpeg 8.0.1 has no `drawtext`** — use ImageMagick PNG + overlay.
- **ReplayKit `.mov` is variable FPS** — must pre-encode to CFR before scale/pad/seek or filters reinit mid-stream and pad fails.
- **x264 + yuv420p needs EVEN dimensions** — use `scale=-2:…` or explicit 332/948 (not 333/947).
- **Don't chain 6 `-loop 1` PNG overlays in one pass** — brew build loops unbounded. Bake one caption per clip, then concat.
- **GitHub Release CDN != inline-playable** — Safari rejects due to octet-stream + attachment headers. Serve from `web/public/`.
- **`yuvj420p` kills Safari** — always append `format=yuv420p,setrange=tv` in filter graph.
- **`DemoSection` around line 730-755 and 1159 is DEAD CODE** — edit the hero at line 1123 only.
- **`web/` must stay under 10 MB** — deploy script enforces. Current video is 3.3 MB.

## Deploy
```bash
./scripts/deploy-web.sh
# If wrangler errors transiently, retry. It runs:
#   cd web && @opennextjs/cloudflare build && wrangler deploy
```

## Reference — User Messages (final few)
- "also i think its better to skip auth from video. just start from hot reload and go back to yaver set task and go back to reload and see the change thats it make videoe shorter dont mention about auth etc. at all to get attention quickly make video less than 30 seconds"
- "the text exceeds the frame also consider that as well" (resolved by removing header chrome — was a cached older deploy)
- "dont say claude say coding agent" ← **PENDING**
