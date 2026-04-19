# Yaver Feedback SDKs

Visual bug reports + Black Box flight recorder, embedded in your app. Testers shake their phone (or click a floating button), narrate the bug, and the AI agent on your dev machine receives the report and fixes it.

The Feedback SDK ships across four runtimes — **React Native (mobile)**, **Flutter (mobile)**, **Web (browser)**, and **Python (server-side / scripts)**. They share one auth model, one Yaver backend, one wire protocol. Pick the package that matches your stack; the API shape is the same.

| Package | Runtime | Install | Version | Auth UX |
|---|---|---|---|---|
| `yaver-feedback-react-native` | React Native iOS + Android | `npm install yaver-feedback-react-native` | 0.6+ | Native Apple + in-app browser OAuth + email |
| `yaver-feedback-web` | Browser (any framework) | `npm install yaver-feedback-web` | 0.2+ | Popup OAuth + email |
| `yaver_feedback` (Flutter) | Flutter iOS + Android | `flutter pub add yaver_feedback` | 0.2+ | Native Apple + `flutter_web_auth_2` OAuth + email (via host bindings) |
| `yaver` (Python) | Server / CLI / scripts | `pip install yaver` | 0.3+ | `signin_via_browser()` (CLI-style local listener) or direct email/password |

> **Umbrella install:** `npm install -g yaver-cli` brings down the Yaver CLI binary. Run `yaver feedback setup` inside any project root and it will detect the stack and install the right package automatically.

## How auth works (one model across all SDKs)

Every Feedback SDK signs the user into Yaver — not into your app. The token belongs to a Yaver account that owns (or is invited to) a dev machine running `yaver serve`. When the SDK uploads a bug report, the agent uses that token to attribute the report and to scope what the user can see.

Five OAuth providers + email/password are supported on every platform that has a UI:

- **Apple** — Sign in with Apple (native sheet on iOS / popup on web)
- **Google** — Workspace + personal Gmail
- **Microsoft** — Office 365 + personal accounts
- **GitHub** — public + enterprise accounts
- **GitLab** — gitlab.com + self-hosted instances
- **Email + password** — for users without an OAuth identity

The transport differs per platform but the endpoints and tokens are the same:

| Platform | OAuth path | Apple path | Email path | Callback |
|---|---|---|---|---|
| React Native | `expo-web-browser` `openAuthSessionAsync` | `expo-apple-authentication` → `/auth/apple-native` | inline `loginWithEmail`/`signupWithEmail` → Convex | `yaver://oauth-callback` (intercepted in-session) |
| Web | `window.open` popup → `window.opener.postMessage` | Same popup | inline form | `https://yaver.io/auth/sdk-callback` |
| Flutter | `flutter_web_auth_2` (host-supplied closure) | `sign_in_with_apple` (host-supplied closure) | inline `loginWithEmail`/`signupWithEmail` | `yaver://oauth-callback` |
| Python | `signin_via_browser()` opens default browser → local HTTP listener on `127.0.0.1:19836` | n/a (browser path covers it) | `login_with_email(email, password)` | `http://127.0.0.1:19836/callback` |

**No device-code anywhere.** Pre-0.6 SDKs used a 6-character code that the user had to copy into a web page; that flow is removed because of UX issues and a 3-minute TTL bug. Every SDK now uses an in-app or in-process flow.

**2FA accounts** complete TOTP via the OAuth web callback (`web/app/auth/totp/page.tsx`) — the popup or in-app browser stays open until the second factor is verified, then returns the token. Email/password sign-in does NOT prompt for TOTP inline; users with 2FA enabled are asked to use OAuth instead.

**Apple App Store policy 4.8** does not require host apps to offer Sign in with Apple just because they bundle this SDK — the SDK is a developer/QA testing tool, not the host app's primary user authentication. We also offer Apple as one of the providers, so even the strictest reading of the rule is satisfied.

---

## React Native (`yaver-feedback-react-native`)

Mobile-only. Uses the same auth UX as the Yaver mobile app itself.

### Install

```bash
npm install yaver-feedback-react-native expo-web-browser
# Optional — enables native Apple Sign-In on iOS:
npm install expo-apple-authentication
```

For Android OAuth, register the callback in `AndroidManifest.xml`:

```xml
<intent-filter>
  <action android:name="android.intent.action.VIEW" />
  <category android:name="android.intent.category.DEFAULT" />
  <category android:name="android.intent.category.BROWSABLE" />
  <data android:scheme="yaver" android:host="oauth-callback" />
</intent-filter>
```

iOS needs nothing — `ASWebAuthenticationSession` intercepts the redirect inside the auth session.

### Quick start

```tsx
import { YaverFeedback, FeedbackModal } from 'yaver-feedback-react-native';

YaverFeedback.init({ trigger: 'shake' }); // no authToken — modal handles it

function App() {
  return (
    <>
      <YourApp />
      <FeedbackModal />
    </>
  );
}
```

The first time a tester shakes their phone, the SDK opens the embedded login screen (Apple / Google / GitHub / GitLab / Microsoft / email). After sign-in the token is saved in `AsyncStorage`; subsequent reports go straight through.

### Programmatic auth

```tsx
import { signInWithApple, signInWithOAuth, loginWithEmail } from 'yaver-feedback-react-native';

const { token } = await signInWithApple();             // native Apple sheet
const { token } = await signInWithOAuth('google');     // in-app browser
const { token } = await loginWithEmail(email, pass);  // direct API
```

