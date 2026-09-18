# Yaver.io — Agents Guide

This file is for AI coding agents (OpenAI Codex, Aider, Amp, Goose, Claude Code, OpenCode, …) that look for an `AGENTS.md` convention. The detailed project guide lives in [`CLAUDE.md`](CLAUDE.md) — read that first. This file only calls out the rules that every agent needs to follow regardless of which tool is driving.

## Golden rule: .md files go stale, code is the source of truth

Every Markdown file in this repo — including this one, [`CLAUDE.md`](CLAUDE.md), [`docs/architecture/AI_ARCH.md`](docs/architecture/AI_ARCH.md), [`docs/architecture/REMOTE_WORKER.md`](docs/architecture/REMOTE_WORKER.md), `init.md`, and every `*.md` under `docs/` — was accurate on the day it was written. Drift is the norm, not the exception. Routes get renamed, handlers get refactored, fields get added, version numbers roll forward. The docs don't always keep up.

**Before you act on any claim a `.md` file makes:**

1. **Grep the code.** If a doc says the agent has `POST /foo/bar`, run `grep -n 'HandleFunc.*"/foo/bar"' desktop/agent/*.go`. We shipped CLI 1.99.33 with `yaver diagnose` handlers compiled in but the `mux.HandleFunc` line missing — `/diagnose` returned 404 in production despite the doc saying the endpoint existed, and the bug only got caught because a smoke test hit the real route.
2. **Re-read the file on disk, not from memory.** If a doc says a function signature is `foo(a, b int) error`, open the file — it may be `foo(a int, b string) (Result, error)` now.
3. **Check versions.** `yaver --version` (binary on PATH) vs `/info.version` (running process) vs `git log --oneline -- <file>` (HEAD). Disagreement means the doc describes a different slice of time than the one you're operating on.
4. **When the doc and the code disagree, the code wins, and fix the doc as part of your change.** Don't just code around a stale doc — update it.

Treat `.md` files the way you'd treat a commit message from six months ago: useful context, never the authoritative answer.

## What to read before making changes

- Full project guide → [`CLAUDE.md`](CLAUDE.md)
- Runtime architecture (auth / bootstrap / relay / recovery) → [`docs/architecture/AI_ARCH.md`](docs/architecture/AI_ARCH.md)
- Slave-machine / remote-build flows → [`docs/architecture/REMOTE_WORKER.md`](docs/architecture/REMOTE_WORKER.md)
- Per-project cached context → `init.md` at the project root (best-effort; may be out of date)
- For local iOS/TestFlight deploys on this Mac, also read the "iOS TestFlight deploy gotchas" and "iOS — TestFlight" sections in [`CLAUDE.md`](CLAUDE.md) before assuming the vault path is working.

After reading the docs, **grep the code for the symbols the docs name** before relying on them.

## Local Deploy Memory

### Project release-secret continuity

Release signing material must not depend on one workstation. Keep the active
local copy owner-only (`0600` files inside a `0700` directory), and keep a
client-side-encrypted recovery copy in the project's dedicated Hetzner Storage
Box namespace. For Yaver Android releases, that namespace is
`project-secrets/other-projects/yaver/android-play/`; keep signing keys under
`keystore/` and store credentials under `credentials/`.

- Encrypt locally before upload (AES-256 with PBKDF2, at least 600,000
  iterations). Never upload plaintext keys, passwords, property files, or
  service-account JSON.
- Store the recovery phrase in the operator's password manager and local
  Keychain, never in this repository, shell history, logs, manifests, or the
  Storage Box.
- Upload atomically through a `.part` name, rename only after completion, then
  download the ciphertext and verify its SHA-256 against the local ciphertext.
- Keep every project in a separate namespace. New projects start from
  `project-secrets/other-projects/_template/`; never share an upload key or
  credential bundle across products.
- A backup is not authorization to publish. Yaver's existing human release
  gate and canonical `./deploy/deploy.sh <target>` entry point still apply.

### Android local validation (no submission)

For Android readiness checks, compile and sign locally but do not submit to
Google Play. This lane is deliberately separate from deployment and must not
call `./deploy/deploy.sh android`, Play APIs, or upload scripts.

