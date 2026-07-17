---
doer: codex
---

<!-- No `master:` seat: this task's ground truth is already audited and inlined
below, so the planning seat would only re-derive what the doc states. The doer
reads the phases and the gate is the oracle.

Seat is codex, not claude, and deliberately so: the mini's claude credential
record has `expiresAt: 0` (epoch) despite a present keychain item, a present
~/.claude/.credentials.json, a live refreshToken, and subscriptionType=max. So
every readiness probe reads it as expired and autorun fails over — correctly,
and with a recorded heal event. `claude --version` succeeding proves the binary
exists, not that it is signed in. Re-auth on the mini with
`claude auth login --claudeai` before ever setting this seat back to claude. -->


# Task: user-authored routine agents (normie agents on someone else's box)

Goal: a non-technical person, from the Yaver **mobile app or web UI**, describes
an intent in plain language ("tell me when my health portal has a new result",
"ship TestFlight every Friday"). Yaver turns that into a **routine** — a
connector + a schedule + a notification — that runs periodically on a machine
they were **granted guest access to**. They never see the code. They can turn it
off from any surface.

This is NOT a new engine. The scheduler, the gateway, the connector registry,
the browser co-browse, and the notification fan-out all already exist and are
real. This task is **wiring, tenancy, and honesty** — joining things that were
built from opposite banks and never met.

Read `docs/architecture/AI_ARCH.md` before touching auth/relay seams.
**Docs drift. grep the code; when a doc disagrees with code, the doc is the
bug — fix it in the same change.**

## The shape (decided 2026-07-17 — do NOT re-litigate)

- **Host**: the owner's Mac mini (macOS). Generalizes later to self-host /
  managed cloud / BYO VPS, but macOS is the target that must work.
- **Access**: Yaver **guest access**. The guest is a real second user with their
  own `userId`, not a shared login.
- **Runner**: **the `opencode` runner on the `zai-coding-plan` provider. Never
  claude/codex.** This is already Yaver's design, not a new rule: `vibing.go:1567`
  forces a guest onto `pickReadyGuestVibeRunner` and 503s when none is ready
  (`:1571`), gated by `isSubscriptionRunner` (`:1641`). Guest access **slices
  compute/inference** — that slice is the opencode/GLM lane.
  - **Say "opencode + zai-coding-plan", never "the glm runner".**
    `tasks/glm-remove-runner.md` is deleting `builtinRunners["glm"]` (it sets
    `Command: "claude"` — the subscription-OAuth CLI driven by a z.ai API key,
    crossing the boundary `mcp_tools.go:1931` declares). After that task
    `supportedRunnerIDs = {claude, codex, opencode}`. **GLM survives as an
    opencode provider** (`opencode_config.go:528`, `zai-coding-plan/glm-4.7`),
    which is the lane built for API keys. Write this task against the
    post-deletion world.
  - **Why this lane is right, not a compromise**: it's key-based, so it needs
    **no OAuth** — a normie can never complete a Claude/Codex device-auth flow
    and has no subscription. It's also the **only per-user-able lane on any OS**,
    because a per-user key needs env injection, not OS-user separation. That is
    precisely why it sidesteps `tenant_runtime`'s Linux-only wall (below) and
    works on the Mac mini today.
  - **A subscription runner is never lent to a guest.** Not by grant, not by
    toggle. `apiKeyEnvBannedFor` (`autorun_runner_env.go:26`) already strips
    `ANTHROPIC_*` from runner env via `env -u`; `isSubscriptionRunner` is the
    predicate. The only work is applying the existing guard where it's missing.
  - Do not try to port `tenant_runtime` to Darwin in this task. This lane makes
    it unnecessary.

## Coordinate with `tasks/glm-remove-runner.md` (do NOT fight it)

That task and this one touch the same seam. Its axis breakdown governs; re-read
it before touching anything GLM-shaped. Summary of what it settles:

| axis | fate | relevance here |
|---|---|---|
| GLM as a **runner** (`Command: "claude"`) | **DELETE** | Never reference it. The guest lane is `opencode`. |
| GLM as a **model** (`glm-4.7`, `zai-coding-plan/glm-4.7`) | KEEP | P0.2 bumps the version here. |
| GLM as an **opencode provider** (`zai-coding-plan`) | KEEP | **This is the guest lane.** |
| GLM as the **yaver-agent control-plane provider** (`yaver_agent_config.go:47`) | KEEP — "different axis entirely, not a coding runner" | **This is P1's `infer` substrate.** |

