# Security audit — screen black box + remote monitoring + mesh

**Scope:** the `screenlog` feature (screen-frame + keystroke/mouse capture,
reboot-durable, remotely controllable) and the "mesh" surfaces that can reach
it (account-shared devices, ACL peer tool-calls, relay transport). Written
2026-06-06 against the code on `main` at `d0814cba` + the kill-switch change in
this session.

**Why this audit exists:** what we built is, by construction, **surveillance
software**. A box can be made to record everything on screen (and optionally
every keystroke), keep doing it across reboots and sign-out, and stream it to a
remote operator. That is legitimate for "watch my own headless box" and
consented "help my dad with his PC" — and indistinguishable, at the bytes
level, from spyware. This document states the threat model honestly, rates the
real findings, and documents the **kill switch** (`yaver screenlog kill`) added
in response.

> **One-line takeaways**
> - The dangerous-by-design property is **silent local auto-resume after
>   reboot** — fixed-direction recommendation below, *not* yet enforced.
> - There is now a real panic button: **`yaver screenlog kill [--purge]`**.
> - "Mesh" today = the **application-layer ACL peer layer**, not a network
>   overlay (no WireGuard in tree). Peering does **not** implicitly grant
>   screen access — but the grant check has a header-trust gap (F4).

---

## 1. Capability = attack surface

| Capability | Where | Worst-case abuse |
|---|---|---|
| Screen capture (all displays, 2 s cadence) | `screenlog_capture.go` | full visual record incl. anything on screen (banking, DMs, passwords typed into visible fields) |
| Keystroke/mouse capture | `screenlog_input*.go` | keylogger — credentials, 2FA codes, message content |
| Reboot-durable auto-resume, **auth/internet-independent** | `screenlog_autostart.go` | persistent covert recorder that survives sign-out + offline |
| Remote start/stop | `screenlog_http.go`, MCP `screenlog_*` | operator turns recording on from afar |
| Bulk export (frames + events as tar.gz) | `/screenlog/<id>/export` | one-request exfil of the whole record |
| Activity analysis ("what did this machine spend time on") | `screenlog_analyze.go` | behavioural profiling |

Every item above is **local-first and never touches Convex** (enforced by
`convex_privacy_test.go` — window titles, paths, frames, events are all on the
forbidden list). That privacy property is real and good. The risk is **not**
cloud leakage; it's that the *device owner / token holder* has a turnkey
surveillance tool and the *recorded person* may not.

---

## 2. Trust & permission model (as built)

Defense layers a start request passes through, outermost first:

1. **Transport auth (`s.auth`)** — every `/screenlog/*` route is wrapped in
   `s.auth` (`httpserver.go:606-616`). No public/share path (unlike clips).
2. **Guest scope** — delegated guests hit `guest_scope.go`; `/screenlog/*` is
   on the **full-scope allow-list only**. feedback-only / read-only support
   guests can't reach it.
3. **ScreenlogPolicy** (`screenlog_policy.go`) — the consent gate, enforced in
   `startScreenlogGuarded()` so neither the HTTP nor the MCP surface can skip
   it:
   - `Enabled` — master kill-switch; `false` ⇒ refuse all starts.
   - `AllowRemoteControl` — may a non-loopback caller start/stop (default
     **true**).
   - `RequireMeshGrant` + `AllowedPeers` — a mesh peer must be explicitly
     granted (default **true**).
   - `AllowInputCapture` — keystroke/mouse ingest gate (default **OFF**).
   - `NotifyOnStart` — notify on remote start (default **true**).
4. **Audit trail** — every start/stop/deny/policy/kill appended to
   `~/.yaver/screenlog/audit.jsonl` (local, 0600).

This is a genuinely thoughtful model. The findings below are about the **gaps
between it and the threat**, not its absence.

---

## 3. Findings

Severity = impact × ease, in the context "attacker holds a credential that
clears `s.auth`, or is the device owner acting against the recorded person."

