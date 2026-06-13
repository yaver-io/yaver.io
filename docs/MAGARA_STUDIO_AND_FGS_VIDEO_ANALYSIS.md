# magara studio + the Play "special use" FGS declaration video — deep analysis

What this documents: the real state of the **magara** office box as Yaver's redroid
"studio" host, an end-to-end run driving the app there, and exactly why the
foreground-service ("This phone as a box") **declaration video can't be captured
in any current build** — with the concrete fixes. Everything below was verified
live on 2026-06-13 (Hetzner + magara), not inferred.

---

## 0. TL;DR

- **magara** (office box, `10.0.0.45`) is the staged redroid studio host. Reachable
  **only via the Hetzner Tailscale subnet route** (`simkab-vostro-3888` online) — or
  from a Mac with Tailscale **up + `--accept-routes`** (this Mac's key is authorized;
  Hetzner's is not). magara is **x86_64** (i5-3210M, 3.7 GB RAM, Intel HD, no GPU).
- The full pipeline **works**: redroid boots, the app installs + launches, and it can be
  driven headlessly. On Hetzner (arm64) I drove the production arm64 APK; on magara
  (x86_64) the x86 APK is staged.
- **The FGS notification cannot be captured today**, on either host, because the on-device
  agent never reaches a runnable "Start" state:
  - arm64 prod build (Hetzner): **rootfs "not hosted yet"** → no Start control.
  - x86 build (magara, `yaver-x86_64.apk` installed): **proot "not bundled"** *and* rootfs
    "not hosted yet" → no Start control.
  - `am start-foreground-service … SandboxService` from the shell **fails silently** —
    the service is `android:exported="false"`, so only the app can start it (verified:
    `record-demo.sh` ran but the notification shade showed **NO NOTIFICATIONS**).
- This is a **build/infra blocker, not an automation one**. The in-app warnings name the
  fixes (below). Once fixed, the same redroid drive captures the real video in ~1 min.

---

## 1. magara — what's actually there

```
host:    magara  (Ubuntu 20.04, kernel 5.4.0-216, x86_64)
cpu/mem: Intel i5-3210M (4c), 3.7 GiB RAM (~1.5 GiB free), Intel HD (no discrete GPU)
disk:    468 G (310 G free)
docker:  24.0.5 ; binder_linux LOADED (CONFIG_ANDROID_BINDERFS=m)
redroid: redroid/redroid:13.0.0-latest
  - yaver-studio-redroid   Up 4 days   (the live studio device; abi=x86_64; io.yaver.mobile installed)
  - yaver-base             exited      (warm base-image snapshot)
  - yaver-qa               exited      (testkit/qa device)
  + several kylemanna/openvpn containers (unrelated)
home (~kivi):
  record-demo.sh           # FGS demo recorder (launch → start FGS → show notif → bg → stop → pull)
  redroid-setup.sh / yaver-ui.sh   # boot + UI-automation helpers (docker exec based)
  yaver-x86_64.apk         (233M)   # x86 app, NON-proot variant  ← currently installed
  yaver-x86_64-proot.apk   (233M)   # x86 app, proot-BUNDLED variant  ← NOT installed
  alpine-x86_64.tar.gz (3.4M) / yaver-rootfs-x86/ / alpine-rootfs/   # local x86 Alpine rootfs
  build-rootfs-x86.sh / build-proot-x86.sh / proot-x86/             # x86 build pipeline
  .yaver/  (agent.pid, config.json, base/, blackbox/, hermesc, host-share, …)  # full agent state
  talos-robotics/ print/ print-station/   # Ender-3 / robotics (no /dev/ttyUSB attached now)
```

Key point: there are **two x86 APK variants**. The one installed in `yaver-studio-redroid`
is `yaver-x86_64.apk` (**no proot**). The proot-bundled one (`yaver-x86_64-proot.apk`)
exists but isn't installed — and `yaver-ui.sh install` installs the non-proot one.

### Reaching magara
- This Mac → magara: **no route** until Tailscale is up. `tailscale up --accept-routes`
  on the Mac → `10.0.0.45:22` reachable (Mac's SSH key already authorized → silent login).
- Hetzner → magara: TCP open via the subnet route, but `kivi@10.0.0.45` **rejects Hetzner's
  key** (authorize it once: append Hetzner's `id_ed25519.pub` to `~kivi/.ssh/authorized_keys`).

---

## 2. The FGS feature and why the video is blocked

The special-use foreground service is `io.yaver.mobile/.sandbox.SandboxService`
(subtype `on_device_coding_agent`): it keeps the on-device coding agent
(`libyaver.so serve` on `127.0.0.1`) alive with a wake lock + a persistent notification.
User entry: **Settings → SANDBOX → "This phone as a box"** (a capability ladder).

Observed ladder state (magara x86 build, verified live):
```
Agent binary   shipped                                    OK
proot          not bundled — runners can't be isolated yet —
Linux rootfs   not hosted yet                              —
Agent running  stopped                                     —
[Install Linux rootfs] (~40 MB, 2026-06-08-1)   [Refresh status]
```
There is **no "Start" control** — it only appears once proot + rootfs are present. So the
service is never started by the app → no `startForeground()` → **no notification to film**.

In-app warnings (the exact fixes):
- proot: *"Rebuild with `PROOT_SRC` or `YAVER_PROOT_URL` set so `build-android-sandbox.sh`
  includes it"* — i.e. install `yaver-x86_64-proot.apk` (or rebuild the arm64 APK with proot).
- rootfs: *"hasn't been published yet — run `scripts/build-android-rootfs-alpine-arm64.sh`
  + `scripts/publish-android-rootfs.sh`, flip `ROOTFS_PUBLISHED` in `sandboxRootfsManifest.ts`"*
  (x86 equivalent: host `alpine-x86_64.tar.gz` where the app's installer fetches it).

### Why the `am` shortcut doesn't work
`record-demo.sh` does `am start-foreground-service -n io.yaver.mobile/.sandbox.SandboxService
-a …START`. `SandboxService` is `exported="false"`, so the shell (uid shell/root in the
container, ≠ app uid) **cannot start it** — the call no-ops. Verified: the 6 MB `demo.mp4`
recorded fine but the expanded notification shade showed **"NO NOTIFICATIONS"** and the
post-background frame showed an empty status bar. A foreground service can only be started
from within the app's own process (the "Start" toggle calling `startForegroundService`).

### The path to a real declaration video (≈1 min once unblocked)
1. Install the proot variant: `adb/docker cp yaver-x86_64-proot.apk` → `pm install -r -g`
   (or rebuild arm64 with proot for the production app).
2. Make the rootfs fetchable (host `alpine-x86_64.tar.gz` at the installer URL, or bundle it)
   and flip `ROOTFS_PUBLISHED`.
3. App UI: Settings → This phone as a box → **Install** (rootfs) → **Start** → expand the
   shade → the "Yaver sandbox running" notification is now real → screen-record.
   The existing `record-demo.sh` capture/scroll/pull steps then produce the MP4.

Until step 1–2 ship, the most honest artifact is a **feature demo** (the capability-ladder
screen + purpose text), captured this session at
`~/Downloads/yaver_fgs_thisphoneasabox_demo.mp4`.

---

## 3. What the run proved (validates the AI-testing analysis)

Driving the app headlessly on a remote redroid host **works end-to-end**, which is the
foundation the [REMOTE_REDROID_AI_TESTING_ANALYSIS](./REMOTE_REDROID_AI_TESTING_ANALYSIS.md)
pipeline assumes:
- Boot redroid → install APK (arm64 on Hetzner, x86 on magara) → launch → drive.
- Login: mint an ephemeral account (`/auth/signup` on `perceptive-minnow-557.…convex.site`),
  then UI sign-in; full 7-step onboarding cleared; reached the target Settings screen.
- **Driver caveat — uiautomator is dead on magara's redroid** (empty tree; the analysis
  flagged this "verified magara 2026-06-09"). The brain's **vision/coordinate fallback** is
  mandatory there. On Hetzner's redroid the uiautomator dump worked. Practical rule:
  **don't rely on `uiautomator dump` on magara — drive by screenshot coordinates** (the app
  layout is identical at 1080×2340, so coordinates transfer between hosts).
- The Settings → SANDBOX section exposes the two analysis-relevant features in-product:
  **"App-Test Agent — drive your app on redroid, catch bugs (red box/crash/ANR) — catch-only
  or fix"** and **"Store Studio — App Store/Play assets, permission-justification videos &
  prose"** (the latter is the in-product tool meant to generate exactly this declaration
  video — but it showed **"Not connected"**, i.e. it needs a paired agent/machine to drive).

---

## 4. Recommendations

1. **To get the FGS video now:** install `yaver-x86_64-proot.apk` into `yaver-studio-redroid`
   and point the rootfs installer at the local `alpine-x86_64.tar.gz` (or flip
   `ROOTFS_PUBLISHED` for the hosted one), then run the UI Start flow + `record-demo.sh`.
2. **Wire Store Studio to a local agent** so its "permission-justification videos" path can
   produce the declaration video without manual redroid driving.
3. **Ship proot + a hosted rootfs in the production arm64 build** so "This phone as a box"
   actually starts on real phones — otherwise the special-use FGS has no working user flow,
   which itself weakens the Play declaration.
4. **MCP-expose** `boot_redroid` / `install_app` / `qa_run` (see the companion analysis) so an
   agent can run this loop without bespoke shell scripts; and standardize on **coordinate
   driving** for magara (uiautomator unreliable there).
