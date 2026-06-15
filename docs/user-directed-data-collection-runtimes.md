# Yaver User-Directed Data Collection Runtimes

Date: 2026-06-15

Audience: Claude Code / Yaver implementation agent.

Purpose: define Yaver as a general user-controlled data collection, observation,
and task execution platform. This document is intentionally not centered on
Yaver Bet. Yaver Bet is only one example of a domain adapter that can consume
data collected by Yaver.

## Executive Summary

The product goal:

```text
User asks Yaver for data or monitoring.
Yaver decides how to collect it safely.
Yaver executes through managed cloud, self-hosted runtime, a browser collector
(CDP/chromedp today), redroid, mobile/manual input, or MCP.
Yaver stores normalized data and raw evidence.
Yaver exposes results through Web UI, mobile, MCP, API, and downstream tools.
```

Yaver should become the user's data collection layer across:

- public websites
- user-approved authenticated websites
- Android apps
- mobile user-present workflows
- local/private files and networks
- third-party MCP tools
- domain-specific adapters
- scheduled background jobs
- live monitoring tasks
- multiple machines, regions, and egress identities (multi-vantage collection)

Collection is not single-machine. A source can legitimately serve different
content by region or network origin (regional pricing, regional odds, localized
catalogs, geo-served availability). Yaver should be able to observe the same
source from several runtimes at once, label each observation with the vantage it
came from, and surface the differences. IP/egress identity and "this source is
blocked from this vantage" are first-class concepts, not edge cases. See
"Multi-Vantage Collection, Geo, and Egress/IP" below.

Yaver should not be a betting product. It should be a general collection product
that can support betting analysis, ecommerce price monitoring, SaaS status
tracking, app QA, research databases, local workflows, compliance audits,
business intelligence, and user-controlled automation.

Implementation rule:

```text
Build Yaver data collection first. Treat Yaver Bet as one adapter/use case.
Do not name the core runtime, UI, schema, MCP tools, or scheduling model around
betting concepts.
```

The important abstraction is:

```text
collection request -> runtime + vantage selection -> permission gate
-> execution (from one or many egress identities) -> extraction
-> normalization -> per-vantage storage -> audit -> downstream use
```

"Vantage" = the (runtime, egress IP, geo) tuple a single observation was
collected from. One request can fan out across many vantages.

## Core Product Thesis

Users often need AI to collect data for them, but the collection environment
matters:

- Some tasks need an always-on server.
- Some tasks need the user's own IP, laptop, files, or private network.
- Some tasks need a real browser.
- Some tasks need Android app observation.
- Some tasks need the user to solve a challenge or approve a sensitive step.
- Some tasks should only run through official APIs or MCP tools.
- Some tasks should stop immediately when a source blocks automation.

Yaver should unify those cases into one operator experience:

```text
Ask -> Plan -> Approve -> Run -> Inspect -> Reuse
```

The user should not need to think in terms of "Playwright vs redroid vs MCP vs
systemd". The user should ask for an outcome, and Yaver should present a clear
collection plan:

```text
I can collect this from:
1. a public web page with Playwright,
2. your self-hosted desktop because the page is behind your local network,
3. a managed cloud box because it must run every minute,
4. your mobile app because the source requires user-present verification,
5. an MCP connector because the domain already has a tool.
```

## Non-Goals

Yaver should not:

- bypass CAPTCHA/reCAPTCHA/hCaptcha or similar human verification systems
- bypass bot protection
- bypass device-integrity checks
- impersonate hidden human behavior
- create third-party accounts for the user
- automate KYC, identity verification, banking, SMS, or email takeover
- store third-party passwords/session cookies without a separately designed and
  compliant credential product
- scrape private/protected APIs without permission
- ignore site/app terms, access restrictions, or legal boundaries
- turn domain adapters into hidden policy circumvention tools
- rotate IPs, proxies, or geo-vantages to defeat a geo-block, IP ban, or
  rate limit a source has deliberately imposed
- present an egress identity the user is not entitled to use

Multi-vantage / egress support exists so the user can collect from machines,
networks, regions, and proxies they already own or are authorized to use, and to
honestly compare what each legitimate vantage is served. It is not an
anti-blocking or ban-evasion tool. When a source blocks a vantage, Yaver records
the block and stops for that vantage — it does not hop to a fresh IP to get
around it.

When Yaver meets a boundary, the correct product behavior is not "try harder".
The correct behavior is:

```text
pause -> show the user what happened -> request user-present action or stop
```

## Runtime Model

Yaver should treat all execution environments as "runtimes".

Runtime types:

```text
yaver_managed_cloud
self_hosted_desktop
self_hosted_server
self_hosted_vps
mobile_user_present
redroid_user_present
playwright_browser
external_mcp
official_api_connector
manual_entry
```

Each runtime advertises capabilities:

```json
{
  "runtime_id": "host_123",
  "type": "self_hosted_vps",
  "capabilities": {
    "always_on": true,
    "browser": true,
    "redroid": false,
    "docker": true,
    "systemd": true,
    "local_files": true,
    "private_network": false,
    "mobile_user_present": false,
    "external_mcp": true,
    "sqlite": true,
    "artifact_storage": true,
    "egress_proxy": true,
    "accepts_proxy": true
  },
  "egress": {
    "ip": "203.0.113.10",
    "geo": { "country": "DE", "region": "eu", "city": "Falkenstein", "asn": "AS24940" },
    "ip_detected_at": "2026-06-15T11:00:00Z",
    "stable_ip": true,
    "via_proxy": false,
    "via_peer": null
  }
}
```

