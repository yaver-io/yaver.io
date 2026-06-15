# Access Layer — implementation handoff (for the next agent)

**Where:** committed on **`main`** (F3, F2, F4, F5 + this doc). Local; push when ready for cloud agents.
**Design spec:** `../yaver-bet/YAVER_ACCESS_LAYER.md` (the *why* + the 6-feature plan).
**This doc:** the *what's done / what's next*, precise enough for Codex to continue without context.

> **Goal.** Make Yaver automate the long tail of sites that block bots/geo/KYC, by driving the
> 95% on a region-appropriate node and handing the human the irreducible 5% (login/2FA/captcha/
> KYC/payment) as a one-tap card. Six features F1–F6 (see spec). **F3, F2, F4, F5 implemented on
> `main`; F1 partially exists; F6 mostly exists. Remaining engineering = F1 + UI surfacing.**

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

## DONE — F4: vault-backed credential auto-resolve + capture
Wires the existing `VaultStore` (`vault.go`; `Get(project,name)→*VaultEntry`, `Set(entry)`) into
the `yaver_ask_user` handoff so the human supplies a credential ONCE.
**Changes (`desktop/agent/agent_question_http.go`):**
- `registerTaskQuestion`: if `kind=="secret"` && `vault_hint` set && `s.vaultStore.Get("",hint)`
  has a non-empty Value → respond immediately `{ok,answer:value,fromVault:true}` — **no human
  prompt, no SSE broadcast** (secret stays off neighbouring devices). Gated to kind=secret so
  only credential lookups auto-resolve, never a judgement question.
- `handleTaskAnswer`: peek the pending question's `{VaultHint,Kind}` via `registry.Pending(taskID)`
  BEFORE `Answer` (which deletes it); after a successful answer, if kind=secret + vault_hint →
  `vaultStore.Set(VaultEntry{Name:hint,Value:answer,Category:"custom"})`. So the first answer is
  remembered and every later run auto-resolves.
- Added `"strings"` import.
**Verified:** `go build ./...` OK.
**Composition:** F4 (credential) + F2 (cookies persist in the profile dir) + F3 (the one-time
human prompt) = "log in once, reused after." Acceptance MET for credentials: 2nd run with the
same vault_hint does not re-prompt. (Cookie/session reuse is F2's profile dir.)
**Not yet (optional polish):** a real "use stored value / remember?" toggle in the card (today it
auto-stores when the runner set vault_hint — the runner's vault_hint IS the consent signal); a
node-side inject path that types a secret WITHOUT returning it to the runner (current behaviour
returns it as the tool result, same as any kind=secret answer — no new exposure, but a stricter
mode could keep it node-only).

## DONE — F5: Policy Guard
The boundary that keeps remote-hands legitimate. `desktop/agent/access_policy.go`:
`EvaluateAccessPolicy(source, action, jurisdiction) → {decision: allow|warn|block, reason, category}`.
- Built-in rules: foreign sportsbooks (betfair/bet365/1xbet/pinnacle/…) BLOCK funding/betting from
  TR + US; regional books (superbet/mozzart/…) BLOCK funding from TR; TR state-licensed
  (misli/nesine/iddaa/…) ALLOW from TR. Read/observe/scrape = always allow (public data).
  login/signup in a blocked jurisdiction = warn. Unknown source = allow (no over-blocking).
- `~/.yaver/access-policy.json` (array of `{match,category,blocked_in,note}`) overrides/extends.
- MCP tool `access_policy_check {source, action, jurisdiction}` + handler (httpserver.go, before
  `browser_open`). Returns the decision. Agents MUST honor `block` and surface `warn`.
- Tested: `access_policy_test.go` (8 cases) + `go build ./...` OK.
**Use it:** call `access_policy_check` before any gated-source automation; on `block`, refuse
(e.g. don't place a bet from a jurisdiction where it's illegal); `data` actions always pass.

## TODO — remaining features (in priority order)

### F1 — Egress Fabric (partially exists)
**Why:** pick a `{region, residential|datacenter}` node per source automatically.
**What exists:** `browser_open`/`OpenSessionWithProfile` already take `proxy_url` (egress vantage);
the mesh has region-tagged nodes. **Build:** a node/proxy registry tagged by region+type, a
per-source egress policy, and auto-selection (so "Misli → TR-residential" is declarative, not
manual). Keep the existing policy note ("only egress the user owns; never to defeat a block").

### F6 — Task/async/mobile orchestration
Mostly exists (tasks + SSE + the F3 cards). Polish: a "waiting on you" view aggregating pending
handoffs across tasks.

### UI surfacing (cross-cutting, do alongside)
The agent-side F4/F5 are done but headless; the apps could surface them: a Vault "remember?"
toggle on the secret card (F4), and an `access_policy_check` red/amber/green **badge** per source
in the web/mobile UI (F5). Not required for the engine to work — polish.

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
