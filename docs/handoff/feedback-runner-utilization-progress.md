# Yaver autorun progress
## 2026-07-19T04:09:50Z

Iteration 1: SCOPE FAILED. Runner changes were removed from the worktree and preserved in a diagnostic git stash.

changes outside autorun scope: desktop/agent/ping_cmd.go, desktop/agent/ping_cmd_test.go, mobile/app/(tabs)/tasks.tsx, mobile/src/components/RemoteBoxPickerModal.tsx, mobile/src/context/DeviceContext.tsx, mobile/src/lib/connectionManager.ts, mobile/src/lib/deviceStatus.test.ts, mobile/src/lib/deviceStatus.ts, mobile/src/lib/deviceStatusRunnerProbe.ts

## 2026-07-19T04:09:50Z

autorun: final autorun commit for feedback-runner-utilization (scope violation)

This is the final autorun commit for task feedback-runner-utilization. No further autorun commits will follow for this run.

Finish reason: scope violation
Iterations run: 1
Verified commits kept: 0
Runner: codex (doer — implemented each iteration)
Master: opencode (planned each iteration; did not edit)
Gate: ./scripts/gate-webrtc-vibe.sh
Machine at finish: disk 7.9 GB free, RAM 8.0 GB, 8 CPUs, load 6.06 (0.76/core)

The run ended on an error. Its code changes were not kept; they are preserved in a diagnostic git stash:
iteration 1 violated scope; changes were preserved in a diagnostic git stash: changes outside autorun scope: desktop/agent/ping_cmd.go, desktop/agent/ping_cmd_test.go, mobile/app/(tabs)/tasks.tsx, mobile/src/components/RemoteBoxPickerModal.tsx, mobile/src/context/DeviceContext.tsx, mobile/src/lib/connectionManager.ts, mobile/src/lib/deviceStatus.test.ts, mobile/src/lib/deviceStatus.ts, mobile/src/lib/deviceStatusRunnerProbe.ts

## 2026-07-19T04:31:00Z

Manual recovery after the autorun scope-violation finish restored the diagnostic stash into this worktree and reran the gate. Verified changes now present in the working tree:

- `desktop/agent/ping_cmd.go`: `yaver ping primary|secondary` resolves elevated slots before matching device rows.
- `mobile/src/lib/connectionManager.ts` and `mobile/src/context/DeviceContext.tsx`: network-change and foreground reconnects target the focused pooled client, avoiding stale Mac mini paths after Wi-Fi/cellular changes.
- `mobile/app/(tabs)/tasks.tsx`: cached task filters and Clear remain usable while disconnected/project scanning; Clear is local-first and server cleanup is best-effort.
- `mobile/src/lib/deviceStatus*.ts` and `mobile/src/components/RemoteBoxPickerModal.tsx`: `/agent/runners` failures are surfaced as `timed-out`, `http-error`, or `network-error` instead of collapsing into "no runners installed".

Verification:

- `cd desktop/agent && go test -count=1 -run 'TestResolveReachPingLookupHintElevatedSlots|TestRemoteAgent|TestRemoteStatus' .`
- `cd mobile && npx tsc --noEmit && npx tsx src/lib/deviceStatus.test.ts && npx tsx src/lib/probeWithRepair.test.ts`
- `./scripts/gate-webrtc-vibe.sh` ended `GATE: PASS`.
- Live patched probe: `go run . ping primary --timeout 10s` reached `Mobiles-Mac-mini.local [229aeb03…] via direct`, agent `1.99.316`, lifecycle `ready-to-connect`.
- Ubuntu 4 GB public probe: `yaver ping 5e79cf10 --timeout 10s` reached `ubuntu-4gb-hel1-1 [5e79cf10…] via public`, agent `1.99.316`, lifecycle `ready-to-connect`.
- Ubuntu 4 GB exec remains blocked: `yaver exec --device 5e79cf10 -- true` failed with `device not connected to relay`.

No TestFlight, npm, Play, Cloudflare, Convex deploy, push, or publish was run.

## 2026-07-19T07:50:00Z

Additional closed-loop findings:

- Ubuntu 4 GB was re-registered as `2ed7da41…` / alias `linux-3` in the current device list; the remote agent itself still reports its local config device ID as `5e79cf10…`.
- `yaver exec -direct --device linux-3 -- 'yaver auth status'` confirms Ubuntu is already signed in as the same Yaver account.
- From Ubuntu, `yaver ping 229aeb03 --timeout 10s` reaches the Mac mini.
- From Ubuntu, `yaver primary status --json` reaches the Mac mini via `http://100.89.155.25:18080` (Tailscale path).
- From Ubuntu, `yaver exec --device 229aeb03 -- true` fails with `device not connected to relay`.
- From Ubuntu, `yaver exec -direct --device 229aeb03 -- true` chooses the Mac mini LAN address `192.168.111.38:18080` and times out. This is the mobile-5G class of failure: off-LAN clients need a non-LAN candidate that is not Tailscale-only, or a working relay attachment.

Todo repos on the Mac mini:

- `/Users/pokayoke/Workspace/yaver-todo-web` at GitHub main `0faac4a…`
- `/Users/pokayoke/Workspace/yaver-todo-flutter` at GitHub main `0ed149b…`
- `/Users/pokayoke/Workspace/yaver-todo-swift` at GitHub main `dbf299c…`
- `/Users/pokayoke/Workspace/yaver-todo-kt` at GitHub main `81d0bca…`

Project discovery bug found live:

- `yaver primary projects --json` timed out across all candidates.
- `yaver primary mobiles --json` returned `scanning:true` and empty projects even though the four todo repos exist.

Code added in this worktree:

- `projectDiscoveryRoots()` now scans project roots (`~/Workspace`, `~/Projects`, etc.) before all of `$HOME`.
- Mobile project discovery now persists a machine-readable cache at `~/.yaver/mobile-projects.json` and hydrates from it before reporting an empty cold cache.
- Added disk-light tests for the standalone todo repo shapes and persistent cache round-trip.

Verification:

- `cd desktop/agent && go test -count=1 -run 'TestScanMobileProjects_DiscoversStandaloneTodoRepos|TestMobileProjectsPersistentCacheRoundTrip|TestScanMobileProjects_DiscoversNestedFrameworksInsideYaverRepo|TestScanMobileProjects_DemoShowcaseEdgeCases|TestMobileCapableProjects_FiltersWebOnly' .`