`browser` replaces the older `playwright` flag: the capability is "this runtime
can drive a real browser", independent of the engine. The current
implementation is Chrome DevTools Protocol (chromedp), not Playwright — see the
Browser Collector section.

`egress` describes the network identity a source will actually see. Every
runtime advertises it. `egress_proxy: true` means this runtime can act as a
forward proxy so another runtime's collector exits through this IP.
`accepts_proxy: true` means this runtime's collector can be pointed at an
external/peer proxy. `via_peer` is set when this runtime's own egress is already
being routed through another runtime.

A collection request declares requirements:

```json
{
  "needs_schedule": true,
  "needs_browser": true,
  "needs_android": false,
  "needs_user_presence": false,
  "needs_private_network": false,
  "allowed_sources": ["public_web", "official_api"],
  "forbidden_actions": ["captcha_bypass", "login_automation"],
  "vantages": {
    "mode": "multi",
    "require_geo": ["eu", "us", "tr"],
    "require_distinct_egress": true,
    "egress_policy": "machine_native",
    "compare": true
  }
}
```

Yaver matches request requirements to runtime capabilities AND to egress/geo.
For `vantages.mode`:

```text
single   one runtime, whatever its egress is (default, backward compatible)
pinned   one specific runtime/region/egress
multi    fan out across N vantages, store one observation per vantage
```

`egress_policy` selects how each vantage's network identity is obtained:

```text
machine_native   exit from that runtime's own IP (no proxy)
peer_egress      route a collector on runtime A out through runtime B's IP
user_proxy       use a user-supplied HTTP/SOCKS proxy the user is entitled to
```

Yaver never invents egress to defeat a geo-block. `peer_egress`/`user_proxy`
exist so the user can collect from machines/networks they already control or are
authorized to use. Defeating access controls is a Non-Goal (see below).

## Managed Cloud vs Self-Hosted

Managed cloud and self-hosted should be peer options.

Managed cloud is best when:

- the user wants Yaver to provision compute
- the task must run while the user's laptop is offline
- stable timers and logs matter
- the task is public web/API collection
- the user wants simple setup
- the user wants Yaver to manage lifecycle, updates, and health

Self-hosted is best when:

- the user wants data and artifacts to remain on their own machine
- the task needs the user's local network
- the task needs local files or hardware
- the user wants their own VPS/IP/region
- the user wants lower compute cost on existing infrastructure
- the user wants to run redroid or Playwright in a controlled environment
- the task is sensitive and should not run on Yaver-managed machines

Comparison:

| Capability | Yaver managed cloud | Self-hosted desktop/server/VPS |
|---|---|---|
| Machine provisioning | Yaver | User |
| Agent pairing | automatic/bootstrap | user installs or bootstrap script |
| Always-on jobs | yes | yes if host is always on |
| Playwright | yes | yes |
| redroid | possible if host supports it | possible if user host supports it |
| Local network access | no, unless configured | yes |
| User files | no, unless synced | yes |
| Region/IP control | Yaver regions | user's host/VPS |
| Secret storage | Yaver vault + host env | local vault + host env |
| Raw artifacts | Yaver host storage | user storage |
| Debug shell | mediated by Yaver | local shell + Yaver |
| Cost model | Yaver compute billing | user's compute |

Yaver UI should present this as:

```text
Run on:
[Yaver Cloud] [This Computer] [My Server/VPS] [Mobile Session]
```

## Multi-Vantage Collection, Geo, and Egress/IP

This is a first-class part of the platform, not an add-on. Many sources serve
different content depending on the region or network origin of the request:
regional pricing, regional odds, localized catalogs, geo-gated availability,
A/B by network. A single-machine collector silently sees only one slice of
reality. Yaver should let the user observe the same source from several
vantages and compare.

### Definitions

```text
egress identity   the (ip, geo, asn) a source actually observes for a request
vantage           a (runtime, egress identity) pair an observation came from
multi-vantage run one collection request fanned across N vantages concurrently
egress policy     how a vantage's network identity is obtained
```

### Egress identity is advertised, not guessed

Every runtime reports its egress identity (see the runtime capability block):
detected public IP, resolved geo (country/region/city/ASN), whether the IP is
stable, and whether it is already routed through a proxy or peer. The collection
planner selects vantages from this advertised data — it never assumes a region
from a hostname.

Egress detection rules:

- detect best-effort (public-IP probe) and cache; re-check on a sane interval
- resolve geo from IP locally or via an allowed geo source; cache it
- treat a changed egress IP as a material event (re-tag observations, refresh
  source health) — a dynamic-IP home box is a different vantage after a reconnect
- never store the egress IP inside normalized rows as PII; store it as vantage
  metadata on the observation/run, redactable per retention policy

### Egress policies

```text
machine_native   collector exits from the runtime's own IP (default)
peer_egress      collector on runtime A is routed out through runtime B's IP,
                 so the source sees B (A and B are both user-controlled)
user_proxy       collector uses a user-supplied HTTP/SOCKS proxy URL the user
                 is entitled to use; credentials are vault-stored, never logged
```

