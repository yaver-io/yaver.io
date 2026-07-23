# Browser Lane — "blank / never loads" Deep Audit

**Date:** 2026-07-24
**Symptom:** the mobile Browser Reload lane opens and shows nothing. No error,
no failure panel, no recovery — indefinitely.
**Scope:** `mobile/app/(tabs)/apps.tsx` browser lane → `mobile/src/lib/quic.ts`
URL construction → agent `/dev/status` + `/dev/` proxy (`devserver.go`,
`devserver_http.go`).
**Method:** source trace only. Not reproduced on a device — the failure modes
below are derived from the code paths, and each is marked with what would
confirm it. Recent commits `b3c61fb6e`, `9ac4d4540`, `321f0985e` touched this
area and are noted as untested.

---

## 0. The finding in one paragraph

The browser lane's loading overlay has **exactly one exit**: a
`postMessage({t:'yaver-rendered'})` from injected JavaScript running inside a
successfully loaded page. There is no timeout, no deadline, and no
status-driven exit. Meanwhile the only paths that can show the *failure* panel
are `onError` and `onHttpError` **with `statusCode >= 500`**. Everything in
between — an empty URL, a 401 from the relay, a 403, a 404, a 200 that renders
something the heuristic doesn't recognise, or a page that becomes ready after
60 s — lands in a state where the overlay never lifts and the failure panel
never appears. That state is indistinguishable from "blank / never loads",
and it is not one bug: it is at least five distinct routes into the same dead
end, because the overlay is gated on *success being proven* rather than on
*failure being detected*.

The single most likely trigger, given the lane opens the WebView before it has
a URL, is **§2.1: the WebView is mounted with `uri: ""`**.

---

## 1. The path, end to end

```
user taps "Browser Reload"                       apps.tsx:~876
  └─ quicClient.startDevServer({web:true, workDir})      → agent POST /dev/start
  └─ setActionSheet(null); resetWebPreview()
  └─ setWebViewKey(k+1); setWebViewLoading(true); setShowWebView(true)   ← opens NOW

         (no devStatus fetch anywhere in this handler)

separate 3s poll                                  apps.tsx:551-586
  └─ getDevServerStatus()  → setDevStatus(isActiveDevServerStatus(s) ? s : null)

render                                            apps.tsx:1601
  └─ bundleUrl = devStatus ? getDevServerBundleUrl(devStatus.bundleUrl || "/dev/") : ""
                                                  apps.tsx:2521
  └─ <WebView source={{uri: bundleUrl}} … />
                                                  apps.tsx:2553
  └─ {!webPreviewContentLoaded && <View style={s.previewOverlay}>…</View>}
```

Two facts about that shape decide everything downstream:

1. **The WebView is opened before a URL exists.** The handler never awaits or
   triggers a status fetch; `devStatus` arrives only on the next tick of an
   independent 3-second poll.
2. **The overlay covers the WebView and is gated on proof of success.**
   `webPreviewContentLoaded` flips true only in `onMessage`, only for
   `{t:'yaver-rendered'}`, only from `injectedJavaScript` (apps.tsx:2537-2549).

---

## 2. Five routes to a permanent blank

### 2.1 The WebView mounts with an empty URI *(most likely trigger)*

`bundleUrl` is `""` whenever `devStatus` is null (apps.tsx:1601), and
`devStatus` is null:

- immediately after a Remote Box switch (`setDevStatus(null)`, apps.tsx:531);
- for up to 3 s after `startDevServer` resolves, because nothing fetches
  status in the open path;
- for as long as `/dev/status` reports neither `running` nor `building` —
  `isActiveDevServerStatus` requires one of those two
  (`mobile/src/lib/devServerState.ts:6`);
- whenever `getDevServerStatus()` returns null, which it does for **any**
  non-2xx and for any thrown error (`quic.ts:7282-7291`), and also when the
  client is not yet connected (`quic.ts:7283`).

`<WebView source={{uri: ""}}>` issues no request. Therefore:

- no `onHttpError` → no retry, no failure panel;
- no `onError` → `webPreviewRetryRef` never increments, so the 30-strike
  `setWebPreviewFailed(true)` at apps.tsx:507-511 is unreachable;
