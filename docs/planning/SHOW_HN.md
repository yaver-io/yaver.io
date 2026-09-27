# Show HN draft — Relay Pro launch

Target window: Tuesday–Thursday, September 29–October 1, 2026, 8–10am ET.

## Title

**Show HN: Yaver – Control Claude Code or Codex from any screen, on your own box**

Alternates:

1. Show HN: Yaver – Open-source clients for coding agents running on your machine
2. Show HN: Yaver – Vibe from your phone or watch while Codex runs on your VPS

The first title states the product boundary: Yaver is the control and feedback
layer; the user's machine or VPS remains the execution environment.

## Body

Hi HN — I'm Kıvanç. Yaver is an open-source control and feedback layer for
terminal coding agents. Claude Code, Codex, OpenCode, or another runner keeps
working on a computer you control, while you can start a task, watch its live
console, review the result, reload a real-device preview, and send feedback from
the web, phone, tablet, TV, watch, car, or spatial clients.

The shortest loop is:

1. Install the CLI on your Mac, Windows/Linux box, or VPS.
2. Keep using the coding-agent login and repository already on that machine.
3. Pair whichever Yaver client surface is convenient.
4. Send a task, follow the live output, render it through the browser lane or
   Hermes native lane, and return screenshots/logs through the feedback SDK.

The clients are optional. Yaver can also be embedded as a library or driven
through MCP, so another app can use the same task, transport, render, and
feedback primitives without launching the first-party Yaver app.

The design constraint is that Yaver does not sell or host the coding computer.
Your source, shell, agent credentials, and builds remain on your own device or
VPS. On a LAN the clients connect directly. Remotely, you can self-host the Go
relay or use Relay Pro, a managed account-scoped pass-through lane on shared
infrastructure. Relay Pro is $9/month and includes all client surfaces, browser
lane, Hermes lane, MCP/library integration, and the feedback SDK. It does not
include compute, model access, or a dedicated server.

The interesting implementation details:

- Browser clients use the same authenticated HTTP/SSE endpoints as native
  clients instead of a second browser-only backend.
- React Native previews use Hermes bytecode and the native bridge rather than
  loading third-party apps in a WebView.
- The relay forwards ciphertext and does not hold device keys. Device access is
  still enforced by the user's own agent with public-key authentication.
- The hosted coordination plane stores identity and discovery metadata, not
  prompts, source files, command output, secrets, or absolute paths; repository
  tests enforce that boundary.
- The client and protocol pieces are reusable libraries, so Yaver's own UI is
  one consumer rather than a requirement.

The CLI, agent, relay, and backend are open source. The core uses
FSL-1.1-Apache-2.0 and converts to Apache-2.0 after two years; client SDKs are
Apache-2.0 from day one.

Repo: https://github.com/kivanccakmak/yaver.io

Install: `npm install -g yaver-cli && yaver auth`

I built this because coding agents made writing the change fast, but directing
them away from a desk and closing the loop on a real device was still awkward.
I would especially value feedback on the own-machine security model and which
non-desktop surface is genuinely useful rather than novelty.

## First comment

A few details that did not fit above:

- The free path is complete: LAN, your own private network, or your own relay.
  Relay Pro is convenience, not an unlock for the core product.
- “Private” describes the account-scoped authenticated lane, not a dedicated
  VM. Shared hosts are cryptographically tenant-isolated and capacity-limited.
- There is deliberately no hosted workspace in this release. Yaver coordinates
  tools that already run on your own machine or VPS.
- Yaver does not resell Claude, ChatGPT, or model tokens. It uses the runner and
  subscription already configured on your machine.
- Stack: Go agent and QUIC relay, React Native clients, Next.js dashboard,
  Convex for coordination metadata, plus MCP and embeddable client libraries.

Demo: [60–90 second video]

I can go deep on the transport boundary, Hermes loading, browser lane, relay
pool isolation, or why the watch/TV clients exist.

## Go / no-go checklist

- [ ] Fresh account completes install → auth → pair → first task on a machine
  that has never run Yaver.
- [ ] A task can be started and reviewed from the phone, and the live console
  names any failure with a route to its fix.
- [ ] Browser lane and Hermes lane each complete one real feedback loop.
- [ ] Remote test succeeds through Relay Pro; a second account cannot access
  the first account's agent or traffic.
- [ ] Free LAN/self-hosted relay remains fully usable without checkout.
- [ ] Production landing, pricing, docs, downloads, privacy, and terms return
  2xx and contain no hosted-workspace offer.
- [ ] Lemon Squeezy live store is approved; the only launch variant is the
  $9/month Relay Pro subscription; tax behavior is understood.
- [ ] Signed webhook rejects missing/bad signatures and handles create, renew,
  pause, payment failure, cancel-at-period-end, expiry, and full refund.
- [ ] One real low-value live purchase proves checkout → webhook → authenticated
  relay health → client delivery; cancel/refund then proves deprovision.
- [ ] Relay image is built from the release commit, signed/published through the
  release workflow, and production uses its immutable sha256 digest.
- [ ] Provider quotas, billing alerts, relay traffic alerts, support inbox, and
  status monitoring are watched during launch.
- [ ] README and landing page show the same promise, price, and limitations.
- [ ] Demo video is above the fold and the first comment is ready.
- [ ] Someone is available for support and HN replies for at least six hours.

If any payment, isolation, fresh-install, or remote-loop box is unchecked,
postpone the launch. Those are product claims, not optional polish.

## Launch-day metrics

Record visits → installs → authenticated machines → paired client surfaces →
first successful task → successful remote session → checkout started → paid →
cancel/refund/support request. Do not treat GitHub stars as activation.
