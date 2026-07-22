# Preview Mode, the Escape Hatch, and Developing Yaver With Yaver

Date: 2026-07-21
Status: design.
Scope: preview mode as a first-class state with a guaranteed exit — for
third-party apps **and for Yaver itself** — plus shake/feedback ownership and
the fast-reload latency budget.

Companions: `yaver-four-tier-deep-analysis.md` (§9 default path),
`yaver-activation-trial-analysis.md`,
`desktop/agent/workspace_preview_strategy.go`.

---

## 1. The trap, stated first

Yaver's mobile app is a **container**: it loads third-party RN apps in-process
via a Hermes bundle. The container owns the shake gesture, because shake is how
you get the "Reload / Back to Yaver" overlay. Guest apps cooperate — the RN
feedback SDK detects it is inside Yaver (`YaverInfo.isYaver`) and silently
suppresses its own shake handler so the two overlays cannot collide.

Now load **Yaver into Yaver**.

Both layers are the same code. Both believe they own shake. Both believe they
are the container. The suppression rule was written for *guest suppresses,
host wins* — and with two identical layers there is no principled way to decide
which is which from inside the process.

**The failure mode is not a glitch. It is being stuck**: a preview you cannot
exit, on a phone whose only escape gesture is being consumed by the thing you
are trying to escape. Force-quitting the app is the user's remaining option,
which is exactly the experience a dogfooding tool must never produce.

So the design rule:

> **The escape hatch must live in a layer the previewed app cannot reach.**

Everything below follows from that one sentence.

---

## 2. Preview Mode as an explicit state

Preview is a **mode**, not an ambient condition. It has an entry, a visible
indication, and a guaranteed exit.

```
                 ┌──────────────────────────────────────┐
   Yaver shell → │  PREVIEW MODE                        │ → Yaver shell
                 │  • banner: what is running, where    │
                 │  • EXIT affordance (always present)  │
                 │  • reload affordance                 │
                 └──────────────────────────────────────┘
```

**Non-negotiable properties:**

1. **The exit is always visible.** Not gesture-only. A gesture can be captured;
   a persistent affordance in the host's own chrome cannot.
2. **The exit is owned by the outermost layer**, never by the previewed app.
3. **The exit works when the preview is wedged** — infinite render loop, blocked
   JS thread, crash loop. If exiting requires the preview to respond, it is not
   an exit.
4. **Entering is explicit.** A user who did not ask for preview mode should
   never find themselves in it.

Property 3 is the one that gets designed away under time pressure, and it is the
only one that matters when things are actually broken.

---

## 3. Who owns the escape, per strategy

| Strategy | Previewed app runs | Escape owned by | Can the app capture it? |
|---|---|---|---|
| **chrome-webrtc** | on the box, in Chrome | phone's **native viewer chrome** | ❌ never — it only sends pixels |
| **hermes-bundle** (guest) | **in-process**, in the container | container's ShakeDetector + overlay | ⚠️ only if the guest ignores the suppression contract |
| **redroid-webrtc** | on the box, in Android | phone's native viewer chrome | ❌ never |
| **hermes-bundle (Yaver-in-Yaver)** | **in-process, same code** | 🔴 **ambiguous — this is the trap** | ✅ yes |

The pattern is stark: **whenever the preview is pixels, the escape is safe.
Whenever the preview is in-process, the escape depends on cooperation.**

Cooperation is fine for third-party guests — they link our SDK, and the SDK
suppresses itself. It is *not* fine when the guest is Yaver, because Yaver's own
shake handler is not a cooperating guest; it is the same code with the same
claim.

---

## 4. Therefore: develop Yaver with Yaver over WebRTC, not Hermes

**Yaver-on-Yaver uses `chrome-webrtc`.** Not because Hermes is worse in general —
it is Yaver's best path for third-party RN — but because it is *structurally
unsafe for this one case*.

```
  phone (outer Yaver)                    Cloud Workspace (Linux box)
  ┌────────────────────┐                 ┌───────────────────────────┐
  │ native viewer      │ ←── WebRTC ──── │ Chrome (headless)         │
  │  ┌──────────────┐  │     video       │   └ Yaver mobile RN web   │
  │  │ streamed     │  │                 │       target (Metro)      │
  │  │ pixels       │  │ ──── events ──→ │                           │
  │  └──────────────┘  │                 └───────────────────────────┘
  │  [EXIT] [RELOAD]   │ ← native chrome, OUTSIDE the stream
  └────────────────────┘
```

The inner Yaver renders into a browser on the box. It has no access to the
phone's gesture layer, cannot register a shake handler on the host, and cannot
draw over the exit button — **it is a video**. The recursion collapses because
the two layers are no longer in the same process.

Accepted limitations, stated rather than hidden:

- You are testing the **web target** of the RN app, not the native container.
  Native modules (Hermes loader, ShakeDetectingWindow, `YaverHTTPServer`) are
  not exercised. This is a UI/logic loop, not a native-integration test.
- Native container changes still need `yaver wire push` to a real device.

That is an honest scope, and it covers the large majority of day-to-day work:
screens, navigation, state, styling.

---

## 5. Shake ownership matrix

Shake means different things depending on who is listening, and the ambiguity is
the bug.