### F1 — Silent local auto-resume (HIGH, design) — *recommend, not yet enforced*
`NotifyOnStart` fires **only when `caller.Remote` is true**
(`screenlog.go:483`). The reboot auto-resume path (`resumeScreenlogIfEnabled`)
runs locally, so it produces **no notification and no on-screen indicator**.
Combined with `--persist`, this is a recorder that comes back silently on every
boot, signed-out and offline, with nothing telling the person at the keyboard.
This is the single most dangerous property and the one that crosses from
"monitoring tool" into "spyware."

**Recommendations (in priority order):**
1. A **persistent recording indicator** on the recorded machine whenever a
   session is active — tray icon / periodic toast — independent of
   remote-vs-local. Capture without an indicator should be impossible.
2. **Notify on auto-resume** too (reuse `defaultDesktopNotify` /
   `globalNotifyManager`), not just on remote start.
3. A **consent receipt** at `--persist` arm time (record who/when/what into the
   audit log; optionally require a typed confirmation on first arm).
4. Surface "this machine is being recorded" in `yaver status` and the mobile/web
   device row.

Why not enforced in this pass: forcing an always-on indicator is a product /
UX decision that changes the legitimate "headless box" use case; it needs a
deliberate default, not a unilateral flip. Flagged HIGH so it gets one.

### F2 — No single panic button (was HIGH) — **FIXED this session**
Before: `stop` ended the live session but left autostart armed and the feature
enabled; `disable` flipped the policy but left the running loop capturing until
process restart. No one command guaranteed "all recording, everywhere, off."

**Fix:** `yaver screenlog kill [--purge]` (`screenlog_kill.go`) does, in order:
(1) stop the live loop + input capture, (2) disarm the reboot auto-resume
marker, (3) flip the master kill-switch (`policy.Enabled=false`) so nothing —
local, remote, mesh, or autostart — restarts it, (4) optional `--purge` wipes
all captured sessions off disk. It is **deliberately not gated** by
`screenlogEnforce`: stopping surveillance is always allowed for any
authenticated caller. Available as CLI (`kill`/`panic`), HTTP
(`POST /screenlog/kill`), and MCP (`screenlog_kill`). Tests in
`screenlog_kill_test.go`.

