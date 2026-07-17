# Task: host-side guest access on tvOS and watchOS, and the leave gaps

Goal: finish the guest-access lifecycle on the native surfaces. The guest
half (leave / accept) landed in `d4ef0b2c2`. The **host** half — seeing who
you share with and cutting them off — exists nowhere except web, mobile and
the CLI. You should be able to remove a guest's access from the TV and the
watch, not just from a laptop.

**Docs drift. grep the code; when a doc disagrees with code, the doc is the
bug — fix it in the same change.**

## Ground truth (audited 2026-07-17 — do NOT re-litigate, do NOT redo)

- All four backend verbs already exist and are deployed to prod. Do not add,
  rename, or "improve" any Convex function or HTTP route in this task:
  - `POST /guests/invite` — host gives access (needs an email or public userId)
  - `POST /guests/accept-code` — guest accepts, body `{"code":"ABC123"}`
  - `POST /guests/revoke` — host removes a guest, body `{"email":…}` or `{"userId":…}`
  - `POST /guests/leave` — guest removes their OWN access, `{"hostUserId":…}`/`{"hostEmail":…}`
  - `GET /guests/list` — host's guests. `GET /guests/hosts` — `{pending, active}`.
- **Already done in `d4ef0b2c2` — do not rewrite:**
  - tvOS: `MachineRegistry.swift` decodes `isGuest/hostName/hostEmail/
    hostUserIdString/accessScope`; `postGuest` is the Convex-direct helper;
    `MachinePickerView` has the SHARED badge, a `.contextMenu` leave, and an
    `AcceptInviteView` sheet.
  - watchOS: `Backend.swift` has `GuestPendingInvite`/`GuestActiveHost`/
    `GuestHosts` + `enum GuestAccess { hosts, acceptCode, leave }`;
    `GuestAccessView.swift` lists pending + active; `SettingsView` links to it.
  - Electron: `leave-shared-access` IPC + SHARED badge + leave button.
  - MCP: `guest_leave`, `guest_accept`, `guest_invite`, `guest_revoke`.
- **`AgentClient.postJSON` is hardwired to the box's own address.** Guest calls
  must go to **Convex**, not to a box. On tvOS copy `MachineRegistry.postGuest`;
  on watchOS copy the request builder in `enum GuestAccess`. Both already set
  `Authorization: Bearer <token>` and `X-Yaver-Surface`.
- **The Convex site URL needs its region**: `https://perceptive-minnow-557.eu-west-1.convex.site`.
  Omitting `eu-west-1` 404s. Read it from the existing constant, don't retype it.
- **`listHosts` reports `hostUserId` as the Convex doc `_id`, NOT the public
  `users.userId`.** `guests.leave` resolves the public one, so leave keys on
  **`hostEmail`**. Three surfaces hit this independently. Same trap will bite
  you on revoke: `guests.revoke` takes the guest's **email** or **public
  userId** — `listGuests` returns both (`email`, `userId`); use them as given.
- **watchOS holds a token ONLY in standalone mode** (`@AppStorage("yaver.watch.token")`).
  In phone-paired mode there is no token; `GuestAccessView` is already gated on
  `!store.token.isEmpty`. Keep that gate. Do not invent a phone-relay auth path.
- **Wear OS auth cannot obtain a token at all** — `wear/.../Backend.kt` disagrees
  with the live contract on three axes (FormBody vs JSON; `user_code`/
  `session_token` vs `userCode`/`token`; `"approved"` vs `"authorized"`), two of
  them independently fatal. **Wear is OUT OF SCOPE. Do not touch `wear/`.**

## Hard constraints

- Never commit secrets, infra IPs, hostnames, or real emails. The repo is public.
  Use `example-host` / `someone@example.com` in comments.
- **Never `go test ./...` in `desktop/agent`** — `TestAuthLogout` hits the real
  `~/.yaver` and signs this box out. This task shouldn't touch Go at all.
- **Only `git commit -- <explicit paths>`.** Never `git add -A` / `git add .` /
  `git commit -a`. This checkout is shared with parallel sessions and a sweep has
  eaten other sessions' work repeatedly today.
- Stay inside the scope globs. Anything outside is a scope violation that kills
  the run:
  - `tvos/YaverTV/**`
  - `watch/YaverWatch/**`
  - `desktop/app/src/**`
- Do NOT touch `backend/convex/**`, `mobile/**`, `web/**`, `desktop/agent/**`,
  `wear/**`. Other loops own those and the guest work there is already done.
- Do not add dependencies.
- Match each file's existing style and comment density. These are small native
  codebases with a house voice — read a neighbouring view before writing one.

## Work

### 1. tvOS — remove a guest's access

`GET /guests/list` → the host's guests (`email`, `userId`, `fullName`, `status`,
`createdAt`, `acceptedAt`, `revokedAt`). Add to `MachineRegistry`:
- `listGuests(token:)` and `revokeGuest(email:userId:token:)` (`POST /guests/revoke`).
  Reuse `postGuest`; add a GET sibling if needed.

UI: a "Shared with" screen reachable from the dashboard (mirror how
`AcceptInviteView` is presented). List accepted guests; each row offers
"Remove access" behind a `.confirmationDialog`. **No typing** — that's the whole
reason revoke belongs on a TV and invite does not.

Copy must be honest: revoking cuts that person off every machine you share, and
they'd need a fresh invitation to come back.

### 2. watchOS — remove a guest's access

Same, in `GuestAccessView` (or a sibling view if that file gets crowded). Add
`GuestAccess.guests(token:)` + `GuestAccess.revoke(email:userId:token:)`. Reuse
the existing request builder and its `{error:"…"}` unwrapping. Keep the
`!store.token.isEmpty` gate. Confirm via the existing `ConfirmView`.

Show the guest's name and email; a wrist is a bad place to remove the wrong
person.

### 3. Electron — an offline host can't be left

`desktop/app/src/renderer/index.html` `refreshDevices()` filters
`d => d.isOnline` before rendering. If every machine a host shared is offline,
no card renders and there is no way to leave that host — even though
`/guests/leave` doesn't need the box online at all. Fix so shared devices remain
leavable when offline. Do not change the filter for owned devices; render offline
shared boxes in a visibly offline state rather than silently dropping them.

### 4. Invite is deliberately NOT in this task

`guests.invite` requires an email or a public userId — there is no email-less
"open invite code" in the backend. Typing an email on a TV remote or a watch is
hostile, and inventing a new backend verb is out of scope. Both surfaces already
reach invite by **voice** through MCP `guest_invite`. If you believe an
email-less invite is right, say so in the handoff; do not build it here.

## Out of scope

- `wear/` (auth is broken — see Ground truth).
- Any Convex/backend change, any new HTTP route.
- Deploys of any kind. Do not run `convex deploy`, `deploy-web.sh`,
  TestFlight, or `gh workflow run`.
- Rewriting the leave/accept code that already landed.

## Definition of done

Say DONE, alone, only when:

- tvOS can list the host's guests and revoke one, confirmed, with no typing.
- watchOS can do the same, still gated on a real standalone token.
- An Electron user can leave a host whose machines are all offline.
- The gate passes and it is all in the git log.
- Every new call goes to Convex (not a box), carries the Bearer token, and
  surfaces the backend's `{error:…}` text rather than a bare status code.
</content>
</invoke>
