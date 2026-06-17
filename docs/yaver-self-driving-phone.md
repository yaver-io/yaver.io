# Self-driving phone — second-hand Android as a zero-friction clone

A second-hand Android running **only the Yaver app** (no Raspberry Pi, no USB-adb
host) can be a full Personal Agent Gateway clone — *if the app can drive its own
screen*. Android's **AccessibilityService** provides exactly that (tap, type, read
the on-screen node tree, take a screenshot). This is the lowest-friction real-phone
substrate: one settings toggle to enable the service, then it runs untouched.

It is the third `deviceDriver` implementation, alongside:

| Driver | Substrate | How it drives |
|---|---|---|
| `redroidDeviceDriver` | redroid container / adb-attached phone | adb (`droid_*`) |
| `redroidDeviceDriver{serial}` | a specific pinned clone | adb to that serial |
| **`localAccessibilityDriver`** | **a phone driving itself** | **on-device AccessibilityService over loopback** |

All three satisfy the same `deviceDriver` interface, so the auth broker, invoke,
act, and app-sync code run **unchanged** on any of them. A connector selects the
self-driving driver with `"device": "self"` in its manifest.

## Why this is the zero-friction winner

- **No cable, no second box.** The phone is the clone *and* the host.
- **Passes attestation** — it's a genuine certified device (covers redroid's gap).
- **Reads its own SMS** — real SIM (covers redroid's other gap), consent-gated.
- **Stays logged in** — a physical phone is its own persistent session, so
  `RestoreSnapshot` is a no-op success. This **sidesteps the golden-snapshot
  engine entirely** — log in once, then never again.

## Architecture (two processes on one phone)

```
┌──────────────── second-hand Android ────────────────┐
│  Yaver app (Kotlin)                                  │
│   └─ YaverA11yService : AccessibilityService         │
│        binds 127.0.0.1:18092  (loopback only)        │
│            ▲  loopback HTTP                           │
│  Go agent (libyaver.so serve, 127.0.0.1:18080)       │
│   └─ localAccessibilityDriver ──────────────────────►│
│        (gateway_local_driver.go)                     │
└──────────────────────────────────────────────────────┘
        ▲ relay / QUIC (remote dev drives the gateway)
```

The Go driver (built + tested: `desktop/agent/gateway_local_driver.go`) is the
client. The native service is the server. They talk over **loopback only** — the
control surface is never network-exposed.

## Loopback control protocol (what the service must implement)

Base: `http://127.0.0.1:18092`. JSON in/out; `{ok:true}` on success, `{ok:false,
error}` on a soft failure (e.g. a tap label not found).

| Method / path | Body | Returns | Service action |
|---|---|---|---|
| `POST /a11y/launch` | `{package}` | `{ok}` | `startActivity` the package's launch intent |
| `POST /a11y/launch-url` | `{url}` | `{ok}` | `ACTION_VIEW` intent for the URL/deep-link |
| `POST /a11y/type` | `{text}` | `{ok}` | `ACTION_SET_TEXT` on the focused node |
| `POST /a11y/tap` | `{label}` or `{x,y}` | `{ok}` | find node by text/desc → `getBoundsInScreen` → `dispatchGesture`; or tap coords |
| `GET /a11y/texts` | — | `{nodes:[{text,resourceId,contentDesc,bounds,clickable}]}` | walk `rootInActiveWindow` |
| `GET /a11y/frame` | — | `image/png` | `takeScreenshot` (API 30+) |
| `GET /a11y/sms/latest` | — | `{code}` | newest OTP from `content://sms/inbox` (see consent) |

The node fields map 1:1 onto the Go `uiNode` struct already used by the redroid
path, so self-heal + answer-schema matching work identically.

## Native pieces the app needs (implementation spec)

1. **`YaverA11yService extends AccessibilityService`** — implements the protocol
   above. Holds `rootInActiveWindow` for reads; uses `dispatchGesture` for taps
   and `performAction(ACTION_SET_TEXT)` for typing. Runs a minimal loopback HTTP
   server (reuse the app's existing GCDWebServer/NanoHTTPD-style embed) bound to
   `127.0.0.1:18092`.

2. **Manifest declaration** (`mobile/android/app/src/main/AndroidManifest.xml`):
   ```xml
   <service
       android:name=".YaverA11yService"
       android:permission="android.permission.BIND_ACCESSIBILITY_SERVICE"
       android:exported="false">
     <intent-filter>
       <action android:name="android.accessibilityservice.AccessibilityService" />
     </intent-filter>
     <meta-data
       android:name="android.accessibilityservice"
       android:resource="@xml/yaver_a11y_config" />
   </service>
   ```

3. **`res/xml/yaver_a11y_config.xml`** — `canPerformGestures="true"`,
   `canRetrieveWindowContent="true"`, `accessibilityFlags` incl.
   `flagRequestScreenshot` (API 30+).

4. **Enablement** — the user enables the service ONCE in Android Settings →
   Accessibility → Yaver. There is no way to enable it programmatically (by
   design); the app shows a one-time deep-link to that settings screen. After
   that it runs untouched.

## Security & policy (load-bearing)

- **Loopback only.** `18092` binds `127.0.0.1`; the agent on the same device is
  the only caller. It is never reachable off-device.
- **Drives only your own device, your own accounts.** The Accessibility capability
  controls *this* phone (a clone you own), to act on *your* logged-in apps.
- **Consent-gated SMS.** `/a11y/sms/latest` is gated by the `read_device_sms`
  opt-in (enforced agent-side in `localAccessibilityDriver.ReadSMS` *and* it
  should be re-checked natively). Ungranted ⇒ the agent never calls it.
- **No bypass.** The service relays *your* taps/codes; it does not solve CAPTCHAs,
  defeat attestation, or evade bot detection. Blocks are a "no" — the flow stops.
- **Disclosure lives in docs + the privacy policy**, not in app-UI clutter.

## Status

- Agent driver + broker wiring + tests: **built and green**
  (`gateway_local_driver.go`, `gateway_local_driver_test.go`, broker `"self"`
  selection in `gateway_broker.go`).
- Native `YaverA11yService` + manifest + config XML: **specified here, needs a
  device to implement + verify** (this Mac cannot build/run mobile).
