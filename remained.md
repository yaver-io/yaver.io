# remained — yaver.io

Autodev checklist for the yaver.io repo. Each unchecked item is a
focused, shippable unit of work. `yaver autodev yaver.io` picks
the first `- [ ]`, implements it, checks it off, commits, pushes.

## TestFlight / iOS build unblock (highest priority)

- [ ] Port the Objective-C++ guest-bridge + Hermes validator code
      from `mobile/ios/Yaver/AppDelegate.mm` into a new Swift
      `AppDelegate.swift` (the pbxproj already references it).
      Use `~/Workspace/sfmg/ios/sfmg/AppDelegate.swift` as the
      starting template, then re-add the yaver-specific logic:
      guest bridge via `ExpoReactNativeFactory`, bundle validation
      via `YaverBundleValidator`, and the `safeReloadBridge`
      deallocation-poll. The existing .mm file contains the
      reference implementation — translate it to Swift.

- [ ] Create `mobile/ios/Yaver/YaverHTTPServer.swift` — an empty
      placeholder is not enough. This is the on-device HTTP
      server that accepts bundles pushed by the `yaver-cli` npm
      package (see `cli/src/transport.js`). Port from
      `cli/src/http-server-swift-template.swift` if it exists,
      or write one using GCDWebServer on port 8347.

- [ ] Create `mobile/ios/Yaver/YaverInfo.swift` — native module
      that exposes `isYaver`, `hardwareID`, `version`, `platform`
      to React Native. Small bridge — ~50 lines. Mirror the
      existing `YaverInfo.m` or port from Android's
      `YaverInfoModule.kt`.

- [ ] Create `mobile/ios/Yaver/YaverBundleValidator.swift` —
      validates Hermes bytecode version against
      `sdk-manifest.json` before loading a pushed bundle. Port
      the logic from the Go side (`desktop/agent/bundlecheck.go`
      `ValidateHBC`) to Swift: read HBC header, compare magic
      `0x1F1903C1` at offset 4 and BC version at offset 8
      against the manifest's `hermes.bytecodeVersion`.

- [ ] Verify the archive builds end-to-end with
      `./scripts/deploy-testflight.sh`. The Podfile already has
      `RCT_USE_PREBUILT_RNCORE=1` so the `fmt` consteval bug is
      gone. The sdk-manifest has been copied into
      `mobile/ios/Yaver/`. Once the Swift files exist the
      archive should complete.

## Autodev logs viewable from mobile (P2P)

- [ ] Tee each kick's stdout/stderr into
      `~/.yaver/autodev-logs/<loop>/<iter>.log` during
      `runAutodevLoop`. Redirect `os.Stdout` / `os.Stderr` to
      a tee writer that also writes to the log file, so the
      terminal user still sees real-time output and the log is
      a full transcript.

- [ ] Add `GET /autodev/reports/logs?name=<loop>&kick=<n>` that
      returns the raw log for one kick, plus `name` without
      `kick` to return a tar.gz of all logs for the run.

- [ ] Add MCP tool `autodev_logs` that wraps the same endpoint.

- [ ] Extend mobile Auto Dev tab to open a log viewer when a
      user taps a kick row in the report view.

## Autodev run summary

- [ ] Extend `AutodevReport` with a `Summary` block that records,
      per kick: duration in seconds, runner actually used (primary
      vs fallback), tokens consumed, and a short title for the
      feature implemented (parsed from the commit message).
      Roll up to per-runner totals at the end of the run.

- [ ] Add quota-tracking: read the current Claude / Codex /
      whatever session window's usage after each kick and record
      cumulative tokens in the report. Hit the runner's existing
      session-window tracker (see `loop_cmd.go` defaultProviderLimits).

- [ ] Render the summary in `printAutodevPlan` (end-of-run) and
      in the mobile Auto Dev tab's report view.

## Autodev — small quality-of-life

- [ ] Add a `--dry-run` alias for `--plan` (more intuitive).

- [ ] When the schedule hits a provider that's rate-limited, log
      the fallback explicitly: "claude-code 5h window full, falling
      back to codex". Currently the fallback is silent.

