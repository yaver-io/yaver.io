# Web App preview — failure modes

Cookbook of every way the dashboard's **Web App** (Webview → Web App)
preview has gotten stuck while pulling a third-party Expo / React-Native-
Web project into the iframe. Each entry includes the symptom, the root
cause, the fix, and the version it landed in. Use this as the first
reference when "sfmg renders nothing" reports come in — most repeats
of the same failure resolve quickly when you can see the pattern.

---

## 1. 503 on iframe assets after agent restart

**Symptom:** `entry-…js` fetches return 503. Bundle was built earlier
and visible on disk, but the agent doesn't serve it.

**Root cause:** `handleServeWebBundle` returned 503 when
`s.devServerMgr` was nil — which happens on every fresh agent process
(restart, systemd auto-update, crash-restart). The bundle directory
under `<project>/.yaver-build-web/` was still there, but the
in-memory pointer was empty.

**Fix:**

- `desktop/agent/devserver.go`: `SetWebBundleInfo` persists to
  `~/.yaver/web-bundle-info.json`. `GetWebBundleInfo` rehydrates from
  that file when the in-memory struct is empty AND the BuildDir still
  exists.
- `desktop/agent/build_web.go`: `handleServeWebBundle` lazy-inits the
  manager via `ensureDevServerManager()` instead of returning 503.

**Shipped:** cli 1.99.80.

---

## 2. Tunnel envelope silently truncates large assets

**Symptom:** assets over ~8 MB get a corrupt response. Browser
sometimes shows "unexpected end of input" or the script just fails to
parse with no clear error. Often surfaces as `entry-…js` partially
loading.

**Root cause:** `relay/server.go` and `desktop/agent/main.go` both
buffered the JSON tunnel envelope with `io.ReadAll(io.LimitReader(stream, 10<<20))`
— 10 MB cap. Body bytes get base64-inflated by ~33 % when JSON-encoded,
so the effective raw cap was ~7 MB. Anything over that silently
truncated.

**Fix:** bumped to 64 MB at all three sites (relay request body, relay
response, agent tunnel-client). TODO in comments to switch to a
streaming protocol — the long-term fix is to not buffer the envelope
at all.

**Shipped:** cli 1.99.80, relay 0.1.17.

---

## 3. "Project is mobile-only" 400 on Start

**Symptom:** Clicking **Start sfmg** in the Web App tab pops a red
banner: "Project is mobile-only (Metro/RN); use Hot Reload + Yaver app
instead of Web Reload." iframe never loads.

**Root cause:** the agent's `/dev/start` handler reasonably rejects
`surface=web-reload` for mobile-framework projects (Metro can't render
in an iframe). But the dashboard wanted exactly that — to compile a
**static** web bundle, not start Metro.

**Fix:** caller-identity protocol.

- Web UI sends `caller: "web-ui"` in the `/dev/start` body and pre-flights
  Expo / RN frameworks → routes directly to the static-bundle build,
  skipping `/dev/start` entirely.
- Mobile clients send `caller: "mobile"` for symmetry.
- Agent (≥ 1.99.80) treats `caller=web-ui` + mobile-framework + `surface=web-reload`
  as a request for the static-bundle path and answers
  `{ ok: true, mode: "static-bundle", bundleUrl, bundleReady }` instead
  of 400.

**Shipped:** cli 1.99.80, web 1.1.91.

---

## 4. React error #527 — incompatible React versions inside the bundle

**Symptom:** iframe loads HTML and JS, then immediately crashes. Safari
console shows
`Error: Minified React error #527; visit https://react.dev/errors/527?args[]=19.1.0&args[]=19.2.5`.
White screen because the React tree fails to mount on the very first
render.

**Root cause:** the project's `node_modules/react/package.json` and
`node_modules/react-dom/package.json` had different versions —
`19.1.0` and `19.2.5` respectively. React's runtime requires they
match exactly.

This was sfmg's case: `package.json` pinned both at `19.1.0`, but a
transitive `peerDependencies` resolution under
`--legacy-peer-deps` hoisted `react-dom` up to `19.2.5`. The disk
versions disagreed even though `package.json` looked fine.

**Fix:**

```bash
cd <project>
npm install --legacy-peer-deps react@19.2.5 react-dom@19.2.5
```

Then rebuild the bundle.

**Prevention:** preflight integrity check landed in agent 1.99.85
(`desktop/agent/bundlecheck_web.go`). Runs before every
`expo export -p web` and refuses to start the build with a
fix-it command in the error when versions drift.

**Shipped:** cli 1.99.85 (preflight). Project-side fix: align
react/react-dom versions in `package.json`.

---

## 5. Relay body-rewriter doubles the `/dev/` prefix on `<base href>`

**Symptom:** iframe URL bar shows `/d/<id>/dev/web-bundle/` correctly,
but every relative asset request 503s. The agent's `/dev/web-bundle/`
handler never sees them.

