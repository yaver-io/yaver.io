# Yaver.io — Claude Code Project Guide

## Read This First

**Markdown files drift. Code is the source of truth.** This file,
`docs/architecture/AI_ARCH.md`, `docs/architecture/REMOTE_WORKER.md`, every
other `*.md` — accurate when written, stale by the next handler rename. Before
acting on any `.md` claim:

1. **grep the actual code.** Routes referenced in docs may exist as functions
   without ever being wired to the mux (we shipped `yaver diagnose` like that
   in 1.99.33).
2. **Verify versions.** `yaver --version` (binary on PATH) vs `/info.version`
   (running daemon) vs `git log -- <file>`. If they disagree, the doc matches
   none of them.
3. **Re-read the file on disk, not in the doc.** Other threads bump constants
   in parallel.
4. **When the doc and the code disagree, the doc is the bug.** Fix it in the
   same change.

`docs/architecture/AI_ARCH.md` is the runtime-architecture reference for auth,
bootstrap, relay, mobile discovery, and remote-recovery — read it before
changing those.

## Hard rules

- **THE SNOWBALL PRINCIPLE — fix the PRODUCT, never the machine.** This is
  Yaver's development philosophy and it outranks finishing the immediate task.
  When something is stuck — a box, a phone, a build, a variable, a piece of
  state — the tempting move is to unstick *that instance* and move on. Don't.
  Mutating one machine back to health teaches the product nothing and
  guarantees the next user meets the same wall with no more help than you had.
  Every stuck state is a product requirement you have not written yet.
  The order is always:
  1. **Resolve it first** — get the user unblocked, on the real box, now.
  2. **Then ask: why did the product let this happen, and why didn't it say
     so?** Almost always the answer is that something reported success, or
     reported nothing, while the operation was impossible.
  3. **Land the change that makes it impossible or self-evident next time** —
     in the Go agent, the mobile app, the relay, the CLI, the web, and the
     wiring between them. Not in a runbook, not in a doc, not in your memory.
  4. **Prove the guard works** by breaking it: disable the fix and watch the
     test fail. A guard you have not seen fail is a guess.
  Worked example from 2026-07-24: a Mac mini looked "online" to the phone while
  its relay said 502, Projects said "No projects yet", and every deployed fix
  seemed to do nothing. Re-authing that one box would have "fixed" it in a
  minute and taught the product nothing. The real deliverables were: `serve`
  now logs one guaranteed line before anything that can fail (a silent serve is
  unfalsifiable), bootstrap mode announces that it is NOT serving and names the
  command that fixes it, the discovery walk is bounded from OUTSIDE the walk
  (a deadline you can only check inside a callback is not a bound), and every
  subprocess got a timeout plus `WaitDelay` (a context kill does not free you
  while a grandchild holds the pipe). Those ship to every user; the re-auth
  helped exactly one machine.
  **Applies to the whole stack, equally.** A fix that lands in one of two
  browser-preview implementations is not landed — the same drift shipped a
  broken heartbeat, dropped SSE frames, and a dead shake gesture in `apps.tsx`
  while `DevPreview.tsx` was fine. Cross-surface parity (below) is the same
  rule wearing a different hat.
  **The UI is part of the product you must harden.** A user who cannot see what
  the box is doing feels the same way an operator does staring at a silent
  `serve` — unsure whether it is working or hung, and that uncertainty IS a
  defect. Every wait the product imposes must narrate itself: what is running,
  where (the command + directory), how long it has been going, when it last
  made progress, and — when something is wrong — what to do about it, in plain
  language, on the surface the user is actually looking at. A spinner with no
  elapsed time, a preview that sits blank while the box compiles, a "connecting"
  that never says why: each is the customer-facing shape of "the inventory says
  yes while the operation says no". Fix the silence in the UI with the same
  seriousness as the silence in a log. This session's heartbeat line
  ("2:14 elapsed · last output 3s ago"), the streamed compile logs, and the
  named-cause error panels are that rule in practice.
  **No surprise re-render while the user is watching or typing.** Preview
  refreshes are disruptive: they steal focus, reset scroll/state, and can make
  the whole app feel frozen. Runner/MCP events such as
  `runtime_render_requested` are render *recommendations*, not permission.
  **Auto-render Vibing mode defaults off**: after a visible UI change, show a
  compact `Render updates` action beside the response and leave the last good
  surface alone. When the account setting is on, Yaver may act on one
  structured agent recommendation after `completed`/`review`; mere completion
  never renders. A whole-message `reload`/`re-render`/`refresh the preview`
  command or an explicit Fast/Full Reload tap always queues one user-requested
  render and executes once no task is coding. Keep queued coding prompts in
  order. First open may show a loading
  surface; reloads must not replace a working preview with a branded
  placeholder. Render/reload is atomic: if one reload is in flight, every other
  trigger is ignored or coalesced until it finishes, and a new coding turn must
  not start on that surface mid-reload. This is cross-surface policy, not a web
  preference: web, mobile, tablet, tvOS, watchOS, Wear OS, car, AR/VR, and CLI
  companion surfaces must all follow the same queue → quiet status → one final
  render pattern.
  A named command such as `reload sfmg` pins `projectName` through
  `/dev/reload-app` into `/dev/build-native`; it must not refresh an unrelated
  active browser preview merely because that is the box's current dev lane.
  In RN-web the same command pins the project into a `web-js-bundle` build and
  refreshes the browser surface; native and browser lanes share the intent but
  execute the artifact their surface can actually consume.
- **LESS IS MORE — every UI/UX surface earns its pixels or it's cut.** Yaver
  shows a lot: devices, runners, transports, previews, statuses, incidents. The
  default failure mode of a control surface is *accretion* — one more chip, one
  more status line, one more banner — until the thing the user actually came for
  is buried in metadata. Fight it on every surface (mobile, web, tvOS, watch,
  car, glass, CLI). The rule of thumb:
  1. **Show the answer, not the inventory.** When the primary/preferred choice
     is healthy, don't also render every alternative's status — the one good
     line says it all. Surface the alternatives exactly when they help: when the
     primary needs attention. (Worked example 2026-07-28: the device card
     repeated all three coding-agents' statuses under the "Preferred" chip; now
     the status row appears only when the preferred runner isn't ready —
     `DevicesView.tsx`.)
  2. **One primary action per view.** Diagnostics (ping/ssh/copy) belong behind
     a "⋯", not competing with the thing you came to do — the same reasoning as
     the single-CTA device card.
  3. **Progressive disclosure over walls of state.** Detail lives one tap deeper
     (a modal, an expand), never crowding the summary. A dense diagnostics wall
     that squeezes the action lane to zero height is a *worse* bug than missing
     information (build 482, `40eec39ef`).
  4. **Advisory never outranks the route** — not in pixels, not in vertical
     space, not in reading order. This is the same law as "advisory work must
     never sit in the critical path": here it's the *visual* critical path.
  5. **A quiet, honest status beats a loud, busy one.** One line that says the
     real state ("Relay · 301ms", "Preferred: Codex ✓") is worth more than a
     grid of chips. Removing a widget that only restated something already shown
     is a feature. When unsure whether an element earns its place, cut it and
     see if anyone misses it.
- **Every incident must leave the product harder than it found it.** When you
  debug a real failure — yours, a user's, or a past session's — fixing the
  immediate symptom is only half the job. Before you call it done, ask: *"what
  would have told me this in ten seconds instead of an hour?"* and then build
  that into Yaver. This is not optional cleanup; it is the deliverable. The
  standing pattern:
  1. **Encode the diagnosis where the agent already looks.** A new `doctor`
     probe (`desktop/agent/doctor_*.go`), an ops verb, a preflight in the
     deploy script. If the check exists but was GREEN during the incident, it
     was a *false green* — fix the check, don't add a second one beside it.
  2. **Probe the real capability, never the proxy.** The whole class of bugs
     here is "the inventory says yes, the operation says no": a certificate is
     present but cannot sign, a tool is on PATH but is a stub, a device is
     `online` but unreachable, a deploy key resolves but the file is gone. If
     you can only learn the truth by attempting the operation, attempt it.
  3. **Carry the *why* into the error text.** The remedy string should name the
     specific fix, not "check your configuration". The cost of a vague error is
     measured in whole sessions — see `errSecInternalComponent` (2026-07-19),
     where the obvious reading ("keychain locked, need the login password") was
     wrong in a way that wasted a session and would have wasted another.
  4. **Ship it to every surface.** Go agent + MCP verb + CLI + web + mobile.
     A diagnosis only the CLI can see doesn't exist for a user on their phone.
     See "Cross-surface parity" below.
  5. **Self-heal when the fix is unambiguous and idempotent**; ask when it
     isn't. Unlocking a keychain the operator explicitly configured is a
     no-brainer. Guessing at passwords or mutating account state is not.
  6. **Write the postmortem into the code, not just the commit message.** The
     file-top comment blocks in `doctor_build_deep.go` and
     `doctor_build_signing.go` are the model: each bullet is a real incident,
     stated as the false green it produced.
- **Never push or commit without explicit user permission.**
- **Committed and pushed work is IMMUTABLE unless the user says otherwise.**
  Once a change is committed — and certainly once pushed — nobody reverts it,
  force-pushes over it, `git checkout --`s it away, or drops it in a rebase
  without the user explicitly asking. This is not a style preference: work has
  already been lost this way. On 2026-07-18 a session's `backend/convex/`
  changes were reverted off disk *after they had been deployed to Convex prod*,
  leaving prod carrying a schema the source tree no longer contained — the next
  deploy from that tree would have dropped live fields. If you believe a
  committed change is wrong, say so and propose a NEW commit that fixes it.
  Never quietly un-land someone's work.
- **Many sessions are always running. Assume concurrency; isolate for it.**
  Multiple Claude/autorun threads share this machine and this repo at all times.
  - **Work on a branch, then merge.** This supersedes the older "always work
    directly on main" habit. A branch per unit of work (`wake-ux`, `forge-seam`)
    keeps concurrent threads from interleaving half-finished edits in one tree.
    Finish → rebase onto latest `main` → merge → push. Conflicts get resolved,
    never bulldozed (see the immutability rule above).
  - **`git commit -- <paths>` ALWAYS.** Never `-a`, never `add -A`. The index is
    shared and goes stale between two consecutive commands, so a bare commit
    sweeps a sibling's staged files. Pathspec commits are the only atomic form.
  - **Autorun gets its own clone, never a shared checkout.** A dirty shared tree
    kills a run at iteration 0. Clone to `~/Workspace/yaver-<topic>-autorun`,
    give it its own branch, and pass task paths as ABSOLUTE paths.
  - After any deploy from a shared checkout, `grep` the source for a symbol you
    added to confirm it is still there. A green deploy is not evidence the code
    survived the next five minutes.
- **Hetzner = metered, NEVER monthly. Never leave a server running.** Hetzner
  bills servers hourly (metered) up to the monthly cap, and bills even
  **stopped** servers — only **deleting** a server stops the meter. NEVER launch
  a Hetzner box and leave it running (or merely stopped) to accrue the monthly
  charge. Every provisioned box MUST be **scale-to-zero**: preserve state
  first, then **DELETE** the server when idle. The preferred Cloud Workspace
  path keeps mutable state on a Hetzner Volume and wakes from a slim base image;
  snapshot+delete remains the legacy/hosted recovery fallback. You pay only for
  active hours + cheap parked storage. No always-on servers for beta/dev/test.
  Before provisioning, confirm the volume-backed or snapshot-backed teardown
  path exists; delete every test/builder box the moment it's done. The user's
  standing directive (2026-06-20): "always pay metered, never ever launch
  something Hetzner will charge monthly automatically."
