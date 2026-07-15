# visionOS Reload + Session Handoff — 2026-07-14

## Aim

Make the native visionOS Yaver surface actually useful for third-party app development:

- The headset should not be a passive/status-only shell.
- Reload controls should call the same backend paths mobile/feedback SDK uses.
- Hermes bundle push should pass the active React Native project path correctly.
- Failures must be visible in the headset UI instead of looking like dead buttons.
- Live coding sessions should be selectable and driven explicitly, not guessed by the backend.
- Keep this pass focused on visionOS. tvOS work is intentionally left for the other session, except shared Swift files that visionOS imports.

## Main Findings

1. `ops reload mode=dev` was not equivalent to `/dev/reload`.
   It only called `devServerMgr.Reload()`, so it could succeed locally without broadcasting the BlackBox reload command that phones/simulators/preview workers actually need.

2. `ops reload mode=bundle` sent `workDir` to `/dev/reload-app`.
   `handleReloadApp` expects `projectPath`, so the Hermes bundle path could be dropped and the push could appear to do nothing.

3. The shared Swift `AgentClient.ops<T>` accepted HTTP 200 `{ "ok": false, "error": "..." }` as decodable success.
   visionOS buttons could therefore look inert instead of showing the backend error.

4. The shared session client had moved toward named runner sessions, but the new visionOS session sheet initially called it without `session`.
   That would either fail to compile or force backend guessing. The final version loads `runner_sessions` and passes the selected tmux session name.

5. The generated Xcode project is ignored by `visionos/.gitignore`.
   `visionos/project.yml` includes `YaverVision/`, so the real source addition is `VisionSessionView.swift`; run `xcodegen generate` when opening/building locally if the ignored project is stale.

## Files Changed

### Backend reload path

- `desktop/agent/ops_reload.go`
  - `mode=dev` now calls `HTTPServer.handleDevServerReload` through an internal `POST /dev/reload` request.
  - `mode=dev` returns the real `/dev/reload` response in `OpsResult.Initial`, including delivery/change metadata where present.
  - `mode=bundle` now forwards `{ "mode": "bundle", "projectPath": workDir }` to `/dev/reload-app`.
  - Errors from both internal handlers are surfaced as `reload_failed` with the HTTP status and body.

### Shared Swift clients imported by visionOS

- `tvos/YaverTV/AgentClient.swift`
  - `ops<T>` now throws when the ops envelope contains `ok: false`.
  - `call()` already had related error handling and now consistently treats `ok: false` as failure.
  - `reload(mode:workDir:)` is used by the visionOS UI for typed reload results.
  - `runnerSessions()` uses the `runner_sessions` verb, not `runner agents_list`.

- `tvos/YaverTV/SessionClient.swift`
  - `sendText` and `sendChoice` accept an explicit `session`.
  - The JSON body sends that `session` to `/runner/session/turn`.
  - Fixed the `session.data(for:)` shadowing compile bug by calling `self.session.data(for:)`.

- `tvos/YaverTV/Models.swift`
  - Already dirty before this pass, but the current shape matters for visionOS:
  - `RunnerSession` is `name/runner/attached`, with `id == name` and `label`.
  - visionOS dashboard/session sheet now uses this actual shape.

### visionOS UI

- `visionos/YaverVision/Views/VisionDashboardView.swift`
  - Replaced the thin status screen with a spatial control dashboard.
  - Panels now show machine, runtime, preview target, reload controls, coding agents, and Apple surfaces.
  - `Hot Reload` calls `client.reload(mode: "dev")`.
  - `Hermes Push` calls `client.reload(mode: "bundle", workDir: status.devServer.workDir)`.
  - Buttons disable when required state is missing:
    - Hot Reload requires a running dev server.
    - Hermes Push requires a non-empty work dir.
  - Shows visible success/warning/error notices.
  - Warns if reload was accepted but `deliveredTo == 0`.
  - Opens the new native session sheet.

- `visionos/YaverVision/Views/VisionSessionView.swift`
  - New file.
  - Loads runner sessions from the selected machine.
  - Provides a session picker, prompt composer, choice buttons, pane display, and error state.
  - Sends prompts/choices to the explicitly selected session.

## Verification Done

From repo root unless noted:

```sh
xcodegen generate
```

Ran from `visionos/`; regenerated the ignored local Xcode project so the new Swift file was included.

```sh
xcodebuild -project visionos/YaverVision.xcodeproj \
  -scheme YaverVision \
  -configuration Debug \
  -sdk xrsimulator \
  -destination 'generic/platform=visionOS Simulator' \
  CODE_SIGNING_ALLOWED=NO build
```

