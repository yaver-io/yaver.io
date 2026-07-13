# Yaver Consolidated Deep Security Audit — 2026-07-13

Merges the 6-agent parallel audit (Claude) + the independent source review
(codex, `deep-security-analysis-2026-07-13.md`). Threat model: attacker has full
public source + git history, can reverse/sniff clients, can create a free Yaver
account, and can be a rooted paying customer on a managed box. Code was source of
truth; every finding cites `file:line`.

**Status legend:** ✅ FIXED+DEPLOYED · 🟡 FIXED (pending client release) ·
⏳ DEFERRED (documented; needs decision/larger change) · ✔ verified SOLID.

---

## Fixed this session

| id | sev | area | status | commit |
|----|-----|------|--------|--------|
| Apple ATO | **CRITICAL** | `/auth/apple-native` unsigned-JWT → any-account takeover by email | ✅ deployed + verified (forged token → 401) | 606721530 |
| deviceMetrics/deviceEvents IDOR | MED | authed cross-user telemetry/events read | ✅ deployed | 606721530 |
| requireFreshTotp fail-open | LOW | 2FA bypass on unlink/merge when seed missing | ✅ deployed | 606721530 |
| bootstrap userId leak | LOW-MED | `/devices/bootstrap` returned account userId | ✅ deployed | 606721530 |
| Host-share P0 | **CRITICAL** | arbitrary-abs-path read/write via `rootPath` | 🟡 pending agent release | ec848b9a5 |
| Metadata SSRF | **CRITICAL** | http_request/timing/curl_timings → 169.254 | 🟡 pending agent release | 74b28bb06 |
| Relay-loopback remoteness | HIGH | relay traffic bypassed RD-control + dev-bundle-sig | 🟡 pending agent release | f6f648697 |
| Cross-machine secret exfil | **CRITICAL** | ops:secrets/env/runner_auth pullable by remote worker | 🟡 pending agent release | f6f648697 |

The 🟡 agent fixes ship to machines via the next `cli/v*` release.

---

## CRITICAL — remaining (need decision / larger change)

### exec_command/curl reaches cloud metadata (go agent #2)
`exec.go:94 StartExec` → `ValidateCommand` has no link-local/metadata filter, so
`exec_command{command:"curl http://169.254.169.254/..."}` fetches the managed-box
broker/cloud creds. The `guardOutboundHTTPURL` fix only covers the 3 http tools.
⏳ **Fix:** OS-level firewall drop of `169.254.169.254` on managed boxes (a code
check races DNS rebinding) + a target classifier on the exec outbound path. Best
done in the box golden image / firstboot, not agent code.

### LAN bootstrap auto-pair leaks the phone's session bearer (relay #1)
`mobile DeviceContext.tsx:2178-2228` + `pairDevice.ts:211-219` + `beacon.ts:183`.
A signed-in phone auto-POSTs its raw session bearer in cleartext to any UDP-19837
bootstrap beacon (`na=true` bypasses the token-fingerprint + known-device gates);
the "passkey" is read from the attacker's own beacon. Zero-interaction LAN account
takeover; the encrypted path is downgradable via a bogus `dpk`.
⏳ **Fix (mobile, needs care):** never send a raw token over plaintext HTTP;
require the NaCl-box encrypted path for ALL auto-pair; a `dpk`/Convex mismatch
must abort, not downgrade; auto-pair only Convex-known devices; unknown fresh box
needs explicit human confirmation + out-of-band passkey.

