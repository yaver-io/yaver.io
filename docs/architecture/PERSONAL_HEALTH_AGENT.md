# Yaver Personal Health Agent

Status: foundation, 2026-07-06.

This is the health-specific branch of the Personal Agent Gateway. It exists so
users can automate their own health portals, track new results, summarize what
changed, and create reminders without Yaver becoming a medical provider or
storing health records in the hosted control plane.

## Product Thesis

Yaver should let a user say:

```text
Check e-Nabız every morning.
Tell me when new lab results appear.
Summarize what changed since last time.
Remind me to ask the doctor about out-of-range values.
Create a follow-up reminder two weeks from now.
```

The product is not diagnosis. It is user-owned retrieval, organization,
comparison, summarization, and reminders.

## Reference Connector: e-Nabız

The first connector should be `health_enabiz`.

Initial scope is read-only:

- result list
- result detail
- reference ranges when visible
- prescriptions
- appointments
- report metadata
- downloaded report local index
- user-authored reminders/questions

Do not start with writes. Appointment booking/canceling, record sharing, or
delegated access changes are higher-risk ACT flows and need a separate design.

## Execution Model

```text
routine wakes
  -> pick runtime: local box | user self-hosted | opt-in Yaver Cloud
  -> open browser/redroid with named profile
  -> if auth/session works, read requested data
  -> if OAuth/2FA/CAPTCHA/block appears, stop and ask user
  -> extract structured result locally
  -> run deterministic checks
  -> optionally call selected inference mode
  -> notify user and create reminders
```

Supported engines:

- official API, if a portal offers one
- Selenium / WebDriver
- chromedp / Playwright
- redroid for mobile-only flows

Use official APIs first. UI automation is for the user's own account when no
reasonable API exists.

## User Handoff Rule

OAuth, e-Devlet login, 2FA, CAPTCHA, WAF/rate-limit blocks, or suspicious-login
screens are not puzzles to solve.

Yaver behavior must be:

1. Stop automation at the screen.
2. Preserve the visible browser/redroid session.
3. Notify the user on phone/watch/web.
4. Let the user complete login/2FA/CAPTCHA manually.
5. Resume only after the user explicitly says to continue.
6. Record a local audit event.

Never bypass CAPTCHA, never auto-solve 2FA, never spoof headers to defeat a
block, never rotate IPs to get around a denial. A block is a result.

## Scheduling

Health routines should use human-cadence scheduling:

- daily or weekly checks
- one-shot "check again in 3 days"
- jittered wakeups
- pause/resume
- run-now
- visible last-run/next-run status
- no high-frequency polling

Examples:

```json
{
  "connector": "health_enabiz",
  "capability": "results.check_new",
  "schedule": "daily 09:00 Europe/Istanbul",
  "runtime": "local-preferred",
  "onNewResult": ["notify_phone", "summarize_optional", "create_review_reminder"]
}
```

## Data Policy

Health artifacts are sensitive. Default policy:

- portal credentials: local encrypted vault only
- browser profile/session: local runtime or explicitly approved managed runtime
- raw lab results/reports/screenshots: local runtime store only
- detailed extraction/audit: local only
- Convex: coordination metadata only
- notifications: minimal summary unless user opted into richer previews

Convex must not receive raw health values, report text, screenshots, credentials,
absolute paths, or portal session data.

## Inference Modes

Yaver should offer four modes:

1. `none`: deterministic extraction and reminders only.
2. `local`: local model where available.
3. `byok`: user/provider key in vault.
4. `yaver_managed`: paid Yaver inference.

Managed inference should be sold as capability:

- plain-language summary
- changed-since-last-time summary
- report section extraction
- reminder planning
- question list for doctor visit

Do not sell it as "medical diagnosis." Do not let model output present itself as
clinical advice.

## Reminder Model

Reminder objects should be generic enough for non-health use too:

```json
{
  "id": "rem_x",
  "source": "health_enabiz",
  "title": "Ask doctor about vitamin D result",
  "dueAt": "2026-07-13T09:00:00+03:00",
  "surface": ["phone", "watch"],
  "sensitivity": "health",
  "detailsRef": "local://health/enabiz/result/...",
  "status": "scheduled"
}
```

`detailsRef` is local. Hosted services only need enough metadata to wake the
right device/runtime.

## Monetization

Do not require managed cloud or managed inference.

Useful paid lanes:

- local/manual: included
- scheduled local routines: included or low-tier
- Yaver Cloud browser/redroid runner: paid runtime
- Yaver managed inference: paid summary/planning
- Family Care: delegated notifications and caregiver workflows
- BYOK: no inference margin; charge runtime/control plane

This lets privacy-sensitive users stay local and lets less technical users pay
for managed convenience.

## Build Order

1. Connector schema for sensitive read-only portals.
2. Local routine runner using existing schedules.
3. e-Nabız login/session wizard with visible user handoff.
4. Result-list and result-detail extraction.
5. Local encrypted artifact store and audit.
6. Reminder creation and phone/watch notifications.
7. Optional inference modes.
8. Managed cloud runtime only after local path works.

## Hard No

- No diagnosis.
- No medication start/stop/change advice.
- No autonomous writes.
- No 2FA/CAPTCHA bypass.
- No block evasion.
- No health data in Convex.
- No hidden caregiver sharing.
