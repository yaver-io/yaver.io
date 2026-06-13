"use client";

// Standalone dashboard route for the screw cell, at /dashboard/screw-cell. Its
// own route (not a tab in the big dashboard page) so it wires cleanly without
// touching the shared page — same agent verbs the firmware pushes to and the
// coding agent reads via MCP (screw_cell_analytics), over the relay.
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import ScrewCellView from "@/components/dashboard/ScrewCellView";

export default function ScrewCellDashboardPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <div className="mx-auto max-w-3xl p-4 sm:p-6">
      <ScrewCellView devices={devices} token={token} />
    </div>
  );
}
