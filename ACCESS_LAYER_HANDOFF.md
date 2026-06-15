# Access Layer — implementation handoff (for the next agent)

**Branch:** `access-layer-f3-handoff` (2 commits: F3, then F2). **Committed, NOT pushed.**
**Design spec:** `../yaver-bet/YAVER_ACCESS_LAYER.md` (the *why* + the 6-feature plan).
**This doc:** the *what's done / what's next*, precise enough to continue without context.

> **Goal.** Make Yaver automate the long tail of sites that block bots/geo/KYC, by driving the
> 95% on a region-appropriate node and handing the human the irreducible 5% (login/2FA/captcha/
> KYC/payment) as a one-tap card. Six features F1–F6 (see spec). **F3 and F2 are implemented.**

> **Boundary (do not cross).** This is for legitimate access to the user's own accounts, public
> data, and entitled services. **Do NOT build flows whose purpose is to evade a jurisdiction's
> law or a service's geo/ToS controls** (e.g. betting on a book that's illegal from the user's
> country). The Policy Guard (F5) enforces this. The `browser_open proxy_url` description already
> says "never to defeat a geo/IP block" — keep that framing.

---

## Module layout / build (READ FIRST — non-obvious)
- **Go agent module root is `desktop/agent/`** (its own `go.mod`), NOT `desktop/`. Build with:
  ```
  cd desktop/agent && go build ./...
  ```
- Web: `cd web && npx tsc --noEmit` (Next.js). Mobile: `cd mobile && npx tsc --noEmit` (Expo).
- Other Go modules: `mcp/`, `relay/`, `ci/oauth-mock/` (irrelevant here).

---

## DONE — F3: Human-in-the-loop handoff (screenshot + step on `yaver_ask_user`)
F3 **extends the existing ask/answer primitive** — do not rebuild it. The existing flow already
does: runner calls `yaver_ask_user` → MCP forwarder POSTs `/tasks/{id}/question` → daemon
registers + **blocks** the runner on the HTTP long-poll + broadcasts `agent_question` SSE →
mobile/web card → user answers `POST /tasks/{id}/answer` → long-poll returns → runner resumes.
TTL auto-expire + cancel-on-stop already handled. **F3 just adds two optional fields** that ride
the existing wire format end-to-end.

**Changes (all additive, backward-compatible):**
- `desktop/agent/agent_question.go` — `AgentQuestion` struct: added `Screenshot string json:"screenshot,omitempty"` (base64 PNG of the relevant page region) + `Step string json:"step,omitempty"` (login|two_factor|captcha|kyc_upload|payment_confirm|region_confirm|tap_relay).
- `desktop/agent/mcp_tools.go` — `yaver_ask_user` inputSchema: added `screenshot` + `step` properties (step has the enum).
- `desktop/agent/agent_question_forward.go` — `forwardYaverAskUser`: added `Screenshot`/`Step` to the args struct + to the POST body map.
- `desktop/agent/agent_question_http.go` — `registerTaskQuestion`: added `Screenshot`/`Step` to the decode struct + the `AgentQuestion{...}` it Registers. (SSE broadcast sends the full registered struct, so the fields reach the UI with **no new wire format**.)
- `mobile/app/(tabs)/tasks.tsx` — `agentQuestion` state type + the SSE-event cast type both gain `screenshot?`/`step?`; the card render (between the header chip and the prompt) shows an `<Image>` of the screenshot + a step chip. `Image` already imported.
- `mobile/src/lib/quic.ts` — `getPendingTaskQuestion` return type gains `screenshot?`/`step?`.
- `web/app/dashboard/page.tsx` — `agentQuestion` state type gains `screenshot?`/`step?`; the card render shows an `<img>` + step chip.
- `web/lib/agent-client.ts` — `getPendingTaskQuestion` return type gains `screenshot?`/`step?`.

**Verified:** `go build ./...` OK; `tsc --noEmit` clean for web (page.tsx, agent-client.ts) and mobile (tasks.tsx, quic.ts).

