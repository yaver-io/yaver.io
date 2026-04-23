import { cronJobs } from "convex/server";

// All cron schedules moved to Hetzner (ubuntu-4gb-hel1-1, 37.27.184.85).
// Systemd timers POST to /crons/run with a bearer token (CRON_TRIGGER_SECRET).
// See convex/http.ts for the trigger endpoint and convex/cronSecret.ts for
// the shared auth helper.
//
// The underlying functions remain available for manual invocation:
//   - internal.cleanup.pruneAuthLogs           (Hetzner: daily 03:00 UTC)
//   - internal.cleanup.pruneMobileStreamLogs   (Hetzner: daily 03:05 UTC)
//   - internal.cleanup.pruneDeveloperLogs      (Hetzner: daily 03:10 UTC)
//   - internal.cleanup.pruneDeviceEvents       (Hetzner: daily 03:15 UTC)
const crons = cronJobs();

export default crons;