**Root cause:** `web/app/d/[deviceId]/[[...path]]/route.ts`'s body-
rewriter prepended `/d/<id>/dev` to every absolute path it found in
HTML — including the agent's own `<base href="/dev/web-bundle/">`.
The rewrite produced `<base href="/d/<id>/dev/dev/web-bundle/">`
(note **dev/dev**). Browser then resolved `_expo/...js` against the
broken base to `/d/<id>/dev/dev/web-bundle/_expo/...js`, which fell
through to the agent's `/dev/` reverse-proxy catchall (no Metro
running) → 503.

**Fix:** path-aware rewrite. If the captured path already starts
with `dev/`, just prepend `/d/<id>/`. Otherwise prepend
`/d/<id>/dev/`. Same helper is used for HTML and CSS rewrites.

**Shipped:** web 1.1.94.

---

## 6. Trailing-slash 308 redirect strips the canonical URL

**Symptom:** iframe URL bar shows `/d/<id>/dev/web-bundle/?...` but
underneath the iframe loads "yaver web ui" or other unrelated content.
Reproduces only when the iframe URL ends with `/`.

**Root cause:** Cloudflare Workers' default Next.js behaviour 308-
redirects `/foo/` → `/foo`. Iframe followed the redirect, ending up at
`/d/<id>/dev/web-bundle?...` (no trailing slash). The relay's
`PATH_REBASE_SCRIPT` then rewrote `location.pathname` to `/web-bundle`
(no `/dev/`, no slash). The agent's downstream router-reset script
expected `/dev/web-bundle{,/}` and didn't match `/web-bundle`, so
expo-router landed on `/web-bundle` → no route → unmatched render.

**Fix:**

- `web/next.config.ts`: `skipTrailingSlashRedirect: true` — the
  trailing slash now stays end-to-end.
- `desktop/agent/build_web.go`: agent's `injectStaticBundleRouterReset`
  also catches `/web-bundle{,/}` for defense in depth.

**Shipped:** web 1.1.100, cli 1.99.86.

---

## 7. `Animated.timing` callback never fires inside an iframe

**Symptom:** App splash → name input renders. User types a name and
taps Next. Screen fades to black/green and stays there indefinitely.
No errors in console, no network activity.

**Root cause:** RN-Web's `Animated.timing(value, { useNativeDriver: false })`
schedules its frame callbacks via `requestAnimationFrame`. Browsers
**throttle rAF aggressively when the iframe is not the focused frame**
(or its parent is backgrounded). The fade-out's `onComplete` callback
never fires → the `setStep(nextStep)` inside it never runs → screen
stays at `stepFade=0`.

**Fix (project-side):** on web platform, skip the animated transition
and drive `setStep` synchronously.

```tsx
const transitionTo = (nextStep: Step) => {
  if (Platform.OS === "web") {
    setStep(nextStep);
    stepFade.setValue(1);
    return;
  }
  // native: keep the polished fade
  Animated.timing(stepFade, { toValue: 0, duration: 200, useNativeDriver: true })
    .start(() => {
      setStep(nextStep);
      Animated.timing(stepFade, { toValue: 1, duration: 300, useNativeDriver: true }).start();
    });
};
```

Apply the same gate to **every** `Animated.timing` site in the
project — sfmg's commit `bf2831f` did exactly that across all
animation call sites. If any call is missed, the same lock-up happens
the moment that animation runs.

**No agent fix possible** — this is a contract between the bundle and
the browser. The dashboard can't intervene in the bundle's animation
loop.

---

## 8. ConvexReactClient retry-storm against a placeholder URL

**Symptom:** App renders, but every `useQuery` hook stays in loading
state forever. DevTools network panel shows
`WebSocket connection to 'wss://offline.invalid/api/<v>/sync' failed`
on a backoff loop (~535 ms, then 1.7 s, 4 s, …).

**Root cause:** common offline-mode pattern in Expo apps:

```ts
const convexUrl = process.env.EXPO_PUBLIC_CONVEX_URL ?? "https://offline.invalid";
const convex = new ConvexReactClient(convexUrl, { ... });
```

When the env var is missing (typical on yaver-test-ephemeral, which
has no `.env`), the client constructs against `https://offline.invalid`
and **immediately** opens a WebSocket. DNS resolution fails forever,
ConvexReactClient retries forever, and any query subscribed to that
client stays pending.

**Fix (project-side):** lazy-construct only when there's a real URL,
and skip the provider entirely when there isn't.

```tsx
const convexUrl = process.env.EXPO_PUBLIC_CONVEX_URL ?? "";
const convex = convexUrl
  ? new ConvexReactClient(convexUrl, { ... })
  : (null as unknown as ConvexReactClient);

// later in JSX:
{hasRealConvex && convex ? (
  <ConvexAuthProvider client={convex} storage={...}>
    <AppStack />
  </ConvexAuthProvider>
) : convex ? (
  <ConvexProvider client={convex}><AppStack /></ConvexProvider>
) : (
  // No URL → no provider. useQuery throws on the spot
  // (audible failure beats silent infinite-spin).
  <AppStack />
)}
```

