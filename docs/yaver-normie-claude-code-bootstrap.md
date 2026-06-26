# Yaver bootstrap for the "normie + Claude Code" workflow

**Status:** analysis + first build, 2026-06-26. Source-verified against the Go
agent (not docs). The Android HTTPS-serving cell described in §5 is BUILT in this
change (`desktop/agent/apk_serve.go`, tests green); everything else is a status
map of what already exists.

> Per CLAUDE.md: code is the source of truth. Every claim below was grepped in
> `desktop/agent/` on 2026-06-26. Re-verify before acting — other threads bump
> these files in parallel.

## The target flow

A non-technical user opens **Claude Code in a terminal**, talks to **Yaver over
MCP**, and:

1. builds a code monorepo,
2. uses **Yaver managed cloud** to compile the Hermes bundle and push it to their
   phone (no local build toolchain),
3. ships to **TestFlight + Google Play internal test**,
4. serves the Android app over **HTTPS for real installs** (talos-style), and
5. (nice-to-have) triggers commands into already-running sessions.

Verdict: pillars 1–3 are fully wired today; pillar 4 was the real gap (now
built); pillar 5 works locally only.

---

## Pillar 1 — Bootstrap + MCP wiring → WIRED ✅

- **Transport: stdio.** Claude Code spawns `yaver mcp`; JSON-RPC over
  stdin/stdout (`main.go:10726` `runMCPStdio`).
- **Auto-registration.** `yaver auth` (`main.go:1120`) and `yaver serve`
  (`main.go:2453`) both call `autoSetupMCP()` → `ensureClaudeCodeMCPConfig()`
  (`mcp-setup.go:107`) which runs:
  `claude mcp add --scope user yaver -- <path> mcp`. The user never hand-edits
  `.mcp.json`. Codex + opencode get the same treatment.
- **Normie onboarding verb.** `yaver_lazy_setup` (`mcp_lazy_setup.go:89`):
  idempotent; if signed in returns instantly, else starts device-code OAuth,
  optionally blocks ≤180s, returns sign-in URL + user code + a `next_action`
  string meant to be read by another agent.
- Supporting (all real, not stubs): `yaver_doctor` (`httpserver.go:15980`),
  `yaver_self_host_onboarding` / `yaver_managed_cloud_onboarding`
  (`mcp_onboarding_flows.go`), `init_project` (`init_project.go:41`),
  `project_wizard_*`.

**Minimal sequence:** `npm install -g yaver-cli` → `yaver auth` (auto-registers
MCP + starts serve) → open Claude Code → `init_project` / `project_wizard_start`.

## Pillar 2 — Monorepo dev via MCP → WIRED ✅

- `yaver.workspace.yaml` manifest at repo root (`workspace.go:48-128`): N apps
  with `path`, `stack` (nextjs/react-native-expo/flutter/go/convex), per-app
  `scripts`, `depends` (topological), `env` (vault), runtime placement.
- `workspace_scaffold` auto-detects on disk (`monorepo_detect.go:76`),
  `workspace_init` wires every app in one call.
- **Division of labor:** Claude Code is the editor (Read/Edit/Bash); Yaver is the
  runtime/orchestration plane — dev-server lifecycle (`code_dev`,
  `devserver.go`), remote attach over QUIC mesh (`code_attach`,
  `code_control_plane.go:88`, candidate scoring `agent_mesh_remote.go:120`),
  Hermes bundling + Git-SHA build cache, provider/placement planning
  (`project_runtime`).

## Pillar 3 — Managed cloud + Hermes → WIRED ✅ (no stubs)

- **Real Hetzner.** `yaver_managed_cloud_onboarding` (`mcp_onboarding_flows.go:81`)
  with `confirm_checkout=true` → LemonSqueezy checkout (`ops_cloud.go:376`) →
  webhook → `provisionHetzner` (`cloud_provisioners.go:57`) → cloud-init boots
  the same agent binary. Cost guardrails (metered / scale-to-zero) enforced.
- **Hermes pipeline.** `mobile_project_build` → `buildNativeBundleForProject`
  (`devserver_http.go:1039`) → `expo export:embed` / `react-native bundle` →
  `hermesc -emit-binary` (`devserver_http.go:3295`), HBC cache keyed on Git SHA
  (`:3264`). Validation: magic `0x1F1903C1`, BC version 96
  (`hermes_runtime.go:47`).
- **Phone delivery.** `mobile_hermes_reload` → `/dev/reload` broadcasts a
  `BlackBoxCommand` over the QUIC relay (4433) via the `/blackbox/stream` SSE
  channel (`blackbox.go:309`), optionally targeted to one device.

A normie with no build toolchain can: write RN code → managed cloud compiles
Hermes → phone hot-loads it. Works today.

## Pillar 4 — TestFlight + Play internal → ASYMMETRIC ⚠️

