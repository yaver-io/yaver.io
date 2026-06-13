"use client";

// Standalone dashboard route for the electrical-circuit cell, at
// /dashboard/circuit. Its own route (not a tab in the big dashboard page) so it
// wires cleanly without touching the shared page — same agent verbs the mobile
// circuit cell calls, over the relay.
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";
import CircuitCellView from "@/components/dashboard/CircuitCellView";

export default function CircuitDashboardPage() {
  const { token } = useAuth();
  const { devices } = useDevices(token);
  return (
    <div className="mx-auto max-w-3xl p-4 sm:p-6">
      <CircuitCellView devices={devices} token={token} />
    </div>
  );
}