`peer_egress` reuses the existing peer transport: runtime B exposes a scoped
forward proxy, runtime A's browser/collector is pointed at it. The browser
collector must accept a proxy argument for this to work (today it does not — see
gaps). `user_proxy` is for proxies the user already owns or has rights to.

Yaver must NOT:

- maintain a pool of rotating IPs to evade blocks
- auto-switch egress when a source blocks a vantage
- source third-party residential/botnet proxies
- present an egress the user is not entitled to use

### Multi-vantage run model

```text
request declares vantages: { mode, require_geo, egress_policy, compare }
  -> planner resolves a concrete vantage set from advertised runtimes
  -> if a required geo has no eligible runtime, planner says so (no faking)
  -> one collection_run fans to N runtimes concurrently (fleet fan-out)
  -> each runtime collects independently and returns observations
  -> every observation is tagged with vantage_id (runtime + egress + geo)
  -> normalization stores one row per (source, vantage, timestamp)
  -> if compare=true, a diff view highlights cross-vantage differences
  -> per-vantage source health: a block in one vantage does not fail the others
```

Example user asks this enables:

```text
Collect this product's price from EU, US, and TR and show me where it differs.
Watch these odds from my Frankfurt box and my home connection and alert on gaps.
Check whether this page is geo-gated by collecting it from two of my regions.
Confirm this app shows the same catalog from two of my devices in different
  countries.
```

### Vantage selection example

```json
{
  "collection_plan_id": "plan_geo_1",
  "vantage_mode": "multi",
  "compare": true,
  "vantages": [
    {
      "vantage_id": "v_eu",
      "runtime_id": "hetzner_fsn1_box",
      "egress_policy": "machine_native",
      "egress_geo": "eu",
      "egress_ip_known": true
    },
    {
      "vantage_id": "v_us",
      "runtime_id": "hetzner_ash_box",
      "egress_policy": "machine_native",
      "egress_geo": "us",
      "egress_ip_known": true
    },
    {
      "vantage_id": "v_tr_home",
      "runtime_id": "home_desktop",
      "egress_policy": "machine_native",
      "egress_geo": "tr",
      "egress_ip_known": true,
      "egress_ip_stable": false
    }
  ],
  "unsatisfied_geo": [],
  "stop_conditions": ["captcha_detected", "geo_block_detected", "ip_block_detected"]
}
```

If a required geo cannot be satisfied by any eligible runtime, the planner must
report it (`unsatisfied_geo`) instead of silently collecting from the wrong
place or pretending to be elsewhere.

### Block handling (geo-block, IP-block, rate-limit)

Blocks are first-class source/vantage states, with the same "pause, surface,
do not bypass" philosophy as challenges:

```text
geo_block_detected     source refuses this region ("not available in your
                       country", 451, geo redirect)
ip_block_detected      source has blocked/banned this IP/ASN (403 from this
                       egress only, datacenter-IP block, hard rate ban)
rate_limited           source is throttling this vantage (429, soft limits)
```

Correct behavior on a block:

```text
mark the VANTAGE blocked (not the whole source)
record block reason + which egress/geo saw it in source health
keep other vantages running
surface to the user: "blocked from <geo/ip>; available from <other vantages>"
offer user choices: use one of MY other entitled vantages, slow down, or stop
do NOT auto-rotate to a fresh IP to defeat the block
```