- no document → `injectedJavaScript` never signals → `webPreviewContentLoaded`
  stays false → **overlay renders forever, in its non-failed branch.**

The user sees the header, a loading bar, and nothing else. Permanently, if
`devStatus` never becomes active.

*Confirm by:* logging `bundleUrl` at mount, or observing whether the lane
recovers ~3 s in (transient) or never (status never goes active).

### 2.2 Relay auth failures are 401 — and 401 is ignored

`getDevServerBundleUrl` (quic.ts:7978-7992) appends `?token=` and `&__rp=`
because a WebView cannot set an `Authorization` header. Its own comment records
the failure this fixed:

> Without these, loading the browser-lane preview through relay fails with
> "Unauthorized: relay password missing — sign in again to fetch it" — the relay
> drops the unauthenticated request before it ever reaches the agent.

But the guard is conditional:

```js
if (this.activeRelayUrl && this.activeRelayPassword) url += `&__rp=…`;
```

If `activeRelayPassword` is empty at render time — not yet fetched, rotated, or
cleared — the URL ships without `__rp`, the relay answers **401**, and:

```js
onHttpError={(e) => { if (e.nativeEvent.statusCode >= 500) scheduleWebPreviewRetry(); }}
```

401 < 500. Nothing happens. No retry, no failure panel, overlay forever.

The same applies to **403** and **404**. Only 5xx is treated as a real failure,
even though every auth and routing failure in this stack is 4xx.

*Confirm by:* the relay's own logs, or temporarily logging
`e.nativeEvent.statusCode` in `onHttpError`.

### 2.3 The "still compiling" 503 is handled, but its success path is fragile

This one is *correct* and worth stating so it isn't broken while fixing the
others. The agent returns a structured 503 while the framework's dev server is
still binding (`devserver.go:679-685`):

```
HTTP 503, Retry-After: 2, X-Yaver-DevServer: starting
{"status":"starting","framework":"…","port":…,"message":"…"}
```

503 ≥ 500 → `scheduleWebPreviewRetry()` → up to 30 retries at 2.5 s ≈ 75 s,
then the failure panel. And the injected JS explicitly refuses to mark the page
as rendered when the body contains `"status":"starting"` (apps.tsx:2537), so
the JSON blob is never mistaken for the app.

The fragility: the retry budget (~75 s) is *shorter* than the agent's own
readiness budgets — `devserver.go` waits 120 s in one path and 180 s in another
before declaring "did not become ready". A cold Flutter/Expo web compile that
takes 90 s therefore exhausts the phone's retries and shows the failure panel
while the box is still legitimately compiling and about to succeed.

### 2.4 The paint heuristic gives up after 60 s and then can never recover

`injectedJavaScript` polls `ok()` every 500 ms, at most 120 times
(apps.tsx:2537) — 60 seconds — then `clearInterval` and stops. It is injected
per page load, so if the app's first meaningful paint happens at 61 s (a heavy
CanvasKit boot, a slow asset fetch, a late hydration), the signal is never sent
and `webPreviewContentLoaded` stays false **even though the app is on screen
behind the overlay**.

The user is then looking at an opaque overlay covering a working app, with no
way to dismiss it other than Reload.

### 2.5 A page that renders but doesn't match the heuristic

`ok()` accepts only: a `flutter-view` / `flt-glass-pane` / `flt-scene-host`
element, or `body.children.length > 1`, or non-empty `body.innerText`.

A React/Vite app that mounts into a single `<div id="root">` with a canvas or a
purely graphical first frame satisfies none of those: one child, no text. It
renders correctly and is judged "not yet loaded" → overlay forever.

---

## 3. Why all five look identical

The overlay is the last thing in the stack and it is **fail-open on failure
detection and fail-closed on success detection** — the wrong way round for a
progress UI:

| Condition | Overlay lifts? | Failure panel? | User sees |
|---|---|---|---|
| Rendered, heuristic matched | yes | — | the app |
| Empty URI | no | no | **blank forever** |
| 401 / 403 / 404 | no | no | **blank forever** |
| 5xx | no | after ~75 s | spinner → logs panel |
| Rendered at >60 s | no | no | **blank forever, app hidden behind** |
| Rendered, heuristic missed | no | no | **blank forever, app hidden behind** |