**No agent fix possible** — same contract as #7.

---

## 9. Iframe served the OLD bundle hash because of browser cache

**Symptom:** You rebuilt the bundle, fixed React versions, applied
patches — but the iframe still shows the same broken behaviour.
DevTools console still references the **old** bundle hash, not the
new one.

**Root cause:** Safari's HTTP cache is more aggressive than the
agent's `Cache-Control: no-cache, no-store, must-revalidate` headers
when the URL hasn't changed. The dashboard's `__preview_reload=N`
nonce is appended to the iframe's URL but the deferred `<script src>`
inside the bundle's HTML uses a static path. When that hash file is
cached, Safari serves it without revalidating.

**Fix:** **hard cache bypass**:

- Safari → Develop menu → **Empty Caches** (Cmd+Option+E), then Cmd+R.
- Or **Cmd+Option+R** for "ignore cache and hard reload" if the
  Develop menu is enabled.

(The fact that this is necessary at all is a UX bug in the dashboard
— a future fix should append a content-hash nonce to the iframe URL
on every bundle rebuild so Safari treats it as a different resource.
Tracked.)

---

## 10. `<base href>` was missing entirely

**Symptom:** iframe HTML loads. Asset paths like
`_expo/static/js/web/entry-…js` 404 because the browser resolves them
against the document URL (`/d/<id>/dev/web-bundle/`) but the relay's
PATH_REBASE_SCRIPT has already rewritten pathname to something else,
so the base for resolution is wrong.

**Root cause:** very early bundle builds didn't inject `<base href>`.

**Fix:** `desktop/agent/build_web.go::serveWebBundleHTML` now always
calls `injectBaseHref(data, "/dev/web-bundle/")` after stripping
absolute paths. The base href + the path-rebase scripts are the two
things that make the bundle render correctly through the relay
proxy.

**Shipped:** cli 1.99.74 era — settled long before this writing.

---

## 11. Iframe URL flips to `/dev/` during a rebuild

**Symptom:** While clicking **Rebuild**, the iframe momentarily
shows yaver's own dashboard (or whatever else `/dev/` proxies to)
instead of the previous bundle's content.

**Root cause:** the dashboard's URL priority chain only used
`/dev/web-bundle/` when `staticBundleState === "ready"`. During a
rebuild the state flipped to `"building"` and the chain fell back to
`previewUrl` (the dev-server proxy at `/dev/`). With no Metro
running, the agent's `/dev/` catchall returned its own dashboard or
"no dev server".

**Fix:** keep the iframe URL on `/dev/web-bundle/` when state is
either `"building"` OR `"ready"`. Brief 404 underneath during a
rebuild is fine; the bundlingState overlay covers it.

**Shipped:** web 1.1.99.

---

## Quick triage flowchart

```
iframe shows nothing / wrong content
├── HTTP 503 on entry-…js        → §1 (agent restart) or §2 (envelope cap)
├── HTTP 308 in network tab      → §6 (trailing slash)
├── React #527 in console        → §4 (react vs react-dom drift)
├── WS to offline.invalid        → §8 (Convex placeholder URL)
├── Console: stuck after fade    → §7 (Animated.timing rAF throttle)
├── Old bundle hash in console   → §9 (browser cache — hard refresh)
├── iframe URL says /dev/        → §11 (URL chain bug, fixed in 1.1.99)
├── 503 + path doubled           → §5 (relay /dev/dev/ rewrite)
└── 404 on assets, base missing  → §10 (very old agent — upgrade)
```

## Defence in depth — what we built

- **Preflight** (`bundlecheck_web.go`): refuses the build when
  intra-bundle dep coherence is wrong. Catches §4 family early.
- **Caller compat baseline**: web UI declares `expectReact` in the
  build request; agent rejects projects outside that range with a
  structured error. Stops the user from burning a 60 s `expo export`
  on a doomed build.
- **Persisted bundle info** (`web-bundle-info.json`): bundle survives
  agent restarts so §1 doesn't recur.
- **Path-aware proxy rewriter** + **router-reset script with
  defensive regex**: §5 + §6 don't recur even if Cloudflare's
  redirect behaviour changes again.

## Things still on the cleanup list

- Browser-cache nonce on the iframe URL so §9 doesn't need a manual
  cache wipe.
- Streaming tunnel protocol so §2 isn't a magic number — large bundles
  just stream through QUIC instead of buffering JSON envelopes.
- A health-probe in the dashboard that fetches `/dev/web-bundle/` +
  the entry hash, follows the redirect chain, and surfaces any of
  the §6 / §5 / §11 categories as a structured pre-render error
  instead of relying on the user to open DevTools.
