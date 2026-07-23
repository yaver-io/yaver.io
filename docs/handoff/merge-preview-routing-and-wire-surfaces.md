# Handoff: merge preview-routing + wire-surfaces to `main`

**Written** 2026-07-22 · **For** the session holding uncommitted work in
`desktop/agent/` · **Author** a parallel session that has now stopped touching
the tree.

---

## TL;DR

Two commits need to reach `main`. `main` is an **ancestor** of them, so this is
a **fast-forward — no conflicts are possible**.

```
69579911d feat(wire): --surface routes push to watchos/wearos/tvos/visionos
cae2ee548 feat(preview): route pixels-or-URL per viewer; wire surfaces beyond ios|android
```

They currently sit on branch **`tmux-vibe-sessions`**.
`main` is at `93cad6563`.

```bash
git merge-base --is-ancestor main tmux-vibe-sessions && echo "fast-forward, safe"
```

Run that first. If it prints nothing, **stop** — someone pushed to `main` since
this was written and the situation below no longer holds.

---

## ⚠️ Read this before you check anything out

**Your uncommitted work is in the shared working tree and I did not touch it.**
As of writing, these were modified and **not committed by me**:

```
desktop/agent/tmux.go
desktop/agent/runner_pty.go
desktop/agent/terminal_session.go
desktop/agent/runner_pty_attach.go   (untracked, new)
desktop/agent/httpserver.go
desktop/agent/mcp_tools.go
backend/convex/…, mobile/…, cloud-images.json
```

Every commit I made used `git commit -F <msgfile> -- <explicit paths>`, so
nothing of yours was swept in. Verify if you like:

```bash
git show --stat 69579911d
git show --stat cae2ee548
```

You should see only these five files across both commits:

```
desktop/agent/wire_surfaces.go              (new)
desktop/agent/wire_cmd.go
desktop/agent/preview_transport_route.go    (new)
desktop/agent/preview_transport_route_test.go (new)
desktop/agent/workspace_preview_strategy.go
desktop/agent/Dockerfile.yaver-swiftwasm
```

**One thing to know:** the branch name changed under me mid-session — I believed
I was on `task-history-whatsapp-thread` and the commits landed on
`tmux-vibe-sessions`. That was almost certainly a branch switch from your side
of the shared tree. It is harmless (the commits are pathspec-scoped and the
content is correct), but it is why this doc names the branch explicitly instead
of saying "my branch".

---

## Recommended merge: a worktree

This never checks anything out in the shared tree, so your dirty files are not
even eligible to be disturbed.

```bash
cd /Users/kivanccakmak/Workspace/yaver.io

git worktree add /tmp/yaver-merge main
cd /tmp/yaver-merge

git merge --ff-only tmux-vibe-sessions     # fast-forward; refuses if anything unexpected
git push github main

cd /Users/kivanccakmak/Workspace/yaver.io
git worktree remove /tmp/yaver-merge
```

`--ff-only` is the safety: if the world changed and a real merge would be
needed, it **fails instead of creating a merge commit** you did not intend.

### If you would rather not use a worktree

Only when your own changes are committed or stashed:

```bash
git stash push -u -m "wip: pty/tmux"   # -u includes runner_pty_attach.go
git checkout main
git merge --ff-only tmux-vibe-sessions
git push github main
git checkout tmux-vibe-sessions        # or wherever you were
git stash pop
```

I did **not** do this myself precisely because it moves your files.

---

## Build state — read before you judge the diff

**The package does not compile in the shared tree right now, and it is not
because of these commits.** The errors are in your in-flight work:

```
./tmux.go:457  not enough arguments in call to m.pollTmuxOutput
./tmux.go:790  not enough arguments in call to m.pollTmuxOutput
./runner_pty_attach.go:101  ts.onClose undefined (type *terminalSession …)
```

I verified my code separately: copied `desktop/agent/` to a scratch dir,
restored `tmux.go` / `runner_pty.go` / `terminal_session.go` to their **committed**
state, dropped the untracked `runner_pty_attach.go`, and built there.

- `go build` — clean
- `go vet` — clean
- 9 route tests — pass

Once your work lands, the tree should be green again. If it is not, suspect the
overlap in `wire_cmd.go` (I added a `--surface` flag and one call site) before
suspecting anything else — that is the only file both of us are near.

Two verification traps I hit, so you do not:

- `go build ./...` in `desktop/agent` fails on an **output-binary name
  collision**, not on code. Use `go build -o /dev/null ./...`. It cost me a
  false "BUILD FAILED".