**How the agent USES it (no further code — it's usage):** during browser automation, call
`browser_screenshot` → take the returned base64 → call `yaver_ask_user` with
`{prompt, step:"two_factor", screenshot:"<base64>"}`. The card shows the page + the field; the
answer (e.g. OTP) returns as the tool result; the agent types it and continues.

**F3 protocol summary**
```
states: running → awaiting_human(handoff_id) → resumed | aborted(cancel) | paused(ttl)
HandoffRequest  (agent→app): {handoff_id,task_id,node,source,step,title,screenshot,fields[],options[],ttl}
HandoffResponse (app→agent): {handoff_id,action:submit|cancel,values:{...}}
```
(Today `fields[]` is the single `answer` of kind text/choice/secret — enough for OTP/approve/one
field. If a future step needs multi-field, extend the answer payload; not required yet.)

---

## DONE — F2: persistent-clearance browser sessions
Codebase already had the headful co-browse path with a persistent profile + anti-detection
(`browser_interactive.go::OpenInteractiveSession`) and helpers `findChromePath()` +
`profileDirFor(id)`→`~/.yaver/browser-profiles/<id>`. F2 brings the same to the **headless
automated** path and makes them **share** the profile dir.

**Changes:**
- `desktop/agent/browser.go` — new `OpenSessionWithProfile(id, headful, proxyURL, profileDir string)`; `OpenSessionWithProxy` now delegates to it with `profileDir=""`. Always adds `chromedp.Flag("disable-blink-features","AutomationControlled")` + a real desktop `chromedp.UserAgent(...)`. When `profileDir != ""` → `chromedp.UserDataDir(profileDir)` + `chromedp.ExecPath(findChromePath())`. Sets `BrowserSession.ProfileDir`.
- `desktop/agent/httpserver.go` — `browser_open` handler: new `profile` arg → resolve (abs path used as-is; bare name → `profileDirFor(name)`), `os.MkdirAll`, call `OpenSessionWithProfile`. ALSO aligned `browser_interactive_start` to resolve bare profile names via `profileDirFor` so **the same name = the same dir for both** (shared clearance).
- `desktop/agent/mcp_tools.go` — `browser_open` schema: added `profile` arg.

**Verified:** `go build ./...` OK.

**The composition (the whole point — F2+F3+F4):**
1. `browser_interactive_start {url, profile:"betfair"}` opens a **visible** window; the human
   solves the Cloudflare challenge / logs in (delivered via F3 handoff).
2. Clearance + cookies are written to `~/.yaver/browser-profiles/betfair`.
3. `browser_open {profile:"betfair"}` (headless) reuses that dir → rides the saved `cf_clearance`
   until it expires. Re-trigger the human only when it lapses.

**HONEST caveat — do not oversell.** F2 does NOT let headless beat Cloudflare *cold* (a fresh
profile still fails the JS challenge — confirmed: SofaScore/FotMob 403 even via headless Playwright).
F2's value is **reusing clearance a human passed once**. It only pays off with F3 (human solves)
and F4 (the profile/credentials persist + are managed).

---

## TODO — remaining features (in priority order)

### F4 — Credential & Session Vault (do this next)
**Why:** so the human logs in *once*; cookies/sessions/credentials persist encrypted and are reused.
**What exists:** the profile dirs (`~/.yaver/browser-profiles/<name>`) already persist cookies on
disk per F2. `yaver_ask_user` has `vault_hint` + kind=`secret`. Search the repo for an existing
secret store: `grep -rn "vault" desktop/agent/*.go` (there are vault hooks; wire them).
**Build:** (a) an encrypted store keyed by `{source, profile}` for credentials; (b) inject creds
node-side at login time (never return them to the model or put them in the SSE/card); (c) a
"sessions/credentials" management surface (revoke). Acceptance: a 2nd run of the same task on the
same profile does NOT re-prompt for login.

### F1 — Egress Fabric (partially exists)
**Why:** pick a `{region, residential|datacenter}` node per source automatically.
**What exists:** `browser_open`/`OpenSessionWithProfile` already take `proxy_url` (egress vantage);
the mesh has region-tagged nodes. **Build:** a node/proxy registry tagged by region+type, a
per-source egress policy, and auto-selection (so "Misli → TR-residential" is declarative, not
manual). Keep the existing policy note ("only egress the user owns; never to defeat a block").

### F5 — Policy Guard (partially exists)
**Why:** per-source × per-jurisdiction allow/deny; refuse illegal actions; steer to legitimate use.
**What exists:** the yaver-bet MCP has `source_policy_check`; `proxy_url` descriptions carry the
boundary note. **Build:** a policy table consulted BEFORE any task/handoff; red/amber/green badge
per source in the UI; hard-refuse jurisdiction-illegal actions (the example to block: placing bets
from a country where it's illegal — data-only is allowed).

### F6 — Task/async/mobile orchestration
Mostly exists (tasks + SSE + the F3 cards). Polish: a "waiting on you" view aggregating pending
handoffs across tasks.

---

## Gotchas / notes for the next agent
- Build the Go agent from **`desktop/agent/`**, not `desktop/`.
- `AgentQuestion` is the single wire format — adding a JSON-tagged field flows it to the UI via the
  SSE broadcast automatically; you only touch the decode structs that copy fields explicitly
  (`agent_question_http.go`, `agent_question_forward.go`) + the UI types.
- The runner **blocks** inside the `yaver_ask_user` tool call via an HTTP long-poll; there is no
  separate "pause" task state — the parked HTTP request IS the pause. Cancel path:
  `globalQuestionRegistry.CancelTask(id)` (called by `StopTask`).
- Browser screenshots come back as **base64 PNG** (`BrowserActionResult.ScreenshotB64`); feed that
  straight into `yaver_ask_user.screenshot`.
- There was pre-existing uncommitted WIP in this repo (`mcp_external.go`, `arm.tsx`, a DESIGN-*.md,
  a docs/ file) — those are NOT part of this branch's commits; leave them.
- First real test target for the F2+F3 loop: a Cloudflare-gated football stats/odds source
  (SofaScore / FotMob) — human solves once in co-browse, headless then collects. (Respect F5.)
