# Yaver MCP Coverage — One Developer Tool, Everywhere

## Elevator pitch

Yaver is a **super-tool for monorepo app development**. A developer uses it as
their primary workspace — CLI, mobile app, web dashboard, desktop app —
for everything that happens between "clone the repo" and "ship to
production": building, deploying, debugging, running AI coding agents,
managing backends, pushing Hermes bytecode to a phone, exporting
projects to the Yaver cloud, inviting teammates, reading black-box
crashes, rotating a cert.

Yaver is **simultaneously** a first-class **MCP server**. Every
capability the primary tool has — every button in the mobile app, every
command in the CLI, every card in the web dashboard — is reachable as an
MCP tool with the exact same semantics. A developer who lives inside a
different AI coding agent (Claude Code, Cursor, Aider, Codex, Goose,
Opencode, whatever) can add Yaver as an MCP provider and have their
vibe-coding session drive the real Yaver engines: their hot-reload
pushes a Hermes bundle to their physical iPhone, their "deploy this"
runs the TestFlight pipeline, their "move this project to the cloud"
calls `cloud_deploy` through Yaver. **The agent never knows; Yaver did
the work.**

This document is the contract: what the MCP surface covers, how it
maps to the primary tool, how we keep the two from drifting, and what
is still missing.

---

## Two modes, one engine

Yaver has to work identically in two modes, or it breaks the promise:

| Mode | Who drives | Where Yaver runs | Who sees the result |
|---|---|---|---|
| **Primary tool** | Human at keyboard / phone | Their own machine (or cloud dev machine) | Them |
| **MCP provider**  | Another agent (Cursor, Claude Code, Aider, ...) acting for the human | Same machine as the agent, or a peer | The human's app, their phone, their cloud — but via the agent |

The ONLY difference between the two modes is the call site. The work
is identical:

- `yaver push ./apps/bento` (CLI) ⇄ `mobile_tap_push_project` (mobile) ⇄ `POST /push/bundle` (agent HTTP) ⇄ `phone_project_push` (MCP).
- They **all** compile Hermes bytecode via the embedded hermesc and
  POST it to the phone's on-device HTTP server. Same bytes, same
  safe-reload sequence, same SDK-manifest handshake.

That identity is not an aspiration; it's the design invariant. The MCP
dispatcher in `desktop/agent/httpserver.go` calls the **same Go
function** the CLI command calls. The mobile UI's `P2PClient` hits the
**same agent HTTP endpoint** the MCP tool proxies through. This is why
the coverage can actually be complete.

---

## What "coverage" means

Three things make Yaver's MCP coverage different from a random tool
wrapping a bash CLI:

1. **Every feature surface is reachable.** When a feature ships to the
   mobile tab, it ships to CLI, to the web dashboard, AND to MCP — in
   the same PR. The `CLAUDE.md` "tech" section lists 200+ MCP tools
   today; the ones that are missing are tracked as gaps (see "What's
   missing" below) and are closed feature-by-feature, not
   retroactively.

2. **Single source of truth for semantics.** Input schemas for MCP
   tools are generated from the same Go structs the HTTP handlers
   validate against. An agent that calls `create_task` with the
   wrong shape gets the same validation error a CLI user would.

3. **Streaming by default.** Anything that takes more than a second
   (install, build, deploy, hot-reload, AI run) exposes
   an SSE/stream channel (`/streams/<name>`). MCP tools return the
   stream handle; the agent subscribes and sees live lines, subprocess
   stdout, sudo prompts, success/failure frames — exactly what the
   mobile user would see in the terminal view.

When you see `mcp__yaver__X` in the tool list of an LLM, you can
assume X works the same way it would from any other Yaver surface, on
the same devices, against the same state. That's the coverage promise.

---

## The MCP surface, by domain

This is organised by what a **monorepo app developer** actually reaches
for day-to-day. Each domain lists the tools that already exist.

### 1. Monorepo essentials

The baseline a dev needs to navigate a repo with dozens of apps,
libraries, tools.

| Capability | Tools |
|---|---|
| Enumerate projects across a monorepo | `list_projects`, `phone_project_list`, `mobile_project_status` |
| Read + edit a project's config | `get_config`, `config_set`, `init_project`, `set_work_dir` |
| Wizard for new project (phone / desktop / monorepo sub-package) | `project_wizard_start`, `project_wizard_answer`, `project_wizard_generate`, `project_new_quick`, `template_list`, `template_use` |
| Files + search | `search_files`, `search_content`, `read_file`, `write_file`, `list_directory`, `tree_dir` |
| Local shell when the tool doesn't fit | `exec_command` (streamed, auth'd) |
| Git surface | `git_info`, `git_log_advanced`, `git_stats`, `git_tags`, `git_branches`, `git_blame_file`, `git_remotes`, `git_reflog`, `git_shortlog`, `git_stash` |
| Change cost estimation | `lines_of_code`, `loc_count`, `cyclomatic_complexity`, `lizard` |

The crucial UX win: when the agent asks "what apps live in this repo?"
it calls one MCP tool and gets the same answer the mobile Projects tab
shows, with the same slug + stack detection. Not a re-implementation.

### 2. Build & deploy

| Platform | Primary MCP tools |
|---|---|
| **Go** | `go_build`, `go_test_suite`, `go_vet_check`, `go_fmt_check`, `go_generate`, `go_module_info`, `go_module_versions`, `go_mod_graph`, `go_mod_tidy`, `go_mod_why`, `go_staticcheck`, `go_pprof_cpu`, `go_pprof_heap`, `go_vulncheck`, `gosec` |
| **Node / TS** | `npm_info`, `npm_run_script`, `npm_search`, `npm_versions`, `lint`, `tsc_check`, `eslint_check`, `prettier_check`, `biome_suite`, `drizzle_generate`, `drizzle_push`, `prisma_generate`, `prisma_push`, `prisma_status` |
| **Rust** | `cargo_build`, `cargo_check_only`, `cargo_test_suite`, `cargo_bench_suite`, `cargo_clippy`, `cargo_fmt`, `cargo_audit_deps`, `cargo_doc`, `cargo_clean`, `cargo_add_crate`, `cargo_remove_crate`, `cargo_update_deps`, `cargo_tree_deps`, `cargo_search`, `crates_info`, `crates_search` |
| **Python** | `pytest_suite`, `mypy_check`, `ruff_suite`, `black_format`, `bandit`, `safety_check`, `uv_install`, `pip_compile`, `pip_list`, `pip_show`, `pypi_info`, `pypi_versions` |
| **Flutter / Dart** | `flutter_build`, `flutter_doctor`, `flutter_test`, `pubdev_info`, `pubdev_search` |
| **Android / Kotlin** | `gradle_build`, `gradle_test`, `android_lint` |
| **iOS / Swift** | `xcode_build`, `xcode_test` |
| **C / C++** | `gcc_compile`, `clang_compile`, `cmake_build`, `cmake_configure`, `cmake_install`, `cmake_test`, `cppcheck`, `clang_tidy_check`, `clang_format_file`, `make_run`, `make_targets`, `make_clean` |
| **Docker** | `docker_build`, `docker_compose`, `docker_images`, `docker_ps`, `docker_exec`, `docker_cp`, `docker_restart`, `docker_rm`, `docker_rmi`, `docker_logs`, `docker_top`, `docker_stats`, `docker_inspect`, `docker_network`, `docker_volumes`, `docker_pull`, `docker_push`, `docker_prune`, `hadolint`, `dockerhub_search`, `dockerhub_tags` |
| **Kubernetes / Helm** | `k8s_apply`, `k8s_describe`, `k8s_events`, `k8s_exec`, `k8s_get`, `k8s_logs`, `k8s_namespaces`, `k8s_pods`, `k8s_top`, `k8s_contexts`, `helm_list`, `helm_repos`, `helm_history`, `helm_search`, `helm_status`, `helm_values` |

