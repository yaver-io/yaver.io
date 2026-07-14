# ADR: External relay watchdog — authenticated probe + bounded self-heal

Date: 2026-07-14
Status: Proposed (script landed; relay endpoint NOT yet built)

## Problem

The relay can fail in three ways, and today we can detect only one of them.

| Failure | Relay `/health` | Who can see it |
|---|---|---|
| Relay process crashed | fails | on-box watchdog (restarts it) |
| Relay box down / wedged / partitioned | **cannot answer** | **nobody** — the on-box watchdog is down too |
| **Zombie tunnel** — registered, forwarding nothing | **returns 200** | **nobody** |

The zombie case is not hypothetical. On 2026-07-14 an always-on Mac mini was
unreachable from the phone for over an hour while `/health` returned `200` the
entire time. The relay was healthy; the *tunnel through it* was dead. Both ends
believed it was fine: `OpenStreamSync` succeeds against a dead QUIC peer (the
stream is created locally, no round-trip), so the request path opens a stream and
then blocks forever on a response that never comes.

`relay/tunnel_liveness.go` now evicts zombies from the relay side. This ADR is
about the layer above: **an external observer that verifies the user-visible
promise — "a phone can reach my machine right now" — and can unstick the relay
when it cannot.**

## Why an anonymous probe cannot work (the mistake this ADR exists to prevent)

The first version of `scripts/relay-e2e-watchdog.sh` probed
`GET /d/<deviceId>/info` unauthenticated and treated `401` as "healthy — the
request crossed the tunnel."

**It was wrong, and it would have reported green through a total outage.** A
device id that does not exist returns the *same* `401`, with a byte-identical
body:

```
$ curl https://public.yaver.io/d/<real-device>/info
{"code":"Unauthorized","error":"relay password missing — sign in again to fetch it",…}
$ curl https://public.yaver.io/d/00000000-dead-beef-…/info
{"code":"Unauthorized","error":"relay password missing — sign in again to fetch it",…}
```

The relay rejects unauthenticated callers **at its own edge and never touches the
tunnel**. That is correct — it deliberately refuses to disclose which devices
exist, so an attacker cannot enumerate the fleet — and it means **an anonymous
prober is structurally incapable of testing delivery.**

Rule: *a health check that cannot fail is not a health check.* Any probe that
reports "healthy" without a signal that could have said "unhealthy" must be
deleted, not tuned.

## Threat model

The watchdog is a privileged actor: it can ask the relay to introspect its
tunnels and (bounded) to restart itself. So it is exactly what an attacker wants.

Non-negotiables:

1. **No new unauthenticated endpoint.** An attacker must not be able to probe,
   enumerate devices, or trigger any action. Today `/d/*` already fails closed;
   the new endpoint must too.
2. **No shared secret.** The relay password is a bearer credential that grants
   the whole data plane. A watchdog that holds it is a second copy of the crown
   jewels on a box whose only job is to run `curl` in a cron.
3. **Relay stores public keys only.** Compromising the relay must not yield a
   credential that can act *as* the watchdog. (Consistent with the asymmetric
   direction in `project_relay_security`.)
4. **Replay-proof.** A captured request must not be re-playable — the box sits on
   the public internet.
5. **Least privilege on self-heal.** "Restart the relay" must not be reachable as
   "run any command as root".

## Design

### 1. Watchdog identity: Ed25519, relay holds only the public key

```
watchdog box:  /etc/yaver/watchdog.ed25519        (private, 0600, never leaves)
relay:         config: watchdog_pubkeys = [ "<base64 ed25519 pub>", … ]
```

Rotation = add the new pubkey, deploy, remove the old one. No downtime, no
shared secret to leak, and a stolen relay config gives an attacker nothing but
public keys.

### 2. The probe: `POST /admin/selftest`

Request body (canonical JSON, exactly these fields, no whitespace):

```json
{"nonce":"<32 hex chars>","issuedAtMs":1752500000000}
```

Header:

```
X-Yaver-Watchdog-Sig: base64(ed25519_sign(privkey, rawBody))
```

Relay verification, in order — **fail closed at every step**:

1. Body ≤ 512 bytes. (Signature verification on attacker-sized input is a DoS.)
2. Signature verifies against **some** key in `watchdog_pubkeys`. If not → `403`.
3. `issuedAtMs` within **±60 s** of relay clock → else `401` (bounds replay window).
4. `nonce` unseen in the last 10 minutes (in-memory LRU) → else `401` (kills
   replay inside the window).
5. Rate limit: **6 req/min per pubkey**, shared with the existing abuse guard.

Only then does the relay act. Note the ordering: **cheap checks before expensive
ones**, so an unauthenticated flood costs a length check, not a curve operation.

Response `200`:

```json
{"ok":true,"tunnels":12,"delivering":11,"zombies":1,"evicted":1}
```

**Counts only — never device ids.** The watchdog does not need to know *which*
box is sick to know the relay is sick, and a compromised watchdog must not become
a fleet-enumeration oracle. (The relay logs the ids; that is where an operator
looks.)

The relay answers this by reusing `probeTunnel()` from `tunnel_liveness.go` — the
same delivery probe eviction already relies on. **No new liveness logic**, so the
watchdog and the relay can never disagree about what "delivering" means.

### 3. Self-heal, bounded

Two tiers, and the split matters:

**Tier 1 — the relay fixes itself (already built).** `watchTunnelLiveness` evicts
a non-delivering tunnel and closes the connection, which makes the agent's serve
loop return and redial. The watchdog does not need to ask.

