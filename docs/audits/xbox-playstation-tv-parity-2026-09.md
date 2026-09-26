# Xbox and PlayStation TV parity audit

Status: 2026-09-26  
Scope: carry Yaver's native tvOS runtime-control experience to Xbox and
PlayStation while preserving its auth, transport, task, failure, privacy, and
closed-loop verification contracts.

## Outcome

Xbox is technically actionable first through a dedicated x64 UWP app. Microsoft
still documents a retail Xbox lane for UWP **apps** through Partner Center,
with additional Xbox review. Yaver is not a game and should not be moved onto a
GDK/ID@Xbox plan unless Microsoft explicitly reclassifies it.

PlayStation is an external product-category gate. Sony publicly documents that
PlayStation Partner approval plus the GDPA unlocks official SDKs, publishing
resources, documentation, and support, but its public onboarding is framed for
games. Before any PlayStation source tree exists, an authorized representative
must obtain written confirmation that a non-game developer/runtime-control app
is an accepted title category.

No console can run the current Electron app or local Go agent. A console client
is a signed-in remote surface for a Yaver agent running on the user's own
computer or approved remote runtime.

## Baseline: what tvOS actually does

The current tvOS implementation is the reference because it contains the
widest lean-back contract:

- email/password and phone-approved device-code sign-in;
- installation-bound session storage, refresh, revoke, and account settings;
- direct-LAN-first agent access with HTTPS relay fallback and one bounded relay
  credential repair;
- account machine list, primary selection, shared-device labels, reachability,
  managed-box wake, and agent update requests;
- project discovery, runtime preparation, preview target selection, web preview
  frames, Android/React Native frame streams, and WebRTC runtime video;
- retained coding task conversations, short prompts, runner questions/choices,
  model/reasoning options, explicit Complete, and semantic task state;
- runtime control room, runner status and OAuth QR, voice readiness, reload,
  feedback reports, capture-card view, and Apple TV remote control;
- structured failure classification with named cause and route to fix;
- explicit no-surprise-render policy, last-good-frame retention, bounded
  networking, and controller-first focus/back behavior.

Android TV is not yet an equal baseline. It has auth, LAN/relay transport,
machine picker/wake, settings, update requests, dashboard, and task list/detail,
but task compose, live session, Vibing preview, device stream, and project
preview are still visible placeholders. Console work must not copy this partial
surface and call it parity.

## Capability matrix

| Capability | tvOS | Android TV | Xbox | PlayStation |
|---|---|---|---|---|
| Device-code and email sign-in | Ready | Ready | Portable | Portable after SDK access |
| Secure session + refresh/revoke | Ready | Ready | Adapter required | Adapter required |
| Machine list/select | Ready | Ready | Portable | Portable |
| Direct LAN HTTP | Ready | Ready | UWP networking probe required | SDK networking probe required |
| Relay HTTPS fallback/repair | Ready | Ready | Portable | Portable |
| UDP LAN approval beacon | Ready | Ready | Xbox API/support probe required | SDK probe required |
| Managed wake + narrated progress | Ready | Ready | Portable | Portable |
| Runtime/runner status | Ready | Partial | Portable | Portable |
| Runner OAuth QR | Ready | Partial | Portable | Portable |
| Tasks/list/detail/semantic output | Ready | Ready | Portable | Portable |
| Task compose/continue/answer | Ready | Placeholder/partial | Portable with controller editor | Portable with controller editor |
| `/model`, reasoning, `/exit` controls | Ready | Incomplete | Portable | Portable |
| Projects and target selection | Ready | Placeholder | Portable | Portable |
| Web preview frames | Ready | Placeholder | Native image/media path | SDK media path |
| WebRTC runtime stream/input | Ready | Incomplete | Feasibility spike required | SDK feasibility spike required |
| Reload/render queue contract | Ready | Incomplete | Portable | Portable |
| Feedback reports | Ready | Incomplete | Portable | Portable |
| Voice/dictation | tvOS system path | Android remote mic path | No Cortana dependency; text/phone fallback | SDK-dependent; text/phone fallback |
| Credential handoff | Continuity Camera | Not equal | Phone-approved encrypted handoff | Phone-approved encrypted handoff |
| Apple TV remote/capture | Apple-specific | Not core parity | Omit | Omit |
| Failure → named route to fix | Ready | Shared classifier | Required before UI | Required before UI |