### 3. Mobile pipeline — Yaver's flagship

This is where Yaver is unique. MCP makes every step of the RN/Flutter
mobile dev loop reachable from any AI coding agent.

| Flow | Tools |
|---|---|
| **Hermes push (vibe-reload)** to a physical iPhone/Android via LAN beacon or relay | `phone_project_push`, `mobile_project_prepare`, `mobile_project_build` — compiles Hermes bytecode with the embedded `hermesc`, validates HBC header, pushes to the phone's on-device HTTP server, triggers safe-reload (invalidate + GC wait + `ExpoReactNativeFactory`) |
| **Simulator / Emulator flows** | `simulators`, `simulator_boot`, `simulator_screenshot`, `emulators`, `adb_devices`, `adb_command`, `adb_screenshot` |
| **TestFlight deploy** | `testflight_builds` + the `scripts/deploy-testflight.sh` pipeline (MCP wrapper: `mobile_project_build` + existing release action) |
| **Play Store deploy** | `playstore_status`, `playstore_track` + `scripts/deploy-playstore.sh` |
| **Expo / EAS** | `eas_build`, `eas_submit`, `expo_status` |
| **Dev server proxy** (Metro / Flutter / Vite / Next hot-reload over relay) | `dev_start`-equivalent via the agent HTTP (`/dev/start`, `/dev/reload`, `/dev/stop`) |
| **Crash + feedback loop** | `error_list`, `error_resolve`, `firebase_crashlytics`, `blackbox` SSE channel for live log streams from the phone |
| **App Store status, review check** | `appstore_status`, `app_review_check` |
| **Pod + native module health** | `pod_install`, `pod_outdated` |
| **iOS / Android install-method toggles** | `get_ios_install_method`, `set_ios_install_method` |

**Worked example — the "vibe-reload from Cursor" flow:**

1. Developer is in Cursor. Yaver CLI is running on the same Mac as `yaver serve`. Phone is on same Wi-Fi, TestFlight Yaver app installed.
2. Cursor's Claude Code calls `mcp__yaver__phone_project_push` with `{ workDir: "./apps/bento" }`.
3. Yaver's Go agent bundles with Metro, runs embedded `hermesc`, validates HBC version against `sdk-manifest.json`, POSTs the bundle to the phone's `YaverHTTPServer` on port 8347.
4. Phone's native bridge invalidates, waits for Hermes GC, spawns a fresh bridge via `ExpoReactNativeFactory`.
5. New code runs on the phone in ~3–5 seconds.

Cursor never had to know there was a phone involved, or what a
TurboModule is, or that the bundle had to go through a LAN beacon
before falling back to the relay. Yaver handled the whole thing.

### 4. Backend engines — 19 targets, one surface

Yaver's "switch engine" can migrate a project between any two of 19
backends (Convex, Supabase, Postgres/Neon, MySQL/PlanetScale, SQLite,
MongoDB, Firestore, DynamoDB, D1, KV, R2, Turso, Redis, Elasticsearch,
etc.) with a 7-day rollback snapshot.

| Capability | Tools |
|---|---|
| **Enumerate + inspect current backend** | `backend_status`, `backend_collections`, `backend_schema`, `backend_records`, `backend_users`, `data_browse`, `data_query`, `data_tables` |
| **CRUD against whichever backend is wired** | `data_insert`, `data_update`, `data_delete`, `data_browse` |
| **Switch / migrate** | `switch_plan`, `switch_run`, `switch_rollback`, `switch_cost`, `switch_cleanup`, `switch_history`, `switch_runner`, `switch_targets` |
| **Migration surface** | `migrate_plan`, `migrate_run`, `migrate_rollback`, `migrate_status`, `migrate_targets`, `migrate_verify` |
| **Provider-specific** | `convex_*` (15 tools), `supabase_*` (5), `pscale_branches`, `cf_d1`, `cf_kv`, `cf_r2`, `cf_pages`, `cf_workers` |
| **DB ops** | `db_push`, `db_pull`, `db_migrate`, `db_seed`, `db_studio`, `db_backup`, `db_restore`, `db_reset`, `db_status`, `db_query`, `db_schema`, `db_generate` |
| **Phone-first mini backends** | `phone_project_create`, `phone_project_schema`, `phone_project_seed`, `phone_project_export`, `phone_project_promote` — the whole "build a backend from your phone and promote it to Convex later" loop |

The contract: a MCP agent calling `backend_records` on a Supabase
project and a Convex project gets the same shape. `switch_run` moves
state + config + code between them and never loses data.

### 5. Cloud & infrastructure — one tenant, many providers

| Capability | Tools |
|---|---|
| **Yaver Cloud tenants** | `cloud_plans`, `cloud_status`, `cloud_provision`, `cloud_deploy`, `cloud_redeploy`, `cloud_destroy`, `cloud_scale`, `cloud_backup`, `cloud_logs`, `cloud_cli`, `cloud_emu_start`, `cloud_emu_stop`, `cloud_emu_status`, `cloud_emu_config` |
| **Cloudflare** | `cf_d1`, `cf_kv`, `cf_r2`, `cf_pages`, `cf_workers`, `cf_deploy` |
| **Vercel / Netlify / Fly / Railway** | `vercel_env`, `vercel_logs`, `vercel_status`, `netlify_status`, `fly_deploy`, `fly_logs`, `fly_status`, `railway_deploy`, `railway_status` |
| **Firebase** | `firebase_deploy`, `firebase_projects`, `firebase_crashlytics` |
| **AWS-ish** | `lambda_invoke`, `lambda_list`, `lambda_logs` |
| **Terraform / IaC** | `tf_apply`, `tf_init`, `tf_output`, `tf_plan`, `tf_state`, `tf_validate` |
| **Platform deploys (Yaver-managed)** | `platform_apps`, `platform_deploy`, `platform_init`, `platform_logs`, `platform_preview`, `platform_redeploy`, `platform_remove`, `platform_status`, `platform_webhook` |
| **DNS + domain + SSL** | `dns_add`, `dns_flush`, `dns_list`, `dns_lookup`, `dns_remove`, `domain_add`, `domain_check`, `domain_detect_ip`, `domain_list`, `domain_setup`, `domain_dns_check`, `domain_ddns_start`, `domain_ssl_status`, `ssl_check` |
| **Remote dev boxes (GPU / CPU)** | `remote_provision`, `remote_deploy`, `remote_destroy`, `remote_status`, `remote_setup`, `remote_exec`, `remote_cost` |
| **Infra power / inventory** | `infra_summary`, `infra_power`, `infra_service_action`, `agent_machine_inventory`, `console_machines` |

**"Export to Yaver cloud"** — the flagship flow: from any agent,
`cloud_provision` + `cloud_deploy` takes a local project and puts it
on a dedicated CPU or GPU machine with all the dev tools pre-installed
(node, go, rust, python, docker, expo, eas, ollama when GPU), with
Yaver's multi-user + relay already wired. The agent gets back an URL
+ a SSH-free way to attach. See `docs/remote-worker.md` for the
mechanics.

### 6. DevOps, observability, reliability