### Idle-reaper "billing forever" (secrets C1/C2/C3/M1) — RESOLVED AS DESIGN
`idleSweepCron` exists but is intentionally NOT registered in `crons.ts`.
**This is correct:** a perpetual Convex cron is recurring paid compute (owner
directive: never Convex crons — they make you poor). Scale-to-zero = the box
self-parks (agent `machine_activity.go` 90s local → `/machine/park-self` →
snapshot-before-delete). ⏳ **Residual (agent-dead case):** add the control-plane
backstop as a **relay-server systemd timer** POSTing `/crons/run
{name:"cloudIdleSweep"}` (NOT a Convex cron) + a hard max-runtime reaper. Also:
on wallet-empty `suspend`, drive a control-plane snapshot+delete (don't rely on
the box's own agent); reaper must consider rows in `error`/`provisioning` with a
live `hetznerServerId`.

---

## HIGH — remaining

- **git-credentials peer-proxy leak** (go agent #3, `env_profile.go:619`): `/agent/toolchain-sync/git-credentials` streams plaintext PATs to any same-user remote device. ⏳ Refuse when relay-bridged/proxied (reuse the new `opsCallIsRemote`/`isRelayBridged`).
- **nmap/port/arp third-party scan** (go agent #4, `mcp_network.go:115/143/158`): no target validation → aggressive scans from the datacenter box (account-suspension + do-no-harm violation). ⏳ Wire the Policy Guard / a target classifier (allow RFC1918/LAN + authorized only). Ties to go agent #10 (Policy Guard is advisory, never on the exec/net path).
- **Gateway spend not hard-capped** (secrets H2/H3): check-then-call, no atomic reserve; `MAX_CENTS_PER_REQUEST` clamps not rejects; input tokens unbounded; missing provider usage bills zero. ⏳ Reserve-and-settle in the UserMeter DO; hard-reject `worst>maxCents`; price real input tokens; bill estimate on missing usage.
- **Activity-sync privacy-contract breach** (secrets H1, `convex_state_sync.go:214/298`): ships absolute paths (`target`) + deploy-log tails (`error`) to Convex; the privacy test structurally misses it. ⏳ Send `filepath.Base` / omit `error`; add a real-payload test.
- **Relay password fallback + `?__rp=`** (codex P1, relay #4): shared account-wide password, in-URL secret, stored plaintext+indexed in Convex (`userSettings.ts:631`). ⏳ Make device-sig mandatory on public relay; disable `?__rp=` by default; hash the stored relay password.
- **Relay prefix routing** (codex P1, `relay/server.go:1235`): authorizes the URL deviceId but may route to a prefix-sharing tunnel. ⏳ Remove prefix routing from public `/d/`; resolve aliases before the relay.
- **Machine-scope enforcement incomplete** (codex P1): many mutations call only `validateSessionInternal`; a compromised box's machine token acts as full owner. ⏳ `requireFullSession` vs `requireMachineOwnDevice`; default-deny machine scope + allowlist.
- **Sign-key rotation by any full session** (codex P1, `devices.ts:565`): stolen session can rotate a device's relay signing key. ⏳ Require old-key proof or step-up.
- **Beacon spoofing MITM** (relay #2): 32-bit fingerprint + cleartext id let a LAN attacker repoint an authed device to attacker IP; phone sends bearer over plaintext, no server-identity check. ⏳ Pin per-device TLS fingerprint (`tf`), connect 18443, or require signed requests on LAN.
- **Relay is cleartext MITM** (relay #3, `main.go:10669` `InsecureSkipVerify:true`): agent↔relay QUIC unverified + no device↔device E2E. ⏳ Pin relay pubkey; layer NaCl-box E2E off `signPublicKey`.

---

## MEDIUM / LOW — remaining

- Provisioning `attest` replayable in ±10min (auth #2, `provisioning.ts:348`) → single-use nonce.
- TOTP seed stored plaintext in Convex (auth #3 / relay note) → envelope-encrypt.
- `POST /machines` ungated (Convex #3) → apply the `/billing/yaver-cloud/provision` gates.
- Non-atomic wallet reservation (Convex #4 / secrets M2) → single-mutation reserve+create.
- RD view default-on + no `"view"` audit emitted + kill-switch TOCTOU (go agent #6/#7).
- `read_file`/`write_file` no path jail for delegation-scoped SDK tokens (go agent #8).
- Host-share/SDK path allowlists use prefix (not segment-aware) matching (codex P2); host-share allowed-projects by basename (codex P2).
- `guests.lookupPublicUser` returns email to any authed caller (Convex #5) — LOW, has invite-UX use.
- Relay password cache key includes raw secret (codex P2); QUIC register has no read deadline / slowloris (relay #5).
- Vault v1 key derived from auth token (secrets M3) → finish v2 OS-keychain migration.
- CI: `TAILSCALE_AUTHKEY`/`GLM_API_KEY` reach same-repo branch PRs; `mobile-headless.yml` has no `permissions:` (secrets L1). No `pull_request_target`, prod secrets tag/dispatch-gated ✔.

---

## Verified SOLID (attacked, held up — don't re-audit)

- **Guest-grant authorization** (the "attacker forges a guest link into a victim's resources" attack): invite binds `hostUserId = session.user._id` (can only share your OWN resources); redemption requires the invitation to exist for that guest; approved devices are **clamped to the host's owned devices** (`guests.ts materializeInvitationAccept`). Guests walled off from `/mcp`,`/exec`,`/vault`,`/rd/*`,`/capture/*`, file tools; header spoofing stripped + re-stamped.
- Device ownership on register/heartbeat/update/list (`device.userId===session.user._id`). Session tokens 256-bit, hashed-only in Convex, rotation grace. Account merge requires source session + fresh TOTP. Device-code/broker onboarding single-use + TTL + user-bound. Relay sig verify fails CLOSED (codex "fail-open" was a mischaracterization — it falls back to the password path which itself requires Convex-validated same-owner ownership); routing anti-hijack refuse-on-collision; DoS admission controls comprehensive. Vault: fresh nonce, 0600, owner-only HTTP, guests excluded. All `resolveUser`-based Convex fns fail closed (no `auth.config`). `pauseMachine` snapshot-before-delete, HCLOUD fail-closed. No `pull_request_target` in CI. Egress proxy anti-SSRF (RFC1918+169.254). Command-injection hardening (git argv, native-build metachar reject) complete.

---

## Recommended fix order (remaining)
1. Ship the 🟡 agent fixes (next `cli/v*` release) — closes host-share, metadata SSRF, relay-loopback, secret exfil on all machines.
2. exec metadata firewall on managed boxes (golden image).
3. Mobile LAN auto-pair: kill plaintext token send + downgrade.
4. Relay-server systemd timer for idle-sweep backstop (NOT Convex cron).
5. git-credentials peer-proxy refuse (quick, reuse `opsCallIsRemote`).
6. Gateway reserve-and-settle; activity-sync path/log sanitize.
7. Relay: disable `?__rp=`, hash stored relay password, remove prefix routing.
8. Machine-scope default-deny; sign-key rotation step-up; TOTP encryption; attest nonce.
