# Serverless Companion Compute — Deep Audit

Status: audit / design analysis. Code is source of truth; grep before relying on
any route/type named here.

## Implemented (2026-06-04, uncommitted)

The Companion layer below is built and tested on the Go agent + web + SDK:

- `desktop/agent/managed_units.go` (+ `_windows.go`) — reboot-durable per-service
  OS-unit writer (systemd user unit / launchd LaunchAgent). `services.go`
  `startBinaryService` now honours `Command`/`Args`/`Env`.
- `desktop/agent/companion.go` — manifest engine (`yaver.companion.yaml`), a
  registered `companion_http` ops verb (crons fire it via the scheduler's
  Verb-mode), env interpolation (dotenv + vault), `Up`/`Down`/`Status`/
  `Reconcile`. `Reconcile()` is wired in `main.go` (one-shot on boot).
- `desktop/agent/companion_detect.go` — serverless detector (Supabase cron
  endpoints, Convex/wrangler notes, worker services, subscription-reconcile
  inference). Verified against the real `../e-back` repo.
- `desktop/agent/companion_http.go` + `mcp_companion.go` — `/companion/*` routes
  and `companion_*` MCP verbs (mirroring phone_backend).
- `desktop/agent/companion_sync.go` + `convex_privacy_test.go` — the privacy
  seam (bookkeeping projection) + pin/payload guards.
- `web/components/dashboard/CompanionView.tsx` + dashboard tab; companion
  methods in `web/lib/agent-client.ts`.
- `sdk/js/src/companion.ts` — standalone `CompanionClient` (+ index export).
- Tests: 13 Go companion tests (engine arm/idempotent/down, cron reboot-reload,
  HTTP verb fires + failure detection, detector synthetic + live e-back, privacy
  pin/payload, unit renderers) + SDK tsc/tests + web tsc, all green.

**Deferred / not done** (intentional):
- The Convex `companionProjects` bookkeeping table + `companion.ts` functions +
  `/companion/*` Convex routes were NOT added — `backend/convex` is under active
  parallel-session edits, and status flows P2P via the agent `/companion/*`
  routes (no Convex needed). The privacy seam/builder/tests are in place for when
  it is wired; the engine's `syncer` is left nil so no mutation is emitted.
- The `companion` workKind in `companyAIOptions.ts` was NOT added — it is pure
  `appProfile` data in the generic resolver; no code edit required, and that file
  is being actively edited elsewhere.
- A live systemd/launchd install was not run on the dev Mac (side-effecting);
  durability is covered by the unit-renderer tests + the cron reboot-reload test.
- Nothing is committed/pushed/deployed.

## The thesis

A "serverless" app (Convex / Supabase Edge Functions / Cloudflare Workers /
Vercel) is never *fully* serverless. Every real one grows a tail of work that a
request/response function platform handles badly or not at all:

- **always-on crons / timers** that must fire whether or not a request arrives
- **long-running processes** (poll loops, queue workers, websocket clients, AI
  token streams that outlive a function timeout)
- **an AI-wrapper / MCP / agent server** that needs a real process, a toolchain,
  and a filesystem
- **stateful background jobs** with retries that survive a deploy

Today each project bolts this on differently: an external cron pinger, a stray
VPS, a GitHub Action on a timer, a `setInterval` that dies with the request.
Yaver already owns a user's machines, relay, runners, and a managed-cloud box
lifecycle. The opportunity is to make Yaver the **conjugate compute layer**: the
always-on companion that every serverless project plugs into, declared once and
wired from a web UI.

## Evidence: two real projects

### e-back (Elevathor) — Supabase Edge Functions (Deno)

Pure serverless, but with a background tail that is currently **manual or
missing**:

