# Phone Clone & Personal Agent Gateway — what it does, plainly

Yaver can do things for you inside apps that have no public API — check a balance,
read a price, place an order you'd otherwise tap through yourself — by driving a
**clone**: either a containerized Android instance (redroid) on a machine you
control, or a spare physical Android phone you own. The clone signs in to *your
own* accounts so the AI can act as you.

This page is the honest, plain-language version of what that involves. The legally
binding version is the [Privacy Policy](https://yaver.io/privacy). We keep the
disclosure here and in the policy — **not** as a pile of toggles cluttering the
app.

## The three properties that matter

- **Peer-to-peer.** The clone runs on your own device or your own machine. Your
  credentials, your sessions, and the data the gateway reads move *directly
  between your devices* over Yaver's P2P transport (direct LAN → Tailscale →
  self-hostable relay). They do not pass through our backend.

- **Open source.** The whole gateway — connector framework, the consent and audit
  layer, app provisioning, and the engines that drive the device — is in the
  public Yaver source. You can read exactly what it does and self-host every part.

- **Local-first.** Your installed-app list, your connector credentials (in the
  encrypted [vault](https://yaver.io/privacy)), and the audit ledger live on your
  devices. None of it goes to our backend (Convex) — and that boundary is enforced
  by automated tests in the repo, not just promised here.

## Everything sensitive is opt-in

Three capabilities are each a **separate, explicit grant**, off until you turn
them on, recorded locally, and revocable at any time:

| Capability | What it allows | Stored where |
|---|---|---|
| `share_app_inventory` | The Yaver app reports which apps are on your phone so the same set can be mirrored onto your clone. **App names only.** | Your devices, never our backend |
| `auto_relay_otp` | Your phone passes a one-time code to a sign-in that is waiting for it on your clone. | Relayed only — **never stored** |
| `read_device_sms` | A clone you own reads one-time codes from **its own SIM's** inbox (a dedicated number). | The clone you own |

You grant or revoke these conversationally (the AI asks once) or with one call —
there is no settings wall to wade through. The record of what you allowed, and
when, is written to a local audit ledger on your own device.

## What Yaver will **not** do

- **It does not bypass two-factor authentication.** When a sign-in needs a code or
  an approval, *you* still receive and approve it. Yaver only carries your answer
  to the clone. One-time codes are relayed and never persisted.
- **It does not defeat security controls.** No CAPTCHA solving, no device-attestation
  spoofing, no IP rotation, no bot-detection evasion. If a service blocks automated
  access, Yaver backs off and stops — a block is a "no", not a puzzle.
- **It only ever acts on your own accounts.** The gateway automates tasks *you*
  already do by hand, on services where *you* hold the account. It is never for
  reaching anyone else's data or systems.
- **It never reads another phone's messages.** `read_device_sms` reads only the
  inbox of a clone device you own and control.

## Why a "clone" instead of copying your phone

Yaver does **not** copy your phone's app data or sessions onto another device
(that's fragile and often breaks the service's own security). Instead the clone is
provisioned with the same *apps* and signs in *fresh* to your accounts. Cleaner,
and it keeps each device a legitimate, separately-authenticated session.

## Where this lives in the code

- Consent layer: `desktop/agent/gateway_consent.go`
- App-list bridge: `desktop/agent/gateway_phone_inventory.go`
- One-time-code relay: `desktop/agent/gateway_gate.go`
- Local audit ledger: `desktop/agent/gateway_audit.go` (never Convex)
- Privacy boundary tests: `desktop/agent/convex_privacy_test.go`