- Run one Gradle build at a time on the 8 GB validation Mac. Keep
  `org.gradle.parallel=false`, `org.gradle.workers.max=2`, and the mobile
  Gradle heap capped at 3 GiB; standalone Wear OS/Android TV builds use a 2 GiB
  heap.
- Phone/tablet + Android Auto are validated by the mobile release APK:
  `cd mobile/android && ./gradlew --no-daemon :app:assembleRelease`.
- Wear OS and Android TV are standalone projects. Use a Gradle >= 8.7 binary
  (the pinned Android plugins are validated with Gradle 8.14.3), then run
  `bundleRelease` in `wear/` and `androidtv/` separately.
- Verify APKs with `apksigner verify --verbose --print-certs`; verify AABs with
  `jarsigner -verify -verbose -certs` and `keytool -printcert -jarfile`.
  The Yaver upload certificate fingerprint is
  `FF:E9:9C:94:63:B0:2A:42:0D:11:42:35:CD:A7:3F:76:7A:5A:0D:18:0F:73:CE:CF:BC:2E:E2:DF:C4:AC:D5:4E`.
- After recording the verification result, remove only the exact generated
  build directories (`mobile/android/app/build`, `mobile/android/app/.cxx`,
  `wear/app/build`, `androidtv/app/build`, and their project `build/` or
  `.gradle` directories). List each target first; never remove keystores,
  `node_modules`, the Android SDK/NDK, or credential files.

The Android validation artifacts are disposable. A successful local signature
does not authorize Play submission; approval and release remain explicit human
steps.

- **Apple web consoles use Chromium through Yaver MCP only.** For Apple
  Developer and App Store Connect work, use the authenticated Chromium profile
  through Yaver's `browser_*` MCP tools (or a headed Chromium handoff when the
  human must complete login/2FA). Never open or fall back to Safari, and never
  use coordinate clicks: inspect the DOM and use named selectors. Keep every
  mutation scoped to Yaver identifiers/apps; never touch another product just
  because it is visible in the same Apple team.
- **One deploy path:** use `./deploy/deploy.sh <target>` from the repo root.
  Do not call `scripts/deploy-web.sh`, `scripts/deploy-testflight.sh`,
  `scripts/deploy-playstore.sh`, `scripts/deploy-convex.sh`, or npm publish
  directly unless you are editing/testing those scripts. Targets:
  `all`, `backend`, `cloudflare`, `ios`, `android`, `npm`, `feedback-sdk`,
  `mcp`. This wrapper
  is the canonical human/agent/MCP entrypoint and performs owner/permission
  checks before delegating to the maintained deploy scripts.
- **Deploy is owner-only:** MCP deploy tools are hidden/denied for non-owner
  accounts, and `deploy/deploy.sh` refuses group/other-writable repo or script
  paths before using deploy credentials. Never weaken this to make a test pass.
- On this Mac, local TestFlight deploys can work even when `yaver vault env --project mobile` is unauthenticated, because the deploy guide in [`CLAUDE.md`](CLAUDE.md) already documents the fallback `APP_STORE_KEY_*` / `APPLE_TEAM_ID` exports used by the working local path.
- The API-key-free lane is **Xcode-managed, not pre-verified**. An Apple ID in
  Xcode preferences or a browser login can coexist with an expired/missing
  Xcode account token; only the provisioning operation proves it. If archive
  says `No Accounts`, refresh the SIMKAB account under Xcode → Settings →
  Accounts (or configure the complete App Store Connect API-key triplet).
