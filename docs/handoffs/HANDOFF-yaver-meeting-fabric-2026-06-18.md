# Handoff: Yaver Meeting Fabric

Date: 2026-06-18

This handoff is for continuing the Yaver online meeting/call work in OpenCode/GML.

## Goal

Yaver needs two meeting capabilities:

1. **Yaver-native calls**: Yaver creates a meeting room; guests join from a browser/mobile link with audio/video. Later add screen share and PSTN dial-in.
2. **External meeting bridge**: Yaver can wrap Zoom, Google Meet, and Microsoft Teams using provider adapters:
   - official media APIs where available and authorized,
   - remote-browser/remote-PC bridge as universal fallback,
   - PSTN audio bridge for users with no internet.

The important architecture decision: **Yaver owns the meeting fabric; Zoom/Meet/Teams are adapters.**

## Files Touched

Meeting fabric backend:

- `desktop/agent/meeting_fabric.go`
- `desktop/agent/meeting_fabric_test.go`
- `desktop/agent/httpserver.go`
- `desktop/agent/mcp_tools.go`
- `desktop/agent/meetings.go`

Mobile surface:

- `mobile/src/lib/quic.ts`
- `mobile/app/(tabs)/solostack.tsx`

## What Exists Now

### New Meeting Room Model

`desktop/agent/meeting_fabric.go` adds:

- `MeetingRoom`
- `MeetingMediaConfig`
- `MeetingPSTNConfig`
- `MeetingParticipantToken`
- `MeetingMediaJoin`
- `MeetingAdapterCapability`
- `MeetingRoomCreateRequest`

Rooms persist locally in:

```text
~/.yaver/meeting_rooms.json
```

This is intentional: keep meeting state local-first, consistent with the repo privacy model. Do not put meeting tokens/media state in Convex unless a future design explicitly narrows what is allowed.

### Provider Capability Matrix

`meetingAdapterCapabilities()` exposes four providers:

- `yaver-native`
- `zoom`
- `google-meet`
- `microsoft-teams`

Adapter modes:

- `native-sfu`
- `official-media-api`
- `remote-browser`
- `link-only`
- `pstn-audio-bridge`

The current capability matrix is deliberately honest:

- Zoom realtime media requires RTMS/app setup.
- Google Meet realtime media requires Workspace Meet Media API/admin setup.
- Teams realtime media requires Graph Cloud Communications/application-hosted media and tenant permissions.
- External providers fall back to `remote-browser`.

### HTTP Routes

Wired in `desktop/agent/httpserver.go`:

```text
GET  /meeting-rooms                 owner auth
POST /meeting-rooms                 owner auth
GET  /meeting-rooms/capabilities    owner auth
GET  /call/<slug>                   public room page
POST /call/<slug>/join              public guest join token mint
```

Existing scheduling routes remain:

```text
GET/POST /meetings                  owner auth
GET/POST /meet/<slug>               public booking page
GET      /bookings                  owner auth
```

### MCP Tools

Added in `desktop/agent/mcp_tools.go` and dispatched in `httpserver.go`:

```text
meeting_room_create
meeting_room_list
meeting_capabilities
```

Existing `meeting_create` was extended to accept:

```text
provider: google | o365 | yaver | yaver-native
hosting:  meet | teams | yaver | none
```

### LiveKit-Compatible Native Join

`/call/<slug>/join` returns a `media` payload.

If these env vars are set:

```text
YAVER_LIVEKIT_URL
YAVER_LIVEKIT_API_KEY
YAVER_LIVEKIT_API_SECRET
```

then Yaver-native rooms are marked `media.status = "ready"` and join returns a signed HS256 LiveKit-compatible JWT:

```json
{
  "media": {
    "provider": "livekit-compatible",
    "url": "wss://...",
    "room": "...",
    "token": "...",
    "status": "ready"
  }
}
```

If LiveKit env is not configured, the same room exists but returns `needs-media-server` with setup requirements.

### Booking Integration

`desktop/agent/meetings.go` now supports Yaver-native booking links.

If an event type has:

```json
{
  "provider": "yaver-native",
  "hosting": "yaver"
}
```

then booking a slot creates a Yaver call room and stores its `/call/<slug>` URL as the booking join URL.

Also fixed an existing deadlock bug pattern:

- old code did `meetMu.Lock()` then `loadMeetings()` / `loadBookings()`;
- those loaders also lock `meetMu`;
- now it snapshots first, then locks for assignment/save.

### Mobile Surface

`mobile/src/lib/quic.ts` adds:

```ts
meetingRooms()
meetingRoomCreate(...)
meetingCapabilities()
```

`mobile/app/(tabs)/solostack.tsx` now shows:

- Yaver call rooms,
- adapter capabilities,
- media/PSTN setup status,
- a quick `+ New Yaver call` button.

## Tests Added