“Portable” means the canonical agent/backend verb already exists. It does not
mean a console package or tested UI exists.

## Console product boundary

The shared product on Xbox and PlayStation is deliberately narrower than the
phone/web workstation UI:

1. Chat/tasks: read semantic conversation, send short prompts, answer explicit
   runner questions, choose typed runner controls, and close only with explicit
   confirmation.
2. Vibing: choose a project and supported render target, request one render,
   retain the last good frame, and show named stream failures with recovery.
3. Devices: choose/wake/heal a machine and see one truthful preferred route.
4. Settings: selected machine/runner/project, appearance, accessibility,
   account, and sign-out.

Dense source editing, raw terminal interaction, arbitrary shell access, local
build hosting, USB/device installation, vault-secret entry, provider console
automation, and deployment are out of scope. The console can request a typed,
permission-checked operation from an agent; it cannot become the agent.

## Xbox implementation decision

Use a separate native x64 UWP client:

- native XAML/C# shell for deterministic XY focus, controller input, lifecycle,
  secure local state, image/media rendering, and certification evidence;
- the existing Convex device-code/session endpoints and agent HTTP/relay
  contracts; no new console-only backend;
- a small platform adapter for storage, suspend/resume, network reachability,
  gamepad buttons, QR display, system text entry, and media/WebRTC support;
- shared JSON fixtures and protocol conformance tests, not copied business
  logic from Swift/Kotlin.

Microsoft documents hosted WebView as an Xbox application architecture, but
WebView2 is not supported on Xbox. Yaver's preview is untrusted user-project
output and must remain an image/video/runtime stream inside a native shell, not
the navigation shell itself. This also preserves the existing rule that a
third-party React Native app is never loaded through WebView.

Xbox-specific release gates:

- x64 UWP package and Business Developer Partner Center product;
- single-user application behavior verified across Xbox profile changes;
- visible focus at all times outside full-screen preview, deterministic A and B,
  no focus traps, and correct initial focus;
- important controls/text outside the TV unsafe outer five percent;
- suspend/resume, constrained memory, token expiry, network loss, controller
  disconnect, and app update tested on physical retail-equivalent hardware;
- no use of unsupported broad filesystem, print, OCR, graphics-capture,
  Bluetooth/Wi-Fi, device-picker, key-credential, or HID APIs;
- every transport leg time-bounded and every retry able to supersede a wedged
  attempt.

## PlayStation implementation gate

Before implementation:

1. Register through PlayStation Partners using approved company information.
2. Ask whether a remote developer/runtime-control application is eligible as a
   non-game title and obtain the applicable submission path in writing.
3. Sign the applicable agreements and obtain official SDK, test hardware,
   publishing rules, privacy rules, and certification requirements.
4. Keep confidential SDK knowledge and source in access-controlled partner
   infrastructure. Commit only public protocol/model work to this repository.

Until those steps complete, status is
`PARTNER_CATEGORY_APPROVAL_REQUIRED`. Browser reach, leaked SDKs,
reverse-engineered toolchains, consumer-console exploits, and sideload-only
demonstrations are not release strategies.

## Shared architecture and protocol work

```text
Convex identity/discovery/settings
             │
             ▼
Yaver agent canonical operations
  auth · machines · wake · tasks · runtime · preview · repair
             │
             ▼
console-neutral protocol fixtures and state machines
  auth/session · endpoint ladder · task conversation · render queue
  capability envelope · failure/recovery · focus/navigation semantics
      ├─ tvOS adapter
      ├─ Android TV adapter
      ├─ Xbox UWP adapter
      └─ PlayStation adapter (partner gate)
```