- [ ] `yaver autodev stop <project>` as a shortcut for
      `yaver loop stop <project>-autodev && yaver loop stop <project>-autodev-regression`.

- [ ] When the autodev run ends, print a one-line summary to stdout
      even in non-interactive contexts: commits landed, tests run,
      deploy result, total time.

## Google Play internal test deploy (needs keys)

- [ ] When `keys/google-play-service-account.json` exists locally,
      `./scripts/deploy-playstore.sh && python3 scripts/upload-playstore.py`
      should land a fresh AAB on the internal testing track.
      Blocked on the service account key file being present.

## Mobile UI polish — Auto Dev tab

- [ ] Show the running loop's kick counter, last commit subject,
      and deploy status in the Auto Dev tab list row.

- [ ] Tapping a row opens the per-run report with a kick-by-kick
      list the user can tap to view the diff and a checkbox to
      mark for revert.

- [ ] "Revert selected" button calls `POST /autodev/reports/revert`
      with the ticked SHAs.

## Solo-dev SaaS replacements — queued for next batches

### #5 — Read-only DB admin UI (TablePlus / DBeaver replacement)

- [ ] `desktop/agent/db_admin.go` — owner-only read-only SQL
      browser for SQLite and Postgres. Endpoints:
      `GET /db/connections` list configured connections,
      `POST /db/connections` add `{kind, dsn, label}`,
      `GET /db/tables?conn=…` list tables with row counts,
      `GET /db/rows?conn=…&table=…&limit=&offset=&where=…`
      page through rows, `POST /db/query?conn=…` run an ad-hoc
      read-only SQL statement (reject anything outside
      SELECT / EXPLAIN / PRAGMA / SHOW). Connections persist in
      `~/.yaver/db-connections.json`.
- [ ] Extend doctor to check for `sqlite3` CLI and `psql`.
- [ ] Mobile: new "DB" pane inside Studio under More — table
      picker, row list (virtualised), tap row to see JSON,
      SQL input at the bottom.
- [ ] MCP tools: `db_connect`, `db_tables`, `db_rows`, `db_query`.
- [ ] Doctor: add checks for sqlite3 and psql binaries.

### VS Code extension for Copilot-lite

- [ ] `editor/vscode-copilot-lite/` — tiny extension that
      registers an InlineCompletionItemProvider, streams the
      current prefix + suffix window to
      `${YAVER_AGENT}/copilot/complete` over SSE, and renders
      tokens as they arrive. One setting: `yaver.agent` (URL +
      bearer token). Ships as a .vsix the dev sideloads.

### Analytics mobile UI

- [ ] New "Analytics" pane inside Studio that hits
      `/analytics/summary`, `/analytics/top`, `/analytics/funnel`,
      `/analytics/retention` and renders them with plain-React
      Native views (no chart lib — pixel bars are fine).

### Mail classifier inline controls

- [ ] In `mobile/app/(tabs)/mail.tsx`, add a small "Mark as
      bulk / personal" two-button row on each message card that
      calls `POST /mail/mark`. Show a brief toast confirming the
      verdict was recorded.

## Autodev "human-like simulation" view (Apr 2026)

Re-shape autodev's live output so the user (terminal, mobile, web)
sees a structured chat: yaver's voice on one side, the AI runner's
voice on the other, tool uses as small inline tags, runner-agnostic
across Claude / Codex / Aider / Ollama. Same pattern as the
existing tasks chat in mobile.

- [x] Define a structured event schema published over /streams/{name}
      instead of raw text lines. Event kinds:
      * yaver_say     — what yaver sent to the runner (kick prompt, refill, regression)
      * runner_action — tool use: { runner, tool, detail }
      * runner_text   — assistant chatter
      * runner_result — { runner, status, duration_ms, cost_usd }
      Each event JSON-encoded, one per SSE frame, backwards-compatible
      additive (legacy "line" frames still allowed).
- [x] Update LogStream / streamPublisher to accept structured events
      alongside string lines. Publisher API: AppendEvent(kind, fields).
- [x] phaseThink emits yaver_say before each spawn with a 1-line
      prompt summary (target, mode, focus).
