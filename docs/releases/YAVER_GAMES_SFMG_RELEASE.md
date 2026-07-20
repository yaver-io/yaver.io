# Yaver Games / SFMG Release Readiness

This is a release-readiness note, not a deployment record. Do not submit iOS,
Android, tvOS, or Play builds without explicit user confirmation.

## Included In The Next Yaver Release

- Yaver Games public web surface at `/games`.
- Hosted MCP guidance tool: `yaver_strategy_game_native_guide`.
- Hosted MCP scanner: `yaver_game_manifest_audit`.
- SFMG first-party Yaver Game manifest support via `../sfmg/yaver.game.json`.
- Yaver OAuth required for Yaver-native/Yaver-first game builds.
- Yaver billing ownership required for official in-Yaver catalog release.
- Source/package sharing required only for official Yaver catalog release, not
  for development, private testing, self-hosting, or remote-runner previews.
- Third-party developer lifecycle baseline: remote box first, source in
  Yaver Git/GitHub/GitLab/self-hosted Git/local repo, private deploy before
  optional Yaver catalog publish, and full exit rights.
- SFMG owner workflow: Kivanc and Serhat can develop SFMG through Yaver, allocate
  temporary scale-to-zero Hetzner/Yaver Cloud workers, configure OpenCode/GLM on
  the target machine, and deploy private previews without publishing in Yaver.
- TV launch entry points for Yaver Games/SFMG in the Expo TV home and native
  tvOS dashboard.

## Surface Positioning

Yaver is strategy-games-first:

- primary full-play surfaces: browser, phone, tablet, Apple TV, Android TV
- developer/QA surface: Yaver Remote Runner
- companion-only surfaces: watch and car

Watch and car should handle briefings, approvals, voice commands, notifications,
and async decisions. They should not attempt to render the dense SFMG dashboard.