Result: passed.

```sh
./scripts/deploy-visionos.sh
```

Result: passed. This does Info.plist/entitlement compatibility checks and a native Release `xros` build with `CODE_SIGNING_ALLOWED=NO`. It does not upload unless `--upload` is passed.

```sh
cd desktop/agent
go test . -run 'TestMobileHermesReload'
```

Result: passed.

```sh
cd desktop/agent
go build -o /tmp/yaver-vision-fix .
```

Result: passed.

```sh
git diff --check -- \
  desktop/agent/ops_reload.go \
  tvos/YaverTV/AgentClient.swift \
  tvos/YaverTV/SessionClient.swift \
  visionos/YaverVision/Views/VisionDashboardView.swift \
  visionos/YaverVision/Views/VisionSessionView.swift
```

Result: passed.

## Known Test Noise / Not Fixed Here

These three fail:

- `TestDispatchOpsGuestDeployScopeAllowsDeploy` — `exec manager not initialised (unavailable)`.
- `TestDispatchOpsHostShareDeployHonorsProjectAndInfraPolicy` — same.
- `TestWebReload_DevStartFallbackSurfaceGating` — expected `400`, got `404`
  (`workDir not found on this machine: /tmp/whatever`).

**Resolved question (2026-07-14):** these are pre-existing failures on `main`,
not fallout from this work. Verified by running the same three in a clean
worktree at `HEAD` with none of the dirty tree present — they fail identically:

```sh
git worktree add --detach /tmp/yaver-clean-head HEAD
cd /tmp/yaver-clean-head/desktop/agent && go test . -run '<the three>'
```

Still unfixed, and still out of scope for the visionOS pass — but nobody needs
to re-investigate whether this pass caused them. It did not.

## Important Runtime Note

The installed/running CLI on this machine is still:

```sh
yaver 1.99.303
```

The source builds, but the currently running agent has not been replaced/restarted with the newly built binary in this pass.

Because the user said not to ask keychain if it is us, do not run:

- `yaver auth`
- keychain-backed `yaver vault ...`
- anything that may trigger a macOS keychain prompt

For live local validation, prefer one of:

- Build and run a separate source binary on an alternate port with isolated temp `HOME`.
- Or ask the user to restart their normal agent themselves after they are ready.

## Continuation — 2026-07-14 (Claude)

### 1. Go tests for `opsReloadHandler` — DONE

`desktop/agent/ops_reload_test.go`, six tests. No handler seam was needed: the
existing `startGuestShareFixture` already stands up a real `HTTPServer` with a
real dev-server manager and a real guest config, which is enough to drive the
verb end to end.

Each test was verified to FAIL against the pre-fix code before being kept — a
test that passes on the broken version pins nothing:

- `TestOpsReloadDevDelegatesToDevReloadHandler` — `mode=dev` goes through
  `/dev/reload`. Asserts `deliveredTo` is present in `Initial`, which is only
  computed on the handler path; the old `devServerMgr.Reload()` version returned
  `{mode, framework, reloaded}` and no listener count.
- `TestOpsReloadBundleSendsProjectPathNotWorkDir` — with no dev server running
  there is no fallback to mask a dropped path, so the inner `/dev/build-native`
  answers `PROJECT_REQUIRED` if the field name is wrong and echoes the real path
  if it is right.
- `TestWithDeliveredToMergesWithoutLosingBuildFields` — see (3).
- `TestOpsReloadGuestCannotReloadUnsharedActiveProject` — handler-level project gate.
- `TestOpsReloadIsolatedGuestCannotBuildBundle` — handler-level isolation gate.
- `TestOpsReloadOverHTTPGuestIsRefusedTheReloadVerb` — the live `/ops` route.

### 2. Guest identity was not reaching the forged requests — FIXED (hardening)

`opsReloadHandler` calls the two dev handlers in-process with an
`http.NewRequest` it forges. Both handlers authorize guests *by header* —
`requireGuestAccessToActiveDevServer` (project share) and, for bundle mode,
`isolatedGuestDevMutationBlocked` (isolation) — and both read a missing
`X-Yaver-GuestUserID` as "the owner is calling". The forged request carried no
headers, so both gates saw an owner.

`forwardGuestIdentity` (`ops_reload.go`) now carries the server-stamped guest
headers inward. Safe to do: the auth middleware strips inbound `X-Yaver-Guest*`
and re-stamps them from server-resolved state, so these are not caller-supplied.

