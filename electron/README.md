# Yaver GUI (Electron)

A hardened desktop shell around the **Yaver web dashboard** — chat, vibing,
devices, projects, and previews in a native window with tray, notifications,
and deep links. Built from the findings in
[`docs/audits/webui-chat-vibing-gui-2026-08-12.md`](../docs/audits/webui-chat-vibing-gui-2026-08-12.md).

**The desktop app IS a Yaver node, not just a client shell.** It embeds and
supervises the real Go agent (`src/agent-manager.js` → `yaver serve`), so the
machine it runs on is both:
- a **remote box** — vibed from any other surface (phone, TV, watch, web,
  another desktop) over the same device routing, and
- a **client surface** — the window vibes the local agent or any other owned
  device through the dashboard.

**The GUI is sign-in → app only.** It never shows the marketing web app —
no docs, blog, pricing, download, or any other web surface. Only the auth
flow (`/auth`, `/api/*`, OAuth providers) and the app itself (`/dashboard`,
`/d/<deviceId>/` agent proxy) may render in the window; everything else is
bounced to the auth gate by `src/navigation-policy.js` (unit-tested).

The in-app auth journey supports account creation as well as sign-in: passkeys,
Google, Microsoft, Apple, GitHub, and GitLab OAuth, plus email/password when the
backend enables that capability. Yaver account auth does not silently grant
runner, Git-provider, cloud-provider, or Office access; those remain separate
user-authorized connections.

The web app agrees: on the auth flow and app surfaces the marketing header
nav (Pricing / FAQ / Docs / Developers / Download / Blog) and footer are not
rendered at all (`web/lib/app-surface.ts` + `web/components/Header.tsx`,
`Footer.tsx`) — the login page is chrome-free, exactly like the dashboard's
existing `dashboard-mode`. A project / MCP set picked in the chat composer is
shared with mobile through the same Convex rows
(`defaultRuntimeProjectByDevice` / `mcpServersByDevice`), so the GUI's tasks
respect the latest project + MCP choices on every surface.

## Run

```bash
cd electron
npm install
npm test           # lifecycle, connectivity repair, auth transport, policy, navigation
npm start          # production dashboard (https://yaver.io/dashboard)
npm run dev        # localhost:3000 when a web dev server answers, else production
```

Env overrides:

| Env | Effect |
|---|---|
| `YAVER_DASHBOARD_URL` | Load this URL instead (https or http). |
| `YAVER_DEV=1` | Prefer `http://localhost:3000` (probed, 1.2s timeout) over production. |
| `YAVER_AGENT_BINARY` | Exact absolute executable to supervise in development/automation. |
| `YAVER_ELECTRON_AUTOMATION=1` | Unpackaged tests only: isolated mock keychain; ignored by packaged builds. |
| `YAVER_ELECTRON_USER_DATA_DIR` | Absolute isolated profile path used with automation mode. |

## Embedded agent (adopt-or-spawn)

On app start the GUI probes `127.0.0.1:18080/health`:

1. **Healthy → adopt** the running agent (launchd/systemd/manual `yaver serve`
   already live on the box). Matching the agent's own reuse semantics
   (`main.go:2506-2516`), never duplicates a process.
2. **Not healthy → spawn** `yaver serve --debug` as a supervised foreground
   child: health-wait, restart-on-crash with backoff, stop-on-quit.

A bootstrap `/health` response is shown as **pair this PC**, never as a green
ready state. Closing the window keeps the node and task streams alive in the
tray; explicit Quit stops only a child the GUI spawned and leaves an
independently running adopted agent alone.

Binary resolution: explicit `YAVER_AGENT_BINARY` in development/automation →
bundled `<app>/Resources/bin/yaver` (electron-builder
`extraResources`) → `~/.yaver/bin/current/<platform>/yaver` (CLI cache) →
`PATH`. The bundled binary is fetched at release time by
`scripts/fetch-agent-binary.mjs` (version from `versions.json` → `cli`), so
the packaged app carries the agent with **no network dependency at boot**.
The fetcher requires the matching SHA-256 from the release `checksums.txt`.
Windows release workflows additionally require valid timestamped Authenticode
on the raw agent, installed GUI, embedded agent, and outer installer.

