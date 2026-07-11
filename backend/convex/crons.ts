import { cronJobs } from "convex/server";
import { internal } from "./_generated/api";

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
//   - internal.cloudLifecycle.idleSweep        (Hetzner: every ~10–15 min
//       — POST /crons/run {name:"cloudIdleSweep"}; pauses active managed
//       boxes idle past YAVER_CLOUD_IDLE_MINUTES. DEFAULT OFF
//       (YAVER_CLOUD_IDLE_ENABLE) until the box agent reports activity via
//       /machine/activity; pause is HCLOUD_TOKEN/dryRun fail-closed.)
const crons = cronJobs();

// Auto-off (scale-to-zero): every 15 min, snapshot+delete managed boxes idle
// past YAVER_CLOUD_IDLE_MINUTES so the owner NEVER pays for an unused box. This
// one runs natively in Convex (unlike the self-hosted crons above) precisely
// because a "don't bill me for idle" guarantee must not depend on an external
// box staying up. Fail-closed: no-op unless YAVER_CLOUD_IDLE_ENABLE is set AND
// HCLOUD_TOKEN is present (pauseMachine token-gates itself).
crons.interval(
  "cloud idle sweep",
  { minutes: 15 },
  internal.cloudLifecycle.idleSweepCron,
  {},
);

export default crons;
