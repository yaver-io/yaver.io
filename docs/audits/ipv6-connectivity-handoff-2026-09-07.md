# IPv6 connectivity and `yaver ssh primary` handoff — 2026-09-07

This is a continuation handoff for another coding session. Code is the source
of truth; re-read every named file and diff before acting. This document is
intentionally sanitized: it contains no customer email, device UUID, real
machine address, access token, or private credential.

## User's requested outcome

- Make `yaver ssh primary` work when the caller has working IPv6 but broken or
  absent IPv4.
- Make IPv6 a first-class transport throughout the Yaver Go agent/CLI, relay,
  Convex, web, mobile/dogfood libraries, SDKs, and native client surfaces.
- Preserve IPv4 as a fallback; relay fallback must actually try later
  candidates instead of stopping at a dead first candidate.
- Integrate **all current Yaver workspace work** into `main`, resolving rebase
  conflicts if required, then commit and push.
- Never hardcode the owner's email/account identifiers, personal machine
  addresses, or other private data in the public product repository.

## Incident diagnosis and immediate recovery — complete

The Mac's Wi-Fi association and global IPv6 route were healthy, but IPv4 DHCP
had fallen back to a `169.254/16` self-assigned address and there was no IPv4
default route. Browser traffic worked over IPv6. The original public relay DNS
had an A record only, and the CLI's direct target was also IPv4-only, so both
direct SSH and relay fallback were unreachable from that network.

Immediate, scoped recovery was completed:

- The public relay nginx configuration was backed up, changed to listen on
  IPv4 and IPv6 for ports 80/443, syntax-tested, and reloaded.
- A DNS-only AAAA record was added for the existing public relay hostname.
- Forced-IPv6 relay `/health` and authenticated device `/info` probes returned
  HTTP 200.
- The primary Ubuntu machine's existing Yaver config was backed up and its own
  globally routed IPv6 HTTP endpoint was prepended to `publicEndpoints`.
- The installed CLI then successfully ran `yaver ssh primary`, selected IPv6,
  executed a remote marker/hostname/user command, and returned successfully.

Do not repeat or undo these live mutations blindly. Re-probe the operation
first. No mobile/npm release or general product deployment was performed.

## Branch and commit state

At handoff time:

- Current branch: `preserve/mac-dirty-20260907`
- `51ed590ee chore: preserve concurrent task relay and CI improvements`
  contains the previously dirty 77-file workspace, including the first IPv6
  agent/relay/Convex/client changes plus concurrent CI/task/UI work.
- `59cf7b0a8 fix(relay): configure wildcard IPv6 records` contains the
  self-hosted wildcard relay AAAA setup.
- `origin/main` was at `a43df1acd` when the branch was created. Fetch again;
  do not assume it is still current.
- There are additional uncommitted cross-client IPv6 edits listed by
  `git status`; they must be verified and committed before integration.

The working tree is shared. Preserve new user/session changes and inspect the
reflog/status before rebasing. The user explicitly authorized committing all
current Yaver workspace work, rebasing/merging, conflict resolution, and push.

## Product changes already in the two branch commits

### Go agent and CLI

- `desktop/agent/auto_public_ip.go`
  - Publishes globally routed interface IPv6 endpoints using bracketed URL
    authority syntax.
  - Keeps IPv4 echo discovery as a fallback and deduplicates endpoints.
- `desktop/agent/main.go`
  - Publishes private IPv6 ULA addresses in `localIps`.
  - Preserves the real relay-shell error instead of printing only “relay shell
    also unavailable”.
  - Public SSH endpoint selection now probes address-family candidates and can
    fall through from unreachable IPv4 to reachable IPv6.
- `desktop/agent/shell_cmd.go`
  - Remote terminal fallback dials all ordered candidates instead of only
    `candidates[0]`.
- `desktop/agent/remote_status_cmd.go`
  - Classifies local `network is unreachable` / `no route to host` accurately,
    rather than blaming remote auth/bootstrap state that was never measured.