Its HAZARD applies to us too: `GetRunnerConfig` (`tasks.go:236`) falls back to
`defaultRunner` for anything unknown, so a retired `glm` id **silently runs
claude** rather than failing. If this task lands first, do not introduce a new
`glm`-id caller. `glm_loop.go:262` returns `"glm"` and is on both tasks' path —
whoever lands second reconciles it. Visible failure over silent retry.
- **Accepted risk, stated once**: on macOS there is no per-tenant filesystem or
  vault isolation. The guest's portal cookies land in the owner's profile dir;
  the guest's routine runs as the owner's OS user. This is a deliberate
  family/trusted-peer tradeoff. It MUST be surfaced in the grant UI, not
  discovered. Do not ship this posture to strangers.

## Ground truth (audited 2026-07-17, 9 parallel code audits — do NOT re-litigate)

### What is REAL and must NOT be rewritten

- **Scheduler** — `scheduler.go` (774 ln). Supervised 30s tick
  (`SupervisedGo("scheduler", 30*time.Second, ...)`, `:163-170`), booted at
  `main.go:3359`. Dependency-free cron parser (`nextCronRun`, `:642+`) with
  `*`, lists, ranges, `*/N`, `@daily`/`@hourly`/`@weekly`/`@monthly` macros.
  Circuit breaker auto-pauses after N consecutive failures with a human-readable
  `PausedReason` (`:380-386`). Persists `~/.yaver/schedules.json`, 0600,
  reloaded before `Start()` (`:141`). **This is the spine. Build on it.**
- **One struct, two modes**: `ScheduledTask` (`scheduler.go:17-77`) forks on
  `Verb != ""` (`:234-237`). Verb-mode → `executeRoutine` → ops dispatcher
  (`:410`, wired `main.go:3278-3284`), fails closed if unwired (`:403-405`).
  Task-mode → `TaskManager.CreateTaskWithOptions` → a coding runner.
- **`schedule_self`** (`routines_mcp.go:151-273`) already IS a user-authored
  periodic agent: `prompt`, `memo`→`CarryNotes` (carried verbatim into the next
  run, `scheduler.go:243` — the continuity bridge across cold processes),
  `runner` pin validated by `IsSupportedRunner` (`:219-221`), cron/interval/once
  with an exactly-one-cadence check (`:198-213`), runaway cap 100 (`:147`).
- **`agent_schedule_intent.go`** — detects a prompt that implied recurrence
  ("every morning", "remind me", "from now on", regexes `:32-39`) but created no
  schedule, and asks once. Guarded: once per task, never for scheduler-spawned
  runs, never when the runner self-scheduled.
- **Gateway** — the compliant stack. `gateway_act.go:198-292` runs Policy Guard →
  velocity cap (file-backed via the real audit ledger, survives restart, `:238`)
  → dry-run preview (pure/offline, `:139-179`) → two-key confirm for
  high/financial (`:247-252`) → `Idempotency-Key` (`:342-344`) → exactly one
  audit line on every path (`:217-230`). Default is dry-run
  (`gateway_act_mcp.go:101-110`). **403/429/451 → `blocked_remote`, no retry,
  honest User-Agent (`:311-313`).**
- **Connector registry** — `~/.yaver/connectors/<id>.json`
  (`gateway_registry.go:155-162`), reloaded per call so on-disk edits need no
  restart (`:164-170`). Validation rejects inline secrets, act-verb-with-GET,
  and act capabilities without a risk tier (`:360-440`). **ZERO connectors ship.
  A fresh install has an empty registry. The path is entirely BYO — this is the
  feature, not a gap.**
- **`gateway_dynamic_tools.go`** — connector manifests auto-surface as
  `gw_<connector>_<capability>` MCP tools. **This is how "MCP does it at the
  end" already works.** The connector IS the agent.
- **OAuth** — `gateway_connect.go` is real OAuth2 authorization-code + PKCE on an
  ephemeral **127.0.0.1-only** loopback listener (`:114-119`); client secret in
  memory only (`gateway_mcp.go:249`); pasted-code fallback for headless
  (`:278-285`). Tokens → encrypted vault, project `gateway`
  (`gateway_creds.go:19-20`). Never Convex. `validateCredRef`
  (`gateway_registry.go:500-514`) rejects token-shaped strings.
- **`gateway_provide_otp`** (`gateway_gate.go:410-434`) — blocks on a human gate
  (`awaitHuman`, `:198-249`), pushes to the user's real phone
  (`:120-145`), FIFO-resolves the oldest `enter_code` gate for that connector
  (`:291-323`), consent-checked fail-closed (`:417`). Code is **never
  persisted**. Timeout → `GateExpired`; cancel → `GateAborted`, "never treat
  cancellation as approval" (`:243-248`). **This relays a factor the user
  legitimately received. It does not bypass one. Keep it that way.**