| Capability | Tools |
|---|---|
| **Uptime + monitors** | `uptime_monitor_add`, `uptime_monitor_remove`, `uptime_monitor_list`, `uptime_monitor_history`, `uptime_monitor_status`, `monitor_add`, `monitor_list`, `monitor_remove` |
| **Performance** | `perf_compare`, `perf_lighthouse`, `perf_loadtest`, `perf_record`, `perf_stat`, `benchmark`, `binary_size`, `bandwidth_test`, `speed_test`, `http_timing`, `http_status`, `curl_timings` |
| **Logs** | `docker_logs`, `vercel_logs`, `cloud_logs`, `lambda_logs`, `fly_logs`, `platform_logs`, `k8s_logs`, `journalctl`, `syslog`, `tail_logs`, `log_search`, `auth_log`, `services_logs`, `yaver_logs`, `yaver_clear_logs` |
| **Metrics** | `system_info`, `cpu_info`, `free_memory`, `load_average`, `iostat`, `vmstat`, `df`, `du`, `disk_usage`, `find_large_files`, `process_list`, `process_kill`, `ps_aux`, `ps_tree`, `top_snapshot`, `listen_ports`, `network_connections`, `network_interfaces` |
| **Tracing + debugging** | `gdb_attach`, `gdb_backtrace`, `gdb_core_dump`, `lldb_attach`, `lldb_backtrace`, `strace_trace`, `ltrace_trace`, `valgrind_memcheck`, `valgrind_massif`, `valgrind_callgrind`, `perf_record`, `heaptrack`, `coredump_list`, `coredump_info` |
| **Security scans** | `trivy_fs_scan`, `semgrep`, `bandit`, `gosec`, `brakeman`, `safety_check`, `deps_audit`, `cargo_audit_deps`, `go_vulncheck`, `go_staticcheck`, `sonarscanner` |
| **Error aggregation** | `sentry_issues`, `firebase_crashlytics`, `error_list`, `error_resolve` |
| **Linear / GitHub** | `linear_issues`, `github_issues`, `github_prs`, `github_ci_status`, `github_releases`, `github_repo_info`, `github_stars`, `github_trending`, `create_gist`, `gitlab_issues`, `gitlab_mrs`, `gitlab_pipelines`, `gitlab_ci` |

### 7. Collaboration — guests, teams, support, session transfer

Yaver's strength: a human can bring another human (or another AI
agent) into the SAME machine with **scoped** access. Every collab
primitive is MCP-exposed.

| Capability | Tools |
|---|---|
| **Guest access (share your machine)** | `guest_invite`, `guest_list`, `guest_revoke`, `guest_config`, `guest_usage` |
| **Teams (shared machine mode)** | `cloud_provision --multi-user --team=<id>`, team membership management via Convex |
| **Remote support (TeamViewer-style)** | `support_start`, `support_status`, `support_stop` |
| **AI session transfer** (Claude Code ⇄ Codex ⇄ Opencode) | `session_transfer`, `session_export`, `session_import`, `session_list` |
| **Chat / comms** | `chat_conversations`, `chat_history`, `chat_reply`, `email_send`, `email_get`, `email_list_inbox`, `email_search`, `email_sync`, `mail_inbox`, `mail_draft`, `mail_dev_*` (7 tools) |

### 8. Edge workers & the "mobile is a dev machine too" story

Yaver treats idle phones + Macs + Vostros as compute nodes that can
run small models or background jobs while their user sleeps.

| Capability | Tools |
|---|---|
| **Agent graph (distributed inference fabric)** | `agent_graph_list`, `agent_graph_show`, `agent_graph_start`, `agent_graph_stop`, `agent_machine_inventory` |
| **Edge profiles (phone capabilities)** | `edgeProfile` on every device record — surfaced via `mobile_api_devices` |
| **Model hosting** | `models_list`, `models_pull`, `models_recommend`, `models_remove`, `models_run`, `models_serve`, `models_ps`, `models_status` |
| **Ollama / local inference** | `copilot_complete`, `copilot_models` (Qwen / DeepSeek / …) |
| **Voice (on-prem or cloud)** | `voice_*` suite in agent (via `/voice/providers`, `/voice/transcribe`) — PersonaPlex 7B, OpenAI Realtime, Whisper, Deepgram, AssemblyAI |

### 9. AI runners

Yaver wraps *every* major AI coding CLI, so a MCP caller can pick the
one that fits the task + budget + privacy posture.

| Capability | Tools |
|---|---|
| **Kick a one-shot task on a runner** | `create_task`, `continue_task`, `stop_task`, `get_task`, `list_tasks` |
| **Idea / init scaffolding** | `autoideas_*`, `autoinit_start`, `autoinit_status` |
| **Stream subprocess output** | any MCP tool that runs a long job returns a stream name; subscribe via `/streams/<name>` for real-time frames |
| **Model + runner management** | `models_list`, `models_pull`, `models_run`, `models_serve` |

### 10. Security, secrets, auth

| Capability | Tools |
|---|---|
| **Vault (on-device AES-GCM + Argon2id)** | `vault` family — local-only, never Convex |
| **1Password** | `op_get`, `op_list` |
| **API keys (per-provider, per-scope)** | `sdk_token_*` (create, rotate, list, revoke — each token has allowed IPs, scopes, expiry) |
| **OAuth (Apple / Google / Microsoft / GitHub / GitLab / email)** | `yaver_auth_link_start`, `yaver_auth_link_wait`, `yaver_auth_merge_start`, `yaver_auth_merge_wait`, `yaver_auth_list_identities`, `yaver_auth_status`, `yaver_auth_unlink`, `yaver_auth_poll`, `yaver_auth_start`, `yaver_auth_wait`, `yaver_auth_logout`, `yaver_auth_factory_reset` |
| **ACL / federation** | `acl_add_peer`, `acl_call_peer_tool`, `acl_list_peers`, `acl_list_peer_tools`, `acl_remove_peer`, `acl_health` |
| **Change password, forgot, TOTP** | `change_password`, `forgot_password`, `totp_enable_begin`, `totp_enable_confirm`, `totp_disable`, `totp_status` |
| **Auth dev loop** | `auth_dev_start`, `auth_dev_setup`, `auth_dev_status`, `auth_dev_stop`, `auth_dev_tokens`, `auth_dev_users`, `auth_oauth_setup`, `auth_oauth_save`, `auth_oauth_test`, `auth_oauth_list`, `auth_oauth_migrate`, `auth_log` |
| **Container sandbox** | `sandbox_status`, `sandbox_config`, `sandbox_quickstart` — optional Docker-based isolation for guest tasks |

### 11. Data plane — browser, storage, pipelines

