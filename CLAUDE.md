# Yaver.io — Claude Code Project Guide

## Read This First

**Markdown files drift. Code is the source of truth.** This file, `AI_ARCH.md`,
`REMOTE_WORKER.md`, every other `*.md` — accurate when written, stale by the
next handler rename. Before acting on any `.md` claim:

1. **grep the actual code.** Routes referenced in docs may exist as functions
   without ever being wired to the mux (we shipped `yaver diagnose` like that
   in 1.99.33).
2. **Verify versions.** `yaver --version` (binary on PATH) vs `/info.version`
   (running daemon) vs `git log -- <file>`. If they disagree, the doc matches
   none of them.
3. **Re-read the file on disk, not in the doc.** Other threads bump constants
   in parallel.
4. **When the doc and the code disagree, the doc is the bug.** Fix it in the
   same change.

`AI_ARCH.md` is the runtime-architecture reference for auth, bootstrap, relay,
mobile discovery, and remote-recovery — read it before changing those.

## Hard rules

- **Never push or commit without explicit user permission.**
- **Destructive paths**: before `rm -rf`, `git clean -fdx`, `find -delete`,
  `mv` over an existing dir:
  1. `ls -la <path>` first; show what's about to be deleted.
  2. Absolute paths with exact case. macOS is case-insensitive — `~/workspace`
     matches `~/Workspace`. This already wiped a repo once.
  3. Confirm before deleting under `$HOME`, a git repo root, any path computed
     from a variable, or any path transcribed from a user message.
  4. Treat `rm -rf` on anything you didn't create this session like
     `git push --force` to main.
- **Cloudflare deploy size guard**: `web/` must stay under 10 MB. If you add
  video, compress first (`ffmpeg -crf 32 -vf scale=720 -an`) or host external.
- **Never WebView for third-party RN apps.** Use the Hermes-bundle native load
  path (`/dev/build-native` → ExpoReactNativeFactory). WebView is OK for plain
  web content (landing pages, docs).
- **Never commit credentials, infra IPs, or hostnames.** The repo is public on
  GitHub. Apple keys, Hetzner IPs, npm tokens, Play service-account JSON,
  relay passwords, Tailscale IPs — all gitignored / GH secrets only. If a
  secret was ever committed: rotate it AND `git filter-repo --replace-text`
  before pushing.
- **Public docs use machine aliases**, not real infra labels. Prefer
  `primary`, `selected-machine`, `your-box`, `example-host` in examples.
- **Hetzner test box (`yaver-test-ephemeral`)** is disposable. Its IP, SSH
  key material, `hcloud` token, real device IDs never go in tracked files.
  Refer to it in code/docs only as `yaver-test-ephemeral`.

## Distribution — npm only

As of 1.99.124, **`npm install -g yaver-cli`** is the **only** supported install
path on every platform: macOS (Apple Silicon + Intel), Linux (x64 + arm64,
including Raspberry Pi / ARM cloud), and Windows via WSL2.

The npm package detects the platform and downloads the matching, signed +
notarized agent binary into `~/.yaver/bin/<version>/<platform>/yaver`. macOS
binaries are Developer ID + hardened runtime + notarized — Gatekeeper passes
on first run.

The previous distribution paths — apt repo, Homebrew tap, Scoop bucket, AUR,
Winget, Chocolatey, raw tarballs, install.sh, Docker image — are **all
removed**. Their repos (`kivanccakmak/{homebrew,scoop,aur,apt}-yaver`) are
archived read-only with a deprecation README.

`release-cli.yml` only does: validate → publish-npm → publish-mcp-registry →
build (with darwin sign+notarize) → release. Don't add deb/rpm/dmg/brew/scoop
steps back without an explicit ADR.

```bash
# install (any supported platform)
npm install -g yaver-cli

# update
npm install -g yaver-cli@latest

# headless (Pi / VPS / SSH-only) — short code + URL, sign in from any browser
yaver auth --headless
```

## Repository

- **Source of truth**: `github.com/kivanccakmak/yaver.io` (open source). The
  legacy GitLab mirror may still be wired as `origin`; use the `github` remote
  or `git push github main` directly.
