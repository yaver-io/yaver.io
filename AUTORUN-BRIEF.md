# Brief — what the user asked for (verbatim intent)

Captured 2026-07-19 from the requesting session. This file is **the user's
requirements**, recorded so codex can execute them on the remote via the yaver
MCP. Companion file `AUTORUN-HANDOFF.md` holds the verified technical findings,
blockers and exact commands — read both; where they disagree, HANDOFF has the
code evidence.

---

## 1. The thing to fix

> "tasks mobile app chat ui followup message is not seen etc"

Mobile app, Tasks screen, chat UI: **the follow-up message is not seen.** The
fuller original report was: *write something to create a task, it does something,
then write a new message again but can't see my message at all, then it shows a
new task.*

## 2. How it must be run

- Run it as an **autorun on the Mac mini**.
- Use **`/goal`** with **Claude Code**.
  (User's opening question was explicitly: *"first let me know whether you can
  run this as autorun with /goal with claude code in my mac mini or not"*.)
- **Always use a tmux session for autorun**, and **tell the user the tmux session
  name**.
- Run **this session's context** as the autorun task.
- Add it to the **Mac mini's autorun queue**.
- Codex on the remote will drive the autorun **via the yaver MCP**.

## 3. Runners to check

> "check with all claude code codex opencode etc"

Verify against **claude code, codex, and opencode**.
⚠️ See HANDOFF §0.2 — `--goal` is claude/glm-only, so codex and opencode can run
as autorun runners but cannot take a `/goal` loop.

## 4. Subscription, not API

> "no we need to use subscription path!!!!"

**Subscription path only.** Not API-usage billing. The mini currently reports
`API Usage Billing` — must be switched via `/login` → "Claude account with
subscription" before any loop starts.

## 5. Test surface

- Use the **simulator** on the mini for this.
- User also proposed the **Ubuntu 4 GB box as the remote client side**, or
  **Chrome there**.
  ⚠️ See HANDOFF §0.4 — the browser/RN-web route cannot reach signed-in screens
  (`expo-secure-store` is `export default {}` on web), so the simulator is the
  only viable surface for this specific bug. Flagged for the user to overrule if
  they meant something else by "remote client side".

## 6. Autorun visibility in the mobile app  (second deliverable)

> "if mac mini or any of remote device is running an autorun with a runner or
> multiple runners etc i should see this in my mobile app as autorun etc clearly"

> "i should see autorun as task in mobile app etc once i connected to that device
> as autorun and its tmux session info as label etc too"

> "always in autorun use tmux session and tell me the tmux session for autorun and
> show that in mobile app as well too the tmux session info in mobile tasks etc"

Requirements:
1. An autorun running on the mini **or any remote device** appears in the mobile
   app **as an autorun**, clearly, once connected to that device.
2. It appears **as a task** in the mobile Tasks screen.
3. The **tmux session info shows as a label** — in the autoruns view and on the
   task.
4. Works when the autorun uses **one runner or multiple runners**.

Add this to the Mac mini's autorun queue as well.

## 7. Do not disturb the live codex

> "dont touch session 1 codex does his job use yaver mcp to learn what he is doing
> etc and read it etc as well but dont inteferfere it"

- tmux session **`1` is live** (codex, gpt-5.5). **Do not touch it.**
- Sessions **`yaver` is a dummy**; `clauth` was offered as usable for claude.
- **Read** what codex is doing **through the yaver MCP** — observe, never
  interfere.
- User's earlier instruction: *"sync with that make developments etc once finished
  deploy with care."*

## 8. Ship it

> "commit push and then deploy to npm go agent and testflight etc from mac mini"

- **Commit and push.**
- Then deploy: **npm**, **Go agent**, and **TestFlight** — from the Mac mini.
- **"Deploy with care."**
  ⚠️ See HANDOFF §4 — TestFlight from the mini is currently blocked (no
  codesigning identity on that box, and disk under the deploy preflight floor).
  npm and the Go agent are fine from there.

## 9. Operational

> "can i close this pc if you started autorun"

Once the autorun is up in tmux on the mini, the laptop can be closed — the loop
lives on the mini. (Confirm the session name is reported before disconnecting.)

Context the user supplied along the way: Claude OAuth was done on the remote Mac
mini and keys were passed; `--dangerously-skip-permissions` inside tmux was the
requested way to bring a fresh claude up there.