- **`browser_interactive_start`** — headful Chrome (`Flag("headless", false)`,
  `browser_interactive.go:98`) on a persistent profile. Human drives it **from
  the phone** (`mobile/app/browser-interactive.tsx`): single-JPEG polling
  (`:8` — RN can't do MJPEG in an `<img>`), taps POST to
  `/browser/interactive/input`, real CDP injection (`InjectClick`/`InjectKeys`/
  `InjectScroll`, `:173-215`). Handback is via the **shared profile dir**, not a
  session transfer: human solves login → cookies land in
  `~/.yaver/browser-profiles/<id>` → later headless `browser_open` with the same
  profile inherits the clearance (`browser.go:216-219`, `httpserver.go:15464`).
  **This is the OAuth-passthrough seam. It works. Use it.**
- **redroid login refusal** — `gateway_redroid_invoke.go:570` `detectIntegrityBlock`
  catches Play Integrity / SafetyNet / "this device isn't secure" and refuses,
  because (`:552-560`) *"an emulated / uncertified / rooted device can NEVER pass
  attestation... Retrying, self-healing, or asking a human are all futile."*
  Captchas → live human gate, **never auto-solved** (`:607`). 2FA routes by
  mechanism; passkeys fail clean (non-relayable). **This is the standard. Match
  it everywhere.**
- **`notify`** (`httpserver.go:7857` → `notifications.go`) — Telegram/Discord/
  Slack/Teams/Linear/Jira/PagerDuty/Opsgenie/Email, genuine HTTP
  (`sendTelegram`, `:248-271`). No push/mobile channel in the struct. Telegram
  pragmatically reaches a phone — **shortest real path to "text him".**
- **`ops mail_send`** (`ops_mail.go:74`) — real, SMTP, **routine-reachable**,
  dry-run by default requiring `execute=true, confirm:'send'`. **The email answer.**
- **`models_run`** (`httpserver.go:16324` → `models.go:243-273`) — real ollama
  `/api/generate` wrapper. Local-only. `models_serve` (`:281-311`) is a correct
  ollama supervisor with an honest not-found error.
- **Guest chokepoint** — `httpserver.go:2100` `allowGuest` →
  `isGuestAllowedPathForScopeVibe` before dispatch. Segment-aware matching with
  an exact-only set to stop `/agent/runners` → `/agent/runners/test` collisions
  (`guest_scope.go:328`, `:312`). `stripGuestRequestHeaders`
  (`httpserver.go:2118`, `:2136`) wipes inbound `X-Yaver-Guest*` and re-stamps
  from server state. **Header spoofing is already defended. Don't redo it.**
- **Subscription-key stripping** — `apiKeyEnvBannedFor`
  (`autorun_runner_env.go:26`) strips `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`/
  `OPENAI_API_KEY`, via `env -u` on the command line so the tmux server env
  can't reintroduce them (`autorun_tmux.go:109-114`). Genuinely good.
- **`gateway_runner_env.go`** — `cleanTenantEnv` (`:150`) strips host secrets;
  `gatewayInjectEnv` (`:183`) injects a per-tenant scoped gateway token, fails
  closed (`:190`, "no token → no provider, never the host key"). **The per-user
  inference lane. Reuse this pattern.**

### What is DEAD / ORPHANED (the actual work)

- **`glm_loop.go`** — a COMPLETE, correct, tested OpenAI-compatible agentic loop:
  chat-completion wire types (`:56-88`), a `bash` tool spec (`:90-108`),
  `RunGLMLoop` driving real tool round-trips with exit codes (`:115`).
  **ZERO production callers** — `grep RunGLMLoop` returns only its own tests.
  Header (`:29-33`) says "deliberately NOT auto-wired... a separate, reviewed
  step". **This task IS that step.**
- **`yaver_agent_config.go`** — real vault-backed BYOK provider config
  (`glm|anthropic|openai|openrouter`, `:47`), correct OpenRouter base URL
  (`:90`), write-only API key that never leaves the host (`:52-54`, `:62-66`),
  URL-scheme validation (`:184`), real tests. **`loadYaverAgentConfig` has
  exactly ONE caller: the HTTP GET handler (`:241`).** The key is stored,
  validated, protected — and never read by any inference path.
- **No LLM ops verb and no `notify` ops verb exist.** So "poll → ask → notify"
  on a cron today is `routine_create{verb:"run", payload:{command:"curl ..."}}`
  — a shell escape hatch, not a product.
- **`ghost_vision.go`** (`:5`, `:40`) — proof the HTTP-to-OpenRouter pattern
  already works in this codebase (gated behind `--ghost`, `ops_ghost.go:136`).

### What LIES (fix or delete — do not build on it)

- **`morning_*`** — all four (`morning_latest`/`list`/`show`/`rollback`) declared
  `mcp_tools.go:4368-4407` with confident descriptions. **NO HANDLER EXISTS.**
  `build_cache_git.go:547` references a `morning_cmd.go` that does not exist.
  Calling any → `unknown tool`.
- **~60 dropped MCP tools still advertised** — `say`, `weather`, `translate`,
  `calculate`, `timer`, `music`, `brightness`, `stock_price`, `world_clock`, …
  still in `mcp_tools.go`, dispatching to `mcp_dropped_stubs.go:77` →
  `{"error":"feature_removed"}` (cut 2026-04-28). The handlers went; the
  advertisements stayed. **Every model reading our tool list is lied to.**
- **`deploy_all.go:12-14`** claims preflight runs "`go build ./...` and the
  P0-P8 scoped test selector". `deployPreflight` (`:131-141`) runs **only
  `go build ./...`**. Then reports `gateStatus: "green"`. Ships a red suite.
- **`gateway_redroid.go:143` `Snapshot()`** returns `(serial, "snap-<unix>",
  **nil**)` having taken no snapshot. Caller stores the fabricated ref in the
  **vault**; `RestoreSnapshot` (`:150`) then always errors. A real snapshot
  engine exists in `studio/base.go`.
- **`gateway_redroid.go:124` `Tap(target)`** silently discards `target` and
  presses ENTER (keycode 66). `droidUIElements` (`droid_interactive.go:243`)
  already parses `bounds` — the data to do it right is in the same package.
- **`remote_runtime.go:191` `FeedbackSDKCompatible`** is a tautology of
  `executionMode`, and **inverted**: `true` for swift/kotlin (no SDK exists),
  `false` for react-native (SDK exists). Dashboard renders it as a green
  "compatible" box (`web/components/dashboard/ProjectsView.tsx:525-530`).
- **`multiuser.go:13-20`** promises "isolated user environment" with
  `/home/yaver-{short}`. Implementation is `os.MkdirAll(dir, 0700)` (`:166`).
  No `useradd`, no `chown`, no setuid. Namespacing cosplaying as tenancy.
- **`backend/convex/teams.ts`** — 10 internal-only functions, **zero public
  `query`/`mutation` exports**. Not callable from any client.
- **`guest_config.go:76`** — `case "idle-only": // TODO ... for now allow`.
  Silently allows.
- **`browser.go:236`** hardcodes a **Windows** User-Agent on every session, and
  `:235` sets `disable-blink-features=AutomationControlled` commented
  `// F2 anti-detect`. `:219` states the intent: "so a headless session looks
  less like a bot". **CLAUDE.md:47-48 forbids exactly this.** No 403/429/451
  backoff, no robots.txt handling anywhere in the browser stack.

### Hard walls (do NOT try to route around)

- **`tenant_runtime.go:100-102`** — `"tenant runtime is Linux-only"`.
  `tenantOSUsersEnabled()` (`tenant_osuser.go:32-38`) needs `runtime.GOOS ==
  "linux"` + `sudo` + `useradd`. **On macOS two users CANNOT have separate
  Claude auth.** There is one `~/.claude` and it is the owner's. Fail-loud is
  correct (`tasks.go:2516-2519` hard-errors rather than running unconfined).
  **Do not port this to Darwin in this task.**
- **Vault is single-tenant, per-OWNER.** Key derives from `cfg.AuthToken` — the
  owner's (`vault.go:899`, `main.go:333`). `VaultEntry` (`vault.go:47-57`) has
  `Name`/`Project`/`Category`/`DeviceID` — **no user dimension**. Guests reach
  `/vault` in no scope (`vault_http.go:195`). A per-guest vault is a redesign,
  **out of scope** — see P2 for the interim.
- **Convex CANNOT hold the agent body.** `convex_privacy_test.go` fences `goal`
  (commented "user-written natural language"), `command`, `gate`, `messageText`,
  and **`baseUrl`/`endpointUrl`** (`:216-219` — URLs embed auth tokens). The
  governing comment (`:150-154`): *"A free-text field is how content leaks under
  a respectable name."* Convex gets a slug/status/outcome/timestamp row modeled
  on `userActivity` (`schema.ts:2081`) and nothing more.
- **redroid is Linux-only** (`remote_runtime_redroid.go:54`, needs Docker +
  `CONFIG_ANDROID_BINDERFS`). iOS sim is macOS-only (`remote_runtime.go:355`).
  **The Mac mini gives iOS-sim + browser, NOT Android.** Google ships no
  linux/arm64 emulator binary (`remote_runtime.go:405`).
- **`routines_mcp.go:11-21`** declares routines MCP-only by explicit owner
  decision, with the auth reasoning "owner-only /mcp endpoint, so no per-tool
  guest check is needed". **P2 deliberately reverses this.** It is a conscious
  reversal, not an oversight — say so in the commit.
- `POST /schedules` already decodes `verb`/`machine`/`opsPayload`
  (`httpserver.go:4965`), so the "MCP-only" claim is **already** false. Reconcile
  the comment with reality.

## Hard constraints

- **Never commit secrets, infra IPs, hostnames.** Repo is public. The mini is
  `yaver-test-ephemeral`-class: refer to machines by alias only.
- **Never `go test ./...` in `desktop/agent`** — `TestAuthLogout` hits the real
  `~/.yaver` and signs the box out. Always `-run` scoped.
- **Only `git commit -- <explicit paths>`.** Never `git add -A` / `git add .` —
  a shared checkout sweep has eaten other sessions' work before.
- **Never `-p` headless for claude/codex** — it fakes "OAuth expired" even when
  the TUI on the same box is signed in. tmux TUI only.
- **No new API-key runners.** Subscription login or an explicitly-granted
  key-based runner. Never mint an `ANTHROPIC_API_KEY` path.
- **One deploy per converged change, and only when asked.** Do not deploy to
  check something. Do not touch TestFlight (~15-20/day, no rollback).
- **Do not "improve" the browser anti-detect flags into something worse.** P5
  removes them; until then leave them alone.
- Do not restyle web/mobile. Change content and wiring, not design.

## Phases (in order; each must gate green before the next)

### P0 — Close the live hole, and stop lying about GLM

**This ships independently of everything below. Do it first.**

1. **`guest_config.go:129-131` is fail-open on runner choice:**
   ```go
   if !ok || len(cfg.AllowedRunners) == 0 {
       return nil // no restriction
   }
   ```
   Full-tier guests reach `/tasks` (`guest_scope.go:91`); that path's only gate
   is `CheckRequestedRunner` → `CheckRunner`, which delegates here. **So a guest
   posts `{"runner":"claude"}` and spends the owner's Claude Max subscription
   today.** The correct guard already exists — `isSubscriptionRunner`
   (`vibing.go:1641`) — and is used at exactly one callsite (`vibing.go:1567`,
   the `/vibing` path, which correctly forces guests onto GLM/BYO and 503s when
   none is ready, `:1571`).
   **This is a missed slice, not a new policy.** `/vibing` already implements the
   intended design; `/tasks` never got it. Fix: gate `/tasks` with
   `isSubscriptionRunner` exactly as `/vibing` does — force guests onto the
   GLM/opencode lane, 503 when none is ready. **There is no grant that opens a
   subscription runner to a guest.** `AllowedRunners` stays what it is: an owner
   knob to *narrow* the guest's key-based choices, never to widen them into
   claude/codex.
2. **The guest lane's model is pinned to `glm-4.7`, which silently no-ops.**
   opencode returns exit 0 and ~29 bytes on `zai-coding-plan/glm-4.7` — its
   adapter can't read that model's reasoning reply. So the fail-closed guard in
   (1) currently routes guests onto a lane **that does nothing and reports
   success**. Bump to `glm-5.2` at `opencode_config.go:528`
   (`zai-coding-plan/glm-4.7`) and `httpserver.go:3487` (`IsDefault: true`) /
   `:3489` (`openrouter/z-ai/glm-4.7`).
   - **Run the CONTROL first.** Seven wrong hypotheses preceded this diagnosis
     last time. Prove the bump with `opencode --print-logs --log-level DEBUG` and
     a reply >29 bytes of real content before believing any other theory.
   - `tasks.go:228` (`Model: "glm-4.7"`) belongs to `builtinRunners["glm"]`,
     which `tasks/glm-remove-runner.md` **deletes**. Do not edit it; do not
     depend on it. If that task hasn't landed, leave the line alone.

GATE P0: `scripts/gate-routine-agents.sh p0` — a guest token POSTing
`{"runner":"claude"}` to `/tasks` is refused; no `glm-4.7` pin survives on the
opencode-provider axis; a real `zai-coding-plan/glm-5.2` round-trip returns >29
bytes of actual content. Scoped `go test -run` green. Pushed.

### P1 — The two missing verbs (this is the feature)

The bridge halves exist. Join them.

1. **`infer` ops verb** — reads `yaverAgentConfig` (the BYOK provider/key that
   nothing currently reads) and calls `RunGLMLoop` (the agentic loop nothing
   currently calls). Registered like any `ops_*.go` verb so `routine_create{verb:
   "infer"}` reaches it. Fail closed with an honest error when no provider is
   configured — never silently fall back to a host key (`gateway_runner_env.go:190`
   is the pattern).
   - Prompt/model/system in the payload. Tool access **off by default**:
     `RunGLMLoop`'s `bash` tool spec (`glm_loop.go:90-108`) must NOT be reachable
     from a guest routine in this phase.
2. **`notify` ops verb** — routine-reachable wrapper over the existing
   `NotificationManager` (`httpserver.go:7857`). No new channels.
   - **Bug to fix in passing**: `notify` currently calls `TestNotification(channel)`
     AND then sends the real message (`httpserver.go:7869-7883`) — **every call
     fires two messages**, one of them a test string.
3. **Dead-man's switch.** A routine that polls must be able to report "checked,
   nothing new" on a cadence, so **silence is itself a signal**. For lab results,
   a silently-dead agent is the dangerous failure — and `scheduler.go:628-635`
   makes that failure invisible today (below).
4. **`scheduler.go` silent data loss** — `load()` discards the
   `json.Unmarshal` error (`:628-635`); `save()`/`saveLocked()` ignore the
   `os.WriteFile` error (`:619`, `:625`). **A corrupt `schedules.json` boots with
   ZERO routines and no log line — every schedule silently gone.** Violates
   visible-failure-over-silent-retry. Fix: log loudly, refuse to start with a
   corrupt store rather than start empty, and surface it.
5. **Cron with no match in 48h** (`nextCronRun` brute-forces 2880 min) →
   `NextRunAt=""` → routine silently marked `completed` (`:446-454`). Reject
   such crons at create time instead.

GATE P1: `scripts/gate-routine-agents.sh p1` — a `routine_create` with
`verb:"infer"` + a cron produces a real model reply on a schedule; a
`verb:"notify"` routine delivers exactly ONE message; a corrupted
`schedules.json` fails loudly instead of booting empty; `infer` with no provider
returns an honest error and never touches a host key. Scoped tests. Pushed.

### P2 — Guests can own routines (the tenancy reversal)

1. **`/schedules` is in NO guest allow-list** (`guest_scope.go:90-230`), so a
   guest cannot create a timer at all. Add a **routine-scoped** guest path — NOT
   blanket `/schedules`. A guest may CRUD **only their own** routines; the
   `ScheduledTask` needs a `GuestUserID` dimension (mirror `task.GuestUserID`)
   and every read/write must filter on it.
2. **Per-guest inference key.** The guest's routine runs on the GLM/opencode
   lane with a key the **owner provisions per guest** — not the owner's personal
   key, and never a subscription runner (P0). `gateway_runner_env.go` is the
   existing pattern to copy: `cleanTenantEnv` (`:150`) strips host secrets,
   `gatewayInjectEnv` (`:183`) injects a per-tenant scoped token and **fails
   closed** (`:190`, "no token → no provider, never the host key"). Reuse it;
   don't invent a second mechanism. Record grant/revoke in the activity summary
   (`userActivity`, `schema.ts:2081` — action/target/outcome/timestamp only).
   This is the compute/inference slice: per-guest key, per-guest metering,
   revocable without touching the owner's own runner.
3. **Scope default fails open** — `guestScopeOrDefault` maps unknown/empty →
   `full` (`guest_scope.go:249`); `GetScope` returns `full` when config hasn't
   synced (`:346-355`). Convex defaults new invites to `feedback-only`
   (`guests.ts:259`). In the ~10s sync window (`httpserver.go:2046`) a fresh
   grant is transiently **full**. Fix the default to deny.
4. **`deploy` scope is unmintable dead code** — Go has a full allow-list
   (`guest_scope.go:195-212`), but `guests.ts:133` won't mint it and
   `schema.ts:1517` doesn't list it. Either wire it or delete it. Do not leave an
   aspirational allow-list.
5. **Guest credential interim.** The vault is per-owner and that's a redesign
   (out of scope). For this phase: a guest's portal credential lives in the
   owner's vault under a **guest-namespaced project** (`vaultKey(project, name)`,
   `vault.go:132`, is the existing namespacing seam — `ops_store.go:9-11` uses it
   for per-project ASC creds). **This is namespacing, not isolation — the owner
   can still read it.** The grant UI MUST say so in plain language. Do not
   describe it as private.