A geo/IP block from one vantage is often itself the signal the user wants ("this
is geo-gated; my US box sees it, my TR box does not"). Treat the block as data,
not a failure to route around.

### Cross-vantage comparison output

When `compare=true`, Yaver produces a diff keyed by source field across
vantages:

```text
field         v_eu        v_us        v_tr_home
price         €19.99      $17.99      ₺—  (geo_block_detected)
availability  in_stock    in_stock    blocked
```

Domain adapters consume the per-vantage rows; the comparison is generic and
lives in core (it is not betting-specific).

## User Request Flow

The user should be able to ask:

```text
Collect prices for these products every hour.
Watch this page and alert me when a number changes.
Open this app screen and extract the visible table.
Collect the latest public odds and put them into my SQLite DB.
Monitor these competitor landing pages for copy changes.
Run this MCP tool daily and store results.
Use my phone if a verification challenge appears.
```

Yaver turns that into a plan:

```json
{
  "goal": "collect public product prices hourly",
  "sources": [
    {
      "kind": "public_web",
      "url": "https://example.com/product/123",
      "collector": "playwright_visible_dom"
    }
  ],
  "runtime": {
    "preferred": "yaver_managed_cloud",
    "fallback": "self_hosted_vps"
  },
  "schedule": "hourly",
  "permission": {
    "requires_user_presence": false,
    "stop_on_challenge": true
  },
  "output": {
    "format": "sqlite",
    "dataset": "product_prices"
  }
}
```

Yaver should show the plan before enabling recurring collection.

## Collection Planner

Yaver needs a collection planner that decides:

- what source types are involved
- what data needs to be extracted
- whether the source is public, authenticated, private, or user-present
- whether official APIs/MCP connectors exist
- whether Playwright is permitted and suitable
- whether redroid/mobile observation is required
- whether the user must approve or perform a step
- where the job should run
- how often it should run
- what raw artifacts should be retained
- what schema should store the normalized data
- when to stop

Planner output should be explicit and auditable:

```json
{
  "collection_plan_id": "plan_abc",
  "risk_level": "medium",
  "runtime_candidates": ["self_hosted_vps", "yaver_managed_cloud"],
  "selected_runtime": "self_hosted_vps",
  "collector": "playwright_visible_dom",
  "requires_user_approval": true,
  "stop_conditions": [
    "captcha_detected",
    "login_wall_detected",
    "terms_warning_detected",
    "unexpected_payment_or_account_step"
  ],
  "artifact_policy": {
    "screenshots": "retain_7_days",
    "html": "retain_7_days",
    "normalized_rows": "retain_until_deleted"
  }
}
```

## Permission and Challenge Handling

Yaver should model source access states:

```text
public_allowed
official_api
authenticated_user_present
authenticated_connector
manual_required
permission_required
blocked_challenge
blocked_terms
blocked_device_integrity
blocked_geo
blocked_ip
rate_limited
unsupported
```

`blocked_geo`, `blocked_ip`, and `rate_limited` are tracked per vantage, not
just per source: a source can be `public_allowed` from one of the user's
vantages and `blocked_geo` from another. That asymmetry is itself useful data.

### CAPTCHA / reCAPTCHA Flow

Yaver must not solve or bypass CAPTCHA/reCAPTCHA. If a collector encounters a
challenge, it should pause and pass the challenge to the user when that is
allowed and useful.

Recommended flow:

```text
Playwright/redroid sees challenge
  -> collector pauses
  -> Yaver creates mobile approval task
  -> mobile app notifies user
  -> user opens live session/screenshot/remote view
  -> user decides whether to continue manually
  -> if user completes challenge, collector resumes only after normal page/app
     state is visible
  -> if user declines or challenge repeats, mark source manual_required/blocked
```

Important constraints:

- Yaver does not use CAPTCHA-solving services.
- Yaver does not use ML/OCR to solve challenge puzzles.
- Yaver does not hide the challenge from the source.
- Yaver does not fake device/browser integrity.
- Yaver does not keep trying at high frequency.
- Yaver records the event in source health.
- User completion should be explicit and visible in mobile/Web UI.

Mobile challenge task fields:

```json
{
  "type": "source_challenge",
  "source_id": "source_123",
  "runtime_id": "runtime_456",
  "challenge_kind": "recaptcha",
  "user_action": "open_live_session",
  "allowed_actions": ["continue_manually", "mark_manual_only", "stop_source"],
  "expires_at": "2026-06-15T12:10:00Z"
}
```

If the challenge requires account login, payment, KYC, identity verification,
SMS, banking, or sensitive information, Yaver should not automate the step. It
should only let the user control their own session and should not store secrets
or tokens.

## Browser Collector

Note on engine: this document historically said "Playwright". The shipped
implementation drives a real browser over the Chrome DevTools Protocol
(chromedp), not Playwright. "Browser collector" is the capability; the engine is
an implementation detail. Wherever "Playwright" appears below, read it as "the
browser collector". A future Playwright backend would satisfy the same contract.

Use the browser collector for web collection when:

- the page is public or user-approved
- browser rendering is required
- DOM extraction is more reliable than HTTP fetch
- screenshots are useful evidence
- the site allows normal browser access
- the task can stop on challenge/login/access boundary

Browser collectors should support:

- host/route allowlists
- frequency limits
- challenge detection
- login-wall detection
- geo-block / IP-block / rate-limit detection
- proxy / egress selection (`--proxy-server`, per-session, vault-stored creds)
- egress identity reporting (which IP/geo this session actually used)
- screenshot capture
- HTML snapshot capture
- visible text extraction
- structured DOM extraction
- network request logging with redaction
- schema mapping
- per-vantage source health rows
- artifact retention policy

Do not use Playwright for:

- hidden logged-in operation without user approval
- private/protected API replay
- CAPTCHA bypass
- betslip/payment/final-submit automation
- KYC or identity workflows
- high-frequency scraping against source limits

Collector status examples:

```text
ok
no_data
selector_changed
blocked_challenge
blocked_login_wall
blocked_terms
blocked_geo
blocked_ip
rate_limited
manual_required
parse_error
```

## redroid Collector

Use redroid when Android app observation is the right tool:

- mobile app QA
- screenshot-based extraction
- app UI regression checks
- user-present app observation
- reproducing Android bugs
- observing data only exposed in an app

redroid should be user-present for sensitive third-party apps. The user should
know what app is open and what Yaver is observing.

redroid collectors should support:

- app install/open
- screenshot capture
- OCR/text extraction
- accessibility-tree extraction when allowed
- tap/scroll only for approved flows
- source challenge detection
- device-integrity block detection
- user mobile handoff
- artifact retention

Do not use redroid to:

- bypass emulator checks
- bypass device integrity
- create accounts
- perform hidden logged-in actions
- submit payments, bets, orders, or irreversible actions
- store app sessions for unattended sensitive use

If an app blocks redroid:

```text
mark redroid_blocked
offer physical-device user-present workflow
do not bypass
```

## Mobile App Role

The mobile app is not only a dashboard. It is a user-presence and approval
surface.

Mobile should handle:

- approve collection plans
- choose runtime
- receive challenge notifications
- open a live browser/redroid session
- complete human verification when appropriate
- decline a source
- mark a source manual-only
- enter manual observations
- review extracted rows
- approve schema mappings
- pause/resume collectors
- receive alerts

Mobile should show clear states:

```text
Running
Paused for user
Challenge requires user
Manual-only source
Blocked by source
Blocked from this region/IP (other vantages still running)
Needs approval
Collecting from self-hosted
Collecting from Yaver Cloud
Collecting from N vantages
```

Mobile should never hide sensitive automation behind a background process. If
the workflow becomes account-sensitive, payment-sensitive, KYC-sensitive, or
irreversible, the user must be in control.

## Web UI Role

The Web UI is the operations console.

It should expose:

- collection requests
- generated plans
- runtime selection
- runtime egress/geo map
- vantage sets + cross-vantage diff
- source registry
- collector status
- schedule/timer state
- logs
- raw artifacts
- normalized rows
- schemas
- MCP connectors
- challenge queue
- block queue (geo/IP/rate, per vantage)
- user approval history
- audit output
- exports

Suggested navigation:

```text
Data Collection
  Requests
  Sources
  Runtimes
    Egress / Geo
  Vantages
  Collectors
  Datasets
  Artifacts
  Approvals
  Blocks
  Logs
  MCP Connectors
```

The Web UI should not be domain-specific by default. Domain adapters can add
their own pages, but the core UI should work for generic collection.

## MCP Role

MCP is how Yaver exposes collection and domain capabilities to agents.

Generic MCP tools:

```text
collection_request_create
collection_plan_preview
collection_plan_approve
collection_run_once
collection_run_multi_vantage
collection_schedule_enable
collection_schedule_disable
collection_status
collection_logs
collection_artifacts
collection_dataset_query
collection_vantage_compare
collection_source_register
collection_source_health
runtime_list
runtime_capabilities
runtime_egress
runtime_select
vantage_list
vantage_register
challenge_list
challenge_handoff_to_mobile
challenge_mark_manual_required
block_list
block_acknowledge
```

`runtime_egress` reports each runtime's advertised egress (ip/geo/asn/stable).
`vantage_list`/`vantage_register` manage the runtime+egress pairs.
`collection_run_multi_vantage` fans one request across a vantage set.
`collection_vantage_compare` returns the cross-vantage diff for a source.
`block_list`/`block_acknowledge` surface geo/IP/rate blocks and let the user
acknowledge them (acknowledge = "I see this is geo-gated", never "route around
it"). These register on the existing ops verb registry; machine routing, guest
scoping, and peer dispatch come for free.

External domain MCP tools can consume datasets:

```text
domain_tool_read_dataset
domain_tool_analyze_latest
domain_tool_backtest
domain_tool_alert
domain_tool_export
```

Yaver should distinguish:

```text
generic collection MCP = owned by Yaver
domain MCP = owned by adapter/project
```

For example, Yaver owns "collect this page every minute". A betting, ecommerce,
finance, QA, or research adapter owns "interpret these rows for my domain".

## Generic Data Model

Yaver should use generic tables/objects that can support many domains.

Core entities:

```text
collection_requests
collection_plans
collection_sources
collection_runtimes
collection_vantages
collection_runs
collection_artifacts
collection_observations
collection_schemas
collection_datasets
collection_source_health
collection_approvals
collection_challenges
collection_exports
```

`collection_vantages` is the runtime+egress identity an observation was
collected from. Every `collection_run` and every observation references a
vantage, so geo/IP provenance is always attached and never lost in
normalization. `collection_source_health` is keyed by (source, vantage), so a
geo/IP block in one vantage does not poison the source's health everywhere.

Suggested shape:

```sql
CREATE TABLE collection_sources (
  source_id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  source_type TEXT NOT NULL,
  base_url TEXT,
  app_package TEXT,
  access_state TEXT NOT NULL,
  allowed_runtime_types TEXT,
  notes TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE collection_vantages (
  vantage_id TEXT PRIMARY KEY,
  runtime_id TEXT NOT NULL,
  egress_policy TEXT NOT NULL,      -- machine_native | peer_egress | user_proxy
  egress_ip TEXT,                   -- last observed egress IP (vantage metadata)
  egress_geo_country TEXT,
  egress_geo_region TEXT,
  egress_asn TEXT,
  egress_ip_stable INTEGER DEFAULT 0,
  via_peer_runtime_id TEXT,         -- set when routed through another runtime
  proxy_ref TEXT,                   -- vault key for user_proxy creds; never plaintext
  last_checked_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE collection_runs (
  run_id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL,
  plan_id TEXT NOT NULL,
  runtime_id TEXT NOT NULL,
  vantage_id TEXT NOT NULL,         -- which runtime+egress this run used
  collector_type TEXT NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  rows_extracted INTEGER DEFAULT 0,
  artifacts_count INTEGER DEFAULT 0,
  egress_ip_used TEXT,              -- IP actually observed for this run
  egress_geo_used TEXT,
  block_kind TEXT,                  -- null | geo | ip | rate_limit
  error_code TEXT,
  error_message TEXT
);

CREATE TABLE collection_challenges (
  challenge_id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  source_id TEXT NOT NULL,
  challenge_type TEXT NOT NULL,
  status TEXT NOT NULL,
  handoff_surface TEXT,
  created_at TEXT NOT NULL,
  resolved_at TEXT,
  resolution TEXT
);
```

Domain adapters can mirror or import generic observations into domain-specific
schemas. Yaver Bet can import normalized odds observations into its own live DB.
An ecommerce adapter can import product rows. A QA adapter can import app screen
states.

## Artifact Policy

Artifacts are evidence and debugging material.

Artifact types:

```text
screenshot_png
html_snapshot
visible_text
ocr_text
accessibility_tree
network_log_redacted
json_response
csv_export
sqlite_snapshot
mobile_session_record
redroid_session_record
```

Artifact rules:

- redact secrets before storing
- never store passwords or session tokens
- separate raw artifacts from normalized rows
- allow per-source retention policies
- allow user deletion/export
- link artifacts to collection runs
- expose artifact provenance in Web UI
- avoid keeping sensitive app screenshots longer than necessary

Retention examples:

```text
public web screenshots: 7-30 days
public normalized data: until user deletes
sensitive user-present screenshots: 24 hours or disabled
logs with possible secrets: redacted and short retention
```

## Source Health

Every source should have health state.

Fields:

```text
last_ok_at
last_error_at
last_error_code
last_rows
last_artifact_id
challenge_count_24h
manual_required
blocked_reason
collector_version
schema_version
runtime_id
vantage_id
egress_ip
egress_geo
geo_block_count_24h
ip_block_count_24h
rate_limit_count_24h
```

Source health is keyed by (source, vantage). The same source can be `healthy`
from one vantage and `blocked_geo` from another; aggregate views roll the
per-vantage rows up to a source-level summary.

Health states:

```text
healthy
stale
selector_changed
manual_required
blocked_challenge
blocked_login_wall
blocked_device_integrity
blocked_geo
blocked_ip
rate_limited
permission_required
disabled_by_user
```

Yaver should use source health to avoid repeated unsafe retries.

## Scheduling

Schedulers differ by runtime:

```text
managed cloud -> systemd timer / Yaver job scheduler
self-hosted Linux -> systemd timer / cron
self-hosted macOS -> launchd
self-hosted Windows -> Task Scheduler
mobile -> push/user-present task
external MCP -> scheduled tool call
```

The UI should hide scheduler details behind:

```text
Run once
Run every N minutes
Run hourly
Run daily
Pause
Resume
Stop permanently
```

But the run detail should show the real scheduler for debugging.

## Runtime Selection Examples

### Public Web Price Monitor

```text
User: collect these product prices every hour.
Runtime: managed cloud or self-hosted VPS.
Collector: Playwright visible DOM or HTTP fetch.
Challenge behavior: pause and ask user; if repeated, mark manual_required.
Output: product_prices dataset.
```

### Local Admin Dashboard

```text
User: monitor my internal dashboard every 5 minutes.
Runtime: self-hosted desktop/server on the same network.
Collector: Playwright.
Challenge behavior: user-present if login expires.
Output: dashboard_metrics dataset.
```

### Android App QA

```text
User: open my app every build and check this screen.
Runtime: redroid on self-hosted or managed host.
Collector: redroid screenshot/accessibility.
Challenge behavior: not applicable for owned app.
Output: app_screen_observations dataset.
```

### Mobile User-Present Extraction

```text
User: this app blocks automation; I will open it manually and Yaver should help
extract the visible table.
Runtime: mobile_user_present or redroid_user_present.
Collector: screenshot/OCR/manual confirmation.
Challenge behavior: user controls all sensitive actions.
Output: manual_observations dataset.
```

### Geo / Multi-Vantage Price Diff

```text
User: collect this product's price from EU, US, and TR and show where it differs.
Runtime: fan out across an eu box, a us box, and a tr-resident vantage.
Collector: browser visible DOM, machine_native egress per vantage.
Vantage behavior: one observation per (source, vantage); compare=true.
Block behavior: if one vantage is geo-blocked, mark that vantage blocked_geo,
  keep the others, surface the asymmetry; do NOT rotate IPs to bypass.
Output: product_prices dataset with vantage column + cross-vantage diff view.
```

### Domain Adapter Consumption

```text
User: collect live market data and let my domain tool analyze it.
Runtime: managed cloud or self-hosted.
Collector: official API / Playwright / manual input.
Domain adapter: reads normalized rows through MCP or SQLite.
Output: adapter-specific decisions and reports.
```

## Domain Adapter Contract

A domain adapter should declare:

```json
{
  "adapter_id": "example_adapter",
  "name": "Example Adapter",
  "required_datasets": ["public_prices"],
  "mcp_server": {
    "transport": "http",
    "url": "http://127.0.0.1:8765/mcp"
  },
  "tools": [
    "analyze_latest",
    "backtest",
    "export_report"
  ],
  "forbidden_actions": [
    "account_creation",
    "payment_submit",
    "captcha_bypass"
  ]
}
```

Yaver should provide:

- runtime execution
- source management
- approvals
- artifacts
- generic datasets
- MCP registration
- logs
- scheduling

The adapter should provide:

- domain schema
- domain interpretation
- model logic
- backtesting or analysis
- domain-specific validation
- domain-specific UI panels if needed

## Implementation Plan

### Phase 1 - Runtime Registry

Implement/standardize:

- runtime list
- runtime type
- capabilities
- egress identity (ip, geo, asn, stable, via_proxy, via_peer) per runtime
- health
- pairing status
- managed vs self-hosted label
- supported collectors

Acceptance:

- Web UI can show Yaver Cloud and self-hosted devices in one list.
- MCP can query runtime capabilities.
- MCP `runtime_egress` reports each runtime's egress ip/geo.
- Managed-cloud boxes auto-tag their region/geo at provision time.
- Mobile can show which runtime (and which egress/geo) is running a task.

### Phase 2 - Collection Request and Plan

Implement:

- create collection request
- generate plan
- preview plan
- approve plan
- save plan
- run once

Acceptance:

- User can ask for a collection task.
- Yaver returns a concrete runtime/source/collector plan.
- User can approve or reject it.

### Phase 3 - Browser Collector (CDP/chromedp today)

Implement:

- allowlisted URL collector
- screenshot artifact
- visible text extraction
- structured selector extraction
- challenge/login detection
- source health
- run logs

Acceptance:

- Public pages can be collected into generic observations.
- Challenge triggers mobile/Web handoff instead of bypass.

### Phase 4 - Mobile Challenge Handoff

Implement:

- challenge queue
- push/mobile notification
- open live session or screenshot
- user actions: continue manually, mark manual-only, stop source
- audit row for resolution

Acceptance:

- reCAPTCHA/CAPTCHA does not get solved by automation.
- User can handle allowed human steps from mobile.
- Repeated challenges degrade source to manual_required.

### Phase 5 - redroid User-Present Runtime

Implement:

- redroid runtime capability
- session start/stop
- screenshot/OCR artifact
- user-present action approval
- block detection

Acceptance:

- Yaver can observe app screens where permitted.
- Emulator/device-integrity blocks stop the run.
- Sensitive actions stay user-controlled.

### Phase 6 - Generic Dataset and Artifact UI

Implement:

- dataset browser
- run history
- artifacts panel
- source health page
- export
- audit trail

Acceptance:

- User can inspect what Yaver collected, when, from where, and with which
  runtime.

### Phase 7 - Domain Adapter Integration

Implement:

- external MCP registration per adapter
- adapter dataset permissions
- adapter tool calls
- adapter-specific panel slots

Acceptance:

- A domain adapter can consume generic Yaver datasets without owning runtime
  orchestration.

### Phase 8 - Multi-Vantage, Egress, and Geo (lands with Phase 1 + 3)

> Build status (2026-06-15): the egress + multi-vantage core is BUILT and tested
> in `desktop/agent` (uncommitted). Files: `browser.go` (per-session proxy +
> `CheckEgressIP`), `egress.go` (egress identity + `runtime_egress` verb),
> `egress_proxy.go` (B-side auth-gated, opt-in forward proxy with RFC1918/port
> guardrails + audit), `egress_bridge.go` (A-side local CONNECT proxy +
> `egress_via_peer_*` verbs), `collection_store.go` + `collection_ops.go`
> (local-first vantage-keyed data model + `collection_*` / `block_list` verbs).
> Tests: `browser_proxy_test.go`, `egress_test.go`, `egress_proxy_test.go`,
> `collection_test.go` (all green; real servers, no mocks). Not yet built:
> Convex-side region auto-tag surfacing in the device picker, the web/mobile
> vantage-compare UI, and the planner's vantage selection.

This is first-class, not optional. It depends on the runtime registry (Phase 1)
and the browser collector (Phase 3) and should be built alongside them, not
bolted on later — the data model (`collection_vantages`, vantage-keyed
observations and source health) must exist before any single-vantage code
hardens around a one-machine assumption.

Implement:

- egress detection + geo resolution per runtime (reuse public-IP detection),
  cached and re-checked; treat IP change as a material vantage event
- `collection_vantages` table + vantage-keyed runs/observations/source-health
- browser collector proxy support (`--proxy-server`, per-session, vault creds)
- peer-egress: a runtime exposes a scoped forward proxy; another runtime's
  collector routes out through it (reuses existing peer transport)
- `vantages.mode` in requests (single | pinned | multi) and planner vantage
  resolution, including honest `unsatisfied_geo` reporting
- multi-vantage fan-out for one request (reuse fleet fan-out)
- geo-block / IP-block / rate-limit detection as per-vantage stop conditions
- cross-vantage comparison view
- MCP: `runtime_egress`, `vantage_list/register`, `collection_run_multi_vantage`,
  `collection_vantage_compare`, `block_list/acknowledge`

Acceptance:

- User can collect one source from several owned/entitled vantages and see a
  per-field cross-vantage diff.
- Each observation carries its vantage (runtime + egress ip/geo); provenance
  survives normalization.
- A geo/IP block on one vantage is recorded and surfaced without failing the
  other vantages.
- Yaver never auto-rotates IPs/proxies to defeat a block; a required geo with no
  eligible runtime is reported, not faked.
- Browser collector can be pinned to a runtime's native egress, a peer egress,
  or a user-supplied proxy.

## Product UI Sketch

### Data Collection Home

Sections:

```text
New Request
Active Collectors
Paused for User
Sources
Datasets
Runtimes
Artifacts
Approvals
```

### New Request

Fields:

```text
What do you want Yaver to collect?
Where is the source?
How often?
Where should it run?
Can Yaver use browser automation?
Can Yaver use Android/redroid?
Should Yaver ask you on mobile if verification appears?
Where should results be stored?
```

Runtime selector:

```text
Auto
Yaver Cloud
This Computer
My Server/VPS
Mobile only
```

### Paused for User

Rows:

```text
Source
Reason
Runtime
Started
Action
```

Actions:

```text
Open on Mobile
View Screenshot
Continue Manually
Mark Manual Only
Stop Source
```

## Security Requirements

Yaver must separate:

```text
configuration
secrets
raw artifacts
normalized rows
logs
approvals
domain outputs
```

Rules:

- no secrets in logs
- no secrets in screenshots
- no third-party passwords in generic collection records
- no hidden background account operation
- per-source retention
- per-runtime trust label
- user-visible approval history
- clear stop conditions
- least-privilege adapter access to datasets
- egress IPs stored only as vantage metadata (run/observation provenance),
  never embedded in normalized rows, redactable per retention policy
- proxy credentials for `user_proxy` egress live in the vault, never in config,
  logs, plan JSON, or Convex
- no rotating-IP / proxy-pool infrastructure; egress is limited to runtimes and
  proxies the user owns or is entitled to use

Trust labels:

```text
public
user_private
account_sensitive
payment_sensitive
identity_sensitive
forbidden
```

Identity-sensitive and payment-sensitive collection should require explicit
product design before any automation support. The generic collector should stop.

## Failure Modes

### Challenge Appears

Action:

```text
pause
create challenge task
notify mobile/Web
wait for user
resume only after user-approved visible state
```

### Login Wall Appears

Action:

```text
pause
ask whether this source should be user-present/manual
do not collect credentials
```

### Selector Changes

Action:

```text
mark selector_changed
save screenshot/html
ask user or agent to update extractor
```

### Runtime Offline

Action:

```text
mark runtime_offline
show last successful run
offer move to another runtime
```

### redroid Blocked

Action:

```text
mark blocked_device_integrity
offer physical device/mobile manual workflow
do not bypass
```

### Source Rate Limits

Action:

```text
back off
show source health warning
disable aggressive schedules
```

### Geo-Block or IP-Block Appears

Action:

```text
mark the VANTAGE blocked_geo or blocked_ip (not the whole source)
record which egress/geo saw the block in source health
keep other vantages running and report what they still see
surface to user: "blocked from <geo/ip>, available from <other vantages>"
offer: collect from one of MY other entitled vantages, slow down, or stop
do NOT auto-rotate IPs/proxies to defeat the block
```

Treat the block as a data point about the source, not a routing problem to
solve. A source that is reachable from one of the user's regions and blocked
from another is often exactly the finding the user wanted.

## Acceptance Criteria

A working general Yaver data collection system must prove:

1. User can create a generic collection request.
2. Yaver can generate a plan with runtime, collector, source, schedule, and stop
   conditions.
3. User can choose managed cloud or self-hosted runtime.
4. The browser collector can collect from an approved public page and store
   artifacts.
5. redroid can run as a user-present observation runtime where supported.
6. CAPTCHA/reCAPTCHA pauses the run and creates a mobile challenge task; Yaver
   does not solve or bypass it.
7. Mobile can approve, continue manually, mark manual-only, or stop a source.
8. Web UI can show runs, logs, source health, artifacts, and datasets.
9. MCP can create requests, inspect plans, run collectors, and query datasets.
10. Domain adapters can consume collected datasets without owning the collection
    runtime.
11. Each runtime advertises its egress identity (ip, geo, asn), and MCP can
    query it.
12. A single request can fan out across multiple vantages; every observation is
    tagged with the runtime + egress/geo it came from.
13. A geo-block or IP-block on one vantage is recorded per vantage and surfaced,
    without failing other vantages and without auto-rotating IPs to bypass it.
14. The browser collector can be pinned to a runtime's native egress, a peer
    egress, or a user-supplied proxy, and reports the egress it actually used.
15. When a required geo has no eligible runtime, Yaver reports it instead of
    collecting from the wrong place or faking the location.

## What This Means For Yaver Bet

Yaver Bet should be treated as an adapter:

```text
Yaver collects licensed/public/manual live observations.
Yaver Bet imports or reads those observations.
Yaver Bet performs betting-domain analysis.
Yaver Web UI/mobile may expose a Yaver Bet panel, but the core collection
architecture remains generic.
```

Do not let Yaver Bet-specific concepts leak into generic Yaver:

```text
bookmaker -> source
live odds -> observation
bet decision -> domain output
paper ledger -> domain dataset
license gate -> domain policy gate
```

This keeps Yaver reusable for non-betting use cases.

## Related Documents

In `../yaver.io`:

```text
YAVER_CLOUD_HANDOFF.md
YAVER_MCP_COVERAGE.md
YAVER_MCP_SELF_HOST_HANDOFF.md
MOBILE_HEADLESS.md
web-headless/README.md
docs/REMOTE_REDROID_AI_TESTING_ANALYSIS.md
docs/managed-cloud-go-live-runbook.md
```

In `../yaver-bet`, as one adapter example:

```text
LIVE_BETTING_HANDOFF.md
SERBIAN_LICENSE_AND_PRIOR_ODDS.md
SERBIAN_BETTING_SITE_FIT.md
```