**Self-contained, no keychain prompts:** the Go agent **embeds hermesc**
(`hermesc_embedded.go`, mac-arm64/x64 + linux-x64) so Hermes-bundle reload
tooling ships inside the app. Runner CLIs (Claude Code / Codex / OpenCode)
install through the agent's `/install/` route (`ensureRunnerInstalledStream`).
A global `yaver-cli` install bootstraps all three official npm packages on
macOS, Linux, WSL, and Windows; a standalone direct GUI exposes the same
deterministic Doctor install actions. Runner OAuth/API credentials are never
bundled. Mac App Store/TestFlight is a client-only sandbox and runs runners on
a connected Yaver node instead of executing downloaded CLIs inside the app.
Doctor/diagnose is served at `/diagnose`, `/agent/doctor`, `/net/doctor`,
`/mobile/hermes/doctor` — rendered by the dashboard's HealthView tab (tray →
"Diagnose"). The spawned agent runs with `YAVER_VAULT_SKIP_KEYCHAIN=1`, which
the agent honors as a global keychain gate (`vault_keychain.go::keychainAccessDisabled`)
for the vault mirror AND the runner-auth probe — so the desktop app never
triggers a macOS "security wants to use your confidential information" prompt.
Chromium secure storage is a separate layer: production uses macOS Keychain
under Yaver's stable signed identity, so the OS may ask once on first access.
Unpackaged browser automation uses an isolated mock keychain so the generic
Electron development identity cannot create an unanswerable repeated prompt;
the switch is structurally disabled when `app.isPackaged` is true.

The window deliberately keeps the native OS frame (`frame`, shadow, rounded
corners and macOS title bar) instead of drawing a square frameless web shell.
On macOS the Dock icon is also set from the canonical Yaver artwork during
unpackaged development; packaged builds use `assets/icon.icns`. The canonical
artwork has a transparent squircle silhouette, and the icon build regenerates
the macOS ICNS and Windows ICO from that same source.

The Electron bridge exposes a non-secret `localDeviceId`. Only the device row
with that exact ID is labelled **This PC · Desktop GUI**; ordinary browsers
remain **Web UI** and never guess from a hostname. Runner/render choices persist
the real device ID, so “This PC” is a label, not a magic routing target.

## Connectivity and remote access doctor

Tray → **Diagnose (doctor)** opens a desktop-native **Connectivity & Remote
Access** section before the development toolchain checks. It remains available
when the local agent is down and operation-probes localhost health, current
LAN/Tailscale candidates, Tailscale daemon state, and OS firewall posture. The
agent-backed half reads back its owner-scoped Convex device row, probes relays,
and checks the real Yaver screen-capture policy/engine.

Named findings expose fixed-ID repairs only; the renderer never supplies a
command or executable path. Windows firewall repair requests UAC and installs
program-scoped inbound rules for TCP 18080/18443 and UDP 4433, limited to
Private/Domain profiles and `LocalSubnet,100.64.0.0/10`. It never disables the
firewall, enables the Public profile, or opens RDP port 3389. Microsoft RDP is
a separate explicit Windows setting. For a Windows device with a published
Tailscale address, **Open RDP** first operation-probes TCP 3389 and only then
launches the OS RDP client; an IP row alone is never called usable.

**Enable local view** grants the first Yaver Remote Desktop consent through an
authenticated loopback request. This is intentionally unavailable remotely.
macOS Screen Recording/Accessibility and Windows RDP consent stay in their OS
settings. **Fix with AI** starts an OpenCode task through the connected agent
using the account/machine's configured provider and model; it is secondary to
deterministic fixes and its prompt forbids changing firewall exposure, RDP,
Tailscale ownership/ACLs, or capture consent without asking the local owner.