6. **tmux session names are not user-scoped** — `runner_pty.go:107`
   (`"yaver-" + runnerID`) and `autorun_tmux.go:70-76`. `new-session -A`
   *attaches* rather than erroring (`runner_pty.go:134`), so a second user's
   keystrokes land in the first's live TUI, silently. Add the user dimension.
   (`autorun_tmux.go:64-69` shows the author already reasoned about exactly this
   for the master/doer split.)
7. **Runner selection is process-global** — `switch_runner`
   (`httpserver.go:5999`) mutates one `s.taskMgr.runner` field under a mutex, not
   per-user, not persisted. A guest picking a runner mutates it for the owner's
   concurrent tasks. Per-task override already exists
   (`CreateTaskWithOptions(..., pickedRunner, ...)`, `vibing.go:1605`) — make
   selection per-user state, not machine state.

GATE P2: `scripts/gate-routine-agents.sh p2` — guest A cannot see, pause, or
delete guest B's routine, nor the owner's; a guest without a grant cannot use a
subscription runner; a revoked grant stops the next tick; two users' tmux
sessions do not collide; a guest routine's Convex row carries no prompt, no URL,
no free text. Scoped tests. Pushed.

### P3 — The off switch (parity across surfaces)

**Today a routine runs forever and neither party has a button.** `routine_pause`/
`routine_resume` exist and the circuit breaker works — but `mobile/app/schedules.tsx`
and the web dashboard Schedules tab (`web/app/dashboard/page.tsx:2006`) render
**task-mode only**: grep the mobile screen for `verb|routine|opsPayload` → **zero
hits**. Routines travel in the same `ListSchedules()` array and are invisible.