| Need | Today | Companion need |
|---|---|---|
| Auto-mail sender (`/rest/autoMailSenderDirect`) | endpoint exists, **no scheduler in code** — must be pinged externally | a durable daily cron |
| Daily summary mail (`/rest/dailySummaryMailDirect`) | endpoint exists, **no scheduler** | a durable daily cron |
| Subscription-expiry check | **does not exist** — purely reactive on LemonSqueezy webhooks; a missed webhook = wrong billing state | an hourly reconcile job (proactive poll of `next_payment_date`) |
| Telegram polling (`getUpdates` loop) | infra present, runs in webhook mode to avoid an always-on loop | if ever enabled, an always-on worker |
| LLM SSE keep-alive | `setInterval` scoped to a request | fine on serverless; flagged as the shape that breaks past function timeouts |

Net: e-back is ~95% serverless. The 5% that isn't (two should-be crons + one
missing reconcile job) is exactly the companion gap — and it's invisible until
billing is wrong or a mail never sends.

### Talos — AI-wrapper server

The opposite shape: the dedicated server *is* the product surface. Needs a real
process running `yaver serve` + a runner (OpenCode/Claude/Codex) + the Talos MCP
server + toolchains (Node, Convex CLI, OpenSCAD). Already partially wired via the
new `companyAIOptions` slice and `/company-ai/resolve`. This is the
"AI wrapper dedicated server" case the user named.

**The two projects bracket the whole space:** e-back = crons/timers/workers,
Talos = AI-wrapper/MCP. A generic companion has to serve both.

## What Yaver already has (the primitives — ~80% built)

| Primitive | File | State | Survives restart | Survives **reboot** | Generic? |
|---|---|---|---|---|---|
| In-process scheduler (cron/interval/one-shot) | `desktop/agent/scheduler.go` | `~/.yaver/schedules.json` | ✅ | ❌ | ✅ any task |
| Routines (verb-mode schedules, MCP-only) | `desktop/agent/routines_mcp.go` | same | ✅ | ❌ | ✅ any ops verb |
| Job queue (retries/backoff/DLQ) | `desktop/agent/jobqueue.go` | `~/.yaver/jobs/*` | ✅ | ✅ | ⚠️ handlers registered in-code |
| Service supervisor (Docker + binary) | `desktop/agent/services.go` | `.yaver/services.yaml` | ✅ | ⚠️ config persists, **no auto-start** | ✅ any image/binary |
| pg_cron bridge | `desktop/agent/cron_manager.go` | Postgres | ✅ | ✅ | SQL only |
| Convex cron bridge | `desktop/agent/cron_manager.go` | Convex | ✅ | ✅ | read/snippet only |
| Managed-cloud box lifecycle | `cloudMachines` table, `ops_cloud.go`, `mcp_onboarding_flows.go` | Convex + box | — | — | ✅ provider-agnostic facade |
| Company policy + resolver | `backend/convex/companyAIOptions.ts` | Convex (no secrets) | — | — | ✅ generic `appProfile` |
| Project init / detect | `init_project.go`, `autoinit_cmd.go`, `monorepo_detect`, `deploy_script_gen.go` | local | — | — | ✅ |
| Phone mini-backend (portable schema + promote) | `phone_backend*.go` | `~/.yaver/phone-projects` | — | — | ✅ |

The runtime muscle exists. Crons, queues, service supervision, a box to run them
on, a no-secrets policy store, and a clean dashboard-tab/HTTP/MCP plug pattern.

## The gaps (the missing ~20% that blocks "plug-and-play")

1. **No reboot-durable supervision.** The single biggest gap. In-process
   schedules and binary services die on reboot — there is **no systemd/launchd
   unit generation** that makes a project's companion crons/services come back
   up. An always-on companion that doesn't survive reboot is not always-on.

2. **No project-level "companion" manifest.** Crons live in
   `~/.yaver/schedules.json`, services in `.yaver/services.yaml`, queue handlers
   in Go. Nothing ties "this serverless repo needs *these* crons + *these*
   workers + *this* AI-wrapper" into one declared, versionable unit.