- **Repo/resource boundary: touch only Yaver project resources.** When working
  in this `yaver.io` repo, agents may modify only files, cloud resources,
  Convex rows, Hetzner servers/snapshots/volumes, LemonSqueezy state, and other
  external resources that are clearly owned by the Yaver project. Do **not**
  delete, revoke, rename, stop, snapshot, migrate, or otherwise mutate personal
  machines, private sibling-project resources, generic `ubuntu-*` boxes, or
  storage volumes unless the user explicitly says that exact resource belongs
  to the Yaver task and should be changed. Before any destructive provider or
  Convex cleanup, list the candidates and verify their Yaver-specific labels,
  names, IDs, subscription links, or `cloudMachines` rows. Ambiguity means stop
  and ask.
- **Do no harm to third parties — it is not our purpose.** Yaver exists to lower
  the *user's own* dev/ops cost, not to attack, scrape-abuse, or burden anyone
  else's infrastructure. A datacenter IP (Hetzner/AWS/…) that hammers a third
  party gets the whole account suspended — and it's wrong regardless. Concretely:
  1. **No scanning/attacking hosts you don't own.** `nmap`, `port_scan`,
     `arp_scan`, brute-force, floods, probes are for the user's OWN LAN/hosts or
     explicitly authorized targets only. Never point them at a third party.
  2. **Collect public data respectfully.** Don't spoof a browser
     `User-Agent`/`Origin`/`Referer` to defeat bot detection; don't bypass
     `robots.txt`, rate limits, WAFs, or geo/IP blocks. Prefer official/keyless
     APIs. **Back off on 403/429/451 and stop** — a block is a "no", not a
     puzzle. The collection layer records blocks as findings; it must never
     rotate IPs or impersonate to route around them.
  3. **Use the right resource, not the loudest one.** Move third-party reads off
     a flagged datacenter IP onto the user's *own* devices/residential vantages
     (`runtime: mobile_user_present`), distributed friend-roster runners, or an
     official API — that's what `collection_plan` runtime selection and the Task
     Packages phone-runner are for. Don't sustain a 24/7 scrape loop from one
     cloud box.
  4. **Peer-egress lends your OWN machines' egress only** — default-off,
     same-user, RFC1918-blocked, never an open relay, never ban/geo evasion.
  5. **If you build a loop that hits an external service:** identify honestly,
     cap concurrency, jitter + back off, make it killable, and stop on a block.
     When in doubt, don't. See `desktop/agent/access_policy.go` (Policy Guard)
     and `egress_proxy.go` (anti-pivot). Mirrored in `../yaver-bet/CLAUDE.md`.
- **Streaming is a neutral tool — like OBS.** Yaver *helps you stream* whatever
  source you point it at (capture card / satellite / set-top box / console /
  camera / screen) to devices signed into your **own account** — never public
  by default. Yaver is **content-agnostic**: it does
  not inspect, classify, block, or police what's on the wire. If a source is
  dark or HDCP-blanks itself, Yaver streams that as-is (a terse diagnostic hint
  is fine; do **not** litter the code/UI with warnings). **What you capture and
  stream, and the right to do so, is the user's responsibility** — exactly as
  with OBS. The one line in *our* code: Yaver adds **no** DRM/HDCP
  circumvention (no stripper); it passes through exactly what the hardware
  gives. Note: Yaver's aim is utility, **not** privacy — don't sell or gate
  streaming features on a privacy promise (the Convex privacy contract below is
  a separate, data-storage constraint and still holds).
- **Destructive paths**: before `rm -rf`, `git clean -fdx`, `find -delete`,
  `mv` over an existing dir:
  1. `ls -la <path>` first; show what's about to be deleted.
  2. Absolute paths with exact case. macOS is case-insensitive — `~/workspace`
     matches `~/Workspace`. This already wiped a repo once.
  3. Confirm before deleting under `$HOME`, a git repo root, any path computed
     from a variable, or any path transcribed from a user message.
  4. Treat `rm -rf` on anything you didn't create this session like
     `git push --force` to main.
- **Cloudflare deploy size guard**: `web/` must stay under 15 MB (raised from
  10 MB in ddd5868d — demo videos push it over; enforced identically in
  `scripts/deploy-web.sh` and `release-web.yml`). If you add video, compress
  first (`ffmpeg -crf 32 -vf scale=720 -an`) or host external.
- **Deploys and cloud tools cost money — coalesce, never spray.** Vercel billed
  us harshly per build/bandwidth; that is why `web/` runs on Cloudflare Workers
  (`@opennextjs/cloudflare` + `wrangler`). **Cloudflare is cheaper, not cheap.**
  Every deploy is metered somewhere — Workers requests, Convex function calls,
  GitHub Actions minutes, and TestFlight's hard **~15–20 uploads/app/day** cap
  (which has no rollback: a bad build can only be superseded). Treat a deploy
  like a Hetzner hour, not like a save:
  1. **One deploy per converged change — never one per iteration.** When several
     autorun loops or queued tasks (p0…p9) touch the same target, they
     `commit` + `push` only; the **last** one deploys, once, after the whole
     queue converges. N loops must not mean N deploys.
  2. **Never deploy to "check" something.** Use the local dev server, `wrangler
     dev`, or a preview. A deploy is not a build step.
  3. **Be quota-aware before you are quota-blind.** Read the remaining budget
     (TestFlight uploads today, CI minutes) and park the run rather than burn
     it. Out-of-quota is a state that needs a human, not a retry.
  4. Cost-awareness is a product requirement, not just a house rule — it is the
     whole "lower dev opex" wedge. Cloud tool usage and deploys should report
     what they cost (`remote_cost`, `switch_cost` are the existing seams).
- **CLOSED-LOOP TESTS: THE MOBILE SURFACE IS A DEVICE CONTEXT, NEVER A RESIZED
  DESKTOP ONE.** Any browser-driven test of the mobile app MUST create a NEW
  Playwright context with the full device descriptor
  (`browser.newContext({ ...devices["iPhone 15 Pro"] })`) and assert the
  viewport it actually got. This is not a nicety — `isMobile`, `hasTouch`,
  `deviceScaleFactor` and the user agent are **context** properties that
  `page.setViewportSize()` cannot change, so shrinking a desktop context to
  393px yields a *narrow desktop browser*, and RN-web renders a **different
  component tree** for it than for a phone. A green result there says nothing
  about the app the user holds — it is the exact class of false confidence the
  suite exists to prevent, reproduced inside the suite.
  1. Use `web/lib/surfaceViewports.ts` (`profileFor(surface)` +
     `viewportMatchesSurface`) — one table, not a literal per spec.
  2. **Assert the viewport inside the arc**, so a context that silently came
     back desktop-shaped fails loudly instead of passing quietly.
  3. The mobile arc drives **RN-web at `MOBILE_WEB_URL`**, and SKIPS without
     one. Never substitute the web dashboard at a small size: the two share
     neither transport ladder, auth storage key
     (`yaver.secure.yaver_auth_token` vs `yaver_auth_token`), nor render path.
  4. Same law for every future surface (tablet, glass, TV): the profile comes
     from the table, and the test proves it got it.
  `e2e/tests/vibe-color-loop.spec.ts` is the reference implementation.
- **A web/dev fix must NEVER regress the shipped native app.** The mobile app
  also runs as RN-web (`expo start`, driven by Playwright at iPhone viewport) —
  that is the only way to automate the REAL app instead of the web dashboard.
  Making it work there is legitimate; doing so by weakening the native path is
  not. The shape that is always safe:
  1. **Branch on `Platform.OS === "web"`, or use a `.web.ts` file.** Never edit
     the native path to accommodate the browser. `secureStoreCompat.ts` is the
     model: web gets localStorage, native delegates to hardware-backed
     `expo-secure-store` bit-for-bit unchanged.
  2. **State the downgrade in the file, at the top.** localStorage is not the
     Keychain. Whoever reads it next must not have to infer that.
  3. **`.web.ts` stubs DRIFT, and drift crashes at RUNTIME.** A `foo.ts` /
     `foo.web.ts` pair is two independent classes chosen by Metro's platform
     extension, NOT two implementations of one interface — so a method added to
     the native twin is invisible to `tsc` on web and throws
     `x is not a function` in a timer, on the surface least likely to be tested.
     This shipped twice on 2026-07-25: `ExpoSecureStore.setValueWithKeyAsync is
     not a function` (correct login, exploded while storing the token) and
     `beaconListener.getBootstrapDevices is not a function` (permanent
     "Reconnecting (0/5)…"). Both read as broken products, not missing stubs.
     **Every such pair needs a parity test** — `beaconParity.test.ts` reads both
     sources and asserts the web surface covers the native one.
  4. **Prove the guard by breaking it.** Rename the method, watch the test fail,
     put it back. A parity test nobody has seen fail is a guess.
- **Browser transport contract — one channel, industry-standard, additive only.**
  The mobile app also runs as RN-web so Chromium/Playwright can drive the REAL
  app (see the parity rule above). A browser cannot do what a phone does, and the
  gap is absolute, not flaky: **no UDP** (so no LAN beacon) and **no raw QUIC**
  (so no relay dial). On 2026-07-25 that produced "Transport pending · Agent
  status unavailable" forever in Chromium while the same account showed
  "Relay · 301ms" on a real iPhone — the app waiting on an event that could never
  arrive, and calling it "pending".
  1. **One lane, standard protocols.** Browser talks to the agent over plain
     **HTTP/1.1+ to :18080** for every request/response API, and **SSE** for
     server→client streams (the app already ships `sseClient` on XHR because RN
     `fetch` cannot stream a body). WebSocket only where a channel is genuinely
     bidirectional. No bespoke framing, no second protocol: if a native API
     exists, the browser reaches the SAME endpoint.
  2. **Convex is unchanged and shared.** Identity, device rows and peer discovery
     already come from Convex over HTTPS, which browsers speak natively — that is
     how the browser resolves WHICH host to dial without a beacon. Same routes,
     same bearer token, same privacy contract; nothing Convex-side is
     browser-specific.
  3. **Security is the SAME boundary, never a weaker one.** Identical bearer
     token, identical validation, identical `# yaver-managed` key rules. The
     agent already echoes `Access-Control-Allow-Origin` for the caller and passes
     preflight with `Authorization` — verified live 2026-07-25 — so **no server
     relaxation is needed and none may be added**. Never widen CORS to `*` on an
     authenticated agent route, never put a token in a URL or query string, never
     let the browser path skip a check the native path performs. The relay stays
     pass-through and authorizes nothing.
  4. **Additive only — the phone channel is untouchable.** New code goes in a
     `.web.ts` sibling or behind the capability table in
     `mobile/src/lib/platformTransport.ts`. Never edit the native connect path to
     accommodate a browser. If no native file changed, no native behaviour can
     change — that is the property to preserve and to state in the commit.
  5. **An impossible transport must SAY SO.** `explainNoTransport()` returns the
     sentence to render instead of a spinner, and `null` while something is still
     worth waiting for. A connect path that can report "waiting" with no possible
     transport regenerates the original bug.
  6. **Dual implementation implies a parity test.** Every `foo.ts`/`foo.web.ts`
     pair gets one (`beaconParity.test.ts` is the model) — Metro picks between two
     INDEPENDENT classes, so drift is invisible to `tsc` and crashes at runtime in
     a timer. Prove it by breaking it.
  7. **This is how Yaver gets developed: closed loop.** Browser channel → the
     Playwright matrix (`e2e/tests/mobile-app-lane-matrix.spec.ts`) drives the
     real app against a real box → PIXELS / NAMED / **SILENT**, where SILENT is
     the only failing verdict. A change that cannot be observed end-to-end from
     the app the user actually holds is not finished.