- If `scripts/deploy-testflight.sh` appears stuck with almost no output, check for another active `xcodebuild archive` from another local mobile project or an earlier Yaver run before assuming credentials are broken.
- If you must clean local archive artifacts, inspect the exact path first (`ls -la /tmp/YaverBuild /tmp/Yaver.xcarchive /tmp/YaverExport`) and only then remove those specific directories.
- **Headless codesign (SSH / no GUI):** `CodeSign … errSecInternalComponent` means the signing **private key** is in a **locked** keychain, not that the cert is missing. The identity spans TWO keychains — `yaver-ci.keychain-db` (Apple Distribution) + `login.keychain-db` (Apple Development private keys) — so BOTH must be `unlock-keychain` + `set-key-partition-list`'d before archiving, or the archive dies at `CodeSign …/*.appex`. Full recipe in [`CLAUDE.md`](CLAUDE.md) → "Headless codesign". `launchctl asuser` does not help; only the passwords do.
- **Local privileged secrets** (signing-keychain / login / sudo passwords) live in `~/.yaver/local-secrets.env` — `chmod 600`, owner-only, **never committed, never synced to any cloud/GH secret** (a macOS login/sudo password in GH secrets widens the attack surface). The yaver agent reads these to unlock keychains / run sudo headlessly. Canonical home is the encrypted `yaver vault`; the env file is the fallback. See [`CLAUDE.md`](CLAUDE.md) → "Local privileged credential store". Never echo these values into logs, commits, or docs.

## The Snowball Principle — Yaver's development philosophy

**Fix the PRODUCT, never the machine.** This outranks finishing the task in
front of you.

When something is stuck — a box, a phone, a build, a variable, a piece of
state — the tempting move is to unstick *that instance* and move on. Don't.
Mutating one machine back to health teaches the product nothing and guarantees
the next user hits the same wall with no more help than you had. **Every stuck
state is a product requirement nobody has written yet.**

The order is always:

1. **Resolve it first.** Get the user unblocked, on the real box, now.
2. **Then ask: why did the product allow this, and why didn't it say so?**
   Almost always the answer is that something reported success — or reported
   nothing — while the operation was impossible.
3. **Land the change that makes it impossible or self-evident next time**, in
   the Go agent / mobile app / relay / CLI / web *and the wiring between them*.
   Not in a runbook, not in a doc, not in your memory.
4. **Prove the guard works by breaking it** — disable the fix, watch the test
   fail, restore it. A guard you have not seen fail is a guess.

The recurring shape to hunt for: **the inventory says yes, the operation says
no.** A process is alive so liveness passes, while it binds no port. A dev
server is "running" so status is green, while the page it serves cannot load an
asset. A tool is on PATH but is a stub. If you can only learn the truth by
attempting the operation, attempt the operation.

**It applies to the whole stack, equally.** A fix that lands in one of two
browser-preview implementations is not landed — that exact drift shipped a
broken heartbeat, dropped SSE frames, and a dead shake gesture on one screen
while the other was fine. Cross-surface parity is this same rule wearing a
different hat.

## Headless first, then closed loop — and snowball BOTH

**Rule of thumb for developing and testing anything in Yaver:**

1. **HEADLESS FIRST — ask the machine directly.** `curl` the agent route, run
   the CLI verb, call the MCP tool, `codex exec --model <id>`, hit Convex with
   a bearer token. Seconds, not minutes; the failure names itself; no spinner,
   stale bundle or viewport can confound it.
2. **CLOSED LOOP SECOND — prove it where the user lives.** Drive the real
   surface (web dashboard, or the RN app as RN-web in a genuine device
   context) and judge on **PIXELS**, never a status badge.

Why the order is load-bearing (2026-08-02): a colour vibe loop failed on both
surfaces for hours and three ~25-minute browser runs were burned on harness
bugs. One headless probe answered it in ninety seconds — `gpt-5.3-codex` is
REJECTED by the ChatGPT-account subscription, `gpt-5.6-terra` WORKS, and all
six of the owner's machines were pinned to a model that could not run. Browser
automation could never have said that. Equally, no API call could prove the
picker renders and the preview's pixels actually change — that needed the loop.
Each layer answers what the other cannot; the cheap one goes first.

**Both layers must grow with every incident** — the Snowball Principle applied
to the test surface itself:

- **Headless grows a VERB.** If answering took ssh plus a hand-rolled python
  one-liner, that is a missing endpoint. Land it as an ops verb / MCP tool /
  CLI subcommand so every surface and every future session can ask it in one
  call. A question only answerable by hand is a product gap.