- **Tags trigger releases**: `cli/v*` → release-cli.yml, `mobile/v*` →
  release-mobile.yml, `web/v*` → release-web.yml. Tag protection rules limit
  pushes to the repo owner.
- **Cloudflare web deploy**: `./scripts/deploy-web.sh` (size-guarded at 10 MB,
  uses `@opennextjs/cloudflare` + `wrangler deploy`).
- **Convex backend deploy**: `cd backend && npx convex deploy --yes`. Not
  triggered by CI — deploy explicitly when schema or HTTP routes change.

## Secrets

Three places. Never anywhere else. Never in a tracked file or git history.

1. **GitHub Actions secrets** (CI). `gh secret list -R kivanccakmak/yaver.io`
   for the canonical list. Includes:
   `ANDROID_KEYSTORE`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`,
   `ANDROID_KEY_PASSWORD`, `APPLE_CERTIFICATE_P12`,
   `APPLE_CERTIFICATE_PASSWORD`, `APP_STORE_CONNECT_API_KEY`,
   `APP_STORE_CONNECT_KEY_ID`, `APP_STORE_CONNECT_ISSUER_ID`,
   `APPLE_TEAM_ID`, `PLAY_STORE_SERVICE_ACCOUNT_JSON`, `NPM_TOKEN`,
   `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`, `CONVEX_DEPLOY_KEY`,
   `RELAY_PASSWORD`, `HCLOUD_TOKEN`, `HETZNER_TEST_SERVER_*`, etc.
2. **Local gitignored files** (dev machine): `.env.test`,
   `mobile/android/keystore.properties`, `keys/*`. All in `.gitignore`. Never
   force-add.
3. **Runtime env vars** (ad-hoc scripts). Scripts exit 2 if required vars are
   missing — never fall back to a hardcoded default.

If you find yourself about to put a secret in a tracked file, stop. Add as
GitHub secret + read from env. If it ever reached your clipboard, rotate.

`yaver vault` is for project-scoped local secrets that the daemon needs at
runtime. `yaver vault add APP_STORE_KEY_ISSUER --project mobile --value <uuid>`
etc. — vault is encrypted with your auth-token-derived key, so it locks if
your token rotates.

## Privacy contract — what lives where

Yaver's promise: Convex stores identity, peer discovery, and session
bookkeeping only. Anything sensitive or work-derived stays on the user's
devices and flows P2P.

**Allowed in Convex**: `users`, `sessions` (token hashes only), `sdkTokens`
(hashes only), `devices`, `relayServers`, `platformConfig`,
`guestInvitations`, `guestAccess`, `teams`, `teamMembers`, `userProjects`
(slug + deviceId + flags + branch — **no absolute paths**), activity audit
summaries (action + target + outcome + timestamp).

**Forbidden in Convex** (enforced by `desktop/agent/convex_privacy_test.go`):
vault values, raw tokens / API key plaintext, task input prompts or stdout,
file contents, exec session output, absolute filesystem paths (they leak the
user's home-dir username), customer LAN IPs.

All Convex-bound calls go through `convexSyncer.callMutation`. The privacy
test enumerates forbidden keys (`path`, `workDir`, `token`, `stdout`,
`output`, `vaultValue`, `secret`, …) AND scans for path leaks (`/Users/`,
`/home/`, `/root/`, `C:\Users\`). New sync paths must add their fields to
`fieldsWeForbidInAnyConvexPayload` and a test for the payload.

## Three-part architecture

1. **Mobile app** (App Store / Play Store) — native container for testing
   third-party RN apps via Hermes push. AI agent control. HTTP server on
   port 8347 for `yaver-cli` push-to-device.
2. **`yaver-cli` (npm)** — third-party RN devs push their own projects to
   the phone via `yaver push`. No agent needed; talks directly to the phone's
   8347 server. Same npm package.
3. **Desktop agent (Go)** — same `yaver-cli` package. P2P, relay, MCP, hot
   reload (Expo / Flutter / Vite / Next.js), session transfer, builds, vault.
   Used to drive AI agents from your phone.

## Connection strategy

Direct-first, relay-fallback, per surface:

| Surface | Strategy |
|---|---|
| Mobile app | LAN beacon (UDP 19837) → Convex-known IP → relay. WiFi → direct first; cellular → relay only. |
| Desktop Electron | Local IPC (`localhost:18080`) → direct LAN → relay. |
| Web dashboard | Relay only (browser CORS blocks LAN). |
| Go CLI | Local daemon by default. `yaver connect` / `yaver code --attach <device>` resolve a remote and tunnel. |

Each surface stores its own session token independently. The same OAuth user
can be signed into all surfaces simultaneously. Sessions are 1-year, refreshed
on every heartbeat. Sign-out on one surface doesn't affect others.

## Daily commands

```bash
# auth + serve
yaver auth                           # OAuth (Apple / Google / GitHub / GitLab / Microsoft / email)
yaver auth --headless                # short-code flow for SSH-only boxes
yaver serve                          # start agent (auto-installs systemd unit on Linux)

# everyday
yaver status                         # local agent + auth + relay state
yaver primary status                 # remote primary device — agent version, lifecycle, runners
yaver primary auth                   # SSH into primary + run yaver auth --headless there
yaver primary set <deviceId|alias>   # pick a device as your primary
yaver primary ping                   # one-shot reachability + auth-as-same-user check
yaver ping <alias|deviceId|name>     # same, any device

# devices
yaver devices                        # list registered devices
yaver alias set <name> <deviceId>    # short name for ssh / connect / ping
yaver ssh <alias|primary>            # OpenSSH wrap, resolves via Tailscale → device row → ssh config

# code
yaver code                           # local TUI on this machine
yaver code --attach <device>         # remote machine via QUIC tunnel
yaver autodev <slug>                 # autonomous loop on remained.md
yaver insert <app>                   # tell the paired mobile to load <app> via Hermes push

# cable
yaver wire detect                    # USB-attached iPhone/iPad/Android
yaver wire push [path]               # framework-aware build + install (Expo/RN/Flutter/Native)

# vault
yaver vault add <name> --project <p> --value <v>
yaver vault env --project <p>        # source for deploy scripts
```

## Networking — short reference

| Layer | Port | Purpose |
|---|---|---|
| HTTP | 18080 | agent API (auth + tasks + dev server proxy) |
| QUIC | 4433 | relay tunnel out + direct phone connections |
| UDP beacon | 19837 | LAN auto-discovery (auth-aware via token-hash fingerprint) |
| HTTPS (LAN) | 18443 | self-signed TLS for SDK clients on LAN |
| Phone HTTP | 8347 | mobile-app inbound for `yaver push` from CLI |

Relay is application-layer QUIC, password-protected, self-hostable, no
TUN/TAP. Pass-through — never stores task data.

## Mobile app

### Hermes-push (default for RN/Expo)

`yaver-cli push` and the agent's `/dev/build-native` both produce a Hermes
bytecode bundle and load it into the Yaver mobile container via
`ExpoReactNativeFactory + RCTAppDependencyProvider` (TurboModules, Fabric,
JSI). Validation: HBC magic `0x1F1903C1` at offset 4, BC version (96) at
offset 8.

**Suppress-when-inside-Yaver** (RN SDK 0.5.5+): when a third-party RN app
loads inside the Yaver container, `YaverFeedback.init()` and
`ShakeDetector.start()` silently no-op via the `YaverInfo` native module.
Yaver's container owns shake/feedback ("Reload" + "Back to Yaver" overlay).
Standalone TestFlight/Play builds are unaffected.

### `sdk-manifest.json` contract

Source of truth: `mobile/sdk-manifest.json`. Must be in sync with
`mobile/android/app/src/main/assets/sdk-manifest.json`, the iOS bundle copy,
and `cli/sdk-manifest.json`. Update all four when bumping `mobile/package.json`.

### iOS — TestFlight (LOCAL ONLY)

CI is intentionally disabled (`if: false` in `release-mobile.yml`) because GH
runner keychains don't carry your registered iPhone UDIDs. Always run from
this Mac.

```bash
# vault path (preferred when auth is fresh)
$(yaver vault env --project mobile)
./scripts/deploy-testflight.sh

# explicit env path (when vault is locked)
export APP_STORE_KEY_PATH="$HOME/.appstoreconnect/private_keys/AuthKey_77Z6B543D5.p8"
export APP_STORE_KEY_ID="77Z6B543D5"
export APP_STORE_KEY_ISSUER="<uuid from yaver vault env --project mobile>"
export APPLE_TEAM_ID="5SJZ4KA39A"
./scripts/deploy-testflight.sh
```

The script auto-bumps CFBundleVersion, archives at `/tmp/Yaver.xcarchive`,
exports, and uploads. On flake/timeout, re-export from the existing archive
without re-archiving:

```bash
xcodebuild -exportArchive -archivePath /tmp/Yaver.xcarchive \
  -exportOptionsPlist /tmp/ExportOptions.plist -exportPath /tmp/YaverExport \
  -allowProvisioningUpdates -authenticationKeyPath "$APP_STORE_KEY_PATH" \
  -authenticationKeyID "$APP_STORE_KEY_ID" \
  -authenticationKeyIssuerID "$APP_STORE_KEY_ISSUER"
```

**TestFlight rate limit**: ~15-20 uploads/app/day. Don't re-run after
"Upload limit reached" — wait 24h.

**`uploadSymbols=false`** in ExportOptions.plist is mandatory. Xcode 15+
treats missing dSYMs as a fatal export error; `rnwhisper` ships without
dSYMs. Apple symbolicates server-side from bitcode anyway.

### Android — Play Store

CI handles it via `release-mobile.yml` on `mobile/v*` tags using
`PLAY_STORE_SERVICE_ACCOUNT_JSON`, `ANDROID_KEYSTORE*` secrets. Android
versionCodes auto-increment.

Local equivalent:
```bash
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh
PLAY_STORE_KEY_FILE=keys/google-play-service-account.json \
  python3 scripts/upload-playstore.py
```

`mobile/android/keystore.properties` is gitignored. Restore after
`expo prebuild --clean`:
```
storeFile=../../../keys/yaver-upload.keystore
storePassword=<password manager>
keyAlias=yaver-upload
keyPassword=<password manager>
```

### Force-tracked iOS overlay files

`mobile/ios/` is gitignored, but a few overlays are force-added because
`expo prebuild --clean` regenerates the dir:

- `mobile/ios/Yaver/AppDelegate.swift` — super-host bootstrap, ShakeDetectingWindow, YaverHTTPServer.shared.start(), safe bridge reload
- `mobile/ios/Yaver/Yaver-Bridging-Header.h` — Swift ↔ ObjC, GCDWebServer
- `mobile/ios/Yaver/YaverBundleLoader.swift` + `.m` — `NativeModules.YaverBundleLoader`
- `mobile/ios/Yaver/YaverScreenRecorder.swift` + `.m` — feedback visual capture
- `mobile/ios/Yaver/YaverHTTPServer.swift` — port-8347 bundle-receive server (currently a stub)
- `mobile/ios/Yaver/YaverInfo.swift` + `.m` — `isYaver` detection from guest bundles
- `mobile/ios/Yaver/YaverBundleValidator.swift` — HBC validation + `SDKManifest` singleton (currently stub)
- `mobile/ios/Yaver/sdk-manifest.json` — copy of mobile/sdk-manifest.json

The HTTPServer / Validator / Info stubs exist because `pbxproj` references
them. When filling in real implementations, match the signatures in
`YaverBundleLoader.swift` and `AppDelegate.swift`.

### Cold-start mobile rebuild (after `expo prebuild --clean`)

```bash
cd mobile
npm install --legacy-peer-deps
npx expo prebuild --platform ios     --clean --no-install
npx expo prebuild --platform android --clean --no-install
git checkout -- mobile/ios/ mobile/android/   # restore force-tracked overlays
cp mobile/sdk-manifest.json mobile/ios/Yaver/sdk-manifest.json
cd mobile/ios && pod install && cd ../..
# create mobile/android/keystore.properties (see above)
./scripts/deploy-testflight.sh
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh
```

First-time pod install is ~28 min, archive is ~15-20 min, gradle bundleRelease
is ~28 min.

### Disk-space preflight

Before any mobile deploy:
```bash
mobile-cache-cleanup.sh preflight    # fails hard if < 20 GB free
```
After successful deploy:
```bash
mobile-cache-cleanup.sh mark-deployed yaver
```

The script lives at `~/.local/bin/mobile-cache-cleanup.sh` (shared across
sfmg/talos/yaver/botox — don't fork).

### Version bumping (5 places, all must match)

When bumping `mobile/v<x>`:
1. `mobile/app.json` → `expo.version`
2. `mobile/package.json` → `version`
3. `mobile/ios/Yaver/Info.plist` → `CFBundleShortVersionString`
4. `mobile/ios/Yaver.xcodeproj/project.pbxproj` → `MARKETING_VERSION` (×2: Debug + Release)
5. `mobile/android/app/build.gradle` → `versionName`
6. `versions.json` → `mobile`

Build numbers (`CFBundleVersion` / `versionCode`) auto-increment in deploy
scripts.

### Mobile dev iteration (fast, no TestFlight)

USB-connected iPhone, no daily limit:
```bash
xcrun xctrace list devices 2>&1 | grep -v Simulator    # find UDID
yaver wire push                                          # detect framework + install Release build
# OR: cd mobile && npx expo run:ios --device <UDID>     # Debug build (needs Metro on :8081)
```

Multiple RN projects fight for port 8081. Either kill the others
(`pgrep "expo start" | xargs kill`) or build Release.

## Mobile dev-server proxy / Hermes flow on remote agent

Three commands matter:
- `POST /dev/start {framework, workDir}` — Metro/Vite/Flutter/Next
- `POST /dev/build-native` — compile Hermes bundle once
- `POST /dev/reload` (or `/dev/reload-app {mode}`) — hot reload (Expo/RN
  refresh through native Hermes path; web frameworks refresh via WebView)

`yaver-test-ephemeral` (Linux ARM) → mobile flow:
```bash
# from any machine signed in as the same user:
yaver insert sfmg                  # broadcast "open_app" to paired phones
# mobile receives via /blackbox/command-stream → navigates to Hot Reload tab
# → POST /dev/build-native on the agent → loads Hermes bundle
```

## Hetzner test box rules

`yaver-test-ephemeral` (cax21 arm64) is for remote integration testing.
**Disposable** — kill anytime. Reproducible from `ci/remote/bootstrap.sh`.

- No secrets ever live there.
- No user-visible copy mentions it.
- Cost-cheap pause: snapshot via `ci/hcloud/snapshot-server.sh`, then
  `hcloud server delete yaver-test-ephemeral`. Snapshot ~€0.10/mo vs
  €6.49/mo running.
- Recreate from snapshot: `ci/hcloud/create-server.sh` (uses
  `HETZNER_TEST_SNAPSHOT_ID` GH secret).
- Wired secrets (set with `gh secret set`): `HCLOUD_TOKEN`,
  `HCLOUD_SSH_PRIVATE_KEY`, `HETZNER_TEST_SERVER_ID`,
  `HETZNER_TEST_SERVER_IP`, `HETZNER_TEST_SNAPSHOT_ID`.

In Yaver device lists / Convex rows / `yaver ssh`, the same box may appear
as `ubuntu-4gb-hel1-1`. Set per-user alias `test` and use `yaver ssh test`.

## Conventions

- Go: standard layout, `gofmt`, build tags only when truly platform-specific.
- TypeScript / React: functional components, hooks. No class components.
- Convex: mutations for writes, queries for reads, httpAction for HTTP routes.
- Mobile: native builds (xcodebuild + Gradle), never Expo CLI for production.
- Tests: real HTTP servers on random ports, no mocks, no external deps. See
  `desktop/agent/*_test.go` for the pattern.

## Tests

```bash
# unit
cd desktop/agent && go test -count=1 ./...
cd relay && go test ./...

# e2e + integration suites
./scripts/test-suite.sh                     # full
./scripts/test-suite.sh --unit --lan --relay  # subset
./scripts/run-ci-local.sh                   # mirrors GH CI matrix

# trigger CI from terminal
./scripts/run-gh-ci.sh                      # all workflows
./scripts/run-gh-ci.sh ci e2e               # subset
```

Browser e2e (`e2e/`): Playwright + Chromium. CI runs on PRs touching `web/`
or `e2e/`.

## Feature pointers

For everything below, the canonical reference is the source. Brief pointers
only; the directories have their own READMEs / inline comments where it
matters.

| Area | Where |
|---|---|
| Auth + bootstrap + recovery | `desktop/agent/auth*.go`, `auth_recover.go`, `auth_bootstrap.go`; `backend/convex/auth.ts`, `deviceCode.ts` |
| Heartbeat + device registry | `desktop/agent/auth.go::SendHeartbeat`, `backend/convex/devices.ts::heartbeat`, `backend/convex/http.ts /devices/heartbeat` |
| Hot reload / dev server | `desktop/agent/devserver.go`, `devserver_http.go`, `dev_cmd.go`; mobile `app/(tabs)/apps.tsx` |
| Hermes push for guest apps | `cli/src/{analyzer,bundler,discovery,transport}.js`; mobile `ios/Yaver/YaverBundleLoader.*` + `YaverBundleValidator.swift` |
| Wire (cable) | `desktop/agent/wire_cmd.go`, `device_install.go`, `native_build.go` |
| Remote / insert (paired phone) | `desktop/agent/remote_mobile_cmd.go`, `mobile_session_http.go`; mobile `src/lib/openAppBus.ts`, `app/(tabs)/_layout.tsx` |
| Ping (reachability + auth-as-same-user) | `desktop/agent/ping_cmd.go`; mobile `DeviceDetailsModal.tsx::PingRow`; web `DevicesView.tsx::InlinePingButton` |
| Vault | `desktop/agent/vault.go`, `vault_cmd.go`, `vault_http.go`. NaCl secretbox + Argon2id, encrypted with auth-token-derived key |
| Deploy script generator + doctor | `desktop/agent/deploy_script_gen.go`, `doctor_build.go`, `deploy_script_http.go` |
| Guest access | `backend/convex/guests.ts`; `desktop/agent/guest_*.go`. Scopes: `full` / `feedback-only` / `deploy` |
| Container sandbox (deferred) | `desktop/agent/container_runner.go`, `Dockerfile.sandbox`. End-to-end testing TODO — see `DOCKER_REMAINED.md` |
| Multi-user | `desktop/agent/multiuser.go`, `multiuser_http.go`; `backend/convex/teams.ts` |
| Account linking + merge | `backend/convex/auth.ts::mergeUserInto`; `desktop/agent/account_cmd.go`, `mcp_auth_link_tools.go` |
| Phone-first mini backend | `desktop/agent/phone_backend.go`, `phone_backend_http.go`; mobile `app/phone-projects*` |
| Switch engine (target migrations) | `desktop/agent/switch_*.go` — 19 targets, snapshots, 7-day rollback |
| Hybrid mode (planner + implementer) | `desktop/agent/hybrid.go`, `hybrid_cmd.go`, `loop_exec_hybrid.go` |
| Autodev / loop | `desktop/agent/autodev_cmd.go`, `loop_cmd.go`, `loop_exec.go` |
| Session transfer / handoff | `desktop/agent/session_*.go`, `handoff_cmd.go`, `transfer.go` |
| Feedback SDK + black box | `sdk/feedback/{react-native,web,flutter}/`; `desktop/agent/blackbox*.go`, `feedback*.go` |
| Support sessions (TeamViewer-style) | `desktop/agent/support*.go` |
| SDK token security | `desktop/agent/auth.go::ValidateSdkToken*`, `sdk_token.go`, `tls.go` |
| Networking (relay + beacon + QUIC) | `desktop/agent/quic.go`, `beacon.go`; `relay/` |
| Workspace manifest | `desktop/agent/workspace*.go`; `yaver.workspace.yaml` |
| Managed toggle | `desktop/agent/managed*.go`; `backend/convex/userSettings.ts` |
| `ops` MCP grand-tool | `desktop/agent/ops*.go` — single MCP tool, 20 verbs |

## Local development

```bash
cd backend && npx convex dev          # convex dev
cd web && npm run dev                  # next dev
cd mobile && npm run web               # browser RN preview (dev only)
cd desktop/agent && go run . serve     # agent
cd relay && go run . serve --password <secret>
```

For dev-server iteration on a specific RN project: see "Mobile dev iteration"
above. Don't tell users to run `expo start` manually — the agent handles it.