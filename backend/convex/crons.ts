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
//   - internal.cloudLifecycle.idleSweep        (Hetzner: every ~10–15 min
//       — POST /crons/run {name:"cloudIdleSweep"}; pauses active managed
//       boxes idle past YAVER_CLOUD_IDLE_MINUTES. DEFAULT OFF
//       (YAVER_CLOUD_IDLE_ENABLE) until the box agent reports activity via
//       /machine/activity; pause is HCLOUD_TOKEN/dryRun fail-closed.)
const crons = cronJobs();

// Auto-off (scale-to-zero) is NOT a Convex cron — a perpetual 15-min sweep is
// recurring Convex compute you'd pay for precisely to save money, which is
// backwards (and fights the Convex cost-optimization work). Instead the box
// DRIVES ITS OWN PARK: the agent's activity monitor (machine_activity.go) runs
// every 90s locally (free), and when a managed/byo box has been idle past the
// grace-confirmed threshold it calls POST /machine/park-self, which schedules
// cloudLifecycle.pauseMachine for exactly that box. Zero cost while active; one
// call at the moment of parking; the box that isn't running pays nothing to
// decide it should stop. idleSweep stays callable via POST /crons/run
// {name:"cloudIdleSweep"} for a manual fleet-wide sweep if ever needed.

export default crons;