- **CONNECTIVITY ROBUSTNESS — no unbounded await, no un-releasable guard, ever.**
  A connection path is a ladder of network calls behind a concurrency guard, and
  it must be *impossible* to wedge. Two invariants, both non-negotiable, on every
  surface and every transport (direct/LAN, Tailscale/tailnet, Cloudflare tunnel,
  the free relay, mesh):
  1. **Every await in a connect/poll/health path is wall-clock bounded.** The
     trap is RN-specific and lethal: **`fetch` AND `NetInfo.fetch()` have NO
     default timeout** — they hang forever. On 2026-07-28 `NetInfo.fetch()` at
     the top of the mobile ladder hung in the iOS simulator and pinned the pill
     at "Connecting" for 30+ minutes while the relay was up and Retry no-op'd.
     Native surfaces are immune *by construction* (URLSession
     `timeoutIntervalForRequest`, OkHttp `connect/readTimeout`) — but the
     invariant is universal. Route every network/native call through
     `withDeadline` / `fetchWithTimeout` (`mobile/src/lib/connectGuard.ts`,
     `quic.ts`). The ONE sanctioned exception is a genuine long-lived stream
     (`text/event-stream` SSE) torn down by an `AbortController` — never a
     request/response call.
  2. **A concurrency guard is releasable by a NEWER attempt, never only by the
     attempt that took it.** An attempt-owned boolean (`_connectingInProgress`)
     turns one hung await into a *permanent* stuck state: every retry and the
     Retry button short-circuit at "already in progress." Use
     `ConnectAttemptGuard` — a monotonic attempt id (only the latest clears the
     guard) plus a wedge deadline that lets a fresh attempt ABANDON a holder hung
     past the sum of all bounded legs. "Connecting" can then never be permanent.
  Both live as pure, dependency-free primitives in `connectGuard.ts` so the
  logic that SHIPS is the logic that's TESTED (`connectGuard.test.ts`, run by
  `npx tsx`) — with negative-control cases that reproduce the old wedge (a bare
  await never completes; a never-wedging guard stays stuck forever). Prove any
  guard by breaking it. When you add a transport leg or a new connect entry
  point, it inherits both invariants or it is a connectivity bug even if it
  "works" on your fast network. Incident + rationale:
  `memory/project_netinfo_wedges_connect_guard`.
- **NEVER `scp` a locally-built agent binary onto a macOS box.** macOS kills an
  unsigned binary under launchd with `last exit reason = OS_REASON_CODESIGNING`,
  and because launchd keeps "state = spawn scheduled" the box looks like it is
  starting rather than failing — the agent simply never answers, the phone goes
  offline, and nothing in `yaver serve`'s own log explains it (the process is
  killed before it writes a line). Hit again 2026-07-25 while hot-swapping a
  `go build` output onto the Mac mini to test a fix.
  1. **Preferred: ship through the release path** (`npm install -g yaver-cli@…`
     or the versioned `~/.yaver/bin/<version>/<platform>/` layout the installer
     writes). That is the only path that yields a signed + notarized binary.
  2. **If you MUST hot-swap for a test, ad-hoc sign it in the same breath:**
     ```bash
     codesign --force -s - ~/.yaver/bin/current/darwin-arm64/yaver
     launchctl kickstart -k gui/$(id -u)/io.yaver.agent
     ```
     Unsigned is not "unsigned but runs" on macOS — it is *killed*.
  3. **`nohup … &` over SSH does NOT survive the session.** Use
     `launchctl kickstart -k gui/$(id -u)/io.yaver.agent` so launchd owns the
     process; a hand-started agent dies with your shell and takes the user's
     box offline without saying so.
  4. **Verify by asking the agent, not by reading exit codes:** `curl -s -m 20
     localhost:18080/info` must return 200. `pgrep` finding a PID is the
     inventory; the HTTP answer is the operation.
  5. A locally-built binary also reports a STALE `--version` (the real version is
     injected at release time), so `/info.version` will lie about what is
     running — never diagnose from it after a hot-swap.
- **A MISSING TOOLCHAIN IS A PRODUCT REQUIREMENT, NOT A USER ERROR.** When an
  operation needs a tool the box does not have, the answer is never a bare
  "executable file not found". Three obligations, in order:
  1. **NAME IT, on the surface the user is looking at.** Measured 2026-07-26: the
     agent correctly returned `exec flutter: executable file not found in $PATH`
     for e-mobile on a Hetzner box, and the phone showed "Waiting for the dev
     server to report its address…" — a spinner over a fact the agent had
     already stated. A truthful agent plus a client that drops the truth is
     still an unfalsifiable product.
  2. **OFFER THE INSTALL when it is possible, and STREAM IT.** The agent already
     has recipes (`ensureRunnerInstalledStream`, `installNodeBackedCLI` in
     `install_cmd.go` — claude, codex, opencode, node, vercel, convex,
     supabase). A missing tool should render as a button, not a dead end, and
     the download must narrate itself: stdout streamed to the surface with bytes
     and elapsed time. A 2 GB SDK behind a silent spinner is the same defect as
     a silent `serve` — the user cannot tell fetching from hung. Extend the
     recipe table rather than special-casing per call site.
  3. **RESOLVE PER OS/ARCH — and only claim impossible after checking.** The
     trap is answering from the happy path. Flutter publishes **no Linux/arm64
     tarball** (verified against `releases_linux.json`), so a naive installer
     404s on an aarch64 box — but git-clone IS Flutter's supported install for
     that platform, and `flutter_install.go` has done exactly that the whole
     time. So the honest answer there is "yes, installable", not "render
     elsewhere". Getting this backwards in EITHER direction is a defect:
     offering an install that cannot work teaches the user Yaver lies, and
     declaring impossible what the product already supports withholds a working
     capability. Probe per-platform (`flutterStableTarball` → fall back to
     clone), then state the specific truth for THIS machine.
  The generic shape for any capability gap: **state it → offer the fix if the fix
  exists → stream the fix → name the constraint if it does not.** Applies to
  runners, SDKs, simulators, emulators, `adb`, keychains and anything else a
  future lane needs.
- **EVERY FAILURE MUST CARRY A ROUTE TO ITS FIX — four layers, no exceptions.**
  The missing-toolchain rule above is the *first instance* of a general law, not
  a special case. Generalize it to every failure in the product: remote runners,
  reload/preview (Hermes, browser, WebRTC), the feedback SDK, connectivity,
  auth, builds, deploys. Full architecture, with the failure×route matrix and
  every `file:line`, in **`docs/architecture/FAILURE_PLUMBING_ARCHITECTURE.md`**;
  the failure *inventory* is `docs/audits/failure-recovery-audit-2026-07.md`.
  A failure is shipped only when all four layers exist:
  1. **DETECTION — probe the operation, never the inventory.** `commandExists`
     is a proxy; a tool on PATH can be a stub, a cert can be present and unable
     to sign, a device can be `online` and unreachable. If the only way to know
     is to attempt it, attempt it.
  2. **SIGNAL — structured and named, never prose.** A stable code (there are
     already 14 in `desktop/agent/reason_codes.go`) plus typed fields, carried
     on *every* channel the failure can take: the HTTP body, the `/dev/events`
     SSE event, the task event, the incident, the heartbeat. A bare
     `{ok:false, error:"<sentence>"}` forces every surface to invent a regex,
     and the regexes drift — mobile already carries **three** different
     relay-auth matchers, none a superset of the others.
  3. **UI — a named cause the user can actually SEE.** A spinner is a bug. And
     "rendered" is not enough: an unbounded diagnostics wall squeezed the
     action lanes to zero height in build 482 (`40eec39ef`), so the one lane
     the agent offered could not be seen. Advisory content never wins over the
     route — not in pixels, and not in time (a blocking preflight in front of a
     capability that already works is the same defect: see the PTY launch gate).
  4. **ROUTE-TO-FIX — the next tap, in place, streamed.** Not a sentence
     describing a remedy: an invocable `method + path + stream` the surface can
     render as a button, which streams bytes + elapsed and then **returns the
     user to what they were trying to do**.
  Three corollaries, each learned the hard way:
  - **A signal with no consumer is not shipped.** `recoverKind`
    (`devserver_http.go:3958`), `capture_error`
    (`remote_runtime_video_track.go:139`), `RunnerPreflightByID`
    (`runner_preflight.go:35`, called by voice only), `RelaySessionExpiredAt`
    and all 14 `reason_codes.go` values are correct producers that **nothing
    reads**. Land the consumer in the same change, with a test that fails when
    it is removed.
  - **Never report success for an operation that did not happen.** `if x != nil`
    with no `else`, then `{"ok":true}`, is a false green wearing a bow —
    `feedback_fix` with no task manager and `launch-feedback` with no
    DataChannel both do exactly this, and both surfaces show a *success alert
    on a no-op*.
  - **Escalate to a coding agent only when there is no deterministic fixer.**
    "Fix in Yaver" costs an LLM run; `POST /install/flutter` costs one command.
    Spending the former on a class that has the latter is the most expensive
    possible answer to the cheapest possible question.
  **The worked example, verbatim from the user (2026-07-26):** Flutter was not
  installed on the box. The agent knew (`exec flutter: executable file not found
  in $PATH`), the installer existed and was arch-aware (`flutter_install.go`,
  git-clone on linux/arm64 where no tarball ships), `POST /install/flutter`
  worked — and the phone showed *"Waiting for the dev server to report its
  address…"*. Layer A had the truth; layer B flattened it to prose
  (`devserver_start_remedy.go:104`); layer C rendered text with no button
  (`apps.tsx:3120`); layer D was unreachable. The remedy string even said *"use
  Install on the preview panel"* — **there is no such button on any surface.**
  What the user asked for is the contract: **say "flutter is not installed",
  offer an Install button, start it, and stream the output well.**
  **And it must reach EVERY surface** — mobile, web, tvOS, watchOS, Wear OS,
  car, glass, Electron, CLI (see "Cross-surface parity"). Today custodian
  findings, playbook remedies and incidents render on **web only**; relay repair
  exists on **two** surfaces; runner OAuth is **tvOS-only** among native ones;
  and the three wearable/TV surfaces all say *"sign it in from your phone"* with
  no way to do it. Native surfaces cannot import `mobile/src/lib/*`, so a copied
  classifier drifts by construction — the wake ladder's percentages already
  disagree across three copies whose comments claim they match. **Key off the
  code, not the copy.**
- **Never WebView for third-party RN apps.** Use the Hermes-bundle native load
  path (`/dev/build-native` → ExpoReactNativeFactory). WebView is OK for plain
  web content (landing pages, docs).
