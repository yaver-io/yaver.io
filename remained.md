# Remained

## What Is Fixed

- Mobile recovery now sends the signed-in bearer token to `/auth/recover`.
- Mobile watches `authExpired` and can recover a reachable-but-stale agent.
- `/auth/recover` pair mode now persists the recovered token into the running daemon.
- Heartbeat reloads the latest token from config, so recovery applies without a restart.
- Backend owner lookup now compares against the caller's real Convex user doc id.
- Agent startup now caches Convex-provided relay settings locally and reuses them after reboot when auth is stale.

## What Still Needs Real-World Validation

- Run the new GitHub workflow [remote-infra.yml](/Users/kivanccakmak/Workspace/yaver.io/.github/workflows/remote-infra.yml) with real secrets.
- Confirm the rebooted remote box still reconnects through the public relay using only cached relay config.
- Run the manual `mesh` workflow against two Yaver-controlled machines plus the runner and verify `yaver agent mesh-smoke` stays green.
- Confirm on a physical phone that recovery keeps using the successful target URL end to end when relay is dead but direct HTTP/Tailscale still works.

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
- [scripts/test-remote-infra-ci.sh](/Users/kivanccakmak/Workspace/yaver.io/scripts/test-remote-infra-ci.sh)
- [scripts/test-anthropic-local.sh](/Users/kivanccakmak/Workspace/yaver.io/scripts/test-anthropic-local.sh)

## Local Anthropic Path

- Run `./scripts/run-ci-local.sh anthropic-local` for a manual local-only Claude-backed validation path.
- It drives real Yaver HTTP endpoints instead of GitHub Actions and never uses repository secrets.
- It currently covers `autoinit` through the daemon; extend it to `autoideas` or `autodev` once the local spend profile is acceptable.