## Diagnostic logs

The shell writes structured, credential-redacted diagnostics to its per-user
data directory. Writes are batched once per second, the memory queue is capped
at 256 KiB, and files rotate at 2 MiB with at most three files. Agent state,
renderer/load failures, updater errors and native process failures are covered.
Tray → **Open diagnostic logs…** reveals the exact file without Terminal.

## Updates

Signed direct builds update from the architecture-specific metadata attached
to the protected `gui/v*` GitHub release:

- macOS Developer ID ZIP/DMG and Windows NSIS download in the background and
  install on quit;
- Linux AppImage uses the same in-place updater; deb/rpm installs remain owned
  by the Linux package manager and the GUI says so explicitly;
- Mac App Store/TestFlight never loads the direct updater. Apple manages that
  build's updates through App Store/TestFlight.

Automatic updates default on and are reversible from the tray or the bounded
`window.yaver.setAutomaticUpdates()` bridge. “Check for updates” remains an
explicit one-shot action when background updates are off. The updater requests
`latest-<arch>{-mac|-linux}.yml`, so x64 and arm64 jobs cannot overwrite each
other's release metadata.

## macOS TestFlight / Mac App Store

Yaver has two honest macOS distributions:

- **Developer ID DMG** — full local node: embedded Go agent, runners,
  repositories, rendering/capture and cross-device remote access.
- **Mac App Store / TestFlight** — App Sandbox client: account/OAuth, tasks,
  devices and remote previews, connecting to a Yaver node elsewhere. It does
  not bundle or start the Go agent because arbitrary repo/process/capture
  access is incompatible with a least-privilege Store sandbox.

On Apple-silicon macOS 26+, the Store build starts V8 in jitless mode. This is
a narrowly scoped runtime workaround for an Electron MAS renderer crash: the
signed main process stays alive while every renderer exits with code 5 before
first paint. The installed 0.1.10 TestFlight binary was operation-probed on
2026-09-05 both ways; adding `--js-flags=--jitless` kept the renderer alive and
loaded the dashboard. Direct/notarized builds, Intel Macs, and earlier macOS
releases retain normal JIT behavior.

Build locally with `./deploy/deploy.sh desktop-mas`. After a separate explicit
release approval, `./deploy/deploy.sh desktop-testflight` builds a universal
MAS package, verifies signature/entitlements and absence of the agent, validates
with App Store Connect, then uploads it to the macOS TestFlight train. Required
credentials and profiles are listed by
`scripts/deploy-macos-testflight.sh --help`; private material stays outside the
repository. The Store build uses `io.yaver.mobile`, matching the existing
Yaver IO app record: Apple requires all platforms in one universal-purchase
record to share a bundle ID. Add the macOS platform to that record once in App
Store Connect before the first desktop upload; later builds use the scripted
deploy path. The Developer ID DMG remains the separately signed
`io.yaver.gui` desktop lane.

## What it fixes in the shell (not the web app)

The web dashboard passes the bearer token and relay password to agent SSE streams as
query params (`?token=` / `?__rp=`) because `EventSource` cannot set headers
(`web/lib/agent-client.ts:6135-6164`). On a bare browser that leaks the token
into agent access logs and browser history. The GUI intercepts every request in
the main process:

1. strips agent-marked `?token=` / `?__rp=` from the outgoing URL without
   consuming application/OAuth/reset parameters that also happen to be named
   `token`,
2. captures the material per-origin (process-lifetime, never written to disk),
3. re-injects it as `Authorization: Bearer <token>` / `X-Relay-Password`
   headers — both already in the agent's CORS allowlist
   (`desktop/agent/httpserver.go:3231`) and used by the mobile app today.

Result: GUI users get header-auth SSE with no token-in-URL exposure, matching
the mobile app's transport.

## Security posture

- `contextIsolation: true`, `sandbox: true`, `nodeIntegration: false`,
  `webSecurity: true` — the preload exposes a single frozen `window.yaver`
  object with bounded task/agent/status/settings methods.