1. **Show routines** in mobile + web schedules UI. Stop filtering them out.
2. **Per-routine pause/delete** wired to the existing verbs.
3. **Per-connector revoke** that kills the **credential**, not just the timer —
   pausing a routine leaves the portal session live in the profile dir. These are
   different kill switches; both must exist.
4. **Owner master switch** — stop ALL guest routines on this machine, reachable
   from the owner's phone, no negotiation with the guest.
5. **Quota breaker** for deploy-shaped routines. Nothing reads any budget today:
   `grep quota deploy_all.go deploy_run.go publish.go autorun.go` → **zero**.
   `testflight_builds` (`httpserver.go:9502`) exists and **has no callers**. Wire
   it. `deploy_all` has **no coalescing/dedup** — no content hash, no
   "did HEAD change" check — so a flapping schedule sprays uploads against a
   ~15-20/day cap with **no rollback** (a bad build can only be superseded, which
   costs another upload). Add a converged-change check.
6. Per the cross-surface parity rule: the RN surfaces (mobile/tablet/car/glass)
   share `DeviceContext` and come free — **verify**, don't assume. Web is its own
   port. tvOS/watch: note the gap explicitly in the commit; don't silently skip.

GATE P3: `scripts/gate-routine-agents.sh p3` — a routine created via MCP is
visible and pausable in BOTH mobile and web; the owner master switch stops a
guest routine mid-schedule; a connector revoke invalidates the stored credential;
a deploy routine refuses to fire when the TestFlight budget is exhausted. Parity
table in the commit message. Pushed.