- **The loop grows an ARC.** Every confirmed defect earns an assertion in the
  loop that would have caught it, on the surface where it broke. If a loop
  fails for a HARNESS reason, fix the harness and keep the assertion — never
  delete it to get green.
- **Never infer what you can measure.** "The `-codex` suffix must mean
  codex-safe" is precisely the instinct that pinned six machines to a dead
  model, in both directions, twice in one day. Probe, then write the
  measurement into the code with its date so the next reader inherits evidence
  instead of instinct.
- **A guard that has never failed is a guess.** Break it, watch it fail,
  restore it.

## Cross-Surface Task / Render UX Contract

Tasks, Vibing, render pages, browser previews, native previews, remote-runtime
surfaces, and runner OAuth must follow the same product rule on every surface:
web, mobile, tablet, tvOS, watchOS, Wear OS, car, AR/VR, and companion CLI.

- **Runner coding state is explicit.** A task in `queued` or `running` means the
  runner is coding. Do not infer reload permission from output text, spinner
  state, or a dev server saying "ready".
- **Render/reload intent is not render/reload execution.** A
  `runtime_render_requested` event is the agent's structured judgement that UI
  changes are ready. With **Auto-render Vibing mode off (the default)**, it
  becomes a `Render updates` action beside the response and does not execute.
- **Auto-render is opt-in and selective.** When the account setting is on,
  Yaver may execute one render after a structured agent request reaches
  `completed`/`review`; mere task completion never renders. The agent should
  say `UI updates are ready` when a visible change warrants that signal.
- **Explicit user intent always works.** A whole-message command such as
  `reload`, `re-render`, or `refresh the preview`, or a Fast/Full Reload tap,
  queues one render while coding and executes once safe. Mentions such as
  `fix the reload bug` are ordinary coding prompts.
- **Do not reload while coding.** Keep queued coding prompts in order and wait
  until no turn is coding before draining a queued render.
- **Reload is atomic.** If a render/reload is already in flight, coalesce or
  ignore new triggers until it finishes. Do not start another coding turn on the
  same surface mid-reload.
- **Keep the last good surface visible.** First open may show a loading surface;
  a reload must not replace a working iframe/native preview with a branded
  placeholder. Show a quiet status line such as "reload queued" or "refreshing
  after task completion" instead of a modal, overlay, focus steal, or spinner
  that blocks interaction.

## Browser transport contract (RN-web / Selenium lane)

The mobile app also runs as RN-web so a browser can drive the REAL app. A
browser has **no UDP** (no LAN beacon) and **no raw QUIC** (no relay dial) — an
absolute gap, not a flaky one. Rules, full version in [`CLAUDE.md`](CLAUDE.md):

- **One lane, standard protocols**: HTTP to the agent for request/response, SSE
  for server→client streams, WebSocket only when genuinely bidirectional. The
  browser hits the SAME endpoints as native — no second protocol.
- **Convex unchanged**: identity + device rows already arrive over HTTPS; that is
  how the browser learns which host to dial without a beacon.
- **Same security boundary, never weaker**: same bearer token, same checks. The
  agent already echoes the caller's `Access-Control-Allow-Origin` and allows
  `Authorization` — no server relaxation is needed, and none may be added. Never
  CORS `*` on an authed agent route, never a token in a URL.
- **Additive only**: `.web.ts` siblings or `platformTransport.ts` capability
  checks. Never edit the native connect path for the browser. No native file
  changed ⇒ no native behaviour changed.
- **Impossible ⇒ say so**: `explainNoTransport()` instead of a spinner.
- **Dual implementation ⇒ parity test** (`beaconParity.test.ts`), proven by
  breaking it — Metro picks between two independent classes, so drift is
  invisible to `tsc` and crashes at runtime.
- **Closed loop is the method**: changes are verified through the real app via
  `e2e/tests/mobile-app-lane-matrix.spec.ts` — PIXELS / NAMED / SILENT, and
  SILENT is the only failing verdict.
