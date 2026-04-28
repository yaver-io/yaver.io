# Mobile Devices / Auth / Hermes Audit — 2026-04-27

Scope: mobile Devices tab, mobile runner re-auth, Yaver re-auth, and
Hermes reload from iPhone against local and remote agents.

## Landed in code

- Devices now pins the connected machine first in the mobile list.
- Device cards now distinguish Yaver auth from runner auth:
  `Yaver needs auth`, `Yaver expired`, `Claude Code expired/ready`,
  `Codex expired/ready`, plus agent version on the card.
- Device details now shows Yaver version and separates
  non-destructive `Recover Yaver Auth` from destructive
  `Factory-reset Yaver auth`.
- Mobile Yaver recovery now prefers relay owner-claim for
  bootstrap-mode devices before falling back to the older recovery
  path.
- Runner sign-in modal is now device-bound instead of depending on
  the currently active workspace connection.
- Codex mobile auth now uses device-auth UX; only Claude keeps the
  paste-back code field.
- Devices now has explicit `Refresh State` so manual `yaver auth` on
  the box can clear stale expired state without reconnect churn.
- Hot Reload now exposes a `Stop Discovery` action for project scan.
- Agent `/projects/mobile` now supports cancel via `DELETE`.
- Hot Reload now surfaces `/dev/build-native` failure details
  (`phase`, `helpHint`, bundler output tail) instead of collapsing
  them into a generic load error.

## Findings confirmed during audit

- Web had stronger re-auth behavior than mobile because web used a
  device-bound client while mobile reused the shared active-device
  transport.
- Mobile had removed the safer Yaver owner-claim recovery path from
  the surfaced UI even though the transport already existed.
- Devices UI previously conflated Yaver auth and runner auth into a
  generic `needs auth` label.
- Manual console re-auth on a machine could leave stale mobile UI
  until the next poll/reconnect cycle.
- The Linux/iPhone Hermes recording showed a build-phase failure on
  the agent (`JavaScript bundle build failed`), not yet a proven
  relay/native-load failure.

## Residual verification

- Re-test Hermes reload on:
  - macOS agent + iPhone on LAN
  - Linux remote agent + iPhone off-LAN/relay
- For the Linux remote case, confirm whether the blocker is now only
  project bundling or whether a second relay/native-load issue remains
  after bundling succeeds.