### P4 — Intent → connector (the normie path)

1. **`health_*` is honest scaffold, not a lie** — `mcp_health.go` returns static
   templates marked `"status": "template"`, with the header (`:3-9`) "These tools
   deliberately do not scrape or diagnose... Browser/redroid execution lands in a
   later slice" and `:133` "intentionally not executable until a user binds a
   visible browser/redroid session". **This task is that later slice.**
   **Generalize it.** A health portal must NOT be a Yaver feature. Turn
   `health_connector_template` into a **generic connector-authoring** path that
   emits a real `Connector` (`gateway_registry.go:132-143`) into the registry —
   any portal, any site. Delete the health-specific naming or reduce it to one
   example fixture.
2. **Bind the login.** `browser_interactive_start` → human solves login/2FA on
   his phone → cookies persist in the profile dir → the scheduled headless poll
   inherits clearance. Wire the template's "bind" step to this.
3. **OTP seam** — `gateway_provide_otp` is redroid-only today
   (`grep browser|chromedp|selenium gateway_act.go` → nothing). Wire it to
   `browser_interactive`'s `InjectKeys`/`Prefill` (`browser_interactive.go:186-233`).
   Both halves work; it's a short wire. **Keep the human gate. Never auto-solve.**
4. **Browser profiles are not namespaced per user** — flat
   `~/.yaver/browser-profiles/<id>` (`browser_interactive_http.go:18-24`). A
   guest's authenticated portal session and the owner's sit in one tree with a
   name between them. Namespace by guest userId.