- **VIEWPORT IS A DEVICE CONTEXT, NOT A WINDOW SIZE.** The mobile arc MUST open
  a NEW context with the full device descriptor
  (`browser.newContext({ ...devices["iPhone 15 Pro"] })`) and then ASSERT the
  viewport it got. `isMobile`, `hasTouch`, `deviceScaleFactor` and the UA are
  **context** properties — `page.setViewportSize()` cannot change them — so a
  desktop context resized to 393px is a narrow desktop browser, and RN-web
  renders a DIFFERENT component tree for it. Shrinking the web dashboard and
  calling it "mobile" is a false equivalence: the two share neither transport
  ladder, auth storage key (`yaver.secure.yaver_auth_token` vs
  `yaver_auth_token`), nor render path. Use `web/lib/surfaceViewports.ts`
  (`profileFor` / `viewportMatchesSurface`) — one table, never a literal per
  spec — and drive RN-web at `MOBILE_WEB_URL`, skipping when it is unset rather
  than substituting the dashboard. Reference: `e2e/tests/vibe-color-loop.spec.ts`.
- **Opening the mobile app for a HUMAN to test (manual session):** run
  `node e2e/open-mobile-app.mjs` (headed Chromium, iPhone 13 viewport, touch
  enabled, PERSISTENT profile at `~/.yaver-e2e-profile` — sign in once by hand,
  the session persists for later runs). If the default profile is locked by
  another instance, set `E2E_PROFILE=~/.yaver-e2e-profile-manual`. Never open
  `localhost:8081` in a plain desktop Chrome tab and call it "mobile" — a
  narrowed desktop window is exactly the false equivalence the viewport bullet
  above forbids. For headless verification, inject the agent token into
  `localStorage["yaver.secure.yaver_auth_token"]` (the RN-web key via
  `mobile/src/lib/secureStoreCompat.ts`), then drive the app with
  `devices["iPhone 15 Pro"]` context. Verified 2026-08-09 with
  `e2e/verify_live_console7.mjs` (task detail → LiveConsoleSection).
- **Shared browser queue for concurrent threads:** when a closed-loop arc must
  wait for the browser coordinator, add one Markdown file under
  `e2e/browser-automation/test-cases/YYYY-MM-DD/`; never append to another
  thread's file. Every attempt gets a separate Markdown result under
  `e2e/browser-automation/results/YYYY-MM-DD/`. Run `cd e2e && npm run
  browser-queue:validate`, then the coordinator alone runs `MOBILE_WEB_URL=...
  npm run browser-queue:open`. The launcher enforces one owner-only session,
  an isolated profile, a full `iPhone 15 Pro` device descriptor, and the real
  RN-web URL—no guessed port or dashboard fallback. Cases/results contain no
  secrets, customer names, machine addresses, or absolute home paths; artifacts
  remain untracked and are referenced only by safe relative labels. Headless
  first still applies: the queue is for the PIXELS/NAMED/SILENT proof, not API
  discovery. The executable validator and launcher are the source of truth;
  `e2e/browser-automation/README.md` explains the append-only workflow.
- **Mobile tasks render the LIVE opencode console, not a collapsed summary.**
  `mobile/app/(tabs)/tasks.tsx` consumes the RAW runner stdout lane
  (`?rawSince=` + `onRaw` SSE) into a per-task 512 KB buffer and renders it in a
  foldable `LiveConsoleSection` via the shared `AnsiConsoleText` — same
  colours/grammar as the opencode console (`$` prompts, `> build · model`
  banners, diffs, `● live`/`○ idle` dot, byte counter). This replaced the
  `_Working through implementation details…_` collapse (and the removed
  Chat|Terminal toggle, 33b3d798e). If a task detail shows folded command cards
  with no output, the AGENT is stale: rebuilt agents must contain the opencode
  stdout-capture fix (`closePendingCommand`/`command_output` events; verify with
  `strings <bin> | grep pendingCmdID`). Command cards also stream real stdout
  now — `command_start` → `command_output` → `command_end` SSE.

## Never hot-swap an unsigned agent binary onto macOS

`scp`-ing a local `go build` output over `~/.yaver/bin/.../yaver` takes the box
OFFLINE: macOS kills the unsigned binary under launchd
(`last exit reason = OS_REASON_CODESIGNING`), launchd reports
`state = spawn scheduled` so it looks like it is starting, and the agent never
answers. The user's phone disconnects and nothing in the serve log explains it.