| Capability | Tools |
|---|---|
| **Browser automation (runs on YOUR machine)** | `browser_click`, `browser_close`, `browser_evaluate`, `browser_extract_attribute`, `browser_extract_text`, `browser_get_dom`, `browser_navigate`, `browser_open`, `browser_screenshot`, `browser_scroll`, `browser_select`, `browser_sessions`, `browser_type`, `browser_wait`, `browser_wait_navigation` |
| **Shared storage (attach disk to every runner)** | `shared_storage_upsert`, `shared_storage_list`, `shared_storage_search`, `shared_storage_delete`, `shared_storage_profiles` |
| **Object storage (managed)** | `storage_upload`, `storage_list`, `storage_bucket_create`, `storage_bucket_list`, `storage_presign`, `storage_start`, `storage_stop`, `storage_status`, `storage_config` |
| **Pipelines (CI-style)** | `pipeline_run`, `pipeline_status`, `pipeline_stop`, `pipeline_list`, `pipeline_cancel_cloud`, `pipeline_hardware` |
| **Jobs** | `jobs_enqueue`, `jobs_list`, `jobs_cancel`, `jobs_retry`, `schedule_task`, `list_schedules`, `cancel_schedule`, `crontab`, `cron_list`, `cron_explain`, `countdown`, `timer` |
| **Analytics (self-host + hosted)** | `analytics_dashboard`, `analytics_events`, `analytics_selfhost_events`, `analytics_setup`, `analytics_start`, `analytics_status`, `analytics_stop` |
| **Billing / customers / invoices / AB** | `customer_create`, `customer_list`, `invoice_create`, `invoice_list`, `invoice_payment_link`, `invoice_render_pdf`, `invoice_send`, `affiliate_create`, `affiliate_list`, `affiliate_conversion`, `affiliate_payout`, `ab_assign`, `ab_event`, `ab_experiment_create`, `ab_experiment_list`, `ab_results` |
| **Lemonsqueezy** | `lemonsqueezy_create_discount`, `lemonsqueezy_customers`, `lemonsqueezy_discounts`, `lemonsqueezy_orders`, `lemonsqueezy_products`, `lemonsqueezy_revenue`, `lemonsqueezy_setup`, `lemonsqueezy_status`, `lemonsqueezy_subscriptions`, `lemonsqueezy_webhook_listen`, `lemonsqueezy_webhook_stop` |

### 12. Networking & transport

| Capability | Tools |
|---|---|
| **Relay servers (self-host or Yaver-managed)** | `add_relay_server`, `remove_relay_server`, `relay_set_password`, `relay_clear_password`, `relay_test`, `get_relay_config` |
| **Tunnels (Cloudflare, custom)** | `tunnel_add`, `tunnel_list`, `tunnel_remove`, `tunnel_test` |
| **Expose local service** | `expose_start`, `expose_stop`, `expose_list` |
| **Network diag** | `ping`, `dns_lookup`, `nmap_scan`, `port_scan`, `port_check`, `http_request`, `traceroute_host`, `mtr_report`, `public_ip`, `ip_geo`, `ip_route`, `iptables_list`, `ufw`, `arp_table`, `arp_scan`, `subnet_calc`, `wifi_info`, `hostname_info`, `tcpdump`, `tcpdump_dns`, `tcpdump_http`, `tshark`, `pcap_analyze`, `pcap_stats`, `netcat`, `whois`, `wake_on_lan` |
| **Presence (relay-driven)** | `GET /presence` endpoint on relay, mobile + web + MCP all read it |

### 13. Misc — because devs are also humans

| Capability | Tools |
|---|---|
| **Time / world clock / weather** | `world_clock`, `weather`, `epoch` |
| **Clipboard / say / music / volume / brightness / screenshot / screen lock** | `clipboard_read`, `clipboard_write`, `say`, `music`, `volume`, `brightness`, `screenshot`, `screen_lock` |
| **HA / Hue / Sonos / Shelly / Tasmota / MQTT** | all exposed — the mobile app ships with the lights-down ergonomic |
| **Stripe** | `stripe_listen`, `stripe_status`, `stripe_stop`, `stripe_trigger` |

---

## The "vibe from elsewhere" contract

An AI coding agent elsewhere (Cursor, Claude Desktop, Aider's repl,
Goose, Opencode, Amp, whatever) adds Yaver as an MCP server. From
then on, the agent's toolbox silently gains everything above.

The canonical flows we optimise for:

### Flow A: Hermes hot-reload from any agent

```
[Cursor / Claude Code / Aider / Goose]
   |
   | calls mcp__yaver__phone_project_push {workDir: "./apps/bento"}
   v
[Yaver agent on the dev Mac]
   |
   | 1. Bundle with Metro (embedded hermesc, strict BC-version validation)
   | 2. POST bundle to phone's :8347 (LAN beacon, fall back to relay)
   | 3. phone.YaverBundleLoader invalidates old bridge, waits for Hermes GC
   | 4. phone.ExpoReactNativeFactory creates a new bridge, loads new code
   v
[Phone runs new code in ~3s]

Agent sees: {"ok":true, "bundleBytes":1234567, "elapsedMs":3120}
```

### Flow B: TestFlight deploy from any agent

```
[Agent]
  -> mcp__yaver__mobile_project_build  {platform:"ios", track:"testflight"}
[Yaver]
  -> archives + exports + uploads via App Store Connect API key
  -> returns streamId; agent subscribes to /streams/<id>
  -> live xcodebuild frames flow back (errors, warnings, upload progress)
[App Store Connect]
  -> processes, gets the build number, notifies Yaver
[Agent]
  -> polls mcp__yaver__testflight_builds until status == "ready"
```

### Flow C: Export to Yaver cloud (managed)

```
[Agent]
  -> mcp__yaver__cloud_plans           (list + pick)
  -> mcp__yaver__cloud_provision       (spins up a CPU/GPU machine)
  -> mcp__yaver__cloud_deploy          (pushes the repo + boots yaver serve)
  -> mcp__yaver__cloud_status          (polls)
[New remote dev machine]
  -> running yaver serve --multi-user --team=<id>
  -> shows up in the user's mobile device list immediately
  -> agents (Claude Code, Codex, Aider) pre-installed
[Agent can now:]
  -> mcp__yaver__session_transfer     (move the current vibe session to the cloud box)
```

### Flow D: Vibe on phone while commuting, resume on desktop

```
Phone session (voice input + mobile-headless-driven)
  -> mcp__yaver__session_export {format:"zip"}
Desktop (agent pulls via relay)
  -> mcp__yaver__session_import {bundle:...}
```

All four flows are **already possible today** via the MCP tools listed
above. They wire the exact same engines the primary Yaver tool uses.

---

## Coverage matrix

How many of the listed features are reachable from each surface? This
table is the scoring board — it should only ever get more green.

| Feature domain | CLI | Mobile | Web | MCP | Notes |
|---|:-:|:-:|:-:|:-:|---|
| Monorepo essentials | ✅ | ✅ | ✅ | ✅ | |
| Build (per language) | ✅ | ⚠️ | ⚠️ | ✅ | Mobile shows build status only; CLI/MCP run the build |
| Hermes push-to-device | ✅ (`yaver push`) | ✅ | — | ✅ | Web can only *initiate*; requires agent |
| TestFlight / Play | ✅ | ⚠️ | — | ⚠️ | Scripted, not yet a first-class MCP schema — tracked |
| Dev server hot-reload | ✅ | ✅ | ✅ | ✅ | `/dev/*` suite + SSE |
| Backends (switch engine) | ✅ | ✅ | ✅ | ✅ | 19 targets, 7-day rollback |
| Phone-first mini backend | ✅ | ✅ | ✅ | ✅ | |
| Cloud provision / deploy | ✅ | ✅ | ✅ | ✅ | Hetzner CPU/GPU |
| Guests / teams / support | ✅ | ✅ | ✅ | ✅ | 5 tools each |
| Session transfer | ✅ | ⚠️ | — | ✅ | Web pending |
| Autoideas / autoinit | ✅ | ✅ | ✅ | ✅ | |
| Black-box streaming | ✅ | ✅ | ✅ | ✅ | SDK provides ring buffer |
| Voice (STT + S2S) | ✅ | ✅ | ⚠️ | ✅ | Web lacks mic UI |
| Vault | ✅ | ✅ | ⚠️ | ✅ | Web cannot decrypt — on-device only |
| SDK tokens | ✅ | ✅ | ✅ | ✅ | |
| Cloudflare / Vercel / Netlify / Fly / Railway | ✅ | ⚠️ | ⚠️ | ✅ | Mobile + web show statuses; actions via CLI/MCP |
| Relay self-host | ✅ | ⚠️ | ⚠️ | ✅ | Setup script; mobile + web can swap URLs |
| Browser automation | ✅ | ⚠️ | — | ✅ | Mobile shows sessions; agent runs it |
| DNS / domain / SSL | ✅ | ⚠️ | ⚠️ | ✅ | |
| Analytics | ✅ | ⚠️ | ✅ | ✅ | |
| Docker / K8s / Helm | ✅ | — | — | ✅ | Not a phone UX |
| Remote dev box (GPU) | ✅ | ✅ | ✅ | ✅ | |
| Primary device + multi-IP + relay presence | ✅ | ✅ | ✅ | ✅ | Latest rollout |

