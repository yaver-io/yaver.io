# Tasks and Yaver-on-Yaver audit — 2026-09-10

Code and live probes, not this document, are authoritative.

## Findings fixed

- Tasks: every task-identity change dismisses the follow-up composer. Text taps always focus text; a previous voice submission cannot schedule microphone capture. Follow-up retains the New Task-style keyboard-safe modal. Empty filters no longer claim the account has no tasks.
- Shared feedback SDK: delayed Stop and handoff completion cannot overwrite a newer Retry or remove its cleanup. An immediate Stop prevents startup after asynchronous cleanup. Fallback also verifies attempt identity across cleanup.
- ACP: the task-scoped Yaver MCP descriptor explicitly carries task ID and source. A live task had been unable to signal render readiness because its adapter-launched MCP child lacked this context. External MCP servers receive no Yaver task environment.
- MCP: losing observation of a started remote command now returns its execution identity and an `exec_status` recovery action, not an invitation to run it twice. Mesh-device execution is distinguished from provisioned-worker execution in tool descriptions/errors.
- Startup: changing an inline completion callback cannot restart the decorative splash deadline and obscure an already usable app.
- iOS CI: the release job now delegates to `deploy/deploy.sh ios`, retains the production environment boundary, and uses the maintained native-overlay restoration and App Store build-number lookup instead of a separate archive and run-number counter.

## Verification

- Ubuntu live machine: approximately 8 GB RAM despite its historical 4 GB name.
- Focused Go ACP and remote-execution tests passed on Ubuntu.
- Shared React Native feedback SDK: 31 suites, 265 tests passed.
- Mobile Tasks/Dogfood contracts: 43 tests passed on Ubuntu; the additional splash regression passed. Mobile TypeScript passed after the final UI change.
- Keyboard and SDK race regressions were observed failing without their fixes. Splash and iOS CI contract tests also failed before their fixes.
- `e2e/tasks-dogfood-audit.mjs`: real RN-web, full iPhone 15 Pro browser context, authenticated existing task; no task dispatch. Verified reading-mode navigation, explicit focus, dismiss/reopen with retained draft, task-card entry, runtime choices, and runner-sheet dismissal. Final screenshots inspected: follow-up controls and Browser/Hermes/WebRTC lane choices are actually visible.
- Browser artifacts are untracked under `e2e/test-results/tasks-dogfood-1789067834420`. Earlier failed attempts were retained. The harness now waits for splash disappearance and actual filter results.
- Physical iOS keyboard occlusion, Hermes delivery to a phone, and a complete coding/render loop are **not** proven by this browser arc.

## Repository and cleanup

Local and Ubuntu canonical main both matched origin/main at baseline `8c8573174`. Audit changes were isolated, tested, and pushed; release preparation uses signed commits. Existing unrelated local integration changes were preserved.

Removed 14 clean, inactive linked Yaver worktrees without force. Their branch refs remain; all ten recovery stashes remain. Older independent recovery clones were not deleted or blindly merged: their existence is not evidence that every historical patch belongs on current main. “Current audit changes are on main” does not mean every archived experiment has been integrated.

## Release evidence and limits

CLI 1.99.462 was dispatched through the canonical npm deploy target. Shared SDK 0.9.21 was prepared; local npm authentication was unavailable, so the existing protected feedback-SDK workflow was used. Check the corresponding GitHub Actions runs and registry before treating either publish as complete.

Local iOS storage is below the 10 GiB archive floor. The CI fallback uses the same canonical deploy implementation; an upload or physical-device result must be checked separately and must not be inferred from a workflow dispatch.