- **TestFlight is Mac-only by physics.** `publish.go:478` gates
  `if target.Kind == "testflight" { return runtime.GOOS == "darwin" }`. Archiving
  needs `xcodebuild`; the registered-iPhone-UDID keychain isn't on Linux. The ASC
  client (`appstoreconnect.go`) does tester/group/build **management** but NOT
  build upload (shelled to `deploy-testflight.sh`). **A normie with no Mac cannot
  ship to TestFlight via managed cloud** — needs their own Mac, or a Mac-farm
  node (`yaver publish ios --machine <deviceId>`). This is the second real gap;
  the honest fix is a managed Mac-farm queue, not server-side iOS.
- **Play internal is server-capable.** `deploy-playstore.sh` (Gradle, any JVM) +
  `upload-playstore.py` (chunked resumable `edits().bundles().upload()`). Play
  client (`playpublish_api.go`) does edit sessions, track reads, Google-Group
  tester binding, and `PromoteRelease(status, userFraction)` for staged/full
  rollout.
- **Tester model.** Apple = per-email beta testers; Google = track-bound Google
  Groups only (per-email is Console-only).
- **Multi-tenant creds.** `resolveAppleASCCreds(project)` /
  `resolveGoogleSA(project)` read from the project's vault scope with global
  fallback (`store_push_live.go`) — dev-B's app managed with dev-B's keys.
- **Blockers** (`publish_status.go:45`): missing iOS usage strings, listing
  identity, screenshots, and FGS permission-justification video are hard blocks.
  The "Console FGS declaration" is a Play Console concern, not an API gate.

## Pillar 5 — Android HTTPS serving (talos-style) → BUILT in this change ✅

This was the missing pillar. talos had it
(`talos/cli/cmd/apkserve.go` + `apk_server/publish_apk.sh` +
`web/.../api/assetlinks/route.ts`); yaver did not.

**What yaver already had:** Caddy domain + auto Let's Encrypt
(`domains.go:69` `AddDomain` → `RegenerateCaddyfile`), 18443 TLS, `expose_start`,
`site_serve`, a static `web/public/.well-known/assetlinks.json` for
`io.yaver.mobile` only.

**What was missing:** APK hosting, install page, publish pipeline, and a
**dynamic** assetlinks keyed on the *user's* package + signing SHA.

**The new cell — `desktop/agent/apk_serve.go`** (MCP verbs registered in
`mcp_workspace.go` + dispatched in `mcp_workspace_handlers.go`):