Green ✅ = full parity. Yellow ⚠️ = partial (read-only, or a button
that kicks CLI/MCP underneath). Dash "—" = deliberately not on that
surface.

---

## Design principles

These are non-negotiable. If an MCP tool violates one, we fix the tool.

1. **One tool, one verb, one payload shape.** A tool either does one
   thing or it's split. `guest_config` without args lists configs;
   with `email` returns one; with `email` + `daily_limit` writes. That
   overload is tolerable. But never pack two unrelated operations into
   one tool — agents can't reason about it.

2. **Every long-running tool returns a stream handle.** `install`,
   `cloud_deploy`, `mobile_project_build`
   all immediately return `{streamId}`; the agent follows via
   `/streams/<id>` SSE. The stream event schema is normalised: `line`,
   `sudo_prompt`, `result`, `event`. No tool blocks the agent loop for
   more than 30 seconds.

3. **P2P by default.** MCP tools that touch data (tasks, feedback,
   output, vault, projects) go through the agent's local HTTP / P2P
   channel — never through Convex. The `convex_privacy_test.go`
   enforces this at the code level (forbidden-key list + absolute-path
   scanner).

4. **Same input, same output, every surface.** Changes to an input
   schema must update: the Go handler validator, the MCP tool schema,
   the mobile client method, the web component, the CLI flag parser.
   The drift test (`mobile-headless/test/drift.test.ts`) catches
   mobile drift; a similar test catches MCP drift.

5. **Idempotent by default.** Re-invoking a tool with the same args
   either no-ops or converges to the same state. Destructive tools
   (revoke, remove, rollback, cancel) are explicit verbs; non-
   destructive ones are "upsert-flavoured". No surprises.

6. **Typed refusals.** When a tool can't do something (unauthorized,
   guest-scope violation, missing dependency, schema mismatch), it
   returns a structured error with a `code` that's stable enough for
   the agent to branch on. No free-text-only errors.

7. **Streamable preview.** Tools that produce files (bundle, archive,
   PDF, report) return a `previewUrl` served by the agent's HTTP so
   the user can see it in the mobile app without the agent needing to
   re-upload it. The URL is short-lived and bound to the current
   session.

8. **Self-host vs managed is a config, not a fork.** `relayUrl`
   controls which relay is used. `cloud_plans` lists managed plans but
   a user can also `remote_provision` their own VPS and point
   everything at it. The MCP surface doesn't care — tool behaviour is
   identical either way.

---

## What's missing (honest gap list)

These are the known coverage gaps. Each one is a single PR worth of
work.

1. **First-class MCP schemas for TestFlight + Play Store release**
   flows. Right now it's `mobile_project_build` + shell scripts; needs
   a proper tool with `platform`, `track`, `version`, and a streamed
   result frame instead of "tail the log and hope".

2. **`ops` meta-tool** that dispatches to every provider with a single
   verb (`ops deploy`, `ops logs`, `ops env`) so agents don't need to
   learn 50 CF-specific + 50 Vercel-specific tools. Track target via a
   project-level `provider:` setting.

3. **Per-feature `managed: true|false` toggle.** Relay has this; DNS,
   storage, analytics, email, CI don't — users still have to pick a
   provider for each. The endgame: one checkbox per feature, the user
   never sees a provider name.

4. **Web parity for voice, vault, browser.** Non-trivial (needs a
   native bridge), but today these only work via mobile + CLI +
   MCP-on-agent.

5. **MCP drift test.** We have `mobile-headless/test/drift.test.ts`
   for the mobile surface; need an analogous `mcp-drift.test.ts` that
   catches silent removal of MCP tools.

6. **Session transfer for web.** Web can start a session (Claude Code
   in browser), but can't import/export it. The CLI + MCP can.

7. **Relay presence push to Convex.** Today mobile pulls `/presence`
   from the relay on every device-list refresh. A relay→Convex push
   would give truly real-time presence in the Convex reactive UI
   without mobile needing to poll.

8. **A unified search MCP tool.** "Find everything named X in my
   Yaver universe" — across projects, devices, tasks, phone projects,
   guests, runners, etc.

9. **Declarative multi-project setup.** A monorepo with 12 apps today
   needs `init_project` per app. A `monorepo_init` or
   `workspace_register` that takes a manifest and wires everything
   (autoinit per app, shared vault, shared relay, per-app primary
   device defaults) would collapse a half-day of setup into a minute.

10. **TUI dashboard mode for CLI.** Right now the CLI is command-
    line-only; a `yaver tui` with tabs for Devices / Tasks /
    Projects would mirror the mobile app's information density.

---

## Appendix: tool naming convention

- `<domain>_<verb>[_<qualifier>]` — `guest_invite`, `backend_collections`, `switch_plan`, `phone_project_promote`.
- Domains are nouns, verbs are imperatives. Read verbs are `list`, `show`, `get`, `status`. Write verbs are `create`, `update`, `delete`, `run`, `invite`, `revoke`.
- Tools that proxy to a specific provider use the provider name as domain: `cf_workers`, `vercel_logs`, `lemonsqueezy_products`, `supabase_migrations`.
- Tools that are mobile-surface equivalents of agent HTTP routes use `mobile_tap_*` (screen actions) and `mobile_api_*` (raw endpoints). This is a mobile-headless convention and lives in `mobile-headless/src/bin/mcp.ts`; agent MCP tools do not use that prefix.

---

## Coverage analysis — ground truth from the source

The claims above should be falsifiable. They are. Running two
analysers over the `desktop/agent/` source yields concrete numbers:

```
$ grep '"name":' mcp_tools.go      -> 744 tool definitions
$ grep 'case "..." :' httpserver.go -> 744 dispatch cases (+28 noise from
                                        nested switches, filtered out)
registered = 744
dispatched = 744
both       = 744      # every registered tool has a handler
reg-only   = 0        # no orphaned tool advertisements
dsp-only   = 0        # no hidden tools pretending to be features
```

