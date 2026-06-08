"use client";

// Standalone dashboard route for a generic robotic-arm cell, at /dashboard/arm.
// Its own route (not a tab in the big dashboard page) so it wires cleanly
// without touching the shared dashboard page. Drive any arm (Fairino / Elephant
// myCobot / PAROL6 / generic) over the relay — same agent verbs as mobile.
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import ArmCellView from "@/components/dashboard/ArmCellView";

export default function ArmDashboardPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <div className="mx-auto max-w-3xl p-4 sm:p-6">
      <ArmCellView devices={devices} token={token} />
    </div>
  );
}