5. **Chrome singleton lock** — two concurrent Chrome instances on one
   `user-data-dir` conflict; the code never guards this. A months-long poll must
   re-open (`cleanupLoop`, `browser.go:113`, idles sessions out), not hold. Guard it.
6. **Vault → browser is plaintext today.** `Prefill` (`browser_interactive.go:218-233`)
   takes a plaintext value straight from the MCP caller (`httpserver.go:15476-15480`);
   it reads no vault. A credential reaching a form transits the agent's context in
   the clear. Add a vault-ref path so the secret never enters a prompt.

GATE P4: `scripts/gate-routine-agents.sh p4` — a connector authored from a
generic template, bound via an interactive login, polls headlessly on a cron and
delivers a notification; nothing health-specific remains in the verb names; a
guest's browser profile is unreachable from another guest's routine; a credential
reaches a form without appearing in any prompt or log.

### P5 — Honesty pass (delete what lies)

Do these even if P1-P4 slip. Each is independent.

1. **Delete the four `morning_*` tool declarations** (`mcp_tools.go:4368-4407`)
   or implement them. Fix the dangling `morning_cmd.go` ref
   (`build_cache_git.go:547`).
2. **Drop the ~60 dropped-tool schemas** from `mcp_tools.go` —
   `mcp_dropped_stubs.go` fixed the build and left the advertisement. The stub
   file's header (`:1-16`) explains the tactical reason (a concurrent thread kept
   restoring the cases); the debt is now ours.
