"use client";

// Standalone dashboard route for the robot cell, at /dashboard/robot. Kept as
// its own route (not a tab in the big dashboard page) so it wires cleanly
// without touching the shared dashboard page that other work is editing. Drive
// any robot device over the relay — same agent verbs as the mobile app.
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import RobotCellView from "@/components/dashboard/RobotCellView";

export default function RobotDashboardPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <div className="mx-auto max-w-3xl p-4 sm:p-6">
      <RobotCellView devices={devices} token={token} />
    </div>
  );
}