- Camera, microphone, location, notifications, and device permissions requested
  by remote content are denied by the Electron session. WebAuthn/passkeys and
  receiving an authorized WebRTC stream do not require those grants.
- Navigation allowlist: only `yaver.io` / `relay.yaver.io` / `cloud.yaver.io` /
  `localhost:3000` may navigate the window; everything else opens in the
  system browser (`setWindowOpenHandler` + `will-navigate`).
- In-page SPA navigations are bounced through
  `navigation-policy.js::inPageNavigationDecision` (unit-tested) so a soft
  link to `/docs` or `/pricing` cannot render the marketing site in-window.
- No secrets persisted by the shell; auth lives in the dashboard's own
  `localStorage` / cookie as in a normal browser.

## Native value-adds

- **Shared task lifecycle**: the dashboard lists ongoing/review/completed/
  failed/stopped agent tasks, hydrates historical turns, resumes the raw console,
  and exposes Stop / Complete / confirmed Delete. Structured terminal events
  drive native notifications; DOM observation remains an older-dashboard
  fallback.
- **Tray**: show/hide, agent status line, Diagnose (doctor) → health tab,
  tasks, reload, notifications, automatic-update opt-out, **Keep this PC
  available**, start-at-login, and quit. The window hides instead of closing, so tasks keep streaming. The
  availability blocker is process-scoped and never edits an OS power plan or
  asks for administrator access.
- **Deep links**: `yaver://dashboard?tab=chat|runtime|devices|projects|health`
  (macOS `open-url` + single-instance argv on Windows/Linux). The dashboard
  already syncs `?tab=` on navigation (`page.tsx:1218-1221`).

## Build / package

```bash
npm run dist:mac    # dmg + zip (electron-builder)
npm run dist:win    # nsis
npm run dist:linux  # AppImage
```

Per-platform icons are generated once (`scripts/build-icons.sh`): `icon.icns`
(mac), `icon.ico` (win, multi-size), `icon.png` (linux) from the canonical
`web/public/icon-512.png`. CI (`.github/workflows/release-gui.yml`, tag
`gui/v*`) fetches the agent binary, runs `npm test`, builds macOS and Linux,
and cuts a GitHub release whose asset names match the
[yaver.io/download](https://yaver.io/download) landing page
(`yaver-gui-<version>-mac-<arm64|x64>.dmg` /
`-win-x64-setup.exe` / `-linux-<arm64|x64>.AppImage`).

The direct-download Windows candidate uses the local release Mac's non-exportable
SimplySign identity through `./deploy/deploy.sh desktop-windows`. The build
certificate-pins the embedded agent and outer installer; a clean Windows VM
then verifies every installed PE and performs silent install/uninstall. Hosted
CI never substitutes unsigned bytes. Do not describe a new Windows build as
shipped until the same candidate passes that preflight and Windows App
Certification Kit.

The Microsoft Store AppX/MSIX lane is separate and is documented in
`store/README.md`. Microsoft signs accepted Store packages; do not describe the
SimplySign-signed NSIS installer as the Store candidate.

## Known limitations

The GUI renders the dashboard as-is, so it inherits the web-only findings the
audit recorded — the GUI's job is to *surface* them, not hide them:

- A clean packaged Windows journey has not yet operation-proven account
  creation → local-agent owner claim → authenticated `/info`. The shared
  dashboard can reclaim a bootstrap device, but the first-run native status
  panel and clean-VM pixel test remain release gates.
- The GUI system-awake blocker is implemented; AC/battery policy and a
  display-awake assertion scoped only to an authorized live-view session are
  not yet unified with the Go agent's cross-platform inhibitor.
- The 900 ms `onLoad` overlay timer and the green-over-blank "preview live"
  pill are web-app rendering choices; the GUI keeps
  `StreamHealthNotice`/`RawFailureBanner` visible and adds no status chrome of
  its own (LESS IS MORE).