Three of the six outcomes are silent, indefinite, and indistinguishable from
each other and from a hung box. That is the actual defect: not any one route,
but that the UI cannot tell the user *which* of them is happening.

This is also a false green in the sense CLAUDE.md means: commit `9ac4d4540`
("replace the black screen with real startup steps, %, live logs + failure
logs") added the diagnostic panel, and the panel is genuinely good — but it is
reachable only from the 5xx path, so the failure modes that dominate in
practice (no URL, 401) still terminate in exactly the black screen that commit
set out to remove.

---

## 4. Fixes, in order

### F1 — Never mount the WebView without a URL *(P0, fixes §2.1)*

Do not gate the browser lane on the 3 s poll. In the open handler, after
`startDevServer` resolves, fetch status directly and set it, then open:

```js
const st = await quicClient.getDevServerStatus();
if (isActiveDevServerStatus(st)) setDevStatus(st);
setShowWebView(true);
```

And make the empty case impossible to render silently — if `bundleUrl` is `""`,
render an explicit "waiting for the dev server to report in" state, never a
`<WebView>`. An empty `uri` must be treated as a bug, not a value.

### F2 — Treat every non-2xx as a failure signal, not just 5xx *(P0, fixes §2.2)*

```js
onHttpError={(e) => {
  const code = e.nativeEvent.statusCode;
  if (code === 401 || code === 403) { setWebPreviewAuthFailed(code); return; } // terminal, distinct message
  if (code >= 400) scheduleWebPreviewRetry();
}}
```

401/403 deserve their own message naming the actual remedy ("the relay
credential is missing — reconnect"), because retrying will never fix them. This
is the "carry the *why* into the error text" rule: `errSecInternalComponent`
cost a session for exactly this reason.

### F3 — Add a wall-clock deadline to the overlay *(P0, fixes §2.4 and §2.5)*

The overlay must have a deadline independent of any success signal. After N
seconds with no `yaver-rendered`, stop covering the app: drop to a translucent
banner ("still waiting for first paint — tap to hide") rather than an opaque
panel. A rendered-but-unrecognised app then becomes usable instead of hidden.

This is the house rule about advisory work: the paint heuristic is *advisory*,
and advisory work must never sit in the critical path of the thing it
annotates.

### F4 — Align the retry budget with the agent's readiness budget *(P1, §2.3)*

The phone gives up at ~75 s; the agent's own timeouts are 120 s and 180 s.
Either raise the phone's budget above the agent's, or have the agent report its
remaining readiness budget so the phone can wait exactly as long as the box
intends to. Two independently-chosen timeouts on the two ends of one operation
is how a healthy build gets reported as a failure.

### F5 — Make the heuristic report, not just decide *(P1, §2.5)*

Have the injected script post *why* it thinks the page isn't ready
(`{t:'yaver-not-ready', reason:'single-child, no text'}`) on a slow cadence.
The overlay can then say what it is waiting for, and the reason string is the
thing that tells you which branch of `ok()` is wrong for a given stack.

### F6 — A probe that attempts the real operation

Per the standing incident rule, the check must attempt the operation rather
than inspect the inventory. A `doctor` probe for the browser lane should: start
the dev server, fetch `bundleUrl` **with the exact URL the WebView would use**
(including `token`/`__rp`), and assert a 2xx with a non-empty body. A probe
that merely confirms `/dev/status` says `running` is precisely the false green
that lets §2.2 through — the status endpoint is authenticated by header and
succeeds while the query-param-authenticated WebView URL 401s.

---

## 5. What I did not verify

- Nothing here was reproduced on a device; the routes are read from code.
- Whether `devStatus` in practice goes active for the web lane at all — if
  `/dev/status` never sets `running`/`building` for a `web:true` server, §2.1 is
  permanent rather than transient, and that is the first thing to check.
- Whether `activeRelayPassword` is populated at the moment the browser lane
  renders. §2.2 depends entirely on that timing.
- The `web: true` flag's handling inside `/dev/start` — I traced the request
  out of the phone and the proxy back, not the middle.

The fastest disambiguation is one line: log `bundleUrl` and
`e.nativeEvent.statusCode` at the WebView. Empty string → §2.1. 401 → §2.2.
200 with the overlay stuck → §2.4/§2.5.
