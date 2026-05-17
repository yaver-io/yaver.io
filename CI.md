# CI / Validation Notes

## Verified

- `cd mobile && npx tsc --noEmit`
- `cd web && npx tsc --noEmit`
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