### F3 — Path traversal via session id (MEDIUM) — **FIXED this session**
`screenlogSessionDir(id)` did `filepath.Join(base, id)` + `MkdirAll` on an id
taken straight from the URL path (`/screenlog/<id>/...`), with no validation.
Behind `s.auth` and gated by needing an `index.json` in the target dir, so
low-but-real. **Fix:** reject any id containing `/`, `\`, or `..`
(`screenlog.go`), with a regression test
(`TestScreenlogSessionDirRejectsTraversal`).

### F4 — Mesh-grant gate trusts a client header (MEDIUM) — *open*
`RequireMeshGrant`/`AllowedPeers` only triggers when `caller.Mesh` is true, and
`caller.Mesh` is set from the **client-supplied** `X-Yaver-Peer` header
(`screenlog_http.go:81-84`). A peer that holds a token clearing `s.auth` can
simply **omit** the header → treated as a plain remote caller → gated only by
`AllowRemoteControl` (default **true**), bypassing the per-peer grant.

**Recommendations:**
- Bind peer identity **server-side** from the authenticated token (the token a
  peer presents should map to a known peer id); never trust `X-Yaver-Peer` from
  the client to *lower* the trust level.
- Verify (and add a test for) **what token classes clear `s.auth` for
  `/screenlog/*`**. If SDK-scoped tokens or peer-granted tokens reach it, the
  start gate is weaker than it looks. Ideally screenlog requires an owner-class
  token, not any valid token.
- Consider defaulting `AllowRemoteControl=false` for the surveillance-grade
  feature and making remote enable an explicit, audited owner action.

### F5 — Owner token is the crown jewel (HIGH, inherent) — *open / document*
Any credential that clears `s.auth` can: start recording, enable `--persist`,
pull the entire record via `/export`, and read the activity analysis. Sessions
are 1-year and refreshed on every heartbeat. **A single stolen token = full,
durable, covert surveillance + exfil.** This is inherent to "the agent is
controllable," but for *this* feature the blast radius is uniquely severe.

**Recommendations:**
- Treat screenlog as a **separately-consented capability**, not "anything the
  agent can do." Require an explicit local opt-in (`yaver screenlog enable`)
  before the feature is reachable at all — i.e. ship with `Enabled=false` by
  default on a fresh install, flipped on only by a local human. (Today the
  default is `Enabled=true`.)
- Shorten/scope the token class that can reach `/screenlog/*`.
- Rate-limit + alert on `/export` (bulk pull is the exfil signature).

### F6 — Keylogger is the sharpest edge (MEDIUM, mitigated) — *partial*
Input capture is **off by default** (`AllowInputCapture=false`) and ingest is
refused unless the owner runs `yaver screenlog allow-input`
(`screenlog_http.go:219-223`). Typed text is **redacted by default**
(`redact = !AllowRawText`), `--allow-raw-text` opts into verbatim. Good
defaults. Residual risk: redaction is heuristic (timing + app attribution still
leak), and **screen frames capture passwords visually regardless of input
redaction**. Recommend: a window/app **denylist** (skip capture when a password
manager / banking app / known-sensitive title is foreground), and never store
raw text from secure input fields.

### F7 — `/export` is a one-shot exfil primitive (MEDIUM) — *open*
`GET /screenlog/<id>/export` streams the whole session (frames + events) as a
tar.gz to any authed caller. Stays off Convex (good) but is wide open to token
holders and granted peers over relay/LAN. Pair with F5. Recommend: audit-log
every export (it currently isn't), rate-limit, and consider requiring a
fresh/owner-class token for export specifically.

### F8 — Data at rest is unencrypted (LOW/MEDIUM) — *open / document*
Frames + `events.jsonl` live under `~/.yaver/screenlog/` (dir 0700, files
0600). Anyone with local FS access as the user — or a machine backup / cloud
sync of the home dir — reads the full record in the clear. Recommend: optional
at-rest encryption (reuse the vault's NaCl secretbox + Argon2id key path), and
clearly document that the black box is plaintext-on-disk by default.

### F9 — "Local-only, never uploaded" is true for *storage*, not *viewing* (LOW, accuracy)
The viewer HTML and docs say "local-only, never uploaded"
(`screenlog_http.go:289`). Accurate for **storage** (frames never go to
Convex/cloud). But **remote viewing does transmit frames** — over authed QUIC
(relay or LAN) when a phone/web client pulls them. Recommend wording: "stored
only on this machine; transmitted only to your own authenticated devices when
you view remotely." Don't let the copy imply frames never leave the box when
remote viewing is a headline feature.

### F10 — Autostart marker is user-writable (LOW, inherent)
`autostart.json` is plain JSON; any process running as the user can arm
persistent recording. Not privilege escalation (same user), but it means
"malware-as-user" can turn the box into a recorder using Yaver's own durable
machinery. Inherent to a user-level agent; note it, and have the recording
indicator (F1) be the backstop that makes it *visible* even when armed by
something other than the owner.

---

## 4. "Yaver mesh" — what it actually is, and its exposure

There is **no WireGuard / network overlay in the tree** (`grep` for
`wireguard`/`networkingMode`/`mesh_` finds nothing in `desktop/` or `docs/`).
The 2026-06-05 "Yaver Mesh WireGuard overlay" was built but never committed; it
is **not** part of the current attack surface. The "mesh" that can reach
screenlog today is the **application-layer ACL peer layer** (`acl.go`):

- **Outbound** (you call a peer's tools): `sendHTTP` authenticates with a
  **per-peer token** (`peer.Auth`), *not* your owner token (`acl.go:312`). Good
  — a peering doesn't hand the peer your master credential, and a compromised
  peer relationship is scoped to that token.
- **Inbound** (a peer calls your tools): the peer presents the token you
  granted; `RequireMeshGrant` + `AllowedPeers` add a screenlog-specific gate on
  top, so **peering ≠ screen access** — it must be granted per peer. Subject to
  the header-trust gap in **F4**.

**When the WireGuard mesh does land, audit it separately:** an overlay IP makes
*every* agent port (incl. `:18080` screenlog HTTP) reachable to peers at L3.
The saving grace is that `s.auth` + ScreenlogPolicy still gate the HTTP — but
the overlay widens who can *attempt* auth, so the F4/F5 token questions become
more load-bearing, not less.

**Relay:** screenlog never transits the relay as stored data; the relay is
pass-through, password-protected QUIC. Frames cross it only as the payload of an
authed pull, end-to-end inside the QUIC session. No new storage exposure, but
see F9 for the "leaves the box on view" nuance.

---

## 5. The kill switch — operator + recorded-person reference

```bash
# PANIC: stop live session + disarm reboot-resume + flip master kill-switch
yaver screenlog kill

# ...and also wipe every captured frame/event off disk
yaver screenlog kill --purge
```

- Works **locally and remotely** (it's behind `s.auth` like every screenlog
  route, but **not** behind the start-gate — stopping is always permitted).
- After `kill`, recording cannot restart — not on reboot, not remotely, not via
  a mesh peer — until the owner runs `yaver screenlog enable` again.
- `--purge` deletes only the `slog-*` session dirs under
  `~/.yaver/screenlog/`; it **preserves** `policy.json`, `autostart.json`, and
  `audit.jsonl` so the kill itself stays auditable. Nothing outside the
  screenlog root is ever touched.

**For the recorded person (e.g. a family member whose PC is monitored):** the
off-switch is local and needs no Yaver account —
```bash
yaver screenlog kill        # stop everything, now and across reboots
```
or, with no Yaver at all, delete `~/.yaver/screenlog/autostart.json` and stop
the agent. This must be **documented in plain language** for non-technical
recorded users — see F1; an off-switch nobody knows about isn't consent.

**Granular controls** (unchanged, for reference):
```bash
yaver screenlog disable          # master kill-switch only (leaves data)
yaver screenlog deny-remote      # block all non-local start/stop
yaver screenlog deny-input       # block keystroke/mouse capture
yaver screenlog revoke-peer <id> # drop a mesh peer's screen grant
yaver screenlog audit            # who started recording, when, from where
```

---

## 6. Hardening backlog (prioritised)

| # | Action | Severity | Status |
|---|---|---|---|
| F2 | `yaver screenlog kill` panic button | HIGH | ✅ done |
| F3 | session-id traversal guard | MED | ✅ done |
| F1 | always-on recording indicator + notify on auto-resume | HIGH | ⏳ recommend |
| F5 | ship `Enabled=false` by default; require local opt-in; scope token class | HIGH | ⏳ recommend |
| F4 | derive peer id from token server-side, not `X-Yaver-Peer`; test token classes vs `/screenlog/*` | MED | ⏳ recommend |
| F7 | audit-log + rate-limit `/export` | MED | ⏳ recommend |
| F6 | sensitive-window capture denylist | MED | ⏳ recommend |
| F8 | optional at-rest encryption (vault key path) | LOW/MED | ⏳ recommend |
| F9 | fix "never uploaded" copy to "transmitted only to your own devices on view" | LOW | ⏳ recommend |
| — | re-audit when WireGuard mesh lands (overlay exposes all ports at L3) | — | ⏳ future |

**Bottom line:** the privacy-from-cloud story is solid and enforced. The real
risk is local/operator-side covert surveillance, and the two highest-leverage
moves are **(F1)** make recording impossible to run invisibly and **(F5)**
make the whole capability off-by-default + owner-opt-in. The panic button
(F2) and traversal fix (F3) are done now.
