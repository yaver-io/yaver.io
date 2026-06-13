# FGS declaration video — why every take was ugly, and the correct path

Honest root-cause analysis after multiple bad takes. The point: stop screen-
scraping a broken emulator build and shoot the **fixed** build properly.

## 1. What went wrong (behavioral)

I shot 4+ clips and called them "clean" off a single frame without watching the
whole thing. A montage (`ffmpeg -vf "fps=1,scale=240:-1,tile=NxN"` → view) shows
the truth. **Rule going forward: never claim a video is good until I've viewed a
full-clip montage.**

## 2. What's actually ugly (per clip, from montages)

- `yaver_fgs_special_use_REAL.mp4` / `..._clip.mp4`: red **"agent did not come up
  on 127.0.0.1:18080"** banner, the **"Starting…"** spinner, **"NO NOTIFICATIONS"**
  frames during the start gap, and it **ends on the home launcher**. Broken-looking.
- `yaver_fgs_notification_clean.mp4`: technically clean, but it's *just the
  notification shade* over generic AOSP quick-settings tiles (Internet/Bluetooth/
  DND/Alarm) — no app/feature context, obviously an emulator. Thin + unconvincing.

## 3. Root causes (technical)

1. **The demo runs a PRE-FIX APK** (`yaver-x86_64-proot.apk`, 2026-06-08). It has all
   three bugs I just fixed on `main` but which aren't in that binary:
   - rootfs-not-published warning shows even when installed (`local-box.tsx`),
   - `startSandbox` hangs on `ensureConnected` → "Starting…" (`sandboxControl.ts`),
   - **`serve` daemonizes** (forks to bg, parent exits) → `SandboxService` sees
     "stopped" + the in-app probe fails → red error (`main.go`). ← the big one.
   So *any* app-UI footage from this build is broken.
2. **redroid is a headless AOSP emulator** → generic chrome, emulator status bar; it
   reads as fake, not a real phone.
3. **Crude capture** (`adb screenrecord` + `cmd statusbar expand`) → jerky transitions,
   quick-settings clutter, abrupt cuts, trailing black frames.
4. **The feature can't fully *run*** here: the coding CLIs aren't in the x86 rootfs
   (npm-install-at-runtime) and the box is in bootstrap mode (needs pairing), so even
   fixed, the demo is "Start → notification", not "watch a coding agent work".

## 4. The fix is feasible HERE (verified)

- Mac on `main` with the fixes (`62c14e9c`); Go 1.26; NDK 26/27 present;
  `reactNativeArchitectures=…,x86_64` already set.
- `ABI=x86_64 scripts/build-android-sandbox.sh` cross-compiles a **fixed** `libyaver.so`
  (Go+NDK x86_64 clang, CGO) into `jniLibs/x86_64/` and drops in the x86 `libproot.so`
  (I already have the prebuilt x86 proot + the 94 MB x86 rootfs from magara — **no
  Docker needed**, which is good because Docker isn't running on this Mac).
- `gradlew assembleRelease -PreactNativeArchitectures=x86_64` packages it with the
  **fixed JS bundle** (the 2 JS fixes come from source automatically).

## 5. Plan (the take that won't be ugly)

1. Stage x86 proot into `out/android-proot-x86_64/proot` (from magara).
2. `ABI=x86_64 scripts/build-android-sandbox.sh` → fixed `libyaver.so` + `libproot.so`.
3. `gradlew assembleRelease -PreactNativeArchitectures=x86_64` → fixed x86 APK.
4. To magara redroid: uninstall old → install fixed → re-stage 94 MB rootfs +
   `.installed` → sign in → Settings → **This phone as a box**.
   - Expected (fixed): ladder all-green, **no orange warning**; tap **Start** →
     **"On-device box is running"** (no spinner, no red error, no daemonize) +
     persistent **"Yaver sandbox running"** notification.
5. Record the *app flow* (ladder → Start → running → notification), **montage-verify**,
   then trim. This is the clean declaration video.

## 6. Alternatives (ranked)

- **A (this plan): fixed x86 APK → clean redroid flow.** Self-contained, feasible,
  ~20–40 min build. Residual: still an emulator look (acceptable for a declaration).
- **B: real phone.** Best look + most convincing, but needs: publish the **arm64**
  rootfs (`build-android-rootfs-alpine-arm64.sh` → `gh release` → flip
  `ROOTFS_PUBLISHED`), a production build from a **clean** branch (current `main` is
  mixed with the circuit-simulator agent's work), install on the user's phone, pair.
  Much heavier; needs the phone + the rootfs hosting decision.
- **C: keep `yaver_fgs_notification_clean.mp4`.** Works for the FGS declaration
  (shows the persistent notification) but thin/emulator-looking.

**Recommendation: A now** (clean, self-contained). Escalate to **B** only if a
polished real-device video is required for the listing.
