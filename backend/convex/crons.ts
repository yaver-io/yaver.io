import { cronJobs } from "convex/server";
import { internal } from "./_generated/api";

const crons = cronJobs();

// Clean up log tables daily at 03:00 UTC — delete entries older than 7 days
crons.daily("cleanup:authLogs", { hourUTC: 3, minuteUTC: 0 }, internal.cleanup.pruneAuthLogs);
crons.daily("cleanup:mobileStreamLogs", { hourUTC: 3, minuteUTC: 5 }, internal.cleanup.pruneMobileStreamLogs);
crons.daily("cleanup:developerLogs", { hourUTC: 3, minuteUTC: 10 }, internal.cleanup.pruneDeveloperLogs);
crons.daily("cleanup:deviceEvents", { hourUTC: 3, minuteUTC: 15 }, internal.cleanup.pruneDeviceEvents);

export default crons;
