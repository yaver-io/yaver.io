# CI / Validation Notes

## Verified

- `cd mobile && npx tsc --noEmit`
- `cd web && npx tsc --noEmit`
- `cd e2e && E2E_SKIP_LIVE_AUTH=1 npx playwright test tests/dashboard-autodev.spec.ts`
- `cd desktop/agent && go test -run 'TestRenderStreamEvent|TestLogStream' -count=1`
- `bash -n scripts/test-yaver-to-yaver-local.sh`

## Fixed During Validation

- `desktop/agent/remote_yaver.go`
  yaver-to-yaver `--to` calls now try relay first and fall back to direct HTTP instead of hard-failing on a dead relay path.
- `desktop/agent/stream_cmd.go`
  remote stream tailing now uses the same relay-to-direct fallback path.
- `scripts/test-yaver-to-yaver-local.sh`
  always builds a fresh binary instead of trusting a stale `desktop/agent/yaver`.
- `scripts/test-yaver-to-yaver-local.sh`
  now uses the full target device id for remote calls, and the truncated 8-char id only for inventory matching.
- `scripts/test-yaver-to-yaver-local.sh`
  can reuse an already-running local agent on `:18080` as the target, which avoids false failures when this machine already has Yaver running.

## Real End-to-End Result

I ran:

```bash
RUNNER_SPEC=codex ./scripts/test-yaver-to-yaver-local.sh autodev
```

What succeeded:

- controller agent came up on `:18081`
- existing local agent on `:18080` was reused as the target
- remote `yaver autodev ... --to <device>` reached the correct target
- target accepted `POST /autodev/start`
- fixture repo got `.autodev.loop.yaml` created
- authenticated `/autodev/loops` showed `fixture-autodev`

What failed:

- the loop settled in:

```json
{
  "name": "fixture-autodev",
  "status": "stuck",
  "iterationCount": 7,
  "lastSummary": "AI runner failed",
  "runner": "codex"
}
```

- fixture code never changed
- no `autodev_fixture-autodev-latest.log` file was created under `/tmp/yaver`

## Current Highest-Signal Blocker

The remaining blocker is no longer transport or routing. It is the actual runner execution path for remote/local autodev on this machine:

- autodev loop starts
- loop kicks repeatedly
- runner resolves to `codex`
- loop ends `stuck` with `AI runner failed`

This is the next thing to debug.

## Next Checks

1. pull the live `autodev:fixture-autodev` stream with auth and capture the exact Codex failure text
2. inspect the autodev kick path for why HTTP-started loops are not leaving a `/tmp/yaver/autodev_fixture-autodev-*.log`
3. run the same peer-local autodev harness with `RUNNER_SPEC=claude` to separate a Codex-specific failure from a generic HTTP-started loop failure
