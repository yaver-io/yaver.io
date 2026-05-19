# Managed Cloud P4-P6 Handoff

Date: 2026-05-19

## Completed

### P4 — Agent First-Boot Readiness

Implemented first-boot readiness on the existing agent audit endpoint:

- `GET /yaver-agent/audit` now includes:
  - `readiness.state`: `"ready"` or `"needs-reauth"`
  - `readiness.reasons`: `["vault" | "runner" | "git"]`
  - `readiness.vault`: `"open" | "missing" | "locked"`
  - `readiness.runner`: `"ready" | "needs-reauth"`
  - `readiness.git`: `"ready" | "needs-reauth"`
- Readiness uses existing code paths:
  - vault: runtime `VaultStore` / on-disk `vault.enc` probe
  - runner: existing Claude/Codex/OpenCode runtime checks
  - git: existing machine onboarding GitHub/GitLab clone credential checks
- Added recommendations for:
  - `vault_reauth_required`
  - `git_auth_required`

Files:

- `desktop/agent/yaver_agent_tools.go`
- `desktop/agent/yaver_agent_tools_test.go`

### P5 — Mobile Managed Cloud Card

Extended mobile managed-cloud UI/client:

- Shows prepaid balance from `/billing/yaver-cloud/balance` or `/subscription.balance`.
- Shows stopped machines instead of hiding them.
- Adds Start button for `status === "stopped"`.
- Adds Stop button for running/non-stopping machines.
- Keeps Decommission as a separate action.
- Adds owner-dev top-up button.
- Adds API error handling for decommission/start/stop/top-up.

Files:

- `mobile/src/components/ManagedCloudCard.tsx`
- `mobile/src/lib/subscription.ts`

### P6 — Owner-Dev Top-Up + Balance Routes

Added owner-gated backend routes wired to the P0 `cloudLifecycle` ledger:

- `GET /billing/yaver-cloud/balance`
- `POST /billing/yaver-cloud/topup-dev`

Also extended `GET /subscription` response with:

- `prepaidBalanceCents`
- `currency`
- `balance`

Files:

- `backend/convex/http.ts`

### Start/Stop Route Contract

Added owner-gated backend route contract for mobile/web:

- `POST /billing/yaver-cloud/stop`
- `POST /billing/yaver-cloud/start`

Important: these are currently `dryRun: true` state transitions only.

- `stop` patches the machine to `status: "stopped"`.
- `start` checks `internal.cloudLifecycle.canStart` before patching to `status: "active"`.
- No Hetzner API call happens here yet.
- Real provider stop/start remains blocked on P1.

File:

- `backend/convex/http.ts`

## Verification Run

Passed:

```bash
cd desktop/agent
go test . -run 'Test(RecommendNextActions|BuildYaverAgentReadiness|MachineOnboardingGitReady|HandleYaverAgentDeviceAudit)'
```

Passed:

```bash
cd backend/convex
../node_modules/typescript/bin/tsc -p tsconfig.json --noEmit
```

Passed:

```bash
git diff --check -- backend/convex/http.ts mobile/src/lib/subscription.ts mobile/src/components/ManagedCloudCard.tsx desktop/agent/yaver_agent_tools.go desktop/agent/yaver_agent_tools_test.go docs/managed-cloud-metered-stopstart-plan.md
```

Mobile full typecheck still has unrelated pre-existing errors outside the touched files. Filtered compiler output showed no errors from:

- `mobile/src/components/ManagedCloudCard.tsx`
- `mobile/src/lib/subscription.ts`

## Remaining Work

Real end-to-end stop/start is not complete until P1 lands:

- Hetzner stop/snapshot verb
- recreate/start-from-snapshot verb
- agent `cloud_stop` / `cloud_start` ops verbs
- route integration that calls the real provider operation instead of the current `dryRun: true` state patch

Once P1 is available, update `/billing/yaver-cloud/start` and `/billing/yaver-cloud/stop` in `backend/convex/http.ts` to call the real lifecycle path and remove or narrow the dry-run behavior.

