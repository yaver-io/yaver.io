import { cronJobs } from "convex/server";

// All cron schedules moved to a self-hosted box (the yaver-test-ephemeral box).
// Systemd timers POST to /crons/run with a bearer token (CRON_TRIGGER_SECRET).
// See convex/http.ts for the trigger endpoint and convex/cronSecret.ts for
// the shared auth helper.
//
// The underlying functions remain available for manual invocation:
//   - internal.cleanup.pruneAuthLogs           (Hetzner: daily 03:00 UTC)
//   - internal.cleanup.pruneMobileStreamLogs   (Hetzner: daily 03:05 UTC)
//   - internal.cleanup.pruneDeveloperLogs      (Hetzner: daily 03:10 UTC)
//   - internal.cleanup.pruneDeviceEvents       (Hetzner: daily 03:15 UTC)
//   - internal.cleanup.pruneExpiredSessions    (Hetzner: daily 03:20 UTC —
//       POST /crons/run {name:"pruneExpiredSessions"}; deletes session
//       rows past the 1-year refresh grace. ADD the systemd timer on the
//       Hetzner cron box alongside the others; until then trigger once
//       manually to clear the historical backlog.)
//   - internal.cloudLifecycle.meterTick        (Hetzner: hourly — POST
//       /crons/run {name:"cloudMeter"}; managed-cloud prepaid meter,
//       dryRun:true until the prepaid product launches)
const crons = cronJobs();

export default crons;