3. **No serverless-aware detector.** `monorepo_detect` finds frameworks; nothing
   reads a Supabase/Convex/Workers repo and *proposes* the companion wiring
   (e.g. "you have `/rest/autoMailSenderDirect` and no scheduler — want a daily
   cron?", "you have a `next_payment_date` column and webhook-only billing —
   want a reconcile job?").

4. **No web-UI wiring for it.** `companyAIOptions` is AI-runtime policy, not
   "here are my project's background jobs." There's no panel to point at a
   project, see proposed/active crons + services + workers, and toggle them onto
   a companion box.

5. **Framing is runner-centric.** Everything is shaped around AI coding runners.
   The crons/timers/workers case (e-back) has no first-class home even though the
   primitives exist.

## Proposed shape: the Companion layer

A thin generic feature that *composes the existing primitives* rather than
adding a new runtime.

1. **Companion manifest** — `yaver.companion.yaml` at a project root, the one
   declared unit:

   ```yaml
   companion:
     project: e-back
     # bind to a yaver device (managed box, primary, or BYO)
     runtime: { device: "${YAVER_COMPANION_DEVICE}", durable: true }
     crons:
       - name: auto-mail
         schedule: "0 9 * * *"
         action: { http: { url: "${SUPABASE_FN}/rest/autoMailSenderDirect", method: GET } }
       - name: daily-summary
         schedule: "0 8 * * *"
         action: { http: { url: "${SUPABASE_FN}/rest/dailySummaryMailDirect" } }
       - name: subscription-reconcile      # the missing one
         schedule: "0 * * * *"
         action: { http: { url: "${SUPABASE_FN}/rest/subscriptionReconcile" } }
     services: []          # long-running: telegram poller, queue worker, AI wrapper
     env_from: vault       # secrets stay in vault, never in this file or Convex
   ```

   Maps directly onto `scheduler.go` (crons), `services.go` (services), and the
   vault. HTTP-action crons are a tiny new action type next to the existing
   task/verb actions.

2. **Reboot durability** — generate a per-project systemd unit (Linux box) /
   launchd plist (Mac) that runs `yaver companion up <project>`, which reads the
   manifest and re-arms crons + restarts services. Closes gap #1, and lifts the
   existing scheduler/services from "survives restart" to "survives reboot" for
   free across all of Yaver.

3. **Serverless detector** — `yaver companion detect` reads the repo (Supabase
   functions, Convex `crons.ts`, Workers `wrangler.toml`, package scripts) and
   emits a *proposed* manifest with reasoning. This is the "plug-and-play" hook:
   point it at e-back and it surfaces the two unscheduled endpoints + the missing
   reconcile job.

4. **Web-UI wiring** — a "Companion" dashboard tab (same trivial
   import→register→case pattern as `company-ai`/`phone`): pick project → pick
   companion device (or buy a managed box inline) → review detected crons/services
   → toggle on → live status (next run, last result, logs). Reuses
   `cloudMachines` onboarding for the "buy a box" path.

5. **First-class init + managed cloud** — `InitProject` gains a "needs a
   companion?" step; managed-cloud onboarding gains a post-provision phase that
   runs `companion up` so a bought box arrives with the project's crons/services
   already armed and reboot-durable.

6. **Policy reuse** — register a `companion` workKind in the `companyAIOptions`
   `appProfile` so approvals/data-policy/MCP gating come for free; the AI-wrapper
   (Talos) case stays on the same resolver.

## Slot-in points (all follow existing patterns)

- Dashboard tab: `web/app/dashboard/page.tsx` (import → tabs[] → switch case).
- HTTP routes: new `companion_http.go` mirroring `phone_backend_http.go`.
- MCP verbs: `companion_detect`, `companion_up`, `companion_status`,
  `companion_cron_*` mirroring the `phone_project_*` family.
- Manifest engine: thin layer over `scheduler.go` + `services.go`.
- Durability: new unit generators (the only genuinely net-new runtime piece).

## Bottom line

~80% of the runtime already exists. The build is: (a) one durability primitive
(systemd/launchd unit gen — the real missing piece), (b) a manifest that unifies
crons+services+workers under a project, (c) a serverless detector, (d) a web tab,
(e) wiring into init + managed-cloud. Everything else is composition of primitives
Yaver already ships.