- **Yaver is not single-user — never hardcode a path, username, or home dir.**
  A remote box can be any OS, any user, any layout. Resolve at runtime
  (`os.UserHomeDir()`, `filepath.Abs`, explicit config). A literal
  `/Users/<name>` or `/home/<name>` outside a deliberately-fixed system path
  (`/home/linuxbrew`, a container tenant root) is a bug. **And never let the
  daemon's CWD stand in for a missing path** — "unspecified" means *unknown*,
  not "use whatever directory I happen to be sitting in". On 2026-07-20
  `workDir` defaulted to `"."`, which was the agent's CWD — the user's HOME —
  so every `POST /tasks` recursively classified the whole home tree and never
  returned. The phone showed "the machine accepted the connection but never
  answered" while the box was idle with three ready runners.
  Guard: `desktop/agent/task_placement_scan_bounds_test.go`.
- **Advisory work must never sit in the critical path of the operation it
  annotates.** Placement labels, project classification, repo metrics and
  telemetry must carry a wall-clock deadline and degrade to empty rather than
  block. A depth limit is not a bound — breadth defeats it; only a deadline
  bounds wall-clock. Same lesson as the `/tasks` payload incident: bounding the
  proxy (rows, depth) never bounds the thing that actually hurts (bytes, time).
- **Never commit credentials, infra IPs, or hostnames.** The repo is public on
  GitHub. Apple keys, Hetzner IPs, npm tokens, Play service-account JSON,
  relay passwords, Tailscale IPs — all gitignored / GH secrets only. If a
  secret was ever committed: rotate it AND `git filter-repo --replace-text`
  before pushing.
- **The relay is MULTI-TENANT — a hostile relay user must NEVER reach another
  user's box or phone.** The Yaver free relay (and Relay Pro) are shared by many
  unrelated users, and the code is open source, so an attacker reads everything.
  **Security therefore rests on KEYS, not on secret request shapes — never add a
  "security" that a source reader defeats.** Non-negotiable invariants (see
  `docs/architecture/ROBUST_TRANSPORT_SSH_QUIC.md` §4d +
  `SECURE_FRICTIONLESS_TRANSPORT_SETUP.md`): (1) the relay is **pass-through +
  access-graph-scoped** — it forwards ciphertext, authorizes nothing, holds no
  device keys, and bridges only within the **same owner/access-graph** (the same
  userID check as the `already registered` eviction in `relay/server.go`); (2) a
  box authenticates the **client's device key, public-key ONLY**, against its own
  `# yaver-managed` set — a stranger's key isn't there, so the handshake fails,
  and a *fully compromised relay still can't get in* (it has no key; the phone key
  lives in the Secure Enclave); (3) SSH / reverse-SSH channels are **forced-command
  cages** (no shell/pty/forward/subsystem — `ssh_control_server.go`,
  `ssh_session_cmd.go`); (4) **Free vs Pro is NOT a security boundary** — identical
  auth, Pro only buys capacity. Any new transport/relay/mesh code must preserve all
  four; a change that lets tenant A's traffic reach tenant B's agent, or that
  trusts the relay/tier to authorize, is a security bug even if it "works".
- **No PRIVATE data in the codebase — ever. PUBLIC key material may live in code;
  private material may not.** Yaver is public open source: a hacker reads every
  tracked file. So the split is cryptographic, not vibes-based:
  - **Private → secret store, never tracked.** Private keys, certificates *with*
    their private key, session/auth tokens, passwords, provider API credentials,
    relay passwords, signing keys → **GitHub Actions secrets**, **Convex env**,
    or the **local encrypted `yaver vault` / `~/.yaver` 0600 store** (or the
    device **Secure Enclave / Keychain**). If leaking it lets someone
    impersonate, decrypt, or sign, it is private.
  - **Public → codebase is fine.** *Public* keys, *public* certificates, relay/
    mesh **public** identities, host-key **fingerprints to pin** — these are
    verifiers, safe to ship in the repo (that is the whole point of public-key
    crypto). Shipping a public key is not a leak.
  - The test before committing any key/cert/credential: *"if an attacker reads
    this exact bytes from the public repo, can they get in or forge?"* Yes →
    secret store. No (it only lets them *verify*) → code is acceptable.
  This generalizes the "never commit credentials" rule to the open-source reality
  and to the new SSH/mesh key material.
- **Public docs use machine aliases**, not real infra labels. Prefer
  `primary`, `selected-machine`, `your-box`, `example-host` in examples.
- **Hetzner test box (`yaver-test-ephemeral`)** is disposable. Its IP, SSH
  key material, `hcloud` token, real device IDs never go in tracked files.
  Refer to it in code/docs only as `yaver-test-ephemeral`.
- **Local deploy first, CI second.** Every deploy that can run on this Mac
  should run on this Mac. Yaver's wedge is "lower dev opex" — defaulting to
  GitHub Actions burns CI minutes you don't need to spend, slows down
  iteration, and re-introduces the SaaS roundtrip you're trying to remove.
  Use CI only when the deploy genuinely cannot work locally (a Linux-only
  toolchain that isn't on macOS, a runner that needs a secret you don't
  have on this machine, etc.) — and when you do, say so explicitly.

  **One deploy path:** from this repo, humans, agents, and MCP should start with
  `./deploy/deploy.sh <target> [options]`. That is the canonical front door.
  It delegates to the maintained vault-aware scripts and `yaver deploy`
  internals, but centralizes target names, owner/permission checks, and dry-run
  discovery so future sessions do not choose a different path.

  Do not call `scripts/deploy-web.sh`, `scripts/deploy-testflight.sh`,
  `scripts/deploy-playstore.sh`, `scripts/deploy-convex.sh`, or `npm publish`
  directly unless you are editing/testing those scripts. Use:
  `./deploy/deploy.sh all`, `backend`, `cloudflare`, `ios`, `android`, `npm`,
  `feedback-sdk`, or `mcp`.

  Deploy is owner-only. MCP deploy tools are hidden and denied for non-owner
  accounts, and `deploy/deploy.sh` refuses group/other-writable repo or script
  paths before touching deploy credentials. Never weaken that gate to make a
  test pass.

  | Target | Local command (preferred) | CI fallback |
  |---|---|---|
  | Full Yaver stack | `./deploy/deploy.sh all` | none; run locally unless explicitly told otherwise |
  | npm (`yaver-cli`) | `./deploy/deploy.sh npm` | `release-cli.yml` after the local command tags/pushes |
  | React Native feedback SDK | `./deploy/deploy.sh feedback-sdk` | `release-feedback-sdk.yml` when local npm owner credentials are absent |
  | TestFlight (iOS) | `./deploy/deploy.sh ios` | local-only by design |
  | Google Play internal | `./deploy/deploy.sh android` | `release-mobile.yml` (android job) |
  | Convex backend | `./deploy/deploy.sh backend` | `release-backend.yml` (manual; production environment) |
  | Cloudflare web | `./deploy/deploy.sh cloudflare` | `release-web.yml` |
  | MCP registry metadata | `./deploy/deploy.sh mcp` | release CLI workflow when publishing npm |

  When the user asks to ship, run the local command — don't push a tag and
  let CI do it unless the user explicitly says "use CI". If a local deploy
  fails for a reason that CI would also hit, fix the root cause; don't
  switch to CI as a workaround.

## Distribution — npm only

As of 1.99.124, **`npm install -g yaver-cli`** is the **only** supported install
path on every platform: macOS (Apple Silicon + Intel), Linux (x64 + arm64,
including Raspberry Pi / ARM cloud), and Windows via WSL2.

The npm package detects the platform and downloads the matching, signed +
notarized agent binary into `~/.yaver/bin/<version>/<platform>/yaver`. macOS
binaries are Developer ID + hardened runtime + notarized — Gatekeeper passes
on first run.

The previous distribution paths — apt repo, Homebrew tap, Scoop bucket, AUR,
Winget, Chocolatey, raw tarballs, install.sh, Docker image — are **all
removed**. Their repos (`kivanccakmak/{homebrew,scoop,aur,apt}-yaver`) are
archived read-only with a deprecation README.

`release-cli.yml` only does: validate → publish-npm → publish-mcp-registry →
build (with darwin sign+notarize) → release. Don't add deb/rpm/dmg/brew/scoop
steps back without an explicit ADR.

```bash
# install (any supported platform)
npm install -g yaver-cli

# update
npm install -g yaver-cli@latest

# headless (Pi / VPS / SSH-only) — short code + URL, sign in from any browser
yaver auth --headless
```

## Repository

- **Source of truth**: `github.com/yaver-io/yaver.io` (open source). Owned by
  the `yaver-io` org since 2026-07-17 — it was `kivanccakmak/yaver.io` before,
  and GitHub still redirects the old URL, so a stale remote keeps working and
  will quietly hide its own staleness. Only one remote here — `origin`, over
  **SSH** (`git@github.com:yaver-io/yaver.io.git`). `branch.main.remote=origin`,
  so plain `git push` works. No GitLab mirror.
- **Tags trigger releases**: `cli/v*` → release-cli.yml, `mobile/v*` →
  release-mobile.yml, `web/v*` → release-web.yml. Tag protection is a repo
  ruleset (`release tag protection`); it survived the org transfer, as did all
  61 Actions secrets — verified by diffing before/after.
- **Cloudflare web deploy**: `./deploy/deploy.sh cloudflare` (delegates to the
  size-guarded Cloudflare script using `@opennextjs/cloudflare` + `wrangler
  deploy`).
- **Convex backend deploy**: `./deploy/deploy.sh backend`. Not triggered by CI
  — deploy explicitly when schema or HTTP routes change.

## Secrets

Three places. Never anywhere else. Never in a tracked file or git history.

