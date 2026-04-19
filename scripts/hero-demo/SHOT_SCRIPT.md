# Hero demo — shot-by-shot script

Target: **20 seconds**, 1080p, one unedited take. One muted H.264 MP4
under **2 MB** after compression. Same video goes on: yaver.io hero,
Show HN embed, pinned tweet, Product Hunt.

**Pre-shoot checklist (5 min, one time):**

- [ ] Terminal: iTerm2 full-screen, no notifications, dark theme,
      font size 18+. Close Slack / Discord / email.
- [ ] macOS menu bar hidden (System Settings → Control Center →
      Auto-hide menu bar: Always).
- [ ] Phone: unlock, enable Do Not Disturb, AirPlay mirror to the
      Mac (Control Center → Screen Mirroring → this Mac). The Yaver
      app is installed, paired, and showing the "Waiting for push"
      home screen.
- [ ] `yaver` CLI installed and on PATH: `yaver version` must
      print 1.96.10+.
- [ ] Target RN project picked. Default: `demos/bento`. Must have a
      visible color theme we can edit mid-shoot (e.g., a
      `tintColor` in `src/theme.ts`).
- [ ] Run `yaver serve` in a background terminal or via launchd so
      the agent is already up.
- [ ] Dry-run once: `yaver push` from the project dir and verify
      it lands on the phone. This warms the hermesc + bundle
      caches — the REAL shoot should land faster.
- [ ] macOS screen layout: left ~60% for terminal + editor split,
      right ~40% for the phone mirror.

## Frame-by-frame plan (20s total)

| Time | Mac (left) | Phone (right) | Audio |
|------|-----------|---------------|-------|
| 0:00–0:01 | Terminal clean prompt in `demos/bento/`. Clock shows 00:00. | Yaver app "Waiting for push from your machine…" | — |
| 0:01–0:03 | **Type** `yaver push` and press Enter. Terminal shows `[analyze] Bento, RN 0.81.5`. | Still waiting. | — |
| 0:03–0:06 | Terminal: `[compile] JS → Hermes bytecode (3.2 MB)` then `[push] transferring...` | App switches to **Receiving bundle** screen, progress bar climbs 0 → 100%. | — |
| 0:06–0:08 | Terminal: `✓ live on device in N.Ns`. | App launches — Bento menu appears with salmon / veggie / chicken / sashimi cards, indigo theme. | — |
| 0:08–0:11 | **Move cursor to editor** pane (already open on `src/theme.ts`). Change `primary: "#6366f1"` (indigo) → `primary: "#10b981"` (emerald). **Cmd+S**. | Menu still showing, indigo theme. | — |
| 0:11–0:14 | Terminal flips to a live tail: `[watch] change: src/theme.ts` → `[reload] pushed ✓`. | App re-renders: theme shifts from indigo to emerald. A "Hot reload ✓" toast pops for ~1s. | — |
| 0:14–0:17 | Terminal back to a clean prompt. Typed cursor blinks. | Menu stays on new theme — user can visually confirm cards took the color. | — |
| 0:17–0:20 | Terminal stays still. Cursor blinks. | Phone stays still. | — |

**Why 20 seconds and not 15:** padding at the start + end gives room
for the video to loop cleanly on the landing without jarring cuts.
First and last second should both show a quiet "ready" state — that
creates a seamless loop.

## Takes to expect

- Take 1: something goes wrong (pairing, keystroke typo, phone
  screen-mirror disconnects). Abort.
- Take 2: usable but slow. Keep.
- Take 3–5: aim for one where the push lands in ≤4 seconds.

## Post-processing

After the raw recording (a QuickTime / OBS `.mov`), run:

```bash
./scripts/hero-demo/encode.sh /path/to/raw.mov
```

The script cuts to 20 seconds, scales to 1080p, drops audio, encodes
H.264 CRF 28 + AAC, writes to `scripts/hero-demo/output/hero.mp4`
and prints the final size. Target: ≤2 MB.

Copy the output into `web/public/demo-push.mp4` (the existing
landing hero asset) to replace the synthetic Playwright version
without touching the landing page at all.

## Fallback

If the live shoot doesn't land anything usable, keep the current
synthetic Playwright video. It is under 250 KB and reads clearly.
Real footage is a nice-to-have for the Show HN thread, not a
blocker.