- Prefer the release path (`npm install -g yaver-cli@…`) — signed + notarized.
- If you must hot-swap for a test, ad-hoc sign in the same breath:
  `codesign --force -s - <binary>` then
  `launchctl kickstart -k gui/$(id -u)/io.yaver.agent`.
- `nohup … &` over SSH does not survive the session — let launchd own it.
- Verify with `curl localhost:18080/info` → 200. A PID is inventory; the HTTP
  answer is the operation.
- A local build reports a STALE `--version`, so `/info.version` lies after a
  hot-swap. Never diagnose from it.

## A missing toolchain is a product requirement, not a user error

Never surface a bare "executable file not found". Full version in
[`CLAUDE.md`](CLAUDE.md); the shape is:

**state it → offer the fix if the fix exists → stream the fix → name the
constraint if it does not.**

- **State it on the surface the user is on.** 2026-07-26: the agent correctly
  said `exec flutter: executable file not found in $PATH`, and the phone showed
  "Waiting for the dev server to report its address…". A truthful agent plus a
  client that drops the truth is still an unfalsifiable product.
- **Offer + stream the install.** `install_cmd.go` already has recipes
  (`ensureRunnerInstalledStream`, `installNodeBackedCLI`). Render a button, not a
  dead end, and stream stdout with bytes + elapsed — a 2 GB SDK behind a silent
  spinner is the same defect as a silent `serve`.
- **Resolve per os/arch; only claim impossible after checking.** Flutter ships no
  Linux/arm64 *tarball*, but git-clone is its supported install there and
  `flutter_install.go` already does it — so on the aarch64 box the honest answer
  is "installable", not "render elsewhere". Wrong in either direction is a defect:
  offering an install that 404s teaches the user Yaver lies; declaring impossible
  what the product supports withholds a working capability.

Applies to runners, SDKs, simulators, emulators, adb, keychains — any capability
gap.

## Every failure must carry a route to its fix — four layers

The missing-toolchain rule above is the **first instance of a general law**, not
a special case. It applies to every failure in the product: remote runners,
reload/preview (Hermes, browser, WebRTC), the feedback SDK, connectivity, auth,
builds, deploys. Full architecture with the failure×route matrix and every
`file:line` → [`docs/architecture/FAILURE_PLUMBING_ARCHITECTURE.md`](docs/architecture/FAILURE_PLUMBING_ARCHITECTURE.md);
the failure *inventory* → `docs/audits/failure-recovery-audit-2026-07.md`.
Condensed rule in [`CLAUDE.md`](CLAUDE.md). A failure is shipped only when all
four layers exist:

1. **DETECTION — probe the operation, never the inventory.** `commandExists` is
   a proxy. A tool on PATH can be a stub; a cert can be present and unable to
   sign; a device can be `online` and unreachable.
2. **SIGNAL — structured and named, never prose.** A stable code (14 already
   exist in `desktop/agent/reason_codes.go`) plus typed fields, on *every*
   channel the failure can take: HTTP body, `/dev/events` SSE, task event,
   incident, heartbeat. A bare `{ok:false, error:"<sentence>"}` forces every
   surface to invent a regex, and regexes drift — mobile already ships **three**
   different relay-auth matchers, none a superset of the others.
3. **UI — a named cause the user can actually SEE.** A spinner is a bug, and
   "rendered" is not enough: in build 482 an unbounded diagnostics wall squeezed
   the action lanes to zero height (`40eec39ef`), so the one lane the agent
   offered could not be seen. **Advisory content never wins over the route** —
   not in pixels, and not in time (a blocking preflight in front of a capability
   that already works is the same defect).
4. **ROUTE-TO-FIX — the next tap, in place, streamed.** Not a sentence
   describing a remedy: an invocable `method + path + stream` a surface can
   render as a button, streaming bytes + elapsed, then returning the user to
   what they were doing.

Corollaries, each learned the hard way:

