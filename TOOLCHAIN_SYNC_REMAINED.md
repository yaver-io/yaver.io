# Toolchain Sync Remained

## Achieved

- Added agent-side Toolchain Sync profile/apply plumbing in `desktop/agent/env_profile.go`.
- Added owner-auth routes:
  - `/agent/toolchain-sync/profile`
  - `/agent/toolchain-sync/apply`
  - `/agent/toolchain-sync/git-credentials`
- Toolchain Sync now carries:
  - discovered tools and runners
  - Linux-safe install plan / apply
  - sync-store kinds (`provider-keys`, `presets`, `flags`, `env`, `monitors`)
  - Git host credentials
  - project path hints
- Added `removeMissing` semantics for:
  - removing Git host credentials missing on source
  - reporting target-only tools in `removalPlan`
- Added mobile client methods in `mobile/src/lib/quic.ts`:
  - `getToolchainSyncProfile()`
  - `applyToolchainSync()`
- Added mobile UI in `mobile/app/(tabs)/settings.tsx`:
  - owner-only Toolchain Sync section
  - source-device picker
  - preview/apply flow
  - toggles for install/sync/remove options
- Added focused agent tests in `desktop/agent/env_profile_test.go`.

## Not Done Yet

- No full filesystem sync. Toolchain Sync intentionally excludes user files.
- No repo/content replication. Users still need Git clone/pull or phone-project export/push flows.
- No automatic OS-package uninstall. `removalPlan` is informational only for packages/tools.
- No dedicated web UI yet. Web client helpers can exist separately, but mobile is the only surfaced UI path in this work.
- No source-profile diff UI beyond the alert summary and “Last Preview” block.
- No background sync scheduling. This is manual preview/apply only.
- No per-tool install progress UI yet. The agent runs installs headlessly, but mobile does not stream an install log sheet for Toolchain Sync.
- No builder-specific/Xcode migration path. Linux target remains the supported managed-cloud target.
- No cloud-machine-specific onboarding CTA from phone-project flows yet. Toolchain Sync currently lives in Settings on a connected target.

## Known Constraints

- Source machine must be online and reachable through Yaver.
- Target machine must already be connected in Yaver mobile.
- Guest sessions cannot run Toolchain Sync.
- Cross-platform behavior is intentionally conservative:
  - source can be macOS or Linux
  - target is assumed Linux-safe
  - unsupported/manual runner auth (for example Claude/Codex local auth state) is surfaced as manual follow-up

## Next Good Steps

1. Add a streamed progress/log surface for Toolchain Sync installs.
2. Add a clearer review screen instead of only alert summaries.
3. Add “clone Git setup only” and “clone AI setup only” quick presets.
4. Add a “clone into newly provisioned Yaver Cloud machine” onboarding step after managed-cloud first boot.
5. Add safe repo bootstrap suggestions after sync:
   - clone recent repos
   - set default workdir
   - offer project attach/import choices
6. Add optional web dashboard UI for Toolchain Sync.
