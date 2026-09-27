# Microsoft Store certification notes — Yaver for Windows

Yaver's Microsoft Store package is a full-trust Electron desktop application
with a native x64 Go agent. It supports three truthful modes: client for an
authorized remote node, local agent on this PC, and client plus local agent.
The selected node runs coding tools against user-selected repositories and can
serve Browser, Hermes, or WebRTC rendering lanes when their prerequisites are
available.

Package facts are enforced by
`electron/store/assert-microsoft-store-package.ps1`:

- PC only, x64 AppX/MSIX-family package for `Windows.Desktop`.
- Native x64 Go agent included. No WSL executable/distribution, VHDX, service,
  driver, or scheduled task is bundled or created by default.
- Capabilities are limited to `internetClient`, `privateNetworkClientServer`,
  and the `runFullTrust` capability required by the Electron desktop entry point
  and its native child agent.
- Partner Center signs, hosts, installs, updates, and removes the accepted
  package. No third-party package URL or publisher certificate is used.

Consent and Windows behavior:

- The Store build does not expose Start-at-login and creates no scheduled task.
- Keep-awake, firewall repair, screen view, and remote input remain explicit
  user actions. Firewall repair is UAC-gated and program-scoped to Private and
  Domain networks; screen view and input control require local consent.
- Yaver does not install an NT service, enable Remote Desktop, silently enable
  WSL, or change the global Windows power plan.

Authentication/testability:

- Certification requires a working test account in Partner Center's private
  Notes for certification field. Never place those credentials in this file or
  the public repository.
- The dashboard and privacy policy must be live throughout certification.
- On a clean machine, sign in with the private synthetic account. Validate the
  local-node case with a synthetic local project, the client case with the
  preconfigured synthetic remote node, and the combined case by switching
  between them. The account includes Browser, Hermes, and WebRTC test projects.

Live generative AI:

- The listing and Partner Center declaration identify live generative AI. Yaver
  displays output from the coding runner selected by the user; it does not
  present that output as publisher-authored fact.
- Users can report inappropriate output to the publisher through
  the **Report inappropriate AI output** action in Settings, or through
  `https://yaver.io/terms#contact`. The synthetic certification account must
  be able to reach that action and support route.

Runtime/rendering boundary:

- Browser Lane requires a compatible project dev server and installed browser.
  Hermes Lane applies to compatible React Native/Expo projects. WebRTC desktop
  capture requires FFmpeg; mobile/emulator paths use their declared native
  encoders. Missing prerequisites are reported rather than silently installed.
- The Store package's local agent is available while the logged-in desktop app
  is running. It is not advertised as an always-on NT service.