3. **`deploy_all.go:12-14`** — either run the tests the comment claims, or fix
   the comment. Do not report `gateStatus: "green"` for a build-only gate.
4. **`gateway_redroid.go:143` `Snapshot()`** — return a real error instead of
   `nil` + a fabricated ref. `studio/base.go`'s snapshot engine is right there.
5. **`gateway_redroid.go:124` `Tap(target)`** — use `droidUIElements`' parsed
   `bounds` instead of pressing ENTER and discarding the argument.
6. **`remote_runtime.go:191`** — rename to `feedbackTriggerAvailable` or actually
   probe the manifest. Stop telling Swift users an SDK exists.
7. **`gateway_act.go:202-203`** — delete the dead
   `tier := gatewayRiskTier(cap.Verb)` (every other callsite passes `cap.Risk`).
   It's `_ =`'d so there's no live exploit, but wiring it up silently downgrades
   every financial act to `riskUnknown` → no tap required.
8. **`gateway_registry.go:365`** — engine `"device"` is dispatched
   (`gateway_invoke.go:123`) and documented (`:135`) but rejected by
   `validateConnectorManifest`, and has no `gatewayActExecute` case
   (`gateway_act.go:280-291`). Support it or drop it from the docs.
9. **`guest_config.go:76`** — `"idle-only"` silently allows. Implement or reject.
10. **`guest_config.go:116`** — `DailyTokenLimit` meters **task-seconds**, not
    tokens, and prints `%.0f/%d seconds`. Rename it.
11. **`browser.go:235-236`** — remove the Windows UA spoof and the
    `// F2 anti-detect` flag; add 403/429/451 backoff-and-stop and robots.txt
    respect. `gateway_act.go:311-313` is the correct implementation to copy.
    **CLAUDE.md:47-49 is the rule; the code is currently its exact inverse.**
    If the owner decides the rule should change instead, change the RULE
    explicitly — do not leave the two contradicting each other.
12. **`routines_mcp.go:11-15`** — the "NOT exposed: HTTP routes" claim is already
    false (`POST /schedules` decodes `verb`/`machine`/`opsPayload`,
    `httpserver.go:4965`). Reconcile.
13. **`multiuser.go:13-20`** — either implement OS users or stop promising
    "isolated user environment" for `os.MkdirAll`.

GATE P5: `grep -rn "morning_latest" --include=*.go` returns only a deletion or a
real handler; no MCP tool in the schema dispatches to `droppedMCPStub`; no
browser session sets a UA it isn't; `go build ./...` green; scoped tests green.

## Out of scope (do NOT do these here)

- **Porting `tenant_runtime` to macOS.** Real work, security-critical, separate
  reviewed task. The Linux path already works.
- **A per-guest vault.** Redesign — `VaultEntry` has no user dimension and the
  key derives from the owner's token. P2 ships namespacing and says so honestly.
- **Retargeting `agent_graph_*`.** Every node bottoms out in
  `os.Stat(WorkDir)` (`agent_mode.go:249-252`) + `CreateTaskWithOptions` (`:895`);
  there are two node kinds and `autoideas` local exec is already deleted
  (`:970-975`); there is no graph file format or loader — `agent-graphs.json` is
  state output, not input. It's a DAG scheduler for coding runners in git repos.
  Different product; it happens to share a scheduler. **Do not build normie
  agents on it.**
- **Native push notifications.** `pushNotifications.ts` is complete but dormant:
  no EAS `projectId` in `mobile/app.json` (so `getExpoPushTokenAsync()` throws
  and `pushAuth.ts:46-49` swallows it), `sendDeviceAuthPush` has **zero callers**,
  and it's hardcoded to `"Approve sign-in"` (`:85-87`). Both files document their
  own dormancy accurately. 1-2 days, but not this task — Telegram + email cover
  the notification leg.
- **`voice_speak` as a notification path.** `voice_mcp.go:81-112` writes a
  BlackBox command **no client parses** (grep outside Go → docs only, all future
  tense). It also needs a live WebSocket, so it can't wake a phone regardless.
- **Android/redroid on the mini.** Linux-only. Needs a second box.
- **Deploying anything.** Commit + push only.