That's the good news: **every MCP tool that shows up in `tools/list`
actually works**. No silent "unknown tool" traps for an agent that
dispatches against them. This is the guarantee we keep with the
`mcp-drift.test.ts` (pending — see gap #5 below).

### What's covered, measured

Against the CLI — the other surface a dev has — 57 top-level
commands dispatch from `main.go`. Of those, the vast majority have a
matching MCP tool family:

| CLI command | MCP tool(s) | Notes |
|---|---|---|
| `yaver attach` | `gdb_attach`, `lldb_attach` | |
| `yaver build` | `cargo_build`, `cmake_build`, `docker_build`, `eas_build`, `flutter_build`, `gcc_compile`, `go_build`, `gradle_build`, `make_run`, `xcode_build` | |
| `yaver ci` | `github_ci_status`, `gitlab_ci` | |
| `yaver clean` | `cargo_clean`, `make_clean`, `switch_cleanup` | |
| `yaver cloud` | `cloud_*` (14 tools) | |
| `yaver connect` | `account_connect`, `account_disconnect` | |
| `yaver devices` | `adb_devices`, `yaver_devices` | |
| `yaver factory-reset` | `yaver_auth_factory_reset` | |
| `yaver install` | `cmake_install`, `convex_install_helper`, `get_ios_install_method`, `pkg_install`, `pod_install`, `set_ios_install_method`, `uv_install` | plus streamed installer via `mobile_api_install` |
| `yaver logs` | `convex_logs`, `docker_logs`, `fly_logs`, `k8s_logs`, `lambda_logs`, `platform_logs`, `vercel_logs`, `yaver_logs` | |
| `yaver machine` | `agent_machine_inventory`, `console_machines` | |
| `yaver primary` | `device_primary_get`, `device_primary_set` | rolled out this cycle |
| `yaver push` | `docker_push`, `drizzle_push`, `prisma_push` + phone push via `phone_project_push` / `mobile_project_build` | |
| `yaver setup` | `analytics_setup`, `auth_dev_setup`, `lemonsqueezy_setup`, `yaver_lazy_setup` | |
| `yaver shutdown` | `agent_shutdown` | |
| `yaver status` | `*_status` (26 tools) | one per domain |
| `yaver test` | `*_test*` (27 tools) | |

This is what "broad coverage" actually looks like: one CLI verb
explodes into 10–30 MCP tools grouped by subsystem.

### True gaps — CLI features with no MCP equivalent

The following CLI commands have **zero** related MCP tools today.
Each one is a concrete work item.

| CLI command | What it does | Why it matters for vibe-coding |
|---|---|---|
| `yaver apikey` | Manage provider API keys in the local keychain | Agent can't rotate its own keys over MCP today |
| `yaver backup` | Snapshot config + vault + projects | Can't back up from a remote agent driving this one |
| `yaver blob` | Manage blob storage (`~/.yaver/blobs/`) | Blob attachments invisible to MCP callers |
| `yaver debug` | Toggle debug logging, dump diagnostics | Remote diagnostics require SSH today |
| `yaver feedback` | Fire a Feedback-SDK bug report from CLI | Agents can create tasks but not proper feedback reports |
| `yaver flags` | Feature-flag evaluate / list / set | Works in-process but can't be driven by another agent |
| `yaver pair` | Pair a fresh agent via relay passkey | Bootstrap flow is manual-only |
| `yaver permissions` | Request macOS accessibility/automation perms | N/A via MCP — intrinsically GUI |
| `yaver phone` | Phone-first project operations (distinct from `phone_project_*`) | Only the `phone_project_*` CRUD is MCP; the compound flow isn't |
| `yaver purge` | Destructive wipe of tasks/sessions | Probably correct to gate off MCP |
| `yaver sdk` | SDK token CLI (create/rotate/revoke) | Agent can't manage its own SDK tokens |
| `yaver sdk-token` | Same domain, different verbs | Same gap |
| `yaver set-runner` | Pick which AI runner the user prefers | Multi-agent flows can't switch |
| `yaver signout` | Drop the auth token | Intentional — gates agent hijack |
| `yaver sourcemaps` | Upload sourcemaps for crash symbolication | Mobile release flow needs this |
| `yaver stream <name>` | Subscribe to a log stream | Partial — `/streams/{name}` SSE is accessible via `mobile_api_get`, but there's no dedicated MCP tool |
| `yaver uninstall` | Remove yaver + data | Intentional |
| `yaver vault` | Local AES-GCM vault management | **Important gap** — no MCP-gated vault access path |
| `yaver wipe` | Full-device factory reset | Intentional |

After stripping the commands that are intentionally MCP-blocked
(`signout`, `uninstall`, `wipe`, `purge`, `permissions`), the true
actionable gap list is **15 tool families**. Each can be closed with
a single MCP tool registration + dispatch case, reusing the existing
Go handler.

### Why the gaps exist

Historical pattern: the CLI ships first when a feature is new. MCP
coverage is added in the "wire-up" PR that comes one or two cycles
later, which sometimes slips. The `mcp-drift.test.ts` work item
(gap #5 in the main list) closes the loop — it fails CI when a new
CLI command ships without a matching MCP tool.

---

## Remote-ops MCP — the "perfect" surface for vibe coders

The thesis in the elevator pitch: a developer is inside any AI
coding agent, has **at least one Yaver-provisioned dev machine** (cloud
CPU, cloud GPU, a physical Mac Mini at home, someone else's shared
machine) and wants to run ops on it purely through Yaver MCP. Today
this works in pieces; making it *perfect* means the agent's mental
model collapses to:

> *"I have machines. I do ops on them. Yaver handles the transport."*

The machines have names. The ops have verbs. That's the API.

### The contract

```
ops(<machine>, <verb>, <payload>) -> stream<result>
```

`<machine>` is either:
- a Yaver `deviceId` (own, guest-accessed, or team-shared),
- a device alias (`"primary"`, `"gpu"`, `"mac-mini"`, etc.),
- the sentinel `"local"` for the machine the agent is running on,
- a list (`"all"`, `"all-owned"`, `"team:<teamId>"`) for fan-out.

`<verb>` is one of a small, named set (below).

`<payload>` is a typed struct per verb.

Routing is Yaver's problem: the call reaches the right agent via
LAN beacon → Tailscale → relay tunnel, in that order, using the
multi-IP heartbeat + real-time presence the previous cycle shipped.
The agent surfaces a `stream handle` on return; the caller
subscribes via `/streams/<id>` SSE to follow frames.

### Verb catalogue

The minimum viable set for vibe-coder remote ops. Everything else is
a specialisation.

| Verb | Purpose | Payload shape | Currently backed by |
|---|---|---|---|
| `info` | Machine specs + status snapshot | `{}` | `infra_summary`, `get_system_info`, `/info` |
| `run` | Execute a command, stream output | `{cmd, argv, cwd, env, timeoutSec}` | `exec_command`, `remote_exec` |
| `build` | Build a target (detects language) | `{workDir, target}` | `go_build`, `cargo_build`, `npm_run_script`, `mobile_project_build`, `gradle_build`, `xcode_build`, etc. |
| `test` | Run tests for a target | `{workDir, pattern?, coverage?}` | 27 existing `*_test*` tools |
| `deploy` | Deploy a target anywhere | `{workDir, target, provider?, env?}` | `cloud_deploy`, `vercel_deploy`, `fly_deploy`, `cf_deploy`, `platform_deploy`, `firebase_deploy`, TestFlight/Play flows |
| `push` | Push code/bundle to a target runtime | `{workDir, target: "phone"|"docker"|"remote"|"registry"}` | `phone_project_push`, `docker_push`, `prisma_push`, `drizzle_push` |
| `reload` | Hot-reload an in-flight process | `{workDir, mode: "dev"|"bundle"}` | `/dev/reload`, `/dev/reload-app` |
| `logs` | Tail / search logs | `{source, sinceSec?, grep?, tailLines?}` | all `*_logs` tools + agent streams |
| `status` | Service / deploy / build status | `{target?, provider?}` | all `*_status` tools |
| `env` | Read / write environment config | `{target, op: "get"|"set"|"list"|"unset", key?, value?}` | `vercel_env`, `env_list`, `env_read`, `flag_*` (unify) |
| `dns` | Manage DNS records for a domain | `{domain, op, record?}` | `dns_*` family |
| `cert` | Check / renew TLS | `{domain, op: "status"|"renew"}` | `ssl_check`, domain ssl tools |
| `secrets` | Manage secrets (vault, 1Password) | `{scope, op, key?, value?}` | `op_get`, `op_list`, vault CLI (needs MCP wire-up) |
| `files` | Read / list / write / delete | `{path, op, content?}` | `read_file`, `write_file`, `list_directory`, `search_files`, `search_content` |
| `stream` | Subscribe to a named stream | `{name}` | `/streams/<name>` SSE (currently `mobile_api_get` raw) |
| `session` | Transfer an AI coding session | `{op, to?, from?, runner?, workDir?}` | `session_transfer`, `session_export`, `session_import` |
| `scale` | Resize a cloud machine | `{deviceId, cpu?, ram?, gpu?}` | `cloud_scale` |
| `provision` | Create a new Yaver-managed machine | `{plan, region?, sshKey?}` | `cloud_provision`, `remote_provision` |
| `destroy` | Decommission a machine | `{deviceId, confirm}` | `cloud_destroy`, `remote_destroy` |
| `backup` | Snapshot / restore / list backups | `{op, target, id?}` | `cloud_backup`, `db_backup`, agent `/backup/*` (needs MCP) |

### Canonical vibe-coder flows

**Flow 1: Debug a Tailscale-connected Mac Mini remotely**

```
mcp__yaver__ops { machine: "mac-mini", verb: "info" }
  -> { cpu: 8, ram: 16GB, disk: "210GB free", online: true,
       runtimes: [{name:"go",version:"1.22.3"}, {name:"node",version:"22.8.0"}, ...] }

mcp__yaver__ops { machine: "mac-mini", verb: "logs",
                  payload: { source: "launchd", grep: "yaver", tailLines: 100 } }
  -> streamId; subscribe for real-time frames

mcp__yaver__ops { machine: "mac-mini", verb: "run",
                  payload: { cmd: "brew", argv: ["upgrade"], cwd: "/tmp" } }
  -> streamId; live output, exit frame
```

**Flow 2: Full mobile release from Cursor**

```
# 1. Build on the user's phone-reachable Mac
mcp__yaver__ops { machine: "primary", verb: "build",
                  payload: { workDir: "./apps/bento", target: "ios-release" } }

# 2. Push to TestFlight
mcp__yaver__ops { machine: "primary", verb: "deploy",
                  payload: { workDir: "./apps/bento", target: "testflight" } }

# 3. Wait for processing
mcp__yaver__ops { machine: "primary", verb: "status",
                  payload: { target: "testflight", workDir: "./apps/bento" } }
```

**Flow 3: Cloud GPU provisioning + job run**

```
# 1. Provision a fresh GPU box (Yaver cloud plan)
mcp__yaver__ops { machine: "local", verb: "provision",
                  payload: { plan: "gpu-4000", region: "hel1" } }
  -> { deviceId: "devx-9c71...", online: false, eta: "~90s" }

# 2. Wait for it to come online
mcp__yaver__ops { machine: "devx-9c71", verb: "info" }

# 3. Run the training job on it
mcp__yaver__ops { machine: "devx-9c71", verb: "run",
                  payload: { cmd: "python", argv: ["train.py","--epochs=3"],
                             cwd: "/workspace/ml" } }

# 4. Tear down
mcp__yaver__ops { machine: "devx-9c71", verb: "destroy",
                  payload: { confirm: true } }
```

**Flow 4: Move a session from phone to GPU machine**

```
# Phone is running Claude Code inside yaver-mobile-headless
mcp__yaver__ops { machine: "iphone-15", verb: "session",
                  payload: { op: "export" } }
  -> { bundle: "...zip..." }

mcp__yaver__ops { machine: "gpu", verb: "session",
                  payload: { op: "import", bundle: "..." } }
  -> gpu picks up the imported context for the next task
```

### Why this surface is better than today's 744 tools

**Today**: the agent needs to know `cloud_deploy` vs `vercel_deploy`
vs `fly_deploy` vs `cf_deploy` vs `platform_deploy` vs `firebase_deploy`
vs `eas_build + submit`. Six tools for "deploy". Each has its own
input schema. The agent has to learn all six schemas.

**With `ops`**: one tool, one verb `deploy`, one payload shape with a
provider discriminator. The provider-specific behaviour is behind the
Go handler where it belongs, keyed off the project's `provider:`
config OR an explicit override. The agent learns the shape once and
it works for every provider the user adds next week.

**Today**: routing to a remote device requires guessing the right
peer-federation tool (`acl_call_peer_tool`) plus knowing the peer's
deviceId + permissions model.

**With `ops`**: `machine:` is a first-class field. Yaver looks up
the device, reaches it via the best transport (LAN beacon > Tailscale
> relay > tunnel, per the multi-IP heartbeat), applies the right
auth (owner / guest / support-bearer), and the verb runs.

**Today**: streaming is per-tool — some tools return synchronous
JSON, some return `{streamId}`, some block for minutes. Agents have
no uniform way to treat them.

**With `ops`**: every verb returns `{streamId, initialFrame?}`.
Every verb. Agents subscribe or don't, but the interface is the same.

### Implementation plan

1. **Ship `ops` as a meta-tool alongside the existing 744.** Don't
   break anyone — specific tools keep working; `ops` is additive.
2. **Build the dispatcher inside `desktop/agent/ops.go`.** It parses
   `verb`, looks up the handler from a registry populated at startup,
   calls it with the remote-routing middleware.
3. **Remote routing.** When `machine != "local"`, the `ops` handler
   resolves the machine via `listMyDevices`, picks the best transport
   (same logic as `raceDirectCandidates` on the mobile side), and
   forwards the call to the peer agent's `/ops` endpoint. The peer
   runs the verb locally and streams frames back.
4. **Auth propagation.** Caller's session token rides along;
   destination enforces owner/guest/support scope against the
   forwarded request. Reuses existing `auth()` middleware — no new
   trust boundary.
5. **Schema generation.** Each verb handler registers its payload
   schema once; `tools/list` auto-derives the `ops` tool's
   `inputSchema.payload.oneOf` list. No hand-written schema drift.
6. **Mobile + CLI surfaces.** `yaver ops <machine> <verb> ...` on
   CLI. Mobile: every feature button internally issues an `ops` call
   to the active device; makes dispatch uniform there too.

A rough size estimate: ~1,000 LOC in `ops.go` + ~400 LOC per verb
handler. 19 verbs, many of which reuse existing handlers behind a
thin adapter, puts the whole surface at ≈ 5–7 KLoC. A single release
cycle.

### Coexistence

The 744 domain tools don't go away. They continue to exist for
agents that want them (specificity is still valuable — `cloud_backup`
does something a generic `ops backup` can't easily express). `ops`
is the door for agents that want one API and don't care about
provider details. Both coexist, both covered by drift tests.

---

## Session log — what shipped in this round

What got built on top of the 744-MCP-tools baseline:

### Grand-MCP `ops` meta-tool

The unified verb API. One MCP tool (`ops`) + a companion discovery
tool (`ops_verbs`) lets an agent learn the Yaver surface in one
schema instead of 744 domain tools.

**20 verbs shipped**, each registering itself via `registerOpsVerb` in
`desktop/agent/ops_<verb>.go`:

```
info      run       status     env        files
logs      session   secrets    build      push
test      deploy    reload     provision  scale
destroy   workspace dns        cert       backup
```

Transport: `/ops` HTTP endpoint on the agent; MCP tool dispatch via
`handleMCPToolCallWithAddr` in `httpserver.go`; CLI `yaver ops
<verb>` in `ops_cmd.go`.

Remote routing works: `machine != "local"` forwards via the existing
`proxyToDevice` peer proxy. The `primary` alias resolves to
`userSettings.primaryDeviceId` via `ops_resolve.go`.

Guarantees:
- Stable error codes for agent branching: `unknown_verb`,
  `bad_payload`, `unauthorized`, `remote_failed`, `remote_malformed`,
  `invalid_machine`, `internal`.
- Panic recovery — a buggy verb can't take the daemon down.
- Guest-scope gating per verb via `AllowGuest: false` default.
- Every long-running verb returns a `streamId` for SSE follow-up.

### Monorepo manifest

`yaver.workspace.yaml` declares apps + stacks + per-app providers +
shared infra. Dogfooded against this repo:

```
$ yaver workspace list
yaver.io — 16 app(s):
  - backend           convex              ./backend
  - cli               node                ./cli
  - desktop-agent     go                  ./desktop/agent
  - …
  - mobile            react-native-expo   ./mobile             ← backend
  - web               nextjs              ./web                ← backend
  - mobile-headless   bun                 ./mobile-headless    ← mobile
```

Commands:
- `yaver workspace init [--scaffold --force --dry-run --app --autoinit]`
- `yaver workspace list`
- `yaver workspace status`

MCP: `workspace_init`, `workspace_list`, `workspace_status`,
`workspace_scaffold` + ops verb `workspace`.

Engine (`workspace_engine.go`):
- Path check per app
- Env-var check (per-app `env:` + workspace `shared.env`)
- `init.md` scaffold (autoideas / autoinit read it as cached
  context — saves minutes of re-grepping every kick)
- Optional `yaver autoinit` hint per app

Parser (`workspace.go`):
- YAML shape validation
- Duplicate-name detection
- Dependency cycle detection via Kahn's topo-sort with stable
  alphabetic tiebreak

### Per-subsystem managed toggle

`userSettings.managed = {relay, dns, analytics, storage, email, ci,
voice, llm}` — each accepts `true` (Yaver-hosted) | `false`
(self-hosted) | `null` (unset → legacy default).

Plumbing shipped:
- Convex schema + mutation validator (`mergeManagedPatch` in
  `userSettings.ts` merges partial patches so setting one subsystem
  doesn't wipe the others)
- `/settings` HTTP POST forwards the field
- MCP: `managed_get`, `managed_set`
- CLI: `yaver managed get/set <subsystem> <true|false|null>`

What still needs wiring: **the implementers** (DNS code, analytics
code, email code, …) must read the flag and branch their provider
selection. That's a per-subsystem PR but the plumbing is done.

### Relay-driven real-time presence

- Relay: `GET /presence?ids=a,b,c` returns `{online, since,
  uptimeSec}` per id. Unknown ids return `online:false`
  indistinguishably from "offline" — no enumeration leak.
- Relay: optional push to Convex on tunnel up/down when
  `CONVEX_PRESENCE_URL` + `CONVEX_PRESENCE_SECRET` env vars are set
  (`relay/convex_presence.go`). Convex stores `lastTunnelEvent` on
  the device record; reactive clients see state flip in ~2s.
- Convex: `/devices/presence` HTTP action validates a platform-level
  shared secret (`platformConfig.relay_presence_secret`) and runs
  `devices.presenceUpdate` mutation.
- Mobile: `Device.lastTunnelEvent` surfaced as a cyan **RELAY LIVE**
  badge on device cards when the event is fresh (within 60s).

### Multi-IP heartbeat + parallel-race connect

- Agent enumerates every reachable IPv4 (Wi-Fi, Tailscale `100.x`,
  Ethernet, VPNs) via `getLocalIPs()`; heartbeat broadcasts the full
  set every 30s.
- Convex `devices.localIps` schema field stores the array; returned
  via `listMyDevices`.
- Mobile `quic.ts::raceDirectCandidates` races beacon IP + every
  heartbeat IP + Convex primary host in parallel via `Promise.any`
  with AbortController cancellation. New connection-path labels:
  `lan-tailscale`, `lan-heartbeat`.
- Fake-success bug fixed: `connect()` throws when no path actually
  reached the agent (was silently logging `[connect-success] via
  null`).
- Reconnect attempts reduced 15 → 3 (user UX note).

### Auto-connect + primary device

- Schema: `userSettings.primaryDeviceId`.
- `registerDevice` auto-marks a user's **first** registered device
  as primary — single-device users never see a picker.
- Mobile auto-connect rule: (1) single online → auto, (2) multi +
  primary set → primary, (3) multi + no primary → wait for user.
- Surfaces: mobile PRIMARY ★ badge + long-press, web dashboard +
  DevicesView toggle, CLI `yaver primary`, MCP `device_primary_get/set`.

### Remaining coverage gaps — closed

Per the original audit:
- **Vault MCP** — already existed (verified)
- **Flag MCP** — already existed (verified)
- **SDK-token MCP** — shipped: `sdk_token_create`
- **Feedback MCP** — shipped: `feedback_list/show/fix/delete` via
  the loopback `feedbackHttpMCP` helper
- **Sourcemaps MCP** — shipped: `sourcemaps_list/delete/resolve`
  against `GlobalSourceMapStore`
- **MCP drift test** — shipped at
  `mobile-headless/test/mcp-drift.test.ts` (3 assertions: every CLI
  verb has MCP coverage or opt-out; no orphaned MCP tools; `ops` +
  `ops_verbs` present)

### Tests green

- Go unit tests (ops + workspace + managed) pass
- Convex `codegen` clean — schema deployed to prod
  (`perceptive-minnow-557`)
- `bun test` mobile-headless — 17 pass / 4 skip / 0 fail
- `tsc --noEmit` mobile — clean
- Relay `go build ./...` — clean

### Still open (for future sessions)

Strategic:
- **Managed-toggle implementers** — every subsystem (DNS, analytics,
  storage, email, CI, voice, LLM) needs to read the flag from
  `userSettings.managed.<sub>` and pick Yaver-hosted vs
  user-provided credentials accordingly. The plumbing is done; the
  per-subsystem PRs are not.
- **Workspace build/test/deploy fan-out** — `yaver workspace build
  <app>` should dispatch to the ops verb with the right `workDir`
  automatically.
- **Web UI for `managed` + richer `primary device` controls** —
  today the web dashboard has the primary toggle; the per-subsystem
  managed toggle needs its own Settings section.

Cosmetic / small:
- Upload path for sourcemaps MCP (the binary payload doesn't fit the
  MCP JSON surface; CLI still owns the upload path).
- Per-user alias table for ops — today only `primary` + `local`
  resolve; `gpu`, `mac-mini`, team labels are TODO.
- Subsystem managed toggle surfaces in mobile Settings (only web +
  CLI + MCP today).

Bigger:
- **TestFlight + Play as first-class MCP schemas** — today they're
  scripted via `mobile_project_build`.
- **Sourcemap symbolication inside crash feedback** — the store
  exists; wiring the Errors dashboard to call `sourcemaps_resolve`
  on every frame is pending.
- **Relay-side presence for LAN discovery** — when a device connects
  via LAN beacon (no relay tunnel), Convex still shows `isOnline`
  from heartbeat. A parallel LAN-presence signal would close that
  gap but needs a new discovery channel.

---

## TL;DR

Yaver is for monorepo app developers who want **one tool** for
everything they touch between clone and production. Every capability
the primary tool ships is also an MCP tool with the same semantics,
so the developer can vibe from any AI coding tool they prefer and
have Yaver execute the real work: pushing Hermes bytecode to a phone,
uploading to TestFlight, migrating a backend, provisioning a GPU box,
rotating a token, deploying to CF Workers. One throat to choke,
either the human's or Yaver's — same engines, same state, same
outcomes.
