# Sandbox install deep dive — redroid · proot · rootfs · npm/coding-agents

How Yaver turns a phone into "its own Linux coding box": the droid (redroid for
dev/test), the proot userland, the Alpine rootfs, and the npm/coding-agent layer
— with the **live-verified success** (`agent started (proot=true)`) reproduced on
magara's x86_64 redroid on 2026-06-13. Source paths inline.

---

## 0. TL;DR

- The on-device agent (`libyaver.so serve`) ships in the APK as a jniLib; **proot**
  (`libproot.so`) and the **Alpine rootfs** are what let it spawn isolated coding
  runners (claude/codex/opencode) on the phone.
- Three things must be true for the box to run **with** proot
  (`SandboxService.startAgent`): `agentPresent` (libyaver.so), `prootPresent`
  (libproot.so in nativeLibDir), `rootfsInstalled` (`filesDir/rootfs/.installed`).
  The **UI Start button** only needs `agentPresent && rootfsInstalled` (proot is not
  gated there), but the **agent runs proot only when `.installed` AND `libproot.so`
  both exist** — else "control-plane only" (`proot=false`).
- **rootfs differs by arch**: the **arm64** build bakes in node/npm/git +
  claude-code/codex/opencode (npm-installed at *build* time); the **x86_64** build
  (redroid/dev) ships only node/npm/git/bash/ripgrep — so the coding CLIs are the
  **npm-install-at-runtime** case there.
- **Verified live**: installing the proot APK (`libproot.so` x86_64) + staging the
  full 94 MB x86 rootfs into `filesDir/rootfs` flipped the ladder all-green and the
  log to **`agent started (proot=true)`**; the "Yaver sandbox running" FGS
  notification posted. Video: `~/Downloads/yaver_fgs_proot_success_clip.mp4`.

---

## 1. The droid: redroid (dev/test Android), distinct from on-device proot

redroid (Android-in-Docker) is the **dev/test** surface — it is NOT how phones run;
it's where the sandbox is validated headlessly. Provision (`studio/redroid.go`,
`redroid_capture.sh`):
```bash
# binder via throwaway privileged helper (no host sudo)
docker run --rm --privileged -v /lib/modules:/lib/modules debian:bullseye-slim \
  bash -c 'modprobe binder_linux devices=binder,hwbinder,vndbinder'
docker run -itd --privileged --name yaver-studio-redroid -v $WORK:/data -p 5555:5555 \
  redroid/redroid:13.0.0-latest androidboot.redroid_width=1080 ...height=2340 ...dpi=440
# wait sys.boot_completed=1 ; install:
docker cp app.apk C:/data/local/tmp/app.apk ; adb/pm install -r -g /data/local/tmp/app.apk
```
- Needs `CONFIG_ANDROID_BINDERFS=m`; **no GPU/KVM**.
- **Arch must match the APK's native libs**: arm64 host runs the arm64 APK; magara is
  **x86_64**, so it needs the **x86 APK + x86 proot + x86 rootfs** (separate builds).
- magara redroid is **SELinux Disabled** → proot can exec binaries from app-data
  (on a real enforcing phone, exec-from-app-data is the harder constraint — see §6).

## 2. The APK payload — agent + proot as jniLibs

`scripts/build-android-sandbox.sh` cross-compiles the Go agent and bundles proot:
```
jniLibs/arm64-v8a/libyaver.so   (CGO off, static, GOOS=android GOARCH=arm64)
jniLibs/x86_64/libyaver.so      (NDK clang, CGO on, GOARCH=amd64)
jniLibs/<abi>/libproot.so       (from build-android-proot-<arch>.sh: proot v5.4.0,
                                 -O2 -static musl, loader embedded)
```
Key: `expo.useLegacyPackaging=true` + `extractNativeLibs` → Android extracts these to
`applicationInfo.nativeLibraryDir` **on disk with the executable bit** (so the agent
can `ProcessBuilder` them; default RN packaging mmaps from the APK = W^X, not
executable). The payload report `.sandbox-payload.txt` flags FULL (agent+proot) vs
AGENT-ONLY (runners disabled). **The two x86 APK variants seen on magara**:
`yaver-x86_64.apk` (no x86 libproot.so → proot "not bundled") vs
`yaver-x86_64-proot.apk` (has the 247 KB x86 `libproot.so`).

