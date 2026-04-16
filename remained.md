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

## Notes for whoever runs this on another machine

* Build: `cd desktop/agent && go build ./...`
* Local smoke test: `go run . autodev sfmg --plan` (dry run)
* Full autodev run: `yaver autodev sfmg --hours 8 --deploy testflight`
* Status from anywhere: `yaver autodev status`, or the mobile Auto
  Dev tab, or `GET /autodev/reports` over P2P / relay.
