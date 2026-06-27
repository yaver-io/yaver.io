# Yaver — open TODOs

Working checklist as of 2026-06-27. Grouped by **who can do it**. Checkboxes are
yours to tick. Code refs are clickable.

---

## 🔴 You only (manual — I can't do these)

- [ ] **Create the $19 "Cloud Agent" variant in LemonSqueezy**, then paste me the
  variant id → I set `LEMONSQUEEZY_YAVER_CLOUD_HOSTED_VARIANT_ID` on the prod
  Convex deployment and the $19 Agent checkout goes live. **Until then Agent
  checkout returns a clean 503 ("not configured") — never a mis-bill.** $9
  Workspace already works.
  - Optional: also create/confirm a distinct **$9 "Cloud Workspace" variant** and
    give me `LEMONSQUEEZY_YAVER_CLOUD_BYOK_VARIANT_ID`. Today byok falls back to
    the single default `LEMONSQUEEZY_YAVER_CLOUD_VARIANT_ID` (which is byok-priced),
    so this is only needed if you want the two SKUs fully separated in LS.
- [ ] **Re-auth this Mac's daemon: `! yaver auth`** — a broad test run earlier
  cleared `auth_token` from `~/.yaver/config.json`, so the local daemon is signed
  out (web/mobile sessions are unaffected). Needed before the local daemon can be
  swapped onto the new binary or used for billing/MCP testing.
- [ ] **Decide on the cli release.** The agent-side fixes below are committed to
  `main` but only reach users via a `cli/v*` npm publish (public, hard to undo).
  Currently **held** to bundle with the OpenRouter inference work. Say "cut the
  cli release" when ready (or "bundle it" to keep waiting).

## 🟡 Parallel session (in-progress, uncommitted — not mine to ship)

- [ ] **Finish + commit the OpenRouter inference path**: `gateway/src/{index.ts,
  pricing.ts}`, `gateway/wrangler.toml` (the `OR_USER_KEYS` KV namespace), and the
  per-user-key flow in `backend/convex/openrouterKeys.ts`. Then **deploy the
  gateway Worker** (`wrangler deploy` — separate from `convex deploy`; I did NOT
  deploy it). The convex half is already committed + deployed but dormant (empty
  key table ⇒ no-op).
- [ ] (Their `/billing/yaver-cloud/change-plan` tier-switch endpoint is committed +
  deployed already — covers in-app Agent⇄Workspace switching.)

## 🟢 Me (I'll do as soon as unblocked — just ping me)

- [ ] **Set the hosted variant env** the moment you give me the $19 LS variant id
  (`convex env set --prod LEMONSQUEEZY_YAVER_CLOUD_HOSTED_VARIANT_ID …`) → Agent
  goes live. No code change needed; the path is already built + deployed.
- [ ] **Managed-box gateway join** (`backend/convex/cloudMachines.ts`): set
  `YAVER_GATEWAY_URL` on hosted boxes, inject the per-user OpenRouter key, and
  point hosted `opencode.json` at the gateway instead of `zai-coding-plan`. The
  bridge already exists (`desktop/agent/gateway_runner_env.go` `gatewayInjectEnv`).
  **Do this AFTER the parallel OpenRouter-key model commits**, so we don't conflict
  in the same files.
- [ ] **Cut the cli release** when you give the word (ships every agent fix below).

## ✅ Verify before flipping $19 Agent live (sandbox)

- [ ] `yaver_billing_status` reads correctly (no plan → active after purchase).
- [ ] `yaver_billing_checkout workspace` mints a $9 link; **`agent` mints a $19
  link once the variant is set** (503 before).
- [ ] Plan change (Agent⇄Workspace), `yaver_billing_manage` → cancel via portal.
- [ ] Webhook attributes the purchase by `custom_data.user_email` for a real
  terminal buy (pay with the same email you signed into Yaver with).

---

## Shipped this session (for context — already done)

**Live now (deployed):**
- Owner-gate: experimental hardware cells hidden from non-owners via the
  server `user.isOwner` flag (no email in any client bundle). Backend + web
  deployed; mobile gate ships in the next mobile build.
- MCP install commands (Claude Code / Codex / opencode) on the landing hero,
  `/download`, README, `docs/mcp`. Web deployed.
- Billing backend: checkout tier fix + `GET /billing/status` + `GET
  /billing/portal`. Convex deployed.
- macOS keychain prompt-spam fix (`-A` on the vault master-key mirror).

**Committed to `main`, ships in the cli release:**
- `mobile_deploy_to_phone` one-shot Hermes deploy + honest "no phone listening"
  gate.
- `yaver_lazy_setup` daemon-health gate (kills the #1 silent first-run failure).
- Auto-register Yaver MCP for **opencode + glm** on a managed box (not just
  claude/codex).
- Buyer-side billing MCP tools: `yaver_billing_{status,checkout,manage}`.

**Design docs:** [`yaver-mcp-billing.md`](yaver-mcp-billing.md),
[`yaver-normie-first-run.md`](yaver-normie-first-run.md).