`desktop/agent/meeting_fabric_test.go` covers:

- default Yaver-native room creation,
- duplicate slug rejection,
- provider capability matrix,
- public `/call/<slug>/join` token mint,
- LiveKit-compatible JWT return when env vars are configured.

Known passing commands from this handoff:

```bash
cd desktop/agent
go test . -run 'Test(CreateMeetingRoom|MeetingCapabilities|HandleCallJoin)'

cd ../../mobile
npx tsc --noEmit --pretty false
```

## Current Blocker In Worktree

The broader Go package check is blocked by an unrelated untracked file:

```text
desktop/agent/wifi_mesh.go:429:6: nonEmptyLines redeclared in this block
desktop/agent/precheck.go:877:6: other declaration of nonEmptyLines
```

This is not from the meeting work. Do not “fix” it by reverting user changes. If you need full `go test .`, rename the helper in the WiFi mesh work or coordinate with whoever owns that change.

Also note: `httpserver.go` already had unrelated WiFi console changes in this worktree. Do not revert them.

## Next Step 1: Real Web Client Join

The current `/call/<slug>` page only mints a participant token and alerts the media status.

Next implementation should make it actually join the LiveKit room in browser:

1. Add a browser client bundle path or static JS loaded by `/call/<slug>`.
2. On form submit, call `POST /call/<slug>/join`.
3. If `media.status === "ready"` and `media.provider === "livekit-compatible"`:
   - connect with LiveKit JS client,
   - publish mic/camera,
   - render remote tracks.
4. If not ready:
   - show setup status from `room.media.setupRequired`.

Keep the page lightweight. Do not add a landing page.

## Next Step 2: Mobile Native Join

The mobile UI can create/list rooms, but it does not join media yet.

Next:

1. Add a room detail/join screen.
2. Use `meetingRoomCreate` or selected room.
3. Call `/call/<slug>/join` through a mobile client method.
4. For LiveKit, add the React Native LiveKit client only if repo patterns allow it; otherwise use a WebView first for proof.

Before adding dependencies, inspect existing mobile package state and design conventions.

## Next Step 3: Owner/Guest Scoping

Current public join is intentionally open if `room.AllowGuests` is true.

Before production:

- Add optional lobby enforcement for `RequireLobby`.
- Add room-level guest TTL policy.
- Add participant revocation or token cleanup.
- Decide if public join should require a signed invite token for some room types.
- Do not put raw tokens into Convex.

## Next Step 4: PSTN Audio Bridge

The model exists (`MeetingPSTNConfig`) but no SIP bridge exists yet.

Recommended path:

1. Add provider config env/vault for SIP/PSTN.
2. Start with Twilio or generic SIP.
3. PSTN user dials in, enters PIN.
4. Bridge PSTN audio into native SFU room.
5. Keep PSTN audio-only. Do not imply video without internet.

## Next Step 5: External Provider Adapters

Add adapters behind the existing model, not new product semantics.

### Zoom

Preferred:

- RTMS for realtime media ingestion.

Fallback:

- remote-browser bridge opens the Zoom link as a normal human client.

### Google Meet

Preferred:

- Meet Media API where Workspace/admin policy allows.

Fallback:

- remote-browser bridge.

### Microsoft Teams

Preferred:

- Microsoft Graph Cloud Communications / application-hosted media bot where tenant permissions allow.

Fallback:

- remote-browser bridge.

## Remote-Browser Bridge Plan

This should reuse existing Yaver remote-runtime/browser/screen primitives where possible.

Candidate existing files to inspect first:

- `desktop/agent/remote_runtime.go`
- `desktop/agent/remote_runtime_webrtc.go`
- `desktop/agent/browser_interactive_http.go`
- `desktop/agent/ops_remote_session.go`
- `mobile/app/browser-interactive.tsx`
- `mobile/app/remote-runtime.tsx`

Do not build a parallel remote desktop stack.

## Important Product Boundary

Avoid promising “join any Zoom/Meet/Teams invisibly.”

Correct language:

- Yaver-native calls: first-party Yaver meeting rooms.
- External meetings: adapter-backed bridge, subject to provider permissions.
- Remote-browser fallback: Yaver opens the meeting on a user-controlled machine/browser and streams/controls that session.
- Dial-up/no-internet: audio-only bridge.

## Suggested Immediate TODO

1. Fix/coordinate the unrelated `wifi_mesh.go` duplicate `nonEmptyLines` build blocker.
2. Run:

```bash
cd desktop/agent && go test .
cd ../../mobile && npx tsc --noEmit --pretty false
```

3. Implement actual browser LiveKit join in `/call/<slug>`.
4. Add one integration smoke:
   - set fake LiveKit env,
   - create room,
   - call `/call/<slug>/join`,
   - verify media payload shape.
5. Add UI route for mobile room detail/join.
