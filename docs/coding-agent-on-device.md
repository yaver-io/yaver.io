# Coding agents on-device — Claude Code / Codex inside the Yaver mobile app

> Design + spike notes. Code is the source of truth; when this drifts, fix it
> in the same change (see CLAUDE.md). Status as of 2026-06-08:
> **Built (Go, tested):** proot shim with credential binds (`sandbox_proot.go`),
> runner-spawn wiring across the console PTY + all four task paths (warmup /
> version-check / task / fork) so claude/codex/opencode resolve in the rootfs,
> cross-compile script. **Built (TS, tsx-tested):** the subscription transport
> (`claudeSubscription.ts` + store), the unifying `codingSession.ts` policy
> (engine×target, incl. Android in-app Hermes), `localBox.ts` (synthetic "This
> phone"), `remoteApplyTarget.ts` (Hermes-only-remote edits land on the box), and
> the `codingExecution.ts` glue. **Built (Kotlin/native):** `SandboxService.kt`
> (foreground service launches `libyaver.so serve` with the proot+cred env),
> `RootfsInstaller.kt` (download/verify/extract, self-contained tar.gz),
> `SandboxModule.kt`/`SandboxPackage.kt` (JS bridge), manifest service + FGS
> perms, `useLegacyPackaging=true`. **NOT done:** the baked rootfs image asset,
> the DeviceContext injection of the synthetic device + a settings/enable UI, and
> any on-device run (the T0–T6 plan below has never executed on a phone).

## The reframe

We are **not wrapping a binary. We are wrapping a behavior** — and the heavy
compute already lives in the cloud. `claude` and `codex` are thin Node CLIs
that stream to the Anthropic / OpenAI API. The model never runs on the phone.

Consequence:

- **Android:** running the *real* CLI on the phone is cheap (it's a
  network-bound thin client). The only obstacles are Android's W^X exec rule
  and lack of root — both solved by the same userspace tricks Termux /
  proot-distro / UserLAnd have shipped for years.
- **iOS:** you *cannot* run the binary (no exec of downloaded code, no JIT →
  no V8 → no Node, no ptrace → no proot, no on-device Linux VM). So iOS wraps
  the **behavior**: remote-made-invisible by default, plus a Hermes-native
  agent loop for standalone use.

## What already exists (the reuse surface)

~80% of the client is built. None of it changes to put the agent *on the phone*:

| Layer | File | Already does |
|---|---|---|
| VT rendering | `mobile/src/components/XtermView.tsx`, `xtermBridge.ts`, `vendor/xtermBundle.ts` | Full xterm.js grid in offline WebView |
| Terminal route | `mobile/app/shell.tsx`, `glass-terminal.tsx` | `/ws/terminal`, binary stdin/stdout, resize + sudo meta frames |
| Runner toggles | `mobile/src/lib/agentLaunch.ts` | `claude …`, `codex …`, `opencode` launch/close lines typed into the PTY |
| Box selection | `mobile/src/components/RemoteBoxPickerModal.tsx` | Picks a device via `connectionManager.clientFor(id)` |
| Server PTY | `desktop/agent/terminal_session.go`, `console_terminal.go` | `creack/pty` over `/dev/ptmx`, replay buffer, idle TTL |
| Runner spawn | `desktop/agent/tasks.go` (`builtinRunners`), `runner.go`, `code_terminal.go` | Spawns claude/codex/opencode with yolo flags |
| Native agent loop | `mobile/src/lib/{yaverAgentRunner,localAgent/*}.ts` | On-phone agent loop, GBNF tool grammar, remote-first brain |
| Backend picker | `mobile/app/sandbox-ai.tsx`, `codingBackend.ts` | on-device / cloud (Anthropic/OpenAI/GLM) selector |
| Asset hosting | `localAgent/models.ts` + `kivanccakmak/yaver-models` | download/verify/% with sha256 manifest |
| Cred mirror | glass OAuth token mirror | `~/.claude/.credentials.json` mirrors agent→agent |

**Key move:** the mobile terminal connects to a *device* at a base URL via
`connectionManager.clientFor(id)`. It doesn't care whether that URL is a
Hetzner box or `127.0.0.1` on the phone. **Make the phone host its own agent on
loopback → every existing screen works on-device with zero protocol changes.**
The phone becomes a device in its own device list.

## Android has BOTH engines — proot CLI *and* in-app Hermes

The Hermes-native agent loop isn't iOS-only: it's the same RN/Hermes runtime on
Android, so Android phones get **two** on-device engines and pick per task:

| | proot CLI (`cli-on-device`) | in-app Hermes (`hermes`, phone) |
|---|---|---|
| what runs | the REAL claude/codex/opencode | Yaver's Hermes loop, model via `codingBackend` |
| fidelity | full Claude Code (all its tools, MCP, hooks) | "Claude that edits files + reaches for a machine" |
| weight | ~400–500 MB rootfs, ptrace overhead | zero extra install, no rootfs |
| backgrounding | at risk (proot tree, test T5) | survives like any JS work |
| auth | mirror → cred-bind into rootfs | the one mirrored plan token, in-process |

`codingSession.ts` picks via `prefs.onDeviceEngine`: `auto` runs the real CLI
when the rootfs is installed (richest), else in-app Hermes; `hermes` forces the
lightweight in-app loop even with a rootfs present (dodges the backgrounding
risk, instant, still $0 on plan); `cli` forces proot. So a low-storage phone, or
a user who just wants a quick edit, never needs the rootfs at all — Hermes-in-app
is the universal floor on both platforms, with proot as the Android power tier.

## Android — run the real thing

### Stack

```
 Yaver RN app (Hermes)
   XtermView ── /ws/terminal ──► 127.0.0.1:18080        (unchanged client)
                                   │ loopback
 libyaver.so   (Go agent, android/arm64, STATIC, from jniLibs — executable)
   • HTTP/QUIC/PTY/MCP/runner machinery — all reused
   • opens /dev/ptmx for the PTY
   • when YAVER_ANDROID_ROOTFS is set, wraps PTY/runner exec with proot
                                   │ exec
 libproot.so + libproot-loader.so  (userspace chroot, ptrace, NO root)
   └─ Alpine arm64 rootfs (app filesDir):
        node · npm · git · ripgrep · bash · claude-code · codex · opencode
```

**Why this layering:** the Go agent is static (no libc) → runs directly from
the native-lib dir as a normal Android process (binds loopback, reads app dirs,
opens `/dev/ptmx`). proot wraps only the *runner subtree* (node + CLI), which is
the only part that needs a real Linux FS + dynamic loader. claude-code is
network-bound, not syscall-bound, so proot's ptrace overhead is negligible.

### Three platform tricks (all battle-tested)

1. **Ship executables as `lib*.so` in jniLibs** with `extractNativeLibs=true`.
   The native-lib dir is the one place Android guarantees executable,
   APK-signed code. `libyaver.so`, `libproot.so`, `libproot-loader.so`. Launch
   with `ProcessBuilder("$nativeLibDir/libyaver.so", "serve", …)`.
2. **proot for the rootfs** — `proot -r $rootfs -b /dev -b /proc -b /sys -w …
   /usr/bin/env … /bin/sh`. ptrace-based, no root, remaps paths, execs ELF via
   the loader so W^X never bites inside the rootfs.
3. **`/dev/ptmx` is open to unprivileged apps** — `creack/pty` in
   `terminal_session.go` works unmodified.

### The Go hook — env-gated, no build tags

`sandboxWrapCmd(cmd)` rewrites an `*exec.Cmd` to run under proot **iff**
`YAVER_ANDROID_ROOTFS` / `YAVER_ANDROID_PROOT` are present in the environment
(set only by the Android `SandboxService`). On every other platform the env
vars are absent → no-op. No build tags, zero risk to the Mac/Linux build, and
the argv builder is a pure function unit-tested on any host.

- `desktop/agent/sandbox_proot.go` — `sandboxConfig`, `buildProotArgv()`,
  `sandboxConfigFromEnv()`, `sandboxWrapCmd()`.
- `desktop/agent/sandbox_proot_test.go` — argv construction, env round-trip,
  no-op when env absent, idempotency.
- Hook: `console_terminal.go` calls `sandboxWrapCmd(cmd)` before
  `newTerminalSession`. For the PTY-only spike this is the *only* agent change —
  the shell itself runs inside proot, so when the user taps the `claude` runner
  toggle (which types `claude …` into the PTY) it resolves inside the rootfs.

### Bootstrapping the rootfs

Pre-bake the image — don't make users `apk add`. Add a `yaver-rootfs` release
to `kivanccakmak/yaver-models` (or a sibling repo): a compressed Alpine arm64
tarball with `node npm git ripgrep bash @anthropic-ai/claude-code @openai/codex
opencode` pre-installed. First run downloads ~150–250 MB compressed, extracts
into `filesDir/rootfs` via proot, writes a version stamp. Reuse the
download/verify/% pattern from `models.ts` / `RootfsInstaller.kt`. On-disk
~400–500 MB. arm64-only.

Offline fallback: bundle the bare Alpine miniroot (~3 MB) in `assets/`, then
`apk add` on device when network is present.

### Credentials — the bind that actually closes the loop

There's a path-translation trap here the first cut missed. `AcceptMirrorPayload`
(runner_auth_mirror.go) writes the mirrored credential to the **agent's**
`os.UserHomeDir()/.claude/.credentials.json`. But `claude` runs *inside* proot
with `HOME=/root`, so it reads `$rootfs/root/.claude/.credentials.json` — a
**different path**. Mirroring alone does nothing; the token never reaches the CLI.

The fix (in `sandbox_proot.go`): `buildProotArgv` binds the agent's host cred
dirs into the rootfs `/root`, driven by `YAVER_ANDROID_CRED_HOME` (set by the
launcher to the agent's host `$HOME`):

```
-b $CRED_HOME/.claude:/root/.claude
-b $CRED_HOME/.codex:/root/.codex
-b $CRED_HOME/.config/opencode:/root/.config/opencode
-b $CRED_HOME/.local/share/opencode:/root/.local/share/opencode
```

`sandboxWrapCmd` MkdirAll's those host dirs first (proot refuses a bind whose
source is missing). Now the mirror writes to `$CRED_HOME/.claude`, the bind
surfaces it at `/root/.claude`, and the in-rootfs `claude` is authed as you.
Bonus: runner *state* (claude projects/todos, opencode config) lives on the host
side, so it survives a rootfs rebuild.

- **Mirror (default, claude + codex):** desktop → phone via
  `runner_auth_mirror.go`. Mac stays source of truth. **opencode is not
  mirror-supported yet** (the mirror only knows claude/codex) — its dir is bound
  so on-device login persists, but a desktop push needs mirror support added.
- **On-device login (fallback):** claude-code's OAuth is a localhost-callback
  flow; bind the callback on loopback, open the system browser, redirect back
  via custom scheme. Reuse `runner_auth_browser_*`. Because the cred dirs are
  bound to the host side, a login inside proot persists across rebuilds.

### Why perf/battery is a non-issue

The LLM is in the cloud. On-device claude-code does: read/write files, run
git/tsc/tests, stream tokens over HTTPS. I/O + light CPU. The opposite of the
"Mobile Sandbox local-LLM" idea (which *is* heavy).

### New code (Android)

| File | Purpose | Status |
|---|---|---|
| `desktop/agent/sandbox_proot.go` | proot wrap (env-gated) + pure argv builder + **credential binds** | **built + tested** |
| `desktop/agent/sandbox_proot_test.go` | unit tests incl. cred-bind + env round-trip | **built** |
| `desktop/agent/console_terminal.go` | `sandboxWrapCmd` before PTY start | **hooked** |
| `desktop/agent/tasks.go` | `sandboxWrapCmd` at warmup / version-check / task / fork spawns; `CheckRunner` skips host `LookPath` under the sandbox | **hooked** |
| `scripts/build-android-sandbox.sh` | cross-compile `libyaver.so`, fetch proot, bake rootfs | scaffolded (PROOT_SRC TODO) |
| `mobile/src/lib/codingSession.ts` (+ `.test.mts`) | unifying engine×target policy (sandbox / remote / hermes-only-remote / Android in-app Hermes) | **built + tested** |
| `mobile/src/lib/localBox.ts` (+ `.test.mts`) | synthetic "This phone" device + loopback reachability probe | **built + tested** |
| `mobile/src/lib/remoteApplyTarget.ts` (+ `.test.mts`) | `ApplyTarget` over the box's `/host-share/fs/*` (Hermes-only-remote edits) | **built + tested** |
| `mobile/src/lib/codingExecution.ts` | session → {applyTarget, exec} surface (phone vs box) | **built** (RN glue) |
| `mobile/src/lib/sandboxControl.ts` | JS bridge: start/stop/status/installRootfs + wire loopback client | **built** (RN glue) |
| `…/sandbox/SandboxService.kt` | foreground service: launch + supervise `libyaver.so serve`, set `YAVER_ANDROID_*` incl. `CRED_HOME`, WAKE_LOCK | **built** |
| `…/sandbox/RootfsInstaller.kt` | download/verify(sha256)/extract rootfs (self-contained ustar tar.gz) | **built** |
| `…/sandbox/SandboxModule.kt` + `SandboxPackage.kt` | `NativeModules.YaverSandbox` bridge + progress events | **built** |
| `…/MainApplication.kt`, `AndroidManifest.xml`, `gradle.properties` | register package, `<service specialUse>` + FGS/WAKE_LOCK perms, `useLegacyPackaging=true` | **wired** |
| DeviceContext injection of `buildLocalBoxDevice` + enable UI | surface "This phone" in the picker; settings toggle to start the sandbox | **NOT built** |

Client diff is near-zero: `RemoteBoxPickerModal` gains one synthetic entry;
runner toggles / xterm / OpenCode modal all speak to `127.0.0.1:18080` exactly
like a remote box.

**Launcher contract (for `SandboxService.kt` when it's written):** set
`YAVER_ANDROID_ROOTFS`, `YAVER_ANDROID_PROOT`, `YAVER_ANDROID_LOADER`,
`YAVER_ANDROID_TMP`, and `YAVER_ANDROID_CRED_HOME` (= the agent process `$HOME`,
where the mirror writes creds). The Go side is a no-op until all of
rootfs+proot are present, so a half-set env can't half-activate the sandbox.

### Android phasing

1. **PTY-only spike** — proot + Alpine + node + claude in jniLibs, `/ws/terminal`
   to a local PTY. Proves the toolchain runs + renders. ← *this scaffold*
2. **Agent-in-the-loop** — `libyaver.so serve` on loopback; phone shows up as a
   device; runner toggles + OpenCode config + MCP light up for free.
3. **Pre-baked rootfs + installer UI** — instant-on, versioned, sha256.
4. **Credential mirror** — auto-seed `.claude`/`.codex`.

## Android on-device test plan

Everything Android-side is a bet on facts that can only be checked on a real
device. Run these in order; each gates the next. Hardware: one arm64 phone
(API 29+), `adb` over USB. Use a debug APK with the jniLibs payload from
`build-android-sandbox.sh` and a minimal `SandboxService` that just execs
`libyaver.so serve` with the launcher env (write the throwaway service for the
test even though the production one isn't built).

**T0 — payload lands executable.** After install:
`adb shell run-as io.yaver.mobile ls -l files/../lib/arm64/` (or the native-lib
dir). Confirm `libyaver.so`, `libproot.so`, `libproot-loader.so` are present and
`-rwxr-xr-x`. *Fails if* `extractNativeLibs=false` — set `useLegacyPackaging true`.

**T1 — proot runs at all (the W^X + ptrace smoke).**
`adb shell` into the app uid context and run the proot argv by hand against a
bare Alpine miniroot: `…/libproot.so -r <rootfs> -b /dev -b /proc -w /root /bin/sh -c 'echo ok; uname -m'`.
Expect `ok` + `aarch64`. *Fails if* the kernel blocks ptrace from the app uid →
the whole approach is dead on that device; record the OEM/Android version.

**T2 — node + the real CLI execute in the rootfs.**
`… /bin/sh -lc 'node -v && claude --version && codex --version && opencode --version'`.
All four print versions. *Fails if* a binary is the wrong libc (codex is a musl
arm64 Rust binary — verify it's the musl build, not glibc) → rebuild the rootfs.

**T3 — the agent ↔ PTY loopback path.** Launch `libyaver.so serve` via the test
service with the full launcher env (incl. `YAVER_ANDROID_CRED_HOME`). From the
app, open the terminal against `127.0.0.1:18080` `/ws/terminal`. Type `ls`, then
the `claude` runner toggle. Expect a shell prompt rendered in `XtermView` and
`claude` starting inside the rootfs. This proves `console_terminal.go`'s
`sandboxWrapCmd` hook end-to-end.

**T4 — credential loop closes (#3).** *Before* any login, on the desktop run the
mirror to the phone device. Then in the rootfs:
`… /bin/sh -lc 'cat /root/.claude/.credentials.json | head -c 40'` — expect the
`sk-ant-oat-` prefix (NOT `sk-ant-api-`). Then a real one-shot:
`… claude -p 'print hello and exit'` should run with **no auth prompt**. *Fails
if* the file is absent → the bind isn't wired; re-check `YAVER_ANDROID_CRED_HOME`
matches the agent's `os.UserHomeDir()` and that `sandboxWrapCmd` MkdirAll'd the
dir before the mirror wrote.

**T5 — THE load-bearing test: survive backgrounding.** Start a long task:
`claude -p 'read these 5 files, summarize each, then wait 90s'` (or a scripted
`sleep 120 && echo DONE`). Immediately background the app (Home), open 2–3 other
apps, wait 120s, return. Expect `DONE` / completion. *Fails if* Android SIGSTOPs
or kills the proot tree on background → the on-device-task story needs a
foreground-service + `WAKE_LOCK` (already the plan) and possibly a persistent
notification "agent running"; if it *still* dies, on-device long tasks are
remote-only and only interactive/short runs work on-device. **This is the test
that decides how much of the Android story ships.**

**T6 — task path (phase 2).** With the phone registered as a synthetic device,
fire a *task* (not the interactive PTY) and confirm `tasks.go`'s wrapped spawn +
`CheckRunner`-skips-`LookPath` actually start the runner in the rootfs. Confirms
the four `sandboxWrapCmd` hooks + the `CheckRunner` sandbox guard.

Record each device's result (OEM, Android version, T1/T5 pass/fail) — T1 and T5
are the two that vary by vendor and can't be reasoned about a priori.

## iOS — wrap the behavior, three tiers

You cannot run the binary on a stock iPhone: no exec of downloaded code, no JIT
→ no V8 → no Node, no ptrace → no proot, no on-device Linux VM
(Virtualization.framework is macOS-only; iSH/UTM are slow emulation +
App-Store-fragile). So:

### Tier 1 — Remote, made invisible (the real default)

iOS runs the *actual* claude/codex on a paired box (Mac / Linux / auto cloud)
and the phone is the thin xterm.js terminal over `/ws/terminal`. **Already works
today.** "Perfect" is product polish:

- **Zero-config box:** one-tap spin up a cheap arm64 cloud box (Yaver Cloud
  credits + Hetzner lifecycle, both dormant) pre-baked with claude/codex.
- **Sub-second feel:** relay-first + direct-LAN upgrade + warm pool + replay
  buffer on reconnect (all built). 90s idle TTL survives backgrounding.
- **Credential mirror** so the box is authed as you instantly.

### Tier 2 — Hermes-native agent loop (standalone, cloud model)

When there's no box, run a Yaver-native loop in Hermes that *behaves* like
claude-code — reuse `yaverAgentRunner.ts` + `localAgent/orchestrator.ts` +
`sandbox-ai.tsx`, pointed at the **cloud model** (mirrored OAuth / BYOK):

- File tools on-device (read/write/edit an app-sandbox project dir).
- `git` via `isomorphic-git` (pure JS, no binary).
- Light shell via WASM busybox/coreutils in Hermes (WASM-in-Hermes is now
  native / lower-risk).
- Build / test / `tsc` / native compile → route to a remote box if reachable,
  else "needs a machine" (same remote-first brain as `brain.ts`).

Not claude-code; "Claude in a box that edits your phone-local project and
reaches for a machine when it needs to compile."

### Tier 3 — what NOT to do on iOS

No iSH-style emulation, no chasing the JIT entitlement, no UTM/VM. Slow,
fragile, or App-Store-rejected.

## Unifying abstraction — two dimensions, not one

`mobile/src/lib/codingSession.ts` (built, tsx-tested) is the single policy. The
earlier sketch collapsed everything into one "backend" enum; the real model
separates two orthogonal axes:

- **ENGINE** — who runs the agent brain: `cli-on-device` (real CLI in the proot
  rootfs), `cli-on-box` (real CLI on a paired box, box carries its own auth), or
  `hermes` (the phone's Hermes loop, model picked by `codingBackend.ts`).
- **TARGET** — where files + shell execute: `phone` (sandbox / rootfs) or
  `box:<id>` (remote FS + shell over the exec channel).

```ts
resolveCodingSession(intent, env, prefs) → { engine, target, label, reason, boxAuthFree }
```

The cells:

| engine × target | what it is |
|---|---|
| `cli-on-device` × `phone` | **Android:** real Claude Code / Codex / opencode ON the phone, $0 on plan |
| `cli-on-box` × `box` | classic remote dev — the box is authed |
| `hermes` × `phone` | Mobile Sandbox standalone; model via `codingBackend` (subscription preferred) |
| `hermes` × `box` | **Hermes-only remote** — see below |

`cli-on-device` and `cli-on-box` are identical at the wire (both `/ws/terminal`
to an agent, loopback vs remote), so the xterm view is reused verbatim;
`sessionEndpointDeviceId()` returns `null` for loopback or the box id otherwise.
`codingBackend.ts` only matters when `engine === "hermes"`; the CLI engines carry
their own auth and ignore it.

### Hermes-only remote — the auth-overhead killer

`(engine: hermes, target: box)` is the topology you asked for: the phone runs the
agent loop using its **single** mirrored Max/Pro token (or a local GGUF) and
drives **edits + build/test on the box over the exec channel**. The box never
runs claude/codex, so it needs **no runner credentials at all** —
`boxAuthFree: true`. You mirror the plan token *once to the phone* instead of to
every box.

This is the **default** for `intent:"project"` whenever the phone has any usable
Hermes backend — even if the box happens to be authed — because one-token-on-the-
phone strictly beats N-boxes-each-mirrored. Overrides via `prefs.remoteEngine`:
`"cli"` forces the box's own runner (when you *want* the box authed, e.g. for
long unattended runs the phone shouldn't babysit); `"hermes"` forces auth-free
even if available; `"auto"` is the policy above.

Implementation seam (not yet wired): when `engine==="hermes" && target.kind==="box"`,
the orchestrator's file/exec tools (`localAgent/orchestrator.ts`) point at the
box via `connectionManager.clientFor(deviceId)` instead of the phone-local FS —
the same remote-first move `brain.ts` already makes for the troubleshooting
brain. The brain stays on the phone; only the tool target changes.

### How the three surfaces resolve

- **Mobile Sandbox** (`sandbox-ai.tsx`): `intent:"sandbox"`. Android+proot →
  real CLI on the phone-local tree; otherwise the Hermes editor. Same preview +
  apply `EditPlan` either way.
- **Tasks / remote dev** (create-wizard `codingMode`): `intent:"project"`.
  Reachable box → Hermes-only remote by default (auth-free), `cli-on-box` when
  forced or when the phone has no backend; no box → on-device CLI (Android) or
  the "edits here, reaches for a machine to build" Hermes loop (iOS).
- **opencode** is a first-class `RunnerId` everywhere: a rootfs runner
  (`cli-on-device`), a box runner (`cli-on-box`), and `prefs.runner:"opencode"`
  selects it. Its cred dir is bound into the rootfs; desktop *mirror* support is
  the one missing piece (mirror only knows claude/codex today).

## Risk / gotcha table

| Risk | Surface | Mitigation |
|---|---|---|
| W^X exec block (API 29+) | Android | Binaries only via `lib*.so` in jniLibs; rest under proot |
| proot ptrace overhead | Android | Only the runner subtree is proot'd; agent is native; claude is net-bound |
| Rootfs download size | Android | Pre-baked compressed image on GH Releases, sha256, resumable |
| `extractNativeLibs=false` (modern AGP default) | Android | Set `true` so binaries land executable on disk |
| Go CGO for android | Android | Prefer `CGO_ENABLED=0`; if a dep needs CGO, use NDK clang in the build script |
| OAuth on a phone | both | Default to Mac credential mirror; loopback-callback as fallback |
| No exec / no JIT | iOS | Don't fight it — Tier 1 remote + Tier 2 Hermes-native loop |
| App Store / Play review | both | Android = own signed binaries + standard userspace chroot (Termux-class, allowed); iOS never execs |
| Secrets in public repo | both | Mirrored creds stay on-device (SecureStore); never Convex |