- `desktop/agent/net_doctor.go`
  - Detects IPv4 and IPv6 routes/addresses, probes both internet families in
    parallel, uses a DNS hostname for HTTPS trace, and reports v4-only,
    v6-only, or dual-stack.
  - A live run found that macOS requires `ping6` for IPv6. The source was then
    updated to use family-aware ping and to downgrade gateway ICMP failure when
    successful public TCP disproves a broken gateway. **This latest doctor
    fix still needs an isolated compile/test and another live run.**
- `desktop/agent/remote_status_network_test.go` contains regression tests for
  local-route diagnosis, relay error preservation, terminal candidate
  fallthrough, and IPv4-to-IPv6 SSH-host fallthrough. Incident addresses were
  replaced with RFC 5737 documentation addresses.

### Relay and provisioning

- `relay/server.go` binds HTTP and QUIC to family-neutral wildcard addresses.
- Both canonical nginx relay configs listen on IPv4 and IPv6 for HTTP/HTTPS.
- Bootstrap/install/cloud-init nginx snippets include IPv6 listeners.
- `backend/convex/provisionRelay.ts`, `managedRelays.ts`, `relayPool.ts`, and
  `schema.ts` carry `serverIpv6`, derive the usable Hetzner host address from
  its routed prefix, create A+AAAA records, inherit IPv6 on shared pool hosts,
  and delete all tenant DNS records during teardown.
- `scripts/setup-relay-wildcard.sh` now detects/accepts an optional public IPv6
  and creates/updates the wildcard AAAA record while retaining A-only fallback.
- Mobile's built-in free-relay fallback uses the relay hostname rather than a
  literal IPv4 address.
- `scripts/relay-dual-stack-parity.test.mjs` guards nginx listeners, bootstrap
  listeners, relay hostname use, managed AAAA provisioning, and copied client
  core parity.

### Convex device transport

The existing device mutations and list routes already accept and preserve
`quicHost`, `localIps`, and `publicEndpoints` as strings/string arrays. The Go
agent now supplies valid IPv6 values; no IPv4-only validator was found in that
path. Managed relay rows gained the optional `serverIpv6` field described
above. Re-run Convex code generation/type validation before merge.

## Additional uncommitted all-client work

A shared RFC 3986 URL-host formatter was introduced in the copied client core:

- `mobile/src/_core/urlHost.ts`
- `web/lib/_core/urlHost.ts`
- `sdk/feedback/react-native/src/_core/urlHost.ts`

Thin re-exports/helpers also exist for mobile lib, web lib, and the feedback
web SDK. The helper brackets raw IPv6 literals, preserves already bracketed
hosts, and accepts nullable host state used during connection startup.

Mechanical call-site conversion is currently present across:

- Mobile device context, QUIC/HTTP transport, pairing, probe/cache helpers,
  app screens, and every dogfood operations client (CI, robot, arm, printer,
  testkit, data collection, home, EV charging, twins, etc.).
- Web agent client, transport model, dashboard device actions, and managed
  cloud direct URL.
- React Native feedback SDK discovery/auth/pairing and copied core.
- Web feedback SDK discovery.
- Flutter feedback SDK candidate construction, managed-machine discovery, and
  manual URL normalization.
- Desktop Electron direct connection.
- Android TV `BoxTarget` endpoint construction.
- tvOS model, machine registry, capture, and feedback endpoint construction.
- JS SDK already had IPv6 bracketing and a passing test; its advanced example
  was aligned.

IPv4 subnet sweep expressions such as `prefix.suffix` remain IPv4-only by
definition and are acceptable only as additive LAN discovery. Wear OS stores
and consumes a complete base URL instead of reconstructing a host authority;
visionOS had no generic raw-host HTTP construction in the audited paths.

## Verification evidence so far

Passed:

- Real installed `yaver ssh primary` over the primary server's IPv6 address.
- Forced-IPv6 public relay health and authenticated tunneled agent probes.
- `go test ./... -count=1` in `relay/`.
- Isolated clean-HEAD agent tests with only the intended Go IPv6 files
  overlaid; all targeted package/subpackage tests passed.