- **A signal with no consumer is not shipped.** `recoverKind`
  (`devserver_http.go:3958`), `capture_error`
  (`remote_runtime_video_track.go:139`), `RunnerPreflightByID`
  (`runner_preflight.go:35` — called by voice only), `RelaySessionExpiredAt` and
  all 14 `reason_codes.go` values are correct producers that **nothing reads**.
  Land the consumer in the same change, with a test that fails without it.
- **Never report success for an operation that did not happen.** `if x != nil`
  with no `else`, then `{"ok":true}` — `feedback_fix` with no task manager and
  `launch-feedback` with no DataChannel both do this, and both surfaces show a
  *success alert on a no-op*.
- **Escalate to a coding agent only when there is no deterministic fixer.** "Fix
  in Yaver" costs an LLM run; `POST /install/flutter` costs one command.

**Worked example (2026-07-26, the user's own):** Flutter was not installed. The
agent knew (`exec flutter: executable file not found in $PATH`), the installer
existed and was arch-aware (`flutter_install.go` git-clones on linux/arm64 where
no tarball ships), `POST /install/flutter` worked — and the phone showed
*"Waiting for the dev server to report its address…"*. A had the truth; B
flattened it to prose (`devserver_start_remedy.go:104`); C rendered text with no
button (`apps.tsx:3120`); D was unreachable. The remedy string even said *"use
Install on the preview panel"* — **no such button exists on any surface.** The
contract the user asked for: **say "flutter is not installed", offer an Install
button, start it, and stream the output well.**

**And it must reach EVERY surface** — mobile, web, tvOS, watchOS, Wear OS, car,
glass, Electron, CLI. Today custodian findings, playbook remedies and incidents
render on **web only**; relay repair exists on **two** surfaces; runner OAuth is
**tvOS-only** among native ones; and all three wearable/TV surfaces say *"sign it
in from your phone"* with no way to do it. Native surfaces cannot import
`mobile/src/lib/*`, so a copied classifier drifts by construction — the wake
ladder's percentages already disagree across three copies whose comments claim
they match. **Key off the code, not the copy.**

## Hard safety rules (summarised from CLAUDE.md)

- **Never push or commit without explicit user permission.**
- **Never run `rm -rf` on a computed path without `ls -la` first** — case-insensitive macOS filesystems already cost us a full repo once.
- **Only touch Yaver project resources from this repo.** Do not delete, revoke, stop, snapshot, migrate, or mutate personal machines, private sibling-project resources, generic `ubuntu-*` boxes, storage volumes, or non-Yaver provider state unless the user explicitly identifies that exact resource as part of the Yaver task. Before destructive provider/Convex cleanup, list candidates and verify Yaver-specific labels, names, IDs, subscription links, or `cloudMachines` rows; ask on ambiguity.
- **Never use WebView to load third-party React Native apps** — use the Hermes bundle push path (`/dev/build-native`).
- **Never commit credentials, customer IPs, relay hostnames, or any secret** — the repo is public on GitHub.
- **The relay is MULTI-TENANT — a hostile relay user must never reach another user's box or phone.** The Yaver free relay (and Relay Pro) are shared by many unrelated users, and the code is open source, so security rests on KEYS, not secret request shapes. Invariants (see `docs/architecture/ROBUST_TRANSPORT_SSH_QUIC.md` §4d): the relay is pass-through + same-owner/access-graph-scoped (forwards ciphertext, holds no keys, authorizes nothing); the box does **public-key-only** device auth against its own `# yaver-managed` set (a compromised relay still can't get in — it has no key); SSH/reverse-SSH channels are forced-command cages (no shell/pty/forward); Free vs Pro is **not** a security boundary. Any transport/relay/mesh change that lets tenant A reach tenant B, or trusts the relay/tier to authorize, is a security bug.
- **No PRIVATE data in the codebase — ever; PUBLIC key material is fine.** Yaver is public open source (a hacker reads every tracked file). Private keys, certs *with* a private key, tokens, passwords, provider/relay credentials, signing keys → **GitHub Actions secrets / Convex env / encrypted `yaver vault` / Secure Enclave** — never tracked. *Public* keys, *public* certs, mesh/relay **public** identities, host-key **fingerprints to pin** MAY live in code (they only let you *verify*, not forge). Test before committing any key/cert/secret: *"if an attacker reads these exact bytes from the public repo, can they get in or forge?"* — yes → secret store; no → code is fine.
- **Never deploy mobile / publish npm / push a tag without confirming with the user first.**
- **Yaver is not single-user. Never hardcode a path, username, or home directory** — and never let the daemon's CWD stand in for a missing one. A remote box can be any OS, any user, any layout. Resolve at runtime (`os.UserHomeDir()`, `filepath.Abs`, explicit config); a literal `/Users/<name>` or `/home/<name>` outside of a deliberately-fixed system path (`/home/linuxbrew`, a container tenant root) is a bug. On 2026-07-20 `workDir` defaulted to `"."`, which was the agent's CWD — the user's HOME — so every `POST /tasks` recursively classified the entire home tree and never returned. The phone reported the machine unreachable while it was idle and healthy. See `desktop/agent/task_placement_scan_bounds_test.go`.
- **Never put advisory work in the critical path of the operation it annotates.** Placement labels, project classification, telemetry and metrics must be bounded by wall-clock and must degrade to empty, never block. Depth limits are not bounds — breadth defeats them; only a deadline bounds wall-clock.
- **LESS IS MORE — every UI/UX surface earns its pixels or it's cut.** A control surface's default failure mode is accretion (one more chip / status / banner) until the thing the user came for is buried. On every surface (mobile, web, tvOS, watch, car, glass, CLI): show the answer, not the inventory (when the preferred/primary choice is healthy, don't also render every alternative's status — surface alternatives only when the primary needs attention); one primary action per view (diagnostics go behind a "⋯"); progressive disclosure over walls of state (detail lives one tap deeper); advisory never outranks the route, in pixels *or* vertical space; a quiet honest status ("Relay · 301ms", "Preferred: Codex ✓") beats a grid of chips. Removing a widget that only restated something already shown is a feature. Worked example 2026-07-28: the device card repeated all three coding-agents' statuses under "Preferred" — now the status row shows only when the preferred runner isn't ready (`web/components/dashboard/DevicesView.tsx`).