**Tier 2 — the watchdog restarts a wedged relay.** Only when `/health` fails from
outside (process hung, or box wedged). This needs remote execution, and that is
the dangerous part, so it is deliberately *not* an HTTP endpoint:

```
# on the relay, ~/.ssh/authorized_keys — a FORCED-COMMAND key:
command="/usr/bin/systemctl restart yaver-relay",no-pty,no-agent-forwarding,\
no-port-forwarding,no-X11-forwarding,from="<watchdog-ip>" ssh-ed25519 AAAA…
```

That key **cannot open a shell**. It cannot forward a port, cannot pivot into
your LAN, and it works from one IP. Its total authority is "restart one unit" —
so if the watchdog box is fully compromised, the blast radius is *bouncing the
relay*, which is annoying, not fatal. Compare with putting the relay password or
an unrestricted SSH key there: an attacker gets the whole data plane or the box.

**Do not** add an HTTP `POST /admin/restart`. An HTTP restart endpoint is a
DoS button with authentication in front of it, and authentication is the thing
most likely to be misconfigured.

Escalation ladder:

```
/health fails from outside
  → restart via forced-command SSH (once)
  → re-probe after 10s
     → recovered: alert "auto-healed" (once per streak, so flapping stays visible)
     → still down: page a human. Do NOT restart-loop.
```

A watchdog that restarts forever hides a crashloop and turns a 5-minute outage
into a silent 5-hour one.

### 4. Where it runs

On the **existing Ubuntu Hetzner box** (the Talos machine), not on the relay and
not on a new box:

- **€0** — it already exists and is already paid for.
- It is *off* the relay, which is the entire point: it can report the failure the
  on-box watchdog cannot.
- It is *not* a second relay. **Two relays would not have prevented the outage we
  actually had** (a zombie tunnel dies identically on both, being the same host,
  same NAT, same path). Observability beats redundancy when the box isn't what
  is failing. See W0 in the relay handoff doc.

Systemd timer, every 2 minutes:

```ini
# /etc/systemd/system/yaver-relay-e2e-watchdog.timer
[Timer]
OnBootSec=90s
OnUnitActiveSec=2min
```

## Security properties this buys

| Attack | Outcome |
|---|---|
| Anonymous probe / fleet enumeration | `403`. No unauthenticated endpoint exists. |
| Replay a captured self-test | Rejected: nonce seen, or clock skew > 60 s. |
| Steal the relay's config | Public keys only — cannot forge a request. |
| Compromise the watchdog box | Can restart the relay. Cannot shell in, cannot pivot, cannot read traffic, cannot enumerate devices. |
| Flood `/admin/selftest` | Length check → rate limit, before any signature verification. |
| Relay wedges | Detected from outside, restarted once, human paged if it stays down. |

## Related finding — the QUIC relay leg encrypts but does not authenticate the relay

While auditing this I checked the question directly: *does the free relay carry
encrypted traffic between the mobile app, self-hosted, BYO, and managed boxes,
with a Yaver auth layer?* The honest answer is **yes on both counts, with one
real caveat that belongs in a security backlog, not hand-waved.**

**Encrypted: yes.** The agent↔relay leg is QUIC, which is TLS 1.3 by mandate —
a passive eavesdropper on the wire sees ciphertext only.

**Yaver auth layer: yes.** `/d/*` is not an open proxy. Every proxied request
needs `Authorization: Bearer` or `X-Relay-Password` (`relay/server.go:349`), and
device requests are signed and verified against the device's **public** key —
`verifyDeviceSig` (`relay/server.go:618`), relay holds only public material.
That is the asymmetric model from `project_relay_security`, and it is right.

**Caveat (real, file a ticket): the agent does not verify the RELAY's identity.**

```
desktop/agent/main.go:11069   InsecureSkipVerify: true   // agent → relay QUIC
relay/tunnel.go:87            InsecureSkipVerify: true   // yaver-relay CLI tunnel
```

The relay presents a self-signed cert generated fresh on every boot with an
ephemeral key (`relay/server.go:2253` — `ecdsa.GenerateKey`), and the agent
accepts **any** cert. So the encryption protects against a *passive* attacker but
not an *active* one: anyone on the path (DNS spoof, hostile hosting/ISP, BGP
hijack) can impersonate the relay, terminate the QUIC session, and read the
plaintext inside — task content, source, and the relay password on its way in.
The device signatures still stop the attacker from *forging* a request, but they
do not restore *confidentiality* of what flows through.

**Fix (cross-cutting — coordinate; `main.go` and `relay/server.go` are under
active edit by another session as of 2026-07-14):**

1. Relay **persists** its keypair instead of regenerating per boot.
2. Relay publishes its SPKI SHA-256 in `platformConfig` (delivered to agents over
   Convex's already-authenticated TLS — a trusted channel).
3. Agent pins: replace `InsecureSkipVerify: true` with a
   `VerifyPeerCertificate` that checks the presented cert's SPKI against the
   published pin. No behaviour change until a pin is published, so it can ship
   dark and activate per-relay.

This is defence-in-depth over the existing app-layer auth, not a replacement for
it. It is **not** in scope for the watchdog and must not be rushed into a dirty
file.

## What is NOT yet built

- `POST /admin/selftest` on the relay (`relay/server.go` — currently owned by
  another session; coordinate before editing).
- `watchdog_pubkeys` in relay config + the LRU nonce cache.
- The forced-command SSH key on the relay box.

`scripts/relay-e2e-watchdog.sh` already speaks this protocol and degrades
honestly: with no key it does relay-liveness only and **says so on stderr**
rather than implying it verified delivery. It treats `404` from `/admin/selftest`
as "endpoint not deployed yet", not as a failure.