- [ ] spawnClaudeCode / spawnCodex / spawnAider / spawnOllama each
      emit runner_action + runner_text + runner_result via a shared
      adapter (so adding a runner only requires implementing the
      event mapping, not duplicating UI logic).
- [x] CLI `yaver stream` renders structured events as colored,
      indented chat-style output (yaver=cyan, runner=white, tools=dim).
- [x] Mobile Auto Dev tab adds a per-loop chat view that subscribes
      to /streams/autodev:<loop> and renders bubbles by kind. Same
      EventSource pattern the tasks chat already uses.
- [ ] Web dashboard mirrors the mobile chat view.
- [ ] Add an `events_only` query param to GET /streams/{name} that
      filters out raw text frames so older pre-structured publishers
      don't pollute the chat UI.
- [x] Test: extractRefillTitles-style unit tests for the event
      adapter — verify each runner's output translates into the
      right event sequence.

## Autodev follow-ons (Apr 2026 wave)

- [ ] Optional Claude --resume across kicks (warm cache, big cost
      savings on long runs). Persist session_id per loop in
      ~/.yaver/loops/<name>/claude_session.id; spawnClaudeCode
      passes --resume <id> on subsequent kicks. Already wired in
      parseClaudeStream return tuple, just needs the read/write +
      flag plumbing in spawnClaudeCode.
- [ ] Same for Codex (--resume) and Aider (--restore-chat-history).
- [x] Replace `time.Sleep` between kicks with a context-cancellable
      ticker so `yaver loop stop` interrupts mid-sleep instead of
      waiting up to 5 min for the current sleep to finish.
- [ ] When --auto-branch is on, push to the branch + open a draft
      PR after the deploy step so overnight runs land as a single
      reviewable PR rather than committing-as-you-go on a branch.
- [x] /autodev/options should also report which deploy targets the
      project actually ships to (testflight + convex + vercel +
      playstore) so mobile can pre-check the right boxes.
- [ ] Mobile "start autodev" form: render engine + harden + auto_ideas
      + auto_branch + deploy targets pulled from /autodev/options.
      Current mobile autodev start UI predates these fields.
- [ ] CI: extend hybrid-local.yml to exercise --engine hybrid via
      `yaver autodev <fixture> --engine hybrid --max-iterations 1`
      against a checked-in tiny project so the planner+implementer
      path is smoke-tested per PR.

## Autoideas (Apr 2026)

`yaver autoideas <project>` is a sibling of autodev that ONLY
generates ideas — appends `- [ ] <title>` lines to `ideas.md` (or
`--output`) on a timer, runs detached, mirrors autodev's flag set
(`--hours`, `--lite/--heavy`, `--prompt`, `--harden`, `--engine`,
`--hybrid`). Mobile / web shows the file as checkboxes; user
selects items to implement and triggers `autoideas_select` /
POST /autoideas/select / MCP `autoideas_select` which materialises
a curated checklist and starts an autodev run with --remained
pointed at it. Generation continues in parallel with implementation.

Wired in this commit:
  CLI:   `yaver autoideas <project> [...flags]`
  HTTP:  POST /autoideas/start, GET /autoideas/file, POST /autoideas/select
  MCP:   autoideas_start, autoideas_file, autoideas_select

Still to do:

- [ ] Mobile Auto Dev tab gets an "Ideas" section that fetches
      `/autoideas/file?work_dir=…` every few seconds, renders one
      checkbox per item, "Select all" and "Implement selected"
      buttons. "Implement selected" calls `/autoideas/select` with
      the picked line numbers and switches to the Chat section.
      Also needs a "Generate more" button that calls /autoideas/start.
- [ ] Web dashboard mirrors the same Ideas pane.
- [ ] Yaver-to-yaver: when the user picks "Run on <other-device>"
      from the mobile Ideas pane, the call goes through the
      existing P2P/relay device-routing layer (same one
      yaver_handoff uses) instead of hitting localhost. The HTTP
      contract is identical — just resolve the target device's
      base URL via resolveDeviceURL.
- [ ] CI: smoke test `yaver autoideas <fixture> --plan` so the
      flag/help surface stays linted per PR.

## Smarter session-limit pacing (Apr 2026)