## Every incident must leave the product harder than it found it

Fixing the symptom is half the job. Whenever you debug a real failure — yours, a user's, or a past session's — ask **"what would have told me this in ten seconds instead of an hour?"** and build that answer into Yaver before you call the task done. This is the deliverable, not cleanup.

1. **Encode the diagnosis where the agent already looks** — a `doctor` probe (`desktop/agent/doctor_*.go`), an ops verb, a deploy preflight. If a check existed and was GREEN during the incident, it's a *false green*: fix that check, don't add a second one next to it.
2. **Probe the real capability, never the proxy.** The recurring bug class here is "the inventory says yes, the operation says no" — a certificate that is present but cannot sign, a tool on PATH that is a stub, a device marked `online` that is unreachable, a deploy key that resolves to a deleted file. If the only way to know is to attempt the operation, attempt it.
3. **Put the *why* in the error text.** Name the specific fix; "check your configuration" is worthless. Vague errors cost whole sessions — `errSecInternalComponent` (2026-07-19) reads as "keychain locked, need the login password", which was wrong, and the wrong reading burned a session before anyone tested it.
4. **Ship it to every surface** — Go agent, MCP verb, CLI, web, mobile. A diagnosis only the CLI can see does not exist for a user on their phone. See "Cross-surface parity" in [`CLAUDE.md`](CLAUDE.md).
5. **Self-heal only when the repair is unambiguous and idempotent.** Unlocking a keychain the operator explicitly configured: yes. Guessing passwords or mutating Apple account state: no.
6. **Write the postmortem into the code.** `desktop/agent/doctor_build_deep.go` and `doctor_build_signing.go` are the model — every bullet in their file-top comments is a real incident, stated as the false green it produced.

Every other rule, convention, and subsystem detail is in [`CLAUDE.md`](CLAUDE.md). When it disagrees with the code you're looking at, the code wins.
