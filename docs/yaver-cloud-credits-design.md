# Yaver Cloud Credits Design (Retired)

Status: retired.

This was the old OpenRouter-style prepaid credit design. It is no longer the
Yaver product model.

Current billing model:

- Free: limited shared public relay usage.
- Relay Pro: paid relay subscription.
- Cloud Workspace: paid flat monthly subscription with internal included
  allowance and cost guardrails.

Rules:

- Do not expose credit-pack checkout to users.
- Do not expose direct prepaid workspace provisioning to users.
- Keep wallet/ledger concepts internal for allowance, metering, and fail-closed
  cost protection only.
- Cloud Workspace purchases, cancellations, and payment updates are web-only.
- Mobile must not call checkout, portal, cancel, or plan-change APIs.
- New managed compute must come from active Cloud Workspace subscription,
  reconcile, or placement activation paths.

Implementation evidence now lives in:

- `docs/planning/machine-backed-task-architecture-plan-2026-07-18.md`
- `docs/managed-cloud-go-live-runbook.md`
