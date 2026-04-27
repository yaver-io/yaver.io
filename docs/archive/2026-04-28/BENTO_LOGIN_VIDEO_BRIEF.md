# Bento Login Video Brief

Goal: replace the old dummy/acme login-validation story with the Bento app and
ship two short product videos that explain Yaver in about 2 minutes total.

- Video 1: `Feedback SDK` — ~60s
- Video 2: `Yaver Mobile` — ~60s
- Shared story: Bento login accepts a malformed email, surfaces an ugly backend
  error, Yaver fixes the bug, the app hot reloads, login works cleanly.

This is the same narrative as the old footage in:

- `videos/desktop/no-email-validation-needs-to-be-fixed.mp4`
- `videos/desktop/fix-trig-from-yaver-mobile.MP4`
- `videos/desktop/fix-impact-from-yaver.mov`

But the on-camera app should now be `demo/BentoApp`, not the old dummy app.

## Source of truth in code

The Bento login bug already exists in `demo/BentoApp`:

- [demo/BentoApp/src/components/LoginForm.tsx](/Users/kivanccakmak/Workspace/yaver.io/demo/BentoApp/src/components/LoginForm.tsx)
  has `TODO: add input validation — email format, empty fields`
- [demo/BentoApp/src/context/AuthContext.tsx](/Users/kivanccakmak/Workspace/yaver.io/demo/BentoApp/src/context/AuthContext.tsx)
  throws:
  - malformed email -> `invalid_email_format`
  - empty email -> null/toLowerCase style exception
  - empty password -> `password_required`
- [demo/BentoApp/app/_layout.tsx](/Users/kivanccakmak/Workspace/yaver.io/demo/BentoApp/app/_layout.tsx)
  already wires the embedded Yaver SDK (`FeedbackModal`, `FloatingButton`,
  `BlackBox`, shake trigger)

## Product message

What we need the viewer to understand in 2 minutes:

1. `Feedback SDK`: add Yaver inside your app, report a bug from the live app,
   agent fixes it, app reloads.
2. `Yaver Mobile`: from the Yaver app on your phone, trigger vibe coding
   against the Bento app and watch the fix land.

Keep the story simple. Do not explain every system. Show one bug, one fix, one
visible outcome.

## Shared Bento bug story

Use the same exact repro in both videos:

1. Open Bento login screen.
2. Enter malformed email: `kivanccakmak@gmail.`
3. Enter any non-empty password.
4. Tap `Sign In`.
5. App shows ugly raw error:
   `Error: 400 Bad Request — {"error":"invalid_email_format","message":"Malformed email address"}`
6. Trigger Yaver fix flow.
7. Agent finds missing client-side validation in `LoginForm.tsx`.
8. Patch lands.
9. Hot reload.
10. Retry same malformed email.
11. Now Bento shows a clean inline validation message instead of backend error.

Recommended fixed UX on camera:

- invalid email -> `Enter a valid email address`
- empty email -> `Email is required`
- empty password -> `Password is required`
- block submit before calling `onLogin`

## Video 1 — Feedback SDK (~60s)

Story: "Embed Yaver in your app. Report a bug from inside the app. It fixes and
reloads."

### Beat sheet

```text
0:00  Open Bento login screen
0:04  Type malformed email + password
0:08  Tap Sign In
0:10  Ugly raw invalid_email_format error appears
0:14  Shake / floating button opens Yaver feedback UI
0:18  Short prompt: "Fix login email validation"
0:22  Agent starts, reads LoginForm.tsx
0:32  Agent explains root cause: no client-side validation before onLogin
0:40  Patch applied
0:44  Bento hot reloads
0:48  Retry malformed email
0:52  Clean inline validation message shows
0:56  End card / caption: "Feedback SDK: bug report to fix in one loop"
```

### What to show

- Primary footage: Bento app only
- Small amount of agent output is fine, but the app should stay primary
- Prefer embedded SDK UI over split-screen

### Voiceover / caption draft

```text
This is Bento with Yaver Feedback SDK embedded.
The login screen sends a malformed email to the backend and shows a raw error.
From inside the app, I report the bug.
Yaver reads the code, adds client-side validation, and hot reloads.
Now the same login attempt is handled cleanly in the UI.
```

## Video 2 — Yaver Mobile (~60s)

Story: "Use Yaver from your phone to vibe code another app."

### Beat sheet

```text
0:00  Start in Bento login screen, reproduce malformed email bug
0:08  Switch to Yaver mobile app
0:12  Open Bento project / task view
0:16  Prompt: "Fix the login email validation"
0:20  Agent run starts on connected machine
0:28  Show key finding:
      LoginForm.tsx submits without checking email/password first
0:38  Patch completes
0:42  Return to Bento
0:46  Bento hot reload already applied
0:50  Retry malformed email
0:54  Clean validation message appears
0:58  End card / caption: "Yaver Mobile: fix your app from your phone"
```

### What to show

- Start and end on Bento so the bug/fix is obvious
- Middle section is Yaver mobile task UI
- Keep terminal/logs secondary; this cut should feel mobile-first

### Voiceover / caption draft

```text
This is the same Bento bug, but fixed from Yaver Mobile.
I reproduce the issue in Bento, jump into Yaver, and send one task.
The agent updates the login form on the connected machine.
When I return to Bento, the app has already reloaded with the fix.
```

## Recording notes

- Reuse the visual language of the old login-validation footage, but with Bento
  branding and file paths.
- Keep each cut under 60 seconds.
- Do not spend time on auth setup, pairing, or infrastructure details on camera.
- The visible before/after matters more than the coding process.
- If logs are shown, show only the key finding and the patched file.

## Exact file/path details worth surfacing on camera

- App: `demo/BentoApp`
- Buggy file: `src/components/LoginForm.tsx`
- Supporting auth logic: `src/context/AuthContext.tsx`
- Suggested task title:
  - `Fix Bento login email validation`

## Shoot order

1. Record Bento bug repro once.
2. Record `Feedback SDK` fix loop.
3. Reset app to buggy state if needed.
4. Record `Yaver Mobile` fix loop.
5. Cut both to ~60s each.

## Done criteria

- Both videos use Bento, not the old dummy/acme app.
- Both videos use the same malformed-email login bug.
- One video clearly sells `Feedback SDK`.
- One video clearly sells `Yaver Mobile`.
- A new viewer can understand the product from the two videos alone.
