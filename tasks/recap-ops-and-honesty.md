---
doer: codex
---

# Recap: the ops surface, and the honesty gap in `verified`

## Why this exists

`feat(recap)` (75f72bdee) landed the recap engine: it builds a narrated MP4 of
what an autorun run did, from screenlog frames the box already keeps. Read
`desktop/agent/recap.go` first — its header is the contract, and the "WHERE THE
BYTES LIVE" and "WHY A SIDECAR VTT" paragraphs are decisions, not preferences.
Do not relitigate them.

Two things are missing. One is plumbing. The other is a correctness bug that the
feature's own author found and did not fix.

## Ground rules

- Do the priorities **in order**. Each is one increment the gate can verify.
- **Gate: `cd desktop/agent && go build ./...`** and the scoped recap tests:
  `cd desktop/agent && go test -count=1 -run '^(TestRecap|TestPaceFrames|TestBuildRecap_|TestTimeRecapCues_|TestSelectRecapFrames|TestWriteVTT|TestWriteWAV|TestAutorunRunLooksBad|TestPruneRecaps|TestListRecaps|TestParseRecapScriptJSON|TestVTTTimestamp|TestWriteConcatList|TestFfmpegConcatEscape|TestPcmDurationSec|TestBuildRecapPrompt)' .`
- **NEVER run bare `go test ./...` in `desktop/agent`.** `TestAuthLogout` hits the
  real `~/.yaver` and will sign this machine out mid-run. Always pass `-run` with
  an anchored pattern. This is not a style note; it has cost real time before.
- Scope is `desktop/agent/**` only. No new dependencies. Do not touch `web/**`,
  `mobile/**`, `tvos/**` — surfaces are a follow-up loop.
- ffmpeg stays a **soft** dependency: `LookPath` and degrade, never assume.

## P0 — the ops/MCP verbs do not exist

`recap_http.go` wired the HTTP routes (`/recaps`, `/recaps/build`, `/recap/<id>`,
`/recap/<id>/video|poster|subtitles.vtt`, `/recap/config`). Nothing wired the
`ops` grand-tool or MCP, so no agent, phone, or runner can ask for a recap — only
a raw HTTP caller can. Every neighbouring feature has both.

- Add `desktop/agent/recap_ops.go` with verbs: `recap_list`, `recap_show`,
  `recap_build`, `recap_delete`, `recap_config_get`, `recap_config_set`.
- Follow `ops_glass_pc.go` / `ops_studio.go` for the handler shape and how verbs
  register. Follow `ops_deploy.go` only for what NOT to do — see P2.
- `recap_list` takes `autorun`, `slot`, `tag`, `limit` and maps onto
  `listRecaps(RecapFilter{…})`. The `(autorunId, tag)` pair is the address; a
  listing without it is a recency guess.
- `recap_build` is **owner-only** and must stay so: it spends CPU on a box that is
  usually mid-build, disk, and — with narration — inference tokens.
- A recap resolves to bytes over HTTP, so verbs return the `recapId` + the route,
  never the bytes and never a filesystem path. `recapSlotLabel` exists because a
  slot embeds an absolute task path; use it. A path in an MCP reply is a leak.

## P1 — `verified` says a run worked when it did one ninth of its task

This is the real bug, and there is a worked example.

`recapVerified` (`recap.go`) returns true when `Commits > 0 && FinalCommit != ""`.
That was written to defeat `3a32a4fc3` — a runner claiming DONE without landing
anything. It does defeat that. But it proves only that **commits exist**, not that
**the task is complete**, and the recap then narrates "Landed N verified commits",
which reads as success.

The counterexample is in the git log. Run `ed9311d1a` ("final autorun commit for
deploy-orchestration (task marked DONE)") ended after 2 iterations with 2 real
commits and a passing gate — having implemented part of **P0 of nine priorities**.
`recapVerified` calls that verified. A recap of it would have told you the night
went well.

- Split the concept. `Commits > 0 && FinalCommit != ""` is **`landed`** — work
  reached the tree. It is not **`complete`** — the task's definition of done was
  met. Name them separately in `RecapRecord` and stop using one to imply the other.
- Completion cannot be inferred from the loop's own report, because the loop is
  the thing making the claim. Derive it from the **task file**: count its `## P<n>`
  priorities and check the progress file for verified evidence per priority.
  Where that is not derivable, the honest value is `unknown` — never `true`.
- `recapCloser` must not say "Landed N verified commits" full stop when the task
  has nine priorities and the run touched one. Say what landed AND what remains.
  Copy the tone already there: state the claim as a claim, the evidence as
  evidence.
- `autorunRunLooksBad` should treat "claimed DONE but the task's priorities are
  not all evidenced" as bad — that is exactly the `failure` cut worth watching.
- Add a test named for the real run: assert a 2-commit, 2-iteration run against a
  nine-priority task does NOT report complete.

## P2 — `morning_*` and `record_*` advertise this feature and do not exist

`mcp_tools.go` declares `morning_latest`, `morning_list`, `morning_show`,
`morning_rollback` (~:4331) and `record_start`, `record_stop`, `record_drivers`
(~:4372). The record_* descriptions promise
`/recordings/{run_id}/{task_id}/video.mp4` for a "morning reel" — i.e. precisely
this feature. **None has a dispatch case.** Calling any of them today returns
`unknown tool` from the `default:` at the end of the dispatcher.

`morning_cmd.go`/`morning_http.go` were deleted in `0185942ff` to unbreak the
build; the declarations were never removed. `say` (~:1603) is the same disease —
declared, but `mcp_dropped_stubs.go` returns `feature_removed`.

The tool list is a promise. A schema with nothing behind it is a lie that costs
an agent a whole turn to discover.

- **Delete** the four `morning_*` and three `record_*` declarations. Do not
  reimplement them: `recap_list`/`recap_show` (P0) already answer "what happened
  overnight", keyed by the run instead of by the wall clock.
- Add one test that walks every entry in the MCP tool list and asserts the
  dispatcher does not return `unknown tool` for it. This class of rot has now
  produced three separate phantoms; a test is the only thing that stops a fourth.

## P3 — retention runs only after a successful build

`pruneRecaps` is called at the end of `BuildRecap`, so a box whose builds keep
failing never prunes, and a box that stops making recaps keeps the old ones
forever. `~/.yaver/clips` is the cautionary tale: no cap, no prune, grows until
the disk does — on a machine where autorun already reclaims caches to stay above
`autorunDiskFloorGB`.

- Prune on agent start too, next to `pruneOldScreenlogSessions` (`screenlog.go`),
  which already does exactly this and is the pattern to copy.
- Prune on the failure path as well, not only the success path.

## Definition of done

Say DONE, alone on its own line, only when P0–P3 are complete, each verified in
the git log, and the gate passes. A run that ends with its work still on a branch
is **not** DONE — landing is part of converging.

Do not say DONE because this file contains the word DONE.
