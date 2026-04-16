# Remained

## What Is Fixed

- Mobile recovery now sends the signed-in bearer token to `/auth/recover`.
- Mobile watches `authExpired` and can recover a reachable-but-stale agent.
- `/auth/recover` pair mode now persists the recovered token into the running daemon.
- Heartbeat reloads the latest token from config, so recovery applies without a restart.
- Backend owner lookup now compares against the caller's real Convex user doc id.
- Agent startup now caches Convex-provided relay settings locally and reuses them after reboot when auth is stale.
- Mobile autodev now behaves like a live machine session instead of a loop-admin panel: live transcript first, ideas second, setup third.
- Mobile autoideas now supports multi-select implementation with clearer backlog/status UI and jumps back into live autodev after starting.
- Web dashboard console now has an actual `autodev` workbench with live transcript, backlog selection, and one-shot loop start.
- Mocked browser coverage now exists for the web autodev workbench in [e2e/tests/dashboard-autodev.spec.ts](/Users/kivanccakmak/Workspace/yaver.io/e2e/tests/dashboard-autodev.spec.ts).
- CLI stream rendering now keeps piped output free of ANSI escapes and the local yaver-to-yaver harness asserts that autodev transcript output is human-readable instead of raw JSON.

## What Still Needs Real-World Validation

- Run the new GitHub workflow [remote-infra.yml](/Users/kivanccakmak/Workspace/yaver.io/.github/workflows/remote-infra.yml) with real secrets.
- Confirm the rebooted remote box still reconnects through the public relay using only cached relay config.
- Run the manual `mesh` workflow against two Yaver-controlled machines plus the runner and verify `yaver agent mesh-smoke` stays green.
- Confirm on a physical phone that recovery keeps using the successful target URL end to end when relay is dead but direct HTTP/Tailscale still works.
- Run `./scripts/run-ci-local.sh peer-local` against your preferred real runner stack and confirm the yaver-to-yaver autodev transcript matches the improved app UX.
- Validate Expo web / mobile web rendering of the new autodev and autoideas screens on a narrow viewport.
- Add one real device-facing automation path for the mobile app itself once the preferred harness is chosen (Expo/native vs browser wrapper).

## Required GitHub Secrets

- `CONVEX_SITE_URL`
- `RELAY_HTTP_URL`
- `YAVER_CI_SSH_HOST_PRIMARY`
- `YAVER_CI_SSH_HOST_SECONDARY` for the mesh workflow
- `YAVER_CI_SSH_USER`
- `YAVER_CI_SSH_PORT`
- `YAVER_CI_SSH_PRIVATE_KEY`
- `YAVER_CI_SSH_KNOWN_HOSTS`

## Main Files To Read Next

- [AI_ARCH.md](/Users/kivanccakmak/Workspace/yaver.io/AI_ARCH.md)
- [desktop/agent/main.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/main.go)
- [desktop/agent/auth_recover.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/auth_recover.go)
- [mobile/src/context/DeviceContext.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/context/DeviceContext.tsx)
- [mobile/app/(tabs)/autodev.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/(tabs)/autodev.tsx)
- [mobile/src/components/AutoIdeasPane.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/components/AutoIdeasPane.tsx)
- [web/components/dashboard/ConsoleView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/ConsoleView.tsx)
- [web/lib/agent-client.ts](/Users/kivanccakmak/Workspace/yaver.io/web/lib/agent-client.ts)
- [scripts/test-remote-infra-ci.sh](/Users/kivanccakmak/Workspace/yaver.io/scripts/test-remote-infra-ci.sh)
- [scripts/test-anthropic-local.sh](/Users/kivanccakmak/Workspace/yaver.io/scripts/test-anthropic-local.sh)

## Local Anthropic Path

- Run `./scripts/run-ci-local.sh anthropic-local` for a manual local-only Claude-backed validation path.
- It drives real Yaver HTTP endpoints instead of GitHub Actions and never uses repository secrets.
- It currently covers `autoinit` through the daemon; extend it to `autoideas` or `autodev` once the local spend profile is acceptable.

## Local Yaver-To-Yaver Path

- Run `./scripts/run-ci-local.sh peer-local` for a local controller→target Yaver harness.
- It starts two local agents with separate homes, puts the target on port `18080`, and exercises real `yaver ... --to <device>` flows.
- Use environment overrides like `RUNNER_SPEC`, `MODEL_SPEC`, `PLANNER_SPEC`, and `IMPLEMENTER_SPEC` to validate non-Anthropic runners or hybrid splits.