| Context | Shake does | Owner |
|---|---|---|
| Yaver shell, no preview | opens Yaver's own feedback | container |
| Guest RN app via Hermes | opens **container** overlay (Reload / Back to Yaver). Guest SDK suppressed via `YaverInfo.isYaver` | container |
| Guest app **standalone** (TestFlight/Play) | opens the **guest's own** feedback overlay | guest SDK |
| **WebRTC preview** (any strategy) | phone sends a `shake` **session command** to the box, which injects a synthetic event into the streamed surface; the app's own SDK fires **inside the stream** | box + inner app |
| **Yaver-on-Yaver over WebRTC** | same as above — the inner Yaver's own feedback opens **inside the stream**, the outer phone keeps its exit | box + inner Yaver |

The last row is the payoff: **both feedback loops work simultaneously and cannot
collide**, because one is pixels on the box and the other is native on the phone.

For native (Kotlin/Swift) apps there is no in-app SDK at all, so the viewer
pushes `launch-feedback` down the events DataChannel instead
(`remote_runtime.go`). Same channel, different trigger.

---

## 6. Fast reload — the latency budget

Reload speed decides whether this is a dev loop or a demo. The chain, per
strategy:

| Strategy | Chain | Realistic |
|---|---|---|
| **chrome-webrtc** | save → Metro HMR → browser repaint → next WebRTC frame | **~200–600 ms** |
| **hermes-bundle** | save → Metro rebuild → HBC compile → device pull → bridge reload | ~2–6 s |
| **redroid-webrtc** | save → Gradle/adb install → app restart | ~20–60 s |

**Chrome-webrtc is an order of magnitude faster than Hermes**, and that is the
second reason it is the right default for self-development — the recursion
safety being the first.

### 6.1 Where the milliseconds actually go

- **Metro HMR** on a 2c/4GB box: fine for a normal project. A large monorepo is
  the known ceiling, handled by detecting it and offering the class upgrade
  rather than pre-provisioning for it.
- **WebRTC frame latency** is the part people mis-attribute. A slow-feeling
  reload is usually the *stream*, not the rebuild. Keep the stream at a low
  resolution with a high frame rate: a dev loop needs to see *change*, not read
  8-point type.
- **Do not full-page-reload when HMR would do.** A full reload loses component
  state, which turns a 300 ms edit into a 10 s re-navigation to wherever you
  were. `/dev/reload` and `/dev/reload-app {mode}` already distinguish these;
  preview mode must default to the cheap one.

### 6.2 The rule

> **Preserve state by default; full-reload only on request or when HMR
> genuinely cannot apply.**

A dev loop that resets to the login screen on every save is not fast, however
quick the rebuild was.

---

## 7. Feedback SDK across every mode

The promise is "feedback works everywhere". It does — by three different
mechanisms, and conflating them is how it silently stops working.

| App | Transport | Package |
|---|---|---|
| RN / Expo | in-app SDK | `yaver-feedback-react-native` |
| Flutter | in-app SDK | `yaver_feedback` |
| Web | in-app SDK | `yaver-feedback-web` |
| Unity | in-app SDK | `yaver-feedback-unity` |
| **Kotlin / Swift native** | **viewer-triggered** over the WebRTC events channel | **none exists** |
| **Yaver itself** | its own RN SDK, firing inside the streamed browser | `yaver-feedback-react-native` |

`FeedbackSDKPackage()` returns `""` for Kotlin/Swift deliberately — inventing a
package name would send someone hunting for something that does not exist.

---

## 8. What to build

**P0 — the escape hatch**
1. `PreviewSession` state in the mobile shell: entered explicitly, exited from
   **native chrome**, never gesture-only.
2. Exit must work when the preview is wedged — it tears down the session
   locally rather than asking the preview to cooperate.
3. Persistent banner: what is running, on which workspace, and how to leave.

**P1 — Yaver-on-Yaver**
4. Route Yaver's own repo to `chrome-webrtc` explicitly. **Refuse
   `hermes-bundle` for it**, with a message naming the recursion — a silent
   refusal would look like a bug.
5. Self-development preset: workspace → Yaver repo → Metro web → Chrome →
   stream.

**P2 — fast reload**
6. Default to HMR; full reload only on request.
7. Surface reload latency in the preview banner. A number makes regressions
   visible; a vibe does not.

**P3 — shake plumbing**
8. In WebRTC preview, phone shake → `shake` session command → synthetic event
   injected into the streamed surface (the seam exists at
   `remote_runtime.go:1416`).
9. Outer phone shake **while in preview mode** must reach the *outer* Yaver's
   feedback, never the inner one. Two shakes, two destinations, disambiguated by
   who is listening — not by timing.

---

## 9. Open questions

- **Double-shake disambiguation.** If shake-in-preview goes to the inner app,
  how does the user give feedback about *Yaver's* preview UI itself? Proposal: a
  long-press on the exit affordance, because it is already in the layer the
  preview cannot reach.
- **Does headless Chrome + WebRTC actually hold up on 2c/4GB?** Unverified. The
  whole default-class decision rests on it, and this codebase has produced four
  cases in one session where the live check contradicted the code. Probe it
  before promising it.
- **Native-container changes** still need a real device. Worth stating in the
  product copy so self-development is not oversold as covering everything.