- React Native feedback SDK IPv6 candidate test and SDK build.
- JS SDK: 31 tests passed, including direct IPv6 candidate bracketing.
- `node scripts/relay-dual-stack-parity.test.mjs`.
- `git diff --check` at multiple checkpoints.
- Live pre-fix doctor measurement proved IPv6-only TCP, DNS, and HTTPS were
  healthy; it exposed the `ping6` diagnostic issue described above.

Not yet complete / must rerun:

- Mobile and web TypeScript initially found nullable `host` arguments after
  the mechanical conversion. The formatter signature was changed to accept
  `string | null | undefined`; **rerun both typechecks**.
- Flutter is not installed on this Mac (`flutter: command not found`). Run its
  tests on a configured runner, or clearly record that limitation.
- Re-run `bash -n` on all changed relay scripts after the final wildcard block
  placement.
- Re-run the isolated Go agent test/build after the family-aware ping edit,
  then execute the built `yaver net doctor --json`. Expected result on the
  current network: link/internet healthy with family `IPv6-only`; gateway ICMP
  must not become a false overall failure.
- Run Android TV compilation/unit tests and an available tvOS Swift build/test
  lane in proportion to local tool availability.
- Run Convex codegen/type validation and relevant backend tests.
- Add direct unit tests for the shared `urlHost` helper in mobile/web or extend
  the parity test to execute the helper, not only compare its source.
- Re-scan all generic URL construction after compilation:
  `rg 'http://\$\{[^}]+\}' mobile web sdk desktop/app androidtv tvos wear`.
  Raw generic host/IP authorities should use a formatter. Literal IPv4 subnet
  sweeps, loopback URLs, and variables already preformatted immediately above
  are expected exceptions.

## Privacy and secret audit gate before commit

Before staging, inspect the entire range `origin/main...HEAD` **plus the working
tree**, including untracked files. Required gates:

1. No owner email/name/account identifier added by the diff.
2. No real incident/server IPv4 or IPv6 literal. Tests use reserved
   `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`, and `2001:db8::/32`.
3. No private-key marker, token, password, API key, credential file, or
   high-entropy secret.
4. Inspect `electron/.yaver/services.yaml` from the broad preservation commit;
   confirm it is generic project configuration and contains no local/private
   value. Remove it from the branch if it is machine-local.
5. Do not print secret values while scanning; report filenames/line numbers or
   use a secret scanner.

## Recommended finish sequence

1. Re-read `CLAUDE.md`, this handoff, current status, recent log, and reflog.
2. Finish/fix the validation items above. Do not weaken tests to get green.
3. Audit all branch + worktree content for secrets and personal infrastructure.
4. Commit the remaining all-client IPv6 changes on the preservation branch.
5. Fetch `origin` (the Mac may still lack IPv4; use a temporary SOCKS proxy over
   the already working IPv6 SSH route if GitHub is unreachable directly).
6. Rebase the preservation branch onto current `origin/main`; resolve conflicts
   by preserving both the concurrent feature work and IPv6 behavior. Re-run
   checks after conflict resolution.
7. Fast-forward/merge the verified branch into local `main`, then push `main`.
   No force push unless the user separately authorizes it.
8. Verify remote `main` contains the resulting commit(s), and re-run the real
   `yaver ssh primary` operation. Do not publish npm/mobile or deploy unrelated
   surfaces without separate release authorization.

## Definition of done

- Direct SSH chooses a reachable IPv6 endpoint when IPv4 is unavailable.
- Relay DNS, nginx, QUIC/HTTP server binds, and managed/self-hosted provisioning
  are dual-stack with IPv4 fallback.
- The Go agent advertises IPv6 through Convex without losing it.
- Every generic client URL builder emits `[IPv6]:port`, while IPv4 and DNS names
  remain unchanged.
- Relay/terminal selection attempts later candidates after an earlier family
  fails.
- Diagnostics say “IPv6-only internet is healthy” when that operation is
  proven, and do not let blocked ICMP override successful TCP/HTTPS.
- Relevant builds/tests pass or any unavailable platform lane is explicitly
  documented.
- No private data or owner-specific infrastructure is committed.
- All authorized workspace work is rebased into and pushed on `main`.