The first landed groundwork is surface identity parity: account appearance
settings now recognize `xbox` and `playstation` alongside the existing native
surfaces. This is intentionally not a deploy target and not an availability
claim.

Next shared code should define a versioned capability envelope returned by a
client and consumed by policy code:

- input: controller, text, voice, companion handoff;
- transport: direct HTTP, UDP discovery/approval, relay HTTPS, WebRTC;
- render: image frames, video codecs, data-channel input;
- tasks: create, continue, answer, typed controls, semantic/raw lanes;
- lifecycle: suspend, resume, user switch, background limits;
- repair routes: wake, relay repair, runner auth, install/update, reconnect.

Unknown capabilities remain visible as named unavailable states. They must not
silently disappear or spin forever.

## Delivery sequence

1. Close Android TV parity gaps first. Every shared model/test gained there is
   directly reusable by Xbox and exposes protocol drift before a new platform
   exists.
2. Extract fixture-driven task, render-queue, endpoint-ladder, and failure
   state-machine tests from tvOS. Run identical fixtures against Kotlin and the
   new C# client.
3. Build an Xbox spike proving device-code auth, one authenticated agent call,
   suspend/resume, controller focus, one preview frame, and relay fallback on a
   physical console. A launchable shell without these operations is not a
   milestone.
4. Implement Chat, Devices, Settings, then Vibing. Add each route only with a
   headless agent assertion and a physical-console pixel/focus arc.
5. Complete Partner Center account/product classification and certification
   evidence. Do not mark Xbox available before retail distribution is proven.
6. Start PlayStation only after category approval and SDK access; port the same
   fixtures and state machines through the confidential adapter.

## Cross-surface release assertions

- The same task state has the same meaning on phone, web, tvOS, Android TV,
  Xbox, and PlayStation.
- A render recommendation never reloads without user intent unless account
  auto-render is explicitly enabled; one request produces at most one render.
- Last good preview remains visible during refresh and transient failure.
- Every failure carries stable code, visible cause, and an invocable recovery
  route; no permanent spinner or success-on-no-op.
- Auth tokens never enter URLs, screenshots, logs, crash reports, or project
  content; relay authorization never replaces agent authorization.
- Console storage never contains task prompts/output beyond bounded local
  presentation state; Convex privacy boundaries remain unchanged.
- Store/reviewer evidence contains no private project, machine, customer,
  notification, token, IP, or relay data.
- Physical console evidence is required. Simulator, desktop UWP, and API tests
  answer different questions and cannot substitute for it.

## Primary public sources

- Microsoft, [Partner Center for Xbox](https://learn.microsoft.com/en-us/windows/uwp/apps-for-xbox/partner-center-xbox)
- Microsoft, [Xbox UWP FAQ](https://learn.microsoft.com/en-us/windows/uwp/xbox-apps/frequently-asked-questions)
- Microsoft, [Xbox media application architecture](https://learn.microsoft.com/en-us/windows/uwp/apps-for-xbox/application-architecture)
- Microsoft, [UWP features not supported on Xbox](https://learn.microsoft.com/en-us/uwp/extension-sdks/uwp-limitations-on-xbox)
- Microsoft, [Gamepad and remote interactions](https://learn.microsoft.com/en-us/windows/uwp/ui-input/gamepad-and-remote-interactions)
- Microsoft, [Designing for Xbox and TV](https://learn.microsoft.com/en-us/windows/apps/design/devices/designing-for-tv)
- Microsoft, [App package architectures](https://learn.microsoft.com/en-us/windows/uwp/packaging/device-architecture)
- Sony Interactive Entertainment, [Showing your game to PlayStation](https://sonyinteractive.com/en/news/blog/showing-your-game-to-playstation/)
- Sony Interactive Entertainment, [Complimentary PlayStation development hardware](https://sonyinteractive.com/en/news/blog/complimentary-development-hardware/)