- A broad `go test ./...` in `desktop/agent` **signs you out** — it hits the real
  `~/.yaver`. Always scope with `-run`.

---

## What the commits actually do

### `cae2ee548` — preview routing

Every browser-renderable stack was resolving to `chrome-webrtc`
(`workspace_preview_strategy.go:142`): headless Chromium on the workspace
encoding **video of a web page**, so a viewer's *browser* could watch a picture
of something it could have loaded directly. ~1 vCPU per session, permanently, on
a 2c/4GB box.

Adds `PreviewDirectURL` and routes on `(stack, viewer, reachability)`. All three
matter, which is why stack alone cannot decide it:

- a **watch** cannot render either an app or a video → `status-only`
- a **car** must not render an interactive app (CarPlay templates forbid it)
- an **unreachable dev server** forces pixels however web the app is

WebRTC becomes what it should always have been: the transport for native Swift
and native Kotlin, which genuinely cannot be a URL. Feedback SDK follows the
transport — in-app SDK on the URL path (RN / Flutter / web / wasm all have real
ones), viewer-triggered over the events DataChannel on the video path, since no
native Kotlin/Swift feedback SDK exists.

Same commit removes Chromium from `Dockerfile.yaver-swiftwasm`. **Ubuntu jammy
ships no working Chromium deb under either name** — `chromium-browser` is the
snap stub, and plain `chromium` is Debian's package name and fails with exit
100. A previous comment claiming "jammy ships a real chromium deb" was written
from memory and never built. With SwiftWasm routing to `direct-url`, that image
needs no browser at all.

### `69579911d` — `yaver wire push --surface`

`wire push` accepted only `ios|android`, so asking for a watch produced
`--platform must be ios or android`: a syntax error in reply to a reasonable
question.

```
--surface all       lists every surface, whether THIS repo builds it, what would happen
--surface watchos   builds+pushes the iOS HOST app, then names the phone tap
--surface wearos    routed to adb — a Wear OS watch is an ordinary adb target
```

Third-party monorepos are handled by reading build files rather than assuming
the Yaver layout: `pbxproj SDKROOT watchos/appletvos/xros`, and
`android.hardware.type.watch` in a manifest. A mismatch **warns and continues** —
detection reads conventional locations only, and being wrong about someone
else's layout must not block them.

Two deliberate refusals rather than false greens:

- **tvOS / visionOS `exit(2)`** stating the build path is not wired. Detection
  and device listing genuinely work, but `native_build.go` has no scheme/SDK
  selection for them; building the iOS target and calling it a tvOS push would
  install the wrong app and report success.
- **`--surface watch` rejected as ambiguous.** Apple Watch and Wear OS install
  in completely different ways; guessing hands the user confident wrong
  instructions.

---

## Context you may be asked about

**The user's watch report** — "Yaver is on my phone from TestFlight but I don't
see it on my watch" — is **not a bug**. The watch target ships correctly (50
pbxproj references, 4 `Embed Watch Content` phases, `deploy-testflight.sh` runs
`add-watch-ios-target.js`). Apple ships **no tool** — not devicectl, not
ios-deploy, not Xcode — that installs a watchOS app onto a paired watch
independently of its host iPhone app. The fix is a tap:

> iPhone → **Watch** app → **AVAILABLE APPS** → **INSTALL**

That is now encoded in `wire_surfaces.go` as `ChannelCompanion` so no one has to
rediscover it.

**Swift-on-Linux closed loop** is proven, measured in a `swift:6.3.0-jammy`
container on this Mac:

| | |
|---|---|
| Cold build (Swift → wasm) | 11 s |
| **Edit → artifact** (background-colour change) | **6 s** |
| Artifact | 10,056,587 bytes |
| Verified | sha changed `bfd29042…` → `113223bc…`, and `#1e3a8a` present *inside* the wasm |

The fixture (`demo/yaver-todo-swift-wasm`) was reverted to `#fafafc` afterwards;
the test image was deleted (5.93 GB reclaimed). No cloud resources were
provisioned — no Hetzner box was created, so nothing is metering.

---

## Not done

- **tvOS / visionOS build paths** — need scheme + SDK selection in
  `native_build.go`.
- **TestFlight release** — untouched. Note the ~15–20 uploads/app/day cap with
  no rollback.
- **Surface ports** — tvOS / watchOS / web still need the preview-route layer
  ported natively; RN surfaces (mobile, tablet, car, glass) inherit it through
  shared `DeviceContext`.