1. **GitHub Actions secrets** (CI). `gh secret list -R yaver-io/yaver.io`
   for the canonical list. Includes:
   `ANDROID_KEYSTORE`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`,
   `ANDROID_KEY_PASSWORD`, `APPLE_CERTIFICATE_P12`,
   `APPLE_CERTIFICATE_PASSWORD`, `APP_STORE_CONNECT_API_KEY`,
   `APP_STORE_CONNECT_KEY_ID`, `APP_STORE_CONNECT_ISSUER_ID`,
   `APPLE_TEAM_ID`, `PLAY_STORE_SERVICE_ACCOUNT_JSON`, `NPM_TOKEN`,
   `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`, `CONVEX_DEPLOY_KEY`,
   `RELAY_PASSWORD`, `HCLOUD_TOKEN`, `HETZNER_TEST_SERVER_*`, etc.
2. **Local gitignored files** (dev machine): `.env.test`,
   `mobile/android/keystore.properties`, `keys/*`. All in `.gitignore`. Never
   force-add.
3. **Runtime env vars** (ad-hoc scripts). Scripts exit 2 if required vars are
   missing — never fall back to a hardcoded default.

If you find yourself about to put a secret in a tracked file, stop. Add as
GitHub secret + read from env. If it ever reached your clipboard, rotate.

`yaver vault` is for project-scoped local secrets that the daemon needs at
runtime. `yaver vault add APP_STORE_KEY_ISSUER --project mobile --value <uuid>`
etc. — vault is encrypted with your auth-token-derived key, so it locks if
your token rotates.

## Privacy contract — what lives where

Yaver's promise: Convex stores identity, peer discovery, and session
bookkeeping only. Anything sensitive or work-derived stays on the user's
devices and flows P2P.

**Allowed in Convex**: `users`, `sessions` (token hashes only), `sdkTokens`
(hashes only), `devices`, `relayServers`, `platformConfig`,
`guestInvitations`, `guestAccess`, `teams`, `teamMembers`, `userProjects`
(slug + deviceId + flags + branch — **no absolute paths**), activity audit
summaries (action + target + outcome + timestamp).

**Forbidden in Convex** (enforced by `desktop/agent/convex_privacy_test.go`):
vault values, raw tokens / API key plaintext, task input prompts or stdout,
file contents, exec session output, absolute filesystem paths (they leak the
user's home-dir username), customer LAN IPs.

All Convex-bound calls go through `convexSyncer.callMutation`. The privacy
test enumerates forbidden keys (`path`, `workDir`, `token`, `stdout`,
`output`, `vaultValue`, `secret`, …) AND scans for path leaks (`/Users/`,
`/home/`, `/root/`, `C:\Users\`). New sync paths must add their fields to
`fieldsWeForbidInAnyConvexPayload` and a test for the payload.

## Monorepo rule

**Yaver is ONE monorepo — this one.** Agent, CLI, mobile, watch, TV, car,
AR/VR, web, relay, backend, SDKs all ship together here. Don't propose
splitting a surface out: they have to agree on protocol and version, and
separate repos would only buy skew.

The `yaver-io` org holds this repo plus **validation / use-case apps** —
anything whose job is to exercise Yaver from the OUTSIDE. Those get their own
repos on purpose: a fixture that lives in here inherits this repo's tooling and
stops being an honest test of what a user's project actually hits.

**Rule: product code here; anything that tests the product from outside gets
its own repo.**

## Validation apps live OUTSIDE this repo

The todo fixtures are their own public repos, not `demo/` subdirectories:

| Repo | Stack | Reach | Feedback SDK |
|---|---|---|---|
| [yaver-todo-rn](https://github.com/yaver-io/yaver-todo-rn) | Expo / RN | Hermes (HBC swap) | ✅ `yaver-feedback-react-native` |
| [yaver-todo-kt](https://github.com/yaver-io/yaver-todo-kt) | Android / Kotlin | `native-webrtc` | ❌ **none exists** — viewer-triggered only |
| [yaver-todo-swift](https://github.com/yaver-io/yaver-todo-swift) | iOS / SwiftUI | `native-webrtc` | ❌ **none exists** — viewer-triggered only |
| [yaver-todo-flutter](https://github.com/yaver-io/yaver-todo-flutter) | Flutter | `native-webrtc` | ✅ `yaver_feedback` (pub.dev) |
| [yaver-todo-web](https://github.com/yaver-io/yaver-todo-web) | Next.js | dev server / HMR | ✅ `yaver-feedback-web` |

Same todo UX five ways, on purpose: the app is the control, so any difference
you observe is the transport. Local-only — no backend, no accounts, no network.

Three facts worth not rediscovering:
- **Hermes is RN/Expo-only**, gated at `hotreload.tsx:77`. Flutter is classed
  `DevServerKindWeb` (`devserver_kind.go:37`). Native and Flutter apps can never
  load into the Yaver container — they take `native-webrtc` or a standalone
  install via `yaver wire push`.
- **There is no native Kotlin/Swift feedback SDK.** `sdk/feedback/` ships
  react-native, web, flutter, kotlin, swift, browser-extension. Don't import one.
- **The viewer owns the feedback trigger** for streamed apps:
  `remote_runtime.go:709` `case "launch-feedback"` pushes
  `feedback-launch-request` down the events DataChannel. That's how kt/swift get
  the loop without an in-app SDK.

## Three-part architecture

1. **Mobile app** (App Store / Play Store) — native container for testing
   third-party RN apps via Hermes push. AI agent control. HTTP server on
   port 8347 for `yaver-cli` push-to-device.
2. **`yaver-cli` (npm)** — third-party RN devs push their own projects to
   the phone via `yaver push`. No agent needed; talks directly to the phone's
   8347 server. Same npm package.
3. **Desktop agent (Go)** — same `yaver-cli` package. P2P, relay, MCP, hot
   reload (Expo / Flutter / Vite / Next.js), session transfer, builds, vault.
   Used to drive AI agents from your phone.

### Feedback SDK Dogfood surfaces

The React Native feedback SDK exposes separate native setup and runtime
surfaces. `DogfoodSettings` owns Yaver OAuth, installation approval,
box/runner/checkout/lane selection and the UI-mode preference;
`DogfoodNativeMenu` owns the stateful Launch/Reload/Exit + Tasks controls;
`DogfoodEntryIcon` is the only affordance over a Yaver-hosted app; and the
standalone `FeedbackModal` owns `DogfoodQuickControls` so SDK consumers do not
mount a second Dogfood widget. Launch
must keep `DogfoodLiveConsole` visible until the browser/Hermes/WebRTC runtime
is proven — never redirect to Tasks and leave the retained output with no
consumer. Tasks are a parallel control surface: navigating there keeps the
root runtime owner alive, stays pinned to the prepared checkout/runner, and
must not mount a second preview controller for the same Expo server.
`reload-only` removes SDK chat so the developer can code through Yaver Tasks,
MCP, Claude Code, Codex, or another control plane. `reload-and-chat` retains the
SDK conversation. Both modes require the same Yaver OAuth and approved
installation.

Reload execution is `POST /dogfood/reload` / MCP `dogfood_reload`. It targets
the selected checkout and lane and renders the checkout's current working tree
(often a solo developer's `main`) without committing, checking out, rebasing,
pulling, or pushing Git. Yaver's own active Expo attach continues to use the
narrower `dogfood_status` + `dogfood_rerender` pair.

## Connection strategy

Direct-first, relay-fallback, per surface:

| Surface | Strategy |
|---|---|
| Mobile app | LAN beacon (UDP 19837) → Convex-known IP → relay. WiFi → direct first; cellular → relay only. |
| Desktop Electron | Local IPC (`localhost:18080`) → direct LAN → relay. |
| Web dashboard | Relay only (browser CORS blocks LAN). |
| Go CLI | Local daemon by default. `yaver connect` / `yaver code --attach <device>` resolve a remote and tunnel. |

Each surface stores its own session token independently. The same OAuth user
can be signed into all surfaces simultaneously. Sessions are 1-year, refreshed
on every heartbeat. Sign-out on one surface doesn't affect others.

## Daily commands

```bash
# auth + serve
yaver auth                           # OAuth (Apple / Google / GitHub / GitLab / Microsoft / email)
yaver auth --headless                # short-code flow for SSH-only boxes
yaver serve                          # start agent (auto-installs systemd unit on Linux)

# everyday
yaver status                         # local agent + auth + relay state
yaver primary status                 # remote primary device — agent version, lifecycle, runners
yaver primary auth                   # SSH into primary + run yaver auth --headless there
yaver primary set <deviceId|alias>   # pick a device as your primary
yaver primary ping                   # one-shot reachability + auth-as-same-user check
yaver ping <alias|deviceId|name>     # same, any device

# devices
yaver devices                        # list registered devices
yaver alias set <name> <deviceId>    # short name for ssh / connect / ping
yaver ssh <alias|primary>            # OpenSSH wrap, resolves LAN-on-subnet → Tailscale (gated on local 100.x interface up) → device row → ssh config

# code
yaver code                           # local TUI on this machine
yaver code --attach <device>         # remote machine via QUIC tunnel
yaver insert <app>                   # tell the paired mobile to load <app> via Hermes push

# Mac mini remote worker (Yaver via Yaver + Codex)
scripts/setup-mac-mini-dev.sh        # on the mini: Xcode platforms, simulators, Codex gpt-5.5, Yaver MCP
                                      # details: docs/mac-mini-remote-worker.md

# cable
yaver wire detect                    # USB-attached iPhone/iPad/Android
yaver wire push [path]               # framework-aware build + install (Expo/RN/Flutter/Native)

# vault
yaver vault add <name> --project <p> --value <v>
yaver vault env --project <p>        # source for deploy scripts
```

## Networking — short reference

| Layer | Port | Purpose |
|---|---|---|
| HTTP | 18080 | agent API (auth + tasks + dev server proxy) |
| QUIC | 4433 | relay tunnel out + direct phone connections |
| UDP beacon | 19837 | LAN auto-discovery (auth-aware via token-hash fingerprint) |
| HTTPS (LAN) | 18443 | self-signed TLS for SDK clients on LAN |
| Phone HTTP | 8347 | mobile-app inbound for `yaver push` from CLI |

Relay is application-layer QUIC, password-protected, self-hostable, no
TUN/TAP. Pass-through — never stores task data.

## Mobile app

### Task detail renders semantic conversation + folded runner evidence (2026-08-31)

`desktop/agent/task_presentation.go` preserves runner meaning in the schema-1
`presentation` lane (`message`, `status`, `action_required`, `warning`,
`error`, `tool`, `patch`). Task detail renders human-facing messages and state
by default; it never reconstructs chat from terminal bytes when that lane is
present. `presentation_snapshot` is replayed on every SSE subscription, and
`presentation` upsert/append frames carry live updates. The bounded snapshot
stays device-local and MCP `get_task` exposes it for human-readable status.

The app independently consumes bounded RAW runner stdout/stderr/PTY bytes
(`/tasks/{id}/output?rawSince=` + `onRaw` SSE) into a per-task 512 KB buffer and
keeps it folded in `LiveConsoleSection` via the shared `AnsiConsoleText`
(same colours/grammar as the opencode console: green `$` prompts, orange
`> build · <model>` banners, diff +/- lines, `● live`/`○ idle` dot, byte
counter). Raw output is evidence for diagnosis, not the primary remote UI.

Live `raw` frames name their process `stream` (`stdout`, `stderr`, or `pty`).
Feedback/Dogfood React Native SDK clients use the source-gated
`/vibing/task/{id}/output` mirror through `XMLHttpRequest.onprogress` because
Hermes fetch can buffer SSE until EOF. Runner task memory is bounded: 1 MiB
groomed transcript, 512 KiB raw replay, and shallow recoverable live queues.
Full contract: `docs/architecture/DESKTOP_RUNNER_STREAMING.md`.

Task lifecycle is a conversation contract on every client surface: `queued` /
`running` means the runner is coding; `ready` means this turn has a clean
reply and the same runner conversation remains available; `review` is emitted
only when the runner explicitly calls `yaver_report_complete` after fully
completing and verifying the requested work; only an explicit user Complete
closes the retained seat. Phone/web/macOS can disclose command cards and raw
evidence under Details. TV, watch, car, and spatial surfaces consume only the
semantic answer, named state, and one next action—never patches, source, or
terminal output.

Task runner controls are typed, task-scoped operations. A whole-message
`/model` opens the catalog reported by that task's runner machine (Codex also
opens the selected model's supported reasoning levels); `/exit` always asks
for confirmation and the agent verifies the live runner seat is gone before
reporting success. Clients POST the chosen operation to
`/tasks/{id}/control` (Feedback/Dogfood uses
`/vibing/task/{id}/control`) rather than typing menu keystrokes or parsing TUI
pixels. Entering `/` suggests only these two supported controls. Mentions such
as `fix the /exit bug` remain ordinary coding prompts.

- The raw lane is independent of the groomed transcript: `?rawSince=` seeds a
  full `raw_replay` snapshot (finished tasks), then live `raw` frames append.
  Reattach passes the byte cursor back as `rawSince` so the console never
  gaps across a tunnel drop. Transport: `mobile/src/lib/quic.ts`
  `streamTaskOutput` `onRaw`; classifier: `mobile/src/_core/ansi.ts` (web twin
  `web/lib/_core/ansi.ts` — keep in sync).
- **If task detail shows folded command cards with NO output, the agent binary
  is stale.** Rebuilt agents (≥ 2026-08-09) emit `command_output` SSE
  (`desktop/agent/opencode_stream.go` `closePendingCommand`); verify with
  `strings <bin> | grep pendingCmdID`. Command cards stream real stdout now:
  `command_start` → `command_output` → `command_end`.
- **Opening the app for manual testing**: `node e2e/open-mobile-app.mjs`
  (headed Chromium, iPhone 13 viewport, touch enabled, persistent profile at
  `~/.yaver-e2e-profile`; use `E2E_PROFILE=~/.yaver-e2e-profile-manual` if the
  default is locked). Never open `localhost:8081` in a desktop Chrome tab and
  call it "mobile" — a narrowed window is a different RN-web component tree.
  Headless: inject the agent token into
  `localStorage["yaver.secure.yaver_auth_token"]` (RN-web key,
  `mobile/src/lib/secureStoreCompat.ts`), drive with `devices["iPhone 15 Pro"]`
  — see `e2e/verify_live_console7.mjs` (verified 2026-08-09).

### Shared browser-automation queue (concurrent sessions)

Browser preview state is exclusive even though coding threads are concurrent.
Threads therefore dump one append-only Markdown case into
`e2e/browser-automation/test-cases/YYYY-MM-DD/` and never start competing
closed-loop sessions. The coordinator drains the oldest queued date with one
isolated Chromium profile; every attempt writes a new Markdown result under
`e2e/browser-automation/results/YYYY-MM-DD/` so failures and reruns do not
overwrite history.

```bash
cd e2e
npm run browser-queue:validate
MOBILE_WEB_URL=http://127.0.0.1:8081 npm run browser-queue:open
```

The launcher requires an explicit RN-web URL, takes an owner-only singleton
lock, uses the full Playwright `iPhone 15 Pro` descriptor, and asserts the
viewport before opening the app. It never guesses port 8081 and never
substitutes the dashboard. Cases and results must not contain credentials,
customer/project names, machine addresses, or absolute home paths; screenshots
and traces remain untracked and are referenced by safe relative labels. Run the
headless verb/test first, then queue only the PIXELS/NAMED/SILENT proof. Full
format and status rules live in `e2e/browser-automation/README.md`; the
validator and launcher are code-level truth when prose drifts.

### Hermes-push (default for RN/Expo)

`yaver-cli push` and the agent's `/dev/build-native` both produce a Hermes
bytecode bundle and load it into the Yaver mobile container via
`ExpoReactNativeFactory + RCTAppDependencyProvider` (TurboModules, Fabric,
JSI). Validation: HBC magic `0x1F1903C1` at offset 4, BC version (96) at
offset 8.

**Suppress-when-inside-Yaver** (RN SDK 0.5.5+): when a third-party RN app
loads inside the Yaver container, `YaverFeedback.init()` and
`ShakeDetector.start()` silently no-op via the `YaverInfo` native module.
Yaver's bridge-safe native Y owns the same compact Chat / Reload / Settings /
Exit contract as `DogfoodQuickControls`; the Y is the first-run default and
shake is the opt-in no-persistent-pixels alternative.
Standalone TestFlight/Play builds are unaffected.

### `sdk-manifest.json` contract

Source of truth: `mobile/sdk-manifest.json`. Must be in sync with
`mobile/android/app/src/main/assets/sdk-manifest.json`, the iOS bundle copy,
and `cli/sdk-manifest.json`. Update all four when bumping `mobile/package.json`.

### iOS — TestFlight (LOCAL ONLY)

CI is intentionally disabled (`if: false` in `release-mobile.yml`) because GH
runner keychains don't carry your registered iPhone UDIDs. Always run from
this Mac.

**Apple portal automation is Chromium-only.** Use Yaver's `browser_*` MCP
tools with their persistent authenticated Chromium profile for Apple Developer
and App Store Connect. If Apple requires login, 2FA, or another human-only
step, open a headed Chromium handoff and continue through the same profile once
the user finishes. Never use or fall back to Safari. Read the DOM and act on
named selectors rather than screen coordinates. The Apple team contains other
products: verify the bundle ID or App Store Connect Apple ID before every
mutation, and change only Yaver resources (`io.yaver.*`, existing Yaver IO app
Apple ID `6760467669`) unless the user explicitly names another target.

```bash
# vault path (preferred when auth is fresh)
$(yaver vault env --project mobile)
./scripts/deploy-testflight.sh

# fallback when vault is locked: source the gitignored env file
source ~/.appstoreconnect/yaver.env
./scripts/deploy-testflight.sh

# explicit env path (no vault, no env file — type the values yourself)
export APP_STORE_KEY_PATH="$HOME/.appstoreconnect/private_keys/AuthKey_<key-id>.p8"
export APP_STORE_KEY_ID="<key-id>"
export APP_STORE_KEY_ISSUER="<uuid>"
export APPLE_TEAM_ID="<team-id>"
./scripts/deploy-testflight.sh

# interactive-Mac fallback: ask Xcode to use its managed Apple account.
# The archive operation is the auth probe; a browser login or account name in
# preferences is not proof that Xcode's provisioning token is usable.
# Read the current highest iOS build in TestFlight first; never guess.
YAVER_IOS_BUILD_NUMBER=<next-build> ./scripts/deploy-testflight.sh
```

When provisioned, `~/.appstoreconnect/yaver.env` is gitignored and carries all
four exports — sourcing it is the friction-free path when vault is locked
(common after auth token rotation). Its absence is valid and selects the
Xcode-managed account lane, which requires an explicit verified next build.
Keep the file in sync if you rotate the App Store Connect issuer/key.

GitHub Actions secrets backing the same flow (already populated): `APPLE_TEAM_ID`,
`APP_STORE_CONNECT_API_KEY`, `APP_STORE_CONNECT_KEY_ID`, `APP_STORE_CONNECT_ISSUER_ID`,
`APPLE_CERTIFICATE_P12`, `APPLE_CERTIFICATE_PASSWORD`. Verify with `gh secret list -R yaver-io/yaver.io`.

Vault entries (write once when vault is unlocked, then `yaver vault env --project mobile`
emits the same exports as the env file):
```bash
yaver vault add APP_STORE_KEY_PATH   --project mobile --value "$HOME/.appstoreconnect/private_keys/AuthKey_77Z6B543D5.p8"
yaver vault add APP_STORE_KEY_ID     --project mobile --value 77Z6B543D5
yaver vault add APP_STORE_KEY_ISSUER --project mobile --value <uuid>
yaver vault add APPLE_TEAM_ID        --project mobile --value 5SJZ4KA39A
```

With a complete API-key configuration, the script queries App Store Connect and
auto-bumps CFBundleVersion. With no API-key variables, it asks Xcode to use its
managed account and requires `YAVER_IOS_BUILD_NUMBER`; the archive itself may
still report `No Accounts` when Xcode's token is absent/expired even if the
browser is signed in or Xcode preferences list the Apple ID. Refresh Xcode →
Settings → Accounts in that case. The explicit build requirement prevents
a stale local plist from colliding with an existing TestFlight build. A partial
API-key configuration or unreadable key path is always an error. The script
archives at `/tmp/Yaver.xcarchive`, exports, and uploads. API-key deploys export
an IPA locally and then validate/upload it with `altool`; this avoids Xcode's
expirable App Store upload session. Xcode-managed-account deploys still use
Xcode's direct upload lane. On an export flake, preserve the archive and rerun
through the canonical `./deploy/deploy.sh ios` path; do not submit an
unvalidated IPA by hand.

**TestFlight rate limit**: ~15-20 uploads/app/day. Don't re-run after
"Upload limit reached" — wait 24h.

**`uploadSymbols=false`** in ExportOptions.plist is mandatory. Xcode 15+
treats missing dSYMs as a fatal export error; `rnwhisper` ships without
dSYMs. Apple symbolicates server-side from bitcode anyway.

#### Headless codesign — unlocking the keychain from an SSH/agent session

The single hardest failure for an **autonomous (SSH / yaver-agent) deploy** is
`CodeSign … errSecInternalComponent`. It is NOT "cert missing" — `security
find-identity` happily lists the identities. It means the signing **private key**
is in a **locked** keychain that a non-GUI session can't open. Two facts learned
the hard way (2026-07-19), verified with per-cert `codesign -s <SHA1>` probes:

1. **The signing identity spans two keychains.** The **Apple Distribution**
   (SIMKAB, team `5SJZ4KA39A`) cert+key live in a dedicated
   `~/Library/Keychains/yaver-ci.keychain-db`; the **Apple Development** cert
   *private keys* live in `login.keychain-db`. During an App Store archive,
   Xcode signs the app-extension/watch intermediates with the **development**
   identity and re-signs with distribution at export — so **both** keychains
   must be unlocked, or the archive dies at `CodeSign …/*.appex`.
2. **Unlock is not enough — you must set the partition list.** For codesign to
   use a key *without a GUI prompt* you must run, per keychain:
   ```bash
   security unlock-keychain      -p "<pw>" "$KC"
   security set-keychain-settings          "$KC"   # no flags = never auto-lock
   security set-key-partition-list -S apple-tool:,apple: -s -k "<pw>" "$KC"
   ```
   Do this for **both** `yaver-ci.keychain-db` AND `login.keychain-db` before
   `deploy-testflight.sh`. `launchctl asuser` / gui-domain LaunchAgents do NOT
   help (they still hit the locked key); only the password does.

The passwords come from the secure local store below — never hard-code them, and
`deploy-testflight.sh` sources them so a headless run self-unlocks. `set-keychain-settings`
with no flags disables auto-lock so the ~20-min archive can't relock mid-build.

### Local privileged credential store (`~/.yaver/local-secrets.env`)

Some autonomous ops need **local-machine unlock secrets** a human would normally
type at a GUI: the **signing-keychain password**, the **login-keychain / macOS
login password**, and **sudo**. These live in `~/.yaver/local-secrets.env`:

- **`chmod 600`, owner-only. NEVER committed, NEVER synced to a cloud/GH secret.**
  A macOS login/sudo password in GitHub secrets would *widen* the attack surface —
  the exact opposite of the goal. It stays on the box, protected by filesystem
  perms + FileVault at-rest encryption, and never leaves it.
- A CI-scoped signing-keychain password MAY also go in GH Actions secrets
  (`YAVER_CI_KEYCHAIN_PASSWORD`) because CI needs it and it only unlocks the
  disposable signing keychain — but the **login/sudo** password must not.
- Canonical home is the encrypted **`yaver vault`**; this env file is the
  fallback used when the vault is unavailable (mirrors `~/.appstoreconnect/yaver.env`).
  If `yaver vault` reports "corrupted vault", repair with `yaver vault reset --yes`
  then re-add, or restore `~/.yaver/master.key`.
- Keys: `YAVER_CI_KEYCHAIN_PATH/PASSWORD`, `YAVER_LOGIN_KEYCHAIN_PATH`,
  `YAVER_LOGIN_PASSWORD`, `YAVER_SUDO_PASSWORD`. The yaver agent reads these to
  unlock keychains / run sudo headlessly; a hostile process can only read them
  with local FS access, which already implies the box is compromised.

### Android — Play Store

#### Android local validation (no submission)

The local readiness lane compiles and signs Android artifacts without
submitting or deploying them. Do not call `./deploy/deploy.sh android`, Play
APIs, or upload scripts during validation; Google approval is a separate human
gate.

Run only one build at a time on the 8 GB validation Mac. The checked-in
Gradle settings cap memory and disable project parallelism: mobile uses a 3 GiB
heap and two workers; Wear OS and Android TV use a 2 GiB heap and two workers.

Validated commands:

```bash
REPO_ROOT="$(git rev-parse --show-toplevel)"

# Phone/tablet + Android Auto native modules
(cd "$REPO_ROOT/mobile/android" && ./gradlew --no-daemon :app:assembleRelease)

# Standalone Wear OS and Android TV; use Gradle >= 8.7 (8.14.3 is validated)
(cd "$REPO_ROOT/wear" && <gradle-8.14.3> --no-daemon bundleRelease)
(cd "$REPO_ROOT/androidtv" && <gradle-8.14.3> --no-daemon bundleRelease)
```

The mobile APK is checked with `apksigner verify --verbose --print-certs`.
The Wear OS and Android TV AABs are checked with
`jarsigner -verify -verbose -certs` and `keytool -printcert -jarfile`. The
current Yaver upload certificate SHA-256 is
`FF:E9:9C:94:63:B0:2A:42:0D:11:42:35:CD:A7:3F:76:7A:5A:0D:18:0F:73:CE:CF:BC:2E:E2:DF:C4:AC:D5:4E`.

After verification, list and remove only generated outputs before starting the
next surface: `mobile/android/app/build`, `mobile/android/app/.cxx`,
`wear/app/build`, `androidtv/app/build`, and the corresponding project
`build/`/`.gradle` directories. Never remove `mobile/android/keystore.properties`,
`keys/yaver-upload.keystore`, `node_modules`, the Android SDK/NDK, or any
credential store. A signed local APK/AAB proves compilation and key access; it
does not authorize a store submission.

CI handles it via `release-mobile.yml` on `mobile/v*` tags using
`PLAY_STORE_SERVICE_ACCOUNT_JSON`, `ANDROID_KEYSTORE*` secrets. Android
versionCodes auto-increment.

Local equivalent:
```bash
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh
PLAY_STORE_KEY_FILE=keys/google-play-service-account.json \
  python3 scripts/upload-playstore.py
```

`mobile/android/keystore.properties` is gitignored. Restore after
`expo prebuild --clean`:
```
storeFile=../../../keys/yaver-upload.keystore
storePassword=<password manager>
keyAlias=yaver-upload
keyPassword=<password manager>
```

**Play app-signing SHA-256 fallback (mirrors the TestFlight env file)**:
the SHA lives in Play Console → Setup → App integrity. The canonical
source is the vault (`yaver vault add ANDROID_RELEASE_SHA256 --project
mobile --value <fingerprint>`), but after an auth-token rotation the
vault locks. Pre-seed `~/.androidplay/yaver.env` once and
`deploy-web.sh` will source it on every run:
```bash
mkdir -p ~/.androidplay && cat > ~/.androidplay/yaver.env <<'EOF'
export ANDROID_RELEASE_SHA256="AA:BB:CC:..."
EOF
```
Without this, `assetlinks.json` ships with only the upload-keystore
SHA — passkey enrollment silently fails on Play-distributed builds
because Credential Manager can't bind `yaver.io` to the Play-resigned
APK.

### Force-tracked iOS overlay files

`mobile/ios/` is gitignored, but a few overlays are force-added because
`expo prebuild --clean` regenerates the dir:

- `mobile/ios/Yaver/AppDelegate.swift` — super-host bootstrap, ShakeDetectingWindow, YaverHTTPServer.shared.start(), safe bridge reload
- `mobile/ios/Yaver/Yaver-Bridging-Header.h` — Swift ↔ ObjC, GCDWebServer
- `mobile/ios/Yaver/YaverBundleLoader.swift` + `.m` — `NativeModules.YaverBundleLoader`
- `mobile/ios/Yaver/YaverScreenRecorder.swift` + `.m` — feedback visual capture
- `mobile/ios/Yaver/YaverHTTPServer.swift` — port-8347 bundle-receive server (currently a stub)
- `mobile/ios/Yaver/YaverInfo.swift` + `.m` — `isYaver` detection from guest bundles
- `mobile/ios/Yaver/YaverBundleValidator.swift` — HBC validation + `SDKManifest` singleton (currently stub)
- `mobile/ios/Yaver/sdk-manifest.json` — copy of mobile/sdk-manifest.json

The HTTPServer / Validator / Info stubs exist because `pbxproj` references
them. When filling in real implementations, match the signatures in
`YaverBundleLoader.swift` and `AppDelegate.swift`.

### Cold-start mobile rebuild (after `expo prebuild --clean`)

```bash
cd mobile
npm install --legacy-peer-deps
npx expo prebuild --platform ios     --clean --no-install
npx expo prebuild --platform android --clean --no-install
git checkout -- mobile/ios/ mobile/android/   # restore force-tracked overlays
cp mobile/sdk-manifest.json mobile/ios/Yaver/sdk-manifest.json
cd mobile/ios && pod install && cd ../..
# create mobile/android/keystore.properties (see above)
./scripts/deploy-testflight.sh
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh
```

First-time pod install is ~28 min, archive is ~15-20 min, gradle bundleRelease
is ~28 min.

### Disk-space preflight

The canonical `./deploy/deploy.sh ios` path measures the real filesystem and
fails before CocoaPods or a build-number bump unless at least 10 GiB is free.
That floor and its warm/cold-cache logic live in
`scripts/deploy-testflight.sh`; do not rely on a workstation-only cleanup
helper being installed. Inspect the exact Yaver generated paths named by the
failure, reclaim only reviewed disposable artifacts, and rerun the wrapper.

### Version bumping (6 locations, all must match)

When bumping `mobile/v<x>`:
1. `mobile/app.json` → `expo.version`
2. `mobile/package.json` → `version`
3. `mobile/ios/Yaver/Info.plist` → `CFBundleShortVersionString`
4. `mobile/ios/Yaver.xcodeproj/project.pbxproj` → every `MARKETING_VERSION` build configuration
5. `mobile/android/app/build.gradle` → `versionName`
6. `versions.json` → `mobile`

Edit `versions.json`, then run `./scripts/sync-versions.sh`; the script also
keeps the Go agent variable and CLI package/lock metadata aligned.

Build numbers (`CFBundleVersion` / `versionCode`) auto-increment in deploy
scripts.

### Mobile dev iteration (fast, no TestFlight)

USB-connected iPhone, no daily limit:
```bash
xcrun xctrace list devices 2>&1 | grep -v Simulator    # find UDID
yaver wire push                                          # detect framework + install Release build
# OR: cd mobile && npx expo run:ios --device <UDID>     # Debug build (needs Metro on :8081)
```

Multiple RN projects fight for port 8081. Either kill the others
(`pgrep "expo start" | xargs kill`) or build Release.

### Pushing Yaver itself (`yaver wire push` / `yaver wireless push`)

Both commands auto-detect the mobile project from CWD by walking up to
the first `app.json` / `package.json` with `expo` or `react-native`,
or `pubspec.yaml`, or `ios/*.xcodeproj`. **CWD matters.**

**To iterate on Yaver mobile (this repo):**
```bash
# from repo ROOT — wire/wireless walks into ./mobile automatically
cd <repo>
yaver wireless push                                      # WiFi-paired iPhone
# or:
yaver wire push                                          # USB-attached
```
Before an iOS build mutates dependencies, the command measures the checkout
volume and requires 10 GiB of free or reusable Wire-owned DerivedData space
(a cold build still requires 10 GiB free, and every build retains a 2 GiB
absolute free-space floor). If CocoaPods artifacts are missing,
partial, or stale, it then streams an incremental `pod install` and rechecks
the actual Pods project/config files and every referenced React Native codegen
source before invoking Xcode. A low-disk refusal names the measured free and
reusable space plus the `diskguard_scan` ops route instead of failing midway
with `No space left on device`.
Running from `desktop/agent`, `web/`, `relay/`, or any non-mobile
subdir fails with `no mobile project detected at <path>`. Always
`cd` to repo root first.

**To iterate on a third-party app:**
```bash
cd <example-app>
yaver wire push       # builds + installs that app, NOT Yaver
```
Third-party RN apps load INSIDE the Yaver container via Hermes-push;
they don't need their own native install once Yaver is on-device.
But `yaver wire push` from the third-party repo will native-build +
install the third-party app (useful for first-time setup or when
testing a non-Hermes change).

**Rule of thumb**: the binary getting installed = whatever mobile
project lives in CWD. If you want to ship Yaver, `cd yaver.io` first.

Output ends with `App installed:` + `bundleID: io.yaver.mobile` (when
pushing Yaver) or `bundleID: <third-party>` (when pushing a guest
app). Check the bundleID line if unsure what just got installed.

## Mobile dev-server proxy / Hermes flow on remote agent

Three commands matter:
- `POST /dev/start {framework, workDir}` — Metro/Vite/Flutter/Next
- `POST /dev/build-native` — compile Hermes bundle once
- `POST /dev/reload` (or `/dev/reload-app {mode}`) — hot reload (Expo/RN
  refresh through native Hermes path; web frameworks refresh via WebView)

`yaver-test-ephemeral` (Linux ARM) → mobile flow:
```bash
# from any machine signed in as the same user:
yaver insert sfmg                  # broadcast "open_app" to paired phones
# mobile receives via /blackbox/command-stream → navigates to Hot Reload tab
# → POST /dev/build-native on the agent → loads Hermes bundle
```

## Hetzner test box rules

`yaver-test-ephemeral` (cax21 arm64) is for remote integration testing.
**Disposable** — kill anytime. Reproducible from `ci/remote/bootstrap.sh`.

- No secrets ever live there.
- No user-visible copy mentions it.
- Cost-cheap pause: snapshot via `ci/hcloud/snapshot-server.sh`, then
  `hcloud server delete yaver-test-ephemeral`. Snapshot ~€0.10/mo vs
  €6.49/mo running.
- Recreate from snapshot: `ci/hcloud/create-server.sh` (uses
  `HETZNER_TEST_SNAPSHOT_ID` GH secret).
- Wired secrets (set with `gh secret set`): `HCLOUD_TOKEN`,
  `HCLOUD_SSH_PRIVATE_KEY`, `HETZNER_TEST_SERVER_ID`,
  `HETZNER_TEST_SERVER_IP`, `HETZNER_TEST_SNAPSHOT_ID`.

In Yaver device lists / Convex rows / `yaver ssh`, the same box may appear
as `ubuntu-4gb-hel1-1`. Set per-user alias `test` and use `yaver ssh test`.

## Conventions

- Go: standard layout, `gofmt`, build tags only when truly platform-specific.
- TypeScript / React: functional components, hooks. No class components.
- Convex: mutations for writes, queries for reads, httpAction for HTTP routes.
- Mobile: native builds (xcodebuild + Gradle), never Expo CLI for production.
- Tests: real HTTP servers on random ports, no mocks, no external deps. See
  `desktop/agent/*_test.go` for the pattern.

## Cross-surface parity — every fix ships to EVERY UI surface

**A fix is not done until it exists on all surfaces.** This holds for
connectivity, auth/session, wake/park, and any user-facing behavior. The
surfaces are: **mobile** (phone + tablet), **web**, **tvOS**, **watchOS +
Wear OS**, **CarPlay (car)**, and **glass / AR-VR**.

Two families with DIFFERENT propagation:

1. **React-Native surfaces share code** — mobile, tablet, car
   (`app/car-voice-coding.tsx`), and glass/AR-VR (`app/glass-*.tsx`) all consume
   the same `DeviceContext` + `AuthContext`. A fix there (e.g. the relay
   self-heal in `DeviceContext.tsx`, auth extend-only refresh in `lib/auth.ts`)
   reaches all of them for free. Verify it isn't gated to one screen.
2. **Native surfaces have their OWN code and must be ported explicitly** —
   tvOS (`tvos/YaverTV/YaverStore.swift`, `AgentClient.swift`), watchOS
   (`watch/YaverWatch/WatchStore.swift`, `SessionClient.swift`), Wear OS
   (`wear/.../*.kt`), and web (`web/lib/use-auth.ts`, `web/app/dashboard/`).
   They do NOT inherit RN fixes — port the same behavior and note it in the
   commit. Examples already mirrored this way: Netflix-grade session persistence
   (extend-only `/auth/refresh` on launch), Stream-C narrated auto-connect, and
   relay self-heal (`POST /settings/repair-relay` → re-pull creds → retry, once
   per failure streak, per-user only).

When you fix connectivity/auth/lifecycle on one surface, grep the others for the
same seam and either confirm shared-code coverage or land the native port in the
same change. A per-surface parity table belongs in the handoff/commit.

## HEADLESS FIRST, THEN CLOSED LOOP — and both must SNOWBALL

**The order is not a preference, it is how you avoid spending a UI run to learn
an API fact.** Every investigation and every feature goes:

1. **HEADLESS FIRST — ask the machine directly.** `curl` the agent endpoint,
   run the CLI verb, call the MCP tool, `codex exec --model <id>`, hit the
   Convex route with a bearer token. It answers in seconds, its failure names
   itself, and it cannot be confounded by a spinner, a stale bundle, or a
   viewport.
2. **CLOSED LOOP SECOND — prove it where the user lives.** Drive the REAL
   surface (web dashboard; the RN app as RN-web in a true device context) and
   read the terminal signal — PIXELS, never a status badge.

Worked example, 2026-08-02, and the reason this rule is written down: a colour
vibe loop failed on both surfaces for hours. Three browser runs of ~25 minutes
each were spent on harness bugs before anyone asked the box directly. One
headless probe — `codex exec --model <id> "reply OK"` — answered it in ninety
seconds: `gpt-5.3-codex` was REJECTED by the ChatGPT-account subscription while
`gpt-5.6-terra` WORKED, and every device was pinned to a model that could not
run. No amount of browser automation could have said that; the API said it
immediately. Symmetrically, no API call could have proven the picker renders,
the preview refreshes, and the pixels actually change — that needed the loop.

**Both layers SNOWBALL. Every incident must leave BOTH bigger than it found
them** (this is the Snowball Principle applied to the test surface itself):

- **The headless layer grows a VERB.** If the answer required ssh + a hand-
  rolled python one-liner, that is a missing endpoint. Land it as an ops verb /
  MCP tool / CLI subcommand so the next session — and every surface — can ask
  the same question in one call. A question you can only answer by hand is a
  product gap, not a research skill.
- **The closed loop grows an ARC.** Every confirmed defect earns a check in the
  loop that would have caught it, on the surface it broke. When a loop fails
  for a HARNESS reason, fix the harness and keep the arc — never delete the
  assertion to get green.
- **Never let a probe stay ad-hoc.** The `codex exec` model probe above should
  become a `runner_model_probe` verb; the "which models does this subscription
  accept" answer belongs in the product, refreshed by measurement, not in a
  session transcript.
- **A guard that has never failed is a guess.** Break it, watch it fail, put it
  back. Two guards shipped inverted here precisely because nobody did that.

Corollary — **NEVER infer a fact you can measure.** "The `-codex` suffix must
mean codex-safe" is the exact reasoning that pinned six machines to a dead
model. Probe, then write the measurement into the code as a comment with its
date, so the next reader inherits evidence instead of instinct.

## Tests

```bash
# unit
cd desktop/agent && go test -count=1 ./...
cd relay && go test ./...

# e2e + integration suites
./scripts/test-suite.sh                     # full
./scripts/test-suite.sh --unit --lan --relay  # subset
./scripts/run-ci-local.sh                   # mirrors GH CI matrix

# trigger CI from terminal
./scripts/run-gh-ci.sh                      # all workflows
./scripts/run-gh-ci.sh ci e2e               # subset
```

Browser e2e (`e2e/`): Playwright + Chromium. CI runs on PRs touching `web/`
or `e2e/`.

## Feature pointers

For everything below, the canonical reference is the source. Brief pointers
only; the directories have their own READMEs / inline comments where it
matters.

| Area | Where |
|---|---|
| Auth + bootstrap + recovery | `desktop/agent/auth*.go`, `auth_recover.go`, `auth_bootstrap.go`; `backend/convex/auth.ts`, `deviceCode.ts` |
| Heartbeat + device registry | `desktop/agent/auth.go::SendHeartbeat`, `backend/convex/devices.ts::heartbeat`, `backend/convex/http.ts /devices/heartbeat` |
| Hot reload / dev server | `desktop/agent/devserver.go`, `devserver_http.go`, `dev_cmd.go`; mobile `app/(tabs)/apps.tsx` |
| Hermes push for guest apps | `cli/src/{analyzer,bundler,discovery,transport}.js`; mobile `ios/Yaver/YaverBundleLoader.*` + `YaverBundleValidator.swift` |
| Wire (cable) | `desktop/agent/wire_cmd.go`, `device_install.go`, `native_build.go` |
| Remote / insert (paired phone) | `desktop/agent/remote_mobile_cmd.go`, `mobile_session_http.go`; mobile `src/lib/openAppBus.ts`, `app/(tabs)/_layout.tsx` |
| Ping (reachability + auth-as-same-user) | `desktop/agent/ping_cmd.go`; mobile `DeviceDetailsModal.tsx::PingRow`; web `DevicesView.tsx::InlinePingButton` |
| Vault | `desktop/agent/vault.go`, `vault_cmd.go`, `vault_http.go`. NaCl secretbox + Argon2id, encrypted with auth-token-derived key |
| Deploy script generator + doctor | `desktop/agent/deploy_script_gen.go`, `doctor_build.go`, `deploy_script_http.go` |
| Store tester/build management (TestFlight + Play) | `desktop/agent/appstoreconnect.go` (ASC API: beta testers/groups/builds), `playpublish_api.go` (Play tracks/testers/rollout), `ops_store.go` (`store_*` MCP verbs, multi-tenant per-project vault, runs on managed cloud). Web `StoresView.tsx` Testers tab; mobile `app/store-testers.tsx` + `src/lib/storeTestersClient.ts`. Reuses Store Studio auth (`resolveAppleASCCreds`/`mintASCJWT`, `resolveGoogleSA`/`getGoogleAccessToken`). Apple=per-email testers; Google=track Google-Groups + rollout (per-email = Console-only). Doc `docs/yaver-store-tester-management.md`, blog `/blog/mobile-beta-testing-apple-google`. |
| Container sandbox (deferred) | `desktop/agent/container_runner.go`, `Dockerfile.sandbox`. End-to-end testing TODO — see `docs/guides/DOCKER_REMAINED.md` |
| Account linking + merge | `backend/convex/auth.ts::mergeUserInto`; `desktop/agent/account_cmd.go`, `mcp_auth_link_tools.go` |
| Phone-first mini backend | `desktop/agent/phone_backend.go`, `phone_backend_http.go`; mobile `app/phone-projects*` |
| Switch engine (target migrations) | `desktop/agent/switch_*.go` — 19 targets, snapshots, 7-day rollback |
| Session transfer | `desktop/agent/session_*.go`, `transfer.go` |
| Hands-free voice (all surfaces) | `mobile/src/lib/voice/` — one surface-agnostic `VoiceConversationCore` (streaming STT → semantic endpointing → runner dispatch → TTS → barge-in) + adapters; `useHandsFreeVoice` React seam; car wired in `app/car-voice-coding.tsx`. Semantic "when to submit" = timing trigger (`endpointer.ts`) → on-device judge (`completenessJudge.ts`, free llama.rn). Runner-only (no Flux). Doc `docs/architecture/VOICE_CONVERSATION.md`. tsx tests. |
| Feedback SDK + black box | `sdk/feedback/{react-native,web,flutter}/`; `desktop/agent/blackbox*.go`, `feedback*.go` |
| SDK token security | `desktop/agent/auth.go::ValidateSdkToken*`, `sdk_token.go`, `tls.go` |
| Networking (relay + beacon + QUIC) | `desktop/agent/quic.go`, `beacon.go`; `relay/` |
| Workspace manifest | `desktop/agent/workspace*.go`; `yaver.workspace.yaml` |
| Managed toggle | `desktop/agent/managed*.go`; `backend/convex/userSettings.ts` |
| `ops` MCP grand-tool | `desktop/agent/ops*.go` — single MCP tool, 20 verbs |
| Remote Desktop (screen view + mouse/kbd control) | `desktop/agent/remotedesktop*.go` (`/rd/status`,`/rd/policy`,`/rd/stream` MJPEG,`/rd/frame.jpg`,`/rd/input`); reuses `ghost/` engine + `ghost_stream.go`. Runtime consent policy (control opt-in), NOT the `--ghost` flag. Web `RemoteDesktopView.tsx`/`RemoteDesktopModal.tsx`; mobile `app/remote-desktop.tsx` (iOS-safe snapshot-poll, MJPEG only on web). Fullscreen on shell + remote view both surfaces. |
| Apple TV control + capture-card streaming | `desktop/agent/appletv.go` (pyatv sidecar supervisor + vault creds), `ops_appletv.go` (`appletv_*` + `capture_*` verbs), `appletv_cmd.go` (`yaver appletv …`), `capture.go` (ffmpeg→MJPEG, HDCP-black detection, `/capture/stream`+`/capture/frame.jpg`), `appletv/yaver_atv_bridge.py` (embedded). First-class image tool `appletv_now_playing` (robot_camera pattern). Mobile `app/appletv-remote.tsx` + `src/lib/appletvClient.ts` (D-pad/transport/now-playing/capture video, `?surface=glass` HUD). Control+metadata always-legal; capture = OWN non-protected sources only (NO HDCP capture, NO CarPlay video). Doc `docs/yaver-appletv-remote-control.md`, README `desktop/agent/appletv/README.md`. |
| Circuit simulator cell | `desktop/agent/circuit/` (dep-free pure-Go MNA solver op/dc/tran/ac + ngspice pass-through; SPICE/KiCad/EPLAN import; generic ERC; PNG plot) + `ops_circuit.go` (`circuit_*` verbs) + `circuit_plot` first-class MCP image tool (`mcp_tools.go`/`httpserver.go`). Web `CircuitCellView.tsx` @ `/dashboard/circuit`; mobile `circuit.tsx` + `circuitClient.ts`. Netlists = vault-local, never Convex. Doc `docs/yaver-circuit-simulator.md`. |

## Local development

```bash
cd backend && npx convex dev          # convex dev
cd web && npm run dev                  # next dev
cd mobile && npm run web               # browser RN preview (dev only)
cd desktop/agent && go run . serve     # agent
cd relay && go run . serve --password <secret>
```

For dev-server iteration on a specific RN project: see "Mobile dev iteration"
above. Don't tell users to run `expo start` manually — the agent handles it.
