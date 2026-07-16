# Deploy orchestration — run AFTER both auto-runners complete (autorun + n2n)

Trigger: only once BOTH overnight runners have finished their features and the tree is
build-clean — the `yaver-video` (autorun) codex loop AND the n2n runner. Check: their screen
sessions idle / DONE markers in their progress MDs, and `git status` clean on `main`.

Then run ALL deploys SEQUENTIALLY, one at a time, RESOURCE-AWARE. Rules:
- Before EACH deploy: RESOURCE preflight — DISK (`df -h /`, need >=20 GiB for any iOS/tvOS
  archive; `~/.local/bin/mobile-cache-cleanup.sh preflight`), RAM (`vm_stat` free/inactive
  pages — don't start a heavy archive when free RAM is low), and CPU LOAD (`uptime` load
  average vs core count — if load is high, WAIT/back off before starting the next step).
  One deploy at a time — never parallel (CPU/RAM/disk/ENOSPC risk). Also pause deploys while
  the autorun/n2n runners are still CPU-hot; deploy in the quiet window.
- GATE first: `cd desktop/agent && go build ./... && go test ./...`; `cd mobile && npx tsc
  --noEmit`. Do NOT deploy anything if the gate fails — stop and record the failure.
- Deploy ONLY what actually changed since the last release, and ONLY where creds exist on
  this box. If creds are missing, SKIP that target with a logged note (do not guess/fake).
- STOP ON FIRST FAILURE — do not cascade broken deploys. Log each step to
  `docs/handoff/deploy-orchestration-progress.md`.

Order (skip any with no changes / no creds):
1. Convex backend  — `cd backend && npx convex deploy --yes`  (only if `backend/convex` changed)
2. Cloudflare web  — bump `versions.json` web + tag `web/v*` (CI) OR `./scripts/deploy-web.sh`
                     if CLOUDFLARE_API_TOKEN/ACCOUNT_ID present (only if `web/` changed)
3. CLI/agent (npm) — cut a `cli/v*` tag (only if `desktop/agent` shipped user-facing changes)
4. TestFlight iOS  — AFTER pod install + clean /tmp/YaverBuild if node_modules was touched
                     (see reference_testflight_node_modules_reinstall). `deploy-testflight.sh`.
5. tvOS/visionOS   — `deploy-tvos.sh --upload` / `deploy-visionos.sh --upload` (bump build#).

IMPORTANT: these are OUTWARD-FACING + hard to reverse (prod Convex, App Store, npm). A human
check is strongly preferred before the store/prod steps. If run by a runner, it must still
gate + stop-on-failure + log, and NEVER force/retry a store upload past a rate limit.

Note: this session (on the developer's MacBook) cannot execute this — it ends when the laptop
closes. Execute this via a runner on the mini once features complete, or by the developer
when back.