**Severity, stated honestly: defence in depth, not a live breach.** No guest can
reach this handler today. `dispatchOps` authorizes *before* invoking the verb:
`authorizeGuestOpsExecution` allows a deploy-scope guest only `info`/`status`/
`deploy`, and every other guest scope lacks `/ops` on its path allow-list
(`circuit`/`stream` reach `/ops` but are pinned to their own verb prefixes).
Host-share *can* call `reload`, but it carries `X-Yaver-HostShareGuestUserID`,
not `X-Yaver-GuestUserID`, and is project-gated at the ops layer instead.

It is still worth holding, because `reload` is declared `AllowGuest: true` —
the verb *intends* to be guest-reachable ("guests with dev-server scope already
hit `/dev/reload`") and the only thing stopping that is an allow-list in another
file. If `reload` is ever added there, the dev-server project gate must already
be standing. It now is.

### 3. A bundle push could not report delivery — FIXED (real UX bug)

The dashboard warns when `deliveredTo == 0` ("nothing received this"). That
warning could never fire for **Hermes Push**: in bundle mode `handleReloadApp`
forwarded `build-native`'s response body verbatim and *discarded* the broadcast's
return value, so the response carried no `deliveredTo` at all. `ReloadResult
.deliveredTo` decoded to nil and the headset reported
`"Hermes bundle built and push command sent."` even when zero devices got it —
a build can succeed into an empty room (no phone paired, preview worker down,
phone attached to a different agent).

`handleReloadApp` now broadcasts *before* answering and merges the true count in
via `withDeliveredTo` (`devserver_http.go`), so bundle reports delivery the same
way hot reload always has. The merge is additive — every field existing SDK/CLI
consumers read off the build response is preserved, and a non-JSON body passes
through untouched.

This is the exact failure-visibility goal of the pass: the dev path was honest,
the bundle path was not.

### 4. tvOS — VERIFIED, not left dangling

The shared Swift files carry a signature change (`SessionClient.sendText` now
requires `session:`), and tvOS imports them, so "leave tvOS to the other
session" risked leaving the target uncompilable. It does not:

```sh
cd tvos && xcodegen generate
xcodebuild -project tvos/YaverTV.xcodeproj -scheme YaverTV -configuration Debug \
  -sdk appletvsimulator -destination 'generic/platform=tvOS Simulator' \
  CODE_SIGNING_ALLOWED=NO build     # BUILD SUCCEEDED
```

`tvos/YaverTV/Views/SessionView.swift` was already updated to the named-session
call. tvOS *UX* decisions still belong to the other session; the build does not.

### 5. Deploy script docs — no change needed

`scripts/deploy-visionos.sh` accepts `--upload` and `--native-only` only. There
is no `build-only`; running it bare IS the build/analyze path. The script's own
`usage()` already says exactly this, so there is nothing to correct in the
script — only in the stale memory that claimed otherwise.

## visionOS UI tests — the surface is now actually driven

`visionos/YaverVisionUITests/` (+ `stubagent/`, + README). Six XCUITests drive the
real app in the visionOS simulator with real taps, against a stub agent that
speaks the `/ops` wire protocol. All six pass. Setup notes are in the README; the
short version is that the app is signed in through UserDefaults' argument domain,
so **no production code has a test hook in it** and nothing touches the keychain.

This was the gap nothing else could close. Every interesting behaviour on this
surface is a *reaction to something awkward the backend said*, and a Go test or a
compile cannot see whether the reaction reached a pixel.

Verified on-device (simulator), not by inspection:

- The dashboard renders live machine state (agent version, framework, project,
  task counts) fetched over `/ops`.
- **The refusal reaches a pixel.** The agent answers a refusal with HTTP 200 +
  `{ok:false,error}`. Codex's `AgentClient` fix is what turns that into a thrown
  error; the test asserts the agent's own words ("no dev server is currently
  running") appear on screen. That is the dead-button bug, dead.
- **`deliveredTo == 0` warns instead of claiming success** — the path that only
  became reachable once the backend started reporting the count for bundle pushes.
- **The session turn names its session.** The stub refuses an unnamed turn the
  same way the agent does when several runners are live, so a reply in the pane
  is itself the proof. The stub log confirms `session="yaver-codex"` on the wire.
- `runner_sessions` decodes and lists (the panel used to say "no active runner
  sessions" while a runner was live, because the old client asked the wrong verb).

### Found by driving it: the notice was wiped before anyone could read it

`reload()` set the success/warning notice and then called `refresh()` — and
`refresh()` ended with `notice = nil`. Every success and every warning was
destroyed milliseconds after being set. Only *errors* survived, because a throw
skips the refresh entirely.

So the headset could never show "Hot reload command sent", and — even with the
backend fix in (3) supplying `deliveredTo` — could never show the "nobody
received this" warning either. The button looked dead on the happy path for the
mirror-image reason it looked dead on the sad one.

`refresh(clearNotice:)` now preserves the outcome a reload just reported, while a
user-initiated refresh (pull-to-refresh, toolbar, switching machine) still clears
what is genuinely stale. Both reload tests failed before this and pass after.

## Sign-in — the headset was asking for something impossible

The visionOS sign-in was the tvOS one, copied. Its own header said so: *"identical
flow to tvOS… only the presentation is spatial."* The presentation is the one
thing that could not be shared.

**"Scan this QR with your phone" cannot be done by someone wearing a headset.**
The QR lives on a virtual plane inside the display; the phone's camera is pointed
at the room. You are wearing the thing you are being told to photograph. On a TV
the same screen is fine — a television is a real screen a real camera can see — so
tvOS keeps its QR. In the headset it was the first screen a new user ever hit, and
no user could ever complete it.

`VisionSignInView` now offers three paths, each covering what the others cannot:

1. **Sign in with Apple (native, the fast path).** The headset already holds an
   Apple ID: look, pinch, Optic ID. No code, no QR, no second device, seconds.
   New shared `tvos/YaverTV/AppleSignIn.swift`; the backend contract already
   existed (`POST /auth/apple-native`, verified against Apple's JWKS with
   `audience: io.yaver.mobile` — the exact bundle ID tvOS and visionOS already
   ship under, so **no backend change**). Also added to tvOS, above its QR.
2. **Sign in with Safari.** Everything Apple cannot serve: Google, Microsoft,
   GitHub, GitLab, passkey, email — and any account with 2FA, which
   `/auth/apple-native` deliberately refuses to mint a session for.
3. **Approve from your phone.** The short code, set large. A phone IS visible
   through passthrough, so reading a code and typing it is possible — only
   photographing the panel is not.

### The fast path had to be guarded, or it would strand people

Yaver resolves an OAuth login **strictly by linked provider identity, never by
email** — `auth.ts::findUserForOAuth` takes the email as `_email` and ignores it,
deliberately. So a user whose Yaver account is Google would tap "Sign in with
Apple" and NOT land in their account: they would silently get a **second, empty
one**, same email, no machines. On a headset that reads as "all my machines are
gone". That is the known OAuth-split failure, and a big blind Apple button walks
users straight into it.

So the fast path checks before it mints: it reads the email claim off Apple's
token (unverified — used only to ask a public question), calls the existing
anonymous `GET /auth/email-providers?email=`, and if the address already signs in
with providers that do NOT include Apple, it **refuses and names them**: "This
email already signs in to Yaver with Google, not Apple… Use Sign in with Safari
and choose Google." The lookup fails OPEN — a guard rail, not a gate.

Two UI tests pin this: the QR instruction is gone, and all three paths are
offered.

## Still Remaining

**Real hardware.** The six UI tests run against the visionOS *simulator* and a
*stub* agent. That covers every branch the surface can take, but it has still
never talked to a real `yaver serve` from a real headset with a real phone
attached. What that would add over the simulator: actual Hermes bundle build
timing, a real BlackBox broadcast to a physical device, and Vision Pro's real
input model (gaze/pinch) rather than synthesised taps.

The running agent on this machine is still `yaver 1.99.303` — the source builds
but the daemon has not been restarted onto it. Restarting it is the user's call
(and per their standing instruction, do not run anything that can raise a
keychain prompt: no `yaver auth`, no keychain-backed `yaver vault`).

## Suggested Claude Code Start Point

1. Read this file.
2. Run:

```sh
git diff -- desktop/agent/ops_reload.go \
  tvos/YaverTV/AgentClient.swift \
  tvos/YaverTV/SessionClient.swift \
  tvos/YaverTV/Models.swift \
  visionos/YaverVision/Views/VisionDashboardView.swift \
  visionos/YaverVision/Views/VisionSessionView.swift
```

3. Re-run:

```sh
xcodebuild -project visionos/YaverVision.xcodeproj \
  -scheme YaverVision \
  -configuration Debug \
  -sdk xrsimulator \
  -destination 'generic/platform=visionOS Simulator' \
  CODE_SIGNING_ALLOWED=NO build
```

4. If the Xcode project does not include `VisionSessionView.swift`, run:

```sh
cd visionos && xcodegen generate
```

5. Do not use keychain/auth commands unless the user explicitly permits it.