Lite mode already paces autodev / autoideas / autotest at 5 min
between kicks and routes through `pickRunnerWithinLimits`, but the
provider window tracking is approximate and the user-facing log
doesn't surface what's happening. Make it explicit and accurate so
an "8 hour autodev" run actually finishes 8 hours of work without
exhausting Claude's 5h session window mid-run.

- [ ] Read the runner's actual session window state (Claude's
      `~/.claude/sessions/<id>.json` or whatever surface the CLI
      exposes for "remaining tokens / minutes in this 5h window")
      before each kick. Skip / sleep / fall back when usage > 80 %.
- [ ] Print a per-kick line: "claude window: 23 % used, 3h12m
      remaining" so the user tailing the stream sees pacing.
- [ ] When the configured `--hours` exceeds the runner's session
      window, automatically span across windows by sleeping past
      the boundary instead of stalling on a 429.
- [ ] When `--load lite` and the user is interactively using the
      same Claude session in another terminal, back off harder
      (sleep through any concurrent foreground claude process).
- [ ] /autodev/options reports the detected window limits per
      runner so mobile / web can display "Claude session: 4h12m
      remaining" alongside the start form.

## Codex parity (Apr 2026)

`--engine codex` now routes all four entry points (autodev /
autoinit / autoideas / autotest) through `spawnCodex` and through
`RunAIGenerator`'s codex adapter, so codex CLI users get the same
flag set. Two follow-ons remain to bring it to full Claude parity:

- [ ] Codex live event publishing: parse `codex --json` (or
      whatever the current CLI's structured-event flag is) and emit
      `runner_action` / `runner_text` events through
      AutodevPublishRunnerAction / Text — same chat-bubble UX
      Claude already gets via parseClaudeStream. Today codex output
      shows up as legacy "line" frames inside the yaver_say /
      runner_result bubble pair, which works but is less rich.
- [ ] Codex session-window tracking equivalent to Claude's: detect
      remaining quota on Plus / Pro plans and surface "codex
      window: 41 % used" lines per kick, same way the planned
      Claude-window pacing work will work.

## Claude opus+sonnet split inside hybrid mode (Apr 2026)

The Apr 2026 user feedback: Claude Max 20x ($200/mo) burns its
weekly bucket in 2-3 days when most work runs on Opus, but only
12% of the user's bucket usage came from Sonnet — so the asymmetry
is "default to Sonnet, escalate to Opus only when needed". Today
yaver supports:

- `--model sonnet|opus|haiku` flag — picks ONE model for the whole
  autodev run via YAVER_CLAUDE_MODEL → claude --model <id>.
- `--engine hybrid` — Claude planner + local Aider+Ollama
  implementer (free implementations, but Ollama quality varies).

What's still missing: a Claude-only hybrid where Opus PLANS and
Sonnet IMPLEMENTS each kick, so the cheap-default user pays Opus
prices only on the planner subtask and Sonnet prices on the
implementation subtasks. Concrete plan:

- [ ] Add `--engine claude-split` (also `--planner-model` /
      `--implementer-model` flags). Behaves like `--engine hybrid`
      structurally but the implementer is `claude --model
      claude-sonnet-4-6` instead of aider+ollama.
- [ ] Extend `HybridSpec` with an Implementer = "claude" branch
      (today the implementer hardcodes aider). hybrid.go's per-
      subtask spawn dispatches to `spawnClaudeCode`-with-
      sonnet-model when Implementer is "claude".
- [ ] /autodev/options reports the new engine + the model
      breakdown so the mobile / web start form can render an
      "Opus plans, Sonnet implements" preset.
- [ ] /autodev/cost splits cumulative spend into planner vs
      implementer columns when running in claude-split mode so the
      user can see the 5x cost asymmetry directly.

## Notes for whoever runs this on another machine

* Build: `cd desktop/agent && go build ./...`
* Local smoke test: `go run . autodev sfmg --plan` (dry run)
* Full autodev run: `yaver autodev sfmg --hours 8 --deploy testflight`
* Status from anywhere: `yaver autodev status`, or the mobile Auto
  Dev tab, or `GET /autodev/reports` over P2P / relay.