## 3. The rootfs — build, publish, on-device install

**Build** (`scripts/build-android-rootfs-alpine-{arm64,x86_64}.sh`): `docker run alpine:3.20`
→ `apk add`, then `docker export | tar`:
- arm64: `nodejs npm git ripgrep bash coreutils … ca-certificates libstdc++ openssh`
  **+ `npm install -g @anthropic-ai/claude-code @openai/codex opencode-ai`** + hermesc.
- x86_64: `nodejs npm git bash ripgrep coreutils` **only** (no coding CLIs, no hermesc).
**Publish** (`publish-android-rootfs.sh`): `gh release upload` to `kivanccakmak/yaver-models`
→ pin `mobile/src/lib/sandboxRootfsManifest.ts` (`version`/`url`/`sha256`/`sizeBytes`)
and flip `ROOTFS_PUBLISHED = true`. Current manifest: `2026-06-08-1`, 39.75 MB,
**`ROOTFS_PUBLISHED = false`** (not yet hosted — the source of the in-app "not hosted
yet" warning; the in-app **Install** button is gated on `ROOTFS_PUBLISHED`).

**On-device install** (`RootfsInstaller.kt`): download (HTTP, 30/60s timeouts, follows
GH redirects) → **sha256 verify** → wipe → **self-contained ustar extract** (no `tar`
binary; handles dirs/files/symlinks/hardlinks, zip-slip guarded, exec bit from mode) →
pre-create cred-bind dirs (`.claude/.codex/.config/opencode/.local/share/opencode`) →
write `filesDir/rootfs/.installed = version`. `isInstalled` = that stamp exists.

## 4. proot — how a runner actually runs isolated

`desktop/agent/sandbox_proot.go` wraps every runner/terminal/build subprocess
(`sandboxWrapCmd`, integrated at `console_terminal.go:140`, `tasks.go` runner spawns).
`buildProotArgv`:
```
libproot.so --kill-on-exit --link2symlink
  -r <filesDir/rootfs>
  -b /dev -b /proc -b /sys -b /dev/urandom:/dev/random
  -b <credHome>/.claude:/root/.claude  (+ .codex, .config/opencode, .local/share/opencode)
  [-b <projectDir>:<projectDir>]        (builds)
  -w /root|<workdir>  <inner cmd>
```
Env (`sandboxEnv`): `HOME=/root`, Alpine `PATH=/usr/local/sbin:…:/bin`,
`PROOT_NO_SECCOMP=1` (ptrace+seccomp conflict on Android), `PROOT_LOADER`/`PROOT_TMP_DIR`
if set. `--link2symlink` makes npm/git hardlinks work; cred binds put mirrored runner
auth at `/root/.claude` etc. so the CLI is authed instantly.

## 5. The npm / coding-agent layer

The agent discovers a runner by PATH lookup (`ai_generator.go pickAIGeneratorCLI`):
`claude` → `codex` → `opencode` (each `/usr/bin/<x>` inside the rootfs), invoked under
proot with `--print/--output-format stream-json` (claude), `exec --full-auto -` (codex),
`run --dangerously-skip-permissions` (opencode).
- **arm64 phones**: the CLIs are **baked into the rootfs** at build time → no runtime
  npm install; first run just `LookPath("claude")`.
- **x86 redroid (the magara case)**: the rootfs has node/npm/git but **NOT** the CLIs →
  the **"npm install case"**: a runner needs `npm install -g @anthropic-ai/claude-code`
  (and/or codex/opencode-ai) executed once inside the proot rootfs before it resolves on
  PATH. `node`/`npm` themselves are present (43 MB x86_64 `node` verified in the staged
  rootfs) and run under proot.

## 6. SandboxService runtime + the proot gate (the crux)

`SandboxService.startAgent()` (Kotlin):
```
startForeground(NOTIF_ID, "Yaver sandbox running")   // notification posts HERE, first
acquireWakeLock()                                     // PARTIAL_WAKE_LOCK "yaver:sandbox"
ProcessBuilder(libyaver.so, "serve", "--port", "18080")  → filesDir/sandbox/agent.log
env HOME = YAVER_ANDROID_CRED_HOME = filesDir/home
rootfsReady = File(rootfs/.installed).exists() && libproot.so.exists()
if (rootfsReady) env += YAVER_ANDROID_{ROOTFS,PROOT,LOADER?,TMP}   // → proot=true
else  Log.w("rootfs not installed yet — control-plane only")        // → proot=false
START_STICKY  // OS recreates under memory pressure
```
So: the **notification always posts** on start; **proot activates iff `.installed` +
`libproot.so`**. "control-plane only" = agent serves HTTP/QUIC for the IDE but can't
spawn isolated runners.

## 7. Live success on magara (2026-06-13) — reproduce

```bash
# 1. proot APK (gives x86 libproot.so). Different signature → uninstall first (wipes data).
docker exec C pm uninstall io.yaver.mobile
docker cp ~/yaver-x86_64-proot.apk C:/data/local/tmp/p.apk; docker exec C pm install -r -g /data/local/tmp/p.apk
docker exec C am start -n io.yaver.mobile/.MainActivity   # creates filesDir
# 2. stage the full 94 MB x86 rootfs (node/npm/git) into filesDir/rootfs + stamp
docker cp ~/yaver-rootfs-x86 C:/data/local/tmp/rootfs-stage
APP=$(docker exec C stat -c %u /data/data/io.yaver.mobile)   # u0_a81 / 10081
docker exec C sh -c "cp -a /data/local/tmp/rootfs-stage /data/data/io.yaver.mobile/files/rootfs;
  echo 2026-06-08-1 > /data/data/io.yaver.mobile/files/rootfs/.installed;
  chown -R $APP:$APP /data/data/io.yaver.mobile/files/rootfs; chmod -R u+rwX,go+rX .../rootfs"
# 3. in-app: sign in → Settings → This phone as a box → Refresh → ladder all-green
#    (Agent OK · proot shipped OK · Linux rootfs installed OK) → tap Start
```
Result (logcat `YaverSandbox`): **`agent started (proot=true)`** (vs `proot=false` on the
non-proot/marker-only attempts). FGS notification "Yaver sandbox running" posted +
persisted after HOME. Video: `yaver_fgs_proot_success_clip.mp4`.
Note: `libyaver.so serve` then `exited code=0` quickly in redroid (no auth token / needs
pairing) — the FGS + proot path is what's proven; a full runner needs pairing + (on x86)
the runtime `npm install` of the CLIs.

## 8. Gotchas (learned)
- `/sdcard` is FUSE → `docker cp` from `/sdcard` fails; copy to the bind-mounted
  `/data/local/tmp` first, then pull.
- redroid's **uiautomator dump is empty on magara** → drive by screenshot coordinates,
  not `uiautomator`. Layouts are identical at 1080×2340 across hosts.
- The proot APK signature ≠ the non-proot APK → `install -r` rejected; must uninstall
  (loses login → re-onboard). Onboarding is server-side per account, so reusing the same
  account skips it.
- The in-app **Install rootfs** button needs `ROOTFS_PUBLISHED=true` + a hosted asset;
  staging the rootfs directly into `filesDir/rootfs` + `.installed` bypasses that for
  dev/redroid.
- Real **arm64 phones** with enforcing SELinux: executing rootfs binaries from app-data
  is the production constraint redroid (SELinux Disabled) doesn't exercise — validate on
  a real device before claiming on-phone runner support.