---

## Web (`yaver-feedback-web`)

Browser-only. Vanilla DOM modal — no React/Vue/Svelte peer dep.

### Install

```bash
npm install yaver-feedback-web
```

No URL scheme registration needed — the popup auth callback (`https://yaver.io/auth/sdk-callback`) posts the token back to the opener via `postMessage`.

### Quick start

```ts
import { YaverFeedback } from 'yaver-feedback-web';

if (process.env.NODE_ENV === 'development') {
  YaverFeedback.init({ trigger: 'floating-button' });
}
```

The first time a user clicks the floating "Y" button (or presses the keyboard shortcut), a compact sign-in modal opens with the five OAuth providers and email/password. Each provider opens in a popup window that closes itself once authentication completes; the SDK persists the token in `localStorage`.

### Programmatic auth

```ts
import { signInWithOAuth, loginWithEmail, openLoginModal } from 'yaver-feedback-web';

const { token } = await signInWithOAuth('google'); // popup OAuth
const { token } = await loginWithEmail(email, pass);
const token = await openLoginModal();              // show the modal yourself
```

### Opt out of the modal

```ts
YaverFeedback.init({
  authToken: 'token-from-your-server',
  autoLogin: false,
});
```

---

## Flutter (`yaver_feedback`)

Mobile-first (iOS + Android). Native Apple + in-app browser OAuth via host-supplied closures so the SDK does not force `sign_in_with_apple` or `flutter_web_auth_2` on you.

### Install

```bash
flutter pub add yaver_feedback
flutter pub add flutter_web_auth_2     # for OAuth providers
flutter pub add sign_in_with_apple     # for native Apple Sign-In on iOS
```

For Android OAuth, register the callback in `AndroidManifest.xml`:

```xml
<intent-filter>
  <action android:name="android.intent.action.VIEW" />
  <category android:name="android.intent.category.DEFAULT" />
  <category android:name="android.intent.category.BROWSABLE" />
  <data android:scheme="yaver" android:host="oauth-callback" />
</intent-filter>
```

### Quick start

```dart
import 'package:yaver_feedback/yaver_feedback.dart';
import 'package:flutter_web_auth_2/flutter_web_auth_2.dart';
import 'package:sign_in_with_apple/sign_in_with_apple.dart';

final bindings = YaverLoginBindings(
  openAuthSession: (url) =>
      FlutterWebAuth2.authenticate(url: url, callbackUrlScheme: 'yaver'),
  requestAppleCredential: () async {
    final cred = await SignInWithApple.getAppleIDCredential(scopes: [
      AppleIDAuthorizationScopes.email,
      AppleIDAuthorizationScopes.fullName,
    ]);
    return (
      identityToken: cred.identityToken!,
      fullName: [cred.givenName, cred.familyName].whereType<String>().join(' '),
    );
  },
);

YaverLoginPage(
  bindings: bindings,
  onLoggedIn: (token) async { /* save & dismiss */ },
  onCancel: () => Navigator.of(context).pop(),
);
```

### Programmatic auth

```dart
final res = await loginWithEmail(email: email, password: pass);
final res = await signInWithApple(requestNativeCredential: bindings.requestAppleCredential!);
final res = await signInWithOAuth(
  provider: OAuthProvider.google,
  openAuthSession: bindings.openAuthSession!,
);
```

---

## Python (`yaver`)

Server-side / CLI / scripts. No UI — auth happens via direct credential exchange or a CLI-style browser handoff.

### Install

```bash
pip install yaver
```

### Three ways to sign in

```python
import yaver

# 1. Email + password — non-interactive, good for scripts/CI/cron
token = yaver.login_with_email("you@example.com", "your-password")

# 2. Sign up a new account — non-interactive
token = yaver.signup_with_email("Your Name", "you@example.com", "your-password")

# 3. Interactive OAuth — opens https://yaver.io/auth?client=cli in the
#    user's default browser, listens on http://127.0.0.1:19836/callback for
#    the issued token. Same flow as `yaver auth` on the CLI. Supports
#    Apple/Google/GitHub/GitLab/Microsoft.
token = yaver.signin_via_browser()
```

Tokens are long-lived (30 days). Cache them in your app's keystore or env var; a fresh sign-in is only required after expiry or sign-out.

### Use the client

```python
from yaver import YaverClient

client = YaverClient("http://localhost:18080", token)
task = client.create_task("Fix the login bug")
for chunk in client.stream_output(task["id"]):
    print(chunk, end="")
```

---

## Privacy

The Feedback SDK never sends bug reports, screenshots, voice recordings, or task data to Yaver's servers. All payloads stream peer-to-peer between the SDK and your dev machine's `yaver serve` process — the relay (when used) is a pass-through tunnel. Convex sees only auth identity + the device registry. See [`AI_ARCH.md`](../../AI_ARCH.md) and the "Privacy Contract" section of [`CLAUDE.md`](../../CLAUDE.md) for the full data-flow contract.

## Per-runtime READMEs

| Package | Detailed README |
|---|---|
| React Native | [`react-native/README.md`](react-native/README.md) |
| Web | [`web/README.md`](web/README.md) |
| Flutter | [`flutter/README.md`](flutter/README.md) |
| Python | [`../python/README.md`](../python/README.md) |
