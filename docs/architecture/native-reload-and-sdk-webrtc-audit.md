# Native Reload, and Should the Feedback SDK Embed a WebRTC View?

Date: 2026-07-22
Status: audit + recommendation.
Question: a Kotlin-only / Swift-only app uses the Yaver Feedback SDK standalone.
The agent makes a code change. **How does the app reload?**

Companions: `yaver-preview-mode-and-self-development.md`,
`desktop/agent/workspace_preview_strategy.go`, `sdk/feedback/{kotlin,swift}/`.

---

## 1. The asymmetry nobody can engineer away

| Stack | Reload mechanism | Realistic |
|---|---|---|
| Web / Flutter web | HMR — module swap in a live runtime | ~200–600 ms |
| React Native | Metro HMR, or Hermes bundle swap | ~0.3–6 s |
| **Kotlin / Swift** | **recompile → relink → reinstall → relaunch** | **20–90 s** |

RN and web hot-reload because they run an **interpreter over swappable
bytecode**. Kotlin and Swift compile to a signed, linked binary; changing one
line changes the artifact, and iOS additionally requires codesigning and an
install. There is no in-process swap to perform.

**This is not a Yaver limitation.** Android Studio and Xcode have poured
enormous effort into it — Apply Changes, SwiftUI Previews, Injection — and all
of them either restrict what may change or run a *separate* preview process
rather than mutating the shipped app.

So the honest framing: **for native, "reload" is not a thing you make faster.
It is a thing you relocate.**

---

## 2. Where the loop actually is, per adoption path

The three paths from the SDK README have different answers, and conflating them
is why this looked confusing.

### Path 1 — SDK only, app on a real device

The user runs their own app on their own phone, SDK embedded, no Yaver app.

- **Feedback: works.** Shake → capture → POST `/feedback` → agent files it.
- **Reload: their existing toolchain.** Xcode / Android Studio, 20–90 s.
- **Yaver's role: capture, not reload.** The SDK never claims otherwise.

This is a coherent, complete product. A native dev who wants *"shake to file a
rich bug report into my agent's work queue"* gets exactly that. **We should not
apologise for it or bolt a runtime onto it.**

### Path 2 — SDK + remote runtime (simulator/Redroid on a Cloud Workspace)

The app runs in a **simulator on the box**, streamed to the phone over WebRTC.

- **Reload happens ON THE BOX**, where the build tools already are. The phone
  never reinstalls anything — it is receiving video.
- Rebuild is still 20–90 s, but it is *the box's* 20–90 s, running on 8c/16GB
  rather than contending with the developer's laptop.
- **Feedback: the phone's shake becomes a `shake` session command; the box
  injects a hardware shake (`simctl` / `adb sensor`); the SDK fires inside the
  simulator; the overlay streams back.**

**This is the native loop.** Not because reload got faster, but because the
whole edit-build-run cycle moved to a machine that can do it while the developer
watches from a phone.

### Path 3 — full Yaver

Path 2 plus the coding agent doing the edits.

---

## 3. The proposal: embed a WebRTC view *inside* the Feedback SDK

The idea: the SDK ships a view a native app can present, showing the remote
streamed version of itself. The dev sees changes without leaving their app.

### Arguments for

- One dependency instead of two for a native dev who wants the full loop.
- The SDK is already the integration point they accepted.
- Path 1 → Path 2 becomes a config change, not a new install.

### Arguments against — and they are heavier

**1. It destroys the SDK's best property: being tiny.**
Both native SDKs are currently **zero-dependency** — one `init`, one token, one
HTTP POST. That is deliberate: a feedback SDK is never worth a dependency
conflict in someone else's build. WebRTC is the opposite of tiny:
`libwebrtc` adds tens of MB, its own threading model, and a notorious history of
version conflicts with anything else doing media.

A native dev who wants *only* shake-to-report would pay all of that for a
feature they never open. That is precisely the lock-in shape the README rejects.

**2. It puts the viewer in the wrong place.**
`yaver-preview-mode-and-self-development.md` establishes: **the escape hatch
must live in a layer the previewed app cannot reach.** If the app embeds the
viewer, the previewed content and the escape are back in one process — the same
structure that makes Hermes unsafe for Yaver-on-Yaver. A wedged stream could
take the host app's UI thread with it.

**3. It duplicates the Yaver app.**
Yaver mobile already *is* a WebRTC viewer with a native exit affordance, session
management, device pairing and relay auth. Rebuilding a lesser copy inside every
third-party app is a large surface to maintain for a benefit — "one less app" —
that mostly matters to us, not to them.

**4. Watching your own app inside your own app is confusing.**
Two versions of the same UI on one screen, one live and one streamed, with no
visual distinction. The Yaver-on-Yaver analysis showed why layered identical
surfaces are a trap.

---

## 4. Recommendation

**Do not embed WebRTC in the Feedback SDK.**

Instead, keep three artifacts with clean boundaries:

| Artifact | Job | Weight |
|---|---|---|
| `yaver-feedback-{kotlin,swift}` | capture + submit | **zero deps** |
| Yaver mobile app | the WebRTC viewer, with a native exit | already exists |
| Yaver agent | build, run, stream | already exists |

And add the one thing that is genuinely missing — a **deep link** from the SDK
to the viewer:

```
yaver://session/<workspaceId>?project=<slug>
```

The SDK offers *"View live on your Cloud Workspace"* only when Yaver is
installed. If it is not, nothing appears and the SDK stays tiny. This gets the
proposal's real benefit — a path from Path 1 to Path 2 — at the cost of a URL,
and it keeps the viewer in the layer that owns the escape.

### If you still want an embedded view later

Ship it as a **separate optional package** (`yaver-preview-kotlin` /
`yaver-preview-swift`) that depends on the feedback SDK, never the reverse. Then
the tiny SDK stays tiny and the heavy dependency is opt-in. That ordering is the
whole decision, and it is much harder to reverse afterwards.

---

## 5. What actually makes the native loop feel fast

Since reload cannot be made fast, spend the effort where it changes the
experience:

1. **Rebuild on the box, not the laptop.** Path 2's real win. A `cpx42` doing a
   Gradle build while you watch from a phone beats a laptop fan.
2. **Warm the toolchain.** Gradle daemon and simulator already booted turns
   90 s into 25 s. This is a trial-image-style bake: pay it once at image build.
3. **Stream at low resolution, high frame rate.** A slow-*feeling* reload is
   usually the stream, not the build. A dev loop needs to see change, not read
   8-point type.
4. **Show the build phase.** "Compiling 41/78" is tolerable; a spinner for the
   same 30 s is not. The `provisionPhase` ladder already models this.
5. **Never silently rebuild.** If a change needs a full reinstall, say so — an
   unexplained 60 s gap reads as a hang.

---

## 6. Open questions

- **Is `simctl` available on a Cloud Workspace?** **No** — iOS simulators need
  macOS, and a Hetzner box is Linux. Swift Path 2 requires a **Mac host**, which
  the four-tier plan does not currently include. Android/Redroid Path 2 works on
  Hetzner today; **iOS Path 2 does not exist yet.** That gap should be stated in
  product copy rather than discovered by a Swift developer.
- **Does the deep link need the workspace to be awake?** If the box is parked,
  the link should trigger a wake with visible progress, not fail.
- **Rebuild latency on the box is unmeasured.** The 20–90 s figures above are
  from general experience, not from a Yaver workspace. Measure before promising.