| Verb | Does |
|---|---|
| `android_apk_serve` | Stage the APK + start an in-process LAN server; returns `http://<lanIP>:<port>/` to open on any phone on the same Wi-Fi. |
| `android_apk_publish` | Stage + serve, then register a Caddy HTTPS domain (auto Let's Encrypt) reverse-proxying the server. Writes `latest.apk`, `version.json`, and a live `/.well-known/assetlinks.json`. |
| `android_apk_status` | Running state, port, published apps, install url. |
| `android_apk_stop` | Stop the server. |

**Design choices (yaver-native, not a verbatim talos port):**
- **No hardcoded infra.** talos SCP's to a fixed Hetzner IP — forbidden in this
  public repo. yaver reuses the existing Caddy plane: publish copies the APK into
  `~/.yaver/apk/<app>-<code>.apk` and calls
  `AddDomain(domain, "localhost:<port>", "", dnsMode)`. DNS A-record → box;
  Caddy fetches TLS on first hit.
- **One in-process server owns every route** (apk bytes, install page,
  version.json, assetlinks). Caddy's `file_server` hides dotfiles by default, so
  serving `/.well-known/assetlinks.json` ourselves (Caddy reverse-proxies, not
  static) keeps full control of dotfile paths + content-types.
- **Dynamic, aggregated assetlinks.** Every published app with a `package` + at
  least one SHA-256 fingerprint becomes one `android_app` statement. SHA falls
  back to vault `ANDROID_RELEASE_SHA256` when not passed (mirrors CLAUDE.md's
  Play-signing-SHA guidance). Without package+SHA, assetlinks is `[]` and the
  response carries an `assetlinksWarning` (App Links / passkeys won't bind).
- **Multi-app.** A registry keyed by app slug; `/latest.apk` aliases the most
  recent publish; assetlinks lists all packages.

**Caveat:** the APK must be a *universal* APK (build from the AAB with
`bundletool` if needed). This is a real-install path that bypasses the Play
Store; the user is responsible for what they distribute and the right to do so.

**Tests:** `apk_serve_test.go` — real HTTP server on an ephemeral port (yaver
convention, no mocks): download by slug + `latest.apk` alias, `version.json`,
assetlinks reflecting package+SHA, empty assetlinks without them, SHA parsing.

### Architecture (daemon residency — important)

The `yaver mcp` stdio process and one-shot CLI are ephemeral; the install
server must outlive them. `dispatchOps` runs local verbs **in-process**, so a
naive ops handler would start the server in the dying stdio process. Resolved
by:
- Core methods (`apkServeCore`/`apkPublishCore`/`apkStatusCore`/`apkStopCore`)
  operate on the package-global `apkSrv`.
- Dedicated **daemon HTTP routes** `/android/apk/{serve,publish,status,stop}`
  (httpserver.go, behind `s.auth`) own the persistent server.
- **ops verbs** `android_apk_*` run the core directly when `isDaemonProcess`
  (set in `runServe`), else proxy over loopback to the daemon via
  `localAgentRequest`. So the server always lives in the always-on daemon, no
  matter the front-door.

### Surfaces (all backed by the same ops verbs)

- **Claude Code / MCP**: `ops` grand-tool → `android_apk_*`.
- **CLI**: `yaver android serve|publish` and `yaver android apk status|stop`
  (`apk_serve_cmd.go`, wired into `runAndroid` in `android_cmd.go`).
- **Web**: StoresView "Install (APK)" tab (`callOps("android_apk_*")`).
- **Mobile**: `storeTestersClient.apk{Serve,Publish,Status,Stop}`.

### Verified with the real app

Built a signed **universal APK** from the release AAB with `bundletool`
(`yaver-268.apk`, v1.18.141 / code 268, 247 MB, signer SHA-256
`A4:45:1B:…:CD:D2`) and served it through the cell end-to-end: install page,
`application/vnd.android.package-archive` bytes (236 MB), `version.json`, and a
correct aggregated `/.well-known/assetlinks.json` for `io.yaver.mobile` + the
real signing SHA. version.json no longer leaks the absolute apk path (home-dir
username) — `Path` is `json:"-"`, basename exposed as `file`.

**Why no public HTTPS URL was stood up:** (1) the local daemon is currently in
`mode: bootstrap` (auth token needs refresh) so it mounts no full routes —
`yaver auth` re-run required before live local serving; (2) public HTTPS "like
talos" (talos serves `https://mcp.talos.works/apk/` from its own always-on
Hetzner box) needs a public box + a `yaver.io` DNS A-record, which is a
decision/credential for the owner and must respect the never-leave-Hetzner-
running rule. One command once those exist:
`yaver android publish --apk yaver-268.apk --package io.yaver.mobile --version-name 1.18.141 --version-code 268 --sha256 A4:45:… --domain apps.yaver.io`.

**Still TODO:** auto-derive the signing SHA from `keystore.properties`/Play
console (today: arg or vault `ANDROID_RELEASE_SHA256`); build the universal APK
in-pipeline from an AAB (today: `bundletool` by hand); a scale-to-zero public
serving box wired to a `yaver.io` subdomain.

## Pillar 6 — Trigger commands into existing sessions → LOCAL only ⚠️

- **`tmux_send_input`** (`tmux.go:292`) — the literal answer: `tmux send-keys`
  injects keystrokes into a running tmux pane. If a Claude Code session runs in a
  local tmux pane, Yaver MCP can type into it. **Local only.**
- **`continue_task`** (`tasks.go:3487`) — queues a follow-up prompt into a
  running *local* Yaver task (`PendingFollowUps`). No `device_id`.
- **`device_broadcast_command`** (`mcp_device_broadcast.go:47`) — pushes
  `reload`/`open_app` to paired *mobile SDK* devices over BlackBox SSE. Phones
  only.
- **Now implemented (this change):** both `continue_task` and `tmux_send_input`
  accept an optional `device_id`. When set they proxy to the target device's
  daemon via `proxyToDevice` (`/tasks/{id}/continue`, `/tmux/input`); an
  empty/own deviceId returns `errProxyLocal` and falls through to the local
  path. So a Claude Code session can now inject a follow-up into a task — or
  type into a live tmux pane (e.g. another Claude Code session) — on a *remote*
  device. Schemas updated in `mcp_tools.go`.

---

## Mac-farm TestFlight (pillar 4 follow-up — built this change)

The queue spine already routed `testflight` correctly (it's a *ship* target →
`/deploy/ship` on the claiming node, not a platform target). The missing piece
was a **capability preflight**: `yaver publish ios --queue --machine <id>` (and
the sync `--machine` form) now call `preflightTestFlightMachine` — if the chosen
device isn't a TestFlight-capable Mac it refuses *before* enqueuing and lists
the capable Macs (`publish_ship.go`). So a no-Mac normie queues the archive to a
registered Mac node and walks away; a wrong (Linux) target fails fast with an
actionable message instead of 20 minutes later. Re-auth of the local box is the
only other prerequisite.

---

## Scoreboard

| Pillar | Status |
|---|---|
| 1. Bootstrap + MCP auto-wiring | ✅ done, normie-ready |
| 2. Monorepo dev via MCP (+ remote attach) | ✅ done |
| 3. Managed cloud → Hermes → phone | ✅ done, no stubs |
| 4. Play internal | ✅ server-capable |
| 4. TestFlight (Mac-farm queue) | ✅ queue routes + capability preflight (needs a registered Mac) |
| 5. Android HTTPS serving | ✅ built — cell + CLI + web/mobile UI; real APK verified |
| 6. Trigger existing session (local + remote) | ✅ `device_id` on continue_task + tmux_send_input |

**Remaining (infra/owner-decision, not code):** a scale-to-zero public box +
`yaver.io` subdomain to host the APK over HTTPS like talos; re-auth the local
box; register a Mac as a TestFlight build node.
